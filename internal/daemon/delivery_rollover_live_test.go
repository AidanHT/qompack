package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// These tests drive ACTUAL segment rollover across the capacity seam through the live journal API: a
// lease that would exceed the (seam-lowered) threshold drains both pipelines, rotates to a fresh
// segment, and retries — never refusing while capacity can be reclaimed. They exercise >=70 live
// rotations, restart across a rotation, archived-nonce identity, dormant-session arrival continuity, and
// an archived-ACK exact join with the settled frontier retained across the transitions. The legacy four
// files stay the original segment; history is resolved through the generation-store radix, not a
// whole-manifest reread.

// setRollover turns the seam on and lowers the entry threshold so rotation happens after `entries`
// leases in a segment, restoring both on cleanup.
func setRollover(t *testing.T, entries int) {
	t.Helper()
	prevEnable, prevEntries, prevBytes := enableDeliveryGenerations, deliveryRolloverEntries, deliveryRolloverBytes
	enableDeliveryGenerations = true
	deliveryRolloverEntries = entries
	t.Cleanup(func() {
		enableDeliveryGenerations = prevEnable
		deliveryRolloverEntries = prevEntries
		deliveryRolloverBytes = prevBytes
	})
}

func openRolloverJournal(t *testing.T, root string) *deliveryJournal {
	t.Helper()
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	j, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	return j
}

