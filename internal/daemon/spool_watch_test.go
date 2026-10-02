package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// V6 close-out C1.13 (remainder). A hook that cannot hand its delivery to the daemon — its dial
// failed, the daemon told the hot path to spool (HotSpool), or its ACK came too late — appends it to
// its own client spool, which only a drain reads. The daemon's drains ran only at startup, on its
// first served request, on a flush, on admin.drain, at Stop, when a lane parked behind a leased
// predecessor, and on an idle tick once the whole project had been idle for DetectAfterSeconds (120 s
// by default). A delivery that never reached the daemon holds no lease, so no lane parks behind it:
// while its session was active it waited for the session's end or two quiet minutes (the ingest
// lane's open item; plans/sdd/V6-closeout/ingest/report.md §8).

// spoolWatchTick is the check interval these tests give the watcher in place of the product's
// spoolCheckInterval, so a test of its cadence runs in a fraction of a second. The subject is what
// the watcher does on its cadence, not the cadence's length.
const spoolWatchTick = 20 * time.Millisecond

// startSpoolWatch runs the daemon's client-spool watcher, as Run does, looking every interval and
// retrying an unconsumed spool for horizon, until cleanup.
func startSpoolWatch(t *testing.T, dd *daemon, every, horizon time.Duration) {
	t.Helper()
	dd.spool.every, dd.spool.horizon = every, horizon
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		dd.watchClientSpools(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// spoolWatchTraffic keeps sess's hooks arriving through the served path — each one a live, leased
// tool delivery the worker pool publishes — until stop is closed, one every tick.
func spoolWatchTraffic(t *testing.T, dd *daemon, root string, sess core.SessionID, first int, stop <-chan struct{}) {
	t.Helper()
	tick := time.NewTicker(spoolWatchTick)
	defer tick.Stop()
	for i := first; ; i++ {
		req := liveOrderTool(dd, root, sess, i)
		require.True(t, dd.dispatchOp(context.Background(), req).OK)
		select {
		case <-stop:
			return
		case <-tick.C:
		}
	}
}

// spoolWatchRunTraffic runs spoolWatchTraffic on its own goroutine until the returned stop function
// is called, or until cleanup, whichever comes first.
func spoolWatchRunTraffic(t *testing.T, dd *daemon, root string, sess core.SessionID, first int) (stop func()) {
	t.Helper()
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		spoolWatchTraffic(t, dd, root, sess, first, quit)
	}()
	stopped := false
	stop = func() {
		if !stopped {
			stopped = true
			close(quit)
			<-done
		}
	}
	t.Cleanup(stop)
	return stop
}

// spoolWatchPublished reports whether the delivery carrying nonce is leased and on the committed
// frontier.
func spoolWatchPublished(dd *daemon, nonce string) bool {
	j, err := dd.deliveryJournal()
	if err != nil {
		return false
	}
	l, held, err := j.leaseHeld(nonce)
	return err == nil && held && j.acknowledged(l.Delivery)
}

// spoolWatchGone reports whether the client spool base has been released.
func spoolWatchGone(root, base string) bool {
	_, err := os.Stat(paths.Long(filepath.Join(paths.Of(root).Spool, base)))
	return os.IsNotExist(err)
}

// writeHookSpool writes reqs into the client spool base exactly as a hook's spool writer leaves them:
// each request's encoding, which ends its own line, and nothing between them. (writeSpoolLines adds
// a newline of its own after each, so its files carry a blank line after every record, which a drain
// consumes as a line; the rows that count what a budgeted pass consumed must not have those.)
func writeHookSpool(t *testing.T, root, base string, reqs ...ipc.Request) {
	t.Helper()
	var buf []byte
	for _, req := range reqs {
		line, err := ipc.EncodeRequest(req)
		require.NoError(t, err)
		buf = append(buf, line...)
	}
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(spool, base)), buf, 0o600))
}

// TestSpoolWatch_AClientSpoolIsPublishedWhileItsSessionIsActive is the C1.13 regression: a delivery
// that reached only its client spool, in the middle of a session that keeps sending hooks, is
// published while the session is still active — with no flush, no admin.drain, no drain the lanes
// ask for and no idle drain, none of which runs here.
func TestSpoolWatch_AClientSpoolIsPublishedWhileItsSessionIsActive(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	startSpoolWatch(t, dd, spoolWatchTick, liveOrderBound)

	const sess core.SessionID = "sess-spool-watch"
	require.True(t, dd.dispatchOp(context.Background(), spD3Prompt(dd, root, sess, orderNonce(0), "p0")).OK)

	// The delivery only its client spool holds: never leased, never in the WAL.
	spooled := liveOrderTool(dd, root, sess, 1)
	writeSpoolLines(t, root, "client-5151.ndjson", spooled)

	spoolWatchRunTraffic(t, dd, root, sess, 2)

	require.Eventually(t, func() bool { return spoolWatchPublished(dd, spooled.Nonce) },
		liveOrderBound, liveOrderTick,
		"a delivery only its client spool held was never published while its session stayed active")
	require.Eventually(t, func() bool { return spoolWatchGone(root, "client-5151.ndjson") },
		liveOrderBound, liveOrderTick, "the consumed client spool is released")
	require.Zero(t, dd.m.Counter(counterDrainFileError).Value(),
		"a client-spool pass never meets a live WAL line in flight: it reads no WAL segment")
}

// TestSpoolWatch_ASpoolWaitingOnItsSessionIsRetriedWithNoFurtherHook: the watcher's first pass over a
// spool can find its line waiting on an earlier arrival of the session that is still publishing —
// the usual case, since the pass gives the spooled delivery its lease, and so its arrival, only then.
// The spool is passed again once that earlier arrival has published, although no hook arrives after
// it and no drain the lanes ask for runs: the session's LAST events are exactly the ones no later
// kick would rescue.
func TestSpoolWatch_ASpoolWaitingOnItsSessionIsRetriedWithNoFurtherHook(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	const sess core.SessionID = "sess-spool-retry"
	release := make(chan struct{})
	liveOrderWorkers(t, dd, 2, func(ctx context.Context, req ipc.Request) ipc.Response {
		if req.Event != nil && req.Event.Prompt == "p0" {
			select { // p0 is still publishing when the watcher first passes the spool
			case <-release:
			case <-ctx.Done():
				return ipc.Response{Err: "stopped"}
			}
		}
		return dd.runIngested(ctx, req)
	})
	startSpoolWatch(t, dd, spoolWatchTick, liveOrderBound)

	p0 := spD3Prompt(dd, root, sess, orderNonce(0), "p0")
	acceptPrompt(t, dd, p0)
	spooled := liveOrderTool(dd, root, sess, 1)
	writeSpoolLines(t, root, "client-5252.ndjson", spooled)
	dd.kickSpoolWatch() // the served request that answered the hook too late for its ACK

	require.Eventually(t, func() bool {
		j, err := dd.deliveryJournal()
		if err != nil {
			return false
		}
		_, held, err := j.leaseHeld(spooled.Nonce)
		return err == nil && held && dd.m.Counter(counterSpoolWatchDrains).Value() >= 1
	}, liveOrderBound, liveOrderTick, "the settled spool gets its first pass, which leases its line")
	require.False(t, spoolWatchPublished(dd, spooled.Nonce),
		"control: the first pass cannot publish it while its session's earlier arrival is publishing")

	close(release)
	require.Eventually(t, func() bool { return spoolWatchPublished(dd, p0.Nonce) },
		liveOrderBound, liveOrderTick, "the earlier arrival publishes")
	require.Eventually(t, func() bool { return spoolWatchPublished(dd, spooled.Nonce) },
		liveOrderBound, liveOrderTick,
		"the watcher passed the spool again, with no hook after it and no other drain, and published it")
	require.Eventually(t, func() bool { return spoolWatchGone(root, "client-5252.ndjson") },
		liveOrderBound, liveOrderTick, "the consumed client spool is released")
}

// TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick: a spool whose line waits on an
// earlier arrival nothing will ever publish is passed again at a doubling wait, and not at all once
// it has waited out the retry horizon — however many hooks keep arriving meanwhile. No busy loop, and
// nothing of it is lost: it stays for a drain that can publish it.
func TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick(t *testing.T) {
	spoolWatchBackoffRow(t, 0)
}

// TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff (V6 close-out C7.2) is the
// backoff row with the first pass held, in real time, inside its sync of the spool file for longer
// than the pass's own budget (idleRunBudget) and the row's retry horizon, as an fsync queue on a
// loaded host can hold it. It pins two things. The pass consumed no line, so its budget could not
// stop it (D31: a budget ends a pass only once the pass has consumed a line); it finished the spool
// and found it unconsumable, so the spool keeps the doubling wait instead of being due at the next
// look. And the schedule is read from the clock the look is handed (lookAtClientSpools' now), never
// from how long the pass took in real time.
//
// It pins no retry after a first look that outlasted the horizon, because the live loop makes none.
// watchClientSpools hands each look time.Now() taken before the pass, so a first look longer than
// the horizon closes the retry window before any second pass, and by design the requested, flush,
// idle and restart drains take the spool (spool_watch.go's doc). Against the product's horizon
// (DetectAfterSeconds, 120 s) and interval (2 s) that takes a first look of nearly two minutes.
// Against this row's compressed horizon (50 ticks, 1 s) one slow look was enough: when the row still
// ran on the wall clock, hosted windows-latest failed it on candidate 7 with "the unconsumed spool is
// passed again" after 30.60 s.
func TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff(t *testing.T) {
	spoolWatchBackoffRow(t, idleRunBudget+spoolWatchTick)
}

