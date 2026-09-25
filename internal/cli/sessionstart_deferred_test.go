package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// C1.16's client half: a compact SessionStart the daemon never answers must not be answered {}.
// In the packaging lane's live session 2 the daemon's reply missed the hook client's 10 s deadline,
// the client wrote {}, and the model continued from a compacted context with no word of what it had
// lost. The daemon now bounds its own answer (internal/daemon session_start_compact.go), but a
// daemon that cannot be reached, or cannot answer at all, still leaves only the client to say so.

// unansweredProject is a project whose state record says the daemon is enabled and may act, with
// NO daemon listening at its address and no executable to spawn one (Env.Self is empty), so every
// Reply request is spooled unanswered — the same outcome as a reply that missed its deadline.
func unansweredProject(t *testing.T, mutate func(*ipc.State)) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	st := ipc.StateFromConfig(config.Defaults())
	if mutate != nil {
		mutate(&st)
	}
	require.NoError(t, ipc.WriteState(root, st))
	return root
}

// sessionStartEntry is the manifest's SessionStart entry point.
var sessionStartEntry = pluginmanifest.HookEntryPoint{Event: "SessionStart", Subcommand: "session-start"}

// runSessionStartWith drives `qompack session-start` against root with the given source.
func runSessionStartWith(t *testing.T, root, source string) []byte {
	t.Helper()
	payload := strings.Replace(string(entryPayload(t, "SessionStart", root)), `"source":"compact"`,
		`"source":"`+source+`"`, 1)
	return runEntryPointWithPayload(t, root, sessionStartEntry, []byte(payload))
}

// runEntryPointWithPayload is runEntryPoint with the payload supplied.
func runEntryPointWithPayload(t *testing.T, root string, e pluginmanifest.HookEntryPoint, payload []byte) []byte {
	t.Helper()
	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), entryArgv(e), Env{
		Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}),
		Stdin:   bytes.NewReader(payload),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code, "%s: a hook always exits 0; stderr=%s", e.Subcommand, errw.String())
	return out.Bytes()
}

// TestSessionStartCompact_UnansweredIsTheDeferredNote is the regression row: the explicit deferred
// note, host-conforming and within the host cap, instead of {}.
func TestSessionStartCompact_UnansweredIsTheDeferredNote(t *testing.T) {
	schema := loadHostHookSchema(t)
	root := unansweredProject(t, nil)

	stdout := runSessionStartWith(t, root, "compact")
	require.Empty(t, hostSchemaViolations(schema, "SessionStart", stdout), "stdout=%s", stdout)
	out := string(stdout)
	require.Contains(t, out, daemon.DeferredNoteTag, "an unanswered compact SessionStart must say so, not answer {}")
	require.Contains(t, out, daemon.DeferredNoAnswer)
	require.Contains(t, out, "expand(tool_use_id=prompt_sess-host-contract_0)")
}

// TestSessionStartCompact_UnansweredNoteOnlyWhereARehydrationWasDue: every other unanswered case
// keeps {} — a non-compact source has no rehydration to lose, and a disabled daemon, a mode that may
// not act (§12.1) or runtime.mode off/passive is Qompack deliberately injecting nothing.
func TestSessionStartCompact_UnansweredNoteOnlyWhereARehydrationWasDue(t *testing.T) {
	for _, source := range []string{"startup", "resume", "clear", ""} {
		t.Run("source="+source, func(t *testing.T) {
			require.Equal(t, "{}\n", string(runSessionStartWith(t, unansweredProject(t, nil), source)))
		})
	}
	t.Run("daemon disabled", func(t *testing.T) {
		root := unansweredProject(t, func(s *ipc.State) { s.DaemonEnabled = false })
		require.Equal(t, "{}\n", string(runSessionStartWith(t, root, "compact")))
	})
	t.Run("degraded passive", func(t *testing.T) {
		root := unansweredProject(t, func(s *ipc.State) { s.Mode = contract.ModeDegradedPassive })
		require.Equal(t, "{}\n", string(runSessionStartWith(t, root, "compact")))
	})
	for _, mode := range []string{"passive", "off"} {
		t.Run("runtime.mode="+mode, func(t *testing.T) {
			root := unansweredProject(t, nil)
			writeProjectConfig(t, root, `{"runtime":{"mode":"`+mode+`"}}`)
			require.Equal(t, "{}\n", string(runSessionStartWith(t, root, "compact")))
		})
	}
	t.Run("reinjection switched off", func(t *testing.T) {
		root := unansweredProject(t, nil)
		writeProjectConfig(t, root, `{"runtime":{"migration":{"reinjection":{"sessionStartCompact":false}}}}`)
		require.Equal(t, "{}\n", string(runSessionStartWith(t, root, "compact")))
	})
}
