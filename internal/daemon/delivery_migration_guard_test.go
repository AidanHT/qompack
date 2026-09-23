package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
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

// legacyFixture runs build the way a build that predates segmented rollover would have: with the
// rollover mechanism off, so the store it leaves is a plain legacy store (no segment authority, no
// generation store). The default is restored before the test goes on, so everything after the fixture
// runs as production does.
func legacyFixture(t *testing.T, build func()) {
	t.Helper()
	prev := enableDeliveryGenerations
	enableDeliveryGenerations = false
	defer func() { enableDeliveryGenerations = prev }()
	build()
}

// withRolloverDisabled turns segmented rollover off for the rest of t. It is for the tests that pin a
// single journal file's own admission bounds (65,536 entries, 64 MiB): with rollover on, reaching a
// bound rotates rather than refusing, so the refusal those tests pin — and the batch-equals-sequential
// answers around it — is the behaviour of a journal that cannot rotate. Rotation at the bound has its
// own tests (delivery_rollover_*_test.go, TestCarriedDefect_SP20D4_*).
func withRolloverDisabled(t *testing.T) {
	t.Helper()
	prev := enableDeliveryGenerations
	enableDeliveryGenerations = false
	t.Cleanup(func() { enableDeliveryGenerations = prev })
}

// Replaces the historical PrecommitArtifactsKeepLegacyAnchorUsable assertion.
// Surviving migration artifacts without authority can also mean lost authority
// after new writes; intact legacy anchors alone cannot authorize continuation.
func TestDeliveryMigration_OrphanArtifactsPreserveLegacyAnchorsWithoutContinuing(t *testing.T) {
	var (
		root  string
		lease deliveryLease
	)
	legacyFixture(t, func() {
		var (
			lock    *Lock
			journal *deliveryJournal
			err     error
		)
		root, lock, journal = newTestDeliveryJournal(t)
		lease, err = journal.lease(context.Background(), testDeliveryToken('a'), "s", testDeliveryRequest("first"))
		require.NoError(t, err)
		require.NoError(t, lock.Release())
	})
	var err error
	state := paths.Of(root).State
	before := make(map[string][]byte)
	for _, name := range []string{deliveryLeaseFile, deliveryPositionFile, deliveryAckFile, deliveryAckPositionFile} {
		before[name], err = os.ReadFile(filepath.Join(state, name))
		require.NoError(t, err)
	}
	require.Contains(t, string(before[deliveryLeaseFile]), string(lease.ObservationID))
	require.NoError(t, os.Mkdir(filepath.Join(state, "delivery-generations"), 0o700))
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	_, err = lock.openDeliveryJournal()
	require.ErrorIs(t, err, core.ErrDegraded, "orphan migration evidence must not authorize legacy continuation")
	for name, want := range before {
		got, readErr := os.ReadFile(filepath.Join(state, name))
		require.NoError(t, readErr)
		require.Equal(t, want, got, "refusal must preserve %s and its original identities", name)
	}
	require.DirExists(t, filepath.Join(state, "delivery-generations"))
}

func TestDeliveryMigration_MissingAckAnchorsAreNotRecreated(t *testing.T) {
	var root string
	legacyFixture(t, func() {
		var lock *Lock
		root, lock, _ = newTestDeliveryJournal(t)
		require.NoError(t, lock.Release())
	})
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
