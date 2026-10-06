package rehydrate

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/rules"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/skills"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
	"github.com/stretchr/testify/require"
)

// Compile-time assertions that every fake still satisfies the seam it stands in for. They are the
// point of writing the unused halves out in full: a fake checked only against the subset this
// package calls quietly stops matching the interface as the owning subplan evolves it.
var (
	_ store.Store      = (*fakeStore)(nil)
	_ negknow.Ledger   = (*fakeLedger)(nil)
	_ dag.Graph        = (*fakeGraph)(nil)
	_ logging.Logger   = (*spyLogger)(nil)
	_ rules.Scanner    = (*fakeScanner)(nil)
	_ skills.Indexer   = (*fakeIndexer)(nil)
	_ tokens.Estimator = (*fakeEstimator)(nil)
)

// generousTestBudget is a budget large enough that nothing is truncated, so a builder test that
// happens to run through Build is asserting on the builder rather than on the budget pass.
const generousTestBudget = core.Tokens(9000)

// ── fakeStore ──

// fakeStore is a scripted, counting store.Store.
//
// searchCalls is the load-bearing field: item 2 must resolve the verbatim original prompt by
// deriving its tool_use id, never by searching. Search ranks newest-first and clamps K, so it
// excludes exactly the earliest prompt of a long session — a relevance search would inject a
// MID-session prompt as "the verbatim original user intent". The counter is how the test proves
// that path is never taken.
type fakeStore struct {
	// records is the scripted tool_use index, keyed by id.
	records map[core.ToolUseID]store.ToolUseRecord
	// content is the scripted object store, keyed by root hash.
	content map[core.Hash][]byte
	// toolUseErr, when set for an id, is returned instead of a record.
	toolUseErr map[core.ToolUseID]error
	// openErr, when set for a root, is returned instead of a reader.
	openErr map[core.Hash]error

	// toolUseCalls records every ToolUse argument in call order. Its contents ARE the "exactly
	// once, with this id" assertion.
	toolUseCalls []core.ToolUseID
	// openCalls records every Open argument in call order.
	openCalls []core.Hash
	// searchCalls counts Search calls. It must stay 0.
	searchCalls int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		records:    map[core.ToolUseID]store.ToolUseRecord{},
		content:    map[core.Hash][]byte{},
		toolUseErr: map[core.ToolUseID]error{},
		openErr:    map[core.Hash]error{},
	}
}

// withPrompt scripts one L0 prompt capture: a tool_use record under id whose root resolves to
// body. session and turn are what buildUserIntent sanity-checks before trusting the record.
func (f *fakeStore) withPrompt(id core.ToolUseID, sess core.SessionID, turn core.TurnIndex, body string) *fakeStore {
	root := core.HashBytes("rehydrate.faketest", []byte(string(id)))
	f.records[id] = store.ToolUseRecord{
		ID: id, Session: sess, Turn: turn, Tool: "UserPromptSubmit", Root: root,
	}
	f.content[root] = []byte(body)
	return f
}

// withRootlessPrompt scripts a record whose Root is the zero hash — an indexed capture whose
// content never made it to the object store.
func (f *fakeStore) withRootlessPrompt(id core.ToolUseID, sess core.SessionID, turn core.TurnIndex) *fakeStore {
	f.records[id] = store.ToolUseRecord{ID: id, Session: sess, Turn: turn, Tool: "UserPromptSubmit"}
	return f
}

// withToolUseErr scripts an error for one id.
func (f *fakeStore) withToolUseErr(id core.ToolUseID, err error) *fakeStore {
	f.toolUseErr[id] = err
	return f
}

// withOpenErr scripts an Open failure for the root id resolves to.
func (f *fakeStore) withOpenErr(id core.ToolUseID, err error) *fakeStore {
	f.openErr[f.records[id].Root] = err
	return f
}

func (f *fakeStore) ToolUse(ctx context.Context, id core.ToolUseID) (store.ToolUseRecord, error) {
	f.toolUseCalls = append(f.toolUseCalls, id)
	if err, ok := f.toolUseErr[id]; ok {
		return store.ToolUseRecord{}, err
	}
	rec, ok := f.records[id]
	if !ok {
		return store.ToolUseRecord{}, core.ErrNotFound
	}
	return rec, nil
}

