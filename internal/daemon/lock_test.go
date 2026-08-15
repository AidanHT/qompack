package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// TestAcquireLockExclusive pins the singleton property: a second AcquireLock against the same
// project, while the first is still held (by this very process — kill(0) on our own pid always
// succeeds), returns ErrLockHeld.
func TestAcquireLockExclusive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	clk := testutil.NewFakeClock(testutil.Epoch)

	l1, err := AcquireLock(root, addr, clk)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l1.Release() })

	_, err = AcquireLock(root, addr, clk)
	require.ErrorIs(t, err, ErrLockHeld)
}

// TestStaleLockReclaimed pins the staleness protocol's reclaim path: a lock file naming a dead
// pid, no live listener, and a heartbeat well past staleAfter is reclaimed and rewritten to name
// the reclaiming process.
func TestStaleLockReclaimed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	clk := testutil.NewFakeClock(testutil.Epoch)

	writeCraftedLock(t, root, addr, 999999, clk.Now().Add(-10*time.Minute))

	l, err := AcquireLock(root, addr, clk)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Release() })

	info, ok := ReadLock(root)
	require.True(t, ok)
	require.Equal(t, os.Getpid(), info.PID, "the lock must be rewritten to record the reclaiming process")
}

// TestLiveLockNotReclaimed pins that the liveness dial wins over both a dead pid and a stale
// heartbeat: a real listener at the recorded address makes AcquireLock report ErrLockHeld
// regardless of what the rest of the lock file says.
func TestLiveLockNotReclaimed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	clk := testutil.NewFakeClock(testutil.Epoch)

	srv, err := ipc.NewServer(addr, logging.Nop(), nil, 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	go func() {
		_ = srv.Serve(ctx, func(context.Context, ipc.Request) ipc.Response {
			return ipc.Response{OK: true}
		})
	}()

	// A dead pid and a 10-minute-old heartbeat: on their own, both steps 3 and 4 would say stale.
	writeCraftedLock(t, root, addr, 999999, clk.Now().Add(-10*time.Minute))

	_, err = AcquireLock(root, addr, clk)
	require.ErrorIs(t, err, ErrLockHeld, "a live listener must win even with a dead pid and a stale heartbeat")
}

// TestAcquireLockRaceWindowIsNotStale pins I-2: AcquireLock's own window between CreateNew(lockPath)
// becoming visible and its initial heartbeat write must never let a competing acquirer conclude
// the just-created lock is stale merely because daemon.hb does not exist yet. This simulates that
// exact window by hand — a lock file with a fresh Started timestamp and deliberately no heartbeat
// file at all — and asserts a second AcquireLock still reports ErrLockHeld rather than stealing it.
func TestAcquireLockRaceWindowIsNotStale(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	clk := testutil.NewFakeClock(testutil.Epoch)

	runDir := paths.Of(root).Run
	require.NoError(t, os.MkdirAll(runDir, 0o700))
	lockPath := filepath.Join(runDir, lockFileName)
	body, err := json.Marshal(LockInfo{
		PID: os.Getpid(), Started: clk.Now().UnixMilli(), Addr: addr.Path, Version: "0.0.0-test",
	})
	require.NoError(t, err)
	require.NoError(t, paths.CreateNew(lockPath, body))
	// Deliberately no heartbeat file: this is exactly the window between CreateNew(lockPath) and
	// AcquireLock's own touchFile(hbPath) call.

	_, err = AcquireLock(root, addr, clk)
	require.ErrorIs(t, err, ErrLockHeld,
		"a lock created moments ago must never be treated as stale just because no heartbeat exists yet")
}

// TestHeartbeatUpdatesMtime pins Heartbeat's contract: it advances daemon.hb's mtime to the
// clock's current instant.
func TestHeartbeatUpdatesMtime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	clk := testutil.NewFakeClock(testutil.Epoch)

	l, err := AcquireLock(root, addr, clk)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Release() })

	hbPath := filepath.Join(paths.Of(root).Run, heartbeatFileName)
	fi1, err := os.Stat(hbPath)
	require.NoError(t, err)

	clk.Advance(60 * time.Second)
	require.NoError(t, l.Heartbeat())

	fi2, err := os.Stat(hbPath)
	require.NoError(t, err)
	require.True(t, fi2.ModTime().After(fi1.ModTime()))
	require.Equal(t, clk.Now().Unix(), fi2.ModTime().Unix())
}

// TestLockRefusesAfterReclaim pins Minor 5: once the on-disk lock file no longer names this
// process (reclaimed by a later AcquireLock — a legitimate 90s-stale takeover, or a race), neither
// Heartbeat nor Release may touch the new owner's files.
func TestLockRefusesAfterReclaim(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	clk := testutil.NewFakeClock(testutil.Epoch)

	l, err := AcquireLock(root, addr, clk)
	require.NoError(t, err)

	// Simulate a different process reclaiming the lock out from under l.
	removeLockFiles(l.path, l.hb)
	body, err := json.Marshal(LockInfo{
		PID: os.Getpid() + 1, Started: clk.Now().UnixMilli(), Addr: addr.Path, Version: "0.0.0-test",
	})
	require.NoError(t, err)
	require.NoError(t, paths.CreateNew(l.path, body))

	require.Error(t, l.Heartbeat(), "Heartbeat must refuse once the lock has been reclaimed by someone else")
	require.NoError(t, l.Release(), "Release must not error, but must not touch a lock file it no longer owns")

	info, ok := ReadLock(root)
	require.True(t, ok, "the new owner's lock file must survive the old owner's Release call")
	require.Equal(t, os.Getpid()+1, info.PID)
}

// TestReleaseIsIdempotent pins that Release may be called twice without error.
func TestReleaseIsIdempotent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	clk := testutil.NewFakeClock(testutil.Epoch)

	l, err := AcquireLock(root, addr, clk)
	require.NoError(t, err)
	require.NoError(t, l.Release())
	require.NoError(t, l.Release())

	_, ok := ReadLock(root)
	require.False(t, ok, "Release must remove the lock file")
}

// TestReadLock_UnparseableFileReportsNotOK pins step 1 of the staleness protocol's own primitive:
// a garbage lock file is reported as absent, never causing a panic or a decode error escaping.
func TestReadLock_UnparseableFileReportsNotOK(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runDir := paths.Of(root).Run
	require.NoError(t, os.MkdirAll(runDir, 0o700))
	require.NoError(t, paths.CreateNew(filepath.Join(runDir, lockFileName), []byte("not json")))

	_, ok := ReadLock(root)
	require.False(t, ok)
}

// writeCraftedLock writes a daemon.lock + daemon.hb pair by hand (bypassing AcquireLock), for
// tests that need to control exactly what pid and heartbeat age a stale/live scenario starts from.
func writeCraftedLock(t *testing.T, root string, addr ipc.Addr, pid int, hbTime time.Time) {
	t.Helper()

	runDir := paths.Of(root).Run
	require.NoError(t, os.MkdirAll(runDir, 0o700))

	lockPath := filepath.Join(runDir, lockFileName)
	hbPath := filepath.Join(runDir, heartbeatFileName)

	body, err := json.Marshal(LockInfo{PID: pid, Started: hbTime.UnixMilli(), Addr: addr.Path, Version: "0.0.0-test"})
	require.NoError(t, err)
	require.NoError(t, paths.CreateNew(lockPath, body))

	require.NoError(t, os.WriteFile(hbPath, nil, 0o600))
	require.NoError(t, os.Chtimes(hbPath, hbTime, hbTime))
}
