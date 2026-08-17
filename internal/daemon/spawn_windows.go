//go:build windows

package daemon

import "syscall"

// createNoWindow and detachedProcess are declared locally rather than pulled from
// golang.org/x/sys/windows, because §2.5 closes the runtime dependency list at
// klauspost/compress/zstd and Microsoft/go-winio (task-3-spec.md spawn.go).
const (
	createNoWindow  = 0x08000000 // CREATE_NO_WINDOW
	detachedProcess = 0x00000008 // DETACHED_PROCESS
)

// sysProcAttr detaches the spawned daemon from the spawning client's console (task-3-spec.md
// spawn.go): no window is created, and the child is fully detached from the parent's process
// tree.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | detachedProcess}
}
