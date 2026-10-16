//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestLiveSignals_Unix: a closed terminal (SIGHUP) and SIGQUIT stop the run too, so the host in its
// own process group is not left running (re-review F3); but a SIGHUP the run was started with ignored
// (nohup) stays ignored (re-review 4, G1).
func TestLiveSignals_Unix(t *testing.T) {
	require.Contains(t, liveSignalList(), os.Signal(syscall.SIGHUP))
	require.Contains(t, liveSignalList(), os.Signal(syscall.SIGQUIT))
	signal.Ignore(syscall.SIGHUP)
	defer func() {
		// Reset alone does not undo Ignore; Notify clears the ignored state first (R53, H3).
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGHUP)
		signal.Reset(syscall.SIGHUP)
		require.False(t, signal.Ignored(syscall.SIGHUP), "SIGHUP is no longer ignored for later tests")
	}()
	require.NotContains(t, liveSignalList(), os.Signal(syscall.SIGHUP), "nohup's SIGHUP stays ignored")
	require.Contains(t, liveSignalList(), os.Signal(syscall.SIGQUIT))
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
