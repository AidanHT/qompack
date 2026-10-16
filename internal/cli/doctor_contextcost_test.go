package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/hostperm"
	"github.com/qompack/qompack/internal/paths"
)

// ccEnv is a hermetic machine for the context-and-cost rows: a project, a home and a managed
// directory under t.TempDir, and an environment the test controls.
type ccEnv struct {
	root, home, managed string
	vars                map[string]string
}

func newCCEnv(t *testing.T) *ccEnv {
	t.Helper()
	return &ccEnv{root: t.TempDir(), home: t.TempDir(), managed: t.TempDir(), vars: map[string]string{}}
}

func (e *ccEnv) getenv(k string) string { return e.vars[k] }

func (e *ccEnv) opts() hostperm.Options {
	return hostperm.Options{
		ProjectRoot: e.root, Home: e.home, Getenv: e.getenv,
		Managed: &hostperm.ManagedSources{Dirs: []string{e.managed}},
	}
}

func (e *ccEnv) rows() []doctorRow { return contextCostRows(e.opts(), e.getenv) }

func ccWrite(t *testing.T, file, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(file)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(file), []byte(body), 0o600))
}

// ccRow returns the row with id, failing when there is none.
func ccRow(t *testing.T, rows []doctorRow, id string) doctorRow {
	t.Helper()
	for _, r := range rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no row %q in %+v", id, rows)
	return doctorRow{}
}

// ccRequireVocabulary asserts the two rules every row of the section keeps: only ok or unknown, and
// no effective value computed.
func ccRequireVocabulary(t *testing.T, rows []doctorRow) {
	t.Helper()
	require.NotEmpty(t, rows)
	for _, r := range rows {
		require.Contains(t, []string{doctorOK, doctorUnknown}, r.Status, "row %+v", r)
		require.True(t, strings.HasPrefix(r.ID, "context."), "row %+v", r)
		require.NotContains(t, strings.ToLower(r.Observed), "effective", "row %+v", r)
	}
}

func TestDoctorContextCost_ReportsEachLayerRaw(t *testing.T) {
	e := newCCEnv(t)
	ccWrite(t, filepath.Join(e.managed, "managed-settings.json"), `{"autoCompactWindow":300000}`)
	ccWrite(t, filepath.Join(e.managed, "managed-settings.d", "10.json"), `{"idleCompaction":false}`)
	ccWrite(t, filepath.Join(e.home, ".claude", "settings.json"), `{"autoCompactEnabled":false}`)
	ccWrite(t, filepath.Join(e.root, ".claude", "settings.json"), `{"autoCompactWindow":"500k"}`)
	ccWrite(t, filepath.Join(e.root, ".claude", "settings.local.json"),
		"\xEF\xBB\xBF{\"idleCompaction\":true,\"other\":1}")

	rows := e.rows()
	ccRequireVocabulary(t, rows)

	for id, want := range map[string]string{
		"context.layer.managed":         "autoCompactWindow=300000",
		"context.layer.managed-drop-in": "idleCompaction=false",
		"context.layer.user":            "autoCompactEnabled=false",
		"context.layer.project":         `autoCompactWindow="500k"`,
		"context.layer.local":           "idleCompaction=true",
	} {
		r := ccRow(t, rows, id)
		require.Equal(t, doctorOK, r.Status, "%+v", r)
		require.Equal(t, want, r.Observed, "the value is reported as written, never interpreted")
	}
	require.Contains(t, ccRow(t, rows, "context.layer.project").Detail,
		filepath.Join(e.root, ".claude", "settings.json"), "the detail names the file")
}

func TestDoctorContextCost_ListsModelSettingsBesideTheTopLevelKey(t *testing.T) {
	e := newCCEnv(t)
	ccWrite(t, filepath.Join(e.home, ".claude", "settings.json"),
		`{"autoCompactWindow":500000,"modelSettings":{"opus":{"autoCompactWindow":200000},`+
			`"sonnet":{"autoCompactWindow": 800000},"haiku":{"other":true}}}`)

	r := ccRow(t, e.rows(), "context.layer.user")
	require.Equal(t, doctorOK, r.Status)
	require.Equal(t, "autoCompactWindow=500000; modelSettings.opus.autoCompactWindow=200000; "+
		"modelSettings.sonnet.autoCompactWindow=800000", r.Observed,
		"every model's entry is listed beside the top-level key and none is resolved against it")
}

func TestDoctorContextCost_MalformedJSONReadsUnknown(t *testing.T) {
	e := newCCEnv(t)
	ccWrite(t, filepath.Join(e.root, ".claude", "settings.json"), `{"autoCompactWindow":`)
	ccWrite(t, filepath.Join(e.home, ".claude", "settings.json"), `{"autoCompactWindow":400000}`)
	ccWrite(t, filepath.Join(e.root, ".claude", "settings.local.json"), `{"modelSettings":[1]}`)

	rows := e.rows()
	ccRequireVocabulary(t, rows)
	require.Equal(t, doctorUnknown, ccRow(t, rows, "context.layer.project").Status)
	require.Equal(t, doctorUnknown, ccRow(t, rows, "context.layer.local").Status,
		"a modelSettings that is not an object cannot be read as documented")
	user := ccRow(t, rows, "context.layer.user")
	require.Equal(t, doctorOK, user.Status, "one broken layer does not hide the others")
	require.Equal(t, "autoCompactWindow=400000", user.Observed)
}

func TestDoctorContextCost_AbsentFilesAreOK(t *testing.T) {
	e := newCCEnv(t)

	rows := e.rows()
	ccRequireVocabulary(t, rows)
	for _, id := range []string{
		"context.layer.managed", "context.layer.remote", "context.layer.user",
		"context.layer.project", "context.layer.local",
	} {
		r := ccRow(t, rows, id)
		require.Equal(t, doctorOK, r.Status, "%+v", r)
		require.Equal(t, "absent", r.Observed, "%+v", r)
	}
}

func TestDoctorContextCost_RemoteSettingsAreNotDecoded(t *testing.T) {
	e := newCCEnv(t)
	ccWrite(t, filepath.Join(e.home, ".claude", "remote-settings.json"), `{"autoCompactWindow":100000}`)

	r := ccRow(t, e.rows(), "context.layer.remote")
	require.Equal(t, doctorUnknown, r.Status)
	require.Equal(t, "present; not decoded", r.Observed)
	require.NotContains(t, r.Observed+r.Detail, "100000")
}

func TestDoctorContextCost_EmptyProjectRoot(t *testing.T) {
	e := newCCEnv(t)
	ccWrite(t, filepath.Join(e.home, ".claude", "settings.json"), `{"idleCompaction":false}`)
	o := e.opts()
	o.ProjectRoot = ""

	rows := contextCostRows(o, e.getenv)
	ccRequireVocabulary(t, rows)
	require.Equal(t, "idleCompaction=false", ccRow(t, rows, "context.layer.user").Observed)
	for _, r := range rows {
		require.NotEqual(t, "context.layer.project", r.ID)
		require.NotEqual(t, "context.layer.local", r.ID)
	}
}

func TestDoctorContextCost_DuplicateKindsGetDistinctIDs(t *testing.T) {
	e := newCCEnv(t)
	ccWrite(t, filepath.Join(e.managed, "managed-settings.d", "a.json"), `{"autoCompactWindow":100000}`)
	ccWrite(t, filepath.Join(e.managed, "managed-settings.d", "b.json"), `{}`)

	rows := e.rows()
	require.Equal(t, "autoCompactWindow=100000", ccRow(t, rows, "context.layer.managed-drop-in.1").Observed)
	require.Equal(t, "no context-size keys set", ccRow(t, rows, "context.layer.managed-drop-in.2").Observed)
	seen := map[string]bool{}
	for _, r := range rows {
		require.False(t, seen[r.ID], "row id %s repeats", r.ID)
		seen[r.ID] = true
	}
}

