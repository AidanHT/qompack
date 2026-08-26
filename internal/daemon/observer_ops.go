package daemon

import (
	"context"
	"fmt"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/tokens"
)

// The one file SP-08 adds outside its own package: it attaches the L0 observer to SP-05's
// late-bound Services seams without editing daemon internals and without touching the op-routing
// table, exactly as §5.4's extension-seam note prescribes and as SP-12 will later do with
// scheduler_runtime.go.
//
// SP-08 registers no ops here, and that is not a style preference. SP-05's five default routes
// are where the durability machinery lives (WAL-before-ACK, the session registry, the
// terminal-hook marker, the hot-path breach submode); overriding one would delete those and could
// not put them back, because ingest is an unexported field nothing outside SP-05's files can
// reach. Bind is the sanctioned seam. Panic recovery stays where SP-05 put it, on the route side.
//
// Service lifetime. The daemon has NO services shutdown path: Stop closes its own ingest, server
// and lock, and never a Store handed in through Options. The store WireObserver opens is flushed
// by OnSessionEnd and by the idle Persist task, and lives for the process; runDaemon
// (internal/cli/daemon.go) releases its file handles after Run returns, which in production is
// the moment before process exit.

// observerPersistTask and observerPersistPrio are the idle registration RegisterObserverIdleWork
// makes: priority 50 is mid-band — below SP-12's frontier advancement, above GC.
const (
	observerPersistTask = "observer.persist"
	observerPersistPrio = 50
)

// WireObserver constructs the L0 observer over the daemon's live services and binds it to the
// five L0 function seams. Called from runDaemon on the ADDRESSABLE Options, BEFORE New: New
// applies o.binds inside itself, so a Bind registered afterwards never runs — and a value-form
// call would append to a copy and leave every seam nil with every hook still exiting 0.
//
// runDaemon sets only Log/Metrics/Clock, so the store, the DAG and the sketch set are opened HERE
// when their fields are nil, and assigned back onto *Options so New copies the SAME instances
// into Services — the observer and every route must share one store, one graph, one sketch set.
func WireObserver(o *Options) (observer.Observer, error) {
	log := o.Log
	if log == nil {
		log = logging.Nop()
	}
	clk := o.Clock
	if clk == nil {
		clk = core.SystemClock()
	}

	if o.Store == nil {
		st, err := store.Open(o.ProjectRoot, o.Cfg, store.Deps{Log: log, Metrics: o.Metrics, Clock: clk})
		if err != nil {
			return nil, fmt.Errorf("daemon: wire observer: open store: %w", err)
		}
		o.Store = st
	}
	if o.Graph == nil {
		g, err := dag.Open(o.ProjectRoot, o.Cfg, log)
		if err != nil {
			return nil, fmt.Errorf("daemon: wire observer: open dag: %w", err)
		}
		o.Graph = g
	}
	if o.Sketches == nil {
		o.Sketches = NewSketchSet(o.Cfg)
		o.Sketches.Load(o.ProjectRoot, log)
	}

	// modeSrc is filled by the bind body below and read per event: contract.NewMonitor runs
	// inside New, into an unexported field on neither Options nor the Daemon interface, so
	// WireObserver — which must return before New is called at all — provably cannot reach a
	// monitor. Services.Mode (§5.4) closes the gap in the OPPOSITE direction to the five seams
	// this file binds: SP-05 provides it, SP-08 consumes it. The nil guard is not decoration —
	// modeSrc is nil in any test that builds the observer without ever calling New, and falling
	// through to ModePassive is the safe default (§12.1: when in doubt, do not act).
	var modeSrc func() contract.Mode
	sched := o.Sched // nil through wave 2; captured so the callbacks below need no *Options

	obsv, err := observer.New(observer.Options{
		ProjectRoot: o.ProjectRoot,
		Cfg:         o.Cfg,
		Store:       o.Store,
		Graph:       o.Graph,
		Grammar:     o.Grammar,
		// The raw sketch pointers, NOT SketchSet.Write: the observer serializes its own feeds and
		// owns the sketch files' SessionEnd write (see internal/observer/session.go) precisely
		// because these hand-overs leave SketchSet.dirty false.
		Touch:   o.Sketches.Touch,
		Explore: o.Sketches.Explore,
		Hot:     o.Sketches.Top,
		Tokens:  tokens.NewForProject(o.Cfg, tokens.DefaultCalibPath(), o.ProjectRoot),
		Symbols: symbolAdapter{ex: symbols.New()},
		Mode: func() observer.Mode {
			if modeSrc == nil {
				return observer.ModePassive
			}
			return mapContractMode(modeSrc())
		},
		OnSignals: func(sess core.SessionID, sig observer.Signals) {
			log.Debug("observer: task-boundary signals", "session", sess,
				"todo", sig.TodoCompleted, "test", sig.TestPassed, "git", sig.GitCommit)
			if sched != nil && (sig.TodoCompleted || sig.TestPassed || sig.GitCommit) {
				// The G1.5 task-boundary delivery: a TodoTransition nudge, not a full sample.
				sched.Observe(context.Background(), scheduler.Features{TodoTransition: 1}, 0)
			}
		},
		OnFeatures: func(_ core.SessionID, fs observer.FeatureSample) {
			if sched == nil {
				return
			}
			sched.Observe(context.Background(), scheduler.Features{
				PathJaccard: fs.PathJaccard, ToolShift: fs.ToolShift,
				LexicalCohesion: fs.LexicalCohesion, GapSeconds: fs.GapSeconds,
				TodoTransition: fs.TodoTransition,
			}, fs.Turn)
		},
		Log:     log,
		Metrics: o.Metrics,
		Clock:   clk,
	})
	if err != nil {
		return nil, fmt.Errorf("daemon: wire observer: %w", err)
	}

	// The single Bind attaching the five seams. ObserveTool, ObserveStop and SessionEnd return
	// only error (their Output is fixed at hookio.Empty() by the observer's own error policy);
	// ObservePrompt and SessionStart are the two entry points that may emit output, so the first
	// is assigned directly and the second is wrapped only to add SP-09's staleness refresh.
	o.Bind(func(s *Services) {
		modeSrc = s.Mode // SP-05 assigns svc.Mode before any bind body runs
		s.ObserveTool = func(ctx context.Context, e hookio.Event) error {
			_, err := obsv.OnToolUse(ctx, e)
			return err
		}
		s.ObservePrompt = obsv.OnUserPrompt
		s.ObserveStop = func(ctx context.Context, e hookio.Event, subagent bool) error {
			_, err := obsv.OnStop(ctx, e, subagent)
			return err
		}
		// SessionStart is the one seam that is wrapped rather than assigned, and SP-09 is why:
		// its out-of-scope table hands RefreshStaleness's only wave-2 production caller to SP-08,
		// here — never inside internal/observer, whose import set excludes negknow. The call is
		// nil-tolerant on both Ledger and Store (a stub build has neither), runs only on the
		// startup/resume/"" branches (compact and clear delegate to the rehydrator and are
		// SP-11's), and its error is a Warn, never a returned failure.
		s.SessionStart = func(ctx context.Context, e hookio.Event) (hookio.Output, error) {
			out, err := obsv.OnSessionStart(ctx, e)
			if s.Ledger != nil && s.Store != nil && e.Source != "compact" && e.Source != "clear" {
				if _, rErr := s.Ledger.RefreshStaleness(ctx, s.Store); rErr != nil {
					log.Warn("negknow: staleness refresh failed", "err", rErr.Error())
				}
			}
			return out, err
		}
		s.SessionEnd = func(ctx context.Context, e hookio.Event) error {
			_, err := obsv.OnSessionEnd(ctx, e)
			return err
		}
	})

	return obsv, nil
}

