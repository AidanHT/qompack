package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/symbols"
)

// runDaemon implements `qompack daemon [--project <root>] [--foreground]` (task-6-spec.md).
//
// It is not a hook, but it MUST still never surface a non-zero exit: it is the process a hook's
// own lazySpawn (internal/ipc/client.go) or session-start's EnsureRunning launches detached, and
// the spawning client has already exited 0 by the time anything this process does could still
// matter to a user or block a turn. Every error path here is therefore logged Loud (never silent,
// §12) and swallowed into a nil return — Dispatch's own exit-code mapping (non-hook, err == nil ->
// ExitOK) does the rest.
//
// The one deliberate exception is a project whose runtime.daemon.enabled is false (coordinator
// decision D36(b)): the daemon refuses to run there and exits non-zero, before it creates run/,
// the lock or the socket (refuseDisabledDaemon). No spawner in this build launches it for such a
// project, so the non-zero exit only reaches an operator who started it by hand, or an older
// binary's spawner, which does not wait for the detached child's exit status.
func runDaemon(ctx context.Context, env Env, args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(errw)
	project := fs.String("project", "", "project root (defaults to QOMPACK_PROJECT_ROOT or the process cwd's nearest .git)")
	foreground := fs.Bool("foreground", false, "print a startup/shutdown line to stderr for an operator watching this process directly")
	if err := fs.Parse(args); err != nil {
		return nil // a malformed flag must not turn a lazily-spawned daemon into a non-zero exit.
	}

	root := *project
	if root == "" {
		root = resolveProjectRoot(env, nil)
	}
	if root == "" {
		fmt.Fprintln(errw, "qompack daemon: could not resolve a project root")
		return nil
	}
	// D18: no daemon serves the home directory, however it was named — the override, --project or
	// the working directory. The refusal comes before the writer lease, which would create run/ and
	// daemon.lock inside the user-global layer's directory. daemon.AcquireLock refuses the same root
	// again for any other embedder.
	if refused := refuseHomeRoot(env, root); refused != nil {
		fmt.Fprintf(errw, "qompack daemon: %v\n", refused)
		return nil
	}
	// D36(b): refuse a daemon-disabled project before the writer lease, which creates run/ and
	// daemon.lock, so docs/release.md §4's "no resident process and no lock file" holds whoever
	// starts this process. Dispatch prints the error as one "qompack daemon: ..." line and exits
	// ExitError.
	if refused := refuseDisabledDaemon(env, root); refused != nil {
		return refused
	}

	// Exclude maintenance and competing daemon bootstraps before wiring opens
	// any store handles. Keep the lease through all deferred writer closes.
	leaseCtx, lease, releaseLease, err := acquireWriterLease(ctx, root)
	if err != nil {
		if !errors.Is(err, daemon.ErrLockHeld) {
			fmt.Fprintf(errw, "qompack daemon: writer lease unavailable: %v\n", err)
			return nil
		}
		reportLockHeld(env, root, *foreground, errw)
		return nil
	}
	defer func() { _ = releaseLease() }()
	ctx = leaseCtx
	l := paths.Of(root)
	if err := paths.EnsureLayout(l); err != nil {
		fmt.Fprintf(errw, "qompack daemon: could not create %s: %v\n", l.Dot, err)
		return nil
	}

	log, closer, err := logging.New(l.Logs, logging.Info)
	if err != nil {
		log = logging.Nop()
	} else {
		defer func() { _ = closer.Close() }()
	}

	clk := env.Clock
	if clk == nil {
		clk = core.SystemClock()
	}
	reg := obs.New(clk)
	AttachLoudCounter(reg)

	// config-corrupt fault site (fix round 1, Minor M-12): inert for every hook subcommand
	// (hookclient.go never calls config.Load on the hot path), but `qompack daemon` DOES load
	// config, so this is where the site actually engages for real — the per-leaf fallback +
	// Loud line task-6-spec.md's fault table names for it.
	faultCorruptConfigIfNeeded(root)

	cfgEnv := config.Env{
		ProjectRoot: root, HomeDir: homeDir(env), Getenv: env.Getenv, Flags: env.Set,
	}
	cfg, _, cfgErr := LoadConfigAndReport(cfgEnv, log, reg)
	if cfgErr != nil {
		cfg = config.Defaults()
		log.Loud("daemon: could not load configuration, using defaults", "err", cfgErr.Error())
	}

	opts := daemon.NewOptions(root, cfg)
	// The daemon's config reload loads through the same environment, so it neither drops a --set
	// flag nor reads a different home than this load did.
	opts.CfgEnv = cfgEnv
	opts.Log = log
	opts.Metrics = reg
	opts.Clock = core.SystemClock() // §6.1's own "connection deadlines are always real wall-clock time" rule applies to the daemon's own lifecycle clock too.

	// &opts, never opts: Bind is pointer-receiver over the unexported binds slice, so the value
	// form would compile, append to a copy, and leave all five L0 seams nil with every hook still
	// exiting 0. On error the daemon still starts — degraded, per §12.3's "everything else fails
	// toward do nothing" — with L0 capture disabled and the failure Loud.
	obsv, obsErr := daemon.WireObserver(&opts)
	if obsErr != nil {
		opts.Log.Loud("observer unavailable; L0 capture disabled", "err", obsErr.Error())
	}
	// The checkpoint layer's first phase: assemble the LIVE source supplier and bind the
	// PreCompact seam. It must run after WireObserver (it reads the store and the DAG that call
	// opened) and before daemon.New (Options.Bind is what New applies). It opens NO ledger here:
	// the supplier's accessor (recordedLedger) opens one through the shared opener in a project
	// that already holds elimination records, but only on a resolve after Run is serving, never
	// during wiring — see wireCheckpointSources.
	ckpt := wireCheckpointSources(&opts)
	sched, schedOpts := wireScheduler(&opts, env.Getenv, ckpt.sources)
	// store.Open pre-creates .qompack/tmp/quarantine as scaffolding for its corrupt-object path,
	// but store's own quarantine() MkdirAlls that directory again at use — so the EMPTY directory
	// is redundant from the moment it exists, and it is the one entry that would make the
	// write-set guard's ".qompack/tmp/ is empty once every write has landed" assertion
	// (test/guards IT-4) read scaffolding as staging debris. It is removed HERE, at startup,
	// because the daemon's lock release — the guard's "the daemon has finished" signal — happens
	// inside Run, before any cleanup this function could do afterwards. os.Remove refuses a
	// non-empty directory, so genuine quarantine evidence is never touched.
	if opts.Store != nil {
		_ = os.Remove(paths.Long(filepath.Join(paths.Of(root).Tmp, "quarantine")))
	}
	// The daemon has no services shutdown path (§5.4): the store WireObserver opened is flushed
	// by OnSessionEnd/Persist and lives for the process. Its file handles are still released once
	// Run returns — in production that is the moment before process exit, and in the in-process
	// tests that drive runDaemon directly an unreleased append handle would fail the caller's
	// TempDir cleanup on Windows.
	defer func() {
		if opts.Store != nil {
			if closeErr := opts.Store.Close(); closeErr != nil {
				opts.Log.Warn("daemon: closing the observer's store", "err", closeErr.Error())
			}
		}
		// The ledger the rehydrator opens on its FIRST compaction holds an append handle on
		// records/eliminations.jsonl. Its owner is the DAEMON -- WireRehydrator's opener registers
		// it on Options.OnStop and daemon.Stop closes it, so every embedder of daemon.New gets the
		// release and not just this one composition root. This defer is the backstop for the one
		// path Stop cannot cover: a daemon.New that FAILED, after wiring had already run. Close is
		// idempotent and the hook list empties itself, so on the ordinary path this is a no-op.
		if ledger := opts.LedgerHandle(); ledger != nil {
			if closeErr := ledger.Close(); closeErr != nil {
				opts.Log.Warn("daemon: closing the negative-knowledge ledger", "err", closeErr.Error())
			}
		}
	}()

	installMCPTools(&opts, root, cfg, log, reg, clk)

	d, err := daemon.NewWithLease(opts, lease)
	if err != nil {
		log.Loud("daemon: could not construct", "err", err.Error())
		return nil
	}
	if obsv != nil {
		daemon.RegisterObserverIdleWork(d, obsv)
	}
	// The checkpoint layer's second phase: Idle() is a method on the constructed Daemon, so the
	// three idle registrations — advance_frontier, act.checkpoint_cadence, materialize_pins —
	// can only happen here. Without this call the shipped daemon registers none of them and the
	// frontier advances only in tests.
	registerCheckpointIdle(d, cfg, ckpt, sched)
	registerSchedulerIdle(d, sched, schedOpts)
	defer closeScheduler(sched, schedOpts)

	if *foreground {
		fmt.Fprintf(errw, "qompack daemon: starting for project %s\n", root)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-runCtx.Done():
		}
	}()

	runErr := d.Run(runCtx)
	if *foreground {
		fmt.Fprintln(errw, "qompack daemon: stopped")
	}
	switch {
	case runErr == nil:
		return nil
	case errors.Is(runErr, daemon.ErrLockHeld):
		// Another daemon already owns this project — success, not failure (daemon.Run itself
		// already maps this to a nil return; the check is kept here too, defensively, since the
		// task-6-spec.md wiring table names it as its own exit-0 case).
		return nil
	default:
		log.Loud("daemon: run exited with an error", "err", runErr.Error())
		return nil
	}
}

