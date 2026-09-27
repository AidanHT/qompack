package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// runSpawnLockFileName mirrors ipc's own unexported spawnLockName ("spawn.lock"), the file every
// spawner of a project's daemon claims inside <root>/.qompack/run before it launches one
// (ipc.ClaimSpawn). ipc's copy cannot be reached from this package (it is unexported and ipc may not
// import daemon — §3.2), so the literal is respelled here for the one caller that needs it: Run,
// which deletes it once the daemon it names has actually come up.
const runSpawnLockFileName = "spawn.lock"

// removeSpawnLockFile deletes <root>/.qompack/run/spawn.lock, if present (task-5-spec.md
// daemon.go Run step 3: "delete run/spawn.lock after listen"). A spawner's claim is already
// self-clearing via staleness, so this is a courtesy cleanup, not a correctness requirement — a
// missing file is not an error. paths.CreateNew leaves the file read-only
// (0o444/FILE_ATTRIBUTE_READONLY), which blocks deletion on Windows, so the mode is cleared first;
// harmless on POSIX, where permissions never gate an unlink.
func removeSpawnLockFile(root string) {
	p := filepath.Join(paths.Of(root).Run, runSpawnLockFileName)
	_ = os.Chmod(paths.Long(p), 0o600)
	_ = os.Remove(paths.Long(p))
}

// ensureRunningDialTimeout bounds EnsureRunning's own initial liveness dial (task-3-spec.md
// spawn.go): "costs nothing", so it is short.
const ensureRunningDialTimeout = 20 * time.Millisecond

// spawnClaimDialTimeout bounds the dial EnsureRunning makes after it has claimed run/spawn.lock and
// before it spawns (V6 close-out D17a). A free lock does not mean no daemon: the daemon a claim
// announced deletes the lock once it listens (Run), so a caller whose last dial missed that daemon by
// moments can claim the lock it freed. A daemon that has only just listened, or has just accepted
// another dial, may not have its next accept posted yet: on Windows a named pipe with no accept
// pending answers ERROR_PIPE_BUSY, which go-winio retries every 10 ms (ipc's dialBusyRetryQuantum),
// and ensureRunningDialTimeout's 20 ms buys two attempts, which a loaded machine's scheduling can
// outlast. So this dial gets the budget every other dial of a daemon that may be just starting gets:
// 250 ms, the value of internal/cli's hookConnectDeadlineFloor, which a hook gives session-start's
// dial for the same race and which this package cannot import. It costs nothing when no daemon is
// there, because a missing pipe or socket fails a dial at once; only a pipe that exists and never
// accepts, a hung daemon, holds a spawn back this long. Too short, and a daemon that is up but slow
// to accept is missed and a second one spawned, which loses daemon.lock and exits after costing the
// caller a spawn; too long, and a hung daemon delays its replacement by that much.
const spawnClaimDialTimeout = 250 * time.Millisecond

// ensureRunningPollInterval and ensureRunningPollBound bound EnsureRunning's poll: a ticker every
// 25ms for up to 1500ms total (task-3-spec.md spawn.go), counted from the moment the poll begins —
// after this call's own spawn, or once it found another spawner's claim. Both are real wall-clock
// durations, like every other connection deadline in this codebase (ipc.ClientOptions.Clock's own
// doc comment: "never for connection deadlines ... because that is what the OS network stack
// enforces regardless of what a test's injected Clock says") — waiting for a real spawned OS
// process to come up cannot be faked by advancing a test clock. session-start does not use the
// 1500ms bound: it polls until the instant its own hook budget allows (EnsureRunningUntil).
const (
	ensureRunningPollInterval = 25 * time.Millisecond
	ensureRunningPollBound    = 1500 * time.Millisecond
)

// qompackFaultEnv is the fault-injection environment variable a spawned daemon must never
// inherit: a client testing its own fault paths must not accidentally make the daemon it spawns
// fault too.
const qompackFaultEnv = "QOMPACK_FAULT"

// qompackProjectRootEnv is the variable SpawnDetached sets so the spawned process resolves the
// same project root without re-walking for the nearest .git.
const qompackProjectRootEnv = "QOMPACK_PROJECT_ROOT"

// daemonSubcommand and projectFlag are SpawnDetached's fixed argv (task-3-spec.md spawn.go:
// exec.Command(self, "daemon", "--project", projectRoot)).
const (
	daemonSubcommand = "daemon"
	projectFlag      = "--project"
)

