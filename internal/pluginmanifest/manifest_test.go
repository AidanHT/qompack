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
		require.Equal(t, "${CLAUDE_PLUGIN_ROOT}/bin/qompack", groups[0].Hooks[0].Command,
			"%s must invoke the bundled binary", event)
		require.NotEmpty(t, groups[0].Hooks[0].Args, "%s must carry its subcommand in args", event)
	}

	require.Equal(t, "*", m.Hooks.Hooks["PostToolUse"][0].Matcher,
		"PostToolUse matches every tool")
	for _, event := range []string{"UserPromptSubmit", "SessionStart", "PreCompact", "Stop", "SubagentStop", "SessionEnd"} {
		require.Empty(t, m.Hooks.Hooks[event][0].Matcher,
			"%s does not match on tool name, so matcher must be omitted", event)
	}
}

// TestManifest_SevenCommands pins the shipped command list. Its name is historical and kept because
// the V1/V3 verification plans quote it: §7.5 names seven commands, and the bundle ships six of
// them. /qompack:checkpoint is not shipped — its only route, `qompack checkpoint`, is the PreCompact
// hook entry point, so the command wrote nothing (V6 close-out w7b-checkpoint). Checkpoints are
// written automatically before every compaction; docs/cannot-do.md says a manual one is not offered.
func TestManifest_SevenCommands(t *testing.T) {
	m := pluginmanifest.Default(testVersion)
	names := make([]string, 0, len(m.Commands))
	for _, c := range m.Commands {
		names = append(names, c.Name)
	}
	require.Equal(t, []string{"status", "recall", "pin", "why", "dropped", "eval"}, names,
		"the six shipped §7.5 commands, in §7.5 order; checkpoint is not shipped")
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
	require.Equal(t, pluginmanifest.Description, pj["description"])
}

// TestManifest_KeywordsClaimOnlyWhatIsSupported pins plugin.json's keywords. A marketplace search
// matches them, so each is a claim: "compaction" names what Qompack responds to, while "cache"
// suggested the cache awareness the audit's F8 calls unsupported (V6 close-out D53(e)).
func TestManifest_KeywordsClaimOnlyWhatIsSupported(t *testing.T) {
	files, err := pluginmanifest.Default(testVersion).Files()
	require.NoError(t, err)

	var pj struct {
		Keywords []string `json:"keywords"`
	}
	require.NoError(t, json.Unmarshal(files["plugin/.claude-plugin/plugin.json"], &pj))
	require.Equal(t, []string{"compaction", "context", "memory"}, pj.Keywords)
}

