package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
)

// V6 close-out C1.1, review round. The lanes (delivery_order.go) made same-session leased
// deliveries publish in arrival order without a drain, and an independent review then found four
// ways work could still strand or be closed over silently:
//
//   - one session whose head never publishes could fill the lanes' shared bound, after which every
//     other session's live jobs were refused and left for a drain (F1);
//   - a job the lanes or the ring could not hold, and a lane parked behind a predecessor only the
//     WAL holds, waited for a flush, admin.drain, a restart or 120 s of project-wide idleness (F2, F8);
//   - a flush over an unreadable committed frontier ran SessionEnd ahead of the session's
//     deliveries without counting or announcing it (F3, F5);
//   - the flush waited a fixed 5 s however steadily the session's backlog was publishing (F4).

// laneTestSetLanes gives dd's ingest lanes of the given total and per-session capacity.
func laneTestSetLanes(dd *daemon, capacity, perSession int) {
	dd.ing.lanes = newDispatchLanes(capacity, perSession)
}

// laneTestStartDrainRequests runs the daemon's drain requester (Run starts the same one) until the
// test ends, and joins it before the cleanups registered earlier close the store and the lock.
func laneTestStartDrainRequests(t *testing.T, dd *daemon) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		dd.drainOnRequest(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// laneTestSetSettle sets how long a flush waits for its session: stall without one of the lane's
// jobs settling, and limit in all.
func laneTestSetSettle(dd *daemon, stall, limit time.Duration) {
	dd.ing.settleStall = stall
	dd.ing.settleLimit = limit
}

// laneTestDaemon is wireTestDaemon holding the delivery lock, with its prompt recordings stopped at
// cleanup, as every live-order test sets it up.
func laneTestDaemon(t *testing.T) (*daemon, *Options, string) {
	t.Helper()
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})
	return dd, o, root
}

// TestDeliveryOrder_AStuckSessionCannotTakeTheOtherSessionsLanes (F1): a session whose head never
// publishes keeps its later arrivals queued behind that head. They must not take the lanes' whole
// bound: a session holds at most its own share, the rest of its jobs stay in the WAL, and another
// session's arrivals still publish live, with no drain. Once the stuck session's flush has run
// SessionEnd, its parked lane is released too — its jobs are the WAL's, and a lane nothing will
// run again must not keep holding them for the rest of the daemon's life.
func TestDeliveryOrder_AStuckSessionCannotTakeTheOtherSessionsLanes(t *testing.T) {
	dd, o, root := laneTestDaemon(t)
	const stuck, other core.SessionID = "sess-stuck", "sess-other"
	const capacity, perSession, stuckArrivals = 4, 2, 6
	laneTestSetLanes(dd, capacity, perSession)
	run := func(ctx context.Context, req ipc.Request) ipc.Response {
		if req.Session == stuck && req.Event != nil && req.Event.Prompt == "p0" {
			return ipc.Response{Err: "injected: this head never publishes"}
		}
		return dd.runIngested(ctx, req)
	}
	liveOrderWorkers(t, dd, 2, run)

	stuckLeases := make([]deliveryLease, stuckArrivals)
	for i := range stuckArrivals {
		req := spD3Prompt(dd, root, stuck, orderNonce(i), fmt.Sprintf("p%d", i))
		acceptPrompt(t, dd, req)
		stuckLeases[i] = liveOrderLease(t, dd, req.Nonce)
	}
	require.Eventually(t, func() bool {
		queued, running := liveOrderLane(dd, stuck)
		return len(dd.ing.ring) == 0 && queued > 0 && !running
	}, liveOrderBound, liveOrderTick, "the stuck session's lane parks on its failing head")

	var otherLeases []deliveryLease
	for i := range 2 {
		req := spD3Prompt(dd, root, other, orderNonce(10+i), fmt.Sprintf("p%d", i))
		acceptPrompt(t, dd, req)
		otherLeases = append(otherLeases, liveOrderLease(t, dd, req.Nonce))
	}
	require.Eventually(t, func() bool { return liveOrderAcked(dd, otherLeases) == len(otherLeases) },
		liveOrderBound, liveOrderTick, "another session publishes live beside a stuck one: %s",
		liveOrderDiag{dd, otherLeases})
	liveOrderRequireTurns(t, o, other, len(otherLeases))

	queued, _ := liveOrderLane(dd, stuck)
	require.LessOrEqual(t, queued, perSession, "a session's lane never holds more than its share")
	require.Zero(t, liveOrderAcked(dd, stuckLeases), "nothing of the stuck session publishes past its head")
	require.Equal(t, int64(stuckArrivals-perSession), dd.m.Counter(counterOrderingLaneFull).Value(),
		"every stuck arrival past its share is refused, counted and left in the WAL")

	laneTestSetSettle(dd, liveOrderTick, liveOrderTick)
	resp := dd.flushRoute(context.Background(), liveOrderFlush(dd, root, stuck), false)
	require.True(t, resp.OK, resp.Err)
	require.Equal(t, int64(1), dd.m.Counter(counterFlushUnsettled).Value(),
		"the stuck session's SessionEnd ran ahead of its deliveries, and says so")
	queued, running := liveOrderLane(dd, stuck)
	require.Zero(t, queued, "the ended session's parked lane gives its jobs back to the WAL")
	require.False(t, running)
	dd.ing.lanes.mu.Lock()
	held := dd.ing.lanes.held
	dd.ing.lanes.mu.Unlock()
	require.Zero(t, held)
}

