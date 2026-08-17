package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// selfTestSyntheticSessionID is the fixed session id selfTestContractAssertions stamps onto its
// synthetic Env — see that function's own doc comment for why it must be both well-formed and
// unmistakably synthetic.
const selfTestSyntheticSessionID = core.SessionID("qompack-self-test")

// selfTestPingDeadline bounds self-test's own admin.ping round trip: generous, since an operator
// running self-test by hand is not on any hot-path budget, but still bounded so a truly wedged
// daemon cannot hang the command forever.
const selfTestPingDeadline = 5 * time.Second

// selfTestProbeTimeout bounds the plain reachability dial self-test performs before deciding
// whether to attempt EnsureRunning at all.
const selfTestProbeTimeout = 50 * time.Millisecond

// selfTestCheck is one row of `qompack self-test`'s output — the shape task-6-spec.md's JSON mode
// names: {id, ok, severity, expected, observed, detail}.
type selfTestCheck struct {
	ID       string            `json:"id"`
	OK       bool              `json:"ok"`
	Severity contract.Severity `json:"severity"`
	Expected string            `json:"expected"`
	Observed string            `json:"observed"`
	Detail   string            `json:"detail,omitempty"`
}

// selfTestReport is `--json`'s top-level document.
type selfTestReport struct {
	Checks []selfTestCheck `json:"checks"`
	Mode   string          `json:"mode"`
	Exit   int             `json:"exit"`
}

// runSelfTest implements `qompack self-test [--json]` (task-6-spec.md): the ONLY subcommand
// permitted a non-zero exit (§2.3). It runs config load, .qompack/ writability, the append-only
// guard, ipc.Resolve, daemon reachability, an admin.ping round trip, an ops-coverage summary, and
// contract.StandardAssertions against a synthetic Env built from the persisted SessionHistory, in
// that order, then reports a fixed-width table (or, under --json, {checks,mode,exit}). Exit 0 iff no
// check failed at SevCritical.
func runSelfTest(ctx context.Context, env Env, args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("self-test", flag.ContinueOnError)
	fs.SetOutput(errw)
	asJSON := fs.Bool("json", false, "emit {checks,mode,exit} as JSON instead of a table")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root := resolveProjectRoot(env, nil)
	clk := env.Clock
	if clk == nil {
		clk = core.SystemClock()
	}

	// config-corrupt fault site (fix round 1, Minor M-12): inert for every hook subcommand, but
	// self-test DOES call config.Load (selfTestConfigLoad, below) — this is where the site
	// actually engages for real, before that call, exactly as task-6-spec.md's fault table says.
	faultCorruptConfigIfNeeded(root)

	checks, mode := runSelfTestChecks(ctx, root, env, clk)

	critical := false
	for _, c := range checks {
		if !c.OK && c.Severity == contract.SevCritical {
			critical = true
		}
	}
	exit := ExitOK
	if critical {
		exit = ExitError
	}

	if *asJSON {
		report := selfTestReport{Checks: checks, Mode: mode.String(), Exit: exit}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		writeSelfTestTable(out, checks, mode)
	}

	if critical {
		return fmt.Errorf("%w: self-test found a critical failure", errAlreadyReported)
	}
	return nil
}

// runSelfTestChecks runs every check in order and returns them together with the contract mode
// they collectively observed.
func runSelfTestChecks(ctx context.Context, root string, env Env, clk core.Clock) ([]selfTestCheck, contract.Mode) {
	var checks []selfTestCheck

	cfg, cfgCheck := selfTestConfigLoad(env, root)
	checks = append(checks, cfgCheck)
	checks = append(checks, selfTestWritable(root))
	checks = append(checks, selfTestAppendOnlyGuard(root))

	addr, addrCheck := selfTestResolve(root)
	checks = append(checks, addrCheck)

	reachable := selfTestDaemonReachable(root, addr, env.Self, clk)
	checks = append(checks, reachable)

	checks = append(checks, selfTestAdminPing(root, addr, clk))
	checks = append(checks, selfTestOpsCoverage())

	assertionChecks, mode := selfTestContractAssertions(ctx, root, cfg, clk)
	checks = append(checks, assertionChecks...)

	return checks, mode
}

