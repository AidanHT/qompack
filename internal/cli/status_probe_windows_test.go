package cli

import (
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
)

// TestStatusProbe_OutlastsAListenerThatIsReArming is the wave 19b review's finding. fetchDaemonStatus
// resends a fast failure only when daemonListening saw a live listener first, so a probe that gives
// up on a live daemon makes status say none is listening ("asked one to start"), the same false,
// millisecond reason C4.5 is about. A go-winio listener between instances still holds the pipe name
// (its first handle stays disconnected), so the probe meets ERROR_PIPE_BUSY until the next Accept:
// the probe needs the same budget as the command client's own connect, not a smaller one.
//
// The listener here is real and calls its first Accept rearm after the probe starts. rearm is twice
// self-test's 50 ms liveness bound, the bound the probe used to dial with, and well inside
// commandConnectDeadline. Windows only: a Unix socket accepts a connect into its backlog without an
// Accept, so there is no such window to show there.
func TestStatusProbe_OutlastsAListenerThatIsReArming(t *testing.T) {
	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	ln, err := winio.ListenPipe(addr.Path, nil)
	require.NoError(t, err)
	rearm := 2 * selfTestProbeTimeout
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		time.Sleep(rearm)
		if c, aerr := ln.Accept(); aerr == nil {
			_ = c.Close()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-accepted
	})

	require.True(t, daemonListening(root)(),
		"a live listener that re-arms within %s must read as listening (probe budget %s)",
		rearm, statusProbeTimeout)
	require.Less(t, rearm, statusProbeTimeout, "the row must re-arm inside the probe's budget")
}
