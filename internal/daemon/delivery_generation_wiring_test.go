package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// These tests exercise the LIVE wiring of the generation store into the delivery journal, with the
// seam turned on. They prove the reader/writer half of rollover is really wired — not a dormant
// primitive: an admitted lease is mirrored into the durable store, an archived nonce's redelivery
// resolves its ORIGINAL lease instead of minting, a session whose active leases were archived still
// continues its arrivals densely, and an acknowledgement mirrors and advances the settled frontier.
// The capacity-freeing compaction stays off (its cross-package retention blocker is documented in
// delivery-capacity-integration-work.md); these tests do not assert it.

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

// TestDeliveryJournal_GenerationMirrorsAdmittedLease: an admitted lease is mirrored into the durable
// store as part of its commit (the writer half of the wiring).
func TestDeliveryJournal_GenerationMirrorsAdmittedLease(t *testing.T) {
	j, ctx := openWiredJournal(t)
	const sess core.SessionID = "sess-wire"
	nonce := genNonce(0)

	l, err := j.lease(ctx, nonce, sess, testDeliveryRequest("wire-1"))
	require.NoError(t, err)
	require.Equal(t, uint64(1), l.ArrivalSeq)

	got, found, err := j.gen.resolveLease(ctx, nonce)
	require.NoError(t, err)
	require.True(t, found, "an admitted lease is mirrored into the generation store")
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

// TestDeliveryJournal_GenerationAckMirrorAdvancesFrontier: an acknowledgement is mirrored into the
// store and advances the per-session settled frontier through the wired live path.
func TestDeliveryJournal_GenerationAckMirrorAdvancesFrontier(t *testing.T) {
	j, ctx := openWiredJournal(t)
	const sess core.SessionID = "sess-ack"

	first, err := j.lease(ctx, genNonce(0), sess, testDeliveryRequest("ack1"))
	require.NoError(t, err)
	second, err := j.lease(ctx, genNonce(1), sess, testDeliveryRequest("ack2"))
	require.NoError(t, err)

	f, has, err := j.gen.sessionFrontier(ctx, sess)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, uint64(1), f, "nothing acked: the oldest pending is arrival 1")

	require.NoError(t, j.acknowledge(ctx, first.Delivery, first.ObservationID, core.Hash{}))
	f, has, err = j.gen.sessionFrontier(ctx, sess)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, uint64(2), f, "acking arrival 1 advances the frontier to 2")

	require.NoError(t, j.acknowledge(ctx, second.Delivery, second.ObservationID, core.Hash{}))
	_, has, err = j.gen.sessionFrontier(ctx, sess)
	require.NoError(t, err)
	require.False(t, has, "every arrival acked: no pending frontier")
}
