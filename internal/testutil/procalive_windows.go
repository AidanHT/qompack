//go:build windows

package testutil

import (
	"errors"
	"syscall"
)

// ProcessAlive reports whether a process with this pid is still running — and, on Windows, "not
// running" means its process object has been SIGNALED, not merely that it has an exit code.
//
// internal/daemon asks the same question as step 3 of its staleness protocol and, on Windows,
// declines to answer: lock_windows.go's pidAlive returns known=false. That is the right call
// there, because AcquireLock has a heartbeat-mtime fallback to reach for when the pid probe has
// no opinion. A test's shutdown helper has no such fallback — its only alternative would be
// daemon.staleAfter, 90 seconds, per subtest — so it asks Windows directly instead.
//
// Every caller returns on "not alive" and hands a project directory to t.TempDir's RemoveAll, so
// the answer has to mean the process can no longer hold anything open there. An exit code does not
// mean that. Windows sets a process's exit code before it closes the process's handles and unmaps
// its memory, and signals the process object only after both. This probe used to be
// GetExitCodeProcess != STILL_ACTIVE, and measured against a child holding files open without
// FILE_SHARE_DELETE (plans/sdd/V6-closeout/w4-e2eflakes/runs/diag-a-exitcode-vs-signaled-windows.txt):
// in 120 of 120 exits the exit code was set while the object was not yet signaled, for up to
// 151 ms; in 115 of them the child's file still could not be deleted at that instant; in 0 of 120
// was it still held once the object was signaled. TestProcessAlive_V6_WindowsExitedChildHoldsNoHandles
// pins the difference.
//
// OpenProcess(SYNCHRONIZE) + a zero-timeout WaitForSingleObject is decisive in both directions.
// Measured on this tree's own runner (windows/amd64, go1.26.6; w4-e2eflakes runs/
// diag-a-probe-table-windows.txt), with a DETACHED_PROCESS child standing in for a spawned daemon:
//
//	this process                                 -> WAIT_TIMEOUT             -> alive
//	a live detached child                        -> WAIT_TIMEOUT             -> alive
//	that child after Kill + Wait + Release       -> ERROR_INVALID_PARAMETER  -> dead
//	a pid that never existed                     -> ERROR_INVALID_PARAMETER  -> dead
//	pid 4 (System, not ours to query)            -> ERROR_ACCESS_DENIED      -> alive
//
// The two OpenProcess errors are not interchangeable. ERROR_INVALID_PARAMETER means no process
// carries that id; ERROR_ACCESS_DENIED means one does and this process may not look at it. Only
// the first is proof of death, and reading the second as "dead" would be the same mistake these
// helpers exist to prevent: returning while a live writer still holds the caller's directory.
//
// os.FindProcess plus Signal(0) — the shape that answers this on POSIX — is not usable here.
// Measured on the same runner, Signal(0) returns "not supported by windows" for a live process
// and for an exited one alike, so it cannot tell them apart at all.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = syscall.CloseHandle(h) }()

	event, err := syscall.WaitForSingleObject(h, 0)
	if err != nil {
		// The handle opened, so a process object is there; a wait that failed for some other
		// reason is not evidence of death, and guessing "dead" is the expensive direction.
		return true
	}
	return event != syscall.WAIT_OBJECT_0
}
