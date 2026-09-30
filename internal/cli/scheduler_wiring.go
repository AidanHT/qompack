package cli

import (
	"fmt"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
)

// SP-12's three composition-root blocks, extracted from runDaemon because internal/cli/daemon.go
// is the one file four subplans write into in a fixed order and had already crossed SP-12's
// 150-line house rule (plan V4-SP-12-scheduler-l3, "internal/cli/daemon.go"; ruling R12).
// runDaemon keeps exactly two call lines: wireScheduler after the WireObserver block and before
// daemon.New, registerSchedulerIdle after the RegisterObserverIdleWork block. Nothing here is
// reachable from any hook subcommand path (TestSchedulerNotOnHotPath, budget B-A).

// wireScheduler is Block 1 (construct) and Block 2 (tap). It reads opts.Store/opts.Graph, which
// WireObserver populated, and opts.Ledger/opts.Checkpoints. Original SP10–13 deliveries are
// integrated; accepted shared ledger/source wiring is still pending M1/M2. It never opens a
// second store. Session is deliberately empty: the daemon is per
// project and starts before any session exists; the runtime binds the first id its tap sees.
// sources is the LIVE checkpoint source supplier wireCheckpointSources built, or nil when the
// checkpoint layer did not construct. It is handed straight to SchedulerRuntimeOptions.Sources,
// which NewSchedulerRuntime pairs with Checkpoints to build the frontier advancer — the pair is
// what makes schedRuntime.advancer non-nil, and a nil supplier is still an explicitly unavailable
// frontier rather than a fabricated one. A construction failure is
// Loud and leaves L3 disabled — the daemon, the store and every hook keep working (00-ARCHITECTURE
// §12.3) and runDaemon never surfaces a non-nil error. The Bind registered here runs AFTER
// SP-08's (Bind hooks run in registration order), so the tap decorates seams SP-08 has set.
func wireScheduler(opts *daemon.Options, getenv func(string) string,
	sources func() (checkpoint.SourceSet, error),
) (scheduler.Runtime, daemon.SchedulerRuntimeOptions) {
	log := opts.Log
	if log == nil {
		log = logging.Nop()
	}
	schedOpts := daemon.SchedulerRuntimeOptions{
		ProjectRoot: opts.ProjectRoot, Cfg: opts.Cfg,
		Clock: opts.Clock, Log: log, Metrics: opts.Metrics,
		Store: opts.Store, Graph: opts.Graph, Ledger: opts.LedgerHandle(),
		// LedgerFn closes over opts — the POINTER runDaemon holds — so it reads the FIELD, not the
		// nil value it holds right now. WireRehydrator opens the negative-knowledge ledger lazily
		// on the first compaction and publishes the handle back onto opts; without this closure
		// the scheduler's rebuild_bloom task captured that nil at registration and was a
		// permanent no-op. It never OPENS a ledger and never owns one: the lifecycle stays where
		// SP-19 M0-02 put it. LedgerHandle, not the raw field: this runs on the idle-task
		// goroutine and the publication happens on a worker goroutine.
		LedgerFn:    func() negknow.Ledger { return opts.LedgerHandle() },
		Checkpoints: opts.Checkpoints,
		// Sources resolves the SourceSet at every advance, for the same reason LedgerFn resolves
		// the ledger: the set is not complete at composition time and must not be frozen here.
		Sources: sources,
		Getenv:  getenv,
	}
	sched, err := daemon.NewSchedulerRuntime(schedOpts)
	if err != nil {
		log.Loud("scheduler runtime unavailable; L3 disabled for this daemon", "err", err.Error())
		return nil, schedOpts
	}
	opts.Sched = sched
	// Block 2 — tap. This is the only path by which L0 events reach L3.
	opts.Bind(func(s *daemon.Services) { daemon.WrapServicesForScheduler(s, sched, schedOpts) })
	return sched, schedOpts
}

// registerSchedulerIdle is Block 3 (idle registration), called immediately after daemon.New
// succeeds: SP-12's six O3/O5 tasks join SP-05's controller behind the Background gate. A nil
// sched means Block 1 already logged why L3 is disabled; a registration failure is Loud and
// leaves a runtime that observes and persists but runs no background work — never an error out
// of runDaemon.
func registerSchedulerIdle(d daemon.Daemon, sched scheduler.Runtime, o daemon.SchedulerRuntimeOptions) {
	if sched == nil {
		return
	}
	if err := daemon.RegisterSchedulerIdleWork(d, sched, o); err != nil {
		wiringLog(o).Loud("scheduler idle work not registered", "err", err.Error())
	}
}

// closeScheduler is the daemon's shutdown path for L3 (ruling R52): deferred in runDaemon right
// after registration, it persists the scheduler's state and releases the p-selection gate once
// d.Run has returned. Draft lifecycle stays with the checkpointer. Without persistence a daemon loses
// everything since the last idle persist. nil-safe; a failure is a Warn, never an exit code.
func closeScheduler(sched scheduler.Runtime, o daemon.SchedulerRuntimeOptions) {
	if sched == nil {
		return
	}
	if err := daemon.CloseSchedulerRuntime(sched); err != nil {
		wiringLog(o).Warn("scheduler runtime close at daemon shutdown failed", "err", err.Error())
	}
}