// TestDeliveryOrder_ParkedLaneAsksForADrainRatherThanWaitingForIdle (F2): a live successor whose
// predecessor never reached the worker pool — here the ring dropped it, as a full ring does, so it
// exists only as a WAL line and a lease — parks behind it. Nothing live can publish that
// predecessor, and the daemon's own drains run only on a flush, admin.drain, a restart or after
// DetectAfterSeconds (120 s) of project-wide idleness. The parked lane must ask for a drain itself:
// the drain publishes the predecessor from the WAL, and the session completes without the test (or
// an operator) draining anything.
func TestDeliveryOrder_ParkedLaneAsksForADrainRatherThanWaitingForIdle(t *testing.T) {
	dd, o, root := laneTestDaemon(t)
	const sess core.SessionID = "sess-parked-asks"
	dd.drain.Store(newDrainer(contentDrainConfig(dd)))

	first := spD3Prompt(dd, root, sess, orderNonce(0), "p0")
	acceptPrompt(t, dd, first)
	dropped := <-dd.ing.ring // the ring's drop: the WAL line and the lease are all that remain
	require.Equal(t, first.Nonce, dropped.req.Nonce)
	second := spD3Prompt(dd, root, sess, orderNonce(1), "p1")
	acceptPrompt(t, dd, second)
	leases := []deliveryLease{liveOrderLease(t, dd, first.Nonce), liveOrderLease(t, dd, second.Nonce)}

	liveOrderWorkers(t, dd, 2, dd.runIngested)
	laneTestStartDrainRequests(t, dd)

	require.Eventually(t, func() bool { return liveOrderAcked(dd, leases) == len(leases) }, liveOrderBound, liveOrderTick,
		"the parked lane never got the drain its predecessor needs: %s", liveOrderDiag{dd, leases})
	liveOrderRequireTurns(t, o, sess, len(leases))
	require.Equal(t, int64(1), dd.m.Counter(counterOrderingDeferred).Value(),
		"the successor was deferred once, then parked; the drain, not a retry loop, released it")
}

