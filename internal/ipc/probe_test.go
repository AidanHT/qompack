package ipc

import (
	"io"
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
// alone — it never writes a request and never waits on a response. Proven against a peer that
// accepts, reads, and never answers: Probe must still return true, the peer must see the
// connection closed with not one byte written to it, and neither judgement reads a clock — the
// only timer is probeHangGuard, which a Probe that waited for a response would run into because
// its own budget is probeSilentPeerTimeout.
func TestProbe_DoesNotWaitForAResponse(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addr, err := Resolve(root)
	require.NoError(t, err)

	ln, err := listen(addr, logging.Nop(), MaxLineBytes)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	// The peer: accept one connection and read it until the prober closes it, answering nothing.
	// It reports how many bytes the prober wrote before closing.
	wrote := make(chan int, 1)
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			wrote <- -1
			return
		}
		defer func() { _ = conn.Close() }()
		n, _ := io.Copy(io.Discard, conn)
		wrote <- int(n)
	}()

	returned := make(chan bool, 1)
	go func() { returned <- Probe(addr, probeSilentPeerTimeout) }()

	guard := time.NewTimer(probeHangGuard)
	defer guard.Stop()
	select {
	case alive := <-returned:
		require.True(t, alive, "an accepting listener counts as alive even though it never answers")
	case <-guard.C:
		t.Fatalf("Probe had not returned after %s against a peer that never answers: it is waiting for "+
			"a response, which Ruling #22 says it never does", probeHangGuard)
	}

	select {
	case n := <-wrote:
		require.Zero(t, n, "Probe must close the connection without writing a request")
	case <-guard.C:
		t.Fatalf("the peer never saw Probe close its connection within %s", probeHangGuard)
	}
}
