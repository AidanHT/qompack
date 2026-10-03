package checkpoint_test

// The SP-10 writer/draft/DPI-guard behaviour suite of plans/V4-SP-10-checkpointer-l4.md
// (§16's "writer, draft, DPI guard" table), driven end to end against REAL backends: the
// conformance-suite packages export no constructible fakes (pre-flight B2), so every fixture here
// is a real store.Open, dag.Open and negknow.Open over a testutil project. The two seams that are
// still stubs on this branch are substituted the only honest way available:
//
//   - pins.Open still returns the SP-01 stub, so SourceSet.Pins is a fakePins declared below —
//     a test-local pins.Store, exactly as the SP-10 brief authorizes.
//   - OpenReader still returns the SP-01 stub, so the parent-intent test installs a fakeReader
//     through checkpoint.SetReaderForTesting (declared in draft_test.go, in-package).
//
// Finalize is another seat's file and does not exist on *FileWriter yet, so every frontier
// assertion reads Draft.Frontier() rather than a Ref.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
)

// writerSession is the one session every test in this file writes checkpoints for.
const writerSession = core.SessionID("sess_sp10_writer")

// fakePins is the test-local pins.Store the brief authorizes while internal/pins is still the
// SP-01 stub: Begin's invariant seeding needs a working All, and nothing else.
type fakePins struct {
	invs []pins.Invariant
	// materialized counts Materialize calls, so finalize_test.go can assert that sealing a
	// checkpoint refreshes the view rather than leaving invariants.json a checkpoint behind.
	materialized int
	// ctxDeadline is the deadline of the context Materialize was last called with, and hasDeadline
	// says whether there was one. Materialize is the last thing Finalize does with PreCompact's
	// derived context, which makes it the one seam a test can read that context's budget from —
	// see TestPreCompactDerivesItsBudgetFromTheCallersDeadline.
	ctxDeadline time.Time
	hasDeadline bool
	// allErr, when set, is what All answers instead of the list: a pin log that cannot be read.
	allErr error
}

var _ pins.Store = (*fakePins)(nil)

func (f *fakePins) Add(_ context.Context, inv pins.Invariant) error {
	f.invs = append(f.invs, inv)
	return nil
}
func (f *fakePins) Remove(_ context.Context, id string) error { return nil }
func (f *fakePins) All(_ context.Context) ([]pins.Invariant, error) {
	if f.allErr != nil {
		return nil, f.allErr
	}
	out := make([]pins.Invariant, len(f.invs))
	copy(out, f.invs)
	return out, nil
}

func (f *fakePins) Materialize(ctx context.Context) error {
	f.materialized++
	f.ctxDeadline, f.hasDeadline = ctx.Deadline()
	return nil
}

// fakeReader is the test-local checkpoint.Reader the parent-intent test installs while OpenReader
// is still the SP-01 stub. Get answers from a fixed map; everything else is ErrNotFound.
type fakeReader struct {
	bySeq map[core.CheckpointSeq]checkpoint.Checkpoint
}

var _ checkpoint.Reader = fakeReader{}

func (r fakeReader) Latest(context.Context, core.SessionID) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	return checkpoint.Checkpoint{}, checkpoint.Ref{}, core.ErrNotFound
}

func (r fakeReader) Get(_ context.Context, seq core.CheckpointSeq) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	c, ok := r.bySeq[seq]
	if !ok {
		return checkpoint.Checkpoint{}, checkpoint.Ref{}, core.ErrNotFound
	}
	return c, checkpoint.Ref{Seq: seq}, nil
}
func (r fakeReader) List(context.Context) ([]checkpoint.Ref, error) { return nil, nil }
func (r fakeReader) Chain(context.Context, core.CheckpointSeq) ([]checkpoint.Checkpoint, error) {
	return nil, core.ErrNotFound
}

func (r fakeReader) Verify(context.Context) ([]core.CheckpointSeq, error) { return nil, nil }

// draftWire mirrors the state/draft-<session>.json wrapper writer.go persists, so tests can
// assert the on-disk draft without reaching into the Draft struct.
type draftWire struct {
	Session       core.SessionID     `json:"session"`
	Seq           core.CheckpointSeq `json:"seq"`
	Parent        core.CheckpointSeq `json:"parent"`
	Frontier      core.TurnIndex     `json:"frontier"`
	Encoded       []core.SegmentID   `json:"encoded"`
	Started       core.UnixMilli     `json:"started"`
	WorkExplicit  bool               `json:"work_explicit"`
	UserQuestions []string           `json:"user_questions"`
	Checkpoint    json.RawMessage    `json:"checkpoint"`
}

// fx is one complete writer fixture: a real project, real store/graph/ledger backends, the
// test-local pins fake, and a FileWriter over the same root.
type fx struct {
	t      *testing.T
	p      *testutil.Project
	store  store.Store
	graph  dag.Graph
	ledger negknow.Ledger
	pins   *fakePins
	w      *checkpoint.FileWriter
	src    checkpoint.SourceSet
	sess   core.SessionID
	pos    int
}

