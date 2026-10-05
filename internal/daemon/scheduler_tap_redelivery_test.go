package daemon

// Delivery is at least once (ingest.dispatch's doc comment, DrainConfig.Seen): the ingest worker
// runs the handler, and the scheduler tap inside it, BEFORE it commits the delivery's frontier
// record, so a commit that fails — a Stop's runCancel landing in it, a bounded drain's context
// ending in it — leaves the WAL line for a drain to replay through the same handler. The observer
// absorbs such a replay (observer.redelivery_absorbed); the tap folded it a second time: the open
// segment's tokens, the detector's posterior and the request-start anchor all took the same
// delivery twice, and the doubled account was persisted (V6 close-out wave 20, C1.x; the 8-vs-12
// red of TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook).
//
// These rows pin "one delivery, one application" at the tap, across a restart, and through the
// real worker and drain with the commit cut where Stop cuts it. They also pin the one effect a replay
// must still have: the segment close its cut run owed and did not make. None waits on a clock: the
// cut is a context cancelled between the handler and the commit, which is the exact interleaving Stop
// makes.

import (
	"context"
	"encoding/json"
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
	cfg := contentDrainConfig(dd)
	dispatch := cfg.Dispatch
	cfg.Dispatch = func(c context.Context, got ipc.Request) ipc.Response {
		resp := dispatch(c, got)
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
