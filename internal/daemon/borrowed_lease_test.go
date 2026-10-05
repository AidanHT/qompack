package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

func TestBorrowedLease_SurvivesDaemonUntilCallerClosesWriters(t *testing.T) {
	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	lease, err := AcquireLock(root, addr, core.SystemClock())
	require.NoError(t, err)
	defer func() { _ = lease.Release() }()
	s, err := store.Open(root, config.Defaults(), store.Deps{})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	o := NewOptions(root, runTestConfig())
	o.Store = s
	d, err := NewWithLease(o, lease)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	require.Eventually(t, func() bool { return ipc.Probe(addr, 50*time.Millisecond) }, 5*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-hangGuard(t):
		t.Fatal("daemon did not stop")
	}
	require.NoError(t, lease.Heartbeat())
	_, err = AcquireLock(root, addr, core.SystemClock())
	require.ErrorIs(t, err, ErrLockHeld, "maintenance must not enter before the caller closes its writers")
	require.NoError(t, s.Close())
	require.NoError(t, lease.Release())
	next, err := AcquireLock(root, addr, core.SystemClock())
	require.NoError(t, err)
	require.NoError(t, next.Release())
}

func TestBorrowedLease_RejectsWrongProjectAndReleasedLease(t *testing.T) {
	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	lease, err := AcquireLock(root, addr, core.SystemClock())
	require.NoError(t, err)
	defer func() { _ = lease.Release() }()
	_, err = NewWithLease(NewOptions(t.TempDir(), config.Defaults()), lease)
	require.Error(t, err)
	require.NoError(t, lease.Release())
	_, err = NewWithLease(NewOptions(root, config.Defaults()), lease)
	require.Error(t, err)
}

func TestBorrowedLease_ReportsResidualOnlyAfterOwnerRelease(t *testing.T) {
	root, lease, journal := newFormatTwoJournal(t)
	_, err := journal.lease(context.Background(), leaseToken(1), "borrowed-residual", testDeliveryRequest("residual"))
	require.NoError(t, err)
	journal.seal.path = unwritableSealPath(t, root)
	log := newRecordingLogger()
	d := newStoppableTestDaemon(t, root, log)
	d.borrowedLease = true
	d.setLock(lease)
	require.NoError(t, d.Stop(context.Background()))
	require.NoError(t, lease.Heartbeat(), "Stop must leave ownership with the composition root")
	require.Empty(t, sealResidualWarnings(log), "no residual exists before the owner closes the journal")
	require.NoError(t, lease.ReleaseWithReport(log, "writer owner"))
	require.ErrorIs(t, lease.SealDowngradeResidual(), core.ErrDegraded)
	reports := sealResidualWarnings(log)
	require.Len(t, reports, 1)
	require.Contains(t, reports[0].KV, "writer owner")
}
