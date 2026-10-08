package daemon

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// Client spools drained while sessions are active (V6 close-out C1.13, the remainder C1.1 left open).
//
// A hook that cannot hand its delivery to the daemon — its dial failed, the daemon told the hot path
// to spool (HotSpool), or its ACK came too late — appends the delivery to its own client spool
// (client-<pid>-<writer id>.ndjson), which only a drain reads. The daemon's drains ran at startup,
// on its first served request, on a flush, on admin.drain, at Stop, when the ingest's lanes ask for
// one (a parked lane or a refused job, delivery_order.go), and on an idle tick once the whole project
// has been idle for DetectAfterSeconds (120 s by default). None of those is triggered by a client
// spool appearing, and a delivery that never reached the daemon holds no lease, so no lane ever parks
// behind it: it waited for its session's end or two quiet minutes, while everything the session sent
// after it published ahead of it.
//
// The watcher closes that without polling an idle daemon. Every request the daemon serves kicks it
// (noteServed; a PreCompact once it has sealed), and so does Run's idle tick while the hot path is in
// spool submode, when no hook request is served at all (kickSpoolWatchInSpoolSubmode). A kicked
// watcher looks at the spool
// directory once per spoolCheckInterval for as long as kicks keep coming, and once more an interval
// after they stop, so a spool file written just after the last hook (a late ACK spools after the
// request was served) is still seen. A client spool that has stood unchanged across a whole interval
// — its hook has finished writing it, and a live copy of the same delivery, when there is one, has
// had an interval to publish — gets a client-spool drain pass (drainer.DrainClientSpools): bounded by
// idleRunBudget, run on the watcher's own goroutine, never on a worker, under the drain's own mutex,
// ordering gate and frontier. The pass reads no WAL segment, which is the worker pool's.
//
// A spool a pass could not consume — its line waits on an earlier arrival of its session that is
// still publishing, which is the usual reason, or on one nothing will ever publish — is passed over
// again after twice the previous wait (spoolRetryAfter): 2, 4, 8, ... intervals. A pass that stopped
// because its budget was spent (withPassBudget: once the budget is spent and the pass has made
// progress, it finishes the line it is on and starts no other) judged nothing about the spools it left
// unfinished, so those are due again at the next look; the ones it reached and finished keep the
// doubling wait. The retries end once the spool has been waiting for its first pass for longer than the
// idle drain's own horizon (DetectAfterSeconds): past it the idle drain, a drain the lanes ask for (the
// pass leased the line, so its session's next arrival parks behind it and asks), the session's flush or
// a restart takes it, exactly as before. So a spool that can never publish costs a handful of passes,
// not one every interval, and a watcher with no kick and no retry due does nothing at all. The back-off
// limits the passes the spool itself makes due; a pass another spool makes due reads it too, as every
// pass reads every client spool. Once a pass has read it, a later one does not redo what it consumed
// there, nor, while the spool is unchanged, sync it again: it pays the spool's read and its waiting
// lines, within the bounds spoolMemo states, and, while a blob's cleanup waits, one more read of the
// spool for references to that blob (cleanupAcknowledged).
//
// The first retry is due two intervals after the first pass, so a horizon of two intervals or less
// (scheduler.idle.detectAfterSeconds of 4 or less, against spoolCheckInterval's 2 s) leaves the
// watcher no retry at all: a spool its first pass cannot consume waits for those other drains.

// spoolCheckInterval is how often, at most, the watcher looks at the spool while requests keep
// arriving, and how long a client spool must stand unchanged before it gets a pass. It is the pass's
// own budget (idleRunBudget): the drains the ingest's lanes ask for get the same budget and then rest
// as long as they took, so looking more often than once per budget could not start a pass sooner.
// A delivery that reaches only its client spool during an active session is therefore published about
// two intervals after it was spooled, instead of at the session's end or after two idle minutes.
const spoolCheckInterval = idleRunBudget

// counterSpoolWatchDrains counts the client-spool drain passes the watcher ran. (Not "..._passes":
// gosec reads a constant named for passes as a hard-coded credential.)
const counterSpoolWatchDrains = "l0_spool_watch_drains"

// spoolWatcher is the watcher's configuration and its kick. New creates it; tests change the
// durations before the watcher starts, and after that only a config reload changes the horizon,
// through setHorizon, while the watcher's own goroutine (watchClientSpools) reads it through
// horizonNow.
type spoolWatcher struct {
	// kick is signalled by every served request (kickSpoolWatch), a PreCompact's after its seal.
	// Capacity one: kicks merge.
	kick chan struct{}
	// every is spoolCheckInterval.
	every time.Duration
	// mu guards horizon once the watcher runs.
	mu sync.Mutex
	// horizon is how long a spool its passes cannot consume keeps being retried: the idle drain's
	// own DetectAfterSeconds, after which that drain covers it.
	horizon time.Duration
}

