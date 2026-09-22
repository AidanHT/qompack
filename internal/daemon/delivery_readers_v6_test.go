package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// Exercise store readers using the daemon's actual writer bytes. Literal store
// fixtures alone cannot catch drift in the duplicated, cycle-free wire readers.
func TestDeliveryReaders_V6_GCRetainsActiveAndArchivedLeases(t *testing.T) {
	for _, missing := range []string{"", deliveryPositionFile, deliveryAckFile} {
		name := missing
		if name == "" {
			name = "complete"
		}
		t.Run(name, func(t *testing.T) {
			setRollover(t, 1)
			ctx := context.Background()
			root := t.TempDir()
			s, err := store.Open(root, config.Defaults(), store.Deps{})
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			var roots []core.Hash
			for i := 0; i < 4; i++ {
				put, err := s.PutBytes(ctx, []byte(fmt.Sprintf("unique retained payload %d\n", i)), store.PutOptions{})
				require.NoError(t, err)
				roots = append(roots, put.Root.Hash)
			}
			j := openRolloverJournal(t, root)
			var leases []deliveryLease
			for i := 0; i < 3; i++ {
				// A root in the request field is a structural retention reference;
				// the same real writer encodes it and binds the observation identity.
				l, err := j.lease(ctx, genNonce(i), "retention", roots[i])
				require.NoError(t, err)
				leases = append(leases, l)
			}
			require.Equal(t, uint64(2), j.segment)
			// ACK in segment 2 settles segment 0, but segment 1 remains unsettled.
			require.NoError(t, j.acknowledge(ctx, leases[0].Delivery, leases[0].ObservationID, core.Hash{}))
			require.NoError(t, j.owner.Release())
			if missing != "" {
				require.NoError(t, os.Remove(filepath.Join(segmentDir(paths.Of(root).State, 1), missing)))
			}
			rep, err := s.GC(ctx, store.GCPolicy{RetainDays: -1, RetainSessions: -1})
			require.NoError(t, err)
			if missing != "" {
				require.True(t, rep.RetentionRootsError, "a required archived file is missing")
				require.Zero(t, rep.DeletedObjects)
				for _, h := range roots {
					_, err := s.GetRoot(ctx, h)
					require.NoError(t, err, "refusal must preserve all content")
				}
				return
			}
			require.False(t, rep.RetentionRootsError)
			for _, i := range []int{1, 2} {
				_, err := s.GetRoot(ctx, roots[i])
				require.NoError(t, err, "archived and active unsettled leases both retain their roots")
			}
			for _, i := range []int{0, 3} {
				_, err := s.GetRoot(ctx, roots[i])
				require.ErrorIs(t, err, core.ErrNotFound, "settled and unreferenced controls are collectible")
			}
		})
	}
}

func TestDeliveryReaders_V6_BackupRestoresHistoryAndAcceptsLaterWrites(t *testing.T) {
	setRollover(t, 1)
	ctx := context.Background()
	root := t.TempDir()
	cfg := config.Defaults()
	s, err := store.Open(root, cfg, store.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	payload := []byte("content preserved alongside segmented delivery history\n")
	put, err := s.PutBytes(ctx, payload, store.PutOptions{})
	require.NoError(t, err)
	j := openRolloverJournal(t, root)
	var first deliveryLease
	for i := 0; i < 3; i++ {
		l, err := j.lease(ctx, genNonce(i), "backup-session", put.Root.Hash)
		require.NoError(t, err)
		if i == 0 {
			first = l
		}
	}
	require.NoError(t, j.acknowledge(ctx, first.Delivery, first.ObservationID, put.Root.Hash))
	require.NoError(t, j.owner.Release())
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	m, err := store.NewMaintenance(s, root, store.MaintenanceOptions{Cfg: cfg, WriterLeaseHeld: lock.Heartbeat})
	require.NoError(t, err)
	man, err := m.TakeBackup(ctx, "segmented")
	require.NoError(t, err)
	_, err = m.VerifyBackup("segmented")
	require.NoError(t, err, "the segmented backup verifies")
	// The backup holds every piece of segmented delivery state: the authority and its log, each
	// segment's four journal/seal files (segment 0's seals frozen), and the generation store's
	// manifest, head, packs and root pointers.
	names := map[string]bool{}
	packs, rootPointers := 0, 0
	for _, f := range man.Files {
		names[f.Name] = true
		switch {
		case strings.HasPrefix(f.Name, "state/delivery-generations/pages/packs/") && strings.HasSuffix(f.Name, radixPackSuffix):
			packs++
		case strings.HasPrefix(f.Name, "state/delivery-generations/pages/") && strings.HasSuffix(f.Name, radixRootSuffix):
			rootPointers++
		}
	}
	for _, want := range []string{
		"state/" + deliverySegmentHeadFile, "state/" + deliverySegmentLogFile,
		"state/delivery-generations/" + genLogFile, "state/delivery-generations/" + genHeadFile,
		"state/" + deliveryLeaseFile, "state/" + deliveryPositionFile, "state/" + deliveryAckFile, "state/" + deliveryAckPositionFile,
		"state/delivery-segments/00000000000000000002/" + deliveryLeaseFile,
		"state/delivery-segments/00000000000000000002/" + deliveryPositionFile,
		"state/delivery-segments/00000000000000000002/" + deliveryAckFile,
		"state/delivery-segments/00000000000000000002/" + deliveryAckPositionFile,
	} {
		require.True(t, names[want], "the backup covers %s", want)
	}
	require.Positive(t, packs, "the backup covers the generation packs")
	require.Positive(t, rootPointers, "the backup covers the generation root pointers")
	restoreRoot := filepath.Join(t.TempDir(), "restored")
	proof, err := m.Restore(ctx, "segmented", restoreRoot)
	require.NoError(t, err)
	require.True(t, proof.SameBuildOnly)
	require.NoError(t, lock.Release())
	_, err = checkSeal(t, restoreRoot)
	require.NoError(t, err, "offline integrity must inspect all copied segments")
	restored := openRolloverJournal(t, restoreRoot)
	got, err := restored.lease(ctx, first.Delivery, first.Session, first.RequestHash)
	require.NoError(t, err)
	require.Equal(t, first, got)
	require.True(t, restored.acknowledged(first.Delivery))
	next, err := restored.lease(ctx, genNonce(3), first.Session, put.Root.Hash)
	require.NoError(t, err)
	require.Equal(t, uint64(4), next.ArrivalSeq)
	rs, err := store.Open(restoreRoot, cfg, store.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = rs.Close() })
	r, err := rs.Open(ctx, put.Root.Hash)
	require.NoError(t, err)
	gotBytes, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, payload, gotBytes)
}

