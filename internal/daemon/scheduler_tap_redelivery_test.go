package daemon

// Delivery is at least once (ingest.dispatch's doc comment, DrainConfig.Seen): the ingest worker
// runs the handler, and the scheduler tap inside it, BEFORE it commits the delivery's frontier
// record, so a commit that fails — a Stop's runCancel landing in it, a bounded drain's context
// ending in it — leaves the WAL line for a drain to replay through the same handler. The tap folded
// such a replay a second time: the open segment's tokens, the detector's posterior and the
// request-start anchor all took the same delivery twice, and the doubled account was persisted (V6
// close-out wave 20, C1.x; the 8-vs-12 red of TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook).
// The observer absorbed every replay (observer.redelivery_absorbed) except a main-agent Stop's: a
// Stop writes no record for its recognition rule to find, so a replayed Stop advanced the turn again
// and every later record of the session was numbered one turn late (audit 2, finding 3). It now
// recognizes a Stop by the identity of the session's last applied one, kept with the turn.
//
// These rows pin "one delivery, one application" at the tap, across a restart, and through the
// real worker and drain with the commit cut where Stop cuts it, for the observer's turn as well as
// the tap's account. They also pin the one effect a replay must still have: the segment close its
// cut run owed and did not make. None waits on a clock: the cut is a context cancelled between the
// handler and the commit, which is the exact interleaving Stop makes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
)

// delivered is ctx carrying the observation identity the ingest worker and the drain give the
// delivery leased with arrival in rtSession.
func delivered(t *testing.T, arrival uint64) context.Context {
	t.Helper()
	return deliveredFor(t, rtSession, arrival)
}

// deliveredFor is delivered for a delivery of sess.
func deliveredFor(t *testing.T, sess core.SessionID, arrival uint64) context.Context {
	t.Helper()
	id, err := core.NewObservationID(sess, arrival)
	require.NoError(t, err)
	return observer.WithObservation(context.Background(), id)
}

// cutDelivery is delivered with its context already cancelled: Stop's runCancel landing while the
// tap runs. The tap's only context-sensitive step is the segment close (the record lookup and the
// claim ignore ctx), so a context cancelled before the tap starts is the same interleaving as one
// cancelled after the claim and before the close.
func cutDelivery(t *testing.T, arrival uint64) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(delivered(t, arrival))
	cancel()
	return ctx
}

// detectorState is the runtime's BOCD posterior, encoded.
func detectorState(t *testing.T, r *schedRuntime) []byte {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := r.det.MarshalBinary()
	require.NoError(t, err)
	return b
}

// TestWrapServices_ARedeliveredToolUseIsAppliedOnce: the same delivery of one Read, run through the
// tap twice (the first run's commit failed), leaves the runtime exactly as one run does: its tokens
// in the open segment once, one detector observation. A different delivery is still applied.
func TestWrapServices_ARedeliveredToolUseIsAppliedOnce(t *testing.T) {
	t.Parallel()
	once, twice := newRTFixture(t), newRTFixture(t)
	for _, fx := range []*rtFixture{once, twice} {
		fx.bind(rtSession)
		tapRecord(fx, tapToolUseID, "Read", 700)
	}
	sOnce, sTwice := allSeams(), allSeams()
	WrapServicesForScheduler(sOnce, once.rt, once.options())
	WrapServicesForScheduler(sTwice, twice.rt, twice.options())
	read := tapToolEvent(tapToolUseID, "Read", "", "")

	require.NoError(t, sOnce.ObserveTool(delivered(t, 1), read))
	require.NoError(t, sTwice.ObserveTool(delivered(t, 1), read))
	twice.clock.Advance(time.Minute) // the drain replays it later
	require.NoError(t, sTwice.ObserveTool(delivered(t, 1), read))

	require.Equal(t, core.Tokens(700), twice.rt.openSegTokens, "the Read's tokens are in the open segment once")
	require.Equal(t, once.rt.contextTokens, twice.rt.contextTokens)
	require.Equal(t, detectorState(t, once.rt), detectorState(t, twice.rt),
		"the detector observed the delivery once")
	require.Equal(t, once.rt.lastActivity, twice.rt.lastActivity)
	require.Equal(t, int64(1), twice.counter(counterTapRedelivery))

	const next core.ToolUseID = "toolu_tap_next"
	putRead(twice, next, tapToolUseTurn+1, 300)
	require.NoError(t, sTwice.ObserveTool(delivered(t, 2), tapToolEvent(next, "Read", "", "")))
	require.Equal(t, core.Tokens(1_000), twice.rt.openSegTokens, "another delivery is applied")
	require.Equal(t, int64(1), twice.counter(counterTapRedelivery))
}

// TestWrapServices_ARedeliveredStopOrCapturedPromptIsAppliedOnce: a replayed Stop does not observe
// the detector again, and a replayed prompt capture does not move the request-start anchor to the
// instant of the replay.
func TestWrapServices_ARedeliveredStopOrCapturedPromptIsAppliedOnce(t *testing.T) {
	t.Parallel()
	t.Run("stop", func(t *testing.T) {
		t.Parallel()
		once, twice := newRTFixture(t), newRTFixture(t)
		sOnce, sTwice := allSeams(), allSeams()
		for _, fx := range []*rtFixture{once, twice} {
			fx.bind(rtSession)
		}
		WrapServicesForScheduler(sOnce, once.rt, once.options())
		WrapServicesForScheduler(sTwice, twice.rt, twice.options())
		stop := tapEvent("Stop", rtSession)

		require.NoError(t, sOnce.ObserveStop(delivered(t, 1), stop, false))
		require.NoError(t, sTwice.ObserveStop(delivered(t, 1), stop, false))
		require.NoError(t, sTwice.ObserveStop(delivered(t, 1), stop, false))

		require.Equal(t, detectorState(t, once.rt), detectorState(t, twice.rt),
			"the detector observed the Stop once")
		require.Equal(t, int64(1), twice.counter(counterTapRedelivery))
	})
	t.Run("captured prompt", func(t *testing.T) {
		t.Parallel()
		fx := newRTFixture(t)
		fx.bind(rtSession)
		s := allSeams()
		WrapServicesForScheduler(s, fx.rt, fx.options())
		prompt := hookio.Event{HookEventName: "UserPromptSubmit", SessionID: rtSession, Prompt: "carry on"}
		capture := func() {
			_, err := s.ObservePrompt(observer.WithPromptCaptureOnly(delivered(t, 1)), prompt)
			require.NoError(t, err)
		}

		capture()
		anchored := tapReadOnly(fx.rt, func(r *schedRuntime) core.UnixMilli { return r.lastRequestStartTS })
		fx.clock.Advance(time.Minute)
		capture()

		require.Equal(t, anchored, tapReadOnly(fx.rt, func(r *schedRuntime) core.UnixMilli { return r.lastRequestStartTS }),
			"the replay does not claim the prompt started when it was replayed")
		require.Equal(t, int64(1), fx.counter(counterTapRedelivery))
	})
}

