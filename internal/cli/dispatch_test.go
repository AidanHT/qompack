package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
)

// fakeClock is a frozen Clock. internal/testutil does not exist yet (it lands in this subplan's
// test-scaffolding phase, which may import cli but not the reverse), so cli's own tests carry the
// three lines they need rather than inverting the dependency.
type fakeClock struct{ t time.Time }

func (f fakeClock) Now() time.Time                  { return f.t }
func (f fakeClock) Since(t time.Time) time.Duration { return f.t.Sub(t) }

func testClock() core.Clock {
	return fakeClock{t: time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)}
}

// errReader fails on the first Read, standing in for a stdin the host closed or never opened.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("stdin is unreadable") }

// noEnv is a Getenv that reports every variable as unset, so a test never inherits the developer's
// own QOMPACK_* overrides.
func noEnv(string) string { return "" }

// envWith returns a Getenv serving only the given pairs.
func envWith(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

// hookNames is the six hook subcommands of §7.3, in the order §2.3 lists them.
var hookNames = []string{
	"observe tool",
	"observe prompt",
	"observe stop",
	"session-start",
	"checkpoint",
	"flush",
}

// argvFor splits a subcommand name into argv form, so "observe tool" becomes two arguments.
func argvFor(name string, extra ...string) []string {
	return append(append([]string{"qompack"}, strings.Fields(name)...), extra...)
}

// validPayload is a well-formed hook event rooted at dir.
func validPayload(t *testing.T, dir string) io.Reader {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"session_id": "s-test",
		"cwd":        dir,
		"tool_name":  "FileRead",
	})
	require.NoError(t, err)
	return bytes.NewReader(b)
}

// TestDispatch_HookAlwaysExitsZero is the load-bearing test of the whole package: §2.3's rule that
// a hook subcommand exits 0 no matter what, across the six hooks and five distinct failure modes.
//
// It is deliberately a full cross product rather than five one-off tests. The rule is not "these
// particular failures are handled" but "no failure escapes", and a table is the only shape that
// keeps that true as hooks are added — a seventh hook inherits all five faults for free.
func TestDispatch_HookAlwaysExitsZero(t *testing.T) {
	t.Parallel()

	type fault struct {
		name string
		// setup returns the Env for the run and, optionally, a replacement command table. A nil
		// table means "use the real commands".
		setup func(t *testing.T, hook string) (Env, []Cmd)
	}

	faults := []fault{
		{
			name: "unreadable stdin",
			setup: func(t *testing.T, _ string) (Env, []Cmd) {
				return Env{Getenv: noEnv, Stdin: errReader{}, Clock: testClock()}, nil
			},
		},
		{
			name: "malformed JSON",
			setup: func(t *testing.T, _ string) (Env, []Cmd) {
				dir := t.TempDir()
				return Env{
					Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": dir}),
					Stdin:   strings.NewReader(`{"session_id": "s1", "cwd": ` + "\x00" + ` not json`),
					Clock:   testClock(),
					HomeDir: t.TempDir(),
				}, nil
			},
		},
		{
			// Named for what it actually exercises (fix round 1, Minor M-13): with
			// QOMPACK_PROJECT_ROOT pinned, paths.Resolve always succeeds outright (§3.3 step 1) —
			// "unresolvable" is no longer reachable through the new hook body at all. What this
			// case actually provokes is doHook's own isDir(root) refuse-to-conjure-a-store guard.
			name: "project root that does not exist",
			setup: func(t *testing.T, _ string) (Env, []Cmd) {
				// A payload with no cwd, and no QOMPACK_PROJECT_ROOT override, would make
				// resolveProjectRoot's pre-stdin guess (hookclient.go) fall back to the process's
				// own cwd — which, run under `go test ./internal/cli/...` from inside this
				// checkout, is a REAL, EXISTING directory that resolveProjectRoot then walks
				// upward from to the checkout's own .git, resolving to the checkout root itself.
				// (This was a real, confirmed hazard during task 6's own development: with no
				// override here, a hook's spool append landed inside the actual repository.)
				// Pinning QOMPACK_PROJECT_ROOT to a path that deliberately does not exist avoids
				// that entirely — paths.Resolve honours the env override outright (§3.3 step 1),
				// so every hook resolves to this exact, nonexistent, isolated path — and exercises
				// doHook's own isDir(root) refuse-to-create-a-store-under-a-missing-root guard.
				missing := filepath.Join(t.TempDir(), "does-not-exist")
				return Env{
					Getenv: envWith(map[string]string{"QOMPACK_PROJECT_ROOT": missing}),
					Stdin:  strings.NewReader(`{"session_id":"s1"}`),
					Clock:  testClock(),
				}, nil
			},
		},
		{
			name: "unwritable store",
			setup: func(t *testing.T, _ string) (Env, []Cmd) {
				// The plan names this fault "read-only .qompack". Expressed portably: put a
				// regular FILE where the store directory belongs. os.Chmod on a directory does
				// not stop writes on Windows, so a permission bit would make this case silently
				// pass there; a file-where-a-directory-must-be fails MkdirAll with ENOTDIR on
				// every platform, which is the condition the hook actually has to survive.
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, ".qompack"), []byte("not a dir"), 0o600))
				return Env{
					Getenv:  noEnv,
					Stdin:   validPayload(t, dir),
					Clock:   testClock(),
					HomeDir: t.TempDir(),
				}, nil
			},
		},
		{
			name: "panicking handler",
			setup: func(t *testing.T, hook string) (Env, []Cmd) {
				dir := t.TempDir()
				// Rebuild the table with this hook's body replaced by one that panics, keeping
				// Hook: true so the panic barrier is exercised on the path a real hook takes.
				cmds := make([]Cmd, 0, len(hookNames))
				for _, n := range hookNames {
					c := Cmd{Name: n, Hook: true, Summary: "test", Run: func(context.Context, Env, []string, io.Writer, io.Writer) error {
						return nil
					}}
					if n == hook {
						c.Run = func(context.Context, Env, []string, io.Writer, io.Writer) error {
							panic("injected panic in " + n)
						}
					}
					cmds = append(cmds, c)
				}
				return Env{
					Getenv:  noEnv,
					Stdin:   validPayload(t, dir),
					Clock:   testClock(),
					HomeDir: t.TempDir(),
				}, cmds
			},
		},
	}

	for _, hook := range hookNames {
		for _, f := range faults {
			t.Run(hook+"/"+f.name, func(t *testing.T) {
				env, cmds := f.setup(t, hook)
				if cmds == nil {
					cmds = All()
				}

				var out, errw bytes.Buffer
				code := Dispatch(context.Background(), cmds, argvFor(hook), env, &out, &errw)

				require.Equal(t, ExitOK, code,
					"hook %q under fault %q must exit 0; stderr=%s", hook, f.name, errw.String())

				var got hookio.Output
				require.NoError(t, json.Unmarshal(bytes.TrimSpace(out.Bytes()), &got),
					"hook %q under fault %q must write parseable hookio.Output, got %q",
					hook, f.name, out.String())
			})
		}
	}
}

