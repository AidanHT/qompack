//go:build linux

package daemon

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
)

func TestLock_V6_ExitedUnreapedOwnerCanBeReplaced(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Wait() })
	require.Eventually(t, func() bool {
		alive, known := pidAlive(cmd.Process.Pid)
		return known && !alive
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, syscall.Kill(cmd.Process.Pid, 0), "the exited child has not been reaped")
	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	clk := newFakeClock(epoch)
	writeCraftedLock(t, root, addr, cmd.Process.Pid, clk.Now())
	lock, err := AcquireLock(root, addr, clk)
	require.NoError(t, err, "an exited owner cannot block recovery, even with a recent heartbeat")
	require.NoError(t, lock.Release())
}
