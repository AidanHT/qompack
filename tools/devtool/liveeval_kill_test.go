package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The helper process: started by a shell script the way npm's claude.cmd shim starts claude.exe, it
// writes its pid, prints an overage rate_limit_event and then keeps running, so a kill that ends only
// the shim leaves it alive.
const (
	liveHelperEnv     = "QOMPACK_LIVE_HELPER"
	liveHelperBinEnv  = "QOMPACK_LIVE_HELPER_BIN"
	liveHelperPIDFile = "QOMPACK_LIVE_HELPER_PIDFILE"
)

func TestLiveHelperProcess(t *testing.T) {
	if os.Getenv(liveHelperEnv) != "1" {
		return
	}
	_ = os.WriteFile(os.Getenv(liveHelperPIDFile), []byte(strconv.Itoa(os.Getpid())), 0o600)
	fmt.Println(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1790116800,` +
		`"rateLimitType":"five_hour","isUsingOverage":true}}`)
	deadline := time.Now().Add(2 * time.Minute) // never outlives a failed test by long
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	os.Exit(0)
}

// TestRunLiveProcess_KillEndsTheWholeTree: a real host started through a shim (a .bat on Windows, a
// shell script on Unix, each starting the long-lived helper as its child) is stopped on the overage
// event, and afterwards neither the shim nor the helper runs: the kill ends the process tree the
// harness started, not only its direct child (re-review I1).
func TestRunLiveProcess_KillEndsTheWholeTree(t *testing.T) {
	dir := t.TempDir()
	bin, err := os.Executable()
	require.NoError(t, err)
	var shim string
	if runtime.GOOS == "windows" {
		shim = filepath.Join(dir, "claude.cmd")
		require.NoError(t, os.WriteFile(shim, []byte("@echo off\r\n\"%"+liveHelperBinEnv+"%\" -test.run=TestLiveHelperProcess\r\n"), 0o600))
	} else {
		shim = filepath.Join(dir, "claude")
		require.NoError(t, os.WriteFile(shim, []byte("#!/bin/sh\n\"$"+liveHelperBinEnv+"\" -test.run=TestLiveHelperProcess &\nwait\n"), 0o700))
	}
	pidFile := filepath.Join(dir, "helper.pid")
	spec := liveProcSpec{
		Bin: shim, Dir: dir, Messages: []string{"hello"}, StepTimeout: time.Minute, SessionTimeout: 2 * time.Minute,
		Env:    []string{liveHelperEnv + "=1", liveHelperBinEnv + "=" + bin, liveHelperPIDFile + "=" + pidFile},
		StopOn: liveOverageStop,
	}
	started := time.Now()
	res := runLiveProcess(context.Background(), spec)
	require.Contains(t, res.Stopped, "overage", "stream %q, err %q", res.Stream, res.Err)
	require.Less(t, time.Since(started), liveExitGrace, "the stream closed at once: nothing in the tree held it open")

	raw, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	gone := false
	for i := 0; i < 50 && !gone; i++ {
		gone = !liveProcessAlive(pid)
		if !gone {
			time.Sleep(100 * time.Millisecond)
		}
	}
	require.True(t, gone, "the shim's child (pid %d) outlived the kill", pid)
}

// TestLiveHolderGone: only "no such process" means a lock holder is gone; access denied, or any other
// error, means it may be alive, so its lock is never taken over (re-review M8).
func TestLiveHolderGone(t *testing.T) {
	require.True(t, liveHolderGone(os.ErrProcessDone))
	require.False(t, liveHolderGone(nil))
	require.False(t, liveHolderGone(errors.New("something else")))
	if runtime.GOOS == "windows" {
		require.True(t, liveHolderGone(os.NewSyscallError("OpenProcess", syscall.Errno(87))), "ERROR_INVALID_PARAMETER")
		require.False(t, liveHolderGone(os.NewSyscallError("OpenProcess", syscall.Errno(5))), "ERROR_ACCESS_DENIED")
	} else {
		require.True(t, liveHolderGone(syscall.ESRCH))
		require.False(t, liveHolderGone(syscall.EPERM))
	}
}
