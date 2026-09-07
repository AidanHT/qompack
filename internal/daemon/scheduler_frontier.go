package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
)

// O5 continuous frontier advancement (00-ARCHITECTURE.md §8.5, G1.5, G7.1; plan
// V4-SP-12-scheduler-l3 "scheduler_frontier.go"). Three responsibilities: close a segment when a
// boundary is observed, roll its successor open, and — during idle time — encode
// closed-and-unencoded segments into the checkpoint draft so the residual span stays O(delta).
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
	msgDPIGuardRecurred   = "frontier advance hit the DPI guard again; draft abandoned, frontier unchanged"
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

// advanceFrontier is the body of the O3-registered "act.advance_frontier" task (gated on the
// BackgroundAdvanceFrontier value in Decision.Background) and the O5 mechanism.
//
// The store, the writer and the segment log are called WITHOUT the runtime lock: SP-10's
// Advance reads originals back out of the store and encodes them, and a hook arriving mid-idle
// must not queue behind that on mu. Everything the call needs is snapshotted under mu first and
// every result is stored under mu only if the session is still the one it was computed for.
func (r *schedRuntime) advanceFrontier(ctx context.Context) error {
	r.mu.Lock()
	r.frontierRuns++
	r.frontierSkipTicks = 0
	r.gauge(gaugeFrontierTicks, 0)
	sess, ckpt, sources := r.session, r.ckpt, r.sources
	r.mu.Unlock()

	// 1. No writer (SP-10 not in the build) is the Rule W-2 posture: counted, not an error.
	if ckpt == nil || sources == nil {
		r.count(counterFrontierNoWriter)
		return nil
	}

	// 2. The batch: closed, unencoded, ascending by StartTurn.
	unencoded, err := r.segs.Unencoded(ctx, sess)
	if err != nil {
		r.log.Warn("scheduler: frontier advance could not list unencoded segments", "err", err.Error())
		return err
	}
	batch := unencoded[:0:0]
	for _, s := range unencoded {
		if s.Closed {
			batch = append(batch, s)
		}
	}
	slices.SortFunc(batch, func(a, b store.Segment) int { return cmp.Compare(a.StartTurn, b.StartTurn) })

	// 3. Nothing to encode still recomputes the residual and runs the over-budget check:
	// "nothing left to encode and still over budget" is one of the two states that check exists
	// to report.
	if len(batch) == 0 {
		r.settleFrontier(ctx, sess, nil, 0)
		return nil
	}

	// 4. The draft, opened lazily. SP-12 never constructs the SourceSet: SP-10 owns it, and a
	// SourceSet has no field that can carry live context text, so "checkpoint from a summary"
	// stays uncompilable whoever builds it.
	draft, err := r.ensureDraft(ctx, sess, ckpt, sources)
	if err != nil {
		var noSources errNoSources
		if errors.As(err, &noSources) {
			r.count(counterFrontierNoWriter)
			r.log.Warn("scheduler: frontier advance has no source set; skipped", "err", noSources.err.Error())
			return nil
		}
		r.log.Warn("scheduler: frontier advance could not open a draft", "err", err.Error())
		return err
	}

	// 5. Advance, with the §4.6 DPI guard handled exactly once: drop the offenders, retry once
	// with the remainder, abandon the draft on a recurrence. Never re-encode from a checkpoint.
	ids := make([]core.SegmentID, len(batch))
	for i, s := range batch {
		ids[i] = s.ID
	}
	newFrontier, err := ckpt.Advance(ctx, draft, ids)
	if errors.Is(err, core.ErrAlreadyEncoded) {
		r.count(counterFrontierDPIGuard)
		r.log.Loud(msgDPIGuard, "segments", segmentIDInts(ids), "err", err.Error())
		remainder := r.withoutEncoded(ctx, ids)
		if len(remainder) == 0 {
			r.settleFrontier(ctx, sess, nil, len(batch))
			return nil
		}
		newFrontier, err = ckpt.Advance(ctx, draft, remainder)
		if errors.Is(err, core.ErrAlreadyEncoded) {
			r.count(counterFrontierDPIGuard)
			r.log.Loud(msgDPIGuardRecurred, "segments", segmentIDInts(remainder), "err", err.Error())
			r.abandonDraft(sess, ckpt, draft)
			r.settleFrontier(ctx, sess, nil, len(batch))
			return nil
		}
	}
	if err != nil {
		r.log.Warn("scheduler: frontier advance failed; frontier unchanged", "segments", segmentIDInts(ids), "err", err.Error())
		return err
	}

	// 6–8. Move the frontier, recompute the residual, check the budget.
	r.settleFrontier(ctx, sess, &newFrontier, len(batch))
	return nil
}

// errNoSources marks a Sources() failure, which advanceFrontier treats exactly like a missing
// writer (counter, Warn, return nil) rather than as a task error.
type errNoSources struct{ err error }

func (e errNoSources) Error() string { return "scheduler: no source set: " + e.err.Error() }
func (e errNoSources) Unwrap() error { return e.err }

// ensureDraft returns the open draft, opening one through Begin when there is none. A draft
// opened for a session that was rebound while Begin ran is aborted rather than kept.
func (r *schedRuntime) ensureDraft(ctx context.Context, sess core.SessionID, ckpt checkpoint.Writer, sources func() (checkpoint.SourceSet, error)) (*checkpoint.Draft, error) {
	r.mu.Lock()
	draft, parent := r.draft, r.lastCheckpointSeq
	r.mu.Unlock()
	if draft != nil {
		return draft, nil
	}
	src, err := sources()
	if err != nil {
		return nil, errNoSources{err: err}
	}
	draft, err = ckpt.Begin(ctx, sess, parent, src)
	if err != nil {
		return nil, err
	}
	if draft == nil {
		return nil, fmt.Errorf("scheduler: checkpoint writer returned no draft: %w", core.ErrNotImplemented)
	}
	r.mu.Lock()
	switch {
	case r.session != sess:
		r.mu.Unlock()
		_ = ckpt.Abort(draft)
		return nil, fmt.Errorf("scheduler: session rebound while opening a draft: %w", context.Canceled)
	case r.draft != nil:
		// Another opener won the race (none exists today; the idle controller is serial).
		existing := r.draft
		r.mu.Unlock()
		_ = ckpt.Abort(draft)
		return existing, nil
	default:
		r.draft = draft
		r.mu.Unlock()
		return draft, nil
	}
}

// withoutEncoded returns ids minus every segment the log now reports as encoded. The log is the
// DPI guard's source of truth: a segment that MarkEncoded refused is one the log already
// carries EncodedOnce for. A writer refusing on state of its own leaves the batch unchanged, and
// the retry then recurs and abandons the draft — the safe direction.
func (r *schedRuntime) withoutEncoded(ctx context.Context, ids []core.SegmentID) []core.SegmentID {
	out := make([]core.SegmentID, 0, len(ids))
	for _, id := range ids {
		seg, err := r.segs.Get(ctx, id)
		if err == nil && seg.EncodedOnce {
			continue
		}
		out = append(out, id)
	}
	return out
}

// abandonDraft aborts draft and forgets it, unless the runtime has already moved on from it.
func (r *schedRuntime) abandonDraft(sess core.SessionID, ckpt checkpoint.Writer, draft *checkpoint.Draft) {
	r.mu.Lock()
	if r.session == sess && r.draft == draft {
		r.draft = nil
	}
	r.mu.Unlock()
	if err := ckpt.Abort(draft); err != nil {
		r.log.Warn("scheduler: abandoning the draft after a DPI violation", "err", err.Error())
	}
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
