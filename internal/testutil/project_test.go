package testutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// TestProject_LayoutAndCleanup asserts NewProject produces a complete §3.3 layout, points the
// environment at it, and — when the test that created it finishes — takes the whole tree with it
// while touching nothing outside t.TempDir().
func TestProject_LayoutAndCleanup(t *testing.T) {
	envBefore := os.Getenv("QOMPACK_PROJECT_ROOT")

	// A file outside the subtest's own temp tree. Cleanup must not touch it.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("untouched"), filePerm))

	var innerRoot, innerHome string

	t.Run("disposable", func(t *testing.T) {
		p := NewProject(t)
		innerRoot, innerHome = p.Root, p.Home()

		// t.TempDir returns <per-test base>/NNN; every call inside one test shares that base, so
		// a shared prefix is exactly the assertion "NewProject used this test's own t.TempDir".
		base := filepath.Dir(t.TempDir())
		require.True(t, strings.HasPrefix(p.Root, base), "the project root must live under t.TempDir(), got %s", p.Root)
		require.True(t, strings.HasPrefix(p.Home(), base), "the user-global home must live under t.TempDir(), got %s", p.Home())

		l := paths.Of(p.Root)
		for _, dir := range []string{
			l.Dot, l.Objects, l.Index, l.Sketches, l.DAG, l.Grammar, l.Checkpoints, l.Pins,
			filepath.Join(l.Eval, "replay"), filepath.Join(l.Eval, "opt"),
			l.Records, l.State, l.Run, l.Spool, l.Logs, l.Metrics, l.Tmp,
		} {
			require.DirExists(t, dir)
		}

		gitignore, err := os.ReadFile(filepath.Join(l.Dot, ".gitignore"))
		require.NoError(t, err)
		require.Equal(t, "*\n", string(gitignore), "the store self-ignores (§3.3)")

		require.Equal(t, p.Root, os.Getenv("QOMPACK_PROJECT_ROOT"),
			"NewProject sets QOMPACK_PROJECT_ROOT in the process environment, not only in an injected Getenv")
		require.Equal(t, p.Home(), os.Getenv("HOME"))
		require.Equal(t, p.Home(), os.Getenv("USERPROFILE"))

		resolved, err := paths.Resolve(p.Getenv, "")
		require.NoError(t, err)
		require.Equal(t, p.Root, resolved)

		require.Equal(t, config.Defaults(), p.Cfg, "a project with no config file loads exactly the defaults")
		require.NotNil(t, p.Log)
		require.Equal(t, Epoch, p.Clock.Now())

		require.NoDirExists(t, filepath.Join(p.Root, ".git"),
			"without WithGit the project deliberately has no .git, so paths.Resolve's step-3 fallback is the one exercised")
	})

	require.NoDirExists(t, innerRoot, "the project root is removed when the test that created it finishes")
	require.NoDirExists(t, innerHome, "so is the user-global home")
	require.FileExists(t, outside, "cleanup must remove nothing outside the project's own temp tree")
	require.Equal(t, envBefore, os.Getenv("QOMPACK_PROJECT_ROOT"), "t.Setenv restores the environment")
}

// TestProject_WithGitExercisesTheDotGitWalk asserts WithGit produces a real working tree, so that
// paths.Resolve reaches the root through §3.3 step 2 (the .git walk) and not only through
// QOMPACK_PROJECT_ROOT.
func TestProject_WithGitExercisesTheDotGitWalk(t *testing.T) {
	p := NewProject(t, WithGit())
	require.DirExists(t, filepath.Join(p.Root, ".git"))

	// Resolve from a nested directory with the environment override deliberately absent: only the
	// .git walk can find the root from here.
	nested := filepath.Join(p.Root, "src", "deep")
	require.NoError(t, os.MkdirAll(nested, dirPerm))

	root, err := paths.Resolve(func(string) string { return "" }, nested)
	require.NoError(t, err)
	require.Equal(t, p.Root, root)
}

