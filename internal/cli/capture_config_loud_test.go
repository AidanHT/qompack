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
// LOUD.log stays clean of it. The daemon owns the loud report: one line at each start
// (loadDaemonConfig) and one per reload of a changed file or forced admin.reload.
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

// TestRecordHolds_OnlyAPlainFileWithTheSameBytes pins the compare-first read's limits (wave 20
// review). It reads only a plain file whose size is the new encoding's, and no more than that, so a
// file of another size, a directory, or anything else at the record path is reported as not holding
// the list without a read, and syncViolationsRecord's atomic write replaces it. The unix-only
// TestHookCapture_FIFORecordDoesNotBlockTheHook covers the FIFO, which no Windows path can hold.
func TestRecordHolds_OnlyAPlainFileWithTheSameBytes(t *testing.T) {
	dir := t.TempDir()
	want := []byte("[\n  {\"Key\": \"a\"}\n]\n")
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, b, 0o600))
		return p
	}

	require.True(t, recordHolds(write("same", want), want))
	require.False(t, recordHolds(write("longer", append(append([]byte(nil), want...), ' ')), want))
	require.False(t, recordHolds(write("shorter", want[:len(want)-1]), want))
	other := append([]byte(nil), want...)
	other[len(other)-3] = '}'
	require.False(t, recordHolds(write("same size, other bytes", other), want))
	require.False(t, recordHolds(filepath.Join(dir, "missing"), want))
	sub := filepath.Join(dir, "a directory")
	require.NoError(t, os.Mkdir(sub, 0o700))
	require.False(t, recordHolds(sub, want))
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
// §6 has an operator re-run after a downgrade). It records the reset itself, after the leaves, so
// the two writers encode the same bytes and neither rewrites the other's record (wave 20 review).
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

	// Both writers encode the same list: the leaves in config's order, then each reset in
	// VersionedSections order. A command load that recorded only the leaves dropped a hook's resets,
	// and the next hook wrote them back, two durable writes of the same state per command run.
	both := `{"runtime":{"migration":{"settingsVersion":99},"phase7":{"settingsVersion":99},` +
		`"telemetry":{"enabled":true}},"scheduler":{"softFloorPct":9.5},"eval":{"minSessions":-3}}`
	runPromptHooks(t, root, both, 1)
	hookRecord, err := os.ReadFile(violationsRecord(root))
	require.NoError(t, err)
	pinRecord(t, root)
	load(both)
	require.False(t, recordReplaced(t, root),
		"a command load writes the same list a hook recorded, so it is not rewritten")
	runPromptHooks(t, root, both, 1)
	require.False(t, recordReplaced(t, root), "and the next hook does not rewrite it back")
	raw, err = os.ReadFile(violationsRecord(root))
	require.NoError(t, err)
	require.Equal(t, string(hookRecord), string(raw))
	for _, key := range []string{"runtime.telemetry.enabled", "runtime.migration", "runtime.phase7"} {
		require.Contains(t, string(raw), `"Key": "`+key+`"`)
	}

	load(`{}`)
	_, err = os.Lstat(violationsRecord(root))
	require.True(t, os.IsNotExist(err), "a clean command load removes the record: %v", err)
}

// TestDaemonStart_ReportsAnUnchangedConfigOnce is the composition root's half of the re-Loud
// finding, through the real runDaemon. A daemon's startup load (loadDaemonConfig) is its one report
// of the configuration it starts on: each §11.3 violation and each newer-settingsVersion reset Loud,
// every other keyed warning at warn. Its first configuration check (here the session.start route,
// which runs it before answering) used to reload the same unchanged file and Loud every warning
// again, so the violation reached LOUD.log twice per daemon start and the unknown key, a warning
// everywhere else, once. runDaemon now stamps the file before its load and hands the stamp to the
// daemon (daemon.Options.CfgStamp). The reset row is the review finding on that fix: with the
// duplicate gone, a reset was Loud nowhere at all, so after a restart on an unchanged downgraded
// file status showed nothing about it, while D59 has the daemon report it loudly once per start.
func TestDaemonStart_ReportsAnUnchangedConfigOnce(t *testing.T) {
	root := bootstrapProject(t)
	writeReloadConfig(t, root,
		`{"runtime":{"telemetry":{"enabled":true},"notAKey":1,"migration":{"settingsVersion":99}}}`)
	// The Loud ring is process-wide and outlives this test (and each -count repetition), so the
	// lines this daemon adds are the ones after a marker of its own.
	marker := "w20 daemon start marker " + t.Name()
	logging.Nop().Loud(marker)
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
	require.Equal(t, 1, linesWith(loud, "configuration block reset to defaults", "key=runtime.migration",
		"is newer than this build understands"),
		"a newer-settingsVersion reset is the start's other loud report, once:\n%s", loud)
	// status prints the daemon process's Loud ring (daemon/metrics.go, loudTail), which this
	// in-process daemon shares with the test.
	ring := logging.LastLoud()
	since := -1
	for i, line := range ring {
		if strings.Contains(line, marker) {
			since = i
		}
	}
	require.GreaterOrEqual(t, since, 0, "the marker is still in the ring:\n%s", strings.Join(ring, "\n"))
	require.Equal(t, 1, linesWith(strings.Join(ring[since+1:], "\n"),
		"configuration block reset to defaults", "key=runtime.migration"),
		"the reset reaches the ring status reads its recent loud lines from")
}

// TestLoadConfigAndReport_WrongTypeBlockIsNotAReset is the review finding on round 2's writer
// match. config.Load's merge also keys a warning by a versioned block's own path when the block is
// not an object ("expected an object"), and a reset was identified by that key alone. A command
// load then recorded the warning as a §11.3 setting, every hook (LoadForCapture, which classifies it
// correctly) removed the record again, doctor's verdict depended on which ran last, and each daemon
// start Louded "configuration block reset to defaults" for a block nothing reset. A reset is the
// warning config.Load marks as one (config.Warning.VersionedReset); a wrong-type block is an
// ordinary keyed warning, which none of the three loads records and the daemon logs at warn.
func TestLoadConfigAndReport_WrongTypeBlockIsNotAReset(t *testing.T) {
	for _, s := range config.VersionedSections() {
		t.Run(s.Path, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(paths.Of(root).Logs, 0o700))
			parts := strings.Split(s.Path, ".")
			var doc any = 5
			for i := len(parts) - 1; i >= 0; i-- {
				doc = map[string]any{parts[i]: doc}
			}
			b, err := json.Marshal(doc)
			require.NoError(t, err)
			body := string(b)
			writeAdmissionConfig(t, root, body)
			env := config.Env{ProjectRoot: root, HomeDir: t.TempDir(), Getenv: noEnv}
			noRecord := func(msg string) {
				t.Helper()
				_, statErr := os.Lstat(violationsRecord(root))
				require.True(t, os.IsNotExist(statErr), "%s: %v", msg, statErr)
			}

			log := &bootstrapLogger{}
			_, _, err = loadDaemonConfig(env, log, nil)
			require.NoError(t, err)
			require.Zero(t, log.countLouds("configuration block reset to defaults"),
				"a daemon start does not report a block it did not reset: %v", log.louds)
			require.Contains(t, log.warns, "configuration warning", "the wrong type stays a warning")
			noRecord("a daemon start records no setting for a wrong-type block")

			_, _, err = LoadConfigAndReport(env, logging.Nop(), nil)
			require.NoError(t, err)
			noRecord("a command load records no setting for a wrong-type block")

			runPromptHooks(t, root, body, 1)
			noRecord("a hook records no setting for a wrong-type block")
		})
	}
}
