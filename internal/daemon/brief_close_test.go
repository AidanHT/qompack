package daemon

// The brief's segment close (V7 plan §5.3 step 1): a sibling of CloseSegmentForCompaction that
// shares its body, with cause "brief" and counters of its own, so no compaction counter moves on a
// brief; and the PreCompact that follows a brief is not counted as unobserved for finding the
// segment the brief already closed.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/store"
)

// briefCloseRig is a bound runtime whose open segment holds one observed Read of 700 tokens, with
// the tap's services wrapped around a PreCompact seam that only counts its calls.
type briefCloseRig struct {
	fx        *rtFixture
	svc       *Services
	open      core.SegmentID
	preCompax int
}

func newBriefCloseRig(t *testing.T) *briefCloseRig {
	t.Helper()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	ctx := context.Background()
	open, err := fx.store.segs.Open(ctx, store.Segment{Session: rtSession, StartTurn: 0})
	require.NoError(t, err)
	r := &briefCloseRig{fx: fx, open: open}
	r.svc = &Services{
		ObserveTool: func(context.Context, hookio.Event) error { return nil },
		PreCompact: func(context.Context, hookio.Event) (hookio.Output, error) {
			r.preCompax++
			return hookio.Empty(), nil
		},
	}
	WrapServicesForScheduler(r.svc, fx.rt, fx.options())
	r.observe(t, tapToolUseID, tapToolUseTurn, 700)
	return r
}

// observe has the tap apply one stored Read at turn of tok tokens to the open segment.
func (r *briefCloseRig) observe(t *testing.T, id core.ToolUseID, turn core.TurnIndex, tok core.Tokens) {
	t.Helper()
	r.fx.store.put(store.ToolUseRecord{
		ID: id, Session: rtSession, Turn: turn, TS: r.fx.now() + 5_000, Tool: "Read",
		ArgsPreview: "read src/a.go", Path: "src/a.go", Tokens: tok,
	})
	require.NoError(t, r.svc.ObserveTool(context.Background(), tapToolEvent(id, "Read", "", "")))
}

// compactionCounters is every counter only a compaction may move.
func (r *briefCloseRig) compactionCounters() map[string]int64 {
	return map[string]int64{
		counterSegmentClosedPrefix + causeCompact: r.fx.counter(counterSegmentClosedPrefix + causeCompact),
		counterTapCompactUnobserved:               r.fx.counter(counterTapCompactUnobserved),
		counterTapCompactForeign:                  r.fx.counter(counterTapCompactForeign),
	}
}

func zeroCompactionCounters() map[string]int64 {
	return map[string]int64{
		counterSegmentClosedPrefix + causeCompact: 0,
		counterTapCompactUnobserved:               0,
		counterTapCompactForeign:                  0,
	}
}

// TestCloseSegmentForBrief_ClosesTheOpenSegmentWithNoCompactionCounter: the brief closes the
// session's open segment at the highest observed turn with its tokens, exactly as a compaction's
// close does, and counts only its own close.
func TestCloseSegmentForBrief_ClosesTheOpenSegmentWithNoCompactionCounter(t *testing.T) {
	t.Parallel()
	r := newBriefCloseRig(t)
	ctx := context.Background()

	closed, err := r.fx.rt.CloseSegmentForBrief(ctx, rtSession)
	require.NoError(t, err)

	require.True(t, closed)
	seg, err := r.fx.store.segs.Get(ctx, r.open)
	require.NoError(t, err)
	require.True(t, seg.Closed)
	require.Equal(t, tapToolUseTurn, seg.EndTurn)
	require.Equal(t, core.Tokens(700), seg.Tokens)
	cur, err := r.fx.store.segs.Current(ctx, rtSession)
	require.NoError(t, err)
	require.False(t, cur.Closed, "the successor is open")
	require.Equal(t, int64(1), r.fx.counter(counterSegmentClosedPrefix+causeBrief))
	require.Equal(t, zeroCompactionCounters(), r.compactionCounters(), "no compaction counter moves on a brief's close")
}

// TestCloseSegmentForBrief_ThePreCompactAfterItClosesNothingAndIsNotUnobserved: the compaction the
// brief was for arrives with nothing new in the segment the brief rolled open. Its close is a no-op,
// as any close of an empty segment is, and it is counted as a compaction after a brief, not as one
// whose open segment the runtime had no account of.
func TestCloseSegmentForBrief_ThePreCompactAfterItClosesNothingAndIsNotUnobserved(t *testing.T) {
	t.Parallel()
	r := newBriefCloseRig(t)
	ctx := context.Background()
	closed, err := r.fx.rt.CloseSegmentForBrief(ctx, rtSession)
	require.NoError(t, err)
	require.True(t, closed)
	closes := len(r.fx.store.segs.closeCalls)

	_, err = r.svc.PreCompact(ctx, tapEvent("PreCompact", rtSession))
	require.NoError(t, err)

	require.Equal(t, 1, r.preCompax, "the checkpointer still seals")
	require.Len(t, r.fx.store.segs.closeCalls, closes, "PreCompact's own close is a no-op")
	require.Equal(t, zeroCompactionCounters(), r.compactionCounters(),
		"neither a compact close nor sched.tap.compact_unobserved is counted")
	require.Equal(t, int64(1), r.fx.counter(counterTapCompactAfterBrief))
}

