package daemon

import (
	"context"
	"encoding/json"
	"sync"

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

	// OpenLedger opens the negative-knowledge ledger the FIRST time a caller actually needs one,
	// assigns the handle to Ledger, and answers with that same handle — or with the same nil —
	// on every later call. WireRehydrator publishes it; it is nil in any Options that wiring has
	// not run over.
	//
	// It is the seam that keeps the ledger's laziness and the checkpointer's need for it from
	// being in conflict. negknow.Open has exactly ONE production call site, inside this accessor,
	// because an eager open creates sketches/tried.bloom and holds a records/eliminations.jsonl
	// handle in every daemon that never compacts, and §3.3 gives that file to the ledger alone.
	// But a compaction needs the ledger BEFORE it needs the rehydration that used to open it: the
	// PreCompact hook fires first and the SessionStart(source=compact) that follows is already too
	// late for it. Publishing the opener here lets the PreCompact seam trigger the SAME one-shot
	// open, so nothing opens a second handle and nothing opens anything at all in a daemon that
	// never compacts.
	//
	// The memoization is what makes it safe to call from anywhere: two compactions racing on the
	// daemon's worker pool get one handle, and an open that FAILED is Loud once and then answers
	// nil for the life of the process rather than retrying per compaction.
	OpenLedger func() negknow.Ledger

	// ledger is the synchronized home of the handle OpenLedger produces. It is a POINTER for the
	// same reason shutdown is — New copies Options by value, and a sync.RWMutex in a copied
	// struct is both a vet copylocks failure and a lock nobody shares — so every copy of an
	// Options, and every closure over the *Options wiring holds, addresses one cell.
	//
	// It exists because the publication crosses goroutines. The write happens on whichever worker
	// goroutine reaches the first PreCompact; the reads happen on the per-connection goroutines
	// ipc.Server spawns, through three accessors that never call the opener: the MCP tools'
	// liveLedger, the scheduler's LedgerFn, and the checkpoint SourceSet supplier. sync.Once
	// orders only goroutines that call Do — a plain field read elsewhere has no edge to it — so
	// the raw field this replaced was a data race on a two-word interface value, which under the
	// detector is a CI failure and without it is a non-nil interface over a nil data pointer.
	ledger *ledgerCell

	// handlers is the op-routing table. It is a map rather than a switch so a later wave adds an
	// op by calling Handle at wiring time instead of editing a function in this package — the
	// difference between four wave-3 subplans composing and four subplans conflicting.
	handlers map[ipc.Op]ipc.Handler

	// binds is the ordered list of late-binding functions Bind registers. New applies every one,
	// in registration order, to the Services it seeds from the fields above, before calling
	// DeclareProducers.
	binds []func(*Services)

	// shutdown is the close list for resources the daemon's own WIRING opened. It is a POINTER on
	// purpose: New copies Options by value, and every resource that matters here is opened LAZILY,
	// long after that copy was taken -- OpenLedger fires on the first compaction, on a worker
	// goroutine, with New already several seconds in the past. A slice would register onto a copy
	// nothing reads. The pointer is shared, so a closer registered at any time before Stop reaches
	// the daemon that must run it.
	//
	// It is nil in an Options no wiring has run over, and closeAll is nil-safe, so a bare
	// Options{} literal stays valid.
	shutdown *shutdownHooks
}

// shutdownHooks is the list of resources the daemon OWNS: the ones its own wiring opened, as
// opposed to the ones a composition root opened and handed in on Options.
//
// The distinction is the whole point. A caller-supplied Store or Ledger belongs to the caller and
// is closed by the caller; a handle the daemon's own lazy opener created has no other owner, and
// before this list existed it had no close path at all except one defer in internal/cli's
// runDaemon. Every other embedder of daemon.New -- the in-process e2e harnesses included -- leaked
// it: Stop returned with an append handle still open on records/eliminations.jsonl, which on
// Windows blocks the enclosing TempDir cleanup and on Linux leaks silently for the life of the
// process.
type shutdownHooks struct {
	mu  sync.Mutex
	fns []ownedResource
}

// ownedResource is one closer plus the name Stop reports it under.
type ownedResource struct {
	name  string
	close func() error
}

