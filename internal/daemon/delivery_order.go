package daemon

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

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
	// is retried when the lane is next joined or woken (below), and the parked lane asks for a drain
	// (ingest.requestDrain), which retries it too. Since the lanes dispatch a session in arrival order,
	// a live deferral means the predecessor never reached the worker pool (a ring drop, a job the lanes
	// refused, a reordered ring, a spooled or drain-owned delivery).
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
// active window is scanned over the journal's per-file lease map (bounded, no I/O) under Lock.mu then
// st, exactly as acknowledged does. The ARCHIVED predecessors are the generation store's frontier for
// the session, and that read runs with neither lock held (V6 close-out rollover review, finding 5): it
// holds an archive-read slot instead (beginArchiveReadLocked), which keeps a rotation from archiving
// between the window scan and the store read, so the two see one history. Each answer is a fact that
// stays true once true — a settlement is never undone — so a true answer is still true when used; a
// journal that began closing or faulted during the read answers false. A false answer is a retryable
// defer: the caller preserves the WAL bytes and blob and releases seen ownership.
func (j *deliveryJournal) predecessorsAcknowledged(session core.SessionID, arrival uint64) bool {
	if j == nil || j.owner == nil {
		return false
	}
	j.owner.mu.Lock()
	if !j.owner.owned() {
		j.owner.mu.Unlock()
		return false
	}
	j.st.Lock()
	unlock := func() {
		j.st.Unlock()
		j.owner.mu.Unlock()
	}
	if j.closing || j.closed || j.rotating || j.fault != nil {
		unlock()
		return false
	}
	if arrival <= 1 {
		unlock()
		return true // journal readable and owned AND first arrival: no predecessor
	}
	for _, l := range j.leases {
		if l.Session != session || l.ArrivalSeq >= arrival {
			continue
		}
		if _, ok := j.acks[l.Delivery]; !ok && j.terminal[l.Delivery] != terminalFor(l) {
			unlock()
			return false
		}
	}
	if j.gen == nil {
		unlock()
		return true
	}
	gen := j.beginArchiveReadLocked()
	unlock()
	frontier, pending, err := gen.sessionFrontier(context.Background(), session)
	return j.endArchiveRead() && err == nil && (!pending || frontier >= arrival)
}

