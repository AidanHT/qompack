package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeModClaude is a scripted claude CLI for mod-test and mod-validate. Every invocation is checked
// for the isolation both tasks promise: a private, existing CLAUDE_CONFIG_DIR that is not the
// user's own, and the auto-updater off. answer handles every call but `--version`.
type fakeModClaude struct {
	t      *testing.T
	calls  [][]string
	envs   [][]string
	answer func(args []string) claudeRun
}

func (f *fakeModClaude) claude() modClaude {
	return modClaude{
		lookPath: func() (string, error) { return "claude-fake", nil },
		run: func(env []string, bin string, args ...string) claudeRun {
			f.t.Helper()
			require.Equal(f.t, "claude-fake", bin)
			f.calls = append(f.calls, args)
			f.envs = append(f.envs, env)
			requireIsolatedClaudeEnv(f.t, env)
			if len(args) == 1 && args[0] == "--version" {
				return claudeRun{Stdout: []byte("2.1.300 (Claude Code)\n")}
			}
			return f.answer(args)
		},
	}
}

// requireIsolatedClaudeEnv asserts env points claude at a fresh private configuration directory.
func requireIsolatedClaudeEnv(t *testing.T, env []string) {
	t.Helper()
	cfg := envValues(env, "CLAUDE_CONFIG_DIR")
	require.Len(t, cfg, 1, "exactly one CLAUDE_CONFIG_DIR reaches claude")
	require.Equal(t, []string{"1"}, envValues(env, "DISABLE_AUTOUPDATER"))
	fi, err := os.Stat(cfg[0])
	require.NoError(t, err, "the private configuration directory exists")
	require.True(t, fi.IsDir())
	if home, herr := os.UserHomeDir(); herr == nil {
		require.False(t, strings.EqualFold(filepath.Clean(cfg[0]), filepath.Join(home, ".claude")),
			"claude never sees the user's own configuration directory")
	}
}

// envValues returns every value env gives name, matching names case-insensitively as Windows does.
func envValues(env []string, name string) []string {
	var out []string
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(k, name) {
			out = append(out, v)
		}
	}
	return out
}

func TestModClaudeEnv_ReplacesTheConfigDirAndDisablesTheUpdater(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"CLAUDE_CONFIG_DIR=/home/u/.claude",
		"claude_config_dir=/home/u/other",
		"DISABLE_AUTOUPDATER=0",
		"HOME=/home/u",
	}
	got := modClaudeEnv(base, "/tmp/private")
	require.Equal(t, []string{"/tmp/private"}, envValues(got, "CLAUDE_CONFIG_DIR"))
	require.Equal(t, []string{"1"}, envValues(got, "DISABLE_AUTOUPDATER"))
	require.Equal(t, []string{"/usr/bin"}, envValues(got, "PATH"))
	require.Equal(t, []string{"/home/u"}, envValues(got, "HOME"))
	require.Equal(t, "CLAUDE_CONFIG_DIR=/home/u/.claude", base[1], "the caller's slice is not modified")
}

// modTestFiles lists the TypeScript tests under modsrc/tests, as slash paths relative to it.
func modTestFiles(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(testModuleRoot(t), filepath.FromSlash(modTestsDir))
	var out []string
	require.NoError(t, filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(rel))
		return relErr
	}))
	sort.Strings(out)
	require.NotEmpty(t, out)
	return out
}

