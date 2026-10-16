package daemon

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/modwire"
)

// The mod route: the daemon's end of `qompack mod <verb>` (docs/adr/0015). The CLI sends a verb
// that needs the daemon as an ipc.OpMod request carrying an ipc.ModCall. Brief is the only such
// verb: hello is answered by the CLI process itself, with no daemon.
//
// The answer to a call the route could read is OK, with the verb's modwire response in Data,
// whatever that response says (a timeout included). A call it could not read, a verb it does not
// serve, and a spool replay are refused (OK false, Err naming why); the CLI reports those as
// modwire.ReasonError.

// handleMod serves one OpMod request.
func (d *daemon) handleMod(ctx context.Context, req ipc.Request) ipc.Response {
	// The budget is counted from receipt, before anything else is spent (modDeadline).
	receipt := time.Now()
	// The CLI never spools a mod request. A copy found in a spool anyway is not answered: the
	// caller is gone, and a brief closes the session's open segment.
	if spoolReplay(ctx) {
		return ipc.Response{OK: false, Err: "a mod request is never replayed"}
	}
	call, err := ipc.DecodeModCall(req.Raw)
	if err != nil {
		return ipc.Response{OK: false, Err: err.Error()}
	}
	if call.Verb != string(modwire.VerbBrief) {
		return ipc.Response{OK: false, Err: fmt.Sprintf("the daemon serves no mod verb %.32q", call.Verb)}
	}
	breq, err := modwire.DecodeBriefRequest(bytes.NewReader(call.Req))
	if err != nil {
		return ipc.Response{OK: false, Err: "brief request: " + err.Error()}
	}

	resp, out := d.serveBrief(ctx, briefCall{
		Req:      breq,
		At:       req.TS,
		Deadline: modDeadline(receipt, req.TS, call.BudgetMs),
		MaxChars: d.currentCfg().Runtime.Mod.BriefMaxChars,
	})
	data, err := modwire.Encode(resp)
	if err != nil {
		return ipc.Response{OK: false, Err: "brief response: " + err.Error()}
	}
	d.log.Debug("daemon: brief", "session", breq.SessionID, "trigger", breq.Trigger,
		"outcome", string(out.Outcome), "empty", out.Empty, "closed", out.Closed, "items", out.Items,
		"invalid_ids", out.InvalidIDs, "over_cap", out.OverCap, "unreplayed", out.Unreplayed)
	return ipc.Response{OK: true, Data: bytes.TrimSuffix(data, []byte("\n"))}
}

// modDeadline is the instant the route must answer by: receipt plus the CLI's budget (modBudget), and
// never later than the CLI's own start (the request's TS) plus TimeoutMs - RecordMarginMs -
// DaemonReplyMarginMs. The CLI stops waiting at its start plus TimeoutMs - RecordMarginMs, and it
// fixed the budget before it dialled, so the second bound takes the dial and the transit out of the
// daemon's time instead of out of the margin its answer needs. It can only shorten the deadline. Both
// processes read the same host clock, and TS is truncated to the millisecond, which errs early.
func modDeadline(receipt time.Time, ts core.UnixMilli, budgetMs int64) time.Time {
	deadline := receipt.Add(modBudget(budgetMs))
	if ts > 0 {
		layer := time.Duration(modwire.TimeoutMs-modwire.RecordMarginMs-modwire.DaemonReplyMarginMs) * time.Millisecond
		if by := time.UnixMilli(int64(ts)).Add(layer); by.Before(deadline) {
			deadline = by
		}
	}
	return deadline
}

// modBudget is how long the route has for a call whose CLI sent budgetMs: that many milliseconds,
// never more than the mod waits for the CLI (modwire.TimeoutMs). A budget that is not positive is
// already over.
func modBudget(budgetMs int64) time.Duration {
	return time.Duration(min(budgetMs, modwire.TimeoutMs)) * time.Millisecond
}
