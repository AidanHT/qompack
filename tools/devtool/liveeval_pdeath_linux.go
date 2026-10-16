//go:build linux

package main

import "syscall"

// livePdeath asks Linux to SIGKILL the host when devtool itself dies, a crash included (re-review F3).
// The signal follows the thread that started the child; Go keeps its threads, so that is devtool's
// life in practice.
func livePdeath(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }
