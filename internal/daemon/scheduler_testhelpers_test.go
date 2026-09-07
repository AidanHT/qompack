package daemon

// This file holds the in-memory doubles every SP-12 daemon-side test drives the scheduler
// runtime through: fakeStore, fakeGraph, fakeSegmentLog, fakeWriter, fakeLedger (and its
// Maintainer-bearing sibling fakeMaintLedger) and recordingLogger. Later SP-12 seats (the
// Runtime, the tap, the idle work) reuse them, so every recorder and hook is documented on the
// type. All of them are safe for concurrent use: the daemon's worker pool and idle controller
// reach the real seams from more than one goroutine, and the doubles must survive `-race`.
//
// internal/testutil is deliberately NOT imported (daemon → testutil → cli → daemon would be a
// cycle in the test build graph); time comes from the in-package newFakeClock/epoch of
// fakeclock_test.go, exactly as the other daemon tests do.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
)

// Compile-time seam checks: a double that stops matching its interface fails here, not at the
// first test that happens to call the missing method.
var (
	_ store.Store        = (*fakeStore)(nil)
	_ store.SegmentLog   = (*fakeSegmentLog)(nil)
	_ dag.Graph          = (*fakeGraph)(nil)
	_ checkpoint.Writer  = (*fakeWriter)(nil)
	_ negknow.Ledger     = (*fakeLedger)(nil)
	_ negknow.Ledger     = (*fakeMaintLedger)(nil)
	_ negknow.Maintainer = (*fakeMaintLedger)(nil)
	_ logging.Logger     = (*recordingLogger)(nil)
)

// ── fakeStore ────────────────────────────────────────────────────────────────────────────────

// fakeStore is an in-memory store.Store carrying only the tool_use index and a fakeSegmentLog.
// It embeds a nil store.Store so the object, file-history and search methods — which nothing in
// SP-12 calls — exist without being written out; calling one of them panics with a nil-pointer
// dereference, which is the loud failure wanted if a scheduler path ever reaches that far.
//
// Overridden: ToolUse, RecordToolUse, Segments, GC, Flush, Close.
//
// Recorders: toolUseCalls (every id looked up, hits and misses), gcCalls (every policy passed to
// GC), flushCalls, closeCalls. Hooks: gcReport/gcErr script GC's answer; toolUseErr, when set,
// is returned by every ToolUse call.
type fakeStore struct {
	store.Store

	mu      sync.Mutex
	records map[core.ToolUseID]store.ToolUseRecord
	segs    *fakeSegmentLog

	gcReport   store.GCReport
	gcErr      error
	toolUseErr error

	toolUseCalls []core.ToolUseID
	gcCalls      []store.GCPolicy
	flushCalls   int
	closeCalls   int
}

// newFakeStore returns an empty fakeStore owning a fresh fakeSegmentLog.
func newFakeStore() *fakeStore {
	return &fakeStore{
		records: make(map[core.ToolUseID]store.ToolUseRecord),
		segs:    newFakeSegmentLog(),
	}
}

// put stores rec without going through RecordToolUse's recorder, for fixture set-up.
func (s *fakeStore) put(rec store.ToolUseRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[rec.ID] = rec
}

func (s *fakeStore) RecordToolUse(ctx context.Context, rec store.ToolUseRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.put(rec)
	return nil
}

func (s *fakeStore) ToolUse(ctx context.Context, id core.ToolUseID) (store.ToolUseRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolUseCalls = append(s.toolUseCalls, id)
	if s.toolUseErr != nil {
		return store.ToolUseRecord{}, s.toolUseErr
	}
	rec, ok := s.records[id]
	if !ok {
		return store.ToolUseRecord{}, fmt.Errorf("%w: tool_use %s", core.ErrNotFound, id)
	}
	return rec, nil
}

func (s *fakeStore) Segments() store.SegmentLog { return s.segs }

func (s *fakeStore) GC(ctx context.Context, p store.GCPolicy) (store.GCReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcCalls = append(s.gcCalls, p)
	if s.gcErr != nil {
		return store.GCReport{}, s.gcErr
	}
	return s.gcReport, nil
}

func (s *fakeStore) Flush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushCalls++
	return nil
}

func (s *fakeStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCalls++
	return nil
}

// ── fakeSegmentLog ───────────────────────────────────────────────────────────────────────────

// segCloseCall is one recorded SegmentLog.Close call.
type segCloseCall struct {
	ID      core.SegmentID
	EndTurn core.TurnIndex
	Feats   map[string]float64
}