func newFx(t *testing.T) *fx {
	t.Helper()
	p := testutil.NewProject(t)
	st := p.Store(t)
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)
	led, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Session: writerSession, Clock: p.Clock, Log: p.Log, Graph: g, Store: st,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })

	fp := &fakePins{}
	est := tokens.NewForProject(p.Cfg, tokens.DefaultCalibPath(), p.Root)
	w, err := checkpoint.OpenWriter(p.Root, p.Cfg, p.Log, obs.New(p.Clock), p.Clock)
	require.NoError(t, err)

	f := &fx{t: t, p: p, store: st, graph: g, ledger: led, pins: fp, w: w, sess: writerSession}
	f.src = checkpoint.SourceSet{
		Store: st, Segments: st.Segments(), Ledger: led, Pins: fp,
		Graph: g, Grammar: grammar.New(), Tokens: est,
	}
	return f
}

func (f *fx) ctx() context.Context { return context.Background() }

func (f *fx) now() core.UnixMilli {
	f.p.Clock.Advance(time.Second)
	return core.NowMilli(f.p.Clock)
}

func (f *fx) nextPos() int { f.pos += 64; return f.pos }

// put stores text and returns its root hash. Prompts go through the observer's own verbatim
// canon request (empty non-nil Strip) so the bytes read back are byte-identical to the input.
func (f *fx) put(text, tool, path string, verbatim bool) core.Hash {
	f.t.Helper()
	o := store.PutOptions{Tool: tool, Path: path}
	if verbatim {
		o.Canon = canon.Options{Strip: []canon.Class{}}
	}
	res, err := f.store.PutBytes(f.ctx(), []byte(text), o)
	require.NoError(f.t, err)
	return res.Root.Hash
}

// prompt records one user prompt the way internal/observer does: verbatim bytes in the store, a
// ToolUseRecord keyed "prompt_<session>_<turn>", and a userprompt:<turn> node whose Ref is that
// record id. rootOnNode additionally stamps the content root onto the node itself — the shape the
// SP-10 plan assumes — so both read paths in the writer are exercised.
func (f *fx) prompt(turn core.TurnIndex, text string, rootOnNode bool) {
	f.t.Helper()
	root := f.put(text, "UserPromptSubmit", "", true)
	recID := core.ToolUseID(fmt.Sprintf("prompt_%s_%d", f.sess, int(turn)))
	require.NoError(f.t, f.store.RecordToolUse(f.ctx(), store.ToolUseRecord{
		ID: recID, Session: f.sess, Turn: turn, TS: f.now(), Tool: "UserPromptSubmit", Root: root,
	}))
	n := dag.Node{
		ID: dag.UserPromptNode(turn), Kind: dag.KindUserPrompt,
		Turn: turn, TS: f.now(), Pos: f.nextPos(), Ref: string(recID),
	}
	if rootOnNode {
		n.Root = root
	}
	require.NoError(f.t, f.graph.AddNode(n))
}

// tool records one observed tool call: stored result bytes, the index record, the §8.1 item 4
// node/edge set via dag.BuildToolUse, and — when it touched a path — a file version.
func (f *fx) tool(id string, turn core.TurnIndex, toolName, pathKey, body string, ephemeral bool) core.Hash {
	f.t.Helper()
	root := f.put(body, toolName, pathKey, false)
	require.NoError(f.t, f.store.RecordToolUse(f.ctx(), store.ToolUseRecord{
		ID: core.ToolUseID(id), Session: f.sess, Turn: turn, TS: f.now(), Tool: toolName,
		ArgsPreview: toolName + " " + pathKey, Root: root, Path: pathKey, Ephemeral: ephemeral,
	}))
	require.NoError(f.t, dag.BuildToolUse(f.graph, dag.ObservedTool{
		ToolUseID: core.ToolUseID(id), Turn: turn, TS: f.now(), Pos: f.nextPos(),
		Tool: toolName, PathKey: pathKey, Writes: toolName == "Edit" || toolName == "Write",
		Root: root, Tokens: core.Tokens(len(body) / 4), Ephemeral: ephemeral,
	}))
	if pathKey != "" {
		require.NoError(f.t, f.store.AppendFileVersion(f.ctx(), pathKey, store.FileVersion{
			TS: f.now(), Root: root, Turn: turn, Bytes: int64(len(body)),
		}))
	}
	return root
}

func (f *fx) closedSeg(id core.SegmentID, start, end core.TurnIndex) {
	f.t.Helper()
	got, err := f.store.Segments().Open(f.ctx(), store.Segment{ID: id, Session: f.sess, StartTurn: start})
	require.NoError(f.t, err)
	require.Equal(f.t, id, got)
	require.NoError(f.t, f.store.Segments().Close(f.ctx(), id, end,
		map[string]float64{"tokens": 400, "tool_rate": 0.5}))
}

func (f *fx) elim(sess core.SessionID, scope negknow.Scope, target, approach, reason string) string {
	f.t.Helper()
	id, err := f.ledger.Record(f.ctx(), negknow.Record{
		Session: sess, Scope: scope, Target: target, Approach: approach, Reason: reason,
		Evidence: core.HashBytes(core.DomainChunk, []byte(target+"\x00"+approach)),
	})
	require.NoError(f.t, err)
	return id
}

func (f *fx) begin() *checkpoint.Draft {
	f.t.Helper()
	d, err := f.w.Begin(f.ctx(), f.sess, 0, f.src)
	require.NoError(f.t, err)
	return d
}

func (f *fx) draftPath() string {
	return filepath.Join(paths.Of(f.p.Root).State, "draft-"+string(f.sess)+".json")
}

