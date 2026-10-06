package cli

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// TestDoctor_NewerSettingsVersionCountsWithoutARecord is audit 2's finding #16. doctor's
// config.violations row built its own load's list with config.ViolationsFromWarnings, which drops a
// newer-settingsVersion reset, so the row counted a reset only once a hook or a command had written
// state/config-violations.json. In one doctor run on a project nothing had loaded yet,
// config.violations read "none" while config.capture, in the same report, named the reset. The row
// now selects what the writers record (recordedViolations), so its verdict no longer depends on
// whether a writer has run, and it counts the same settings config.capture does.
func TestDoctor_NewerSettingsVersionCountsWithoutARecord(t *testing.T) {
	for _, s := range config.VersionedSections() {
		t.Run(s.Path, func(t *testing.T) {
			root := t.TempDir()
			writeAdmissionConfig(t, root, newerSettingsVersionBody(t, s.Path))

			code, doc, errw := doctorJSON(t, root)
			require.Equal(t, ExitOK, code, "stderr=%s", errw)
			_, err := os.Lstat(violationsRecord(root))
			require.True(t, os.IsNotExist(err), "doctor writes no record: %v", err)

			row := doctorFindRow(t, doc, "controls", "config.violations")
			require.Equal(t, "degraded", row["status"], "row=%v", row)
			require.Equal(t, "1 setting(s) fell back to the default", row["observed"], "row=%v", row)
			require.Equal(t, s.Path+" (block reset: newer settingsVersion)", row["detail"], "row=%v", row)

			capture := doctorFindRow(t, doc, "recording", "config.capture")
			require.Equal(t, "capture continues: 1 setting(s) fell back to the default, 0 key(s) not applied",
				capture["observed"], "config.capture counts the same settings: %v", capture)
		})
	}
}

// TestLoadDaemonConfig_ViolationsCounterCountsResets is the first half of finding #17. The daemon's
// config.violations counter, which status prints, counted §11.3 leaves only, while the record,
// doctor and self-test each count a newer-settingsVersion reset as a setting too. A daemon start on a
// file with one invalid leaf and both versioned blocks reset now counts three settings, not one.
func TestLoadDaemonConfig_ViolationsCounterCountsResets(t *testing.T) {
	root := t.TempDir()
	writeAdmissionConfig(t, root, `{"runtime":{"migration":{"settingsVersion":99},"phase7":{"settingsVersion":99}},`+
		`"eval":{"minSessions":-3}}`)
	reg := obs.New(testClock())
	_, _, err := loadDaemonConfig(config.Env{ProjectRoot: root, HomeDir: t.TempDir(), Getenv: noEnv},
		logging.Nop(), reg)
	require.NoError(t, err)
	require.Equal(t, int64(1+len(config.VersionedSections())), reg.Counter("config.violations").Value(),
		"one leaf and every reset block are each a setting")
}

// treeEntries lists every path under dir, relative to it, in walk order.
func treeEntries(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir {
			rel, relErr := filepath.Rel(dir, p)
			require.NoError(t, relErr)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	}))
	return out
}

