package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// Owner decision D6 (2026-09-23) accepted the rotation pause and the carry bounds as documented
// residuals on the condition that each shows itself when it happens, and that a store's first rotation
// is announced before it. These tests pin that every such event is a Loud line (or, for the advice, a
// Warn) and a counter. None of them asserts a duration: a pause is reported, never judged.

// diagnosedJournal opens root's journal through a lock carrying recording diagnostics.
func diagnosedJournal(t *testing.T, root string) (*deliveryJournal, *recordingLogger, obs.Registry) {
	t.Helper()
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	log, m := attachRecordingDiagnostics(lock)
	j, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	return j, log, m
}

func attachRecordingDiagnostics(lock *Lock) (*recordingLogger, obs.Registry) {
	log, m := newRecordingLogger(), obs.New(newFakeClock(epoch))
	lock.attachDeliveryDiagnostics(&deliveryDiagnostics{log: log, m: m})
	return log, m
}

// kvOf returns the value logged under key in e, and whether it was logged.
func kvOf(e logEntry, key string) (any, bool) {
	for i := 0; i+1 < len(e.KV); i += 2 {
		if k, ok := e.KV[i].(string); ok && k == key {
			return e.KV[i+1], true
		}
	}
	return nil, false
}

func requireKV(t *testing.T, e logEntry, key string, want any) {
	t.Helper()
	got, ok := kvOf(e, key)
	require.True(t, ok, "%q is logged with %q", e.Msg, key)
	require.Equal(t, want, got, "%q: %s", e.Msg, key)
}

func leaseN(t *testing.T, j *deliveryJournal, session core.SessionID, from, n int) []deliveryLease {
	t.Helper()
	out := make([]deliveryLease, 0, n)
	for i := from; i < from+n; i++ {
		l, err := j.lease(context.Background(), genNonce(i), session, testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
		out = append(out, l)
	}
	return out
}

// TestDeliveryDiagnostics_EveryRotationIsLoudAndCounted: each live rotation is one Loud line naming
// the segments, the window it archived, the carry it staged and the pause, and it adds to the rotation
// count, the pause total and the pause histogram.
func TestDeliveryDiagnostics_EveryRotationIsLoudAndCounted(t *testing.T) {
	setRollover(t, 2)
	j, log, m := diagnosedJournal(t, t.TempDir())
	ctx := context.Background()

	leases := leaseN(t, j, "diag", 0, 2)
	require.Zero(t, log.count(logLoud), "no rotation below the threshold, no line")
	require.NoError(t, j.acknowledge(ctx, leases[0].Delivery, leases[0].ObservationID, core.Hash{}))
	leaseN(t, j, "diag", 2, 1) // rotates out of segment 0
	require.Equal(t, uint64(1), j.segment)

	louds := log.entries(logLoud)
	require.Len(t, louds, 1)
	require.Contains(t, louds[0].Msg, "delivery journal rotated")
	requireKV(t, louds[0], "from_segment", uint64(0))
	requireKV(t, louds[0], "to_segment", uint64(1))
	requireKV(t, louds[0], "at_open", false)
	requireKV(t, louds[0], "archived_leases", 2)
	requireKV(t, louds[0], "archived_acks", 1)
	requireKV(t, louds[0], "carried_leases", 1)
	pause, ok := kvOf(louds[0], "pause_ms")
	require.True(t, ok, "the pause is logged")
	require.GreaterOrEqual(t, pause.(int64), int64(0))

	leaseN(t, j, "diag", 3, 2) // fills segment 1 and rotates out of it
	require.Equal(t, uint64(2), j.segment)
	require.Equal(t, 2, log.count(logLoud))
	requireKV(t, log.entries(logLoud)[1], "from_segment", uint64(1))
	// Segment 1 carried lease 1 and holds leases 2 and 3, none acknowledged.
	requireKV(t, log.entries(logLoud)[1], "carried_leases", 3)

	snap := m.Snapshot()
	require.Equal(t, int64(2), snap.Counters[counterDeliveryRotations])
	require.Contains(t, snap.Counters, counterDeliveryRotationPauseMS, "the pause total is published")
	require.Equal(t, int64(2), snap.Hists[histDeliveryRotationPause].N, "each pause is observed once")
	require.Zero(t, snap.Counters[counterDeliveryRotationFailures])
	// At a threshold of 2 the advice point is the threshold itself: one Warn, in segment 0.
	require.Equal(t, 1, log.count(logWarn))
}

// TestDeliveryDiagnostics_RotationFinishedAtOpenIsReported: a rotation an open finishes (an earlier
// owner archived the window and stopped before the transition) is reported like a live one.
func TestDeliveryDiagnostics_RotationFinishedAtOpenIsReported(t *testing.T) {
	setRollover(t, 100)
	root := t.TempDir()
	j := openRolloverJournal(t, root)
	leaseN(t, j, "at-open", 0, 3)
	require.NoError(t, j.reconcileGenerations(context.Background()))
	require.NoError(t, j.owner.Release())

	reopened, log, m := diagnosedJournal(t, root)
	require.Equal(t, uint64(1), reopened.segment)
	louds := log.entries(logLoud)
	require.Len(t, louds, 1)
	require.Contains(t, louds[0].Msg, "delivery journal rotated")
	requireKV(t, louds[0], "at_open", true)
	requireKV(t, louds[0], "archived_leases", 3)
	require.Equal(t, int64(1), m.Counter(counterDeliveryRotations).Value())
}

// TestDeliveryDiagnostics_FailedRotationIsLoudAndCounted: a rotation that fails (here on a damaged
// carry) leaves the journal refusing, and says so, once, with a counter; it is not a carry-bound
// failure.
func TestDeliveryDiagnostics_FailedRotationIsLoudAndCounted(t *testing.T) {
	setRollover(t, 2)
	j, log, m := diagnosedJournal(t, t.TempDir())
	leaseN(t, j, "failed", 0, 3)
	require.Equal(t, uint64(1), j.segment)
	path := filepath.Join(segmentDir(j.stateDir, 1), deliveryCarryFile)
	raw, err := os.ReadFile(paths.Long(path))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(path), raw[:len(raw)-5], 0o600))

	leaseN(t, j, "failed", 3, 1)
	_, err = j.lease(context.Background(), genNonce(4), "failed", testDeliveryRequest(genNonce(4)))
	require.Error(t, err)
	require.NotErrorIs(t, err, errCarryOverBound)

	require.Equal(t, int64(1), m.Counter(counterDeliveryRotations).Value(), "only the first rotation completed")
	require.Equal(t, int64(1), m.Counter(counterDeliveryRotationFailures).Value())
	require.Zero(t, m.Counter(counterDeliveryRotationCarryOverBound).Value())
	louds := log.entries(logLoud)
	require.Len(t, louds, 2)
	require.Contains(t, louds[1].Msg, "rotation failed")
	requireKV(t, louds[1], "from_segment", uint64(1))

	_, err = j.lease(context.Background(), genNonce(5), "failed", testDeliveryRequest(genNonce(5)))
	require.Error(t, err, "the journal fails closed after a failed rotation")
	require.Equal(t, int64(1), m.Counter(counterDeliveryRotationFailures).Value(), "a refused lease is not a second rotation")
}