// TestDeliveryOrder_LaneOverflowIsDrainedOnRequest (F8): jobs the session's lane could not hold are
// refused while the lane is busy — durable in the WAL, counted — and once the lane has published what
// it did hold and runs dry, it asks for the drain those refused jobs need. They publish, in arrival
// order after the ones the lane held, without anyone draining.
func TestDeliveryOrder_LaneOverflowIsDrainedOnRequest(t *testing.T) {
	dd, o, root := laneTestDaemon(t)
	const sess core.SessionID = "sess-overflow-asks"
	const perSession, k = 2, 5
	laneTestSetLanes(dd, laneCapacity, perSession)
	dd.drain.Store(newDrainer(contentDrainConfig(dd)))
	run, open := liveOrderPromptGate(t, dd.runIngested, "p0")
	liveOrderWorkers(t, dd, 2, run)
	laneTestStartDrainRequests(t, dd)
	t.Cleanup(open)

	leases := make([]deliveryLease, k)
	for i := range k {
		req := spD3Prompt(dd, root, sess, orderNonce(i), fmt.Sprintf("p%d", i))
		acceptPrompt(t, dd, req)
		leases[i] = liveOrderLease(t, dd, req.Nonce)
	}
	require.Eventually(t, func() bool { return dd.m.Counter(counterOrderingLaneFull).Value() == k-perSession },
		liveOrderBound, liveOrderTick, "every job past the session's share is refused while its lane is busy")

	open()
	require.Eventually(t, func() bool { return liveOrderAcked(dd, leases) == k }, liveOrderBound, liveOrderTick,
		"the refused jobs were never drained: %s", liveOrderDiag{dd, leases})
	liveOrderRequireTurns(t, o, sess, k)
}

// TestDeliveryOrder_ARequestedDrainCutShortByItsBudgetIsRequestedAgain (F8 under load): the drain
// the lanes ask for runs under idleRunBudget, and on a loaded host its pass can run out of that
// budget before it has published every job the lanes refused. Nothing asked again: the lane had run
// dry and dropped its overflow when it asked, and the cut pass's release woke no lane, because the
// session had none left. The refused jobs then waited for the session's next arrival, a flush or
// DetectAfterSeconds of project-wide idleness — the waits F8 exists to remove. Under -race and CPU
// co-load on Linux TestDeliveryOrder_LaneOverflowIsDrainedOnRequest met exactly that one run in
// six: one requested pass, one line published, "context deadline exceeded" after 2.26 s, and the
// last refused job never drained. Here the drain's first attempt at the first refused job takes
// longer than the whole pass budget, as a publication on such a host does, so the pass has no budget
// left for the other two. The requester must ask for another pass, and the rest must publish in
// arrival order without anyone draining.
func TestDeliveryOrder_ARequestedDrainCutShortByItsBudgetIsRequestedAgain(t *testing.T) {
	dd, o, root := laneTestDaemon(t)
	const sess core.SessionID = "sess-overflow-cut-short"
	const perSession, k = 2, 5
	laneTestSetLanes(dd, laneCapacity, perSession)
	cfg := lineDeadlineDrainConfig(dd)
	dispatch := cfg.Dispatch
	var cut atomic.Bool
	cfg.Dispatch = func(ctx context.Context, req ipc.Request) ipc.Response {
		if req.Event != nil && req.Event.Prompt == "p2" && cut.CompareAndSwap(false, true) {
			// Slower than the whole pass budget: whatever the pass does with this line, it has no
			// budget left for the next one.
			timer := time.NewTimer(idleRunBudget + time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return ipc.Response{Err: ctx.Err().Error()}
			}
			// The stall has spent 3 s of the line's 5 s drainLineDeadline. The real publication
			// that follows runs without it: under it, a host that needs more than the 2 s left
			// cuts the line with nothing published, and passLeftWork rightly does not ask again
			// after a pass that made no progress, so the row stranded whatever its bound (D73(2)).
			// The row is about the pass budget, which the stall alone spends.
			return withoutLineDeadline(dispatch)(ctx, req)
		}
		return dispatch(ctx, req)
	}
	dd.drain.Store(newDrainer(cfg))
	run, open := liveOrderPromptGate(t, dd.runIngested, "p0")
	liveOrderWorkers(t, dd, 2, run)
	laneTestStartDrainRequests(t, dd)
	t.Cleanup(open)

	leases := make([]deliveryLease, k)
	for i := range k {
		req := spD3Prompt(dd, root, sess, orderNonce(i), fmt.Sprintf("p%d", i))
		acceptPrompt(t, dd, req)
		leases[i] = liveOrderLease(t, dd, req.Nonce)
	}
	require.Eventually(t, func() bool { return dd.m.Counter(counterOrderingLaneFull).Value() == k-perSession },
		liveOrderBound, liveOrderTick, "every job past the session's share is refused while its lane is busy")

	open()
	require.Eventually(t, func() bool { return liveOrderAcked(dd, leases) == k }, liveOrderBound, liveOrderTick,
		"the refused jobs a pass cut short by its budget left were never drained: %s", liveOrderDiag{dd, leases})
	require.True(t, cut.Load(), "the requested pass met the slow publication and ran out of its budget")
	require.Equal(t, int64(1), dd.m.Counter(counterOrderingDrainRequested).Value(),
		"the lanes asked once; the second pass is the requester's own, for the pass it saw cut short")
	liveOrderRequireTurns(t, o, sess, k)
}