func selfTestConfigLoad(env Env, root string) (config.Config, selfTestCheck) {
	cfg, _, err := LoadConfigAndReport(config.Env{
		ProjectRoot: root, HomeDir: homeDir(env), Getenv: env.Getenv, Flags: env.Set,
	}, logging.Nop(), nil)
	if err != nil {
		return config.Defaults(), selfTestCheck{
			ID: "config.load", Severity: contract.SevCritical,
			Expected: "configuration loads", Observed: "failed", Detail: err.Error(),
		}
	}
	return cfg, selfTestCheck{
		ID: "config.load", OK: true, Severity: contract.SevInfo,
		Expected: "configuration loads", Observed: "loaded",
	}
}

// selfTestWritableProbeName is the throwaway file selfTestWritable creates and removes.
const selfTestWritableProbeName = "self-test-probe.tmp"

func selfTestWritable(root string) selfTestCheck {
	l := paths.Of(root)
	if err := paths.EnsureLayout(l); err != nil {
		return selfTestCheck{
			ID: "qompack.writable", Severity: contract.SevCritical,
			Expected: ".qompack/ is writable", Observed: "layout could not be created", Detail: err.Error(),
		}
	}
	p := l.Tmp + string(os.PathSeparator) + selfTestWritableProbeName
	if err := os.WriteFile(paths.Long(p), []byte("ok"), 0o600); err != nil {
		return selfTestCheck{
			ID: "qompack.writable", Severity: contract.SevCritical,
			Expected: ".qompack/ is writable", Observed: "write failed", Detail: err.Error(),
		}
	}
	_ = os.Remove(paths.Long(p))
	return selfTestCheck{
		ID: "qompack.writable", OK: true, Severity: contract.SevInfo,
		Expected: ".qompack/ is writable", Observed: "writable",
	}
}

// selfTestCheckpointProbeSeq is a reserved, deliberately out-of-range checkpoint sequence number:
// real checkpoints start small and increment by one, so this can never collide with one a real
// session ever wrote. selfTestAppendOnlyGuard uses it to probe the append-only guard without ever
// aiming a destructive syscall at real data (fix round 1, Critical C-2 — the previous version
// probed seq 0, a real sequence number, and left a permanent read-only artifact in checkpoints/,
// the immutable tier §7.4 exists to protect, unlisted in checkpoints/MANIFEST.jsonl).
const selfTestCheckpointProbeSeq = core.CheckpointSeq(999999999)

// selfTestAppendOnlyGuard proves paths.OpenFile's §7.4 refusal actually holds for this project by
// seeding one checkpoint-shaped probe file at selfTestCheckpointProbeSeq and attempting a
// truncating rewrite of it — the same shape testutil.Project.AssertAppendOnly exercises, reduced
// to the one check self-test has room for. The probe file is ALWAYS removed before this function
// returns (clearing paths.CreateNew's own read-only bit first, exactly as
// internal/daemon/spawn.go's removeSpawnLockFile already does), regardless of the guard's outcome
// or of the create itself failing — a diagnostic command must leave the project exactly as it
// found it.
func selfTestAppendOnlyGuard(root string) selfTestCheck {
	l := paths.Of(root)
	p := paths.CheckpointPath(l, selfTestCheckpointProbeSeq)
	cleanup := func() {
		_ = os.Chmod(paths.Long(p), 0o600)
		_ = os.Remove(paths.Long(p))
	}

	if err := paths.CreateNew(p, []byte("{}\n")); err != nil {
		cleanup() // in case CreateNew wrote the file and failed after (e.g. the trailing Sync/Chmod).
		return selfTestCheck{
			ID: "paths.guard", Severity: contract.SevWarn,
			Expected: "the append-only guard refuses a truncating rewrite", Observed: "could not seed a probe file", Detail: err.Error(),
		}
	}
	defer cleanup()

	f, err := paths.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0o600)
	if f != nil {
		_ = f.Close()
	}
	if err == nil {
		return selfTestCheck{
			ID: "paths.guard", Severity: contract.SevCritical,
			Expected: "the append-only guard refuses a truncating rewrite", Observed: "the rewrite was ALLOWED",
		}
	}
	return selfTestCheck{
		ID: "paths.guard", OK: true, Severity: contract.SevInfo,
		Expected: "the append-only guard refuses a truncating rewrite", Observed: "refused",
	}
}

