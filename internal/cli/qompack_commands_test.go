package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// rootedEnv returns an Env whose project root is dir, so a test never resolves the real tree.
func rootedEnv(dir string, stdin string) Env {
	return Env{
		Getenv: func(k string) string {
			if k == "QOMPACK_PROJECT_ROOT" {
				return dir
			}
			return ""
		},
		Stdin: strings.NewReader(stdin),
		Clock: testClock(),
	}
}

// TestExitCodes_MatchTheCLITable pins the two exit-code tables together.
//
// internal/commands declares its own ExitOK/ExitError/ExitUsage because the dependency runs
// cli -> commands and it cannot import back. Two tables that must agree and are never compared
// are two tables that eventually do not.
func TestExitCodes_MatchTheCLITable(t *testing.T) {
	t.Parallel()

	require.Equal(t, ExitOK, commands.ExitOK)
	require.Equal(t, ExitError, commands.ExitError)
	require.Equal(t, ExitUsage, commands.ExitUsage)
}

// TestSlashCommands_AreDiscoverable is the installed-discoverability check: every §7.5 command the
// manifest ships is reachable as the subcommand its markdown shells out to, and that subcommand is
// an ordinary one.
//
// There is no exception. /qompack:checkpoint used to be one: it shelled out to `qompack checkpoint`,
// which is the PreCompact hook entry point — it reads a hook event from stdin and exits 0 whatever
// happens — so the command wrote nothing and reported nothing while its description said "Write an
// immutable checkpoint now". A shipped command whose route is a hook is a command that silently
// does nothing, so the manifest may not ship one.
func TestSlashCommands_AreDiscoverable(t *testing.T) {
	t.Parallel()

	byName := map[string]Cmd{}
	for _, c := range All() {
		byName[c.Name] = c
	}

	for _, doc := range pluginmanifest.Default("0.0.0-test").Commands {
		got, ok := byName[doc.Subcommand]
		require.True(t, ok, "/qompack:%s shells out to `qompack %s`, which is not registered",
			doc.Name, doc.Subcommand)

		require.False(t, got.Hook, "/qompack:%s shells out to `qompack %s`, which is a hook entry "+
			"point: it reads a hook event from stdin and exits 0, so the command would do nothing",
			doc.Name, doc.Subcommand)
		require.Equal(t, doc.Description, got.Summary,
			"%s: the registered summary must be the installed description", doc.Subcommand)
	}
}

// TestSlashCommands_AppearInHelp keeps `qompack help` honest about the binary's surface.
func TestSlashCommands_AppearInHelp(t *testing.T) {
	t.Parallel()

	var out, errw bytes.Buffer
	require.Equal(t, ExitOK, Dispatch(context.Background(), All(), []string{"qompack", "help"},
		Env{Getenv: noEnv, Clock: testClock()}, &out, &errw))

	help := out.String()
	for _, name := range []string{"status", "recall", "pin", "why", "dropped", "eval"} {
		require.Contains(t, help, name)
	}
	require.NotContains(t, help, "(SP-14)", "the not-implemented markers are gone for these names")
}

// TestSlashCommands_StatusSucceedsWithoutADaemon: status reports what it could not observe, which
// is an answer rather than a failure, so it exits 0 and prints the report.
func TestSlashCommands_StatusSucceedsWithoutADaemon(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), []string{"qompack", "status"},
		rootedEnv(dir, ""), &out, &errw)

	require.Equal(t, ExitOK, code)
	require.Contains(t, out.String(), "qompack status")
	require.Contains(t, out.String(), "unavailable")
	require.Contains(t, out.String(), "hooks — per-entry-point latency")
}

// TestSlashCommands_ReadOnlyCommandsCreateNoProject is the "no status request mutates user
// configuration" rule, checked where it actually bites: a person running `qompack status` in a
// directory that has never been used with Qompack must not find one there afterwards.
func TestSlashCommands_ReadOnlyCommandsCreateNoProject(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"qompack", "status"},
		{"qompack", "pin", "--list"},
		{"qompack", "dropped"},
		{"qompack", "recall", "anything"},
		{"qompack", "eval"},
	} {
		dir := t.TempDir()
		var out, errw bytes.Buffer
		Dispatch(context.Background(), All(), args, rootedEnv(dir, ""), &out, &errw)

		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Empty(t, entries, "%v left %d entries behind in a directory it only read", args, len(entries))

		_, statErr := os.Stat(filepath.Join(dir, ".qompack"))
		require.True(t, os.IsNotExist(statErr), "%v created a .qompack directory", args)
	}
}