func newSpoolWatcher(horizon time.Duration) *spoolWatcher {
	return &spoolWatcher{kick: make(chan struct{}, 1), every: spoolCheckInterval, horizon: horizon}
}

// horizonNow is the watcher's current retry horizon.
func (w *spoolWatcher) horizonNow() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.horizon
}

// setHorizon applies a reloaded idle horizon (scheduler.idle.detectAfterSeconds) to the watcher.
func (w *spoolWatcher) setHorizon(h time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.horizon = h
}

// setDetectAfter applies a reloaded scheduler.idle.detectAfterSeconds, with newIdleController's
// fallback for a value <= 0.
func (c *idleController) setDetectAfter(afterSeconds int) {
	if afterSeconds <= 0 {
		afterSeconds = defaultIdleDetectAfterSeconds
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.afterSeconds = afterSeconds
}

// detectAfter is the idle horizon the controller was built with (DetectAfterSeconds).
func (c *idleController) detectAfter() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Duration(c.afterSeconds) * time.Second
}

// kickSpoolWatch tells the watcher the daemon just served a request. It never blocks: a kick while
// one is pending merges into it. A daemon value that never went through New has no watcher.
func (d *daemon) kickSpoolWatch() {
	if d.spool == nil {
		return
	}
	select {
	case d.spool.kick <- struct{}{}:
	default:
	}
}

// kickSpoolWatchInSpoolSubmode kicks the watcher from Run's idle tick (onIdleTick) while the hot path
// is in spool submode (V6 close-out D55, wave 16b). In spool submode no hot-path hook
// connects (ipc client.go Send step 3), so no served request kicks the watcher, and a session's tool
// results, prompts and Stops waited in their client spools for a non-hot request or for the idle
// drain, DetectAfterSeconds after the last served request: recall lagged by minutes. The tick is
// Run's existing cadence (idleTickMax, or a tenth of idleExitSeconds), so spooled captures now reach
// the store within a tick and the watcher's two intervals. The kick is the watcher's ordinary one: a
// look per interval while kicks keep coming, a pass only for a spool that has stood unchanged for an
// interval, each pass bounded by idleRunBudget as a pass budget, and the retry backoff and horizon
// for a spool its passes cannot consume. No new number. In sync submode the tick does not kick: the
// hooks' own requests do.
func (d *daemon) kickSpoolWatchInSpoolSubmode() {
	if d.registry == nil || d.registry.HotMode() != ipc.HotSpool {
		return
	}
	d.kickSpoolWatch()
}

// spoolWatchEntry is what the watcher remembers about one client spool between two looks.
type spoolWatchEntry struct {
	// size is the file's size at the last look.
	size int64
	// settled is set once the file has stood at size across one whole interval.
	settled bool
	// passes counts the passes run while the file stood at size; first is when the first of them ran,
	// and next is the earliest time for another.
	passes int
	first  time.Time
	next   time.Time
}

// retryDue reports whether e, a settled spool, is due a pass at now, and whether it is still waiting
// for one — due now or later — at all.
func (e *spoolWatchEntry) retryDue(now time.Time, horizon time.Duration) (due, waiting bool) {
	if !e.settled {
		return false, false
	}
	if e.passes == 0 {
		return true, true
	}
	if now.Sub(e.first) >= horizon {
		return false, false // the idle drain's horizon: it is that drain's, and the others', now
	}
	return !now.Before(e.next), true
}

// spoolRetryAfter is the wait after a spool's passes-th pass that did not consume it: twice the wait
// before, starting at two intervals.
func spoolRetryAfter(every time.Duration, passes int) time.Duration {
	return every << min(passes, spoolRetryDoublings)
}

// spoolRetryDoublings caps the shift in spoolRetryAfter so the wait cannot overflow; the horizon ends
// the retries long before a wait that long (2 s << 16 is about 36 hours).
const spoolRetryDoublings = 16