// spoolWatchBackoffRow is the backoff row. A positive firstPassStall holds the first pass that long,
// in real time, inside its sync of the spool file (drainer.syncFile) before the real sync.
//
// The row drives the watcher's look (lookAtClientSpools, which watchClientSpools runs) on the look's
// injected clock, one tick per look, every look kicked as a hook every tick kicks it, while a busy
// session's hooks really are served and published beside it. Every pass is a real one. Its subject
// is the schedule — the doubling wait and the stop at the horizon — which the look decides from the
// clock it is handed alone, so no length of a pass on a loaded host can exhaust it. On the wall clock
// it could: the row's 1 s horizon is half of one pass's idleRunBudget, and a first look slower than
// it left no second pass (C7.2). The live loop retrying an unconsumed spool with no further hook is
// TestSpoolWatch_ASpoolWaitingOnItsSessionIsRetriedWithNoFurtherHook's subject, under liveOrderBound;
// spoolRetryAfter's own values are TestSpoolRetryAfter_Doubles'.
func spoolWatchBackoffRow(t *testing.T, firstPassStall time.Duration) {
	t.Helper()
	dd, _, root := laneTestDaemon(t)
	dr := newDrainer(dd.drainConfig())
	if firstPassStall > 0 {
		sync := dr.syncFile
		var syncs atomic.Int32
		dr.syncFile = func(path string) error {
			if syncs.Add(1) == 1 { // the first pass's sync of the one client spool there is
				timer := time.NewTimer(firstPassStall)
				defer timer.Stop()
				<-timer.C
			}
			return sync(path)
		}
	}
	dd.drain.Store(dr)
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	const horizon = 50 * spoolWatchTick
	dd.spool.every, dd.spool.horizon = spoolWatchTick, horizon
	ctx := context.Background()

	// The most passes the schedule allows inside the horizon: the first, then one per doubling wait
	// that still starts inside it.
	maxPasses := 1
	for at := spoolRetryAfter(spoolWatchTick, 1); at < horizon; at += spoolRetryAfter(spoolWatchTick, maxPasses) {
		maxPasses++
	}

	// Session "stuck": arrival 0 is leased and then lost, so its arrival 1, which only a client spool
	// holds, can never pass the ordering gate.
	const stuck core.SessionID = "sess-spool-stuck"
	lost := spD3Prompt(dd, root, stuck, orderNonce(0), "lost")
	_, ok := dd.ing.leaseDelivery(ctx, lost)
	require.True(t, ok)
	blocked := spD3Prompt(dd, root, stuck, orderNonce(1), "blocked")
	// As a hook writes it, and one record: no blank line after it and no other line behind it. A pass
	// that has consumed a line can be stopped by its budget (idleRunBudget) once a slow host has spent
	// it, and a spool a budget-stopped pass leaves unfinished is due again at the next look
	// (TestSpoolWatch_APassItsBudgetCutShortDoesNotBackOffTheSpoolsItLeft), which is not this row's
	// schedule. A line behind the blocked one, such as another delivery a reused pid's hook appended,
	// is published by the first pass, which its budget can then stop inside the spool; the passes after
	// it only consume that line again, which is no progress (notePassConsumed). That schedule is
	// TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadKeepsTheBackoff's. With the record
	// alone no pass consumes anything, so none can stop on its budget in this spool.
	writeHookSpool(t, root, "client-6161.ndjson", blocked)

	stop := spoolWatchRunTraffic(t, dd, root, "sess-spool-busy", 1)
	entries := map[string]*spoolWatchEntry{}
	start := time.Now()
	var passedAt []time.Duration // when each pass ran, on the look's injected clock, from the first look
	look := func(now time.Time, kicked bool) bool {
		before := dd.m.Counter(counterSpoolWatchDrains).Value()
		_, more := dd.lookAtClientSpools(ctx, entries, kicked, now)
		if dd.m.Counter(counterSpoolWatchDrains).Value() > before {
			passedAt = append(passedAt, now.Sub(start))
		}
		return more
	}
	// Looks for well past the horizon: a kick at every one, and one look every interval.
	now := start
	for ; now.Sub(start) <= 3*horizon; now = now.Add(spoolWatchTick) {
		look(now, true)
	}
	require.GreaterOrEqual(t, len(passedAt), 2, "the unconsumed spool is passed again")
	require.LessOrEqual(t, len(passedAt), maxPasses,
		"an unconsumable spool is passed at a doubling wait inside the horizon (at most %d passes), not "+
			"on every look while hooks keep arriving (passes at %v)", maxPasses, passedAt)
	// Two intervals after the first pass, then twice the wait before each time.
	for i, wait := 1, 2*spoolWatchTick; i < len(passedAt); i, wait = i+1, 2*wait {
		require.Equal(t, wait, passedAt[i]-passedAt[i-1],
			"pass %d of the unconsumed spool came at the doubling wait after the one before (passes at %v)",
			i+1, passedAt)
	}
	require.Len(t, passedAt, maxPasses,
		"every wait the schedule starts inside the horizon got its pass (passes at %v)", passedAt)
	for _, at := range passedAt {
		require.Less(t, at-passedAt[0], horizon,
			"past the horizon the watcher stops passing it; the idle drain and the others own it (passes at %v)",
			passedAt)
	}
	require.False(t, look(now, false),
		"past the horizon, with no kick, the watcher has nothing left to look for: it polls nothing")
	stop()
	require.FileExists(t, filepath.Join(paths.Of(root).Spool, "client-6161.ndjson"),
		"nothing of it was lost: it stays for a drain that can publish it")
	require.False(t, spoolWatchPublished(dd, blocked.Nonce), "control: its predecessor never published")
}

