//go:build !windows

package main

// Ending a host process tree, and asking whether a lock holder still runs, on Unix (V7 W6 item 8,
// re-review I1 and M8).

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// liveSetProcessGroup starts cmd in a process group of its own, so its whole tree can be ended.
func liveSetProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// liveKillTree ends the process group of the process the harness started: the host and everything
// it started that stayed in its group.
func liveKillTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// liveHolderGone reports that err says the process does not exist. Anything else, EPERM included,
// means it may be alive.
func liveHolderGone(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}

// liveProcessAlive reports whether a process with this pid may still run: signal 0 asks without
// signalling, and only "no such process" reads as gone.
func liveProcessAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return !liveHolderGone(err)
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || !liveHolderGone(err)
}