// segMarkCall is one recorded SegmentLog.MarkEncoded call.
type segMarkCall struct {
	IDs []core.SegmentID
	Seq core.CheckpointSeq
}

// segTokensFeature mirrors the real segment log's "tokens" feature key: Close lifts it off the
// feature map into Segment.Tokens, exactly as store/segments.go does, so a test that closes a
// segment the way the observer does sees the same Segment the checkpointer would.
const segTokensFeature = "tokens"

// fakeSegmentLog is an in-memory store.SegmentLog implementing all eight methods with the real
// log's semantics: IDs are assigned from 1 upward by Open; Range matches closed segments on
// [StartTurn, EndTurn] and open ones on StartTurn <= to; Current returns the highest-ID open
// segment of a session (core.ErrNotFound when none); MarkEncoded is the §4.6 DPI guard (refuses
// an unknown id, an open segment, and a re-encode under a different seq with
// core.ErrAlreadyEncoded; idempotent under the same seq); Frontier is the EndTurn of the last
// segment in an unbroken encoded prefix of the session; Unencoded lists the rest.
//
// Recorders: openCalls, closeCalls, markCalls, rangeCalls (each [from, to] pair). Hooks: the
// *Err fields, when set, are returned by the matching method before anything is touched.
type fakeSegmentLog struct {
	mu     sync.Mutex
	nextID core.SegmentID
	segs   map[core.SegmentID]*store.Segment
	order  []core.SegmentID // insertion order == ascending ID, like the real log

	openErr, closeErr, getErr, rangeErr, currentErr, markErr, frontierErr, unencodedErr error

	openCalls  []store.Segment
	closeCalls []segCloseCall
	markCalls  []segMarkCall
	rangeCalls [][2]core.TurnIndex
}

func newFakeSegmentLog() *fakeSegmentLog {
	return &fakeSegmentLog{nextID: 1, segs: make(map[core.SegmentID]*store.Segment)}
}

// insertLocked stores seg under the next ID, under l.mu, and returns that ID.
func (l *fakeSegmentLog) insertLocked(seg store.Segment) core.SegmentID {
	seg.ID = l.nextID
	l.nextID++
	l.segs[seg.ID] = &seg
	l.order = append(l.order, seg.ID)
	return seg.ID
}

// addSegment opens and closes one segment of sess spanning [start, end] with the given token
// count, bypassing the recorders, and returns its ID. It is the fixture shortcut.
func (l *fakeSegmentLog) addSegment(t testing.TB, sess core.SessionID, start, end core.TurnIndex, tokens core.Tokens) core.SegmentID {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.insertLocked(store.Segment{
		Session: sess, StartTurn: start, EndTurn: end, Tokens: tokens, Closed: true,
		Features: map[string]float64{},
	})
}

// orderedIDs returns every segment ID ascending, under l.mu.
func (l *fakeSegmentLog) orderedIDs() []core.SegmentID { return l.order }