// ccEnvVars are the variables the section reports, named at GT/env-vars.md:202, 220, 236, 322,
// 449 and 450.
var ccEnvVars = []string{
	"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", "CLAUDE_CODE_DISABLE_1M_CONTEXT",
	"DISABLE_AUTO_COMPACT", "DISABLE_COMPACT", "CLAUDE_CODE_IDLE_COMPACT_MIN_TOKENS",
}

func TestDoctorContextCost_EnvironmentAsSeenByThisProcess(t *testing.T) {
	for _, name := range ccEnvVars {
		t.Run(name, func(t *testing.T) {
			e := newCCEnv(t)
			unset := ccRow(t, e.rows(), "context.env."+name)
			require.Equal(t, doctorOK, unset.Status)
			require.Equal(t, "unset", unset.Observed)
			require.Contains(t, unset.Detail, "as seen by this doctor process")

			e.vars[name] = "500k"
			set := ccRow(t, e.rows(), "context.env."+name)
			require.Equal(t, doctorOK, set.Status)
			require.Equal(t, `set to "500k"`, set.Observed, "the value is reported as written")
		})
	}
}

func TestDoctorContextCost_SaysWhatItCannotSee(t *testing.T) {
	r := ccRow(t, newCCEnv(t).rows(), "context.invisible")
	require.Equal(t, doctorUnknown, r.Status)
	for _, want := range []string{"--autocompact", "--settings", "session's own environment"} {
		require.Contains(t, r.Detail, want)
	}
}

// TestDoctorContextCost_SectionKeepsTheExitCode runs the real command against a project whose
// settings do not parse: the section is there, its rows keep the vocabulary, and doctor exits 0.
func TestDoctorContextCost_SectionKeepsTheExitCode(t *testing.T) {
	root := t.TempDir()
	ccWrite(t, filepath.Join(root, ".claude", "settings.json"), `{broken`)

	code, doc, errw := doctorJSON(t, root)
	require.Equal(t, ExitOK, code, "stderr=%s", errw)
	require.EqualValues(t, ExitOK, doc["exit"])
	rows := doctorSection(t, doc, "context")
	require.NotEmpty(t, rows)
	for _, r := range rows {
		require.Contains(t, []any{doctorOK, doctorUnknown}, r["status"], "row %v", r)
	}
	require.Equal(t, doctorUnknown, doctorFindRow(t, doc, "context", "context.layer.project")["status"])
	require.Equal(t, "unset", doctorFindRow(t, doc, "context", "context.env.DISABLE_COMPACT")["observed"])
}

// TestDoctorContextCost_ReportsSettingsEnvRaw: a settings file's `env` sets the same variables
// (GT/settings-reference.md:2878-2899), so each layer reports env.<NAME> for the six, as written,
// and the process-environment rows are not resolved against them.
func TestDoctorContextCost_ReportsSettingsEnvRaw(t *testing.T) {
	e := newCCEnv(t)
	ccWrite(t, filepath.Join(e.managed, "managed-settings.json"), `{"env":{"DISABLE_COMPACT":"1"}}`)
	ccWrite(t, filepath.Join(e.home, ".claude", "settings.json"),
		`{"autoCompactWindow":400000,"env":{"OTHER":"x","DISABLE_AUTO_COMPACT":"1",`+
			`"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"500000"}}`)
	ccWrite(t, filepath.Join(e.root, ".claude", "settings.local.json"),
		`{"env":{"CLAUDE_AUTOCOMPACT_PCT_OVERRIDE":50}}`)
	ccWrite(t, filepath.Join(e.root, ".claude", "settings.json"), `{"env":{"OTHER":"x"}}`)

	rows := e.rows()
	ccRequireVocabulary(t, rows)
	for id, want := range map[string]string{
		"context.layer.managed": `env.DISABLE_COMPACT="1"`,
		"context.layer.user": `autoCompactWindow=400000; env.CLAUDE_CODE_AUTO_COMPACT_WINDOW="500000"; ` +
			`env.DISABLE_AUTO_COMPACT="1"`,
		"context.layer.local":   "env.CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=50",
		"context.layer.project": "no context-size keys set",
	} {
		r := ccRow(t, rows, id)
		require.Equal(t, doctorOK, r.Status, "%+v", r)
		require.Equal(t, want, r.Observed, "each env entry is reported as written, in the section's order")
	}
	for _, name := range []string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "DISABLE_AUTO_COMPACT", "DISABLE_COMPACT"} {
		r := ccRow(t, rows, "context.env."+name)
		require.Equal(t, "unset", r.Observed, "the process row is not resolved against a settings env")
		require.Contains(t, r.Detail, "env", "the process row points at the settings env")
	}
}

