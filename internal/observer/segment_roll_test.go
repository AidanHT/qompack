package observer

// The observer follows every segment roll it did not make (V6 close-out wave 14; the w13-pinsckpt
// ticket). The scheduler closes a session's segment and opens its successor on a changepoint, a
// todo completion, a passing test, a git commit and a compaction, and no SessionStart need follow
// any of them. Before this, the observer adopted a segment only in OnSessionStart, so after such a
// roll it kept enrolling DAG members against the closed id, never gave the closed segment a DAG
// segment node, and at SessionEnd closed the already-closed id (a soft ErrAppendOnly), leaving the
// successor open and never encoded.

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// rollReadTokens is what every captured read costs in these rows, so each read moves the prefix
// position by exactly this much and every expected Pos and Tokens below is a multiple of it.
const rollReadTokens core.Tokens = 40

// TestSessionEndClosesTheSuccessorOfAScheduledRoll is the w13-pinsckpt ticket's reproduction
// (plans/sdd/V6-closeout/w13-pinsckpt/runs/16-ticket-observer-stale-segment.go.txt), committed:
// the scheduler closes the session's segment and opens its successor, the host sends no
// SessionStart afterwards (a host-failed compaction, or the rest of a session after a todo roll),
// and SessionEnd must close the session's OPEN segment, the successor, not the one the observer
// adopted at the last SessionStart.
func TestSessionEndClosesTheSuccessorOfAScheduledRoll(t *testing.T) {
	h, ss := newSessionHarness(t)
	ctx := context.Background()
	_, err := h.obs.OnSessionStart(ctx, startEvent("startup"))
	require.NoError(t, err)
	st := h.state(testSession)
	st.mu.Lock()
	adopted := st.Segment
	st.Turn = 12
	st.mu.Unlock()
	require.Equal(t, core.SegmentID(1), adopted)

	// The scheduler closes segment 1 at turn 9 and opens its successor 2 at turn 10 (what
	// daemon.closeSessionSegmentLocked does); the observer is not told.
	require.NoError(t, ss.seg.Close(ctx, adopted, 9, map[string]float64{}))
	ss.seg.setCurrent(store.Segment{ID: 2, Session: testSession, StartTurn: 10, Closed: false})

	_, err = h.obs.OnSessionEnd(ctx, endEvent())
	require.NoError(t, err)
	closes := ss.seg.closesList()
	require.Len(t, closes, 2)
	require.Equal(t, core.SegmentID(2), closes[1].ID, "SessionEnd closes the open successor, not the rolled segment")
	require.Equal(t, core.TurnIndex(12), closes[1].EndTurn)
	require.Zero(t, h.counter(counterErrPrefix+stageSegClose), "no refused close of the rolled segment")
}

