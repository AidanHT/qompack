package cli

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// Wave 19 statusorder's review (C4.5, D53(a)): on Windows one command-client connect miss inside
// the hot path's 25 ms runtime.daemon.connectDeadlineMs made `qompack status` answer source none and
// say the daemon "did not answer within 10s", for a read that failed in milliseconds. The rows below
// drive that through a real daemon and the real ipc client. The miss is real too: a real client
// aimed at an address nothing listens on, swapped in through newCommandIPCClient.

// connectMissClient sends through miss, a real client whose connect cannot succeed, while misses
// stays positive, and through the real command client after that. misses is shared by every client
// the seam builds, so it counts misses across commands, not per client.
type connectMissClient struct {
	ipc.Client
	miss   ipc.Client
	misses *atomic.Int64
}

func (c connectMissClient) Send(ctx context.Context, req ipc.Request, d time.Duration) (ipc.Response, error) {
	if c.misses.Add(-1) >= 0 {
		return c.miss.Send(ctx, req, d)
	}
	return c.Client.Send(ctx, req, d)
}

func (c connectMissClient) Close() error {
	_ = c.miss.Close()
	return c.Client.Close()
}

// injectCommandConnectMisses makes the next n command-client Sends miss their connect, and returns
// the counter so a row can show the misses really happened. It also records the options every
// command client was built with.
func injectCommandConnectMisses(t *testing.T, n int64) (*atomic.Int64, *[]ipc.ClientOptions) {
	t.Helper()
	nowhere, err := ipc.Resolve(t.TempDir())
	require.NoError(t, err)
	misses := &atomic.Int64{}
	misses.Store(n)
	var built []ipc.ClientOptions
	prev := newCommandIPCClient
	newCommandIPCClient = func(addr ipc.Addr, sp ipc.SpoolWriter, log logging.Logger, m obs.Registry,
		o ipc.ClientOptions,
	) ipc.Client {
		built = append(built, o)
		real := prev(addr, sp, log, m, o)
		o.Self, o.Spawn = "", nil // the missing client never spawns a daemon
		return connectMissClient{Client: real, miss: prev(nowhere, sp, log, m, o), misses: misses}
	}
	t.Cleanup(func() { newCommandIPCClient = prev })
	return misses, &built
}

// statusConnectRead runs `qompack status --json` in root and decodes the part these rows inspect.
func statusConnectRead(t *testing.T, root string) (string, statusOrderEnvelope) {
	t.Helper()
	code, out, errw := fsckDispatchIn(t, root, "status", "--json")
	require.Equal(t, ExitOK, code, "stderr=%s", errw)
	var env statusOrderEnvelope
	require.NoError(t, json.Unmarshal([]byte(out), &env), "stdout=%s", out)
	return out, env
}

// TestCommandClient_HasItsOwnConnectBudget: a command client dials with commandConnectDeadline,
// not the hooks' runtime.daemon.connectDeadlineMs, whatever the project configures for the hooks
// (D60(e)). The hooks' budget itself is left as configured: state.bin still carries it.
//
// Not parallel: it swaps newCommandIPCClient.
func TestCommandClient_HasItsOwnConnectBudget(t *testing.T) {
	root := bootstrapProject(t)
	writeProjectConfig(t, root, `{"runtime":{"daemon":{"connectDeadlineMs":25}}}`)
	_, built := injectCommandConnectMisses(t, 0)

	cfg, _, err := LoadConfigAndReport(config.Env{ProjectRoot: root, HomeDir: t.TempDir(), Getenv: noEnv},
		logging.Nop(), obs.New(testClock()))
	require.NoError(t, err)
	require.Equal(t, 25, cfg.Runtime.Daemon.ConnectDeadlineMs, "the hooks' budget is as configured")

	client := newCommandClient(root, cfg, Env{}, logging.Nop(), obs.New(testClock()), testClock())
	t.Cleanup(func() { _ = client.Close() })
	require.Len(t, *built, 1)
	require.Equal(t, commandConnectDeadline, (*built)[0].ConnectDeadline,
		"a command client must dial with its own budget, not the hot path's connectDeadlineMs")
	require.Less(t, commandConnectDeadline, commandCallDeadline,
		"fetchDaemonStatus tells a connect miss from a call-deadline expiry by the time a send took")
}