// wiringLog is the options' logger, or a Nop when none was wired.
func wiringLog(o daemon.SchedulerRuntimeOptions) logging.Logger {
	if o.Log != nil {
		return o.Log
	}
	return logging.Nop()
}

// checkpointWiring is what the composition root keeps between its two checkpoint phases: the
// writer BindCheckpoint published before daemon.New and WireCheckpoint registers idle work over
// after it, and the LIVE source supplier both of them, and the scheduler's frontier advancer,
// resolve through.
//
// A zero value means the checkpoint layer did not construct. Every consumer treats that as
// "frontier advancement is unavailable", never as an error out of runDaemon (§12.3).
type checkpointWiring struct {
	w       *checkpoint.FileWriter
	sources func() (checkpoint.SourceSet, error)
	// set is the supplier's set BEFORE Resolve: it opens no ledger, so it is what the wiring-time
	// snapshots (before Run accepts) are taken from. See wireCheckpointSources' phase-1 note.
	set func() checkpoint.SourceSet
}

// wireCheckpointSources is the checkpoint layer's Block 1: it assembles the SourceSet the L4
// checkpointer and the L3 frontier advancer are both built from, and binds the PreCompact seam.
// It runs AFTER daemon.WireObserver — which opens the store and the DAG and assigns them onto
// Options — and BEFORE daemon.New, because Options.Bind is what New applies.
//
// THE SOURCE SET IS A SUPPLIER, NOT A VALUE, and that is the whole design of this function.
// SourceSet.Ledger is the negative-knowledge ledger, and this process must not open one here:
// WireRehydrator opens it LAZILY on the first compaction (see RehydrateOptions.OpenLedger — an
// eager open creates sketches/tried.bloom and holds an eliminations.jsonl handle in every daemon
// that never compacts, and §3.3 reserves that file for the ledger itself) and assigns the handle
// back onto the SAME *daemon.Options this closure captures. So the closure calls
// opts.LedgerHandle on every call, exactly as LedgerFn does one layer up — the synchronized read
// of a publication that happens on another goroutine. It never owns a ledger and never creates an
// unused one: it opens one, through the daemon's shared opener, only in a project that already
// holds elimination records (recordedLedger, D49). The lifecycle stays where SP-19 M0-02 put it,
// including runDaemon's existing shutdown defer, which is the only thing that closes it.
//
// Until a ledger is open the supplier returns its PARTIAL set together with a reason wrapping
// core.ErrDegraded (checkpoint.ErrNoLedger). materialize_pins, which needs the pin log and nothing
// else, runs off the partial set; so does the frontier, which begins its drafts without negative
// knowledge while the project holds none (D49), and says why it advances nothing otherwise.
//
// The three collaborators that have no instance anywhere else on the daemon path are constructed
// here and are safe to construct here, for the reason installMCPTools states about its own three:
// pins.OpenWith holds no handle open beyond its constructor and replays the pin log (later calls
// fold in only the tail another process appended, such as a `qompack pin` beside this daemon),
// grammar.New is a fresh compressor over no shared state, and tokens.NewForProject reads the
// calibration file per project. None of them is a second handle on a single-writer resource, which
// is what makes a second store.Open or a second negknow.Open illegitimate and these legitimate.
func wireCheckpointSources(opts *daemon.Options) checkpointWiring {
	log := opts.Log
	if log == nil {
		log = logging.Nop()
	}

	w, err := checkpoint.OpenWriter(opts.ProjectRoot, opts.Cfg, log, opts.Metrics, opts.Clock)
	if err != nil {
		log.Loud("checkpoint writer unavailable; no checkpoints will be sealed and the frontier will not advance",
			"err", err.Error())
		return checkpointWiring{}
	}

	pinStore, pinErr := pins.OpenWith(opts.ProjectRoot, log, opts.Metrics, opts.Clock)
	if pinErr != nil {
		// Degrade rather than abort: with no pin store the SourceSet never validates, so the
		// frontier reports unavailable and PreCompact still seals nothing — which is the same
		// answer this daemon gave before the layer was wired at all, minus the silence.
		log.Loud("checkpoint pin store unavailable; the frontier will report unavailable",
			"err", pinErr.Error())
		pinStore = nil
	}
	gram := opts.Grammar
	if gram == nil {
		gram = grammar.New()
	}
	toks := tokens.NewForProject(opts.Cfg, tokens.DefaultCalibPath(), opts.ProjectRoot)

	// set assembles the SourceSet WITHOUT resolving it: it reads the ledger handle but never calls
	// the accessor, so it opens nothing. The wiring-time snapshots below are built from it (see
	// the note at the phase-1 call); sources, the supplier every later consumer resolves through,
	// is set plus Resolve.
	set := func() checkpoint.SourceSet {
		var segs store.SegmentLog
		if opts.Store != nil {
			segs = opts.Store.Segments()
		}
		return checkpoint.SourceSet{
			Store:    opts.Store,
			Segments: segs,
			// Read now — nil until something (a compaction, a ledger tool call, or this set's own
			// accessor on a resolve) has opened it. See the note above.
			Ledger: opts.LedgerHandle(),
			// ... and the ACCESSOR onto that same field, so a set published before the open is a
			// wired seam rather than a rejected one. Without it SourceSet.Validate refused every
			// set this supplier built before the first compaction, SetSources dropped them, and
			// the first PreCompact of the daemon's life sealed nothing at all. It is
			// recordedLedger (D49): it reads the field on every call and opens nothing in a
			// project with no elimination record, while a project that already holds records has
			// the ledger opened through the shared opener, so its frontier reads them.
			LedgerFn: recordedLedger(opts),
			Pins:     pinStore,
			Graph:    opts.Graph,
			Grammar:  gram,
			Tokens:   toks,
		}
	}
	sources := func() (checkpoint.SourceSet, error) {
		src := set()
		// Resolve, not Validate: this supplier answers the question "may a draft be BEGUN from
		// this?", and until something has opened the ledger the honest answer is no. Validate
		// would say yes on the strength of the accessor alone, and every consumer -- frontier
		// advancement, the scheduler's advancer -- would then discover the missing handle one
		// frame deeper, inside Begin, as an idle-task error on every tick instead of the one
		// reported-unavailable line §16 asks for. Resolving costs a field read and opens nothing in
		// a project with no elimination record; in one with records it is the one lazy open
		// (recordedLedger), which is why only consumers that run once the daemon is serving call it.
		resolved, valErr := src.Resolve()
		if valErr != nil {
			// The partial set travels WITH the reason: a consumer that needs only one seam (pins)
			// must not be degraded by a seam it never reads (the ledger). It keeps its live
			// accessor, so a consumer that only needs to PUBLISH it -- SetSources -- still can.
			return src, fmt.Errorf("%w: %w", valErr, core.ErrDegraded)
		}
		// The resolved set, so the ledger this call may just have opened travels with it.
		return resolved, nil
	}

	// Publish the writer on Options so daemon.New copies it onto Services and, with Sources, so
	// NewSchedulerRuntime's adapter clause builds the frontier advancer. Assigned only on success:
	// a nil *FileWriter in this interface-typed field is a NON-nil interface, which would pass
	// every nil check downstream and fault inside Advance instead.
	opts.Checkpoints = w

	// Phase 1, before New. The snapshot handed over here is set(), never sources(): it has no ledger
	// HANDLE until something has opened one, but it carries the accessor, so SetSources accepts it
	// and the writer's cold path is usable from this moment on. It is not RESOLVED here, because
	// resolving calls recordedLedger, and in a project that holds records that is a negknow.Open
	// (log load, reconcile, possibly a tried.bloom rebuild and fsync, refreshAtOpen) on the path
	// that runs before Run accepts -- the window a hook's SessionStart dial waits on, bounded by
	// hookConnectDeadlineFloor. The open happens instead on the first resolve after the daemon is
	// serving: an idle tick's advance_frontier or materialize_pins, a scheduler advance, or a
	// compaction. The SUPPLIER goes over too: the bound PreCompact seam re-resolves through it at
	// every compaction, which is what lets the FIRST compaction of a daemon's life trigger the one
	// lazy ledger open and then seal against it.
	daemon.BindCheckpoint(opts, opts.Cfg, w, set(), daemon.WithSourceSupplier(sources))

	return checkpointWiring{w: w, sources: sources, set: set}
}

