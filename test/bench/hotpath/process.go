package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
)

// exeSuffix returns ".exe" on Windows, matching tools/devtool/util.go's own helper — this package
// cannot import that one (it is package main of a different program), so it carries its own copy.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// findModuleRoot returns the nearest ancestor of the working directory holding a go.mod — the
// same walk test/e2e/harness.go's own moduleRoot performs, needed here for the identical reason:
// `go build ./cmd/qompack` is only meaningful from the module root, and this program's own
// working directory is wherever `go run ./test/bench/hotpath` (or devtool) happened to invoke it
// from.
func findModuleRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("hotpath: getwd: %w", err)
	}
	for d := wd; ; {
		if fi, statErr := os.Stat(filepath.Join(d, "go.mod")); statErr == nil && fi.Mode().IsRegular() {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("hotpath: no go.mod found in %s or any parent directory", wd)
		}
		d = parent
	}
}

// buildBinary compiles ./cmd/qompack (run from moduleRoot) to outPath — the REAL binary every
// later step spawns, per task-7-spec.md step 1.
func buildBinary(moduleRoot, outPath string) error {
	cmd := exec.Command("go", "build", "-o", outPath, "./cmd/qompack")
	cmd.Dir = moduleRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hotpath: go build -o %s ./cmd/qompack (in %s): %w\n%s", outPath, moduleRoot, err, stderr.String())
	}
	return nil
}

// resolveProjectDir returns the temp project directory the harness runs against: --project's
// value if given (created if it does not exist, never removed by this program), or a fresh
// os.MkdirTemp directory (removed by the returned cleanup).
func resolveProjectDir(project string) (root string, cleanup func(), err error) {
	if project != "" {
		abs, aerr := filepath.Abs(project)
		if aerr != nil {
			return "", nil, fmt.Errorf("hotpath: --project: %w", aerr)
		}
		if merr := os.MkdirAll(abs, 0o700); merr != nil {
			return "", nil, fmt.Errorf("hotpath: --project: %w", merr)
		}
		return abs, func() {}, nil
	}
	dir, merr := os.MkdirTemp("", "qompack-bench-project-")
	if merr != nil {
		return "", nil, fmt.Errorf("hotpath: creating temp project: %w", merr)
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// buildChildEnv returns os.Environ() with overrides applied on top — every spawned child (the
// daemon, `qompack version`, `qompack observe tool`, `qompack checkpoint`) gets exactly this
// environment, so QOMPACK_PROJECT_ROOT/QOMPACK_IPC_ADDR/HOME/USERPROFILE are identical across
// every process this bench run creates.
func buildChildEnv(overrides map[string]string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		key, _, found := strings.Cut(kv, "=")
		if found {
			if _, override := overrides[key]; override {
				continue
			}
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

// daemonProcess wraps the real `qompack daemon` child process (task-7-spec.md step 2), keeping
// its captured stderr around for a diagnostic if it never becomes reachable.
type daemonProcess struct {
	cmd    *exec.Cmd
	stderr *bytes.Buffer
}

// startDaemon launches `binPath daemon --project projectRoot` as a real, detached-from-this-
// program's-stdio child process, matching the REAL child process the brief requires (not
// daemon.SpawnDetached's lazy-spawn seam, which this harness deliberately bypasses so it controls
// the daemon's lifetime directly).
func startDaemon(binPath, projectRoot string, env []string) (*daemonProcess, error) {
	var stderrBuf bytes.Buffer
	cmd := exec.Command(binPath, "daemon", "--project", projectRoot)
	cmd.Env = env
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("hotpath: starting daemon: %w", err)
	}
	return &daemonProcess{cmd: cmd, stderr: &stderrBuf}, nil
}

// stderrString returns whatever the daemon has written to stderr so far — used only to enrich a
// "daemon never came up" error.
func (d *daemonProcess) stderrString() string {
	if d == nil || d.stderr == nil {
		return ""
	}
	return d.stderr.String()
}

// stopDaemon asks the daemon to shut down (admin.shutdown, best-effort) and waits up to
// daemonDownBound for the real process to exit, killing it outright past that bound. Once this
// program's OWN daemon child is confirmed dead (by a real os.Process.Wait, not a guess), it probes
// once more for an ORPHANED second daemon (FIX ROUND 1, I-3) and asks it to stop too, so teardown
// leaves zero qompack processes behind rather than just the one this program spawned itself.
//
// task-7-brief.md's realities #2 (binding): go-winio's pipe listener Close can race a fresh
// Accept and run long on Windows. This teardown tolerates a slow daemon exit rather than treating
// it as a bench failure — the measurement is already complete by the time this runs. errw carries
// only teardown diagnostics (M-5's "silent Kill" fix and I-3's orphan warning); nothing on the
// measured path writes to it.
func stopDaemon(d *daemonProcess, addr ipc.Addr, spool ipc.SpoolWriter, errw io.Writer) {
	if d == nil || d.cmd == nil || d.cmd.Process == nil {
		return
	}
	shutdownDaemon(addr, spool)

	done := make(chan error, 1)
	go func() { done <- d.cmd.Wait() }()

	t := time.NewTimer(daemonDownBound)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		// FIX ROUND 1, M-5: this used to Kill silently. go-winio's own documented Close/Accept
		// race (realities #2) is tolerated here — a slow-but-eventually-clean exit is normal on
		// this platform — but a bound actually being HIT is worth one line so the signal is not
		// thrown away; it costs nothing and never fails the bench.
		fmt.Fprintf(errw, "hotpath: daemon did not exit within %s of admin.shutdown — killing it\n", daemonDownBound)
		_ = d.cmd.Process.Kill()
		<-done
	}

	detectAndStopOrphan(addr, spool, errw)
}

// orphanCheckDeadline and orphanCheckTick bound how long detectAndStopOrphan waits for an
// orphaned daemon to actually stop once asked, after this program's own daemon child is already
// confirmed gone.
const (
	orphanCheckDeadline = 5 * time.Second
	orphanCheckTick     = 100 * time.Millisecond
)

// detectAndStopOrphan is FIX ROUND 1's I-3 fix. It runs only after this program's own daemon
// child has already exited (a real os.Process.Wait, per stopDaemon above) — so anything still
// answering admin.ping at addr at this point cannot be OUR child; it can only be a SECOND daemon.
//
// The one way that can happen: observe.tool runs on state.bin's tight ConnectDeadlineMs (5ms
// default), and on a host with the process-creation dispersion this harness itself documents, a
// dial can plausibly time out during a measured spawn. internal/ipc/client.go's Send then calls
// lazySpawn on that failure path, which launches a DETACHED daemon
// (internal/daemon.SpawnDetached, wired in by internal/cli/hookclient.go) inheriting the same
// QOMPACK_IPC_ADDR/QOMPACK_PROJECT_ROOT this harness set for every child. Ordinarily that detached
// process finds the project's own daemon.lock already held by OUR daemon and exits immediately
// (internal/cli/daemon.go: errors.Is(runErr, daemon.ErrLockHeld) -> nil), but a narrow race
// remains if the lazy spawn happens to land after our own daemon has already released the lock
// (e.g. right as this program's own teardown begins) — in which case the detached process can
// legitimately win the lock and keep running past this program's own exit.
//
// This is best-effort, not a hard failure: the measurement itself is already complete and correct
// by the time this runs (I-2's delivery-integrity check already caught any degraded-run scenario
// that would have triggered lazySpawn in the first place), so an orphan that refuses to stop is
// reported loudly rather than failing the whole bench run over a teardown hygiene issue.
func detectAndStopOrphan(addr ipc.Addr, spool ipc.SpoolWriter, errw io.Writer) {
	c := newProbeClient(addr, spool, probeConnectDeadline)
	defer func() { _ = c.Close() }()

	req := ipc.Request{Op: ipc.OpAdminPing, Session: adminPingSessionID, TS: core.NowMilli(core.SystemClock())}
	resp, _ := c.Send(context.Background(), req, probeAckDeadline)
	if !resp.OK {
		return // nothing else answering: teardown is clean.
	}

	fmt.Fprintln(errw, "hotpath: WARNING: a second daemon is still reachable after this program's own daemon exited "+
		"(likely internal/cli/hookclient.go's lazySpawn, triggered by a dial timeout during a measured spawn) — asking it to shut down")
	shutdownDaemon(addr, spool)

	deadline := time.Now().Add(orphanCheckDeadline)
	for time.Now().Before(deadline) {
		resp2, _ := c.Send(context.Background(), req, probeAckDeadline)
		if !resp2.OK {
			fmt.Fprintln(errw, "hotpath: orphaned daemon stopped")
			return
		}
		time.Sleep(orphanCheckTick)
	}
	fmt.Fprintln(errw, "hotpath: WARNING: the orphaned daemon did not stop within the bound — it may still be running "+
		"and holding the project's daemon.lock/named-pipe or socket")
}

// measureSpawns spawns binPath with args exactly n times, one at a time, timing wall-clock around
// each cmd.Run() call (task-7-spec.md step 5: "timing each spawn with time.Now() around
// cmd.Run()"). payloadFn(i) is fed to the child's stdin. A non-nil error from any single spawn
// aborts the whole measurement — a hook subcommand's own §2.3 contract is to always exit 0, so a
// non-zero exit here is a real failure worth stopping the run over, not a sample to discard.
func measureSpawns(ctx context.Context, binPath string, args []string, n int, env []string, payloadFn func(seq int) []byte) ([]time.Duration, error) {
	samples := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cmd := exec.Command(binPath, args...)
		cmd.Env = env
		cmd.Stdin = bytes.NewReader(payloadFn(i))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		start := time.Now()
		err := cmd.Run()
		elapsed := time.Since(start)
		if err != nil {
			return nil, fmt.Errorf("hotpath: spawn #%d (qompack %s): %w\n%s", i, strings.Join(args, " "), err, stderr.String())
		}
		samples = append(samples, elapsed)
	}
	return samples, nil
}

// measureSpawnFloor is measureSpawns specialised for `qompack version` (task-7-spec.md step 4):
// no stdin, no project resolution, no store I/O — the same binary, loader and OS process cost
// with none of the hook work.
func measureSpawnFloor(ctx context.Context, binPath string, n int, env []string) ([]time.Duration, error) {
	return measureSpawns(ctx, binPath, []string{"version"}, n, env, func(int) []byte { return nil })
}