// EnsureRunning dials projectRoot's resolved address; on success it returns (false, nil) — a
// daemon is already there. Otherwise it makes sure one is coming, then polls the address for up to
// ensureRunningPollBound, returning on the first successful dial, or with core.ErrNotFound if no
// daemon came up in time. A caller treats the latter as "log, spool, exit 0" — never a hook failure
// (§2.3).
//
// Making sure one is coming follows the spawn-lock rule every spawner of the daemon shares
// (ipc.ClaimSpawn, V6 close-out D17): EnsureRunning spawns only when it claims run/spawn.lock. A
// claim another spawner made within the lock's freshness window — a hook's lazy spawn, the MCP
// server's, another session-start, a Windows spawn still copying its staged binary — means that
// spawner's daemon is on its way, and EnsureRunning waits for it instead of starting a second one.
// Each poll dials first and claims only when the dial fails, and a claim is not licence to spawn on
// its own: the lock is also free once the daemon it announced is listening, because that daemon
// deletes it (Run), so EnsureRunning dials once more (spawnClaimDialTimeout) before it spawns, and
// a daemon that answers keeps it from spawning and gets its claim back. A claim whose spawner died
// holds spawning off until it is older than the freshness window (ipc's spawnLockStaleAfter, 10 s):
// if that happens inside this call's wait, EnsureRunning reclaims it and spawns after all
// (fail-safe), and that daemon gets its own wait, as any daemon this call starts does; otherwise
// the wait ends first, ErrNotFound (session-start spools), and the first spawner after the claim
// has aged out reclaims it. A lock it cannot take for any other reason does not stop it:
// session-start is the designated starter, and spawned before the lock existed. Its own claim makes
// every later spawner wait for its daemon in turn, until that daemon is listening and removes the
// lock.
//
// spawned reports whether THIS call started the daemon. If the spawn itself fails, nothing was
// spawned, so this reports (false, serr) rather than claiming a spawn that never happened, and
// releases its claim so it holds no later spawner off.
//
// The liveness check is ipc.Probe (Ruling #22: a successful dial, not a round trip through
// admin.ping) — deliberately: unlike lock.go's staleness protocol, which can afford to fall
// through to slower POSIX/heartbeat checks on an inconclusive network result, a false "dead" here
// costs a real SpawnDetached plus a full poll on the B-A hot path (budget 15 ms). Requiring
// Response.OK from a specific op would make that false negative depend on how a later op-routing
// table answers a probe op — Probe never does, because it never asks. A daemon accepts dials from
// the moment it listens, while its startup still runs (Run), so a successful dial means "a daemon
// is there and will answer", not "it has finished starting".
//
// clk stamps and ages the spawn claim. The liveness dial itself, like every connection deadline in
// this codebase, always runs against real wall-clock time (ipc.Probe takes a plain time.Duration,
// not a Clock).
func EnsureRunning(projectRoot, self string, log logging.Logger, clk core.Clock) (spawned bool, err error) {
	return ensureRunning(projectRoot, self, log, clk, pollBound{after: ensureRunningPollBound},
		detachedSpawner(log))
}

// EnsureRunningUntil is EnsureRunning bounded by session-start's hook budget (internal/cli
// hookBudget, D17b): its poll runs until until, the pre-send deadline, and never past latest, the
// last instant a reply could still follow. Everything before the poll — the dial, the claim and a
// spawn, which on Windows stages the binary (spawn_stage.go) and can stall in process creation for
// seconds on a loaded machine — is never cut short, and a daemon this call started, or found
// already on its way, late still gets ensureRunningPollBound to come up, the wait EnsureRunning has
// always given it, counted from its spawn or from the moment this call found it on its way, up to
// latest; session-start then waits that much less for the reply. A deadline that has already
// passed therefore still gets one dial and, when this call may spawn, its spawn, so the session
// always gets a daemon started. Zero instants fall back to EnsureRunning's bound.
func EnsureRunningUntil(projectRoot, self string, log logging.Logger, clk core.Clock, until, latest time.Time) (spawned bool, err error) {
	return ensureRunning(projectRoot, self, log, clk,
		pollBound{until: until, after: ensureRunningPollBound, latest: latest}, detachedSpawner(log))
}

// pollBound is when ensureRunning's poll ends, for a poll that begins once this call spawned or
// found another spawn in flight: at until or after the given duration from that moment, whichever
// is later, and never past latest. A zero until or latest bounds nothing. A call that found another
// spawn in flight and later spawns after all (the claim went stale) begins its poll again from its
// own spawn, which can only move the end later.
type pollBound struct {
	until  time.Time
	after  time.Duration
	latest time.Time
}

// deadline is the poll's end for a poll beginning at begun.
func (b pollBound) deadline(begun time.Time) time.Time {
	end := begun.Add(b.after)
	if b.until.After(end) {
		end = b.until
	}
	if !b.latest.IsZero() && end.After(b.latest) {
		end = b.latest
	}
	return end
}

// detachedSpawner is the spawner EnsureRunning uses in production: spawnDetached under the user's
// home, reporting a staging failure to log.
func detachedSpawner(log logging.Logger) func(projectRoot, self string) error {
	return func(root, self string) error { return spawnDetached(root, self, userHomeDir(), log) }
}