// TestDispatch_NonHookErrorExitsOne proves the other half of the exit-code policy: an ordinary
// subcommand is allowed to fail, and says which one failed.
func TestDispatch_NonHookErrorExitsOne(t *testing.T) {
	t.Parallel()

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), []string{"qompack", "status"},
		Env{Getenv: noEnv, Stdin: strings.NewReader(""), Clock: testClock()}, &out, &errw)

	require.Equal(t, ExitError, code)
	require.Contains(t, errw.String(), "status")
}

// TestDispatch_UnknownCommandExitsTwo checks the usage path, including that the usage text lands on
// stderr rather than stdout — a hook host parsing stdout must never see help text.
func TestDispatch_UnknownCommandExitsTwo(t *testing.T) {
	t.Parallel()

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), []string{"qompack", "wat"},
		Env{Getenv: noEnv, Clock: testClock()}, &out, &errw)

	require.Equal(t, ExitUsage, code)
	require.Contains(t, errw.String(), "unknown subcommand")
	require.Contains(t, errw.String(), "usage: qompack")
	require.Empty(t, out.String(), "usage must not pollute stdout")
}

// TestDispatch_PanicRecovered asserts the panic barrier reports as well as recovers: §12 says a
// crash in the hot path is a degradation, and degradations are never silent.
func TestDispatch_PanicRecovered(t *testing.T) {
	cmds := []Cmd{{
		Name: "boom", Hook: true, Summary: "panics on purpose",
		Run: func(context.Context, Env, []string, io.Writer, io.Writer) error {
			panic("kaboom")
		},
	}}

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), cmds, []string{"qompack", "boom"},
		Env{Getenv: noEnv, Clock: testClock()}, &out, &errw)

	require.Equal(t, ExitOK, code)
	require.Contains(t, errw.String(), "panic recovered")

	var got hookio.Output
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(out.Bytes()), &got),
		"a panicking hook still owes the host valid JSON")

	loud := strings.Join(logging.LastLoud(), "\n")
	require.Contains(t, loud, "panic in subcommand")
	require.Contains(t, loud, "kaboom")
	require.Contains(t, loud, "stack", "the Loud line must carry a stack for post-mortem")
}
