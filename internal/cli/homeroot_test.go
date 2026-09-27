package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// Owner decision D18 (2026-09-26): a session whose project root resolves to the home directory is
// refused. The project store would otherwise be <home>/.qompack, which is the user-global layer's
// own directory (config.json, calibration, fallback logs, the D10 staged daemon copies under bin/).
// Such a session records nothing and says why: SessionStart answers with one short host message,
// the other hooks stay silent, the MCP tools answer with a stable error, status and doctor explain,
// and every command that would touch a store refuses with a non-zero exit.
//
// Every home below is a fake one under t.TempDir(). HOME and USERPROFILE are pointed at it too, so
// that even a regression reaching a component that reads the process environment for its home
// (internal/daemon's staging and config reload) stays inside the fake home.

// homeUserConfig is the fake home's user-global layer: one valid leaf, so a row can see the layer
// applied, and one out-of-range leaf, so a loader that persists violations under the project root
// would write <home>/.qompack/state/config-violations.json if the project root were home.
const homeUserConfig = `{"scheduler":{"softFloorPct":0.42},"eval":{"minSessions":-3}}`

// homeFixture is a fake home with the user-global layer a real installation has, and a snapshot of
// its whole tree taken once the row has finished laying it out.
type homeFixture struct {
	home   string
	skip   []string
	before map[string]string
}