func (l *fakeSegmentLog) Open(ctx context.Context, s store.Segment) (core.SegmentID, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.openCalls = append(l.openCalls, s)
	if l.openErr != nil {
		return 0, l.openErr
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.Closed = false
	return l.insertLocked(s), nil
}

func (l *fakeSegmentLog) Close(ctx context.Context, id core.SegmentID, endTurn core.TurnIndex, feats map[string]float64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closeCalls = append(l.closeCalls, segCloseCall{ID: id, EndTurn: endTurn, Feats: feats})
	if l.closeErr != nil {
		return l.closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	seg, ok := l.segs[id]
	if !ok {
		return fmt.Errorf("%w: segment %d", core.ErrNotFound, id)
	}
	clean := make(map[string]float64, len(feats))
	for k, v := range feats {
		if k == segTokensFeature {
			seg.Tokens = core.Tokens(math.Round(v))
			continue
		}
		clean[k] = v
	}
	seg.Features = clean
	seg.EndTurn = endTurn
	seg.Closed = true
	return nil
}

func (l *fakeSegmentLog) Get(ctx context.Context, id core.SegmentID) (store.Segment, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.getErr != nil {
		return store.Segment{}, l.getErr
	}
	seg, ok := l.segs[id]
	if !ok {
		return store.Segment{}, fmt.Errorf("%w: segment %d", core.ErrNotFound, id)
	}
	return *seg, nil
}

func (l *fakeSegmentLog) Range(ctx context.Context, from, to core.TurnIndex) ([]store.Segment, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rangeCalls = append(l.rangeCalls, [2]core.TurnIndex{from, to})
	if l.rangeErr != nil {
		return nil, l.rangeErr
	}
	var out []store.Segment
	for _, id := range l.orderedIDs() {
		seg := l.segs[id]
		var hit bool
		if seg.Closed {
			hit = seg.StartTurn <= to && seg.EndTurn >= from
		} else {
			hit = to >= seg.StartTurn
		}
		if hit {
			out = append(out, *seg)
		}
	}
	return out, nil
}

func (l *fakeSegmentLog) Current(ctx context.Context, s core.SessionID) (store.Segment, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.currentErr != nil {
		return store.Segment{}, l.currentErr
	}
	var found *store.Segment
	for _, id := range l.orderedIDs() {
		seg := l.segs[id]
		if seg.Session == s && !seg.Closed {
			found = seg
		}
	}
	if found == nil {
		return store.Segment{}, fmt.Errorf("%w: no open segment for session %s", core.ErrNotFound, s)
	}
	return *found, nil
}

func (l *fakeSegmentLog) MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.markCalls = append(l.markCalls, segMarkCall{IDs: append([]core.SegmentID(nil), ids...), Seq: seq})
	if l.markErr != nil {
		return l.markErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	pending := make([]*store.Segment, 0, len(ids))
	for _, id := range ids {
		seg, ok := l.segs[id]
		if !ok {
			return fmt.Errorf("%w: segment %d", core.ErrNotFound, id)
		}
		if !seg.Closed {
			return fmt.Errorf("%w: segment %d", store.ErrSegmentOpen, id)
		}
		if seg.EncodedOnce {
			if seg.CheckpointSeq == seq {
				continue
			}
			return fmt.Errorf("%w: segment %d already encoded into checkpoint %d, refused for %d",
				core.ErrAlreadyEncoded, id, seg.CheckpointSeq, seq)
		}
		pending = append(pending, seg)
	}
	for _, seg := range pending {
		seg.EncodedOnce, seg.CheckpointSeq = true, seq
	}
	return nil
}

func (l *fakeSegmentLog) Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.frontierErr != nil {
		return 0, l.frontierErr
	}
	var frontier core.TurnIndex
	for _, id := range l.orderedIDs() {
		seg := l.segs[id]
		if seg.Session != s {
			continue
		}
		if !seg.EncodedOnce {
			break
		}
		frontier = seg.EndTurn
	}
	return frontier, nil
}

func (l *fakeSegmentLog) Unencoded(ctx context.Context, s core.SessionID) ([]store.Segment, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.unencodedErr != nil {
		return nil, l.unencodedErr
	}
	var out []store.Segment
	for _, id := range l.orderedIDs() {
		seg := l.segs[id]
		if seg.Session == s && !seg.EncodedOnce {
			out = append(out, *seg)
		}
	}
	return out, nil
}

// ── fakeGraph ────────────────────────────────────────────────────────────────────────────────

// fakeGraph is an in-memory dag.Graph implementing all thirteen methods. Nodes live in a map
// keyed by ID (AddNode upserts, like the real graph); NodesAfter answers in ascending Pos order
// with ID as the tiebreak; CrossingEdges counts, from the endpoints' Pos, the stored edges with
// lo < pos <= hi (the real graph's definition) unless crossingFn overrides it.
//
// Recorders: crossingCalls (every pos), nodesAfterCalls (every pos), backwardSliceCalls (every
// criteria slice), compactCalls, statsCalls. Hooks: crossingFn replaces the coupling
// computation; panicAt makes CrossingEdges(pos) panic with the stored message for that exact
// Pos (see panicOn); sliceResult/sliceErr script both slice methods; compactErr scripts Compact.
type fakeGraph struct {
	mu    sync.Mutex
	nodes map[dag.NodeID]dag.Node
	edges []dag.Edge

	crossingFn  func(pos int) int
	panicAt     map[int]string
	sliceResult dag.Slice
	sliceErr    error
	compactErr  error

	crossingCalls      []int
	nodesAfterCalls    []int
	backwardSliceCalls [][]dag.NodeID
	compactCalls       int
	statsCalls         int
}

func newFakeGraph() *fakeGraph {
	return &fakeGraph{nodes: make(map[dag.NodeID]dag.Node), panicAt: make(map[int]string)}
}

// panicOn arms the per-Pos panic hook: the next CrossingEdges(pos) panics with msg.
func (g *fakeGraph) panicOn(pos int, msg string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.panicAt[pos] = msg
}

// addTurnNodes adds one KindToolUse node per turn in [1, turns], at Pos turn*posPerTurn. It is
// the fixture shortcut for "a session whose turn t begins at position t·k".
func (g *fakeGraph) addTurnNodes(t testing.TB, turns int, posPerTurn int) {
	t.Helper()
	for turn := 1; turn <= turns; turn++ {
		require.NoError(t, g.AddNode(dag.Node{
			ID:   dag.NodeID(fmt.Sprintf("tooluse:turn-%d", turn)),
			Kind: dag.KindToolUse,
			Turn: core.TurnIndex(turn),
			Pos:  turn * posPerTurn,
		}))
	}
}

func (g *fakeGraph) AddNode(n dag.Node) error {
	if n.ID == "" {
		return dag.ErrInvalidNode
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes[n.ID] = n
	return nil
}

func (g *fakeGraph) AddEdge(e dag.Edge) error {
	if e.From == "" || e.To == "" {
		return dag.ErrInvalidEdge
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.edges = append(g.edges, e)
	return nil
}

func (g *fakeGraph) Node(id dag.NodeID) (dag.Node, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[id]
	return n, ok
}

func (g *fakeGraph) Out(id dag.NodeID) []dag.Edge {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []dag.Edge
	for _, e := range g.edges {
		if e.From == id {
			out = append(out, e)
		}
	}
	return out
}

func (g *fakeGraph) In(id dag.NodeID) []dag.Edge {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []dag.Edge
	for _, e := range g.edges {
		if e.To == id {
			out = append(out, e)
		}
	}
	return out
}

func (g *fakeGraph) BackwardSlice(criteria []dag.NodeID, o dag.SliceOptions) (dag.Slice, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.backwardSliceCalls = append(g.backwardSliceCalls, append([]dag.NodeID(nil), criteria...))
	return g.sliceResult, g.sliceErr
}

func (g *fakeGraph) ForwardSlice(criteria []dag.NodeID, o dag.SliceOptions) (dag.Slice, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sliceResult, g.sliceErr
}

func (g *fakeGraph) CrossingEdges(pos int) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.crossingCalls = append(g.crossingCalls, pos)
	if msg, ok := g.panicAt[pos]; ok {
		panic(msg)
	}
	if g.crossingFn != nil {
		return g.crossingFn(pos)
	}
	if pos <= 0 {
		return 0
	}
	n := 0
	for _, e := range g.edges {
		from, okF := g.nodes[e.From]
		to, okT := g.nodes[e.To]
		if !okF || !okT {
			continue
		}
		lo, hi := min(from.Pos, to.Pos), max(from.Pos, to.Pos)
		if lo < pos && pos <= hi {
			n++
		}
	}
	return n
}

func (g *fakeGraph) NodesAfter(pos int) []dag.Node {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodesAfterCalls = append(g.nodesAfterCalls, pos)
	out := make([]dag.Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		if n.Pos >= pos {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pos != out[j].Pos {
			return out[i].Pos < out[j].Pos
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (g *fakeGraph) Flush(ctx context.Context) error { return ctx.Err() }

func (g *fakeGraph) Compact(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.compactCalls++
	if g.compactErr != nil {
		return g.compactErr
	}
	return ctx.Err()
}

func (g *fakeGraph) Stats() dag.GraphStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.statsCalls++
	st := dag.GraphStats{
		Nodes:       len(g.nodes),
		Edges:       len(g.edges),
		NodesByKind: make(map[string]int),
		EdgesByKind: make(map[string]int),
	}
	for _, n := range g.nodes {
		st.MaxPos = max(st.MaxPos, n.Pos)
	}
	return st
}

// ── fakeWriter ───────────────────────────────────────────────────────────────────────────────

// writerBeginCall is one recorded checkpoint.Writer.Begin call.
type writerBeginCall struct {
	Session core.SessionID
	Parent  core.CheckpointSeq
}

// fakeWriter is an in-memory checkpoint.Writer. Begin hands out a non-nil *checkpoint.Draft
// (distinct per call) tagged with a fresh sequence number; Advance refuses any id armed in
// alreadyEncoded with core.ErrAlreadyEncoded, otherwise marks the ids through segs when one is
// wired (so the fakeSegmentLog's own DPI guard also applies) and returns the highest EndTurn
// among them, or the scripted frontier when no log is wired; Finalize returns the scripted ref;
// Abort only counts.
//
// Recorders: beginCalls, advanceCalls (the ids of every Advance, including refused ones),
// finalizeCalls (the budgets), abortCalls. Hooks: alreadyEncoded, beginErr, advanceErr,
// finalizeErr, ref, frontier.
type fakeWriter struct {
	mu   sync.Mutex
	segs *fakeSegmentLog

	nextSeq        core.CheckpointSeq
	drafts         map[*checkpoint.Draft]core.CheckpointSeq
	alreadyEncoded map[core.SegmentID]bool
	frontier       core.TurnIndex
	ref            checkpoint.Ref

	beginErr, advanceErr, finalizeErr error

	beginCalls    []writerBeginCall
	advanceCalls  [][]core.SegmentID
	finalizeCalls []core.Tokens
	abortCalls    int
}

// newFakeWriter returns a fakeWriter; segs may be nil, in which case Advance answers with the
// scripted frontier instead of marking anything encoded.
func newFakeWriter(segs *fakeSegmentLog) *fakeWriter {
	return &fakeWriter{
		segs:           segs,
		nextSeq:        1,
		drafts:         make(map[*checkpoint.Draft]core.CheckpointSeq),
		alreadyEncoded: make(map[core.SegmentID]bool),
	}
}

func (w *fakeWriter) Begin(ctx context.Context, s core.SessionID, parent core.CheckpointSeq, src checkpoint.SourceSet) (*checkpoint.Draft, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.beginCalls = append(w.beginCalls, writerBeginCall{Session: s, Parent: parent})
	if w.beginErr != nil {
		return nil, w.beginErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d := &checkpoint.Draft{}
	w.drafts[d] = w.nextSeq
	w.nextSeq++
	return d, nil
}

func (w *fakeWriter) Advance(ctx context.Context, d *checkpoint.Draft, segs []core.SegmentID) (core.TurnIndex, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.advanceCalls = append(w.advanceCalls, append([]core.SegmentID(nil), segs...))
	if w.advanceErr != nil {
		return 0, w.advanceErr
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	seq, ok := w.drafts[d]
	if d == nil || !ok {
		return 0, errors.New("fakeWriter: Advance on a draft Begin did not return")
	}
	for _, id := range segs {
		if w.alreadyEncoded[id] {
			return 0, fmt.Errorf("%w: segment %d", core.ErrAlreadyEncoded, id)
		}
	}
	if w.segs == nil {
		return w.frontier, nil
	}
	if err := w.segs.MarkEncoded(ctx, segs, seq); err != nil {
		return 0, err
	}
	var frontier core.TurnIndex
	for _, id := range segs {
		seg, err := w.segs.Get(ctx, id)
		if err != nil {
			return 0, err
		}
		frontier = max(frontier, seg.EndTurn)
	}
	return frontier, nil
}

func (w *fakeWriter) Finalize(ctx context.Context, d *checkpoint.Draft, budget core.Tokens) (checkpoint.Ref, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.finalizeCalls = append(w.finalizeCalls, budget)
	if w.finalizeErr != nil {
		return checkpoint.Ref{}, w.finalizeErr
	}
	if err := ctx.Err(); err != nil {
		return checkpoint.Ref{}, err
	}
	seq, ok := w.drafts[d]
	if d == nil || !ok {
		return checkpoint.Ref{}, errors.New("fakeWriter: Finalize on a draft Begin did not return")
	}
	ref := w.ref
	if ref.Seq == 0 {
		ref.Seq = seq
	}
	delete(w.drafts, d)
	return ref, nil
}

func (w *fakeWriter) Abort(d *checkpoint.Draft) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.abortCalls++
	delete(w.drafts, d)
	return nil
}

// ── fakeLedger ───────────────────────────────────────────────────────────────────────────────

// fakeLedger is a negknow.Ledger double for the three methods SP-12's idle work reaches —
// RefreshStaleness, RebuildBloom, Health — plus Close. It embeds a nil negknow.Ledger so the
// record/query methods exist unwritten; calling one panics, which is the wanted loud failure.
//
// Recorders: refreshCalls, rebuildCalls, closeCalls. Hooks: refreshResult/refreshErr script
// RefreshStaleness; bloom/rebuildErr script RebuildBloom; health is what Health and RebuildBloom
// report.
type fakeLedger struct {
	negknow.Ledger

	mu            sync.Mutex
	health        negknow.Health
	refreshResult []string
	refreshErr    error
	bloom         *sketch.Bloom
	rebuildErr    error

	refreshCalls int
	rebuildCalls int
	closeCalls   int
}

func newFakeLedger() *fakeLedger { return &fakeLedger{} }

func (l *fakeLedger) RefreshStaleness(ctx context.Context, s store.Store) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refreshCalls++
	if l.refreshErr != nil {
		return nil, l.refreshErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]string(nil), l.refreshResult...), nil
}

func (l *fakeLedger) RebuildBloom(ctx context.Context) (*sketch.Bloom, negknow.Health, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rebuildCalls++
	if l.rebuildErr != nil {
		return nil, negknow.Health{}, l.rebuildErr
	}
	if err := ctx.Err(); err != nil {
		return nil, negknow.Health{}, err
	}
	return l.bloom, l.health, nil
}

func (l *fakeLedger) Health() negknow.Health {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.health
}

func (l *fakeLedger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closeCalls++
	return nil
}

// fakeMaintLedger is a fakeLedger that also satisfies negknow.Maintainer, for tests of the idle
// registration path that type-asserts `led.(negknow.Maintainer)`. Only NeedsRebuild and
// MaintenanceTask are written out; the ranking/ingest methods come from the embedded nil
// Maintainer and panic if called. Ledger and Maintainer share no method names, so the two
// embeddings never collide.
//
// MaintenanceTask returns (taskName, taskPrio, fn) where fn counts into taskRuns and returns
// taskErr; maintenanceTaskCalls records how many times the triple was requested.
type fakeMaintLedger struct {
	*fakeLedger
	negknow.Maintainer

	needsRebuild bool
	taskName     string
	taskPrio     int
	taskErr      error

	maintenanceTaskCalls int
	taskRuns             int
}

func newFakeMaintLedger(taskName string, taskPrio int) *fakeMaintLedger {
	return &fakeMaintLedger{fakeLedger: newFakeLedger(), taskName: taskName, taskPrio: taskPrio}
}

func (l *fakeMaintLedger) NeedsRebuild() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.needsRebuild
}

func (l *fakeMaintLedger) MaintenanceTask(s store.Store) (string, int, func(ctx context.Context) error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maintenanceTaskCalls++
	return l.taskName, l.taskPrio, func(ctx context.Context) error {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.taskRuns++
		return l.taskErr
	}
}

// ── recordingLogger ──────────────────────────────────────────────────────────────────────────

// The level keys recordingLogger files entries under.
const (
	logDebug = "debug"
	logInfo  = "info"
	logWarn  = "warn"
	logError = "error"
	logLoud  = "loud"
)

// logEntry is one captured log call.
type logEntry struct {
	Msg string
	KV  []any
}

// recordingLogger captures all six logging.Logger methods by level. It is distinct from
// registry_test.go's captureLogger, which records Loud only. With returns the same recorder, so
// entries logged through a derived logger land in the parent's capture; the derived kv is
// prepended to every entry's KV so a test can still see it.
//
// Read back with entries(level), msgs(level) and count(level).
type recordingLogger struct {
	mu   sync.Mutex
	kv   []any
	logs map[string][]logEntry
}

func newRecordingLogger() *recordingLogger {
	return &recordingLogger{logs: make(map[string][]logEntry)}
}

func (l *recordingLogger) record(level, msg string, kv []any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	all := make([]any, 0, len(l.kv)+len(kv))
	all = append(all, l.kv...)
	all = append(all, kv...)
	l.logs[level] = append(l.logs[level], logEntry{Msg: msg, KV: all})
}

func (l *recordingLogger) With(kv ...any) logging.Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.kv = append(l.kv, kv...)
	return l
}

func (l *recordingLogger) Debug(msg string, kv ...any) { l.record(logDebug, msg, kv) }
func (l *recordingLogger) Info(msg string, kv ...any)  { l.record(logInfo, msg, kv) }
func (l *recordingLogger) Warn(msg string, kv ...any)  { l.record(logWarn, msg, kv) }
func (l *recordingLogger) Error(msg string, kv ...any) { l.record(logError, msg, kv) }
func (l *recordingLogger) Loud(msg string, kv ...any)  { l.record(logLoud, msg, kv) }

// entries returns a copy of everything logged at level.
func (l *recordingLogger) entries(level string) []logEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]logEntry(nil), l.logs[level]...)
}

// msgs returns the messages logged at level, in order.
func (l *recordingLogger) msgs(level string) []string {
	es := l.entries(level)
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Msg
	}
	return out
}

// count returns how many entries were logged at level.
func (l *recordingLogger) count(level string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.logs[level])
}