// TestWrapServices_ARedeliveryAfterARestartIsNotFoldedAgain: the previous daemon applied a delivery,
// persisted its account, and stopped before the delivery's commit landed, so the restarted daemon's
// drain replays it. The persisted account already holds it: whether the replay reaches the restarted
// runtime before the live session binds it (a startup drain) or after, the account is the persisted
// 700 plus the live read's 300, never the 700 again.
func TestWrapServices_ARedeliveryAfterARestartIsNotFoldedAgain(t *testing.T) {
	t.Parallel()
	const live core.ToolUseID = "toolu_live_after_restart"
	restarted := func(t *testing.T) (*rtFixture, *Services) {
		t.Helper()
		ctx := context.Background()
		prev := newRTFixture(t)
		prev.bind(rtSession)
		tapRecord(prev, tapToolUseID, "Read", 700)
		ps := allSeams()
		WrapServicesForScheduler(ps, prev.rt, prev.options())
		require.NoError(t, ps.ObserveTool(delivered(t, 1), tapToolEvent(tapToolUseID, "Read", "", "")))
		require.NoError(t, prev.rt.Persist(ctx))

		fx := newRTFixture(t, withRoot(prev))
		require.Empty(t, fx.rt.session, "fixture sanity: the restarted runtime starts unbound")
		putRead(fx, live, tapToolUseTurn+2, 300)
		s := allSeams()
		WrapServicesForScheduler(s, fx.rt, fx.options())
		return fx, s
	}
	replay := func(t *testing.T, s *Services) {
		t.Helper()
		require.NoError(t, s.ObserveTool(delivered(t, 1), tapToolEvent(tapToolUseID, "Read", "", "")))
	}

	t.Run("replayed before the live session binds", func(t *testing.T) {
		t.Parallel()
		fx, s := restarted(t)
		reg := NewSessionRegistry()
		fx.rt.mu.Lock()
		fx.rt.d = &registryDaemon{reg: reg}
		fx.rt.mu.Unlock()

		replay(t, s)
		require.Empty(t, fx.rt.session, "fixture sanity: a session no hook touched does not bind")
		reg.Touch(rtSession, fx.now())
		require.NoError(t, s.ObserveTool(delivered(t, 2), tapToolEvent(live, "Read", "", "")))

		require.Equal(t, rtSession, fx.rt.session)
		require.Equal(t, core.Tokens(1_000), fx.rt.openSegTokens, "the persisted 700 and the live read's 300")
	})
	t.Run("replayed after a SessionStart binds", func(t *testing.T) {
		t.Parallel()
		fx, s := restarted(t)

		// A resumed session's SessionStart binds before any later drain replays the delivery; no
		// later delivery of the session can be applied first, since the ordering gate holds it
		// behind the unacknowledged one.
		_, err := s.SessionStart(context.Background(), tapEvent("SessionStart", rtSession))
		require.NoError(t, err)
		replay(t, s)
		require.Equal(t, core.Tokens(700), fx.rt.openSegTokens, "the persisted account already holds it")
		require.NoError(t, s.ObserveTool(delivered(t, 2), tapToolEvent(live, "Read", "", "")))

		require.Equal(t, rtSession, fx.rt.session)
		require.Equal(t, core.Tokens(1_000), fx.rt.openSegTokens, "the persisted 700 and the live read's 300")
	})
}

// tappedDaemon is wireTestDaemon's real observer, store and ingest with the scheduler's tap over its
// seams, a delivery journal under a held lock, and the drainer Stop runs: the composition the
// restart row in internal/cli drives, at the dispatch layer, with no transport and no worker pool.
func tappedDaemon(t *testing.T) (*daemon, *Options, *schedRuntime) {
	t.Helper()
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	spD3Drainer(dd, root)
	so := SchedulerRuntimeOptions{
		ProjectRoot: root, Cfg: o.Cfg, Clock: dd.clk, Log: dd.log, Metrics: dd.m,
		Store: o.Store, Graph: o.Graph,
	}
	rt, err := NewSchedulerRuntime(so)
	require.NoError(t, err)
	t.Cleanup(scheduler.DisablePSelection)
	WrapServicesForScheduler(dd.svc, rt, so)
	r, ok := rt.(*schedRuntime)
	require.True(t, ok)
	return dd, o, r
}

// tappedRead accepts one leased Read of a project file into dd's WAL and returns it with its job.
func tappedRead(t *testing.T, dd *daemon, id core.ToolUseID) (ipc.Request, job) {
	t.Helper()
	const sess core.SessionID = "sess-redelivered-read"
	req := ipc.Request{
		Op: ipc.OpObserveTool, Session: sess, TS: core.NowMilli(dd.clk), Nonce: testDeliveryToken('d'),
		Event: &hookio.Event{
			HookEventName: "PostToolUse", SessionID: sess, CWD: dd.root,
			ToolName: "Read", ToolUseID: id,
			ToolInput:    json.RawMessage(`{"file_path":"src/redelivered.py"}`),
			ToolResponse: json.RawMessage(`{"content":"A = 1\nB = 2\n"}`),
		},
	}
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	dd.registry.Touch(sess, req.TS) // the hook's route: its session is live, so a drain keeps its WAL
	require.NoError(t, dd.ing.Accept(req, line))
	return req, <-dd.ing.ring
}

// openTokens reads the runtime's open-segment account under its lock.
func openTokens(r *schedRuntime) core.Tokens {
	return tapReadOnly(r, func(r *schedRuntime) core.Tokens { return r.openSegTokens })
}

// TestIngest_AStopThatCutsTheCommitFoldsTheReplayedReadOnce is the defect's own interleaving. The
// worker runs the Read's handler — the observer indexes it and the tap folds its tokens — and then
// Stop's runCancel lands before the commit, so the delivery is not acknowledged and its WAL line is
// retained. Stop's drain replays it: the observer absorbs the replay, and the account must still
// hold the Read once.
func TestIngest_AStopThatCutsTheCommitFoldsTheReplayedReadOnce(t *testing.T) {
	dd, o, r := tappedDaemon(t)
	ctx := context.Background()
	const id core.ToolUseID = "toolu_cut_by_stop"
	req, j := tappedRead(t, dd, id)

	workerCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	dd.ing.dispatch(workerCtx, func(c context.Context, got ipc.Request) ipc.Response {
		resp := dd.runIngested(c, got)
		runCancel() // Stop, between the handler and the commit
		return resp
	}, j)

	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.False(t, journal.acknowledged(req.Nonce), "fixture sanity: the cut commit left the delivery pending")
	rec, err := o.Store.ToolUse(ctx, id)
	require.NoError(t, err)
	require.Positive(t, rec.Tokens, "fixture sanity")
	require.Equal(t, rec.Tokens, openTokens(r), "fixture sanity: the first run folded the Read")

	_, err = dd.Drain(ctx) // Stop's own drain
	require.NoError(t, err)

	require.True(t, journal.acknowledged(req.Nonce), "fixture sanity: the drain replayed and committed it")
	require.Equal(t, int64(1), dd.m.Counter("observer.redelivery_absorbed").Value(),
		"fixture sanity: the replay reached the observer")
	require.Equal(t, rec.Tokens, openTokens(r), "the replayed Read is in the account once")
}

