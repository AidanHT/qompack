package daemon

// The brief route's body (V7 plan §5.3 steps 0-4 and its degraded modes): serveBrief settles the
// session, closes its open segment for the triggers that close one, arms the checkpoint writer's
// sources, snapshots the draft and renders the served instructions. The IPC op and the CLI verb
// that call it are a later task's; these rows call it directly.
//
// briefRig wires a real daemon (wireTestDaemon), a real checkpoint writer bound through
// BindCheckpoint, and a real scheduler runtime over the daemon's own store, so the close, the
// catch-up and the PreCompact that follows a brief meet the same segment log.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/modwire"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
)

const briefSession = core.SessionID("sess-brief-route")

// briefPins is a settable pins.Store: what a brief re-reads is whatever the row last set.
type briefPins struct{ invs []pins.Invariant }

func (p *briefPins) Add(context.Context, pins.Invariant) error { return nil }
func (p *briefPins) Remove(context.Context, string) error      { return nil }
func (p *briefPins) All(context.Context) ([]pins.Invariant, error) {
	return append([]pins.Invariant(nil), p.invs...), nil
}
func (p *briefPins) Materialize(context.Context) error { return nil }

type briefRig struct {
	t    *testing.T
	dd   *daemon
	o    *Options
	root string
	w    *checkpoint.FileWriter
	rt   *schedRuntime
	pins *briefPins
	open core.SegmentID
	pos  int
}

// newBriefRig builds the rig with the session bound to the runtime and its first segment open in
// the daemon's store, nothing observed yet and no draft.
func newBriefRig(t *testing.T) *briefRig {
	t.Helper()
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	led := o.OpenLedger()
	require.NotNil(t, led)
	// The ledger holds an append handle on records/eliminations.jsonl, which Windows refuses to
	// unlink while it is open; closing it twice is harmless.
	t.Cleanup(func() { _ = led.Close() })

	w, err := checkpoint.OpenWriter(root, o.Cfg, logging.Nop(), dd.m, core.SystemClock())
	require.NoError(t, err)
	bp := &briefPins{}
	src := checkpoint.SourceSet{
		Store: o.Store, Segments: o.Store.Segments(), Ledger: led, Pins: bp,
		Graph: o.Graph, Grammar: grammar.New(), Tokens: tokens.New(o.Cfg, ""),
	}
	require.NoError(t, src.Validate())
	bo := &Options{Log: logging.Nop()}
	BindCheckpoint(bo, o.Cfg, w, src)
	for _, bind := range bo.binds {
		bind(dd.svc)
	}
	require.NotNil(t, dd.svc.BriefSnapshot, "BindCheckpoint binds the brief's snapshot seam")

	rt, err := NewSchedulerRuntime(SchedulerRuntimeOptions{
		ProjectRoot: root, Cfg: o.Cfg, Clock: core.SystemClock(), Log: logging.Nop(), Metrics: dd.m,
		Store: o.Store, Graph: o.Graph,
	})
	require.NoError(t, err)
	t.Cleanup(scheduler.DisablePSelection)
	dd.svc.Sched = rt
	WrapServicesForScheduler(dd.svc, rt, SchedulerRuntimeOptions{Log: logging.Nop(), Metrics: dd.m})
	concrete, ok := rt.(*schedRuntime)
	require.True(t, ok)
	concrete.BindSession(briefSession, nil)

	open, err := o.Store.Segments().Open(context.Background(), store.Segment{Session: briefSession, StartTurn: 0})
	require.NoError(t, err)
	return &briefRig{t: t, dd: dd, o: o, root: root, w: w, rt: concrete, pins: bp, open: open}
}

func (r *briefRig) ctx() context.Context { return context.Background() }

func (r *briefRig) nextPos() int { r.pos += 64; return r.pos }

// observed has the runtime hold tok tokens observed up to turn in the open segment, as the tap's
// applied tool uses leave it.
func (r *briefRig) observed(turn core.TurnIndex, tok core.Tokens) {
	r.rt.mu.Lock()
	defer r.rt.mu.Unlock()
	r.rt.maxTurn = max(r.rt.maxTurn, turn)
	r.rt.openSegTokens += tok
}

