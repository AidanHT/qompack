package daemon

import (
	"cmp"
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/modwire"
)

// The brief route's body: the daemon side of `qompack mod brief` (docs/adr/0015; V7 plan §5.3). The
// mod asks for a brief just before the host compacts, and adds the instructions it gets to the
// compaction's, so the summarizer is told to reproduce the session's must-keep items verbatim. The
// route owns the whole sequence, in this order:
//
//  0. Settle the session, bounded (briefSettleBound): the PreCompact seal's settle (settleWithin)
//     with a smaller bound and counters of its own, so a capture the daemon acknowledged but has not
//     yet published, or that sits in a hook's client spool, is in the store the brief reads.
//  1. Close the session's open segment when the trigger is one that closes it
//     (modwire.ClosesSegment: manual, auto, plugin), through the scheduler runtime's
//     CloseSegmentForBrief, which shares the compaction close's body with cause "brief" and its own
//     counters. Precompute and any other trigger leave the segment open: the conversation goes on.
//     With no scheduler runtime, or one bound to another session, nothing is closed, and the brief
//     encodes the segments already closed.
//  2-3. Arm the checkpoint writer's sources and snapshot the draft (Services.BriefSnapshot, which
//     BindCheckpoint binds to armSources plus checkpoint.FileWriter.BriefSnapshot).
//  4. Render the served instructions (renderBrief): modwire.BriefMarker, a newline, then one line per
//     item, within the character cap; an item whose id modwire's alphabet refuses is skipped and
//     counted, never failing the brief.
//
// Degraded modes. When the contract mode may not act (degraded-passive or off), the route serves an
// empty brief and closes nothing, as PreCompact seals nothing then. An empty brief is also the answer
// when the snapshot fails (Begin cannot open a draft, for instance because no sources are armed) and
// when no checkpointer is bound: the mod then proceeds with the user's instructions only. A deadline
// already past on arrival, or passed before the snapshot answered, is a timeout.
//
// The IPC op and the CLI verb that reach this route, the brief-outcome record and the config key
// that sets the cap are the caller's; serveBrief is what that caller calls.

// briefSettleMax is the brief's settle bound: PreCompact settles for up to 500 ms (precompactSettleBound)
// because it seals, while a brief must leave the rest of its budget to the snapshot. Provisional,
// with the mod's timeout (modwire.TimeoutMs), until measured on a warm daemon with a backlog.
const briefSettleMax = 300 * time.Millisecond //nomagic:allow the brief's settle bound (V7 plan §5.3 step 0), not a config default's duplicate

// The brief route's instruments.
const (
	// counterBriefSettle, counterBriefUnreplayed and counterBriefSettleSpoolReads are the PreCompact
	// settle's three counters for the brief's settle.
	counterBriefSettle           = "brief_settle"
	counterBriefUnreplayed       = "brief_unreplayed_captures"
	counterBriefSettleSpoolReads = "brief_settle_spool_reads"

	// counterBriefServed counts briefs served with instructions.
	counterBriefServed = "mod.brief.served"
	// counterBriefServedEmptyPrefix counts briefs served empty, by reason (briefEmpty*).
	counterBriefServedEmptyPrefix = "mod.brief.served_empty."
	// counterBriefTimeout counts briefs whose deadline passed before they could be served.
	counterBriefTimeout = "mod.brief.timeout"
	// counterBriefInvalidID counts stored items left out of a brief because modwire's fact-id
	// alphabet or length refuses their id (R22).
	counterBriefInvalidID = "mod.brief.invalid_id"
	// counterBriefOverCap counts items left out of a brief because they did not fit its character
	// cap or its fact-id count.
	counterBriefOverCap = "mod.brief.over_cap"
)

// msgBriefSettleLeft is the brief settle's leftMsg (settleSpec).
const msgBriefSettleLeft = "daemon: brief served before some of the session's captures were replayed; " +
	"the daemon replays them next"

// The reasons a brief is served empty, as briefOutcome.Empty and the served-empty counter name them.
const (
	// briefEmptyMode: the contract mode may not act.
	briefEmptyMode = "mode"
	// briefEmptyUnwired: no checkpointer is bound (Services.BriefSnapshot is nil).
	briefEmptyUnwired = "unwired"
	// briefEmptySnapshot: the snapshot failed, Begin included.
	briefEmptySnapshot = "snapshot"
	// briefEmptyNothing: the snapshot had no item that could be served within the cap.
	briefEmptyNothing = "nothing"
)

// briefCall is one brief for serveBrief.
type briefCall struct {
	// Req is the decoded request the mod sent.
	Req modwire.BriefRequest
	// At is when the brief was asked for, the hook time the settle measures spooled captures
	// against (a capture fired after it is not this compaction's). Zero means the daemon's now.
	At core.UnixMilli
	// Deadline is the absolute instant the daemon must answer by. The mod route sets it with
	// modDeadline: receipt plus the budget the CLI sends (never more than modwire.TimeoutMs), capped
	// at the request's TS (the CLI process's start) plus TimeoutMs - RecordMarginMs -
	// DaemonReplyMarginMs. Zero means none.
	Deadline time.Time
	// MaxChars is the cap on the instructions. The mod route sets it with modMaxChars: the smaller of
	// the daemon's configured runtime.mod.briefMaxChars and the cap the CLI sent in its envelope.
	// Zero means modwire.BriefMaxChars, and a larger value is clamped to that wire ceiling. The
	// request's own max_chars also bounds them (briefCharLimit).
	MaxChars int
}

