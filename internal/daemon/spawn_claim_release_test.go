package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// The D27 rows (V6 close-out w7-spawnclaim). A hook whose connect deadline expired spawns a daemon;
// that daemon loses daemon.lock to the one already running and exits before it listens, so it never
// removes the run/spawn.lock its spawner wrote. While the winner runs, that claim is harmless: it
// only holds further lazy spawns off until it goes stale. But if the winner stops inside the claim's
// freshness window, a SessionEnd flush then finds no daemon and a fresh claim, stands aside (D17),
// and starts nothing. Whoever holds daemon.lock therefore gives run/spawn.lock back when it lets go.

// strayClaimThenStop reproduces the sequence: daemon A runs; a hook's lazy spawn claims spawn.lock
// and its daemon B loses the lock to A and exits; then A stops. It returns the project root with no
// daemon running.
func strayClaimThenStop(t *testing.T) (root string, clk core.Clock) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root = t.TempDir()
	t.Setenv("QOMPACK_IPC_ADDR", uniqueTestAddr(t))
	cfg := runTestConfig()
	clk = core.SystemClock()

	dA, err := New(Options{ProjectRoot: root, Cfg: cfg, Log: logging.Nop(), Clock: clk})
	require.NoError(t, err)
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	errChA := make(chan error, 1)
	go func() { errChA <- dA.Run(ctxA) }()
	// An answered admin.ping, not the lock: requests wait at serveOp's gate until A's startup is
	// done, and its startup is what deletes spawn.lock after listening, so a claim taken any earlier
	// could be A's to delete rather than the stray one this fixture stages.
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	c := ipc.NewClientWithOptions(addr, nil, logging.Nop(), nil, ipc.ClientOptions{
		ConnectDeadline: spawnLockTestBound, AckDeadline: spawnLockTestBound,
	})
	defer func() { _ = c.Close() }()
	awaitWhileRunning(t, errChA, stopCleanupBound, func() bool {
		resp, sendErr := c.Send(context.Background(), ipc.Request{
			Op: ipc.OpAdminPing, TS: core.NowMilli(clk), Reply: true,
		}, spawnLockTestBound)
		return sendErr == nil && resp.OK
	}, "daemon A never answered admin.ping")

	_, st := ipc.ClaimSpawn(root, clk)
	require.Equal(t, ipc.SpawnClaimed, st, "fixture: the hook's lazy spawn claims spawn.lock")
	dB, err := New(Options{ProjectRoot: root, Cfg: cfg, Log: logging.Nop(), Clock: clk})
	require.NoError(t, err)
	ctxB, cancelB := context.WithTimeout(context.Background(), spawnLockTestBound)
	defer cancelB()
	require.NoError(t, dB.Run(ctxB), "fixture: the spawned daemon loses the lock and exits quietly")
	require.FileExists(t, filepath.Join(paths.Of(root).Run, runSpawnLockFileName),
		"fixture: the losing daemon never listened, so its spawner's claim is still there")

	cancelA()
	select {
	case <-errChA:
	case <-hangGuard(t):
		t.Fatal("daemon A did not shut down")
	}
	_, held := ReadLock(root)
	require.False(t, held, "fixture: no daemon holds the lock once A has stopped")
	return root, clk
}

// awaitWhileRunning polls cond every ensureRunningPollInterval until it holds, and fails the row at
// once, with what Run returned, if the daemon's Run ends first: a Run that has returned (a listen
// that failed, a lock it lost) can never make cond hold. The require.Eventually it replaced never
// read Run's channel, so such a Run was reported only as a condition that never held, bound later:
// the 0.3.2 cover run's AFlush row failed at 7.3 s, while its daemon had exited 26 ms after the
// spawn with "listen: Access is denied" (ipc.TestServerCloseFreesTheEndpoint has the cause). cond
// runs on this goroutine, so a cond that blocks can overrun bound by one call of its own (admin.ping's
// Send is itself bounded by spawnLockTestBound); neither row asserts a latency, so the bound is a
// hang guard, not a claim.
func awaitWhileRunning(t *testing.T, runErr <-chan error, bound time.Duration, cond func() bool, msg string) {
	t.Helper()
	tick := time.NewTicker(ensureRunningPollInterval)
	defer tick.Stop()
	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	for !cond() {
		// The bound is checked before the next tick is waited for, not raced against it: a select
		// with both ready picks either, and a cond as slow as admin.ping's Send could then run again
		// past the bound.
		select {
		case err := <-runErr:
			t.Fatalf("%s: the daemon's Run returned first, with %v", msg, err)
		case <-deadline.C:
			t.Fatalf("%s: not within %s", msg, bound)
		default:
		}
		select {
		case err := <-runErr:
			t.Fatalf("%s: the daemon's Run returned first, with %v", msg, err)
		case <-deadline.C:
			t.Fatalf("%s: not within %s", msg, bound)
		case <-tick.C:
		}
	}
}