// TestDrain_AnInterruptedCommitFoldsTheReplayedReadOnce: the drain has the same window. A drain pass
// whose context ends after the handler and before the commit (Stop's drain runs under
// stopDrainBound) leaves the line for the next pass, whose replay must not fold the Read again.
func TestDrain_AnInterruptedCommitFoldsTheReplayedReadOnce(t *testing.T) {
	dd, o, r := tappedDaemon(t)
	ctx := context.Background()
	const id core.ToolUseID = "toolu_cut_in_drain"
	req, _ := tappedRead(t, dd, id) // the ring's job is never run: only the WAL holds it, as after a ring drop

	passCtx, endPass := context.WithCancel(ctx)
	defer endPass()
	cfg := dd.drainConfig()
	cfg.Dispatch = func(c context.Context, got ipc.Request) ipc.Response {
		resp := dd.drainDispatch(c, got)
		endPass() // the pass's context ends between the handler and the commit
		return resp
	}
	_, _ = newDrainer(cfg).Drain(passCtx)

	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.False(t, journal.acknowledged(req.Nonce), "fixture sanity: the cut commit left the delivery pending")
	rec, err := o.Store.ToolUse(ctx, id)
	require.NoError(t, err)
	require.Positive(t, rec.Tokens, "fixture sanity")
	require.Equal(t, rec.Tokens, openTokens(r), "fixture sanity: the first pass folded the Read")

	_, err = dd.Drain(ctx) // the next pass
	require.NoError(t, err)

	require.True(t, journal.acknowledged(req.Nonce), "fixture sanity: the next pass replayed and committed it")
	require.Equal(t, rec.Tokens, openTokens(r), "the replayed Read is in the account once")
}

// tappedReadOf is tappedRead for a Read of sess under its own nonce, tool_use_id and file.
func tappedReadOf(t *testing.T, dd *daemon, sess core.SessionID, id core.ToolUseID, nonce rune, file string) job {
	t.Helper()
	req := ipc.Request{
		Op: ipc.OpObserveTool, Session: sess, TS: core.NowMilli(dd.clk), Nonce: testDeliveryToken(nonce),
		Event: &hookio.Event{
			HookEventName: "PostToolUse", SessionID: sess, CWD: dd.root,
			ToolName: "Read", ToolUseID: id,
			ToolInput:    json.RawMessage(`{"file_path":"` + file + `"}`),
			ToolResponse: json.RawMessage(`{"content":"A = 1\nB = 2\nC = 3\n"}`),
		},
	}
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	dd.registry.Touch(sess, req.TS)
	require.NoError(t, dd.ing.Accept(req, line))
	return <-dd.ing.ring
}

// TestIngest_AStopThatCutsItsOwnCommitAdvancesTheTurnOnce is audit 2's finding 3 through the real
// worker and drain. A main-agent Stop between two Reads moves the second Read one turn past the
// first. When Stop's runCancel lands between the Stop's handler and its commit, Stop's drain replays
// the Stop: the tap recognized the replay, but the observer ran the turn boundary again, so the
// second Read landed two turns past the first and the scheduler's maxTurn followed it. A cut Stop
// must leave the turns exactly as an uncut one does.
func TestIngest_AStopThatCutsItsOwnCommitAdvancesTheTurnOnce(t *testing.T) {
	run := func(t *testing.T, cut bool) (core.TurnIndex, core.TurnIndex) {
		t.Helper()
		dd, o, r := tappedDaemon(t)
		ctx := context.Background()
		const sess core.SessionID = "sess-stop-replayed"
		require.Equal(t, dispatchSettled,
			dd.ing.dispatch(ctx, dd.runIngested, tappedReadOf(t, dd, sess, "toolu_before_stop", 'a', "src/a.py")))

		stop := ipc.Request{
			Op: ipc.OpObserveStop, Session: sess, TS: core.NowMilli(dd.clk), Nonce: testDeliveryToken('f'),
			Event: &hookio.Event{HookEventName: "Stop", SessionID: sess, CWD: dd.root},
		}
		line, err := ipc.EncodeRequest(stop)
		require.NoError(t, err)
		dd.registry.Touch(sess, stop.TS)
		require.NoError(t, dd.ing.Accept(stop, line))
		workerCtx, runCancel := context.WithCancel(ctx)
		defer runCancel()
		dd.ing.dispatch(workerCtx, func(c context.Context, got ipc.Request) ipc.Response {
			resp := dd.runIngested(c, got)
			if cut {
				runCancel() // Stop, between the handler and the commit
			}
			return resp
		}, <-dd.ing.ring)
		journal, err := dd.deliveryJournal()
		require.NoError(t, err)
		require.Equal(t, !cut, journal.acknowledged(stop.Nonce), "fixture sanity: the commit was cut or not")
		_, err = dd.Drain(ctx) // Stop's own drain replays a cut Stop
		require.NoError(t, err)
		require.True(t, journal.acknowledged(stop.Nonce), "fixture sanity: the Stop is committed")

		require.Equal(t, dispatchSettled,
			dd.ing.dispatch(ctx, dd.runIngested, tappedReadOf(t, dd, sess, "toolu_after_stop", 'b', "src/b.py")))
		before, err := o.Store.ToolUse(ctx, "toolu_before_stop")
		require.NoError(t, err)
		after, err := o.Store.ToolUse(ctx, "toolu_after_stop")
		require.NoError(t, err)
		return after.Turn - before.Turn, tapReadOnly(r, func(r *schedRuntime) core.TurnIndex { return r.maxTurn })
	}
	uncutDelta, uncutMax := run(t, false)
	cutDelta, cutMax := run(t, true)
	require.Equal(t, core.TurnIndex(1), uncutDelta, "fixture sanity: an uncut Stop is one turn boundary")
	require.Equal(t, uncutDelta, cutDelta, "a replayed Stop must advance the turn exactly as one uncut Stop does")
	require.Equal(t, uncutMax, cutMax, "the scheduler's highest turn follows the observer's")
}

// segmentOf reads one segment of fx's log.
func segmentOf(t *testing.T, fx *rtFixture, id core.SegmentID) store.Segment {
	t.Helper()
	seg, err := fx.store.segs.Get(context.Background(), id)
	require.NoError(t, err)
	return seg
}

// closeAttempts counts the SegmentLog.Close calls fx's log has seen, failed ones included.
func closeAttempts(fx *rtFixture) int {
	fx.store.segs.mu.Lock()
	defer fx.store.segs.mu.Unlock()
	return len(fx.store.segs.closeCalls)
}

// TestWrapServices_ARedeliveryMakesTheBoundaryCloseItsCutRunMissed: a git commit's delivery is
// applied, and Stop's runCancel lands before the tap's segment close, so the close fails and the
// commit with it. The replay is recognized and folds nothing, but the close the first run owed is
// made: the segment ends at the commit's turn with the commit's tokens counted once, exactly as one
// uncut run leaves it. A further replay finds nothing owed and closes nothing.
func TestWrapServices_ARedeliveryMakesTheBoundaryCloseItsCutRunMissed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	open, err := fx.store.segs.Open(ctx, store.Segment{Session: rtSession, StartTurn: 0})
	require.NoError(t, err)
	const commit core.ToolUseID = "toolu_commit_cut"
	tapRecord(fx, commit, "Bash", 50)
	s := allSeams()
	WrapServicesForScheduler(s, fx.rt, fx.options())
	ev := tapToolEvent(commit, "Bash", `{"command":"git commit -m \"feat: x\""}`,
		`"[main 1a2b3c] feat: x\n 1 file changed"`)

	require.NoError(t, s.ObserveTool(cutDelivery(t, 1), ev))
	require.False(t, segmentOf(t, fx, open).Closed, "fixture sanity: the cut run's close failed")
	require.Equal(t, core.Tokens(50), openTokens(fx.rt), "fixture sanity: the cut run folded the commit")

	require.NoError(t, s.ObserveTool(delivered(t, 1), ev))

	seg := segmentOf(t, fx, open)
	require.True(t, seg.Closed, "the replay makes the close the cut run owed")
	require.Equal(t, tapToolUseTurn, seg.EndTurn)
	require.Equal(t, core.Tokens(50), seg.Tokens, "with the commit's tokens counted once")
	cur, err := fx.store.segs.Current(ctx, rtSession)
	require.NoError(t, err)
	require.Equal(t, tapToolUseTurn+1, cur.StartTurn, "and its successor is open")
	require.Zero(t, openTokens(fx.rt), "the successor starts empty")
	require.Equal(t, int64(1), fx.counter(counterTapRedelivery))
	require.Equal(t, int64(1), fx.counter(counterTapBoundaryPrefix+causeCommit), "the commit signal is counted once")
	require.Equal(t, int64(1), fx.counter(counterSegmentClosedPrefix+causeCommit))

	attempts := closeAttempts(fx)
	require.NoError(t, s.ObserveTool(delivered(t, 1), ev))
	require.Equal(t, attempts, closeAttempts(fx), "a close already made is not owed again")
	require.Equal(t, int64(2), fx.counter(counterTapRedelivery))
}

