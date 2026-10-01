//go:build linux || darwin

package testutil

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestProcessAlive_V6_ExitedUnreapedChildCannotWrite pins that an exited child its parent has not
// reaped (a zombie) counts as gone. It ran on Linux only until run 36816905394, where darwin's
// kill(pid, 0) probe called the test's own exited stand-in alive for ShutdownDaemonUntilGone's whole
// bound (procalive_darwin.go); it now runs on both POSIX platforms Qompack ships for.
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
