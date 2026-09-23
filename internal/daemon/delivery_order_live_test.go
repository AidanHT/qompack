package daemon

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/observer"
)

// V6 close-out C1.1: the live-ingest regression. The same-session ordering gate
// (delivery-order-decision.md) defers a leased delivery whose earlier arrival is not yet
// acknowledged, and a deferred delivery used to be retried ONLY by a later drain. With several
// workers, arrival N+1 is dispatched while arrival N is still publishing, so N+1 was deferred, N+2
// was deferred behind it, and so on: every event of a session after the first was stranded until a
// drain ran, and the daemon's own drain runs only on SessionEnd, admin.drain, restart or an idle
// tick once the project has been idle for DetectAfterSeconds (120 s by default). These tests pin the
// live path: same-session leased deliveries publish in arrival order through the worker pool alone,
// with no drain.

// liveOrderRelease bounds how long a test holds arrival 1's handler to give the pre-fix dispatch
// the chance to defer every later arrival. The bug did not need it: a deferral, once counted, is
// permanent. It is only the upper bound of a poll that ends as soon as the deferrals are counted,
// and the fixed dispatch never counts one, so on the fixed code it is simply how long arrival 1
// stays in flight while its successors queue behind it.
const liveOrderRelease = time.Second

// liveOrderBound bounds waiting for the worker pool to publish a handful of prompts. Each one is a
// few fsyncs; the bound is generous for a co-loaded host, and on the pre-fix code it expires
// because nothing ever retries the deferred deliveries.
const liveOrderBound = 30 * time.Second

const liveOrderTick = 10 * time.Millisecond

// liveOrderWorkers starts the ingest worker pool with run and joins it at cleanup, before the
// store, the WAL handles and the lock are closed by the cleanups registered earlier.
func liveOrderWorkers(t *testing.T, dd *daemon, workers int, run func(context.Context, ipc.Request) ipc.Response) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dd.ing.Start(ctx, workers, run)
	t.Cleanup(func() {
		cancel()
		dd.ing.Wait()
	})
}

// liveOrderPollUntil polls cond every liveOrderTick until it holds or bound expires, and reports
// which. Unlike require.Eventually, running out of time is an answer here, not a failure.
func liveOrderPollUntil(bound time.Duration, cond func() bool) bool {
	tick := time.NewTicker(liveOrderTick)
	defer tick.Stop()
	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	for !cond() {
		select {
		case <-tick.C:
		case <-deadline.C:
			return cond()
		}
	}
	return true
}

// liveOrderLease returns the lease Accept took for nonce.
func liveOrderLease(t *testing.T, dd *daemon, nonce string) deliveryLease {
	t.Helper()
	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	l, held, err := j.leaseHeld(nonce)
	require.NoError(t, err)
	require.True(t, held, "Accept leased %s", nonce)
	return l
}

// liveOrderAcked counts how many of leases have reached the committed frontier.
func liveOrderAcked(dd *daemon, leases []deliveryLease) int {
	j, err := dd.deliveryJournal()
	if err != nil {
		return -1
	}
	n := 0
	for _, l := range leases {
		if j.acknowledged(l.Delivery) {
			n++
		}
	}
	return n
}

// liveOrderDiag reports, when a failure message is FORMATTED rather than when the assertion is
// called, how many of leases are acknowledged and how many dispatches the gate deferred: testify
// evaluates message arguments before it waits, so a plain count would describe the starting state.
type liveOrderDiag struct {
	dd     *daemon
	leases []deliveryLease
}

func (d liveOrderDiag) String() string {
	return fmt.Sprintf("%d of %d arrivals acknowledged, %s=%d", liveOrderAcked(d.dd, d.leases), len(d.leases),
		counterOrderingDeferred, d.dd.m.Counter(counterOrderingDeferred).Value())
}

// liveOrderRequireTurns asserts prompt i holds turn i: the observer assigns turns in publication
// order, so this is the arrival-order publication the gate exists to guarantee.
func liveOrderRequireTurns(t *testing.T, o *Options, sess core.SessionID, k int) {
	t.Helper()
	for i := range k {
		require.Equal(t, fmt.Sprintf("p%d", i), spD3PromptText(t, o, observer.VerbatimPromptID(sess, core.TurnIndex(i))),
			"turn %d holds arrival %d", i, i+1)
	}
}

