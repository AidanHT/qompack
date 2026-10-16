//go:build windows

package main

import (
	"os"
	"syscall"
)

// liveSignalList is the signals that stop live-eval and live-check. On Windows Ctrl-C and Ctrl-Break
// arrive as os.Interrupt, and closing the console, logging off or shutting down arrive as SIGTERM
// (re-review F5).
func liveSignalList() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }
