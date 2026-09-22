package daemon

import (
	"context"
	"sort"
	"sync"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
)

// Bounded leased-delivery ordering (V6 SP08-D3 issue 1, coordinator decision
// plans/sdd/V6-remediation/delivery-order-decision.md). EVERY leased observer event may be published
// only after every EARLIER leased arrival of the same session has reached the committed frontier.
// This makes turn assignment follow arrival order, so the first leased arrival of a session takes turn
// 0 rather than whichever worker happened to publish first. It is NOT a claim about host-first order:
// a client-spool delivery not yet leased has no arrival sequence and remains a coverage uncertainty.
//
// Explicit policy denial may retire an existing leased arrival through a separate
// durable terminal disposition. Failed/unknown policy and unreadable identity
// remain pending. Retirement permits later arrivals without fabricating a capture
// ACK; both facts remain independently observable.

const (
	// orderingLookaheadBound caps how many blocked lines the drain BUFFERS (holding their full
	// requests) while looking ahead within one file, and orderingProcessedCap caps how many
	// out-of-order-consumed line offsets it remembers to roll the front over. Both are memory bounds,
	// not scan bounds: the drain keeps SCANNING past them so a predecessor deeper in the file is still
	// reached and acknowledged this pass — overflow entries are not buffered/recorded but are made
	// durable in the ACK journal and skipped (frontier-absorbed) on a later pass. This is what keeps a
	// >bound reversed prefix from wedging: "stop at the bound" would re-read the same prefix forever.
	//
	orderingLookaheadBound = 1024 //nomagic:allow bounded look-ahead memory, independent of configurable budgets.
	orderingProcessedCap   = 4096 //nomagic:allow offset memory bound; overflow relies on the durable frontier.

	// counterOrderingDeferred / counterDrainOrderingDeferred count leased dispatches deferred because
	// an earlier arrival of the session was not yet acknowledged (live worker / drain). The delivery
	// stays pending (WAL retained), never spun on: a live one stays queued in its session's lane and
	// is retried when the lane is next joined or woken (below), and a drain retries it too. Since the
	// lanes dispatch a session in arrival order, a live deferral means the predecessor never reached
	// the worker pool (a ring drop, a reordered ring, a spooled or drain-owned delivery).
	counterOrderingDeferred      = "l0_ordering_deferred"
	counterDrainOrderingDeferred = "l0_drain_ordering_deferred"
	// counterDrainOrderingResolved counts deferred lines a bounded look-ahead un-blocked within the
	// same pass (a reversed WAL-vs-lease order recovered without wedging the file).
	counterDrainOrderingResolved = "l0_drain_ordering_resolved"
	// counterOrderingFrontierUnavailable counts leased dispatches deferred because the committed
	// frontier could not be READ (no journal getter, or the getter failed). It is the explicit
	// diagnostic the fail-closed path emits: publication never proceeds over an unreadable frontier.
	counterOrderingFrontierUnavailable = "l0_ordering_frontier_unavailable"
	// counterDrainLeasedDenyPending counts leased lines the drain preserved pending because they were
	// DENIED on retry while still holding a lease — the terminal-completion gap above.
	counterDrainLeasedDenyPending = "l0_drain_leased_deny_pending"
)

// predecessorsAcknowledged reports whether every LEASED arrival strictly below `arrival` in `session`
// has reached the committed frontier. It is the bounded ordering query the dispatch gate and the
// drain look-ahead consult.
//
// It FAILS CLOSED (coordinator item 1): when the frontier cannot be read — no journal, ownership lost,
// closing, closed or faulted — it returns false, so publication never proceeds over an unreadable
// frontier. The arrival<=1 first-arrival short-circuit is taken ONLY after the journal is confirmed
// readable and owned, so a first arrival on an unusable journal is deferred too, not bypassed. The
// scan is over the journal's per-file lease map (bounded, no I/O) and holds Lock.mu then st exactly as
// acknowledged does. A false answer is a retryable defer: the caller preserves the WAL bytes and blob
// and releases seen ownership.
func (j *deliveryJournal) predecessorsAcknowledged(session core.SessionID, arrival uint64) bool {
	if j == nil || j.owner == nil {
		return false
	}
	j.owner.mu.Lock()
	defer j.owner.mu.Unlock()
	if !j.owner.owned() {
		return false
	}
	j.st.Lock()
	defer j.st.Unlock()
	if j.closing || j.closed || j.rotating || j.fault != nil {
		return false
	}
	if arrival <= 1 {
		return true // journal readable and owned AND first arrival: no predecessor
	}
	if j.gen != nil {
		frontier, pending, err := j.gen.sessionFrontier(context.Background(), session)
		if err != nil || (pending && frontier < arrival) {
			return false
		}
	}
	for _, l := range j.leases {
		if l.Session != session || l.ArrivalSeq >= arrival {
			continue
		}
		if _, ok := j.acks[l.Delivery]; !ok && j.terminal[l.Delivery] != terminalFor(l) {
			return false
		}
	}
	return true
}