// carryBytes is the size of the carry a rotation would stage for leases into segment.
func carryBytes(t *testing.T, segment uint64, leases []deliveryLease) int64 {
	t.Helper()
	raw, err := encodeDeliveryCarry(segment, leases)
	require.NoError(t, err)
	return int64(len(raw))
}

func setCarryBound(t *testing.T, n int64) {
	t.Helper()
	prev := deliveryCarryMaxBytes
	deliveryCarryMaxBytes = n
	t.Cleanup(func() { deliveryCarryMaxBytes = prev })
}

// TestDeliveryRollover_CarryPastItsBoundRefusesTheRotation: a rotation whose carried leases would pass
// deliveryCarryMaxBytes refuses before it stages anything — it used to write the carry and commit the
// transition, leaving a segment whose carry the next rotation, the offline check and fsck all refuse
// — and the refusal fails the journal closed with the carry-bound diagnostic.
func TestDeliveryRollover_CarryPastItsBoundRefusesTheRotation(t *testing.T) {
	setRollover(t, 2)
	root := t.TempDir()
	j, log, m := diagnosedJournal(t, root)
	window := leaseN(t, j, "bound", 0, 2)
	setCarryBound(t, carryBytes(t, 1, window)-1) // the two unacknowledged leases no longer fit

	_, err := j.lease(context.Background(), genNonce(2), "bound", testDeliveryRequest(genNonce(2)))
	require.ErrorIs(t, err, errCarryOverBound, "the rotation refuses rather than write a carry past its bound")
	require.Equal(t, uint64(0), j.segment, "no transition committed")
	_, statErr := os.Stat(paths.Long(segmentDir(j.stateDir, 1)))
	require.True(t, os.IsNotExist(statErr), "nothing is staged for a carry that cannot be read back")
	_, err = j.lease(context.Background(), genNonce(3), "bound", testDeliveryRequest(genNonce(3)))
	require.Error(t, err, "the journal fails closed")

	require.Equal(t, int64(1), m.Counter(counterDeliveryRotationFailures).Value())
	require.Equal(t, int64(1), m.Counter(counterDeliveryRotationCarryOverBound).Value())
	require.Zero(t, m.Counter(counterDeliveryRotations).Value())
	louds := log.entries(logLoud)
	require.Len(t, louds, 1)
	require.Contains(t, louds[0].Msg, "carried-lease file's bound")
	requireKV(t, louds[0], "carried_leases", 2)
	requireKV(t, louds[0], "carry_bound_bytes", carryBytes(t, 1, window)-1)

	// Nothing is half-written: the offline check (what fsck --seal-check runs) passes over the refused
	// rotation. And a restart refuses the same rotation again, and says so: the window was archived
	// before the carry was built, so the open finishes the rotation and meets the same bound
	// (docs/troubleshooting.md).
	require.NoError(t, j.owner.Release())
	out, err := checkSeal(t, root)
	require.NoError(t, err, "the offline check passes over the refused rotation: %s", out)
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	reopenLog, reopenM := attachRecordingDiagnostics(lock)
	_, err = lock.openDeliveryJournal()
	require.ErrorIs(t, err, errCarryOverBound, "the open meets the same bound")
	require.Equal(t, int64(1), reopenM.Counter(counterDeliveryRotationCarryOverBound).Value())
	reopened := reopenLog.entries(logLoud)
	require.Len(t, reopened, 1)
	require.Contains(t, reopened[0].Msg, "carried-lease file's bound")
	requireKV(t, reopened[0], "at_open", true)
	_, statErr = os.Stat(paths.Long(segmentDir(j.stateDir, 1)))
	require.True(t, os.IsNotExist(statErr), "and still stages nothing")
}

