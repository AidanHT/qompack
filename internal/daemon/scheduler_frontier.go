package daemon

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
)

// O5 continuous frontier advancement (00-ARCHITECTURE.md §8.5, G1.5, G7.1; plan
// V4-SP-12-scheduler-l3 "scheduler_frontier.go"). Three responsibilities: close a segment when a
// boundary is observed, roll its successor open, and — during idle time — encode
// closed-and-unencoded segments into the local checkpoint draft. This does not shorten the
// host's native summary request or establish a committed publication frontier.
//
// Two entry points reach closeSegmentLocked and together cover every boundary event the design
// names: Observe (scheduler_runtime.go) with cause "changepoint" when the detector declares, and
// CloseSegmentOn, called by the tap, for todo completion, a passing test run and a git commit.

// The frontier's instruments and log lines.
const (
	counterSegmentClosedPrefix = "sched.segment.closed."
	counterFrontierNoWriter    = "sched.frontier.no_writer"
	counterFrontierDPIGuard    = "sched.frontier.dpi_guard"
	counterFrontierUnverified  = "sched.frontier.unverified_evidence"
	gaugeResidualOverBudget    = "sched.residual_over_budget"

	msgDPIGuard           = "frontier advance hit the DPI guard"
	msgDPIViolation       = "checkpoint: DPI violation: segments are already encoded by another checkpoint and were skipped"
	msgResidualOverBudget = "scheduler: residual span over budget; O5 is not keeping up"
	msgUnverifiedEvidence = "scheduler: frontier stopped short of unverified evidence"
)

// Verification has TWO failure modes and they are deliberately not the same kind of event.
//
// The QUIET one is missing evidence: a gap, an unreadable segment, work still in flight. Nothing
// is wrong with the system — the durable record simply does not reach far enough yet — so the
// frontier stops at the longest verified prefix, counts counterFrontierUnverified, warns once, and
// the next pass picks the span up when its evidence lands. That is an evidenceStop.
//
// The LOUD one is a DPI violation: the segment log OFFERED a segment as unencoded and, on the
// re-read, reports it as already encoded. Unencoded filters on EncodedOnce, so one writer over one
// log can never produce that pair; it means two writers disagree about who owns the segment, which
// is the §4.6 invariant this layer exists to enforce mechanically. It is not a stop — the evidence
// is durable and contiguity legitimately continues across it — but it must NEVER be inferred from
// silence, so it is carried out of verification by name, reported Loud with its counter, and its
// ids dropped. That is the dpi return, and it is why verifyEvidence does not simply filter these
// segments out of the batch.

// The reasons verification refuses to carry the frontier any further. They are values, not free
// text, because they are what the log line and the report are keyed off.
const (
	evidenceUnreadable = "unreadable" // the segment log cannot re-read the segment
	evidenceNotClosed  = "not_closed" // it came back open: in-flight work, still being produced
	evidenceForeign    = "foreign"    // it came back belonging to another session
	evidenceMalformed  = "malformed"  // its turn range is not a range
	evidenceGap        = "gap"        // a turn range between it and its predecessor has no evidence
	evidenceCancelled  = "cancelled"  // the verification budget ran out mid-batch
)

// CloseSegmentOn closes the session's current segment at turn at with cause ∈ {todo, test,
// commit} — the tap's task-boundary signals — and rolls its successor open. It takes the lock and
// delegates to closeSegmentLocked.
func (r *schedRuntime) CloseSegmentOn(ctx context.Context, at core.TurnIndex, f scheduler.Features, cause string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closeSegmentLocked(ctx, at, f, cause)
}

// CloseSegmentForCompaction closes sess's current segment at the highest turn observed, with cause
// "compact", and rolls its successor open: the host is compacting sess, and the checkpoint it seals
// next encodes closed segments only (F-UAT03-1).
//
// The turn and the open-segment token count the close records are this runtime's, and they belong
// to the session it is bound to. A compaction of ANOTHER session is therefore left alone and
// counted. A runtime bound to no session yet — a daemon that restarted mid-session, which gets no
// SessionStart, and whose first hook for the session has not reached the tap (bindOnFirstHook) —
// holds only what it observed since it started, so it first binds sess exactly as a SessionStart
// would, restoring the session's persisted account from state/scheduler.json, and then folds in
// what it observed since the restart (bindUnboundLocked). A segment the
// runtime still has no token account for is left open and counted rather than closed with zero
// tokens, which Segment.Tokens would keep for good. The turn is the highest tool-use turn
// observed, so a final prompt answered without a tool lands in the successor and is encoded by a
// later checkpoint rather than this one.
func (r *schedRuntime) CloseSegmentForCompaction(ctx context.Context, sess core.SessionID) error {
	if sess == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.session {
	case sess:
	case "":
		r.bindUnboundLocked(sess)
	default:
		r.count(counterTapCompactForeign)
		return nil
	}
	if r.openSegTokens == 0 {
		r.count(counterTapCompactUnobserved)
		return nil
	}
	return r.closeSessionSegmentLocked(ctx, sess, r.maxTurn, scheduler.Features{}, causeCompact)
}