// registerCheckpointIdle is the checkpoint layer's Block 2, called immediately after daemon.New:
// the three idle tasks whose NAMES are §12.1's mode gate — advance_frontier and materialize_pins
// keep running while degraded, act.checkpoint_cadence does not.
//
// Until this call existed, WireCheckpoint had no production caller at all and the shipped daemon
// registered none of the three; frontier advancement ran only in test/e2e.
//
// sched is optional and is passed as the local-checkpoint noter when the runtime supports it, so a
// cadence seal is recorded as OURS and never restarts the Young-Daly clock that measures the host.
func registerCheckpointIdle(d daemon.Daemon, cfg config.Config, cw checkpointWiring, sched scheduler.Runtime) {
	if cw.w == nil {
		return
	}
	opts := []daemon.WireOption{daemon.WithSourceSupplier(cw.sources)}
	if noter, ok := sched.(daemon.LocalCheckpointNoter); ok && sched != nil {
		opts = append(opts, daemon.ReportLocalCheckpointsTo(noter))
	}
	// set(), not sources(): this runs before Run accepts, and resolving would open the ledger in a
	// project with records (wireCheckpointSources' phase-1 note). The snapshot is only the frozen
	// fallback WireCheckpoint uses when no supplier is given, and one always is here.
	daemon.WireCheckpoint(d, cfg, cw.w, cw.set(), opts...)
}
