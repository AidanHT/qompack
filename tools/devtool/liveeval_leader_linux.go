//go:build linux

package main

import "golang.org/x/sys/unix"

// liveLeaderExited reports that the process pid, a child of devtool, has exited but may not yet be
// reaped. It asks waitid with WNOWAIT, so the child stays waitable and Go's own Wait still collects it
// (ruling R56). On Linux a kill of the group of such a zombie leader succeeds, so the tree kill cannot
// learn of the exit from the kill itself.
func liveLeaderExited(pid int) bool {
	var info unix.Siginfo
	if err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil); err != nil {
		return false
	}
	return info.Signo == int32(unix.SIGCHLD)
}