// TestEventsAfterAScheduledRollEnrolInTheSuccessor drives a whole session across a roll: two reads
// in segment 1, a todo roll the scheduler makes at the second read's turn, then a Stop, a prompt and
// a read, then SessionEnd. The rolled segment gets its DAG node when the observer first sees the
// roll, spanning exactly what was enrolled in it; everything after the roll is enrolled in the
// successor; SessionEnd closes the successor with its own tokens, and the chain edge links the two.
func TestEventsAfterAScheduledRollEnrolInTheSuccessor(t *testing.T) {
	h, ss := newSessionHarness(t)
	ss.DefaultTokens = rollReadTokens
	ctx := context.Background()
	_, err := h.obs.OnSessionStart(ctx, startEvent("startup"))
	require.NoError(t, err)

	h.drive(readOf("toolu_roll_1", "src/a.ts", "alpha\n")) // turn 0, pos 0..40
	h.stop(stopOf(false), false)                           // turn 1
	h.drive(readOf("toolu_roll_2", "src/b.ts", "beta\n"))  // turn 1, pos 40..80
	succ := ss.seg.roll(t, 1, 1, 2*rollReadTokens)         // closes 1 at turn 1, opens 2 at turn 2

	h.stop(stopOf(false), false)                           // turn 2: the first event after the roll
	h.submit("now the second task")                        // turn 2, pos 80..120
	h.drive(readOf("toolu_roll_3", "src/c.ts", "gamma\n")) // turn 3, pos 120..160
	h.stop(stopOf(false), false)                           // turn 4
	_, err = h.obs.OnSessionEnd(ctx, endEvent())
	require.NoError(t, err)

	rolled, ok := h.Graph.Node(dag.SegmentNode(1))
	require.True(t, ok, "the rolled segment gets its DAG segment node")
	require.Equal(t, 0, rolled.Pos, "it starts where the observer opened it")
	require.Equal(t, 2*rollReadTokens, rolled.Tokens, "and spans exactly the two reads enrolled in it")
	require.Equal(t, "0-1", rolled.Ref, "its turn range is the one the scheduler closed it with")

	next, ok := h.Graph.Node(dag.SegmentNode(succ))
	require.True(t, ok, "SessionEnd builds the successor's node")
	require.Equal(t, int(2*rollReadTokens), next.Pos, "the successor starts at the roll's prefix position")
	require.Equal(t, 2*rollReadTokens, next.Tokens, "the prompt and the third read")
	require.Equal(t, "2-4", next.Ref)
	_, ok = findEdge(h.Graph.edges(), dag.SegmentNode(1), dag.SegmentNode(succ), dag.EdgeSequence)
	require.True(t, ok, "the chain edge runs from the rolled segment to its successor")

	for _, n := range []dag.NodeID{dag.ToolUseNode("toolu_roll_1"), dag.ToolResultNode("toolu_roll_2")} {
		_, ok = findEdge(h.Graph.edges(), n, dag.SegmentNode(1), dag.EdgeSequence)
		require.True(t, ok, "%s is a member of the rolled segment", n)
	}
	for _, n := range []dag.NodeID{
		dag.UserPromptNode(2), dag.ToolUseNode("toolu_roll_3"), dag.ToolResultNode("toolu_roll_3"),
	} {
		_, ok = findEdge(h.Graph.edges(), n, dag.SegmentNode(succ), dag.EdgeSequence)
		require.True(t, ok, "%s is enrolled in the successor", n)
		_, ok = findEdge(h.Graph.edges(), n, dag.SegmentNode(1), dag.EdgeSequence)
		require.False(t, ok, "%s is not enrolled against the closed segment", n)
	}

	closes := ss.seg.closesList()
	require.Len(t, closes, 2, "the scheduler's close and SessionEnd's, nothing refused")
	require.Equal(t, succ, closes[1].ID)
	require.Equal(t, core.TurnIndex(4), closes[1].EndTurn)
	require.Equal(t, float64(2*rollReadTokens), closes[1].Feats[featSegmentTokens])
	require.Zero(t, h.counter(counterErrPrefix+stageSegClose))
	require.Equal(t, int64(1), h.counter(counterSegmentFollowed), "one roll followed")
}

// TestSessionStartAfterACompactionRollEmitsTheRolledSegment: the scheduler closes the compacting
// session's segment at PreCompact and the host then sends SessionStart(compact). The observer must
// not simply adopt the successor as its current segment: the segment the compaction closed still
// needs its DAG node, and the successor its chain edge back to it.
func TestSessionStartAfterACompactionRollEmitsTheRolledSegment(t *testing.T) {
	h, ss := newSessionHarness(t)
	ss.DefaultTokens = rollReadTokens
	ctx := context.Background()
	_, err := h.obs.OnSessionStart(ctx, startEvent("startup"))
	require.NoError(t, err)
	h.drive(readOf("toolu_cmp_1", "src/a.ts", "alpha\n"))
	h.stop(stopOf(false), false)
	succ := ss.seg.roll(t, 1, 0, rollReadTokens) // PreCompact closes at the highest tool turn

	_, err = h.obs.OnSessionStart(ctx, startEvent(sourceCompact))
	require.NoError(t, err)

	rolled, ok := h.Graph.Node(dag.SegmentNode(1))
	require.True(t, ok, "the compacted segment gets its DAG segment node")
	require.Equal(t, 0, rolled.Pos)
	require.Equal(t, rollReadTokens, rolled.Tokens)
	require.Equal(t, "0-0", rolled.Ref)

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, succ, st.Segment, "the successor is the segment events now enrol in")
	require.Equal(t, core.SegmentID(1), st.PrevSegment, "and its chain edge will run back to the rolled one")
	require.Equal(t, core.TurnIndex(1), st.SegStartTurn)
	require.Equal(t, int(rollReadTokens), st.SegStartPos, "it starts at the roll's prefix position")
	require.Len(t, ss.seg.opensList(), 2, "the observer's first open and the scheduler's roll; nothing re-opened")
}

