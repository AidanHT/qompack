package ipc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// relistenIterations is how many stop-and-replace cycles TestServerCloseFreesTheEndpoint runs. The
// race it guards was hit in a few percent of cycles on a quiet Windows host (before the fix, the
// first failure came at cycles 6, 42, 90, 98 and 222 in five runs, and a reviewer's three runs failed
// twice), so the row catches a regression in most runs, not every run. A cycle takes 10 to 25 ms
// there, so the whole row runs in a few seconds.
const relistenIterations = 300

// TestServerCloseFreesTheEndpoint: once Close has returned, the address is free, so the daemon that
// replaces a stopped one listens on it. Each cycle ends the way a daemon's does when a hook's
// liveness probe (ipc.Probe, daemon.lockIsStale) has just dialled and hung up: the server has
// re-posted its accept when Close arrives.
//
// On Windows, go-winio v0.6.2 can lose that Close: the pending accept takes the close signal, its
// aborted connect comes back as a bare ERROR_OPERATION_ABORTED rather than the error it maps to
// "listener closed", and the listener goroutine carries on with its first pipe handle open while
// Close waits for it for good. closeListenerBounded used to give up after serverCloseWait and
// report success, leaving the pipe name bound for the life of the process: the next listen on it
// failed with "Access is denied", and the daemon a flush spawned in-process after a stop exited
// before it listened (V6 close-out, the 0.3.2 cover run's AFlush row).
func TestServerCloseFreesTheEndpoint(t *testing.T) {
	addr, err := Resolve(t.TempDir())
	require.NoError(t, err)
	handler := func(context.Context, Request) Response { return Response{OK: true} }

	for i := range relistenIterations {
		srv, err := NewServer(addr, logging.Nop(), obs.New(core.SystemClock()), MaxLineBytes)
		require.NoError(t, err, "cycle %d: the previous cycle's Close returned, so its endpoint must be free", i)
		serveDone := make(chan error, 1)
		go func() { serveDone <- srv.Serve(context.Background(), handler) }()

		conn, err := dial(addr, rawDialBound)
		require.NoError(t, err, "cycle %d: dial", i)
		require.NoError(t, conn.Close())

		require.NoError(t, srv.Close(), "cycle %d", i)
		select {
		case err := <-serveDone:
			require.NoError(t, err, "cycle %d: Serve after Close", i)
		case <-time.After(stressTestBound):
			t.Fatalf("cycle %d: Serve did not return after Close", i)
		}
	}
}
