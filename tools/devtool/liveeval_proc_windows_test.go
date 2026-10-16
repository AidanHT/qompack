//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// TestLiveTaskkillPath: taskkill is named by its absolute path under %SystemRoot%, never looked up
// on PATH; with no SystemRoot there is no path (re-review N9).
func TestLiveTaskkillPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // a taskkill on PATH would be found first if it were looked up
	p, err := liveTaskkillPath()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe"), p)
	require.True(t, filepath.IsAbs(p))
	// An unusable SystemRoot falls back to the system directory Windows itself reports (re-review F2).
	sys, err := windows.GetSystemDirectory()
	require.NoError(t, err)
	for _, bad := range []string{"", "relative\\dir"} {
		t.Setenv("SystemRoot", bad)
		p, err = liveTaskkillPath()
		require.NoError(t, err, bad)
		require.Equal(t, filepath.Join(sys, "taskkill.exe"), p)
	}
}

// TestLiveKillTree_FailedTaskkillIsNeverSilent: when the tree walk fails the direct child is still
// killed, and the taskkill error is returned so the harness reports it (re-review F2).
func TestLiveKillTree_FailedTaskkillIsNeverSilent(t *testing.T) {
	old := liveTaskkill
	liveTaskkill = func(int) error { return errors.New("taskkill exploded") }
	t.Cleanup(func() { liveTaskkill = old })
	cmd := liveHelperCmd(t, "sleep")
	started, done := liveTreeCancel(cmd)
	require.NoError(t, cmd.Start())
	started()
	err := cmd.Cancel()
	require.ErrorContains(t, err, "taskkill exploded")
	require.ErrorContains(t, err, "direct child")
	require.Error(t, cmd.Wait(), "the direct child was killed")
	done()
}

// TestLiveKillTree_BeforePinStillWalksTheTree: a cancel that lands between Start and the pin still
// runs taskkill: Go's own handle pins the pid until Wait (re-review F2).
func TestLiveKillTree_BeforePinStillWalksTheTree(t *testing.T) {
	calls := 0
	old := liveTaskkill
	liveTaskkill = func(int) error { calls++; return nil }
	t.Cleanup(func() { liveTaskkill = old })
	cmd := liveHelperCmd(t, "sleep")
	_, done := liveTreeCancel(cmd)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); done() }) // the fake taskkill killed nothing
	require.NoError(t, cmd.Cancel())
	require.Equal(t, 1, calls)
}

// TestLiveKillTree_ExitCode259IsAnExit: a host that exited with code 259 (STILL_ACTIVE's value) reads
// as exited, because the pin waits on the handle instead of reading the code (re-review F4).
func TestLiveKillTree_ExitCode259IsAnExit(t *testing.T) {
	calls := 0
	old := liveTaskkill
	liveTaskkill = func(int) error { calls++; return nil }
	t.Cleanup(func() { liveTaskkill = old })
	cmd := liveHelperCmd(t, "exit259")
	started, done := liveTreeCancel(cmd)
	require.NoError(t, cmd.Start())
	started()
	require.Error(t, cmd.Wait())
	require.ErrorIs(t, cmd.Cancel(), os.ErrProcessDone)
	require.Zero(t, calls)
	done()
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