// TestCommandLoad_NeverCreatesTheLayoutForARecord is findings #18 and #82(2). A command's load wrote
// state/config-violations.json with an MkdirAll of state/, so `qompack config print`, which the docs
// say creates no layout, created .qompack/state, the record and .qompack/tmp in a directory never
// used with Qompack whenever a layer other than the project's own (the user-global file, a QOMPACK_*
// variable, a --set flag) held an invalid leaf or, since candidate 8, a newer settingsVersion. Like
// the hook path, a command now records only where a .qompack directory already exists. The helper
// row covers every command that loads through LoadConfigAndReport (status, mcp, self-test's
// config.load), and the last case keeps the write where a layout exists.
func TestCommandLoad_NeverCreatesTheLayoutForARecord(t *testing.T) {
	const sv = `{"runtime":{"migration":{"settingsVersion":99}}}`
	for _, tc := range []struct {
		name, userGlobal string
		env              map[string]string
		set              []string
	}{
		{name: "user-global reset", userGlobal: sv},
		{name: "user-global leaf", userGlobal: `{"eval":{"minSessions":-3}}`},
		{name: "environment reset", env: map[string]string{"QOMPACK_RUNTIME__MIGRATION__SETTINGSVERSION": "99"}},
		{name: "--set reset", set: []string{"--set", "runtime.migration.settingsVersion=99"}},
		{name: "--set leaf", set: []string{"--set", "eval.minSessions=-3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			home := t.TempDir()
			if tc.userGlobal != "" {
				writeAdmissionConfig(t, home, tc.userGlobal)
			}
			kv := map[string]string{"QOMPACK_PROJECT_ROOT": root}
			for k, v := range tc.env {
				kv[k] = v
			}
			env := Env{Getenv: envWith(kv), Clock: testClock(), HomeDir: home}

			var out, errw bytes.Buffer
			argv := append([]string{"qompack", "config", "print"}, tc.set...)
			require.Equal(t, ExitOK, Dispatch(context.Background(), All(), argv, env, &out, &errw),
				"stderr=%s", errw.String())
			require.Empty(t, treeEntries(t, root), "config print creates nothing in a never-used directory")

			flags := map[string]string{}
			if len(tc.set) == 2 {
				k, v, _ := strings.Cut(tc.set[1], "=")
				flags[k] = v
			}
			_, _, err := LoadConfigAndReport(config.Env{
				ProjectRoot: root, HomeDir: home, Getenv: envWith(kv), Flags: flags,
			}, logging.Nop(), nil)
			require.NoError(t, err)
			require.Empty(t, treeEntries(t, root), "no command load creates the layout to hold a diagnostic")

			// Where the project has a .qompack, the same load still records.
			require.NoError(t, os.MkdirAll(paths.Of(root).Dot, 0o700))
			_, _, err = LoadConfigAndReport(config.Env{
				ProjectRoot: root, HomeDir: home, Getenv: envWith(kv), Flags: flags,
			}, logging.Nop(), nil)
			require.NoError(t, err)
			_, err = os.Stat(violationsRecord(root))
			require.NoError(t, err, "a project with a .qompack still gets the record")
		})
	}
}

// readDayAndLoud reads the day log and LOUD.log that logging.New wrote in dir.
func readDayAndLoud(t *testing.T, dir string) (day, loud string) {
	t.Helper()
	days, err := filepath.Glob(filepath.Join(dir, "qompack-*.log"))
	require.NoError(t, err)
	var b strings.Builder
	for _, d := range days {
		raw, readErr := os.ReadFile(d)
		require.NoError(t, readErr)
		b.Write(raw)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "LOUD.log"))
	if err != nil {
		require.True(t, os.IsNotExist(err), "LOUD.log unreadable: %v", err)
	}
	return b.String(), string(raw)
}

// TestCommandLoad_InvalidValueIsWarnOnceAndDaemonStartLoudOnce is finding #19 and the nit beside it.
// D59's rule, which loadDaemonConfig's comment cites, is that a persistent configuration condition is
// loud once per daemon start or change and a warn everywhere else. Commands still Louded every §11.3
// leaf each time they loaded (qompack mcp at every session start, each /qompack: command), one line
// per run in the never-rotated LOUD.log for an unchanged file. A command now logs the leaf at warn,
// and the daemon's start is its one loud report. Each load also logged the same leaf twice, once as a
// "configuration warning" and once as the violation; the violation line, which carries got and want,
// is now the only one.
//
// That one line must also say where the value came from. The warning it replaced was the only line
// with a location, and after the fallback `config print --provenance` shows the key as "fallback
// after violation", so without it no log or command could tell the user-global file, the project
// file, a QOMPACK_* variable and a --set flag apart (fix round 1 of wave 22). Each layer is a case.
func TestCommandLoad_InvalidValueIsWarnOnceAndDaemonStartLoudOnce(t *testing.T) {
	const key = "key=eval.minSessions"
	for _, tc := range []struct {
		name     string
		project  string
		user     string
		env      map[string]string
		flags    map[string]string
		location func(root, home string) string
	}{
		{
			name: "project file", project: `{"eval":{"minSessions":-3}}`,
			location: func(root, _ string) string { return config.ProjectConfigPath(root) + ":1" },
		},
		{
			name: "user-global file", user: `{"eval":{"minSessions":-3}}`,
			location: func(_, home string) string { return config.UserConfigPath(home) + ":1" },
		},
		{
			name: "environment", env: map[string]string{"QOMPACK_EVAL__MINSESSIONS": "-3"},
			location: func(_, _ string) string { return "QOMPACK_EVAL__MINSESSIONS" },
		},
		{
			name: "--set", flags: map[string]string{"eval.minSessions": "-3"},
			location: func(_, _ string) string { return "--set" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			home := t.TempDir()
			writeAdmissionConfig(t, root, `{}`)
			if tc.project != "" {
				writeAdmissionConfig(t, root, tc.project)
			}
			if tc.user != "" {
				require.NoError(t, os.MkdirAll(filepath.Dir(config.UserConfigPath(home)), 0o700))
				require.NoError(t, os.WriteFile(config.UserConfigPath(home), []byte(tc.user), 0o600))
			}
			env := config.Env{ProjectRoot: root, HomeDir: home, Getenv: envWith(tc.env), Flags: tc.flags}
			location := "location=" + logFieldValue(tc.location(root, home))

			load := func(loadFn func(config.Env, logging.Logger, obs.Registry) (config.Config, config.Provenance, error)) (string, string) {
				t.Helper()
				dir := t.TempDir()
				log, closer, err := logging.New(dir, logging.Info)
				require.NoError(t, err)
				_, _, err = loadFn(env, log, nil)
				require.NoError(t, closer.Close())
				require.NoError(t, err)
				return readDayAndLoud(t, dir)
			}

			day, loud := load(LoadConfigAndReport)
			require.Zero(t, linesWith(loud, key), "a command does not write a persistent fallback to LOUD.log:\n%s", loud)
			require.Zero(t, linesWith(day, "level=loud", key), "a command logs the fallback at warn:\n%s", day)
			require.Equal(t, 1, linesWith(day, "level=warn", key), "once, as the violation:\n%s", day)
			require.Equal(t, 1, linesWith(day, "level=warn", "invalid configuration value, using default", key), day)
			require.Equal(t, 1, linesWith(day, "invalid configuration value, using default", key, location),
				"the violation line names where the value came from (%s):\n%s", location, day)

			day, loud = load(loadDaemonConfig)
			require.Equal(t, 1, linesWith(loud, "invalid configuration value, using default", key),
				"the daemon's start is the one loud report:\n%s", loud)
			require.Equal(t, 1, linesWith(loud, "invalid configuration value, using default", key, location),
				"and it names where the value came from (%s):\n%s", location, loud)
			require.Equal(t, 1, linesWith(day, key), "and the day log carries it once:\n%s", day)
			require.Equal(t, 1, linesWith(day, key, location), "with its location (%s):\n%s", location, day)
		})
	}
}

