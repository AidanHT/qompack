package cli

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
)

// TestStatusProbe_OutlastsAListenerThatIsReArming is the wave 19b review's finding. fetchDaemonStatus
// resends a fast failure only when daemonListening saw a live listener first, so a probe that gives
// up on a live daemon makes status say none is listening ("asked one to start"), the same false,
// millisecond reason C4.5 is about. A go-winio listener with no Accept pending, which is where it is
// between instances, still holds the pipe name, so the probe meets ERROR_PIPE_BUSY until the next
// Accept: the probe needs the same budget as the command client's own connect, not a smaller one.
//
// The row never races a listener against a budget (D61); no outcome below depends on scheduling.
// First, on a real listener that never calls Accept, a dial is waited on until its budget runs out
// (go-winio's ErrTimeout), not refused the way an address with no listener is: the busy window is
// real, and only the probe's budget decides whether it is outlasted. Second, a dial with no budget
// of its own, made while that listener is busy, connects once the listener calls Accept: a busy dial
// is waited on, not abandoned. Whether the dial meets the busy pipe first or arrives after Accept
// is up to the scheduler, and either way it must connect, so this half does not guarantee that
// go-winio's busy poll (tryDialPipe retries ERROR_PIPE_BUSY every 10 ms) runs on every pass. Third,
// through statusProbeDial, daemonListening dials the project's own address once, with a budget that
// outlasts a re-arm at rearm, twice self-test's 50 ms liveness bound that the probe used to dial
// with. That half pins the budget relation alone; TestStatusProbe_HasTheCommandConnectBudget pins
// the budget's value. Windows only: a Unix socket accepts a connect into its backlog without an
// Accept, so there is no such window to show there.
//
// Not parallel: it swaps statusProbeDial.
func TestStatusProbe_OutlastsAListenerThatIsReArming(t *testing.T) {
	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	ln, err := winio.ListenPipe(addr.Path, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	budget := selfTestProbeTimeout
	conn, err := winio.DialPipe(addr.Path, &budget)
	if conn != nil {
		_ = conn.Close()
	}
	require.ErrorIs(t, err, winio.ErrTimeout,
		"a listener with no Accept pending holds its name: a dial waits it out as busy")
	require.False(t, ipc.Probe(addr, selfTestProbeTimeout),
		"a probe whose budget ends inside the busy window reads a live listener as absent")

	nowhere, err := ipc.Resolve(t.TempDir())
	require.NoError(t, err)
	_, err = winio.DialPipe(nowhere.Path, &budget)
	require.ErrorIs(t, err, os.ErrNotExist, "with no listener the dial is refused, not waited on")
	require.False(t, errors.Is(err, winio.ErrTimeout))

	// The busy dial is waited on until Accept, not abandoned. Each wait ends on the other side's
	// outcome or on the row's own failure (cancel, then the listener's Close), never on a timer.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	dialed := make(chan error, 1)
	go func() {
		c, derr := winio.DialPipeContext(ctx, addr.Path)
		if c != nil {
			_ = c.Close()
		}
		dialed <- derr
	}()
	accepted := make(chan error, 1)
	go func() {
		c, aerr := ln.Accept()
		if c != nil {
			_ = c.Close()
		}
		accepted <- aerr
	}()
	for range 2 {
		select {
		case derr := <-dialed:
			require.NoError(t, derr, "a dial with no budget of its own must connect once Accept runs")
		case aerr := <-accepted:
			require.NoError(t, aerr, "the listener must accept the waiting dial")
		}
	}

	rearm := 2 * selfTestProbeTimeout
	accepts := func(d time.Duration) bool { return d >= rearm } // busy until rearm, then accepted
	require.False(t, accepts(selfTestProbeTimeout), "the old 50 ms probe gives up before the re-arm")

	var asked []ipc.Addr
	useStatusProbe(t, func(a ipc.Addr, d time.Duration) bool {
		asked = append(asked, a)
		return accepts(d)
	})
	require.True(t, daemonListening(root)(),
		"a live listener that re-arms within %s must read as listening (probe budget %s)",
		rearm, statusProbeTimeout)
	require.Equal(t, []ipc.Addr{addr}, asked, "the probe dials the project's own address, once")
	require.Less(t, rearm, statusProbeTimeout, "the row must re-arm inside the probe's budget")
}
