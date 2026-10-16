//go:build !windows

package main

import (
	"os"
	"syscall"
)

// liveSignals stop live-eval and live-check. The host runs in its own process group, so it is not one
// of the shell's jobs: a closed terminal (SIGHUP) or SIGQUIT that ended devtool by default would leave
// it running, and both are caught like an interrupt (re-review F3).
var liveSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT}