// watchClientSpools is the watcher's loop. Run starts it once the drainer exists and joins it with
// the rest of runWG; it stops when ctx is done. It waits for a kick, then looks at the spool until
// nothing is left to look for, resting between looks.
func (d *daemon) watchClientSpools(ctx context.Context) {
	w := d.spool
	entries := map[string]*spoolWatchEntry{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.kick:
		}
		kicked := true
		for {
			rest, more := d.lookAtClientSpools(ctx, entries, kicked, time.Now())
			if !more {
				break
			}
			t := time.NewTimer(rest)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			select {
			case <-w.kick:
				kicked = true
			default:
				kicked = false
			}
		}
	}
}

// lookAtClientSpools lists the client spools, runs one pass if any of them is due one, and reports
// whether another look is wanted and after how long. Another look is wanted after one interval when
// this look followed a kick (the trailing look: a late ACK spools after its request was served) or
// found a spool that is new or still changing, and otherwise at the earliest retry still due within
// its horizon. With none of those, the watcher goes back to waiting for a kick.
func (d *daemon) lookAtClientSpools(ctx context.Context, entries map[string]*spoolWatchEntry, kicked bool,
	now time.Time,
) (rest time.Duration, more bool) {
	w := d.spool
	horizon := w.horizonNow()
	listed, err := os.ReadDir(paths.Long(paths.Of(d.root).Spool))
	if err != nil {
		listed = nil // no spool directory: nothing was spooled
	}
	present := make(map[string]bool, len(listed))
	unsettled := false
	due := map[string]*spoolWatchEntry{}
	for _, de := range listed {
		if !de.Type().IsRegular() || !isClientSpoolName(de.Name()) {
			continue
		}
		info, ierr := de.Info()
		if ierr != nil {
			continue // gone between the listing and the stat: the next look sees what replaced it
		}
		base := de.Name()
		present[base] = true
		e, seen := entries[base]
		if !seen || e.size != info.Size() {
			// New, or written since the last look (its writer appended to it, or, to a 0.3.0 hook's
			// client-<pid>.ndjson, a later 0.3.0 hook that reused the pid did): it starts over.
			entries[base] = &spoolWatchEntry{size: info.Size()}
			unsettled = true
			continue
		}
		e.settled = true
		if ok, _ := e.retryDue(now, horizon); ok {
			due[base] = e
		}
	}
	for base := range entries {
		if !present[base] {
			delete(entries, base) // consumed and released, or removed by another drain
		}
	}

	if len(due) > 0 {
		if d.m != nil {
			d.m.Counter(counterSpoolWatchDrains).Add(1)
		}
		// A pass budget, not a deadline (withPassBudget): the line the pass is publishing when the
		// budget runs out keeps its own drainLineDeadline and is published.
		pass, budget := newPassBudget(ctx, idleRunBudget)
		if dr := d.drain.Load(); dr != nil {
			// The pass is capture work, as every drain is (Drain, V6 close-out D51).
			d.capture.enter()
			_, perr := dr.DrainClientSpools(pass)
			d.capture.leave()
			if perr != nil && ctx.Err() == nil {
				d.log.Debug("daemon: a client-spool pass ended early", "err", perr)
			}
		}
		// The spools the pass left are indexed for the PreCompact settle (spool_heads.go), bounded
		// like the pass by idleRunBudget, a deadline here: a read it cuts short is only left to the
		// next look that needs it. A session that compacts beside these spools then reads none of them.
		left := make(map[string]bool, len(due))
		for base := range due {
			left[base] = true
		}
		ictx, cancel := context.WithTimeout(ctx, idleRunBudget)
		d.indexClientSpools(ictx, left)
		cancel()
		// A spool the pass left unfinished because its budget was spent has not been found
		// unconsumable: the pass may never have reached it. It is due again at the next look rather
		// than after the doubling wait meant for a spool a pass could not consume. A spool the pass
		// reached and finished without consuming it was found so, however the pass ended, and keeps
		// that wait (TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached). The horizon still
		// runs from each one's first pass.
		for base, e := range due {
			if e.passes == 0 {
				e.first = now
			}
			e.passes++
			if budget.leftUnfinished(base) {
				e.next = now
				continue
			}
			e.next = now.Add(spoolRetryAfter(w.every, e.passes))
		}
	}

	if kicked || unsettled {
		return w.every, true
	}
	// Every entry is settled here (a new or changed one returned above) and has had its first pass
	// (a settled one without it was due, and the pass above counted it), so each still waiting has a
	// next time.
	var earliest time.Time
	for _, e := range entries {
		if _, waiting := e.retryDue(now, horizon); waiting && (earliest.IsZero() || e.next.Before(earliest)) {
			earliest = e.next
		}
	}
	if earliest.IsZero() {
		return 0, false
	}
	return max(earliest.Sub(now), w.every), true
}
