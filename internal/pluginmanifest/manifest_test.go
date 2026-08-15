package pluginmanifest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/pluginmanifest"
)

const testVersion = "0.1.0"

func TestManifest_CoversAllSixHooks(t *testing.T) {
	m := pluginmanifest.Default(testVersion)

	got := make([]string, 0, len(m.Hooks.Hooks))
	for k := range m.Hooks.Hooks {
		got = append(got, k)
	}
	sort.Strings(got)

	want := []string{
		"PostToolUse", "PreCompact", "SessionEnd", "SessionStart",
		"Stop", "SubagentStop", "UserPromptSubmit",
	}
	require.Equal(t, want, got, "all six §7.3 hooks must be covered; Stop and SubagentStop are separate host events")

	timeouts := map[string]int{
		"PostToolUse": 5, "UserPromptSubmit": 5, "SessionStart": 15,
		"PreCompact": 20, "Stop": 5, "SubagentStop": 10, "SessionEnd": 20,
	}
	for event, wantTimeout := range timeouts {
		groups := m.Hooks.Hooks[event]
		require.Len(t, groups, 1, "%s", event)
		require.Len(t, groups[0].Hooks, 1, "%s", event)
		require.Equal(t, wantTimeout, groups[0].Hooks[0].Timeout, "%s timeout", event)
		require.Equal(t, "command", groups[0].Hooks[0].Type, "%s type", event)
		require.True(t, strings.HasPrefix(groups[0].Hooks[0].Command, "${CLAUDE_PLUGIN_ROOT}/bin/qompack "),
			"%s must invoke the bundled binary, got %q", event, groups[0].Hooks[0].Command)
	}

	require.Equal(t, "*", m.Hooks.Hooks["PostToolUse"][0].Matcher,
		"PostToolUse matches every tool")
	for _, event := range []string{"UserPromptSubmit", "SessionStart", "PreCompact", "Stop", "SubagentStop", "SessionEnd"} {
		require.Empty(t, m.Hooks.Hooks[event][0].Matcher,
			"%s does not match on tool name, so matcher must be omitted", event)
	}
}

func TestManifest_SevenCommands(t *testing.T) {
	m := pluginmanifest.Default(testVersion)
	names := make([]string, 0, len(m.Commands))
	for _, c := range m.Commands {
		names = append(names, c.Name)
	}
	require.Equal(t, []string{"status", "recall", "pin", "checkpoint", "why", "dropped", "eval"}, names,
		"exactly the seven §7.5 commands, in §7.5 order")
}

func TestManifest_CommandsShellOutToBinary(t *testing.T) {
	m := pluginmanifest.Default(testVersion)
	files, err := m.Files()
	require.NoError(t, err)

	for _, c := range m.Commands {
		body := string(files["plugin/commands/"+c.Name+".md"])
		require.True(t, strings.HasPrefix(body, "---\n"), "%s: frontmatter must open the file", c.Name)
		require.Contains(t, body, "description: ", c.Name)
		require.Contains(t, body, "argument-hint: ", c.Name)
		require.Contains(t, body, "allowed-tools: Bash(qompack "+c.Name+":*)", c.Name)
		require.Contains(t, body, "!`qompack "+c.Name+" $ARGUMENTS`", c.Name)
		require.True(t, strings.HasSuffix(body, "\n"), "%s: must end with a newline", c.Name)
	}
}

func TestMCPJSON_UsesPluginRoot(t *testing.T) {
	m := pluginmanifest.Default(testVersion)
	srv, ok := m.MCP.MCPServers["qompack"]
	require.True(t, ok)
	require.Equal(t, "${CLAUDE_PLUGIN_ROOT}/bin/qompack", srv.Command)
	require.Equal(t, []string{"mcp"}, srv.Args)
}

func TestManifest_PluginJSONShape(t *testing.T) {
	files, err := pluginmanifest.Default(testVersion).Files()
	require.NoError(t, err)

	var pj map[string]any
	require.NoError(t, json.Unmarshal(files["plugin/.claude-plugin/plugin.json"], &pj))
	require.Equal(t, "qompack", pj["name"])
	require.Equal(t, testVersion, pj["version"])
	require.Equal(t, "Cache-aware, retrieval-backed context compaction", pj["description"])
}

