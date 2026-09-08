package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
)

// commandCallDeadline bounds one status or retrieval round trip to the daemon. It is longer than
// the hot path's because a person is waiting at a terminal, not a model mid-turn.
const commandCallDeadline = 10 * time.Second

// latencyFile is the metrics snapshot obs.Registry.Persist writes, and the status command's
// fallback when no daemon answers.
const latencyFile = "latency.json"

// slashCommandCmds routes the §7.5 command names that internal/commands implements.
//
// It does NOT include `checkpoint`. That name is already a hook entry point — PreCompact, in
// hookCmds — and a hook subcommand always exits 0 and reads a hook event from stdin. Giving the
// name a second, non-hook meaning needs the arch/checkpoint-now-subcommand pre-step, which is
// SP-14's handoff edge H3 and is not this commit's to take. The frontend and its tests exist; it
// reports unavailable until a route is bound, which is the true statement in the meantime.
func slashCommandCmds() []Cmd {
	var out []Cmd
	for _, spec := range commands.Specs() {
		if spec.Subcommand == "checkpoint" {
			continue
		}
		out = append(out, Cmd{
			Name:    spec.Subcommand,
			Summary: spec.Summary,
			Run:     runSlashCommand(spec.Name),
		})
	}
	return out
}

// runSlashCommand adapts one commands.Command onto the cli.Cmd contract.
//
// The error is returned unwrapped so Dispatch's own §2.3 mapping sees it. commands.ExitCode and
// cli's ExitOK/ExitError/ExitUsage agree by construction — TestExitCodes_MatchTheCLITable pins
// the two tables together — so a usage error from a frontend still exits 2 here.
func runSlashCommand(name string) func(context.Context, Env, []string, io.Writer, io.Writer) error {
	return func(ctx context.Context, env Env, args []string, out, errw io.Writer) error {
		deps, closer := buildCommandDeps(ctx, env, errw)
		defer closer()

		for _, c := range commands.All(deps) {
			if c.Name() == name {
				return c.Run(ctx, args, out)
			}
		}
		return fmt.Errorf("qompack %s: no such command in this build", name)
	}
}

// buildCommandDeps assembles the dependency set the frontends draw from.
//
// Every member is optional and a member that cannot be built is left nil rather than failing the
// command: `qompack status` in a directory with no project must still print a report saying so,
// which is precisely the case a hard failure here would replace with an error line.
func buildCommandDeps(ctx context.Context, env Env, errw io.Writer) (commands.Deps, func()) {
	noop := func() {}

	clk := env.Clock
	if clk == nil {
		clk = core.SystemClock()
	}
	deps := commands.Deps{Clock: clk, Cfg: config.Defaults()}

	root := resolveProjectRoot(env, nil)
	if root == "" {
		return deps, noop
	}

	l := paths.Of(root)

	// Every one of these commands only reads, so none of them may bring a project into existence.
	// logging.New creates its directory and pins.OpenWith creates its own, so running
	// `qompack status` in a directory that has never been used with Qompack would otherwise leave
	// .qompack/logs and .qompack/pins behind — a directory the user did not ask for, created by a
	// query. When the layout is absent the log is a no-op and pins stay unbound, which the status
	// report and the pin command each state plainly.
	established := projectEstablished(l)

	log := logging.Nop()
	closeLog := noop
	if established {
		if opened, closer, err := logging.New(l.Logs, logging.Info); err == nil {
			log = opened
			closeLog = func() { _ = closer.Close() }
		}
	}

	reg := obs.New(clk)
	if cfg, _, cfgErr := LoadConfigAndReport(config.Env{
		ProjectRoot: root, HomeDir: homeDir(env), Getenv: env.Getenv, Flags: env.Set,
	}, log, reg); cfgErr == nil {
		deps.Cfg = cfg
	} else {
		fmt.Fprintf(errw, "qompack: using default configuration: %v\n", cfgErr)
	}

	// Pins are read directly: pins.Store is append-only and safe for a second reader, and a
	// command that could not list pins without a running daemon would be useless in exactly the
	// session where a user wants to check what is pinned.
	if established {
		if p, perr := pins.OpenWith(root, log, reg, clk); perr == nil {
			deps.Pins = p
		}
	}

	client := newCommandClient(root, deps.Cfg, env, log, reg, clk)
	deps.MCP = buildCommandMCPProxy(deps.Cfg, client, log)
	deps.Status = commandStatusSources(ctx, root, client)

	// CheckpointNow and EvalArtifacts are deliberately left nil. There is no local-seal route
	// (H3), and no committed convention for where a completed evaluation's artifacts live, so both
	// commands report unavailable rather than this file inventing one.

	return deps, func() {
		_ = client.Close()
		closeLog()
	}
}