// bindUnboundLocked binds sess on a runtime bound to nothing, keeping the two accumulators a
// segment close records. The bind resets them and restores the session's persisted values; what
// this daemon observed before the bind came after that persist (an unbound runtime never
// persists), so the turn is the later of the two and the open segment's tokens are their sum. Its
// callers are a compaction close (CloseSegmentForCompaction) and a session's first hook
// (bindOnFirstHook).
func (r *schedRuntime) bindUnboundLocked(sess core.SessionID) {
	seenTurn, seenTokens := r.maxTurn, r.openSegTokens
	r.bindSessionLocked(sess, nil)
	if seenTurn <= r.maxTurn && seenTokens == 0 {
		return
	}
	r.maxTurn = max(r.maxTurn, seenTurn)
	r.openSegTokens += seenTokens
	// The live context and the burn baseline follow, as BindSession sets them: the merge is a
	// recount, never a burn sample.
	r.lastTokens = r.recomputeContextTokensLocked(context.Background())
	r.dirty = true
}

// closeSegmentLocked closes the bound session's current segment and opens its successor.
// Cause is one of: changepoint, todo, test, commit.
// SP-08 owns the session's FIRST Open; every subsequent roll is owned here.
func (r *schedRuntime) closeSegmentLocked(ctx context.Context, at core.TurnIndex, f scheduler.Features, cause string) error {
	return r.closeSessionSegmentLocked(ctx, r.session, at, f, cause)
}

// closeSessionSegmentLocked is closeSegmentLocked for a named session: every close cause but
// "compact" names the bound one.
func (r *schedRuntime) closeSessionSegmentLocked(ctx context.Context, sess core.SessionID, at core.TurnIndex,
	f scheduler.Features, cause string,
) error {
	if !r.conf().Checkpoint.Frontier.AdvanceOnSegmentClose {
		return nil
	}
	cur, err := r.segs.Current(ctx, sess)
	if errors.Is(err, core.ErrNotFound) {
		return nil // SP-08 has not opened one yet; nothing to close
	} else if err != nil {
		return err
	}
	if cur.Closed || at < cur.StartTurn {
		return nil
	}
	feats := map[string]float64{
		// "tokens" is NOT a BOCD feature: it is the pseudo-feature store.SegmentLog.Close reads
		// Segment.Tokens from (internal/store/segments.go, splitSegFeatures). Segment.Tokens has
		// no setter and Close runs exactly once per segment, so omitting this key leaves that
		// segment's Tokens at ZERO PERMANENTLY — the store logs a Warn once per segment when it
		// is missing, and SP-12 is the caller the store's own comment names. Everything
		// downstream is keyed off it: contextTokens, every trigger clause, residual accounting,
		// and therefore ShouldCompact itself.
		"tokens": float64(r.openSegTokens),

		"path_jaccard": f.PathJaccard, "tool_shift": f.ToolShift,
		"lexical_cohesion": f.LexicalCohesion, "gap_seconds": f.GapSeconds,
		"todo_transition":  f.TodoTransition,
		"prob_changepoint": r.det.State().ProbChangepoint,
	}
	if err := r.segs.Close(ctx, cur.ID, at, feats); err != nil {
		return err
	}
	_, err = r.segs.Open(ctx, store.Segment{
		Session: sess, StartTurn: at + 1, StartTS: r.nowMS(),
	})
	r.openSegTokens = 0 // the successor starts empty; the tap refills it from rec.Tokens
	r.dirty = true
	r.count(counterSegmentClosedPrefix + cause)
	return err
}