func (f *fakeStore) Open(ctx context.Context, root core.Hash) (io.ReadCloser, error) {
	f.openCalls = append(f.openCalls, root)
	if err, ok := f.openErr[root]; ok {
		return nil, err
	}
	b, ok := f.content[root]
	if !ok {
		return nil, core.ErrNotFound
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

func (f *fakeStore) Has(h core.Hash) bool { _, ok := f.content[h]; return ok }

// Search is the method item 2 must never reach. It counts and then fails loudly rather than
// returning a plausible answer, so a regression shows up as a failing test instead of a subtly
// wrong payload.
func (f *fakeStore) Search(ctx context.Context, q store.Query) ([]store.Hit, error) {
	f.searchCalls++
	return nil, core.ErrNotImplemented
}

// Everything below is store.Store's remaining surface. This slice never calls any of it.

func (f *fakeStore) Put(ctx context.Context, r io.Reader, o store.PutOptions) (store.PutResult, error) {
	return store.PutResult{}, core.ErrNotImplemented
}

func (f *fakeStore) PutBytes(ctx context.Context, b []byte, o store.PutOptions) (store.PutResult, error) {
	return store.PutResult{}, core.ErrNotImplemented
}

func (f *fakeStore) GetChunk(ctx context.Context, h core.Hash) ([]byte, error) {
	return nil, core.ErrNotImplemented
}

func (f *fakeStore) GetRoot(ctx context.Context, root core.Hash) (store.Root, error) {
	return store.Root{}, core.ErrNotImplemented
}

func (f *fakeStore) OpenSpan(ctx context.Context, root core.Hash, off, n int64) (io.ReadCloser, error) {
	return nil, core.ErrNotImplemented
}

func (f *fakeStore) RecordToolUse(ctx context.Context, rec store.ToolUseRecord) error {
	return core.ErrNotImplemented
}

func (f *fakeStore) ToolUsesByPath(ctx context.Context, path string, limit int) ([]store.ToolUseRecord, error) {
	return nil, core.ErrNotImplemented
}

func (f *fakeStore) MarkSuperseded(ctx context.Context, older, by core.ToolUseID) error {
	return core.ErrNotImplemented
}

func (f *fakeStore) AppendFileVersion(ctx context.Context, path string, v store.FileVersion) error {
	return core.ErrNotImplemented
}

func (f *fakeStore) FileHistory(ctx context.Context, path string) ([]store.FileVersion, error) {
	return nil, core.ErrNotImplemented
}

func (f *fakeStore) FileAt(ctx context.Context, path string, at time.Time) (store.FileVersion, error) {
	return store.FileVersion{}, core.ErrNotImplemented
}

func (f *fakeStore) ChangedSince(ctx context.Context, deps []core.Dep) ([]core.Dep, error) {
	return nil, core.ErrNotImplemented
}

func (f *fakeStore) Segments() store.SegmentLog { return fakeSegmentLog{} }

func (f *fakeStore) Stats(ctx context.Context) (store.Stats, error) {
	return store.Stats{}, core.ErrNotImplemented
}

func (f *fakeStore) GC(ctx context.Context, p store.GCPolicy) (store.GCReport, error) {
	return store.GCReport{}, core.ErrNotImplemented
}

func (f *fakeStore) Flush(ctx context.Context) error { return core.ErrNotImplemented }
func (f *fakeStore) Close() error                    { return nil }

// fakeSegmentLog is fakeStore's SegmentLog counterpart. This slice never reaches it.
type fakeSegmentLog struct{}

func (fakeSegmentLog) Open(ctx context.Context, s store.Segment) (core.SegmentID, error) {
	return 0, core.ErrNotImplemented
}

func (fakeSegmentLog) Close(ctx context.Context, id core.SegmentID, endTurn core.TurnIndex, feats map[string]float64) error {
	return core.ErrNotImplemented
}

func (fakeSegmentLog) Get(ctx context.Context, id core.SegmentID) (store.Segment, error) {
	return store.Segment{}, core.ErrNotImplemented
}

func (fakeSegmentLog) Range(ctx context.Context, from, to core.TurnIndex) ([]store.Segment, error) {
	return nil, core.ErrNotImplemented
}

func (fakeSegmentLog) Current(ctx context.Context, s core.SessionID) (store.Segment, error) {
	return store.Segment{}, core.ErrNotImplemented
}

func (fakeSegmentLog) MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error {
	return core.ErrNotImplemented
}

func (fakeSegmentLog) Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error) {
	return 0, core.ErrNotImplemented
}

func (fakeSegmentLog) Unencoded(ctx context.Context, s core.SessionID) ([]store.Segment, error) {
	return nil, core.ErrNotImplemented
}

// ── fakeLedger ──

// fakeLedger is a scripted negknow.Ledger. Active is the only method item 3 calls, and it is
// scripted per scope so a test can prove BOTH scopes are read — the project scope carries the
// longest-lived negative knowledge and is the one a defaultScope-shaped read would lose.
type fakeLedger struct {
	active    map[negknow.Scope][]negknow.Record
	activeErr map[negknow.Scope]error
	// scopesRead records every Active argument in call order.
	scopesRead []negknow.Scope
	// get scripts Get(id) results; see withGet.
	get map[string]getResult
	// getCalled records every Get argument in call order.
	getCalled []string
}

// getResult is one scripted fakeLedger.Get outcome.
type getResult struct {
	rec negknow.Record
	err error
}

func newFakeLedger() *fakeLedger {
	return &fakeLedger{
		active:    map[negknow.Scope][]negknow.Record{},
		activeErr: map[negknow.Scope]error{},
	}
}

func (f *fakeLedger) withActive(scope negknow.Scope, recs ...negknow.Record) *fakeLedger {
	f.active[scope] = recs
	return f
}

func (f *fakeLedger) withActiveErr(scope negknow.Scope, err error) *fakeLedger {
	f.activeErr[scope] = err
	return f
}

func (f *fakeLedger) Active(ctx context.Context, scope negknow.Scope) ([]negknow.Record, error) {
	f.scopesRead = append(f.scopesRead, scope)
	if err, ok := f.activeErr[scope]; ok {
		return nil, err
	}
	return f.active[scope], nil
}

func (f *fakeLedger) Record(ctx context.Context, r negknow.Record) (string, error) {
	return "", core.ErrNotImplemented
}

func (f *fakeLedger) Query(ctx context.Context, target, approach string, scope negknow.Scope) (negknow.Answer, error) {
	return negknow.Answer{}, core.ErrNotImplemented
}

// withGet scripts Get(id) to return rec, err. Real Active() implementations return only
// StatusActive records (negknow.Ledger.Active's own contract), so a record that went stale or was
// otherwise superseded since a checkpoint was written is NOT rediscoverable through Active() —
// only through Get(id), which is what eliminationCandidates must consult before trusting a
// checkpoint-frozen record Active() no longer names.
func (f *fakeLedger) withGet(id string, rec negknow.Record, err error) *fakeLedger {
	if f.get == nil {
		f.get = map[string]getResult{}
	}
	f.get[id] = getResult{rec: rec, err: err}
	return f
}

func (f *fakeLedger) Get(ctx context.Context, id string) (negknow.Record, error) {
	f.getCalled = append(f.getCalled, id)
	if r, ok := f.get[id]; ok {
		return r.rec, r.err
	}
	return negknow.Record{}, core.ErrNotImplemented
}

func (f *fakeLedger) All(ctx context.Context) ([]negknow.Record, error) {
	return nil, core.ErrNotImplemented
}

func (f *fakeLedger) MarkStale(ctx context.Context, ids, because []string) error {
	return core.ErrNotImplemented
}

func (f *fakeLedger) RefreshStaleness(ctx context.Context, s store.Store) ([]string, error) {
	return nil, core.ErrNotImplemented
}

func (f *fakeLedger) RebuildBloom(ctx context.Context) (*sketch.Bloom, negknow.Health, error) {
	return nil, negknow.Health{}, core.ErrNotImplemented
}

func (f *fakeLedger) Health() negknow.Health { return negknow.Health{} }
func (f *fakeLedger) Close() error           { return nil }

// ── fakeGraph ──

// fakeGraph serves one scripted BackwardSlice score map.
//
// Every test that builds one keys it with dag's exported constructors — dag.EliminationNode,
// dag.FileNode, dag.SymbolNode — for the same reason the production code calls them: a hand-built
// "elimination:"+id literal does not fail when it is wrong, it returns zero for every record, and
// the ordering silently degrades to the TS/ID tiebreak with no signal.
type fakeGraph struct {
	scores map[dag.NodeID]float32
	err    error
	// criteria records every BackwardSlice criterion set in call order.
	criteria [][]dag.NodeID
}

func newFakeGraph(scores map[dag.NodeID]float32) *fakeGraph {
	return &fakeGraph{scores: scores}
}

func (f *fakeGraph) BackwardSlice(criteria []dag.NodeID, o dag.SliceOptions) (dag.Slice, error) {
	c := make([]dag.NodeID, len(criteria))
	copy(c, criteria)
	f.criteria = append(f.criteria, c)
	if f.err != nil {
		return dag.Slice{}, f.err
	}
	return dag.Slice{Scores: f.scores}, nil
}

func (f *fakeGraph) AddNode(n dag.Node) error            { return core.ErrNotImplemented }
func (f *fakeGraph) AddEdge(e dag.Edge) error            { return core.ErrNotImplemented }
func (f *fakeGraph) Node(id dag.NodeID) (dag.Node, bool) { return dag.Node{}, false }
func (f *fakeGraph) Out(id dag.NodeID) []dag.Edge        { return nil }
func (f *fakeGraph) In(id dag.NodeID) []dag.Edge         { return nil }

func (f *fakeGraph) ForwardSlice(criteria []dag.NodeID, o dag.SliceOptions) (dag.Slice, error) {
	return dag.Slice{}, core.ErrNotImplemented
}

func (f *fakeGraph) CrossingEdges(pos int) int         { return 0 }
func (f *fakeGraph) NodesAfter(pos int) []dag.Node     { return nil }
func (f *fakeGraph) Flush(ctx context.Context) error   { return nil }
func (f *fakeGraph) Compact(ctx context.Context) error { return nil }
func (f *fakeGraph) Stats() dag.GraphStats             { return dag.GraphStats{} }

// ── spyLogger ──

// spyLogger counts calls per level. The counts are assertions in their own right: item 2's
// fallback is an Info (an ordinary absence), its id-scheme drift is a Warn (a contract drift), and
// its L0/checkpoint mismatch is a Loud (something regenerated what §8.5 says is never
// regenerated). Getting the level wrong is getting the severity wrong.
type spyLogger struct {
	debug, info, warn, errors, loud int
	msgs                            []string
}

func (s *spyLogger) With(kv ...any) logging.Logger { return s }
func (s *spyLogger) Debug(msg string, kv ...any)   { s.debug++; s.msgs = append(s.msgs, msg) }
func (s *spyLogger) Info(msg string, kv ...any)    { s.info++; s.msgs = append(s.msgs, msg) }
func (s *spyLogger) Warn(msg string, kv ...any)    { s.warn++; s.msgs = append(s.msgs, msg) }
func (s *spyLogger) Error(msg string, kv ...any)   { s.errors++; s.msgs = append(s.msgs, msg) }
func (s *spyLogger) Loud(msg string, kv ...any)    { s.loud++; s.msgs = append(s.msgs, msg) }

// ── fakeScanner ──

// fakeScanner is a scripted rules.Scanner. Item 6a is tested against it rather than against the
// real scanner, which a sibling subagent is writing concurrently.
type fakeScanner struct {
	pathScoped []rules.Rule
	nested     []rules.Rule
	pathErr    error
	nestedErr  error
	// pointers records the pointer set each call was given, which is what proves item 6a passes
	// the checkpoint's file pointers through rather than scanning the whole tree.
	pointers [][]string
}

func (f *fakeScanner) PathScoped(ctx context.Context, root string, pointers []string) ([]rules.Rule, error) {
	f.pointers = append(f.pointers, append([]string(nil), pointers...))
	if f.pathErr != nil {
		return nil, f.pathErr
	}
	return f.pathScoped, nil
}

func (f *fakeScanner) NestedClaudeMD(ctx context.Context, root string, pointers []string) ([]rules.Rule, error) {
	f.pointers = append(f.pointers, append([]string(nil), pointers...))
	if f.nestedErr != nil {
		return nil, f.nestedErr
	}
	return f.nested, nil
}

// ── fakeIndexer ──

// fakeIndexer is a scripted skills.Indexer. It answers the budget-0 "give me everything" call with
// all and any other budget with kept, which is exactly the two-call shape item 6b depends on to
// tell "indexed" from "known but not indexed".
type fakeIndexer struct {
	all  []skills.Entry
	kept []skills.Entry
	err  error
	// budgets records every Index budget in call order, so a test can prove the compact call used
	// runtime.rehydrate.skillIndexTokens and not a literal.
	budgets []core.Tokens
}

func (f *fakeIndexer) Index(ctx context.Context, root string, budget core.Tokens) ([]skills.Entry, core.Tokens, error) {
	f.budgets = append(f.budgets, budget)
	if f.err != nil {
		return nil, 0, f.err
	}
	if budget == 0 {
		return f.all, 0, nil
	}
	return f.kept, budget, nil
}

// fakeBodyTokens returns a bodyTokensFunc serving a scripted per-skill body size. It stands in for
// skills.BodyTokens, which lands with the real Indexer.
func fakeBodyTokens(sizes map[string]core.Tokens) bodyTokensFunc {
	return func(root string, e skills.Entry) (core.Tokens, error) {
		v, ok := sizes[e.Name]
		if !ok {
			return 0, core.ErrNotFound
		}
		return v, nil
	}
}

// ── fakeEstimator ──

// fakeEstimator is the baseline (len+3)/4 estimate, which is what the real estimator falls back to
// before calibration. Using it keeps the budget arithmetic in a test predictable from the rendered
// text alone.
type fakeEstimator struct{}

func (fakeEstimator) Estimate(b []byte, c tokens.Class) core.Tokens {
	return core.Tokens((len(b) + 3) / 4)
}

func (fakeEstimator) EstimateString(s string, c tokens.Class) core.Tokens {
	return core.Tokens((len(s) + 3) / 4)
}

func (fakeEstimator) EstimateRoot(ctx context.Context, chunks []core.ChunkRef, c tokens.Class) core.Tokens {
	var n int
	for _, ch := range chunks {
		n += ch.Len
	}
	return core.Tokens((n + 3) / 4)
}

func (fakeEstimator) Calibrate(observed, estimated core.Tokens) {}
func (fakeEstimator) Factor() float64                           { return 1 }

// ── checkpoint fixtures ──

// goldenCheckpointDir is testdata/golden/contracts/checkpoint/want/, relative to this package's
// own directory. The fixtures there are FROZEN (Rule W-2): these tests may never edit them. A case
// needing more records builds them in a fake or appends to an in-memory copy.
const goldenCheckpointDir = "../../testdata/golden/contracts/checkpoint/want"

// ckFull decodes the frozen §8.5 artifact with every tier populated: 2 invariants, an original
// intent plus 2 evolution entries, 1 project-scoped active elimination on
// src/auth.ts:refreshToken, 1 decision at turn 61 with two rejected alternatives, 2 open
// questions, current work with blocked_on null, 2 file pointers and 1 tool pointer, a narrative,
// and 2 pre-existing dropped entries.
//
// It is decoded rather than hand-built so that a change to the wire shape surfaces here, and it is
// never constructed through checkpoint.Writer — that belongs to a same-wave sibling. Field
// assignment is the technique the inherited conformance suite itself uses.
func ckFull(t *testing.T) checkpoint.Checkpoint {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenCheckpointDir, "0001.json"))
	require.NoError(t, err, "frozen contract fixture missing")

	var cp checkpoint.Checkpoint
	require.NoError(t, json.Unmarshal(b, &cp))

	// Fixture sanity: every assertion downstream reads one of these.
	require.Len(t, cp.Invariants, 2)
	require.Len(t, cp.UserIntent.Evolution, 2)
	require.Len(t, cp.Eliminated, 1)
	require.Len(t, cp.Decisions, 1)
	require.Len(t, cp.Pointers.Files, 2)
	require.Len(t, cp.Pointers.Tools, 1)
	require.Len(t, cp.Dropped, 2)
	return cp
}