// projectConfigJSON is the project-file fixture TestProject_Options writes: one §11.5 runtime key
// set away from its default, which is the smallest thing that can prove the project file layer
// actually reached the loaded Config.
const projectConfigJSON = `{"runtime":{"logging":{"level":"debug"}}}`

// TestProject_Options asserts the remaining four ProjectOpts land where they claim to.
func TestProject_Options(t *testing.T) {
	t0 := time.Date(2027, time.March, 4, 5, 6, 7, 0, time.UTC)
	p := NewProject(t,
		WithConfig(projectConfigJSON),
		WithEnv("QOMPACK_TESTUTIL_MARKER", "set"),
		WithFiles(map[string]string{"src/a.ts": "export const a = 1;\n"}),
		WithClock(t0),
	)

	cfgBytes, err := os.ReadFile(filepath.Join(paths.Of(p.Root).Dot, "config.json"))
	require.NoError(t, err)
	require.JSONEq(t, projectConfigJSON, string(cfgBytes))
	require.Equal(t, "debug", p.Cfg.Runtime.Logging.Level,
		"the project file layer must reach the loaded Config")
	require.NotEqual(t, config.Defaults().Runtime.Logging.Level, p.Cfg.Runtime.Logging.Level,
		"the fixture has to differ from the default, or the assertion above proves nothing")

	require.Equal(t, "set", p.Getenv("QOMPACK_TESTUTIL_MARKER"))
	require.Equal(t, "set", os.Getenv("QOMPACK_TESTUTIL_MARKER"))

	src, err := os.ReadFile(filepath.Join(p.Root, "src", "a.ts"))
	require.NoError(t, err)
	require.Equal(t, "export const a = 1;\n", string(src))

	require.Equal(t, t0, p.Clock.Now())

	// The method form appends to an already-built project and chains.
	require.Same(t, p, p.WithFiles(t, map[string]string{"src/b.ts": "export const b = 2;\n"}))
	require.FileExists(t, filepath.Join(p.Root, "src", "b.ts"))
}

// TestProject_StoreOpens asserts (*Project).Store hands back a usable store.Store wired to this
// project's own config, logger and clock.
//
// SP-06 landed the real store, so this now asserts that a Put actually stores: the wave-0 form of
// this test required core.ErrNotImplemented, which a working store no longer returns.
func TestProject_StoreOpens(t *testing.T) {
	p := NewProject(t)
	s := p.Store(t)
	require.NotNil(t, s)

	res, err := s.PutBytes(t.Context(), []byte("hello"), store.PutOptions{})
	require.NoError(t, err)
	require.False(t, res.Root.Hash.IsZero(), "a real store must report a content root for what it stored")
}

// recorder captures what an assertion reports, so a test can assert that the assertion FAILED
// without failing itself.
type recorder struct {
	msgs []string
}

// Helper satisfies errorReporter; there is no stack to trim in a recorder.
func (r *recorder) Helper() {}

// Errorf records one reported failure.
func (r *recorder) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

// weakenedSeam is a deliberately broken set of write primitives: plain os calls with the §7.4
// append-only guard removed. Substituting it is what proves AssertAppendOnly has teeth.
//
// Reaching for this instead of a build-tagged shim inside internal/paths is deliberate. A shim
// there cannot weaken paths.OpenFile without excluding the real one, which means editing
// appendonly.go — shipping a build tag whose whole purpose is to disable a safety invariant into
// the production package. The seam keeps the weakening entirely inside the test that needs it,
// and still exercises the real paths.OpenFile, paths.WriteAtomic and paths.CreateNew in every
// other call site, including AssertAppendOnly's own default.
func weakenedSeam() writeSeam {
	return writeSeam{
		// os.OpenFile and os.WriteFile ARE the weakening: they are what paths.OpenFile and
		// paths.WriteAtomic reduce to once the §7.4 guard in front of them is removed.
		OpenFile:    os.OpenFile,
		WriteAtomic: os.WriteFile,
		CreateNew: func(p string, b []byte) error {
			// Not os.WriteFile directly: CreateNew's whole contract is O_EXCL, so the weakened
			// form has to be the same write WITHOUT it.
			return os.WriteFile(p, b, filePerm)
		},
	}
}