// prompt records one verbatim user prompt of the session at turn, as the observer does.
func (r *briefRig) prompt(turn core.TurnIndex, text string) {
	r.t.Helper()
	res, err := r.o.Store.PutBytes(r.ctx(), []byte(text), store.PutOptions{
		Tool: "UserPromptSubmit", Canon: canon.Options{Strip: []canon.Class{}},
	})
	require.NoError(r.t, err)
	id := core.ToolUseID(fmt.Sprintf("prompt_%s_%d", briefSession, int(turn)))
	require.NoError(r.t, r.o.Store.RecordToolUse(r.ctx(), store.ToolUseRecord{
		ID: id, Session: briefSession, Turn: turn, TS: core.NowMilli(r.dd.clk), Tool: "UserPromptSubmit",
		Root: res.Root.Hash,
	}))
	require.NoError(r.t, r.o.Graph.AddNode(dag.Node{
		ID: dag.UserPromptNode(turn), Kind: dag.KindUserPrompt, Turn: turn, Pos: r.nextPos(), Ref: string(id),
	}))
}

// explains records an assistant turn that explains a change to path: an encoded segment holding
// turn mints a dec_ decision from it.
func (r *briefRig) explains(turn core.TurnIndex, path, text string) {
	r.t.Helper()
	res, err := r.o.Store.PutBytes(r.ctx(), []byte(text), store.PutOptions{})
	require.NoError(r.t, err)
	a, f := dag.AssistantNode(turn), dag.FileNode(path)
	require.NoError(r.t, r.o.Graph.AddNode(dag.Node{ID: a, Kind: dag.KindAssistant, Turn: turn, Root: res.Root.Hash, Pos: r.nextPos()}))
	require.NoError(r.t, r.o.Graph.AddNode(dag.Node{ID: f, Kind: dag.KindFile, Turn: turn, Ref: path, Pos: r.nextPos()}))
	require.NoError(r.t, r.o.Graph.AddEdge(dag.Edge{From: a, To: f, Kind: dag.EdgeExplains, Weight: 1, Turn: turn}))
}

// call is a brief for trigger with a generous deadline.
func (r *briefRig) call(trigger string) briefCall {
	return briefCall{
		Req: modwire.BriefRequest{
			V: modwire.Version, SessionID: string(briefSession), CWD: r.root, HostVersion: modwire.MinHostVersion,
			Trigger: trigger,
		},
		Deadline: time.Now().Add(liveOrderBound),
	}
}

func (r *briefRig) serve(trigger string) (modwire.BriefResponse, briefOutcome) {
	r.t.Helper()
	resp, out := r.dd.serveBrief(r.ctx(), r.call(trigger))
	require.NoError(r.t, resp.Validate(modwire.BriefMaxChars), "the route's answer obeys the wire contract")
	return resp, out
}

func (r *briefRig) segment() store.Segment {
	r.t.Helper()
	seg, err := r.o.Store.Segments().Get(r.ctx(), r.open)
	require.NoError(r.t, err)
	return seg
}

func (r *briefRig) counter(name string) int64 { return r.dd.m.Counter(name).Value() }

// compactionCounters is every counter only a compaction may move.
func (r *briefRig) compactionCounters() map[string]int64 {
	return map[string]int64{
		counterSegmentClosedPrefix + causeCompact: r.counter(counterSegmentClosedPrefix + causeCompact),
		counterTapCompactUnobserved:               r.counter(counterTapCompactUnobserved),
		counterTapCompactForeign:                  r.counter(counterTapCompactForeign),
	}
}

// draftEncoded reads the live draft file's encoded segments.
func (r *briefRig) draftEncoded() []core.SegmentID {
	r.t.Helper()
	return draftEncodedAt(r.t, r.root, briefSession)
}

// draftEncodedAt reads the encoded segments of sess's draft file under root, the key the draft
// wrapper spells for them (checkpoint draftFile).
func draftEncodedAt(t *testing.T, root string, sess core.SessionID) []core.SegmentID {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).State, "draft-"+string(sess)+".json")))
	require.NoError(t, err)
	var df struct {
		Encoded []core.SegmentID `json:"encoded"`
	}
	require.NoError(t, json.Unmarshal(b, &df))
	return df.Encoded
}

