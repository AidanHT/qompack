package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/store"
)

// The startup publication pass steps aside for capture work (V6 close-out D51): it runs between hook
// requests, never beside one, so a session's I/O does not pay for it. captureGate is what it waits
// on; these tests pin the gate itself, that the request paths hold it, and that the pass pauses on
// it and then finishes with the answer it would have given unpaused. None of them sleeps: a paused
// pass is observed through the gate's park signal, and a running one through the gate's count.

// parkSignal installs a park observer on g and returns the channel it signals, one send per park.
// It is installed before anything can wait on g.
func parkSignal(g *captureGate) <-chan struct{} {
	return parkSignalWhen(g, func(context.Context) bool { return true })
}

// backgroundParkSignal is parkSignal for the background half of the startup pass only. The bounded
// half runs under a context with a deadline (publicationBound) and parks too; the background half
// runs under the daemon's run context, which has none. Telling them apart by what they are, not by
// when they park, loses no park whatever order the two goroutines run in.
func backgroundParkSignal(g *captureGate) <-chan struct{} {
	return parkSignalWhen(g, func(ctx context.Context) bool {
		_, bounded := ctx.Deadline()
		return !bounded
	})
}

func parkSignalWhen(g *captureGate, match func(context.Context) bool) <-chan struct{} {
	parked := make(chan struct{}, 64)
	g.mu.Lock()
	g.onPark = func(ctx context.Context) {
		if !match(ctx) {
			return
		}
		select {
		case parked <- struct{}{}:
		default:
		}
	}
	g.mu.Unlock()
	return parked
}

// TestCaptureGate_WaitReturnsOnlyOnceTheWorkInFlightEnds: a wait with work in flight parks, stays
// parked while the work runs, and returns nil the moment the last of it ends.
func TestCaptureGate_WaitReturnsOnlyOnceTheWorkInFlightEnds(t *testing.T) {
	var g captureGate
	parked := parkSignal(&g)
	require.NoError(t, g.wait(context.Background()), "no work in flight: the wait returns at once")

	g.enter()
	g.enter()
	done := make(chan error, 1)
	go func() { done <- g.wait(context.Background()) }()
	<-parked

	g.leave()
	select {
	case err := <-done:
		t.Fatalf("the wait returned (%v) while one piece of work was still in flight", err)
	default:
	}
	g.leave()
	require.NoError(t, <-done)
	require.Zero(t, g.inFlight())
}

// TestCaptureGate_WaitEndsWithItsContext: a parked wait answers its context's end, so a daemon's Stop
// is never held up by a request that outlives it.
func TestCaptureGate_WaitEndsWithItsContext(t *testing.T) {
	var g captureGate
	parked := parkSignal(&g)
	g.enter()
	defer g.leave()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.wait(ctx) }()
	<-parked
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

// newGateDaemon builds a daemon through New whose ObserveTool seam runs observe, for the tests that
// look inside a request while it is being served.
func newGateDaemon(t *testing.T, observe func()) *daemon {
	t.Helper()
	o := Options{ProjectRoot: t.TempDir(), Cfg: testConfig(), Log: logging.Nop()}
	o.Bind(func(s *Services) {
		s.ObserveTool = func(context.Context, hookio.Event) error {
			observe()
			return nil
		}
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	return dd
}

// TestDispatchOp_HoldsTheCaptureGateWhileServing: every live request counts as capture work for as
// long as dispatchOp serves it, and no longer.
func TestDispatchOp_HoldsTheCaptureGateWhileServing(t *testing.T) {
	var during int
	dd := newGateDaemon(t, func() {})
	dd.routes[ipc.OpStatus] = func(context.Context, ipc.Request) ipc.Response {
		during = dd.capture.inFlight()
		return ipc.Response{OK: true}
	}
	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpStatus})
	require.True(t, resp.OK)
	require.Equal(t, 1, during, "the request is in flight while its route runs")
	require.Zero(t, dd.capture.inFlight(), "and not once it has been answered")
}

// TestRunIngested_HoldsTheCaptureGateWhileApplying: the delivery a worker or a drain applies after
// the hook has had its ACK is capture work too, so the pass does not run beside it either.
func TestRunIngested_HoldsTheCaptureGateWhileApplying(t *testing.T) {
	var during int
	var dd *daemon
	dd = newGateDaemon(t, func() { during = dd.capture.inFlight() })
	req := observeRequest(testDeliveryToken('7'), "gate-ingested", `{"hook_event_name":"PostToolUse"}`)
	resp := dd.runIngested(context.Background(), req)
	require.True(t, resp.OK, resp.Err)
	require.Equal(t, 1, during, "the observation is applied inside the gate")
	require.Zero(t, dd.capture.inFlight())
}