// spoolWatchSlow counts what a slowed drain did with the deliveries it slows: every attempt, and the
// attempts whose context ended inside the slow part.
type spoolWatchSlow struct {
	attempts, cutInside atomic.Int32
}

// spoolWatchSlowDrain installs a drainer whose dispatch of every delivery in slow takes d, or ends
// early with its context, before it publishes the delivery as the product would.
func spoolWatchSlowDrain(dd *daemon, slow map[string]bool, d time.Duration) *spoolWatchSlow {
	counts := &spoolWatchSlow{}
	cfg := dd.drainConfig()
	dispatch := cfg.Dispatch
	cfg.Dispatch = func(ctx context.Context, req ipc.Request) ipc.Response {
		if slow[req.Nonce] {
			counts.attempts.Add(1)
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				counts.cutInside.Add(1)
				return ipc.Response{Err: ctx.Err().Error()}
			}
		}
		return dispatch(ctx, req)
	}
	dd.drain.Store(newDrainer(cfg))
	return counts
}

// TestSpoolWatch_AClientSpoolSlowerThanAPassBudgetIsPublished: the watcher's pass has the idle
// drain's budget (idleRunBudget), and a context deadline enforced it, which cancelled the line the
// pass was publishing. A spooled delivery whose publication took longer than that — a capture on a
// host with a deep fsync queue — was cancelled by every pass, then by fewer and fewer as the retries
// backed off, and was published by none while its session lasted: under CPU and fsync co-load on top
// of two other gate runs on Linux, TestE2E_ThinSliceDropsControlOnlyEdges waited out its 60 s with
// 13 and 20 hooks' client spools still undrained. The pass must give the line it started its own
// drainLineDeadline, as a requested pass does (withPassBudget). The row counts the attempts
// cancelled inside the slow part, which the budget did to every attempt and a line's own deadline
// cannot.
func TestSpoolWatch_AClientSpoolSlowerThanAPassBudgetIsPublished(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	const sess core.SessionID = "sess-spool-slow"
	spooled := liveOrderTool(dd, root, sess, 1)
	slow := idleRunBudget + time.Second // longer than a pass's budget, inside drainLineDeadline
	require.Less(t, slow, drainLineDeadline, "fixture: the slow line must fit its own deadline")
	counts := spoolWatchSlowDrain(dd, map[string]bool{spooled.Nonce: true}, slow)
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	startSpoolWatch(t, dd, spoolWatchTick, liveOrderBound)

	writeSpoolLines(t, root, "client-8181.ndjson", spooled)
	spoolWatchRunTraffic(t, dd, root, "sess-spool-kicks", 2)

	require.Eventually(t, func() bool { return spoolWatchPublished(dd, spooled.Nonce) },
		liveOrderBound, liveOrderTick, "a client spool slower than a pass's budget was never published")
	require.Positive(t, counts.attempts.Load(), "fixture: a pass met the slow line")
	require.Zero(t, counts.cutInside.Load(),
		"no pass cancelled the slow line inside its slow part: the budget never cuts a line it started")
}