// reportLockHeld is the one line a daemon that could not take the project's lock leaves before it
// exits 0 (§2.3). It used to leave none (F-UAT03-4): a store copied from another path with its run/
// directory never started a daemon, every spawned daemon exited 0 silently, and nothing said why.
// The line names the holder and, for a lock written for another project path, how the lock rules
// treat it: stale once this store's own heartbeat is older than the staleness window.
//
// It is written to the project's day log, which exists: a held lock means .qompack/run/ does.
// --foreground also prints it, since an operator watching the process asked to see it.
func reportLockHeld(env Env, root string, foreground bool, errw io.Writer) {
	clk := env.Clock
	if clk == nil {
		clk = core.SystemClock()
	}
	h := daemon.DescribeLockHolder(root, clk)
	msg := "daemon: another daemon holds this project's lock; this one exits"
	kv := []any{
		"pid", h.PID, "addr", h.Addr, "root", h.Root, "version", h.Version,
		"heartbeat_age_s", int64(h.HeartbeatAge / time.Second),
	}
	line := fmt.Sprintf("qompack daemon: another daemon holds this project's lock (pid %d, heartbeat %s ago); exiting",
		h.PID, h.HeartbeatAge.Round(time.Second))
	if h.Foreign {
		msg = "daemon: this project's lock was written for another project path (a copied or moved store); " +
			"it is treated as stale once this store's heartbeat is older than the staleness window, and this daemon exits"
		kv = append(kv, "stale_after_s", int64(daemon.StaleAfter()/time.Second))
		line = fmt.Sprintf("qompack daemon: this project's lock was written for another project path (%s, pid %d); "+
			"it is treated as stale once this store's heartbeat is %s old (now %s); exiting",
			lockOrigin(h.Root, h.Addr), h.PID, daemon.StaleAfter(), h.HeartbeatAge.Round(time.Second))
	}
	if log, closer, lerr := logging.New(paths.Of(root).Logs, logging.Info); lerr == nil {
		if h.Foreign {
			log.Warn(msg, kv...)
		} else {
			log.Info(msg, kv...)
		}
		_ = closer.Close()
	}
	if foreground {
		fmt.Fprintln(errw, line)
	}
}

