package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/hostperm"
	"github.com/qompack/qompack/internal/mcp"
)

// mcpOpHostPolicy is a host policy with no machine behind it: an empty home, an empty environment
// and no managed sources. A ToolDeps left without one makes internal/mcp build the production
// default, which reads the real machine's ~/.claude/settings.json (or $CLAUDE_CONFIG_DIR), the
// managed-settings directory and, on Windows, the policy registry. These tests would then pass or
// fail with the Read rules of whichever machine ran them (C1.9 review finding 3, the internal/daemon
// remainder; the e2e and integration rigs got the same treatment in 3ea6a1f).
func mcpOpHostPolicy(t *testing.T, root string) *hostperm.Policy {
	t.Helper()
	return hostperm.New(hostperm.Options{
		ProjectRoot: root, Home: t.TempDir(),
		Getenv:  func(string) string { return "" },
		Managed: &hostperm.ManagedSources{},
	})
}

// TestMCPOpFixture_TheMachinesHostRulesDoNotReachIt is that finding for this package's fixture. The
// deny-all rule below stands in for a developer or CI machine whose own Claude Code settings deny
// Read: it is placed where the default policy reads user settings, and the fixture must not see it.
// Before the fixture carried a hermetic policy, recall withheld the one hit it had just indexed
// (plans/sdd/V6-closeout/hostperm/runs/95).
//
// Not parallel: t.Setenv changes the process environment, which is exactly what is under test.
func TestMCPOpFixture_TheMachinesHostRulesDoNotReachIt(t *testing.T) {
	cfgDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "settings.json"),
		[]byte(`{"permissions":{"deny":["Read"]}}`), 0o600))
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)

	f := newMCPOpFixture(t)
	f.seedToolUse(t, "Read", "src/pool.ts", "export const poolTimeoutMs = 30_000;\n// a pool timeout here\n", 1)

	resp := f.callTool(t, mcpOpSession, mcp.ToolRecall, map[string]any{"query": "pool timeout", "k": 5})
	payload := decodeMCPOp(t, resp)
	require.False(t, payload.IsError, "recall must not be a tool error: %v", payload.Content)

	var body struct {
		Hits       []mcp.RecallHit `json:"hits"`
		Found      bool            `json:"found"`
		HostPolicy string          `json:"host_policy"`
	}
	mcpOpBody(t, payload, &body)
	require.True(t, body.Found, "the machine's own Read rules reached the daemon's MCP fixture")
	require.NotEmpty(t, body.Hits)
	require.Empty(t, body.HostPolicy, "a hermetic policy has no unreadable source to report")
}
