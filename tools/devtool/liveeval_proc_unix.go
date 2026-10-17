//go:build !windows

package main

// Ending a host process tree, and asking whether a lock holder still runs, on Unix (V7 W6 item 8,
// re-review I1 and M8).

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// liveSetProcessGroup starts cmd in a process group of its own, so its whole tree can be ended.
func liveSetProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	livePdeath(cmd.SysProcAttr)
}

// liveProcPin keeps nothing on Unix: the group is killed by its id, and a group that is gone answers
// ESRCH.
type liveProcPin struct {
	mu      sync.Mutex
	walkErr error
}

func (*liveProcPin) pin(*exec.Cmd) {}

func (*liveProcPin) release() {}

// killError is the error of a kill that failed, for the caller to report after Wait, which drops
// Cancel's error once the killed process exits non-zero (re-review 4, F2).
func (p *liveProcPin) killError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.walkErr
}

// liveKillTree ends the process group of the process the harness started: the host and everything
// it started that stayed in its group. A group that is already gone reports os.ErrProcessDone, so exec
// does not report a cancellation for a process that had exited (re-review N6).
func liveKillTree(cmd *exec.Cmd, pin *liveProcPin) error {
	if cmd.Process == nil {
		return nil
	}
	// On Linux a leader that has exited but is not yet reaped keeps its group killable, so the kill
	// would succeed and exec would report a clean exit as cancelled. The exit is read first, without
	// reaping; the group is still killed, to end any descendant left in it (ruling R56).
	leaderExited := liveLeaderExited(cmd.Process.Pid)
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		pin.mu.Lock()
		pin.walkErr = fmt.Errorf("the process group %d could not be killed: %w", cmd.Process.Pid, err)
		pin.mu.Unlock()
		return pin.walkErr
	}
	// A leader already exiting on its own when the kill landed is an exit too, not a cancellation
	// (R56 refinement).
	if leaderExited || liveLeaderExitedOnItsOwn(cmd.Process.Pid) {
		return os.ErrProcessDone
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
