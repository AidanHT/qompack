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
// counter (the status page's counters, and metrics/latency.json for doctor). The one step that cannot
// be undone — a store's FIRST rotation, after which a build that predates segments refuses the journal
// — is announced before it happens, with a Warn that a backup taken before it is the only way back to
// such a build (docs/backup.md).
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
	// counterDeliveryFirstRotationBackupAdvised counts the Warns that the store's first rotation is
	// near: at most one per journal a daemon opens on a store that has never rotated.
	counterDeliveryFirstRotationBackupAdvised = "delivery_first_rotation_backup_advised"
	// histDeliveryRotationPause is the same pause as a distribution, persisted with the other
	// histograms in metrics/latency.json.
	histDeliveryRotationPause = "delivery_rotation_pause"
)

// The counter names above, exported for the doctor row that reads them back from the metrics the
// last daemon persisted (internal/cli). The store's own GC counter is store.CounterGCDeliveryCarryOverBound.
const (
	CounterDeliveryRotations                  = counterDeliveryRotations
	CounterDeliveryRotationPauseMS            = counterDeliveryRotationPauseMS
	CounterDeliveryRotationFailures           = counterDeliveryRotationFailures
	CounterDeliveryRotationCarryOverBound     = counterDeliveryRotationCarryOverBound
	CounterDeliveryFirstRotationBackupAdvised = counterDeliveryFirstRotationBackupAdvised
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

// firstRotationAdviceAt is the window size at which a store that has never rotated is told its first
// rotation is near: three quarters of the way to the rollover threshold, so a full quarter of the
// window (16,384 deliveries at the production threshold) is still ahead when the Warn fires.
func firstRotationAdviceAt(threshold int64) int64 { return threshold - threshold/4 }

// firstRotationAdviceDueLocked reports, at most once per journal, that the legacy segment's lease
// journal has reached the advice point, by entries or by bytes. Only the lease journal is read: in
// segment 0 every acknowledgement settles a lease of the same segment (nothing is archived before the
// first rotation), and an acknowledgement line is shorter than a lease line, so the lease journal
// always reaches the rollover threshold first. It consumes the one advice only when there is somewhere
// to report it. The caller holds st.
func (j *deliveryJournal) firstRotationAdviceDueLocked() bool {
	if j.firstRotationAdvised || j.segment != 0 || !j.rolloverArmed() || j.diagnostics() == nil {
		return false
	}
	if int64(len(j.leases)) < firstRotationAdviceAt(int64(j.rolloverEntries)) &&
		j.bytes < firstRotationAdviceAt(j.rolloverBytes) {
		return false
	}
	j.firstRotationAdvised = true
	return true
}

// adviseFirstRotationIfDue takes st, and outside it reports the advice firstRotationAdviceDueLocked
// found due. The lease admission path calls firstRotationAdviceDueLocked inside its own st section
// and adviseFirstRotation after it; an open calls this.
func (j *deliveryJournal) adviseFirstRotationIfDue() {
	j.st.Lock()
	due, leases := j.firstRotationAdviceDueLocked(), len(j.leases)
	j.st.Unlock()
	if due {
		j.adviseFirstRotation(leases)
	}
}

// adviseFirstRotation is the Warn and the counter for a store whose first rotation is near.
func (j *deliveryJournal) adviseFirstRotation(windowLeases int) {
	d := j.diagnostics()
	if d == nil {
		return
	}
	d.m.Counter(counterDeliveryFirstRotationBackupAdvised).Add(1)
	d.log.Warn("daemon: this project's delivery journal will rotate for the first time soon; after that "+
		"a Qompack build older than segmented rollover refuses the journal, and a backup taken before the "+
		"rotation is the only way back to one: stop the daemon and run `qompack backup create` "+
		"(docs/backup.md)",
		"window_leases", windowLeases, "rotates_at_entries", j.rolloverEntries,
		"rotates_at_bytes", j.rolloverBytes, "state", j.stateDir)
}
