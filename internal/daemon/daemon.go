package daemon

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// heartbeatInterval is the daemon's own lock-heartbeat cadence (§2.4 lock.go's staleAfter is
// 90s; a 30s heartbeat leaves ample margin).
const heartbeatInterval = 30 * time.Second

// idleTickMax bounds the idle ticker: min(idleTickMax, idleExitSeconds/10), per task-5-spec.md's
// daemon.go Run step 5.
const idleTickMax = 30 * time.Second

// idleRunBudget is the wall-clock budget Run's own idle tick hands to RunOnce, distinct from the
// larger drain-ring-on-stop bound.
const idleRunBudget = 2 * time.Second

// adminIdleBudget is the budget admin.idle hands to RunOnce — generous, since an operator calling
// it explicitly is not on any latency budget.
const adminIdleBudget = 5 * time.Second

// stopDrainBound is Stop's bound on draining the in-flight ring before giving up and shutting
// down anyway.
const stopDrainBound = 5 * time.Second

// stopCleanupBound bounds how long Run waits for an ASYNCHRONOUSLY invoked Stop — admin.shutdown's
// `go func(){ Stop() }()` — to finish its cleanup before returning anyway.
//
// It is expressed as a multiple of stopDrainBound rather than as an independent number because
// Stop's bounded drain is its longest single step; every later step (ingest close, sketch saves,
// metrics persist, state removal, server close, lock release) is either fast or separately bounded
// by ipc's own serverCloseWait. Three times the longest step is therefore a generous ceiling that
// still guarantees Run cannot be wedged forever by a cleanup step that never returns.
const stopCleanupBound = 3 * stopDrainBound

// defaultIdleExitSeconds mirrors config.Defaults().Runtime.Daemon.IdleExitSeconds (1800). Not a
// default source itself — a caller handing New a zero config.Config still gets a sane idle-exit
// window instead of "exit immediately".
const defaultIdleExitSeconds = 1800 //nomagic:allow mirrors config.Defaults(), not a new default (§6.1)

// IdleController schedules the O3/O5 background work that may run only while the session is idle
// (§8.4). Declared in idle.go.

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