// declaringDetector is the runtime's own detector made to declare a changepoint on its declareAt-th
// observation: the posterior is the real one, only the decision is scripted, so a row can put a
// changepoint on the delivery it needs one on.
type declaringDetector struct {
	scheduler.Detector
	declareAt, seen int
}

func (d *declaringDetector) Observe(f scheduler.Features) scheduler.ChangepointState {
	st := d.Detector.Observe(f)
	d.seen++
	if d.seen == d.declareAt {
		st.AtChangepoint = true
	}
	return st
}

// TestWrapServices_ARedeliveryMakesTheChangepointCloseItsCutRunMissed: the delivery's observation
// declares a changepoint, whose close fails under Stop's runCancel, and the delivery's own tokens
// are then folded into the segment that should have closed before them. The replay neither observes
// the detector again nor folds again, and makes the owed close as an uncut run makes it: the segment
// ends at the changepoint's turn with what preceded the delivery, and the delivery's tokens open the
// successor.
func TestWrapServices_ARedeliveryMakesTheChangepointCloseItsCutRunMissed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const before core.ToolUseID = "toolu_before_changepoint"
	setup := func(t *testing.T) (*rtFixture, *Services, core.SegmentID, *declaringDetector) {
		t.Helper()
		fx := newRTFixture(t)
		fx.bind(rtSession)
		open, err := fx.store.segs.Open(ctx, store.Segment{Session: rtSession, StartTurn: 0})
		require.NoError(t, err)
		putRead(fx, before, tapToolUseTurn-1, 300)
		tapRecord(fx, tapToolUseID, "Read", 700)
		s := allSeams()
		WrapServicesForScheduler(s, fx.rt, fx.options())
		require.NoError(t, s.ObserveTool(delivered(t, 1), tapToolEvent(before, "Read", "", "")))
		fx.rt.mu.Lock()
		det := &declaringDetector{Detector: fx.rt.det, declareAt: 1}
		fx.rt.det = det
		fx.rt.mu.Unlock()
		return fx, s, open, det
	}
	read := tapToolEvent(tapToolUseID, "Read", "", "")

	once, sOnce, openOnce, _ := setup(t)
	require.NoError(t, sOnce.ObserveTool(delivered(t, 2), read))
	require.True(t, segmentOf(t, once, openOnce).Closed, "fixture sanity: an uncut run closes at the changepoint")

	twice, sTwice, open, det := setup(t)
	require.NoError(t, sTwice.ObserveTool(cutDelivery(t, 2), read))
	require.False(t, segmentOf(t, twice, open).Closed, "fixture sanity: the cut run's close failed")
	require.Equal(t, core.Tokens(1_000), openTokens(twice.rt), "fixture sanity: the read was folded into it")

	require.NoError(t, sTwice.ObserveTool(delivered(t, 2), read))

	seg, segOnce := segmentOf(t, twice, open), segmentOf(t, once, openOnce)
	require.True(t, seg.Closed, "the replay makes the changepoint close the cut run owed")
	require.Equal(t, segOnce.EndTurn, seg.EndTurn)
	require.Equal(t, core.Tokens(300), segOnce.Tokens, "fixture sanity: an uncut run closes before the read")
	require.Equal(t, segOnce.Tokens, seg.Tokens, "the closed segment holds what preceded the read")
	require.Equal(t, core.Tokens(700), openTokens(once.rt), "fixture sanity: the read opens the successor")
	require.Equal(t, openTokens(once.rt), openTokens(twice.rt), "the read's tokens open the successor, once")
	require.Equal(t, 1, det.seen, "the detector observed the delivery once")
	require.Equal(t, detectorState(t, once.rt), detectorState(t, twice.rt))
	require.Equal(t, int64(1), twice.counter(counterSegmentClosedPrefix+causeChangepoint))
	require.Equal(t, int64(1), twice.counter(counterTapRedelivery))
}

// TestWrapServices_AnotherSessionsRedeliveryAfterARestartIsNotFoldedAgain: the tap folds every
// session's tool use into the bound runtime's account, so the previous daemon's persisted account
// holds a read of another session too. That read's commit was cut and the restarted daemon's drain
// replays it: it is in the persisted account already, whether the replay reaches the restarted
// runtime before the live session binds it or after.
func TestWrapServices_AnotherSessionsRedeliveryAfterARestartIsNotFoldedAgain(t *testing.T) {
	t.Parallel()
	const (
		other     core.SessionID = "sess-other-live"
		otherRead core.ToolUseID = "toolu_other_session"
		live      core.ToolUseID = "toolu_live_after_restart"
	)
	otherEvent := tapToolEvent(otherRead, "Read", "", "")
	otherEvent.SessionID = other
	restarted := func(t *testing.T) (*rtFixture, *Services) {
		t.Helper()
		ctx := context.Background()
		prev := newRTFixture(t)
		prev.bind(rtSession)
		tapRecord(prev, tapToolUseID, "Read", 700)
		prev.store.put(store.ToolUseRecord{
			ID: otherRead, Session: other, Turn: 3, TS: prev.now() + 7_000, Tool: "Read",
			ArgsPreview: "read src/c.go", Path: "src/c.go", Tokens: 300,
		})
		ps := allSeams()
		WrapServicesForScheduler(ps, prev.rt, prev.options())
		require.NoError(t, ps.ObserveTool(delivered(t, 1), tapToolEvent(tapToolUseID, "Read", "", "")))
		require.NoError(t, ps.ObserveTool(deliveredFor(t, other, 1), otherEvent))
		require.Equal(t, core.Tokens(1_000), openTokens(prev.rt), "fixture sanity: both reads are in the account")
		require.NoError(t, prev.rt.Persist(ctx))

		fx := newRTFixture(t, withRoot(prev))
		require.Empty(t, fx.rt.session, "fixture sanity: the restarted runtime starts unbound")
		putRead(fx, live, tapToolUseTurn+2, 100)
		s := allSeams()
		WrapServicesForScheduler(s, fx.rt, fx.options())
		return fx, s
	}
	replay := func(t *testing.T, s *Services) {
		t.Helper()
		require.NoError(t, s.ObserveTool(deliveredFor(t, other, 1), otherEvent))
	}

	t.Run("replayed before the live session binds", func(t *testing.T) {
		t.Parallel()
		fx, s := restarted(t)
		reg := NewSessionRegistry()
		fx.rt.mu.Lock()
		fx.rt.d = &registryDaemon{reg: reg}
		fx.rt.mu.Unlock()

		replay(t, s)
		require.Empty(t, fx.rt.session, "fixture sanity: a session no hook touched does not bind")
		reg.Touch(rtSession, fx.now())
		require.NoError(t, s.ObserveTool(delivered(t, 2), tapToolEvent(live, "Read", "", "")))

		require.Equal(t, rtSession, fx.rt.session)
		require.Equal(t, core.Tokens(1_100), openTokens(fx.rt), "the persisted 1000 and the live read's 100")
	})
	t.Run("replayed after a SessionStart binds", func(t *testing.T) {
		t.Parallel()
		fx, s := restarted(t)

		_, err := s.SessionStart(context.Background(), tapEvent("SessionStart", rtSession))
		require.NoError(t, err)
		replay(t, s)
		require.Equal(t, core.Tokens(1_000), openTokens(fx.rt), "the persisted account already holds it")
		require.NoError(t, s.ObserveTool(delivered(t, 2), tapToolEvent(live, "Read", "", "")))

		require.Equal(t, core.Tokens(1_100), openTokens(fx.rt), "the persisted 1000 and the live read's 100")
	})
}

