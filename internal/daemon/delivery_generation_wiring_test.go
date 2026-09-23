package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// These tests exercise the LIVE wiring of the generation store into the delivery journal, with the
// seam turned on. They prove the reader/writer half of rollover is really wired — not a dormant
// primitive: an admitted lease is archived into the durable store with its window, an archived nonce's
// redelivery resolves its ORIGINAL lease instead of minting, a session whose active leases were
// archived still continues its arrivals densely, and an acknowledgement is archived with its window and
// settles the store's frontier there.
//
// Criterion change (V6 close-out C1.10): the first and last tests here used to pin that an admitted
// lease or acknowledgement is mirrored into the store AS PART OF ITS COMMIT. That put a multi-file
// durable store commit inside ingest.Accept (B-B) and inside every acknowledgement; the store now
// receives the active window whole when the segment rotates (delivery_rollover_archive_test.go pins the
// hot path writing no generation, the rotation archiving every record exactly, and an archived lease's
// settlement being mirrored at once). The two tests keep their subject and now pin the new contract:
// absent from the store before rotation, exactly present after it.

func withGenerationsEnabled(t *testing.T) {
	t.Helper()
	prev := enableDeliveryGenerations
	enableDeliveryGenerations = true
	t.Cleanup(func() { enableDeliveryGenerations = prev })
}

func openWiredJournal(t *testing.T) (*deliveryJournal, context.Context) {
	t.Helper()
	withGenerationsEnabled(t)
	root := t.TempDir()
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	j, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	require.NotNil(t, j.gen, "the enabled seam must open the generation store")
	return j, context.Background()
}

// TestDeliveryJournal_GenerationArchivesAdmittedLeaseWithItsWindow: an admitted lease is in the active
// window, not the store, until its segment rotates; the rotation archives it exactly (the writer half
// of the wiring).
func TestDeliveryJournal_GenerationArchivesAdmittedLeaseWithItsWindow(t *testing.T) {
	setRollover(t, 1)
	j := openRolloverJournal(t, t.TempDir())
	ctx := context.Background()
	const sess core.SessionID = "sess-wire"
	nonce := genNonce(0)

	l, err := j.lease(ctx, nonce, sess, testDeliveryRequest("wire-1"))
	require.NoError(t, err)
	require.Equal(t, uint64(1), l.ArrivalSeq)
	_, found, err := j.gen.resolveLease(ctx, nonce)
	require.NoError(t, err)
	require.False(t, found, "an admitted lease stays in the active window until its segment rotates")

	_, err = j.lease(ctx, genNonce(1), sess, testDeliveryRequest("wire-2")) // rotates
	require.NoError(t, err)
	require.Equal(t, uint64(1), j.segment)
	got, found, err := j.gen.resolveLease(ctx, nonce)
	require.NoError(t, err)
	require.True(t, found, "the rotation archives the lease into the generation store")
	require.Equal(t, l, got)
}

// TestDeliveryJournal_GenerationResolvesArchivedRedelivery: a nonce that lives only in the store (as a
// rollover would leave an archived, compacted-out lease) resolves to its ORIGINAL lease on redelivery,
// with no fresh mint — proving the decide-consult reader half. The active seal is untouched, so this
// isolates the consult rather than a (correctly refused) desynced active map.
func TestDeliveryJournal_GenerationResolvesArchivedRedelivery(t *testing.T) {
	j, ctx := openWiredJournal(t)
	const sess core.SessionID = "sess-archived-nonce"
	req := testDeliveryRequest("archived-1")
	nonce := genNonce(3)

	archived := testLeaseRecord(t, nonce, sess, req, 1)
	_, err := j.gen.commit(ctx, []deliveryLease{archived})
	require.NoError(t, err)
	require.NotContains(t, j.leases, nonce, "the archived nonce is not in the active window")

	got, err := j.lease(ctx, nonce, sess, req)
	require.NoError(t, err)
	require.Equal(t, archived, got, "an archived nonce's redelivery resolves its original lease, not a new mint")
}

// TestDeliveryJournal_GenerationContinuesArrivalsForAnArchivedSession: a session whose earlier arrivals
// live only in the store (archived) still continues densely — the next active lease is the store's last
// arrival + 1, never a restart at zero.
func TestDeliveryJournal_GenerationContinuesArrivalsForAnArchivedSession(t *testing.T) {
	j, ctx := openWiredJournal(t)
	const sess core.SessionID = "sess-archived"

	// The store holds arrival 2 as this session's history; the active window has nothing for it.
	_, err := j.gen.commit(ctx, []deliveryLease{testLeaseRecord(t, genNonce(0), sess, testDeliveryRequest("a2"), 2)})
	require.NoError(t, err)
	require.NotContains(t, j.arrivals, sess, "the session has no active-window arrivals")

	next, err := j.lease(ctx, genNonce(1), sess, testDeliveryRequest("a3"))
	require.NoError(t, err)
	require.Equal(t, uint64(3), next.ArrivalSeq, "arrivals continue from the store, never restart at zero")
}

// TestDeliveryJournal_GenerationAckArchivesWithItsWindowAndSettlesTheFrontier: an acknowledgement of an
// active-window lease is archived with its window, and the store's per-session settled frontier then
// says exactly which arrivals are settled; an acknowledgement of an archived lease advances it at once.
func TestDeliveryJournal_GenerationAckArchivesWithItsWindowAndSettlesTheFrontier(t *testing.T) {
	setRollover(t, 3)
	j := openRolloverJournal(t, t.TempDir())
	ctx := context.Background()
	const sess core.SessionID = "sess-ack"

	var leases []deliveryLease
	for i := 0; i < 3; i++ {
		l, err := j.lease(ctx, genNonce(i), sess, testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
		leases = append(leases, l)
	}
	require.NoError(t, j.acknowledge(ctx, leases[0].Delivery, leases[0].ObservationID, core.Hash{}))
	_, has, err := j.gen.sessionFrontier(ctx, sess)
	require.NoError(t, err)
	require.False(t, has, "nothing is archived yet: the store has no frontier for the session")

	_, err = j.lease(ctx, genNonce(3), sess, testDeliveryRequest(genNonce(3))) // rotates
	require.NoError(t, err)
	f, has, err := j.gen.sessionFrontier(ctx, sess)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, uint64(2), f, "arrival 1 was acked in its window: the oldest pending archived is 2")

	require.NoError(t, j.acknowledge(ctx, leases[1].Delivery, leases[1].ObservationID, core.Hash{}))
	f, has, err = j.gen.sessionFrontier(ctx, sess)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, uint64(3), f, "acking archived arrival 2 advances the frontier at once")

	require.NoError(t, j.acknowledge(ctx, leases[2].Delivery, leases[2].ObservationID, core.Hash{}))
	_, has, err = j.gen.sessionFrontier(ctx, sess)
	require.NoError(t, err)
	require.False(t, has, "every archived arrival acked: no pending frontier")
}
