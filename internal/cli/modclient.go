package cli

import (
	"context"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/modwire"
)

// The daemon half of the `qompack mod` bridge: how a verb that needs the daemon reaches it, and how
// an exchange that went unanswered is told apart. Hello is served from this process and does not
// use it; a verb that asks the daemon for something (brief) sends its request through
// sendModRequest and reads the answer it returns.

// modConnectDeadline is the dial budget of a mod request: the state's ConnectDeadlineMs, widened to
// hookConnectDeadlineFloor as every hook off the hot path is. The configured default is sized for a
// warm hot-path connection (5 ms, 25 ms on Windows) and would report a daemon that is merely between
// accepts as busy.
func modConnectDeadline(st ipc.State) time.Duration {
	d := time.Duration(st.ConnectDeadlineMs) * time.Millisecond
	if d < hookConnectDeadlineFloor {
		d = hookConnectDeadlineFloor
	}
	return d
}

// modUnservedReason maps the step an exchange failed at to the unserved reason the mod is told:
// nothing listening is cold, a daemon that did not accept in time is busy, one that accepted and
// missed its deadline is a timeout, and everything else (a disabled daemon included) is an error.
func modUnservedReason(f ipc.SendFailure) modwire.Reason {
	switch f {
	case ipc.SendNotListening:
		return modwire.ReasonCold
	case ipc.SendNotAccepted:
		return modwire.ReasonBusy
	case ipc.SendUnanswered:
		return modwire.ReasonTimeout
	}
	return modwire.ReasonError
}

// modUnservedDetail is the one-line detail an unserved exchange carries, naming the step.
func modUnservedDetail(f ipc.SendFailure) string {
	switch f {
	case ipc.SendNotListening:
		return "no daemon is listening; starting one"
	case ipc.SendNotAccepted:
		return "the daemon did not accept the connection in time"
	case ipc.SendUnanswered:
		return "the daemon did not answer in time"
	case ipc.SendDisabled:
		return "the daemon is disabled (runtime.daemon.enabled)"
	}
	return "the exchange with the daemon failed (" + string(f) + ")"
}

// sendModRequest sends req to root's daemon and waits for its answer until replyBy, the instant the
// caller stops waiting, the dial included: the dial takes at most modConnectDeadline(st) and never
// past replyBy, and whatever it takes comes out of the wait (ipc.ClientOptions.ReplyBy). With replyBy
// already past nothing is dialled and the status is timeout. st is the client state the verb works
// under (for a verb with a loaded configuration, daemonClientState(root, cfg)).
//
// When the daemon answered, whatever it answered, the status is modwire.Served() and resp is its
// answer, for the verb to read. Otherwise resp is the zero Response and the status is unserved:
//
//   - cold: nothing is listening. A detached daemon is started through the spawn claim every spawner
//     shares (ipc.ClaimSpawn), and the call returns at once without waiting for it. An Env with no
//     executable path (Env.Self) starts nothing, as for the hooks.
//   - busy: something holds the address and did not accept within the dial budget.
//   - timeout: the daemon accepted and missed the reply deadline, or no time was left to dial.
//   - error: anything else, including a disabled daemon.
//
// A NAK on an ACK-path request (Reply false) also reads as answered, with resp.OK false; every mod
// verb sends a Reply request, so it never meets one.
//
// In runtime mode off the client dials nothing and Send answers OK with Mode off, which arrives here
// as an answer with no Data. Brief has refused a project in mode off before it gets here, so it
// meets one only if the mode changed in between, and reads it as an answer that breaks the contract.
//
// A mod request is never spooled: its client gets nopSpool, because an answer replayed later has no
// caller to reach. The client's own lazy spawn is off, so only a cold exchange spawns.
func sendModRequest(ctx context.Context, env Env, root string, st ipc.State, req ipc.Request,
	replyBy time.Time,
) (ipc.Response, modwire.Status) {
	remaining := time.Until(replyBy)
	if remaining <= 0 {
		return ipc.Response{}, modwire.Failure(modwire.ReasonTimeout, "no time was left to reach the daemon")
	}
	addr, err := ipc.Resolve(root)
	if err != nil {
		return ipc.Response{}, modwire.FailureFor(err)
	}
	clk := env.Clock
	if clk == nil {
		clk = core.SystemClock()
	}

	var failure ipc.SendFailure
	c := ipc.NewClientWithOptions(addr, nopSpool{}, logging.Nop(), nil, ipc.ClientOptions{
		ProjectRoot: root, State: st, Clock: clk,
		ConnectDeadline: min(modConnectDeadline(st), remaining),
		ReplyBy:         replyBy,
		OnFailure:       func(f ipc.SendFailure) { failure = f },
	})
	defer func() { _ = c.Close() }()

	resp, _ := c.Send(ctx, req, remaining)
	if failure == "" {
		return resp, modwire.Served()
	}
	if failure == ipc.SendNotListening {
		startModDaemon(root, env.Self, clk)
	}
	return ipc.Response{}, modwire.Failure(modUnservedReason(failure), modUnservedDetail(failure))
}

// startModDaemon starts root's daemon detached, without waiting for it: only a spawner that claims
// the project's spawn lock spawns (a fresh claim means a spawn is already underway), and a spawn
// that fails to start releases its claim. It is ipc.Client's lazy spawn, taken for a cold exchange
// only.
func startModDaemon(root, self string, clk core.Clock) {
	if self == "" || spawnDaemon == nil {
		return
	}
	lock, claim := ipc.ClaimSpawn(root, clk)
	if claim != ipc.SpawnClaimed {
		return
	}
	if err := spawnDaemon(root, self); err != nil {
		lock.Release()
	}
}