// persisted reads the draft file back and returns both the wrapper and the embedded checkpoint —
// the observable state "persist after every mutating call" promises.
func (f *fx) persisted() (draftWire, checkpoint.Checkpoint) {
	f.t.Helper()
	b, err := os.ReadFile(paths.Long(f.draftPath()))
	require.NoError(f.t, err, "the draft must be persisted at %s", f.draftPath())
	var w draftWire
	require.NoError(f.t, json.Unmarshal(b, &w))
	cp, err := checkpoint.Unmarshal(w.Checkpoint)
	require.NoError(f.t, err)
	return w, cp
}

func (f *fx) advance(d *checkpoint.Draft, segs ...core.SegmentID) core.TurnIndex {
	f.t.Helper()
	fr, err := f.w.Advance(f.ctx(), d, segs)
	require.NoError(f.t, err)
	return fr
}

// ── Begin ─────────────────────────────────────────────────────────────────────────────────────

func TestBeginValidatesSources(t *testing.T) {
	f := newFx(t)
	_, err := f.w.Begin(f.ctx(), f.sess, 0, checkpoint.SourceSet{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "SourceSet.Store")
}

func TestBeginSeedsTierOneFromSources(t *testing.T) {
	f := newFx(t)
	f.pins.invs = []pins.Invariant{
		{ID: "inv_1", Text: "tests use the FakeClock", Source: "user", Pinned: 1},
		{ID: "inv_2", Text: "never compress a compression", Source: "user", Pinned: 2},
		{ID: "inv_3", Text: "checkpoints carry no code", Source: "agent", Pinned: 3},
	}
	original := "Fix the flaky auth refresh so the session survives a cold start."
	f.prompt(1, original, true)
	f.elim(f.sess, negknow.ScopeSession, "src/auth.ts", "widen pool timeout", "ignored in transaction mode")
	f.elim("sess_other", negknow.ScopeProject, "docker", "compose v1 syntax", "removed upstream")

	d := f.begin()
	require.Equal(t, core.CheckpointSeq(1), d.Seq(), "first checkpoint of a project is seq 1")

	w, cp := f.persisted()
	require.Equal(t, f.sess, w.Session)
	require.Len(t, cp.Invariants, 3)
	require.Equal(t, "inv_1", cp.Invariants[0].ID, "invariants stay in pins.All order")
	require.Equal(t, original, cp.UserIntent.Original, "the original intent is the stored prompt, verbatim")
	require.Len(t, cp.Eliminated, 2)
	require.Equal(t, "", cp.Parent, "parent 0 has no parent file")
	require.Equal(t, "unknown", cp.Cache.TTLState)
}

func TestBeginFiltersEliminationsToSessionAndProjectScope(t *testing.T) {
	f := newFx(t)
	f.elim(f.sess, negknow.ScopeSession, "a", "x", "r")
	f.elim("sess_other", negknow.ScopeProject, "b", "y", "r")
	f.elim("sess_other", negknow.ScopeSession, "c", "z", "r")

	f.begin()
	_, cp := f.persisted()
	require.Len(t, cp.Eliminated, 2, "another session's session-scoped record must not leak in")
}

func TestBeginInheritsOriginalIntentFromParent(t *testing.T) {
	f := newFx(t)
	original := "Original intent, verbatim — including <angles> & ampersands."
	checkpoint.SetReaderForTesting(f.w, fakeReader{bySeq: map[core.CheckpointSeq]checkpoint.Checkpoint{
		1: {
			Version: checkpoint.SchemaVersion, Session: f.sess, Seq: 1,
			UserIntent: checkpoint.UserIntent{Original: original},
		},
	}})
	require.NoError(t, paths.AppendManifest(paths.Of(f.p.Root),
		paths.ManifestEntry{Seq: 1, SHA256: "0", Bytes: 1, Created: 1}))

	d, err := f.w.Begin(f.ctx(), f.sess, 1, f.src)
	require.NoError(t, err)
	require.Equal(t, core.CheckpointSeq(2), d.Seq())

	w, cp := f.persisted()
	require.Equal(t, original, cp.UserIntent.Original, "the parent chain carries the verbatim original")
	require.Equal(t, core.CheckpointSeq(1), w.Parent)
	require.Equal(t, "0001.json", cp.Parent)
}

func TestBeginResumesPersistedDraft(t *testing.T) {
	f := newFx(t)
	raw := fmt.Sprintf(`{"session":%q,"seq":2,"parent":0,"frontier":40,"encoded":[7],`+
		`"started":1767225600000,"checkpoint":{"version":1,"session":%q,"seq":2,"created":"",`+
		`"encoded_segments":[7]}}`, f.sess, f.sess)
	require.NoError(t, os.WriteFile(paths.Long(f.draftPath()), []byte(raw), 0o600))

	d := f.begin()
	require.Equal(t, core.TurnIndex(40), d.Frontier())
	require.Equal(t, core.CheckpointSeq(2), d.Seq())
	require.Equal(t, 1, d.EncodedCount())
}

func TestBeginDiscardsDraftWithClaimedSeq(t *testing.T) {
	f := newFx(t)
	raw := fmt.Sprintf(`{"session":%q,"seq":3,"parent":0,"frontier":40,"encoded":[],`+
		`"started":1767225600000,"checkpoint":{"version":1,"session":%q,"seq":3,"created":"",`+
		`"encoded_segments":[]}}`, f.sess, f.sess)
	require.NoError(t, os.WriteFile(paths.Long(f.draftPath()), []byte(raw), 0o600))
	require.NoError(t, paths.AppendManifest(paths.Of(f.p.Root),
		paths.ManifestEntry{Seq: 3, SHA256: "0", Bytes: 1, Created: 1}))

	d := f.begin()
	require.Equal(t, core.CheckpointSeq(4), d.Seq(), "a claimed seq begins fresh at maxSeq+1")
	require.Equal(t, core.TurnIndex(0), d.Frontier())

	stale := filepath.Join(paths.Of(f.p.Root).State, "draft-"+string(f.sess)+".stale.json")
	_, err := os.Stat(paths.Long(stale))
	require.NoError(t, err, "the claimed draft is renamed aside, not deleted")
}

// ── Advance ───────────────────────────────────────────────────────────────────────────────────

func TestAdvanceEncodesClosedSegmentsOnly(t *testing.T) {
	f := newFx(t)
	f.tool("tu_a", 12, "Read", "src/a.ts", "alpha body", false)
	f.closedSeg(12, 10, 19)
	_, err := f.store.Segments().Open(f.ctx(), store.Segment{ID: 13, Session: f.sess, StartTurn: 20})
	require.NoError(t, err)

	d := f.begin()
	fr := f.advance(d, 12, 13)
	require.Equal(t, core.TurnIndex(19), fr, "the frontier reaches segment 12's EndTurn")
	require.Equal(t, core.TurnIndex(19), d.Frontier())

	_, cp := f.persisted()
	require.Equal(t, []core.SegmentID{12}, cp.EncodedSegments, "the open segment is skipped, not encoded")
}

func TestAdvanceIsDPIGuarded(t *testing.T) {
	f := newFx(t)
	f.tool("tu_a", 12, "Read", "src/a.ts", "alpha body", false)
	f.closedSeg(12, 10, 19)
	require.NoError(t, f.store.Segments().MarkEncoded(f.ctx(), []core.SegmentID{12}, 5))
	require.NoError(t, paths.AppendManifest(paths.Of(f.p.Root),
		paths.ManifestEntry{Seq: 5, SHA256: "0", Bytes: 1, Created: 1}))

	d := f.begin()
	require.Equal(t, core.CheckpointSeq(6), d.Seq())

	_, err := f.w.Advance(f.ctx(), d, []core.SegmentID{12})
	require.ErrorIs(t, err, core.ErrAlreadyEncoded)

	w, cp := f.persisted()
	require.Equal(t, core.CheckpointSeq(6), w.Seq, "the draft is still persisted after the DPI error")
	require.Empty(t, cp.EncodedSegments, "a DPI-guarded segment is never encoded into the draft")

	seg, err := f.store.Segments().Get(f.ctx(), 12)
	require.NoError(t, err)
	require.Equal(t, core.CheckpointSeq(5), seg.CheckpointSeq, "the store's mark is untouched")
}

func TestAdvanceStripsInjectionsFromStoredPrompts(t *testing.T) {
	f := newFx(t)
	f.prompt(1, "Clean original intent.", true)
	injected := "a\n" + checkpoint.OpenTag(4) + "\nstale summary\n" + checkpoint.InjectionCloseTag + "\nb"
	f.prompt(11, injected, true)
	f.closedSeg(12, 10, 19)

	d := f.begin()
	f.advance(d, 12)

	_, cp := f.persisted()
	require.NotEmpty(t, cp.UserIntent.Evolution, "the stripped remainder still evolves the intent")
	for _, e := range cp.UserIntent.Evolution {
		require.NotContains(t, e, "stale summary", "an injected body must never re-enter a checkpoint")
		require.NotContains(t, e, "qompack:injected")
	}
	// The goal is the stripped remainder's first sentence — here all of it, which has no sentence
	// break — exactly: an empty goal would pass a NotContains as well.
	stripped, _ := checkpoint.StripInjectionsCount(injected)
	require.NotContains(t, stripped, "stale summary")
	require.Equal(t, stripped, cp.CurrentWork.Goal)
}

// TestAdvanceReadsPromptTextThroughToolUseRecordFallback pins the fallback the shipped observer
// requires: dag.BuildUserPrompt stamps no Root on a userprompt node (internal/dag/builders.go),
// so the text root has to be resolved through the ToolUseRecord the node's Ref names.
func TestAdvanceReadsPromptTextThroughToolUseRecordFallback(t *testing.T) {
	f := newFx(t)
	f.prompt(1, "Original, stored observer-style.", false)
	f.prompt(11, "Later restatement, observer-style.", false)
	f.closedSeg(12, 10, 19)

	d := f.begin()
	f.advance(d, 12)

	_, cp := f.persisted()
	require.Equal(t, "Original, stored observer-style.", cp.UserIntent.Original)
	require.Equal(t, []string{"Later restatement, observer-style."}, cp.UserIntent.Evolution)
}

func TestAdvanceExcludesSupersededAndEphemeralTools(t *testing.T) {
	f := newFx(t)
	f.tool("tu_ok", 11, "Read", "src/a.ts", "current body", false)
	f.tool("tu_sup", 12, "Read", "src/b.ts", "superseded body", false)
	f.tool("tu_eph", 13, "Read", "src/c.ts", "ephemeral body", true)
	require.NoError(t, f.store.MarkSuperseded(f.ctx(), "tu_sup", "tu_ok"))
	f.closedSeg(12, 10, 19)

	d := f.begin()
	f.advance(d, 12)

	_, cp := f.persisted()
	require.Len(t, cp.Pointers.Tools, 1, "superseded and ephemeral results are excluded")
	require.Equal(t, core.ToolUseID("tu_ok"), cp.Pointers.Tools[0].ToolUseID)
	require.Equal(t, "Read src/a.ts", cp.Pointers.Tools[0].Summary)
}

func TestAdvancePointersCarryNoContent(t *testing.T) {
	f := newFx(t)
	marker := "ZQXJKV-marker-that-must-not-leak"
	big := strings.Repeat("0123456789abcdef\n", 2500) + marker + "\n"
	require.Greater(t, len(big), 40_000)
	f.tool("tu_big", 11, "Read", "src/big.txt", big, false)
	f.closedSeg(12, 10, 19)

	d := f.begin()
	f.advance(d, 12)

	b, err := os.ReadFile(paths.Long(f.draftPath()))
	require.NoError(t, err)
	require.Less(t, len(b), 4096, "a 40 KB read must reach the draft as a pointer, not as content")
	require.NotContains(t, string(b), marker)
}

func TestAdvanceIsIdempotentForSameSeq(t *testing.T) {
	f := newFx(t)
	f.tool("tu_a", 11, "Edit", "src/a.ts", "alpha body", false)
	f.closedSeg(12, 10, 19)

	d := f.begin()
	fr1 := f.advance(d, 12)
	fr2 := f.advance(d, 12)
	require.Equal(t, fr1, fr2)

	_, cp := f.persisted()
	require.Equal(t, []core.SegmentID{12}, cp.EncodedSegments, "one entry, not two")
	require.Equal(t, 1, strings.Count(cp.Narrative, "seg 12 "), "the narrative line is not duplicated")
	require.Len(t, cp.Pointers.Files, 1)
}

func TestAbortLeavesEncodedMarksIntact(t *testing.T) {
	f := newFx(t)
	f.tool("tu_a", 11, "Read", "src/a.ts", "alpha body", false)
	f.closedSeg(12, 10, 19)

	d := f.begin()
	f.advance(d, 12)
	require.NoError(t, f.w.Abort(d))

	_, err := os.Stat(paths.Long(f.draftPath()))
	require.True(t, os.IsNotExist(err), "Abort deletes the draft file")

	seg, err := f.store.Segments().Get(f.ctx(), 12)
	require.NoError(t, err)
	require.True(t, seg.EncodedOnce, "Abort never un-marks an encoded segment: the DPI guard is one-way")

	require.NoError(t, f.w.Abort(d), "Abort is idempotent")
}

// whyTurn parses the turn out of a "touched at turn %d via %s" pointer reason.
func whyTurn(t *testing.T, why string) int {
	t.Helper()
	var turn int
	var tool string
	_, err := fmt.Sscanf(why, "touched at turn %d via %s", &turn, &tool)
	require.NoError(t, err, "unparseable pointer reason: %q", why)
	return turn
}

func TestAdvanceKeepsPointersNewestFirst(t *testing.T) {
	f := newFx(t)
	type touch struct {
		seg        core.SegmentID
		start, end core.TurnIndex
		fresh      string
	}
	plan := []touch{
		{12, 10, 19, "src/c1.ts"},
		{13, 20, 29, "src/c2.ts"},
		{14, 30, 39, "src/c3.ts"},
	}
	for _, p := range plan {
		f.tool(fmt.Sprintf("tu_%d_a", p.seg), p.start+1, "Edit", "src/a.ts", "a body", false)
		f.tool(fmt.Sprintf("tu_%d_b", p.seg), p.start+2, "Edit", "src/b.ts", "b body", false)
		f.tool(fmt.Sprintf("tu_%d_c", p.seg), p.start+3, "Edit", p.fresh, "fresh body", false)
		f.closedSeg(p.seg, p.start, p.end)
	}

	d := f.begin()
	f.advance(d, 12)
	f.advance(d, 13)
	f.advance(d, 14)

	_, cp := f.persisted()
	require.Len(t, cp.Pointers.Files, 5, "a.ts and b.ts are deduped; each fresh path stays")
	require.Equal(t, "src/c3.ts", cp.Pointers.Files[0].Path, "index 0 is the newest pointer")

	turns := make([]int, len(cp.Pointers.Files))
	for i, fp := range cp.Pointers.Files {
		turns[i] = whyTurn(t, fp.Why)
	}
	for i := 1; i < len(turns); i++ {
		require.Greater(t, turns[i-1], turns[i],
			"pointers must be strictly descending by recorded turn: %v", turns)
	}

	var aTurns []int
	for _, fp := range cp.Pointers.Files {
		if fp.Path == "src/a.ts" {
			aTurns = append(aTurns, whyTurn(t, fp.Why))
		}
	}
	require.Equal(t, []int{31}, aTurns, "a.ts appears once, at its highest touch turn")
}

func TestAdvanceDerivesOpenQuestionsFromStaleEliminations(t *testing.T) {
	f := newFx(t)
	f.elim(f.sess, negknow.ScopeSession, "src/db.ts", "raise pool size", "no effect")
	s1 := f.elim(f.sess, negknow.ScopeSession, "src/auth.ts:refreshToken", "widen pool timeout", "ignored in txn mode")
	s2 := f.elim(f.sess, negknow.ScopeSession, "src/auth.ts:rotate", "cache the token", "races the refresh")
	f.tool("tu_a", 11, "Read", "src/a.ts", "alpha body", false)
	f.closedSeg(12, 10, 19)
	f.tool("tu_b", 21, "Read", "src/a.ts", "beta body", false)
	f.closedSeg(13, 20, 29)

	d := f.begin()
	d.AddOpenQuestion("does the retry loop still double-fire?")
	require.NoError(t, f.ledger.MarkStale(f.ctx(), []string{s1, s2}, []string{"docker-compose.yml"}))

	f.advance(d, 12)
	_, cp := f.persisted()
	require.Len(t, cp.OpenQuestions, 3)
	require.Equal(t, "does the retry loop still double-fire?", cp.OpenQuestions[0],
		"explicitly added questions always sort first")
	for _, q := range cp.OpenQuestions[1:] {
		require.True(t, strings.HasPrefix(q, `re-verify "`), "derived question shape: %q", q)
		require.Contains(t, q, "docker-compose.yml")
	}

	f.advance(d, 13)
	_, cp = f.persisted()
	require.Len(t, cp.OpenQuestions, 3, "a second Advance must not duplicate the derived questions")

	stale := 0
	for _, r := range cp.Eliminated {
		if r.Status == negknow.StatusStale {
			stale++
		}
	}
	require.Equal(t, 2, stale, "status flips since Begin are merged in")
}

func TestAdvanceDerivesCurrentWorkGoalFromLatestPrompt(t *testing.T) {
	f := newFx(t)
	f.prompt(11, "Refactor the session store. Keep the API.", true)
	f.prompt(13, "Check the pool wrapper? It hides timeouts.", true)
	f.prompt(15, "Fix the retry loop. Then ship.", true)
	f.closedSeg(12, 10, 19)

	d := f.begin()
	f.advance(d, 12)

	_, cp := f.persisted()
	require.Equal(t, "Fix the retry loop.", cp.CurrentWork.Goal)
	require.Equal(t, "", cp.CurrentWork.NextStep, "an empty next step is a valid checkpoint")
	require.Nil(t, cp.CurrentWork.BlockedOn)
}

func TestSetCurrentWorkSuppressesDerivation(t *testing.T) {
	f := newFx(t)
	f.prompt(11, "Refactor the session store. Keep the API.", true)
	f.prompt(15, "Fix the retry loop. Then ship.", true)
	f.closedSeg(12, 10, 19)

	d := f.begin()
	d.SetCurrentWork(checkpoint.CurrentWork{Goal: "X"})
	f.advance(d, 12)

	w, cp := f.persisted()
	require.Equal(t, "X", cp.CurrentWork.Goal, "derivation must not overwrite an explicit value")
	require.True(t, w.WorkExplicit)
}

// ── package-level functions without observers ─────────────────────────────────────────────────

// ── the Phase 3 exit criterion ────────────────────────────────────────────────────────────────

// TestAdvanceKeepsTheFrontierContiguousAcrossAGap is the frontier's whole claim, and the O1
// instruction is what makes getting it wrong expensive.
//
// store.SegmentLog.Frontier is the EndTurn of the last CONSECUTIVELY encoded segment. A frontier
// of N asserts that the checkpoint FULLY COVERS the session through turn N, and PreCompact then
// tells the summarizer, in writing, not to re-summarize anything before it. Advancing to the
// maximum EndTurn of whatever a batch happened to encode misstates that whenever a gap exists:
// here segments 1 and 3 are encoded and segment 2 is not, so turns 11-20 live in neither the
// checkpoint nor the summary — and a maximum frontier would have instructed the summarizer to
// drop them.
func TestAdvanceKeepsTheFrontierContiguousAcrossAGap(t *testing.T) {
	f := newFx(t)
	f.prompt(1, "Trace the pool exhaustion end to end.", true)
	for k := 1; k <= 3; k++ {
		start := core.TurnIndex(10*k - 9)
		f.tool(fmt.Sprintf("tu_%d", k), start+1, "Read", fmt.Sprintf("src/f%d.ts", k), "body", false)
		f.closedSeg(core.SegmentID(k), start, core.TurnIndex(10*k))
	}

	d := f.begin()
	fr := f.advance(d, 1, 3)

	require.Equal(t, core.TurnIndex(10), fr,
		"coverage ends where segment 1 ends: segment 2 is still only in the raw log")
	require.Equal(t, core.TurnIndex(10), d.Frontier())

	// The O1 instruction that spends this number is asserted in precompact_test.go, where
	// PreCompact lives: TestPreCompactNamesTheContiguousFrontierInTheSpan.
	seg3, err := f.store.Segments().Get(f.ctx(), 3)
	require.NoError(t, err)
	require.True(t, seg3.EncodedOnce,
		"segment 3 IS encoded — the frontier is held back by the gap at segment 2, not by segment 3")
}

// TestBeginGivesEveryOpenDraftItsOwnSequenceNumber pins the allocation against the LIVE drafts,
// not only against the manifest.
//
// maxSeq reads checkpoints/MANIFEST.jsonl, and only Finalize appends to it — so between one
// Finalize and the next, every Begin sees the same highest sequence. The daemon Begins a draft for
// every live session on every idle tick, so all of them would take the same number; the first to
// finalize claims it, and each of the others is then set aside as .stale.json on its next Begin,
// losing its accumulated state while its segments stay flagged encoded-once and unreachable.
func TestBeginGivesEveryOpenDraftItsOwnSequenceNumber(t *testing.T) {
	f := newFx(t)
	sessions := []core.SessionID{"sess_alpha", "sess_bravo", "sess_charlie"}

	seen := map[core.CheckpointSeq]core.SessionID{}
	for _, s := range sessions {
		d, err := f.w.Begin(f.ctx(), s, 0, f.src)
		require.NoError(t, err)
		prev, dup := seen[d.Seq()]
		require.False(t, dup, "sessions %s and %s were both handed sequence %d", prev, s, int(d.Seq()))
		seen[d.Seq()] = s
	}
	require.Len(t, seen, len(sessions))
}

// TestBeginIsSerializedPerSession pins Begin against its own check-then-act.
//
// Begin reads w.drafts under w.mu, RELEASES it, and then does a file read, a manifest read and
// four source reads before re-taking the lock to publish. Two concurrent calls for one session
// both observe no live draft in that window, both build one, and both publish to the same map key
// and the same file path — so the loser is displaced from the registry while still holding the
// path, and retireDraft, which is keyed on the draft's identity, declines to clean up after it.
//
// This is a race probe rather than a deterministic assertion: the window it opens is wide (real
// file I/O), so every caller observing the same draft is strong evidence the gate holds.
func TestBeginIsSerializedPerSession(t *testing.T) {
	f := newFx(t)
	const callers = 24

	var wg sync.WaitGroup
	drafts := make([]*checkpoint.Draft, callers)
	errs := make([]error, callers)
	release := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-release
			drafts[i], errs[i] = f.w.Begin(f.ctx(), f.sess, 0, f.src)
		}()
	}
	close(release)
	wg.Wait()

	for i := range callers {
		require.NoError(t, errs[i])
		require.Same(t, drafts[0], drafts[i],
			"caller %d got a second draft for one session; both would write the same file", i)
	}
	require.Equal(t, []core.SessionID{f.sess}, f.w.OpenDrafts())
}

