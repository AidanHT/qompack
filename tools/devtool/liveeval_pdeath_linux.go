//go:build linux

package main

import "syscall"

// livePdeath asks Linux to SIGKILL the host when devtool itself dies, a crash included (re-review F3).
// The signal follows the OS thread that started the child, not the process: Go keeps its threads, so
// that is devtool's life in practice, but a start from a goroutine that called runtime.LockOSThread
// and then exits without unlocking would end that thread and kill the host (no live code does that).
func livePdeath(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }
