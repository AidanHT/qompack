package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
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
	other, err := ipc.Resolve(t.TempDir())
	require.NoError(t, err)
	clk := newFakeClock(epoch)
	writeCraftedLock(t, root, other, 4242, clk.Now().Add(-10*time.Second))

	h := DescribeLockHolder(root, clk)
	require.True(t, h.Present)
	require.Equal(t, 4242, h.PID)
	require.True(t, h.Foreign, "the lock records another project's address")
	require.Equal(t, other.Path, h.Addr)
	require.Equal(t, 10*time.Second, h.HeartbeatAge)
}

// writeLockInfo writes info as root's daemon.lock, with a heartbeat at hbTime.
func writeLockInfo(t *testing.T, root string, info LockInfo, hbTime time.Time) {
	t.Helper()
	runDir := paths.Of(root).Run
	require.NoError(t, os.MkdirAll(runDir, 0o700))
	body, err := json.Marshal(info)
	require.NoError(t, err)
	require.NoError(t, paths.CreateNew(filepath.Join(runDir, lockFileName), body))
	hb := filepath.Join(runDir, heartbeatFileName)
	require.NoError(t, os.WriteFile(hb, nil, 0o600))
	require.NoError(t, os.Chtimes(hb, hbTime, hbTime))
}

// TestALockNamesItsRootAndIsJudgedByIt: AcquireLock records the project root the lock was taken
// for (LockInfo.Root), and LockIsForeign reads it before anything else. A lock whose root is another
// store is foreign whatever its address says; one whose root is this store keeps the whole
// protocol whatever its address says.
func TestALockNamesItsRootAndIsJudgedByIt(t *testing.T) {
	t.Parallel()

	t.Run("AcquireLock records the root", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		addr, err := ipc.Resolve(root)
		require.NoError(t, err)
		l, err := AcquireLock(root, addr, newFakeClock(epoch))
		require.NoError(t, err)
		t.Cleanup(func() { _ = l.Release() })
		info, ok := ReadLock(root)
		require.True(t, ok)
		abs, err := filepath.Abs(root)
		require.NoError(t, err)
		require.Equal(t, abs, info.Root)
		require.False(t, LockIsForeign(info, root))
		require.False(t, LockIsForeign(info, root+string(filepath.Separator)+"."), "another spelling of one path")
	})

	t.Run("another store's root: foreign, whatever answers at its address", func(t *testing.T) {
		t.Parallel()
		original := t.TempDir()
		require.NoError(t, os.MkdirAll(paths.Of(original).Dot, 0o700))
		copyRoot := t.TempDir()
		addr, err := ipc.Resolve(copyRoot)
		require.NoError(t, err)
		recorded := privateAddr(t)
		serveAt(t, recorded) // the original's daemon, still running
		clk := newFakeClock(epoch)
		writeLockInfo(t, copyRoot, LockInfo{
			PID: os.Getpid(), Started: clk.Now().Add(-10 * time.Minute).UnixMilli(), Addr: recorded.Path,
			Version: "0.0.0-test", Root: original,
		}, clk.Now().Add(-10*time.Minute))

		l, err := AcquireLock(copyRoot, addr, clk)
		require.NoError(t, err, "the copy's own heartbeat is 10 minutes old")
		t.Cleanup(func() { _ = l.Release() })
	})

	t.Run("a root that no longer exists: foreign", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		gone := filepath.Join(t.TempDir(), "moved-away")
		require.True(t, LockIsForeign(LockInfo{Root: gone}, root))
	})

	t.Run("this store's root at an address this process would not resolve: the full protocol", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		addr, err := ipc.Resolve(root)
		require.NoError(t, err)
		recorded := privateAddr(t)
		serveAt(t, recorded)
		clk := newFakeClock(epoch)
		writeLockInfo(t, root, LockInfo{
			PID: 999999, Started: clk.Now().Add(-10 * time.Minute).UnixMilli(), Addr: recorded.Path,
			Version: "0.0.0-test", Root: root,
		}, clk.Now().Add(-10*time.Minute))

		_, err = AcquireLock(root, addr, clk)
		require.ErrorIs(t, err, ErrLockHeld, "step 2: the recorded address answers")
	})
}

// privateAddrSeq keeps two privateAddr pipes of parallel subtests apart when the clock does not.
var privateAddrSeq atomic.Uint64

// privateAddr is an endpoint no project hash names, private to this test: what QOMPACK_IPC_ADDR
// gives a daemon, or what a caller of ipc.NewServer picks.
func privateAddr(t *testing.T) ipc.Addr {
	t.Helper()
	if runtime.GOOS == "windows" {
		return ipc.Addr{Kind: ipc.NamedPipe, Path: `\\.\pipe\qompack-lockid-test-` +
			strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatUint(privateAddrSeq.Add(1), 36)}
	}
	// os.MkdirTemp rather than t.TempDir: a test-named directory can outgrow sun_path's 100 bytes.
	dir, err := os.MkdirTemp("", "qlk")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return ipc.Addr{Kind: ipc.UnixSocket, Path: path.Join(filepath.ToSlash(dir), "o.sock")}
}

// TestALockThisProjectWroteElsewhereKeepsTheFullProtocol is the other side of F-UAT03-4. A lock is
// foreign when it was written for ANOTHER project, not whenever its recorded address differs from
// the one this process resolves: on POSIX that address also depends on XDG_RUNTIME_DIR, TMPDIR and
// the uid, and QOMPACK_IPC_ADDR overrides it everywhere (ipc.Resolve), so a LIVE daemon of this very
// project started under another environment records an address this process would not resolve.
// Such a lock keeps the whole staleness protocol — the dial of its recorded address (step 2) and the
// POSIX pid check (step 3) — and is not reclaimed on a stale heartbeat alone, which after a suspend
// or a stall would put two writers on one store.
func TestALockThisProjectWroteElsewhereKeepsTheFullProtocol(t *testing.T) {
	t.Parallel()

	t.Run("an address no project hash names, answering", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		own, err := ipc.Resolve(root)
		require.NoError(t, err)
		recorded := privateAddr(t)
		serveAt(t, recorded)
		clk := newFakeClock(epoch)
		writeCraftedLock(t, root, recorded, 999999, clk.Now().Add(-10*time.Minute))

		_, err = AcquireLock(root, own, clk)
		require.ErrorIs(t, err, ErrLockHeld,
			"the recorded address answers; nothing says the lock belongs to another project")
	})

	t.Run("this project's endpoint under another socket directory, its pid alive", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		own, err := ipc.Resolve(root)
		require.NoError(t, err)
		// Where a daemon of this project listens when XDG_RUNTIME_DIR was set for it and is not here.
		recorded := ipc.Addr{Kind: own.Kind, Path: "/run/user/1000/qompack/" + ipc.ProjectHash12(root) + ".sock"}
		clk := newFakeClock(epoch)
		writeCraftedLock(t, root, recorded, os.Getpid(), clk.Now().Add(-10*time.Minute))
		info, ok := ReadLock(root)
		require.True(t, ok)
		require.False(t, LockIsForeign(info, root), "the endpoint is named for this project's own hash")

		_, err = AcquireLock(root, own, clk)
		if _, known := pidAlive(os.Getpid()); known {
			require.ErrorIs(t, err, ErrLockHeld, "step 3: the recorded pid is alive")
		} else {
			// Windows has no pid probe (lock_windows.go): step 4, the heartbeat, decides, as it
			// always has there.
			require.NoError(t, err)
		}
	})
}
