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
	"github.com/qompack/qompack/internal/paths"
)

const testDeliveryLeaseFile = "delivery-leases.jsonl"

func TestDeliveryJournal_EqualRequestsHaveDistinctDeliveries(t *testing.T) {
	root, lock, journal := newTestDeliveryJournal(t)
	request := testDeliveryRequest("same permitted request")

	first, err := journal.lease(context.Background(), testDeliveryToken('a'), "session", request)
	require.NoError(t, err)
	second, err := journal.lease(context.Background(), testDeliveryToken('b'), "session", request)
	require.NoError(t, err)

	require.Equal(t, uint64(1), first.ArrivalSeq)
	require.Equal(t, uint64(2), second.ArrivalSeq)
	require.NotEqual(t, first.ObservationID, second.ObservationID,
		"equal permitted request bytes are separate arrivals when their delivery nonces differ")
	require.Equal(t, testDeliveryToken('a'), first.Delivery)
	require.Equal(t, testDeliveryToken('b'), second.Delivery)
	require.Equal(t, request, first.RequestHash)
	require.Equal(t, request, second.RequestHash)

	firstID, err := core.NewObservationID("session", 1)
	require.NoError(t, err)
	secondID, err := core.NewObservationID("session", 2)
	require.NoError(t, err)
	require.Equal(t, firstID, first.ObservationID)
	require.Equal(t, secondID, second.ObservationID)

	// A release closes the journal, so this restart exercises a fresh file handle and a fresh
	// in-memory sequence map rather than merely another call on the original instance.
	require.NoError(t, lock.Release())
	lock, err = acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	journal, err = lock.openDeliveryJournal()
	require.NoError(t, err)

	replayed, err := journal.lease(context.Background(), testDeliveryToken('a'), "session", request)
	require.NoError(t, err)
	require.Equal(t, first, replayed, "a retry must retain the original durable lease")

	third, err := journal.lease(context.Background(), testDeliveryToken('c'), "session", request)
	require.NoError(t, err)
	require.Equal(t, uint64(3), third.ArrivalSeq, "restart resumes the durable per-session sequence")
}

func TestDeliveryJournal_RetryDoesNotAppendAndTokenMustKeepItsBinding(t *testing.T) {
	root, _, journal := newTestDeliveryJournal(t)
	path := filepath.Join(paths.Of(root).State, testDeliveryLeaseFile)
	token := testDeliveryToken('a')
	request := testDeliveryRequest("first")

	first, err := journal.lease(context.Background(), token, "session", request)
	require.NoError(t, err)
	before, err := os.ReadFile(paths.Long(path))
	require.NoError(t, err)

	retry, err := journal.lease(context.Background(), token, "session", request)
	require.NoError(t, err)
	require.Equal(t, first, retry)
	afterRetry, err := os.ReadFile(paths.Long(path))
	require.NoError(t, err)
	require.Equal(t, before, afterRetry, "a known delivery must not append a second lease row")

	for _, mismatch := range []struct {
		name    string
		session core.SessionID
		request core.Hash
	}{
		{name: "request", session: "session", request: testDeliveryRequest("changed")},
		{name: "session", session: "other-session", request: request},
	} {
		t.Run(mismatch.name, func(t *testing.T) {
			_, err := journal.lease(context.Background(), token, mismatch.session, mismatch.request)
			require.ErrorIs(t, err, core.ErrAppendOnly)
			got, readErr := os.ReadFile(paths.Long(path))
			require.NoError(t, readErr)
			require.Equal(t, before, got, "a conflicting nonce must not alter the durable journal")
		})
	}
}

func TestDeliveryJournal_EmptySessionIsAValidIdentityScope(t *testing.T) {
	_, _, journal := newTestDeliveryJournal(t)

	lease, err := journal.lease(context.Background(), testDeliveryToken('a'), "", testDeliveryRequest("request"))
	require.NoError(t, err)
	require.Equal(t, core.SessionID(""), lease.Session)
	require.Equal(t, uint64(1), lease.ArrivalSeq)
	want, err := core.NewObservationID("", 1)
	require.NoError(t, err)
	require.Equal(t, want, lease.ObservationID)
}