// daemonEnabledKey is the configuration key that switches the resident daemon on and off.
const daemonEnabledKey = "runtime.daemon.enabled"

// refuseDisabledDaemon returns the D36(b) refusal when the merged configuration (user file,
// project file, environment and --set, as config.Load merges them) sets runtime.daemon.enabled to
// false, and nil otherwise. config.Load only reads, so the check creates nothing. A configuration
// that cannot be loaded at all falls back to the defaults, where the daemon is enabled: the same
// fallback runDaemon's own load and self-test apply, and that later load is the one that reports
// the problem.
func refuseDisabledDaemon(env Env, root string) error {
	cfg, prov, _, err := config.Load(config.Env{
		ProjectRoot: root, HomeDir: homeDir(env), Getenv: env.Getenv, Flags: env.Set,
	})
	if err != nil || cfg.Runtime.Daemon.Enabled {
		return nil
	}
	src := prov[daemonEnabledKey]
	return fmt.Errorf("the daemon is disabled for this project: %s is false (%s layer, %s); "+
		"set %s to true to enable it", daemonEnabledKey, src.Origin, src.Location, daemonEnabledKey)
}

// installMCPTools registers the L6 retrieval tools on the daemon's op table (SP-13).
//
// It runs BEFORE daemon.New, and that ordering is the whole point rather than a style preference:
// New applies every Bind and only then calls DeclareProducers, so an InstallMCPOp that ran
// afterwards would bind Services.MCPInitialized too late for CMCPRegistered to be declared, and
// the mcp.server_registered assertion would report "not-yet-implemented" for ever while a working
// server answered tools/call beside it.
//
// It also reuses opts.Store, which WireObserver has already opened. A second store.Open in this
// process would be a corruption bug, not a redundancy: the store is single-writer by design and
// two handles over one append log race each other's offsets.
//
// Every failure degrades rather than aborts. A daemon that cannot offer retrieval still observes
// tool use, still checkpoints and still answers `status`; one that refused to start would take the
// whole session down for a feature the model can work without.
func installMCPTools(opts *daemon.Options, root string, cfg config.Config,
	log logging.Logger, reg obs.Registry, clk core.Clock,
) {
	// The three wave-3 collaborators SP-13 could not build on its own branch — its commits predate
	// SP-10 and SP-11 on develop — assembled at the wave-3 integration (SP-19 M0-00). Each is
	// side-effect-free to construct, which is what makes a second instance beside the ones
	// WireRehydrator holds legitimate where a second store or ledger would not be: OpenReader
	// holds no handle and reads the manifest per call, the drop reporter reads its state file per
	// call, and symbols.New is a stateless empty struct. `why` answers from the sealed
	// checkpoints, `dropped` from the last rehydration's persisted drop report, and spans widen
	// to symbol boundaries.
	ckptReader, ckptErr := checkpoint.OpenReader(root, log, reg)
	if ckptErr != nil {
		log.Loud("mcp: checkpoint reader unavailable; `why` will answer found:false", "err", ckptErr.Error())
		ckptReader = nil
	}
	var dropReporter mcp.DropReporter = rehydrate.NewReporter(root, log)
	syms := symbols.New()

	prom, promErr := mcp.NewPromoter(mcp.PromotionsPath(root), cfg.Retrieval.PromoteAfterExpansions, clk)
	if promErr != nil {
		// Counting expansions is advisory (§8.7): losing the signal costs SP-16 a demand-driven
		// hint at the next checkpoint, and costs this session nothing at all.
		log.Loud("mcp: expansion promotion counting disabled", "err", promErr.Error())
	}

	// There is no ledger HERE on the daemon path: the negative-knowledge ledger is opened lazily,
	// through the one-shot opener WireRehydrator published on Options (see
	// RehydrateOptions.OpenLedger for why an eager open is not an option). openingLedger hands the
	// tools an accessor that calls that opener, so the first already_tried or record_eliminated
	// opens the ledger if no compaction has yet; passing a value here would freeze the nil for the
	// life of the process.
	deps := NewToolDeps(root, cfg, opts.Store, openingLedger(opts), ckptReader, dropReporter, prom, syms, log, reg, clk)
	liveToolConfig(&deps, opts)
	if err := daemon.InstallMCPOp(opts, deps); err != nil {
		log.Loud("mcp: retrieval tools unavailable; the daemon is running without them", "err", err.Error())
	}
}

// lockOrigin names where a foreign lock came from for an operator: the project root it records, or,
// for a lock written before daemon.LockInfo.Root existed, the address it records.
func lockOrigin(root, addr string) string {
	if root != "" {
		return root
	}
	return addr
}