// daemon is the real Daemon (task-5-spec.md daemon.go).
type daemon struct {
	root string
	log  logging.Logger
	m    obs.Registry
	clk  core.Clock

	cfgMu        sync.RWMutex
	cfg          config.Config
	cfgEnv       config.Env
	lastCfgMTime time.Time
	lastCfgSize  int64

	svc      *Services
	registry *SessionRegistry
	idle     *idleController
	monitor  contract.Monitor

	ing *ingest

	// drain is written by Run and read by Drain, which callers reach from other goroutines: the
	// admin.drain route, the flush and idle routes, and tests that drive a daemon they started.
	// A plain field made that a data race — the first CI run to actually execute `go test -race`
	// reported it between Run's assignment and Drain's read. The old nil check did not make it
	// safe: under the Go memory model an unsynchronised read concurrent with a write has no
	// guarantee of observing either the old value or the new one, so "nil means not ready yet"
	// was never a promise the race could keep.
	drain atomic.Pointer[drainer]

	breach     *breachDetector
	hotSamples chan time.Duration

	histMu      sync.Mutex
	lastBudgets []obs.BudgetBreach

	// historyMu serializes every access to the SessionHistory value: it carries no lock of its
	// own (contract.SessionHistory's own doc comment), and routes run concurrently.
	historyMu sync.Mutex

	// modeMu guards lastReportedMode, used both for the §12.1 SystemMessage-on-just-degraded
	// check and for the contract_mode_change counter (ruling #26).
	modeMu           sync.Mutex
	lastReportedMode contract.Mode

	// startMu guards the three fields Run publishes while starting up and another goroutine reads:
	// lock, server and addr. The admin.shutdown route answers the client and then runs the whole
	// shutdown on a goroutine of its own (handlers.go handleAdminShutdown, `go func(){ Stop() }()`),
	// so Stop is genuinely concurrent with Run — and Stop's stopOnce orders Stop against Stop only,
	// never against Run. CI run 32391116227 (test (ubuntu-latest), TestAdminShutdownStopsTheDaemon)
	// reported two of these: Run's `d.lock = lock` against Stop's `if d.lock != nil`, and — because
	// an unsynchronised read of a pointer publishes nothing about the value it points at either —
	// AcquireLock's own &Lock{path: ...} (lock.go) against Lock.owned's read of l.path underneath
	// Stop's Release. Reproducing it locally (the test's readiness gate is daemon.lock APPEARING ON
	// DISK, so widening the remainder of AcquireLock makes it fire every run) turned up the third
	// of the set, which CI had not got to naming: `d.server = server` against `if d.server != nil`.
	//
	// The nil checks Stop performed were never the fix, for the reason already written above
	// d.drain when 2a5c31c made that field atomic: under the Go memory model an unsynchronised
	// read concurrent with a write has no guarantee of observing either the old value or the new
	// one, so "nil means Run has not built it yet" was never a promise the race could keep. drain
	// was fixed then; these three were missed, and -race found them the moment a test asked for a
	// shutdown before startup had finished.
	//
	// One mutex rather than three mechanisms: lock is a pointer and would fit atomic.Pointer[Lock]
	// exactly as drain does, but server is an interface and addr is a struct and neither does, and
	// this struct already reaches for a narrow mutex in precisely this situation (cfgMu, histMu,
	// historyMu, modeMu). Foreign readers take the value out under startMu and act on it OUTSIDE:
	// Stop's server.Close() and Lock.Release() both block on I/O and neither may run with this
	// held. Run itself reads neither through the mutex — it keeps the local `server` and `lock`
	// variables it published from, which cannot race by construction.
	//
	// addr has no reader anywhere today (the only occurrence of d.addr in the package is Run's own
	// write), so it is not racing on its own account; it is published through the same mutex as
	// its two siblings so that adding the first reader cannot silently re-open the defect.
	startMu sync.Mutex
	lock    *Lock
	server  ipc.Server
	addr    ipc.Addr

	startTS core.UnixMilli

	routes map[ipc.Op]ipc.Handler

	// runCancel stops the context Run's worker pool, hot-path worker and server.Serve all run
	// under. Stop calls it (if Run ever set it) so a caller that invokes Stop directly — the
	// admin.shutdown route in particular, which runs on a request-handling goroutine entirely
	// separate from Run's own select loop — actually unblocks Run rather than leaving its workers
	// running forever underneath a daemon that believes itself stopped.
	runCancelMu sync.Mutex
	runCancel   context.CancelFunc

	// firstServed closes the first time this daemon dispatches a request that arrived over the
	// transport — the earliest instant it can PROVE server.Serve is accepting. Run watches it to
	// re-drain the spool; see noteServed and redrainOnceServing for why that instant, and not any
	// point inside Run's own startup sequence, is the one that closes V2-MERGE-25's window.
	firstServedOnce sync.Once
	firstServed     chan struct{}

	// runWG tracks the goroutines Run starts DIRECTLY: the hot-path worker and the serving
	// re-drain. Cancelling runCtx tells them to stop; it does not wait for them to have stopped,
	// and both can be inside a paths.WriteAtomic at that moment — hotPathWorker through
	// applyHotPathTransition's ipc.WriteState, redrainOnceServing through drainer.saveState. A
	// staging file under .qompack/tmp/ only survives if the process dies between the os.CreateTemp
	// and the deferred os.Remove, so "the daemon has stopped" has to mean these are joined, not
	// merely signalled. The ingest worker pool has its own join (ing.Wait) and the connection
	// handlers have theirs (ipc.Server.Close); this is the group nothing else covered.
	//
	// Every goRun call sits in Run's startup, ahead of the ipc.NewServer that binds the endpoint,
	// and every way Stop can be reached — admin.shutdown, Run's ctx.Done arm, its idle-exit arm,
	// its serveErrCh arm — is downstream of that endpoint existing or of Run's own select loop. So
	// a goRun can never add to this group after Stop's join has already passed it.
	runWG sync.WaitGroup

	stopOnce sync.Once
	// stopped closes near the START of Stop's cleanup sequence (before the actual work), so Run's
	// own select loop can distinguish an intentional Stop-driven Serve return from a genuine
	// transport failure (see the serveErrCh case below). It is NOT a "Stop has finished" signal —
	// stopDone is.
	stopped chan struct{}
	// stopDone closes only once Stop's ENTIRE cleanup sequence has finished (drain, ingest
	// close, sketches save, metrics persist, state removal, server close, lock release) — the
	// signal a caller that invokes Stop asynchronously (admin.shutdown's `go func(){ Stop() }()`)
	// needs to observe genuine completion. Run returning is not that signal: Run's own select
	// loop returns as soon as runCancel() fires, which happens right after stopped closes, well
	// before the rest of Stop's cleanup has run (shutdown-race fix: a metrics.Persist call still
	// in flight after Run returned raced a test's own TempDir cleanup on .qompack/tmp/).
	stopDone chan struct{}
}