// sealed lists the checkpoints the project's manifest indexes.
func (r *briefRig) sealed() []paths.ManifestEntry {
	r.t.Helper()
	entries, err := paths.ReadManifest(paths.Of(r.root))
	require.NoError(r.t, err)
	return entries
}

// lineFor returns the served instructions' line for id, and whether there is one.
func lineFor(instructions, id string) (string, bool) {
	for _, l := range strings.Split(instructions, "\n") {
		if strings.HasPrefix(l, "- ["+id+"] ") {
			return l, true
		}
	}
	return "", false
}

// TestServeBrief_ABriefBeforeTheFirstCompactionCarriesTheOpenSegment: no draft exists yet; the
// session's open segment holds an explained change. A manual brief closes that segment, encodes it
// into a draft it opens cold, and serves its decision beside the pins and the original intent,
// with the marker first and every item under its id. Only the brief's own close is counted.
func TestServeBrief_ABriefBeforeTheFirstCompactionCarriesTheOpenSegment(t *testing.T) {
	r := newBriefRig(t)
	r.pins.invs = []pins.Invariant{{ID: "inv_0123456789ab", Text: "Never log access tokens.", Source: "user"}}
	r.prompt(0, "Fix the intermittent 500s on POST /api/session/refresh.")
	r.explains(4, "src/auth.ts", "Switched to a transaction-scoped pool. pgbouncer ignores pool_timeout.")
	r.observed(4, 900)
	require.Nil(t, r.w.DraftFor(briefSession), "fixture sanity: no draft yet")

	resp, out := r.serve(modwire.TriggerManual)

	require.True(t, resp.OK)
	require.Equal(t, modwire.OutcomeServed, out.Outcome)
	require.True(t, out.Closed, "the open segment was closed for the brief")
	require.True(t, strings.HasPrefix(resp.Instructions, modwire.BriefMarker+"\n"), "the marker, then a newline")
	_, ok := lineFor(resp.Instructions, "inv_0123456789ab")
	require.True(t, ok, "the pin is served")
	orig, ok := lineFor(resp.Instructions, checkpoint.BriefIDIntentOriginal)
	require.True(t, ok, "the original intent is served")
	require.Contains(t, orig, "Fix the intermittent 500s on POST /api/session/refresh.")
	var dec string
	for _, id := range resp.FactIDs {
		if strings.HasPrefix(id, "dec_") {
			dec = id
		}
	}
	require.NotEmpty(t, dec, "the open segment's decision is served")
	line, _ := lineFor(resp.Instructions, dec)
	require.Contains(t, line, "src/auth.ts")
	require.Equal(t, len(resp.FactIDs), out.Items)

	seg := r.segment()
	require.True(t, seg.Closed, "the brief closed the open segment")
	require.Equal(t, int64(1), r.counter(counterSegmentClosedPrefix+causeBrief))
	require.Equal(t, map[string]int64{
		counterSegmentClosedPrefix + causeCompact: 0, counterTapCompactUnobserved: 0, counterTapCompactForeign: 0,
	}, r.compactionCounters(), "no compaction counter moves on a brief's close")
}

// TestServeBrief_AVetoedCompactionLeavesOneBoundaryInTheLiveDraftAndNoCheckpoint is third audit
// A13: the brief is not read-only. When the host then skips the compaction, what is left is one
// closed segment, encoded into the live draft, and no sealed checkpoint; the next seal encodes it.
func TestServeBrief_AVetoedCompactionLeavesOneBoundaryInTheLiveDraftAndNoCheckpoint(t *testing.T) {
	r := newBriefRig(t)
	r.prompt(0, "Fix the intermittent 500s.")
	r.observed(3, 400)

	resp, _ := r.serve(modwire.TriggerAuto)
	require.True(t, resp.OK)

	require.Equal(t, int64(1), r.counter(counterSegmentClosedPrefix+causeBrief), "one boundary")
	require.Equal(t, []core.SegmentID{r.open}, r.draftEncoded(), "encoded into the live draft")
	require.Empty(t, r.sealed(), "no checkpoint is sealed")
}

