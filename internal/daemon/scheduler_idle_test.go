package daemon

// The plan's 11 scheduler_idle_test.go cases (plans/V4-SP-12-scheduler-l3.md, "Test plan"):
// registration on a real daemon.New controller (ruling R21: relative order after every
// pre-existing name, never a count), the act. prefix under degraded-passive, daemon binding, the
// one-Evaluate-per-idle-pass rule, the starvation counter, the Background gate, the
// BackgroundWork switch, the ledger-nil and Maintainer paths of rebuild_bloom (ruling R19), the
// GC deadline, and context cancellation.
//
// Passes are driven through a real idleController over the in-package fake clock, so "budget
// exhausted" is a task advancing the clock, never a sleep.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/scheduler"
)

// sp12IdleNames is the plan's registration order, act.advance_frontier first.
var sp12IdleNames = []string{
	idleTaskSchedAdvanceFrontier, idleTaskPrecomputeSlice, idleTaskRefreshDelta,
	idleTaskRebuildBloom, idleTaskCompactDAG, idleTaskGC,
}

// allBackground is every O3 task, for a decision that gates everything in.
var allBackground = []scheduler.BackgroundTask{
	scheduler.BackgroundAdvanceFrontier, scheduler.BackgroundPrecomputeSlice, scheduler.BackgroundRefreshDelta,
	scheduler.BackgroundRebuildBloom, scheduler.BackgroundCompactDAG, scheduler.BackgroundGC,
}

// fakeDaemon is a Daemon whose only live member is its IdleController — what
// RegisterSchedulerIdleWork reaches through d.Idle().
type fakeDaemon struct{ idle IdleController }

func (d *fakeDaemon) Run(context.Context) error          { return nil }
func (d *fakeDaemon) Registry() *SessionRegistry         { return nil }
func (d *fakeDaemon) Drain(context.Context) (int, error) { return 0, nil }
func (d *fakeDaemon) Idle() IdleController               { return d.idle }
func (d *fakeDaemon) Stop(context.Context) error         { return nil }

// idleFixture is an rtFixture plus a real idleController (over the fixture's fake clock, logger
// and registry) with SP-12's six tasks registered, reached through a fakeDaemon. mode is read by
// the controller's mode func; tests flip it between passes.
type idleFixture struct {
	*rtFixture
	ctl  *idleController
	d    *fakeDaemon
	mode contract.Mode
}

// idleOpts configures newIdleFixture: rt mods run before the runtime is built; ledger is
// installed after construction and before registration (RegisterSchedulerIdleWork reads it for
// the Maintainer triple).
type idleOpts struct {
	rt     []func(*rtFixture)
	ledger negknow.Ledger
	mode   contract.Mode
}

func newIdleFixture(t testing.TB, o idleOpts) *idleFixture {
	t.Helper()
	fx := newRTFixture(t, o.rt...)
	if o.ledger != nil {
		fx.rt.mu.Lock()
		fx.rt.ledger = o.ledger
		fx.rt.mu.Unlock()
	}
	f := &idleFixture{rtFixture: fx, mode: contract.ModeFull}
	if o.mode != 0 {
		f.mode = o.mode
	}
	f.ctl = newIdleController(fx.cfg.Scheduler.Idle.DetectAfterSeconds, fx.clock, fx.log, fx.reg, func() contract.Mode { return f.mode })
	f.d = &fakeDaemon{idle: f.ctl}
	require.NoError(t, RegisterSchedulerIdleWork(f.d, fx.rt, fx.options()))
	return f
}

// seedDecision installs a decision as if Evaluate had just produced it, so the next pass reuses
// it instead of evaluating.
func (f *idleFixture) seedDecision(bg ...scheduler.BackgroundTask) {
	f.rt.mu.Lock()
	defer f.rt.mu.Unlock()
	f.rt.lastDecision = scheduler.Decision{Background: bg, Breakdown: map[string]float64{}}
	f.rt.lastEvaluateTS = f.now()
}

// pass runs one idle pass with the given budget and requires the controller itself not to fail.
func (f *idleFixture) pass(t testing.TB, budget time.Duration) []string {
	t.Helper()
	ran, err := f.ctl.RunOnce(context.Background(), budget)
	require.NoError(t, err)
	return ran
}