// leaseHeld returns the lease already recorded for nonce WITHOUT creating one, and whether there is
// one. It never leases, never reassigns identity, and reads under the same discipline as
// acknowledged; it returns an error on an unreadable journal (never a proven absence). The drain uses it to tell a
// leased-then-denied line from a never-leased one (coordinator item 4).
func (j *deliveryJournal) leaseHeld(nonce string) (deliveryLease, bool, error) {
	if j == nil || j.owner == nil || nonce == "" {
		return deliveryLease{}, false, deliveryJournalError()
	}
	j.owner.mu.Lock()
	defer j.owner.mu.Unlock()
	if !j.owner.owned() {
		return deliveryLease{}, false, deliveryJournalError()
	}
	j.st.Lock()
	defer j.st.Unlock()
	if j.closing || j.closed || j.rotating || j.fault != nil {
		return deliveryLease{}, false, deliveryJournalError()
	}
	l, ok := j.leases[nonce]
	if !ok && j.gen != nil {
		return j.gen.resolveLease(context.Background(), nonce)
	}
	return l, ok, nil
}

// leasedPredecessorsReady is the drain's wrapper. An UNLEASED line keeps its existing qualified
// (legacy) behaviour and is never ordering-gated. A leased line whose journal getter is missing or
// fails cannot read the frontier, so it FAILS CLOSED (defer) and emits the explicit diagnostic —
// publication does not proceed over an unreadable frontier.
func (dr *drainer) leasedPredecessorsReady(lease deliveryLease, leased bool) bool {
	if !leased {
		return true
	}
	if dr.cfg.Journal == nil {
		dr.noteFrontierUnavailable()
		return false
	}
	j, err := dr.cfg.Journal()
	if err != nil || j == nil {
		dr.noteFrontierUnavailable()
		return false
	}
	return j.predecessorsAcknowledged(lease.Session, lease.ArrivalSeq)
}

func (dr *drainer) noteFrontierUnavailable() {
	if dr.cfg.Metrics != nil {
		dr.cfg.Metrics.Counter(counterOrderingFrontierUnavailable).Add(1)
	}
	dr.cfg.Log.Loud("daemon: drain: committed frontier unreadable; leased delivery deferred, WAL retained")
}

// existingLease distinguishes a proven absence from an unavailable journal and
// binds an inherited request to its original lease without assigning an identity.
func (dr *drainer) existingLease(req ipc.Request) (deliveryLease, bool, error) {
	if req.Nonce == "" || dr.cfg.Journal == nil {
		return deliveryLease{}, false, nil
	}
	j, err := dr.cfg.Journal()
	if err != nil || j == nil {
		return deliveryLease{}, false, deliveryJournalError()
	}
	lease, held, err := j.leaseHeld(req.Nonce)
	if err != nil {
		return deliveryLease{}, false, err
	}
	if held && (lease.Session != req.Session || lease.RequestHash != deliveryRequestHash(req)) {
		return deliveryLease{}, false, deliveryJournalError()
	}
	return lease, held, nil
}

// leasedPredecessorsReady is the live worker path's wrapper. Same fail-closed contract: a leased
// delivery whose frontier cannot be read is deferred, not published.
func (i *ingest) leasedPredecessorsReady(lease deliveryLease, leased bool) bool {
	if !leased {
		return true
	}
	if i.journal == nil {
		i.noteFrontierUnavailable()
		return false
	}
	j, err := i.journal()
	if err != nil || j == nil {
		i.noteFrontierUnavailable()
		return false
	}
	return j.predecessorsAcknowledged(lease.Session, lease.ArrivalSeq)
}

func (i *ingest) noteFrontierUnavailable() {
	if i.m != nil {
		i.m.Counter(counterOrderingFrontierUnavailable).Add(1)
	}
	i.log.Loud("daemon: committed frontier unreadable; leased delivery deferred, WAL retained")
}