// New constructs a Daemon from o. A bare Options{} literal is safe by construction: every field
// defaults exactly as it would running through NewOptions, so construction never fails and every
// seam is usable before Run is ever called.
func New(o Options) (Daemon, error) {
	if o.Log == nil {
		o.Log = logging.Nop()
	}
	if o.Clock == nil {
		o.Clock = core.SystemClock()
	}
	if o.Metrics == nil {
		o.Metrics = obs.New(o.Clock)
	}
	if o.Sketches == nil {
		o.Sketches = NewSketchSet(o.Cfg)
	}

	svc := &Services{
		Store:       o.Store,
		Ledger:      o.Ledger,
		Sketches:    o.Sketches,
		Graph:       o.Graph,
		Grammar:     o.Grammar,
		Sched:       o.Sched,
		Checkpoints: o.Checkpoints,
	}
	for _, bind := range o.binds {
		bind(svc)
	}
	DeclareProducers(svc)

	statePath := filepath.Join(paths.Of(o.ProjectRoot).State, "contract.json")
	monitor := contract.NewMonitor(o.Log, o.Metrics, statePath)
	for _, a := range contract.StandardAssertions() {
		_ = monitor.Register(a)
	}

	d := &daemon{
		root:        o.ProjectRoot,
		log:         o.Log,
		m:           o.Metrics,
		clk:         o.Clock,
		cfg:         o.Cfg,
		cfgEnv:      config.Env{ProjectRoot: o.ProjectRoot, HomeDir: userHomeDir(), Getenv: os.Getenv},
		svc:         svc,
		monitor:     monitor,
		firstServed: make(chan struct{}),
		stopped:     make(chan struct{}),
		stopDone:    make(chan struct{}),
	}
	d.registry = NewSessionRegistry()
	d.registry.SetLogger(o.Log)
	d.registry.SetMaxSessions(o.Cfg.Runtime.Daemon.MaxSessions)

	d.idle = newIdleController(o.Cfg.Scheduler.Idle.DetectAfterSeconds, o.Clock, o.Log, o.Metrics, monitor.Mode)
	d.idle.Register(idleTaskDrain, idlePrioDrain, d.idleDrain)
	d.idle.Register(idleTaskSketches, idlePrioSketches, d.idleSaveSketches)
	d.idle.Register(idleTaskMetrics, idlePrioMetrics, d.idleWriteMetrics)

	need := o.Cfg.Runtime.HotPath.BreachWindows
	limit := time.Duration(o.Cfg.Runtime.HotPath.BudgetMs) * time.Millisecond
	d.breach = newBreachDetector(limit, need)
	d.hotSamples = make(chan time.Duration, ringCapacity)

	d.ing = newIngest(o.ProjectRoot, o.Cfg, o.Log, o.Metrics, o.Clock)

	d.routes = buildRoutes(&o, d)

	d.startTS = core.NowMilli(o.Clock)
	d.lastReportedMode = monitor.Mode()

	return d, nil
}

// idle task names and priorities (task-5-spec.md idle.go): SP-05 itself registers exactly three,
// none carrying the act. prefix, since all three are recording/maintenance work that must keep
// running in degraded-passive.
const (
	idleTaskDrain    = "drain"
	idleTaskSketches = "sketches"
	idleTaskMetrics  = "metrics"

	idlePrioDrain    = 10
	idlePrioSketches = 20
	idlePrioMetrics  = 30
)

// buildRoutes composes the resolved op table: every op o already registered wins; every op it
// did not register falls back to d's own default handler. o is read via its exported Ops/Handler
// accessors only, so the daemon never reaches into Options' unexported map directly.
func buildRoutes(o *Options, d *daemon) map[ipc.Op]ipc.Handler {
	routes := make(map[ipc.Op]ipc.Handler, len(ipc.KnownOps()))
	for _, op := range o.Ops() {
		if h, ok := o.Handler(op); ok {
			routes[op] = h
		}
	}
	for op, h := range defaultRoutes(d) {
		if _, exists := routes[op]; !exists {
			routes[op] = h
		}
	}
	return routes
}

func defaultRoutes(d *daemon) map[ipc.Op]ipc.Handler {
	return map[ipc.Op]ipc.Handler{
		ipc.OpObserveTool:   d.handleObserveTool,
		ipc.OpObservePrompt: d.handleObservePrompt,
		ipc.OpObserveStop:   d.handleObserveStop,
		ipc.OpSessionStart:  d.handleSessionStart,
		ipc.OpCheckpoint:    d.handleCheckpoint,
		ipc.OpFlush:         d.handleFlush,
		ipc.OpStatus:        d.handleStatus,
		ipc.OpMCP:           d.handleMCP,
		ipc.OpAdminPing:     d.handleAdminPing,
		ipc.OpAdminDrain:    d.handleAdminDrain,
		ipc.OpAdminReload:   d.handleAdminReload,
		ipc.OpAdminIdle:     d.handleAdminIdle,
		ipc.OpAdminShutdown: d.handleAdminShutdown,
	}
}

func (d *daemon) Registry() *SessionRegistry { return d.registry }
func (d *daemon) Idle() IdleController       { return d.idle }

// currentCfg returns the daemon's live configuration, safe for concurrent readers against
// reload.go's writer.
func (d *daemon) currentCfg() config.Config {
	d.cfgMu.RLock()
	defer d.cfgMu.RUnlock()
	return d.cfg
}

// currentState renders the daemon's current mode/hot/deadlines into an ipc.State, for WriteState
// calls from the session.start route, Run's startup and reloadConfig.
func (d *daemon) currentState() ipc.State { return d.stateWithHot(d.registry.HotMode()) }

