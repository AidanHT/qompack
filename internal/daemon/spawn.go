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

// runSpawnLockFileName mirrors ipc's own unexported spawnLockName ("spawn.lock"), the file a
// client's lazySpawn takes inside <root>/.qompack/run to serialize concurrent detached-daemon
// spawns. ipc's copy cannot be reached from this package (it is unexported and ipc may not import
// daemon — §3.2), so the literal is respelled here for the one caller that needs it: Run, which
// deletes it once the daemon it names has actually come up.
const runSpawnLockFileName = "spawn.lock"

// removeSpawnLockFile deletes <root>/.qompack/run/spawn.lock, if present (task-5-spec.md
// daemon.go Run step 3: "delete run/spawn.lock after listen"). A client's own lazySpawn lock is
// already self-clearing via staleness, so this is a courtesy cleanup, not a correctness
// requirement — a missing file is not an error. paths.CreateNew leaves the file read-only
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

// ensureRunningPollInterval and ensureRunningPollBound bound EnsureRunning's post-spawn poll: a
// ticker every 25ms for up to 1500ms total (task-3-spec.md spawn.go). Both are real wall-clock
// durations, like every other connection deadline in this codebase (ipc.ClientOptions.Clock's own
// doc comment: "never for connection deadlines ... because that is what the OS network stack
// enforces regardless of what a test's injected Clock says") — waiting for a real spawned OS
// process to come up cannot be faked by advancing a test clock.
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
// daemon is already there. Otherwise it spawns one detached via SpawnDetached and polls the
// address for up to ensureRunningPollBound, returning (true, nil) on the first successful dial, or
// (true, core.ErrNotFound) if the daemon never came up. session-start treats the latter as "log,
// spool, exit 0" — never a hook failure (§2.3). If SpawnDetached itself fails, nothing was
// spawned, so this reports (false, serr) rather than claiming a spawn that never happened.
//
// The liveness check is ipc.Probe (Ruling #22: a successful dial, not a round trip through
// admin.ping) — deliberately: unlike lock.go's staleness protocol, which can afford to fall
// through to slower POSIX/heartbeat checks on an inconclusive network result, a false "dead" here
// costs a real SpawnDetached plus a full ensureRunningPollBound stall on the B-A hot path (budget
// 15 ms). Requiring Response.OK from a specific op would make that false negative depend on how a
// later op-routing table answers a probe op — Probe never does, because it never asks.
//
// clk is accepted to match both task-3-spec.md's exact signature and this package's "every
// component that observes time takes a core.Clock" convention, and is kept for a future caller
// that needs it; it is not read here because the liveness dial itself, like every connection
// deadline in this codebase, always runs against real wall-clock time (ipc.Probe takes a plain
// time.Duration, not a Clock).
func EnsureRunning(projectRoot, self string, log logging.Logger, clk core.Clock) (spawned bool, err error) {
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

	if serr := SpawnDetached(projectRoot, self); serr != nil {
		log.Warn("daemon: spawn failed", "err", serr)
		return false, serr
	}

	deadline := time.Now().Add(ensureRunningPollBound)
	ticker := time.NewTicker(ensureRunningPollInterval)
	defer ticker.Stop()
	for {
		if ipc.Probe(addr, ensureRunningDialTimeout) {
			return true, nil
		}
		if !time.Now().Before(deadline) {
			return true, core.ErrNotFound
		}
		<-ticker.C
	}
}

// SpawnDetached launches a daemon for projectRoot, fully detached from the calling process: no
// window, its own session/process group (via the platform sysProcAttr), standard streams
// redirected to the null device, and Process.Release()d immediately so the spawning client can
// exit without leaving a zombie behind — the spawning client never waits for the daemon it just
// started.
func SpawnDetached(projectRoot, self string) error {
	cmd := buildSpawnCommand(self, projectRoot, os.Environ())

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
// environment.
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