// The work a request leaves running past its answer holds the gate from before the request has
// left until the work ends, so the pass cannot slip in between the answer and the work
// (startPromptRecording, startReplyWork, launchSessionEnd). Each test blocks the launched work,
// reads the gate once the launcher (or the request) has returned, then releases the work and
// reads it again once the work has been joined.

// TestStartPromptRecording_HoldsTheCaptureGateUntilTheCaptureEnds: the verbatim prompt capture an
// observe.prompt request leaves running is capture work until it ends.
func TestStartPromptRecording_HoldsTheCaptureGateUntilTheCaptureEnds(t *testing.T) {
	dd := newGateDaemon(t, func() {})
	release := make(chan struct{})
	started := dd.startPromptRecording(context.Background(), func(context.Context) { <-release })
	require.True(t, started)
	require.Equal(t, 1, dd.capture.inFlight(),
		"the capture holds the gate from before its launcher returns, so the request's own leave leaves no gap")
	close(release)
	dd.promptWG.Wait()
	require.Zero(t, dd.capture.inFlight(), "and releases it once it has ended")
}

// TestStartReplyWork_HoldsTheCaptureGateUntilTheWorkEnds: the compact SessionStart work a
// session.start request leaves running is capture work until it ends.
func TestStartReplyWork_HoldsTheCaptureGateUntilTheWorkEnds(t *testing.T) {
	dd := newGateDaemon(t, func() {})
	release := make(chan struct{})
	started := dd.startReplyWork(context.Background(), "gate test", func(context.Context) { <-release }, nil)
	require.True(t, started)
	require.Equal(t, 1, dd.capture.inFlight(),
		"the work holds the gate from before its launcher returns, so the request's own leave leaves no gap")
	close(release)
	dd.promptWG.Wait()
	require.Zero(t, dd.capture.inFlight(), "and releases it once it has ended")
}

// TestLaunchSessionEnd_HoldsTheCaptureGateUntilTheEndFinishes: the session end a flush request
// leaves running (launchSessionEnd) is capture work until it finishes, though the flush has long
// been answered.
func TestLaunchSessionEnd_HoldsTheCaptureGateUntilTheEndFinishes(t *testing.T) {
	dd, hold, root := flushAsyncDaemon(t)
	const sess core.SessionID = "sess-gate-end"
	resp := flushAsyncDispatch(t, dd, flushAsyncRequest(dd, root, sess, orderNonce(90)))
	require.True(t, resp.OK, resp.Err)
	require.GreaterOrEqual(t, dd.capture.inFlight(), 1,
		"the answered flush's session end holds the gate from before the request left")
	select {
	case <-hold.entered:
	case <-hangGuard(t):
		require.FailNow(t, "the flush's session end never reached SessionEnd")
	}
	require.Equal(t, 1, dd.capture.inFlight(), "the end, held in SessionEnd, is the one piece of capture work")
	hold.open()
	flushAsyncAwait(t, dd)
	require.Zero(t, dd.capture.inFlight(), "and releases it once it has finished")
}

// gateAtDispatch installs dd's drainer with its Dispatch wrapped to record the capture work in flight
// just before each delivery is handed to the daemon: outside runIngested, where the drain has done
// its own I/O for the line (the read of the spool, the lease journal's fsynced record). The drain
// itself is capture work there too, or the pass could run beside that I/O between deliveries.
func gateAtDispatch(dd *daemon) *[]int {
	var seen []int
	cfg := dd.drainConfig()
	dispatch := cfg.Dispatch
	cfg.Dispatch = func(ctx context.Context, req ipc.Request) ipc.Response {
		seen = append(seen, dd.capture.inFlight())
		return dispatch(ctx, req)
	}
	dd.drain.Store(newDrainer(cfg))
	return &seen
}

// TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries: the drain a lane asks for after a
// hook's ACK (drainOnRequest) is capture work for the whole pass, not only inside each delivery.
func TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	seen := gateAtDispatch(dd)
	first := liveOrderTool(dd, root, "sess-gate-requested-a", 1)
	second := liveOrderTool(dd, root, "sess-gate-requested-b", 2)
	writeHookSpool(t, root, "client-9301.ndjson", first)
	writeHookSpool(t, root, "client-9302.ndjson", second)

	dd.requestedDrainPass(context.Background())
	require.True(t, spoolWatchPublished(dd, first.Nonce), "fixture: the pass published the first spool")
	require.True(t, spoolWatchPublished(dd, second.Nonce), "fixture: the pass published the second spool")
	require.Len(t, *seen, 2)
	for i, n := range *seen {
		require.Equal(t, 1, n, "delivery %d: the drain holds the gate between deliveries", i)
	}
	require.Zero(t, dd.capture.inFlight(), "and releases it once the pass has ended")
}