// TestServeBrief_ThePreCompactAfterABriefSealsTheBriefsSegmentWithoutClosingAgain: the compaction
// the brief was for runs the daemon's own PreCompact route. Its tap close finds the segment the
// brief rolled open empty, and is a no-op that is neither a compact close nor unobserved; the seal
// encodes the segment the brief closed.
func TestServeBrief_ThePreCompactAfterABriefSealsTheBriefsSegmentWithoutClosingAgain(t *testing.T) {
	r := newBriefRig(t)
	r.prompt(0, "Fix the intermittent 500s.")
	r.observed(3, 400)
	resp, _ := r.serve(modwire.TriggerManual)
	require.True(t, resp.OK)
	succ, err := r.o.Store.Segments().Current(r.ctx(), briefSession)
	require.NoError(t, err)

	require.True(t, r.dd.dispatchOp(r.ctx(), checkpointRequest(r.dd, briefSession, "nonce-brief-precompact")).OK)

	require.Equal(t, map[string]int64{
		counterSegmentClosedPrefix + causeCompact: 0, counterTapCompactUnobserved: 0, counterTapCompactForeign: 0,
	}, r.compactionCounters())
	require.Equal(t, int64(1), r.counter(counterTapCompactAfterBrief))
	again, err := r.o.Store.Segments().Get(r.ctx(), succ.ID)
	require.NoError(t, err)
	require.False(t, again.Closed, "PreCompact's own close was a no-op")
	entries := r.sealed()
	require.Len(t, entries, 1, "the compaction sealed its checkpoint")
	raw, err := os.ReadFile(paths.Long(paths.CheckpointPath(paths.Of(r.root), entries[0].Seq)))
	require.NoError(t, err)
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)
	require.Equal(t, []core.SegmentID{r.open}, cp.EncodedSegments, "the seal encodes the segment the brief closed")
}

// TestServeBrief_PrecomputeLeavesTheOpenSegmentOpen: the conversation goes on after a precompute,
// so its brief closes nothing; the open segment's prompts are still read fresh.
func TestServeBrief_PrecomputeLeavesTheOpenSegmentOpen(t *testing.T) {
	r := newBriefRig(t)
	r.prompt(0, "Fix the intermittent 500s.")
	r.prompt(2, "Also cover the retry path.")
	r.observed(2, 400)

	resp, out := r.serve(modwire.TriggerPrecompute)

	require.True(t, resp.OK)
	require.False(t, out.Closed)
	require.False(t, r.segment().Closed, "precompute leaves the open segment open")
	require.Zero(t, r.counter(counterSegmentClosedPrefix+causeBrief))
	evo, ok := lineFor(resp.Instructions, checkpoint.BriefIDIntentEvolutionPrefix+"1")
	require.True(t, ok, "the open segment's prompt is still briefed")
	require.Contains(t, evo, "Also cover the retry path.")
}

// TestServeBrief_WhenTheModeMayNotActItServesEmptyAndClosesNothing is third audit A12: in
// degraded-passive (or off) the route acts on nothing, as PreCompact seals nothing.
func TestServeBrief_WhenTheModeMayNotActItServesEmptyAndClosesNothing(t *testing.T) {
	r := newBriefRig(t)
	r.prompt(0, "Fix the intermittent 500s.")
	r.observed(3, 400)
	r.dd.monitor.Degrade("forced for test", []contract.Result{
		{ID: "x.forced", OK: false, Severity: contract.SevCritical, Expected: "e", Observed: "o"},
	})

	resp, out := r.serve(modwire.TriggerManual)

	require.True(t, resp.OK, "served")
	require.Empty(t, resp.Instructions, "empty")
	require.Empty(t, resp.FactIDs)
	require.Equal(t, briefEmptyMode, out.Empty)
	require.False(t, r.segment().Closed, "nothing is closed")
	require.Zero(t, r.counter(counterSegmentClosedPrefix+causeBrief))
	require.Nil(t, r.w.DraftFor(briefSession), "no draft is opened")
}

