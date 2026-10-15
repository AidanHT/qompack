package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The fixtures under testdata/modvalidate are real `claude plugin validate <dir> --strict --json`
// outputs from claude 2.1.300 on Windows, with the plugin directory replaced by <out> (the redaction
// `mod-validate --record` applies):
//   - claude-2.1.300-qompack.json: the materialised Qompack --mods plugin, recorded by
//     `go run ./tools/devtool mod-validate --record tools/devtool/testdata/modvalidate/claude-2.1.300-qompack.json`;
//   - claude-2.1.300-allowed-calls.json: a scratch plugin whose hooks module is
//     allowed-calls.register.ts.txt (allowlisted calls, two through a helper, one gating hook);
//   - claude-2.1.300-unlisted-calls.json: a scratch plugin whose module is
//     unlisted-calls.register.ts.txt ($.fs.read and $.ui.toast, a gating hook with .catch);
//   - claude-2.1.300-strict-warning.json: a scratch plugin whose plugin.json has no author, which
//     --strict turns into success:false and exit 1.
//
// On 2.1.300 the report has no key for the call list: the calls appear only as a
// "<module> calls: ..." line in contents[].notes, which is the format these fixtures pin.
const modValidateFixtures = "testdata/modvalidate"

func readModValidateFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(modValidateFixtures, name))
	require.NoError(t, err)
	return b
}

func TestModStageAAllowlist_IsTheSixStageACalls(t *testing.T) {
	require.Equal(t, []string{
		"$.process.run",
		"$.session.id",
		"$.session.root",
		"$.session.version",
		"$.plugin.root",
		"$.ui.log",
	}, modStageAAllowlist)
}

func TestValidateReport_CallsFromTheRecordedNotes(t *testing.T) {
	for _, tc := range []struct {
		fixture  string
		calls    []string
		unlisted []string
		gating   []validateGatingHook
	}{
		{fixture: "claude-2.1.300-qompack.json", calls: []string{}, unlisted: nil},
		{
			fixture:  "claude-2.1.300-allowed-calls.json",
			calls:    []string{"$.process.run", "$.session.id", "$.session.root", "$.session.version", "$.ui.log"},
			unlisted: nil,
			gating:   []validateGatingHook{{Module: "./register.ts", Pattern: "session.compact", Hook: "session.compact", HasCatch: false}},
		},
		{
			fixture:  "claude-2.1.300-unlisted-calls.json",
			calls:    []string{"$.fs.read", "$.session.id", "$.ui.toast"},
			unlisted: []string{"$.fs.read", "$.ui.toast"},
			gating:   []validateGatingHook{{Module: "./register.ts", Pattern: "tool.call", Hook: "tool.call", HasCatch: true}},
		},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			r, err := parseValidateReport(readModValidateFixture(t, tc.fixture))
			require.NoError(t, err)
			require.NotNil(t, r.Success)
			require.True(t, *r.Success)
			calls, found, err := r.calls()
			require.NoError(t, err)
			require.True(t, found, "2.1.300 lists the calls in a notes line")
			require.Equal(t, tc.calls, calls)
			require.Equal(t, tc.unlisted, unlistedModCalls(calls))
			require.Equal(t, tc.gating, r.gatingHooks())
		})
	}
}

func TestValidateReport_StrictWarningIsAFailedVerdict(t *testing.T) {
	r, err := parseValidateReport(readModValidateFixture(t, "claude-2.1.300-strict-warning.json"))
	require.NoError(t, err)
	require.NotNil(t, r.Success)
	require.False(t, *r.Success)
	require.Len(t, r.Manifest.Warnings, 1)
	require.Contains(t, string(r.Manifest.Warnings[0]), "author")
}