// TestLookAtClientSpools_HoldsTheCaptureGateForItsPass: the client-spool pass the watcher runs after
// a hook spooled (watchClientSpools) is capture work for the whole pass as well.
func TestLookAtClientSpools_HoldsTheCaptureGateForItsPass(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	seen := gateAtDispatch(dd)
	req := liveOrderTool(dd, root, "sess-gate-watched", 3)
	writeHookSpool(t, root, "client-9303.ndjson", req)

	ctx := context.Background()
	entries := map[string]*spoolWatchEntry{}
	now := time.Now()
	dd.lookAtClientSpools(ctx, entries, true, now) // seen for the first time: not settled yet
	dd.lookAtClientSpools(ctx, entries, false, now.Add(spoolWatchTick))
	require.True(t, spoolWatchPublished(dd, req.Nonce), "fixture: the second look's pass published the spool")
	require.Equal(t, []int{1}, *seen, "the watcher's pass holds the gate around its delivery")
	require.Zero(t, dd.capture.inFlight(), "and releases it once the pass has ended")
}

// passFinished closes once every goroutine accountPublicationAtStartup started has returned, the
// join Stop does. accountPublicationAtStartup has added them all by the time it returns.
func passFinished(d *daemon) <-chan struct{} {
	finished := make(chan struct{})
	go func() {
		d.runWG.Wait()
		close(finished)
	}()
	return finished
}

// TestStartupPublicationAccounting_BackgroundPassPausesWhileARequestIsInFlight is D51's pause: with a
// request in flight the background pass parks at its next unit of I/O and cannot finish, and once the
// request ends it resumes and announces exactly the gap an unpaused pass announces.
func TestStartupPublicationAccounting_BackgroundPassPausesWhileARequestIsInFlight(t *testing.T) {
	d, root, mp := newAuditDaemon(t)
	m := *mp
	d.publicationBound = time.Nanosecond
	seedLiveRunSizedStore(t, d, root)
	id := core.ObservationID(core.HashBytes("daemon.audit.obs", []byte("stage-one")).String())
	require.NoError(t, store.WriteCaptureSidecar(root, store.CaptureSidecar{
		ObservationID: id, Session: "sess", Op: "observe.tool", Published: false,
		Outcome: core.OutcomeOK, Bytes: []byte("captured tool result"),
	}))
	backdateCaptures(t, root)

	var loud loudCapture
	loud.attach(t)
	parked := backgroundParkSignal(&d.capture)

	d.capture.enter() // a hook request is being served
	d.accountPublicationAtStartup(context.Background())
	// Only the background pass's parks are signalled (it runs under a context with no deadline). A
	// background pass that does not yield never parks, so it finishes beside the request and fails
	// below.
	finished := passFinished(d)
	select {
	case <-parked:
	case <-finished:
		t.Fatal("the background publication pass finished beside a request in flight")
	case <-hangGuard(t):
		t.Fatal("the background publication pass neither parked nor finished")
	}
	// The pass is parked on the gate's idle channel, which only the request's leave closes, so it
	// cannot have finished: this is the pause itself, not a race with a walk still running.
	select {
	case <-finished:
		t.Fatal("the background publication pass finished beside a request in flight")
	default:
	}
	require.Zero(t, m.Counter(counterPublicationUnpublishedCaptures).Value(),
		"nothing is announced while the pass is paused")

	d.capture.leave() // the request is answered
	<-finished
	require.EqualValues(t, 1, m.Counter(counterPublicationContinued).Value())
	require.EqualValues(t, 1, m.Counter(counterPublicationUnpublishedCaptures).Value(),
		"the resumed pass finds the one gap the store holds")
	require.Zero(t, m.Counter(counterPublicationIncomplete).Value(), "and finishes")
	require.True(t, loud.contains("unpublished captures or unindexed objects"))
}

// TestStartupPublicationAccounting_StopEndsAPausedPass: a pass parked behind a request that outlives
// the daemon's stop is ended by the stop, quietly, as any stopped pass is.
func TestStartupPublicationAccounting_StopEndsAPausedPass(t *testing.T) {
	d, root, mp := newAuditDaemon(t)
	m := *mp
	d.publicationBound = time.Nanosecond
	seedLiveRunSizedStore(t, d, root)

	var loud loudCapture
	loud.attach(t)
	parked := backgroundParkSignal(&d.capture) // only the background pass's parks, as above

	d.capture.enter()
	defer d.capture.leave()
	runCtx, cancel := context.WithCancel(context.Background())
	d.accountPublicationAtStartup(runCtx)
	finished := passFinished(d)
	select {
	case <-parked:
	case <-finished:
		t.Fatal("the background publication pass finished beside a request in flight")
	case <-hangGuard(t):
		t.Fatal("the background publication pass neither parked nor finished")
	}
	cancel()
	<-finished

	require.False(t, loud.contains("publication accounting"),
		"a pass the daemon's stop ended is not a scan that cannot finish")
	require.EqualValues(t, 1, m.Counter(counterPublicationIncomplete).Value(),
		"the stopped pass is counted as not finished")
}

