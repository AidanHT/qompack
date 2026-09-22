//go:build linux

package testutil

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcessAlive_V6_ExitedUnreapedChildCannotWrite(t *testing.T) {
	require.True(t, ProcessAlive(os.Getpid()))
	cmd := exec.Command("sh", "-c", "exit 0")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Wait() })
	require.Eventually(t, func() bool { return !ProcessAlive(cmd.Process.Pid) }, 5*time.Second, 10*time.Millisecond)
	// Wait has deliberately not reaped the child. POSIX signal existence alone
	// gives the old, incorrect answer even though the process has exited.
	require.NoError(t, syscall.Kill(cmd.Process.Pid, 0))
}