// TestBeginBeginsUnchainedWhenTheDerivedParentCannotBeRead separates the two kinds of parent.
//
// An explicit parent is a claim the caller is making about the chain, so a parent it cannot read
// is fatal. A DERIVED parent is Begin's own guess, and a project whose newest artifact has been
// deleted must still be able to checkpoint — the guess is dropped rather than recorded, because a
// Parent naming an artifact that cannot be read would make Reader.Chain refuse the whole lineage.
func TestBeginBeginsUnchainedWhenTheDerivedParentCannotBeRead(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Start the investigation.", true)
	// A manifest line with no artifact behind it: maxSeq names 4, and nothing can read it.
	require.NoError(t, paths.AppendManifest(paths.Of(f.p.Root),
		paths.ManifestEntry{Seq: 4, SHA256: "0", Bytes: 1, Created: 1}))

	d, err := f.w.Begin(f.ctx(), f.sess, 0, f.src)
	require.NoError(t, err, "an unreadable derived parent must not stop the session checkpointing")
	require.Greater(t, int(d.Seq()), 4)

	wire, cp := f.persisted()
	require.Equal(t, core.CheckpointSeq(0), wire.Parent)
	require.Empty(t, cp.Parent, "a link that cannot be resolved is not recorded")
	require.Equal(t, "Start the investigation.", cp.UserIntent.Original,
		"the earliest-prompt branch runs instead")

	// An EXPLICIT parent naming the same unreadable artifact is still fatal.
	_, err = f.w.Begin(f.ctx(), "sess_explicit_parent", 4, f.src)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parent 4")
}

