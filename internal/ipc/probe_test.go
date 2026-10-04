package ipc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/logging"
)

// The dial budgets this file hands Probe, and the hang guard on the one Probe that must return.
//
// probeAbsentTimeout is short on purpose: with nothing bound at the address the dial fails
// immediately on both platforms, so this budget is only ever spent when something is genuinely
// wrong, and it bounds a negative result rather than waiting for a positive one.
//
// probeSilentPeerTimeout is what TestProbe_DoesNotWaitForAResponse hands Probe against a peer that
// accepts and never answers. It is an hour so that a Probe that waited for any response at all
// would wait for the hour, and probeHangGuard — a minute, against a local dial that takes
// microseconds — is the only clock that row reads. It used to hand Probe 2 s and assert the call
// took under 1 s, measured after Probe returned, so a host that descheduled the test for a second
// failed a Probe that had waited for nothing (and a simulated 1.1 s stall inside Probe did).
const (
	probeAbsentTimeout     = 50 * time.Millisecond
	probeSilentPeerTimeout = time.Hour
	probeHangGuard         = time.Minute
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
// because it never gets far enough to invoke the handler at all.
//
// The verdict reads no clock. Probe's own budget is probeSilentPeerTimeout, an hour, so a Probe
// that waited for any response would wait for the hour, and the only timer here is
// probeHangGuard. Whether the server ever sees the connection is not judged: on Windows go-winio
// discards a pipe client that connects and disconnects before its ConnectNamedPipe completes, so
// Accept may never return for a Probe that did everything right.
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

	returned := make(chan bool, 1)
	go func() { returned <- Probe(addr, probeSilentPeerTimeout) }()

	guard := time.NewTimer(probeHangGuard)
	defer guard.Stop()
	select {
	case alive := <-returned:
		require.True(t, alive, "an accepting listener counts as alive even if its handler would hang")
	case <-guard.C:
		t.Fatalf("Probe had not returned after %s against a server that never answers: it is waiting "+
			"for a response, which Ruling #22 says it never does", probeHangGuard)
	}
}