// projectEstablished reports whether this directory has been used with Qompack before.
//
// It tests for .qompack itself rather than for any file inside it: the directory is what
// paths.EnsureLayout creates, and its presence is the difference between "this is a Qompack
// project whose store happens to be empty" and "this is somebody's home directory".
func projectEstablished(l paths.Layout) bool {
	fi, err := os.Stat(l.Dot)
	return err == nil && fi.IsDir()
}

// newCommandClient builds the transport the frontends reach the daemon over.
//
// It is the same lazy-spawn seam `qompack mcp` uses, and for the same reason: the daemon owns the
// warm handles and the single-writer discipline, so a short-lived command process asks it rather
// than opening the store a second time.
func newCommandClient(root string, cfg config.Config, env Env,
	log logging.Logger, reg obs.Registry, clk core.Clock,
) ipc.Client {
	addr, _ := ipc.Resolve(root)
	return ipc.NewClientWithOptions(addr, nopSpool{}, log, reg, ipc.ClientOptions{
		ProjectRoot: root,
		State:       ipc.ReadState(root, cfg),
		Self:        env.Self,
		Spawn:       daemon.SpawnDetached,
		Clock:       clk,
	})
}

// buildCommandMCPProxy gives the retrieval frontends a tool set that forwards to the daemon.
//
// It reuses the same forwarding handler `qompack mcp` registers, so recall, why and dropped reach
// SP-13's handlers in the daemon rather than a second copy of them built here. Nothing is spooled:
// a retrieval is request/response or it is nothing.
func buildCommandMCPProxy(cfg config.Config, client ipc.Client, log logging.Logger) mcp.Server {
	srv := mcp.NewServerWithOptions(mcp.ServerOptions{
		Name:    mcp.ServerName,
		Version: core.Version,
		Log:     log,
		MaxLine: cfg.Runtime.HotPath.MaxPayloadBytes,
	})
	if err := mcp.RegisterProxy(srv, forwardMCPCall(client, log)); err != nil {
		log.Warn("commands: could not register the retrieval proxy", "err", err.Error())
		return nil
	}
	return srv
}

// commandStatusSources binds the two places a status observation comes from.
func commandStatusSources(_ context.Context, root string, client ipc.Client) commands.StatusSources {
	return commands.StatusSources{
		Daemon: func(ctx context.Context) (commands.DaemonStatus, time.Time, error) {
			return fetchDaemonStatus(ctx, client)
		},
		Disk: func(context.Context) (obs.Snapshot, error) {
			return readPersistedMetrics(paths.Of(root))
		},
	}
}

// fetchDaemonStatus round-trips ipc.OpStatus and decodes the payload into the frontend's mirror.
//
// The decode goes through daemon.StatusSnapshot and is then copied member by member, rather than
// unmarshalling straight into commands.DaemonStatus. That is what makes the mirror's staleness a
// COMPILE error here as well as a test failure: a member the daemon adds and the mirror lacks
// stops this function building.
func fetchDaemonStatus(ctx context.Context, client ipc.Client) (commands.DaemonStatus, time.Time, error) {
	resp, err := client.Send(ctx, ipc.Request{
		Op: ipc.OpStatus, Reply: true, TS: core.NowMilli(core.SystemClock()),
	}, commandCallDeadline)
	if err != nil {
		return commands.DaemonStatus{}, time.Time{}, fmt.Errorf("status round trip: %w", err)
	}
	if !resp.OK {
		return commands.DaemonStatus{}, time.Time{}, fmt.Errorf("status refused: %s", resp.Err)
	}

	var snap daemon.StatusSnapshot
	if err := json.Unmarshal(resp.Data, &snap); err != nil {
		return commands.DaemonStatus{}, time.Time{}, fmt.Errorf("decoding status: %w", err)
	}

	sessions := make([]json.RawMessage, 0, len(snap.Sessions))
	for _, s := range snap.Sessions {
		b, merr := json.Marshal(s)
		if merr != nil {
			continue
		}
		sessions = append(sessions, b)
	}

	// A live answer describes now: the daemon assembled it during this round trip.
	return commands.DaemonStatus{
		Mode:       snap.Mode,
		Contract:   snap.Contract,
		Hot:        snap.Hot,
		Sessions:   sessions,
		Latency:    snap.Latency,
		Budgets:    snap.Budgets,
		Counters:   snap.Counters,
		SpoolFiles: snap.SpoolFiles,
		LoudTail:   snap.LoudTail,
		Extra:      snap.Extra,
	}, time.Now(), nil
}

// readPersistedMetrics reads the snapshot obs.Registry.Persist last wrote. Its own TS is what
// gives the fallback reading its age.
func readPersistedMetrics(l paths.Layout) (obs.Snapshot, error) {
	p := filepath.Join(l.Metrics, latencyFile)
	b, err := os.ReadFile(p) //nolint:gosec // a path this process derives from the project root
	if err != nil {
		return obs.Snapshot{}, fmt.Errorf("reading %s: %w", p, err)
	}
	var snap obs.Snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return obs.Snapshot{}, fmt.Errorf("parsing %s: %w", p, err)
	}
	return snap, nil
}