// TestSpoolWatch_APassItsBudgetCutShortDoesNotBackOffTheSpoolsItLeft: a pass that stops because its
// budget is spent has not found the spools it did not reach unconsumable, so it must not put them on
// the doubling wait meant for those; they are due at the next look. Under load every pass stops that
// way, and a watcher that doubled the wait each time left most of a burst of client spools waiting
// through tens of seconds of passes that each published one (TestE2E_ThinSliceDropsControlOnlyEdges,
// 13 and 20 spools still undrained at its 60 s bound). Here the first of two settled spools takes
// longer than the whole pass budget to publish, so the pass publishes it and stops before the second.
func TestSpoolWatch_APassItsBudgetCutShortDoesNotBackOffTheSpoolsItLeft(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	first := liveOrderTool(dd, root, "sess-spool-cut-a", 1)
	second := liveOrderTool(dd, root, "sess-spool-cut-b", 2)
	slow := idleRunBudget + spoolWatchTick // longer than a pass's budget, inside drainLineDeadline
	require.Less(t, slow, drainLineDeadline, "fixture: the slow line must fit its own deadline")
	spoolWatchSlowDrain(dd, map[string]bool{first.Nonce: true}, slow)
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	dd.spool.every, dd.spool.horizon = spoolWatchTick, liveOrderBound
	writeSpoolLines(t, root, "client-8201.ndjson", first)
	writeSpoolLines(t, root, "client-8202.ndjson", second)

	ctx := context.Background()
	entries := map[string]*spoolWatchEntry{}
	now := time.Now()
	dd.lookAtClientSpools(ctx, entries, true, now) // both seen for the first time: not settled yet
	// Look until a pass has published the first, advancing the watcher's clock a tick per look. On a
	// loaded host the publication after the slow part can run out of what is left of the line's own
	// drainLineDeadline; that pass ended on the line's deadline, not its budget, consumed neither
	// spool, and put both on the doubling wait, which the advancing clock reaches within a few looks.
	// The row is about the pass that published the first and then stopped on its budget.
	deadline := time.Now().Add(liveOrderBound)
	for !spoolWatchPublished(dd, first.Nonce) {
		require.True(t, time.Now().Before(deadline), "a spool a budget-spent pass left was not passed again")
		now = now.Add(spoolWatchTick)
		dd.lookAtClientSpools(ctx, entries, false, now)
	}
	require.False(t, spoolWatchPublished(dd, second.Nonce), "fixture: the budget was spent before the second")

	left := entries["client-8202.ndjson"]
	require.NotNil(t, left)
	require.Positive(t, left.passes, "the passes count toward the retry horizon")
	due, waiting := left.retryDue(now, dd.spool.horizon)
	require.True(t, waiting)
	require.True(t, due,
		"a spool a budget-spent pass left is due at the next look, not after %s", spoolRetryAfter(spoolWatchTick, 1))
}

// TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached: a pass its budget stopped did not
// judge the spools it never finished, which are due again at the next look, but it did judge the ones
// it reached and finished: a spool whose line waits on an arrival nothing publishes keeps the doubling
// wait. The watcher set every due spool due again after such a pass, and under load nearly every pass
// ends on its budget, so a blocked spool was re-read and its file re-synced at every look until the
// horizon, the fsync pressure the back-off exists to spare a loaded host. Here a blocked spool comes
// first, then one whose publication outlasts the pass's budget, then one the pass never reaches.
func TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	// Session "stuck": arrival 0 is leased and then lost, so its arrival 1, which only a client spool
	// holds, can never pass the ordering gate.
	const stuck core.SessionID = "sess-spool-reached-stuck"
	_, ok := dd.ing.leaseDelivery(ctx, spD3Prompt(dd, root, stuck, orderNonce(0), "lost"))
	require.True(t, ok)
	blocked := spD3Prompt(dd, root, stuck, orderNonce(1), "blocked")
	slowReq := liveOrderTool(dd, root, "sess-spool-reached-slow", 1)
	behind := liveOrderTool(dd, root, "sess-spool-reached-behind", 2)
	slow := idleRunBudget + spoolWatchTick // longer than a pass's budget, inside drainLineDeadline
	require.Less(t, slow, drainLineDeadline, "fixture: the slow line must fit its own deadline")
	spoolWatchSlowDrain(dd, map[string]bool{slowReq.Nonce: true}, slow)
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	dd.spool.every, dd.spool.horizon = spoolWatchTick, liveOrderBound
	// Pass order is lexical.
	writeHookSpool(t, root, "client-8601.ndjson", blocked)
	writeHookSpool(t, root, "client-8602.ndjson", slowReq)
	writeHookSpool(t, root, "client-8603.ndjson", behind)

	entries := map[string]*spoolWatchEntry{}
	now := time.Now()
	dd.lookAtClientSpools(ctx, entries, true, now) // all seen for the first time: not settled yet
	// Look until a pass has published the slow one, advancing the watcher's clock a tick per look, as
	// TestSpoolWatch_APassItsBudgetCutShortDoesNotBackOffTheSpoolsItLeft does and for its reason.
	deadline := time.Now().Add(liveOrderBound)
	for !spoolWatchPublished(dd, slowReq.Nonce) {
		require.True(t, time.Now().Before(deadline), "no pass published the slow spool")
		now = now.Add(spoolWatchTick)
		dd.lookAtClientSpools(ctx, entries, false, now)
	}
	require.False(t, spoolWatchPublished(dd, behind.Nonce), "fixture: the budget was spent before the last spool")
	require.False(t, spoolWatchPublished(dd, blocked.Nonce), "fixture: the blocked spool cannot publish")

	reached := entries["client-8601.ndjson"]
	require.NotNil(t, reached)
	due, waiting := reached.retryDue(now, dd.spool.horizon)
	require.True(t, waiting, "the blocked spool is still inside its horizon")
	require.False(t, due,
		"a blocked spool the budget-stopped pass reached keeps its back-off (next pass at %s, now %s)",
		reached.next.Format(time.StampMicro), now.Format(time.StampMicro))
	left := entries["client-8603.ndjson"]
	require.NotNil(t, left)
	due, waiting = left.retryDue(now, dd.spool.horizon)
	require.True(t, waiting)
	require.True(t, due, "a spool the budget-stopped pass never reached is due at the next look")
}

