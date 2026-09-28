package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
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

// TestSelfTest_DaemonDisabledStartsNoDaemon pins docs/release.md §4's promise for
// runtime.daemon.enabled: false — "no resident process and no lock file" — against self-test, which
// used to call daemon.EnsureRunning whenever no daemon answered, whatever the configuration said.
// Env.Self names a path that does not exist, so an EnsureRunning attempt reaches SpawnDetached,
// fails fast without starting anything, and logs "spawn failed": the same deterministic proxy
// TestEnsureDaemonRunning_GatedOnDaemonEnabled uses for "EnsureRunning ran". CLAUDE_PLUGIN_ROOT is
// cleared so no attempt could stage a binary under any home directory.
func TestSelfTest_DaemonDisabledStartsNoDaemon(t *testing.T) {
	t.Cleanup(contract.ResetProducers)
	t.Setenv("CLAUDE_PLUGIN_ROOT", "")
	// A manually removed root, not t.TempDir(): the hook logger self-test writes through keeps its
	// log file open (see TestEnsureDaemonRunning_GatedOnDaemonEnabled), which would fail
	// t.TempDir()'s own cleanup on Windows.
	root, err := os.MkdirTemp("", "qompack-selftest-disabled-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	writeAdmissionConfig(t, root, `{"runtime":{"daemon":{"enabled":false}}}`)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), []Cmd{{Name: "self-test", Run: runSelfTest}},
		[]string{"qompack", "self-test", "--json"}, Env{
			Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}),
			Stdin:   bytes.NewReader(nil),
			Clock:   testClock(),
			HomeDir: t.TempDir(),
			Self:    filepath.Join(t.TempDir(), "does-not-exist"),
		}, &out, &errw)
	require.Equal(t, ExitOK, code, "stderr=%s stdout=%s", errw.String(), out.String())

	var report selfTestReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report), "stdout=%s", out.String())
	byID := map[string]selfTestCheck{}
	for _, c := range report.Checks {
		byID[c.ID] = c
	}
	for _, id := range []string{"daemon.reachable", "admin.ping"} {
		c, ok := byID[id]
		require.True(t, ok, "self-test must still report %s", id)
		require.True(t, c.OK, "%s must be skipped by configuration, not failed: %+v", id, c)
		require.Equal(t, contract.SevInfo, c.Severity, "%s: %+v", id, c)
		require.Contains(t, c.Observed, "skipped", "%s: %+v", id, c)
		require.Contains(t, c.Observed, "runtime.daemon.enabled", "%s: %+v", id, c)
	}

	run := paths.Of(root).Run
	for _, name := range []string{"daemon.lock", "spawn.lock"} {
		_, serr := os.Stat(filepath.Join(run, name))
		require.True(t, os.IsNotExist(serr), "runtime.daemon.enabled=false must never create %s (stat err=%v)",
			filepath.Join(run, name), serr)
	}
	logs, err := filepath.Glob(filepath.Join(paths.Of(root).Logs, "qompack-*.log"))
	require.NoError(t, err)
	for _, m := range logs {
		b, rerr := os.ReadFile(m)
		require.NoError(t, rerr)
		require.NotContains(t, string(b), "spawn failed",
			"runtime.daemon.enabled=false must never reach daemon.EnsureRunning/SpawnDetached")
	}
}

// TestSelfTest_DaemonDisabledButReachableWarns covers the other half of the disabled branch: a
// daemon that answers although runtime.daemon.enabled is false (started before the switch was set,
// or by hand) contradicts the configuration, so daemon.reachable warns instead of reading
// "skipped", and self-test still sends it nothing — admin.ping stays skipped by configuration.
func TestSelfTest_DaemonDisabledButReachableWarns(t *testing.T) {
	t.Cleanup(contract.ResetProducers)
	t.Setenv("CLAUDE_PLUGIN_ROOT", "")
	root, err := os.MkdirTemp("", "qompack-selftest-disabled-live-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	writeAdmissionConfig(t, root, `{"runtime":{"daemon":{"enabled":false}}}`)

	var mu sync.Mutex
	var ops []ipc.Op
	replyDaemon(t, root, func(req ipc.Request) *hookio.Output {
		mu.Lock()
		defer mu.Unlock()
		ops = append(ops, req.Op)
		return nil
	})

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), []Cmd{{Name: "self-test", Run: runSelfTest}},
		[]string{"qompack", "self-test", "--json"}, Env{
			Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}),
			Stdin:   bytes.NewReader(nil),
			Clock:   testClock(),
			HomeDir: t.TempDir(),
			Self:    filepath.Join(t.TempDir(), "does-not-exist"),
		}, &out, &errw)
	require.Equal(t, ExitOK, code, "a warning is not critical; stderr=%s stdout=%s", errw.String(), out.String())

	var report selfTestReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report), "stdout=%s", out.String())
	byID := map[string]selfTestCheck{}
	for _, c := range report.Checks {
		byID[c.ID] = c
	}
	reachable := byID["daemon.reachable"]
	require.False(t, reachable.OK, "a live daemon under a disabled configuration must not pass: %+v", reachable)
	require.Equal(t, contract.SevWarn, reachable.Severity, "%+v", reachable)
	require.Contains(t, reachable.Observed, "runtime.daemon.enabled is false", "%+v", reachable)

	ping := byID["admin.ping"]
	require.True(t, ping.OK, "%+v", ping)
	require.Equal(t, selfTestDaemonSkipped, ping.Observed, "%+v", ping)
	mu.Lock()
	defer mu.Unlock()
	require.Empty(t, ops, "self-test must send a disabled daemon no request, only a liveness dial")
}
