package daemon

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
)

// Options is the late-bound dependency set (§5.4).
//
// Every service member may be nil, and every call site must tolerate that: waves 1–2 run with
// Checkpoints and Sched absent. Treating nil as "not built yet" rather than as a programming
// error is what lets the daemon start and serve `status` in a half-built tree, which is when
// being able to ask it anything at all matters most.
type Options struct {
	ProjectRoot string
	Cfg         config.Config
	Log         logging.Logger
	Metrics     obs.Registry
	Clock       core.Clock

	Store       store.Store
	Ledger      negknow.Ledger
	Sketches    *SketchSet
	Graph       dag.Graph
	Grammar     grammar.Sequitur
	Sched       scheduler.Runtime
	Checkpoints checkpoint.Writer

	// handlers is the op-routing table. It is a map rather than a switch so a later wave adds an
	// op by calling Handle at wiring time instead of editing a function in this package — the
	// difference between four wave-3 subplans composing and four subplans conflicting.
	handlers map[ipc.Op]ipc.Handler
}

// Handle registers h as the handler for op, replacing any previous registration.
//
// It is defined on Options rather than on Daemon so the table is complete before Run starts:
// registering a handler against a running server would need locking on the hot path, and B-A has
// no room for a contended mutex per request.
func (o *Options) Handle(op ipc.Op, h ipc.Handler) {
	if o.handlers == nil {
		o.handlers = map[ipc.Op]ipc.Handler{}
	}
	o.handlers[op] = h
}

// Handler returns the handler registered for op, if any.
func (o *Options) Handler(op ipc.Op) (ipc.Handler, bool) {
	h, ok := o.handlers[op]
	return h, ok
}

// Ops returns every registered op. The order is unspecified.
func (o *Options) Ops() []ipc.Op {
	ops := make([]ipc.Op, 0, len(o.handlers))
	for op := range o.handlers {
		ops = append(ops, op)
	}
	return ops
}

// IdleController schedules the O3/O5 background work that may run only while the session is idle
// (§8.4): compaction, GC, sketch persistence — everything whose cost is unacceptable on the hot
// path but acceptable when nobody is waiting.
type IdleController interface {
	// Register adds work to run when idle. Lower prio runs first.
	Register(name string, prio int, fn func(ctx context.Context) error)
	// Notify records the timestamp of the most recent session activity.
	Notify(lastActivity core.UnixMilli)
	// IsIdle reports whether enough time has passed since the last activity.
	IsIdle(now core.UnixMilli) bool
	// RunOnce runs registered work until budget is exhausted, returning the names that ran.
	RunOnce(ctx context.Context, budget time.Duration) (ran []string, err error)
}

// Daemon is the resident process (§5.4).
type Daemon interface {
	// Run serves until ctx is cancelled.
	Run(ctx context.Context) error
	// Registry returns the live session registry.
	Registry() *SessionRegistry
	// Drain replays the spool and the WAL. It is idempotent, and returns how many entries it
	// applied.
	Drain(ctx context.Context) (int, error)
	// Idle returns the controller for O3/O5 background work.
	Idle() IdleController
	// Stop shuts down cleanly.
	Stop(ctx context.Context) error
}

// New returns a stub Daemon. Construction succeeds so a composition root can wire one today; every
// operation reports core.ErrNotImplemented until SP-05 lands.
func New(o Options) (Daemon, error) {
	if o.Log == nil {
		o.Log = logging.Nop()
	}
	if o.Clock == nil {
		o.Clock = core.SystemClock()
	}
	return &stubDaemon{opts: o, registry: NewSessionRegistry()}, nil
}

// stubDaemon reports core.ErrNotImplemented for every operation.
//
// Registry and Idle return real, usable values rather than nil: they are the seams later waves
// register against before the daemon itself runs, and handing back nil would make the extension
// points unusable exactly when they are supposed to be wired.
type stubDaemon struct {
	opts     Options
	registry *SessionRegistry
	idle     stubIdle
}

func (d *stubDaemon) Run(context.Context) error {
	return fmt.Errorf("%w: daemon.Run (SP-05)", core.ErrNotImplemented)
}

func (d *stubDaemon) Registry() *SessionRegistry { return d.registry }

func (d *stubDaemon) Drain(context.Context) (int, error) {
	return 0, fmt.Errorf("%w: daemon.Drain (SP-05)", core.ErrNotImplemented)
}

func (d *stubDaemon) Idle() IdleController { return &d.idle }

func (d *stubDaemon) Stop(context.Context) error {
	return fmt.Errorf("%w: daemon.Stop (SP-05)", core.ErrNotImplemented)
}

// stubIdle accepts registrations — so later waves can wire O3/O5 work today — but runs nothing.
type stubIdle struct {
	mu    sync.Mutex
	work  []idleWork
	last  core.UnixMilli
	dirty bool
}

type idleWork struct {
	name string
	prio int
	fn   func(ctx context.Context) error
}

func (i *stubIdle) Register(name string, prio int, fn func(ctx context.Context) error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.work = append(i.work, idleWork{name: name, prio: prio, fn: fn})
}

func (i *stubIdle) Notify(lastActivity core.UnixMilli) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.last = lastActivity
	i.dirty = true
}

// IsIdle reports false unconditionally.
//
// This is the documented zero value, not an oversight: the real predicate compares now-last
// against runtime.idle.afterMS, and answering "yes, idle" while nothing can actually run would
// invite a caller to schedule work that silently never happens. False is the honest answer for a
// daemon that does not run background work at all.
func (i *stubIdle) IsIdle(core.UnixMilli) bool { return false }

func (i *stubIdle) RunOnce(context.Context, time.Duration) ([]string, error) {
	return nil, fmt.Errorf("%w: daemon.IdleController.RunOnce (SP-05)", core.ErrNotImplemented)
}

// Registered returns the names registered so far, so a wiring test can prove its work landed even
// though nothing runs it yet.
func (i *stubIdle) Registered() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	names := make([]string, 0, len(i.work))
	for _, w := range i.work {
		names = append(names, w.name)
	}
	return names
}
