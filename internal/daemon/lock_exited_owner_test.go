package daemon

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
)

// exitedPID starts a short-lived child — this test binary, running no tests — waits for it to exit
// and returns its pid: a process that is certainly gone.
func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	require.NoError(t, cmd.Run())
	return cmd.ProcessState.Pid()
}

// TestLock_AnExitedOwnerIsReplacedAtOnce is the Phase 4 live lane's F-UAT05-4 / F-C49-4: after a
// daemon was terminated, backup create and verify refused "daemon lock already held" and MCP
// retrieval answered "temporarily offline" for about 90 seconds. On Windows the staleness protocol's
// process probe had no opinion at all, so a lock whose owner was gone stayed held until its
// heartbeat aged past staleAfter. A lock naming a process that has exited is replaced at once,
// whatever its heartbeat says, on every platform.
func TestLock_AnExitedOwnerIsReplacedAtOnce(t *testing.T) {
	pid := exitedPID(t)
	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	clk := newFakeClock(epoch)
	writeCraftedLock(t, root, addr, pid, clk.Now()) // a heartbeat written this very instant

	lock, err := AcquireLock(root, addr, clk)
	require.NoError(t, err, "a lock whose owner (pid %d) has exited must not hold the project", pid)
	require.NoError(t, lock.Release())
}

// TestLock_ALiveOwnerKeepsItsFreshLock: the probe that proves an owner dead must never prove a live
// one dead. This process holds a lock with a fresh heartbeat and no listener; a second acquirer
// must still be refused.
func TestLock_ALiveOwnerKeepsItsFreshLock(t *testing.T) {
	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	clk := newFakeClock(epoch)
	writeCraftedLock(t, root, addr, os.Getpid(), clk.Now())

	_, err = AcquireLock(root, addr, clk)
	require.ErrorIs(t, err, ErrLockHeld)
}
