package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
)

// shutdownReplyBound bounds each wait in the test below. It is headroom over the daemon's own
// bounds (Stop's cleanup is capped by stopCleanupBound), not a latency expectation.
const shutdownReplyBound = stopCleanupBound

// TestAdminShutdownReplyReachesTheCallerOverTheTransport is the V6 close-out linux lane's N1 at the
// daemon's own surface: admin.shutdown sent over the real transport must be answered OK. The
// handler starts Stop before its reply is written, and Stop's cancel reaches ipc's Close through
// Serve's context.AfterFunc, which used to close the very connection the reply was still owed on
// (one Linux -race run in 100 got OK:false in 0.15 s). internal/ipc's
// TestServerCloseLetsAnInFlightReplyFinish injects that ordering deterministically; this row runs
// the shipped route end to end, and is meant to be run many times (-race -count=50).
func TestAdminShutdownReplyReachesTheCallerOverTheTransport(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QOMPACK_IPC_ADDR", uniqueTestAddr(t))

	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop(), Clock: core.SystemClock()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 2*shutdownReplyBound)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	c := ipc.NewClientWithOptions(addr, nil, logging.Nop(), nil, ipc.ClientOptions{
		State:           ipc.State{Mode: contract.ModeFull, DaemonEnabled: true},
		ConnectDeadline: shutdownReplyBound,
		AckDeadline:     shutdownReplyBound,
	})
	defer func() { _ = c.Close() }()

	// A nil SpoolWriter: a Send that cannot reach the daemon yet is dropped, never spooled.
	require.Eventually(t, func() bool {
		resp, sendErr := c.Send(context.Background(), ipc.Request{
			Op: ipc.OpAdminPing, TS: core.NowMilli(core.SystemClock()), Reply: true,
		}, shutdownReplyBound)
		return sendErr == nil && resp.OK
	}, shutdownReplyBound, 20*time.Millisecond, "the daemon never answered admin.ping")

	resp, err := c.Send(context.Background(), ipc.Request{
		Op: ipc.OpAdminShutdown, TS: core.NowMilli(core.SystemClock()), Reply: true,
	}, shutdownReplyBound)
	require.NoError(t, err, "admin.shutdown's reply was lost to the shutdown it announced")
	require.True(t, resp.OK, "admin.shutdown's reply was lost to the shutdown it announced: %q", resp.Err)

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(shutdownReplyBound):
		t.Fatal("admin.shutdown did not stop the running daemon")
	}
	select {
	case <-dd.stopDone:
	case <-time.After(shutdownReplyBound):
		t.Fatal("Stop's cleanup did not finish")
	}
}
