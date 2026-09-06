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
// after registration, it persists the scheduler's state, releases the p-selection gate and
// aborts an open draft once d.Run has returned. Without it a daemon stopped mid-session loses
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
