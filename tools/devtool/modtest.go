package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// mod-test and mod-validate are the mod's two build-time checks. Both materialise the host
// target's plugin with the hooks module (bundle --mods's tree, without the binary, which neither
// needs) into a temporary directory and hand it to the host CLI: `claude plugin test` runs the
// mod's TypeScript tests, `claude plugin validate` runs the host's static analysis of the module.
// Neither starts a session, signs in or calls a model.
//
// Both refuse to pass without claude, unlike bundle --host-validate's "unverified" record: a mod
// that was never tested or validated must not read as one that was.
//
// claude runs with CLAUDE_CONFIG_DIR set to a fresh private directory and DISABLE_AUTOUPDATER=1,
// so neither task reads or writes the user's own ~/.claude, and neither can update the CLI.

// modTestsDir holds the mod's TypeScript tests. They are never embedded or shipped; mod-test
// copies them into the materialised plugin's tests/ directory, where they import ../hooks/*.ts.
const modTestsDir = "internal/pluginmanifest/modsrc/tests"

// modPluginVersion is the version the materialised plugin's plugin.json carries. The checks do not
// depend on it; it is the build's own version so the tree is the one a bundle would ship.
var modPluginVersion = core.Version

// modClaude is the host CLI as the mod tasks use it: found on PATH, and run with an explicit
// environment so the configuration directory is always the task's private one.
type modClaude struct {
	lookPath func() (string, error)
	run      func(env []string, bin string, args ...string) claudeRun
}

func osModClaude() modClaude {
	return modClaude{
		lookPath: func() (string, error) { return exec.LookPath(claudeBinary) },
		run:      runClaudeEnv,
	}
}

// modClaudeEnv is base with CLAUDE_CONFIG_DIR replaced by configDir and DISABLE_AUTOUPDATER=1.
// Names match case-insensitively, as Windows matches them, so no spelling of either survives.
func modClaudeEnv(base []string, configDir string) []string {
	out := make([]string, 0, len(base)+2)
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(k, "CLAUDE_CONFIG_DIR") || strings.EqualFold(k, "DISABLE_AUTOUPDATER") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "CLAUDE_CONFIG_DIR="+configDir, "DISABLE_AUTOUPDATER=1")
}

// modWorkspace is one task run's temporary directories: the materialised plugin and claude's
// private configuration directory, side by side under one parent that close removes.
type modWorkspace struct {
	parent    string
	plugin    string
	configDir string
}

// newModWorkspace materialises goos's mods plugin tree under a fresh temporary directory.
func newModWorkspace(goos string) (*modWorkspace, error) {
	parent, err := os.MkdirTemp("", "qompack-mod-")
	if err != nil {
		return nil, err
	}
	ws := &modWorkspace{
		parent:    parent,
		plugin:    filepath.Join(parent, "plugin"),
		configDir: filepath.Join(parent, "claude-config"),
	}
	if err := os.MkdirAll(ws.configDir, bundleDirPerm); err != nil {
		return nil, errors.Join(err, ws.close())
	}
	tree, err := pluginTreeFiles(modPluginVersion, goos, true)
	if err != nil {
		return nil, errors.Join(err, ws.close())
	}
	for rel, b := range tree {
		if err := writeModFile(filepath.Join(ws.plugin, filepath.FromSlash(rel)), b); err != nil {
			return nil, errors.Join(err, ws.close())
		}
	}
	return ws, nil
}

func (ws *modWorkspace) close() error {
	if err := os.RemoveAll(ws.parent); err != nil {
		return fmt.Errorf("removing the temporary directory %s: %w", ws.parent, err)
	}
	return nil
}

func writeModFile(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), bundleDirPerm); err != nil {
		return err
	}
	return os.WriteFile(p, b, bundleFilePerm)
}