// TestServeBrief_AStoredItemWithAnInvalidIDIsSkippedAndCounted is R22: an id outside modwire's
// alphabet would make the whole response invalid; the item is left out and counted, and the rest of
// the brief is served.
func TestServeBrief_AStoredItemWithAnInvalidIDIsSkippedAndCounted(t *testing.T) {
	r := newBriefRig(t)
	r.pins.invs = []pins.Invariant{
		{ID: "inv bad<id>", Text: "Pinned under an id the wire cannot carry.", Source: "user"},
		{ID: "inv_0123456789ab", Text: "Never log access tokens.", Source: "user"},
	}
	r.prompt(0, "Fix the intermittent 500s.")

	resp, out := r.serve(modwire.TriggerPrecompute)

	require.True(t, resp.OK)
	require.NotEmpty(t, resp.Instructions, "the brief is still served")
	require.NotContains(t, resp.Instructions, "Pinned under an id the wire cannot carry.")
	_, ok := lineFor(resp.Instructions, "inv_0123456789ab")
	require.True(t, ok)
	require.Equal(t, 1, out.InvalidIDs)
	require.Equal(t, int64(1), r.counter(counterBriefInvalidID))
}

// TestServeBrief_ADeadlineAlreadyPastAnswersTimeoutAndDoesNothing: a brief whose budget is gone on
// arrival is not served, and touches neither the segment nor the draft.
func TestServeBrief_ADeadlineAlreadyPastAnswersTimeoutAndDoesNothing(t *testing.T) {
	r := newBriefRig(t)
	r.observed(3, 400)
	c := r.call(modwire.TriggerManual)
	c.Deadline = time.Now().Add(-time.Millisecond)

	resp, out := r.dd.serveBrief(r.ctx(), c)

	require.False(t, resp.OK)
	require.Equal(t, modwire.ReasonTimeout, resp.Reason)
	require.NoError(t, resp.Validate(modwire.BriefMaxChars))
	require.Equal(t, modwire.OutcomeTimeout, out.Outcome)
	require.False(t, r.segment().Closed)
	require.Nil(t, r.w.DraftFor(briefSession))
}

// TestServeBrief_ASnapshotFailureServesEmpty: when the draft cannot be opened (Begin fails, for
// example because the sources are not armed) the mod proceeds with the user's instructions only.
func TestServeBrief_ASnapshotFailureServesEmpty(t *testing.T) {
	r := newBriefRig(t)
	r.dd.svc.BriefSnapshot = func(context.Context, core.SessionID) (checkpoint.Brief, error) {
		return checkpoint.Brief{}, fmt.Errorf("checkpoint: SourceSet.Store is nil")
	}

	resp, out := r.serve(modwire.TriggerPrecompute)

	require.True(t, resp.OK)
	require.Empty(t, resp.Instructions)
	require.Equal(t, briefEmptySnapshot, out.Empty)
	require.Equal(t, int64(1), r.counter(counterBriefServedEmptyPrefix+briefEmptySnapshot))
}

// briefWithShortDeadline is a precompute brief whose deadline passes while its snapshot runs.
func (r *briefRig) briefWithShortDeadline() briefCall {
	c := r.call(modwire.TriggerPrecompute)
	c.Deadline = time.Now().Add(100 * time.Millisecond)
	return c
}

// TestServeBrief_ASnapshotThatAnswersAfterTheDeadlineIsATimeout is review Minor 2: a snapshot cut
// short by the deadline answers what it had, which is not a full brief. Once the deadline has passed
// the route answers timeout, not served.
func TestServeBrief_ASnapshotThatAnswersAfterTheDeadlineIsATimeout(t *testing.T) {
	r := newBriefRig(t)
	r.dd.svc.BriefSnapshot = func(ctx context.Context, _ core.SessionID) (checkpoint.Brief, error) {
		<-ctx.Done()
		return checkpoint.Brief{Items: []checkpoint.BriefItem{
			{Kind: checkpoint.BriefInvariant, ID: "inv_1", Text: "Never log access tokens."},
		}}, nil
	}

	resp, out := r.dd.serveBrief(r.ctx(), r.briefWithShortDeadline())

	require.False(t, resp.OK)
	require.Equal(t, modwire.ReasonTimeout, resp.Reason)
	require.Empty(t, resp.Instructions)
	require.NoError(t, resp.Validate(modwire.BriefMaxChars))
	require.Equal(t, modwire.OutcomeTimeout, out.Outcome)
	require.Zero(t, r.counter(counterBriefServed))
}

