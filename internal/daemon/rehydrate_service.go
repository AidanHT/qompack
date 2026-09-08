package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/rules"
	"github.com/qompack/qompack/internal/skills"
	"github.com/qompack/qompack/internal/tokens"
)

// RehydrateOptions is the dependency set NewRehydrateService reads. Every member is optional: a
// nil member means "that source is unavailable", which degrades the payload and is reported in the
// drop report rather than failing the hook (Qompack.md §12.3).
type RehydrateOptions struct {
	ProjectRoot string
	Cfg         config.Config
	// Checkpoints is the L4 reader. A nil reader, or one whose Latest reports ErrNotFound or
	// ErrNotImplemented, is the no-checkpoint path: the payload is still built from the L0
	// verbatim capture, the ledger and the skill index, and Result.Degraded is set.
	Checkpoints checkpoint.Reader
	Deps        rehydrate.Deps
	Reporter    rehydrate.Reporter
	// Contract supplies the degradation mode when Mode is nil. Tests construct a Monitor directly;
	// the daemon wiring cannot, because contract.NewMonitor runs inside New — after WireObserver
	// has already had to return — so it uses Mode instead.
	Contract contract.Monitor
	// Mode is the late-bound degradation source, filled from Services.Mode by a Bind body. It wins
	// over Contract when both are set. Both nil means ModeFull.
	Mode func() contract.Mode
	// OpenLedger opens the negative-knowledge ledger the FIRST time a compaction actually needs it,
	// and is consulted only while Deps.Ledger is nil.
	//
	// It is a function rather than a handle because opening the ledger is not free of observable
	// effect: negknow.Open loads or rebuilds sketches/tried.bloom, and §3.3 gives that file to the
	// ledger alone — two inherited e2e cases assert it does not exist after the observer has been
	// wired and run. Opening eagerly at wire time would create it in every daemon that never
	// compacts, and would hold an append handle on records/eliminations.jsonl for the process
	// lifetime, which on Windows is enough to fail an unrelated test's directory cleanup.
	OpenLedger func() negknow.Ledger
	Log        logging.Logger
	// Metrics receives rehydrate.build timings and the tokens/dropped/items gauges.
	Metrics obs.Registry
	// Clock stamps State.Emitted. It is the ONLY wall clock in L5: rehydrate.Build reads none.
	Clock core.Clock
}

// rehydrateService implements observer.Rehydrator: the SessionStart compact and clear branches
// (00-ARCHITECTURE.md §5.21, Qompack.md §7.3, §8.6).
type rehydrateService struct {
	o RehydrateOptions

	// mu guards the lazily opened ledger. OnCompact runs on the daemon's worker pool, so two
	// concurrent compactions must not race to open two handles on one JSONL.
	mu         sync.Mutex
	ledgerOnce bool
	ledger     negknow.Ledger
}

// NewRehydrateService returns the observer.Rehydrator the SessionStart source switch delegates to.
// It is nil-tolerant at every call site and never returns nil.
func NewRehydrateService(o RehydrateOptions) observer.Rehydrator {
	if o.Log == nil {
		o.Log = logging.Nop()
	}
	if o.Clock == nil {
		o.Clock = core.SystemClock()
	}
	return &rehydrateService{o: o}
}

var _ observer.Rehydrator = (*rehydrateService)(nil)

// mode reports the degradation mode in force. When in doubt it reports ModeFull only if no source
// was configured at all; a configured source always wins.
func (s *rehydrateService) mode() contract.Mode {
	switch {
	case s.o.Mode != nil:
		return s.o.Mode()
	case s.o.Contract != nil:
		return s.o.Contract.Mode()
	default:
		return contract.ModeFull
	}
}