// stateWithHot is currentState with the hot-path submode supplied explicitly rather than read back
// from the registry. applyHotPathTransition needs exactly this: §12.2's transition must be on disk
// BEFORE the registry publishes it (see persistHotMode), and at that instant the registry still
// reports the OLD submode by construction — reading it back would persist the very value the
// transition is replacing. It also keeps the write off the registry's lock entirely, which is what
// TestHotModeTransitionPersistsBeforeItIsAnnounced turns into a proof of the ordering.
func (d *daemon) stateWithHot(hot ipc.HotPathMode) ipc.State {
	cfg := d.currentCfg()
	return ipc.State{
		Mode:              d.monitor.Mode(),
		Hot:               hot,
		ConnectDeadlineMs: clampU16(cfg.Runtime.Daemon.ConnectDeadlineMs),
		AckDeadlineMs:     clampU16(cfg.Runtime.Daemon.AckDeadlineMs),
		DaemonEnabled:     cfg.Runtime.Daemon.Enabled,
		SpoolOnBreach:     cfg.Runtime.HotPath.SpoolOnBreach,
		MaxPayloadBytes:   clampU32(cfg.Runtime.HotPath.MaxPayloadBytes),
		DaemonPID:         clampU32(os.Getpid()),
		Written:           core.NowMilli(d.clk),
	}
}

func clampU16(v int) uint16 {
	if v < 0 {
		return 0
	}
	if v > math.MaxUint16 {
		return math.MaxUint16
	}
	return uint16(v) //nolint:gosec // bounds-checked above
}

func clampU32(v int) uint32 {
	if v < 0 {
		return 0
	}
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v) //nolint:gosec // bounds-checked above
}

// userHomeDir returns os.UserHomeDir()'s result, or "" on error — reload.go's config.Load call
// treats an empty HomeDir exactly like config.Load's other callers do (no user-global file
// found), never a fatal condition.
func userHomeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// setAddr, setLock and setServer are Run's three publications; currentLock and currentServer are
// how a goroutine that is not Run reads them back. See the comment on startMu for why they exist
// and why neither getter may be called with anything blocking still to do under the mutex.
func (d *daemon) setAddr(a ipc.Addr) {
	d.startMu.Lock()
	d.addr = a
	d.startMu.Unlock()
}

func (d *daemon) setLock(l *Lock) {
	d.startMu.Lock()
	d.lock = l
	d.startMu.Unlock()
}

func (d *daemon) currentLock() *Lock {
	d.startMu.Lock()
	defer d.startMu.Unlock()
	return d.lock
}

func (d *daemon) setServer(s ipc.Server) {
	d.startMu.Lock()
	d.server = s
	d.startMu.Unlock()
}

func (d *daemon) currentServer() ipc.Server {
	d.startMu.Lock()
	defer d.startMu.Unlock()
	return d.server
}

// stopBegun reports whether Stop has ENTERED its cleanup: d.stopped closes as the first act of
// stopOnce's closure, well before any of the work. It is deliberately not "Stop has finished" —
// that is stopDone — because the only thing Run needs to know mid-startup is that the daemon it is
// still assembling has already been told to stop.
func (d *daemon) stopBegun() bool {
	select {
	case <-d.stopped:
		return true
	default:
		return false
	}
}

