package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// mcpKindRecorder is a daemon stand-in that answers every `mcp` op OK and remembers each op's kind.
type mcpKindRecorder struct {
	mu    sync.Mutex
	kinds []string
}

func (r *mcpKindRecorder) handle(_ context.Context, req ipc.Request) ipc.Response {
	var m daemon.MCPOpRequest
	_ = json.Unmarshal(req.Raw, &m)
	r.mu.Lock()
	r.kinds = append(r.kinds, m.Kind)
	r.mu.Unlock()
	return ipc.Response{OK: true, Data: json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`)}
}

func (r *mcpKindRecorder) saw(kind string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range r.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// serve binds a listener at root's address, as a spawned daemon does, and returns its stop.
func (r *mcpKindRecorder) serve(t *testing.T, root string) func() {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	srv, err := ipc.NewServer(addr, logging.Nop(), nil, 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, r.handle)
	}()
	return func() {
		cancel()
		_ = srv.Close()
		<-done
	}
}

// TestCmdMCPHandshakeReachesADaemonThatStartsLate: the host starts the MCP server beside the
// session's first SessionStart, usually before the project's daemon is listening. The handshake
// notice used to be sent once: its connect failed, the lazy spawn started a daemon, and the notice
// was dropped (nothing is spooled), so mcp.server_registered read initialize-not-received in a
// session whose server was connected and serving (Phase 4 install D4, C45-1). The notice must
// reach the daemon the spawn brings up, without a tool call to carry it.
func TestCmdMCPHandshakeReachesADaemonThatStartsLate(t *testing.T) {
	root := mcpCmdRoot(t)
	rec := &mcpKindRecorder{}
	var stop func()
	var stopMu sync.Mutex
	t.Cleanup(func() {
		stopMu.Lock()
		defer stopMu.Unlock()
		if stop != nil {
			stop()
		}
	})

	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	client := ipc.NewClientWithOptions(addr, nopSpool{}, logging.Nop(), obs.New(testClock()),
		ipc.ClientOptions{
			ProjectRoot: root,
			Self:        "w13-not-a-real-executable",
			Clock:       testClock(),
			Spawn: func(string, string) error {
				stopMu.Lock()
				defer stopMu.Unlock()
				stop = rec.serve(t, root)
				return nil
			},
		})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := buildMCPProxy(ctx, root, config.Defaults(), client, logging.Nop())
	require.NoError(t, err)

	var out bytes.Buffer
	stdin := mcpCmdLines(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",`+
			`"clientInfo":{"name":"w13","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	)
	if serveErr := srv.Serve(ctx, strings.NewReader(stdin), &out); serveErr != nil {
		require.ErrorIs(t, serveErr, io.EOF)
	}
	require.Len(t, mcpCmdDecodeStream(t, out.String()), 1, "the handshake is answered at once")

	require.Eventually(t, func() bool { return rec.saw(daemon.MCPKindInitialized) },
		time.Duration(mcpRetryAttempts+1)*mcpRetryDelay+mcpCallDeadline, mcpRetryDelay/3,
		"the handshake notice must reach the daemon the first attempt's spawn started")
}
