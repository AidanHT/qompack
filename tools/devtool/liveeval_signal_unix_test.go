//go:build !windows

package main

import (
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
