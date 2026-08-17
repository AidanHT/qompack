package cli

import (
	"context"
	"io"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
)

// runSessionStart is `qompack session-start`'s body: session-start is the designated daemon
// starter (§2.4, §5.21's split-ownership table — "SP-05: qompack session-start dispatch, daemon
// start, contract.Monitor.RunAll before any other work"), and its hook timeout is generous (15s
// manifest timeout, 10s reply deadline) precisely because bringing a cold daemon up is not
// instantaneous. It otherwise follows the same doHook skeleton as every other hook, with one
// addition: preSend calls daemon.EnsureRunning before the client ever attempts to connect. If the
// daemon fails to come up, Send still runs — it spools — and the hook still answers with (at
// worst) an empty response and exits 0, per §2.3.
func runSessionStart(ctx context.Context, env Env, args []string, out, errw io.Writer) error {
	return doHook(hookSpec{
		op: ipc.OpSessionStart, reply: true, deadline: sessionStartReplyDeadline,
		preSend: ensureDaemonRunning,
	})(ctx, env, args, out, errw)
}

// ensureDaemonRunning calls daemon.EnsureRunning for session-start's preSend seam.
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
func ensureDaemonRunning(root, self string, st ipc.State, clk core.Clock) {
	if _, on := faultActive(faultDaemonDown); on {
		return
	}
	if self == "" {
		return
	}
	if !st.DaemonEnabled {
		return
	}
	_, _ = daemon.EnsureRunning(root, self, newHookLogger(root), clk)
}