// ckMinimal is a checkpoint carrying only tier-1 material, built by FIELD ASSIGNMENT: one
// invariant and an original intent with no evolution, no eliminations, no decisions, no pointers.
// It is the shape that proves the standing instruction is absent when item 3 is.
func ckMinimal() checkpoint.Checkpoint {
	var cp checkpoint.Checkpoint
	cp.Version = checkpoint.SchemaVersion
	cp.Session = core.SessionID("sess_min")
	cp.Seq = core.CheckpointSeq(2)
	cp.Created = "2026-01-01T00:12:30.000Z"
	cp.Invariants = []checkpoint.Invariant{{
		ID: "inv_a1b2c3d4e5f6", Text: "Never bypass the connection pool in transaction mode.",
		Source: "/qompack:pin", Pinned: core.UnixMilli(1767225120000),
	}}
	cp.UserIntent.Original = "Fix the Stripe webhook retry logic; payments are being double-charged under load."
	return cp
}

// ckEmpty is a checkpoint with nothing in it at all — the degenerate shape every builder must
// answer with built{} rather than a panic or an empty section.
func ckEmpty() checkpoint.Checkpoint {
	var cp checkpoint.Checkpoint
	cp.Version = checkpoint.SchemaVersion
	cp.Session = core.SessionID("sess_empty")
	cp.Seq = core.CheckpointSeq(1)
	return cp
}

