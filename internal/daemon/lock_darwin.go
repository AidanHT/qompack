//go:build darwin

package daemon

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

// darwinSZOMB is p_stat's zombie state in darwin's <sys/proc.h> (SIDL 1, SRUN 2, SSLEEP 3,
// SSTOP 4, SZOMB 5). golang.org/x/sys/unix exports the field but not the constant.
const darwinSZOMB = 5

// pidAlive also recognizes exited, unreaped darwin processes, as lock_linux.go does from
// /proc/<pid>/stat. A zombie still answers kill(pid, 0) but cannot own a running daemon or publish
// any further writes.
//
// A daemon is not always reaped by launchd. SpawnDetached starts it in its own session and
// Release()s it, which drops the handle but does not wait, so the daemon stays the child of the
// process that spawned it. A hook or a CLI command exits within milliseconds and the daemon is
// re-parented to launchd, which reaps it; `qompack mcp` lives as long as the host session and
// spawns lazily (internal/cli/cmd_mcp.go), so a daemon it started that dies without releasing its
// lock stays a zombie of that server. kill(pid, 0) alone would then call the lock live for the
// rest of the session: step 3 of lockIsStale is decisive, and the heartbeat is never consulted.
//
// A live IPC listener still wins before this probe in lockIsStale. EPERM, a failed sysctl and any
// state other than SZOMB remain alive.
func pidAlive(pid int) (alive bool, known bool) {
	if pid <= 0 {
		return false, true
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return !errors.Is(err, syscall.ESRCH), true
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return true, true
	}
	return kp.Proc.P_stat != darwinSZOMB, true
}