// liveOrderPromptGate wraps run so that the handler for prompt text blocks until release is
// closed. The returned func closes release once; it is also registered as a cleanup so a failing
// test never leaves a worker parked in the handler while the pool is being joined.
func liveOrderPromptGate(t *testing.T, run func(context.Context, ipc.Request) ipc.Response, text string,
) (func(context.Context, ipc.Request) ipc.Response, func()) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	gated := func(ctx context.Context, req ipc.Request) ipc.Response {
		if req.Event != nil && req.Event.Prompt == text {
			<-release
		}
		return run(ctx, req)
	}
	return gated, open
}

// TestDeliveryOrder_LiveSameSessionArrivalsPublishWithoutDrain is the C1.1 regression. Arrival 1's
// handler is held in flight while arrivals 2..K reach free workers — exactly what the e2e observer
// test's back-to-back hooks produced. Every arrival must then publish, in arrival order, through the
// worker pool alone: no drainer is installed, so nothing but the live path can publish anything.
// Before the fix every one of arrivals 2..K was deferred by the ordering gate and never retried.
func TestDeliveryOrder_LiveSameSessionArrivalsPublishWithoutDrain(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})

	const sess core.SessionID = "sess-live-order"
	const k = 6
	run, open := liveOrderPromptGate(t, dd.runIngested, "p0")
	liveOrderWorkers(t, dd, 4, run)
	t.Cleanup(open)

	leases := make([]deliveryLease, k)
	for i := range k {
		req := spD3Prompt(dd, root, sess, orderNonce(i), fmt.Sprintf("p%d", i))
		acceptPrompt(t, dd, req)
		leases[i] = liveOrderLease(t, dd, req.Nonce)
		require.Equal(t, uint64(i+1), leases[i].ArrivalSeq)
	}

	// Every job has reached a worker. Give the pre-fix dispatch its chance to defer arrivals 2..K
	// while arrival 1 is still in flight; the poll ends as soon as it has.
	require.Eventually(t, func() bool { return len(dd.ing.ring) == 0 }, liveOrderBound, liveOrderTick,
		"the worker pool never took the queued jobs")
	liveOrderPollUntil(liveOrderRelease, func() bool { return dd.m.Counter(counterOrderingDeferred).Value() >= k-1 })
	open()

	require.Eventually(t, func() bool { return liveOrderAcked(dd, leases) == k }, liveOrderBound, liveOrderTick,
		"same-session arrivals did not all reach the committed frontier without a drain: %s",
		liveOrderDiag{dd, leases})
	liveOrderRequireTurns(t, o, sess, k)
	require.Zero(t, dd.m.Counter(counterOrderingDeferred).Value(),
		"same-session jobs are dispatched in arrival order, so none reaches the gate early")
}

// TestDeliveryOrder_LiveReversedRingOrderPublishesWithoutDrain: two concurrent Accepts can lease in
// one order and reach the ring in the other. With one worker the later arrival is dispatched first,
// the gate defers it, and the earlier arrival then publishes. The deferred later arrival must still
// publish, second, without a drain.
func TestDeliveryOrder_LiveReversedRingOrderPublishesWithoutDrain(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})

	const sess core.SessionID = "sess-live-reversed"
	first := spD3Prompt(dd, root, sess, orderNonce(0), "p0")
	second := spD3Prompt(dd, root, sess, orderNonce(1), "p1")
	acceptPrompt(t, dd, first)
	acceptPrompt(t, dd, second)
	early, late := <-dd.ing.ring, <-dd.ing.ring
	require.Less(t, early.lease.ArrivalSeq, late.lease.ArrivalSeq)
	dd.ing.ring <- late // the later arrival reaches the ring first
	dd.ing.ring <- early

	liveOrderWorkers(t, dd, 1, dd.runIngested)

	leases := []deliveryLease{early.lease, late.lease}
	require.Eventually(t, func() bool { return liveOrderAcked(dd, leases) == 2 }, liveOrderBound, liveOrderTick,
		"both arrivals did not reach the committed frontier without a drain: %s", liveOrderDiag{dd, leases})
	liveOrderRequireTurns(t, o, sess, 2)
}