func TestDoctorContextCost_MalformedEnvReadsUnknown(t *testing.T) {
	for _, env := range []string{`"DISABLE_COMPACT=1"`, `["DISABLE_COMPACT"]`, `null`, `1`} {
		t.Run(env, func(t *testing.T) {
			e := newCCEnv(t)
			ccWrite(t, filepath.Join(e.home, ".claude", "settings.json"),
				`{"autoCompactWindow":400000,"env":`+env+`}`)
			ccWrite(t, filepath.Join(e.root, ".claude", "settings.json"), `{"env":{"DISABLE_COMPACT":"1"}}`)

			rows := e.rows()
			ccRequireVocabulary(t, rows)
			user := ccRow(t, rows, "context.layer.user")
			require.Equal(t, doctorUnknown, user.Status, "an env that is not an object cannot be read as documented")
			require.Equal(t, "env is not an object", user.Observed)
			require.Equal(t, `env.DISABLE_COMPACT="1"`, ccRow(t, rows, "context.layer.project").Observed,
				"one broken layer does not hide the others")
		})
	}
}

func TestDoctorContextCost_UnreadableSettingsFileReadsUnknown(t *testing.T) {
	e := newCCEnv(t)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(e.home, ".claude", "settings.json")), 0o700))

	r := ccRow(t, e.rows(), "context.layer.user")
	require.Equal(t, doctorUnknown, r.Status, "%+v", r)
	require.Equal(t, "could not be read", r.Observed)
	require.Contains(t, r.Detail, filepath.Join(e.home, ".claude", "settings.json"))
}

func TestDoctorContextCost_ManagedProfileIsNotDecoded(t *testing.T) {
	e := newCCEnv(t)
	profile := filepath.Join(e.managed, "com.anthropic.claudecode.plist")
	ccWrite(t, profile, `<plist><dict><key>autoCompactWindow</key><integer>100000</integer></dict></plist>`)
	o := e.opts()
	o.Managed.Opaque = []string{profile}

	r := ccRow(t, contextCostRows(o, e.getenv), "context.layer.managed-profile")
	require.Equal(t, doctorUnknown, r.Status, "%+v", r)
	require.Equal(t, "present; not decoded", r.Observed)
	require.NotContains(t, r.Observed+r.Detail, "100000")
}

// TestDoctorContextCost_DropInListingErrorReadsUnknown: a managed-settings.d that cannot be listed
// as documented (here, more drop-ins than hostperm reads) is one unknown row naming the directory.
func TestDoctorContextCost_DropInListingErrorReadsUnknown(t *testing.T) {
	e := newCCEnv(t)
	for i := range 257 {
		ccWrite(t, filepath.Join(e.managed, "managed-settings.d", fmt.Sprintf("%03d.json", i)), `{}`)
	}

	rows := e.rows()
	ccRequireVocabulary(t, rows)
	r := ccRow(t, rows, "context.layer.managed-drop-in")
	require.Equal(t, doctorUnknown, r.Status, "%+v", r)
	require.Equal(t, "could not be read", r.Observed)
	require.Contains(t, r.Detail, filepath.Join(e.managed, "managed-settings.d"))
}
