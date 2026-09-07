package canary

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/mcp"
)

// mcpCanaryProtocolVersion is the newest protocol version internal/mcp negotiates. It is written
// out rather than read from the package so this canary states the version it drove the launcher
// with, the way a host would: a server that silently stopped supporting it should fail here.
const mcpCanaryProtocolVersion = "2025-06-18"

// mcpCanaryReplyBound is how long one JSON-RPC round trip may take. A cold process start plus a
// tools/list carrying eight schemas is well inside this; anything past it is a hang.
const mcpCanaryReplyBound = 30 * time.Second

// TestCanary_MCPLauncherDiscoversTools is M0-G4's "discovers actual tools" half.
//
// It launches the built binary exactly as plugin/.mcp.json tells a host to — `qompack mcp` over
// stdio — negotiates a supported protocol version, and asks for the tool list, asserting the eight
// §8.7 tools come back. It sends no tools/call: a call would forward to the daemon and lazily spawn
// one, and discovery is what packaging validation is about.
//
// Scope, in the record as well as here: this is the launcher THIS BUILD produces, driven by this
// test rather than by an installed host. It does not establish that a host which loaded the plugin
// from an installed directory finds and registers the same server (B01).
func TestCanary_MCPLauncherDiscoversTools(t *testing.T) {
	bin := buildQompack(t)
	_, env := tempProject(t)
	tgt := hostTarget(t)

	ctx, cancel := context.WithTimeout(context.Background(), mcpCanaryReplyBound*3)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "mcp") //nolint:gosec // G204: a binary this package built
	// A neutral working directory that is not the repository, so a root resolution that fell back
	// to the process cwd could not reach this checkout.
	cmd.Dir = filepath.Dir(bin)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	var stderr writerBuf
	cmd.Stderr = &stderr

	require.NoError(t, cmd.Start(), "launching %s mcp", bin)

	// stdout is drained continuously rather than read on demand: a child that fills a pipe buffer
	// while the parent is writing to its stdin deadlocks both.
	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		// A tools/list result carrying eight schemas exceeds bufio's 64 KiB default line limit.
		sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()

	send := func(id int, method string, params any) {
		t.Helper()
		payload := map[string]any{"jsonrpc": "2.0", "method": method}
		if id > 0 {
			payload["id"] = id
		}
		if params != nil {
			payload["params"] = params
		}
		b, mErr := json.Marshal(payload)
		require.NoError(t, mErr)
		_, wErr := io.WriteString(stdin, string(b)+"\n")
		require.NoError(t, wErr, "writing %s; stderr:\n%s", method, stderr.String())
	}

	await := func(id int) json.RawMessage {
		t.Helper()
		timer := time.NewTimer(mcpCanaryReplyBound)
		defer timer.Stop()
		select {
		case line, ok := <-lines:
			require.True(t, ok, "the launcher closed stdout before answering id=%d; stderr:\n%s", id, stderr.String())
			var w struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      *int            `json:"id"`
				Result  json.RawMessage `json:"result"`
				Error   json.RawMessage `json:"error"`
			}
			require.NoError(t, json.Unmarshal([]byte(line), &w),
				"every stdout line must be JSON-RPC; got: %q", line)
			require.Equal(t, "2.0", w.JSONRPC)
			require.Empty(t, string(w.Error), "id=%d answered with a protocol error: %s", id, w.Error)
			require.NotNil(t, w.ID, "a response must echo its request id; got: %q", line)
			require.Equal(t, id, *w.ID)
			return w.Result
		case <-timer.C:
			t.Fatalf("no response to id=%d within %s; stderr:\n%s", id, mcpCanaryReplyBound, stderr.String())
			return nil
		}
	}

	// The host's own order: initialize, then the initialized notification, then discovery.
	send(1, "initialize", map[string]any{
		"protocolVersion": mcpCanaryProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "qompack-canary", "version": "0"},
	})
	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	require.NoError(t, json.Unmarshal(await(1), &initResult))
	require.Equal(t, mcpCanaryProtocolVersion, initResult.ProtocolVersion,
		"the launcher must negotiate the version the client offered")

	send(0, "notifications/initialized", nil)

	send(2, "tools/list", map[string]any{})
	var listResult struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(await(2), &listResult))

	got := make([]string, 0, len(listResult.Tools))
	for _, tool := range listResult.Tools {
		got = append(got, tool.Name)
	}
	require.ElementsMatch(t, mcp.ToolNames(), got,
		"the launcher must advertise exactly the eight §8.7 tools the daemon registers")

	// An MCP session ends when the host closes the pipe. The stdout drain must finish BEFORE Wait —
	// Wait closes the pipe once the child exits, so calling it while a read is outstanding is a
	// documented use-after-close.
	require.NoError(t, stdin.Close(), "closing the launcher's stdin")
	var trailing []string
	for line := range lines {
		trailing = append(trailing, line)
	}
	require.NoError(t, cmd.Wait(), "the launcher must exit 0 on EOF; stderr:\n%s", stderr.String())
	require.Empty(t, trailing, "the launcher wrote unsolicited lines after the last request: %v", trailing)
	require.Empty(t, stderr.String(), "nothing may reach stderr on a clean session")

	rec := Record{
		Name:       "mcp_launcher_discovers_tools",
		Capability: contract.CapObservation,
		Scope:      ScopeRepository,
		Target:     tgt,
		Outcome:    OutcomeVerified,
		Reason: fmt.Sprintf("repository-level: `qompack mcp` (this build's own binary, launched the "+
			"way plugin/.mcp.json instructs) negotiated protocol %s as %s %s and advertised all %d "+
			"§8.7 tools over stdio, then exited 0 on EOF. Installed-host discovery is unverified: "+
			"this test launched the server itself rather than an installed host loading the plugin "+
			"and finding it (B01).",
			initResult.ProtocolVersion, initResult.ServerInfo.Name, initResult.ServerInfo.Version,
			len(mcp.ToolNames())),
	}
	writeRecord(t, rec)
}

// writerBuf is a locked buffer for the child's stderr.
//
// The lock is not defensive noise. Handing exec.Cmd a Stderr that is not an *os.File makes it copy
// the pipe on its own goroutine, and every failure message in this test reads that buffer WHILE the
// child is still running. A bare bytes.Buffer there is a data race the race detector would find, and
// — worse — one that only appears on the paths that are already failing.
type writerBuf struct {
	mu sync.Mutex
	b  []byte
}

func (w *writerBuf) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.b = append(w.b, p...)
	return len(p), nil
}

func (w *writerBuf) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.b)
}