// OnCompact handles SessionStart with source "compact".
func (s *rehydrateService) OnCompact(ctx context.Context, e observer.Event) (out observer.Output, err error) {
	// §13 invariant 6: a hook exits 0 however badly it goes. A panic anywhere below — a fake
	// reader, a malformed checkpoint, a nil map in a builder — becomes an empty output and a Loud,
	// never a non-zero exit that blocks the user's session from starting.
	defer func() {
		if v := recover(); v != nil {
			s.o.Log.Loud("rehydrate: panic recovered", "session", string(e.SessionID), "err", fmt.Sprint(v))
			out, err = hookio.Empty(), nil
		}
	}()

	// §12.1 is explicit: in ModeDegradedPassive there is "no additionalContext injection, no
	// customInstructions, no scheduler-initiated checkpoints, no drop report". L0/L1 keep running;
	// everything that ACTS is off. Getting this wrong would make the degradation doctrine a lie.
	if m := s.mode(); m != contract.ModeFull {
		s.o.Log.Debug("rehydrate: not acting", "session", string(e.SessionID), "mode", m.String())
		return hookio.Empty(), nil
	}

	// Qompack.md v1.5 Appendix C's independent injection kill switch (SP-19): recording is
	// untouched — runtime.mode governs that — and nothing is reinjected. Checked before the
	// checkpoint read for the same reason the mode is: work §12.1 forbids is not paid for first.
	if !s.o.Cfg.Runtime.Migration.Reinjection.SessionStartCompact {
		s.o.Log.Info("rehydrate: injection disabled by runtime.migration.reinjection.sessionStartCompact",
			"session", string(e.SessionID))
		return hookio.Empty(), nil
	}

	cp, ref, degraded := s.latest(ctx, e.SessionID)
	if degraded == errFatalCheckpoint {
		return hookio.Empty(), nil
	}

	budget := core.Tokens(s.o.Cfg.Runtime.Rehydrate.MaxTokens)
	req := rehydrate.Request{
		Session:     e.SessionID,
		Source:      e.Source,
		ProjectRoot: s.o.ProjectRoot,
		Budget:      budget,
		Checkpoint:  cp,
		Ref:         ref,
		Cfg:         s.o.Cfg,
	}

	var res rehydrate.Result
	var stats []rehydrate.ItemStat
	var buildErr error
	deps := s.deps()
	s.timed(func() { res, stats, buildErr = rehydrate.BuildWithStats(ctx, req, deps) })
	if buildErr != nil {
		s.o.Log.Loud("rehydrate: build failed", "session", string(e.SessionID), "err", buildErr.Error())
		return hookio.Empty(), nil
	}
	if degraded == errNoCheckpoint {
		res.Degraded = true
	}

	s.record(ctx, e.SessionID, res, stats, budget)
	s.gauges(res)

	if res.Text == "" {
		// An empty additionalContext is noise: the host would inject a blank block and the §12.1
		// probe would still be appended by the session.start route regardless.
		return hookio.Empty(), nil
	}
	return hookio.Output{HookSpecificOutput: &hookio.HSO{
		HookEventName:     "SessionStart",
		AdditionalContext: res.Text,
	}}, nil
}

// deps returns the collaborator set for this build, resolving the lazily opened ledger.
//
// A ledger supplied in Deps wins outright: tests wire a fake, and the daemon path leaves that
// field nil precisely so the open happens here, on the first compaction, rather than at wire time.
// An open failure is Loud and then permanent for the process — retrying per compaction would turn
// one broken ledger into an unbounded log — and item 3 degrades to the checkpoint's own copy.
func (s *rehydrateService) deps() rehydrate.Deps {
	d := s.o.Deps
	if d.Ledger != nil || s.o.OpenLedger == nil {
		return d
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ledgerOnce {
		s.ledgerOnce = true
		s.ledger = s.o.OpenLedger()
	}
	d.Ledger = s.ledger
	return d
}

// OnClear handles SessionStart with source "clear".
//
// It resets the rehydration state file so the next compact injection is a fresh full payload
// rather than one carrying a stale drop report — and it touches NOTHING else. A /clear is not a
// session end: session-scoped eliminations "die with the session" (§8.3) and the session has not
// ended, so the ledger, the store, the DAG, the sketches and the checkpoints are all left alone.
// SP-08 owns whatever session-registry effects a clear has.
func (s *rehydrateService) OnClear(ctx context.Context, e observer.Event) (out observer.Output, err error) {
	defer func() {
		if v := recover(); v != nil {
			s.o.Log.Loud("rehydrate: panic recovered on clear", "session", string(e.SessionID), "err", fmt.Sprint(v))
			out, err = hookio.Empty(), nil
		}
	}()

	if s.o.Reporter != nil {
		if rerr := s.o.Reporter.Reset(ctx, e.SessionID); rerr != nil {
			s.o.Log.Warn("rehydrate: could not reset state on clear",
				"session", string(e.SessionID), "err", rerr.Error())
		}
	}
	s.o.Log.Info("rehydrate: session cleared; rehydration state reset", "session", string(e.SessionID))

	// Returned verbatim by observer.OnSessionStart's source switch, so it must be the empty output
	// and a nil error: a clear injects nothing.
	return hookio.Empty(), nil
}

// The two non-fatal checkpoint outcomes latest distinguishes.
var (
	errNoCheckpoint    = errors.New("rehydrate: no checkpoint")
	errFatalCheckpoint = errors.New("rehydrate: checkpoint unreadable")
)

// latest resolves the checkpoint to rehydrate from.
//
// ErrNotFound and ErrNotImplemented are the SAME case: "there is no checkpoint to read". The
// second matters on this branch specifically — checkpoint.OpenReader returns SP-01's stub until
// SP-10 merges ahead of SP-11 in the wave-3 order, and treating its ErrNotImplemented as fatal
// would make the compact branch emit nothing at all until then, hiding every integration defect
// this slice is supposed to surface. Both produce a zero Checkpoint and Ref{Seq:0}, from which
// items 2, 3, 6b, 7 and 8 still build a useful payload.
func (s *rehydrateService) latest(ctx context.Context, sess core.SessionID) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	if s.o.Checkpoints == nil {
		s.o.Log.Info("rehydrate: no checkpoint reader; building from L0 and the ledger", "session", string(sess))
		return checkpoint.Checkpoint{}, checkpoint.Ref{}, errNoCheckpoint
	}
	cp, ref, err := s.o.Checkpoints.Latest(ctx, sess)
	switch {
	case err == nil:
		return cp, ref, nil
	case errors.Is(err, core.ErrNotFound), errors.Is(err, core.ErrNotImplemented):
		s.o.Log.Info("rehydrate: no checkpoint for session; building from L0 and the ledger",
			"session", string(sess), "err", err.Error())
		return checkpoint.Checkpoint{}, checkpoint.Ref{}, errNoCheckpoint
	default:
		s.o.Log.Loud("rehydrate: checkpoint unreadable", "session", string(sess), "err", err.Error())
		return checkpoint.Checkpoint{}, checkpoint.Ref{}, errFatalCheckpoint
	}
}

