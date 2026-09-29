//go:build windows

package daemon

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// pidAlive is step 3 of the staleness protocol on Windows, and it answers in one direction only:
// that pid's process has exited.
//
// os.FindProcess succeeds on Windows whether or not pid is running, and Signal(0) answers "not
// supported by windows" for a live process and an exited one alike, so neither can tell them apart.
// OpenProcess(SYNCHRONIZE) plus a zero-timeout wait on the handle is decisive (the probe
// internal/testutil's ProcessAlive measured, w4-e2eflakes runs/diag-a-probe-table-windows.txt):
// ERROR_INVALID_PARAMETER means no process carries that id, and a signaled process object means the
// process has ended. Either is proof the lock's owner is gone, so the lock is stale at once rather
// than after staleAfter of heartbeat silence — the 90 seconds in which maintenance refused "daemon
// lock already held" and retrieval answered "temporarily offline" after a daemon was terminated
// (V6 close-out F-UAT05-4, F-C49-4).
//
// Every other answer is no opinion, and the heartbeat decides as before. A process that is running
// may be a different one that reused the dead owner's pid, which Windows does, so "alive" is not
// proof that the owner lives; and ERROR_ACCESS_DENIED means a process exists that this one may not
// inspect. Reading either as proof of life would hold a dead owner's lock for as long as an
// unrelated process lives, which is worse than the heartbeat window it replaces.
func pidAlive(pid int) (alive bool, known bool) {
	if pid <= 0 {
		return false, false
	}
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false, true
		}
		return false, false
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	event, err := syscall.WaitForSingleObject(h, 0)
	if err == nil && event == syscall.WAIT_OBJECT_0 {
		return false, true
	}
	return false, false
}