func TestValidateReport_CallsNoteShapes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		notes []string
		want  []string
		found bool
		err   string
	}{
		{name: "no calls line", notes: []string{"./register.ts hooks: nothing"}, want: nil, found: false},
		{name: "nothing", notes: []string{"./register.ts calls: nothing on $"}, want: []string{}, found: true},
		{
			name:  "via helpers",
			notes: []string{"./register.ts calls: $.store.get (via loadNotes), $.ui.log"},
			want:  []string{"$.store.get", "$.ui.log"}, found: true,
		},
		{
			name:  "two modules are merged",
			notes: []string{"./a.ts calls: $.ui.log", "./b.ts calls: $.ui.log, $.session.id"},
			want:  []string{"$.session.id", "$.ui.log"}, found: true,
		},
		{name: "an entry it cannot read", notes: []string{"./register.ts calls: $.ui.log and 3 more"}, err: "and 3 more"},
		{name: "an empty list", notes: []string{"./register.ts calls: "}, err: "calls"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := validateReport{Contents: []validateSection{{Notes: tc.notes}}}
			got, found, err := r.calls()
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.found, found)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestScanModuleCalls(t *testing.T) {
	src := strings.Join([]string{
		"const out = await $.process.run(argv, { cwd })",
		"await $ . session . id()",
		"$?.ui.toast({ text: 'x' })",
		"foo$.bar.baz()",
		"const s = `${value}`",
		"// $.fs.read in a comment still counts: the scan fails closed",
		"const bin = $.plugin.root + '/bin/qompack'",
		"await $.process.run(again)",
	}, "\n")
	require.Equal(t, []string{"$.fs.read", "$.plugin.root", "$.process.run", "$.session.id", "$.ui.toast"},
		scanModuleCalls(src))

	reg, err := os.ReadFile(filepath.Join(testModuleRoot(t), "internal", "pluginmanifest", "modsrc", "register.ts"))
	require.NoError(t, err)
	require.Empty(t, scanModuleCalls(string(reg)), "register.ts makes no host call yet")
}

// validateAnswer answers `claude plugin validate` with a fixture, with <out> put back to the real
// plugin directory the way the CLI spells it inside JSON.
func validateAnswer(t *testing.T, fixture []byte, exit int, edit func(dir string)) func(args []string) claudeRun {
	t.Helper()
	return func(args []string) claudeRun {
		require.Len(t, args, 5)
		require.Equal(t, []string{"plugin", "validate"}, args[:2])
		require.Equal(t, []string{"--strict", "--json"}, args[3:])
		dir := args[2]
		if edit != nil {
			edit(dir)
		}
		escaped, err := json.Marshal(dir)
		require.NoError(t, err)
		inner := string(escaped[1 : len(escaped)-1])
		return claudeRun{ExitCode: exit, Stdout: bytes.ReplaceAll(fixture, []byte("<out>"), []byte(inner))}
	}
}

func TestRunModValidate_FailsClosedWhenClaudeIsAbsent(t *testing.T) {
	ran := false
	c := modClaude{
		lookPath: func() (string, error) { return "", errors.New("exec: \"claude\": executable file not found") },
		run: func([]string, string, ...string) claudeRun {
			ran = true
			return claudeRun{}
		},
	}
	err := runModValidate(c, testModuleRoot(t), "windows", "", &bytes.Buffer{})
	require.ErrorContains(t, err, "not on PATH")
	require.False(t, ran)
}

func TestRunModValidate_PassesTheMaterialisedPlugin(t *testing.T) {
	fixture := readModValidateFixture(t, "claude-2.1.300-qompack.json")
	var seen []string
	f := &fakeModClaude{t: t}
	f.answer = validateAnswer(t, fixture, 0, func(dir string) {
		seen = listBundleTree(t, dir)
		var hooks map[string]json.RawMessage
		readBundleJSON(t, dir, "hooks/hooks.json", &hooks)
		require.JSONEq(t, `["./register.ts"]`, string(hooks["modules"]))
	})
	var out bytes.Buffer
	require.NoError(t, runModValidate(f.claude(), testModuleRoot(t), "windows", "", &out))
	want, err := pluginTreeFiles(modPluginVersion, "windows", true)
	require.NoError(t, err)
	require.Len(t, seen, len(want), "the --mods plugin tree, with no tests: %v", seen)
	for _, p := range seen {
		require.Contains(t, want, p)
	}
	require.Contains(t, out.String(), "calls: none")
	require.Contains(t, out.String(), "gating hooks: none")
	require.Contains(t, out.String(), "2.1.300")
}

func TestRunModValidate_SurfacesGatingHooks(t *testing.T) {
	f := &fakeModClaude{t: t}
	f.answer = validateAnswer(t, readModValidateFixture(t, "claude-2.1.300-allowed-calls.json"), 0, nil)
	var out bytes.Buffer
	require.NoError(t, runModValidate(f.claude(), testModuleRoot(t), "windows", "", &out))
	require.Contains(t, out.String(), "gating hook session.compact (./register.ts): no .catch")
	require.Contains(t, out.String(), "$.process.run")
}

func TestRunModValidate_FailsAnUnlistedCall(t *testing.T) {
	f := &fakeModClaude{t: t}
	f.answer = validateAnswer(t, readModValidateFixture(t, "claude-2.1.300-unlisted-calls.json"), 0, nil)
	err := runModValidate(f.claude(), testModuleRoot(t), "windows", "", &bytes.Buffer{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "$.fs.read")
	require.Contains(t, err.Error(), "$.ui.toast")
	require.NotContains(t, err.Error(), "$.session.id")
}

func TestRunModValidate_FailsWithoutAPassingVerdict(t *testing.T) {
	qompack := readModValidateFixture(t, "claude-2.1.300-qompack.json")
	for _, tc := range []struct {
		name    string
		fixture []byte
		exit    int
		want    string
	}{
		{"strict warning", readModValidateFixture(t, "claude-2.1.300-strict-warning.json"), 1, "author"},
		{"success but non-zero exit", qompack, 1, "exit 1"},
		{"exit 0 but success false", bytes.Replace(qompack, []byte(`"success": true`), []byte(`"success": false`), 1), 0, "success"},
		{"no success key", bytes.Replace(qompack, []byte(`"success": true,`), nil, 1), 0, "success"},
		{"not JSON", []byte("Validation passed\n"), 0, "JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeModClaude{t: t}
			f.answer = validateAnswer(t, tc.fixture, tc.exit, nil)
			err := runModValidate(f.claude(), testModuleRoot(t), "windows", "", &bytes.Buffer{})
			require.ErrorContains(t, err, tc.want)
		})
	}

	f := &fakeModClaude{t: t, answer: func([]string) claudeRun { return claudeRun{ExitCode: -1, Err: errors.New("boom")} }}
	require.ErrorContains(t, runModValidate(f.claude(), testModuleRoot(t), "windows", "", &bytes.Buffer{}), "boom")
}

// TestRunModValidate_FallsBackToScanningRegister covers an output that lists no calls: the task then
// scans the materialised hooks/register.ts, and an unlisted call there still fails it.
func TestRunModValidate_FallsBackToScanningRegister(t *testing.T) {
	noCalls := bytes.Replace(readModValidateFixture(t, "claude-2.1.300-qompack.json"),
		[]byte(",\n        \"./register.ts calls: nothing on $\""), nil, 1)
	require.NotContains(t, string(noCalls), "calls:")

	f := &fakeModClaude{t: t}
	f.answer = validateAnswer(t, noCalls, 0, nil)
	var out bytes.Buffer
	require.NoError(t, runModValidate(f.claude(), testModuleRoot(t), "windows", "", &out))
	require.Contains(t, out.String(), "scanned hooks/register.ts")

	f = &fakeModClaude{t: t}
	f.answer = validateAnswer(t, noCalls, 0, func(dir string) {
		p := filepath.Join(dir, "hooks", "register.ts")
		require.NoError(t, os.WriteFile(p, []byte("export function register(on) { on('session.start', async ($, e, next) => { await $.fs.read('x'); return next(e) }) }\n"), 0o600))
	})
	err := runModValidate(f.claude(), testModuleRoot(t), "windows", "", &bytes.Buffer{})
	require.ErrorContains(t, err, "$.fs.read")
}

// TestRunModValidate_RecordsTheRedactedOutput pins --record: the CLI's own bytes, with the temporary
// plugin directory replaced by <out>, which is how the fixtures above were made.
func TestRunModValidate_RecordsTheRedactedOutput(t *testing.T) {
	fixture := readModValidateFixture(t, "claude-2.1.300-qompack.json")
	f := &fakeModClaude{t: t}
	f.answer = validateAnswer(t, fixture, 0, nil)
	rec := filepath.Join(t.TempDir(), "sub", "validate.json")
	require.NoError(t, runModValidate(f.claude(), testModuleRoot(t), "windows", rec, &bytes.Buffer{}))
	got, err := os.ReadFile(rec)
	require.NoError(t, err)
	require.Equal(t, string(fixture), string(got))
}

// TestRunModValidate_RecordsRelativeToTheRepoRootItIsGiven keeps the task off the package-level
// root: a relative --record path lands under the repository root the caller passes in.
func TestRunModValidate_RecordsRelativeToTheRepoRootItIsGiven(t *testing.T) {
	fixture := readModValidateFixture(t, "claude-2.1.300-qompack.json")
	f := &fakeModClaude{t: t}
	f.answer = validateAnswer(t, fixture, 0, nil)
	repo := t.TempDir()
	require.NoError(t, runModValidate(f.claude(), repo, "windows", "sub/validate.json", &bytes.Buffer{}))
	got, err := os.ReadFile(filepath.Join(repo, "sub", "validate.json"))
	require.NoError(t, err)
	require.Equal(t, string(fixture), string(got))
}

// TestRunModValidate_UnionsTheRegisterScanWithTheCallsLine is ruling R12: 2.1.300's calls line
// omits property reads (the allowed-calls module reads $.plugin.root and the line does not list
// it), so the scan of hooks/register.ts is always added to the line's set. An off-list read the
// line cannot show still fails the task; an allowlisted one does not.
func TestRunModValidate_UnionsTheRegisterScanWithTheCallsLine(t *testing.T) {
	fixture := readModValidateFixture(t, "claude-2.1.300-allowed-calls.json")
	require.NotContains(t, string(fixture), "$.plugin.root", "the recorded line omits the property read")
	writeRegister := func(src string) func(dir string) {
		return func(dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "hooks", "register.ts"), []byte(src), 0o600))
		}
	}

	f := &fakeModClaude{t: t}
	f.answer = validateAnswer(t, fixture, 0, writeRegister(
		"export function register(on) { on('session.start', async ($, e, next) => { const c = $.session.cwd; return next(e) }) }\n"))
	err := runModValidate(f.claude(), testModuleRoot(t), "windows", "", &bytes.Buffer{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "$.session.cwd")

	f = &fakeModClaude{t: t}
	f.answer = validateAnswer(t, fixture, 0, writeRegister(
		"export function register(on) { on('session.start', async ($, e, next) => { const r = $.plugin.root; return next(e) }) }\n"))
	var out bytes.Buffer
	require.NoError(t, runModValidate(f.claude(), testModuleRoot(t), "windows", "", &out))
	require.Contains(t, out.String(), "$.plugin.root")
	require.Contains(t, out.String(), "$.process.run", "the calls line's entries are kept")
}