func TestRunModTest_FailsWhenClaudeIsAbsent(t *testing.T) {
	ran := false
	c := modClaude{
		lookPath: func() (string, error) { return "", errors.New("exec: \"claude\": executable file not found") },
		run: func([]string, string, ...string) claudeRun {
			ran = true
			return claudeRun{}
		},
	}
	var out bytes.Buffer
	err := runModTest(c, testModuleRoot(t), "windows", &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not on PATH")
	require.False(t, ran)
}

// TestRunModTest_RunsThePluginTestsOnTheMaterialisedModsPlugin checks what claude is pointed at: the
// host target's --mods plugin tree with modsrc/tests copied to tests/, and nothing else.
func TestRunModTest_RunsThePluginTestsOnTheMaterialisedModsPlugin(t *testing.T) {
	root := testModuleRoot(t)
	tests := modTestFiles(t)
	var pluginDir string
	f := &fakeModClaude{t: t}
	f.answer = func(args []string) claudeRun {
		require.Len(t, args, 3)
		require.Equal(t, []string{"plugin", "test"}, args[:2])
		pluginDir = args[2]

		want, err := pluginTreeFiles(modPluginVersion, "windows", true, false)
		require.NoError(t, err)
		wantTree := make([]string, 0, len(want)+len(tests))
		for rel, b := range want {
			got, readErr := os.ReadFile(filepath.Join(pluginDir, filepath.FromSlash(rel)))
			require.NoError(t, readErr)
			require.Equal(t, string(b), string(got), rel)
			wantTree = append(wantTree, rel)
		}
		for _, rel := range tests {
			src, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(modTestsDir), filepath.FromSlash(rel)))
			require.NoError(t, readErr)
			got, readErr := os.ReadFile(filepath.Join(pluginDir, "tests", filepath.FromSlash(rel)))
			require.NoError(t, readErr)
			require.Equal(t, string(src), string(got), rel)
			wantTree = append(wantTree, "tests/"+rel)
		}
		sort.Strings(wantTree)
		require.Equal(t, wantTree, listBundleTree(t, pluginDir), "the plugin tree plus the tests, nothing else")
		require.Contains(t, wantTree, "hooks/register.ts")

		return claudeRun{Stdout: []byte("(pass) a [1ms]\n\n 20 pass\n 0 fail\nRan 20 tests across " +
			strconv.Itoa(len(tests)) + " files. [0.31s]\n")}
	}

	var out bytes.Buffer
	require.NoError(t, runModTest(f.claude(), root, "windows", &out))
	require.Len(t, f.calls, 2, "claude --version, then claude plugin test")
	require.Contains(t, out.String(), "20 pass", "claude's own output is shown")
	require.Contains(t, out.String(), "2.1.300")
	_, err := os.Stat(pluginDir)
	require.True(t, os.IsNotExist(err), "the temporary plugin is removed afterwards: %v", err)
	cfg := envValues(f.envs[0], "CLAUDE_CONFIG_DIR")[0]
	_, err = os.Stat(cfg)
	require.True(t, os.IsNotExist(err), "the private configuration directory is removed afterwards: %v", err)
}

func TestRunModTest_FailsWhenTheTestsFail(t *testing.T) {
	f := &fakeModClaude{t: t, answer: func([]string) claudeRun {
		return claudeRun{ExitCode: 1, Stdout: []byte(" 19 pass\n 1 fail\nRan 20 tests across 2 files. [0.31s]\n")}
	}}
	err := runModTest(f.claude(), testModuleRoot(t), "windows", &bytes.Buffer{})
	require.ErrorContains(t, err, "exit 1")
}

func TestRunModTest_FailsWhenClaudeCouldNotRun(t *testing.T) {
	f := &fakeModClaude{t: t, answer: func([]string) claudeRun {
		return claudeRun{ExitCode: -1, Err: errors.New("boom")}
	}}
	err := runModTest(f.claude(), testModuleRoot(t), "windows", &bytes.Buffer{})
	require.ErrorContains(t, err, "boom")
}

// TestRunModTest_RefusesARunThatMissedTestFiles keeps a pass meaningful: claude must report running
// every test file mod-test copied in, so a discovery change cannot turn the task into a no-op.
func TestRunModTest_RefusesARunThatMissedTestFiles(t *testing.T) {
	for name, stdout := range map[string]string{
		"fewer files": " 3 pass\n 0 fail\nRan 3 tests across 1 file. [0.1s]\n",
		"no summary":  "all good\n",
		"zero tests":  " 0 pass\n 0 fail\nRan 0 tests across 0 files. [0.1s]\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeModClaude{t: t, answer: func([]string) claudeRun { return claudeRun{Stdout: []byte(stdout)} }}
			err := runModTest(f.claude(), testModuleRoot(t), "windows", &bytes.Buffer{})
			require.Error(t, err)
		})
	}
}

// TestRunModTest_RequiresZeroFailuresInTheSummary keeps the verdict off the exit code alone: the
// summary must report a "0 fail" count, and any skipped or todo test fails the task, because a test
// that did not run proves nothing.
func TestRunModTest_RequiresZeroFailuresInTheSummary(t *testing.T) {
	ran := "Ran 20 tests across " + strconv.Itoa(len(modTestFiles(t))) + " files. [0.3s]\n"
	for name, stdout := range map[string]string{
		"a failure":       " 19 pass\n 1 fail\n" + ran,
		"no fail count":   " 20 pass\n" + ran,
		"a skipped test":  " 18 pass\n 2 skip\n 0 fail\n" + ran,
		"a todo test":     " 19 pass\n 1 todo\n 0 fail\n" + ran,
		"two fail counts": " 20 pass\n 0 fail\n 3 fail\n" + ran,
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeModClaude{t: t, answer: func([]string) claudeRun { return claudeRun{Stdout: []byte(stdout)} }}
			err := runModTest(f.claude(), testModuleRoot(t), "windows", &bytes.Buffer{})
			require.Error(t, err)
		})
	}

	f := &fakeModClaude{t: t, answer: func([]string) claudeRun {
		return claudeRun{Stdout: []byte(" 20 pass\n 0 skip\n 0 fail\n" + ran)}
	}}
	require.NoError(t, runModTest(f.claude(), testModuleRoot(t), "windows", &bytes.Buffer{}),
		"zero skips are not a failure")
}