// TestDeliveryOrder_LiveOtherSessionsAreNotSerialized is the guard against over-correcting: holding
// one session's arrival in flight must not hold up another session's. Cross-session publication
// stays parallel.
func TestDeliveryOrder_LiveOtherSessionsAreNotSerialized(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})

	const held, free core.SessionID = "sess-held", "sess-free"
	run, open := liveOrderPromptGate(t, dd.runIngested, "held")
	liveOrderWorkers(t, dd, 2, run)
	t.Cleanup(open)

	blocked := spD3Prompt(dd, root, held, orderNonce(0), "held")
	acceptPrompt(t, dd, blocked)
	require.Eventually(t, func() bool { return len(dd.ing.ring) == 0 }, liveOrderBound, liveOrderTick)

	other := spD3Prompt(dd, root, free, orderNonce(1), "p0")
	acceptPrompt(t, dd, other)
	otherLease := liveOrderLease(t, dd, other.Nonce)
	require.Eventually(t, func() bool { return liveOrderAcked(dd, []deliveryLease{otherLease}) == 1 },
		liveOrderBound, liveOrderTick, "another session publishes while the first is still in flight")
	liveOrderRequireTurns(t, o, free, 1)

	open()
	heldLease := liveOrderLease(t, dd, blocked.Nonce)
	require.Eventually(t, func() bool { return liveOrderAcked(dd, []deliveryLease{heldLease}) == 1 },
		liveOrderBound, liveOrderTick)
}

// liveOrderLane reports how many jobs sess's lane holds and whether a worker owns it; a lane that
// does not exist holds nothing and is not owned.
func liveOrderLane(dd *daemon, sess core.SessionID) (queued int, running bool) {
	ls := dd.ing.lanes
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if l := ls.lanes[sess]; l != nil {
		return len(l.jobs), l.running
	}
	return 0, false
}

// TestDeliveryOrder_DrainReleaseWakesParkedLiveSuccessor: a live successor whose predecessor never
// reached the worker pool (here it exists only as a client spool line, as it would after a ring drop
// or an ACK-deadline spool) parks: the gate defers it once and nothing spins. When a drain then
// publishes the predecessor, the drain's release wakes the parked lane and the worker pool publishes
// the successor — second, and without a further drain. The drain's own pass cannot have done it: the
// successor's WAL line was deferred behind the predecessor, whose line only comes later, in a
// different spool file.
func TestDeliveryOrder_DrainReleaseWakesParkedLiveSuccessor(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})

	ctx := context.Background()
	const sess core.SessionID = "sess-live-wake"
	first := spD3Prompt(dd, root, sess, orderNonce(0), "p0")
	firstLease, ok := dd.ing.leaseDelivery(ctx, first) // arrival 1: leased, then only spooled
	require.True(t, ok)
	writeSpoolLines(t, root, "client-00001.ndjson", first)

	second := spD3Prompt(dd, root, sess, orderNonce(1), "p1")
	acceptPrompt(t, dd, second) // arrival 2: WAL, lease and ring
	secondLease := liveOrderLease(t, dd, second.Nonce)
	require.Equal(t, firstLease.ArrivalSeq+1, secondLease.ArrivalSeq)

	liveOrderWorkers(t, dd, 2, dd.runIngested)
	require.Eventually(t, func() bool {
		queued, running := liveOrderLane(dd, sess)
		return queued == 1 && !running
	}, liveOrderBound, liveOrderTick, "the successor must park behind its missing predecessor")
	require.Equal(t, int64(1), dd.m.Counter(counterOrderingDeferred).Value(),
		"a parked lane is not retried until something releases it: one deferral, no spin")

	dd.drain.Store(newDrainer(dd.drainConfig()))
	n, err := dd.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the drain publishes only the predecessor; its pass defers the successor's WAL line")

	leases := []deliveryLease{firstLease, secondLease}
	require.Eventually(t, func() bool { return liveOrderAcked(dd, leases) == 2 }, liveOrderBound, liveOrderTick,
		"the woken lane did not publish the successor: %s", liveOrderDiag{dd, leases})
	liveOrderRequireTurns(t, o, sess, 2)
	require.Eventually(t, func() bool {
		queued, running := liveOrderLane(dd, sess)
		return queued == 0 && !running
	}, liveOrderBound, liveOrderTick, "the emptied lane is released")
}

// TestDeliveryOrder_LiveLanesAreBoundedAndOverflowIsLeftForTheDrain: the lanes hold at most their
// capacity. A leased job past it is not queued — it stays durable in the WAL, counted, exactly as a
// full ring's job does — and the drain later publishes it, still in arrival order.
func TestDeliveryOrder_LiveLanesAreBoundedAndOverflowIsLeftForTheDrain(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})

	const sess core.SessionID = "sess-live-bounded"
	const capacity, k = 2, 5
	dd.ing.lanes = newDispatchLanes(capacity)
	run, open := liveOrderPromptGate(t, dd.runIngested, "p0")
	liveOrderWorkers(t, dd, 2, run)
	t.Cleanup(open)

	leases := make([]deliveryLease, k)
	for i := range k {
		req := spD3Prompt(dd, root, sess, orderNonce(i), fmt.Sprintf("p%d", i))
		acceptPrompt(t, dd, req)
		leases[i] = liveOrderLease(t, dd, req.Nonce)
	}
	require.Eventually(t, func() bool { return dd.m.Counter(counterOrderingLaneFull).Value() == k-capacity },
		liveOrderBound, liveOrderTick, "every job past the capacity is counted and left in the WAL")
	queued, _ := liveOrderLane(dd, sess)
	require.Equal(t, capacity, queued, "the lane never holds more than its capacity")

	open()
	require.Eventually(t, func() bool { return liveOrderAcked(dd, leases[:capacity]) == capacity },
		liveOrderBound, liveOrderTick, "the queued jobs publish: %s", liveOrderDiag{dd, leases[:capacity]})
	require.Eventually(t, func() bool {
		queued, running := liveOrderLane(dd, sess)
		return queued == 0 && !running
	}, liveOrderBound, liveOrderTick)
	require.Zero(t, liveOrderAcked(dd, leases[capacity:]), "nothing but the drain can publish an overflowed job")

	dd.drain.Store(newDrainer(dd.drainConfig()))
	n, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, k-capacity, n, "the drain publishes the overflow and absorbs the rest from the frontier")
	require.Equal(t, k, liveOrderAcked(dd, leases))
	liveOrderRequireTurns(t, o, sess, k)
}

// TestDeliveryOrder_LiveFailedHeadIsRetriedByTheNextArrivalNotSpun: a head whose publication fails
// stays queued and its lane parks — the handler is called once, not in a loop. The session's next
// arrival runs the lane again, the head is retried first and publishes, and then the new arrival.
func TestDeliveryOrder_LiveFailedHeadIsRetriedByTheNextArrivalNotSpun(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})

	const sess core.SessionID = "sess-live-failed"
	var mu sync.Mutex
	calls := 0
	headCalls := func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
	run := func(ctx context.Context, req ipc.Request) ipc.Response {
		if req.Event != nil && req.Event.Prompt == "p0" {
			mu.Lock()
			calls++
			first := calls == 1
			mu.Unlock()
			if first {
				return ipc.Response{Err: "injected handler failure"}
			}
		}
		return dd.runIngested(ctx, req)
	}
	liveOrderWorkers(t, dd, 2, run)

	first := spD3Prompt(dd, root, sess, orderNonce(0), "p0")
	acceptPrompt(t, dd, first)
	require.Eventually(t, func() bool {
		queued, running := liveOrderLane(dd, sess)
		return headCalls() == 1 && queued == 1 && !running
	}, liveOrderBound, liveOrderTick, "the failed head stays queued and its lane parks")
	require.Equal(t, 1, headCalls(), "a parked lane has no owner, so nothing retries it in a loop")

	second := spD3Prompt(dd, root, sess, orderNonce(1), "p1")
	acceptPrompt(t, dd, second)
	leases := []deliveryLease{liveOrderLease(t, dd, first.Nonce), liveOrderLease(t, dd, second.Nonce)}
	require.Eventually(t, func() bool { return liveOrderAcked(dd, leases) == 2 }, liveOrderBound, liveOrderTick,
		"the next arrival retries the failed head, then publishes itself: %s", liveOrderDiag{dd, leases})
	require.Equal(t, 2, headCalls(), "the head was retried exactly once, by the arrival that ran its lane")
	liveOrderRequireTurns(t, o, sess, 2)
}

