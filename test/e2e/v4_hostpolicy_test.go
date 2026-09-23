package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/store"
)

// TestV4Server_TheMachinesHostRulesDoNotReachARow is C1.9 review finding 3. A ToolDeps with no
// HostPolicy makes internal/mcp read the real machine's host settings — the user's
// ~/.claude/settings.json or $CLAUDE_CONFIG_DIR, the managed directory and, on Windows, the policy
// registry — so every row built on v4Server passed or failed with whatever Read rules the machine
// running it had. The deny-all rule below stands in for such a machine: it is placed where the
// default policy reads user settings, and a row must not see it.
func TestV4Server_TheMachinesHostRulesDoNotReachARow(t *testing.T) {
	cfgDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "settings.json"),
		[]byte(`{"permissions":{"deny":["Read"]}}`), 0o600))
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)

	ctx := context.Background()
	p := v4Project(t)
	st := x7v4OpenStore(t, p)
	t.Cleanup(func() { _ = st.Close() })
	const id, rel, body = "toolu_v4_hostperm_hermetic", "src/hermetic.ts", "export const hermetic = true;\n"
	res, err := st.PutBytes(ctx, []byte(body), store.PutOptions{Tool: "Read", Path: rel})
	require.NoError(t, err)
	require.NoError(t, st.RecordToolUse(ctx, store.ToolUseRecord{
		ID: id, Session: core.SessionID("sess-e2e-v4-hostperm"), Tool: "Read", Path: rel,
		Root: res.Root.Hash, Bytes: int64(len(body)), Status: store.StatusOK,
	}))

	srv := v4Server(t, mcp.ToolDeps{Store: st, Cfg: p.Cfg, ProjectRoot: p.Root, DisableWhy: true})
	var got struct {
		Found   bool   `json:"found"`
		Content string `json:"content"`
	}
	v4Call(t, srv, "sess-e2e-v4-hostperm", mcp.ToolExpand, map[string]any{"tool_use_id": id, "full": true}, &got)
	require.True(t, got.Found, "the machine's own Read rules reached an e2e row")
	require.Equal(t, body, got.Content)
}
