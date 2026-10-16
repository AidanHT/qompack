//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLiveTaskkillPath: taskkill is named by its absolute path under %SystemRoot%, never looked up
// on PATH; with no SystemRoot there is no path (re-review N9).
func TestLiveTaskkillPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // a taskkill on PATH would be found first if it were looked up
	p, err := liveTaskkillPath()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe"), p)
	require.True(t, filepath.IsAbs(p))
	t.Setenv("SystemRoot", "")
	_, err = liveTaskkillPath()
	require.Error(t, err)
}

// TestLiveKillTree_NeverRunsTaskkillForAnExitedHost: once the pinned process has exited, the tree kill
// does not run taskkill at all, so it cannot name a pid the harness no longer owns (re-review N6).
func TestLiveKillTree_NeverRunsTaskkillForAnExitedHost(t *testing.T) {
	calls := 0
	old := liveTaskkill
	liveTaskkill = func(int) error { calls++; return nil }
	t.Cleanup(func() { liveTaskkill = old })
	bin, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(context.Background(), bin, "-test.run=^$")
	started, done := liveTreeCancel(cmd)
	require.NoError(t, cmd.Start())
	started()
	require.NoError(t, cmd.Wait())
	require.ErrorIs(t, cmd.Cancel(), os.ErrProcessDone)
	done()
	require.ErrorIs(t, cmd.Cancel(), os.ErrProcessDone)
	require.Zero(t, calls)
}
