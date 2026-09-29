package daemon

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
)

// serveAt runs a trivial live listener at addr for the rest of the test: the daemon of ANOTHER
// project, still running.
func serveAt(t *testing.T, addr ipc.Addr) {
	t.Helper()
	srv, err := ipc.NewServer(addr, logging.Nop(), nil, 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	go func() {
		_ = srv.Serve(ctx, func(context.Context, ipc.Request) ipc.Response { return ipc.Response{OK: true} })
	}()
}

// TestForeignLockIsJudgedByThisStoresHeartbeat is F-UAT03-4. A store copied by hand from another
// path carries that path's run/daemon.lock, which records the ORIGINAL project's IPC address. The
// staleness protocol's first question dialled that recorded address, and while the original
// project's daemon was alive the dial always answered — so the copy's lock was never stale, no
// daemon ever started for the copy, and each spawned daemon exited 0 without a word.
//
// A lock recorded for another address cannot be vouched for by that address's listener (it serves
// another store), nor by its pid (the daemon of another path). Only this store's own heartbeat can
// say a daemon is serving it, and the existing 90-second rule applies to it unchanged.
func TestForeignLockIsJudgedByThisStoresHeartbeat(t *testing.T) {
	t.Parallel()

	original := t.TempDir()
	originalAddr, err := ipc.Resolve(original)
	require.NoError(t, err)
	serveAt(t, originalAddr)

	t.Run("a stale heartbeat: the copy's daemon takes the lock", func(t *testing.T) {
		t.Parallel()
		copyRoot := t.TempDir()
		addr, err := ipc.Resolve(copyRoot)
		require.NoError(t, err)
		require.NotEqual(t, originalAddr.Path, addr.Path, "fixture: two paths, two addresses")
		clk := newFakeClock(epoch)
		// The live pid of this very process: step 3 would call it alive on POSIX.
		writeCraftedLock(t, copyRoot, originalAddr, os.Getpid(), clk.Now().Add(-10*time.Minute))

		l, err := AcquireLock(copyRoot, addr, clk)
		require.NoError(t, err, "a lock written for another path, whose heartbeat here is 10 minutes old, is stale")
		t.Cleanup(func() { _ = l.Release() })
		info, ok := ReadLock(copyRoot)
		require.True(t, ok)
		require.Equal(t, addr.Path, info.Addr, "the reclaimed lock records this project's own address")
	})

	t.Run("a fresh heartbeat: still held", func(t *testing.T) {
		t.Parallel()
		copyRoot := t.TempDir()
		addr, err := ipc.Resolve(copyRoot)
		require.NoError(t, err)
		clk := newFakeClock(epoch)
		writeCraftedLock(t, copyRoot, originalAddr, os.Getpid(), clk.Now().Add(-10*time.Second))

		_, err = AcquireLock(copyRoot, addr, clk)
		require.ErrorIs(t, err, ErrLockHeld,
			"inside the heartbeat window the lock is held: a daemon for this store under another spelling heartbeats it")
	})
}

// TestDescribeLockHolderNamesAForeignLock pins the reason a losing daemon logs (F-UAT03-4: the
// spawned daemon exited 0 with no log line at all).
func TestDescribeLockHolderNamesAForeignLock(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	other, err := ipc.Resolve(t.TempDir())
	require.NoError(t, err)
	clk := newFakeClock(epoch)
	writeCraftedLock(t, root, other, 4242, clk.Now().Add(-10*time.Second))

	h := DescribeLockHolder(root, addr, clk)
	require.True(t, h.Present)
	require.Equal(t, 4242, h.PID)
	require.True(t, h.Foreign, "the lock records another project's address")
	require.Equal(t, other.Path, h.Addr)
	require.Equal(t, 10*time.Second, h.HeartbeatAge)
}