// briefOutcome is what one serveBrief came to, beside the response, for the caller's outcome record
// and logs. None of it is content.
type briefOutcome struct {
	// Outcome is served, timeout, or error.
	Outcome modwire.Outcome
	// Empty is the reason a served brief carries no instructions (briefEmpty*), "" otherwise.
	Empty string
	// Closed is true when the brief closed the session's open segment.
	Closed bool
	// Items is how many items the instructions list (len of the response's fact ids).
	Items int
	// InvalidIDs and OverCap are the items left out for their id and for the cap.
	InvalidIDs, OverCap int
	// Unreplayed is how many of the session's captures the settle left unreplayed.
	Unreplayed int
}

// serveBrief serves one brief: settle, close, arm, snapshot, render (see above). It never fails the
// brief for a step that cannot run: what it cannot settle, close or read is left out, and the answer
// is then served, possibly empty. Only a passed deadline is unserved, as modwire.ReasonTimeout.
func (d *daemon) serveBrief(ctx context.Context, c briefCall) (modwire.BriefResponse, briefOutcome) {
	sess := core.SessionID(c.Req.SessionID)
	limit := briefCharLimit(c.Req, c.MaxChars)
	if !c.Deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, c.Deadline)
		defer cancel()
	}
	var out briefOutcome
	if ctx.Err() != nil {
		return d.briefTimeout(out, "the daemon's deadline passed before the brief began")
	}
	if !d.monitor.Mode().MayAct() {
		return d.briefServedEmpty(out, briefEmptyMode)
	}

	at := c.At
	if at <= 0 {
		at = core.NowMilli(d.clk)
	}
	if rep := d.settleBeforeBrief(ctx, sess, at, c.Deadline); rep != nil {
		out.Unreplayed = len(rep.left)
	}
	if ctx.Err() != nil {
		return d.briefTimeout(out, "the daemon's deadline passed while the brief settled the session")
	}

	if modwire.ClosesSegment(c.Req.Trigger) {
		if rt, ok := d.svc.Sched.(*schedRuntime); ok && rt != nil {
			closed, err := rt.CloseSegmentForBrief(ctx, sess)
			if err != nil {
				d.log.Warn("daemon: brief could not close the open segment; the brief encodes the closed ones",
					"session", string(sess), "err", err.Error())
			}
			out.Closed = closed
		}
	}

	snapshot := d.svc.BriefSnapshot
	if snapshot == nil {
		return d.briefServedEmpty(out, briefEmptyUnwired)
	}
	b, err := snapshot(ctx, sess)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return d.briefTimeout(out, "the daemon's deadline passed before the snapshot answered")
		}
		d.log.Warn("daemon: brief snapshot failed; the brief is served empty",
			"session", string(sess), "err", err.Error())
		return d.briefServedEmpty(out, briefEmptySnapshot)
	}
	if ctx.Err() != nil {
		// The snapshot stops starting steps once the deadline passes and answers what the draft had,
		// which is not a full brief: it is not served as one.
		return d.briefTimeout(out, "the daemon's deadline passed while the snapshot ran")
	}

	r := renderBrief(b.Items, limit)
	out.Items, out.InvalidIDs, out.OverCap = len(r.ids), r.invalid, r.over
	d.m.Counter(counterBriefInvalidID).Add(int64(r.invalid))
	d.m.Counter(counterBriefOverCap).Add(int64(r.over))
	if r.instructions == "" {
		return d.briefServedEmpty(out, briefEmptyNothing)
	}
	d.m.Counter(counterBriefServed).Add(1)
	out.Outcome = modwire.OutcomeServed
	return modwire.BriefResponse{Status: modwire.Served(), Instructions: r.instructions, FactIDs: r.ids}, out
}

// briefServedEmpty is a served brief with no instructions, for reason.
func (d *daemon) briefServedEmpty(out briefOutcome, reason string) (modwire.BriefResponse, briefOutcome) {
	d.m.Counter(counterBriefServedEmptyPrefix + reason).Add(1)
	out.Outcome, out.Empty = modwire.OutcomeServed, reason
	return modwire.BriefResponse{Status: modwire.Served()}, out
}

// briefTimeout is an unserved brief whose deadline passed; detail names the step, never content.
func (d *daemon) briefTimeout(out briefOutcome, detail string) (modwire.BriefResponse, briefOutcome) {
	d.m.Counter(counterBriefTimeout).Add(1)
	out.Outcome = modwire.OutcomeTimeout
	return modwire.BriefResponse{Status: modwire.Failure(modwire.ReasonTimeout, detail)}, out
}

