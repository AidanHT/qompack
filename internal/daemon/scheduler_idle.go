package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
)

// O3 idle-time background work (00-ARCHITECTURE.md §8.4, §5.13; plan V4-SP-12-scheduler-l3
// "scheduler_idle.go"). Six tasks registered on SP-05's IdleController without editing it: the
// pure scheduler.Evaluate decides WHAT runs (Decision.Background) and the daemon decides WHEN
// (its idle tick and budget).
//
// The priority band is 110–160, after everything the daemon and SP-08 register (drain@10,
// sketches@20, metrics@30, observer.persist@50), so SP-12's six stay contiguous and in the
// plan's order, never interleaved with a drain that can consume the whole 2 s budget.
//
// The act. prefix is normative: RunOnce skips a task whose registered name begins "act." when
// the contract monitor is not in ModeFull — 00-ARCHITECTURE §12.1's "no scheduler-initiated
// checkpoints" in degraded-passive. Exactly one of the six acts (advance_frontier drives
// checkpoint.Writer.Advance); the other five are recording/maintenance work that must keep
// running while degraded. The scheduler.BackgroundTask VALUES are unchanged: the prefix belongs
// to the registration name, not to the decision vocabulary, and the gate keys on the value.

// The registered names and priorities. Names derive from the decision vocabulary so the two
// cannot drift; only the acting task carries SP-05's prefix.
const (
	idleTaskSchedAdvanceFrontier = actPrefix + string(scheduler.BackgroundAdvanceFrontier)
	idleTaskPrecomputeSlice      = string(scheduler.BackgroundPrecomputeSlice)
	idleTaskRefreshDelta         = string(scheduler.BackgroundRefreshDelta)
	idleTaskRebuildBloom         = string(scheduler.BackgroundRebuildBloom)
	idleTaskCompactDAG           = string(scheduler.BackgroundCompactDAG)
	idleTaskGC                   = string(scheduler.BackgroundGC)

	// The band is 110–160 in steps of 10, spelled as a base and a step so that no priority is
	// a bare literal that happens to coincide with an Appendix C default (§11.6 forbids 120).
	idlePrioSchedAdvanceFrontier = 110
	idlePrioStep                 = 10
	idlePrioPrecomputeSlice      = idlePrioSchedAdvanceFrontier + idlePrioStep
	idlePrioRefreshDelta         = idlePrioPrecomputeSlice + idlePrioStep
	idlePrioRebuildBloom         = idlePrioRefreshDelta + idlePrioStep
	idlePrioCompactDAG           = idlePrioRebuildBloom + idlePrioStep
	idlePrioGC                   = idlePrioCompactDAG + idlePrioStep
)

// The idle path's instruments: one Evaluate per pass, and the starvation counter/gauge that
// separate "O5 never got the budget" from the residual Warn's "O5 ran and is behind".
const (
	counterIdleEvaluate    = "sched.idle.evaluate"
	counterFrontierSkipped = "sched.frontier.skipped"
	gaugeFrontierTicks     = "sched.frontier.ticks_since_advance"
)

// precomputeSliceDeadline bounds one precompute_slice walk (plan: 250 ms).
const precomputeSliceDeadline = 250 * time.Millisecond

// The config values the task guards read.
const (
	rebuildOnStaleNextIdle = "nextIdle"
	slicingThin            = "thin"
)

