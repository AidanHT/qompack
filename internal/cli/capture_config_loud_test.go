package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
)

// captureConfigLoudHookRuns is how many hooks each case runs: enough that a per-hook LOUD line
// is told apart from a single one.
const captureConfigLoudHookRuns = 5

// runPromptHooks runs the real `observe prompt` hook n times in root (no daemon: Env.Executable is
// empty, so lazy spawn is off and every delivery lands in the spool) and returns the day log and
// LOUD.log contents the hooks left behind.
func runPromptHooks(t *testing.T, root, configBody string, n int) (dayLog, loudLog string) {
	t.Helper()
	writeAdmissionConfig(t, root, configBody)
	l := paths.Of(root)
	require.NoError(t, os.MkdirAll(l.Logs, 0o700), "a project that has already run has a logs/")
	t.Chdir(root)
	env := Env{Getenv: noEnv, Clock: testClock(), HomeDir: t.TempDir()}
	for i := 0; i < n; i++ {
		var out, errw bytes.Buffer
		env.Stdin = bytes.NewReader(entryPayload(t, hookio.EventUserPromptSubmit, root))
		require.Equal(t, ExitOK, Dispatch(context.Background(), All(), []string{"qompack", "observe", "prompt"},
			env, &out, &errw), "stderr=%s", errw.String())
	}
	days, err := filepath.Glob(filepath.Join(l.Logs, "qompack-*.log"))
	require.NoError(t, err)
	var b strings.Builder
	for _, d := range days {
		raw, readErr := os.ReadFile(d)
		require.NoError(t, readErr)
		b.Write(raw)
	}
	loud, err := os.ReadFile(filepath.Join(l.Logs, "LOUD.log"))
	if err != nil {
		require.True(t, os.IsNotExist(err), "LOUD.log unreadable: %v", err)
	}
	return b.String(), string(loud)
}

// linesWith counts the lines of log that contain every one of parts.
func linesWith(log string, parts ...string) int {
	n := 0
	for _, line := range strings.Split(log, "\n") {
		all := line != ""
		for _, p := range parts {
			all = all && strings.Contains(line, p)
		}
		if all {
			n++
		}
	}
	return n
}

// TestHookCapture_NewerSettingsVersionWarnsWithoutLoud is finding F-C7-C49-2. A project left on a
// config written by a newer build (the plugin-downgrade case) had every hook log the versioned
// block's reset at LOUD: 18 lines in 30 s in the candidate 7 live lane, into a LOUD.log that is
// append-only and never rotated. The reset is the loader's designed answer to a downgrade, not a
// contract violation or a degradation transition, and config.Load reports the same reset at warn
// (LoadConfigAndReport), as troubleshooting §6's table says. Each hook therefore records it at warn
// in the day log and in state/config-violations.json (which doctor and self-test read), and
// LOUD.log stays clean of it. The daemon owns the loud report: one line at each daemon's first
// config check after start, and one per reload of a changed file or forced admin.reload.
func TestHookCapture_NewerSettingsVersionWarnsWithoutLoud(t *testing.T) {
	root := t.TempDir()
	day, loud := runPromptHooks(t, root, `{"runtime":{"migration":{"settingsVersion":99}}}`,
		captureConfigLoudHookRuns)

	require.Zero(t, linesWith(loud, "key=runtime.migration"),
		"no hook may write the settingsVersion reset to LOUD.log:\n%s", loud)
	require.Zero(t, linesWith(day, "level=loud", "key=runtime.migration"),
		"the day log carries the reset at warn, not loud:\n%s", day)
	require.Equal(t, captureConfigLoudHookRuns,
		linesWith(day, "level=warn", "key=runtime.migration", "is newer than this build understands"),
		"every hook still records the reset at warn in the day log:\n%s", day)

	raw, err := os.ReadFile(filepath.Join(paths.Of(root).State, configViolationsFile))
	require.NoError(t, err, "the hook path still records the reset in state/config-violations.json")
	require.Contains(t, string(raw), `"Key": "runtime.migration"`)
}

// TestHookCapture_InvalidValueStaysLoud is the other row of troubleshooting §6's table, which this
// fix leaves as it was: an invalid value that fell back to its default is a §11.3 violation, logged
// at loud by the hook path.
func TestHookCapture_InvalidValueStaysLoud(t *testing.T) {
	root := t.TempDir()
	_, loud := runPromptHooks(t, root, `{"runtime":{"telemetry":{"enabled":true}}}`, 1)
	require.Equal(t, 1, linesWith(loud, "level=loud", "key=runtime.telemetry.enabled"),
		"an invalid value is still a LOUD line:\n%s", loud)
}