// settleBeforeBrief settles sess before the brief's snapshot, as the PreCompact route settles before
// its seal (settleWithin), under the brief's own bound and counters.
func (d *daemon) settleBeforeBrief(ctx context.Context, sess core.SessionID, at core.UnixMilli,
	deadline time.Time,
) *sealReport {
	return d.settleWithin(ctx, sess, at, d.currentCfg(), settleSpec{
		bound:   d.briefSettleBound(time.Now(), deadline),
		started: counterBriefSettle, unreplayed: counterBriefUnreplayed, spoolReads: counterBriefSettleSpoolReads,
		leftMsg: msgBriefSettleLeft,
	})
}

// briefSettleBound is the brief's settle bound at now: briefSettleMax (or the daemon's test override,
// briefSettleCap), and never more than half of what is left before deadline, so the settle stays
// inside the daemon's deadline and the snapshot keeps at least the other half. A zero deadline leaves
// the cap alone; a passed one leaves no settle.
func (d *daemon) briefSettleBound(now, deadline time.Time) time.Duration {
	bound := briefSettleMax
	if d.briefSettleCap > 0 {
		bound = d.briefSettleCap
	}
	if deadline.IsZero() {
		return bound
	}
	return max(min(bound, deadline.Sub(now)/2), 0)
}

// briefCharLimit is the character cap on one brief's instructions: the configured cap (zero means
// modwire.BriefMaxChars, a larger value is clamped to it) and the request's own (EffectiveMaxChars),
// whichever is smaller.
func briefCharLimit(req modwire.BriefRequest, configured int) int {
	if configured <= 0 || configured > modwire.BriefMaxChars {
		configured = modwire.BriefMaxChars
	}
	return min(configured, req.EffectiveMaxChars())
}

// briefFactID is modwire's fact-id alphabet, built from the published constant as the wire's own
// validator is (codec.go), so the route skips exactly the ids BriefResponse.Validate would refuse.
var briefFactID = regexp.MustCompile("^[" + modwire.FactIDChars + "]+$")

// validBriefID reports whether id can travel as a fact id: 1 to modwire.MaxFactIDBytes bytes of
// modwire.FactIDChars.
func validBriefID(id string) bool {
	return len(id) <= modwire.MaxFactIDBytes && briefFactID.MatchString(id)
}

// briefRender is one rendered brief: the instructions (empty when nothing is served), the ids of the
// items they list, in order, and how many items were left out for their id and for the cap.
type briefRender struct {
	instructions string
	ids          []string
	invalid      int
	over         int
}

// The order in which renderBrief chooses items when they do not all fit: the pins, the original
// intent, the current work, the restatements of the intent newest first, the eliminations, the
// decisions. A brief is cut by importance, whole items at a time; nothing is truncated, because the
// summarizer is asked to reproduce each item verbatim.
var briefPriority = map[checkpoint.BriefKind]int{
	checkpoint.BriefInvariant:       0,
	checkpoint.BriefIntentOriginal:  1,
	checkpoint.BriefCurrentWork:     2,
	checkpoint.BriefIntentEvolution: 3,
	checkpoint.BriefElimination:     4,
	checkpoint.BriefDecision:        5,
}

// renderBrief renders items as served instructions within limit characters (modwire.CharCount):
// modwire.BriefMarker, then for each chosen item a newline and "- [<id>] <text>", in the items'
// own order. An item whose id is not a valid fact id is skipped and counted; the rest are chosen in
// briefPriority order while they fit, whole, and while the fact-id count allows. With nothing chosen
// the instructions are empty: there is no marker without an item.
func renderBrief(items []checkpoint.BriefItem, limit int) briefRender {
	type candidate struct {
		item       int
		line       string
		cost       int
		rank, tieN int
	}
	var r briefRender
	var cands []candidate
	for i, it := range items {
		if !validBriefID(it.ID) {
			r.invalid++
			continue
		}
		if strings.TrimSpace(it.Text) == "" {
			continue
		}
		rank, ok := briefPriority[it.Kind]
		if !ok {
			rank = len(briefPriority)
		}
		tie := i
		if it.Kind == checkpoint.BriefIntentEvolution {
			tie = -i // the newest restatement is the current authority
		}
		line := "- [" + it.ID + "] " + it.Text
		cands = append(cands, candidate{item: i, line: line, cost: 1 + modwire.CharCount(line), rank: rank, tieN: tie})
	}
	order := make([]int, len(cands))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		if c := cmp.Compare(cands[a].rank, cands[b].rank); c != 0 {
			return c
		}
		return cmp.Compare(cands[a].tieN, cands[b].tieN)
	})
	used, n := modwire.CharCount(modwire.BriefMarker), 0
	chosen := make([]bool, len(cands))
	for _, k := range order {
		if n >= modwire.MaxFactIDs || used+cands[k].cost > limit {
			r.over++
			continue
		}
		chosen[k] = true
		used += cands[k].cost
		n++
	}
	if n == 0 {
		return r
	}
	var b strings.Builder
	b.WriteString(modwire.BriefMarker)
	for k, c := range cands {
		if !chosen[k] {
			continue
		}
		b.WriteByte('\n')
		b.WriteString(c.line)
		r.ids = append(r.ids, items[c.item].ID)
	}
	r.instructions = b.String()
	return r
}
