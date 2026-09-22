package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	_, err = m.TakeBackup(ctx, "segmented")
	require.NoError(t, err)
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