// TestStatus_ConnectMissNamesTheConnectBudget: when every connect misses while a real daemon
// listens, status names a connect miss within commandConnectDeadline, and does not claim the
// daemon "did not answer within 10s": that deadline never ran.
//
// Not parallel: bootstrapDaemon resets the process-wide producer set, and the row swaps
// newCommandIPCClient.
func TestStatus_ConnectMissNamesTheConnectBudget(t *testing.T) {
	root := bootstrapProject(t)
	stop := bootstrapDaemon(t, root)
	defer stop()
	misses, _ := injectCommandConnectMisses(t, 1<<30)

	out, env := statusConnectRead(t, root)
	require.NotEqual(t, "daemon", env.Data.Primary.Source, "every connect missed: stdout=%s", out)
	require.Less(t, misses.Load(), int64(1<<30), "the injected miss must have been reached")
	require.NotContains(t, env.Data.Primary.Reason, statusSilentDaemonReason,
		"no call deadline expired, so status must not say the daemon did not answer within it")
	require.Contains(t, env.Data.Primary.Reason, statusConnectMissReason,
		"the reason must name the connect miss")
}

// TestStatus_TransientConnectMissStillReadsTheDaemon: with two sessions registered and a single
// connect miss on the first read, two `status --json` reads of unchanged state agree, both from the
// live daemon (D53(a)).
//
// Not parallel: bootstrapDaemon resets the process-wide producer set, and the row swaps
// newCommandIPCClient.
func TestStatus_TransientConnectMissStillReadsTheDaemon(t *testing.T) {
	root := bootstrapProject(t)
	stop := bootstrapDaemon(t, root)
	defer stop()
	for _, id := range []core.SessionID{"sess-miss-b", "sess-miss-a"} {
		startStatusOrderSession(t, root, id)
	}
	misses, _ := injectCommandConnectMisses(t, 1)

	first, env1 := statusConnectRead(t, root)
	require.LessOrEqual(t, misses.Load(), int64(0), "the injected miss must have been reached")
	second, env2 := statusConnectRead(t, root)
	require.Equal(t, "daemon", env1.Data.Primary.Source,
		"read 1 missed its first connect and must still reach the daemon: %s", env1.Data.Primary.Reason)
	require.Equal(t, "daemon", env2.Data.Primary.Source, "read 2: %s", env2.Data.Primary.Reason)
	require.Len(t, env1.Data.Snapshot.Sessions, 2)
	require.Equal(t, first, second, "two status --json reads of unchanged state disagree (D53(a))")
}

// TestStatus_CallDeadlineExpiryStillSaysSilent: a daemon that accepts the status request and never
// replies still reads as statusSilentDaemonReason, because there commandCallDeadline did expire,
// and the request is sent once: an expired call deadline is not retried. It waits out the real
// commandCallDeadline once.
func TestStatus_CallDeadlineExpiryStillSaysSilent(t *testing.T) {
	root := mcpCmdRoot(t)
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	srv, err := ipc.NewServer(addr, logging.Nop(), obs.New(testClock()), 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	var calls atomic.Int64
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(ctx, func(hctx context.Context, _ ipc.Request) ipc.Response {
			calls.Add(1)
			select { // never answers within the call deadline
			case <-release:
			case <-hctx.Done():
			}
			return ipc.Response{OK: false, Err: "released"}
		})
	}()
	t.Cleanup(func() {
		close(release)
		cancel()
		_ = srv.Close()
		<-served
	})

	cfg := config.Defaults()
	client := newCommandClient(root, cfg, Env{}, logging.Nop(), obs.New(testClock()), testClock())
	t.Cleanup(func() { _ = client.Close() })

	_, _, err = fetchDaemonStatus(context.Background(), client, daemonListening(root))
	require.Error(t, err)
	require.Equal(t, statusSilentDaemonReason, err.Error())
	require.Equal(t, int64(1), calls.Load(), "an expired call deadline must not be retried")
}
