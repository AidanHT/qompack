//go:build darwin

package testutil

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

// darwinSZOMB is p_stat's zombie state in darwin's <sys/proc.h> (SIDL 1, SRUN 2, SSLEEP 3,
// SSTOP 4, SZOMB 5). golang.org/x/sys/unix exports the field but not the constant.
const darwinSZOMB = 5

// ProcessAlive reports whether a process can still execute. kill(pid, 0) succeeds for an exited
// process its parent has not reaped yet (a zombie), which can no longer write to anything. On
// macOS that is the ordinary state of a test's own exited child until its cmd.Wait runs: run
// 36816905394 saw ShutdownDaemonUntilGone wait its whole 15 s bound for a stand-in that had exited
// at once and was reaped 121 µs after the helper returned. So a process kill answers for is asked
// its state through sysctl kern.proc.pid, as ps does, and a zombie counts as gone; Linux asks
// /proc/<pid>/stat the same question (procalive_linux.go).
//
// Only ESRCH, or a zombie state the kernel reports, is proof of death. EPERM from kill, a sysctl
// that fails, or one that answers with any other state counts as alive: a shutdown helper
// returning early would hand a live writer's directory to its caller's t.TempDir RemoveAll.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return !errors.Is(err, syscall.ESRCH)
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return true
	}
	return kp.Proc.P_stat != darwinSZOMB
}