// logFieldValue is v as the logger writes a field value (logging's writeValue): quoted when it is
// empty or holds a space or '=', bare otherwise.
func logFieldValue(v string) string {
	if v == "" || strings.ContainsAny(v, " =") {
		return strconv.Quote(v)
	}
	return v
}

// TestHookCapture_ModeOffWritesNothing is finding #20. troubleshooting §8 Step 3 says that with
// runtime.mode off nothing is written from the hook path, but the hook reported the configuration
// before it read the mode: with an invalid value in the same file every hook wrote state/,
// tmp/, state/config-violations.json and a warn line, and with a clean file it removed a record an
// earlier load left. A mode-off hook now returns before any of it; self-test and doctor still report
// the condition through their own read-only loads.
func TestHookCapture_ModeOffWritesNothing(t *testing.T) {
	t.Run("invalid value", func(t *testing.T) {
		root := t.TempDir()
		day, loud := runPromptHooks(t, root, `{"runtime":{"mode":"off"},"eval":{"minSessions":-3}}`, 3)
		require.Empty(t, day, "a mode-off hook logs nothing")
		require.Empty(t, loud)
		require.Equal(t, []string{"config.json", "logs"}, treeEntries(t, paths.Of(root).Dot),
			"a mode-off hook writes nothing under .qompack")
	})
	t.Run("existing record", func(t *testing.T) {
		root := t.TempDir()
		runPromptHooks(t, root, `{"eval":{"minSessions":-3}}`, 1)
		pinRecord(t, root)
		day, _ := runPromptHooks(t, root, `{"runtime":{"mode":"off"}}`, 1)
		require.False(t, recordReplaced(t, root), "a mode-off hook neither rewrites nor removes the record")
		require.Zero(t, linesWith(day, "could not update config violations"), day)
	})
	// Wave 22 fix round 2. A delivery the read refused still reaches admission when it brought
	// bytes and the project has a .qompack/ (hookRefusalIsRecordable), so the mode-off return must
	// stop the configuration report on that path too. What remains is the read's own refusal: doHook
	// logs it to logs/hook-quiet-YYYYMMDD.jsonl (hookclient.go, a known issue outside this row's
	// files), and that line names the read, never the configuration. A fix that drops it still passes.
	for _, tc := range []struct {
		name, readErr string
		stdin         func(root string) io.Reader
	}{
		{"over the read bound", "hook input exceeds capture bound", func(string) io.Reader {
			return &countedAdmissionReader{remaining: hookCaptureMaxBytes + 1}
		}},
		{"short read", "hook input unavailable", func(root string) io.Reader {
			full := entryPayload(t, hookio.EventUserPromptSubmit, root)
			return &shortAdmissionReader{payload: full[:len(full)/2]}
		}},
		{"nothing read", "hook input unavailable", func(string) io.Reader { return errReader{} }},
	} {
		t.Run("refused read/"+tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeAdmissionConfig(t, root, `{"runtime":{"mode":"off"},"eval":{"minSessions":-3}}`)
			l := paths.Of(root)
			require.NoError(t, os.MkdirAll(l.Logs, 0o700))
			t.Chdir(root)
			var out, errw bytes.Buffer
			env := Env{Getenv: noEnv, Clock: testClock(), HomeDir: t.TempDir(), Stdin: tc.stdin(root)}
			require.Equal(t, ExitOK, Dispatch(context.Background(), All(), []string{"qompack", "observe", "prompt"},
				env, &out, &errw), "stderr=%s", errw.String())
			require.JSONEq(t, `{}`, out.String())

			var quiet []string
			for _, e := range treeEntries(t, l.Dot) {
				switch {
				case e == "config.json", e == "logs":
				case strings.HasPrefix(e, "logs/hook-quiet-") && strings.HasSuffix(e, ".jsonl"):
					quiet = append(quiet, e)
				default:
					t.Errorf("a mode-off hook wrote %s under .qompack", e)
				}
			}
			for _, q := range quiet {
				raw, err := os.ReadFile(filepath.Join(l.Dot, filepath.FromSlash(q)))
				require.NoError(t, err)
				for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
					require.Contains(t, line, tc.readErr, "a hook-quiet line names the read's refusal")
					require.NotContains(t, line, "config", "and never the configuration")
				}
			}
		})
	}
}

