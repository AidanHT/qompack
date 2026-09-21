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

func TestDeliveryTerminal_RestartRetainsDenialWithoutCaptureACK(t *testing.T) {
	root, lock, j := newTestDeliveryJournal(t)
	ctx := context.Background()
	lease, err := j.lease(ctx, testDeliveryToken('a'), "s", testDeliveryRequest("permitted before denial"))
	require.NoError(t, err)
	require.NoError(t, j.retireDenied(ctx, lease))
	require.False(t, j.acknowledged(lease.Delivery), "retired replay must never fabricate a capture ACK")
	name, err := terminalFileName(lease.ObservationID)
	require.NoError(t, err)
	path := filepath.Join(paths.Of(root).State, deliveryTerminalDir, name)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(before), "permitted before denial", "disposition contains no host payload")
	require.NoError(t, j.retireDenied(ctx, lease))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NoError(t, lock.Release())
	lock, err = acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	j, err = lock.openDeliveryJournal()
	require.NoError(t, err)
	require.Equal(t, terminalFor(lease), j.terminal[lease.Delivery])
	require.False(t, j.acknowledged(lease.Delivery))
}

func TestDeliveryTerminal_RejectsUnknownOrChangedLease(t *testing.T) {
	_, _, j := newTestDeliveryJournal(t)
	lease, err := j.lease(context.Background(), testDeliveryToken('a'), "s", testDeliveryRequest("body"))
	require.NoError(t, err)
	for _, mutation := range []func(*deliveryLease){
		func(l *deliveryLease) { l.Delivery = testDeliveryToken('b') },
		func(l *deliveryLease) { l.RequestHash = testDeliveryRequest("changed") },
		func(l *deliveryLease) { l.ArrivalSeq++ },
		func(l *deliveryLease) { l.ObservationID = "../outside" },
	} {
		changed := lease
		mutation(&changed)
		require.ErrorIs(t, j.retireDenied(context.Background(), changed), core.ErrDegraded)
	}
	require.Empty(t, j.terminal)
}

func TestDeliveryTerminal_MalformedEvidenceIsPreservedAndUnavailable(t *testing.T) {
	root, lock, j := newTestDeliveryJournal(t)
	lease, err := j.lease(context.Background(), testDeliveryToken('a'), "s", testDeliveryRequest("body"))
	require.NoError(t, err)
	require.NoError(t, j.retireDenied(context.Background(), lease))
	require.NoError(t, lock.Release())
	name, err := terminalFileName(lease.ObservationID)
	require.NoError(t, err)
	path := filepath.Join(paths.Of(root).State, deliveryTerminalDir, name)
	corrupt := []byte(`{"v":999}`)
	require.NoError(t, os.WriteFile(path, corrupt, 0o600))
	lock, err = acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	_, err = lock.openDeliveryJournal()
	require.ErrorIs(t, err, core.ErrDegraded)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, corrupt, data)
}

func TestDeliveryTerminal_StagingArtifactIsNotACompletion(t *testing.T) {
	root, lock, j := newTestDeliveryJournal(t)
	lease, err := j.lease(context.Background(), testDeliveryToken('a'), "s", testDeliveryRequest("body"))
	require.NoError(t, err)
	require.NoError(t, lock.Release())
	dir := filepath.Join(paths.Of(root).State, deliveryTerminalDir)
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "incomplete.tmp"), []byte("uncommitted"), 0o600))
	lock, err = acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	j, err = lock.openDeliveryJournal()
	require.NoError(t, err)
	require.Empty(t, j.terminal)
	require.False(t, j.acknowledged(lease.Delivery))
}
