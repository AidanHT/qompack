//go:build !windows

package testutil

import (
	"errors"
	"syscall"
)

// ProcessAlive reports whether a process with this pid is still running, through the POSIX
// kill(pid, 0) probe. It is the same question — and the same answer — as step 3 of the daemon's
// own staleness protocol (internal/daemon/lock_unix.go's pidAlive), respelled here because that
// helper is unexported and internal/daemon exports no equivalent.
//
// Only ESRCH is proof of death. EPERM, or any other errno, counts as alive for pidAlive's stated
// reason: a process this call may not signal is still a process, and a shutdown helper returning
// early on one would hand a live writer's directory to its caller's t.TempDir RemoveAll.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return !errors.Is(err, syscall.ESRCH)
}