func TestManifest_FilesAreStableBytes(t *testing.T) {
	// `git diff --exit-code -- plugin/` is a CI gate, so generation must be deterministic and
	// the formatting must match what lands in the tree: two-space indent, trailing newline.
	a, err := pluginmanifest.Default(testVersion).Files()
	require.NoError(t, err)
	b, err := pluginmanifest.Default(testVersion).Files()
	require.NoError(t, err)
	require.Equal(t, a, b, "generation must be deterministic")

	require.Len(t, a, 10, "3 JSON files + 7 command docs")

	for path, content := range a {
		require.True(t, strings.HasSuffix(string(content), "\n"), "%s must end with a newline", path)
		require.NotContains(t, string(content), "\r", "%s must use LF", path)
		if strings.HasSuffix(path, ".json") {
			require.Contains(t, string(content), "\n  ", "%s must use two-space indentation", path)
		}
	}
}

func TestValidate_CleanRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := pluginmanifest.Default(testVersion)
	require.NoError(t, pluginmanifest.Write(dir, m))
	require.Empty(t, pluginmanifest.Validate(dir, m))
}

func TestValidate_DetectsDrift(t *testing.T) {
	dir := t.TempDir()
	m := pluginmanifest.Default(testVersion)
	require.NoError(t, pluginmanifest.Write(dir, m))

	p := filepath.Join(dir, "plugin", "hooks", "hooks.json")
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, append(b, ' '), 0o644))

	diffs := pluginmanifest.Validate(dir, m)
	require.Len(t, diffs, 1)
	require.Equal(t, "plugin/hooks/hooks.json", diffs[0].Path)
	require.Equal(t, "content differs", diffs[0].Reason)
}

func TestValidate_DetectsMissingAndExtra(t *testing.T) {
	dir := t.TempDir()
	m := pluginmanifest.Default(testVersion)
	require.NoError(t, pluginmanifest.Write(dir, m))

	require.NoError(t, os.Remove(filepath.Join(dir, "plugin", ".mcp.json")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin", "commands", "rogue.md"), []byte("x\n"), 0o644))

	byPath := map[string]string{}
	for _, d := range pluginmanifest.Validate(dir, m) {
		byPath[d.Path] = d.Reason
	}
	require.Equal(t, "missing", byPath["plugin/.mcp.json"])
	require.Equal(t, "not produced by the generator", byPath["plugin/commands/rogue.md"])
}

func TestValidate_IgnoresPluginBin(t *testing.T) {
	// plugin/bin/** is populated at package time by SP-17 and is gitignored; the generator must
	// not report it as an extra file or plugin-validate would fail on every packaged tree.
	dir := t.TempDir()
	m := pluginmanifest.Default(testVersion)
	require.NoError(t, pluginmanifest.Write(dir, m))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "plugin", "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin", "bin", "qompack-linux-amd64"), []byte("ELF"), 0o644))

	require.Empty(t, pluginmanifest.Validate(dir, m))
}

func TestValidate_ToleratesCRLFCheckout(t *testing.T) {
	dir := t.TempDir()
	m := pluginmanifest.Default(testVersion)
	require.NoError(t, pluginmanifest.Write(dir, m))

	p := filepath.Join(dir, "plugin", ".mcp.json")
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	crlf := strings.ReplaceAll(string(b), "\n", "\r\n")
	require.NoError(t, os.WriteFile(p, []byte(crlf), 0o644))

	require.Empty(t, pluginmanifest.Validate(dir, m),
		"a CRLF checkout on Windows must not be reported as drift")
}

func TestManifest_VersionFlowsThrough(t *testing.T) {
	files, err := pluginmanifest.Default("9.9.9").Files()
	require.NoError(t, err)
	require.Contains(t, string(files["plugin/.claude-plugin/plugin.json"]), `"version": "9.9.9"`)
}

func TestManifest_NoUnexpandedTemplateLeftovers(t *testing.T) {
	files, err := pluginmanifest.Default(testVersion).Files()
	require.NoError(t, err)
	placeholder := regexp.MustCompile(`%[sdqv]|\{\{`)
	for path, content := range files {
		require.False(t, placeholder.Match(content), "%s contains an unexpanded template directive", path)
	}
}
