package store

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// One GC pass per store at a time (V6 close-out w6-gcserial).
//
// Every session end runs a pass (observer.OnSessionEnd) and the ends run concurrently since C1.15; the
// idle scheduler runs one too (internal/daemon gcTask). Two passes that overlap race on everything a
// pass writes: both resume the same gc.json cursor and each adds that cursor's counts to its own
// report and subtracts its freed bytes from bytesOnDisk again — which state/store.json persists and
// the quota reads, so the store's size is under-reported for good (w6-gcserial runs/04: Stats.Bytes 0
// with 3,506 bytes of live objects on disk). One pass's clearGCState can also delete the cursor
// another just saved, the two can write gc-live.bin under each other and tombstone the same dead
// roots twice, and both compact retention-roots.jsonl. So passes on one store are serialized here.
//
// What a request that finds a pass running does: it WAITS, and it is answered only by a pass that
// STARTED after it arrived. It never joins the pass already running, because that pass harvested its
// retention sources and snapshotted the index before the request existed: whatever the request's
// caller made collectible since (a session's end, a released lease, a removed checkpoint) is
// invisible to it, and answering the request with its result would lose a request that could free
// space. Every request that waits behind one pass is answered together by ONE follow-up pass:
//
//   - When a pass ends, it answers each waiting request that arrived before it started and that it
//     serves (gcOutcome.serves). The rest keep waiting.
//   - Then the next pass is elected: the oldest waiting request's kind of pass (retention, dry run,
//     quota and outcome bound, gcPolicyKey) runs next, started by whichever request of that kind
//     grants the longest Deadline, so a request that grants less is not cut short by being the
//     starter. The elected request runs the pass on its own goroutine and context, exactly as an
//     uncontended caller does, and every other request of that kind is answered by it.
//   - A pass does not serve a request that its starter's cancellation cut short, one whose Deadline
//     was shorter than the request's and ran out, or one that panicked. Such a request waits for the
//     next pass instead (serves). Nothing drops a request except its own caller's context.
//   - A request whose context ends while it waits withdraws and returns ctx.Err(): the wait answers
//     to ctx exactly as a pass does (GC's doc), so Stop, which cancels every session end, is never
//     held behind a queue. GCPolicy.Deadline still bounds only the pass, from the moment it starts;
//     time spent waiting is not charged to it.
//
// The queue is never longer than the requests actually waiting, and a burst of any number of session
// ends (which all ask for one kind of pass) costs at most two passes' time unless Stop cancels them:
// the one running when they arrive and the one follow-up that answers them all. A pass must never
// call GC on its own store — a retention source or a test hook
// that did would wait for the pass it is part of — and nothing in the product does.
//
// Scope. The gate orders the passes of ONE store handle. That is every pass on a project: GC runs only
// inside the daemon (the session end and the idle scheduler), and the daemon opens one writable store
// per project (WireObserver) while holding the project's daemon lock. No command runs GC outside it —
// `qompack fsck --repair` never does (internal/cli/fsck.go, performFsckRepairs) — and every command
// that opens a writable store (fsck --repair's quarantine, backup and restore) first takes the same
// daemon lock (daemon.AcquireLock) and is refused while a daemon runs. So no file lock is needed, and
// none exists that could deadlock with the daemon's writer lease.

// CounterGCQueued counts GC requests that found another pass running on the same store and waited
// for it (gcgate.go).
const CounterGCQueued = "store.gc.queued"

// CounterGCShared counts waiting GC requests answered by a follow-up pass another request started.
const CounterGCShared = "store.gc.shared"

// gcPassFunc runs one GC pass. FSStore.gcPass is the production one; tests of the gate pass their own.
type gcPassFunc func(ctx context.Context, p GCPolicy) (GCReport, error)

// gcGate is one store's GC serializer. Its zero value is ready: no pass running, nothing waiting.
type gcGate struct {
	mu sync.Mutex
	// running reports that a pass is in progress. It is true exactly while one request is inside
	// runGCPass; nothing waits while it is false.
	running bool
	// seq numbers the passes: the running one, or the last to run, is number seq.
	seq uint64
	// queue is every request waiting, in arrival order.
	queue []*gcWaiter
}

