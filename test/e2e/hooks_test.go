package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestMain removes the directories Build and buildNoInject compiled into. It lives here rather
// than in a file of its own because it is a few lines of process lifecycle, and the binaries it
// cleans up are only ever used by the tests in this package.
func TestMain(m *testing.M) {
	code := m.Run()
	removeBuild()
	removeNoInjectBuild()
	os.Exit(code)
}

// hookSubcommands is §7.3's six hook entry points paired with the host event name each answers.
// The list is written out rather than derived so that "all six hooks" cannot quietly become five.
var hookSubcommands = []struct {
	event string
	argv  []string
}{
	{"PostToolUse", []string{"observe", "tool"}},
	{"UserPromptSubmit", []string{"observe", "prompt"}},
	{"Stop", []string{"observe", "stop"}},
	{"SessionStart", []string{"session-start"}},
	{"PreCompact", []string{"checkpoint"}},
	{"SessionEnd", []string{"flush"}},
}

// TestE2E_AllSixHooksExitZero is the wave-0 end-to-end case, RESTORED UNDER ITS ORIGINAL NAME
// after fix round 1's Critical C-1: five downstream VERIFY plan documents name this test BY NAME
// as a verification command (plans/V1-SP-01-foundation-toolchain-and-contracts.md,
// V1/V2/V3/V4/V5/V6-VERIFY-*.md), and a build where `go test -run TestE2E_AllSixHooksExitZero
// ./test/e2e/` reports "[no tests to run]" (exit 0) makes every one of those checkpoints pass
// vacuously — the worst failure mode a verification gate can have.
//
// The real binary, all six hook subcommands, a real temp project, representative payloads, every
// exit code 0, every stdout parses as a hookio.Output — SP-05 makes this property MORE true, not
// obsolete, so it is still fully assertable. What is gone is wave-0's own artifact: the
// hooks-*.jsonl observation log SP-05 replaces with the real transport (hookLogLines), and the
// unconditional SessionStart/PreCompact hookSpecificOutput switch, which — now that a live daemon
// answers session-start with a real §12.1 sentinel and PreCompact answers only once an SP-10
// PreCompact seam exists (absent from a wave-1-only build; see IT-1's own comment in
// v1_integration_test.go) — only SessionStart can still assert positively without a later
// subplan's own seam being present.
func TestE2E_AllSixHooksExitZero(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	// session-start, among the six, brings up a real detached daemon; shut it down before this
	// test's own t.TempDir() cleanup runs (see e2eShutdownIfReachable's own doc comment).
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })

	for _, hook := range hookSubcommands {
		t.Run(hook.event, func(t *testing.T) {
			payload := payloadFor(t, hook.event, p.Root)

			stdout, stderr, code := Run(t, bin, hook.argv, payload, map[string]string{
				"QOMPACK_PROJECT_ROOT": p.Root,
				"HOME":                 p.Home(),
				"USERPROFILE":          p.Home(),
			})

			require.Equal(t, 0, code,
				"§2.3: a hook subcommand must exit 0 no matter what happens inside it\nstdout:\n%s\nstderr:\n%s",
				stdout, stderr)

			var out hookio.Output
			require.NoError(t, json.Unmarshal(stdout, &out),
				"a hook's stdout must always be a valid hookio.Output, got:\n%s", stdout)

			if hook.event == "SessionStart" {
				require.NotNil(t, out.HookSpecificOutput,
					"%s answers through hookSpecificOutput", hook.event)
				require.Equal(t, hook.event, out.HookSpecificOutput.HookEventName)
			}
		})
	}
}

// TestE2E_ConfigPrintFromRealBinary asserts `qompack config print --json`, run as a real process
// against a project with no config file, emits exactly the built-in defaults. It is the end-to-end
// proof that the five-layer load pipeline contributes nothing when no layer above defaults exists.
func TestE2E_ConfigPrintFromRealBinary(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)

	require.NoFileExists(t, filepath.Join(paths.Of(p.Root).Dot, "config.json"))
	require.NoFileExists(t, filepath.Join(paths.Global(p.Home()), "config.json"))

	stdout, stderr, code := Run(t, bin, []string{"config", "print", "--json"}, nil, map[string]string{
		"QOMPACK_PROJECT_ROOT": p.Root,
		"HOME":                 p.Home(),
		"USERPROFILE":          p.Home(),
	})
	require.Equal(t, 0, code, "stderr:\n%s", stderr)

	var got config.Config
	require.NoError(t, json.Unmarshal(stdout, &got), "stdout:\n%s", stdout)
	require.Equal(t, config.Defaults(), got,
		"a project with no config file must print exactly config.Defaults()")
}

// TestE2E_RunHookRealBinaryMode asserts the OTHER half of §6.2's two-mode requirement: the same
// (*Project).RunHook that unit tests drive in process spawns the real binary when
// QOMPACK_E2E_BINARY names one, and every one of the six hooks still exits 0 with a parseable
// response — the same property TestProject_RunHookInProcess pins for the in-process mode.
func TestE2E_RunHookRealBinaryMode(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t, testutil.WithEnv(testutil.E2EBinaryEnv, bin))
	// session-start, among the six, brings up a real detached daemon; shut it down before this
	// test's own t.TempDir() cleanup runs, or a still-open log file handle can make that cleanup
	// fail on Windows (open-file delete semantics).
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })

	for _, name := range testutil.HookNames() {
		p.RunHook(t, name, eventFor(name, p.Root))
	}
}

// TestE2E_UnknownCommandExitsTwo asserts the other side of the §2.3 exit-code policy through the
// real binary: a subcommand that is not a hook may — and for an unknown command must — fail.
func TestE2E_UnknownCommandExitsTwo(t *testing.T) {
	bin := Build(t)

	stdout, stderr, code := Run(t, bin, []string{"wat"}, nil, nil)
	require.Equal(t, 2, code, "an unknown subcommand exits 2 (§2.3)")
	require.Empty(t, stdout)
	require.Contains(t, string(stderr), "wat")
}

// payloadFor marshals the representative payload for one hook the way a host writes it: compact
// JSON, HTML escaping off.
func payloadFor(t *testing.T, event, cwd string) []byte {
	t.Helper()
	b, err := json.Marshal(eventFor(event, cwd))
	require.NoError(t, err)
	return b
}

// eventFor builds a representative payload for one hook: the §5.3 fields that hook populates, and
// a cwd that is a real native path.
//
// The native path matters. A hook refuses to create a store when the resolved project root does
// not exist, so a POSIX-flavoured "/c/Users/..." cwd on Windows would resolve to a directory that
// is not there and the hook would answer correctly while observing nothing — a green test that
// asserted nothing.
func eventFor(event, cwd string) hookio.Event {
	e := hookio.Event{
		HookEventName:  event,
		SessionID:      core.SessionID("sess-e2e-0001"),
		TranscriptPath: filepath.Join(cwd, "transcript.jsonl"),
		CWD:            cwd,
	}
	switch event {
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
