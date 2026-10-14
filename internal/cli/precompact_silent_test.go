//go:build !noinject

package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// PreCompact prints nothing. Claude Code 2.1.293 passes every successful PreCompact hook's stdout,
// even stdout that parsed as JSON, to the summarizer as additional instructions and shows it to the
// user. The empty object Qompack used to print therefore reached both. Empty stdout is dropped from
// those instructions, and "exit code 0 with no output" means the hook has nothing to report.
//
// runGuarded is the one choke point: it used to write the empty object itself for a hook that
// wrote nothing, after a normal return and after a panic, so these tests drive the checkpoint
// command down both fallback paths as well as the ordinary ones.

// runCheckpointUnderFault dispatches the PreCompact entry point with QOMPACK_FAULT set to site and
// returns its stdout and stderr, having asserted exit 0. root has generous deadlines and, unless
// the test starts one, no daemon.
func runCheckpointUnderFault(t *testing.T, root, site string) (stdout, stderr string) {
	t.Helper()
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, site)
	e := pluginmanifest.HookEntryPoint{Event: "PreCompact", Subcommand: "checkpoint"}
	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), entryArgv(e), Env{
		Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}),
		Stdin:   bytes.NewReader(entryPayload(t, e.Event, root)),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code, "a hook always exits 0; stderr=%s", errw.String())
	return out.String(), errw.String()
}

// TestPreCompact_StdoutIsEmptyWhenTheHookPanics covers both panic fault sites: panic:hook fires in
// doHook right after the bounded read, panic:client fires in the client's Send. Either one reaches
// runGuarded's recover, which must write nothing for PreCompact. The exit code is still 0.
func TestPreCompact_StdoutIsEmptyWhenTheHookPanics(t *testing.T) {
	for _, site := range []string{faultPanicHook, faultPanicClient} {
		t.Run(site, func(t *testing.T) {
			stdout, stderr := runCheckpointUnderFault(t, replyProject(t), site)
			require.Contains(t, stderr, "panic recovered", "the fault site must actually have panicked")
			require.Contains(t, stderr, site)
			require.Empty(t, stdout, "PreCompact prints nothing, even after a recovered panic")
		})
	}
}

// TestPreCompact_StdoutIsEmptyWhenTheDaemonIsDown covers the daemon-down fault site (an address
// nothing listens on, and no spawn) and a project where no daemon runs and none can be started
// (Env.Self is empty, which disables lazy spawn).
func TestPreCompact_StdoutIsEmptyWhenTheDaemonIsDown(t *testing.T) {
	t.Run("fault site", func(t *testing.T) {
		stdout, _ := runCheckpointUnderFault(t, replyProject(t), faultDaemonDown)
		require.Empty(t, stdout)
	})
	t.Run("no daemon", func(t *testing.T) {
		root := replyProject(t)
		stdout, _ := runCheckpointUnderFault(t, root, "")
		require.Empty(t, stdout)
		req := onlySpooledRequest(t, root)
		require.Equal(t, ipc.OpCheckpoint, req.Op, "the checkpoint request was spooled for a later daemon")
	})
}

// TestHookFallback_OtherHooksStillAnswerTheEmptyObject is the control for the two tests above: the
// silence is PreCompact's alone. Every other entry point, panicking at the same fault site, still
// gets runGuarded's empty object, byte for byte.
func TestHookFallback_OtherHooksStillAnswerTheEmptyObject(t *testing.T) {
	for _, e := range pluginmanifest.HookEntryPoints() {
		if e.Event == "PreCompact" {
			continue
		}
		t.Run(e.Event, func(t *testing.T) {
			resetFaultState(t)
			t.Setenv(qompackFaultEnv, faultPanicHook)
			root := replyProject(t)
			var out, errw bytes.Buffer
			code := Dispatch(context.Background(), All(), entryArgv(e), Env{
				Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}),
				Stdin:   bytes.NewReader(entryPayload(t, e.Event, root)),
				Clock:   testClock(),
				HomeDir: t.TempDir(),
			}, &out, &errw)
			require.Equal(t, ExitOK, code, "stderr=%s", errw.String())
			require.Contains(t, errw.String(), "panic recovered", "the fault site must actually have panicked")
			require.Equal(t, "{}\n", out.String(), "%s keeps runGuarded's fallback answer", e.Event)
		})
	}
}