// TestDrainClientSpools_ABudgetedPassWhoseSyncsOutlastItsBudgetStillConsumesALine: a budgeted pass
// does bookkeeping before its first line — the spool listing, its progress state, and a sync of each
// spool file it reads — and on a host with a deep fsync queue that alone can outlast the budget.
// A pass that then stopped before its first line made no progress, and neither did the next one it
// was asked for: under CPU and fsync co-load beside two other gate runs every lane row stayed at 2 or
// 3 of 5 acknowledged. The pass must consume a line before its budget can end it. Here the spool
// file's sync takes longer than the whole budget; the pass must still publish the first of two
// spooled deliveries, and stop, its budget spent, before the second.
func TestDrainClientSpools_ABudgetedPassWhoseSyncsOutlastItsBudgetStillConsumesALine(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	first := liveOrderTool(dd, root, "sess-spool-slow-sync", 1)
	second := liveOrderTool(dd, root, "sess-spool-slow-sync", 2)
	writeSpoolLines(t, root, "client-8301.ndjson", first, second)
	dr := newDrainer(dd.drainConfig())
	sync := dr.syncFile
	dr.syncFile = func(path string) error {
		timer := time.NewTimer(idleRunBudget + spoolWatchTick) // longer than the whole budget
		defer timer.Stop()
		<-timer.C
		return sync(path)
	}
	dd.drain.Store(dr)

	_, err := dr.DrainClientSpools(withPassBudget(context.Background(), idleRunBudget))
	require.ErrorIs(t, err, errPassBudgetSpent, "the pass stopped on its budget")
	require.True(t, spoolWatchPublished(dd, first.Nonce), "the pass consumed a line before its budget ended it")
	require.False(t, spoolWatchPublished(dd, second.Nonce), "and started no other once its budget was spent")
}

// TestSpoolWatch_DoesNothingWithoutAKick: the watcher never polls an idle daemon. A client spool on
// disk with no request served is left to the drains that already cover an idle daemon; the next
// served request kicks the watcher, which then publishes it.
func TestSpoolWatch_DoesNothingWithoutAKick(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	startSpoolWatch(t, dd, spoolWatchTick, liveOrderBound)

	const sess core.SessionID = "sess-spool-quiet"
	spooled := spD3Prompt(dd, root, sess, orderNonce(0), "p0")
	writeSpoolLines(t, root, "client-7272.ndjson", spooled)

	quiet := time.NewTimer(10 * spoolWatchTick)
	<-quiet.C
	require.Zero(t, dd.m.Counter(counterSpoolWatchDrains).Value(), "no request served, so no look and no pass")
	require.FileExists(t, filepath.Join(paths.Of(root).Spool, "client-7272.ndjson"))

	dd.noteServed(ipc.OpStatus)
	require.Eventually(t, func() bool { return spoolWatchPublished(dd, spooled.Nonce) },
		liveOrderBound, liveOrderTick, "a served request kicks the watcher, which publishes the spool")
	require.Eventually(t, func() bool { return spoolWatchGone(root, "client-7272.ndjson") },
		liveOrderBound, liveOrderTick, "the consumed client spool is released")
}