// taskFn returns the registered function for name.
func (f *idleFixture) taskFn(t testing.TB, name string) func(context.Context) error {
	t.Helper()
	f.ctl.mu.Lock()
	defer f.ctl.mu.Unlock()
	for _, task := range f.ctl.tasks {
		if task.name == name {
			return task.fn
		}
	}
	require.FailNowf(t, "task not registered", "%s", name)
	return nil
}

// prioOf returns the registered priority of name on c.
func prioOf(t testing.TB, c *idleController, name string) int {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, task := range c.tasks {
		if task.name == name {
			return task.prio
		}
	}
	require.FailNowf(t, "task not registered", "%s", name)
	return 0
}

// requireNoWork asserts none of the six bodies touched a collaborator.
func requireNoWork(t testing.TB, f *idleFixture) {
	t.Helper()
	require.Zero(t, f.counter(counterFrontierNoWriter), "advance_frontier did no work")
	require.Empty(t, f.graph.backwardSliceCalls, "precompute_slice did no work")
	require.Zero(t, f.counter(counterPersist), "refresh_delta did no work")
	require.Zero(t, f.graph.compactCalls, "compact_dag did no work")
	require.Empty(t, f.store.gcCalls, "gc did no work")
	_, ok := f.rt.PrecomputedSlice()
	require.False(t, ok)
}

// ── Registration ────────────────────────────────────────────────────────────────────────────

