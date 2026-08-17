package ipc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/logging"
)

// TestProbe_FalseWhenNothingListens pins the negative case: no server bound at the address.
func TestProbe_FalseWhenNothingListens(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addr, err := Resolve(root)
	require.NoError(t, err)

	require.False(t, Probe(addr, 50*time.Millisecond))
}

// TestProbe_DoesNotWaitForAResponse pins Ruling #22: Probe reports alive on a successful dial
// alone — it never writes a request and never waits on a handler's response. Proven against a
// server whose handler would hang forever if it were ever reached: Probe must still return true,
// promptly, because it never gets far enough to invoke the handler at all.
func TestProbe_DoesNotWaitForAResponse(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addr, err := Resolve(root)
	require.NoError(t, err)

	srv, err := NewServer(addr, logging.Nop(), nil, 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	go func() {
		_ = srv.Serve(ctx, func(context.Context, Request) Response {
			select {} // a handler that hangs forever: Probe must never reach it
		})
	}()

	start := time.Now()
	alive := Probe(addr, 2*time.Second)
	elapsed := time.Since(start)

	require.True(t, alive, "an accepting listener counts as alive even if its handler would hang")
	require.Less(t, elapsed, time.Second, "Probe must not wait for any response")
}