// TestBeginRejectsASessionIDThatIsNotASafeFilenameComponent covers the only place in internal/
// that puts a session id into a path.
//
// core.SessionID is decoded verbatim from the hook payload and validated nowhere in the tree.
// filepath.Join CLEANS its result, so a traversing id does not produce a rejected path — it
// produces a real one OUTSIDE .qompack, where paths.WriteAtomic's IsProtected guard never fires,
// and retireDraft then unlinks it and setAsideStaleDraft renames it.
func TestBeginRejectsASessionIDThatIsNotASafeFilenameComponent(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   core.SessionID
	}{
		{"empty", ""},
		{"dot", "."},
		{"dotdot", ".."},
		{"traversal", "../../../../evil"},
		{"forward_slash", "sub/sess"},
		{"back_slash", `sub\sess`},
		{"drive_colon", "C:sess"},
		{"nul_byte", "sess\x00trailer"},
		{"newline", "sess\nid"},
		{"too_long", core.SessionID(strings.Repeat("s", 65))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFx(t)
			_, err := f.w.Begin(f.ctx(), tc.id, 0, f.src)
			require.Error(t, err, "a session id that is not a safe path component must be refused")
			require.Contains(t, err.Error(), "safe filename component")
			require.Empty(t, f.w.OpenDrafts())
		})
	}
}