// TestDeliveryOrder_ARequestedPassFinishesALineSlowerThanItsBudget (F8 under heavier load): asking
// again is not enough when one line's publication takes longer than the whole pass budget. The
// budget cancelled the line it was publishing, every pass, so each pass it asked for again cut the
// same line and nothing ever published: with CPU and fsync co-load on top of two other gate runs,
// TestDeliveryOrder_LaneOverflowIsDrainedOnRequest failed 4 times in 16 at 4 of 5 acknowledged, its
// log showing pass after pass end "context deadline exceeded" after about 2.6 s with the same prompt
// capture "not durable" each time. The daemon's own worst case for one line is drainLineDeadline,
// longer than the pass budget (idleRunBudget). Here every attempt at the second refused job takes a
// second longer than the whole budget and well inside drainLineDeadline, as such a publication does;
// a requested pass must give a line it has started its own deadline, and stop starting lines once
// its budget is spent. The row counts the attempts cancelled inside their slow part: the pass budget
// did that to every attempt, and a line's own deadline, which ends after it, cannot. The real
// publication after the slow part runs without the line's deadline (withoutLineDeadline): under it, a
// loaded host that needed more than the 2 s the slow part left cut the line with nothing published,
// and a pass that publishes nothing is not asked again (passLeftWork), so the row stranded (D73(2)).
func TestDeliveryOrder_ARequestedPassFinishesALineSlowerThanItsBudget(t *testing.T) {
	dd, o, root := laneTestDaemon(t)
	const sess core.SessionID = "sess-overflow-slow-line"
	const perSession, k = 2, 5
	slow := idleRunBudget + time.Second // longer than a pass's budget, inside drainLineDeadline
	require.Less(t, slow, drainLineDeadline, "fixture: the slow line must fit its own deadline")
	laneTestSetLanes(dd, laneCapacity, perSession)
	cfg := lineDeadlineDrainConfig(dd)
	dispatch := cfg.Dispatch
	var attempts, cutInside atomic.Int32
	cfg.Dispatch = func(ctx context.Context, req ipc.Request) ipc.Response {
		if req.Event != nil && req.Event.Prompt == "p3" {
			attempts.Add(1)
			timer := time.NewTimer(slow)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				cutInside.Add(1)
				return ipc.Response{Err: ctx.Err().Error()}
			}
			// The real publication after the slow part runs without the line's deadline, for the
			// reason the row above gives (D73(2)): the slow part is what this row prices.
			return withoutLineDeadline(dispatch)(ctx, req)
		}
		return dispatch(ctx, req)
	}
	dd.drain.Store(newDrainer(cfg))
	run, open := liveOrderPromptGate(t, dd.runIngested, "p0")
	liveOrderWorkers(t, dd, 2, run)
	laneTestStartDrainRequests(t, dd)
	t.Cleanup(open)

	leases := make([]deliveryLease, k)
	for i := range k {
		req := spD3Prompt(dd, root, sess, orderNonce(i), fmt.Sprintf("p%d", i))
		acceptPrompt(t, dd, req)
		leases[i] = liveOrderLease(t, dd, req.Nonce)
	}
	require.Eventually(t, func() bool { return dd.m.Counter(counterOrderingLaneFull).Value() == k-perSession },
		liveOrderBound, liveOrderTick, "every job past the session's share is refused while its lane is busy")

	open()
	require.Eventually(t, func() bool { return liveOrderAcked(dd, leases) == k }, liveOrderBound, liveOrderTick,
		"a refused job slower than a pass's budget was never drained: %s", liveOrderDiag{dd, leases})
	require.Positive(t, attempts.Load(), "fixture: the drain met the slow line")
	require.Zero(t, cutInside.Load(),
		"no pass cancelled the slow line inside its slow part: the budget never cuts a line it started")
	liveOrderRequireTurns(t, o, sess, k)
}