// Run implements the daemon lifecycle of task-5-spec.md's daemon.go section.
func (d *daemon) Run(ctx context.Context) error {
	// The run context is created and PUBLISHED first — ahead of the address resolve, the lock and
	// everything it actually cancels. Stop reads d.runCancel under runCancelMu and calls it only
	// if it is non-nil, so a Stop that reads it while it is still nil cancels nothing at all; Run
	// then reaches the select loop below and waits forever for a shutdown that nobody will ever
	// signal, with stopOnce already spent so no second admin.shutdown can reach the cleanup either.
	// That is not hypothetical. It is the 8.03s "admin.shutdown did not stop the running daemon"
	// failure in CI run 32391116227: TestAdminShutdownStopsTheDaemon gates on daemon.lock
	// APPEARING ON DISK, which is paths.CreateNew inside AcquireLock — one filesystem write and
	// several statements before Run used to reach this publication down at the old position.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.runCancelMu.Lock()
	d.runCancel = cancel
	d.runCancelMu.Unlock()

	addr, err := ipc.Resolve(d.root)
	if err != nil {
		if isAddrTooLong(err) {
			d.log.Loud("daemon: resolved address too long — running spool-only", "root", d.root, "err", err)
			return nil
		}
		return fmt.Errorf("daemon: run: resolve: %w", err)
	}
	d.setAddr(addr)

	lock, err := AcquireLock(d.root, addr, d.clk)
	if err != nil {
		if errors.Is(err, ErrLockHeld) {
			return nil // another daemon owns this project: success, not failure.
		}
		return fmt.Errorf("daemon: run: acquire lock: %w", err)
	}
	d.setLock(lock)

	// Publishing runCancel above lets a concurrent Stop cancel this startup, but a cancellation is
	// a request, not a rollback. A Stop that ran to completion before d.lock existed read nil and
	// released nothing, so a Run that simply carried on from here would hold daemon.lock until the
	// process died — and while it is held no replacement daemon can ever take this project, the
	// same M-6 failure the listen-error path below exists to prevent. Nothing else is published
	// yet: no ingest workers, no runWG member, no drainer, no server, no state.bin. So the whole
	// of the abort is handing the lock back.
	//
	// The check is sound against every interleaving, not merely the likely one. runCancelMu
	// totally orders Run's store of d.runCancel against Stop's read of it. If Stop's read came
	// first it observed nil, which means close(d.stopped) — Stop's preceding statement — is
	// ordered before Run's store and therefore before this line, so stopBegun sees it. If Run's
	// store came first, Stop observed a non-nil cancel and the select loop below unwinds normally.
	if d.stopBegun() {
		if relErr := lock.Release(); relErr != nil {
			d.log.Warn("daemon: run: releasing lock after a shutdown that arrived mid-startup", "err", relErr)
		}
		return nil
	}

	if d.svc.Sketches != nil {
		d.svc.Sketches.Load(d.root, d.log)
	}

	d.ing.Start(runCtx, 0, d.runIngested)
	d.goRun(func() { d.hotPathWorker(runCtx) })

	d.drain.Store(newDrainer(DrainConfig{
		Root:     d.root,
		Log:      d.log,
		Metrics:  d.m,
		Clock:    d.clk,
		Dispatch: d.drainDispatch,
		Seen:     d.ing.seen,
		IsLive:   d.sessionIsLive,
	}))

	// Started here, before ipc.NewServer binds anything, rather than beside the `go server.Serve`
	// it waits on. It costs nothing — the goroutine's first act is to block on d.firstServed, which
	// only dispatchOp can close and only an accepted request can reach — and it buys the ordering
	// runWG's join depends on: every goRun in this function precedes the existence of the endpoint,
	// so no route into Stop can run before this group is fully populated.
	d.goRun(func() { d.redrainOnceServing(runCtx) })

	server, err := ipc.NewServer(addr, d.log, d.m, ipc.MaxLineBytes)
	if err != nil {
		// The deferred cancel() above already stops the ingest workers and the hot-path worker
		// on this return, but nothing else releases the lock this Run call already holds — left
		// unreleased, daemon.lock would keep naming this (now-dead) process's pid, and no
		// replacement daemon could ever take the project while it lives (fix round 1, M-6).
		//
		// Stop never runs on this path (it needs a server), so the goRun join Stop would have done
		// is done here instead, for the same reason: Run returning is the process exiting, and
		// signalling a goroutine is not waiting for it. Neither of the two can be mid-write here —
		// nothing has served, so no hot-path sample and no re-drain exists to write — but the
		// invariant is "every exit from Run joins them", not "every exit that looked risky".
		cancel()
		d.runWG.Wait()
		if relErr := lock.Release(); relErr != nil {
			d.log.Warn("daemon: run: releasing lock after listen failure", "err", relErr)
		}
		return fmt.Errorf("daemon: run: listen: %w", err)
	}
	d.setServer(server)

	if err := ipc.WriteState(d.root, d.currentState()); err != nil {
		d.log.Warn("daemon: failed to write state.bin", "err", err)
	}
	removeSpawnLockFile(d.root)

	if _, err := d.Drain(runCtx); err != nil && !errors.Is(err, context.Canceled) {
		d.log.Warn("daemon: startup drain failed", "err", err)
	}

	hbTicker := time.NewTicker(heartbeatInterval)
	defer hbTicker.Stop()

	idleTick := idleTickMax
	if secs := d.currentCfg().Runtime.Daemon.IdleExitSeconds; secs > 0 {
		if candidate := time.Duration(secs) * time.Second / 10; candidate < idleTick {
			idleTick = candidate
		}
	}
	if idleTick <= 0 {
		idleTick = idleTickMax
	}
	idleTicker := time.NewTicker(idleTick)
	defer idleTicker.Stop()

	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(runCtx, d.dispatchOp) }()

	var zeroLiveSince time.Time
	for {
		select {
		case <-ctx.Done():
			cancel()
			<-serveErrCh
			// Stop, not a bare nil: without it, sketches are never saved, the ingest WAL handles
			// are never flushed/closed, metrics/latency.json is never written, state.bin is left
			// on disk (a client keeps dialling a dead endpoint until the connect itself fails),
			// and daemon.lock stays held (fix round 1, I-7). context.Background(), deliberately —
			// ctx is already Done here, so Stop's own bounded drain would be a no-op if it
			// inherited it.
			return d.Stop(context.Background())
		case err := <-serveErrCh:
			// A Serve return driven by Stop() (admin.shutdown, or the idle-exit path below, both
			// of which close d.stopped before cancelling runCtx) is an intentional, successful
			// shutdown — report nil, not the raw context.Canceled Serve's own contract returns for
			// a cancellation-driven stop. A Serve return with d.stopped still open is a genuine,
			// unexpected transport failure: this arm gets the same Stop the ctx.Done arm above
			// does (fix round 2, FR-4) — without it, daemon.lock stays held until process death,
			// state.bin keeps advertising a dead daemon, and the ingest WAL/sketches/metrics never
			// flush. err is reported verbatim; Stop's own (expected-nil, in this ordinary case)
			// error is deliberately not allowed to shadow the real failure that triggered this arm.
			select {
			case <-d.stopped:
				// d.stopped closes at the START of Stop, before a single cleanup step has run, so
				// returning here would hand control back to internal/cli's runDaemon — and thence
				// to cmd/qompack's os.Exit — while the rest of the cleanup is still in flight on
				// admin.shutdown's own goroutine. awaitStopCleanup is what makes "Run returned"
				// mean "the daemon has finished"; see its doc comment.
				d.awaitStopCleanup()
				return nil
			default:
				_ = d.Stop(context.Background())
				return err
			}
		case <-hbTicker.C:
			// The local, not d.lock: this is Run's own goroutine reading the value Run itself
			// published, which needs no synchronisation and cannot be nil here (every path that
			// reaches the select loop has already returned from AcquireLock successfully).
			if lock != nil {
				_ = lock.Heartbeat()
			}
		case <-idleTicker.C:
			d.idle.Notify(d.registry.LastActivity())
			now := core.NowMilli(d.clk)
			if d.idle.IsIdle(now) {
				_, _ = d.idle.RunOnce(runCtx, idleRunBudget)
			}
			d.maybeReloadConfig(runCtx, config.Env{})

			exitAfter := d.currentCfg().Runtime.Daemon.IdleExitSeconds
			if exitAfter <= 0 {
				exitAfter = defaultIdleExitSeconds
			}
			// A session whose client vanished without SessionEnd would hold Live() above zero
			// forever and make the countdown below unreachable; sweep those out first, with the
			// idle-exit window itself as the silence bound (see SessionRegistry.EndAbandoned).
			d.registry.EndAbandoned(now, time.Duration(exitAfter)*time.Second)

			if d.registry.Live() == 0 {
				if zeroLiveSince.IsZero() {
					zeroLiveSince = d.clk.Now()
				}
				if d.clk.Now().Sub(zeroLiveSince) >= time.Duration(exitAfter)*time.Second {
					cancel()
					<-serveErrCh
					_ = d.Stop(ctx)
					return nil
				}
			} else {
				zeroLiveSince = time.Time{}
			}
		}
	}
}

