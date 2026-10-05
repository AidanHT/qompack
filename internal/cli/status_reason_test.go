package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// TestStatusSource_NoDaemonNamesTheReason is the Phase 4 live lane's F-UAT03-3 / F-C49-1: with no
// daemon listening, `qompack status` printed "daemon: status refused: " with nothing after the
// colon. The client answers an undelivered request with OK false and no error text, and the status
// source quoted that empty text as the daemon's refusal. Nothing refused it: nothing received it.
func TestStatusSource_NoDaemonNamesTheReason(t *testing.T) {
	root := mcpCmdRoot(t)
	client := mcpCmdOfflineClient(t, root)
	t.Cleanup(func() { _ = client.Close() })

	_, _, err := fetchDaemonStatus(context.Background(), commandClient{Client: client, daemonEnabled: true}, daemonListening(root))
	require.Error(t, err)
	msg := err.Error()
	require.False(t, strings.HasSuffix(strings.TrimSpace(msg), ":"), "the reason must not be empty: %q", msg)
	require.NotContains(t, msg, "refused", "no daemon received the request, so none refused it: %q", msg)
	require.Contains(t, msg, "no daemon", "the reason must say that no daemon answered: %q", msg)
}

// TestStatusSource_ASilentDaemonIsNotReportedAbsent is the w13-diag review's finding: the client
// answers OK false with no error text both when nothing listens and when a daemon accepted the
// request but its reply never came (a read deadline, a broken connection). Status must not tell the
// user no daemon is listening, and that one was asked to start, when one is listening and silent.
// The fixture's daemon accepts the request and drops the connection: its answer cannot be encoded.
// The probe that tells it apart from an absent daemon is hang-guarded (useHangGuardedStatusDials),
// so a connect slower than the 250 ms probe budget does not read it as absent (D61(c)).
//
// Not parallel: it swaps newCommandIPCClient and statusProbeDial.
func TestStatusSource_ASilentDaemonIsNotReportedAbsent(t *testing.T) {
	useHangGuardedStatusDials(t)
	root := mcpCmdRoot(t)
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	srv, err := ipc.NewServer(addr, logging.Nop(), obs.New(testClock()), 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(ctx, func(context.Context, ipc.Request) ipc.Response {
			return ipc.Response{OK: true, Data: json.RawMessage(`{`)} // unencodable: no reply is written
		})
	}()
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
		<-served
	})
	client := mcpCmdOfflineClient(t, root)
	t.Cleanup(func() { _ = client.Close() })

	_, _, err = fetchDaemonStatus(context.Background(), commandClient{Client: client, daemonEnabled: true}, daemonListening(root))
	require.Error(t, err)
	msg := err.Error()
	require.NotContains(t, msg, "none is listening", "a daemon is listening: %q", msg)
	require.Contains(t, msg, "listening", "the reason must say a daemon is listening: %q", msg)
	require.Contains(t, msg, "did not answer", "the reason must say its answer never came: %q", msg)
}