// TestServeBrief_ASnapshotThatFailsOnTheDeadlineIsATimeout is review Minor 3 at the route: a
// snapshot that reports its context's end is a timeout, not a served-empty brief.
func TestServeBrief_ASnapshotThatFailsOnTheDeadlineIsATimeout(t *testing.T) {
	r := newBriefRig(t)
	r.dd.svc.BriefSnapshot = func(ctx context.Context, _ core.SessionID) (checkpoint.Brief, error) {
		<-ctx.Done()
		return checkpoint.Brief{}, ctx.Err()
	}

	resp, out := r.dd.serveBrief(r.ctx(), r.briefWithShortDeadline())

	require.False(t, resp.OK)
	require.Equal(t, modwire.ReasonTimeout, resp.Reason)
	require.Contains(t, resp.Detail, "before the snapshot answered")
	require.Equal(t, modwire.OutcomeTimeout, out.Outcome)
	require.Zero(t, r.counter(counterBriefServedEmptyPrefix+briefEmptySnapshot))
}

// TestServeBrief_WithNoCheckpointerItServesEmpty: a daemon with no checkpoint layer bound has
// nothing to brief from.
func TestServeBrief_WithNoCheckpointerItServesEmpty(t *testing.T) {
	r := newBriefRig(t)
	r.dd.svc.BriefSnapshot = nil

	resp, out := r.serve(modwire.TriggerPrecompute)

	require.True(t, resp.OK)
	require.Empty(t, resp.Instructions)
	require.Equal(t, briefEmptyUnwired, out.Empty)
}

// TestServeBrief_ACaptureStillBeingAppliedIsSettledBeforeTheSnapshot is third audit A4: a Read the
// daemon accepted before the brief is still publishing in the session's lane when the brief
// arrives, and its hook spooled a copy. The brief waits for it, inside its own bound, before it
// snapshots, so the snapshot reads a store that holds it.
func TestServeBrief_ACaptureStillBeingAppliedIsSettledBeforeTheSnapshot(t *testing.T) {
	dd, root := settleTestDaemon(t, liveOrderBound)
	dd.briefSettleCap = liveOrderBound // the row asserts what the settle waits for, not how long
	dd.drain.Store(newDrainer(contentDrainConfig(dd)))
	const sess core.SessionID = "sess-brief-leased"
	tool := liveOrderTool(dd, root, sess, 1)
	run, open, held := settleGate(dd, tool.Nonce)
	liveOrderWorkers(t, dd, 2, run)
	t.Cleanup(open)

	acceptPrompt(t, dd, tool)
	require.True(t, heldWithin(held, liveOrderBound), "fixture sanity: the lane is publishing the Read")
	writeHookSpool(t, root, "client-7171.ndjson", tool) // the copy a late ACK leaves
	var snapshots int
	var published bool
	dd.svc.BriefSnapshot = func(context.Context, core.SessionID) (checkpoint.Brief, error) {
		snapshots++
		published = spoolWatchPublished(dd, tool.Nonce)
		return checkpoint.Brief{}, nil
	}

	go func() {
		liveOrderPollUntil(liveOrderBound, func() bool { return dd.m.Counter(counterBriefSettle).Value() > 0 })
		open() // the slow publication finishes while the settle waits
	}()
	resp, _ := dd.serveBrief(context.Background(), briefCall{
		Req: modwire.BriefRequest{
			V: modwire.Version, SessionID: string(sess), CWD: root, HostVersion: modwire.MinHostVersion,
			Trigger: modwire.TriggerPrecompute,
		},
		At:       tool.TS + 1,
		Deadline: time.Now().Add(liveOrderBound),
	})

	require.True(t, resp.OK)
	require.Equal(t, 1, snapshots)
	require.True(t, published, "the brief's settle waited for the leased Read before the snapshot")
	require.Zero(t, dd.m.Counter(counterPrecompactSettle).Value(), "the brief counts its settle under its own name")
}