// noteServed records that this daemon has dispatched a request, releasing redrainOnceServing. It
// is called from dispatchOp, which is the function Run hands to server.Serve — so it fires only
// once a connection has been accepted, a full frame read, and a request decoded.
//
// It costs one already-completed sync.Once check per request (an atomic load) and never touches
// the filesystem, so it is safe to leave on the B-A/B-B path.
func (d *daemon) noteServed() {
	d.firstServedOnce.Do(func() { close(d.firstServed) })
}

// redrainOnceServing replays the spool a second time, once the daemon is provably serving.
//
// Run's own startup Drain runs BEFORE `go server.Serve(...)`, and after it the next drain is an
// idle tick away — idleTickMax, 30s (V2-MERGE-25). Anything a client spools inside that window is
// durable but invisible for up to half a minute, and on a cold start that window is precisely
// where the spawn-causing entry lives. internal/ipc/client.go now spools before it spawns, which
// keeps the ordinary cold start ahead of the startup drain, but that is a property of the CLIENT:
// a second client dialling the not-yet-accepting daemon in the same window still times out and
// spools with nothing left to notice it. This closes that from the daemon's own side, so
// freshness stops depending on client-side ordering at all.
//
// The trigger is the FIRST SERVED REQUEST rather than a second Drain call placed after
// `go server.Serve(...)` in Run, and that choice is the whole of the fix:
//
//   - `go` orders nothing. A Drain written on the line after it can still run before the accept
//     loop has started, which leaves exactly the window it was meant to close, just narrower and
//     by an unbounded amount. A served request is the earliest fact the daemon can observe that
//     PROVES Serve is accepting.
//   - It is the only trigger with a happens-before edge to the thing being drained. A client that
//     failed to reach this daemon appended to the spool before it gave up, and it gave up before
//     any later client could be served — so every spool entry from the cold-start window is
//     already on disk by the time this fires. A timer-based re-drain proves nothing of the kind.
//
// The idle tick remains the backstop for anything spooled later (a NAK-driven hot-spool submode
// client, an ack timeout against a healthy daemon) — that cadence is §2.5a E's known-deferred
// item and is deliberately not changed here.
func (d *daemon) redrainOnceServing(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-d.firstServed:
	}
	if _, err := d.Drain(ctx); err != nil && !errors.Is(err, context.Canceled) {
		d.log.Warn("daemon: re-drain after first served request failed", "err", err)
	}
}

