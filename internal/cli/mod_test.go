package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/modwire"
	"github.com/qompack/qompack/internal/paths"
)

// modTestNow is the frozen instant every hello in this file is served at.
var modTestNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// modTestClock is a frozen core.Clock.
type modTestClock struct{ t time.Time }

func (c modTestClock) Now() time.Time                  { return c.t }
func (c modTestClock) Since(t time.Time) time.Duration { return c.t.Sub(t) }

// modProject is a project root with a .git marker, so root resolution stops there, and a separate
// home directory, so the home-root refusal applies only where a test asks for it.
type modProject struct {
	root, home string
}

// newModProject makes the project. withStore also creates its .qompack store, which the hello verb
// requires and never creates itself.
func newModProject(t *testing.T, withStore bool) modProject {
	t.Helper()
	p := modProject{root: t.TempDir(), home: t.TempDir()}
	require.NoError(t, os.MkdirAll(filepath.Join(p.root, ".git"), 0o700))
	if withStore {
		require.NoError(t, os.MkdirAll(paths.Of(p.root).Dot, 0o700))
	}
	return p
}

// env is the Env a mod verb runs with in these tests: no ambient environment, the project's home,
// a frozen clock and the request on stdin.
func (p modProject) env(stdin string, getenv map[string]string) Env {
	return Env{
		Getenv:  func(k string) string { return getenv[k] },
		Stdin:   strings.NewReader(stdin),
		Clock:   modTestClock{t: modTestNow},
		HomeDir: p.home,
	}
}

// helloRequest is a hello request for cwd, as the mod writes it.
func helloRequest(t *testing.T, cwd, hostVersion string) string {
	t.Helper()
	b, err := modwire.Encode(modwire.HelloRequest{
		V: modwire.Version, SessionID: "sess-1", CWD: cwd, HostVersion: hostVersion,
	})
	require.NoError(t, err)
	return string(b)
}

// runMod dispatches `qompack mod <args...>` exactly as the binary does and returns the exit code
// and the two streams.
func runMod(t *testing.T, env Env, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errw bytes.Buffer
	code = Dispatch(context.Background(), All(), append([]string{"qompack", "mod"}, args...), env, &out, &errw)
	return code, out.String(), errw.String()
}

// decodeHelloOut reads stdout as exactly one hello response line.
func decodeHelloOut(t *testing.T, stdout string) modwire.HelloResponse {
	t.Helper()
	require.True(t, strings.HasSuffix(stdout, "\n"), "one line, newline-terminated: %q", stdout)
	require.Equal(t, 1, strings.Count(stdout, "\n"), "exactly one JSON object: %q", stdout)
	var r modwire.HelloResponse
	require.NoError(t, json.Unmarshal([]byte(stdout), &r), "stdout: %q", stdout)
	require.NoError(t, r.Validate())
	return r
}

// helloFilePath is where a served hello for sess-1 is written.
func (p modProject) helloFilePath() string {
	return filepath.Join(paths.Of(p.root).State, "mod-hello-sess-1.json")
}

// treeOf lists every path under the given roots, directories included, so a test can show a refused
// hello created and changed nothing.
func treeOf(t *testing.T, roots ...string) []string {
	t.Helper()
	var got []string
	for _, r := range roots {
		require.NoError(t, filepath.WalkDir(r, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// os.Lstat, not d.Info: on Windows the walk's entry carries the parent directory's copy
			// of a child directory's times, which lags a change made just before the snapshot.
			info, ierr := os.Lstat(p)
			if ierr != nil {
				return ierr
			}
			got = append(got, p+"|"+info.ModTime().String())
			return nil
		}))
	}
	sort.Strings(got)
	return got
}

// enableMod is the --set flag that turns runtime.mod.enabled on for one invocation.
var enableMod = []string{"--set", "runtime.mod.enabled=true"}

func TestModHello_ServedWritesTheHelloFile(t *testing.T) {
	p := newModProject(t, true)
	code, stdout, stderr := runMod(t, p.env(helloRequest(t, p.root, "2.1.300"), nil),
		append([]string{"hello"}, enableMod...)...)
	require.Equal(t, ExitOK, code, stderr)

	require.Equal(t, modwire.HelloResponse{Status: modwire.Served(), Enabled: true}, decodeHelloOut(t, stdout))

	b, err := os.ReadFile(p.helloFilePath())
	require.NoError(t, err, "a served hello writes state/mod-hello-<session>.json")
	h, err := modwire.ParseHelloFile(b)
	require.NoError(t, err)
	require.Equal(t, modwire.HelloFile{
		SessionID: "sess-1", HostVersion: "2.1.300", TS: core.UnixMilli(modTestNow.UnixMilli()),
	}, h)
}

