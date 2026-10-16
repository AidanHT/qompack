//go:build !windows

package main

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestLiveSignals_Unix: a closed terminal (SIGHUP) and SIGQUIT stop the run too, so the host in its
// own process group is not left running (re-review F3).
func TestLiveSignals_Unix(t *testing.T) {
	require.Contains(t, liveSignals, os.Signal(syscall.SIGHUP))
	require.Contains(t, liveSignals, os.Signal(syscall.SIGQUIT))
}

// TestLiveSignalContext_SIGTERMCancels: a SIGTERM to devtool cancels the run's context, which runs the
// tree kill of the host (re-review N5).
func TestLiveSignalContext_SIGTERMCancels(t *testing.T) {
	ctx, stop := liveSignalContext()
	defer stop()
	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGTERM))
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM did not cancel the run's context")
	}
}