// TestDispatchLanes_SignalsKeepTheOwnerFromParkingOnAStaleView pins the lane bookkeeping the tests
// above rely on, without a daemon: ownership, arrival order, duplicates, capacity, and the two ways a
// wake can arrive — while the owner dispatches (it must go on rather than park) and while the lane is
// parked (it is listed and handed to a worker).
func TestDispatchLanes_SignalsKeepTheOwnerFromParkingOnAStaleView(t *testing.T) {
	const sess core.SessionID = "s"
	mk := func(arrival uint64) job {
		return job{leased: true, lease: deliveryLease{Session: sess, ArrivalSeq: arrival, Delivery: orderNonce(int(arrival))}}
	}
	ls := newDispatchLanes(3)

	own, full := ls.join(mk(2))
	require.True(t, own, "the first job of an unowned lane makes its worker the owner")
	require.False(t, full)
	own, _ = ls.join(mk(1))
	require.False(t, own, "a job joining an owned lane leaves its worker free")
	own, _ = ls.join(mk(1))
	require.False(t, own)
	require.Equal(t, 2, ls.held, "a delivery already queued is not queued twice")

	head, signals, ok := ls.head(sess)
	require.True(t, ok)
	require.Equal(t, uint64(1), head.lease.ArrivalSeq, "the owner dispatches the lowest arrival first")

	// A wake lands while the owner is dispatching a head that turns out pending: the owner goes on.
	require.False(t, ls.wake(sess), "an owned lane is not listed, only signalled")
	require.True(t, ls.settle(sess, head, dispatchPending, signals), "a signal during the dispatch keeps the owner going")

	// Nothing signals this time: the owner parks.
	head, signals, ok = ls.head(sess)
	require.True(t, ok)
	require.False(t, ls.settle(sess, head, dispatchPending, signals), "no signal: the lane parks")

	// A parked lane is listed by a wake and claimed by a worker.
	require.True(t, ls.wake(sess))
	require.False(t, ls.wake(sess), "a listed lane is listed once")
	got, ok, more := ls.claimReady()
	require.True(t, ok)
	require.False(t, more)
	require.Equal(t, sess, got)

	head, signals, ok = ls.head(sess)
	require.True(t, ok)
	require.True(t, ls.settle(sess, head, dispatchSettled, signals))
	head, signals, ok = ls.head(sess)
	require.True(t, ok)
	require.Equal(t, uint64(2), head.lease.ArrivalSeq)
	require.True(t, ls.settle(sess, head, dispatchSettled, signals))
	_, _, ok = ls.head(sess)
	require.False(t, ok, "an emptied lane is released")
	require.Zero(t, ls.held)
	require.Empty(t, ls.lanes, "and forgotten")

	// Capacity is shared by every lane.
	for a := uint64(1); a <= 3; a++ {
		_, full = ls.join(mk(a))
		require.False(t, full)
	}
	_, full = ls.join(job{leased: true, lease: deliveryLease{Session: "other", ArrivalSeq: 1, Delivery: orderNonce(9)}})
	require.True(t, full, "a job past the capacity is not held")
	require.Equal(t, 3, ls.held)
}

// TestDeliveryOrder_DrainReleasesSessionsOnlyAfterItsPass: the drain tells DrainConfig.Released
// about the sessions whose leased lines it consumed only once its pass is over, once per session.
// Releasing a session mid-pass woke its parked live lane while the same pass was still reading that
// session's later lines: the woken worker took the next queued job, the pass then met that job's
// line with its seen key held, and "delivery still in progress" aborted the pass (a flush answered
// OK:false for it). Every dispatch of the pass must therefore see no release yet.
func TestDeliveryOrder_DrainReleasesSessionsOnlyAfterItsPass(t *testing.T) {
	root := t.TempDir()
	_, dd, _ := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})

	const sess core.SessionID = "sess-release-after-pass"
	const k = 3
	var mu sync.Mutex
	var releases []core.SessionID
	var releasedAtDispatch []int
	cfg := dd.drainConfig()
	dispatch := cfg.Dispatch
	cfg.Dispatch = func(ctx context.Context, req ipc.Request) ipc.Response {
		mu.Lock()
		releasedAtDispatch = append(releasedAtDispatch, len(releases))
		mu.Unlock()
		return dispatch(ctx, req)
	}
	cfg.Released = func(s core.SessionID) {
		mu.Lock()
		releases = append(releases, s)
		mu.Unlock()
	}
	dd.drain.Store(newDrainer(cfg))

	for i := range k {
		acceptPrompt(t, dd, spD3Prompt(dd, root, sess, orderNonce(i), fmt.Sprintf("p%d", i)))
	}
	for range k {
		<-dd.ing.ring // no worker pool: only the drain publishes these
	}

	n, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, k, n)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []int{0, 0, 0}, releasedAtDispatch, "no session is released while the pass can still dispatch its lines")
	require.Equal(t, []core.SessionID{sess}, releases, "the session is released once, after the pass")
}