// TestSpawnClaim_ALostRaceLeavesNoClaimOnceTheRunningDaemonStops: once the daemon that won stops,
// the claim the losing spawn left is gone, so the next spawner claims at once.
func TestSpawnClaim_ALostRaceLeavesNoClaimOnceTheRunningDaemonStops(t *testing.T) {
	root, clk := strayClaimThenStop(t)

	lock, st := ipc.ClaimSpawn(root, clk)
	defer lock.Release()
	require.Equal(t, ipc.SpawnClaimed, st,
		"no daemon runs, so the next lazy spawn must claim; SpawnInFlight means the claim a "+
			"lock-losing daemon left held it off for the claim's whole freshness window")
}

// TestSpawnClaim_AFlushAfterTheRunningDaemonExitsStartsExactlyOneDaemon: the SessionEnd flush that
// arrives after that stop spawns once, and the daemon it spawns comes up and holds the lock.
func TestSpawnClaim_AFlushAfterTheRunningDaemonExitsStartsExactlyOneDaemon(t *testing.T) {
	root, clk := strayClaimThenStop(t)
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)

	var spawns atomic.Int64
	ctxC, cancelC := context.WithCancel(context.Background())
	defer cancelC()
	errChC := make(chan error, 1)
	spawn := func(r, _ string) error {
		spawns.Add(1)
		dC, err := New(Options{ProjectRoot: r, Cfg: runTestConfig(), Log: logging.Nop(), Clock: clk})
		if err != nil {
			return err
		}
		go func() { errChC <- dC.Run(ctxC) }()
		return nil
	}

	sp, err := ipc.NewSpool(filepath.Join(t.TempDir(), "spool"))
	require.NoError(t, err)
	c := ipc.NewClientWithOptions(addr, sp, logging.Nop(), nil, ipc.ClientOptions{
		ProjectRoot: root, Self: "self", Clock: clk,
		ConnectDeadline: spawnLockMissBound, AckDeadline: spawnLockMissBound,
		Spawn: spawn,
	})
	t.Cleanup(func() { _ = c.Close() })
	_, err = c.Send(context.Background(), ipc.Request{Op: ipc.OpFlush, Session: "s", TS: 1}, spawnLockMissBound)
	require.NoError(t, err)

	require.EqualValues(t, 1, spawns.Load(), "the flush's lazy spawn must start the daemon, once")
	awaitWhileRunning(t, errChC, spawnLockTestBound, func() bool {
		info, ok := ReadLock(root)
		return ok && info.PID == os.Getpid() && ipc.Probe(addr, spawnClaimDialTimeout)
	}, "the daemon the flush spawned must come up and hold the lock")

	cancelC()
	select {
	case err := <-errChC:
		require.NoError(t, err)
	case <-hangGuard(t):
		t.Fatal("the flush's daemon did not shut down")
	}
}

// TestLockRelease_GivesBackTheSpawnClaim: every holder of daemon.lock (a daemon's Run, the daemon
// command's writer lease, the operator tools) releases through Lock.Release, so that is where the
// claim is given back. A Release of a lock someone else has since reclaimed touches neither file.
func TestLockRelease_GivesBackTheSpawnClaim(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	clk := newFakeClock(epoch)

	l, err := AcquireLock(root, addr, clk)
	require.NoError(t, err)
	claim := writeSpawnLock(t, root, clk.Now())
	require.NoError(t, l.Release())
	require.NoFileExists(t, claim, "releasing daemon.lock must give run/spawn.lock back")

	// A second acquisition, reclaimed out from under it by another process (as
	// TestLockRefusesAfterReclaim stages it), while a spawn that process's daemon announced is fresh.
	l2, err := AcquireLock(root, addr, clk)
	require.NoError(t, err)
	removeLockFiles(l2.path, l2.hb)
	body, err := json.Marshal(LockInfo{
		PID: os.Getpid() + 1, Started: clk.Now().UnixMilli(), Addr: addr.Path, Version: "0.0.0-test",
	})
	require.NoError(t, err)
	require.NoError(t, paths.CreateNew(l2.path, body))
	claim = writeSpawnLock(t, root, clk.Now())
	require.NoError(t, l2.Release())
	require.FileExists(t, claim, "a Release of a lock it no longer holds must not touch the claim")
}