func TestDeliveryJournal_RejectsCanceledAndInvalidLeaseInputsWithoutAppending(t *testing.T) {
	root, _, journal := newTestDeliveryJournal(t)
	path := filepath.Join(paths.Of(root).State, testDeliveryLeaseFile)
	before, err := os.ReadFile(paths.Long(path))
	require.NoError(t, err)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = journal.lease(canceled, testDeliveryToken('a'), "session", testDeliveryRequest("request"))
	require.ErrorIs(t, err, context.Canceled)

	for _, tc := range []struct {
		name    string
		token   string
		request core.Hash
	}{
		{name: "empty token", request: testDeliveryRequest("request")},
		{name: "short token", token: strings.Repeat("a", 63), request: testDeliveryRequest("request")},
		{name: "upper case token", token: strings.Repeat("A", 64), request: testDeliveryRequest("request")},
		{name: "zero token", token: strings.Repeat("0", 64), request: testDeliveryRequest("request")},
		{name: "zero request hash", token: testDeliveryToken('a')},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := journal.lease(context.Background(), tc.token, "session", tc.request)
			require.ErrorIs(t, err, core.ErrContract)
		})
	}

	after, err := os.ReadFile(paths.Long(path))
	require.NoError(t, err)
	require.Equal(t, before, after, "rejected work must not create an arrival assignment")
}

func TestDeliveryJournal_IsSingletonPerLockAndStopsWithItsLock(t *testing.T) {
	root, lock, first := newTestDeliveryJournal(t)
	path := filepath.Join(paths.Of(root).State, testDeliveryLeaseFile)

	second, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	require.Same(t, first, second, "one held lock owns one shared journal instance")

	before, err := os.ReadFile(paths.Long(path))
	require.NoError(t, err)
	require.NoError(t, lock.Release())

	_, err = first.lease(context.Background(), testDeliveryToken('a'), "session", testDeliveryRequest("request"))
	require.Error(t, err, "a released lock must not authorize a later lease")
	closed, err := lock.openDeliveryJournal()
	require.Error(t, err, "a released lock must not reopen a journal")
	require.Nil(t, closed)
	after, err := os.ReadFile(paths.Long(path))
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestDeliveryJournal_ReplacedLockCannotReleaseOrLeaseForNewOwner(t *testing.T) {
	root, oldLock, oldJournal := newTestDeliveryJournal(t)
	lockPath := LockPath(root)
	heartbeatPath := filepath.Join(paths.Of(root).Run, heartbeatFileName)

	// This models a lock which was legitimately lost and then reacquired by the same process.
	// PID alone cannot distinguish the old lock object from the new on-disk ownership generation.
	require.NoError(t, os.Chmod(paths.Long(lockPath), 0o600))
	require.NoError(t, os.Remove(paths.Long(lockPath)))
	require.NoError(t, os.Remove(paths.Long(heartbeatPath)))

	newLock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = newLock.Release() })
	newJournal, err := newLock.openDeliveryJournal()
	require.NoError(t, err)

	_, err = oldJournal.lease(context.Background(), testDeliveryToken('a'), "session", testDeliveryRequest("old"))
	require.Error(t, err, "the replaced lock must not authorize a delivery")
	require.NoError(t, oldLock.Release(), "an old owner must not remove the new generation")

	_, err = newJournal.lease(context.Background(), testDeliveryToken('b'), "session", testDeliveryRequest("new"))
	require.NoError(t, err, "the current owner remains able to lease after the old owner releases")
	_, statErr := os.Stat(paths.Long(lockPath))
	require.NoError(t, statErr, "the old owner must not remove the new lock file")
}