// RegisterSchedulerIdleWork registers SP-12's six idle tasks on d.Idle() and binds d into the
// runtime. Idempotent: Register replaces by name. rt must be the daemon's own runtime.
func RegisterSchedulerIdleWork(d Daemon, rt scheduler.Runtime, o SchedulerRuntimeOptions) error {
	if d == nil {
		return errors.New("daemon: scheduler idle work: daemon required")
	}
	r, ok := rt.(*schedRuntime)
	if !ok {
		return fmt.Errorf("daemon: scheduler idle work: runtime is not the daemon's (%T)", rt)
	}
	ctl := d.Idle()
	if ctl == nil {
		return errors.New("daemon: scheduler idle work: daemon has no idle controller")
	}

	r.mu.Lock()
	r.d = d
	st := r.st
	r.mu.Unlock()
	// Reporting only. The BODY resolves the ledger live on every run (rebuildBloomBody); this
	// value is the one that existed at registration and is used for the log line alone.
	ledger := r.currentLedger()

	ctl.Register(idleTaskSchedAdvanceFrontier, idlePrioSchedAdvanceFrontier, r.idleTask(scheduler.BackgroundAdvanceFrontier, r.advanceFrontierTask))
	ctl.Register(idleTaskPrecomputeSlice, idlePrioPrecomputeSlice, r.idleTask(scheduler.BackgroundPrecomputeSlice, r.precomputeSliceTask))
	ctl.Register(idleTaskRefreshDelta, idlePrioRefreshDelta, r.idleTask(scheduler.BackgroundRefreshDelta, r.refreshDeltaTask))
	ctl.Register(idleTaskRebuildBloom, idlePrioRebuildBloom, r.idleTask(scheduler.BackgroundRebuildBloom, r.rebuildBloomBody(st)))
	ctl.Register(idleTaskCompactDAG, idlePrioCompactDAG, r.idleTask(scheduler.BackgroundCompactDAG, r.compactDAGTask))
	ctl.Register(idleTaskGC, idlePrioGC, r.idleTask(scheduler.BackgroundGC, r.gcTask))

	log := o.Log
	if log == nil {
		log = r.log
	}
	log.Info("scheduler: idle work registered",
		"tasks", []string{idleTaskSchedAdvanceFrontier, idleTaskPrecomputeSlice, idleTaskRefreshDelta, idleTaskRebuildBloom, idleTaskCompactDAG, idleTaskGC},
		"maintainer", isMaintainer(ledger))
	return nil
}

// isMaintainer reports whether the ledger ships its own maintenance task (ruling R19).
func isMaintainer(l negknow.Ledger) bool {
	_, ok := l.(negknow.Maintainer)
	return l != nil && ok
}

// idleTask wraps one task body in the pass discipline: a cancelled context returns at once,
// the decision is refreshed (one Evaluate per idle pass), the Background gate is consulted, and
// only then does the body run.
func (r *schedRuntime) idleTask(name scheduler.BackgroundTask, body func(ctx context.Context) error) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.refreshDecision(ctx)
		if !r.gate(name) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return body(ctx)
	}
}

// gate is the three guards every task body begins with: background work is on, and the last
// decision named this task. Before any session is bound Evaluate short-circuits on
// error_no_window with an empty Background, so every task is inert — the right posture for a
// daemon that has not yet seen a session, and for one that has not reached the soft floor.
func (r *schedRuntime) gate(name scheduler.BackgroundTask) bool {
	if !r.conf().Scheduler.Idle.BackgroundWork {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.lastDecision.Background, name)
}

// refreshDecision produces at most one decision per idle pass: Evaluate runs when
// idleRunBudget has elapsed since the last evaluation (one RunOnce pass is bounded by that
// budget, so every task in a pass reads the decision its first task produced) and lastDecision
// is reused otherwise. The residual is recomputed first — it is the input Evaluate plans
// advance_frontier from, and nothing else on the idle path refreshes it before the task that
// would consume it has been planned.
//
// It is also where starvation is detected: when the decision being replaced planned
// advance_frontier and frontierRuns has not moved since it was produced, the acting task never
// ran — the budget ran out before it, or degraded-passive skipped it — and that is counted
// (sched.frontier.skipped) and shown (ticks_since_advance) rather than left as silence. The
// clause is guarded by the task's own configuration switches (frontierPlannable): with either
// off the acting body never runs BY DESIGN while Evaluate still plans it, and reporting that as
// "O5 never got the budget" would be a permanently false signal.
func (r *schedRuntime) refreshDecision(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.nowMS()
	if since := int64(now - r.lastEvaluateTS); since >= 0 && since < idleRunBudget.Milliseconds() {
		return // inside the pass's window; a clock that stepped back re-evaluates rather than freezing
	}
	if r.frontierPlannable() && slices.Contains(r.lastDecision.Background, scheduler.BackgroundAdvanceFrontier) && r.frontierRuns == r.frontierPlannedAt {
		r.frontierSkipTicks++
		r.count(counterFrontierSkipped)
		r.gauge(gaugeFrontierTicks, int64(r.frontierSkipTicks))
	}
	r.recomputeResidualLocked(ctx)
	r.evaluateLocked(ctx)
	r.frontierPlannedAt = r.frontierRuns
	r.count(counterIdleEvaluate)
}