// advanceFrontier submits closed, unencoded segments to the checkpoint-owned port. The returned
// frontier describes local draft work, not a durable publication frontier or native context cut.
// Calls run without the runtime lock; a session change prevents stale local state updates.
func (r *schedRuntime) advanceFrontier(ctx context.Context) error {
	// A pass whose idle budget is already gone does nothing and counts as nothing: it has not
	// been starved of budget, it never had any. Checking here rather than only inside the port
	// keeps a cancelled tick from opening a draft it cannot finish advancing.
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	r.frontierRuns++
	r.frontierSkipTicks = 0
	r.gauge(gaugeFrontierTicks, 0)
	sess, advancer := r.session, r.advancer
	r.mu.Unlock()

	if advancer == nil {
		r.count(counterFrontierNoWriter)
		return nil
	}
	unencoded, err := r.segs.Unencoded(ctx, sess)
	if err != nil {
		r.log.Warn("scheduler: frontier advance could not list unencoded segments")
		return err
	}
	batch := unencoded[:0:0]
	for _, s := range unencoded {
		if s.Closed {
			batch = append(batch, s)
		}
	}
	slices.SortFunc(batch, func(a, b store.Segment) int { return cmp.Compare(a.StartTurn, b.StartTurn) })
	if len(batch) == 0 {
		r.settleFrontier(ctx, sess, nil, 0)
		return nil
	}
	ids, dpi, stop := verifyEvidence(ctx, r.segs, sess, batch)
	if len(dpi) > 0 {
		// A violation, not a shortfall: report it before anything else this pass decides, so it
		// cannot be lost behind an unrelated stop or an empty verified prefix.
		r.count(counterFrontierDPIGuard)
		r.log.Loud(msgDPIGuard, "segments", segmentIDInts(dpi))
	}
	if stop != nil {
		// The frontier stops HERE, at the first turn whose evidence is not durable, and the
		// segments beyond it stay unencoded — never skipped over, never encoded out of order.
		// Encoding past a gap would publish a checkpoint claiming a frontier the store cannot
		// support, and the missing span would then be unreachable from every later checkpoint
		// (Unencoded no longer lists an encoded segment, and the DPI guard refuses a second
		// encoding), which is the §8.2 content loss this check exists to prevent.
		r.count(counterFrontierUnverified)
		r.count(counterFrontierUnverified + "." + stop.reason)
		r.log.Warn(msgUnverifiedEvidence,
			"reason", stop.reason, "segment", int(stop.segment), "atTurn", int(stop.atTurn),
			"verified", len(ids), "backlog", len(batch))
	}
	if len(ids) == 0 {
		// Nothing verified. The backlog is still reported, so a frontier held back by a gap shows
		// up as O5 falling behind rather than as silence.
		r.settleFrontier(ctx, sess, nil, len(batch))
		return nil
	}
	newFrontier, err := advancer.Advance(ctx, sess, ids)
	if errors.Is(err, core.ErrAlreadyEncoded) {
		// FileWriter may have persisted clean batch members before reporting skipped foreign
		// encodings. Preserve its local frontier and report the guard once; the scheduler has
		// no authority to retry a subset or abort that shared draft.
		r.count(counterFrontierDPIGuard)
		r.log.Loud(msgDPIGuard, "segments", segmentIDInts(ids))
		r.settleFrontier(ctx, sess, &newFrontier, len(batch))
		return nil
	}
	if err != nil {
		r.log.Warn("scheduler: frontier advance failed; local frontier unchanged")
		return err
	}
	r.settleFrontier(ctx, sess, &newFrontier, len(batch))
	return nil
}

// evidenceStop records the first segment verification refused, and why.
type evidenceStop struct {
	reason  string
	segment core.SegmentID
	atTurn  core.TurnIndex
}

// verifyEvidence is the dependency check that runs BEFORE any advance: it re-reads every
// candidate segment from the durable segment log and returns the longest LEADING run whose
// evidence actually exists, is closed, belongs to this session, is well formed, and is
// turn-contiguous with its predecessor. batch must already be ascending by StartTurn.
//
// It is a PREFIX and not a filter, deliberately. A checkpoint's frontier is a single turn index —
// "everything up to here is encoded" — so encoding the segments AFTER a gap would advance that
// index past turns nothing has encoded, and the gap would then be permanently unreachable: it is
// not in the checkpoint, Unencoded stops listing its neighbours, and the DPI guard refuses to
// re-encode. Stopping at the gap leaves the missing span exactly where it was, still listed, still
// encodable by a later pass once its evidence lands.
//
// A segment that is ALREADY encoded is not a stop: its evidence is durable by definition, so the
// prefix steps over it and contiguity continues from its end turn. It is not resubmitted either —
// but it is returned in dpi rather than dropped, because a listing that offers an encoded segment
// is the §4.6 two-writer disagreement and the caller owes it a Loud line and a counter. Filtering
// it out here instead is what let a violation pass silently once already.
//
// The open segment is the ordinary in-flight case and it never reaches here — Unencoded's callers
// filter to Closed first — but a segment that came back open from the re-read is one that was
// closed when it was listed and is not now, so it is treated as in-flight and stops the run.
func verifyEvidence(ctx context.Context, segs store.SegmentLog, sess core.SessionID, batch []store.Segment) (verified, dpi []core.SegmentID, stop *evidenceStop) {
	ids := make([]core.SegmentID, 0, len(batch))
	var prevEnd core.TurnIndex
	havePrev := false
	for _, s := range batch {
		if err := ctx.Err(); err != nil {
			return ids, dpi, &evidenceStop{reason: evidenceCancelled, segment: s.ID, atTurn: s.StartTurn}
		}
		cur, err := segs.Get(ctx, s.ID)
		if err != nil {
			return ids, dpi, &evidenceStop{reason: evidenceUnreadable, segment: s.ID, atTurn: s.StartTurn}
		}
		switch {
		case cur.Session != sess:
			return ids, dpi, &evidenceStop{reason: evidenceForeign, segment: s.ID, atTurn: cur.StartTurn}
		case !cur.Closed:
			return ids, dpi, &evidenceStop{reason: evidenceNotClosed, segment: s.ID, atTurn: cur.StartTurn}
		case cur.EndTurn < cur.StartTurn:
			return ids, dpi, &evidenceStop{reason: evidenceMalformed, segment: s.ID, atTurn: cur.StartTurn}
		case havePrev && cur.StartTurn > prevEnd+1:
			return ids, dpi, &evidenceStop{reason: evidenceGap, segment: s.ID, atTurn: prevEnd + 1}
		}
		// The listing said unencoded and the durable log says otherwise. Report it, do not
		// resubmit it, and keep walking: the frontier is still contiguous across it.
		if cur.EncodedOnce {
			dpi = append(dpi, cur.ID)
		} else {
			ids = append(ids, cur.ID)
		}
		prevEnd, havePrev = cur.EndTurn, true
	}
	return ids, dpi, nil
}