// writeRecord puts body at state/config-violations.json.
func writeRecord(t *testing.T, root, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(paths.Of(root).State, 0o700))
	require.NoError(t, os.WriteFile(violationsRecord(root), []byte(body), 0o600))
}

// TestDoctor_PersistedRecordReadIsBounded is findings #22, #82(1) and #86 on every platform. doctor
// read state/config-violations.json with an unbounded, link-following paths.ReadFileShared, while
// the hook path's read of the same record (recordHolds) is no-follow, non-blocking, regular-file only
// and bounded. doctor now reads it the same way: a record over the bound or anything that is not a
// regular file is no persisted list, and the row says the record was not read. A regular record
// within the bound is still listed, which the first case pins.
func TestDoctor_PersistedRecordReadIsBounded(t *testing.T) {
	const persistedOnly = `[{"Key":"selection.persistedOnly","Message":"from an earlier load"}]`
	t.Run("regular record", func(t *testing.T) {
		root := t.TempDir()
		writeAdmissionConfig(t, root, `{}`)
		writeRecord(t, root, persistedOnly)
		row := doctorViolationsRow(t, root)
		require.Equal(t, "degraded", row["status"], "row=%v", row)
		require.Equal(t, "selection.persistedOnly (from state/config-violations.json)", row["detail"], "row=%v", row)
	})
	t.Run("over the bound", func(t *testing.T) {
		root := t.TempDir()
		writeAdmissionConfig(t, root, `{}`)
		pad := strings.Repeat("x", 1<<20) // over the 1 MiB bound
		require.Greater(t, len(pad), violationsRecordMaxBytes-len(`[{"Key":"selection.persistedOnly","Message":""}]`),
			"the record this case writes is over doctor's bound")
		writeRecord(t, root, `[{"Key":"selection.persistedOnly","Message":"`+pad+`"}]`)
		row := doctorViolationsRow(t, root)
		require.Equal(t, "ok", row["status"], "row=%v", row)
		require.NotContains(t, row["detail"], "selection.persistedOnly", "row=%v", row)
		require.Contains(t, row["detail"], "state/config-violations.json not read: larger than", "row=%v", row)
	})
	t.Run("a directory", func(t *testing.T) {
		root := t.TempDir()
		writeAdmissionConfig(t, root, `{}`)
		require.NoError(t, os.MkdirAll(violationsRecord(root), 0o700))
		row := doctorViolationsRow(t, root)
		require.Equal(t, "ok", row["status"], "row=%v", row)
		require.Contains(t, row["detail"], "state/config-violations.json not read: not a regular file", "row=%v", row)
	})
}

// doctorViolationsRow runs doctor --json on root and returns its config.violations row.
func doctorViolationsRow(t *testing.T, root string) map[string]any {
	t.Helper()
	code, doc, errw := doctorJSON(t, root)
	require.Equal(t, ExitOK, code, "stderr=%s", errw)
	return doctorFindRow(t, doc, "controls", "config.violations")
}