// TestDeliveryOrder_ARequestedPassAsksAgainOnlyWhileItMakesProgress: the requester asks for another
// pass itself after a pass that stopped with work left, because nothing else asks for it. It asked
// again after every pass that ended "deadline exceeded", and a line that runs out of its own
// drainLineDeadline ends a pass that way too: a line whose dispatch never finishes then made the
// requester run pass after pass for as long as the daemon lived, each spending the line's whole
// deadline under the drain's mutex, where before this branch it stopped after one. A pass stopped by
// its budget has consumed a line, and a pass a line's deadline stopped asks again only if it
// published a line first, so every pass the requester asks for itself follows one that made progress.
// Here a line that never finishes follows one that publishes: the first pass publishes and asks
// again; the next meets only the line that never finishes, and must not.
func TestDeliveryOrder_ARequestedPassAsksAgainOnlyWhileItMakesProgress(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ahead := liveOrderTool(dd, root, "sess-requested-ahead", 1)
	wedged := liveOrderTool(dd, root, "sess-requested-wedged", 2)
	cfg := lineDeadlineDrainConfig(dd)
	dispatch := cfg.Dispatch
	var attempts atomic.Int32
	cfg.Dispatch = func(ctx context.Context, req ipc.Request) ipc.Response {
		if req.Nonce == wedged.Nonce {
			attempts.Add(1)
			<-ctx.Done() // never finishes: only the line's own deadline ends it
			return ipc.Response{Err: ctx.Err().Error()}
		}
		return dispatch(ctx, req)
	}
	dd.drain.Store(newDrainer(cfg))
	// Pass order is lexical: the line that publishes first, then the one that never finishes.
	writeHookSpool(t, root, "client-8501.ndjson", ahead)
	writeHookSpool(t, root, "client-8502.ndjson", wedged)
	ctx := context.Background()

	dd.requestedDrainPass(ctx)
	require.True(t, spoolWatchPublished(dd, ahead.Nonce), "fixture: the first pass published the line ahead")
	require.Equal(t, 1, len(dd.ing.drainKick), "a pass that made progress and stopped with work left asks again")
	<-dd.ing.drainKick // the requester takes it, as drainOnRequest does

	dd.requestedDrainPass(ctx)
	require.Positive(t, attempts.Load(), "fixture: a pass met the line that never finishes")
	require.False(t, spoolWatchPublished(dd, wedged.Nonce), "fixture: the line never finishes")
	require.Zero(t, len(dd.ing.drainKick),
		"a pass that published nothing before a line ran out of its own deadline must not ask again: that "+
			"line would keep the requester running passes for as long as the daemon lives")
}

// TestDeliveryOrder_FlushOverAnUnreadableFrontierIsCountedNotSilent (F3, F5): when the committed
// frontier cannot be read, the flush cannot know whether the session's leased deliveries are
// published. The ordering gate fails closed on exactly that, and so must the flush: SessionEnd still
// runs (the hook has a reply deadline), but never silently — l0_flush_unsettled counts it and a LOUD
// line names it, as for any SessionEnd that runs ahead of its session.
func TestDeliveryOrder_FlushOverAnUnreadableFrontierIsCountedNotSilent(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	const sess core.SessionID = "sess-frontier-unreadable"
	laneTestSetSettle(dd, liveOrderTick, liveOrderTick)

	acceptPrompt(t, dd, spD3Prompt(dd, root, sess, orderNonce(0), "p0")) // leased, never dispatched
	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	j.st.Lock()
	j.fault = errors.New("injected: the committed frontier is unreadable")
	j.st.Unlock()
	t.Cleanup(func() {
		j.st.Lock()
		j.fault = nil
		j.st.Unlock()
	})

	resp := dd.flushRoute(context.Background(), liveOrderFlush(dd, root, sess), false)
	require.True(t, resp.OK, resp.Err)
	require.Equal(t, int64(1), dd.m.Counter(counterFlushUnsettled).Value(),
		"SessionEnd over an unreadable frontier is counted, not taken for settled")
}