// sessionIsLive reports whether sess is still tracked as live, for drainer's IsLive callback (a
// live session's WAL is offset-marked but kept, never deleted mid-session).
func (d *daemon) sessionIsLive(sess core.SessionID) bool {
	return d.registry.IsLive(sess)
}

// Drain replays every spool-tier file under root's spool directory. It is safe to call before Run
// has fully started a drainer only in the sense that a nil drainer reports (0, nil) — Run always
// constructs one before its own startup Drain call, and admin.drain / the flush and idle routes
// only ever run once Run has.
func (d *daemon) Drain(ctx context.Context) (int, error) {
	dr := d.drain.Load()
	if dr == nil {
		return 0, nil
	}
	return dr.Drain(ctx)
}

// runIngested is the ingest worker pool's dispatch callback: it resolves the event, routes to the
// bound Services function when present, and — for observe.prompt — runs the off-reply-path
// sentinel scan. It is also drainer.Dispatch's underlying function, wrapped as dispatchOp so a
// drained line gets exactly the same handling a live request would.
func (d *daemon) runIngested(ctx context.Context, req ipc.Request) {
	ev := resolveEvent(req)
	switch req.Op {
	case ipc.OpObserveTool:
		if d.svc.ObserveTool != nil {
			if err := d.svc.ObserveTool(ctx, *ev); err != nil {
				d.log.Warn("daemon: ObserveTool failed", "err", err)
			}
		} else if d.m != nil {
			d.m.Counter(counterUnhandledObserveTool).Add(1)
		}
	case ipc.OpObserveStop:
		subagent := decodeSubagent(req.Raw)
		if d.svc.ObserveStop != nil {
			if err := d.svc.ObserveStop(ctx, *ev, subagent); err != nil {
				d.log.Warn("daemon: ObserveStop failed", "err", err)
			}
		} else if d.m != nil {
			d.m.Counter(counterUnhandledObserveStop).Add(1)
		}
	case ipc.OpObservePrompt:
		d.scanSentinelForPrompt(ev)
	}
}

// drainDispatch is the drainer's DrainConfig.Dispatch function — an explicit, non-reentrant
// allow-list per op family, not a blanket call into dispatchOp (fix round 1, Critical C-1).
//
// A hot-path op's line came FROM a durability record (a WAL segment or a client spool file) that
// IS the drain's own source of truth, so replaying it must route straight to the same processing
// a live worker-pool job gets (runIngested) rather than back through dispatchOp's ordinary route
// handlers: dispatchOp's observe.* routes call ingest.Accept, which would re-WAL the line — for a
// WAL-sourced line, that is a self-referential write into the very file being drained.
//
// flush is routed to flushRoute with drain=false: drainer.Drain holds a plain, non-reentrant
// sync.Mutex (drain.go's dr.mu) for the WHOLE replay, so a drained flush line that called back
// into d.Drain — as the ordinary flush route does — would deadlock this goroutine on a lock it is
// already holding, permanently. That deadlock is not a rare interleaving: ipc.client.Send spools
// EVERY op on EVERY connect failure (no hot-path filter), so a SessionEnd hook firing while the
// daemon is down leaves exactly this line for the very next daemon's STARTUP drain — before Serve
// has accepted a single connection — to trip over.
//
// admin.* is skipped entirely: an admin op replayed from a stale spool file has no operator
// waiting on its reply, admin.drain would hit the identical re-entrancy hazard as flush, and
// admin.shutdown's asynchronous Stop() also calls d.Drain — safe today only because it runs on a
// separate goroutine that would simply block behind this one, but skipping it here removes the
// question entirely rather than relying on that indirection.
//
// session.start, checkpoint, status and mcp all reach here only via a stale spooled Reply request
// nobody is still waiting on; none of their handlers ever calls Drain, so routing them through the
// full dispatchOp is safe and preserves their side effects (session bookkeeping, contract
// observations, markers).
func (d *daemon) drainDispatch(ctx context.Context, req ipc.Request) ipc.Response {
	switch {
	case req.Op.HotPath():
		d.runIngested(ctx, req)
		return ipc.Response{OK: true}
	case req.Op == ipc.OpFlush:
		return d.flushRoute(ctx, req, false)
	case strings.HasPrefix(string(req.Op), ipc.OpAdminPrefix):
		return ipc.Response{OK: true}
	default:
		return d.dispatchOp(ctx, req)
	}
}