// leaseHeld returns the lease already recorded for nonce WITHOUT creating one, and whether there is
// one. It never leases, never reassigns identity, and reads under the same discipline as
// acknowledged; it returns an error on an unreadable journal (never a proven absence). The drain uses it to tell a
// leased-then-denied line from a never-leased one (coordinator item 4). A nonce the active window does
// not hold is looked up in the generation store with neither Lock.mu nor st held, under an archive-read
// slot, exactly as predecessorsAcknowledged reads the store: no rotation can move the nonce from the
// window into the store between the two reads, so "in neither" is a proven absence.
func (j *deliveryJournal) leaseHeld(nonce string) (deliveryLease, bool, error) {
	if j == nil || j.owner == nil || nonce == "" {
		return deliveryLease{}, false, deliveryJournalError()
	}
	j.owner.mu.Lock()
	if !j.owner.owned() {
		j.owner.mu.Unlock()
		return deliveryLease{}, false, deliveryJournalError()
	}
	j.st.Lock()
	unlock := func() {
		j.st.Unlock()
		j.owner.mu.Unlock()
	}
	if j.closing || j.closed || j.rotating || j.fault != nil {
		unlock()
		return deliveryLease{}, false, deliveryJournalError()
	}
	l, ok := j.leases[nonce]
	if ok || j.gen == nil {
		unlock()
		return l, ok, nil
	}
	gen := j.beginArchiveReadLocked()
	unlock()
	l, ok, err := gen.resolveLease(context.Background(), nonce)
	if !j.endArchiveRead() {
		return deliveryLease{}, false, deliveryJournalError()
	}
	return l, ok, err
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
// drop, a job the lanes refused, a reordered ring, a line the drain owns), or its publication failed
// — stays at the head, and the owner keeps the lane only if something signalled it while that
// dispatch ran. Otherwise the lane PARKS: no owner, no polling, no spin. It is run again by the next
// job that joins it, and by wake, which the drain calls once a pass that published or retired one
// of the session's leased lines has ended (drain.go releaseSessions), so a predecessor the drain
// acknowledges releases the live successors queued behind it. Parked jobs hold no seen ownership,
// so the drain can publish them itself; the lane then finds them complete and drops them.
//
// A lane that parks on a head only a drain can now publish, or that runs dry after the lanes refused
// one of its session's jobs, asks for that drain itself (ingest.requestDrain, served by
// daemon.drainOnRequest) rather than leaving the session to the daemon's other drains, which run only
// on a flush, admin.drain, a restart or after DetectAfterSeconds (120 s) of project-wide idleness.
//
// Nothing here is durable, and nothing needs to be: every queued job's bytes are already in the WAL
// and its identity in the lease journal. A job the lanes cannot hold (laneCapacity, or its session's
// laneSessionCapacity) is refused exactly as a full ring drops one, and the drain delivers it. Crash
// and restart semantics are therefore those of the ring: the WAL is retained until a durable
// acknowledgement, and no identity is minted here.

// laneCapacity bounds the jobs held across every lane. The lanes are the ring's continuation (a job
// leaves the ring for a lane), so they get the ring's own bound.
const laneCapacity = ringCapacity

// laneSessionCapacity bounds the jobs one session's lane holds. Without it one session whose head
// can never publish — its capture or its handler fails on every retry, or it waits on a predecessor
// nothing can publish — kept queuing its later arrivals behind that head until the lanes held
// laneCapacity of them, and every other session's live jobs were refused from then on (C1.1 review
// F1). It is the drain's own bound on the deferred lines of one spool file it holds in memory
// (orderingLookaheadBound), because a lane is that look-ahead's live counterpart for one session.
// laneCapacity is four such lanes: several sessions stuck at once can still fill the lanes, and their
// jobs are then refused like any overflow — left in the WAL for a drain the lanes ask for, never
// lost and never waited on.
const laneSessionCapacity = orderingLookaheadBound

// counterOrderingLaneFull counts leased jobs the lanes could not hold, because every lane together
// or the job's own session was at its bound. Each one is left, durable, in the WAL for the drain, as a
// full ring's job is.
const counterOrderingLaneFull = "l0_ordering_lane_full"

// counterOrderingDrainRequested counts the drains the lanes asked for (ingest.requestDrain): a lane
// parked on a head only a drain can now publish, or jobs the lanes or the ring could not hold.
// Requests made while one is pending merge, so this counts requests, not passes.
const counterOrderingDrainRequested = "l0_ordering_drain_requested"

// dispatchOutcome is what one dispatch of a job tells its lane.
type dispatchOutcome int

const (
	// dispatchPending: the job may still need a live dispatch — the gate deferred it or a stage of
	// its publication failed. Its WAL bytes are retained. It is the zero value, so a dispatch that
	// panicked is pending.
	dispatchPending dispatchOutcome = iota
	// dispatchSettled: nothing is left for a live dispatch to do — the job reached the committed
	// frontier, was already complete, or was retired by a proven policy denial.
	dispatchSettled
	// dispatchBusy: another handler owns the delivery right now — the drain, which publishes it
	// itself. The lane treats it as pending but asks for no drain: the pass that owns it releases the
	// session when it ends.
	dispatchBusy
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
	// settled counts the jobs the lane has settled. A flush waiting for the lane reads it to tell a
	// lane still publishing from one that has stopped (awaitLaneQuiet).
	settled uint64
	// overflow records that the lanes refused one of this session's jobs while a worker owned the
	// lane. That job is only in the WAL now, so when the lane next parks or runs dry its owner asks
	// for a drain.
	overflow bool
	// changed is closed, and cleared, at the lane's next settle or once it is quiet: no worker owns
	// it and no wake has listed it for one. It exists only while someone waits for either (watch).
	changed chan struct{}
}

// dispatchLanes holds every session's lane. mu is a leaf lock: nothing is called while it is held.
type dispatchLanes struct {
	mu         sync.Mutex
	capacity   int
	perSession int
	held       int
	lanes      map[core.SessionID]*sessionLane
	// ready lists parked lanes a wake found with queued jobs, each at most once (sessionLane.woken),
	// so it never holds more entries than there are lanes.
	ready []core.SessionID
}

// newDispatchLanes returns lanes that hold at most capacity jobs in all and perSession jobs of any
// one session.
func newDispatchLanes(capacity, perSession int) *dispatchLanes {
	return &dispatchLanes{capacity: capacity, perSession: perSession, lanes: map[core.SessionID]*sessionLane{}}
}

// join queues j in its session's lane. own reports that no worker owned the lane, so the caller now
// does and must run it (ingest.runLane). full reports that a job was refused — every lane together,
// or its session's lane, is at its bound — and stays durable in the WAL for the drain. The refused
// job is j, unless j is an earlier arrival than the lane's latest: the lowest arrivals are the ones
// that can publish next (j may be the very predecessor the lane is parked on), so j takes the latest
// one's place and that one is refused instead. drain then reports that nothing else will ask for
// the refused job's drain: no worker owns the lane to ask for it when the lane parks or runs dry
// (settle, head), so the caller asks. A second job for a delivery already queued (the same nonce
// accepted twice) is not queued again, but still counts as a signal.
func (ls *dispatchLanes) join(j job) (own, full, drain bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	sess := j.lease.Session
	l := ls.lanes[sess]
	if l == nil {
		if ls.held >= ls.capacity || ls.perSession <= 0 {
			return false, true, true
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
		if ls.held >= ls.capacity || len(l.jobs) >= ls.perSession {
			if pos == len(l.jobs) {
				if l.running {
					l.overflow = true
					return false, true, false
				}
				return false, true, true
			}
			last := len(l.jobs) - 1
			l.jobs[last] = job{} // drop the request the backing array would otherwise keep
			l.jobs = l.jobs[:last]
			ls.held--
			l.overflow = true // its owner, or the caller about to become it, asks for the drain
			full = true
		}
		l.jobs = append(l.jobs, job{})
		copy(l.jobs[pos+1:], l.jobs[pos:])
		l.jobs[pos] = j
		ls.held++
	}
	l.signals++
	if l.running {
		return false, full, false
	}
	l.running = true
	return true, full, false
}

// head returns the owner's next job, the lane's lowest arrival, with the lane's signal count as the
// owner read it. An empty lane is released and forgotten, and ok is false; drain then reports that
// the lanes refused one of the session's jobs while the owner ran the lane, so the owner asks for
// the drain that job needs.
func (ls *dispatchLanes) head(sess core.SessionID) (j job, signals uint64, ok, drain bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	l := ls.lanes[sess]
	if l == nil {
		return job{}, 0, false, false
	}
	if len(l.jobs) == 0 {
		drain = l.overflow
		l.overflow = false
		l.stopRunning()
		if !l.woken {
			delete(ls.lanes, sess)
		}
		return job{}, 0, false, drain
	}
	return l.jobs[0], l.signals, true, false
}

// settle records the owner's outcome for j and reports whether the owner goes on. A settled job
// leaves the lane and the owner takes the next. A pending or busy job stays queued; the owner goes on
// only if the lane was signalled while it dispatched (a join, which may be the missing predecessor,
// or a wake after a drain pass published one of the session's lines), and otherwise parks the lane: it
// gives up ownership and the next join or wake runs it again. drain reports that the parked lane
// needs a drain nothing else will ask for: its head is pending — only a drain can publish a
// predecessor the worker pool never had, and only a drain retries a failed head before the session's
// next arrival — or the lanes refused one of its jobs while it ran.
func (ls *dispatchLanes) settle(sess core.SessionID, j job, outcome dispatchOutcome, signals uint64) (goOn, drain bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	l := ls.lanes[sess]
	if l == nil {
		return false, false
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
		l.settled++
		l.notify()
		return true, false
	}
	if l.signals != signals {
		return true, false
	}
	drain = outcome == dispatchPending || l.overflow
	l.overflow = false
	l.stopRunning()
	return false, drain
}

// park gives up ownership of sess's lane without touching its queue; a lane with nothing queued is
// forgotten, as head forgets it.
func (ls *dispatchLanes) park(sess core.SessionID) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if l := ls.lanes[sess]; l != nil {
		l.stopRunning()
		if len(l.jobs) == 0 && !l.woken {
			delete(ls.lanes, sess)
		}
	}
}

// forget drops sess's queued jobs unless a worker owns its lane, and reports how many it dropped.
// The flush calls it once SessionEnd has run: whatever is still parked then is published by a drain
// or by nothing, every job of it is already durable in the WAL, and a lane parked on a head that
// never publishes would otherwise hold its jobs until the daemon restarts. A lane a worker owns is
// left to its owner, which releases it once it runs dry or parks.
func (ls *dispatchLanes) forget(sess core.SessionID) int {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	l := ls.lanes[sess]
	if l == nil || l.running {
		return 0
	}
	n := len(l.jobs)
	ls.held -= n
	delete(ls.lanes, sess) // a listed lane that is gone is simply unlisted by claimReady
	l.notify()
	return n
}

// stopRunning gives up the lane's ownership. dispatchLanes.mu must be held.
func (l *sessionLane) stopRunning() {
	l.running = false
	l.noteQuiet()
}

// noteQuiet releases whoever waits on the lane, if it is now quiet. dispatchLanes.mu must be held.
func (l *sessionLane) noteQuiet() {
	if !l.running && !l.woken {
		l.notify()
	}
}

// notify releases whoever waits on the lane (watch). dispatchLanes.mu must be held.
func (l *sessionLane) notify() {
	if l.changed != nil {
		close(l.changed)
		l.changed = nil
	}
}

// watch reports whether sess's lane is quiet — no worker owns it and no wake has listed it for one,
// so it is empty, absent or parked with nothing on its way to run it — and, if it is not, how many
// jobs it has settled so far and a channel closed at its next settle or once it is quiet.
func (ls *dispatchLanes) watch(sess core.SessionID) (changed <-chan struct{}, settled uint64, quiet bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	l := ls.lanes[sess]
	if l == nil || (!l.running && !l.woken) {
		return nil, 0, true
	}
	if l.changed == nil {
		l.changed = make(chan struct{})
	}
	return l.changed, l.settled, false
}

// awaitLaneQuiet waits until no worker owns sess's lane, and reports whether it got there. It gives
// up when ctx is done, or when stall passes without one of the lane's jobs settling: a lane that keeps
// publishing is waited for however long its backlog takes within ctx, and one that has stopped
// settling is not waited for any longer. why names the bound it gave up on. A quiet lane can still
// hold parked jobs: they wait on a predecessor the worker pool does not have. The waits are on real
// time (a timer and ctx), never on the daemon's clock.
func (i *ingest) awaitLaneQuiet(ctx context.Context, sess core.SessionID, stall time.Duration) (quiet bool, why string) {
	timer := time.NewTimer(stall)
	defer timer.Stop()
	var last uint64
	for first := true; ; first = false {
		changed, settled, quiet := i.lanes.watch(sess)
		if quiet {
			return true, ""
		}
		if !first && settled != last {
			timer.Reset(stall)
		}
		last = settled
		select {
		case <-changed:
		case <-timer.C:
			return false, fmt.Sprintf("its lane settled no delivery for %s", stall)
		case <-ctx.Done():
			return false, "its deliveries were still publishing when the settle limit expired"
		}
	}
}

// ---------------------------------------------------------------------------
// SessionEnd after the session's own deliveries (V6 close-out C1.1)
//
// SessionEnd is the session's last arrival. The lanes publish a session's deliveries one at a time,
// so under a burst of hooks they lag the hooks, and the flush hook can arrive while earlier ones are
// still queued or parked. The observer's SessionEnd closes the session's segment at its current turn
// and forgets the session: anything of the session published afterwards lands on a fresh observer
// session at the wrong turn, and the flush's own drain meets the delivery still in flight and stops
// with "delivery still in progress". So the flush first lets the session settle, bounded.

// settleSessionStall bounds how long a flush waits for its session's lane to settle one more
// delivery: a lane still publishing is waited for, one that has stopped is not. It is Stop's own
// bound on draining the in-flight ring, used for the same job on one session.
const settleSessionStall = stopDrainBound

// settleSessionHeadroom is what the flush keeps back from its own deadline, after the settle, for
// SessionEnd, the marker, the sketches and the start of the flush's final drain. It is Stop's bound
// on the one step of those it can name, a drain.
const settleSessionHeadroom = stopDrainBound

// sessionEndHook is the host hook event the flush route serves.
const sessionEndHook = "SessionEnd"

// settleSessionLimit bounds the whole settle. The flush's own deadline is the manifest's SessionEnd
// timeout less the slack that already puts the PreCompact route's deadline inside the hook client's
// reply wait (precompactDeadlineSlack): SessionEnd ships with the same 20 s host timeout, and
// internal/cli waits the same 15 s for its reply (flushReplyDeadline), so the same nesting holds —
// the daemon's 14 s inside the client's 15 s inside the host's 20 s. The settle takes that deadline
// less settleSessionHeadroom: 9 s with the shipped manifest. A manifest with no SessionEnd timeout
// (not this build's own) leaves the settle one stall bound, the fixed bound it had before.
func settleSessionLimit() time.Duration {
	timeout := time.Duration(manifestHookTimeoutMs(sessionEndHook)) * time.Millisecond
	if limit := timeout - precompactDeadlineSlack - settleSessionHeadroom; limit > settleSessionStall {
		return limit
	}
	return settleSessionStall
}

// counterFlushUnsettled counts flushes whose SessionEnd ran while some of the session's earlier
// leased deliveries were still unpublished, or while the committed frontier could not be read to
// tell. They are not lost: they stay pending in the WAL, the flush's final drain or a later one
// replays them, and the flush's recovery marker is cleared only by a replay that completes. What they
// lose is the session they belonged to, which SessionEnd closed.
const counterFlushUnsettled = "l0_flush_unsettled"

// settleSession runs before the flush's SessionEnd. It waits for sess's lane to go quiet: for as long
// as the lane keeps settling deliveries, up to settleSessionLimit, and no longer than
// settleSessionStall once it stops. Then, if the committed frontier still lacks any of the session's
// leased arrivals (a lane parked behind a predecessor only the WAL or a client spool holds), or
// cannot be read to tell, it runs one drain pass, which publishes them in arrival order and, when the
// pass ends, wakes the lane for whatever the pass had to leave to it; and it waits for the lane to go
// quiet once more. drain is flushRoute's own: a flush replayed by the drain must not drain again
// (drainer.mu is not reentrant), so it only waits. The waits are on real time (context deadlines,
// timers and channels), never on the daemon's clock. A session that does not settle is not waited
// for any longer, and not silently: noteUnsettled counts and announces it before SessionEnd runs.
func (d *daemon) settleSession(ctx context.Context, sess core.SessionID, drain bool) {
	stall, limit := settleSessionStall, settleSessionLimit()
	if d.ing.settleStall > 0 {
		stall = d.ing.settleStall
	}
	if d.ing.settleLimit > 0 {
		limit = d.ing.settleLimit
	}
	sctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	for drained := false; ; drained = true {
		if quiet, why := d.ing.awaitLaneQuiet(sctx, sess, stall); !quiet {
			d.noteUnsettled(sess, why)
			return
		}
		delivered, known := d.sessionDelivered(sess)
		if known && delivered {
			return
		}
		if !drain || drained {
			if !known {
				d.noteUnsettled(sess, "the committed frontier is unreadable, so whether its deliveries are published is unknown")
			} else {
				d.noteUnsettled(sess, "some of its leased deliveries are not on the committed frontier")
			}
			return
		}
		if _, err := d.Drain(sctx); err != nil {
			d.log.Warn("daemon: flush: the session's drain before SessionEnd did not finish",
				"session", string(sess), "err", err)
		}
	}
}

// noteUnsettled counts and announces a SessionEnd about to run ahead of some of its session's own
// earlier deliveries, or without being able to tell whether it does (counterFlushUnsettled).
func (d *daemon) noteUnsettled(sess core.SessionID, reason string) {
	if d.m != nil {
		d.m.Counter(counterFlushUnsettled).Add(1)
	}
	d.log.Loud("daemon: flush: SessionEnd runs before the session settled; its pending deliveries stay "+
		"in the WAL for replay", "session", string(sess), "reason", reason)
}

// sessionDelivered reports whether every leased arrival of sess is on the committed frontier or
// retired. known is false when the journal cannot be read — none is held, or it is closing, closed,
// rotating or faulted — and delivered then means nothing: the ordering gate fails closed on an
// unreadable frontier, and so does the flush, which never takes one for settled.
func (d *daemon) sessionDelivered(sess core.SessionID) (delivered, known bool) {
	j, err := d.deliveryJournal()
	if err != nil || j == nil {
		return false, false
	}
	last, ok := j.lastArrival(sess)
	if !ok {
		return false, false
	}
	if last == 0 || j.predecessorsAcknowledged(sess, last+1) {
		return true, true
	}
	// predecessorsAcknowledged also answers false for a journal that became unreadable after
	// lastArrival read it; tell the two apart.
	_, ok = j.lastArrival(sess)
	return false, ok
}

// lastArrival returns sess's highest leased arrival (0 when it has none), read under the discipline
// predecessorsAcknowledged uses. ok is false when the journal cannot be read.
func (j *deliveryJournal) lastArrival(session core.SessionID) (uint64, bool) {
	if j == nil || j.owner == nil {
		return 0, false
	}
	j.owner.mu.Lock()
	defer j.owner.mu.Unlock()
	if !j.owner.owned() {
		return 0, false
	}
	j.st.Lock()
	defer j.st.Unlock()
	if j.closing || j.closed || j.rotating || j.fault != nil {
		return 0, false
	}
	if last, known := j.arrivals[session]; known {
		return last, true
	}
	if j.gen != nil {
		last, _, err := j.gen.lastArrival(context.Background(), session)
		return last, err == nil
	}
	return 0, true
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
				l.noteQuiet()
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

// ---------------------------------------------------------------------------
// Drains the lanes ask for (V6 close-out C1.1, review F2/F8)

// drainOnRequest runs one bounded drain pass for each request the ingest makes (ingest.requestDrain):
// a lane parked on a head only a drain can now publish, or jobs the lanes or the ring could not
// hold. Without it those waited for a flush, admin.drain, a restart or DetectAfterSeconds of
// project-wide idleness. Each pass gets the idle drain's own budget (idleRunBudget), so it holds the
// drain's mutex no longer than an idle pass would, and the requester then rests as long as the pass
// took, so requested passes take at most half of its time however often the lanes ask: a session
// whose head fails on every retry can make it drain again and again, but never back to back. Requests
// made during a pass or its rest merge into the next one. The pass's release of the sessions it
// consumed (DrainConfig.Released) is what wakes their parked lanes. Run starts it once the drainer
// exists and joins it with the rest of runWG; it stops when ctx is done.
func (d *daemon) drainOnRequest(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.ing.drainKick:
		}
		began := time.Now()
		pass, cancel := context.WithTimeout(ctx, idleRunBudget)
		if _, err := d.Drain(pass); err != nil && ctx.Err() == nil {
			d.log.Debug("daemon: a drain the lanes asked for ended early", "err", err)
		}
		cancel()
		rest := time.NewTimer(time.Since(began))
		select {
		case <-ctx.Done():
			rest.Stop()
			return
		case <-rest.C:
		}
	}
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