// TestDeliveryRollover_ACarryAlreadyPastItsBoundIsNamedAsSuch: a committed carry that is past the bound
// (written by a build without the refusal above) stops the next rotation with the carry-bound error,
// so its diagnostic names the bound rather than an anonymous unavailable segment.
func TestDeliveryRollover_ACarryAlreadyPastItsBoundIsNamedAsSuch(t *testing.T) {
	setRollover(t, 2)
	j, log, m := diagnosedJournal(t, t.TempDir())
	leaseN(t, j, "bound-read", 0, 3) // segment 1 carries two leases
	require.Equal(t, uint64(1), j.segment)
	carried := readCarry(t, j.stateDir, 1)
	require.Len(t, carried, 2)
	setCarryBound(t, carryBytes(t, 1, carried)-1)

	leaseN(t, j, "bound-read", 3, 1)
	_, err := j.lease(context.Background(), genNonce(4), "bound-read", testDeliveryRequest(genNonce(4)))
	require.ErrorIs(t, err, errCarryOverBound)
	require.ErrorIs(t, err, errSegmentUnavailable, "it is still an unavailable segment to every other caller")
	require.Equal(t, int64(1), m.Counter(counterDeliveryRotationCarryOverBound).Value())
	require.Contains(t, log.entries(logLoud)[1].Msg, "carried-lease file's bound")
}

// TestDeliveryDiagnostics_FirstRotationIsAdvisedOnceBeforeItHappens: a store that has never rotated
// is warned once, when its window reaches three quarters of the threshold — before the rotation, and
// never again after it.
func TestDeliveryDiagnostics_FirstRotationIsAdvisedOnceBeforeItHappens(t *testing.T) {
	setRollover(t, 8) // advice at 6 leases
	j, log, m := diagnosedJournal(t, t.TempDir())
	leaseN(t, j, "advice", 0, 5)
	require.Zero(t, log.count(logWarn), "not yet near the first rotation")

	leaseN(t, j, "advice", 5, 1)
	require.Equal(t, uint64(0), j.segment, "the advice comes before the rotation")
	warns := log.entries(logWarn)
	require.Len(t, warns, 1)
	require.Contains(t, warns[0].Msg, "rotate for the first time")
	require.Contains(t, warns[0].Msg, "qompack backup create")
	require.Contains(t, warns[0].Msg, "docs/backup.md")
	requireKV(t, warns[0], "window_leases", 6)
	require.Equal(t, int64(1), m.Counter(counterDeliveryFirstRotationBackupAdvised).Value())

	leaseN(t, j, "advice", 6, 20) // the rotation, and two more after it
	require.GreaterOrEqual(t, j.segment, uint64(2))
	require.Equal(t, 1, log.count(logWarn), "the advice is given once")
	require.Equal(t, int64(1), m.Counter(counterDeliveryFirstRotationBackupAdvised).Value())
}

