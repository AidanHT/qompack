package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/paths"
)

// TestSelfTest_ExitsZeroOnHealthyProject asserts a freshly created project — no daemon required
// (selfPath() is "" under go test, so self-test's own daemon-reachable check degrades to SevWarn,
// never SevCritical) — reports mode "full" and exits 0, matching TestE2ESelfTestExitsZeroOnHealthy.
func TestSelfTest_ExitsZeroOnHealthyProject(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(contract.ResetProducers)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), []Cmd{{Name: "self-test", Run: runSelfTest}},
		[]string{"qompack", "self-test", "--json"}, Env{
			Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": dir}),
			Stdin:   bytes.NewReader(nil),
			Clock:   testClock(),
			HomeDir: t.TempDir(),
		}, &out, &errw)

	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())

	var report selfTestReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report), "stdout=%s", out.String())
	require.Equal(t, "full", report.Mode)
	require.Equal(t, ExitOK, report.Exit)
	require.NotEmpty(t, report.Checks)
}

// TestSelfTest_ExitsNonZeroOnCritical pre-seeds state/history.json with two consecutive sessions'
// worth of "no terminal-hook marker found" (the real precondition checkSessionStartFires's own
// Check requires — §12.1: "absence across two sessions"), so self-test's live RunAll against that
// persisted History genuinely fails session_start.fires, and asserts self-test exits 1 naming it —
// matching TestE2ESelfTestExitsNonZeroOnCritical.
func TestSelfTest_ExitsNonZeroOnCritical(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(contract.ResetProducers)

	require.NoError(t, paths.EnsureLayout(paths.Of(dir)))
	h := &contract.SessionHistory{
		Version:             1,
		SessionCount:        2,
		LastSessionID:       "some-other-session",
		StartsWithoutMarker: 1, // one more absent start (a different session id) reaches the >=2 fail threshold.
	}
	require.NoError(t, contract.SaveHistory(contract.HistoryPath(dir), h))

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), []Cmd{{Name: "self-test", Run: runSelfTest}},
		[]string{"qompack", "self-test", "--json"}, Env{
			Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": dir}),
			Stdin:   bytes.NewReader(nil),
			Clock:   testClock(),
			HomeDir: t.TempDir(),
		}, &out, &errw)

	require.Equal(t, ExitError, code)

	var report selfTestReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report), "stdout=%s", out.String())
	require.Equal(t, ExitError, report.Exit)

	foundCritical := false
	for _, c := range report.Checks {
		if c.ID == string(contract.CSessionStartFires) {
			require.False(t, c.OK)
			require.Equal(t, contract.SevCritical, c.Severity)
			foundCritical = true
		}
	}
	require.True(t, foundCritical, "self-test must name the failed assertion")
}

// TestSelfTest_TableModeWritesAReadableSummary asserts the non-JSON path renders a table naming
// every check and the overall mode.
func TestSelfTest_TableModeWritesAReadableSummary(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(contract.ResetProducers)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), []Cmd{{Name: "self-test", Run: runSelfTest}},
		[]string{"qompack", "self-test"}, Env{
			Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": dir}),
			Stdin:   bytes.NewReader(nil),
			Clock:   testClock(),
			HomeDir: t.TempDir(),
		}, &out, &errw)

	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())
	require.Contains(t, out.String(), "config.load")
	require.Contains(t, out.String(), "mode: full")
}

// TestSelfTest_RegisteredInAll asserts self-test is wired into the real dispatch table (not just
// the ad-hoc single-Cmd tables the tests above use), and that its Hook flag is unset — §2.3
// reserves the always-exit-0 guarantee for hooks, and self-test must never get it.
func TestSelfTest_RegisteredInAll(t *testing.T) {
	for _, c := range All() {
		if c.Name == "self-test" {
			require.False(t, c.Hook, "self-test must not be a Hook command — it is the one subcommand permitted a non-zero exit")
			return
		}
	}
	t.Fatal("self-test is not registered in All()")
}
