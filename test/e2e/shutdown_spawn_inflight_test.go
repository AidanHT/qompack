package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight is the regression test for the unclassified
// TestHooksExitZeroUnderFaults red — "TempDir RemoveAll cleanup: … The directory is not empty", on a
// different row each run, only on a loaded machine.
//
// Root cause (plans/sdd/V6-closeout/w2-hookout/runs/diag-faultrows-coload-windows.log): a hook's
// lazySpawn starts a detached daemon and exits without waiting for it. On a loaded host that
// daemon's process can take seconds just to reach its first statement, so e2eShutdownIfReachable
// found nothing reachable and no daemon.lock after its settle window, returned, and handed the
// directory to t.TempDir's RemoveAll — and the daemon then took its lock, recreating
// .qompack/run inside a tree being deleted. Measured under co-load: a lock taken 4.2 s after the
// helper returned, with run/spawn.lock present the whole time.
//
// run/spawn.lock is the product's own "a spawn is in flight" marker: lazySpawn writes it, with its
// own timestamp, before it launches the daemon; the daemon removes it once it listens; and any
// client treats it as live until spawnLockStaleAfter. So while a FRESH one exists, "nothing
// reachable, no lock" does not mean "nothing is coming".
//
// This test stages exactly that: the spawn.lock a lazySpawn leaves, and a real daemon whose process
// only starts after the helper's settle window. The helper must not return while that daemon can
// still arrive: by the time it returns, the late daemon must have come up, been shut down, and
// exited.
func TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight(t *testing.T) {
	bin := Build(t)
	dir := e2eFaultProject(t)
	home := t.TempDir()

	run := paths.Of(dir).Run
	require.NoError(t, os.MkdirAll(paths.Long(run), 0o700))
	require.NoError(t, paths.CreateNew(filepath.Join(run, e2eSpawnLockName),
		[]byte(strconv.FormatInt(time.Now().UnixMilli(), 10))),
		"fixture: the spawn.lock a hook's lazySpawn writes before it launches the daemon")

	// The late daemon: an ordinary `qompack daemon` whose process starts only after the helper's
	// own settle window has passed — what a detached spawn looks like on a loaded host. Its idle exit
	// is fast, as every fault-matrix row's is, so that if the helper does return early the daemon
	// still goes away on its own and the assertion below reports WHEN, instead of this test hanging.
	cmd := exec.Command(bin, "daemon", "--project", dir)
	cmd.Env = append(os.Environ(), "QOMPACK_PROJECT_ROOT="+dir, "HOME="+home, "USERPROFILE="+home)
	for k, v := range e2eIdleExitFastEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	started := make(chan error, 1)
	exited := make(chan time.Time, 1)
	late := time.AfterFunc(e2eLazySpawnSettleBound+time.Second, func() {
		err := cmd.Start()
		started <- err
		if err == nil {
			_ = cmd.Wait()
			exited <- time.Now()
		}
	})
	// Whatever the outcome, this test's own child is gone before its TempDir is removed: stopping
	// the timer means it never started; otherwise wait for the exit the body has not yet consumed.
	var startErr error
	startSeen, exitSeen := false, false
	t.Cleanup(func() {
		if late.Stop() {
			return
		}
		if !startSeen {
			startErr = <-started
		}
		if startErr != nil || exitSeen {
			return
		}
		select {
		case <-exited:
		case <-time.After(e2eDaemonDownBound):
			_ = cmd.Process.Kill() // this test's own child, never another process
			<-exited
		}
	})

	e2eShutdownIfReachable(t, dir)
	returned := time.Now()

	startErr, startSeen = <-started, true
	require.NoError(t, startErr, "fixture: the late daemon must have been started")
	select {
	case exitedAt := <-exited:
		exitSeen = true
		require.False(t, exitedAt.After(returned),
			"e2eShutdownIfReachable returned %s before the late daemon exited: it handed a tree a "+
				"live daemon was still writing to to the caller's RemoveAll", exitedAt.Sub(returned))
	case <-time.After(e2eDaemonDownBound):
		t.Fatalf("e2eShutdownIfReachable returned while the late daemon was still running %s later",
			e2eDaemonDownBound)
	}
	_, held := e2eDaemonHoldingLock(dir)
	require.False(t, held, "no live daemon may hold the project's lock once the helper returns")
}

