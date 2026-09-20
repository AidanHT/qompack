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

// pluginRootPlaceholder is the manifest's installation-directory placeholder. The host expands it
// to the plugin's absolute install path and additionally exports it into the hook subprocess's
// environment (Claude Code plugin reference), which is why both expansions are exercised below.
const pluginRootPlaceholder = "${CLAUDE_PLUGIN_ROOT}"

// pluginRootPlainName is the launcher matrix's CONTROL install directory: the same bundle at a path
// with no space in it. Without it, a failure at the spaced path could be anything — a missing
// extension, a drive-letter path Git Bash will not accept, an encoding problem — and the record
// would name the wrong owner.
const pluginRootPlainName = "plugplain"

// hooksManifest is the subset of hooks/hooks.json this package reads.
type hooksManifest struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

// manifestCommand returns the literal `command` string hooks.json carries for one host event.
//
// It is READ from the assembled bundle rather than retyped here. The whole question this test
// answers is what a host's shell does with THAT string, so a copy in the test would answer the
// question about a string nobody ships the moment internal/pluginmanifest changes.
func manifestCommand(t *testing.T, bundleDir, event string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(bundleDir, "hooks", "hooks.json"))
	require.NoError(t, err, "the bundle must carry hooks/hooks.json")

	var m hooksManifest
	require.NoError(t, json.Unmarshal(raw, &m), "hooks/hooks.json must parse")

	groups, ok := m.Hooks[event]
	require.True(t, ok, "hooks.json must define %s", event)
	require.NotEmpty(t, groups, "hooks.json's %s must carry a group", event)
	require.NotEmpty(t, groups[0].Hooks, "hooks.json's %s group must carry a hook", event)
	cmd := groups[0].Hooks[0].Command
	require.True(t, strings.HasPrefix(cmd, quotedBinaryWord),
		"this test derives its unquoted contrast and PowerShell forms from the manifest's own "+
			"string, so that string must still begin with %s; it is %q", quotedBinaryWord, cmd)
	return cmd
}

// quotedBinaryWord is the executable word hooks.json carries since the F-1 fix: the placeholder
// double-quoted, which is what stops a shell splitting a spaced install directory into two
// arguments. It is a SHELL-form spelling only — see mcpExecFormNote.
const quotedBinaryWord = `"` + pluginRootPlaceholder + `/bin/qompack"`

// unquoteBinaryWord returns the PRE-FIX spelling of a manifest command: the same string with the
// executable word's quotes removed. It is what the contrast rows below run, so the package can
// still say what the quoting buys on this host rather than merely asserting it is present.
func unquoteBinaryWord(cmd string) string {
	return strings.Replace(cmd, quotedBinaryWord, pluginRootPlaceholder+"/bin/qompack", 1)
}

// mcpExecFormNote is the scope limit every record and document that carries the shell-form finding
// must repeat, because the obvious generalisation from it is wrong and would break the product.
//
// `.mcp.json` names the SAME placeholder — `{"command": "${CLAUDE_PLUGIN_ROOT}/bin/qompack",
// "args": ["mcp"]}` — but the presence of `args` makes it EXEC form: the host spawns the executable
// directly, with no shell (survey-host-docs.md, "Hook command execution"). There is no word
// splitting to defend against there, and adding quotes would make them literal characters in the
// path, so the spawn would fail with ENOENT on every install directory, spaced or not. Nothing in
// this package reads or exercises `.mcp.json`; extending the remedy to it would be an unmeasured
// extrapolation from a shell-form case to an exec-form one.
const mcpExecFormNote = "SCOPE: hooks.json ONLY. .mcp.json names the same placeholder but carries " +
	"`args`, which makes it exec form (no shell), so it has no word-splitting problem and must NOT " +
	"be quoted — quotes would become literal path characters and break the spawn everywhere. This " +
	"test neither reads nor exercises .mcp.json."

// launcherResult is one launcher form's observation.
type launcherResult struct {
	form      string
	script    string
	code      int
	stdout    []byte
	stderr    []byte
	parseable bool
}

