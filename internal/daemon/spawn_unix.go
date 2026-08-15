//go:build !windows

package daemon

import "syscall"

// sysProcAttr detaches the spawned daemon into its own session (task-3-spec.md spawn.go): Setsid
// so it survives the spawning client's own process exiting and is not delivered signals meant for
// the parent's process group.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