func TestDeliveryJournal_RejectsUntrustworthyRowsWithoutChangingThem(t *testing.T) {
	request := testDeliveryRequest("request")
	valid := testLeaseRecord(t, testDeliveryToken('a'), "session", request, 1)
	future := valid
	future.Version++
	invalidIdentity := valid
	invalidIdentity.ObservationID = "not-the-derived-identity"
	gap := testLeaseRecord(t, testDeliveryToken('b'), "session", request, 3)

	validBytes, err := json.Marshal(valid)
	require.NoError(t, err)
	futureBytes, err := json.Marshal(future)
	require.NoError(t, err)
	invalidIdentityBytes, err := json.Marshal(invalidIdentity)
	require.NoError(t, err)
	gapBytes, err := json.Marshal(gap)
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		body []byte
	}{
		{name: "malformed", body: []byte(`{"v":`)},
		{name: "torn final row", body: validBytes},
		{name: "future version", body: append(futureBytes, '\n')},
		{name: "invalid derived identity", body: append(invalidIdentityBytes, '\n')},
		{name: "duplicate identical delivery", body: append(append(validBytes, '\n'), append(validBytes, '\n')...)},
		{name: "gapped sequence", body: append(append(validBytes, '\n'), append(gapBytes, '\n')...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(paths.Of(root).State, testDeliveryLeaseFile)
			require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(path)), 0o700))
			position, err := json.Marshal(deliveryPosition{Version: core.EvidenceVersion, Chain: deliveryChainSeed})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), deliveryPositionFile), position, 0o600))
			require.NoError(t, os.WriteFile(paths.Long(path), tc.body, 0o600))

			lock, err := acquireTestDeliveryLock(root)
			require.NoError(t, err)
			t.Cleanup(func() { _ = lock.Release() })

			journal, err := lock.openDeliveryJournal()
			require.Error(t, err)
			require.Nil(t, journal, "a corrupt lease file must not yield a writable journal")
			got, readErr := os.ReadFile(paths.Long(path))
			require.NoError(t, readErr)
			require.Equal(t, tc.body, got, "recovery must preserve untrusted evidence for diagnosis")
		})
	}
}

func newTestDeliveryJournal(t *testing.T) (string, *Lock, *deliveryJournal) {
	t.Helper()
	root := t.TempDir()
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	journal, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	return root, lock, journal
}

func acquireTestDeliveryLock(root string) (*Lock, error) {
	addr, err := ipc.Resolve(root)
	if err != nil {
		return nil, err
	}
	return AcquireLock(root, addr, newFakeClock(epoch))
}

func testDeliveryToken(char rune) string { return strings.Repeat(string(char), 64) }

func testDeliveryRequest(s string) core.Hash {
	return core.HashBytes("qompack.delivery.lease.test.v1", []byte(s))
}

func testLeaseRecord(t *testing.T, delivery string, session core.SessionID, request core.Hash, arrival uint64) deliveryLease {
	t.Helper()
	id, err := core.NewObservationID(session, arrival)
	require.NoError(t, err)
	return deliveryLease{
		Version:       core.EvidenceVersion,
		Delivery:      delivery,
		Session:       session,
		RequestHash:   request,
		ArrivalSeq:    arrival,
		ObservationID: id,
	}
}

// TestDeliveryJournal_OneSyncAndOneSealPerLease pins the durability COUNT on the ingress path.
//
// Accept is the operation the p99 budget is measured against, and taking a lease adds two of its
// three durability points: one journal fsync and one sealed position sidecar per assignment. Both
// are required (see the audit on ingest.Accept) and neither may quietly become two — a redundant
// sync on this path is a per-event cost paid by every delivery forever.
func TestDeliveryJournal_OneSyncAndOneSealPerLease(t *testing.T) {
	_, _, journal := newTestDeliveryJournal(t)
	ctx := context.Background()

	syncs := 0
	journal.writer = leaseFaultWriter{file: journal.file, sync: func() error {
		syncs++
		return journal.file.Sync()
	}}

	const leases = 4
	for i := 0; i < leases; i++ {
		_, err := journal.lease(ctx, testDeliveryToken(rune('a'+i)), "session", testDeliveryRequest("x"))
		require.NoError(t, err)
		require.Equal(t, i+1, syncs, "one journal fsync per lease, no more and no fewer")

		position, err := journal.loadPosition()
		require.NoError(t, err)
		require.Equal(t, i+1, position.Count, "one sealed position per lease")
		require.Equal(t, journal.bytes, position.Bytes,
			"the seal names the whole synced file, so a truncated journal is detectable")
		require.Equal(t, journal.chain, position.Chain)
	}
	require.Equal(t, leases, syncs)
}