// TestCloseSegmentForBrief_WorkAfterTheBriefIsClosedByTheCompactionAsUsual: tokens that arrive
// between the brief and the compaction are closed by the compaction's own close, and once a
// compaction has closed past the brief its mark is spent: a later compaction with nothing observed
// is unobserved again.
func TestCloseSegmentForBrief_WorkAfterTheBriefIsClosedByTheCompactionAsUsual(t *testing.T) {
	t.Parallel()
	r := newBriefCloseRig(t)
	ctx := context.Background()
	_, err := r.fx.rt.CloseSegmentForBrief(ctx, rtSession)
	require.NoError(t, err)
	r.observe(t, "toolu_tap_after_brief", tapToolUseTurn+3, 300)

	_, err = r.svc.PreCompact(ctx, tapEvent("PreCompact", rtSession))
	require.NoError(t, err)
	require.Equal(t, int64(1), r.fx.counter(counterSegmentClosedPrefix+causeCompact), "the new work is closed")
	require.Zero(t, r.fx.counter(counterTapCompactAfterBrief))

	_, err = r.svc.PreCompact(ctx, tapEvent("PreCompact", rtSession))
	require.NoError(t, err)
	require.Equal(t, int64(1), r.fx.counter(counterTapCompactUnobserved), "the brief's mark does not outlive a close")
}

// TestCloseSegmentForBrief_ThePreCompactAfterItConsumesTheMark: one brief excuses one compaction.
func TestCloseSegmentForBrief_ThePreCompactAfterItConsumesTheMark(t *testing.T) {
	t.Parallel()
	r := newBriefCloseRig(t)
	ctx := context.Background()
	_, err := r.fx.rt.CloseSegmentForBrief(ctx, rtSession)
	require.NoError(t, err)

	for range 2 {
		_, err = r.svc.PreCompact(ctx, tapEvent("PreCompact", rtSession))
		require.NoError(t, err)
	}
	require.Equal(t, int64(1), r.fx.counter(counterTapCompactAfterBrief))
	require.Equal(t, int64(1), r.fx.counter(counterTapCompactUnobserved))
}

// TestCloseSegmentForBrief_LeavesAnotherSessionsSegment: as for a compaction, a runtime bound to one
// session closes nothing for another, and counts it under the brief's own name.
func TestCloseSegmentForBrief_LeavesAnotherSessionsSegment(t *testing.T) {
	t.Parallel()
	r := newBriefCloseRig(t)
	closes := len(r.fx.store.segs.closeCalls)

	closed, err := r.fx.rt.CloseSegmentForBrief(context.Background(), "sess-other-briefing")
	require.NoError(t, err)

	require.False(t, closed)
	require.Len(t, r.fx.store.segs.closeCalls, closes)
	require.Equal(t, int64(1), r.fx.counter(counterBriefCloseForeign))
	require.Equal(t, zeroCompactionCounters(), r.compactionCounters())
}

// TestCloseSegmentForBrief_LeavesASegmentWithNothingObservedOpen: a segment the runtime holds no
// token account for is left open, as a compaction leaves it, and counted under the brief's name;
// the PreCompact after such a brief is unobserved, because nothing was closed for it.
func TestCloseSegmentForBrief_LeavesASegmentWithNothingObservedOpen(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	ctx := context.Background()
	open, err := fx.store.segs.Open(ctx, store.Segment{Session: rtSession, StartTurn: 0})
	require.NoError(t, err)
	s := &Services{PreCompact: func(context.Context, hookio.Event) (hookio.Output, error) { return hookio.Empty(), nil }}
	WrapServicesForScheduler(s, fx.rt, fx.options())

	closed, err := fx.rt.CloseSegmentForBrief(ctx, rtSession)
	require.NoError(t, err)
	require.False(t, closed)
	seg, err := fx.store.segs.Get(ctx, open)
	require.NoError(t, err)
	require.False(t, seg.Closed)
	require.Equal(t, int64(1), fx.counter(counterBriefCloseUnobserved))
	require.Zero(t, fx.counter(counterTapCompactUnobserved))

	_, err = s.PreCompact(ctx, tapEvent("PreCompact", rtSession))
	require.NoError(t, err)
	require.Equal(t, int64(1), fx.counter(counterTapCompactUnobserved))
	require.Zero(t, fx.counter(counterTapCompactAfterBrief))
}