// record persists the drop report. A write failure is logged and swallowed: the payload has
// already been built and is worth emitting, and `dropped()` degrades to the previous state file.
func (s *rehydrateService) record(ctx context.Context, sess core.SessionID, res rehydrate.Result, stats []rehydrate.ItemStat, budget core.Tokens) {
	if s.o.Reporter == nil {
		return
	}
	st := rehydrate.State{
		Session: sess,
		Seq:     res.Seq,
		// The only wall-clock read in L5. Build is a pure function of (Request, Deps) precisely so
		// that PropBuild_Deterministic can demand byte-identical output for identical input.
		Emitted:  core.UnixMilli(s.o.Clock.Now().UnixMilli()),
		Tokens:   res.Tokens,
		Budget:   budget,
		Items:    stats,
		Dropped:  res.Dropped,
		Degraded: res.Degraded,
	}
	if err := s.o.Reporter.Record(ctx, sess, st); err != nil {
		s.o.Log.Warn("rehydrate: could not record state", "session", string(sess), "err", err.Error())
	}
}

// timed runs f under the rehydrate.build histogram when metrics are wired.
func (s *rehydrateService) timed(f func()) {
	if s.o.Metrics == nil {
		f()
		return
	}
	// obs.Timed takes a func() error and returns it; this call site has nothing to report, so the
	// error is discarded rather than invented.
	_ = obs.Timed(s.o.Metrics.Hist("rehydrate.build"), func() error {
		f()
		return nil
	})
}

// gauges publishes the three §11.2 observables.
func (s *rehydrateService) gauges(res rehydrate.Result) {
	if s.o.Metrics == nil {
		return
	}
	s.o.Metrics.Gauge("rehydrate.tokens").Set(int64(res.Tokens))
	s.o.Metrics.Gauge("rehydrate.dropped").Set(int64(len(res.Dropped)))
	s.o.Metrics.Gauge("rehydrate.items").Set(int64(len(res.Items)))
}

// BindRehydrate installs svc into the late-bound Services set through the Bind seam SP-05 already
// provides, adding NO member to daemon.Services.
//
// Setting Services.Rehydrate is what makes DeclareProducers declare contract.CAdditionalContext,
// and Services.Rehydrate is referenced nowhere else in this package — it is the seam SP-05
// provisioned for exactly this branch. Until something sets it, §12.1's
// hook.additional_context_delivered assertion reports "not-yet-implemented" forever.
//
// observer.Event and observer.Output are aliases of hookio.Event and hookio.Output, so
// svc.OnCompact satisfies the Services.Rehydrate func type directly.
func BindRehydrate(o *Options, svc observer.Rehydrator) {
	if o == nil || svc == nil {
		return
	}
	o.Bind(func(s *Services) { s.Rehydrate = svc.OnCompact })
}