func TestModHello_BelowTheMinimumIsServedButNotEnabled(t *testing.T) {
	for _, version := range []string{"2.1.299", "2.1.300-dev.20261014.t101500.sha1a2b3c4", "not a version"} {
		t.Run(version, func(t *testing.T) {
			p := newModProject(t, true)
			code, stdout, stderr := runMod(t, p.env(helloRequest(t, p.root, version), nil),
				append([]string{"hello"}, enableMod...)...)
			require.Equal(t, ExitOK, code, stderr)
			require.Equal(t, modwire.HelloResponse{Status: modwire.Served(), Enabled: false},
				decodeHelloOut(t, stdout))

			b, err := os.ReadFile(p.helloFilePath())
			require.NoError(t, err, "the hello file records the host version even when it is too old")
			h, err := modwire.ParseHelloFile(b)
			require.NoError(t, err)
			require.Equal(t, version, h.HostVersion)
		})
	}
}

func TestModHello_SwitchOnThroughTheProjectConfigFile(t *testing.T) {
	p := newModProject(t, true)
	require.NoError(t, os.WriteFile(filepath.Join(paths.Of(p.root).Dot, "config.json"),
		[]byte(`{"runtime":{"mod":{"enabled":true}}}`), 0o600))

	code, stdout, stderr := runMod(t, p.env(helloRequest(t, p.root, "2.1.301"), nil), "hello")
	require.Equal(t, ExitOK, code, stderr)
	require.True(t, decodeHelloOut(t, stdout).Enabled)
	require.FileExists(t, p.helloFilePath())
}

// TestModHello_RefusedWritesNothing covers the three refusals: the switch off (the default), a
// project with no store, and a home-directory root. Each answers refused, exits 0, and leaves the
// project and the home byte for byte and entry for entry as they were.
func TestModHello_RefusedWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name      string
		withStore bool
		homeRoot  bool
		// stateOffAlive leaves a state.bin that says runtime.mode off, written by a daemon that is
		// still alive, so it speaks for the project as it does for the hooks.
		stateOffAlive bool
		args          []string
	}{
		{name: "switch off by default", withStore: true, args: []string{"hello"}},
		{name: "switch off explicitly", withStore: true, args: []string{"hello", "--set", "runtime.mod.enabled=false"}},
		{name: "no store", withStore: false, args: append([]string{"hello"}, enableMod...)},
		{name: "home root", withStore: true, homeRoot: true, args: append([]string{"hello"}, enableMod...)},
		{
			name: "mode off in the configuration", withStore: true,
			args: append([]string{"hello", "--set", "runtime.mode=off"}, enableMod...),
		},
		{
			name: "mode off in a live daemon's state.bin", withStore: true, stateOffAlive: true,
			args: append([]string{"hello"}, enableMod...),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newModProject(t, tc.withStore)
			if tc.homeRoot {
				p.home = p.root
			}
			if tc.stateOffAlive {
				require.NoError(t, paths.EnsureLayout(paths.Of(p.root)))
				staleModeOffState(t, p.root)
				useStateDaemonAlive(t, true)
			}
			before := treeOf(t, p.root, p.home)

			code, stdout, stderr := runMod(t, p.env(helloRequest(t, p.root, "2.1.300"), nil), tc.args...)
			require.Equal(t, ExitOK, code, stderr)
			r := decodeHelloOut(t, stdout)
			require.False(t, r.OK)
			require.Equal(t, modwire.ReasonRefused, r.Reason)
			require.False(t, r.Enabled)

			require.Equal(t, before, treeOf(t, p.root, p.home), "a refused hello creates and changes nothing")
		})
	}
}

// TestModHello_ADeadDaemonsModeOffDoesNotRefuse: a state.bin that says runtime.mode off speaks for
// the project only while the daemon that wrote it is alive, as for the hooks (stateModeOffHolds) and
// the command clients (modeFor). Left behind by a daemon that died, it defers to the configuration.
func TestModHello_ADeadDaemonsModeOffDoesNotRefuse(t *testing.T) {
	p := newModProject(t, true)
	require.NoError(t, paths.EnsureLayout(paths.Of(p.root)))
	staleModeOffState(t, p.root)
	useStateDaemonAlive(t, false)

	code, stdout, stderr := runMod(t, p.env(helloRequest(t, p.root, "2.1.300"), nil),
		append([]string{"hello"}, enableMod...)...)
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, modwire.HelloResponse{Status: modwire.Served(), Enabled: true}, decodeHelloOut(t, stdout))
	require.FileExists(t, p.helloFilePath())
}