// frontierPlannable reports whether act.advance_frontier can run at all under the configuration:
// the plan's guard column for the task — background work on AND advance-on-segment-close on.
// With either switch off the acting body returns before advanceFrontier (gate, advanceFrontierTask),
// so a planned-but-unrun frontier is not starvation and refreshDecision must not count it.
func (r *schedRuntime) frontierPlannable() bool {
	return r.conf().Scheduler.Idle.BackgroundWork && r.conf().Checkpoint.Frontier.AdvanceOnSegmentClose
}

// ── Task bodies ─────────────────────────────────────────────────────────────────────────────

// advanceFrontierTask is act.advance_frontier: O5, behind the AdvanceOnSegmentClose switch (the
// BackgroundWork switch is gate's).
func (r *schedRuntime) advanceFrontierTask(ctx context.Context) error {
	if !r.conf().Checkpoint.Frontier.AdvanceOnSegmentClose {
		return nil
	}
	return r.advanceFrontier(ctx)
}

// precomputeSliceTask refreshes the backward-slice cache SP-11 and SP-15 read through
// PrecomputedSlice: criteria are the current segment's node, the files touched in the last
// defaultFeatureWindow turns and the most recent user prompt — §8.3's "current todo items,
// files under edit, the active plan, the most recent user intent" as far as the DAG exposes it.
func (r *schedRuntime) precomputeSliceTask(ctx context.Context) error {
	r.mu.Lock()
	sess, maxTurn := r.session, r.maxTurn
	r.mu.Unlock()
	criteria := r.sliceCriteria(ctx, sess, maxTurn)
	if len(criteria) == 0 {
		return nil
	}
	sl, err := r.graph.BackwardSlice(criteria, dag.SliceOptions{
		Thin: r.conf().Selection.Slicing == slicingThin, Decay: dag.DefaultDecay, Deadline: precomputeSliceDeadline,
	})
	if err != nil {
		return err
	}
	r.mu.Lock()
	if r.session == sess {
		r.precomputed, r.precomputedOK = sl, true
	}
	r.mu.Unlock()
	return nil
}

// sliceCriteria assembles precompute_slice's criterion set, in the order the plan names.
func (r *schedRuntime) sliceCriteria(ctx context.Context, sess core.SessionID, maxTurn core.TurnIndex) []dag.NodeID {
	var out []dag.NodeID
	if cur, err := r.segs.Current(ctx, sess); err == nil {
		if id := dag.SegmentNode(cur.ID); nodeExists(r.graph, id) {
			out = append(out, id)
		}
	}
	since := int(maxTurn) - defaultFeatureWindow
	var latestPrompt dag.Node
	havePrompt := false
	for _, n := range r.graph.NodesAfter(0) {
		switch n.Kind {
		case dag.KindFile:
			if int(n.Turn) > since {
				out = append(out, n.ID)
			}
		case dag.KindUserPrompt:
			if !havePrompt || n.Turn > latestPrompt.Turn {
				latestPrompt, havePrompt = n, true
			}
		}
	}
	if havePrompt {
		out = append(out, latestPrompt.ID)
	}
	return out
}

// nodeExists reports whether g holds id.
func nodeExists(g dag.Graph, id dag.NodeID) bool {
	_, ok := g.Node(id)
	return ok
}

// refreshDeltaTask seeds the Young–Daly δ from the checkpoint_finalize histogram's P50 while
// no compaction has been measured directly (refresh_delta is planned exactly while that is the
// case, so the fold happens once and a direct RecordCompactionCost is never double-counted),
// recomputes the closed-plus-open context count the burn-rate sample reads, and persists.
func (r *schedRuntime) refreshDeltaTask(ctx context.Context) error {
	var p50 time.Duration
	if r.metrics != nil {
		if name := checkpointFinalizeHist(); name != "" {
			if snap := r.metrics.Hist(name).Snapshot(); snap.N > 0 {
				p50 = snap.P50
			}
		}
	}
	r.mu.Lock()
	if p50 > 0 && r.deltaSamples == 0 {
		r.deltaEWMA, r.deltaSamples = p50.Seconds(), 1
	}
	r.recomputeContextTokensLocked(ctx)
	r.dirty = true
	r.mu.Unlock()
	return r.Persist(ctx)
}