func TestIdleTasksRegistered(t *testing.T) {
	t.Parallel()
	d, err := New(Options{ProjectRoot: t.TempDir(), Cfg: config.Defaults(), Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok, "New returns the concrete daemon")
	ctl := dd.idle
	before := ctl.Registered()
	require.Contains(t, before, idleTaskDrain)
	require.Contains(t, before, idleTaskSketches)
	require.Contains(t, before, idleTaskMetrics)

	fx := newRTFixture(t)
	require.NoError(t, RegisterSchedulerIdleWork(d, fx.rt, fx.options()))

	after := ctl.Registered()
	require.Len(t, after, len(before)+len(sp12IdleNames))
	require.Equal(t, before, after[:len(before)], "every pre-existing task keeps its place ahead of SP-12's")
	require.Equal(t, sp12IdleNames, after[len(before):], "SP-12's six, in the plan's order, after all of them")

	require.Equal(t, 110, prioOf(t, ctl, idleTaskSchedAdvanceFrontier))
	require.Equal(t, 120, prioOf(t, ctl, idleTaskPrecomputeSlice))
	require.Equal(t, 130, prioOf(t, ctl, idleTaskRefreshDelta))
	require.Equal(t, 140, prioOf(t, ctl, idleTaskRebuildBloom))
	require.Equal(t, 150, prioOf(t, ctl, idleTaskCompactDAG))
	require.Equal(t, 160, prioOf(t, ctl, idleTaskGC))
	require.Equal(t, actPrefix+string(scheduler.BackgroundAdvanceFrontier), idleTaskSchedAdvanceFrontier,
		"the acting task carries SP-05's prefix; the decision vocabulary does not")
}

func TestIdleActingTaskSkippedInDegradedPassive(t *testing.T) {
	t.Parallel()
	f := newIdleFixture(t, idleOpts{mode: contract.ModeDegradedPassive})
	f.bind(rtSession)
	f.seedDecision(allBackground...)

	ran := f.pass(t, time.Second)
	for _, name := range sp12IdleNames[1:] {
		require.Contains(t, ran, name, "recording/maintenance work keeps running while degraded")
	}
	require.NotContains(t, ran, idleTaskSchedAdvanceFrontier, "§12.1: no scheduler-initiated checkpoints in degraded-passive")
	require.Zero(t, f.counter(counterFrontierNoWriter), "the acting body never ran")
	require.Equal(t, 1, f.graph.compactCalls, "…but the maintenance bodies did")
}

func TestIdleWorkBindsDaemon(t *testing.T) {
	t.Parallel()
	f := newIdleFixture(t, idleOpts{})
	require.Same(t, f.d, f.rt.d)
	names := f.ctl.Registered()
	require.Equal(t, sp12IdleNames, names)

	require.NoError(t, RegisterSchedulerIdleWork(f.d, f.rt, f.options()), "a second registration is idempotent")
	require.Same(t, f.d, f.rt.d)
	require.Equal(t, names, f.ctl.Registered(), "Register replaces by name: still six")

	require.Error(t, RegisterSchedulerIdleWork(nil, f.rt, f.options()))
	require.Error(t, RegisterSchedulerIdleWork(f.d, fakeForeignRuntime{}, f.options()), "a foreign Runtime is refused, not bound")
	require.Error(t, RegisterSchedulerIdleWork(f.d, nil, f.options()))
}

// ── The decision gate ───────────────────────────────────────────────────────────────────────

func TestIdleTaskInertBeforeSessionBind(t *testing.T) {
	t.Parallel()
	led := newFakeMaintLedger("negknow.rebuild", 140)
	f := newIdleFixture(t, idleOpts{ledger: led})
	f.graph.sliceResult = dag.Slice{Order: []dag.NodeID{"file:a.go"}}
	f.store.segs.addSegment(t, rtSession, 1, 2, 500)

	ran := f.pass(t, time.Second)
	require.Equal(t, sp12IdleNames, ran, "every task ran (the controller saw no error)…")
	require.Zero(t, f.log.count(logWarn), "…and none returned one")

	require.Equal(t, int64(1), f.counter(counterIdleEvaluate), "refreshDecision evaluated exactly once for the pass")
	d := f.rt.lastDecision
	require.Equal(t, 1.0, d.Breakdown["error_no_window"], "no session ⇒ no window")
	require.Empty(t, d.Background)
	requireNoWork(t, f)
	require.Zero(t, led.taskRuns, "rebuild_bloom did no work")
	require.Zero(t, f.counter(counterFrontierSkipped))
}

func TestIdleRefreshesDecisionOncePerPass(t *testing.T) {
	t.Parallel()
	f := newIdleFixture(t, idleOpts{})
	f.bind(rtSession)

	ran := f.pass(t, idleRunBudget)
	require.Equal(t, sp12IdleNames, ran)
	require.Equal(t, int64(1), f.counter(counterIdleEvaluate), "six tasks, one Evaluate")
	require.Equal(t, f.now(), f.rt.lastEvaluateTS)

	f.pass(t, idleRunBudget)
	require.Equal(t, int64(1), f.counter(counterIdleEvaluate), "inside idleRunBudget the pass reuses lastDecision")

	f.clock.Advance(idleRunBudget)
	f.pass(t, idleRunBudget)
	require.Equal(t, int64(2), f.counter(counterIdleEvaluate), "a pass idleRunBudget later evaluates again — twice, not twelve times")
	require.Equal(t, f.now(), f.rt.lastEvaluateTS)
}

func TestIdleFrontierSkippedCounted(t *testing.T) {
	t.Parallel()
	f := newIdleFixture(t, idleOpts{mode: contract.ModeDegradedPassive})
	f.bind(rtSession)
	f.rt.AddOpenSegmentTokens(500) // residual > 0 ⇒ every Evaluate plans advance_frontier
	ctx := context.Background()
	skipped := func() int64 { return f.counter(counterFrontierSkipped) }
	ticks := func() int64 { return f.reg.Gauge(gaugeFrontierTicks).Value() }

	// Pass 0 (degraded-passive) produces the first decision that plans advance_frontier; the
	// acting task is skipped by SP-05's prefix rule.
	ran := f.pass(t, idleRunBudget)
	require.NotContains(t, ran, idleTaskSchedAdvanceFrontier)
	require.Contains(t, f.rt.lastDecision.Background, scheduler.BackgroundAdvanceFrontier)
	require.Zero(t, skipped(), "the decision being replaced planned nothing")

	// Pass 1 (still degraded-passive): the refresh replaces a decision that planned the frontier
	// and finds frontierRuns unmoved.
	f.clock.Advance(idleRunBudget)
	f.pass(t, idleRunBudget)
	require.Equal(t, int64(1), skipped())
	require.Equal(t, int64(1), ticks())

	// Pass 2: back in ModeFull, but an earlier task eats the whole budget, so RunOnce breaks
	// before any SP-12 task — nothing refreshes, so the miss is only visible at the NEXT refresh.
	f.mode = contract.ModeFull
	hog := true
	f.ctl.Register("hog", 10, func(context.Context) error {
		if hog {
			f.clock.Advance(idleRunBudget)
		}
		return nil
	})
	f.clock.Advance(idleRunBudget)
	ran = f.pass(t, idleRunBudget)
	require.Equal(t, []string{"hog"}, ran, "the budget ran out before the acting task")
	require.Equal(t, int64(1), skipped(), "a pass in which no SP-12 task runs cannot count itself")

	// The next refresh — the first action of the next pass's first task — counts it.
	f.rt.refreshDecision(ctx)
	require.Equal(t, int64(2), skipped(), "two passes without the acting task ⇒ two skips")
	require.Equal(t, int64(2), ticks())

	// One real run, inside the same idle window (the refresh above is reused): the gauge resets.
	hog = false
	ran = f.pass(t, idleRunBudget)
	require.Contains(t, ran, idleTaskSchedAdvanceFrontier)
	require.Equal(t, int64(1), f.counter(counterFrontierNoWriter), "advance_frontier ran (against no writer)")
	require.Zero(t, ticks(), "the gauge resets when the frontier task runs")
	require.Equal(t, int64(2), skipped())

	// Later passes in which the task keeps running never raise the counter again.
	for range 3 {
		f.clock.Advance(idleRunBudget)
		ran = f.pass(t, idleRunBudget)
		require.Contains(t, ran, idleTaskSchedAdvanceFrontier)
		require.Equal(t, int64(2), skipped(), "the counter stops rising once the task runs")
		require.Zero(t, ticks())
	}

	// With advance-on-segment-close off the acting body returns before advanceFrontier by
	// configuration while Evaluate still plans the task (residual > 0): not starvation, not counted
	// (fix round 1, Important 1). Every pass here goes through the refresh path.
	off := newIdleFixture(t, idleOpts{rt: []func(*rtFixture){func(fx *rtFixture) {
		fx.cfg.Checkpoint.Frontier.AdvanceOnSegmentClose = false
	}}})
	off.bind(rtSession)
	off.rt.AddOpenSegmentTokens(500)
	off.pass(t, idleRunBudget)
	require.Contains(t, off.rt.lastDecision.Background, scheduler.BackgroundAdvanceFrontier, "Evaluate still plans it")
	for range 3 {
		off.clock.Advance(idleRunBudget)
		ran := off.pass(t, idleRunBudget)
		require.Contains(t, ran, idleTaskSchedAdvanceFrontier, "the task ran and returned at its switch")
		require.Zero(t, off.counter(counterFrontierSkipped), "a task configured off is never reported starved")
		require.Zero(t, off.reg.Gauge(gaugeFrontierTicks).Value())
		require.Zero(t, off.counter(counterFrontierNoWriter), "...and never reached advanceFrontier")
	}
	require.Equal(t, int64(4), off.counter(counterIdleEvaluate), "the refresh path was exercised on every pass")
}

func TestIdleTaskGatedByDecisionBackground(t *testing.T) {
	t.Parallel()
	led := newFakeMaintLedger("negknow.rebuild", 140)
	f := newIdleFixture(t, idleOpts{ledger: led})
	f.bind(rtSession)
	f.graph.sliceResult = dag.Slice{Order: []dag.NodeID{"file:a.go"}}
	f.seedDecision(scheduler.BackgroundAdvanceFrontier)

	ran := f.pass(t, time.Second)
	require.Equal(t, sp12IdleNames, ran)
	require.Equal(t, int64(1), f.counter(counterFrontierNoWriter), "only advance_frontier did work")
	require.Empty(t, f.store.gcCalls, "gc is a no-op")
	require.Zero(t, f.graph.compactCalls, "compact_dag is a no-op")
	require.Empty(t, f.graph.backwardSliceCalls)
	require.Zero(t, f.counter(counterPersist))
	require.Zero(t, led.taskRuns)
	require.Zero(t, f.counter(counterIdleEvaluate), "the seeded decision was reused, not replaced")

	// Widen the decision and every body runs.
	require.NoError(t, f.graph.AddNode(dag.Node{ID: dag.FileNode("a.go"), Kind: dag.KindFile, Turn: 1}))
	f.seedDecision(allBackground...)
	f.pass(t, time.Second)
	require.Equal(t, int64(2), f.counter(counterFrontierNoWriter))
	require.Len(t, f.store.gcCalls, 1)
	require.Equal(t, 1, f.graph.compactCalls)
	require.Len(t, f.graph.backwardSliceCalls, 1)
	require.Equal(t, int64(1), f.counter(counterPersist), "refresh_delta persisted")
	require.Equal(t, 1, led.taskRuns, "rebuild_bloom is the Maintainer's fn (R19)")
	sl, ok := f.rt.PrecomputedSlice()
	require.True(t, ok)
	require.Equal(t, f.graph.sliceResult, sl)
}

func TestIdleTasksOffWhenBackgroundWorkFalse(t *testing.T) {
	t.Parallel()
	led := newFakeMaintLedger("negknow.rebuild", 140)
	f := newIdleFixture(t, idleOpts{
		rt:     []func(*rtFixture){func(fx *rtFixture) { fx.cfg.Scheduler.Idle.BackgroundWork = false }},
		ledger: led,
	})
	f.bind(rtSession)
	f.graph.sliceResult = dag.Slice{Order: []dag.NodeID{"file:a.go"}}
	f.seedDecision(allBackground...) // even a stale decision that gates everything in

	ran := f.pass(t, time.Second)
	require.Equal(t, sp12IdleNames, ran, "the tasks are still registered and still return promptly")
	requireNoWork(t, f)
	require.Zero(t, led.taskRuns)
	require.Zero(t, f.log.count(logWarn))

	// The refresh path, past the budget window: the seeded decision planned advance_frontier and
	// frontierRuns never moved, but a task configured off is not starved (fix round 1, Important 1).
	f.rt.AddOpenSegmentTokens(500) // residual > 0: Evaluate WOULD plan the frontier if it could
	f.clock.Advance(idleRunBudget)
	f.pass(t, time.Second)
	require.Equal(t, int64(1), f.counter(counterIdleEvaluate), "the refresh evaluated")
	require.Zero(t, f.counter(counterFrontierSkipped), "no starvation for a task configured off")
	require.Zero(t, f.reg.Gauge(gaugeFrontierTicks).Value())
	require.Empty(t, f.rt.lastDecision.Background, "planBackground plans nothing with background work off")
	f.clock.Advance(idleRunBudget)
	f.pass(t, time.Second)
	require.Equal(t, int64(2), f.counter(counterIdleEvaluate))
	require.Zero(t, f.counter(counterFrontierSkipped))
	require.Zero(t, f.reg.Gauge(gaugeFrontierTicks).Value())
	requireNoWork(t, f)
}

// ── rebuild_bloom (ruling R19) ──────────────────────────────────────────────────────────────

func TestIdleTaskRebuildBloomSkippedWhenLedgerNil(t *testing.T) {
	t.Parallel()
	f := newIdleFixture(t, idleOpts{})
	require.Nil(t, f.rt.ledger)
	f.bind(rtSession)
	f.seedDecision(scheduler.BackgroundRebuildBloom)

	ran := f.pass(t, time.Second)
	require.Contains(t, ran, idleTaskRebuildBloom, "registered, ran, no panic")
	require.Zero(t, f.log.count(logWarn))
	require.Zero(t, f.log.count(logLoud))

	// The plain-Ledger body: RefreshStaleness, then RebuildBloom only when something flipped or
	// the filter needs resizing.
	plain := newFakeLedger()
	p := newIdleFixture(t, idleOpts{ledger: plain})
	p.bind(rtSession)
	p.seedDecision(scheduler.BackgroundRebuildBloom)
	p.pass(t, time.Second)
	require.Equal(t, 1, plain.refreshCalls)
	require.Zero(t, plain.rebuildCalls, "nothing flipped, no resize ⇒ no rebuild")
	plain.refreshResult = []string{"elim_1"}
	p.pass(t, time.Second)
	require.Equal(t, 2, plain.refreshCalls)
	require.Equal(t, 1, plain.rebuildCalls, "a flipped record ⇒ rebuild")
	plain.refreshResult = nil
	plain.health = negknow.Health{NeedsResize: true}
	p.pass(t, time.Second)
	require.Equal(t, 2, plain.rebuildCalls, "NeedsResize ⇒ rebuild")

	// The Maintainer body is the ledger's own fn, requested from MaintenanceTask (single source
	// of the rebuild logic); SP-09's own name is never registered.
	maint := newFakeMaintLedger("negknow.rebuild", 140)
	m := newIdleFixture(t, idleOpts{ledger: maint})
	require.GreaterOrEqual(t, maint.maintenanceTaskCalls, 1)
	require.NotContains(t, m.ctl.Registered(), "negknow.rebuild")
	m.bind(rtSession)
	m.seedDecision(scheduler.BackgroundRebuildBloom)
	m.pass(t, time.Second)
	require.Equal(t, 1, maint.taskRuns)
	require.Zero(t, maint.refreshCalls, "the plan's body is not run alongside the Maintainer's")

	// The config guard: rebuildOnStale != nextIdle leaves the task inert.
	never := newFakeMaintLedger("negknow.rebuild", 140)
	n := newIdleFixture(t, idleOpts{
		rt:     []func(*rtFixture){func(fx *rtFixture) { fx.cfg.Eliminations.RebuildOnStale = "never" }},
		ledger: never,
	})
	n.bind(rtSession)
	n.seedDecision(scheduler.BackgroundRebuildBloom)
	n.pass(t, time.Second)
	require.Zero(t, never.taskRuns)
}

// ── gc and cancellation ─────────────────────────────────────────────────────────────────────

func TestIdleTaskGCPassesRemainingDeadline(t *testing.T) {
	t.Parallel()
	f := newIdleFixture(t, idleOpts{rt: []func(*rtFixture){func(fx *rtFixture) {
		fx.cfg.Store.Retention.Days = 9
		fx.cfg.Store.Retention.Sessions = 4
	}}})
	f.bind(rtSession)
	f.seedDecision(scheduler.BackgroundGC)

	const budget = 500 * time.Millisecond
	f.pass(t, budget)
	require.Len(t, f.store.gcCalls, 1)
	p := f.store.gcCalls[0]
	require.Equal(t, 9, p.RetainDays)
	require.Equal(t, 4, p.RetainSessions)
	require.Greater(t, p.Deadline, time.Duration(0), "the remaining RunOnce budget")
	require.LessOrEqual(t, p.Deadline, budget)

	// Without a deadline on the context the policy is unbounded but still cancellable.
	require.NoError(t, f.taskFn(t, idleTaskGC)(context.Background()))
	require.Len(t, f.store.gcCalls, 2)
	require.Zero(t, f.store.gcCalls[1].Deadline)

	// A budget that is already gone is reported, never passed off as a success: the context's
	// own error when it has one, DeadlineExceeded in the window where the deadline has elapsed
	// but the context's timer has not fired (R6#5). No GC pass runs either way.
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	require.ErrorIs(t, f.rt.gcTask(expired), context.DeadlineExceeded)
	require.ErrorIs(t, f.rt.gcTask(pastDeadline{Context: context.Background()}), context.DeadlineExceeded)
	require.Len(t, f.store.gcCalls, 2, "no GC pass runs on an exhausted budget")

	// A GC error reaches the controller (logged, not fatal) rather than being swallowed.
	f.store.gcErr = errors.New("gc: disk")
	f.pass(t, budget)
	require.Equal(t, 1, f.log.count(logWarn))
}

// pastDeadline is a context whose Deadline has passed while its Err is still nil — the window
// between a deadline elapsing and the context's timer firing, which gcTask must not mistake
// for success.
type pastDeadline struct{ context.Context }

func (pastDeadline) Deadline() (time.Time, bool) { return time.Now().Add(-time.Second), true }

func TestIdleTasksHonourContextCancellation(t *testing.T) {
	// Not parallel: it measures a wall-clock bound on the return, and co-scheduled siblings
	// would only add noise to a number the plan fixes at 5 ms.
	led := newFakeMaintLedger("negknow.rebuild", 140)
	f := newIdleFixture(t, idleOpts{ledger: led})
	f.bind(rtSession)
	f.seedDecision(allBackground...)
	f.graph.sliceResult = dag.Slice{Order: []dag.NodeID{"file:a.go"}}
	f.clock.Advance(idleRunBudget) // a refresh reached now WOULD evaluate

	const returnBound = 5 * time.Millisecond
	for _, name := range sp12IdleNames {
		fn := f.taskFn(t, name)
		// Min-of-3 (ruling R45's pattern, R6#7): one sample under -race and co-load flakes; the
		// minimum is the machine's honest cost of the early return and still catches a body
		// that does work before looking at its context.
		var best time.Duration
		for i := range 3 {
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // cancelled before the body can do anything: "mid-run" for a body of fakes
			start := time.Now()
			err := fn(ctx)
			if elapsed := time.Since(start); i == 0 || elapsed < best {
				best = elapsed
			}
			if err != nil {
				require.ErrorIs(t, err, context.Canceled, name)
			}
		}
		require.LessOrEqual(t, best, returnBound, "%s returned after %v (best of 3)", name, best)
	}
	requireNoWork(t, f)
	require.Zero(t, led.taskRuns)
	require.Zero(t, f.counter(counterIdleEvaluate), "a cancelled pass does not even evaluate")
}