// TestSessionStartKeepsTheHeldSegmentsStart: a SessionStart that finds the segment the observer
// already holds still open (a compaction the scheduler did not roll: nothing observed, or
// checkpoint.frontier.advanceOnSegmentClose off) must not move that segment's start. Resetting
// SegStartPos to the current prefix position gave the segment node a StartPos past everything
// already enrolled in it and a Tokens count that left those members out.
func TestSessionStartKeepsTheHeldSegmentsStart(t *testing.T) {
	h, ss := newSessionHarness(t)
	ss.DefaultTokens = rollReadTokens
	ctx := context.Background()
	_, err := h.obs.OnSessionStart(ctx, startEvent("startup"))
	require.NoError(t, err)
	ss.seg.setCurrent(store.Segment{ID: 1, Session: testSession, StartTurn: 0}) // what the log holds
	h.drive(readOf("toolu_keep_1", "src/a.ts", "alpha\n"), readOf("toolu_keep_2", "src/b.ts", "beta\n"))

	_, err = h.obs.OnSessionStart(ctx, startEvent(sourceCompact))
	require.NoError(t, err)
	st := h.state(testSession)
	st.mu.Lock()
	require.Equal(t, core.SegmentID(1), st.Segment)
	require.Equal(t, 0, st.SegStartPos, "the held segment still starts where it opened")
	st.mu.Unlock()

	_, err = h.obs.OnSessionEnd(ctx, endEvent())
	require.NoError(t, err)
	node, ok := h.Graph.Node(dag.SegmentNode(1))
	require.True(t, ok)
	require.Equal(t, 0, node.Pos)
	require.Equal(t, 2*rollReadTokens, node.Tokens, "both reads, enrolled before the SessionStart")
}

// TestTwoRollsBetweenEventsChainEverySegment: more than one roll can land between two events the
// observer sees (a restarted daemon resuming from an observer state persisted before its
// predecessor's last rolls). Every rolled segment gets its node, chained in order; the ones nothing
// was enrolled in are empty and sit at the boundary.
func TestTwoRollsBetweenEventsChainEverySegment(t *testing.T) {
	h, ss := newSessionHarness(t)
	ss.DefaultTokens = rollReadTokens
	ctx := context.Background()
	_, err := h.obs.OnSessionStart(ctx, startEvent("startup"))
	require.NoError(t, err)
	h.drive(readOf("toolu_chain_1", "src/a.ts", "alpha\n")) // turn 0, pos 0..40
	mid := ss.seg.roll(t, 1, 0, rollReadTokens)
	last := ss.seg.roll(t, mid, 1, 0)

	h.stop(stopOf(false), false)

	first, ok := h.Graph.Node(dag.SegmentNode(1))
	require.True(t, ok)
	require.Equal(t, rollReadTokens, first.Tokens)
	empty, ok := h.Graph.Node(dag.SegmentNode(mid))
	require.True(t, ok, "the segment rolled open and closed again between the two events gets its node")
	require.Equal(t, int(rollReadTokens), empty.Pos, "at the boundary")
	require.Zero(t, empty.Tokens, "nothing was enrolled in it")
	require.Equal(t, "1-1", empty.Ref)
	_, ok = findEdge(h.Graph.edges(), dag.SegmentNode(1), dag.SegmentNode(mid), dag.EdgeSequence)
	require.True(t, ok, "the chain runs through it")

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, last, st.Segment)
	require.Equal(t, mid, st.PrevSegment)
	require.Equal(t, core.TurnIndex(2), st.SegStartTurn)
}