// gcFate is what the gate decided for a waiting request. It is written under gcGate.mu, once, just
// before the request's channel is closed, and never changes after that.
type gcFate int

const (
	// gcWaiting: nothing is decided yet.
	gcWaiting gcFate = iota
	// gcElected: the request runs the next pass itself, as pass number seq.
	gcElected
	// gcServed: a pass that started after the request answered it, with out.
	gcServed
	// gcWithdrawn: the request's context had ended when the gate came to elect a pass.
	gcWithdrawn
)

// gcWaiter is one request waiting for a pass that starts after it.
type gcWaiter struct {
	ctx context.Context
	p   GCPolicy
	key gcPolicyKey
	// floor is gcGate.seq when the request arrived: only a pass numbered above it started after it.
	floor uint64
	// ch is closed once fate is decided.
	ch   chan struct{}
	fate gcFate
	seq  uint64    // the pass number the request was elected to run (gcElected)
	out  gcOutcome // the outcome that answered it (gcServed)
}

// gcPolicyKey is what two requests must share for one pass to answer both: the retention windows as
// resolved against the configuration, the dry-run switch, the quota and the outcome bound, each in its
// canonical form. Only the Deadline may differ (gcOutcome.serves).
type gcPolicyKey struct {
	days, sessions int
	dryRun         bool
	quota          int64
	maxOutcomes    int
}

// gcKey resolves p to the key it is matched on. It reads only the store's immutable configuration.
func (s *FSStore) gcKey(p GCPolicy) gcPolicyKey {
	days, sessions := s.resolveRetention(p)
	quota := p.QuotaBytes
	if quota < 0 {
		quota = 0 // a negative quota disables one explicitly, which reads the same as none (GCPolicy)
	}
	maxOutcomes := p.MaxOutcomes
	switch {
	case maxOutcomes == 0:
		maxOutcomes = defaultMaxOutcomes
	case maxOutcomes < 0:
		maxOutcomes = -1
	}
	return gcPolicyKey{days: days, sessions: sessions, dryRun: p.DryRun, quota: quota, maxOutcomes: maxOutcomes}
}

// gcOutcome is how one pass ended, kept to answer the requests that waited for it.
type gcOutcome struct {
	p        GCPolicy
	key      gcPolicyKey
	rep      GCReport
	err      error
	panicked bool
}

// serves reports whether this pass answers w, a request that arrived before it started.
//
// A pass serves a request of its own kind that it ran to its own conclusion for: collected, truncated
// no earlier than the request's own Deadline would have cut it, halted on an unreadable retention
// source (the request's own pass would read the same sources), or failed on the store itself (the same
// failure would meet the request). It does not serve one when the pass stopped for a reason that is
// its starter's and not the request's: the starter's context ended, the starter's shorter Deadline ran
// out, or the pass panicked.
func (o gcOutcome) serves(w *gcWaiter) bool {
	switch {
	case o.panicked:
		return false
	case errors.Is(o.err, context.Canceled), errors.Is(o.err, context.DeadlineExceeded):
		return false
	case o.key != w.key:
		return false
	case o.rep.Truncated && gcDeadlineLonger(w.p.Deadline, o.p.Deadline):
		return false
	}
	return true
}

// report is the outcome's report for one of the requests it answers. Outcomes is copied, so no two
// callers share a slice.
func (o gcOutcome) report() GCReport {
	r := o.rep
	r.Outcomes = slices.Clone(o.rep.Outcomes)
	return r
}

// gcDeadlineLonger reports whether Deadline a grants a pass more time than b. A Deadline that is not
// positive leaves the pass unbounded (GC), which is the longest of all.
func gcDeadlineLonger(a, b time.Duration) bool {
	switch {
	case a <= 0:
		return b > 0
	case b <= 0:
		return false
	default:
		return a > b
	}
}