// TestDrainClientSpools_LeavesWALSegmentsToTheWorkerPool: the watcher's pass reads client spools
// only. A WAL line the worker pool is publishing right now makes a full drain stop that file with
// "delivery still in progress"; the client-spool pass never reads it, so it neither fails nor
// consumes it, and it publishes the client spool beside it.
func TestDrainClientSpools_LeavesWALSegmentsToTheWorkerPool(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	ctx := context.Background()

	const sess core.SessionID = "sess-client-only"
	acceptPrompt(t, dd, spD3Prompt(dd, root, sess, orderNonce(0), "p0"))
	inFlight := <-dd.ing.ring
	_, acquired := dd.ing.seen.begin(inFlight.key) // a worker is publishing it right now
	require.True(t, acquired)
	t.Cleanup(func() { dd.ing.seen.finish(inFlight.key, false) })

	const other core.SessionID = "sess-client-only-other"
	spooled := spD3Prompt(dd, root, other, orderNonce(1), "p0")
	writeSpoolLines(t, root, "client-7171.ndjson", spooled)

	n, err := dd.drain.Load().DrainClientSpools(ctx)
	require.NoError(t, err, "the WAL line in flight is not the client-spool pass's to read")
	require.Equal(t, 1, n, "the client spool is published")
	require.NoFileExists(t, filepath.Join(paths.Of(root).Spool, "client-7171.ndjson"))
	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.False(t, j.acknowledged(inFlight.lease.Delivery), "the WAL line was left to its worker")

	_, err = dd.Drain(ctx)
	require.Error(t, err, "control: a full drain does read the WAL and meets the line in flight")
}

// TestDrainClientSpools_DefersTheSpooledCopyOfADeliveryInFlight: a hook whose ACK came too late
// spools the delivery the daemon had already accepted, so a client spool can hold a copy of a
// delivery a worker is publishing right now. That is the ordinary shape of a late ACK: the pass
// leaves the copy for later without failing the file (no drain_file_error, no error), and the pass
// after the worker has published absorbs the copy and releases the spool.
func TestDrainClientSpools_DefersTheSpooledCopyOfADeliveryInFlight(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	ctx := context.Background()

	const sess core.SessionID = "sess-late-ack"
	req := spD3Prompt(dd, root, sess, orderNonce(0), "p0")
	acceptPrompt(t, dd, req)
	inFlight := <-dd.ing.ring
	_, acquired := dd.ing.seen.begin(inFlight.key) // its worker is publishing it right now
	require.True(t, acquired)
	writeSpoolLines(t, root, "client-8181.ndjson", req) // the hook's late-ACK copy: same nonce

	n, err := dd.drain.Load().DrainClientSpools(ctx)
	require.NoError(t, err, "a copy of a delivery in flight is not a failure of its spool file")
	require.Zero(t, n)
	require.Zero(t, dd.m.Counter(counterDrainFileError).Value())
	require.FileExists(t, filepath.Join(paths.Of(root).Spool, "client-8181.ndjson"), "the copy waits")

	dd.ing.seen.finish(inFlight.key, false) // the worker's claim ends, and the worker publishes it
	dd.ing.dispatch(ctx, dd.runIngested, inFlight)
	require.True(t, spoolWatchPublished(dd, req.Nonce), "the live copy published")

	n, err = dd.drain.Load().DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "the copy is absorbed, not published a second time")
	require.NoFileExists(t, filepath.Join(paths.Of(root).Spool, "client-8181.ndjson"), "the absorbed copy is released")
}

// TestSpoolWatch_KnowsTheClientSpoolFamily pins the file families the watcher tells apart: a client
// spool is ipc's client-<pid>.ndjson, never a WAL segment, a blob or a stray file.
func TestSpoolWatch_KnowsTheClientSpoolFamily(t *testing.T) {
	require.True(t, isClientSpoolName("client-123.ndjson"))
	require.False(t, isClientSpoolName("wal-sess.ndjson"))
	require.False(t, isClientSpoolName("wal-sess.3.ndjson"))
	require.False(t, isClientSpoolName("blob-1-2.bin"))
	require.False(t, isClientSpoolName("client-123.ndjson.tmp"))
}

// TestSpoolRetryAfter_Doubles pins the retry schedule the watcher's doc comment states: two, four,
// eight ... intervals, and no overflow however many passes a spool has had.
func TestSpoolRetryAfter_Doubles(t *testing.T) {
	require.Equal(t, 2*spoolCheckInterval, spoolRetryAfter(spoolCheckInterval, 1))
	require.Equal(t, 4*spoolCheckInterval, spoolRetryAfter(spoolCheckInterval, 2))
	require.Equal(t, 8*spoolCheckInterval, spoolRetryAfter(spoolCheckInterval, 3))
	require.Positive(t, spoolRetryAfter(spoolCheckInterval, 1<<20))
}

