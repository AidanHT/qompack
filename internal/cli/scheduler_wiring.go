package cli

import (
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/scheduler"
)

// SP-12's three composition-root blocks, extracted from runDaemon because internal/cli/daemon.go
// is the one file four subplans write into in a fixed order and had already crossed SP-12's
// 150-line house rule (plan V4-SP-12-scheduler-l3, "internal/cli/daemon.go"; ruling R12).
// runDaemon keeps exactly two call lines: wireScheduler after the WireObserver block and before
// daemon.New, registerSchedulerIdle after the RegisterObserverIdleWork block. Nothing here is
// reachable from any hook subcommand path (TestSchedulerNotOnHotPath, budget B-A).

// wireScheduler is Block 1 (construct) and Block 2 (tap). It reads opts.Store/opts.Graph, which
// WireObserver populated, and opts.Ledger/opts.Checkpoints, which stay nil until SP-11/SP-10
// merge — it never opens a second store. Session is deliberately empty: the daemon is per
// project and starts before any session exists; the runtime binds the first id its tap sees.
// Sources stays nil until SP-10 wires it alongside opts.Checkpoints. A construction failure is
// Loud and leaves L3 disabled — the daemon, the store and every hook keep working (00-ARCHITECTURE
// §12.3) and runDaemon never surfaces a non-nil error. The Bind registered here runs AFTER
// SP-08's (Bind hooks run in registration order), so the tap decorates seams SP-08 has set.
func wireScheduler(opts *daemon.Options, getenv func(string) string) (scheduler.Runtime, daemon.SchedulerRuntimeOptions) {
	log := opts.Log
	if log == nil {
		log = logging.Nop()
	}
	schedOpts := daemon.SchedulerRuntimeOptions{
		ProjectRoot: opts.ProjectRoot, Cfg: opts.Cfg,
		Clock: opts.Clock, Log: log, Metrics: opts.Metrics,
		Store: opts.Store, Graph: opts.Graph, Ledger: opts.Ledger,
		Checkpoints: opts.Checkpoints,
		Getenv:      getenv,
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
// succeeds. Commit 6 (C2) fills the body with daemon.RegisterSchedulerIdleWork; until then a
// constructed runtime observes and persists but runs no O3/O5 background work.
func registerSchedulerIdle(d daemon.Daemon, sched scheduler.Runtime, o daemon.SchedulerRuntimeOptions) {
	// filled by commit 6 (C2)
	_, _, _ = d, sched, o
}
