//go:build windows

package main

// Ending a host process tree, and asking whether a lock holder still runs, on Windows (V7 W6 item 8,
// re-review I1 and M8).

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// liveSetProcessGroup prepares cmd so its whole tree can be ended: nothing on Windows, where taskkill
// walks the tree from the pid the harness started.
func liveSetProcessGroup(*exec.Cmd) {}

// liveKillTree ends the process the harness started and every process it started in turn: the
// default host is npm's claude.cmd shim, so the direct child is cmd.exe and claude.exe its child,
// which a kill of the direct child alone leaves running. taskkill /T only walks down from the given
// pid, so it touches nothing the harness did not start.
func liveKillTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	out, err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).CombinedOutput()
	if err != nil {
		// The tree walk failed (the direct child may already be gone); end the direct child at least.
		kerr := cmd.Process.Kill()
		if errors.Is(kerr, os.ErrProcessDone) {
			return nil
		}
		return errors.Join(fmt.Errorf("taskkill /T /F /PID %d: %w: %s", cmd.Process.Pid, err, out), kerr)
	}
	return nil
}

// errNoSuchProcess is ERROR_INVALID_PARAMETER, what OpenProcess answers for a pid no process has.
const errNoSuchProcess = syscall.Errno(87)

// processQueryLimited is PROCESS_QUERY_LIMITED_INFORMATION, the least right that reads an exit code.
const processQueryLimited = 0x1000

// stillActive is STILL_ACTIVE, the exit code of a process that has not exited.
const stillActive = 259

// liveHolderGone reports that err says the process does not exist. Anything else, access denied
// included, means it may be alive.
func liveHolderGone(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, errNoSuchProcess)
}

// liveProcessAlive reports whether a process with this pid may still run on this machine: only a
// pid no process has, or a process that has exited, reads as gone.
func liveProcessAlive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimited, false, uint32(pid))
	if err != nil {
		return !liveHolderGone(err)
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