// TestARollWithoutASuccessorKeepsTheChain: the scheduler closed the segment but its successor's Open
// failed. The observer still gives the closed segment its node, holds no segment until the next
// SessionStart opens one (as after an Open failure of its own), and that segment chains back to the
// rolled one rather than starting a new chain.
func TestARollWithoutASuccessorKeepsTheChain(t *testing.T) {
	h, ss := newSessionHarness(t)
	ss.DefaultTokens = rollReadTokens
	ctx := context.Background()
	_, err := h.obs.OnSessionStart(ctx, startEvent("startup"))
	require.NoError(t, err)
	h.drive(readOf("toolu_nosucc_1", "src/a.ts", "alpha\n"))
	require.NoError(t, ss.seg.Close(ctx, 1, 0, map[string]float64{featSegmentTokens: float64(rollReadTokens)}))

	h.stop(stopOf(false), false)
	_, ok := h.Graph.Node(dag.SegmentNode(1))
	require.True(t, ok, "the closed segment gets its node")

	_, err = h.obs.OnSessionStart(ctx, startEvent("resume"))
	require.NoError(t, err)
	_, err = h.obs.OnSessionEnd(ctx, endEvent())
	require.NoError(t, err)
	opens := ss.seg.opensList()
	require.Len(t, opens, 2, "the next SessionStart opens the session's segment again")
	_, ok = findEdge(h.Graph.edges(), dag.SegmentNode(1), dag.SegmentNode(2), dag.EdgeSequence)
	require.True(t, ok, "and the new segment chains back to the rolled one")
}

// TestSessionEndClosesAnEmptySuccessorOnTheRealLog runs against the real segment log, which refuses
// a close whose end turn precedes the segment's start. A roll at the session's current turn (a todo
// completed by the session's last tool call) opens the successor one turn later; a SessionEnd that
// follows with no other event must still close it, at its start turn, rather than leave the
// session's last segment open for good.
func TestSessionEndClosesAnEmptySuccessorOnTheRealLog(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock()
	backing, err := store.Open(root, config.Defaults(), store.Deps{Log: logging.Nop(), Clock: clk})
	require.NoError(t, err)
	t.Cleanup(func() { _ = backing.Close() })
	h := newHarness(t, func(o *Options) {
		o.ProjectRoot = root
		o.Store = backing
	})
	ctx := context.Background()
	_, err = h.obs.OnSessionStart(ctx, startEvent("startup"))
	require.NoError(t, err)
	h.drive(readOf("toolu_real_1", "src/a.ts", "export const a = 1;\n")) // turn 0

	st := h.state(testSession)
	st.mu.Lock()
	held, turn := st.Segment, st.Turn
	st.mu.Unlock()
	require.Equal(t, core.TurnIndex(0), turn)
	segs := backing.Segments()
	require.NoError(t, segs.Close(ctx, held, turn, map[string]float64{featSegmentTokens: 1}))
	succ, err := segs.Open(ctx, store.Segment{Session: testSession, StartTurn: turn + 1})
	require.NoError(t, err)

	_, err = h.obs.OnSessionEnd(ctx, endEvent())
	require.NoError(t, err)
	got, err := segs.Get(ctx, succ)
	require.NoError(t, err)
	require.True(t, got.Closed, "SessionEnd closes the session's last segment")
	require.Equal(t, turn+1, got.EndTurn, "at its start turn: it holds nothing")
	require.Zero(t, got.Tokens)
	require.Zero(t, h.counter(counterErrPrefix+stageSegClose), "no refused close of segment %d", succ)
}
