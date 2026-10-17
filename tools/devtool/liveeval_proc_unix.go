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
// it started that stayed in its group.
//
// Its result follows os/exec's contract (Cmd.Cancel's doc; Wait's "If c.Process.Wait returned an
// error, prefer that"): exec consults Cancel's result only when the command exits with a success
// status. A process this SIGKILL ended never exits with success — Process.Wait yields an ExitError,
// "signal: killed", which Wait reports whatever Cancel returned — so a success status after a cancel
// can only be the process's own exit, which raced the cancel. A successful kill therefore returns
// os.ErrProcessDone: an own clean exit stays clean, and a killed host still surfaces as its
// ExitError. On Linux a group whose leader has exited but is not yet reaped stays killable, so the
// kill cannot tell the two cases apart itself; the contract makes it unnecessary (ruling R56 v2). A
// group already gone (ESRCH) is os.ErrProcessDone too, and a real kill failure is recorded as the walk
// error for the caller to report after Wait.
func liveKillTree(cmd *exec.Cmd, pin *liveProcPin) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.EPERM) && liveZombieLeader(cmd.Process.Pid) {
		// darwin answers EPERM for a group whose only member is an unreaped zombie: the group is gone
		// (ruling R59).
		err = nil
	}
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		pin.mu.Lock()
		pin.walkErr = fmt.Errorf("the process group %d could not be killed: %w", cmd.Process.Pid, err)
		pin.mu.Unlock()
		return pin.walkErr
	}
	return os.ErrProcessDone
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