// TestDeliveryRollover_CrossesCapacitySeamAndAdvancesSegment: the lease that would exceed the threshold
// rotates to a new segment and is admitted there, with its arrival dense across the boundary.
func TestDeliveryRollover_CrossesCapacitySeamAndAdvancesSegment(t *testing.T) {
	setRollover(t, 3)
	ctx := context.Background()
	j := openRolloverJournal(t, t.TempDir())
	require.Equal(t, uint64(0), j.segment, "starts on the legacy segment")

	const sess core.SessionID = "s"
	for i := 0; i < 3; i++ {
		l, err := j.lease(ctx, genNonce(i), sess, testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
		require.Equal(t, uint64(i+1), l.ArrivalSeq)
	}
	require.Equal(t, uint64(0), j.segment, "the window is exactly full but has not rolled")

	l4, err := j.lease(ctx, genNonce(3), sess, testDeliveryRequest(genNonce(3)))
	require.NoError(t, err, "a lease past the threshold rotates rather than refusing")
	require.Equal(t, uint64(1), j.segment, "rolled to segment 1")
	require.Equal(t, uint64(4), l4.ArrivalSeq, "arrivals stay dense across the rotation")
}

// TestDeliveryRollover_SeventyLiveRotationsPreserveIdentity runs >=70 live rotations and then re-leases
// every archived nonce, proving each resolves to its ORIGINAL lease (never re-minted) and arrivals never
// restarted.
func TestDeliveryRollover_SeventyLiveRotationsPreserveIdentity(t *testing.T) {
	setRollover(t, 1) // every lease after the first in a segment rolls
	ctx := context.Background()
	project := t.TempDir()
	s, err := store.Open(project, config.Defaults(), store.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	put, err := s.PutBytes(ctx, []byte("the oldest pending delivery retains this content across seventy rotations\n"), store.PutOptions{})
	require.NoError(t, err)
	j := openRolloverJournal(t, project)
	request := func(i int) core.Hash {
		if i == 0 {
			return put.Root.Hash
		}
		return testDeliveryRequest(genNonce(i))
	}

	const sess core.SessionID = "s"
	const n = 73
	for i := 0; i < n; i++ {
		l, err := j.lease(ctx, genNonce(i), sess, request(i))
		require.NoError(t, err)
		require.Equal(t, uint64(i+1), l.ArrivalSeq, "dense arrival at lease %d", i)
	}
	require.GreaterOrEqual(t, j.segment, uint64(70), "at least 70 live segment transitions")

	for i := 0; i < n; i++ {
		got, err := j.lease(ctx, genNonce(i), sess, request(i))
		require.NoError(t, err, "archived nonce %d re-resolves", i)
		require.Equal(t, uint64(i+1), got.ArrivalSeq, "archived nonce %d keeps its original identity", i)
	}
	require.NoError(t, j.owner.Release())
	rep, err := s.GC(ctx, store.GCPolicy{RetainDays: -1, RetainSessions: -1})
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError)
	_, err = s.GetRoot(ctx, put.Root.Hash)
	require.NoError(t, err, "the oldest unsettled lease still retains its content after seventy rotations")
}

// TestDeliveryRollover_RestartAcrossRotation: the active segment and dense arrivals survive a full
// close/reopen after several rotations, and archived nonces still resolve.
func TestDeliveryRollover_RestartAcrossRotation(t *testing.T) {
	setRollover(t, 2)
	ctx := context.Background()
	root := t.TempDir()

	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	j := openJournalFromLock(t, lock)
	const sess core.SessionID = "s"
	for i := 0; i < 6; i++ {
		_, err := j.lease(ctx, genNonce(i), sess, testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
	}
	seg := j.segment
	require.GreaterOrEqual(t, seg, uint64(2))
	require.NoError(t, lock.Release())

	j2 := openRolloverJournal(t, root)
	require.Equal(t, seg, j2.segment, "the active segment survives a restart")
	// an archived nonce resolves to its original lease
	got, err := j2.lease(ctx, genNonce(0), sess, testDeliveryRequest(genNonce(0)))
	require.NoError(t, err)
	require.Equal(t, uint64(1), got.ArrivalSeq)
	// a new delivery continues dense from the restored history
	next, err := j2.lease(ctx, genNonce(6), sess, testDeliveryRequest(genNonce(6)))
	require.NoError(t, err)
	require.Equal(t, uint64(7), next.ArrivalSeq)
}

func openJournalFromLock(t *testing.T, lock *Lock) *deliveryJournal {
	t.Helper()
	j, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	return j
}

// TestDeliveryRollover_DormantSessionContinuesArrivals: a session that leased once and then went dormant
// across many rotations still continues its arrivals densely when it returns.
func TestDeliveryRollover_DormantSessionContinuesArrivals(t *testing.T) {
	setRollover(t, 1)
	ctx := context.Background()
	j := openRolloverJournal(t, t.TempDir())

	const dormant core.SessionID = "dormant"
	const busy core.SessionID = "busy"
	first, err := j.lease(ctx, genNonce(0), dormant, testDeliveryRequest("d1"))
	require.NoError(t, err)
	require.Equal(t, uint64(1), first.ArrivalSeq)

	for i := 1; i <= 40; i++ { // busy session drives many rotations while dormant sleeps
		_, err := j.lease(ctx, genNonce(i), busy, testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
	}
	require.GreaterOrEqual(t, j.segment, uint64(30))

	back, err := j.lease(ctx, genNonce(100), dormant, testDeliveryRequest("d2"))
	require.NoError(t, err)
	require.Equal(t, uint64(2), back.ArrivalSeq, "a dormant session continues from its last arrival, not zero")
}

// TestDeliveryRollover_ArchivedAckExactJoinAndFrontierRetained: an ack for a lease archived across
// rotations resolves and compares its original binding, and the generation store's per-session settled
// frontier retains the oldest unsettled lease across the transitions.
func TestDeliveryRollover_ArchivedAckExactJoinAndFrontierRetained(t *testing.T) {
	setRollover(t, 1)
	ctx := context.Background()
	j := openRolloverJournal(t, t.TempDir())

	const sess core.SessionID = "s"
	const n = 72
	leases := make([]deliveryLease, n)
	for i := 0; i < n; i++ {
		l, err := j.lease(ctx, genNonce(i), sess, testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
		leases[i] = l
	}
	require.GreaterOrEqual(t, j.segment, uint64(70))

	// The oldest unsettled lease (arrival 1) is retained across every transition.
	oldest, has, err := j.gen.sessionFrontier(ctx, sess)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, uint64(1), oldest, "the oldest unsettled arrival is retained across 70+ transitions")

	// Acknowledge the archived arrival-1 lease: its original binding resolves and the join is exact.
	require.NoError(t, j.acknowledge(ctx, leases[0].Delivery, leases[0].ObservationID, core.Hash{}),
		"an ack for an archived lease resolves its original binding")
	oldest, has, err = j.gen.sessionFrontier(ctx, sess)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, uint64(2), oldest, "settling arrival 1 advances the frontier to the next unsettled")

	// An ack whose identity disagrees with the archived lease is refused, never a nonce-only accept.
	err = j.acknowledge(ctx, leases[2].Delivery, core.ObservationID("sha256:"+genNonce(9)), core.Hash{})
	require.ErrorIs(t, err, core.ErrContract, "an ack whose identity disagrees with the archived lease is refused")
}
