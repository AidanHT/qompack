package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
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

// newerSettingsVersionBody is a project config that declares settingsVersion 99 for the versioned
// block at the dotted path section, nested the way an operator would write it.
func newerSettingsVersionBody(t *testing.T, section string) string {
	t.Helper()
	var doc any = map[string]any{"settingsVersion": 99}
	parts := strings.Split(section, ".")
	for i := len(parts) - 1; i >= 0; i-- {
		doc = map[string]any{parts[i]: doc}
	}
	b, err := json.Marshal(doc)
	require.NoError(t, err)
	return string(b)
}

// violationsRecord is the project's state/config-violations.json path.
func violationsRecord(root string) string {
	return filepath.Join(paths.Of(root).State, configViolationsFile)
}

// TestHookCapture_NewerSettingsVersionWarnsWithoutLoud is finding F-C7-C49-2. A project left on a
// config written by a newer build (the plugin-downgrade case) had every hook log the versioned
// block's reset at LOUD: 18 lines in 30 s in the candidate 7 live lane, into a LOUD.log that is
// append-only and never rotated. The reset is the loader's designed answer to a downgrade, not a
// contract violation or a degradation transition, and config.Load reports the same reset at warn
// (LoadConfigAndReport), as troubleshooting §6's table says. Each hook therefore records it at warn
// in the day log and in state/config-violations.json (which doctor and self-test read), and
// LOUD.log stays clean of it. The daemon owns the loud report: one line per reload of a changed
// file or forced admin.reload.
//
// It runs once per versioned block (config.VersionedSections), so runtime.phase7 — which takes the
// same path — has its row, and a block added later gets one without an edit here.
func TestHookCapture_NewerSettingsVersionWarnsWithoutLoud(t *testing.T) {
	for _, s := range config.VersionedSections() {
		t.Run(s.Path, func(t *testing.T) {
			root := t.TempDir()
			day, loud := runPromptHooks(t, root, newerSettingsVersionBody(t, s.Path), captureConfigLoudHookRuns)

			require.Zero(t, linesWith(loud, "key="+s.Path),
				"no hook may write the settingsVersion reset to LOUD.log:\n%s", loud)
			require.Zero(t, linesWith(day, "level=loud", "key="+s.Path),
				"the day log carries the reset at warn, not loud:\n%s", day)
			require.Equal(t, captureConfigLoudHookRuns,
				linesWith(day, "level=warn", "key="+s.Path, "is newer than this build understands"),
				"every hook still records the reset at warn in the day log:\n%s", day)

			raw, err := os.ReadFile(violationsRecord(root))
			require.NoError(t, err, "the hook path still records the reset in state/config-violations.json")
			require.Contains(t, string(raw), `"Key": "`+s.Path+`"`)
		})
	}
}

// TestHookCapture_InvalidValueWarnsWithoutLoud is the rest of troubleshooting §6's table on the hook
// path, under D59's rule for a persistent configuration condition: the daemon reports it loudly once
// per start and once per reload of a changed file, and a hook logs it at warn. Before wave 20 an
// invalid value or a refused gated switch put one line per hook process into the never-rotated
// LOUD.log for as long as the file stayed as it was (11 lines from 11 hooks in the audit's probe),
// and those lines never reached status, which prints only the daemon's ring. Every hook still
// records the fallback in the day log and in state/config-violations.json.
func TestHookCapture_InvalidValueWarnsWithoutLoud(t *testing.T) {
	for _, tc := range []struct{ name, body, key string }{
		{"invalid value", `{"runtime":{"telemetry":{"enabled":true}}}`, "runtime.telemetry.enabled"},
		{
			"refused gated switch", `{"runtime":{"phase7":{"reuse":{"scopedCandidates":true}}}}`,
			"runtime.phase7.reuse.scopedCandidates",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			day, loud := runPromptHooks(t, root, tc.body, captureConfigLoudHookRuns)

			require.Zero(t, linesWith(loud, "key="+tc.key),
				"no hook may write a persistent fallback to LOUD.log:\n%s", loud)
			require.Zero(t, linesWith(day, "level=loud", "key="+tc.key),
				"the day log carries the fallback at warn, not loud:\n%s", day)
			require.Equal(t, captureConfigLoudHookRuns,
				linesWith(day, "level=warn", "invalid configuration value, using default", "key="+tc.key),
				"every hook still records the fallback at warn in the day log:\n%s", day)

			raw, err := os.ReadFile(violationsRecord(root))
			require.NoError(t, err, "the hook path still records the fallback")
			require.Contains(t, string(raw), `"Key": "`+tc.key+`"`)
		})
	}
}