func newHomeFixture(t *testing.T, dotfiles bool) *homeFixture {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	global := paths.Global(home)
	require.NoError(t, os.MkdirAll(filepath.Join(global, "bin", "0123abcd"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(global, "logs"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(global, "bin", "0123abcd", "qompack.exe"), []byte("staged copy"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(global, "config.json"), []byte(homeUserConfig), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(global, "calibration.json"), []byte("{}\n"), 0o600))
	if dotfiles {
		require.NoError(t, os.Mkdir(filepath.Join(home, ".git"), 0o700))
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return &homeFixture{home: home}
}

// dir makes home/rel, with its own .git when git is set, and returns its absolute path.
func (f *homeFixture) dir(t *testing.T, rel string, git bool) string {
	t.Helper()
	d := filepath.Join(f.home, rel)
	require.NoError(t, os.MkdirAll(d, 0o700))
	if git {
		require.NoError(t, os.Mkdir(filepath.Join(d, ".git"), 0o700))
	}
	return d
}

// project marks d as a directory a regression row is allowed to write into, so the snapshot does
// not count its store against the home.
func (f *homeFixture) project(d string) { f.skip = append(f.skip, d) }

// seal takes the snapshot every later requireUntouched compares against.
func (f *homeFixture) seal(t *testing.T) {
	t.Helper()
	f.before = homeTree(t, f.home, f.skip)
}

// requireUntouched fails when anything under the home was created, removed or rewritten.
func (f *homeFixture) requireUntouched(t *testing.T, what string) {
	t.Helper()
	require.Equal(t, f.before, homeTree(t, f.home, f.skip),
		"%s must create, remove and rewrite nothing under the home directory (D18)", what)
}

// homeTree records every entry under home as a directory marker or the file's size and mtime,
// skipping the given subtrees.
func homeTree(t *testing.T, home string, skip []string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, s := range skip {
			if p == s {
				return filepath.SkipDir
			}
		}
		rel, rerr := filepath.Rel(home, p)
		if rerr != nil {
			return rerr
		}
		if d.IsDir() {
			got[rel] = "dir"
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		got[rel] = fmt.Sprintf("%d bytes @%d", info.Size(), info.ModTime().UnixNano())
		return nil
	})
	require.NoError(t, err)
	return got
}

// requireResolvesTo is every cwd row's precondition: the walk from dir reaches want and goes no
// further. A temp directory's ancestors carry no .git on a sane host (TestResolve_FallsBackToCWD
// rests on the same fact); if one did, the row would be about some other directory, so it stops
// before running anything rather than acting there.
func requireResolvesTo(t *testing.T, dir, want string) {
	t.Helper()
	got, err := paths.Resolve(noEnv, dir)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(want), filepath.Clean(got),
		"precondition: the .git walk from %s must stop at %s", dir, want)
}

// noSuchSelf is an Env.Self naming an executable that does not exist. A hook with a Self may spawn
// a daemon, so a refused session that still tried would leave a spawn lock under the home's
// .qompack/run — which requireUntouched sees — while nothing can actually start.
func noSuchSelf(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "no-such-qompack.exe")
}

// homeArrival is one way a session's project root comes to be the home directory.
type homeArrival struct {
	name     string
	dotfiles bool
	// env is the Getenv overlay; QOMPACK_PROJECT_ROOT set to "<home>" is replaced by the home.
	env map[string]string
	// cwd and payload are the hook process's working directory and the payload's cwd, relative to
	// the home ("." is the home itself), or "<neutral>" for a directory outside it with its own .git.
	cwd, payload string
}

var homeArrivals = []homeArrival{
	{
		name: "QOMPACK_PROJECT_ROOT names home", env: map[string]string{"QOMPACK_PROJECT_ROOT": "<home>"},
		cwd: "<neutral>", payload: ".",
	},
	{name: "session started in the home directory", cwd: ".", payload: "."},
	{name: "payload cwd is the home directory", cwd: "<neutral>", payload: "."},
	{name: "session below a dotfiles home", dotfiles: true, cwd: "notes", payload: "notes"},
	{name: "payload cwd below a dotfiles home", dotfiles: true, cwd: "<neutral>", payload: "notes"},
}

// TestHomeRoot_EveryHookRecordsNothingAndOnlySessionStartSpeaks drives all seven host hooks, by
// their manifest entry points, in a session whose project root resolves to the home directory in
// each of the ways it can. Every hook exits 0; SessionStart answers with the one host message and
// every other hook with {}; and nothing under the home is created — no run/, index/, objects/,
// state/, checkpoints/, spool, daemon.lock or spawn lock — and no daemon is started.
func TestHomeRoot_EveryHookRecordsNothingAndOnlySessionStartSpeaks(t *testing.T) {
	schema := loadHostHookSchema(t)
	for _, a := range homeArrivals {
		t.Run(a.name, func(t *testing.T) {
			f := newHomeFixture(t, a.dotfiles)
			f.dir(t, "notes", false)
			neutral := filepath.Join(t.TempDir(), "neutral")
			require.NoError(t, os.MkdirAll(filepath.Join(neutral, ".git"), 0o700))

			where := func(rel string) string {
				switch rel {
				case "<neutral>":
					return neutral
				case ".":
					return f.home
				}
				return filepath.Join(f.home, rel)
			}
			env := map[string]string{}
			for k, v := range a.env {
				if v == "<home>" {
					v = f.home
				}
				env[k] = v
			}
			if a.env == nil {
				// Without the override, the walk decides; make sure it decides what the row says.
				requireResolvesTo(t, where(a.payload), f.home)
				if a.cwd != "<neutral>" {
					requireResolvesTo(t, where(a.cwd), f.home)
				}
			}
			f.seal(t)
			t.Chdir(where(a.cwd))

			for _, e := range pluginmanifest.HookEntryPoints() {
				var out, errw bytes.Buffer
				code := Dispatch(context.Background(), All(), entryArgv(e), Env{
					Getenv:  envWith(env),
					Stdin:   bytes.NewReader(entryPayload(t, e.Event, where(a.payload))),
					Clock:   testClock(),
					HomeDir: f.home,
					Self:    noSuchSelf(t),
				}, &out, &errw)
				require.Equal(t, ExitOK, code, "%s: a hook always exits 0; stderr=%s", e.Event, errw.String())
				require.Empty(t, hostSchemaViolations(schema, e.Event, out.Bytes()),
					"%s: the refusal must be output the host accepts: %s", e.Event, out.String())

				if e.Event == hookio.EventSessionStart {
					var got hookio.Output
					require.NoError(t, json.Unmarshal(out.Bytes(), &got), "%s stdout: %s", e.Event, out.String())
					require.Equal(t, hookio.Output{SystemMessage: homeRootNotice}, got,
						"SessionStart must answer with the one host message and nothing else")
					continue
				}
				require.Equal(t, "{}\n", out.String(), "%s must stay silent in a refused session", e.Event)
			}
			f.requireUntouched(t, "every hook")
		})
	}
}

// TestHomeRoot_ASessionStartedInHomeStaysRefusedWhenAPayloadNamesAProject pins which root decides.
// A hook resolves the process's own root first and the payload's second, and either one being the
// home directory refuses the delivery. A session started in the home directory was told on
// SessionStart that Qompack records nothing in it; a later delivery whose payload cwd moved into a
// project below home must not quietly start recording into that project's store behind that
// notice — and must not spawn a daemon for it.
func TestHomeRoot_ASessionStartedInHomeStaysRefusedWhenAPayloadNamesAProject(t *testing.T) {
	f := newHomeFixture(t, false)
	proj := f.dir(t, "proj", true)
	requireResolvesTo(t, f.home, f.home)
	requireResolvesTo(t, proj, proj)
	f.seal(t)
	t.Chdir(f.home)

	for _, e := range pluginmanifest.HookEntryPoints() {
		var out, errw bytes.Buffer
		code := Dispatch(context.Background(), All(), entryArgv(e), Env{
			Getenv:  noEnv,
			Stdin:   bytes.NewReader(entryPayload(t, e.Event, proj)),
			Clock:   testClock(),
			HomeDir: f.home,
			Self:    noSuchSelf(t),
		}, &out, &errw)
		require.Equal(t, ExitOK, code, "%s: stderr=%s", e.Event, errw.String())
		if e.Event == hookio.EventSessionStart {
			require.Contains(t, out.String(), homeRootNotice, "SessionStart still says why")
			continue
		}
		require.Equal(t, "{}\n", out.String(), "%s stays silent", e.Event)
	}
	f.requireUntouched(t, "a session started in the home directory")
}

// TestHomeRoot_SessionStartNoticeIsShortAndSaysHowToFixIt pins the one host message: far under the
// host's 10,000-character field cap and under D15's 1,000-character ceiling for a whole banner, and
// it says both why Qompack is inactive and what to do about it.
func TestHomeRoot_SessionStartNoticeIsShortAndSaysHowToFixIt(t *testing.T) {
	// D15 (2026-09-25): a systemMessage banner stays under 1,000 host characters in total.
	const d15BannerCeiling = 1000
	require.LessOrEqual(t, hookio.HostChars(homeRootNotice), d15BannerCeiling)
	require.Contains(t, homeRootNotice, "home directory", "the notice says why")
	require.Contains(t, homeRootNotice, "project directory", "the notice says how to fix it")
	require.NotContains(t, homeRootNotice, "\n", "one line: the host shows it verbatim")
}

// TestHomeRoot_AProjectBelowHomeStillRecords is the regression half of D18: a project below the
// home directory — with its own .git, below a plain home or below a dotfiles home, or a plain
// directory below a plain home — records exactly as before, and the refusal's host message never
// appears there. The delivery lands in the project's own spool (no daemon runs in the test), the
// user-global layer is still applied, and the home's own tree is untouched.
func TestHomeRoot_AProjectBelowHomeStillRecords(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dotfiles bool
		rel      string
		git      bool
	}{
		{name: "a git project below a plain home", rel: "proj", git: true},
		{name: "a git project below a dotfiles home", dotfiles: true, rel: "proj", git: true},
		{name: "a plain directory below a plain home", rel: "plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHomeFixture(t, tc.dotfiles)
			root := f.dir(t, tc.rel, tc.git)
			requireResolvesTo(t, root, root)
			f.project(root)
			f.seal(t)
			t.Chdir(root)

			env := Env{Getenv: noEnv, Clock: testClock(), HomeDir: f.home}

			var out, errw bytes.Buffer
			env.Stdin = bytes.NewReader(entryPayload(t, hookio.EventSessionStart, root))
			require.Equal(t, ExitOK, Dispatch(context.Background(), All(), []string{"qompack", "session-start"}, env, &out, &errw))
			require.NotContains(t, out.String(), homeRootNotice, "a project below home is not refused")

			out.Reset()
			env.Stdin = bytes.NewReader(entryPayload(t, hookio.EventUserPromptSubmit, root))
			require.Equal(t, ExitOK, Dispatch(context.Background(), All(), []string{"qompack", "observe", "prompt"}, env, &out, &errw))
			spooled, err := os.ReadDir(paths.Long(paths.Of(root).Spool))
			require.NoError(t, err, "the prompt must reach the project's own spool")
			require.NotEmpty(t, spooled, "the prompt must be recorded in the project's spool")

			out.Reset()
			env.Stdin = nil
			require.Equal(t, ExitOK, Dispatch(context.Background(), All(),
				[]string{"qompack", "config", "print", "--provenance"}, env, &out, &errw), "stderr=%s", errw.String())
			requireUserLayerApplied(t, out.String(), f.home)

			f.requireUntouched(t, "a project below home")
		})
	}
}