// WireRehydrator builds the L5 service from a daemon Options and binds it.
//
// This is the wave-3 daemon bootstrap block. It deliberately REUSES o.Store and o.Graph rather
// than opening its own: a second store.Store handle on one project root is a corruption bug, not a
// redundancy. WireObserver opens both lazily just above this call, so by the time this runs they
// exist; anything still nil is passed through as nil and degrades that item.
//
// It adds the two handles nothing else opens yet — the negative-knowledge ledger and the
// checkpoint reader — and leaves each nil on failure, logging Loud. SP-12 reads these same fields
// and adds nothing; SP-13 extends this block with the MCP promoter and op registration.
func WireRehydrator(o *Options) observer.Rehydrator {
	log := o.Log
	if log == nil {
		log = logging.Nop()
	}
	clk := o.Clock
	if clk == nil {
		clk = core.SystemClock()
	}

	// The ledger is opened on the FIRST compaction, not here. See RehydrateOptions.OpenLedger: an
	// eager open creates sketches/tried.bloom in every daemon that never compacts, which §3.3
	// reserves for the ledger itself, and holds a records/eliminations.jsonl handle for the process
	// lifetime. The handle is assigned back onto Options so runDaemon's existing shutdown defer
	// closes it; that field is read again only after Run has returned, with no hook still in
	// flight.
	//
	// It is published on Options.OpenLedger rather than kept local, because the rehydration is no
	// longer the first thing in a compaction that needs a ledger: the PreCompact hook fires BEFORE
	// the SessionStart(source=compact) this service handles, and the checkpoint it seals reads
	// eliminations out of the same ledger. Sharing the accessor is what keeps negknow.Open at ONE
	// call site while letting either half of a compaction be the one that triggers it. The
	// sync.Once is the sharing rule: one open, one Loud on failure, no retry, whichever worker
	// arrives first.
	var (
		ledgerOnce sync.Once
		ledger     negknow.Ledger
	)
	openLedger := func() negknow.Ledger {
		ledgerOnce.Do(func() {
			l, err := negknow.Open(o.ProjectRoot, o.Cfg, nil, negknow.Deps{
				Store: o.Store, Graph: o.Graph, Log: log, Metrics: o.Metrics, Clock: clk,
			})
			if err != nil {
				log.Loud("daemon: negative-knowledge ledger unavailable; eliminations will not be rehydrated",
					"err", err.Error())
				return
			}
			o.Ledger = l
			ledger = l
		})
		return ledger
	}
	// A caller that supplied its own accessor keeps it: tests wire a fake ledger this way, and
	// overwriting it here would open a real one beside it.
	if o.OpenLedger == nil {
		o.OpenLedger = openLedger
	}

	ckpt, err := checkpoint.OpenReader(o.ProjectRoot, log, o.Metrics)
	if err != nil {
		log.Loud("daemon: checkpoint reader unavailable; rehydration will run without a checkpoint",
			"err", err.Error())
		ckpt = nil
	}

	svc := NewRehydrateService(RehydrateOptions{
		ProjectRoot: o.ProjectRoot,
		Cfg:         o.Cfg,
		Checkpoints: ckpt,
		OpenLedger:  o.OpenLedger,
		Deps: rehydrate.Deps{
			Store: o.Store,
			// o.Ledger is nil on the daemon path — nothing opens one before this — so the compact
			// branch routes through OpenLedger above. It is non-nil only when a CALLER supplied a
			// ledger on Options, and s.deps() honours that one outright rather than opening a
			// second handle on the same JSONL.
			Ledger: o.Ledger,
			Graph:  o.Graph,
			Rules:  rules.New(rules.WithLogger(log)),
			Skills: skills.New(skills.WithLogger(log)),
			Tokens: tokens.NewForProject(o.Cfg, tokens.DefaultCalibPath(), o.ProjectRoot),
			Log:    log,
		},
		Reporter: rehydrate.NewReporter(o.ProjectRoot, log),
		Log:      log,
		Metrics:  o.Metrics,
		Clock:    clk,
	})
	BindRehydrate(o, svc)
	return svc
}
