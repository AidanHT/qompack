package cli

import (
	"context"
	"encoding/json"
	"errors"
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

// slashCommandCmds routes every §7.5 command the plugin ships, one ordinary subcommand each.
//
// `checkpoint` is not among them. That name is the PreCompact hook entry point, in hookCmds, and
// the plugin ships no /qompack:checkpoint: a slash command routed to a hook would read no event,
// exit 0 and write nothing (internal/pluginmanifest's commandSpecs; TestSlashCommands_AreDiscoverable
// refuses a shipped command whose route is a hook).
func slashCommandCmds() []Cmd {
	var out []Cmd
	for _, spec := range commands.Specs() {
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
	// The eval command reads a completed evaluation's artifacts from disk — the newest
	// `devtool live-eval` run under dist/live-eval and the replay report at
	// testdata/bench-replay.json, or whatever --corpus names — and never runs a harness. It needs
	// no project layout, so it is bound before the early return below.
	deps.EvalArtifacts = commands.FileEvalArtifacts(root)
	if root == "" {
		return deps, noop
	}
	// D18: the home directory is not a project. Nothing below may open a log, a pin store or a
	// client for it: its .qompack is the user-global layer, and projectEstablished would mistake that
	// directory for an established store and let a query write into it.
	if refused := refuseHomeRoot(env, root); refused != nil {
		deps.Refused = refused
		deps.Status = commands.StatusSources{Refused: refused}
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
	deps.Status = commandStatusSources(ctx, root, deps.Cfg, client)

	return deps, func() {
		_ = client.Close()
		closeLog()
	}
}

// projectEstablished reports whether this directory has been used with Qompack before.
//
// It tests for .qompack itself rather than for any file inside it: the directory is what
// paths.EnsureLayout creates, and its presence is the difference between "this is a Qompack
// project whose store happens to be empty" and "this is a directory nobody used Qompack in". It
// cannot tell a project from the home directory, whose .qompack is the user-global layer: callers
// refuse that root first (refuseHomeRoot, D18).
func projectEstablished(l paths.Layout) bool {
	fi, err := os.Stat(l.Dot)
	return err == nil && fi.IsDir()
}

// spawnDaemon is the spawner the lazily spawning clients of `qompack mcp` and the command frontends
// hand ipc: daemon.SpawnDetached, a variable only so a test can observe whether a spawn was reached
// without starting a process.
var spawnDaemon = daemon.SpawnDetached

// daemonClientState is the State a lazily spawning client of `qompack mcp` or the command frontends
// is built with: state.bin as ipc.ReadState reads it, with DaemonEnabled also requiring the loaded
// configuration's runtime.daemon.enabled, exactly as the hook path does (doHook, FR-6).
//
// state.bin records the DaemonEnabled of the daemon that wrote it, and a daemon that died without a
// clean stop leaves it behind. Trusting it alone let a record written while the daemon was enabled
// spawn a daemon for a project whose configuration has since disabled it, which docs/release.md §4
// promises never happens ("no resident process and no lock file"). A client whose State says the
// daemon is disabled never dials and never spawns (ipc.Client.Send, step 2).
func daemonClientState(root string, cfg config.Config) ipc.State {
	st := ipc.ReadState(root, cfg)
	st.DaemonEnabled = st.DaemonEnabled && cfg.Runtime.Daemon.Enabled
	return st
}

// commandConnectDeadline bounds a command client's dial to the daemon: status, doctor and every
// other `qompack` frontend that asks the daemon (D60(e)). It is deliberately not
// runtime.daemon.connectDeadlineMs. That budget is the hot path's, tuned for a hook and a warm
// daemon (25 ms on Windows, about three named-pipe attempts), and one dial landing while the
// listener re-arms its next pipe instance (ERROR_PIPE_BUSY) can miss it. A command is a person at a
// terminal, not a hook mid-turn. It is the same non-hot-path dial floor session-start, checkpoint,
// flush and self-test's admin.ping already use (hookConnectDeadlineFloor, whose derivation is in
// hookclient.go), not a new number. An absent daemon still fails the dial at once: a missing pipe or
// socket is refused, not waited on.
//
// `qompack mcp` does not use it (D61(e)). It is a long-lived server, not a one-shot command: its
// client keeps runtime.daemon.connectDeadlineMs and rides out a miss with its own retry loop,
// mcpRetryAttempts tries mcpRetryDelay apart (cmd_mcp.go), which a person at a terminal would not
// wait through for one status read.
const commandConnectDeadline = hookConnectDeadlineFloor

// newCommandIPCClient is the constructor newCommandClient builds its transport with. It is a
// variable only so a test can see the options a command client is built with and stand in a
// transport whose connect misses or succeeds late, without timing a real dial.
var newCommandIPCClient = ipc.NewClientWithOptions

// newCommandClient builds the transport the frontends reach the daemon over.
//
// It is the same lazy-spawn seam `qompack mcp` uses, and for the same reason: the daemon owns the
// warm handles and the single-writer discipline, so a short-lived command process asks it rather
// than opening the store a second time.
func newCommandClient(root string, cfg config.Config, env Env,
	log logging.Logger, reg obs.Registry, clk core.Clock,
) ipc.Client {
	addr, _ := ipc.Resolve(root)
	return newCommandIPCClient(addr, nopSpool{}, log, reg, ipc.ClientOptions{
		ProjectRoot: root,
		State:       daemonClientState(root, cfg),
		Self:        env.Self,
		Spawn:       spawnDaemon,
		Clock:       clk,

		ConnectDeadline: commandConnectDeadline,
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

// commandStatusSources binds the two places a status observation comes from. cfg is the
// configuration client was built with (newCommandClient), so the daemon source knows, as the client
// does, whether runtime.daemon.enabled lets it dial at all (daemonClientState).
func commandStatusSources(
	_ context.Context, root string, cfg config.Config, client ipc.Client,
) commands.StatusSources {
	enabled := daemonClientState(root, cfg).DaemonEnabled
	return commands.StatusSources{
		Daemon: func(ctx context.Context) (commands.DaemonStatus, time.Time, error) {
			return fetchDaemonStatus(ctx, client, enabled, daemonListening(root))
		},
		Disk: func(context.Context) (obs.Snapshot, error) {
			return readPersistedMetrics(paths.Of(root))
		},
	}
}

// statusNoDaemonReason is why status has no live answer when no daemon received its request.
const statusNoDaemonReason = "no daemon answered: none is listening for this project yet. This command " +
	"asked one to start unless runtime.daemon.enabled is false; run status again once it is up"

// statusSilentDaemonReason is why status has no live answer when a daemon was listening and accepted
// the request but its reply never came: the send took commandCallDeadline or longer, so the
// command client's read deadline passed. A send that failed sooner is statusConnectMissReason.
var statusSilentDaemonReason = fmt.Sprintf("a daemon is listening for this project but did not answer "+
	"within %s: it may be busy or stuck. Run status again; if it stays silent, see "+
	"docs/troubleshooting.md, section 7", commandCallDeadline)

// statusConnectMissReason is why status has no live answer when a daemon was listening but both of
// fetchDaemonStatus's attempts failed before commandCallDeadline: the client could not connect
// within commandConnectDeadline, or the connection closed before a reply. The client reports both
// the same way (OK false, no text), so the reason names both, and never the call deadline, which
// did not expire.
var statusConnectMissReason = fmt.Sprintf("a daemon is listening for this project but did not "+
	"answer this command: on both of two attempts, no connection to it was made within the %s "+
	"connect budget or the connection closed before a reply. Run status again; if it keeps "+
	"failing, see docs/troubleshooting.md, section 7", commandConnectDeadline)

// statusDaemonDisabledReason is why status has no live answer when runtime.daemon.enabled is false
// for this project, in its loaded configuration or in the state.bin its daemon last wrote. The
// command client then never dials (ipc.Client.Send, step 2): it answers OK false with no text at
// once, which is neither a connect miss nor an absent daemon, so status says why no daemon was asked.
// The text names both places because daemonClientState ANDs them: a daemon that reloaded the key to
// false rewrote state.bin, and if it then died without a clean stop, state.bin still says false
// after the configuration is set back to true.
const statusDaemonDisabledReason = "runtime.daemon.enabled is false for this project (in its " +
	"configuration, or in the state.bin its daemon last wrote), so this command does not ask a " +
	"daemon, even one that is still running"

// statusProbeTimeout bounds the dial daemonListening makes. It is the command client's own connect
// budget, not a new number. fetchDaemonStatus resends a fast failure only when this probe saw a
// listener, so a probe that gives up sooner than the send's dial would makes a live daemon read as
// absent ("none is listening") and its read is never resent. A go-winio listener between instances
// keeps its pipe name, so the probe meets ERROR_PIPE_BUSY there and needs the same budget to outlast
// it. An absent daemon still fails the probe at once: a missing pipe or socket is refused.
const statusProbeTimeout = commandConnectDeadline

// statusProbeDial is the dial daemonListening makes. It is a variable only so a test can stand in a
// listener that accepts late without racing a real one against the probe's budget.
var statusProbeDial = ipc.Probe

// statusSendClock times fetchDaemonStatus's sends. It is a variable only so a test can decide how
// long a send took, instead of depending on how fast a real one returns.
var statusSendClock = core.SystemClock()

// daemonListening reports whether anything accepts a connection at root's daemon address (ipc.Probe).
func daemonListening(root string) func() bool {
	return func() bool {
		addr, err := ipc.Resolve(root)
		return err == nil && statusProbeDial(addr, statusProbeTimeout)
	}
}

// fetchDaemonStatus round-trips ipc.OpStatus and decodes the payload into the frontend's mirror.
//
// The decode goes through daemon.StatusSnapshot and is then copied member by member, rather than
// unmarshalling straight into commands.DaemonStatus. That is what makes the mirror's staleness a
// COMPILE error here as well as a test failure: a member the daemon adds and the mirror lacks
// stops this function building.
//
// enabled is the client's own DaemonEnabled (daemonClientState). A client built with it false never
// dials (ipc.Client.Send, step 2), so its OK false says nothing about a listener: status sends once,
// neither probes nor resends, and names the disabled daemon (statusDaemonDisabledReason).
//
// listening is asked before the request is sent, because the client answers OK false with no error
// text both when nothing listened and when a listening daemon never replied: only the dial tells the
// two apart, and asking it after the send would see the daemon the send's own lazy spawn started.
//
// With a daemon listening, the time the send took tells the rest apart, because the client's read
// deadline starts only after its connect, which commandConnectDeadline bounds well inside
// commandCallDeadline: a send that took commandCallDeadline or longer expired that deadline; one
// that failed sooner missed its connect or lost its connection before a reply. Only the second is
// sent once more. A status read changes nothing, so resending it is safe, and a connect miss is not
// always one the dial budget could absorb (go-winio's dial returns any CreateFile error but
// ERROR_PIPE_BUSY at once). A daemon that let the call deadline expire is not asked twice.
func fetchDaemonStatus(
	ctx context.Context, client ipc.Client, enabled bool, listening func() bool,
) (commands.DaemonStatus, time.Time, error) {
	wasListening := enabled && listening != nil && listening()
	send := func() (ipc.Response, time.Duration, error) {
		start := statusSendClock.Now()
		resp, err := client.Send(ctx, ipc.Request{
			Op: ipc.OpStatus, Reply: true, TS: core.NowMilli(core.SystemClock()),
		}, commandCallDeadline)
		return resp, statusSendClock.Since(start), err
	}
	noAnswer := func(resp ipc.Response) bool { return !resp.OK && resp.Err == "" }

	resp, took, err := send()
	if err == nil && noAnswer(resp) && wasListening && took < commandCallDeadline && ctx.Err() == nil {
		resp, took, err = send()
	}
	if err != nil {
		return commands.DaemonStatus{}, time.Time{}, fmt.Errorf("status round trip: %w", err)
	}
	if noAnswer(resp) {
		// The client's answer for a request no daemon answered: nothing listened at the project's
		// address, runtime.daemon.enabled is false, a listening daemon's connect missed, or its
		// reply never came. It carries no text of its own, and quoting it as a refusal printed
		// "status refused: " with nothing after the colon (V6 close-out F-UAT03-3, F-C49-1).
		switch {
		case !enabled:
			return commands.DaemonStatus{}, time.Time{}, errors.New(statusDaemonDisabledReason)
		case !wasListening:
			return commands.DaemonStatus{}, time.Time{}, errors.New(statusNoDaemonReason)
		case took >= commandCallDeadline:
			return commands.DaemonStatus{}, time.Time{}, errors.New(statusSilentDaemonReason)
		default:
			return commands.DaemonStatus{}, time.Time{}, errors.New(statusConnectMissReason)
		}
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
//
// It reads from this CLI process while the daemon replaces the file with paths.WriteAtomic, so the
// read goes through paths.ReadFileShared: on Windows an ordinary handle would fail that replace and
// would itself be refused while one is finishing (test/guards' sharedReaders).
func readPersistedMetrics(l paths.Layout) (obs.Snapshot, error) {
	p := filepath.Join(l.Metrics, latencyFile)
	b, err := paths.ReadFileShared(p)
	if err != nil {
		return obs.Snapshot{}, fmt.Errorf("reading %s: %w", p, err)
	}
	var snap obs.Snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return obs.Snapshot{}, fmt.Errorf("parsing %s: %w", p, err)
	}
	return snap, nil
}