// mapContractMode maps the daemon's §12.1 contract mode onto the observer's two-state Mode: only
// ModeFull may act, and both degraded states fall to ModePassive.
func mapContractMode(m contract.Mode) observer.Mode {
	if m == contract.ModeFull {
		return observer.ModeFull
	}
	return observer.ModePassive
}

// RegisterObserverIdleWork attaches the observer's O3 background work — the Persist that flushes
// the DAG and the state file on idle time. Called from runDaemon immediately AFTER New succeeds:
// the IdleController is reached through Daemon.Idle(), and the daemon does not exist until New
// has returned — the same post-New shape SP-10's WireCheckpoint and SP-12's
// RegisterSchedulerIdleWork use.
//
// The guarded assertion is the single one in the codebase: the value observer.New returns always
// satisfies Persister, but a future alternate Observer implementation must skip the registration
// rather than panic the daemon.
func RegisterObserverIdleWork(d Daemon, obsv observer.Observer) {
	p, ok := obsv.(observer.Persister)
	if !ok {
		return
	}
	d.Idle().Register(observerPersistTask, observerPersistPrio, p.Persist)
}

// symbolAdapter adapts symbols.Extractor to the observer's SymbolLister seam: observer must not
// import symbols (§3.2), so the daemon — a composition root — owns the adapter.
type symbolAdapter struct{ ex symbols.Extractor }

// Names returns the deduplicated symbol names of b in first-appearance order. Sorting is
// deliberately NOT done here: dag.BuildToolUse sorts and deduplicates again, and the observer's
// per-result cap wants the extractor's own order.
func (a symbolAdapter) Names(path string, b []byte) []string {
	syms := a.ex.Extract(path, b)
	if len(syms) == 0 {
		return nil
	}
	out := make([]string, 0, len(syms))
	seen := make(map[string]bool, len(syms))
	for _, s := range syms {
		if s.Name == "" || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		out = append(out, s.Name)
	}
	return out
}