// TestSpoolWatch_TheIdleTickKicksItInSpoolSubmode (V6 close-out D55, wave 16b): in spool submode no
// hot-path hook connects, so no served request kicks the watcher, and a session's spooled captures
// waited for a non-hot request or the idle drain two quiet minutes later. Run's idle tick now kicks
// it while the hot path is in spool submode, and the watcher's ordinary look and pass publish the
// spool. In sync submode the tick does not kick: the hooks' own requests do. The row runs the tick
// Run runs (onIdleTick, wave 16c), with the project active, so the tick's idle tasks (the idle drain
// among them) do not run and only its kick can publish the spool: without the kick the row fails.
func TestSpoolWatch_TheIdleTickKicksItInSpoolSubmode(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	ctx := context.Background()
	// The project is active: a session's hooks ran just now, so the tick runs no idle task.
	dd.idle.Notify(core.NowMilli(dd.clk))

	dd.onIdleTick(ctx)
	select {
	case <-dd.spool.kick:
		t.Fatal("a tick in sync submode kicked the watcher")
	default:
	}

	startSpoolWatch(t, dd, spoolWatchTick, liveOrderBound)
	dd.applyHotPathTransition(ToSpool)
	require.Equal(t, ipc.HotSpool, dd.registry.HotMode(), "fixture sanity: the daemon is in spool submode")
	const sess core.SessionID = "sess-spool-idle-tick"
	spooled := liveOrderTool(dd, root, sess, 1)
	writeHookSpool(t, root, "client-8181.ndjson", spooled)

	quiet := time.NewTimer(10 * spoolWatchTick)
	<-quiet.C
	require.Zero(t, dd.m.Counter(counterSpoolWatchDrains).Value(),
		"control: with no request served and no tick, nothing looks at the spool")
	require.False(t, spoolWatchPublished(dd, spooled.Nonce))

	require.False(t, dd.idle.IsIdle(dd.onIdleTick(ctx)),
		"fixture sanity: the project is active, so the tick ran no idle drain; its kick is all it did")
	require.Eventually(t, func() bool { return spoolWatchPublished(dd, spooled.Nonce) },
		liveOrderBound, liveOrderTick, "the tick's kick lets the watcher publish the spooled Read")
	require.Eventually(t, func() bool { return spoolWatchGone(root, "client-8181.ndjson") },
		liveOrderBound, liveOrderTick, "the consumed client spool is released")
}

// spoolIndexed reports whether the spool index holds the client spool base.
func spoolIndexed(dd *daemon, base string) bool {
	dd.spoolHeads.mu.Lock()
	defer dd.spoolHeads.mu.Unlock()
	_, ok := dd.spoolHeads.files[base]
	return ok
}

// TestSpoolWatch_IndexesTheSpoolsItsPassLeavesForThePreCompactSettle (D55, wave 16b): a spool the
// watcher's pass cannot consume (its line waits on an earlier arrival nothing will publish) is
// indexed by the watcher, off the hook path, so another session compacting beside it reads no spool
// file at all: its settle's cost is the listing.
func TestSpoolWatch_IndexesTheSpoolsItsPassLeavesForThePreCompactSettle(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	startSpoolWatch(t, dd, spoolWatchTick, liveOrderBound)
	ctx := context.Background()

	const stuck core.SessionID = "sess-spool-index-stuck"
	lost := spD3Prompt(dd, root, stuck, orderNonce(0), "lost")
	_, ok := dd.ing.leaseDelivery(ctx, lost)
	require.True(t, ok)
	writeSpoolLines(t, root, "client-8282.ndjson", spD3Prompt(dd, root, stuck, orderNonce(1), "blocked"))
	dd.kickSpoolWatch()
	require.Eventually(t, func() bool {
		return dd.m.Counter(counterSpoolWatchDrains).Value() >= 1 && spoolIndexed(dd, "client-8282.ndjson")
	}, liveOrderBound, liveOrderTick, "the watcher's pass leaves the spool, and the watcher indexes it")
	require.FileExists(t, filepath.Join(paths.Of(root).Spool, "client-8282.ndjson"), "fixture sanity: it stays")

	reads := dd.spoolHeads.reads.Load()
	require.Nil(t, dd.settleBeforeSeal(ctx, "sess-spool-index-healthy", 0))
	require.Equal(t, reads, dd.spoolHeads.reads.Load(),
		"a healthy session compacting beside the indexed spool reads no spool file")
	require.Zero(t, dd.m.Counter(counterPrecompactSpoolReads).Value())
	require.Zero(t, dd.m.Counter(counterPrecompactSettle).Value())
}
