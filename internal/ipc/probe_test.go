package ipc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/logging"
)

// The two dial budgets this file hands Probe, and the one bound it asserts on Probe's own return.
//
// probeReturnBound is DERIVED from probeHangingHandlerTimeout rather than hand-picked: the whole
// assertion in TestProbe_DoesNotWaitForAResponse is that Probe returns in well under the budget it
// was given, so a bound that could drift larger than that budget would assert nothing at all
// (V2-MERGE-25 ②). Expressing it as a fraction makes the two impossible to order wrongly.
//
// probeAbsentTimeout is separate and short on purpose: with nothing bound at the address the dial
// fails immediately on both platforms, so this budget is only ever spent when something is
// genuinely wrong, and it bounds a negative result rather than waiting for a positive one.
const (
	probeAbsentTimeout         = 50 * time.Millisecond
	probeHangingHandlerTimeout = 2 * time.Second
	probeReturnBound           = probeHangingHandlerTimeout / 2
)

// TestProbe_FalseWhenNothingListens pins the negative case: no server bound at the address.
func TestProbe_FalseWhenNothingListens(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addr, err := Resolve(root)
	require.NoError(t, err)

	require.False(t, Probe(addr, probeAbsentTimeout))
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
	alive := Probe(addr, probeHangingHandlerTimeout)
	elapsed := time.Since(start)

	require.True(t, alive, "an accepting listener counts as alive even if its handler would hang")
	require.Less(t, elapsed, probeReturnBound,
		"Probe must not wait for any response — it returned in %s of its own %s dial budget", elapsed, probeHangingHandlerTimeout)
}
