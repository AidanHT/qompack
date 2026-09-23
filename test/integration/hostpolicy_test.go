package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hostperm"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/store"
)

// hermeticHostPolicy is a host policy with no machine behind it: an empty home, an empty
// environment and no managed sources. Left nil, internal/mcp reads the real machine's host
// settings, so a rig's results would depend on the Read rules of whichever machine runs it (C1.9
// review finding 3).
func hermeticHostPolicy(t *testing.T, root string) *hostperm.Policy {
	t.Helper()
	return hostperm.New(hostperm.Options{
		ProjectRoot: root, Home: t.TempDir(),
		Getenv:  func(string) string { return "" },
		Managed: &hostperm.ManagedSources{},
	})
}

// TestRigs_TheMachinesHostRulesDoNotReachThem is C1.9 review finding 3 for this package's two MCP
// rigs. A ToolDeps with no HostPolicy makes internal/mcp read the real machine's host settings, so
// a rig's expansions — the growth walk's ephemeral records, the promoter's counts — depended on the
// Read rules of whichever machine ran it. The deny-all rule below stands in for such a machine.
func TestRigs_TheMachinesHostRulesDoNotReachThem(t *testing.T) {
	cfgDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "settings.json"),
		[]byte(`{"permissions":{"deny":["Read"]}}`), 0o600))
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)

	for name, open := range map[string]func(*testing.T) (store.Store, mcp.Server){
		"x9v4": func(t *testing.T) (store.Store, mcp.Server) { r := x9v4Open(t); return r.Store, r.Server },
		"x8v5": func(t *testing.T) (store.Store, mcp.Server) { r := x8v5Open(t); return r.Store, r.Server },
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st, srv := open(t)
			const id, rel, body = "toolu_hostperm_hermetic", "src/hermetic.ts", "export const hermetic = 1;\n"
			res, err := st.PutBytes(ctx, []byte(body), store.PutOptions{Tool: "Read", Path: rel})
			require.NoError(t, err)
			require.NoError(t, st.RecordToolUse(ctx, store.ToolUseRecord{
				ID: id, Session: core.SessionID("sess-it-hostperm"), Tool: "Read", Path: rel,
				Root: res.Root.Hash, Bytes: int64(len(body)), Status: store.StatusOK,
			}))
			args, err := json.Marshal(map[string]any{"tool_use_id": id, "full": true})
			require.NoError(t, err)
			for _, tool := range srv.Tools() {
				if tool.Name != mcp.ToolExpand {
					continue
				}
				resp, err := tool.Handler(ctx, mcp.Request{Session: "sess-it-hostperm", Name: mcp.ToolExpand, Args: args})
				require.NoError(t, err)
				require.Len(t, resp.Content, 1)
				var got struct {
					Found bool `json:"found"`
				}
				require.NoError(t, json.Unmarshal([]byte(resp.Content[0].Text), &got))
				require.True(t, got.Found, "the machine's own Read rules reached the rig: %s", resp.Content[0].Text)
				return
			}
			require.FailNow(t, "the registered tool set must include expand")
		})
	}
}