// TestPlatform_WindowsHookLauncherForms answers packaging/README.md §7's open questions 1 and 2 by
// measurement rather than by reading the documentation, which does not cover either.
//
// Claude Code runs a shell-form hook command through Git Bash when one is installed and through
// PowerShell otherwise (Claude Code hooks reference). qompack's manifest names
// `"${CLAUDE_PLUGIN_ROOT}/bin/qompack" <subcommand>` — one string for all six targets, with no
// extension — while the Windows bundle ships `bin/qompack.exe`. Two things follow that nothing in
// the repository had ever established: whether the extension is appended, and what happens when the
// install directory contains a space.
//
// Every row runs against TWO install directories, one spaced and one not. The plain one is the
// control: it is what makes a failure at the spaced path attributable to the space rather than to
// the drive-letter path, the missing extension or the encoding.
//
// The unquoted spelling this originally measured was finding F-1: it exits 127 under Git Bash when
// the install directory contains a space, because the expansion word-splits. The fix landed in
// internal/pluginmanifest for hooks.json ALONE — see mcpExecFormNote — and this test is now its
// regression assertion: the string READ FROM THE BUNDLE must carry the quotes and must exit 0 from
// the spaced root under both Git Bash and PowerShell. The pre-fix spelling is still run beside it
// as the recorded contrast, because what a particular Git Bash does with it is a fact about the
// shell rather than a property of the product.
func TestPlatform_WindowsHookLauncherForms(t *testing.T) {
	rec := newRecord(t, "shell-launcher-forms")
	if runtime.GOOS != "windows" {
		skipRecorded(t, rec, "the Git Bash / PowerShell hook launcher forms exist only on windows; this host is "+runtime.GOOS)
	}

	b := assembledBundle(t)
	base := tempBase(t)

	spaced := filepath.Join(base, pluginRootName)
	plain := filepath.Join(base, pluginRootPlainName)
	require.NoError(t, copyTree(b.Dir, spaced), "copying the bundle to %q", spaced)
	require.NoError(t, copyTree(b.Dir, plain), "copying the bundle to %q", plain)
	require.Contains(t, filepath.Base(spaced), " ", "the spaced install directory must contain a space")
	require.NotContains(t, filepath.Base(plain), " ", "the control install directory must not")

	// The extension question, settled before any shell runs: the manifest names `bin/qompack`, and
	// if a file by that exact name existed the launcher rows below would prove nothing about
	// extension resolution.
	_, err := os.Stat(filepath.Join(spaced, "bin", "qompack"))
	require.Error(t, err,
		"the Windows bundle must ship bin/qompack.exe and NOT an extensionless bin/qompack, or the "+
			"extension-resolution question this test exists to answer is not being asked")
	require.FileExists(t, filepath.Join(spaced, "bin", "qompack.exe"))

	// `literal` is the string the bundle actually ships, read from the bundle. Since the F-1 fix it
	// is the QUOTED form, so it is what every "does the shipped manifest work" row runs; `unquoted`
	// is the pre-fix spelling, kept as the contrast that shows what the quotes buy on this host.
	literal := manifestCommand(t, b.Dir, "PostToolUse")
	unquoted := unquoteBinaryWord(literal)
	require.NotEqual(t, literal, unquoted, "the contrast row must differ from the shipped string")
	argTail := strings.TrimPrefix(literal, quotedBinaryWord)

	p := newProjectAt(t, base, plainRootName)
	payload := hookPayload(t, "PostToolUse", p.Root)
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	bash, bashWhy := findGitBash()
	pwsh := findShell("pwsh", "powershell")

	var rows []launcherResult

	t.Run("git-bash", func(t *testing.T) {
		rec := newRecord(t, "shell-gitbash")
		if bash == "" {
			skipRecorded(t, rec, "no Git Bash was found, so the Git Bash launcher form cannot be "+
				"exercised on this host: "+bashWhy)
		}
		t.Logf("git-bash: using %s (%s)", bash, bashWhy)

		// Four rows: the manifest's own string and its quoted variant, each against the spaced and
		// the plain install directory. The placeholder is supplied through the ENVIRONMENT here;
		// the textually-substituted form the host may use instead is the fifth row below.
		for _, form := range []struct {
			name, script, root string
		}{
			{"unquoted/plain", unquoted, plain},
			{"unquoted/spaced", unquoted, spaced},
			{"quoted/plain", literal, plain},
			{"quoted/spaced", literal, spaced},
		} {
			env := launcherEnv(p, form.root)
			out, errOut, code := run(t, bash, b.Dir, []string{"-c", form.script}, payload, env)
			rows = append(rows, launcherResult{
				form: "git-bash " + form.name, script: form.script,
				code: code, stdout: out, stderr: errOut, parseable: emptyOrParseableJSON(out),
			})
			t.Logf("git-bash %s: exit %d\n  script: %s\n  root:   %s\n  stdout: %s\n  stderr: %s",
				form.name, code, form.script, form.root, out, errOut)
		}

		// The host may substitute the placeholder into the string before the shell ever sees it
		// rather than relying on the exported variable. That form has to be measured too, because
		// it is the one where the quoting of the manifest string is the ONLY thing standing between
		// a spaced path and word splitting.
		substituted := strings.Replace(unquoted, pluginRootPlaceholder, spaced, 1)
		out, errOut, code := run(t, bash, b.Dir, []string{"-c", substituted}, payload, launcherEnv(p, spaced))
		rows = append(rows, launcherResult{
			form: "git-bash unquoted/spaced (textually substituted)", script: substituted,
			code: code, stdout: out, stderr: errOut, parseable: emptyOrParseableJSON(out),
		})

		// And the same substitution through the SHIPPED (quoted) manifest string, which is the
		// remedy F-1 asked for and the one the bundle now carries. It has to be measured too: under
		// textual substitution a Windows path reaches bash carrying backslashes, and an unquoted
		// word can have them consumed as escapes as well as being split on the space — two failure
		// modes, one fix, and only a row that tries it can say the fix covers both.
		substitutedQuoted := strings.Replace(literal, pluginRootPlaceholder, spaced, 1)
		out, errOut, code = run(t, bash, b.Dir, []string{"-c", substitutedQuoted}, payload, launcherEnv(p, spaced))
		rows = append(rows, launcherResult{
			form: "git-bash quoted/spaced (textually substituted)", script: substitutedQuoted,
			code: code, stdout: out, stderr: errOut, parseable: emptyOrParseableJSON(out),
		})

		byForm := indexRows(rows)

		// The control must work, or nothing else in this subtest means anything. It is the SHIPPED
		// manifest string against the space-free install directory.
		control := byForm["git-bash quoted/plain"]
		require.Equal(t, 0, control.code,
			"the manifest's own command string must work from an install directory with no space in it; "+
				"exit %d\nstdout:\n%s\nstderr:\n%s", control.code, control.stdout, control.stderr)
		require.True(t, control.parseable, "and its stdout must be empty or one JSON document")

		// Extension resolution, answered: the command named `bin/qompack`, no such file exists, and
		// the process ran and produced a hook's output.
		writeRecord(t, Record{
			Name: "shell-gitbash-exe-resolution", Target: rec.Target, Bundle: rec.Bundle,
			Outcome: OutcomeVerified,
			Reason:  "Git Bash resolves the manifest's extensionless bin/qompack to bin/qompack.exe",
			Detail: fmt.Sprintf("shell %s (%s); script %q with CLAUDE_PLUGIN_ROOT=%q exited %d; the bundle "+
				"contains no extensionless bin/qompack, so the .exe suffix was supplied by the launcher",
				bash, bashWhy, literal, plain, control.code),
		})

		quotedSpaced := byForm["git-bash quoted/spaced"]
		quotedSubstituted := byForm["git-bash quoted/spaced (textually substituted)"]
		require.Equal(t, 0, quotedSpaced.code,
			"quoting the placeholder must make a spaced install directory work — that is the fix this "+
				"finding asks for; exit %d\nstdout:\n%s\nstderr:\n%s",
			quotedSpaced.code, quotedSpaced.stdout, quotedSpaced.stderr)
		require.True(t, quotedSpaced.parseable)
		require.Equal(t, 0, quotedSubstituted.code,
			"and it must work under textual substitution too, or the remedy covers only one of the two "+
				"ways a host can expand the placeholder; exit %d\nstdout:\n%s\nstderr:\n%s",
			quotedSubstituted.code, quotedSubstituted.stdout, quotedSubstituted.stderr)
		writeRecord(t, Record{
			Name: "shell-gitbash-quoted-spaced", Target: rec.Target, Bundle: rec.Bundle,
			Outcome: OutcomeVerified,
			Reason:  "the QUOTED placeholder form works from an install directory containing a space",
			Detail: fmt.Sprintf("shell %s (%s); env-expanded script %q exited %d (stdout parseable=%t); "+
				"the textually substituted script %q exited %d; CLAUDE_PLUGIN_ROOT=%q throughout",
				bash, bashWhy, literal, quotedSpaced.code, quotedSpaced.parseable,
				quotedSubstituted.script, quotedSubstituted.code, spaced),
		})

		// F-1's regression assertion. The row that used to record the finding now ASSERTS the
		// remedy: the string the bundle ships must exit 0 from a spaced install directory under both
		// of the host's expansion strategies, and it must still be the quoted spelling — a manifest
		// that silently lost its quotes would exit 0 on a host whose shell happens not to split, and
		// this row would be the only thing between that and a re-shipped F-1.
		//
		// The pre-fix spelling is still RUN, and its exit codes are recorded rather than asserted.
		// It is the contrast that says what the quoting bought on this host; asserting it must fail
		// would be asserting a property of Git Bash, not of the product.
		unquotedSpaced := byForm["git-bash unquoted/spaced"]
		substitutedSpaced := byForm["git-bash unquoted/spaced (textually substituted)"]
		require.Contains(t, literal, quotedBinaryWord,
			"regression guard for F-1: the shipped hooks.json command must keep the quoted plugin "+
				"root; it is %q", literal)
		require.Equal(t, 0, quotedSpaced.code, "F-1 regression: the shipped manifest string must "+
			"exit 0 from a spaced install directory")
		require.Equal(t, 0, quotedSubstituted.code, "F-1 regression: and under textual substitution")

		writeRecord(t, Record{
			Name: "shell-gitbash-unquoted-spaced", Target: rec.Target, Bundle: rec.Bundle,
			Outcome: OutcomeVerified,
			Reason: "F-1 is fixed and this row is now its regression assertion: the command the " +
				"bundle SHIPS quotes the plugin root and exits 0 from an install directory " +
				"containing a space, under both host expansion strategies",
			Detail: fmt.Sprintf(
				"shell %s (%s). Shipped (quoted) form %q exited %d env-expanded and %d textually "+
					"substituted against the spaced root, and %d against the space-free control %q. "+
					"The PRE-FIX spelling %q is still run as the contrast and is not asserted on: it "+
					"exited %d env-expanded (stderr: %s) and %d textually substituted (stderr: %s). %s",
				bash, bashWhy,
				literal, quotedSpaced.code, quotedSubstituted.code, control.code, plain,
				unquoted,
				unquotedSpaced.code, strings.TrimSpace(string(unquotedSpaced.stderr)),
				substitutedSpaced.code, strings.TrimSpace(string(substitutedSpaced.stderr)),
				mcpExecFormNote),
		})
		t.Logf("launcher verdict: shipped=%d/%d (env/substituted) pre-fix contrast=%d/%d",
			quotedSpaced.code, quotedSubstituted.code, unquotedSpaced.code, substitutedSpaced.code)
	})

	t.Run("powershell", func(t *testing.T) {
		rec := newRecord(t, "shell-powershell")
		if pwsh == "" {
			skipRecorded(t, rec, "neither pwsh nor powershell is on PATH, so the PowerShell launcher "+
				"form cannot be exercised on this host")
		}

		// The form the docs give for PowerShell: the call operator against the exported variable.
		// `${CLAUDE_PLUGIN_ROOT}` is a VARIABLE reference in PowerShell, not an environment lookup,
		// so a host that handed the manifest string to PowerShell unchanged without substituting it
		// first would run `/bin/qompack` — which is why this row uses $env: and the substituted
		// form is measured beside it.
		script := `& "$env:CLAUDE_PLUGIN_ROOT/bin/qompack"` + argTail
		var psRows []launcherResult
		for _, form := range []struct{ name, root string }{{"plain", plain}, {"spaced", spaced}} {
			out, errOut, code := run(t, pwsh, b.Dir,
				[]string{"-NoProfile", "-NonInteractive", "-Command", script}, payload, launcherEnv(p, form.root))
			psRows = append(psRows, launcherResult{
				form: "powershell " + form.name, script: script,
				code: code, stdout: out, stderr: errOut, parseable: emptyOrParseableJSON(out),
			})
			t.Logf("powershell %s: exit %d\n  script: %s\n  root:   %s\n  stdout: %s\n  stderr: %s",
				form.name, code, script, form.root, out, errOut)
		}
		rows = append(rows, psRows...)
		byForm := indexRows(psRows)

		control := byForm["powershell plain"]
		require.Equal(t, 0, control.code,
			"the PowerShell launcher form must work from an install directory with no space; exit %d\nstdout:\n%s\nstderr:\n%s",
			control.code, control.stdout, control.stderr)
		require.True(t, control.parseable)

		spacedRow := byForm["powershell spaced"]
		outcome, reason := OutcomeVerified, "the PowerShell launcher form works from an install "+
			"directory containing a space and non-ASCII characters"
		if spacedRow.code != 0 {
			outcome, reason = OutcomeFailed, "the PowerShell launcher form failed from an install "+
				"directory containing a space"
		}
		writeRecord(t, Record{
			Name: "shell-powershell-spaced", Target: rec.Target, Bundle: rec.Bundle,
			Outcome: outcome, Reason: reason,
			Detail: fmt.Sprintf("shell %s; script %q; CLAUDE_PLUGIN_ROOT=%q exited %d (stdout parseable=%t); "+
				"the space-free control exited %d. The command names bin/qompack with no extension and the "+
				"bundle ships only bin/qompack.exe, so a zero exit is also this host's answer to the "+
				"extension-resolution question",
				pwsh, script, spaced, spacedRow.code, spacedRow.parseable, control.code),
		})
		// F-1's regression assertion on the second launcher: the record is written first so a
		// failure still leaves evidence, and only then is the spaced root asserted.
		require.Equal(t, 0, spacedRow.code,
			"F-1 regression: the PowerShell launcher form must exit 0 from a spaced install "+
				"directory; exit %d\nstdout:\n%s\nstderr:\n%s",
			spacedRow.code, spacedRow.stdout, spacedRow.stderr)
	})

	// The aggregate record's outcome comes from what the subtests actually did, not from reaching
	// this line.
	//
	// t.Skip inside a subtest is runtime.Goexit, which unwinds THAT subtest and returns control
	// here — so on a host with neither shell both subtests write `skipped` records and this line is
	// still reached. Writing `verified` there would say "the Windows hook launcher matrix ran" over
	// zero invocations and four absent per-form records: precisely the "a skip is never counted as
	// verified" rule this package exists to keep (Qompack.md §7.5). It cannot fire on
	// windows-latest, which has both shells, which is exactly why it would not have been caught.
	if len(rows) == 0 {
		skipRecorded(t, rec, "neither Git Bash nor PowerShell could be used on this host, so no "+
			"launcher form ran at all (bash: "+bashWhy+")")
	}
	rec.Outcome = OutcomeVerified
	rec.Reason = fmt.Sprintf("the Windows hook launcher matrix ran (%d invocations)", len(rows))
	rec.Detail = fmt.Sprintf("%d launcher invocations across bash=%q (%s) and powershell=%q against %q and %q",
		len(rows), bash, bashWhy, pwsh, plain, spaced)
	writeRecord(t, rec)
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

// indexRows keys launcher results by their form name.
func indexRows(rows []launcherResult) map[string]launcherResult {
	out := make(map[string]launcherResult, len(rows))
	for _, r := range rows {
		out[r.form] = r
	}
	return out
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
// launcher. That binary cannot execute a Windows `.exe` named by a Windows path, so a matrix that
// found it would either fail its own control for an environmental reason or — worse — attribute a
// WSL result to Git Bash in a record that names Git Bash. Which one PATH yields depends on nothing
// more principled than the shell `go test` happened to be launched from.
//
// So the install locations are searched FIRST, and a PATH hit is accepted only when it sits inside
// something spelled like a Git installation. Anything else returns "" with a reason, which the
// caller records as a `platform:` skip rather than a pass.
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
