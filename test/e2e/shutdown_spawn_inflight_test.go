package e2e

import (
	"encoding/json"
	"fmt"
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
	"github.com/qompack/qompack/internal/testutil"
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
	late := time.AfterFunc(e2eLazySpawnSettleBound+time.Second, func() { started <- cmd.Start() })
	// reap waits for this test's own child, killing it if it outlives e2eDaemonDownBound.
	reap := func() error {
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		select {
		case err := <-waited:
			return err
		case <-time.After(e2eDaemonDownBound):
			_ = cmd.Process.Kill() // this test's own child, never another process
			return fmt.Errorf("still running %s later, so it was killed: %w", e2eDaemonDownBound, <-waited)
		}
	}
	// Whatever the outcome, this test's own child is gone before its TempDir is removed: stopping
	// the timer means it never started; otherwise reap it if the body has not.
	var startErr error
	startSeen, reaped := false, false
	t.Cleanup(func() {
		if late.Stop() {
			return
		}
		if !startSeen {
			startErr = <-started
		}
		if startErr != nil || reaped {
			return
		}
		_ = reap()
	})

	e2eShutdownIfReachable(t, dir)
	returned := time.Now()

	startErr, startSeen = <-started, true
	require.NoError(t, startErr, "fixture: the late daemon must have been started")
	// One definition of "gone", the helper's own, asked of the process this test launched rather
	// than of the pid the lock file names: e2eProcessAlive says it is no longer running. On Windows
	// that means its process object is signaled, which the kernel does only after it has closed the
	// process's handles (testutil.ProcessAlive); on Linux, that it is a zombie or reaped. It is asked
	// once the helper has returned and before the child is reaped, so the pid cannot name any other
	// process: Windows keeps a pid unused while cmd still holds the process handle, and on Linux an
	// exited child stays a zombie until it is reaped.
	//
	// This row used to compare the helper's return with the instant a goroutine's cmd.Wait came
	// back, and under co-load it failed with the helper 2-3 ms ahead (w3-e2ereds, on w2-hookout's
	// aec178a). Two things were in that gap. The helper's probe was GetExitCodeProcess, and Windows
	// sets the exit code before it closes the process's handles, up to 151 ms before the process
	// object is signaled (w4-e2eflakes runs/diag-a-exitcode-vs-signaled-windows.txt): the helper
	// could return while the daemon still held its files, and that was a real defect, fixed in the
	// probe. And a goroutine's time.Now after cmd.Wait is not when the process exited but when that
	// goroutine next ran, so even a helper that waits for the signal can lose that race. The probe
	// asked at the return has no such lag, and it asks exactly what the helper promises.
	aliveAtReturn := e2eProcessAlive(cmd.Process.Pid)
	waitErr := reap()
	reaped = true
	require.False(t, aliveAtReturn,
		"e2eShutdownIfReachable returned while the late daemon (pid %d) was still running (it was reaped "+
			"%s after the return): it handed a tree a live daemon was still writing to to the caller's "+
			"RemoveAll", cmd.Process.Pid, time.Since(returned))
	require.NoError(t, waitErr, "fixture: the late daemon must have run and exited cleanly")
	_, held := testutil.DaemonHoldingLock(dir)
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
// writes its body, and testutil.DaemonHoldingLock counts that as held with no pid) left it with pid 0,
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

	attempts := func() int { return e2eShutdownAttempts(dir) }
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

// TestE2EShutdownIfReachable_WaitsOutALockHolderItNeverIdentified pins the one lock history the
// helper cannot answer by asking a process: a lock it saw held, with no pid it could read, that was
// gone by its next check.
//
// That is what the helper sees when a daemon's whole life after CreateNew falls between two of its
// checks. paths.CreateNew creates daemon.lock and only then writes the body, so one check can read
// the empty file. The daemon then writes its body, starts listening, takes the helper's own
// admin.shutdown, and releases the lock. Stop runs on a goroutine of its own and can release the
// lock before the reply is even written (handleAdminShutdown), so the next check can find no lock
// at all. No check ever read a pid, so none can be asked whether it has exited, and the daemon is
// still unwinding. Before this row the helper counted that as settled, because the set of pids it
// had to wait for was empty.
//
// The test stages that history with no process at all, so the outcome is decided by causality: an
// empty daemon.lock the helper reads once its shutdown loop runs, then removed without a body ever
// landing. The helper must not return at the next check. It cannot learn which process held the
// lock, so the most it can do is wait out its own bound, e2eDaemonDownBound, which covers a
// daemon's whole Stop cleanup (daemon.StopCleanupBound) with margin. The return is asserted against
// that bound from below, which no host load can make fail.
func TestE2EShutdownIfReachable_WaitsOutALockHolderItNeverIdentified(t *testing.T) {
	dir := e2eFaultProject(t)

	lockPath := daemon.LockPath(dir)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(lockPath)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(lockPath), nil, 0o600),
		"fixture: the empty daemon.lock a starting daemon's CreateNew leaves before its body lands")

	called := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		e2eShutdownIfReachable(t, dir)
	}()
	// The helper logs through t, so it must have returned before this test does.
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(e2eDaemonDownBound + e2eDaemonDownBound):
		}
	})

	attempts := func() int { return e2eShutdownAttempts(dir) }
	// awaitAttempts waits until the helper has made at least n shutdown attempts, and fails if it
	// returns first. Nothing listens, so each attempt lands in the helper's own client spool.
	awaitAttempts := func(n int, why string) {
		t.Helper()
		ticker := time.NewTicker(e2eDaemonDownTick)
		defer ticker.Stop()
		deadline := time.Now().Add(e2eDaemonDownBound)
		for attempts() < n {
			select {
			case <-done:
				require.FailNowf(t, "e2eShutdownIfReachable returned too early",
					"it returned %s, after %d shutdown attempts, although no check ever read the pid "+
						"of the process that held the lock", why, attempts())
			case <-ticker.C:
			}
			require.False(t, time.Now().After(deadline), "the helper made no shutdown attempt %s", why)
		}
	}

	// The helper's shutdown loop is running. Every attempt is preceded by a check, so the helper has
	// seen the empty lock by now.
	awaitAttempts(1, "before its shutdown loop started")

	// The lock goes away with no body ever having been readable.
	require.NoError(t, os.Remove(paths.Long(lockPath)))
	awaitAttempts(attempts()+2, "once a lock whose holder it never identified was released")

	select {
	case <-done:
	case <-time.After(e2eDaemonDownBound + e2eDaemonDownBound):
		t.Fatalf("e2eShutdownIfReachable did not return within twice its own bound (%s)", e2eDaemonDownBound)
	}
	require.GreaterOrEqual(t, time.Since(called), e2eDaemonDownBound,
		"with no pid to ask, the helper may only stop waiting at its own bound")
}

// e2eShutdownAttempts counts the admin.shutdown requests a shutdown helper has made against dir while
// nothing listens: the lines in dir's client spools, where each attempt lands in the helper's own
// writer's file. That file's name carries an id its writer drew (internal/ipc newSpoolFor), so the
// count reads every client spool rather than reconstruct the name.
func e2eShutdownAttempts(dir string) int {
	files, _ := ipc.SpoolFiles(paths.Of(dir).Spool)
	n := 0
	for _, f := range files {
		if ipc.SpoolFileKindOf(filepath.Base(f)) != ipc.SpoolFileClient {
			continue
		}
		b, _ := paths.ReadFileShared(f)
		n += strings.Count(string(b), `"`+string(ipc.OpAdminShutdown)+`"`)
	}
	return n
}