// TestDeliveryReaders_V6_GCDuringLiveRotationsKeepsEveryArchivedUnsettledRoot (V6 close-out C1.10, gate
// 3): store GC runs WHILE the journal rotates, pass after pass, and after the rotations stop. A root an
// unsettled lease references is never collected, whichever segment the lease was archived into; a root
// only a settled lease referenced becomes collectible. Every pass either completes or halts (a rotation
// moved the authority under it) — a halted pass deletes nothing. The negative control is
// plans/sdd/V6-closeout/rollover/gc-negative-control.sh: with GC's segmented harvest reduced to segment 0,
// this test fails.
func TestDeliveryReaders_V6_GCDuringLiveRotationsKeepsEveryArchivedUnsettledRoot(t *testing.T) {
	setRollover(t, 1) // every lease after the first in a segment rotates
	ctx := context.Background()
	root := t.TempDir()
	s, err := store.Open(root, config.Defaults(), store.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	const n = 14
	roots := make([]core.Hash, n)
	j := openRolloverJournal(t, root)
	policy := store.GCPolicy{RetainDays: -1, RetainSessions: -1}
	leases := make([]deliveryLease, n)
	settled := map[int]bool{}
	completed, halted := 0, 0
	for i := 0; i < n; i++ {
		// Delivery i's content is written and leased first (an earlier pass must never see it while it
		// is still unreferenced); then a filler delivery (referencing no content) rotates the journal
		// WHILE a GC pass runs, so every judged lease was committed before the pass began.
		put, err := s.PutBytes(ctx, []byte(fmt.Sprintf("payload referenced only by delivery %d\n", i)), store.PutOptions{})
		require.NoError(t, err)
		roots[i] = put.Root.Hash
		l, err := j.lease(ctx, genNonce(i), "gc-live", roots[i])
		require.NoError(t, err)
		leases[i] = l
		done := make(chan error, 1)
		go func(i int) {
			_, err := j.lease(ctx, genNonce(1000+i), "gc-filler", testDeliveryRequest(genNonce(1000+i)))
			done <- err
		}(i)
		rep, err := s.GC(ctx, policy)
		require.NoError(t, err)
		require.NoError(t, <-done)
		if rep.RetentionRootsError {
			halted++
			require.Zero(t, rep.DeletedObjects, "a halted pass deletes nothing")
		} else {
			completed++
		}
		for k := 0; k <= i; k++ {
			if settled[k] {
				continue
			}
			_, err := s.GetRoot(ctx, roots[k])
			require.NoError(t, err, "pass %d: delivery %d is unsettled (active segment %d); its root must be retained", i, k, j.segment)
		}
		// Settle every third delivery once it is archived, so settled roots become collectible across
		// segments; its acknowledgement lands in a later segment than its lease.
		if i%3 == 0 {
			require.NoError(t, j.acknowledge(ctx, leases[i].Delivery, leases[i].ObservationID, core.Hash{}))
			settled[i] = true
		}
	}
	require.GreaterOrEqual(t, j.segment, uint64(2*n-1), "every lease after the first rotated")

	// With the rotations stopped, a pass completes: every unsettled root stays, every settled one goes.
	rep, err := s.GC(ctx, policy)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError, "a quiet pass completes")
	for k := 0; k < n; k++ {
		_, err := s.GetRoot(ctx, roots[k])
		if settled[k] {
			require.ErrorIs(t, err, core.ErrNotFound, "delivery %d was settled; its root is collectible", k)
			continue
		}
		require.NoError(t, err, "delivery %d is unsettled; its root is retained", k)
	}
	t.Logf("GC passes during rotation: %d completed, %d halted by a moving authority", completed, halted)
}
