package daemon

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// actPrefix marks an idle task as an ACTING task (§12.1): RunOnce skips it when the current mode
// does not MayAct(). Every other task name is recording/maintenance work and always runs.
const actPrefix = "act."

// counterIdleTaskPanic is the obs.Counter name incremented when a registered idle task panics.
const counterIdleTaskPanic = "idle_task_panic"

// histIdleTaskPrefix names the per-task histogram RunOnce times each run into: histIdleTaskPrefix
// + task name.
const histIdleTaskPrefix = "idle_task_"

// IdleController schedules the O3/O5 background work that may run only while the session is idle
// (§8.4): compaction, GC, sketch persistence — everything whose cost is unacceptable on the hot
// path but acceptable when nobody is waiting.
type IdleController interface {
	// Register adds work to run when idle. Lower prio runs first. Registering a name that already
	// exists replaces it in place, keeping its priority-sorted position current.
	Register(name string, prio int, fn func(ctx context.Context) error)
	// Notify records the timestamp of the most recent session activity.
	Notify(lastActivity core.UnixMilli)
	// IsIdle reports whether enough time has passed since the last activity.
	IsIdle(now core.UnixMilli) bool
	// RunOnce runs registered work until budget is exhausted, returning the names that ran.
	RunOnce(ctx context.Context, budget time.Duration) (ran []string, err error)
}

// idleTask is one registration: a name (used for replace-in-place, dedup and the act. prefix
// rule), a priority (ascending order = run order) and the function itself.
type idleTask struct {
	name string
	prio int
	fn   func(context.Context) error
}

// idleController is the real IdleController (task-5-spec.md idle.go).
type idleController struct {
	mu    sync.Mutex
	tasks []idleTask

	lastActivity core.UnixMilli
	afterSeconds int // cfg.Scheduler.Idle.DetectAfterSeconds (Appendix C default 120)

	clk  core.Clock
	log  logging.Logger
	m    obs.Registry
	mode func() contract.Mode // RunOnce skips "act."-prefixed tasks when !mode().MayAct()
}

// newIdleController constructs an idleController. afterSeconds <= 0 falls back to
// defaultIdleDetectAfterSeconds, mirroring config.Defaults() without itself being a default
// source. A nil mode falls back to a function that always reports ModeFull, so a caller that has
// not wired a monitor yet (a unit test exercising the controller alone) never skips an act. task
// for a reason it never asked for.
func newIdleController(afterSeconds int, clk core.Clock, log logging.Logger, m obs.Registry, mode func() contract.Mode) *idleController {
	if afterSeconds <= 0 {
		afterSeconds = defaultIdleDetectAfterSeconds
	}
	if clk == nil {
		clk = core.SystemClock()
	}
	if log == nil {
		log = logging.Nop()
	}
	if mode == nil {
		mode = func() contract.Mode { return contract.ModeFull }
	}
	return &idleController{afterSeconds: afterSeconds, clk: clk, log: log, m: m, mode: mode}
}

// defaultIdleDetectAfterSeconds mirrors config.Defaults().Scheduler.Idle.DetectAfterSeconds
// (120). It is not itself a default source (D11 lives in internal/config/defaults.go) — it exists
// only so a caller that hands newIdleController a zero config.Config still gets a usable idle
// window instead of "idle after zero seconds".
const defaultIdleDetectAfterSeconds = 120 //nomagic:allow mirrors config.Defaults(), not a new default (§6.1)

// Register adds fn to run when idle, replacing any existing task of the same name in place (its
// priority is updated and the list re-sorted), or appending a new one. Lower prio runs first.
func (c *idleController) Register(name string, prio int, fn func(ctx context.Context) error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, t := range c.tasks {
		if t.name == name {
			c.tasks[i] = idleTask{name: name, prio: prio, fn: fn}
			c.sortLocked()
			return
		}
	}
	c.tasks = append(c.tasks, idleTask{name: name, prio: prio, fn: fn})
	c.sortLocked()
}

func (c *idleController) sortLocked() {
	sort.SliceStable(c.tasks, func(i, j int) bool { return c.tasks[i].prio < c.tasks[j].prio })
}

// Registered returns the names registered so far, in run order — used by tests and by a status
// route that wants to show what O3/O5 work is wired without running it.
func (c *idleController) Registered() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.tasks))
	for _, t := range c.tasks {
		names = append(names, t.name)
	}
	return names
}

// Notify records the newest activity timestamp; an older timestamp than what is already recorded
// is ignored, so an out-of-order call can never rewind the idle clock.
func (c *idleController) Notify(lastActivity core.UnixMilli) {
	c.mu.Lock()
	if lastActivity > c.lastActivity {
		c.lastActivity = lastActivity
	}
	c.mu.Unlock()
}

// IsIdle reports now-lastActivity >= afterSeconds*1000 — keyed on activity, matching §8.4's idle
// model.
func (c *idleController) IsIdle(now core.UnixMilli) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return int64(now-c.lastActivity) >= int64(c.afterSeconds)*1000
}

// RunOnce runs registered tasks in priority order until budget is spent, giving each task a
// sub-context of budget-elapsed, recovering panics per task (which excludes that task from ran),
// and skipping any "act."-prefixed task when the current mode does not MayAct(). Elapsed time is
// measured through the injected Clock, not time.Now, so a test can simulate a task "consuming the
// whole budget" by advancing a FakeClock inside the task's own function body rather than
// sleeping.
func (c *idleController) RunOnce(ctx context.Context, budget time.Duration) ([]string, error) {
	c.mu.Lock()
	tasks := append([]idleTask(nil), c.tasks...)
	mayAct := c.mode().MayAct()
	c.mu.Unlock()

	deadline := c.clk.Now().Add(budget)
	var ran []string
	for _, t := range tasks {
		if strings.HasPrefix(t.name, actPrefix) && !mayAct {
			continue
		}
		remain := deadline.Sub(c.clk.Now())
		if remain <= 0 {
			break
		}
		tctx, cancel := context.WithTimeout(ctx, remain)
		panicked, err := c.runTask(tctx, t)
		cancel()
		if panicked {
			continue // isolated: a panicking task never counts as having run
		}
		if err != nil {
			c.log.Warn("daemon: idle task returned an error", "name", t.name, "err", err)
		}
		ran = append(ran, t.name)
	}
	return ran, nil
}

// runTask invokes t.fn, recovering a panic so one broken task can never stop the rest of the
// idle-time work from running, and times the call into this task's own histogram when a metrics
// registry is available.
func (c *idleController) runTask(ctx context.Context, t idleTask) (panicked bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			if c.m != nil {
				c.m.Counter(counterIdleTaskPanic).Add(1)
			}
			c.log.Loud("daemon: idle task panicked", "name", t.name, "recover", r)
		}
	}()
	run := func() error { return t.fn(ctx) }
	if c.m != nil {
		return false, obs.Timed(c.m.Hist(histIdleTaskPrefix+t.name), run)
	}
	return false, run()
}
