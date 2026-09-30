package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
)

// TestRun_LogsATakenOverLockAndASpoolReplay is the Phase 4 live lane's F-C49-1 day-log half: after a
// daemon was terminated mid-session, the next daemon took over its lock and replayed the deliveries
// hooks had spooled meanwhile, and the day log said nothing about either. A reader of the log could
// not tell that a daemon had died, nor that the captures of the outage came from a replay. Each now
// leaves one Info line at the start that did it.
func TestRun_LogsATakenOverLockAndASpoolReplay(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QOMPACK_IPC_ADDR", uniqueTestAddr(t))
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)

	dead := exitedPID(t)
	writeCraftedLock(t, root, addr, dead, time.Now()) // the terminated daemon's lock, heartbeat fresh
	writeClientSpoolLine(t, root, "client-88888.ndjson", spooledObserveTool(root, "sess-outage"))

	log := newRecordingLogger()
	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: log, Clock: core.SystemClock()})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), drainDeadlockGuard)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	c := ipc.NewClientWithOptions(addr, nil, logging.Nop(), nil, ipc.ClientOptions{
		State:           ipc.State{Mode: contract.ModeFull, DaemonEnabled: true},
		ConnectDeadline: redrainDialBound,
		AckDeadline:     redrainDialBound,
	})
	defer func() { _ = c.Close() }()
	require.Eventually(t, func() bool {
		resp, sendErr := c.Send(context.Background(), ipc.Request{
			Op: ipc.OpAdminPing, TS: core.NowMilli(core.SystemClock()), Reply: true,
		}, redrainDialBound)
		return sendErr == nil && resp.OK
	}, drainDeadlockGuard, 50*time.Millisecond, "the daemon never finished its startup")
	cancel()
	select {
	case <-errCh:
	case <-time.After(drainDeadlockGuard):
		t.Fatal("Run did not shut down after cancellation")
	}

	var tookOver, replayed bool
	for _, e := range log.entries(logInfo) {
		switch {
		case strings.Contains(e.Msg, "ended without releasing its lock"):
			tookOver = true
			require.Contains(t, e.KV, "pid")
			require.Contains(t, e.KV, dead, "the line names the pid of the daemon that ended")
		case strings.Contains(e.Msg, "replayed spooled deliveries"):
			replayed = true
			require.Contains(t, e.KV, "deliveries")
		}
	}
	require.True(t, tookOver, "the day log must say a daemon ended without releasing its lock: %v", log.msgs(logInfo))
	require.True(t, replayed, "the day log must say the start replayed the spool: %v", log.msgs(logInfo))
}
