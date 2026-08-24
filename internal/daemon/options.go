package daemon

import (
	"context"
	"encoding/json"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
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

	// binds is the ordered list of late-binding functions Bind registers. New applies every one,
	// in registration order, to the Services it seeds from the fields above, before calling
	// DeclareProducers.
	binds []func(*Services)
}

// NewOptions returns an Options with every non-service field defaulted: a no-op logger, a fresh
// metrics registry and system clock sharing one Clock instance, and a SketchSet sized from cfg.
// It is a convenience constructor for a composition root that wants sane defaults; a bare
// Options{} literal remains valid (New tolerates it) for tests and minimal wiring.
func NewOptions(projectRoot string, cfg config.Config) Options {
	clk := core.SystemClock()
	return Options{
		ProjectRoot: projectRoot,
		Cfg:         cfg,
		Log:         logging.Nop(),
		Metrics:     obs.New(clk),
		Clock:       clk,
		Sketches:    NewSketchSet(cfg),
	}
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

// Bind registers fn to run once, in registration order, at daemon construction, against the
// Services set New seeds from the Options fields above. This is the seam a wave-2/3 subplan uses
// to attach its own function seams (ObserveTool, SessionStart, ...) without editing daemon
// internals or colliding with a sibling subplan doing the same thing.
func (o *Options) Bind(fn func(*Services)) {
	o.binds = append(o.binds, fn)
}

// Services is the late-bound dependency set every op handler reads through ServicesFrom. Every
// function-typed field is optional: New seeds the struct-typed fields from Options, applies every
// Bind function in registration order, then calls DeclareProducers. A handler that finds a nil
// seam still ACKs — the event is already durable in the WAL by the time any of these would run,
// so an unbound seam costs freshness, never data.
type Services struct {
	Store       store.Store
	Ledger      negknow.Ledger
	Sketches    *SketchSet
	Graph       dag.Graph
	Grammar     grammar.Sequitur
	Sched       scheduler.Runtime
	Checkpoints checkpoint.Writer

	// Mode reports the daemon's current §12.1 contract mode. It is the one seam here that runs
	// the OPPOSITE way to the nine below: those are CONSUMED by SP-05 and provided by a later
	// subplan through Bind, while Mode is PROVIDED by SP-05 and consumed by a bind body. It
	// exists because a bound function cannot reach the contract monitor any other way —
	// contract.NewMonitor is called inside New, into an unexported daemon field, and it appears on
	// neither Options nor the Daemon interface. New therefore constructs the monitor and assigns
	// this field BEFORE the bind loop runs, so a bind body may capture the func value and call it
	// later. Calling it DURING the bind body is still wrong: the monitor has not yet read
	// state/contract.json at that point and answers ModeFull for every project. Capture the func;
	// call it per event.
	Mode func() contract.Mode

	// The nine nil-tolerant function seams (§5.21, out-of-scope table). None is called by SP-05
	// except through the exact call sites handlers.go documents; every other subplan wires its own
	// implementation in via Bind.
	ObserveTool    func(ctx context.Context, e hookio.Event) error
	ObservePrompt  func(ctx context.Context, e hookio.Event) (hookio.Output, error)
	ObserveStop    func(ctx context.Context, e hookio.Event, subagent bool) error
	SessionStart   func(ctx context.Context, e hookio.Event) (hookio.Output, error)
	SessionEnd     func(ctx context.Context, e hookio.Event) error
	PreCompact     func(ctx context.Context, e hookio.Event) (hookio.Output, error)
	Rehydrate      func(ctx context.Context, e hookio.Event) (hookio.Output, error)
	MCPInitialized func(ctx context.Context) bool
	StatusExtra    func(ctx context.Context) (json.RawMessage, error)
}

// DeclareProducers is the bridge from a daemon's wired Services to the §12.1 not-yet-implemented
// mechanism: an assertion whose producer has never been declared reports OK/SevInfo/not-yet-
// implemented and its real Check never runs (contract.gated). The five names SP-05 itself
// produces are declared unconditionally — it observes every hook arrival, writes the
// SessionEnd/PreCompact marker, and records the PreCompact -> SessionStart(source=compact)
// sequence regardless of which later waves have landed. The four §12.1 names as later-wave —
// exactly these four, no more and no fewer — are declared only once the seam that produces their
// observable is bound.
func DeclareProducers(s *Services) {
	contract.DeclareProducer(contract.CSessionStartFires)
	contract.DeclareProducer(contract.CSessionStartSourceCompact)
	contract.DeclareProducer(contract.CHookPayloadShape)
	contract.DeclareProducer(contract.CTranscriptReadable)
	contract.DeclareProducer(contract.CPluginRootResolves)
	if s.PreCompact != nil || s.Checkpoints != nil {
		contract.DeclareProducer(contract.CPreCompactTiming)
		contract.DeclareProducer(contract.CPreCompactCustomInstr)
	}
	if s.Rehydrate != nil {
		contract.DeclareProducer(contract.CAdditionalContext)
	}
	if s.MCPInitialized != nil {
		contract.DeclareProducer(contract.CMCPRegistered)
	}
}

// ctxKey is the unexported type every context value this package injects is keyed by, so no
// other package can collide with (or read) these keys.
type ctxKey int

const (
	ctxServices ctxKey = iota
	ctxRegistry
	ctxDaemon
)

// emptyServices is handed out by ServicesFrom when a context carries no Services value (a handler
// invoked outside the daemon's own dispatch — a test calling a route function directly, for
// instance): every field is nil, which every call site already tolerates.
var emptyServices = &Services{}

// ServicesFrom returns the Services bound to ctx. It never returns nil: a context with nothing
// bound (or a differently-typed value under the same key, which cannot happen through this
// package's own API but is guarded against defensively) yields emptyServices, so a handler that
// forgets the type assertion still nil-checks fields the ordinary way instead of panicking on a
// nil *Services receiver.
func ServicesFrom(ctx context.Context) *Services {
	if v, ok := ctx.Value(ctxServices).(*Services); ok && v != nil {
		return v
	}
	return emptyServices
}

// RegistryFrom returns the SessionRegistry bound to ctx, or nil if none was bound.
func RegistryFrom(ctx context.Context) *SessionRegistry {
	v, _ := ctx.Value(ctxRegistry).(*SessionRegistry)
	return v
}

// DaemonFrom returns the Daemon bound to ctx, or nil if none was bound. The name is fixed by
// task-5-spec.md's options.go section alongside ServicesFrom/RegistryFrom, so the package-prefix
// stutter revive would otherwise flag is deliberate, not an oversight.
//
//nolint:revive // DaemonFrom is a pinned accessor name (task-5-spec.md options.go), matched to its siblings
func DaemonFrom(ctx context.Context) Daemon {
	v, _ := ctx.Value(ctxDaemon).(Daemon)
	return v
}

// withServices, withRegistry and withDaemon are the injection half of the three accessors above,
// called once per dispatched request by the daemon's composed ipc.Handler.
func withServices(ctx context.Context, s *Services) context.Context {
	return context.WithValue(ctx, ctxServices, s)
}

func withRegistry(ctx context.Context, r *SessionRegistry) context.Context {
	return context.WithValue(ctx, ctxRegistry, r)
}

func withDaemon(ctx context.Context, d Daemon) context.Context {
	return context.WithValue(ctx, ctxDaemon, d)
}
