package daemon

import (
	"context"

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
	// stays pending (WAL retained) and is retried by a later drain, never spun on.
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