// TestModHello_ResolvesTheRootLikeTheHooks: the mod sends the session root, which for a session
// started in a subdirectory is that subdirectory; the verb walks up to the .git as the hooks do, or
// takes QOMPACK_PROJECT_ROOT when it is set.
func TestModHello_ResolvesTheRootLikeTheHooks(t *testing.T) {
	t.Run("subdirectory", func(t *testing.T) {
		p := newModProject(t, true)
		sub := filepath.Join(p.root, "pkg", "deep")
		require.NoError(t, os.MkdirAll(sub, 0o700))

		code, stdout, stderr := runMod(t, p.env(helloRequest(t, sub, "2.1.300"), nil),
			append([]string{"hello"}, enableMod...)...)
		require.Equal(t, ExitOK, code, stderr)
		require.True(t, decodeHelloOut(t, stdout).OK)
		require.FileExists(t, p.helloFilePath(), "the hello lands in the root's store")
		require.NoDirExists(t, filepath.Join(sub, ".qompack"), "no store is made in the subdirectory")
	})

	t.Run("QOMPACK_PROJECT_ROOT", func(t *testing.T) {
		p := newModProject(t, true)
		elsewhere := t.TempDir()
		code, stdout, stderr := runMod(t,
			p.env(helloRequest(t, elsewhere, "2.1.300"), map[string]string{"QOMPACK_PROJECT_ROOT": p.root}),
			append([]string{"hello"}, enableMod...)...)
		require.Equal(t, ExitOK, code, stderr)
		require.True(t, decodeHelloOut(t, stdout).OK)
		require.FileExists(t, p.helloFilePath())
		require.NoDirExists(t, filepath.Join(elsewhere, ".qompack"))
	})
}

// TestModHello_AnUnreadableRequestIsAnErrorObject: a request the verb cannot accept is answered
// with the bare error object, exit 0, and nothing is written.
func TestModHello_AnUnreadableRequestIsAnErrorObject(t *testing.T) {
	for name, stdin := range map[string]string{
		"empty":           "",
		"not json":        "hello",
		"no version":      `{"session_id":"s","cwd":"/p","host_version":"2.1.300"}`,
		"unknown version": `{"v":2,"session_id":"s","cwd":"/p","host_version":"2.1.300"}`,
		"unsafe session":  `{"v":1,"session_id":"../x","cwd":"/p","host_version":"2.1.300"}`,
		"oversize":        `{"v":1,"pad":"` + strings.Repeat("x", modwire.MaxMessageBytes) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := newModProject(t, true)
			before := treeOf(t, p.root, p.home)
			code, stdout, stderr := runMod(t, p.env(stdin, nil), append([]string{"hello"}, enableMod...)...)
			require.Equal(t, ExitOK, code, stderr)

			var st modwire.Status
			require.NoError(t, json.Unmarshal([]byte(stdout), &st), "stdout: %q", stdout)
			require.Equal(t, modwire.Version, st.V)
			require.False(t, st.OK)
			require.Equal(t, modwire.ReasonError, st.Reason)
			require.NotEmpty(t, st.Detail)
			require.Equal(t, before, treeOf(t, p.root, p.home))
		})
	}
}

// TestModHello_ExtraArgumentsAreAnError: the verb takes its input on stdin only.
func TestModHello_ExtraArgumentsAreAnError(t *testing.T) {
	p := newModProject(t, true)
	code, stdout, stderr := runMod(t, p.env(helloRequest(t, p.root, "2.1.300"), nil),
		append([]string{"hello", "extra"}, enableMod...)...)
	require.Equal(t, ExitOK, code, stderr)
	r := decodeHelloOut(t, stdout)
	require.False(t, r.OK)
	require.Equal(t, modwire.ReasonError, r.Reason)
	require.NoFileExists(t, p.helloFilePath())
}

// TestModHello_AFailedWriteIsAnError: a hello file that cannot be written is not a served hello.
func TestModHello_AFailedWriteIsAnError(t *testing.T) {
	p := newModProject(t, true)
	// A file where the state directory belongs makes the write fail.
	require.NoError(t, os.WriteFile(paths.Of(p.root).State, []byte("not a directory"), 0o600))

	code, stdout, stderr := runMod(t, p.env(helloRequest(t, p.root, "2.1.300"), nil),
		append([]string{"hello"}, enableMod...)...)
	require.Equal(t, ExitOK, code, stderr)
	r := decodeHelloOut(t, stdout)
	require.False(t, r.OK)
	require.Equal(t, modwire.ReasonError, r.Reason)
	require.False(t, r.Enabled)
}

// TestModHello_IsNotAHookEntryPoint: the mod verbs answer the mod, not the host, so they are listed
// as ordinary commands and their output is the modwire object, never hookio's.
func TestModHello_IsNotAHookEntryPoint(t *testing.T) {
	var found bool
	for _, c := range All() {
		if c.Name == "mod hello" {
			found = true
			require.False(t, c.Hook)
			require.False(t, c.SilentStdout)
		}
	}
	require.True(t, found, "`qompack mod hello` is registered")
}