// TestE2EShutdownIfReachable_WaitsForEveryLockHolderToExit pins what "gone" means to
// e2eShutdownIfReachable: no live process holds the project's lock, AND every out-of-process daemon
// the helper saw holding it during this call has exited. The lock is released as Stop's last act,
// and the process goes on writing under .qompack while it unwinds, so a helper that returns on the
// lock alone hands its caller's t.TempDir RemoveAll a tree that is still being written to.
//
// The helper used to learn that pid from ONE read of daemon.lock, taken as the shutdown handshake
// began. A lock that did not parse at that instant (paths.CreateNew creates the file and only then
// writes its body, and e2eDaemonHoldingLock counts that as held with no pid) left it with pid 0,
// for which "has it exited?" is always yes, so from then on the lock's disappearance alone ended
// the wait. That is the exact state e2eAwaitSpawnInFlight returns into: it stops at the first
// sighting of a held lock, which can be the empty file a starting daemon has just created.
//
// The test stages that sequence with a process it controls, so both outcomes are decided by
// causality, not by timing:
//   - an empty daemon.lock, a lock caught mid-create, is all the helper can read when it starts;
//   - once its shutdown loop is running, the lock gains a body naming a live stand-in process;
//   - after the helper has read that body, the lock disappears while the stand-in keeps running.
//
// The helper's shutdown attempts land in its own client spool (nothing is listening), and each
// one precedes that iteration's lock check, so the spool's line count shows which lock state each
// check saw. A helper that returns while the stand-in runs fails deterministically, because the
// stand-in exits only when this test closes its stdin. The stand-in is `qompack mcp` over a
// separate project: it serves stdio until stdin closes and touches nothing in the project under
// test.
func TestE2EShutdownIfReachable_WaitsForEveryLockHolderToExit(t *testing.T) {
	bin := Build(t)
	dir := e2eFaultProject(t)
	home := t.TempDir()

	// The stand-in is not reaped until the end, so its pid names it and nothing else throughout:
	// Windows keeps a pid unused while cmd holds the process handle, and on Linux an exited child
	// stays a zombie, which e2eProcessAlive reports as exited, until it is reaped.
	holder := exec.Command(bin, "mcp")
	holder.Dir = filepath.Dir(bin)
	holder.Env = append(os.Environ(), "QOMPACK_PROJECT_ROOT="+t.TempDir(), "HOME="+home, "USERPROFILE="+home)
	holderIn, err := holder.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, holder.Start(), "fixture: the stand-in lock holder must start")
	holderPID := holder.Process.Pid

	lockPath := daemon.LockPath(dir)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(lockPath)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(lockPath), nil, 0o600),
		"fixture: the empty daemon.lock a starting daemon's CreateNew leaves before its body lands")

	done := make(chan struct{})
	go func() {
		defer close(done)
		e2eShutdownIfReachable(t, dir)
	}()
	// Whatever the outcome: the stand-in is told to exit, the helper (which logs through t) has
	// returned before this test does, and the stand-in is reaped. Every process here is this
	// test's own child.
	t.Cleanup(func() {
		_ = holderIn.Close()
		select {
		case <-done:
		case <-time.After(e2eDaemonDownBound + e2eDaemonDownBound):
		}
		reaped := make(chan struct{})
		go func() {
			_ = holder.Wait()
			close(reaped)
		}()
		select {
		case <-reaped:
		case <-time.After(mcpE2EExitBound):
			_ = holder.Process.Kill()
			<-reaped
		}
	})

	spool := filepath.Join(paths.Of(dir).Spool, "client-"+strconv.Itoa(os.Getpid())+".ndjson")
	attempts := func() int {
		b, _ := paths.ReadFileShared(spool)
		return strings.Count(string(b), `"`+string(ipc.OpAdminShutdown)+`"`)
	}
	// awaitAttempts waits until the helper has made at least n shutdown attempts, and fails if it
	// returns first. Its bound is e2eDaemonDownBound, after which the helper's own loop gives up.
	awaitAttempts := func(n int, why string) {
		t.Helper()
		ticker := time.NewTicker(e2eDaemonDownTick)
		defer ticker.Stop()
		deadline := time.Now().Add(e2eDaemonDownBound)
		for attempts() < n {
			select {
			case <-done:
				require.True(t, e2eProcessAlive(holderPID),
					"fixture: the stand-in lock holder (pid %d) exited on its own", holderPID)
				require.FailNowf(t, "e2eShutdownIfReachable returned too early",
					"it returned %s, while the stand-in lock holder (pid %d) was still running",
					why, holderPID)
			case <-ticker.C:
			}
			require.False(t, time.Now().After(deadline), "the helper made no shutdown attempt %s", why)
		}
	}

	// The helper's shutdown loop is running, and it began with a lock it could not parse.
	awaitAttempts(1, "before its shutdown loop started")

	body, err := json.Marshal(daemon.LockInfo{PID: holderPID, Started: time.Now().UnixMilli()})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(lockPath), body, 0o600))
	// An attempt that starts after the body landed is followed by a check that reads it.
	awaitAttempts(attempts()+2, "before it read the lock's holder")

	// The holder releases the lock and keeps running, as a daemon does between Stop's last act and
	// its exit. The helper must go on waiting through at least one check that finds no lock.
	require.NoError(t, os.Remove(paths.Long(lockPath)))
	awaitAttempts(attempts()+2, "once the lock was released")

	// The holder exits, and only now may the helper return.
	require.NoError(t, holderIn.Close())
	select {
	case <-done:
	case <-time.After(e2eDaemonDownBound):
		t.Fatalf("e2eShutdownIfReachable did not return within %s of the last lock holder exiting",
			e2eDaemonDownBound)
	}
	require.False(t, e2eProcessAlive(holderPID),
		"e2eShutdownIfReachable returned while the stand-in lock holder (pid %d) was still running", holderPID)
}