// TestDeliveryDiagnostics_FirstRotationAdviceByBytes: the byte threshold, not only the entry count,
// brings the advice.
func TestDeliveryDiagnostics_FirstRotationAdviceByBytes(t *testing.T) {
	setRollover(t, 1000)
	line, err := json.Marshal(deliveryLease{
		Version: core.EvidenceVersion, Delivery: genNonce(0), Session: "bytes",
		RequestHash: testDeliveryRequest(genNonce(0)), ArrivalSeq: 1,
		ObservationID: func() core.ObservationID {
			id, err := core.NewObservationID("bytes", 1)
			require.NoError(t, err)
			return id
		}(),
	})
	require.NoError(t, err)
	deliveryRolloverBytes = int64(len(line)+1) * 4 // advice at three lines' worth
	j, log, _ := diagnosedJournal(t, t.TempDir())
	leaseN(t, j, "bytes", 0, 2)
	require.Zero(t, log.count(logWarn))
	leaseN(t, j, "bytes", 2, 1)
	require.Equal(t, 1, log.count(logWarn))
	require.Equal(t, uint64(0), j.segment)
}

// TestDeliveryDiagnostics_FirstRotationAdviceAtOpen: a store that already holds most of a window when
// the daemon opens it is warned at the open, and not again at its next admission.
func TestDeliveryDiagnostics_FirstRotationAdviceAtOpen(t *testing.T) {
	setRollover(t, 8)
	root := t.TempDir()
	j := openRolloverJournal(t, root) // no diagnostics attached: nothing to warn through
	leaseN(t, j, "restart", 0, 6)
	require.NoError(t, j.owner.Release())

	reopened, log, m := diagnosedJournal(t, root)
	require.Equal(t, 1, log.count(logWarn), "warned at the open")
	requireKV(t, log.entries(logWarn)[0], "window_leases", 6)
	leaseN(t, reopened, "restart", 6, 1)
	require.Equal(t, 1, log.count(logWarn))
	require.Equal(t, int64(1), m.Counter(counterDeliveryFirstRotationBackupAdvised).Value())
}

// TestDeliveryDiagnostics_AdviceWaitsForSomewhereToReport: a journal with no diagnostics attached does
// not spend its one advice; attached later, the next admission gives it.
func TestDeliveryDiagnostics_AdviceWaitsForSomewhereToReport(t *testing.T) {
	setRollover(t, 8)
	lock, err := acquireTestDeliveryLock(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	j, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	leaseN(t, j, "late", 0, 6)
	log, m := attachRecordingDiagnostics(lock)
	require.Zero(t, log.count(logWarn))
	leaseN(t, j, "late", 6, 1)
	require.Equal(t, 1, log.count(logWarn))
	require.Equal(t, int64(1), m.Counter(counterDeliveryFirstRotationBackupAdvised).Value())
}

// TestDeliveryDiagnostics_StatusShowsTheRotationCounters: the daemon attaches its own logger and
// metrics to the journal, so a rotation shows in the status payload's counters and recent loud lines.
func TestDeliveryDiagnostics_StatusShowsTheRotationCounters(t *testing.T) {
	setRollover(t, 1)
	root := t.TempDir()
	dd, _, _ := newIdentityRecordingDaemon(t, root)
	lockFor(t, dd, root)
	t.Cleanup(func() { _ = dd.currentLock().Release() })
	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	leaseN(t, j, "status", 0, 3)
	require.Equal(t, uint64(2), j.segment)

	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpStatus, Reply: true, TS: core.NowMilli(dd.clk)})
	require.True(t, resp.OK, resp.Err)
	var snap StatusSnapshot
	require.NoError(t, json.Unmarshal(resp.Data, &snap))
	require.Equal(t, int64(2), snap.Counters[counterDeliveryRotations])
	require.Contains(t, snap.Counters, counterDeliveryRotationPauseMS)
	rotated := 0
	for _, l := range snap.LoudTail {
		if strings.Contains(l, "delivery journal rotated") {
			rotated++
		}
	}
	require.Positive(t, rotated, "the rotation is among the recent loud lines: %q", snap.LoudTail)
}