func TestManifest_FilesAreStableBytes(t *testing.T) {
	// `git diff --exit-code -- plugin/` is a CI gate, so generation must be deterministic and
	// the formatting must match what lands in the tree: two-space indent, trailing newline.
	a, err := pluginmanifest.Default(testVersion).Files()
	require.NoError(t, err)
	b, err := pluginmanifest.Default(testVersion).Files()
	require.NoError(t, err)
	require.Equal(t, a, b, "generation must be deterministic")

	require.Len(t, a, 9, "3 JSON files + 6 command docs")

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

// manifestTargets are the three spellings of GOOS the six release targets use.
var manifestTargets = []string{"windows", "linux", "darwin"}

// wantBinaryRef is the executable each target's bundle ships, restated here rather than read from
// BinaryRef so a change to the generator's path cannot pass by agreeing with itself.
func wantBinaryRef(goos string) string {
	if goos == "windows" {
		return "${CLAUDE_PLUGIN_ROOT}/bin/qompack.exe"
	}
	return "${CLAUDE_PLUGIN_ROOT}/bin/qompack"
}

// TestManifest_HooksAreExecForm is C1.11. A hooks.json `command` with no `args` is SHELL form, and
// the host picks the shell: "sh -c on macOS and Linux, Git Bash on Windows, or PowerShell when Git
// Bash isn't installed" (hooks reference, 2026-09-22). The quoted string this package used to ship
// — `"${CLAUDE_PLUGIN_ROOT}/bin/qompack" observe tool` — is a PowerShell ParserError, so every hook
// broke on a Windows machine without Git Bash. Exec form has no shell on any platform: `command` is
// the exact executable, the subcommand is the argument vector, and nothing is quoted — which also
// closes finding F-1 (a spaced install directory) structurally rather than by quoting.
func TestManifest_HooksAreExecForm(t *testing.T) {
	for _, goos := range manifestTargets {
		m := pluginmanifest.ForTarget(testVersion, goos)
		require.Len(t, m.Hooks.Hooks, 7, "the hook event count is part of the plugin-validate contract")
		entries := map[string]string{}
		for _, e := range pluginmanifest.HookEntryPoints() {
			entries[e.Event] = e.Subcommand
		}
		for event, groups := range m.Hooks.Hooks {
			h := groups[0].Hooks[0]
			require.Equal(t, wantBinaryRef(goos), h.Command,
				"%s/%s: command must be exactly the bundled executable and nothing else", goos, event)
			require.Equal(t, strings.Fields(entries[event]), h.Args,
				"%s/%s: the subcommand travels as the argument vector", goos, event)
			require.NotContains(t, h.Command, `"`, "%s/%s: exec form takes no shell quoting", goos, event)
			for _, a := range h.Args {
				require.NotContains(t, a, `"`, "%s/%s: exec form takes no shell quoting", goos, event)
			}
		}
	}
}

// TestManifest_HooksJSONCarriesArgs pins the wire shape: `args` is always present in hooks.json,
// because its presence — not its content — is what the host reads as "exec form".
func TestManifest_HooksJSONCarriesArgs(t *testing.T) {
	for _, goos := range manifestTargets {
		files, err := pluginmanifest.ForTarget(testVersion, goos).Files()
		require.NoError(t, err)
		var doc struct {
			Hooks map[string][]struct {
				Hooks []map[string]json.RawMessage `json:"hooks"`
			} `json:"hooks"`
		}
		require.NoError(t, json.Unmarshal(files["plugin/hooks/hooks.json"], &doc))
		for event, groups := range doc.Hooks {
			for _, h := range groups[0].Hooks {
				_, hasArgs := h["args"]
				require.True(t, hasArgs, "%s/%s: hooks.json must carry args", goos, event)
				_, hasShell := h["shell"]
				require.False(t, hasShell, "%s/%s: exec form ignores `shell`; naming one would mislead", goos, event)
			}
		}
	}
}

// TestManifest_MCPCommandIsTheExactBinary: .mcp.json launches the same executable the hooks do,
// exec form, unquoted.
func TestManifest_MCPCommandIsTheExactBinary(t *testing.T) {
	for _, goos := range manifestTargets {
		srv := pluginmanifest.ForTarget(testVersion, goos).MCP.MCPServers["qompack"]
		require.Equal(t, wantBinaryRef(goos), srv.Command, goos)
		require.Equal(t, []string{"mcp"}, srv.Args, goos)
		require.NotContains(t, srv.Command, `"`,
			"the MCP server command is exec form and must carry no shell quoting, got %q", srv.Command)
	}
}

// TestManifest_DefaultIsThePOSIXRendering says what the committed plugin/ tree is: the rendering
// linux and darwin share. Only windows differs, and only in the executable path.
func TestManifest_DefaultIsThePOSIXRendering(t *testing.T) {
	def, err := pluginmanifest.Default(testVersion).Files()
	require.NoError(t, err)
	linux, err := pluginmanifest.ForTarget(testVersion, "linux").Files()
	require.NoError(t, err)
	darwin, err := pluginmanifest.ForTarget(testVersion, "darwin").Files()
	require.NoError(t, err)
	require.Equal(t, linux, def, "the committed tree is the linux rendering")
	require.Equal(t, linux, darwin, "linux and darwin render byte-identical trees")

	windows, err := pluginmanifest.ForTarget(testVersion, "windows").Files()
	require.NoError(t, err)
	require.Len(t, windows, len(linux))
	for rel, lb := range linux {
		wb := windows[rel]
		if rel == "plugin/hooks/hooks.json" || rel == "plugin/.mcp.json" {
			require.Equal(t, strings.ReplaceAll(string(lb), `/bin/qompack"`, `/bin/qompack.exe"`), string(wb),
				"%s: the windows rendering differs only in the executable's .exe", rel)
			continue
		}
		require.Equal(t, string(lb), string(wb), "%s is target-independent", rel)
	}
}