// TestDeliveryOrder_FlushWaitsForABacklogThatKeepsPublishing (F4): the flush waits for its session
// while the session's lane keeps settling deliveries, even when the whole backlog takes longer than
// the stall bound; it gives up only on a lane that stops settling (the short-bound test above) or at
// its overall limit. Each delivery here takes well under the stall bound and all of them together
// well over it, so a fixed bound of that size gave up with some of them still unpublished and ran
// SessionEnd ahead of them.
func TestDeliveryOrder_FlushWaitsForABacklogThatKeepsPublishing(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	const sess core.SessionID = "sess-flush-backlog"
	const tools = 3
	stall := stopDrainBound
	perTool := stall * 2 / 5 // tools x perTool exceeds stall; one tool plus its own publication does not
	laneTestSetSettle(dd, stall, liveOrderBound)
	dd.drain.Store(newDrainer(contentDrainConfig(dd)))
	run := func(ctx context.Context, req ipc.Request) ipc.Response {
		if req.Event != nil && req.Event.ToolUseID != "" {
			// A slow publication: it takes perTool of real time, and gives up with the worker's ctx.
			slow := time.NewTimer(perTool)
			defer slow.Stop()
			select {
			case <-slow.C:
			case <-ctx.Done():
				return ipc.Response{Err: ctx.Err().Error()}
			}
		}
		return dd.runIngested(ctx, req)
	}
	liveOrderWorkers(t, dd, 2, run)

	acceptPrompt(t, dd, spD3Prompt(dd, root, sess, orderNonce(0), "p0"))
	items := make([]int, 0, tools)
	for i := 1; i <= tools; i++ {
		acceptPrompt(t, dd, liveOrderTool(dd, root, sess, i))
		items = append(items, i)
	}
	require.Eventually(t, func() bool { return len(dd.ing.ring) == 0 }, liveOrderBound, liveOrderTick)

	resp := dd.flushRoute(context.Background(), liveOrderFlush(dd, root, sess), true)
	require.True(t, resp.OK, "the flush finishes once the session's backlog has: %q", resp.Err)
	turns := liveOrderTurns(t, root)
	liveOrderRequireMonotone(t, turns)
	require.Equal(t, liveOrderInOrderTurns(sess, items...), turns,
		"every delivery of the backlog is published before SessionEnd, in arrival order")
	require.Zero(t, dd.m.Counter(counterFlushUnsettled).Value())
}

