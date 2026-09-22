package platform

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// pluginRootPlaceholder is the manifest's installation-directory placeholder. For an exec-form hook
// the host substitutes it "into command and into each args element as plain strings", and it also
// exports CLAUDE_PLUGIN_ROOT into the spawned process's environment (Claude Code hooks reference,
// "Exec form and shell form", fetched 2026-09-22).
const pluginRootPlaceholder = "${CLAUDE_PLUGIN_ROOT}"

// pluginRootPlainName is the launcher matrix's CONTROL install directory: the same bundle at a path
// with no space in it. Without it, a failure at the spaced path could be anything — a drive-letter
// path, an encoding problem — and the record would name the wrong owner.
const pluginRootPlainName = "plugplain"

// hookEntry is one hooks.json handler as the host reads it. The raw map is kept alongside so a key
// the typed view does not name (`shell`) can still be checked for.
type hookEntry struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Timeout int      `json:"timeout"`
}

// bundleHookEntries reads every handler in the bundle's hooks/hooks.json, keyed by event, and
// asserts the exec-form shape on the raw JSON: `args` present, no `shell`.
//
// It is READ from the assembled bundle rather than restated. The question this package answers is
// what a host does with THAT file, so a copy here would answer it about a file nobody ships.
func bundleHookEntries(t *testing.T, bundleDir string) map[string]hookEntry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(bundleDir, "hooks", "hooks.json"))
	require.NoError(t, err, "the bundle must carry hooks/hooks.json")

	var doc struct {
		Hooks map[string][]struct {
			Hooks []json.RawMessage `json:"hooks"`
		} `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc), "hooks/hooks.json must parse")

	out := make(map[string]hookEntry, len(doc.Hooks))
	for event, groups := range doc.Hooks {
		require.Len(t, groups, 1, "%s: one handler group", event)
		require.Len(t, groups[0].Hooks, 1, "%s: one handler", event)
		var keys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(groups[0].Hooks[0], &keys))
		_, hasArgs := keys["args"]
		require.True(t, hasArgs, "%s: no `args`, so the host would hand `command` to a shell (C1.11)", event)
		_, hasShell := keys["shell"]
		require.False(t, hasShell, "%s: `shell` is ignored when `args` is set; naming one would mislead", event)
		var h hookEntry
		require.NoError(t, json.Unmarshal(groups[0].Hooks[0], &h))
		out[event] = h
	}
	return out
}

// hostExecArgv is what the host does with an exec-form entry installed at pluginRoot: substitute
// the placeholder into `command` and each `args` element as plain strings, and spawn `command`
// with `args` — no shell, no tokenization, no quoting.
func hostExecArgv(h hookEntry, pluginRoot string) (exe string, argv []string) {
	exe = filepath.FromSlash(strings.ReplaceAll(h.Command, pluginRootPlaceholder, filepath.ToSlash(pluginRoot)))
	argv = make([]string, len(h.Args))
	for i, a := range h.Args {
		argv[i] = strings.ReplaceAll(a, pluginRootPlaceholder, pluginRoot)
	}
	return exe, argv
}

// launcherResult is one launch's observation.
type launcherResult struct {
	form      string
	code      int
	stdout    []byte
	stderr    []byte
	parseable bool
}

// TestPlatform_HookLauncherForms is C1.11's regression test, on every OS.
//
// Every hook and the MCP server are EXEC form: `command` is exactly the bundled executable —
// ${CLAUDE_PLUGIN_ROOT}/bin/qompack.exe on windows, bin/qompack elsewhere — and `args` is the
// subcommand. The hooks reference (fetched 2026-09-22): "Exec form runs when args is present.
// Claude Code resolves command as an executable on PATH and spawns it directly with args as the
// argument vector. There is no shell ... No shell tokenization happens on any platform", and "On
// Windows, exec form requires command to resolve to a real executable such as a .exe". So there
// is ONE launch path whatever shell the host would otherwise pick — the `shell` field is "Ignored
// when args is set" — and this test drives exactly that launch: the bundle's own entries,
// substituted and spawned directly, from a plain and from a spaced, non-ASCII install directory.
//
// What it replaced. The manifest used to be SHELL form, `"${CLAUDE_PLUGIN_ROOT}/bin/qompack"
// observe tool`, which the host runs through "sh -c on macOS and Linux, Git Bash on Windows, or
// PowerShell when Git Bash isn't installed". Under PowerShell — after the host's documented rewrite
// of the placeholder to ${env:CLAUDE_PLUGIN_ROOT} — that string is a ParserError, so every hook
// failed on a Windows machine without Git Bash; and this test's PowerShell row ran a string nobody
// shipped (`& "$env:CLAUDE_PLUGIN_ROOT/bin/qompack" …`). On windows the pre-C1.11 string is still
// run under both shells below as a RECORDED contrast: it says what exec form bought on this host,
// and it is not asserted, because what a shell does with it is a fact about the shell.
func TestPlatform_HookLauncherForms(t *testing.T) {
	rec := newRecord(t, "hook-launcher-forms")

	b := assembledBundle(t)
	base := tempBase(t)

	spaced := filepath.Join(base, pluginRootName)
	plain := filepath.Join(base, pluginRootPlainName)
	require.NoError(t, copyTree(b.Dir, spaced), "copying the bundle to %q", spaced)
	require.NoError(t, copyTree(b.Dir, plain), "copying the bundle to %q", plain)
	require.Contains(t, filepath.Base(spaced), " ", "the spaced install directory must contain a space")
	require.NotContains(t, filepath.Base(plain), " ", "the control install directory must not")

	// The shipped shape: every entry names exactly the executable this bundle ships.
	wantCmd := pluginRootPlaceholder + "/bin/qompack" + exeSuffix()
	entries := bundleHookEntries(t, b.Dir)
	require.Len(t, entries, len(hookSubcommands)+1,
		"hooks.json declares the six entry points, with Stop and SubagentStop as two events")
	for event, h := range entries {
		require.Equal(t, wantCmd, h.Command, "%s: command must be exactly the bundled executable", event)
		require.NotEmpty(t, h.Args, "%s: the subcommand travels in args", event)
		require.Positive(t, h.Timeout, "%s: declares no timeout", event)
	}
	require.FileExists(t, filepath.Join(spaced, "bin", "qompack"+exeSuffix()),
		"the executable the manifest names must exist in the bundle, exactly, with no resolution step")

	var mcpDoc struct {
		MCPServers map[string]hookEntry `json:"mcpServers"`
	}
	mcpRaw, err := os.ReadFile(filepath.Join(b.Dir, ".mcp.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(mcpRaw, &mcpDoc))
	require.Equal(t, wantCmd, mcpDoc.MCPServers["qompack"].Command, ".mcp.json launches the same executable")
	require.Equal(t, []string{"mcp"}, mcpDoc.MCPServers["qompack"].Args)

	p := newProjectAt(t, base, plainRootName)
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	var rows []launcherResult
	launch := func(form, event, root string) launcherResult {
		exe, argv := hostExecArgv(entries[event], root)
		out, errOut, code := run(t, exe, b.Dir, argv, hookPayload(t, event, p.Root), launcherEnv(p, root))
		r := launcherResult{form: form, code: code, stdout: out, stderr: errOut, parseable: emptyOrParseableJSON(out)}
		rows = append(rows, r)
		t.Logf("%s: exit %d\n  exe:    %s\n  argv:   %q\n  stdout: %s\n  stderr: %s", form, code, exe, argv, out, errOut)
		return r
	}

	// The control, then every installed entry point from the spaced, non-ASCII directory — the
	// shape F-1 and C1.11 both broke on.
	control := launch("exec plain PostToolUse", "PostToolUse", plain)
	require.Equal(t, 0, control.code, "the shipped exec form must work from a space-free install directory\nstderr:\n%s", control.stderr)
	require.True(t, control.parseable, "and its stdout must be empty or one JSON document")
	for _, h := range hookSubcommands {
		r := launch("exec spaced "+h.event, h.event, spaced)
		require.Equal(t, 0, r.code, "%s: the shipped exec form must exit 0 from %q\nstderr:\n%s", h.event, spaced, r.stderr)
		require.True(t, r.parseable, "%s: stdout must be empty or one JSON document: %s", h.event, r.stdout)
	}
	r := launch("exec spaced SubagentStop", "SubagentStop", spaced)
	require.Equal(t, 0, r.code, "SubagentStop: stderr:\n%s", r.stderr)
	require.True(t, r.parseable)

	writeRecord(t, Record{
		Name: "hook-exec-form", Target: rec.Target, Bundle: rec.Bundle, Outcome: OutcomeVerified,
		Reason: "every hooks.json entry and .mcp.json is exec form naming exactly " + wantCmd +
			"; spawned directly as the host does, each exits 0 with parseable stdout from a spaced, non-ASCII install directory",
		Detail: fmt.Sprintf("control %q exited %d; %d entries run from %q; `shell` absent and ignored "+
			"by the host when args is set, so no host shell choice reaches these launches",
			plain, control.code, len(hookSubcommands)+1, spaced),
	})

	if runtime.GOOS == "windows" {
		rows = append(rows, shellFormContrast(t, rec, b, p, entries["PostToolUse"], spaced)...)
	}

	rec.Outcome = OutcomeVerified
	rec.Reason = fmt.Sprintf("the hook launcher matrix ran (%d launches); the shipped exec form passed every one it asserts", len(rows))
	rec.Detail = fmt.Sprintf("%d launches against %q and %q", len(rows), plain, spaced)
	writeRecord(t, rec)
}

// shellFormContrast runs the PRE-C1.11 shell-form string — rebuilt from the shipped entry, so it is
// the string this repository actually used to ship — under the two shells a Windows host picks
// between, and records what each did. Nothing here is asserted: it is the evidence of why the
// manifest is exec form, not a property of the product.
func shellFormContrast(t *testing.T, rec Record, b bundle, p project, h hookEntry, spaced string) []launcherResult {
	t.Helper()
	legacy := `"` + pluginRootPlaceholder + `/bin/qompack" ` + strings.Join(h.Args, " ")
	payload := hookPayload(t, "PostToolUse", p.Root)
	var rows []launcherResult

	if bash, why := findGitBash(); bash != "" {
		out, errOut, code := run(t, bash, b.Dir, []string{"-c", legacy}, payload, launcherEnv(p, spaced))
		rows = append(rows, launcherResult{form: "shell-form git-bash", code: code, stdout: out, stderr: errOut})
		writeRecord(t, Record{
			Name: "shell-form-contrast-gitbash", Target: rec.Target, Bundle: rec.Bundle, Outcome: OutcomeVerified,
			Reason: "recorded contrast, not asserted: the pre-C1.11 shell-form hook string under Git Bash",
			Detail: fmt.Sprintf("shell %s (%s); script %q; CLAUDE_PLUGIN_ROOT=%q; exit %d; stderr: %s",
				bash, why, legacy, spaced, code, strings.TrimSpace(string(errOut))),
		})
	}

	if pwsh := findShell("pwsh", "powershell"); pwsh != "" {
		// The host's documented rewrite for a PowerShell shell-form command: "Claude Code rewrites the
		// ${CLAUDE_PROJECT_DIR}, ${CLAUDE_PLUGIN_ROOT}, and ${CLAUDE_PLUGIN_DATA} placeholders in a
		// PowerShell shell-form command to PowerShell's ${env:NAME} form" (hooks reference).
		rewritten := strings.Replace(legacy, pluginRootPlaceholder, "${env:CLAUDE_PLUGIN_ROOT}", 1)
		out, errOut, code := run(t, pwsh, b.Dir, []string{"-NoProfile", "-NonInteractive", "-Command", rewritten},
			payload, launcherEnv(p, spaced))
		rows = append(rows, launcherResult{form: "shell-form powershell", code: code, stdout: out, stderr: errOut})
		writeRecord(t, Record{
			Name: "shell-form-contrast-powershell", Target: rec.Target, Bundle: rec.Bundle, Outcome: OutcomeVerified,
			Reason: "recorded contrast, not asserted: the pre-C1.11 shell-form hook string under PowerShell, " +
				"the shell a Windows host uses when Git Bash is not installed",
			Detail: fmt.Sprintf("shell %s; script %q; CLAUDE_PLUGIN_ROOT=%q; exit %d; stderr: %s",
				pwsh, rewritten, spaced, code, strings.TrimSpace(string(errOut))),
		})
		t.Logf("shell-form contrast under PowerShell: exit %d\n%s", code, errOut)
	}
	return rows
}

// launcherEnv is the environment a host gives a hook subprocess: the project pinned, and
// CLAUDE_PLUGIN_ROOT pointing at the install directory under test.
func launcherEnv(p project, pluginRoot string) map[string]string {
	env := map[string]string{"CLAUDE_PLUGIN_ROOT": pluginRoot}
	for k, v := range p.Env {
		env[k] = v
	}
	return env
}

// gitBashRelPaths are the layouts a Git for Windows installation puts bash.exe at, relative to the
// install root. `usr/bin` is the real MSYS2 bash; `bin` is the wrapper Git also ships.
var gitBashRelPaths = []string{
	filepath.Join("Git", "usr", "bin", "bash.exe"),
	filepath.Join("Git", "bin", "bash.exe"),
}

// gitBashRootEnvKeys are the directories a Git for Windows installation lives under, named by the
// environment rather than hardcoded so a machine with a relocated `Program Files` still resolves.
var gitBashRootEnvKeys = []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"}

// findGitBash locates GIT Bash specifically, and says how it was found.
//
// `exec.LookPath("bash")` is NOT good enough and is the trap this exists to avoid: on a Windows
// machine with WSL installed, PATH's first `bash.exe` is `C:\Windows\System32\bash.exe`, the WSL
// launcher. That binary cannot execute a Windows `.exe` named by a Windows path, so a contrast row
// that found it would attribute a WSL result to Git Bash in a record that names Git Bash.
//
// So the install locations are searched FIRST, and a PATH hit is accepted only when it sits inside
// something spelled like a Git installation. Anything else returns "" with a reason.
func findGitBash() (path, why string) {
	for _, key := range gitBashRootEnvKeys {
		root := os.Getenv(key)
		if root == "" {
			continue
		}
		for _, rel := range gitBashRelPaths {
			p := filepath.Join(root, rel)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p, "found at the Git for Windows install location under %" + key + "%"
			}
		}
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		for _, rel := range gitBashRelPaths {
			p := filepath.Join(local, "Programs", rel)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p, "found at the per-user Git for Windows install location under %LOCALAPPDATA%\\Programs"
			}
		}
	}
	p, err := exec.LookPath("bash")
	if err != nil {
		return "", "no bash.exe at any Git for Windows install location and none on PATH"
	}
	if !underGitInstall(p) {
		return "", fmt.Sprintf("the only bash on PATH is %q, which is not inside a Git for Windows "+
			"installation (on Windows this is usually WSL's launcher, which cannot execute a Windows "+
			".exe named by a Windows path)", p)
	}
	return p, "found on PATH inside a Git for Windows installation"
}

// underGitInstall reports whether p has a path segment spelled "git", which is what distinguishes
// `…\Git\usr\bin\bash.exe` from `C:\Windows\System32\bash.exe`.
func underGitInstall(p string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if strings.EqualFold(seg, "git") {
			return true
		}
	}
	return false
}

// findShell returns the first of names found on PATH, or "" when none is. Git Bash has its own
// finder (findGitBash) because PATH alone cannot identify it.
func findShell(names ...string) string {
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}