// requireUserLayerApplied finds scheduler.softFloorPct in `config print --provenance` output and
// requires the user-global file's value and location on it.
func requireUserLayerApplied(t *testing.T, out, home string) {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "softFloorPct") {
			require.Contains(t, l, "0.42", "the user-global layer's value is in effect")
			require.Contains(t, l, "// user "+filepath.Join(paths.Global(home), "config.json"),
				"and attributed to the user layer's file")
			return
		}
	}
	require.FailNow(t, "provenance output never mentions softFloorPct", out)
}

// homeCommandEnv is the Env every command row runs with: the project pinned to the home by the
// override, and a Self that could spawn if anything tried.
func homeCommandEnv(t *testing.T, f *homeFixture, stdin string) Env {
	t.Helper()
	return Env{
		Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": f.home}),
		Stdin:   strings.NewReader(stdin),
		Clock:   testClock(),
		HomeDir: f.home,
		Self:    noSuchSelf(t),
	}
}

// TestHomeRoot_MCPToolsAnswerWithTheStableRefusal runs `qompack mcp` in a refused session. The
// server still speaks JSON-RPC and still lists the same eight tools, so the host's view of the
// plugin does not change; every tool call answers with mcp.HomeRootRefusedText as a tool error;
// stderr says why once; and nothing is laid out, logged or spawned under the home.
func TestHomeRoot_MCPToolsAnswerWithTheStableRefusal(t *testing.T) {
	f := newHomeFixture(t, false)
	f.seal(t)

	stdin := mcpCmdLines(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",`+
			`"clientInfo":{"name":"d18","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"recall","arguments":{"query":"pool timeout"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"expand","arguments":{"handle":"x"}}}`,
	)
	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), []string{"qompack", "mcp"}, homeCommandEnv(t, f, stdin), &out, &errw)
	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())
	require.Contains(t, errw.String(), "home directory", "stderr says why, once")

	responses := mcpCmdDecodeStream(t, out.String())
	require.Len(t, responses, 4, "four id-carrying requests, four responses:\n%s", out.String())

	list := mcpCmdCallResult(t, responses, "2")
	var tools []struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(list.Tools, &tools))
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	require.Equal(t, mcp.ToolNames(), names, "the refused server lists the same tools")

	for _, id := range []string{"3", "4"} {
		call := mcpCmdCallResult(t, responses, id)
		require.True(t, call.IsError, "id=%s: a refused call is a tool error the model reads", id)
		require.Equal(t, mcp.HomeRootRefusedText, mcpCmdText(call), "id=%s: the stable refusal, verbatim", id)
	}
	f.requireUntouched(t, "qompack mcp")
}

// TestHomeRoot_DaemonStartsNothing: `qompack daemon` for the home directory — however it was asked
// for — exits 0, as a lazily spawned daemon always does, says why on stderr, and takes no lock and
// lays out no store.
func TestHomeRoot_DaemonStartsNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"from the override", []string{"daemon"}},
		{"from --project", []string{"daemon", "--project", "<home>"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHomeFixture(t, false)
			f.seal(t)
			argv := []string{"qompack"}
			for _, a := range tc.argv {
				if a == "<home>" {
					a = f.home
				}
				argv = append(argv, a)
			}
			var out, errw bytes.Buffer
			code := Dispatch(context.Background(), All(), argv, homeCommandEnv(t, f, ""), &out, &errw)
			require.Equal(t, ExitOK, code, "a daemon never exits non-zero; stderr=%s", errw.String())
			require.Contains(t, errw.String(), "the project root is the home directory")
			f.requireUntouched(t, "qompack daemon")
		})
	}
}
