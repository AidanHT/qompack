//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// liveSignalList is the signals that stop live-eval and live-check. The host runs in its own process
// group, so it is not one of the shell's jobs: a closed terminal (SIGHUP) or SIGQUIT that ended devtool
// by default would leave it running (re-review F3). SIGHUP is caught only when the run was not started
// with it ignored: catching it would undo nohup (re-review 4, G1). SIGQUIT is always caught, and so no
// longer prints Go's goroutine dump: never running into paid overage outranks that debugging aid
// (ruling R52).
func liveSignalList() []os.Signal {
	sigs := []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT}
	if !signal.Ignored(syscall.SIGHUP) {
		sigs = append(sigs, syscall.SIGHUP)
	}
	return sigs
}