// TestDispatchLanes_PerSessionBoundOverflowAndForget pins the lane bookkeeping behind the tests
// above, without a daemon: the per-session bound, who asks for a drain when a job is refused (the
// refusing join when no worker owns the lane, the owner when it parks or runs dry), a busy head that
// parks without asking (the drain that owns it releases the session itself), and forget.
func TestDispatchLanes_PerSessionBoundOverflowAndForget(t *testing.T) {
	const a, b core.SessionID = "a", "b"
	mk := func(sess core.SessionID, arrival uint64) job {
		return job{leased: true, lease: deliveryLease{
			Session: sess, ArrivalSeq: arrival, Delivery: fmt.Sprintf("%s-%d", sess, arrival),
		}}
	}
	ls := newDispatchLanes(8, 2)

	own, full, drain := ls.join(mk(a, 1))
	require.True(t, own)
	require.False(t, full)
	require.False(t, drain)
	_, full, _ = ls.join(mk(a, 2))
	require.False(t, full)
	_, full, drain = ls.join(mk(a, 3))
	require.True(t, full, "a session's lane holds at most its share, whatever room the others leave")
	require.False(t, drain, "its owner asks for the refused job's drain when the lane stops")
	_, full, _ = ls.join(mk(b, 1))
	require.False(t, full, "another session still gets its own share")

	// The owner settles both held jobs and runs dry: the refused job needs a drain, asked for once.
	for range 2 {
		head, signals, ok, _ := ls.head(a)
		require.True(t, ok)
		goOn, _ := ls.settle(a, head, dispatchSettled, signals)
		require.True(t, goOn)
	}
	_, _, ok, drain := ls.head(a)
	require.False(t, ok)
	require.True(t, drain, "a lane that ran dry with a job refused behind it asks for that job's drain")

	// A busy head parks without asking: the drain pass that owns it releases the session.
	own, _, _ = ls.join(mk(a, 4))
	require.True(t, own)
	head, signals, ok, _ := ls.head(a)
	require.True(t, ok)
	goOn, drain := ls.settle(a, head, dispatchBusy, signals)
	require.False(t, goOn)
	require.False(t, drain, "a head another handler owns needs no drain of its own")

	// A parked lane at its share refuses a job with no owner to ask for its drain: join asks.
	_, full, _ = ls.join(mk(a, 5))
	require.False(t, full)
	queued := len(ls.lanes[a].jobs)
	require.Equal(t, 2, queued)
	head, signals, ok, _ = ls.head(a)
	require.True(t, ok)
	goOn, drain = ls.settle(a, head, dispatchPending, signals)
	require.False(t, goOn)
	require.True(t, drain)
	_, full, drain = ls.join(mk(a, 6))
	require.True(t, full)
	require.True(t, drain, "no worker owns the parked lane, so the refusing join asks for the drain")

	// An earlier arrival than a full lane's latest takes the latest one's place: the lowest arrivals
	// are the ones that can publish next. The lane is parked, so the joining worker becomes its owner
	// and asks for the refused job's drain when the lane next stops.
	own, full, drain = ls.join(mk(a, 3))
	require.True(t, full, "the lane is still at its share: one job is refused")
	require.False(t, drain, "the joining worker owns the lane now and asks when it stops")
	require.True(t, own)
	require.Equal(t, []uint64{3, 4}, []uint64{ls.lanes[a].jobs[0].lease.ArrivalSeq, ls.lanes[a].jobs[1].lease.ArrivalSeq},
		"arrival 3 is held in the place of the lane's latest, arrival 5")
	head, signals, ok, _ = ls.head(a)
	require.True(t, ok)
	require.Equal(t, uint64(3), head.lease.ArrivalSeq)
	goOn, drain = ls.settle(a, head, dispatchBusy, signals)
	require.False(t, goOn)
	require.True(t, drain, "the owner that parks asks for the refused job's drain, busy head or not")

	// forget leaves an owned lane to its owner and releases a parked one.
	own, _, _ = ls.join(mk(b, 2))
	require.False(t, own, "b's lane is still owned by the worker that joined b's first job")
	require.Zero(t, ls.forget(b), "an owned lane is its owner's to release")
	require.Equal(t, 2, ls.forget(a), "a parked lane's jobs go back to the WAL")
	_, ok = ls.lanes[a]
	require.False(t, ok)
	require.Equal(t, 2, ls.held, "only b's jobs are still held")
}

// TestAwaitLaneQuiet_WaitsWhileTheLaneSettlesAndStopsWhenItStalls pins awaitLaneQuiet's two bounds
// without a daemon: every settle restarts the stall bound, a lane that settles nothing for a stall
// bound is given up on, and a quiet lane is reported at once.
func TestAwaitLaneQuiet_WaitsWhileTheLaneSettlesAndStopsWhenItStalls(t *testing.T) {
	const sess core.SessionID = "s"
	const stall = 500 * time.Millisecond
	i := &ingest{lanes: newDispatchLanes(8, 8)}
	mk := func(arrival uint64) job {
		return job{leased: true, lease: deliveryLease{Session: sess, ArrivalSeq: arrival, Delivery: orderNonce(int(arrival))}}
	}
	quiet, why := i.awaitLaneQuiet(context.Background(), sess, stall)
	require.True(t, quiet, "an absent lane is quiet")
	require.Empty(t, why)

	const jobs = 6
	for a := uint64(1); a <= jobs; a++ {
		_, _, _ = i.lanes.join(mk(a))
	}
	// The owner settles one job every fifth of a stall bound: jobs x stall/5 is past one stall bound,
	// and each interval leaves four fifths of it for a co-loaded host's scheduling.
	done := make(chan struct{})
	go func() {
		defer close(done)
		pace := time.NewTicker(stall / 5)
		defer pace.Stop()
		for {
			head, signals, ok, _ := i.lanes.head(sess)
			if !ok {
				return
			}
			<-pace.C
			i.lanes.settle(sess, head, dispatchSettled, signals)
		}
	}()
	began := time.Now()
	quiet, why = i.awaitLaneQuiet(context.Background(), sess, stall)
	<-done
	require.True(t, quiet, "a lane that keeps settling is waited for: %s", why)
	require.Greater(t, time.Since(began), stall, "longer than one stall bound in all")

	// Owned, and settling nothing: given up on after one stall bound.
	own, _, _ := i.lanes.join(mk(jobs + 1))
	require.True(t, own)
	quiet, why = i.awaitLaneQuiet(context.Background(), sess, stall)
	require.False(t, quiet)
	require.Contains(t, why, "settled no delivery")
	i.lanes.park(sess)
}

// TestSettleSessionLimitNestsInsideTheFlushDeadlines pins where the flush's overall settle limit
// comes from: the shipped SessionEnd manifest timeout, less the same slack that keeps the PreCompact
// route inside the hook client's reply wait, less the headroom SessionEnd and the final drain get.
// It must leave the stall bound room to matter.
func TestSettleSessionLimitNestsInsideTheFlushDeadlines(t *testing.T) {
	timeout := time.Duration(manifestHookTimeoutMs(sessionEndHook)) * time.Millisecond
	require.Positive(t, timeout, "the shipped manifest declares a SessionEnd timeout")
	limit := settleSessionLimit()
	require.Equal(t, timeout-precompactDeadlineSlack-settleSessionHeadroom, limit)
	require.Greater(t, limit, settleSessionStall, "the limit leaves a steadily publishing backlog more than one stall bound")
}

// TestDeliveryOrder_DrainReleasesOnlySessionsItPublishedOrRetired: DrainConfig.Released wakes a
// parked lane, and a parked lane with a pending head asks for a drain (requestDrain). A pass that
// released a session merely because it re-read lines already on the committed frontier would
// therefore feed a loop that never ends while that session's head keeps failing: pass, release,
// wake, park, request, pass. The offset of a spool file whose prefix waits on another session never
// passes those lines, so every pass re-reads them. The lane learned of those deliveries when they
// were published, live or by the pass that released them then; a pass that only finds them there
// again has nothing to tell it.
func TestDeliveryOrder_DrainReleasesOnlySessionsItPublishedOrRetired(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	const sess core.SessionID = "sess-release-absorbed"
	var released []core.SessionID
	cfg := contentDrainConfig(dd)
	cfg.Released = func(s core.SessionID) { released = append(released, s) }
	dd.drain.Store(newDrainer(cfg))

	for i := range 2 {
		acceptPrompt(t, dd, spD3Prompt(dd, root, sess, orderNonce(i), fmt.Sprintf("p%d", i)))
	}
	drainRing(t, dd) // the live path publishes both
	n, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.Zero(t, n, "the pass publishes nothing: both lines are already on the frontier")
	require.Empty(t, released, "a pass that only absorbed published lines releases no session")

	acceptPrompt(t, dd, spD3Prompt(dd, root, sess, orderNonce(2), "p2"))
	<-dd.ing.ring // this one only the drain publishes
	n, err = dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []core.SessionID{sess}, released, "a pass that published one of the session's lines releases it")
}