// ensureRunning is EnsureRunning with its poll bound and its spawner injected, so the rule it
// follows can be driven without starting a real process.
func ensureRunning(projectRoot, self string, log logging.Logger, clk core.Clock, bound pollBound,
	spawn func(projectRoot, self string) error,
) (spawned bool, err error) {
	if log == nil {
		log = logging.Nop()
	}
	addr, rerr := ipc.Resolve(projectRoot)
	if rerr != nil {
		return false, rerr
	}
	if ipc.Probe(addr, ensureRunningDialTimeout) {
		return false, nil
	}

	var deadline time.Time
	ticker := time.NewTicker(ensureRunningPollInterval)
	defer ticker.Stop()
	for {
		if !spawned {
			lock, claim := ipc.ClaimSpawn(projectRoot, clk)
			switch {
			case claim == ipc.SpawnInFlight:
				// Another spawner's daemon is on its way: the wait for it begins here, the first
				// time this call finds its claim.
				if deadline.IsZero() {
					deadline = bound.deadline(time.Now())
				}
			case ipc.Probe(addr, spawnClaimDialTimeout):
				// A daemon answers after all: the one the lock announced, up since this call's last
				// dial and the reason the lock was free, or one that shorter dial missed while it
				// was busy. Either way it is the daemon this call was making sure of.
				lock.Release()
				return false, nil
			default:
				if serr := spawn(projectRoot, self); serr != nil {
					lock.Release()
					log.Warn("daemon: spawn failed", "err", serr)
					return false, serr
				}
				spawned = true
				// The poll begins here, after this call's own spawn, so that preparing a staged copy
				// or a slow process creation does not eat the wait the new daemon gets — also when
				// this call waited on another spawner's claim first and reclaimed it once stale.
				deadline = bound.deadline(time.Now())
			}
		}
		if !time.Now().Before(deadline) {
			return spawned, core.ErrNotFound
		}
		<-ticker.C
		// Dial before anything else on every poll: a daemon that is up needs no claim, and the
		// lock it announced may already be gone.
		if ipc.Probe(addr, ensureRunningDialTimeout) {
			return spawned, nil
		}
	}
}

// SpawnDetached launches a daemon for projectRoot, fully detached from the calling process: no
// window, its own session/process group (via the platform sysProcAttr), standard streams
// redirected to the null device, and Process.Release()d immediately so the spawning client can
// exit without leaving a zombie behind — the spawning client never waits for the daemon it just
// started.
//
// On Windows, when self lies inside CLAUDE_PLUGIN_ROOT, the program it starts is a verified copy of
// self under the per-user data directory, not self (spawn_stage.go, C1.17): a daemon running from
// the plugin's own binary would keep the plugin directory from being removed or updated for as
// long as it lives.
func SpawnDetached(projectRoot, self string) error {
	return spawnDetached(projectRoot, self, userHomeDir(), nil)
}

// spawnDetached is SpawnDetached with the home directory the binary is staged under, and a logger
// for a staging failure — which is reported and never stops the spawn: the daemon is started from
// self instead, as it was before staging existed, and reports that itself (Run).
func spawnDetached(projectRoot, self, home string, log logging.Logger) error {
	program, stageErr := daemonProgram(self, home, os.Getenv(pluginRootEnv), stagingEnabled)
	if stageErr != nil && log != nil {
		log.Warn("daemon: could not stage the daemon binary; starting it from the plugin binary",
			"err", stageErr)
	}
	cmd := buildSpawnCommand(program, projectRoot, os.Environ())
	cmd.Dir = daemonWorkingDir(self, program)

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("daemon: spawn: open devnull: %w", err)
	}
	defer func() { _ = devNull.Close() }()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, devNull, devNull

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("daemon: spawn: %w", err)
	}
	return cmd.Process.Release()
}

// buildSpawnCommand assembles the *exec.Cmd SpawnDetached starts, apart from its I/O redirection
// (kept in SpawnDetached itself, since that owns the devNull handle's lifetime). environ is
// injected so the env-stripping/adding logic is testable without touching the real process
// environment. The working directory is spawnDetached's to choose (daemonWorkingDir).
func buildSpawnCommand(self, projectRoot string, environ []string) *exec.Cmd {
	cmd := exec.Command(self, daemonSubcommand, projectFlag, projectRoot) //nolint:gosec // G204: self is the plugin's own executable path (os.Executable()), not attacker input
	cmd.Env = buildSpawnEnv(environ, projectRoot)
	cmd.SysProcAttr = sysProcAttr()
	return cmd
}

// buildSpawnEnv is buildSpawnCommand's pure half: environ plus QOMPACK_PROJECT_ROOT=projectRoot,
// with any QOMPACK_FAULT entry stripped. The match is case-insensitive on the key: Windows
// environment variable names are case-insensitive, so a parent-process qompack_fault=... (or any
// other casing) must be stripped exactly like the canonical spelling — the whole point of the
// strip is that fault injection must never leak into a spawned daemon, on any platform.
func buildSpawnEnv(environ []string, projectRoot string) []string {
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if key, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(key, qompackFaultEnv) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, qompackProjectRootEnv+"="+projectRoot)
}