// TestWrapServices_ARedeliveryOfAnUnrecordedToolUseMovesNoAnchor: a tool use the store holds no
// record of (a capture refused by its scope never gets one) still notes the activity and the
// request start, at the clock. Its replay does not move them to the instant of the replay. It claims
// nothing either: a replay that finds the record the first run could not publish still applies it.
func TestWrapServices_ARedeliveryOfAnUnrecordedToolUseMovesNoAnchor(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	s := allSeams()
	WrapServicesForScheduler(s, fx.rt, fx.options())
	const refused core.ToolUseID = "toolu_unrecorded"
	ev := tapToolEvent(refused, "Read", "", "")
	anchors := func() [2]core.UnixMilli {
		return tapReadOnly(fx.rt, func(r *schedRuntime) [2]core.UnixMilli {
			return [2]core.UnixMilli{r.lastRequestStartTS, r.lastActivity}
		})
	}

	require.NoError(t, s.ObserveTool(delivered(t, 1), ev))
	first := anchors()
	require.Equal(t, [2]core.UnixMilli{fx.now(), fx.now()}, first, "fixture sanity: anchored at the clock")
	fx.clock.Advance(time.Minute)
	require.NoError(t, s.ObserveTool(delivered(t, 1), ev))

	require.Equal(t, first, anchors(), "the replay moves neither anchor to the instant of the replay")
	require.Equal(t, int64(2), fx.counter(counterTapNoRecord))
	require.Equal(t, int64(1), fx.counter(counterTapRedelivery))

	putRead(fx, refused, tapToolUseTurn, 400)
	require.NoError(t, s.ObserveTool(delivered(t, 1), ev))
	require.Equal(t, core.Tokens(400), openTokens(fx.rt), "a replay that finds the record still applies it")
	require.Equal(t, int64(1), fx.counter(counterTapRedelivery))
}

// TestWrapServices_AReplayBeforeABindToAnotherSessionCountsForIt is audit 2's finding 5. The
// previous daemon was bound to rtSession and folded a Read of a second live window into that account
// (the tap folds every session's tool use into the bound account), persisted it, and stopped with
// that Read's commit cut. After the restart the second window's next hook is the first to arrive, so
// the runtime binds to it, and rtSession's document is discarded: nothing the bind restores holds the
// Read. The replay the startup drain made before the bind must therefore count for the bound account;
// recognizing it from the document at construction (the wave 20 seed) skipped it, and its tokens were
// lost. A later delivery of the same window replayed before the bind counts too.
func TestWrapServices_AReplayBeforeABindToAnotherSessionCountsForIt(t *testing.T) {
	t.Parallel()
	const (
		window core.SessionID = "sess-other-window"
		cutID  core.ToolUseID = "toolu_window_cut"
		nextID core.ToolUseID = "toolu_window_next"
		liveID core.ToolUseID = "toolu_window_live"
	)
	readOf := func(id core.ToolUseID) hookio.Event {
		return hookio.Event{HookEventName: "PostToolUse", SessionID: window, ToolName: "Read", ToolUseID: id}
	}
	putWindowRead := func(fx *rtFixture, id core.ToolUseID, turn core.TurnIndex, tokens core.Tokens) {
		fx.store.put(store.ToolUseRecord{
			ID: id, Session: window, Turn: turn, TS: fx.now() + 1_000,
			Tool: "Read", ArgsPreview: "read src/w.go", Path: "src/w.go", Tokens: tokens,
		})
	}
	restarted := func(t *testing.T) (*rtFixture, *Services, *SessionRegistry) {
		t.Helper()
		prev := newRTFixture(t)
		prev.bind(rtSession)
		putWindowRead(prev, cutID, 3, 300)
		ps := allSeams()
		WrapServicesForScheduler(ps, prev.rt, prev.options())
		require.NoError(t, ps.ObserveTool(deliveredFor(t, window, 1), readOf(cutID)))
		require.NoError(t, prev.rt.Persist(context.Background()))

		fx := newRTFixture(t, withRoot(prev))
		require.Empty(t, fx.rt.session, "fixture sanity: the restarted runtime starts unbound")
		putWindowRead(fx, cutID, 3, 300)
		putWindowRead(fx, nextID, 4, 50)
		putWindowRead(fx, liveID, 5, 100)
		s := allSeams()
		WrapServicesForScheduler(s, fx.rt, fx.options())
		reg := NewSessionRegistry()
		fx.rt.mu.Lock()
		fx.rt.d = &registryDaemon{reg: reg}
		fx.rt.mu.Unlock()
		return fx, s, reg
	}

	t.Run("the cut read", func(t *testing.T) {
		t.Parallel()
		fx, s, reg := restarted(t)
		require.NoError(t, s.ObserveTool(deliveredFor(t, window, 1), readOf(cutID))) // the startup drain
		require.Empty(t, fx.rt.session, "fixture sanity: a session no hook touched does not bind")
		reg.Touch(window, fx.now())
		require.NoError(t, s.ObserveTool(deliveredFor(t, window, 2), readOf(liveID)))

		require.Equal(t, window, fx.rt.session)
		require.Equal(t, core.Tokens(400), openTokens(fx.rt), "the replayed 300 and the live read's 100")
	})
	t.Run("and a later read of the window", func(t *testing.T) {
		t.Parallel()
		fx, s, reg := restarted(t)
		require.NoError(t, s.ObserveTool(deliveredFor(t, window, 1), readOf(cutID)))
		require.NoError(t, s.ObserveTool(deliveredFor(t, window, 2), readOf(nextID)))
		reg.Touch(window, fx.now())
		require.NoError(t, s.ObserveTool(deliveredFor(t, window, 3), readOf(liveID)))

		require.Equal(t, window, fx.rt.session)
		require.Equal(t, core.Tokens(450), openTokens(fx.rt), "300 and 50 replayed, and the live read's 100")
	})
}