func selfTestResolve(root string) (ipc.Addr, selfTestCheck) {
	addr, err := ipc.Resolve(root)
	if err != nil {
		return ipc.Addr{}, selfTestCheck{
			ID: "ipc.resolve", Severity: contract.SevCritical,
			Expected: "a local transport endpoint resolves", Observed: "failed", Detail: err.Error(),
		}
	}
	return addr, selfTestCheck{
		ID: "ipc.resolve", OK: true, Severity: contract.SevInfo,
		Expected: "a local transport endpoint resolves", Observed: addr.Path,
	}
}

// selfTestDaemonReachable checks for a live daemon first (cheap dial), and — only if none is
// found and this process was handed a Self path at all (cli.Env.Self; see its own doc comment for
// why every Env a test builds leaves it "") — attempts daemon.EnsureRunning, so the check reports
// "spawnable" rather than failing self-test purely because no daemon happened to be running yet.
func selfTestDaemonReachable(root string, addr ipc.Addr, self string, clk core.Clock) selfTestCheck {
	if ipc.Probe(addr, selfTestProbeTimeout) {
		return selfTestCheck{
			ID: "daemon.reachable", OK: true, Severity: contract.SevInfo,
			Expected: "the daemon is reachable or spawnable", Observed: "reachable",
		}
	}
	if self == "" {
		return selfTestCheck{
			ID: "daemon.reachable", Severity: contract.SevWarn,
			Expected: "the daemon is reachable or spawnable", Observed: "not reachable, and this process cannot self-locate to spawn one",
		}
	}
	spawned, err := daemon.EnsureRunning(root, self, newHookLogger(root), clk)
	if err != nil {
		return selfTestCheck{
			ID: "daemon.reachable", Severity: contract.SevWarn,
			Expected: "the daemon is reachable or spawnable", Observed: "spawn attempted but the daemon did not come up in time", Detail: err.Error(),
		}
	}
	observed := "reachable"
	if spawned {
		observed = "was not running; spawned and reachable now"
	}
	return selfTestCheck{
		ID: "daemon.reachable", OK: true, Severity: contract.SevInfo,
		Expected: "the daemon is reachable or spawnable", Observed: observed,
	}
}

func selfTestAdminPing(root string, addr ipc.Addr, clk core.Clock) selfTestCheck {
	sp, _ := ipc.NewSpool(paths.Of(root).Spool)
	c := ipc.NewClientWithOptions(addr, sp, newHookLogger(root), newHookMetrics(clk), ipc.ClientOptions{
		ProjectRoot: root, Self: "", Clock: clk, // Self intentionally empty: self-test never spawns from here, selfTestDaemonReachable already tried.
		// admin.ping is not a hot-path op (§2.4's tight, warm-daemon-tuned State.ConnectDeadlineMs
		// budget is for observe.tool/prompt/stop only) and, like session-start/checkpoint/flush,
		// its own dial can land moments after selfTestDaemonReachable's own daemon.EnsureRunning
		// call — the same race hookConnectDeadlineFloor exists to cover (fix round 1's "New issue
		// found and fixed", fix round 2's Minor N-3). selfTestPingDeadline (5s) already budgets for
		// a slow round trip; only the dial step itself needed widening.
		ConnectDeadline: hookConnectDeadlineFloor,
	})
	defer func() { _ = c.Close() }()

	resp, _ := c.Send(context.Background(), ipc.Request{
		Op: ipc.OpAdminPing, TS: core.NowMilli(clk), Reply: true,
	}, selfTestPingDeadline)
	if !resp.OK {
		return selfTestCheck{
			ID: "admin.ping", Severity: contract.SevWarn,
			Expected: "admin.ping round trips OK:true", Observed: "no OK response (daemon not reachable, or refused)",
		}
	}
	return selfTestCheck{
		ID: "admin.ping", OK: true, Severity: contract.SevInfo,
		Expected: "admin.ping round trips OK:true", Observed: "OK",
	}
}

