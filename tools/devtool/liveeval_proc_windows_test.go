//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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
	started, done, _ := liveTreeCancel(cmd)
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
	_, done, _ := liveTreeCancel(cmd)
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
	started, done, _ := liveTreeCancel(cmd)
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
	started, done, _ := liveTreeCancel(cmd)
	require.NoError(t, cmd.Start())
	started()
	require.NoError(t, cmd.Wait())
	require.ErrorIs(t, cmd.Cancel(), os.ErrProcessDone)
	done()
	require.ErrorIs(t, cmd.Cancel(), os.ErrProcessDone)
	require.Zero(t, calls)
}

// helperSpec is a runLiveProcess spec for the helper process started directly: it prints an overage
// event, so the driver stops it, and then keeps running until it is killed.
func helperSpec(t *testing.T) liveProcSpec {
	t.Helper()
	bin, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	return liveProcSpec{
		Bin: bin, Args: []string{"-test.run=^TestLiveHelperProcess$"}, Dir: dir, Messages: []string{"hello"},
		StepTimeout: time.Minute, SessionTimeout: 2 * time.Minute, StopOn: liveOverageStop,
		Env: []string{liveHelperEnv + "=1", liveHelperPIDFile + "=" + filepath.Join(dir, "helper.pid")},
	}
}

// fakeTaskkill replaces liveTaskkill for one test.
func fakeTaskkill(t *testing.T, f func(pid int) error) {
	t.Helper()
	old := liveTaskkill
	liveTaskkill = f
	t.Cleanup(func() { liveTaskkill = old })
}

// TestRunLiveProcess_FailedWalkReachesTheRecord: a tree walk that fails outright is reported in the
// session's own error, through Cancel, Wait and the driver's stop message, which it joins rather
// than replaces: exec's Wait drops Cancel's error once the killed child exits non-zero (re-review 4,
// F2 path 1).
func TestRunLiveProcess_FailedWalkReachesTheRecord(t *testing.T) {
	fakeTaskkill(t, func(int) error { return errors.New("taskkill exploded") })
	res := runLiveProcess(context.Background(), helperSpec(t))
	require.Contains(t, res.Err, "stopped by the driver")
	require.Contains(t, res.Err, "only the direct child was killed")
	require.Contains(t, res.Err, "taskkill exploded")
}

// TestRunLiveProcess_PartialWalkIsNotAnExit: taskkill that ends the root but fails on a descendant
// exits 255; the root being gone afterwards does not make that "already finished", and the walk's
// failure is reported (re-review 4, F2 path 2). Only taskkill's not-found exit (128) after the root
// exited reads as an exit.
func TestRunLiveProcess_PartialWalkIsNotAnExit(t *testing.T) {
	killRoot := func(pid int) {
		p, err := os.FindProcess(pid) // the helper this test started
		require.NoError(t, err)
		_ = p.Kill()
		_ = p.Release()
	}
	fakeTaskkill(t, func(pid int) error {
		killRoot(pid)
		return &liveTaskkillError{Code: 255, Output: "ERROR: The process with PID 1 (child process of PID 2) could not be terminated."}
	})
	res := runLiveProcess(context.Background(), helperSpec(t))
	require.Contains(t, res.Err, "exit 255")
	require.Contains(t, res.Err, "could not be terminated")

	fakeTaskkill(t, func(pid int) error {
		killRoot(pid)
		return &liveTaskkillError{Code: liveTaskkillNotFound, Output: `ERROR: The process "1" not found.`}
	})
	res = runLiveProcess(context.Background(), helperSpec(t))
	require.Contains(t, res.Err, "stopped by the driver")
	require.NotContains(t, res.Err, "not found", "the root had exited: nothing to report")
	require.NotContains(t, res.Err, "could not be ended")
}

// TestLiveRunPinned_FailedWalkIsReturned: claude, check and git commands report a failed walk too.
func TestLiveRunPinned_FailedWalkIsReturned(t *testing.T) {
	fakeTaskkill(t, func(int) error { return errors.New("taskkill exploded") })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLiveHelperProcess$")
	cmd.Env = append(os.Environ(), liveHelperEnv+"=1", liveHelperModeEnv+"=sleep")
	go func() {
		tick := time.NewTimer(500 * time.Millisecond)
		defer tick.Stop()
		<-tick.C
		cancel()
	}()
	err := liveRunPinned(cmd)
	require.ErrorContains(t, err, "taskkill exploded")
}

// TestLiveTaskkill_NotFoundExit pins what the not-found reading rests on: taskkill exits 128 for a
// pid no process has (established on this machine; see the report's fix round 5).
func TestLiveTaskkill_NotFoundExit(t *testing.T) {
	err := liveTaskkill(2147483647) // an odd pid: Windows gives out multiples of four
	var tk *liveTaskkillError
	require.ErrorAs(t, err, &tk)
	require.Equal(t, liveTaskkillNotFound, tk.Code, tk.Output)
}


// TestRunLiveCLI_FailedWalkIsKept: a claude subcommand whose tree kill failed keeps the walk error in
// its stderr, beside its exit code (R53, H2).
func TestRunLiveCLI_FailedWalkIsKept(t *testing.T) {
	fakeTaskkill(t, func(int) error { return errors.New("taskkill exploded") })
	t.Setenv(liveHelperEnv, "1")
	t.Setenv(liveHelperModeEnv, "sleep")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		tick := time.NewTimer(500 * time.Millisecond)
		defer tick.Stop()
		<-tick.C
		cancel()
	}()
	r := runLiveCLI(ctx, os.Args[0], "", "-test.run=^TestLiveHelperProcess$")
	require.NotZero(t, r.Code)
	require.Contains(t, r.Stderr, "taskkill exploded")
}

// TestRunLiveCheckCommand_FailedWalkOnTimeout: a check command that timed out and whose tree kill
// failed reports both (R53, H2).
func TestRunLiveCheckCommand_FailedWalkOnTimeout(t *testing.T) {
	fakeTaskkill(t, func(int) error { return errors.New("taskkill exploded") })
	old := liveCheckCommandTimeout
	liveCheckCommandTimeout = 500 * time.Millisecond
	t.Cleanup(func() { liveCheckCommandTimeout = old })
	t.Setenv(liveHelperEnv, "1")
	t.Setenv(liveHelperModeEnv, "sleep")
	co := runLiveCheckCommand(t.TempDir(), []string{os.Args[0], "-test.run=^TestLiveHelperProcess$"})
	require.Error(t, co.Err)
	require.Contains(t, co.Err.Error(), "timed out")
	require.Contains(t, co.Err.Error(), "taskkill exploded")
}

// TestLiveKillTree_NoSettleWaitWhenTaskkillNeverRan: when taskkill could not even run, the root is not
// waited for before the direct child is killed (R53, N1).
func TestLiveKillTree_NoSettleWaitWhenTaskkillNeverRan(t *testing.T) {
	fakeTaskkill(t, func(int) error { return errors.New("taskkill.exe cannot be named") })
	cmd := liveHelperCmd(t, "sleep")
	started, done, _ := liveTreeCancel(cmd)
	require.NoError(t, cmd.Start())
	started()
	begun := time.Now()
	require.ErrorContains(t, cmd.Cancel(), "only the direct child")
	require.Less(t, time.Since(begun), time.Second, "no settle wait")
	require.Error(t, cmd.Wait())
	done()
}