// TestSlashCommands_UsageErrorsExitTwo carries the frontends' usage classification through
// Dispatch's own mapping.
func TestSlashCommands_UsageErrorsExitTwo(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"qompack", "recall", "--not-a-flag"},
		{"qompack", "recall"},
		{"qompack", "why"},
		{"qompack", "dropped", "yesterday"},
	} {
		dir := t.TempDir()
		var out, errw bytes.Buffer
		require.Equal(t, ExitUsage,
			Dispatch(context.Background(), All(), args, rootedEnv(dir, ""), &out, &errw), "%v", args)
	}
}

// TestSlashCommands_UnavailableExitsOne keeps "I could not reach anything" a failure for the
// commands whose whole job is to fetch something.
func TestSlashCommands_UnavailableExitsOne(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var out, errw bytes.Buffer
	require.Equal(t, ExitError,
		Dispatch(context.Background(), All(), []string{"qompack", "eval"}, rootedEnv(dir, ""), &out, &errw))
	require.Contains(t, errw.String(), "eval")
}

// TestSlashCommands_HelpExitsZero: asking what a command does is not a mistake.
func TestSlashCommands_HelpExitsZero(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"status", "recall", "pin", "why", "dropped", "eval"} {
		dir := t.TempDir()
		var out, errw bytes.Buffer
		require.Equal(t, ExitOK, Dispatch(context.Background(), All(),
			[]string{"qompack", name, "--help"}, rootedEnv(dir, ""), &out, &errw), name)
		require.Contains(t, out.String(), name)
	}
}

// TestSlashCommands_CheckpointRemainsTheHook pins that `qompack checkpoint` is the PreCompact hook
// and nothing else, now that no slash command shells out to it.
//
// The PreCompact hook must keep its exit-0-always contract: a non-zero exit from it surfaces noise
// and can block the turn. The shipped hooks.json still invokes exactly this name, so it must resolve
// to exactly one entry, the hook.
func TestSlashCommands_CheckpointRemainsTheHook(t *testing.T) {
	t.Parallel()

	var found int
	for _, c := range All() {
		if c.Name == "checkpoint" {
			found++
			require.True(t, c.Hook)
		}
	}
	require.Equal(t, 1, found, "checkpoint must resolve to exactly one entry, the hook")

	dir := t.TempDir()
	var out, errw bytes.Buffer
	require.Equal(t, ExitOK, Dispatch(context.Background(), All(),
		[]string{"qompack", "checkpoint"}, rootedEnv(dir, "not json at all"), &out, &errw),
		"a hook exits 0 whatever happens inside it")
}

// TestSlashCommands_EvalImportStillResolves keeps the two-word verb reachable now that a one-word
// `eval` exists: Dispatch matches two-word names first, and this is what proves it still does.
func TestSlashCommands_EvalImportStillResolves(t *testing.T) {
	t.Parallel()

	cmd, rest, ok := match(All(), []string{"eval", "import", "--help"})
	require.True(t, ok)
	require.Equal(t, "eval import", cmd.Name)
	require.Equal(t, []string{"--help"}, rest)

	bare, rest, ok := match(All(), []string{"eval", "--json"})
	require.True(t, ok)
	require.Equal(t, "eval", bare.Name)
	require.Equal(t, []string{"--json"}, rest)
}

// TestSlashCommands_EvalReportsTheLatestLiveRun is the C5.4 wiring end to end: in a project whose
// dist/live-eval holds a run `devtool live-eval` wrote — here the committed pilot1 run — `qompack
// eval --json` reads it and reports it, qualified as a non-confirmatory, agent-executed run, instead
// of exiting 1 because no artifact provider was installed. It only reads.
func TestSlashCommands_EvalReportsTheLatestLiveRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	run := filepath.Join(dir, "dist", "live-eval", "pilot1-plugin-dir")
	require.NoError(t, os.MkdirAll(run, 0o755))
	pilot := filepath.Join("..", "..", "testdata", "eval", "runs", "pilot1-plugin-dir")
	for _, f := range []string{"plan.json", "summary.json"} {
		raw, err := os.ReadFile(filepath.Join(pilot, f))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(run, f), raw, 0o600))
	}

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), []string{"qompack", "eval", "--json"}, rootedEnv(dir, ""), &out, &errw)
	require.Equal(t, ExitOK, code, "stderr: %s", errw.String())
	env, err := commands.DecodeEnvelope(out.Bytes())
	require.NoError(t, err)
	var rep commands.EvalReport
	require.NoError(t, json.Unmarshal(env.Data, &rep))
	require.NotNil(t, rep.Live)
	require.False(t, rep.Live.Confirmatory)
	require.Contains(t, rep.Live.Qualification, "agent-executed")
	require.Equal(t, commands.VerdictInconclusive, rep.Verdict)

	_, statErr := os.Stat(filepath.Join(dir, ".qompack"))
	require.True(t, os.IsNotExist(statErr), "reading an evaluation created a .qompack directory")
}