// TestWrapServices_AReplayIsDedupedAgainstTheAccountTheBindReads: the restarted daemon's runtime is
// constructed (wireScheduler) before daemon.New and Run take the project's lock, while its
// predecessor's final scheduler persist (closeScheduler, deferred in runDaemon) lands after the
// predecessor released that lock. An identity read from the document at construction could miss the
// one the final persist wrote, and the startup drain's replay of that delivery was then folded on top
// of the restored account that holds it. The bind reads the document, so the dedupe is against what it
// restores, whenever that was written.
func TestWrapServices_AReplayIsDedupedAgainstTheAccountTheBindReads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	prev := newRTFixture(t)
	prev.bind(rtSession)
	tapRecord(prev, tapToolUseID, "Read", 700)
	ps := allSeams()
	WrapServicesForScheduler(ps, prev.rt, prev.options())
	require.NoError(t, ps.ObserveTool(delivered(t, 1), tapToolEvent(tapToolUseID, "Read", "", "")))

	fx := newRTFixture(t, withRoot(prev)) // constructed before the predecessor's final persist
	require.Empty(t, fx.rt.session, "fixture sanity: the restarted runtime starts unbound")
	require.NoError(t, prev.rt.Persist(ctx))

	const live core.ToolUseID = "toolu_live_after_restart"
	tapRecord(fx, tapToolUseID, "Read", 700)
	putRead(fx, live, tapToolUseTurn+2, 300)
	s := allSeams()
	WrapServicesForScheduler(s, fx.rt, fx.options())
	reg := NewSessionRegistry()
	fx.rt.mu.Lock()
	fx.rt.d = &registryDaemon{reg: reg}
	fx.rt.mu.Unlock()

	require.NoError(t, s.ObserveTool(delivered(t, 1), tapToolEvent(tapToolUseID, "Read", "", ""))) // the startup drain
	reg.Touch(rtSession, fx.now())
	require.NoError(t, s.ObserveTool(delivered(t, 2), tapToolEvent(live, "Read", "", "")))

	require.Equal(t, rtSession, fx.rt.session)
	require.Equal(t, core.Tokens(1_000), openTokens(fx.rt), "the persisted 700 and the live read's 300")
}

// tapAppliedBound restates the number of sessions whose last applied delivery the tap remembers
// (maxAppliedSessions), so this file compiles on a tree without it.
const tapAppliedBound = 256

// TestWrapServices_TheAppliedIdentitiesAreBounded is audit 2's finding 6 and D67(b). The tap kept
// one identity per session it ever saw a delivery of, for the daemon's lifetime, and persisted every
// one the bound account held: a daemon that never idles, such as a headless loop of short sessions in
// one project, grew both without bound. Both are now bounded, by recency: the sessions whose
// deliveries the tap applied most recently are the ones remembered, and a replay of the newest is
// still recognized.
func TestWrapServices_TheAppliedIdentitiesAreBounded(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	s := allSeams()
	WrapServicesForScheduler(s, fx.rt, fx.options())
	sessions := make([]core.SessionID, tapAppliedBound+44)
	for i := range sessions {
		sessions[i] = core.SessionID(fmt.Sprintf("sess-headless-%04d", i))
		require.NoError(t, s.ObserveStop(deliveredFor(t, sessions[i], 1), tapEvent("Stop", sessions[i]), false))
	}

	kept := tapReadOnly(fx.rt, func(r *schedRuntime) map[core.SessionID]bool {
		out := make(map[core.SessionID]bool, len(r.applied))
		for sess := range r.applied {
			out[sess] = true
		}
		return out
	})
	require.Len(t, kept, tapAppliedBound, "the tap remembers a bounded number of sessions")
	for i, sess := range sessions {
		require.Equal(t, i >= len(sessions)-tapAppliedBound, kept[sess],
			"the sessions applied most recently are the ones remembered (session %d)", i)
	}

	require.NoError(t, fx.rt.Persist(context.Background()))
	raw, err := os.ReadFile(fx.statePath(stateFileScheduler))
	require.NoError(t, err)
	doc, err := decodeSchedulerState(raw)
	require.NoError(t, err)
	require.Len(t, doc.LastAppliedObservations, tapAppliedBound, "last_applied_observations is bounded too")

	last := sessions[len(sessions)-1]
	require.NoError(t, s.ObserveStop(deliveredFor(t, last, 1), tapEvent("Stop", last), false))
	require.Equal(t, int64(1), fx.counter(counterTapRedelivery), "a replay of the newest is recognized")
}

// TestWrapServices_AReplayWhoseRecordLookupFailsStillMakesTheOwedClose is audit 2's nit on
// observeTool's lookup-error branch. A replay of a delivery the tap applied, whose record lookup then
// fails with something other than not-found, was counted as a redelivery and returned without the
// segment close the cut run owed; if that replay's commit landed, the close was never made.
func TestWrapServices_AReplayWhoseRecordLookupFailsStillMakesTheOwedClose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	open, err := fx.store.segs.Open(ctx, store.Segment{Session: rtSession, StartTurn: 0})
	require.NoError(t, err)
	const commit core.ToolUseID = "toolu_commit_lookup_fails"
	tapRecord(fx, commit, "Bash", 50)
	s := allSeams()
	WrapServicesForScheduler(s, fx.rt, fx.options())
	ev := tapToolEvent(commit, "Bash", `{"command":"git commit -m \"feat: x\""}`,
		`"[main 1a2b3c] feat: x\n 1 file changed"`)

	require.NoError(t, s.ObserveTool(cutDelivery(t, 1), ev))
	require.False(t, segmentOf(t, fx, open).Closed, "fixture sanity: the cut run's close failed")

	fx.store.mu.Lock()
	fx.store.toolUseErr = errors.New("index unavailable")
	fx.store.mu.Unlock()
	require.NoError(t, s.ObserveTool(delivered(t, 1), ev))

	seg := segmentOf(t, fx, open)
	require.True(t, seg.Closed, "the replay makes the close the cut run owed")
	require.Equal(t, core.Tokens(50), seg.Tokens, "with the commit's tokens counted once")
	require.Equal(t, int64(1), fx.counter(counterTapRedelivery))
}

// TestWrapServices_AReplayedSessionStartMovesNoAnchorAndRebindsNothing is audit 2's finding 7 item 2
// (D67(b)). A compact SessionStart whose reply missed the hook's deadline is spooled and replayed by a
// drain, through the same seam, and the tap re-ran it at the replay's instant: the Young–Daly clock's
// last compaction and the last activity moved to when the drain ran, not when the host compacted. The
// class is wider than compact: a replayed start of another session rebound a runtime the live session
// holds, and one of a session no hook has touched since bound an unbound runtime to it, which leaves
// the live session unbound for good (bindOnFirstHook says why). A replayed start is a past event: it
// binds only what a session's first hook would, and moves no anchor.
func TestWrapServices_AReplayedSessionStartMovesNoAnchorAndRebindsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	compactStart := tapEvent("SessionStart", rtSession)
	compactStart.Source = sessionSourceCompact
	anchors := func(fx *rtFixture) [2]core.UnixMilli {
		return tapReadOnly(fx.rt, func(r *schedRuntime) [2]core.UnixMilli {
			return [2]core.UnixMilli{r.lastCompactionTS, r.lastActivity}
		})
	}

	t.Run("a compact start of the bound session", func(t *testing.T) {
		t.Parallel()
		fx := newRTFixture(t)
		s := allSeams()
		WrapServicesForScheduler(s, fx.rt, fx.options())
		_, err := s.SessionStart(ctx, compactStart)
		require.NoError(t, err)
		live := anchors(fx)
		require.Equal(t, fx.now(), live[0], "fixture sanity: the live compact start anchored the clock")

		fx.clock.Advance(time.Minute) // the drain replays the spooled copy later
		_, err = s.SessionStart(withSpoolReplay(ctx), compactStart)
		require.NoError(t, err)

		require.Equal(t, live, anchors(fx), "a replayed compact start moves neither the compaction nor the activity anchor")
	})
	t.Run("a start of another session", func(t *testing.T) {
		t.Parallel()
		fx := newRTFixture(t)
		s := allSeams()
		WrapServicesForScheduler(s, fx.rt, fx.options())
		_, err := s.SessionStart(ctx, tapEvent("SessionStart", rtSession))
		require.NoError(t, err)

		_, err = s.SessionStart(withSpoolReplay(ctx), tapEvent("SessionStart", "sess-replayed-start"))
		require.NoError(t, err)

		require.Equal(t, rtSession, tapReadOnly(fx.rt, func(r *schedRuntime) core.SessionID { return r.session }),
			"a replayed start does not take the runtime from the live session")
	})
	t.Run("a start on an unbound runtime", func(t *testing.T) {
		t.Parallel()
		fx := newRTFixture(t)
		s := allSeams()
		WrapServicesForScheduler(s, fx.rt, fx.options())
		reg := NewSessionRegistry()
		fx.rt.mu.Lock()
		fx.rt.d = &registryDaemon{reg: reg}
		fx.rt.mu.Unlock()
		bound := func() core.SessionID {
			return tapReadOnly(fx.rt, func(r *schedRuntime) core.SessionID { return r.session })
		}

		_, err := s.SessionStart(withSpoolReplay(ctx), tapEvent("SessionStart", "sess-not-live"))
		require.NoError(t, err)
		require.Empty(t, bound(), "a replayed start of a session no hook has touched binds nothing")

		reg.Touch(rtSession, fx.now())
		_, err = s.SessionStart(withSpoolReplay(ctx), tapEvent("SessionStart", rtSession))
		require.NoError(t, err)
		require.Equal(t, rtSession, bound(), "a replayed start of a live session binds it, as its first hook would")
	})
}

