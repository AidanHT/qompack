package cli

import (
	"context"
	"io"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// runSessionStart is `qompack session-start`'s body: session-start is the designated daemon
// starter (§2.4, §5.21's split-ownership table — "SP-05: qompack session-start dispatch, daemon
// start, contract.Monitor.RunAll before any other work"), and its hook timeout is generous (15s
// manifest timeout, 10s reply deadline) precisely because bringing a cold daemon up is not
// instantaneous. It otherwise follows the same doHook skeleton as every other hook, with one
// addition: preSend calls daemon.EnsureRunningUntil before the client ever attempts to connect. If
// the daemon fails to come up, Send still runs — it spools — and the hook still answers with (at
// worst) an empty response, or the deferred note for a compaction, and exits 0, per §2.3.
//
// The whole invocation is bounded by the manifest timeout (V6 close-out D17b): of its 15 s,
// hookExitReserve (1.5 s) is kept for the process's start and exit, the reply deadline (10 s) and
// the dial (hookConnectDeadlineFloor, 250 ms) come last, and what is left before them — 3.25 s from
// doHook's first statement — is everything up to and including the daemon start, whose poll stops
// there. Staging a binary and creating the process are never cut short, and a daemon started late
// still gets the 1.5 s EnsureRunning always gave it to come up; whatever runs over is taken from
// the reply wait instead (hookBudget.replyDeadline), so the hook still ends in time.
func runSessionStart(ctx context.Context, env Env, args []string, out, errw io.Writer) error {
	return doHook(sessionStartSpec(ensureDaemonRunning))(ctx, env, args, out, errw)
}

// sessionStartSpec is session-start's hookSpec with preSend supplied: ensureDaemonRunning in
// production.
func sessionStartSpec(preSend func(root, self string, st ipc.State, clk core.Clock, b hookBudget)) hookSpec {
	return hookSpec{
		op: ipc.OpSessionStart, reply: true, deadline: sessionStartReplyDeadline,
		hostTimeout: sessionStartHostTimeout(), preSend: preSend,
	}
}

// sessionStartHostTimeout is the SessionStart hook's manifest timeout, read from the manifest the
// plugin ships (internal/pluginmanifest) so the two cannot drift apart: the host cancels
// session-start when it runs out.
func sessionStartHostTimeout() time.Duration {
	for _, e := range pluginmanifest.HookEntryPoints() {
		if e.Event == hookio.EventSessionStart && e.TimeoutSeconds > 0 {
			return time.Duration(e.TimeoutSeconds) * time.Second
		}
	}
	return defaultSessionStartHostTimeout
}

// defaultSessionStartHostTimeout is sessionStartHostTimeout when the manifest names no SessionStart
// timeout, which only a malformed build can produce: the 15 s the manifest has always carried.
const defaultSessionStartHostTimeout = 15 * time.Second

// ensureDaemonRunning calls daemon.EnsureRunningUntil for session-start's preSend seam, bounding its
// poll by the hook's budget: the pre-send deadline, and the last instant a reply could still follow.
//
// It is a no-op under the daemon-down fault site (task-6-spec.md's table: that site's whole point
// is that nothing is listening AND nothing may be spawned in response), whenever self is ""
// (cli.Env.Self's own doc comment: every Env a test builds leaves it at the zero value, and any
// host environment where os.Executable() itself failed does too), and whenever
// runtime.daemon.enabled is false (fix round 2, FR-6): an operator who disabled the daemon still
// got a resident process spawned at every session start before this check existed — it served
// nothing (every client spools under DaemonEnabled=false, per ipc.Client.Send's own step 2), but
// its idle drain quietly processed the spool anyway, which an operator who typed "disabled" does
// not expect. None of the three conditions above should pay for a real spawn/dial attempt whose
// daemon can never do anything useful in response.
func ensureDaemonRunning(root, self string, st ipc.State, clk core.Clock, b hookBudget) {
	if _, on := faultActive(faultDaemonDown); on {
		return
	}
	if self == "" {
		return
	}
	if !st.DaemonEnabled {
		return
	}
	_, _ = daemon.EnsureRunningUntil(root, self, newHookLogger(root), clk, b.preSendBy, b.latestPoll)
}