// ---------------------------------------------------------------------------
// Live same-session dispatch lanes (V6 close-out C1.1)
//
// The gate above answers "may this arrival publish now?". On its own it made the worker pool strand
// work: with several workers, arrival N+1 reached a free worker while arrival N was still publishing,
// the gate deferred N+1, and a deferred delivery was retried only by a drain. N+2 then met an
// unacknowledged N+1 and was deferred too, and so on, so every event of a busy session after the
// first waited for SessionEnd, admin.drain, a restart or an idle tick (C1.1).
//
// A lane serializes one session's leased jobs and keeps them in arrival order; different sessions
// keep their own lanes and publish in parallel. At most one worker owns a lane. It dispatches the
// lowest arrival, and when that settles it takes the next, so N+1 is dispatched only after N has
// reached the committed frontier and the gate passes it without a deferral. No worker ever waits
// for another: a worker whose job joins a lane someone else owns returns to the ring at once.
//
// A job that does not settle — the gate deferred it (its predecessor was never queued here: a ring
// drop, a reordered ring, a line the drain owns), or its publication failed — stays at the head, and
// the owner keeps the lane only if something signalled it while that dispatch ran. Otherwise the
// lane PARKS: no owner, no polling, no spin. It is run again by the next job that joins it, and by
// wake, which the drain calls whenever it consumes a leased line of the session, so a predecessor
// the drain acknowledges releases the live successors queued behind it. Parked jobs hold no seen
// ownership, so the drain can publish them itself; the lane then finds them complete and drops them.
//
// Nothing here is durable, and nothing needs to be: every queued job's bytes are already in the WAL
// and its identity in the lease journal. A job the lanes cannot hold (laneCapacity) is dropped
// exactly as a full ring drops one, and the drain delivers it. Crash and restart semantics are
// therefore those of the ring: the WAL is retained until a durable acknowledgement, and no
// identity is minted here.

// laneCapacity bounds the jobs held across every lane. The lanes are the ring's continuation (a job
// leaves the ring for a lane), so they get the ring's own bound.
const laneCapacity = ringCapacity

// counterOrderingLaneFull counts leased jobs the lanes could not hold. Each one is left, durable, in
// the WAL for the drain, as a full ring's job is.
const counterOrderingLaneFull = "l0_ordering_lane_full"

// dispatchOutcome is what one dispatch of a job tells its lane.
type dispatchOutcome int

const (
	// dispatchPending: the job may still need a live dispatch — the gate deferred it, another
	// handler owns it right now, or a stage of its publication failed. Its WAL bytes are retained.
	// It is the zero value, so a dispatch that panicked is pending.
	dispatchPending dispatchOutcome = iota
	// dispatchSettled: nothing is left for a live dispatch to do — the job reached the committed
	// frontier, was already complete, or was retired by a proven policy denial.
	dispatchSettled
)

// sessionLane is one session's queued leased jobs.
type sessionLane struct {
	// jobs holds the queued jobs in ascending ArrivalSeq, at most one per delivery nonce. The owner's
	// current job stays in jobs until it settles.
	jobs []job
	// running is set while a worker owns the lane.
	running bool
	// woken is set while the lane is listed in dispatchLanes.ready.
	woken bool
	// signals counts every join and every wake. The owner compares it across a dispatch to learn
	// whether anything happened that could let a pending head proceed.
	signals uint64
}

// dispatchLanes holds every session's lane. mu is a leaf lock: nothing is called while it is held.
type dispatchLanes struct {
	mu       sync.Mutex
	capacity int
	held     int
	lanes    map[core.SessionID]*sessionLane
	// ready lists parked lanes a wake found with queued jobs, each at most once (sessionLane.woken),
	// so it never holds more entries than there are lanes.
	ready []core.SessionID
}

func newDispatchLanes(capacity int) *dispatchLanes {
	return &dispatchLanes{capacity: capacity, lanes: map[core.SessionID]*sessionLane{}}
}