// checkpointFinalizeHist is the B-E histogram's name, read from the budget table so it stays
// the single source of truth; "" when the table no longer declares B-E.
func checkpointFinalizeHist() string {
	for _, b := range obs.Budgets() {
		if b.ID == obs.BE {
			return b.Hist
		}
	}
	return ""
}

// rebuildBloomBody is rebuild_bloom's body (ruling R19). A ledger that implements
// negknow.Maintainer supplies its own fn through MaintenanceTask — the single source of the
// rebuild logic, registered once under SP-12's name and never under SP-09's. Otherwise the
// plan's body: RefreshStaleness, then RebuildBloom when something flipped or the filter needs
// resizing. A nil ledger, or rebuildOnStale != nextIdle, is inert FOR THAT RUN.
//
// The ledger is resolved INSIDE the returned closure, on every run, and that is the whole point.
// The daemon opens the negative-knowledge ledger lazily on the first compaction, which is always
// after RegisterSchedulerIdleWork has run, so a body that captured the ledger VALUE at
// registration baked in nil and returned a no-op for the life of the process — the task was
// registered, planned by Evaluate, counted as run, and did nothing, forever. Nothing ever
// re-registered it, so opening the ledger later could not revive it. Only the STORE is captured:
// it is required at construction and cannot change.
func (r *schedRuntime) rebuildBloomBody(st store.Store) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.conf().Eliminations.RebuildOnStale != rebuildOnStaleNextIdle {
			return nil
		}
		ledger := r.currentLedger()
		if ledger == nil {
			return nil
		}
		if m, ok := ledger.(negknow.Maintainer); ok {
			_, _, fn := m.MaintenanceTask(st)
			if fn == nil {
				return nil
			}
			return fn(ctx)
		}
		flipped, err := ledger.RefreshStaleness(ctx, st)
		if err != nil {
			return err
		}
		if len(flipped) == 0 && !ledger.Health().NeedsResize {
			return nil
		}
		_, _, err = ledger.RebuildBloom(ctx)
		return err
	}
}

// compactDAGTask compacts the on-disk dependence-DAG log.
func (r *schedRuntime) compactDAGTask(ctx context.Context) error {
	return r.graph.Compact(ctx)
}

// gcTask runs the store's reference-counted GC under the retention window from config and the
// remaining idle budget. A context without a deadline leaves Deadline 0 — unbounded, but still
// cancellable. A truncated mark harvest collects nothing (no cursor), so the budget the idle
// controller grants is what decides whether GC ever makes progress.
//
// A store runs one GC pass at a time (store/gcgate.go). If a session end's pass is running, this
// request waits for it and is answered by the one follow-up pass that starts after it, which also
// answers any session end that queued meanwhile. The wait spends this task's budget, and when the
// budget ends first the request withdraws with the context's error, which RunOnce logs; the next
// idle pass asks again.
func (r *schedRuntime) gcTask(ctx context.Context) error {
	p := store.GCPolicy{
		RetainDays:     r.conf().Store.Retention.Days,
		RetainSessions: r.conf().Store.Retention.Sessions,
	}
	if dl, ok := ctx.Deadline(); ok {
		p.Deadline = time.Until(dl)
		if p.Deadline <= 0 {
			// The budget was gone before the pass reached GC. ctx.Err() can still be nil here —
			// the deadline has elapsed but the context's timer has not fired — and nil would
			// report a pass that never ran as a success; the context's own error wins when it
			// exists, DeadlineExceeded is the honest signal otherwise.
			if err := ctx.Err(); err != nil {
				return err
			}
			return context.DeadlineExceeded
		}
	}
	rep, err := r.st.GC(ctx, p)
	if err != nil {
		return err
	}
	r.log.Debug("scheduler: idle gc pass", "truncated", rep.Truncated)
	return nil
}