// copyModTests copies every *.test.ts under src into dst, keeping the layout, and returns how many
// it copied. Other files under src are copied too (a test may import a sibling helper); a source
// directory with no test file is an error, because the run would then prove nothing.
func copyModTests(src, dst string) (int, error) {
	n := 0
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.HasSuffix(d.Name(), ".test.ts") {
			n++
		}
		return writeModFile(filepath.Join(dst, rel), b)
	})
	if err != nil {
		return 0, fmt.Errorf("copying the mod tests from %s: %w", src, err)
	}
	if n == 0 {
		return 0, fmt.Errorf("no *.test.ts under %s", src)
	}
	return n, nil
}

// lookUpModClaude finds claude or fails: these checks never pass without it.
func lookUpModClaude(c modClaude, task string) (string, error) {
	bin, err := c.lookPath()
	if err != nil {
		return "", fmt.Errorf("the claude CLI is not on PATH (%w); %s fails rather than skips without it",
			err, task)
	}
	return bin, nil
}

// printClaudeVersion reports which CLI ran the check, since the mods contract changes between
// releases.
func printClaudeVersion(c modClaude, env []string, bin, task string, w io.Writer) {
	v := c.run(env, bin, "--version")
	switch {
	case v.Err != nil:
		_, _ = fmt.Fprintf(w, "%s: claude --version could not be run: %v\n", task, v.Err)
	case v.ExitCode != 0:
		_, _ = fmt.Fprintf(w, "%s: claude --version exited %d\n", task, v.ExitCode)
	default:
		_, _ = fmt.Fprintf(w, "%s: claude %s\n", task, strings.TrimSpace(string(v.Stdout)))
	}
}

// modTestSummary is the last line `claude plugin test` prints, e.g. "Ran 20 tests across 2 files.".
var modTestSummary = regexp.MustCompile(`(?m)^Ran (\d+) tests? across (\d+) files?\.`)

func taskModTest(args []string) error {
	fs := flag.NewFlagSet("mod-test", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return errors.Join(errUsage, err)
	}
	if fs.NArg() > 0 {
		return errors.Join(errUsage, fmt.Errorf("mod-test: unexpected argument %q", fs.Arg(0)))
	}
	return runModTest(osModClaude(), root, runtime.GOOS, os.Stdout)
}

// runModTest materialises goos's mods plugin, copies the mod's tests into its tests/ directory and
// runs `claude plugin test` on it. It fails when claude is absent, when the run fails, and when the
// run does not report every copied test file.
func runModTest(c modClaude, repoRoot, goos string, w io.Writer) (err error) {
	const task = "mod-test"
	bin, err := lookUpModClaude(c, task)
	if err != nil {
		return err
	}
	ws, err := newModWorkspace(goos)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, ws.close()) }()

	files, err := copyModTests(filepath.Join(repoRoot, filepath.FromSlash(modTestsDir)), filepath.Join(ws.plugin, "tests"))
	if err != nil {
		return err
	}

	env := modClaudeEnv(os.Environ(), ws.configDir)
	printClaudeVersion(c, env, bin, task, w)
	run := c.run(env, bin, "plugin", "test", ws.plugin)
	_, _ = w.Write(run.Stdout)
	_, _ = w.Write(run.Stderr)
	switch {
	case run.Err != nil:
		return fmt.Errorf("`claude plugin test` gave no verdict: %w", run.Err)
	case run.ExitCode != 0:
		return fmt.Errorf("`claude plugin test` failed (exit %d)", run.ExitCode)
	}

	m := modTestSummary.FindSubmatch(run.Stdout)
	if m == nil {
		return fmt.Errorf("`claude plugin test` exited 0 without its \"Ran N tests across M files\" "+
			"summary, so it is not known that the %d test file(s) ran", files)
	}
	tests, _ := strconv.Atoi(string(m[1]))
	ran, _ := strconv.Atoi(string(m[2]))
	if ran != files || tests == 0 {
		return fmt.Errorf("`claude plugin test` ran %d test(s) across %d file(s), but %d test file(s) "+
			"were copied in", tests, ran, files)
	}
	_, _ = fmt.Fprintf(w, "%s: %d test(s) across %d file(s) passed\n", task, tests, ran)
	return nil
}
