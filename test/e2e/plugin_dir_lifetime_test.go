package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// TestE2E_DaemonLeavesThePluginDirectoryRemovable is C1.17's regression row. The resident daemon
// outlives the session that started it, and on Windows a running executable — and the directory
// holding it — cannot be deleted. In the packaging lane's live session 2 the daemon, started from
// the plugin's own bin/qompack.exe, kept the host's --plugin-dir extraction from being removed
// (plugin.json and .mcp.json deleted, bin/ and the rest left behind), and the same lock breaks a
// plugin update or uninstall.
//
// Here the binary is installed into a plugin directory laid out as the host lays one out, a real
// session-start hook starts the daemon from it exactly as Claude Code does (CLAUDE_PLUGIN_ROOT set,
// the hook running in the plugin's bin directory), and the whole plugin directory is then removed
// while the daemon is still serving. That must succeed on every platform, and the daemon must keep
// serving afterwards. On Windows it runs from a verified copy under the user's .qompack/bin
// (internal/daemon spawn_stage.go), which is asserted too; elsewhere the kernel already allows it.
func TestE2E_DaemonLeavesThePluginDirectoryRemovable(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	pluginRoot, pluginBin := installPluginLayout(t, bin)

	env := e2eEnv(p)
	env["CLAUDE_PLUGIN_ROOT"] = pluginRoot
	stdout, stderr, code := Run(t, pluginBin, []string{"session-start"}, sessionStartPayload(t, p.Root), env)
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	requireParsesAsOutput(t, stdout)
	e2eWaitDaemonUp(t, p.Root)

	requirePluginDirRemovableWhileServing(t, p, pluginRoot, pluginBin)
}

// TestE2E_MCPLazySpawnLeavesThePluginDirectoryRemovable is the same row for the other road a daemon
// is started by: `qompack mcp`, which the host launches from .mcp.json's
// ${CLAUDE_PLUGIN_ROOT}/bin/qompack and which spawns the daemon lazily when nothing is listening
// (internal/cli cmd_mcp.go newMCPClient). Nothing guarantees that process carries CLAUDE_PLUGIN_ROOT
// in its environment — the manifest substitutes the placeholder into the command, not into an
// environment variable — so the variable is unset here, and the plugin is recognised by its layout
// alone (bin/qompack[.exe] with .claude-plugin/plugin.json beside bin/). The MCP server then exits,
// as it does when its session ends, and the plugin directory must be removable while the daemon it
// started keeps serving.
func TestE2E_MCPLazySpawnLeavesThePluginDirectoryRemovable(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	pluginRoot, pluginBin := installPluginLayout(t, bin)

	env := e2eEnv(p)
	env["CLAUDE_PLUGIN_ROOT"] = "" // the last assignment wins: unset for the child, whatever the shell exported
	child := mcpE2EStartWithEnv(t, pluginBin, env)
	child.send(t, mcpE2ERequest(t, 1, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"clientInfo":      map[string]string{"name": "qompack-e2e", "version": "1.0"},
	}))
	child.await(t, 1)
	child.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	// A forwarded tool call waits out the lazy spawn through the proxy's own retry loop, so once it
	// is answered the daemon the MCP process started is serving.
	child.send(t, mcpE2ERequest(t, 2, "tools/call", map[string]any{
		"name": "dropped", "arguments": map[string]any{},
	}))
	child.await(t, 2)
	e2eWaitDaemonUp(t, p.Root)
	child.finish(t)

	requirePluginDirRemovableWhileServing(t, p, pluginRoot, pluginBin)
}

// installPluginLayout copies bin into a fresh plugin directory laid out as the host lays one out —
// bin/<binary> and .claude-plugin/plugin.json — and returns the directory and the installed binary.
func installPluginLayout(t *testing.T, bin string) (pluginRoot, pluginBin string) {
	t.Helper()
	pluginRoot = filepath.Join(t.TempDir(), "qompack-plugin")
	pluginBin = filepath.Join(pluginRoot, "bin", filepath.Base(bin))
	require.NoError(t, os.MkdirAll(filepath.Dir(pluginBin), 0o700))
	copyExecutable(t, bin, pluginBin)
	require.NoError(t, os.MkdirAll(filepath.Join(pluginRoot, ".claude-plugin"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), []byte(`{"name":"qompack"}`), 0o600))
	return pluginRoot, pluginBin
}

// requirePluginDirRemovableWhileServing removes the whole plugin directory while p's daemon is
// running, and asserts that succeeded, that on Windows the daemon runs from its staged copy under
// the user's .qompack/bin, and that the daemon still answers a real round trip afterwards.
func requirePluginDirRemovableWhileServing(t *testing.T, p *testutil.Project, pluginRoot, pluginBin string) {
	t.Helper()
	staged := filepath.Join(paths.Global(p.Home()), "bin", sha256Hex(t, pluginBin), filepath.Base(pluginBin))

	require.NoError(t, os.RemoveAll(pluginRoot),
		"the plugin directory must be removable while the daemon it started is running")
	require.NoDirExists(t, pluginRoot)
	if runtime.GOOS == "windows" {
		require.FileExists(t, staged, "on Windows the daemon runs from a staged copy under the user's .qompack/bin")
	}

	addr, err := ipc.Resolve(p.Root)
	require.NoError(t, err)
	require.True(t, ipc.Probe(addr, e2eProbeTimeout), "the daemon keeps serving after its plugin directory is gone")
	snap := e2eStatus(t, p.Root)
	require.NotEmpty(t, snap.Mode, "and answers a real round trip")
}

// copyExecutable copies src to dst with the executable bit set.
func copyExecutable(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	require.NoError(t, err)
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	require.NoError(t, err)
	_, err = io.Copy(out, in)
	require.NoError(t, err)
	require.NoError(t, out.Close())
}

// sha256Hex is the lower-case hex SHA-256 of the file at p.
func sha256Hex(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
