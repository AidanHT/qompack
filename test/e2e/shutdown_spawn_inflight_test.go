package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
