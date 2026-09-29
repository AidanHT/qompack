package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
)

// runtime.mode "off" through `qompack mcp` (V6 close-out, live report C4.7 F1/F2, retrieval D9): a
// recall answered "the qompack daemon returned an empty result", which does not say Qompack is
// switched off, and `qompack mcp` wrote .gitignore, an empty day log and state/mcp.json into the
// mode-off project.

// mcpModeOffConfig is a project configuration that switches Qompack off and nothing else.
const mcpModeOffConfig = `{"runtime":{"mode":"off"}}`

// projectTree lists every file and directory under root, slash-separated and sorted.
func projectTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		if rel != "." {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	}))
	sort.Strings(out)
	return out
}

// TestCmdMCPModeOffWritesNothingAndSaysSo runs `qompack mcp` in a project whose configuration sets
// runtime.mode to "off". The server still lists the same eight tools, every call is a tool error
// that says Qompack is switched off by runtime.mode, and the project tree is exactly what it was.
func TestCmdMCPModeOffWritesNothingAndSaysSo(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root, mcpModeOffConfig)
	before := projectTree(t, root)

	stdin := mcpCmdLines(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",`+
			`"clientInfo":{"name":"modeoff","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"recall","arguments":{"query":"pool timeout"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"already_tried",`+
			`"arguments":{"target":"src/pool.go","approach":"widen pool timeout"}}}`,
	)
	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), []string{"qompack", "mcp"}, mcpCmdEnv(t, root, stdin), &out, &errw)
	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())

	responses := mcpCmdDecodeStream(t, out.String())
	require.Len(t, responses, 4, "four id-carrying requests, four responses:\n%s", out.String())

	list := mcpCmdCallResult(t, responses, "2")
	var tools []struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(list.Tools, &tools))
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	require.Equal(t, mcp.ToolNames(), names, "a mode-off server lists the same tools")

	for _, id := range []string{"3", "4"} {
		call := mcpCmdCallResult(t, responses, id)
		require.True(t, call.IsError, "id=%s: a mode-off call is a tool error the model reads", id)
		require.Equal(t, mcp.ModeOffText, mcpCmdText(call), "id=%s: the stable mode-off answer, verbatim", id)
	}
	require.Equal(t, before, projectTree(t, root), "`qompack mcp` must write nothing into a mode-off project")
}

// mcpModeOffClient is an ipc.Client that answers the way the real client does in a session whose
// mode is off: OK, Mode off, and no payload — no dial, no daemon.
type mcpModeOffClient struct{}

func (mcpModeOffClient) Send(context.Context, ipc.Request, time.Duration) (ipc.Response, error) {
	return ipc.Response{OK: true, Mode: contract.ModeOff}, nil
}

func (mcpModeOffClient) Close() error { return nil }

// TestForwardMCPCallModeOffSaysSo pins the forwarding handler's half: a client that reports mode
// off answers the mode-off text, not "the qompack daemon returned an empty result". This is the
// path `qompack recall` and the other retrieval commands take too.
func TestForwardMCPCallModeOffSaysSo(t *testing.T) {
	resp, err := forwardMCPCall(mcpModeOffClient{}, logging.Nop())(context.Background(), mcp.Request{
		Name: mcp.ToolRecall, Args: json.RawMessage(`{"query":"pool timeout"}`),
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Len(t, resp.Content, 1)
	require.Equal(t, mcp.ModeOffText, resp.Content[0].Text)
}
