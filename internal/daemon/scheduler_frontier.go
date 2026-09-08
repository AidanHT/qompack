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
	gaugeResidualOverBudget    = "sched.residual_over_budget"

	msgDPIGuard           = "frontier advance hit the DPI guard"
	msgResidualOverBudget = "scheduler: residual span over budget; O5 is not keeping up"
)

// CloseSegmentOn closes the session's current segment at turn at with cause ∈ {todo, test,
// commit} — the tap's task-boundary signals — and rolls its successor open. It takes the lock and
// delegates to closeSegmentLocked.
func (r *schedRuntime) CloseSegmentOn(ctx context.Context, at core.TurnIndex, f scheduler.Features, cause string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closeSegmentLocked(ctx, at, f, cause)
}

// closeSegmentLocked closes the session's current segment and opens its successor.
// Cause is one of: changepoint, todo, test, commit.
// SP-08 owns the session's FIRST Open; every subsequent roll is owned here.
func (r *schedRuntime) closeSegmentLocked(ctx context.Context, at core.TurnIndex, f scheduler.Features, cause string) error {
	if !r.cfg.Checkpoint.Frontier.AdvanceOnSegmentClose {
		return nil
	}
	cur, err := r.segs.Current(ctx, r.session)
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
		Session: r.session, StartTurn: at + 1, StartTS: r.nowMS(),
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
	ids := make([]core.SegmentID, len(batch))
	for i, s := range batch {
		ids[i] = s.ID
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
	limit := core.Tokens(r.cfg.Checkpoint.Frontier.MaxResidualTokens)
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