// TestSessionStartRoute_AReplayedStartBindsOnlyASessionThatWasLive is the wave 22 verifier's
// finding against the row above, which calls the tap's seam alone: through the real session.start
// route, the route registers the replayed start's session (registry.Ensure) before the tap runs, and
// Ensure marks every session it registers live, an ended one included. So the tap's liveness question
// always said yes for the replayed start's own session, and a replay of a session that ended, or that
// no hook of this daemon has touched, bound an unbound runtime. And a replayed start of the live
// session no longer rebound a runtime still bound to a session that had ended, which base did: the
// live session then ran on the ended one's account, with p-selection off, until its next compaction.
// The tap now asks whether the session was live before the route's own Ensure: a replayed start binds
// an unbound runtime, or rebinds one whose session is no longer live, only when its session was.
func TestSessionStartRoute_AReplayedStartBindsOnlyASessionThatWasLive(t *testing.T) {
	ctx := context.Background()
	const ended, live, unseen core.SessionID = "sess-ended", "sess-live", "sess-never-seen"
	attached := func(t *testing.T) (*daemon, *schedRuntime) {
		t.Helper()
		dd, o, r := tappedDaemon(t)
		r.mu.Lock()
		r.d = dd // what RegisterSchedulerIdleWork does in production
		r.mu.Unlock()
		// A compaction opens the negative-knowledge ledger lazily, and no Run closes it here; Windows
		// will not delete an open file.
		t.Cleanup(func() {
			if l := o.LedgerHandle(); l != nil {
				_ = l.Close()
			}
		})
		return dd, r
	}
	bound := func(r *schedRuntime) core.SessionID {
		return tapReadOnly(r, func(r *schedRuntime) core.SessionID { return r.session })
	}
	activity := func(r *schedRuntime) core.UnixMilli {
		return tapReadOnly(r, func(r *schedRuntime) core.UnixMilli { return r.lastActivity })
	}
	startOf := func(dd *daemon, sess core.SessionID, source string, nonce rune) ipc.Request {
		return ipc.Request{
			Op: ipc.OpSessionStart, Session: sess, TS: core.NowMilli(dd.clk), Nonce: testDeliveryToken(nonce),
			Event: &hookio.Event{HookEventName: "SessionStart", SessionID: sess, CWD: dd.root, Source: source},
			Reply: true,
		}
	}
	// replay is a drain's replay of a spooled start through the route, joined with the work a compact
	// start leaves running after the route answers.
	replay := func(t *testing.T, dd *daemon, req ipc.Request) {
		t.Helper()
		require.True(t, dd.dispatchOp(withSpoolReplay(ctx), req).OK)
		dd.promptWG.Wait()
	}
	// boundToEnded binds the runtime by ended's live start, then ends ended as the flush route does:
	// the registry first, then the SessionEnd seam, whose tap persists and closes the runtime and keeps
	// its binding.
	boundToEnded := func(t *testing.T, dd *daemon, r *schedRuntime) {
		t.Helper()
		req := startOf(dd, ended, "startup", 'a')
		require.True(t, dd.dispatchOp(ctx, req).OK)
		require.Equal(t, ended, bound(r), "fixture sanity: the live start binds")
		dd.registry.End(ended, core.NowMilli(dd.clk))
		require.NoError(t, dd.svc.SessionEnd(ctx, *req.Event))
		require.Equal(t, ended, bound(r), "fixture sanity: an end keeps the binding")
	}

	t.Run("the live session after the bound one ended", func(t *testing.T) {
		dd, r := attached(t)
		boundToEnded(t, dd, r)
		dd.registry.Touch(live, core.NowMilli(dd.clk)) // its live hooks; its own start was spooled

		replay(t, dd, startOf(dd, live, "startup", 'b'))

		require.Equal(t, live, bound(r), "the live session's replayed start takes the runtime from the ended one")
		require.Zero(t, activity(r), "and notes no activity at the replay's instant")
	})
	for _, source := range []string{"startup", sessionSourceCompact} {
		t.Run("a live session on an unbound runtime, "+source, func(t *testing.T) {
			dd, r := attached(t)
			dd.registry.Touch(live, core.NowMilli(dd.clk))

			replay(t, dd, startOf(dd, live, source, 'c'))

			require.Equal(t, live, bound(r), "binds it, as its first hook would")
			require.Zero(t, activity(r), "and notes no activity at the replay's instant")
		})
	}
	t.Run("another live session while the bound one is live", func(t *testing.T) {
		dd, r := attached(t)
		require.True(t, dd.dispatchOp(ctx, startOf(dd, rtSession, "startup", 'd')).OK)
		dd.registry.Touch(live, core.NowMilli(dd.clk))

		replay(t, dd, startOf(dd, live, "startup", 'e'))

		require.Equal(t, rtSession, bound(r), "a replayed start does not take the runtime from a live session")
	})
	for _, tc := range []struct {
		name  string
		sess  core.SessionID
		bound bool
	}{
		{name: "a never-seen session on an unbound runtime", sess: unseen},
		{name: "an ended session's start fired before its end, on an unbound runtime", sess: ended},
		{name: "a never-seen session on a runtime bound to an ended one", sess: unseen, bound: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dd, r := attached(t)
			want := core.SessionID("")
			req := startOf(dd, tc.sess, "startup", 'f')
			if tc.bound {
				boundToEnded(t, dd, r)
				want = ended
			} else if tc.sess == ended {
				// A leftover: the host fired this start before the session ended. One fired after the end
				// is the session's resume (TestSessionStartRoute_AReplayedStartFiredAfterItsSessionEndedIsAResume).
				now := core.NowMilli(dd.clk)
				dd.registry.Ensure(&hookio.Event{SessionID: ended, Source: "startup"}, now)
				dd.registry.End(ended, now)
				req.TS = now - 1
			}
			require.False(t, dd.registry.IsLive(tc.sess), "fixture sanity: the replayed start's session is not live")

			replay(t, dd, req)

			require.Equal(t, want, bound(r), "a replayed start of a session that was not live binds nothing")
		})
	}
}