// selfTestOpsCoverage reports the op surface this build knows about. There is no live "list your
// routes" op (internal/daemon's routing table is process-internal, and every op ipc.KnownOps names
// is unconditionally covered by internal/daemon's own defaultRoutes — see internal/daemon/daemon.go
// buildRoutes/defaultRoutes), so this is an informational count rather than a live round trip
// against a daemon that has no such endpoint to ask.
func selfTestOpsCoverage() selfTestCheck {
	ops := ipc.KnownOps()
	return selfTestCheck{
		ID: "ops.coverage", OK: true, Severity: contract.SevInfo,
		Expected: "every known op has a route", Observed: fmt.Sprintf("%d known ops", len(ops)),
	}
}

// selfTestContractAssertions runs contract.StandardAssertions against a synthetic Env built from
// the project's persisted SessionHistory, mirroring what a live daemon's session.start route does
// (internal/daemon/handlers.go's handleSessionStart) without mutating any of the daemon's own
// on-disk state: this Monitor is throwaway (empty statePath disables persistence) and
// daemon.DeclareProducers is called against a zero Services so the same five producers a wave-1
// daemon always declares are declared here too — otherwise every standard assertion would report
// not-yet-implemented regardless of build health, which is not what "assert every host contract"
// means.
func selfTestContractAssertions(ctx context.Context, root string, cfg config.Config, clk core.Clock) ([]selfTestCheck, contract.Mode) {
	daemon.DeclareProducers(&daemon.Services{})

	mon := contract.NewMonitor(logging.Nop(), nil, "")
	for _, a := range contract.StandardAssertions() {
		_ = mon.Register(a)
	}

	// selfTestSyntheticEvent is a plausible, self-test-only SessionStart payload: self-test is not
	// itself a hook invocation, so there is no real event to check hook.payload_shape against, and
	// every field-presence check in that assertion (and the others that read e.Event) needs SOME
	// well-formed value rather than a zero Event. The session id is a fixed, clearly-synthetic
	// value, distinct from any real session id, so it can never be mistaken by
	// checkSessionStartFires for "this session already ran RunAll once" against a real session's
	// own LastSessionID.
	ev := hookio.Event{HookEventName: "SessionStart", SessionID: selfTestSyntheticSessionID, CWD: root}

	h := contract.LoadHistory(contract.HistoryPath(root))
	results, mode := mon.RunAll(ctx, contract.Env{
		ProjectRoot: root, Event: ev, Cfg: cfg, Log: logging.Nop(), Clock: clk, History: h,
	})

	checks := make([]selfTestCheck, 0, len(results))
	for _, r := range results {
		checks = append(checks, selfTestCheck{
			ID: string(r.ID), OK: r.OK, Severity: r.Severity,
			Expected: r.Expected, Observed: r.Observed, Detail: r.Detail,
		})
	}
	return checks, mode
}

// writeSelfTestTable renders checks as a fixed-width table.
func writeSelfTestTable(out io.Writer, checks []selfTestCheck, mode contract.Mode) {
	fmt.Fprintf(out, "%-32s %-5s %-9s %s\n", "CHECK", "OK", "SEVERITY", "OBSERVED")
	for _, c := range checks {
		ok := "ok"
		if !c.OK {
			ok = "FAIL"
		}
		fmt.Fprintf(out, "%-32s %-5s %-9s %s\n", c.ID, ok, severityLabel(c.Severity), c.Observed)
	}
	fmt.Fprintf(out, "\nmode: %s\n", mode.String())
}

// severityLabel renders contract.Severity the way self-test's table wants it; contract.Severity
// has no exported String method (only an unexported label()), so this is self-test's own copy of
// the same three spellings.
func severityLabel(s contract.Severity) string {
	switch s {
	case contract.SevInfo:
		return "info"
	case contract.SevWarn:
		return "warn"
	case contract.SevCritical:
		return "critical"
	default:
		return "unknown"
	}
}