// TestCloseSegmentForBrief_AnUnboundRuntimeIsNotBoundByABrief is review finding I1. The brief route
// is no hook route and touches no registry, and the mod's session id may not be the hooks' (G0.1).
// So a brief on a runtime bound to no session (a daemon restarted mid-session) closes nothing and
// binds nothing; the PreCompact that follows, a hook route, binds and closes as it always has.
func TestCloseSegmentForBrief_AnUnboundRuntimeIsNotBoundByABrief(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newRTFixture(t)
	fx.rt.mu.Lock()
	fx.rt.d = &registryDaemon{reg: NewSessionRegistry()} // the registry knows no session
	fx.rt.mu.Unlock()
	open, err := fx.store.segs.Open(ctx, store.Segment{Session: rtSession, StartTurn: 0})
	require.NoError(t, err)
	s := &Services{
		ObserveTool: func(context.Context, hookio.Event) error { return nil },
		PreCompact:  func(context.Context, hookio.Event) (hookio.Output, error) { return hookio.Empty(), nil },
	}
	WrapServicesForScheduler(s, fx.rt, fx.options())
	tapRecord(fx, tapToolUseID, "Read", 700)
	require.NoError(t, s.ObserveTool(ctx, tapToolEvent(tapToolUseID, "Read", "", "")))
	require.Empty(t, fx.rt.session, "fixture sanity: the runtime is unbound")

	closed, err := fx.rt.CloseSegmentForBrief(ctx, rtSession)

	require.NoError(t, err)
	require.False(t, closed)
	require.Empty(t, fx.rt.session, "the brief bound nothing")
	require.Empty(t, fx.store.segs.closeCalls, "and closed nothing")
	require.Equal(t, int64(1), fx.counter(counterBriefCloseUnbound))

	_, err = s.PreCompact(ctx, tapEvent("PreCompact", rtSession))
	require.NoError(t, err)
	require.Equal(t, rtSession, fx.rt.session, "the PreCompact binds, as today")
	seg, err := fx.store.segs.Get(ctx, open)
	require.NoError(t, err)
	require.True(t, seg.Closed)
	require.Equal(t, core.Tokens(700), seg.Tokens)
	require.Equal(t, int64(1), fx.counter(counterSegmentClosedPrefix+causeCompact))
	require.Zero(t, fx.counter(counterTapCompactAfterBrief))
}

// TestCloseSegmentForBrief_TheMarkLapsesAfterTheBriefsCompactionWindow is review Minor 1: a brief
// whose compaction the host vetoed leaves its mark set, since no PreCompact spends it. The mark only
// excuses a compaction inside the window a brief's own compaction falls in (the mod's TimeoutMs plus
// the PreCompact hook's timeout); a much later empty compaction is unobserved again.
func TestCloseSegmentForBrief_TheMarkLapsesAfterTheBriefsCompactionWindow(t *testing.T) {
	t.Parallel()
	r := newBriefCloseRig(t)
	ctx := context.Background()
	closed, err := r.fx.rt.CloseSegmentForBrief(ctx, rtSession)
	require.NoError(t, err)
	require.True(t, closed)

	r.fx.clock.Advance(briefMarkWindow() + time.Millisecond)
	_, err = r.svc.PreCompact(ctx, tapEvent("PreCompact", rtSession))
	require.NoError(t, err)

	require.Zero(t, r.fx.counter(counterTapCompactAfterBrief), "a lapsed mark excuses nothing")
	require.Equal(t, int64(1), r.fx.counter(counterTapCompactUnobserved))
}

// TestCloseSegmentForBrief_TheMarkHoldsInsideTheWindow: the compaction a brief was for, arriving at
// the window's edge, is still excused.
func TestCloseSegmentForBrief_TheMarkHoldsInsideTheWindow(t *testing.T) {
	t.Parallel()
	r := newBriefCloseRig(t)
	ctx := context.Background()
	_, err := r.fx.rt.CloseSegmentForBrief(ctx, rtSession)
	require.NoError(t, err)

	r.fx.clock.Advance(briefMarkWindow())
	_, err = r.svc.PreCompact(ctx, tapEvent("PreCompact", rtSession))
	require.NoError(t, err)

	require.Equal(t, int64(1), r.fx.counter(counterTapCompactAfterBrief))
	require.Zero(t, r.fx.counter(counterTapCompactUnobserved))
}
