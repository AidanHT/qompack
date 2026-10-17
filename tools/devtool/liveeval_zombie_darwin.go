//go:build darwin

package main

import "golang.org/x/sys/unix"

// darwinSZOMB is SZOMB, the p_stat of a process that has exited and awaits its parent's wait: 5 in
// darwin's sys/proc.h (#define SZOMB 5). golang.org/x/sys/unix does not name it.
const darwinSZOMB = 5

// liveZombieLeader reports that the process pid is an unreaped zombie, read through sysctl
// kern.proc.pid without reaping it. On darwin killpg on a group whose only member is such a zombie
// can answer EPERM instead of ESRCH, and that group is gone (ruling R59).
func liveZombieLeader(pid int) bool {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	return err == nil && kp != nil && kp.Proc.P_stat == darwinSZOMB
}
