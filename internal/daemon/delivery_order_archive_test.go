package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// The ordering gate and the drain's lease probe read the generation store for archived leases once the
// journal has rotated. They used to do it holding Lock.mu and st — the owner mutex every Accept's
// journal accessor takes and the state mutex every lease and acknowledgement admission takes — so each
// dispatch's store read (positioned pack reads for a page outside the branch cache) stalled admission
// behind it (V6 close-out rollover review, finding 5: +35-45 us a call on Windows, +8-10 us on Linux
// with five archived windows). These tests pin that the store read runs with neither held, and that
// the answers keep the gate's contract: fail closed, and never true before the predecessor settled.

// lockProbe replaces the generation store's key hash, which every radix lookup computes first, with one
// that records whether Lock.mu and st were free at that moment.
type lockProbe struct {
	lookups, ownerHeld, stateHeld int
}

func (p *lockProbe) install(t *testing.T, j *deliveryJournal) {
	t.Helper()
	rx := j.gen.radix
	prev := rx.hashKey
	rx.hashKey = func(key []byte) radixHash {
		p.lookups++
		if j.owner.mu.TryLock() {
			j.owner.mu.Unlock()
		} else {
			p.ownerHeld++
		}
		if j.st.TryLock() {
			j.st.Unlock()
		} else {
			p.stateHeld++
		}
		return prev(key)
	}
	t.Cleanup(func() { rx.hashKey = prev })
}

// TestDeliveryOrder_ArchiveReadsHoldNeitherJournalLock: predecessorsAcknowledged and leaseHeld answer
// exactly as before for an archived predecessor and an archived nonce, and their store reads run with
// Lock.mu and st both free.
func TestDeliveryOrder_ArchiveReadsHoldNeitherJournalLock(t *testing.T) {
	setRollover(t, 1)
	ctx := context.Background()
	j := openRolloverJournal(t, t.TempDir())
	first, err := j.lease(ctx, genNonce(0), "gate", testDeliveryRequest(genNonce(0)))
	require.NoError(t, err)
	_, err = j.lease(ctx, genNonce(1), "gate", testDeliveryRequest(genNonce(1)))
	require.NoError(t, err)
	require.Equal(t, uint64(1), j.segment, "arrival 1 is archived, arrival 2 is in the window")

	probe := &lockProbe{}
	probe.install(t, j)
	require.False(t, j.predecessorsAcknowledged("gate", 2), "the archived arrival 1 is not settled")
	got, held, err := j.leaseHeld(first.Delivery)
	require.NoError(t, err)
	require.True(t, held, "the archived nonce is found in the store")
	require.Equal(t, first, got)
	_, held, err = j.leaseHeld(genNonce(99))
	require.NoError(t, err)
	require.False(t, held, "a nonce nobody leased is a proven absence")

	require.NoError(t, j.acknowledge(ctx, first.Delivery, first.ObservationID, core.Hash{}))
	require.True(t, j.predecessorsAcknowledged("gate", 2), "settling the archived predecessor releases arrival 2")

	require.Positive(t, probe.lookups, "the answers came from the store")
	require.Zero(t, probe.ownerHeld, "no store read ran under Lock.mu")
	require.Zero(t, probe.stateHeld, "no store read ran under st")
}

// TestDeliveryOrder_ArchiveReadsFailClosed: a journal that is closing, closed, faulted or rotating
// answers false (and leaseHeld an error), with or without the store read.
func TestDeliveryOrder_ArchiveReadsFailClosed(t *testing.T) {
	setRollover(t, 1)
	ctx := context.Background()
	j := openRolloverJournal(t, t.TempDir())
	first, err := j.lease(ctx, genNonce(0), "closed", testDeliveryRequest(genNonce(0)))
	require.NoError(t, err)
	_, err = j.lease(ctx, genNonce(1), "closed", testDeliveryRequest(genNonce(1)))
	require.NoError(t, err)
	require.NoError(t, j.acknowledge(ctx, first.Delivery, first.ObservationID, core.Hash{}))
	require.True(t, j.predecessorsAcknowledged("closed", 2))

	j.st.Lock()
	j.rotating = true
	j.st.Unlock()
	require.False(t, j.predecessorsAcknowledged("closed", 2), "a rotating journal defers")
	_, _, err = j.leaseHeld(first.Delivery)
	require.Error(t, err)
	j.st.Lock()
	j.rotating = false
	j.st.Unlock()

	// A fault that strikes while the store is being read: the answer read before it is not given.
	probe := j.gen.radix.hashKey
	j.gen.radix.hashKey = func(key []byte) radixHash {
		_ = j.poison(deliveryJournalError())
		return probe(key)
	}
	t.Cleanup(func() { j.gen.radix.hashKey = probe })
	require.False(t, j.predecessorsAcknowledged("closed", 2), "a fault during the store read fails closed")
	_, _, err = j.leaseHeld(first.Delivery)
	require.Error(t, err, "and so does a lease probe")
}

// TestDeliveryOrder_ArchiveReadsNeverRunAheadOfASettlement races the gate against a writer that leases
// and acknowledges a session in arrival order across many rotations. Whenever the gate says arrival k
// may publish, the acknowledgement of every earlier arrival must already have begun. Run it under -race
// too: the store read now shares the journal with admission and rotation without either lock.
func TestDeliveryOrder_ArchiveReadsNeverRunAheadOfASettlement(t *testing.T) {
	setRollover(t, 2)
	ctx := context.Background()
	j := openRolloverJournal(t, t.TempDir())
	const n = 24
	var leased, ackStarted atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			l, err := j.lease(ctx, genNonce(i), "race", testDeliveryRequest(genNonce(i)))
			if err != nil {
				t.Errorf("lease %d: %v", i, err)
				return
			}
			leased.Store(int64(i + 1))
			ackStarted.Store(int64(i + 1))
			if err := j.acknowledge(ctx, l.Delivery, l.ObservationID, core.Hash{}); err != nil {
				t.Errorf("acknowledge %d: %v", i, err)
				return
			}
		}
	}()
	for checks := 0; checks < 4*n || leased.Load() < n; checks++ {
		k := uint64(leased.Load()) + 1 // the next arrival, and every one before it
		for a := uint64(2); a <= k; a++ {
			if j.predecessorsAcknowledged("race", a) {
				require.GreaterOrEqual(t, ackStarted.Load(), int64(a-1),
					"arrival %d was released before arrival %d's acknowledgement began", a, a-1)
			}
		}
	}
	wg.Wait()
	require.GreaterOrEqual(t, j.segment, uint64(n/2-1), "the race crossed rotations")
	require.True(t, j.predecessorsAcknowledged("race", n+1), "every predecessor is settled at the end")
}