// TestSessionStartRoute_AReplayedStartFiredAfterItsSessionEndedIsAResume is the wave 22 verifier's
// second-round finding against the row above. SessionEnd ends the session in the registry (End), and
// only an abandoned session revives on traffic, so the session a user resumes in a daemon that stayed
// up is not live when its resume's SessionStart reaches the daemon only as a drain's replay of the
// hook's spool. The tap refused that start as a leftover: a runtime still bound to the session kept
// p-selection off from the end's Close and never read the resume's model hint, and a runtime bound to
// another session that had ended was not rebound, so the resumed session ran on the ended one's
// account. Base 2bf29705 re-enabled and rebound in both cases. A start the host fired no earlier than
// its session's end is not a leftover the end preceded: it binds as a live start would, moving no
// anchor. A start fired before the end is still refused.
func TestSessionStartRoute_AReplayedStartFiredAfterItsSessionEndedIsAResume(t *testing.T) {
	ctx := context.Background()
	const resumed, other core.SessionID = "sess-resumed", "sess-other-ended"
	attached := func(t *testing.T) (*daemon, *schedRuntime) {
		t.Helper()
		dd, o, r := tappedDaemon(t)
		r.mu.Lock()
		r.d = dd // what RegisterSchedulerIdleWork does in production
		r.mu.Unlock()
		t.Cleanup(func() {
			if l := o.LedgerHandle(); l != nil {
				_ = l.Close()
			}
		})
		return dd, r
	}
	type view struct {
		session  core.SessionID
		model    string
		anchors  [2]core.UnixMilli
		selected bool
	}
	look := func(r *schedRuntime) view {
		v := tapReadOnly(r, func(r *schedRuntime) view {
			return view{session: r.session, model: r.model, anchors: [2]core.UnixMilli{r.lastCompactionTS, r.lastActivity}}
		})
		v.selected = scheduler.PSelectionAvailable()
		return v
	}
	startOf := func(dd *daemon, sess core.SessionID, source, model string, ts core.UnixMilli, nonce rune) ipc.Request {
		return ipc.Request{
			Op: ipc.OpSessionStart, Session: sess, TS: ts, Nonce: testDeliveryToken(nonce),
			Event: &hookio.Event{
				HookEventName: "SessionStart", SessionID: sess, CWD: dd.root, Source: source,
				Extra: map[string]json.RawMessage{extraModel: json.RawMessage(`"` + model + `"`)},
			},
			Reply: true,
		}
	}
	replay := func(t *testing.T, dd *daemon, req ipc.Request) {
		t.Helper()
		require.True(t, dd.dispatchOp(withSpoolReplay(ctx), req).OK)
		dd.promptWG.Wait()
	}
	// liveThenEnded binds the runtime by sess's live start, then ends sess as the flush route does: the
	// registry first, then the SessionEnd seam, whose tap persists and closes the runtime, keeping its
	// binding and turning p-selection off. It returns the end's instant.
	liveThenEnded := func(t *testing.T, dd *daemon, r *schedRuntime, sess core.SessionID) core.UnixMilli {
		t.Helper()
		req := startOf(dd, sess, "startup", "claude-at-startup", core.NowMilli(dd.clk), 'a')
		require.True(t, dd.dispatchOp(ctx, req).OK)
		require.Equal(t, sess, look(r).session, "fixture sanity: the live start binds")
		endedAt := core.NowMilli(dd.clk)
		dd.registry.End(sess, endedAt)
		require.NoError(t, dd.svc.SessionEnd(ctx, *req.Event))
		require.Equal(t, sess, look(r).session, "fixture sanity: an end keeps the binding")
		require.False(t, look(r).selected, "fixture sanity: the end's Close turned p-selection off")
		return endedAt
	}
	// endedUnbound registers sess and ends it in the registry alone: a session this daemon saw start
	// and end without the runtime binding it. It returns the end's instant.
	endedUnbound := func(dd *daemon, sess core.SessionID) core.UnixMilli {
		endedAt := core.NowMilli(dd.clk)
		dd.registry.Ensure(&hookio.Event{SessionID: sess, Source: "startup"}, endedAt)
		dd.registry.End(sess, endedAt)
		return endedAt
	}

	t.Run("the resumed session holds the runtime", func(t *testing.T) {
		dd, r := attached(t)
		endedAt := liveThenEnded(t, dd, r, resumed)
		before := look(r)

		// Fired in the end's own millisecond: no earlier than the end, so not a leftover it preceded.
		replay(t, dd, startOf(dd, resumed, "resume", "claude-resumed", endedAt, 'b'))

		after := look(r)
		require.Equal(t, resumed, after.session)
		require.True(t, after.selected, "the resume's replayed start re-opens p-selection, as base's same-id bind did")
		require.Equal(t, "claude-resumed", after.model, "and reads the resume's model hint")
		require.Equal(t, before.anchors, after.anchors, "and moves no anchor to the replay's instant")
	})
	t.Run("another ended session holds the runtime", func(t *testing.T) {
		dd, r := attached(t)
		endedAt := endedUnbound(dd, resumed)
		liveThenEnded(t, dd, r, other)

		replay(t, dd, startOf(dd, resumed, "resume", "claude-resumed", endedAt+1, 'b'))

		after := look(r)
		require.Equal(t, resumed, after.session, "the resumed session takes the runtime from the ended one")
		require.True(t, after.selected, "with p-selection on")
		require.Equal(t, "claude-resumed", after.model)
	})
	t.Run("the runtime is unbound", func(t *testing.T) {
		dd, r := attached(t)
		endedAt := endedUnbound(dd, resumed)

		replay(t, dd, startOf(dd, resumed, "resume", "claude-resumed", endedAt+1, 'b'))

		after := look(r)
		require.Equal(t, resumed, after.session, "the resumed session binds the unbound runtime")
		require.True(t, after.selected)
		require.Zero(t, after.anchors[1], "and notes no activity at the replay's instant")
	})
	t.Run("a start fired before the end", func(t *testing.T) {
		dd, r := attached(t)
		endedAt := liveThenEnded(t, dd, r, resumed)
		before := look(r)

		replay(t, dd, startOf(dd, resumed, "startup", "claude-leftover", endedAt-1, 'b'))

		require.Equal(t, before, look(r), "a leftover start the end preceded changes nothing")
	})
	t.Run("a start a later live start of the session superseded", func(t *testing.T) {
		dd, r := attached(t)
		first := startOf(dd, resumed, "startup", "claude-first", core.NowMilli(dd.clk), 'a')
		require.True(t, dd.dispatchOp(ctx, first).OK)
		require.True(t, dd.dispatchOp(ctx, startOf(dd, resumed, "resume", "claude-second", first.TS+1, 'c')).OK)
		before := look(r)
		require.Equal(t, "claude-second", before.model, "fixture sanity: the later live start's hint is read")

		replay(t, dd, first) // the first start's spooled copy, after the second start was handled

		require.Equal(t, before, look(r), "a superseded start's hints do not overwrite the newer start's")
	})
	t.Run("a start fired before the end, while another ended session holds the runtime", func(t *testing.T) {
		dd, r := attached(t)
		endedAt := endedUnbound(dd, resumed)
		liveThenEnded(t, dd, r, other)
		before := look(r)

		replay(t, dd, startOf(dd, resumed, "startup", "claude-leftover", endedAt-1, 'b'))

		require.Equal(t, before, look(r), "a leftover start does not take the runtime")
	})
}