// Stop shuts the daemon down cleanly and idempotently (sync.Once): stop accepting, drain the ring
// with a bound, close the ingest WAL handles, save sketches, persist metrics, remove state.bin,
// close the server, release the lock.
func (d *daemon) Stop(ctx context.Context) error {
	var stopErr error
	d.stopOnce.Do(func() {
		// Closed last, by the deferred call below, once every cleanup step in this closure has
		// actually run — not to be confused with d.stopped, which closes next, before any of the
		// cleanup itself (shutdown-race fix: a caller that invokes Stop asynchronously, as
		// admin.shutdown does, needs a way to observe genuine completion; Run returning is not
		// that signal).
		defer close(d.stopDone)
		close(d.stopped)

		d.runCancelMu.Lock()
		runCancel := d.runCancel
		d.runCancelMu.Unlock()
		if runCancel != nil {
			runCancel() // unblocks Run's own select loop and stops the worker pool below.
		}

		// Cancelling is a request, not an acknowledgement. Join Run's own goroutines here, before
		// anything below writes, so that no later step of this cleanup — and no caller who waits
		// for this cleanup — can be racing a paths.WriteAtomic that the hot-path worker or the
		// serving re-drain still has open under .qompack/tmp/. See runWG.
		d.runWG.Wait()

		drainCtx, cancel := context.WithTimeout(ctx, stopDrainBound)
		_, _ = d.Drain(drainCtx)
		cancel()

		d.ing.Wait()
		if err := d.ing.Close(); err != nil {
			d.log.Warn("daemon: stop: closing ingest WAL handles", "err", err)
		}

		if d.svc.Sketches != nil {
			d.svc.Sketches.Save(d.root, d.log)
		}

		if d.m != nil {
			if err := d.m.Persist(paths.Of(d.root)); err != nil {
				d.log.Warn("daemon: stop: persisting metrics", "err", err)
			}
		}

		if err := ipc.RemoveState(d.root); err != nil {
			d.log.Warn("daemon: stop: removing state.bin", "err", err)
		}

		// Read out under startMu, acted on outside it. Both fields are Run's, published from a
		// goroutine this one has no ordering with (see startMu), and both calls below block on
		// I/O — Close waits out the in-flight connection handlers, Release does three filesystem
		// syscalls — so holding the mutex across either would put Run's startup behind them for
		// no reason. A nil here is now a real observation, not the coin-flip the plain field's
		// `!= nil` check was: it means Run genuinely had not published yet.
		if srv := d.currentServer(); srv != nil {
			if err := srv.Close(); err != nil {
				stopErr = err
			}
		}

		if lk := d.currentLock(); lk != nil {
			if err := lk.Release(); err != nil && stopErr == nil {
				stopErr = err
			}
		}
	})
	return stopErr
}

// goRun starts one of Run's own goroutines inside runWG, so Stop can join it rather than merely
// cancel it. Every `go` in Run whose body can still touch the filesystem after the run context is
// cancelled belongs here.
//
// server.Serve deliberately does NOT: Run joins it itself, by reading serveErrCh, and its own
// connection handlers are joined by ipc.Server.Close's bounded wait.
func (d *daemon) goRun(fn func()) {
	d.runWG.Add(1)
	go func() {
		defer d.runWG.Done()
		fn()
	}()
}

// awaitStopCleanup blocks until an asynchronously-invoked Stop has finished its ENTIRE cleanup
// sequence, bounded by stopCleanupBound.
//
// It exists because of what "Run returned" means to the only production caller there is. internal/
// cli's runDaemon returns the moment Run does, and cmd/qompack is `os.Exit(cli.Dispatch(...))` —
// so Run's return is the daemon process's death, not a step before it. admin.shutdown deliberately
// answers the client first and stops the daemon on a separate goroutine (handlers.go
// handleAdminShutdown), and the first two things that goroutine does — close(d.stopped) and
// runCancel() — are precisely what unblock Run. Left unwaited, the process therefore exits with the
// drain, the ingest WAL close, the sketch saves, metrics.Persist, the state.bin removal, the server
// close and the lock release all still to run.
//
// Three of those steps write through paths.WriteAtomic, which stages under .qompack/tmp/ as
// "wa-<random>" and unlinks the staging file in a deferred call. os.Exit lands between the
// os.CreateTemp and that defer often enough to matter, and what it leaves is not transient: the
// staging file outlives every process that knew about it. That is the debris test/guards'
// TestV1_WriteSetConfinedAcrossFullHookSequence sees, and it comes with an unreleased daemon.lock
// (no replacement daemon can take the project until it goes stale, 90s) and a state.bin still
// advertising a dead endpoint.
//
// The wait is bounded rather than open-ended so a cleanup step that never returns degrades to the
// old behaviour — an exit with debris — instead of a daemon that will not die, and it says so out
// loud rather than silently (§13 invariant 10).
func (d *daemon) awaitStopCleanup() {
	t := time.NewTimer(stopCleanupBound)
	defer t.Stop()
	select {
	case <-d.stopDone:
	case <-t.C:
		d.log.Loud("daemon: shutdown cleanup did not finish within its bound; exiting with it still in flight",
			"bound", stopCleanupBound.String())
	}
}

// isAddrTooLong reports whether err wraps ipc.ErrAddrTooLong.
func isAddrTooLong(err error) bool {
	return errors.Is(err, ipc.ErrAddrTooLong)
}
