package daemon

import (
	"cmp"
	"slices"

	"github.com/qompack/qompack/internal/core"
)

// The leased jobs no lane holds (V6 close-out D73(b)).
//
// The PreCompact settle names in the checkpoint's drop report every capture of its session it leaves
// unpublished (precompact_settle.go, D55). It learned what those were from the session's lane and from
// the hook client spools, and a leased arrival that neither held was left out without a word: a job
// still waiting in the ring for a worker, one the ring dropped because it was full, one the lanes
// refused because they or the session's lane were at their bound (dispatchLanes.join), and one a
// flush handed back to the WAL (dispatchLanes.forget). Each is durable in the WAL with its lease, and a
// drain replays it, but the seal did not say it was missing.
//
// So the lanes also record, for each such job, what the settle needs to name it (its capture: op,
// nonce, tool_use_id and time, none of the payload) beside its lease: Accept records a leased job
// before it offers it to the ring, and the lanes stop recording it once one of them holds it, and
// record it again when they give it up (an eviction or a refusal in join, a forget). The settle then
// names the recorded jobs of its session the journal does not show settled, and the delivery journal
// itself, which leased every one of them, names whatever the record lacks (unsettledLeases): an
// arrival a daemon before this one accepted, or one past the record's bound. Those it counts by their
// lease alone, since only the WAL holds what they were.
//
// Nothing here is durable, and nothing needs to be: it describes jobs whose bytes are in the WAL and
// whose identity is in the journal, and a restart that loses it leaves the journal to count them. A
// recorded job leaves the record once the journal shows it settled: when a drain pass that published
// or retired one of its session's lines ends (ingest.wakeSession), and when a settle finds it settled.

// unheldCapacity bounds the jobs the record holds across every session: the ring's and the lanes'
// bounds together, which is every leased job the live path can hold at once, so it is reached only
// while the drains fall behind the jobs the live path gives up. A job past it is not recorded; the
// settle still counts it from the journal, by its lease alone.
const unheldCapacity = ringCapacity + laneCapacity

// unheldJob is the record of one leased job no lane holds: its lease, to ask the journal whether it
// has settled and whether it arrived before a PreCompact, and its capture, to name it.
type unheldJob struct {
	lease   deliveryLease
	capture pendingCapture
}

// noteUnheld records j as a leased job no lane holds. An unleased job is not recorded: it has no
// arrival a PreCompact could be after, and nothing in the journal to tell when it has settled.
func (ls *dispatchLanes) noteUnheld(j job) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.noteUnheldLocked(j)
}

// noteUnheldLocked is noteUnheld with dispatchLanes.mu held. A job already recorded is not recorded
// twice, and none is recorded once the record holds unheldCapacity jobs.
func (ls *dispatchLanes) noteUnheldLocked(j job) {
	if !j.leased {
		return
	}
	sess := j.lease.Session
	if _, ok := ls.unheld[sess][j.lease.Delivery]; ok || ls.unheldN >= unheldCapacity {
		return
	}
	if ls.unheld == nil {
		ls.unheld = map[core.SessionID]map[string]unheldJob{}
	}
	if ls.unheld[sess] == nil {
		ls.unheld[sess] = map[string]unheldJob{}
	}
	ls.unheld[sess][j.lease.Delivery] = unheldJob{lease: j.lease, capture: captureOf(j.req)}
	ls.unheldN++
}

// heldLocked stops recording j's delivery: a lane holds it now. dispatchLanes.mu must be held.
func (ls *dispatchLanes) heldLocked(j job) {
	ls.forgetUnheldLocked(j.lease.Session, j.lease.Delivery)
}

// forgetUnheldLocked drops delivery from sess's record, if it is there. dispatchLanes.mu must be held.
func (ls *dispatchLanes) forgetUnheldLocked(sess core.SessionID, delivery string) {
	m := ls.unheld[sess]
	if _, ok := m[delivery]; !ok {
		return
	}
	delete(m, delivery)
	ls.unheldN--
	if len(m) == 0 {
		delete(ls.unheld, sess)
	}
}

// unheldOf returns a copy of sess's recorded jobs, in arrival order.
func (ls *dispatchLanes) unheldOf(sess core.SessionID) []unheldJob {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	out := make([]unheldJob, 0, len(ls.unheld[sess]))
	for _, u := range ls.unheld[sess] {
		out = append(out, u)
	}
	slices.SortFunc(out, func(a, b unheldJob) int { return cmp.Compare(a.lease.ArrivalSeq, b.lease.ArrivalSeq) })
	return out
}

// forgetUnheld drops deliveries from sess's record.
func (ls *dispatchLanes) forgetUnheld(sess core.SessionID, deliveries []string) {
	if len(deliveries) == 0 {
		return
	}
	ls.mu.Lock()
	defer ls.mu.Unlock()
	for _, d := range deliveries {
		ls.forgetUnheldLocked(sess, d)
	}
}

// leaseSettled reports whether the journal j shows l's delivery settled: on the committed frontier, or
// retired by a proven denial. A journal that cannot tell answers false.
func leaseSettled(j *deliveryJournal, l deliveryLease) bool {
	if j.acknowledged(l.Delivery) {
		return true
	}
	retired, err := j.terminalDenied(l)
	return err == nil && retired
}

// pruneUnheld drops from sess's record the jobs the journal shows settled. The drain calls it, through
// wakeSession, once a pass that published or retired one of sess's lines has ended.
func (i *ingest) pruneUnheld(sess core.SessionID) {
	us := i.lanes.unheldOf(sess)
	if len(us) == 0 || i.journal == nil {
		return
	}
	j, err := i.journal()
	if err != nil || j == nil {
		return
	}
	var settled []string
	for _, u := range us {
		if leaseSettled(j, u.lease) {
			settled = append(settled, u.lease.Delivery)
		}
	}
	i.lanes.forgetUnheld(sess, settled)
}

// unsettledLeases returns the leases of session in the journal's active window whose arrival is
// below upTo and whose delivery is neither on the committed frontier nor retired, in arrival order:
// what predecessorsAcknowledged(session, upTo) answers false for. ok is false when the journal cannot
// be read. A lease a rotation archived before it settled is not listed: the generation store keeps a
// session's oldest unsettled arrival, not a list of them (sessionFrontier). The record above still
// names such an arrival when this daemon accepted it; one a daemon before this one accepted, which a
// rotation then archived unsettled, is the one capture the drop report does not count. Every arrival
// of its session since has waited behind it (the ordering gate), and those are counted.
func (j *deliveryJournal) unsettledLeases(session core.SessionID, upTo uint64) (leases []deliveryLease, ok bool) {
	if j == nil || j.owner == nil {
		return nil, false
	}
	j.owner.mu.Lock()
	defer j.owner.mu.Unlock()
	if !j.owner.owned() {
		return nil, false
	}
	j.st.Lock()
	defer j.st.Unlock()
	if j.closing || j.closed || j.rotating || j.fault != nil {
		return nil, false
	}
	for _, l := range j.leases {
		if l.Session != session || l.ArrivalSeq >= upTo {
			continue
		}
		if _, acked := j.acks[l.Delivery]; acked || j.terminal[l.Delivery] == terminalFor(l) {
			continue
		}
		leases = append(leases, l)
	}
	slices.SortFunc(leases, func(a, b deliveryLease) int { return cmp.Compare(a.ArrivalSeq, b.ArrivalSeq) })
	return leases, true
}