// serializeGC runs pass for (ctx, p) with no other pass of this store running, per the rules above.
func (s *FSStore) serializeGC(ctx context.Context, p GCPolicy, pass gcPassFunc) (GCReport, error) {
	g := &s.gcq
	key := s.gcKey(p)

	g.mu.Lock()
	if !g.running {
		g.running = true
		g.seq++
		seq := g.seq
		g.mu.Unlock()
		return s.runGCPass(ctx, p, key, seq, pass)
	}
	w := &gcWaiter{ctx: ctx, p: p, key: key, floor: g.seq, ch: make(chan struct{})}
	g.queue = append(g.queue, w)
	waiting := len(g.queue)
	g.mu.Unlock()
	s.count(CounterGCQueued, 1)
	s.log.Debug("store: a gc pass is running on this store; this request waits for the next one",
		"waiting", waiting)

	select {
	case <-w.ch:
	case <-ctx.Done():
		g.mu.Lock()
		if w.fate == gcWaiting {
			g.queue = slices.DeleteFunc(g.queue, func(q *gcWaiter) bool { return q == w })
			w.fate = gcWithdrawn
			g.mu.Unlock()
			return GCReport{}, ctx.Err()
		}
		// The gate decided first. An elected request must still run its pass, even on an ended
		// context (it returns at once), or the gate would stay held with nobody running.
		g.mu.Unlock()
	}
	switch w.fate {
	case gcElected:
		return s.runGCPass(ctx, w.p, w.key, w.seq, pass)
	case gcServed:
		s.count(CounterGCShared, 1)
		return w.out.report(), w.out.err
	default: // gcWithdrawn by the gate: the context had ended before a pass was elected
		return GCReport{}, ctx.Err()
	}
}

// runGCPass runs pass number seq, which the caller holds the gate for, and hands the gate on when it
// ends — also when the pass panics, so a broken pass can never leave every later request waiting.
func (s *FSStore) runGCPass(ctx context.Context, p GCPolicy, key gcPolicyKey, seq uint64, pass gcPassFunc) (GCReport, error) {
	out := gcOutcome{p: p, key: key, panicked: true}
	defer func() { s.finishGCPass(seq, out) }()
	rep, err := pass(ctx, p)
	out.rep, out.err, out.panicked = rep, err, false
	return rep, err
}

// finishGCPass records that pass seq has ended with out: it answers every waiting request out serves
// that arrived before the pass started, then elects the next pass from what is left.
func (s *FSStore) finishGCPass(seq uint64, out gcOutcome) {
	g := &s.gcq
	g.mu.Lock()
	defer g.mu.Unlock()
	g.running = false
	g.queue = slices.DeleteFunc(g.queue, func(w *gcWaiter) bool {
		if w.floor >= seq || !out.serves(w) {
			return false
		}
		w.fate, w.out = gcServed, out
		close(w.ch)
		return true
	})
	g.electLocked()
}

// electLocked starts the next pass for the waiting requests, if any are left. g.mu must be held.
//
// A request whose context has already ended is withdrawn rather than elected, so the pass is never
// handed to a caller that has gone. Of the rest, the oldest request's kind of pass runs next (so no
// kind of request can be starved by another), started by the request of that kind that grants the
// longest Deadline, the oldest of those on a tie.
func (g *gcGate) electLocked() {
	g.queue = slices.DeleteFunc(g.queue, func(w *gcWaiter) bool {
		if w.ctx.Err() == nil {
			return false
		}
		w.fate = gcWithdrawn
		close(w.ch)
		return true
	})
	if len(g.queue) == 0 {
		return
	}
	kind := g.queue[0].key
	best := 0
	for i, w := range g.queue {
		if w.key == kind && gcDeadlineLonger(w.p.Deadline, g.queue[best].p.Deadline) {
			best = i
		}
	}
	w := g.queue[best]
	g.queue = slices.Delete(g.queue, best, best+1)
	g.running = true
	g.seq++
	w.fate, w.seq = gcElected, g.seq
	close(w.ch)
}