// TestDispatchOp_HoldsTheCaptureGateForEveryHotPathOp (D53(d), X11's question): every op the hot path
// sends — observe.tool, observe.stop and observe.prompt, each from its own hook process, back to back
// — is capture work while dispatchOp serves it, so the startup publication pass never starts a unit
// of I/O beside one. A second hook arriving while the first is still served adds to the count rather
// than handing the pass a gap.
func TestDispatchOp_HoldsTheCaptureGateForEveryHotPathOp(t *testing.T) {
	dd := newGateDaemon(t, func() {})
	during := map[ipc.Op]int{}
	for _, op := range []ipc.Op{ipc.OpObserveTool, ipc.OpObserveStop, ipc.OpObservePrompt} {
		require.True(t, op.HotPath())
		dd.routes[op] = func(_ context.Context, req ipc.Request) ipc.Response {
			during[req.Op] = dd.capture.inFlight()
			return ipc.Response{OK: true}
		}
	}
	for _, op := range []ipc.Op{ipc.OpObserveTool, ipc.OpObserveStop, ipc.OpObservePrompt, ipc.OpObserveTool} {
		dd.dispatchOp(context.Background(), ipc.Request{Op: op, Session: "gate-hot", TS: core.NowMilli(dd.clk)})
		require.Equal(t, 1, during[op], "%s is in flight while its route runs", op)
		require.Zero(t, dd.capture.inFlight(), "and not once it has been answered")
	}

	// Two hooks overlapping: the second arrives while the first is still being served.
	inner := false
	dd.routes[ipc.OpObserveStop] = func(context.Context, ipc.Request) ipc.Response {
		if !inner {
			inner = true
			dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpObserveTool, Session: "gate-hot"})
		}
		return ipc.Response{OK: true}
	}
	dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpObserveStop, Session: "gate-hot"})
	require.Equal(t, 2, during[ipc.OpObserveTool], "the overlapping second hook counts on top of the first")
	require.Zero(t, dd.capture.inFlight())
}

// TestStartupPublicationAccounting_APausedPassHoldsNothingARequestNeeds (D53(d)): while the background
// pass is parked behind a request, it holds no store lock and blocks no write the request makes into
// the very trees it walks: a request can put objects, index a tool use and write a capture sidecar,
// and each finishes while the pass stays parked. A pass that kept the store's read lock, or a handle
// that refused the writes, across its pause would hang this row instead.
func TestStartupPublicationAccounting_APausedPassHoldsNothingARequestNeeds(t *testing.T) {
	d, root, mp := newAuditDaemon(t)
	m := *mp
	d.publicationBound = time.Nanosecond
	seedLiveRunSizedStore(t, d, root)
	backdateCaptures(t, root)
	parked := backgroundParkSignal(&d.capture)

	d.capture.enter() // a hook request is being served
	d.accountPublicationAtStartup(context.Background())
	finished := passFinished(d)
	select {
	case <-parked:
	case <-finished:
		t.Fatal("the background publication pass finished beside a request in flight")
	case <-hangGuard(t):
		t.Fatal("the background publication pass neither parked nor finished")
	}

	wrote := make(chan error, 1)
	go func() {
		ctx := context.Background()
		res, err := d.svc.Store.PutBytes(ctx, []byte("a tool result the request stores while the pass is parked"),
			store.PutOptions{Tool: "Read", Path: "src/parked.go"})
		if err == nil {
			err = d.svc.Store.RecordToolUse(ctx, store.ToolUseRecord{
				ID: "toolu_while_parked", Session: "sess", Turn: 1, TS: 1, Tool: "Read",
				Root: res.Root.Hash, Path: "src/parked.go", Bytes: res.Root.RawBytes,
			})
		}
		if err == nil {
			err = store.WriteCaptureSidecar(root, store.CaptureSidecar{
				ObservationID: core.ObservationID(core.HashBytes("daemon.audit.obs", []byte("while-parked")).String()),
				Session:       "sess", Op: "observe.tool", Published: true, Outcome: core.OutcomeOK,
				Bytes: []byte("captured while the pass is parked"),
			})
		}
		wrote <- err
	}()
	select {
	case err := <-wrote:
		require.NoError(t, err)
	case <-hangGuard(t):
		t.Fatal("a request's store writes waited on the parked publication pass")
	}
	select {
	case <-finished:
		t.Fatal("the background publication pass finished beside a request in flight")
	default:
	}

	d.capture.leave()
	<-finished
	require.Zero(t, m.Counter(counterPublicationUnpublishedCaptures).Value(),
		"what the request wrote after the snapshot is live work, not a gap")
	require.Zero(t, m.Counter(counterPublicationIncomplete).Value(), "and the resumed pass finishes")
}