// add appends one closer. It is safe to call from any goroutine, because the openers that call it
// are themselves reachable from the daemon's worker pool.
func (h *shutdownHooks) add(name string, closeFn func() error) {
	if h == nil || closeFn == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fns = append(h.fns, ownedResource{name: name, close: closeFn})
}

// closeAll runs every registered closer once, in registration order, and empties the list so a
// second Stop -- or a composition root's own belt-and-braces defer -- does no work. A failure is a
// Warn: shutdown continues, because the steps after it (state.bin removal, the lock release) are
// what let the NEXT daemon start.
func (h *shutdownHooks) closeAll(log logging.Logger) {
	if h == nil {
		return
	}
	h.mu.Lock()
	fns := h.fns
	h.fns = nil
	h.mu.Unlock()
	if log == nil {
		log = logging.Nop()
	}
	for _, r := range fns {
		if err := r.close(); err != nil {
			log.Warn("daemon: stop: closing "+r.name, "err", err.Error())
		}
	}
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
		ledger:      &ledgerCell{},
	}
}

// ledgerCell is the one synchronized home of the lazily opened negative-knowledge ledger handle.
// A plain RWMutex rather than an atomic.Pointer: the reads are per MCP tool call and per idle
// task, not per hook, so the cost is irrelevant beside being obviously correct, and an
// atomic.Pointer[negknow.Ledger] would need its own indirection to hold an interface anyway.
type ledgerCell struct {
	mu sync.RWMutex
	l  negknow.Ledger
}

func (c *ledgerCell) get() negknow.Ledger {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.l
}

func (c *ledgerCell) set(l negknow.Ledger) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.l = l
}

// LedgerHandle answers with the negative-knowledge ledger this daemon has open, or nil when
// nothing has opened one yet. IT OPENS NOTHING: that is the whole difference between it and
// OpenLedger, and it is what keeps negknow.Open's single production call site single and keeps a
// daemon that never compacts from ever creating sketches/tried.bloom. Every cross-goroutine read
// of the handle goes through here.
//
// The Ledger FIELD is still honoured, and read first-come: a caller that supplied its own ledger
// set it before New copied Options, on one goroutine, and the lazy opener adopts rather than
// replaces it (see WireRehydrator). The cell is what the opener publishes into.
func (o *Options) LedgerHandle() negknow.Ledger {
	if l := o.ledger.get(); l != nil {
		return l
	}
	return o.Ledger
}

// publishLedger records the handle the lazy opener produced, and is the ONLY writer. It does not
// also assign Options.Ledger: that field is wiring-time input, written once before any goroutine
// exists, and writing it from a worker goroutine is precisely the race this cell removes.
func (o *Options) publishLedger(l negknow.Ledger) { o.ledger.set(l) }

// ensureLedgerCell creates the cell if an Options built as a literal — every test that does not
// call NewOptions — never got one. It must be called at WIRING time, on the goroutine that owns
// the Options, before anything can read or publish concurrently; WireRehydrator is that point on
// every path, production and test alike, because it is what installs the opener that publishes.
func (o *Options) ensureLedgerCell() {
	if o.ledger == nil {
		o.ledger = &ledgerCell{}
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

// OnStop registers closeFn as the shutdown path for a resource this Options' own wiring opened,
// naming it for the log line a failure produces. Stop runs every registration once, in order,
// after the server has closed -- so no request handler can still be using the resource -- and
// before the lock is released.
//
// It is for resources the DAEMON opened. A handle a composition root opened and assigned onto
// Options belongs to that root and must not be registered here: closing it twice is harmless
// (every Close in this tree is idempotent) but closing it at Stop when its owner expects it to
// outlive the daemon is not.
func (o *Options) OnStop(name string, closeFn func() error) {
	o.ownedResources().add(name, closeFn)
}

// ownedResources returns the shared close list, creating it on first use.
//
// It must be called at WIRING time by anything that will later register a closer, so that the list
// exists before New copies Options and both halves end up holding the same pointer. It is not safe
// for concurrent first use, which is exactly why the lazy openers call it up front rather than
// from inside their own sync.Once.
func (o *Options) ownedResources() *shutdownHooks {
	if o.shutdown == nil {
		o.shutdown = &shutdownHooks{}
	}
	return o.shutdown
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
