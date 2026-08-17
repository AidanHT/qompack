package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// runDaemon implements `qompack daemon [--project <root>] [--foreground]` (task-6-spec.md).
//
// It is not a hook, but it MUST still never surface a non-zero exit: it is the process a hook's
// own lazySpawn (internal/ipc/client.go) or session-start's EnsureRunning launches detached, and
// the spawning client has already exited 0 by the time anything this process does could still
// matter to a user or block a turn. Every error path here is therefore logged Loud (never silent,
// §12) and swallowed into a nil return — Dispatch's own exit-code mapping (non-hook, err == nil ->
// ExitOK) does the rest.
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

	cfg, _, cfgErr := LoadConfigAndReport(config.Env{
		ProjectRoot: root, HomeDir: homeDir(env), Getenv: env.Getenv, Flags: env.Set,
	}, log, reg)
	if cfgErr != nil {
		cfg = config.Defaults()
		log.Loud("daemon: could not load configuration, using defaults", "err", cfgErr.Error())
	}

	opts := daemon.NewOptions(root, cfg)
	opts.Log = log
	opts.Metrics = reg
	opts.Clock = core.SystemClock() // §6.1's own "connection deadlines are always real wall-clock time" rule applies to the daemon's own lifecycle clock too.

	d, err := daemon.New(opts)
	if err != nil {
		log.Loud("daemon: could not construct", "err", err.Error())
		return nil
	}

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
