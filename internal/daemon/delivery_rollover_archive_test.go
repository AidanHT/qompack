package daemon

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// SP20-D4 rotation-time archival (V6 close-out C1.10). The generation store used to be written on the
// hot path: every lease batch and every acknowledgement batch committed a generation before it was
// admitted, which put a multi-file durable commit inside ingest.Accept (budget B-B). The contract these
// tests pin instead: the active segment's journals and in-memory window are the record of the ACTIVE
// window, and the generation store receives that window as a whole when the segment rotates. Only an
// acknowledgement or terminal disposition that settles an ALREADY-ARCHIVED lease is mirrored at once,
// because nothing else could ever settle it in the store.

// countJournalLines returns how many lines a journal file holds.
func countJournalLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(paths.Long(path))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		n++
	}
	require.NoError(t, sc.Err())
	return n
}

// TestDeliveryRollover_HotPathLeavesTheGenerationStoreUntouched: below the rollover threshold, leases
// and acknowledgements commit exactly what the legacy journal always committed — no generation, no pack.
func TestDeliveryRollover_HotPathLeavesTheGenerationStoreUntouched(t *testing.T) {
	setRollover(t, 1000)
	ctx := context.Background()
	j := openRolloverJournal(t, t.TempDir())
	require.NotNil(t, j.gen)
	for i := 0; i < 20; i++ {
		l, err := j.lease(ctx, genNonce(i), "hot", testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
		require.NoError(t, j.acknowledge(ctx, l.Delivery, l.ObservationID, core.Hash{}))
	}
	require.Equal(t, uint64(0), j.segment)
	require.Zero(t, j.gen.generationCount(), "a lease or acknowledgement below the threshold commits no generation")
	packs, roots, other := radixStoreFiles(t, filepath.Join(j.stateDir, deliveryGenerationsDirName, genPagesDir))
	require.Zero(t, packs+roots+other, "the hot path writes no generation page")
}

// TestDeliveryRollover_RotationArchivesTheWholeOutgoingWindow: the rotation hands the generation store
// every lease, acknowledgement and terminal disposition of the outgoing window, and the per-session
// frontier it records is exact.
func TestDeliveryRollover_RotationArchivesTheWholeOutgoingWindow(t *testing.T) {
	setRollover(t, 4)
	ctx := context.Background()
	j := openRolloverJournal(t, t.TempDir())
	var window []deliveryLease
	for i := 0; i < 4; i++ {
		l, err := j.lease(ctx, genNonce(i), "window", testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
		window = append(window, l)
	}
	require.NoError(t, j.acknowledge(ctx, window[0].Delivery, window[0].ObservationID, core.Hash{}))
	require.NoError(t, j.retireDenied(ctx, window[1]))
	require.NoError(t, j.acknowledge(ctx, window[3].Delivery, window[3].ObservationID, core.Hash{}))
	require.Zero(t, j.gen.generationCount(), "nothing is archived before the window rotates")

	next, err := j.lease(ctx, genNonce(4), "window", testDeliveryRequest(genNonce(4)))
	require.NoError(t, err)
	require.Equal(t, uint64(1), j.segment)
	require.Equal(t, uint64(5), next.ArrivalSeq)

	for _, l := range window {
		got, found, err := j.gen.resolveLease(ctx, l.Delivery)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, l, got)
	}
	for _, i := range []int{0, 3} {
		_, found, err := j.gen.resolveAck(ctx, window[i].Delivery)
		require.NoError(t, err)
		require.True(t, found, "acknowledgement %d is archived with its window", i)
	}
	denied, err := j.terminalDenied(window[1])
	require.NoError(t, err)
	require.True(t, denied, "the terminal disposition is archived with its window")
	f, pending, err := j.gen.sessionFrontier(ctx, "window")
	require.NoError(t, err)
	require.True(t, pending)
	require.Equal(t, uint64(3), f, "arrivals 1 (acked) and 2 (denied) are settled, 3 is the oldest pending")
}

// TestDeliveryRollover_ArchivedSettlementIsMirroredAtOnce: an acknowledgement or denial of an archived
// lease settles the store's frontier immediately, so a later arrival of the same session is never held
// behind a predecessor that is in fact settled.
func TestDeliveryRollover_ArchivedSettlementIsMirroredAtOnce(t *testing.T) {
	setRollover(t, 2)
	ctx := context.Background()
	j := openRolloverJournal(t, t.TempDir())
	var leases []deliveryLease
	for i := 0; i < 3; i++ {
		l, err := j.lease(ctx, genNonce(i), "settle", testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
		leases = append(leases, l)
	}
	require.Equal(t, uint64(1), j.segment, "arrivals 1 and 2 are archived")
	require.False(t, j.predecessorsAcknowledged("settle", 3))

	require.NoError(t, j.acknowledge(ctx, leases[0].Delivery, leases[0].ObservationID, core.Hash{}))
	require.False(t, j.predecessorsAcknowledged("settle", 3), "arrival 2 is still pending")
	require.NoError(t, j.retireDenied(ctx, leases[1]))
	require.True(t, j.predecessorsAcknowledged("settle", 3), "both archived predecessors are settled")
	_, found, err := j.gen.resolveAck(ctx, leases[0].Delivery)
	require.NoError(t, err)
	require.True(t, found, "the archived acknowledgement is in the store at once")
}

// TestDeliveryRollover_AcknowledgementJournalRotatesAtItsOwnThreshold: a segment's acknowledgement
// journal can fill before its lease journal does (it also settles leases archived before the segment
// opened). Reaching the threshold rotates rather than refusing the acknowledgement.
func TestDeliveryRollover_AcknowledgementJournalRotatesAtItsOwnThreshold(t *testing.T) {
	setRollover(t, 3)
	ctx := context.Background()
	j := openRolloverJournal(t, t.TempDir())
	var leases []deliveryLease
	for i := 0; i < 4; i++ { // three fill segment 0; the fourth opens segment 1
		l, err := j.lease(ctx, genNonce(i), "acks", testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
		leases = append(leases, l)
	}
	require.Equal(t, uint64(1), j.segment)
	for _, l := range leases { // four acknowledgements against a threshold of three
		require.NoError(t, j.acknowledge(ctx, l.Delivery, l.ObservationID, core.Hash{}))
		require.LessOrEqual(t, len(j.acks), 3, "an acknowledgement journal never passes the threshold")
	}
	require.Equal(t, uint64(2), j.segment, "the fourth acknowledgement rotated the segment")
	for _, l := range leases {
		require.True(t, j.acknowledged(l.Delivery))
	}
}

// TestDeliveryRollover_RedeliveredAcknowledgementOfArchivedDeliveryAppendsNothing: an archived delivery
// that already carries an acknowledgement answers a redelivered one from the store, idempotently,
// instead of appending a second acknowledgement line into the active segment.
func TestDeliveryRollover_RedeliveredAcknowledgementOfArchivedDeliveryAppendsNothing(t *testing.T) {
	setRollover(t, 1)
	ctx := context.Background()
	j := openRolloverJournal(t, t.TempDir())
	first, err := j.lease(ctx, genNonce(0), "dup", testDeliveryRequest(genNonce(0)))
	require.NoError(t, err)
	require.NoError(t, j.acknowledge(ctx, first.Delivery, first.ObservationID, core.Hash{}))
	_, err = j.lease(ctx, genNonce(1), "dup", testDeliveryRequest(genNonce(1)))
	require.NoError(t, err)
	require.Equal(t, uint64(1), j.segment, "the acknowledged delivery is archived")

	require.NoError(t, j.acknowledge(ctx, first.Delivery, first.ObservationID, core.Hash{}))
	require.Zero(t, countJournalLines(t, j.ackPath), "a redelivered acknowledgement appends nothing")
	wrong := core.ObservationID("sha256:" + genNonce(9))
	require.ErrorIs(t, j.acknowledge(ctx, first.Delivery, wrong, core.Hash{}), core.ErrContract,
		"an acknowledgement naming a different identity is still refused")
}

// TestDeliveryRollover_InterruptedRotationRollsForwardAtOpen: an owner that archived its window and
// stopped before committing the transition leaves the store holding a window that is still active. The
// next open finishes that rotation before assigning anything, so the window's later settlements can
// never be stranded behind a stale archived frontier.
func TestDeliveryRollover_InterruptedRotationRollsForwardAtOpen(t *testing.T) {
	setRollover(t, 100)
	ctx := context.Background()
	root := t.TempDir()
	j := openRolloverJournal(t, root)
	var leases []deliveryLease
	for i := 0; i < 3; i++ {
		l, err := j.lease(ctx, genNonce(i), "interrupted", testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
		leases = append(leases, l)
	}
	// The first half of a rotation: the window is archived, the transition is never committed.
	require.NoError(t, j.reconcileGenerations(ctx))
	require.NoError(t, j.owner.Release())

	reopened := openRolloverJournal(t, root)
	require.Equal(t, uint64(1), reopened.segment, "the interrupted rotation is finished at open")
	require.NoError(t, reopened.acknowledge(ctx, leases[0].Delivery, leases[0].ObservationID, core.Hash{}))
	require.NoError(t, reopened.acknowledge(ctx, leases[1].Delivery, leases[1].ObservationID, core.Hash{}))
	require.True(t, reopened.predecessorsAcknowledged("interrupted", 3),
		"settling the archived predecessors releases the next arrival")
	next, err := reopened.lease(ctx, genNonce(3), "interrupted", testDeliveryRequest(genNonce(3)))
	require.NoError(t, err)
	require.Equal(t, uint64(4), next.ArrivalSeq)
}

// TestDeliveryRollover_ConcurrentCallersNeverSeeARotationAsABudgetRefusal: a caller that triggered a
// rotation retries on the new segment — but other callers may fill that segment first. A second
// rotation signal is then progress, not a fault: the caller rotates again. Only a rotation signal on
// the same segment it already rotated would mean something is wrong. (Found by the real-binary drill:
// with a low threshold and the drain leasing beside the hooks, a delivery was refused with ErrBudget.)
//
// The retry decides which segment, and so which window, a raced delivery is assigned in, so the
// identities are pinned too (review finding 7): every session's arrivals are exactly 1..n, every
// observation identity is distinct, every nonce re-leases to the identical lease once its segment has
// been archived, and acknowledgements of leases archived in different segments are all admitted.
func TestDeliveryRollover_ConcurrentCallersNeverSeeARotationAsABudgetRefusal(t *testing.T) {
	setRollover(t, 1) // every lease after the first in a segment rotates
	ctx := context.Background()
	j := openRolloverJournal(t, t.TempDir())
	const callers, each = 8, 6
	errs := make(chan error, callers*each)
	got := make([][]deliveryLease, callers)
	var wg sync.WaitGroup
	for c := 0; c < callers; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			session := core.SessionID(fmt.Sprintf("concurrent-%d", c))
			for i := 0; i < each; i++ {
				nonce := genNonce(c*1000 + i)
				l, err := j.lease(ctx, nonce, session, testDeliveryRequest(nonce))
				errs <- err
				if err == nil {
					got[c] = append(got[c], l)
				}
			}
		}(c)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err, "a rotation raced by other callers is retried, never refused as a budget")
	}
	require.GreaterOrEqual(t, j.segment, uint64(callers*each-1))

	observations := map[core.ObservationID]string{}
	for c := 0; c < callers; c++ {
		session := core.SessionID(fmt.Sprintf("concurrent-%d", c))
		require.Len(t, got[c], each)
		for i, l := range got[c] {
			require.Equal(t, genNonce(c*1000+i), l.Delivery)
			require.Equal(t, session, l.Session)
			require.Equal(t, uint64(i+1), l.ArrivalSeq, "session %s: arrivals are dense and in call order", session)
			require.True(t, validDeliveryLease(l))
			prev, dup := observations[l.ObservationID]
			require.False(t, dup, "observation %s assigned to %s and %s", l.ObservationID, prev, l.Delivery)
			observations[l.ObservationID] = l.Delivery
		}
	}
	// Every nonce re-leases to its original identity (all but the last are archived now), and an
	// acknowledgement of a lease from each caller — archived in different segments — is admitted.
	for c := 0; c < callers; c++ {
		for _, l := range got[c] {
			again, err := j.lease(ctx, l.Delivery, l.Session, l.RequestHash)
			require.NoError(t, err)
			require.Equal(t, l, again, "a raced nonce resolves to the lease it was assigned")
		}
		first := got[c][0]
		require.NoError(t, j.acknowledge(ctx, first.Delivery, first.ObservationID, core.Hash{}))
		require.True(t, j.acknowledged(first.Delivery))
	}
}
