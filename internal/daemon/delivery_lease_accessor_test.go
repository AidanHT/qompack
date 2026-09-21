package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// T20 — design §6.2, O1. Lock.openDeliveryJournal no longer reads the lock FILE when its journal is
// already open and usable: that read ran under Lock.mu on every Accept, a serial section outside
// any group commit (J-AB2). Detection is relocated, not dropped. Every operation on the returned
// journal re-reads ownership itself before it appends or answers: a lease and an acknowledgement
// once per batch (Lock.ownedByFile, between enter and leave), acknowledged through owned under
// Lock.mu. This test pins both halves: the accessor still hands out the journal after the lock was
// replaced, and nothing that journal is then asked to do gets done. O1 is flagged in the design for
// the owner's countersign (Q6); if it is refused, the accessor assertion changes with it.
func TestDeliveryJournal_AccessorOwnershipIsRecheckedPerBatch(t *testing.T) {
	t.Run("a lease after the lock was replaced", func(t *testing.T) {
		root, lock, journal := newTestDeliveryJournal(t)
		ctx := context.Background()
		request := testDeliveryRequest("t20")
		leased, err := journal.lease(ctx, testDeliveryToken('a'), "t20", request)
		require.NoError(t, err)
		ing := newIngest(root, config.Defaults(), logging.Nop(), nil, newFakeClock(epoch))
		t.Cleanup(func() { _ = ing.Close() })
		ing.journal = lock.openDeliveryJournal

		replaceTestLock(t, root)
		positionPath := filepath.Join(paths.Of(root).State, deliveryPositionFile)
		journalBefore, positionBefore := readTestFile(t, journal.path), readTestFile(t, positionPath)

		// O1: the accessor reads no lock file while its journal is open and usable...
		got, err := lock.openDeliveryJournal()
		require.NoError(t, err, "O1: the accessor keeps only the in-memory checks for an open journal")
		require.Same(t, journal, got)

		// ...so the journal's own operations refuse, before anything is appended or answered.
		_, err = journal.lease(ctx, testDeliveryToken('b'), "t20", request)
		require.ErrorIs(t, err, core.ErrDegraded, "a replaced lock must not authorize a new lease")
		_, err = journal.lease(ctx, testDeliveryToken('a'), "t20", request)
		require.ErrorIs(t, err, core.ErrDegraded, "nor answer a known nonce for its old owner")
		require.ErrorIs(t, journal.acknowledge(ctx, leased.Delivery, leased.ObservationID, core.Hash{}), core.ErrDegraded,
			"nor commit a frontier record")
		require.False(t, journal.acknowledged(leased.Delivery))

		// The WAL retains the delivery, but a lost owner cannot ACK or dispatch it.
		nonce, err := ipc.NewDeliveryNonce()
		require.NoError(t, err)
		req := ipc.Request{Op: ipc.OpObserveTool, Session: "t20", TS: benchLeasedBaseTS, Nonce: nonce}
		line, err := ipc.EncodeRequest(req)
		require.NoError(t, err)
		require.ErrorIs(t, ing.Accept(req, line), core.ErrDegraded)
		require.Empty(t, ing.ring, "a refused lease cannot dispatch an identity-free substitute")

		require.Equal(t, journalBefore, readTestFile(t, journal.path), "nothing was appended for the old owner")
		require.Equal(t, positionBefore, readTestFile(t, positionPath), "nothing was sealed for the old owner")
		require.NoError(t, journalFault(journal), "a lost lock refuses without poisoning the handle, as it always has")
	})

	t.Run("a batch queued before the lock was replaced", func(t *testing.T) {
		root, _, journal := newTestDeliveryJournal(t)
		p := newLeaseProbe(journal)
		p.syncGate = newWALGate(t)
		req := testDeliveryRequest("t20")
		lead := holdFirstLeaseBatch(t, p, leaseCall{delivery: leaseToken(0), session: "t20", request: req})
		calls := make([]leaseCall, 8)
		for k := range calls {
			calls[k] = leaseCall{id: k + 1, delivery: leaseToken(k + 1), session: "t20", request: req}
		}
		calls[4] = leaseCall{id: 5, delivery: leaseToken(0), session: "t20", request: req} // a copy of batch 1's
		runs := queueLeases(t, p, calls...)
		replaceTestLock(t, root)
		p.syncGate.release()
		awaitAll(t, append([]*leaseRun{lead}, runs...)...)
		require.NoError(t, lead.err, "batch 1 checked ownership before the lock was replaced")
		for _, r := range runs {
			require.ErrorIsf(t, r.err, core.ErrDegraded, "lease %d was batched after the lock was replaced", r.id)
			require.Equalf(t, deliveryLease{}, r.lease, "lease %d", r.id)
		}
		require.Equal(t, int32(1), p.writes.Load(), "the refused batch never reached the journal")
		require.Equal(t, leaseLines(t, lead), string(readTestFile(t, journal.path)))
		require.NoError(t, journalFault(journal))
	})
}

// replaceTestLock models a lock that was legitimately lost and then reacquired by this same
// process, as TestDeliveryJournal_ReplacedLockCannotReleaseOrLeaseForNewOwner does: the lock file
// and the heartbeat are removed, and a new acquisition with a new owner identity takes the lock.
func replaceTestLock(t *testing.T, root string) *Lock {
	t.Helper()
	lockPath := LockPath(root)
	require.NoError(t, os.Chmod(paths.Long(lockPath), 0o600))
	require.NoError(t, os.Remove(paths.Long(lockPath)))
	require.NoError(t, os.Remove(paths.Long(filepath.Join(paths.Of(root).Run, heartbeatFileName))))
	next, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = next.Release() })
	return next
}

// readTestFile returns the content of p.
func readTestFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err)
	return b
}

// journalFault reads j's fault under st.
func journalFault(j *deliveryJournal) error {
	j.st.Lock()
	defer j.st.Unlock()
	return j.fault
}