// ── request and deps helpers ──

// testCfg is the Appendix C default configuration: eliminationsTopN 8, staleResponse "flag",
// defaultScope "session". Builder tests that vary one key start from this so the rest stay at
// their shipped defaults rather than at Go's zero values.
func testCfg() config.Config { return config.Defaults() }

// requestFor is a compact rehydration request for cp at budget.
func requestFor(t *testing.T, cp checkpoint.Checkpoint, budget core.Tokens) Request {
	t.Helper()
	for _, tp := range cp.Pointers.Tools {
		requireSummaryFitsOnAHostedRunner(t, tp.Summary)
	}
	var r Request
	r.Session = cp.Session
	r.Source = "compact"
	r.ProjectRoot = "/repo"
	r.Budget = budget
	r.Checkpoint = cp
	r.Ref.Seq = cp.Seq
	r.Ref.Path = "/repo/.qompack/checkpoints/0001.json"
	r.Cfg = testCfg()
	return r
}

// fullDeps wires every collaborator with a fake scripted to agree with cp: the L0 prompt matches
// the checkpoint's own original intent (so no mismatch is reported), the ledger serves the
// checkpoint's eliminations at project scope, and the graph scores nothing.
func fullDeps(t *testing.T, cp checkpoint.Checkpoint) Deps {
	t.Helper()
	st := newFakeStore().withPrompt(firstPromptID(cp.Session), cp.Session, 0, cp.UserIntent.Original)
	led := newFakeLedger().withActive(negknow.ScopeProject, cp.Eliminated...)
	return Deps{
		Store:  st,
		Ledger: led,
		Graph:  newFakeGraph(nil),
		Rules:  &fakeScanner{},
		Skills: &fakeIndexer{},
		Tokens: fakeEstimator{},
		Log:    &spyLogger{},
	}
}

// unitTexts projects a built's units down to their rendered text, which is what most builder
// assertions compare.
func unitTexts(b built) []string {
	out := make([]string, 0, len(b.units))
	for _, u := range b.units {
		out = append(out, u.text)
	}
	return out
}

// TestFakesAreScripted is the fakes' own smoke test. Without it a mis-scripted fake shows up as a
// confusing failure inside a builder test rather than as a failure of the fake.
func TestFakesAreScripted(t *testing.T) {
	st := newFakeStore().withPrompt("prompt_s1_0", "s1", 0, "hello")

	rec, err := st.ToolUse(context.Background(), "prompt_s1_0")
	require.NoError(t, err)
	require.Equal(t, core.SessionID("s1"), rec.Session)

	rc, err := st.Open(context.Background(), rec.Root)
	require.NoError(t, err)
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.Equal(t, "hello", string(b))
	require.Equal(t, 0, st.searchCalls, "the fake must never be searched")

	_, err = st.ToolUse(context.Background(), "prompt_s1_9")
	require.ErrorIs(t, err, core.ErrNotFound)
}
