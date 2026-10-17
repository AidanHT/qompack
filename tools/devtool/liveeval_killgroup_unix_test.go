//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestLiveKillTree_RunningLeaderSurfacesAsKilled: killing a running host by cancelling its context
// ends its whole process group, and Wait reports the host's own death by SIGKILL as an *exec.ExitError
// (never nil, never context.Canceled) whatever Cancel returned: exec consults Cancel's result only for
// a success exit, which a killed process never has (ruling R56 v2). The host's descendant in the group
// is gone too.
func TestLiveKillTree_RunningLeaderSurfacesAsKilled(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `sleep 30 & echo $! > "$1"; wait`, "sh", pidFile)
	started, done, killErr := liveTreeCancel(cmd)
	require.NoError(t, cmd.Start())
	started()

	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var raw []byte
	for i := 0; i < 500; i++ {
		if b, err := os.ReadFile(pidFile); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			raw = b
			break
		}
		<-tick.C
	}
	child, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err, "the shell wrote its child's pid")

	cancel()
	werr := cmd.Wait()
	done()
	require.Error(t, werr)
	require.False(t, errors.Is(werr, context.Canceled), "a killed host surfaces as its exit, not as the cancellation: %v", werr)
	var exitErr *exec.ExitError
	require.ErrorAs(t, werr, &exitErr)
	ws, ok := exitErr.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	require.True(t, ws.Signaled(), "%v", werr)
	require.Equal(t, syscall.SIGKILL, ws.Signal())
	require.NoError(t, killErr(), "the group kill itself did not fail")
	require.True(t, waitGone(child), "the host's descendant in its group (pid %d) outlived the kill", child)
}