// TestBeginWritesNoDraftOutsideTheQompackDirectory is the traversal case stated as the file system
// sees it: "../../../../evil" cleans to <root>/evil.json, which is outside .qompack entirely.
func TestBeginWritesNoDraftOutsideTheQompackDirectory(t *testing.T) {
	f := newFx(t)
	escaped := filepath.Join(f.p.Root, "evil.json")

	_, err := f.w.Begin(f.ctx(), "../../../../evil", 0, f.src)
	require.Error(t, err)
	require.NoFileExists(t, paths.Long(escaped),
		"a hostile session id must not turn the draft lifecycle into an arbitrary-path write")
}

// TestAdvanceRoutesToolSummariesThroughFromStore closes the one store read that bypassed the
// regeneration gate.
//
// ArgsPreview is raw tool-argument text the store captured verbatim, and fromStore's doc comment
// claims that every string entering a checkpoint from the store passes through it. Any tool handed
// a slice of the transcript can carry a previous injection's tags in its arguments, and
// re-encoding one is the compress-a-compression §4.6 forbids.
func TestAdvanceRoutesToolSummariesThroughFromStore(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Summarize what the retry loop does.", true)

	preview := "Read src/a.ts " + checkpoint.OpenTag(3) + "stale digest body" + checkpoint.InjectionCloseTag
	root := f.put("file body", "Read", "src/a.ts", false)
	require.NoError(t, f.store.RecordToolUse(f.ctx(), store.ToolUseRecord{
		ID: "toolu_injected_args", Session: f.sess, Turn: 1, TS: f.now(), Tool: "Read",
		ArgsPreview: preview, Root: root, Path: "src/a.ts",
	}))
	require.NoError(t, dag.BuildToolUse(f.graph, dag.ObservedTool{
		ToolUseID: "toolu_injected_args", Turn: 1, TS: f.now(), Pos: f.nextPos(),
		Tool: "Read", PathKey: "src/a.ts", Root: root,
	}))
	f.closedSeg(1, 0, 3)

	d := f.begin()
	f.advance(d, 1)

	_, cp := f.persisted()
	require.Len(t, cp.Pointers.Tools, 1)
	summary := cp.Pointers.Tools[0].Summary
	require.NotContains(t, summary, "stale digest body",
		"an injected body must never re-enter a checkpoint through a tool pointer's summary")
	require.NotContains(t, summary, "qompack:injected")
	require.Contains(t, summary, "Read src/a.ts", "the surrounding text is kept verbatim")
}

// ── the SP-01 conformance suite, against the real writer ──────────────────────────────────────

// ── performance ───────────────────────────────────────────────────────────────────────────────

// BenchmarkAdvanceSegment prices one Advance over a real segment holding 40 tool uses across 12
// files, against the plan's < 25 ms budget. All fixture construction happens off-clock; each
// timed iteration encodes one fresh, closed, unencoded segment into the same live draft, which
// is exactly the idle-window cadence O5 describes.
func BenchmarkAdvanceSegment(b *testing.B) {
	ctx := context.Background()
	root := b.TempDir()
	cfg := config.Defaults()
	l := paths.Of(root)
	require.NoError(b, paths.EnsureLayout(l))
	clk := testutil.NewFakeClock(testutil.Epoch)
	nop := logging.Nop()

	st, err := store.Open(root, cfg, store.Deps{Log: nop, Clock: clk})
	require.NoError(b, err)
	defer func() { _ = st.Close() }()
	g, err := dag.Open(root, cfg, nop)
	require.NoError(b, err)
	led, err := negknow.Open(root, cfg, nil, negknow.Deps{Session: writerSession, Clock: clk, Log: nop})
	require.NoError(b, err)
	defer func() { _ = led.Close() }()

	src := checkpoint.SourceSet{
		Store: st, Segments: st.Segments(), Ledger: led, Pins: &fakePins{},
		Graph: g, Grammar: grammar.New(),
		Tokens: tokens.New(cfg, filepath.Join(l.Tmp, "calib.json")),
	}
	w, err := checkpoint.OpenWriter(root, cfg, nop, obs.New(clk), clk)
	require.NoError(b, err)
	d, err := w.Begin(ctx, writerSession, 0, src)
	require.NoError(b, err)

	pos := 0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		segID := core.SegmentID(i + 1)
		start := core.TurnIndex(i*10 + 1)
		end := start + 9
		for j := 0; j < 40; j++ {
			pathKey := fmt.Sprintf("src/f%02d.ts", j%12)
			id := core.ToolUseID(fmt.Sprintf("tu_%d_%d", i, j))
			turn := start + core.TurnIndex(j%9)
			body := fmt.Sprintf("body %d %d for %s", i, j, pathKey)
			res, perr := st.PutBytes(ctx, []byte(body), store.PutOptions{Tool: "Edit", Path: pathKey})
			require.NoError(b, perr)
			require.NoError(b, st.RecordToolUse(ctx, store.ToolUseRecord{
				ID: id, Session: writerSession, Turn: turn, TS: core.NowMilli(clk), Tool: "Edit",
				ArgsPreview: "Edit " + pathKey, Root: res.Root.Hash, Path: pathKey,
			}))
			pos += 64
			require.NoError(b, dag.BuildToolUse(g, dag.ObservedTool{
				ToolUseID: id, Turn: turn, TS: core.NowMilli(clk), Pos: pos,
				Tool: "Edit", PathKey: pathKey, Writes: true, Root: res.Root.Hash,
			}))
			require.NoError(b, st.AppendFileVersion(ctx, pathKey, store.FileVersion{
				TS: core.NowMilli(clk), Root: res.Root.Hash, Turn: turn, Bytes: int64(len(body)),
			}))
			clk.Advance(time.Millisecond)
		}
		_, serr := st.Segments().Open(ctx, store.Segment{ID: segID, Session: writerSession, StartTurn: start})
		require.NoError(b, serr)
		require.NoError(b, st.Segments().Close(ctx, segID, end, map[string]float64{"tokens": 400}))
		// Drain the fixture's own write-back off the clock: the timed region prices Advance's
		// scan, lookups, MarkEncoded and draft persist — not the OS flush queue the 40 setup
		// puts just filled, which Windows would otherwise bill to the first fsync inside it.
		require.NoError(b, st.Flush(ctx))
		require.NoError(b, g.Flush(ctx))
		b.StartTimer()

		if _, err := w.Advance(ctx, d, []core.SegmentID{segID}); err != nil {
			b.Fatal(err)
		}
	}
}