// pinnedRecordTime is an instant no write in a test run can produce. A record whose mtime is set
// to it and still reads it afterwards has not been replaced: paths.WriteAtomic renames a new file
// over the old one, which carries the time it was written. Counting replacements this way needs no
// clock margin and no file-identity API (os.SameFile re-reads the identity by path on Windows).
var pinnedRecordTime = time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)

// pinRecord sets the record's mtime to pinnedRecordTime.
func pinRecord(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.Chtimes(violationsRecord(root), pinnedRecordTime, pinnedRecordTime))
}

// recordReplaced reports whether the record was rewritten since pinRecord.
func recordReplaced(t *testing.T, root string) bool {
	t.Helper()
	fi, err := os.Stat(violationsRecord(root))
	require.NoError(t, err)
	return !fi.ModTime().Equal(pinnedRecordTime)
}

// TestHookCapture_UnchangedViolationsAreNotRewritten is wave 20's B-A finding. While a condition
// persisted, every hook rewrote the same state/config-violations.json with paths.WriteAtomic: a
// staging file, a write, an fsync, a rename and a directory fsync per hook, about 19 ms on Windows,
// for bytes that had not changed. A hook now compares the record first and writes only a different
// list; a changed list is still written at once.
func TestHookCapture_UnchangedViolationsAreNotRewritten(t *testing.T) {
	root := t.TempDir()
	sv := `{"runtime":{"migration":{"settingsVersion":99}}}`
	runPromptHooks(t, root, sv, 1)
	before, err := os.ReadFile(violationsRecord(root))
	require.NoError(t, err)
	pinRecord(t, root)

	runPromptHooks(t, root, sv, captureConfigLoudHookRuns)
	require.False(t, recordReplaced(t, root), "an unchanged §11.3 list must not be rewritten")
	after, err := os.ReadFile(violationsRecord(root))
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))

	runPromptHooks(t, root, `{"runtime":{"migration":{"settingsVersion":99},"telemetry":{"enabled":true}}}`, 1)
	require.True(t, recordReplaced(t, root), "a changed §11.3 list is written")
	changed, err := os.ReadFile(violationsRecord(root))
	require.NoError(t, err)
	require.Contains(t, string(changed), `"Key": "runtime.telemetry.enabled"`)
	require.Contains(t, string(changed), `"Key": "runtime.migration"`)
}