// TestBriefSettleBound_IsAtMostThreeHundredMillisecondsAndHalfTheBudget: the settle is bounded at
// 300 ms and inside the daemon's deadline, so the snapshot keeps at least half of what is left.
func TestBriefSettleBound_IsAtMostThreeHundredMillisecondsAndHalfTheBudget(t *testing.T) {
	dd := &daemon{}
	now := time.Now()
	require.Equal(t, 300*time.Millisecond, dd.briefSettleBound(now, now.Add(10*time.Second)))
	require.Equal(t, 200*time.Millisecond, dd.briefSettleBound(now, now.Add(400*time.Millisecond)))
	require.Zero(t, dd.briefSettleBound(now, now.Add(-time.Millisecond)))
	require.Equal(t, 300*time.Millisecond, dd.briefSettleBound(now, time.Time{}), "no deadline: the cap alone")
}

// TestRenderBrief_MarkerThenOneLinePerItem: the instructions are the marker, a newline, and one
// "- [id] text" line per item, in the snapshot's order; the fact ids list exactly the items served.
func TestRenderBrief_MarkerThenOneLinePerItem(t *testing.T) {
	items := []checkpoint.BriefItem{
		{Kind: checkpoint.BriefInvariant, ID: "inv_1", Text: "Never log access tokens."},
		{Kind: checkpoint.BriefIntentOriginal, ID: checkpoint.BriefIDIntentOriginal, Text: "Fix the 500s."},
		{Kind: checkpoint.BriefCurrentWork, ID: checkpoint.BriefIDCurrentWork, Text: "goal: Fix the 500s."},
	}
	got := renderBrief(items, modwire.BriefMaxChars)
	require.Equal(t, modwire.BriefMarker+"\n"+
		"- [inv_1] Never log access tokens.\n"+
		"- [intent:original] Fix the 500s.\n"+
		"- [work:current] goal: Fix the 500s.", got.instructions)
	require.Equal(t, []string{"inv_1", "intent:original", "work:current"}, got.ids)
	require.Zero(t, got.invalid)
	require.Zero(t, got.over)
}

// TestRenderBrief_KeepsTheCapAndDropsTheLeastImportantFirst: under the cap, items are chosen by
// importance (pins, the original intent, current work, the newest restatements, eliminations,
// decisions), whole or not at all, and rendered in the snapshot's order.
func TestRenderBrief_KeepsTheCapAndDropsTheLeastImportantFirst(t *testing.T) {
	long := strings.Repeat("x", 60)
	items := []checkpoint.BriefItem{
		{Kind: checkpoint.BriefInvariant, ID: "inv_1", Text: "pin " + long},
		{Kind: checkpoint.BriefIntentOriginal, ID: checkpoint.BriefIDIntentOriginal, Text: "intent " + long},
		{Kind: checkpoint.BriefIntentEvolution, ID: "intent:evolution:1", Text: "old " + long},
		{Kind: checkpoint.BriefIntentEvolution, ID: "intent:evolution:2", Text: "new " + long},
		{Kind: checkpoint.BriefDecision, ID: "dec_1", Text: "decision " + long},
		{Kind: checkpoint.BriefCurrentWork, ID: checkpoint.BriefIDCurrentWork, Text: "work " + long},
	}
	full := renderBrief(items, modwire.BriefMaxChars)
	require.Len(t, full.ids, len(items))
	// Room for the marker and four lines: the pin, the intent, the current work and the newest
	// restatement are chosen; the older restatement and the decision are not.
	limit := modwire.CharCount(modwire.BriefMarker)
	for _, i := range []int{0, 1, 3, 5} {
		limit += 1 + modwire.CharCount("- ["+items[i].ID+"] "+items[i].Text)
	}
	got := renderBrief(items, limit)
	require.Equal(t, []string{"inv_1", "intent:original", "intent:evolution:2", "work:current"}, got.ids)
	require.Equal(t, 2, got.over)
	require.LessOrEqual(t, modwire.CharCount(got.instructions), limit)
	resp := modwire.BriefResponse{Status: modwire.Served(), Instructions: got.instructions, FactIDs: got.ids}
	require.NoError(t, resp.Validate(limit))
}