// join queues j in its session's lane. own reports that no worker owned the lane, so the caller now
// does and must run it (ingest.runLane). full reports that the lanes are at capacity and j was not
// queued; it stays durable in the WAL for the drain. A second job for a delivery already queued (the
// same nonce accepted twice) is not queued again, but still counts as a signal.
func (ls *dispatchLanes) join(j job) (own, full bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	sess := j.lease.Session
	l := ls.lanes[sess]
	if l == nil {
		if ls.held >= ls.capacity {
			return false, true
		}
		l = &sessionLane{}
		ls.lanes[sess] = l
	}
	pos := sort.Search(len(l.jobs), func(k int) bool { return l.jobs[k].lease.ArrivalSeq >= j.lease.ArrivalSeq })
	duplicate := false
	for k := pos; k < len(l.jobs) && l.jobs[k].lease.ArrivalSeq == j.lease.ArrivalSeq; k++ {
		duplicate = duplicate || l.jobs[k].lease.Delivery == j.lease.Delivery
	}
	if !duplicate {
		if ls.held >= ls.capacity {
			return false, true
		}
		l.jobs = append(l.jobs, job{})
		copy(l.jobs[pos+1:], l.jobs[pos:])
		l.jobs[pos] = j
		ls.held++
	}
	l.signals++
	if l.running {
		return false, false
	}
	l.running = true
	return true, false
}

// head returns the owner's next job, the lane's lowest arrival, with the lane's signal count as the
// owner read it. An empty lane is released and forgotten, and ok is false.
func (ls *dispatchLanes) head(sess core.SessionID) (j job, signals uint64, ok bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	l := ls.lanes[sess]
	if l == nil {
		return job{}, 0, false
	}
	if len(l.jobs) == 0 {
		l.running = false
		if !l.woken {
			delete(ls.lanes, sess)
		}
		return job{}, 0, false
	}
	return l.jobs[0], l.signals, true
}

// settle records the owner's outcome for j and reports whether the owner goes on. A settled job
// leaves the lane and the owner takes the next. A pending job stays queued; the owner goes on only
// if the lane was signalled while it dispatched (a join, which may be the missing predecessor, or a
// wake after the drain consumed one of the session's lines), and otherwise parks the lane: it gives
// up ownership and the next join or wake runs it again.
func (ls *dispatchLanes) settle(sess core.SessionID, j job, outcome dispatchOutcome, signals uint64) bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	l := ls.lanes[sess]
	if l == nil {
		return false
	}
	if outcome == dispatchSettled {
		for k := range l.jobs {
			if l.jobs[k].lease.Delivery == j.lease.Delivery {
				copy(l.jobs[k:], l.jobs[k+1:])
				l.jobs[len(l.jobs)-1] = job{} // drop the request the backing array would otherwise keep
				l.jobs = l.jobs[:len(l.jobs)-1]
				ls.held--
				break
			}
		}
		return true
	}
	if l.signals != signals {
		return true
	}
	l.running = false
	return false
}

// park gives up ownership of sess's lane without touching its queue; a lane with nothing queued is
// forgotten, as head forgets it.
func (ls *dispatchLanes) park(sess core.SessionID) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if l := ls.lanes[sess]; l != nil {
		l.running = false
		if len(l.jobs) == 0 && !l.woken {
			delete(ls.lanes, sess)
		}
	}
}

// wake signals sess's lane that one of its session's deliveries was consumed elsewhere. An owned lane
// only records the signal, which keeps its owner from parking on the view it dispatched with. A
// parked lane with queued jobs is listed as ready; wake reports whether it listed one, and the
// caller then hands the list to a worker.
func (ls *dispatchLanes) wake(sess core.SessionID) bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	l := ls.lanes[sess]
	if l == nil {
		return false
	}
	l.signals++
	if l.running || l.woken || len(l.jobs) == 0 {
		return false
	}
	l.woken = true
	ls.ready = append(ls.ready, sess)
	return true
}

// claimReady takes ownership of the first listed lane that is still parked with queued jobs, and
// reports whether more lanes remain listed. A listed lane that someone else took, or that emptied,
// is simply unlisted.
func (ls *dispatchLanes) claimReady() (sess core.SessionID, ok, more bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	for len(ls.ready) > 0 {
		s := ls.ready[0]
		ls.ready = ls.ready[1:]
		l := ls.lanes[s]
		if l == nil {
			continue
		}
		l.woken = false
		if l.running || len(l.jobs) == 0 {
			if !l.running {
				delete(ls.lanes, s)
			}
			continue
		}
		l.running = true
		return s, true, len(ls.ready) > 0
	}
	ls.ready = nil
	return "", false, false
}

// deferredLine is one leased line the drain read but could not yet publish because an earlier arrival
// of its session was unacknowledged. Its bytes stay in the WAL/spool (the durable offset never passes
// it until it is consumed) and its blob is untouched; a bounded look-ahead re-attempts it after a
// later line acknowledges the missing predecessor.
type deferredLine struct {
	req    ipc.Request
	lease  deliveryLease
	leased bool
	key    core.Hash
	start  int64
	next   int64
}