// TestHookCapture_ResolvedConfigRemovesViolationsRecord is the stale-record finding. After
// troubleshooting §6's downgrade procedure, self-test's config.capture read ok while doctor's
// config.violations kept naming runtime.migration "(from state/config-violations.json)" for good:
// nothing ever removed the record. A hook whose load has no §11.3 violation now removes it, at the
// cost of one Lstat when there is nothing to remove, and doctor agrees with the live load.
func TestHookCapture_ResolvedConfigRemovesViolationsRecord(t *testing.T) {
	for _, tc := range []struct{ name, fixed string }{
		{"clean", `{}`},
		{"warnings only", `{"runtime":{"notAKey":1}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			runPromptHooks(t, root, `{"runtime":{"migration":{"settingsVersion":99}}}`, 1)
			_, err := os.Stat(violationsRecord(root))
			require.NoError(t, err, "the reset is recorded first")
			code, doc, errw := doctorJSON(t, root)
			require.Equal(t, ExitOK, code, "stderr=%s", errw)
			row := doctorFindRow(t, doc, "controls", "config.violations")
			require.Equal(t, "degraded", row["status"], "row=%v", row)
			// A block reset is not a leaf: doctor counts settings, as self-test's summary does.
			require.Equal(t, "1 setting(s) fell back to the default", row["observed"], "row=%v", row)
			require.Contains(t, row["detail"], "runtime.migration (from state/config-violations.json)")

			runPromptHooks(t, root, tc.fixed, 1)
			_, err = os.Lstat(violationsRecord(root))
			require.True(t, os.IsNotExist(err), "a load with no violation removes the record: %v", err)

			code, doc, errw = doctorJSON(t, root)
			require.Equal(t, ExitOK, code, "stderr=%s", errw)
			row = doctorFindRow(t, doc, "controls", "config.violations")
			require.Equal(t, "ok", row["status"], "doctor agrees with the live load: %v", row)
		})
	}
}

// TestHookCapture_NoRecordNoQompackStaysUntouched pins the clean path's other edge: a project with
// no .qompack is not one the hook path may create anything in, and removing a record that is not
// there creates nothing either.
func TestHookCapture_NoRecordNoQompackStaysUntouched(t *testing.T) {
	root := t.TempDir()
	reportCaptureConfig(root, t.TempDir(), nil, nil)
	_, err := os.Lstat(paths.Of(root).Dot)
	require.True(t, os.IsNotExist(err), "the clean path creates no .qompack: %v", err)
}

// TestLoadConfigAndReport_RecordFollowsTheLoad is the command path's half of the same record.
// LoadConfigAndReport skips an identical write like the hook path, and it removes the record only
// when nothing it or a hook would record is in force: config.Load reports a newer-settingsVersion
// reset as a keyed warning rather than a violation, so a command that removed the record on zero
// violations would erase the reset a hook had just recorded (self-test is the command troubleshooting
// §6 has an operator re-run after a downgrade).
func TestLoadConfigAndReport_RecordFollowsTheLoad(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(paths.Of(root).Logs, 0o700))
	env := config.Env{ProjectRoot: root, HomeDir: t.TempDir(), Getenv: noEnv}
	load := func(body string) {
		t.Helper()
		writeAdmissionConfig(t, root, body)
		_, _, err := LoadConfigAndReport(env, logging.Nop(), nil)
		require.NoError(t, err)
	}

	leaf := `{"runtime":{"telemetry":{"enabled":true}}}`
	load(leaf)
	pinRecord(t, root)
	load(leaf)
	require.False(t, recordReplaced(t, root), "an unchanged §11.3 list must not be rewritten")

	runPromptHooks(t, root, `{"runtime":{"migration":{"settingsVersion":99}}}`, 1)
	load(`{"runtime":{"migration":{"settingsVersion":99}}}`)
	raw, err := os.ReadFile(violationsRecord(root))
	require.NoError(t, err, "a command load must keep the reset a hook recorded")
	require.Contains(t, string(raw), `"Key": "runtime.migration"`)

	load(`{}`)
	_, err = os.Lstat(violationsRecord(root))
	require.True(t, os.IsNotExist(err), "a clean command load removes the record: %v", err)
}

// TestDaemonStart_ReportsAnUnchangedConfigOnce is the composition root's half of the re-Loud
// finding, through the real runDaemon. A daemon's startup LoadConfigAndReport is its one report of
// the configuration it starts on: each §11.3 violation Loud, each keyed warning at warn. Its first
// configuration check (here the session.start route, which runs it before answering) used to reload
// the same unchanged file and Loud every warning again, so the violation reached LOUD.log twice per
// daemon start and the unknown key, a warning everywhere else, once. runDaemon now stamps the file
// before its load and hands the stamp to the daemon (daemon.Options.CfgStamp).
func TestDaemonStart_ReportsAnUnchangedConfigOnce(t *testing.T) {
	root := bootstrapProject(t)
	writeReloadConfig(t, root, `{"runtime":{"telemetry":{"enabled":true},"notAKey":1}}`)
	stop := bootstrapDaemon(t, root)
	defer stop()

	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	client := ipc.NewClientWithOptions(addr, nopSpool{}, logging.Nop(), obs.New(testClock()),
		ipc.ClientOptions{
			ProjectRoot: root, ConnectDeadline: bootstrapCallDeadline, AckDeadline: bootstrapCallDeadline,
			Clock: testClock(),
		})
	defer func() { _ = client.Close() }()
	ev := &hookio.Event{
		HookEventName: hookio.EventSessionStart, SessionID: "sess-w20-start", Source: "startup",
		CWD: root, TranscriptPath: filepath.Join(root, "transcript.jsonl"),
	}
	req := ipc.Request{Op: ipc.OpSessionStart, Session: ev.SessionID, TS: 1, Reply: true, Event: ev}
	_, err = client.Send(context.Background(), req, bootstrapCallDeadline)
	require.NoError(t, err)
	stop() // releases the log handles, so LOUD.log can be read whole.

	loud := bootstrapLoudLog(t, root)
	require.Equal(t, 1, linesWith(loud, "key=runtime.telemetry.enabled"),
		"the violation is Loud once per daemon start:\n%s", loud)
	require.Zero(t, linesWith(loud, "daemon: config reload warning"),
		"the first check does not reload the file the daemon started on:\n%s", loud)
	require.Zero(t, linesWith(loud, "key=runtime.notAKey"),
		"an unchanged unknown key stays a warning at start:\n%s", loud)
}