// settleFrontier TAKES the lock (hence no *Locked suffix), stores newFrontier when one was
// reached (and the session is still sess), recomputes the residual and runs the over-budget
// check with the size of the closed-unencoded backlog the pass saw.
func (r *schedRuntime) settleFrontier(ctx context.Context, sess core.SessionID, newFrontier *core.TurnIndex, unencodedClosed int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session != sess {
		return
	}
	if newFrontier != nil {
		r.frontier = *newFrontier
		r.dirty = true
	}
	r.recomputeResidualLocked(ctx)
	r.checkResidualBudgetLocked(unencodedClosed)
}

// recomputeResidualLocked sets residual = contextTokens − Σ Segment.Tokens over this session's
// segments with EncodedOnce == true, clamped at ≥ 0. contextTokens is the same closed-plus-open
// sum Evaluate uses, so a segment whose Close omitted the "tokens" feature counts as zero on
// both sides and quietly under-reports — the second reason closeSegmentLocked always passes it.
func (r *schedRuntime) recomputeResidualLocked(ctx context.Context) {
	total := r.recomputeContextTokensLocked(ctx)
	var encoded core.Tokens
	if r.segs != nil {
		segs, err := r.segs.Range(ctx, 0, r.maxTurn)
		if err != nil {
			r.log.Debug("scheduler: segment range unavailable; residual counts nothing as encoded", "err", err.Error())
		} else {
			for _, s := range segs {
				if s.EncodedOnce && s.Session == r.session {
					encoded += s.Tokens
				}
			}
		}
	}
	r.residual = max(total-encoded, 0)
}

// checkResidualBudgetLocked is the honest signal that O5 is not keeping up: once per session,
// when the residual alone exceeds cfg.Checkpoint.Frontier.MaxResidualTokens, a Warn carrying the
// closed-unencoded backlog length and the frontier turn, and the gauge set to 1. The condition
// is the residual alone — a backlog does not suppress it — and the backlog length in the line is
// what keeps "O5 is behind" and "one open segment is over budget on its own" distinguishable.
// The gauge tracks the current state, so it reads 0 again once the residual is back under
// budget; the Warn does not repeat.
func (r *schedRuntime) checkResidualBudgetLocked(unencodedClosed int) {
	limit := core.Tokens(r.conf().Checkpoint.Frontier.MaxResidualTokens)
	if limit <= 0 || r.residual <= limit {
		r.gauge(gaugeResidualOverBudget, 0)
		return
	}
	r.gauge(gaugeResidualOverBudget, 1)
	if r.residualWarned {
		return
	}
	r.residualWarned = true
	r.log.Warn(msgResidualOverBudget,
		"residual", int(r.residual), "max", int(limit),
		"unencodedClosed", unencodedClosed, "frontierTurn", int(r.frontier))
}

// gauge sets a gauge when a registry is wired.
func (r *schedRuntime) gauge(name string, v int64) {
	if r.metrics != nil {
		r.metrics.Gauge(name).Set(v)
	}
}

// segmentIDInts renders ids for a log line.
func segmentIDInts(ids []core.SegmentID) []int {
	out := make([]int, len(ids))
	for i, id := range ids {
		out[i] = int(id)
	}
	return out
}
