package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

func TestDeliveryMigration_MissingLegacyAnchorsDoNotRecreateIdentity(t *testing.T) {
	for _, name := range []string{"delivery-generations", "delivery-journal.json", "delivery-segments"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			state := paths.Of(root).State
			require.NoError(t, os.MkdirAll(state, 0o700))
			evidence := filepath.Join(state, name)
			require.NoError(t, os.WriteFile(evidence, []byte("preserve migration evidence"), 0o600))
			lock, err := acquireTestDeliveryLock(root)
			require.NoError(t, err)
			t.Cleanup(func() { _ = lock.Release() })
			_, err = lock.openDeliveryJournal()
			require.Error(t, err)
			_, err = os.Lstat(filepath.Join(state, deliveryLeaseFile))
			require.True(t, os.IsNotExist(err), "refusal must not create an empty legacy history")
			data, err := os.ReadFile(evidence)
			require.NoError(t, err)
			require.Equal(t, "preserve migration evidence", string(data))
		})
	}
}

func TestDeliveryMigration_PrecommitArtifactsKeepLegacyAnchorUsable(t *testing.T) {
	root, lock, journal := newTestDeliveryJournal(t)
	lease, err := journal.lease(context.Background(), testDeliveryToken('a'), "s", testDeliveryRequest("first"))
	require.NoError(t, err)
	require.NoError(t, lock.Release())
	require.NoError(t, os.Mkdir(filepath.Join(paths.Of(root).State, "delivery-generations"), 0o700))
	lock, err = acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	journal, err = lock.openDeliveryJournal()
	require.NoError(t, err, "uncommitted artifacts cannot invalidate intact legacy anchors")
	again, err := journal.lease(context.Background(), lease.Delivery, lease.Session, lease.RequestHash)
	require.NoError(t, err)
	require.Equal(t, lease, again)
}

func TestDeliveryMigration_MissingAckAnchorsAreNotRecreated(t *testing.T) {
	root, lock, _ := newTestDeliveryJournal(t)
	require.NoError(t, lock.Release())
	state := paths.Of(root).State
	require.NoError(t, os.Mkdir(filepath.Join(state, "delivery-generations"), 0o700))
	require.NoError(t, os.Remove(filepath.Join(state, deliveryAckFile)))
	require.NoError(t, os.Remove(filepath.Join(state, deliveryAckPositionFile)))
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	_, err = lock.openDeliveryJournal()
	require.Error(t, err)
	_, err = os.Lstat(filepath.Join(state, deliveryAckFile))
	require.True(t, os.IsNotExist(err))
}