// TestRenderBrief_SkipsAnInvalidIDAndServesTheRest is R22 at the renderer.
func TestRenderBrief_SkipsAnInvalidIDAndServesTheRest(t *testing.T) {
	items := []checkpoint.BriefItem{
		{Kind: checkpoint.BriefInvariant, ID: "inv with space", Text: "a"},
		{Kind: checkpoint.BriefInvariant, ID: strings.Repeat("i", modwire.MaxFactIDBytes+1), Text: "b"},
		{Kind: checkpoint.BriefInvariant, ID: "", Text: "c"},
		{Kind: checkpoint.BriefInvariant, ID: "inv_ok", Text: "d"},
	}
	got := renderBrief(items, modwire.BriefMaxChars)
	require.Equal(t, 3, got.invalid)
	require.Equal(t, []string{"inv_ok"}, got.ids)
	require.Equal(t, modwire.BriefMarker+"\n- [inv_ok] d", got.instructions)
}

// TestRenderBrief_NothingToServeIsEmpty: with no item, or none that fits, there is no marker
// either: a served brief with nothing to say is empty.
func TestRenderBrief_NothingToServeIsEmpty(t *testing.T) {
	require.Empty(t, renderBrief(nil, modwire.BriefMaxChars).instructions)
	items := []checkpoint.BriefItem{{Kind: checkpoint.BriefInvariant, ID: "inv_1", Text: "Never log access tokens."}}
	got := renderBrief(items, modwire.CharCount(modwire.BriefMarker)+5)
	require.Empty(t, got.instructions)
	require.Empty(t, got.ids)
	require.Equal(t, 1, got.over)
}

// TestBriefCharLimit_TakesTheSmallerOfTheRequestAndTheConfiguredCap: the request's max_chars and
// the caller's configured cap both bound the brief, and neither may exceed modwire.BriefMaxChars.
func TestBriefCharLimit_TakesTheSmallerOfTheRequestAndTheConfiguredCap(t *testing.T) {
	req := modwire.BriefRequest{}
	require.Equal(t, modwire.BriefMaxChars, briefCharLimit(req, 0))
	require.Equal(t, modwire.BriefMaxChars, briefCharLimit(req, modwire.BriefMaxChars*2))
	require.Equal(t, 1000, briefCharLimit(req, 1000))
	req.MaxChars = 500
	require.Equal(t, 500, briefCharLimit(req, 1000))
	require.Equal(t, 400, briefCharLimit(req, 400))
}

// TestBindCheckpoint_ArmsTheSourcesBeforeTheBriefSnapshot: the brief's snapshot seam arms the
// writer's sources first, as the PreCompact seam does, so a brief before any Begin (no draft, no
// published set) still opens a draft from the live sources.
func TestBindCheckpoint_ArmsTheSourcesBeforeTheBriefSnapshot(t *testing.T) {
	f := newCPFixture(t)
	f.closeSegment(cpSession, 1, 3)
	o := &Options{ProjectRoot: f.root, Cfg: f.cfg, Log: logging.Nop(), Clock: f.clk}
	resolved := 0
	sources := func() (checkpoint.SourceSet, error) {
		resolved++
		return f.src, nil
	}
	BindCheckpoint(o, f.cfg, f.w, checkpoint.SourceSet{}, WithSourceSupplier(sources))
	var s Services
	for _, bind := range o.binds {
		bind(&s)
	}
	require.NotNil(t, s.BriefSnapshot)

	b, err := s.BriefSnapshot(f.ctx(), cpSession)

	require.NoError(t, err)
	require.Equal(t, 1, resolved, "the seam re-resolved the sources at the brief")
	require.True(t, b.NewDraft)
	require.Equal(t, []core.SegmentID{1}, draftEncodedAt(t, f.root, cpSession))
}
