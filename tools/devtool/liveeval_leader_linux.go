//go:build linux

package main

import (
	"time"

	"golang.org/x/sys/unix"
)

// liveLeaderSettle bounds the wait, after a successful group kill, for the leader to become waitable:
// SIGKILL makes it waitable promptly, so the bound is only a backstop.
const liveLeaderSettle = 2 * time.Second

// cldExited is CLD_EXITED, siginfo's si_code for a child that called exit (CLD_KILLED is 2,
// CLD_DUMPED 3); golang.org/x/sys/unix does not name it.
const cldExited = 1

// liveLeaderPoll is how often that wait asks.
const liveLeaderPoll = 10 * time.Millisecond

// liveLeaderState asks waitid, without reaping (WNOWAIT, so Go's own Wait still collects the child),
// whether the child pid has exited, and how: ok is false while it has not.
func liveLeaderState(pid int) (code int32, ok bool) {
	var info unix.Siginfo
	if err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil); err != nil {
		return 0, false
	}
	if info.Signo != int32(unix.SIGCHLD) {
		return 0, false
	}
	return info.Code, true
}

// liveLeaderExited reports that the process pid, a child of devtool, has exited but may not yet be
// reaped. On Linux a kill of the group of such a zombie leader succeeds, so the tree kill cannot learn
// of the exit from the kill itself (ruling R56). It is the fast path before the kill.
func liveLeaderExited(pid int) bool {
	_, ok := liveLeaderState(pid)
	return ok
}

// liveLeaderExitedOnItsOwn reports, after a successful group kill, whether the leader had already been
// exiting on its own: a leader in do_exit has closed its descriptors (so its stdout reached EOF) but is
// not yet waitable, and the kill does not change its exit. It waits up to liveLeaderSettle for the
// leader to become waitable and reads how it ended: CLD_EXITED is an exit on its own, whatever its code;
// CLD_KILLED or CLD_DUMPED is the kill, a real cancellation; no answer within the bound is read as the
// kill (R56 refinement).
func liveLeaderExitedOnItsOwn(pid int) bool {
	tick := time.NewTicker(liveLeaderPoll)
	defer tick.Stop()
	deadline := time.NewTimer(liveLeaderSettle)
	defer deadline.Stop()
	for {
		if code, ok := liveLeaderState(pid); ok {
			return code == cldExited
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			return false
		}
	}
}
