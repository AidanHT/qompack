//go:build windows

package e2e

import (
	"errors"
	"syscall"
)

// stillActiveExitCode is GetExitCodeProcess's "has not exited yet" sentinel — STILL_ACTIVE, the
// 259 that Windows reports as a running process's exit code. It is declared here rather than
// pulled from golang.org/x/sys/windows for the reason internal/daemon/spawn_windows.go gives for
// its own CREATE_NO_WINDOW/DETACHED_PROCESS constants; the standard syscall package does carry
// OpenProcess and GetExitCodeProcess, it simply does not export this one constant (measured:
// `undefined: syscall.STILL_ACTIVE`).
const stillActiveExitCode = 259

// e2eProcessAlive reports whether a process with this pid is still running.
//
// internal/daemon asks the same question as step 3 of its staleness protocol and, on Windows,
// declines to answer: lock_windows.go's pidAlive returns known=false. That is the right call
// there, because AcquireLock has a heartbeat-mtime fallback to reach for when the pid probe has
// no opinion. e2eShutdownIfReachable has no such fallback — its only alternative would be
// daemon.staleAfter, 90 seconds, per subtest — so it asks Windows directly instead.
//
// OpenProcess + GetExitCodeProcess is decisive in both directions. Measured on this tree's own
// runner (windows/amd64, go1.26.6), with a DETACHED_PROCESS child standing in for a spawned
// daemon:
//
//	this process                                 -> exit code 259            -> alive
//	a live detached child                        -> exit code 259            -> alive
//	that child after Kill + Wait + Release       -> ERROR_INVALID_PARAMETER  -> dead
//	a pid that never existed                     -> ERROR_INVALID_PARAMETER  -> dead
//	pid 4 (System, not ours to query)            -> ERROR_ACCESS_DENIED      -> alive
//
// The two OpenProcess errors are not interchangeable. ERROR_INVALID_PARAMETER means no process
// carries that id; ERROR_ACCESS_DENIED means one does and this process may not look at it. Only
// the first is proof of death, and reading the second as "dead" would be the same mistake
// e2eShutdownIfReachable is being fixed for: returning while a live writer still holds the
// caller's temp directory open.
//
// os.FindProcess plus Signal(0) — the shape that answers this on POSIX — is not usable here.
// Measured on the same runner, Signal(0) returns "not supported by windows" for a live process
// and for an exited one alike, so it cannot tell them apart at all.
func e2eProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = syscall.CloseHandle(h) }()

	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		// The handle opened, so a process object is there; a query that failed for some other
		// reason is not evidence of death, and guessing "dead" is the expensive direction.
		return true
	}
	return code == stillActiveExitCode
}
