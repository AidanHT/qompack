package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/modwire"
	"github.com/qompack/qompack/internal/paths"
)

// modRefusedHostVersion is the brief's version refusal; like the others it names the condition,
// never the version the request carried.
const modRefusedHostVersion = "the host version is below the minimum or unreadable"

// modBrief is `qompack mod brief`: the instructions the mod adds to a compaction, asked of the
// daemon's brief route.
//
// A brief is refused, with nothing written and nothing spawned, when its host_version is below
// modwire.MinHostVersion or unreadable, and for every refusal modServedProject makes for a verb that
// steers: a home-directory root, no store, runtime.mod.enabled or runtime.mod.steering off, runtime
// mode off. A request that cannot be read is the bare error object, and a configuration that does
// not load is an error; neither is recorded, because the first cannot name its record and the second
// leaves the switches unknown.
//
// Otherwise the verb asks the daemon (askBriefDaemon) and records the call, whatever it came to, as
// one line of the session's brief-outcome log before it answers. A record that cannot be written
// does not change the answer: failing open, the brief is still served, the failure is reported on
// errw with no path in it, and the call is left with no record, which the evaluation reads as such.
func modBrief(ctx context.Context, env Env, stdin io.Reader, errw io.Writer) any {
	clk := env.Clock
	if clk == nil {
		clk = core.SystemClock()
	}
	started := env.Started
	if started.IsZero() {
		started = clk.Now()
	}

	req, err := modwire.DecodeBriefRequest(stdin)
	if err != nil {
		return modwire.FailureFor(err)
	}
	unserved := func(st modwire.Status) modwire.BriefResponse { return modwire.BriefResponse{Status: st} }
	// The version first: it reads nothing at all.
	if !modwire.MeetsMinHostVersion(req.HostVersion) {
		return unserved(modwire.Failure(modwire.ReasonRefused, modRefusedHostVersion))
	}
	root, cfg, refusal, err := modServedProject(env, req.CWD, true)
	switch {
	case err != nil:
		return unserved(modFailure(err, root, homeDir(env)))
	case refusal != "":
		return unserved(modwire.Failure(modwire.ReasonRefused, refusal))
	}

	resp := askBriefDaemon(ctx, env, clk, root, cfg, req, started)
	chars := 0
	if resp.OK {
		chars = modwire.CharCount(resp.Instructions)
	}
	if err := appendBriefRecord(root, req.SessionID, modwire.BriefRecord{
		V: modwire.Version, TS: core.NowMilli(clk), Trigger: req.Trigger, Outcome: modwire.OutcomeFor(resp.Status),
		Chars: chars, MS: max(clk.Since(started).Milliseconds(), 0),
	}); err != nil {
		_, _ = fmt.Fprintf(errw, "qompack mod brief: record not written: %s\n",
			modFailure(err, root, homeDir(env)).Detail)
	}
	return resp
}

// askBriefDaemon asks root's daemon for the brief and returns the answer the mod gets.
//
// Timing (modwire's three layers): the reply wait is modwire.TimeoutMs less the time since the
// process started less modwire.RecordMarginMs, so the record and the answer are written before the
// mod gives up on this process; the wait counts the dial too (sendModRequest). The daemon is sent a
// budget of the reply wait less modwire.DaemonReplyMarginMs, which it counts from receipt. A budget
// that is not positive is a timeout, and the daemon is not asked.
//
// The daemon's answer is passed on unchanged when it is a valid brief within the cap (the configured
// runtime.mod.briefMaxChars and the request's own max_chars, whichever is smaller), its own timeout
// included. An exchange that went unanswered is cold, busy, timeout or error, as sendModRequest
// classifies it; a daemon that refuses the call, and an answer that breaks the contract, are errors.
func askBriefDaemon(ctx context.Context, env Env, clk core.Clock, root string, cfg config.Config,
	req modwire.BriefRequest, started time.Time,
) modwire.BriefResponse {
	unserved := func(st modwire.Status) modwire.BriefResponse { return modwire.BriefResponse{Status: st} }

	replyWait := time.Duration(modwire.TimeoutMs-modwire.RecordMarginMs)*time.Millisecond - clk.Since(started)
	budget := replyWait - modwire.DaemonReplyMarginMs*time.Millisecond
	// The CLI stops waiting at this instant, whatever the steps below and the dial cost.
	replyBy := time.Now().Add(replyWait)
	if budget.Milliseconds() <= 0 {
		return unserved(modwire.Failure(modwire.ReasonTimeout, "no time was left to ask the daemon"))
	}

	body, err := modwire.Encode(req)
	if err != nil {
		return unserved(modwire.FailureFor(err))
	}
	ipcReq, err := ipc.ModRequest(core.SessionID(req.SessionID), core.UnixMilli(started.UnixMilli()), ipc.ModCall{
		Verb: string(modwire.VerbBrief), BudgetMs: budget.Milliseconds(), Req: bytes.TrimSuffix(body, []byte("\n")),
	})
	if err != nil {
		return unserved(modwire.FailureFor(err))
	}

	answer, st := sendModRequest(ctx, env, root, daemonClientState(root, cfg), ipcReq, replyBy)
	if !st.OK {
		return unserved(st)
	}
	if !answer.OK {
		return unserved(modFailure(fmt.Errorf("the daemon refused the brief: %s", answer.Err), root, homeDir(env)))
	}
	var resp modwire.BriefResponse
	if err := json.Unmarshal(answer.Data, &resp); err != nil {
		return unserved(modwire.FailureFor(fmt.Errorf("the daemon's brief: %w", err)))
	}
	if err := resp.Validate(briefCap(cfg, req)); err != nil {
		return unserved(modwire.FailureFor(fmt.Errorf("the daemon's brief: %w", err)))
	}
	return resp
}

// briefCap is the character cap on a brief's instructions: runtime.mod.briefMaxChars (the loader
// keeps it within [1, modwire.BriefMaxChars]; it is clamped again here) and the request's own cap,
// whichever is smaller.
func briefCap(cfg config.Config, req modwire.BriefRequest) int {
	configured := cfg.Runtime.Mod.BriefMaxChars
	if configured <= 0 || configured > modwire.BriefMaxChars {
		configured = modwire.BriefMaxChars
	}
	return min(configured, req.EffectiveMaxChars())
}

// appendBriefRecord appends rec as one line of <root>/.qompack/state/mod-brief-<session>.jsonl. The
// store exists (modServedProject), so only its state directory may need creating.
func appendBriefRecord(root, session string, rec modwire.BriefRecord) error {
	name, err := modwire.BriefRecordName(session)
	if err != nil {
		return err
	}
	if _, err := rec.Line(); err != nil {
		return err
	}
	dir := paths.Of(root).State
	if err := os.MkdirAll(paths.Long(dir), 0o700); err != nil {
		return err
	}
	return paths.AppendJSONL(filepath.Join(dir, name), rec)
}
