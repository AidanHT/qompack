//go:build !windows

package daemon

import "syscall"

// pidAlive answers the POSIX kill(pid, 0) probe (task-3-spec.md lock.go, staleness step 3): alive,
// true means the process answered (still running); false, true means ESRCH (definitely gone).
// POSIX always has an opinion, so known is always true on this platform.
func pidAlive(pid int) (alive bool, known bool) {
	if pid <= 0 {
		return false, true
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true, true
	}
	if err == syscall.ESRCH { //nolint:errorlint // syscall.Errno comparison is the documented idiom for kill(2)'s result
		return false, true
	}
	// EPERM (the process exists but is owned by someone else) or any other errno: treat as alive.
	// A process this call cannot signal is still a process, and reclaiming a lock out from under a
	// live daemon is a worse failure than waiting for the heartbeat to actually go stale.
	return true, true
}
