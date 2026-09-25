package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// Operator diagnostics for segmented rollover (owner decision D6, 2026-09-23).
//
// D6 accepted two rollover residuals as documented behaviour rather than defects to fix before the
// release: a rotation pauses leases and acknowledgements while it archives the outgoing window (2.3 to
// 6.8 s per full window on the close-out's loaded hosts, once every 65,536 deliveries), and the leases
// a rotation carries have bounds — a store GC pass halts once they pass 65,536, and a rotation refuses
// once their file would pass 64 MiB. An accepted residual must still show itself when it happens, so
// each occurrence here is one Loud line (LOUD.log and the status page's recent loud lines) and a
// counter (the status page's counters, and metrics/latency.json for doctor).
//
// The journal has no logger or registry of its own. The daemon attaches both to the Lock that owns the
// journal (daemon.deliveryJournal); a journal opened without them — the offline tools, and tests that
// build a bare lock — reports nothing and behaves identically.
const (
	// counterDeliveryRotations counts the rotations that completed, live or finished by an open.
	counterDeliveryRotations = "delivery_rotations"
	// counterDeliveryRotationPauseMS sums, in milliseconds, how long those rotations held the barrier
	// that excludes every lease and acknowledgement: from the moment new work started waiting, through
	// the drain of work in flight and the archive, to the switch.
	counterDeliveryRotationPauseMS = "delivery_rotation_pause_ms"
	// counterDeliveryRotationFailures counts rotations that began and did not complete. Each one leaves
	// the journal refusing every lease and acknowledgement until the daemon restarts.
	counterDeliveryRotationFailures = "delivery_rotation_failures"
	// counterDeliveryRotationCarryOverBound counts the failures whose cause was the carry bound: the
	// archived leases without an acknowledgement would not fit deliveryCarryMaxBytes.
	counterDeliveryRotationCarryOverBound = "delivery_rotation_carry_over_bound"
	// histDeliveryRotationPause is the same pause as a distribution, persisted with the other
	// histograms in metrics/latency.json.
	histDeliveryRotationPause = "delivery_rotation_pause"
)

// The counter names above, exported for the doctor row that reads them back from the metrics the
// last daemon persisted (internal/cli). The store's own GC counter is store.CounterGCDeliveryCarryOverBound.
const (
	CounterDeliveryRotations              = counterDeliveryRotations
	CounterDeliveryRotationPauseMS        = counterDeliveryRotationPauseMS
	CounterDeliveryRotationFailures       = counterDeliveryRotationFailures
	CounterDeliveryRotationCarryOverBound = counterDeliveryRotationCarryOverBound
)

// deliveryDiagnostics is where a delivery journal reports. Both members are always set.
type deliveryDiagnostics struct {
	log logging.Logger
	m   obs.Registry
}

// attachDeliveryDiagnostics installs d as the place the journal this lock opens reports to. The first
// attachment wins and later ones are a single atomic load, so the daemon can attach on every journal
// access without a startup ordering to get right.
func (l *Lock) attachDeliveryDiagnostics(d *deliveryDiagnostics) {
	if l == nil || d == nil || l.deliveryDiag.Load() != nil {
		return
	}
	l.deliveryDiag.CompareAndSwap(nil, d)
}

// diagnostics is the place this journal reports to, or nil when nothing was attached.
func (j *deliveryJournal) diagnostics() *deliveryDiagnostics {
	if j == nil || j.owner == nil {
		return nil
	}
	return j.owner.deliveryDiag.Load()
}

// rotationStats is what doRotate records about the window it rotates, for the report that follows
// it. doRotate writes it with the barrier held and its caller reads it before releasing the barrier,
// so it needs no lock of its own.
type rotationStats struct {
	leases, acks, terminals int
	// carried and carryBytes are the next segment's carry; -1 until doRotate has built it.
	carried, carryBytes int
}

// rotateAtOpen is doRotate for the rotation an open finishes (an earlier owner archived the window,
// or froze segment 0, and stopped before the transition), reported like a live one. The journal is not
// shared yet, so the whole of doRotate is the pause.
func (j *deliveryJournal) rotateAtOpen(ctx context.Context) error {
	from, started := j.segment, time.Now()
	err := j.doRotate(ctx)
	j.reportRotation(from, j.segment, time.Since(started), j.rotation, true, err)
	return err
}

// reportRotation reports one rotation that ran: its pause, and whether it completed.
func (j *deliveryJournal) reportRotation(from, to uint64, pause time.Duration, st rotationStats, atOpen bool, err error) {
	d := j.diagnostics()
	if d == nil {
		return
	}
	ms := pause.Milliseconds()
	if err != nil {
		d.m.Counter(counterDeliveryRotationFailures).Add(1)
		kv := []any{
			"from_segment", from, "pause_ms", ms, "at_open", atOpen, "window_leases", st.leases,
			"carried_leases", st.carried, "carry_bytes", st.carryBytes, "err", err.Error(),
		}
		if errors.Is(err, errCarryOverBound) {
			d.m.Counter(counterDeliveryRotationCarryOverBound).Add(1)
			d.log.Loud("daemon: delivery journal rotation refused: the archived leases without an "+
				"acknowledgement would pass the carried-lease file's bound, so the journal refuses every "+
				"lease and acknowledgement, and a restart refuses the same rotation again; deliveries are "+
				"kept in durable input (the ingest WAL or the hook spool) and nothing new is captured "+
				"(docs/troubleshooting.md)",
				append(kv, "carry_bound_bytes", deliveryCarryMaxBytes)...)
			return
		}
		d.log.Loud("daemon: delivery journal rotation failed; the journal refuses every lease and "+
			"acknowledgement until the daemon restarts, which finishes or retries the rotation, and "+
			"deliveries stay in durable input (the ingest WAL or the hook spool) meanwhile "+
			"(docs/troubleshooting.md)", kv...)
		return
	}
	d.m.Counter(counterDeliveryRotations).Add(1)
	d.m.Counter(counterDeliveryRotationPauseMS).Add(ms)
	d.m.Hist(histDeliveryRotationPause).Observe(pause)
	d.log.Loud("daemon: delivery journal rotated; leases and acknowledgements waited for the archive, "+
		"and hooks that could not wait kept their deliveries in the durable spool (docs/troubleshooting.md)",
		"from_segment", from, "to_segment", to, "pause_ms", ms, "at_open", atOpen,
		"archived_leases", st.leases, "archived_acks", st.acks, "archived_terminals", st.terminals,
		"carried_leases", st.carried, "carry_bytes", st.carryBytes)
}