// TestProject_AssertAppendOnly asserts the §3.3 conformance list passes against a correct layout
// and — with the guard weakened — reports all four operations, which is the only way to know the
// assertion is doing work rather than passing vacuously.
func TestProject_AssertAppendOnly(t *testing.T) {
	t.Run("passes against a correct layout", func(t *testing.T) {
		p := NewProject(t)
		p.AssertAppendOnly(t)
	})

	t.Run("reports every operation once the guard is weakened", func(t *testing.T) {
		p := NewProject(t)
		p.seam = weakenedSeam()

		rec := &recorder{}
		p.assertAppendOnly(rec)

		require.Len(t, rec.msgs, 4,
			"all four §3.3 operations must be reported when the append-only guard is gone; got:\n%s",
			strings.Join(rec.msgs, "\n"))
		for _, want := range []string{
			"a truncating write to checkpoints/0001.json",
			"an in-place rewrite of pins/invariants.jsonl",
			"paths.WriteAtomic onto sketches/tried.bloom",
			"a SECOND write of checkpoints/0001.json",
		} {
			require.Contains(t, strings.Join(rec.msgs, "\n"), want)
		}
	})
}

// TestProject_RunHookInProcess asserts the default, in-process half of §6.2's two-mode
// requirement: all six hooks run through cli.Dispatch and every one exits 0 with a parseable
// response.
//
// SP-05 (this repo's own next subplan after SP-01) replaces the six hook bodies with thin ipc
// clients: SessionStart's and PreCompact's hookSpecificOutput now comes from a live daemon's
// Response, not from a CLI-side stub, so with no daemon reachable (the case here — an in-process
// dispatch never spawns one; see internal/cli's selfPath) every hook's response is the minimal
// "{}" shape. test/e2e's daemon_e2e_test.go asserts the round trip against a real, running daemon.
func TestProject_RunHookInProcess(t *testing.T) {
	p := NewProject(t)

	for _, name := range HookNames() {
		out := p.RunHook(t, name, eventFor(name, p.Root))
		require.Nil(t, out.HookSpecificOutput,
			"%s: with no daemon reachable, the thin client answers with the minimal response", name)
	}
}

// TestProject_RunHookAcceptsSubcommandSpelling asserts the subcommand spelling reaches the same
// entry point as the host event name, so a test may write whichever names the contract it is
// reasoning about.
func TestProject_RunHookAcceptsSubcommandSpelling(t *testing.T) {
	p := NewProject(t)
	p.RunHook(t, "observe tool", eventFor("PostToolUse", p.Root))
	p.RunHook(t, "session-start", eventFor("SessionStart", p.Root))
}

// eventFor builds a representative payload for one hook: the fields §5.3 says that hook populates,
// and a cwd that is a real native path, because a hook refuses to create a store under a root that
// does not exist.
func eventFor(name, cwd string) hookio.Event {
	e := hookio.Event{
		HookEventName:  name,
		SessionID:      core.SessionID("sess-testutil-0001"),
		TranscriptPath: filepath.Join(cwd, "transcript.jsonl"),
		CWD:            cwd,
	}
	switch name {
	case "PostToolUse":
		e.ToolName = "Read"
		e.ToolUseID = core.ToolUseID("toolu_01A2B3C4D5E6F7G8H9J0K1L2")
		e.ToolInput = json.RawMessage(`{"file_path":"src/auth.ts"}`)
		e.ToolResponse = json.RawMessage(`{"content":"export const auth = 1;"}`)
	case "UserPromptSubmit":
		e.Prompt = "why does the auth middleware reject an expired token twice?"
	case "SessionStart":
		e.Source = "startup"
	case "PreCompact":
		e.Trigger = "auto"
	case "Stop", "SubagentStop":
		e.StopHookActive = true
	}
	return e
}
