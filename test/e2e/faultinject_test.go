package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// qompackFaultEnvKey is faultinject_test.go's own copy of the fault-injection variable name.
// internal/cli/fault.go is the ONE non-test file allowed to carry this literal; every test file
// that needs it (this one included) is explicitly exempted from that grep.
const qompackFaultEnvKey = "QOMPACK_FAULT"

// The eleven fault sites of task-6-spec.md's table, spelled out here rather than imported: cli's
// own copies (internal/cli/faultsites.go) are unexported, and re-typing eleven short strings once,
// in the file that has to enumerate exactly that table, is cheaper than exporting a seam whose only
// consumer would be this test.
//
// "spool-full:0" — not the bare site name — because the site's own default allowance is 1 (per
// the spec's literal wording, "faultArg (default 1)"), and a single hook process makes at most one
// spool.Append call: with the default, that one call always SUCCEEDS, so the site does nothing
// observable in a one-shot combo (fix round 1, Minor M-5). Passing an explicit 0 makes the very
// first Append fail, which is what this matrix actually needs to exercise the site at all.
var e2eFaultSites = []string{
	"stdin-eof", "stdin-garbage", "oversize", "daemon-down", "spool-readonly",
	"spool-full:0", "disk-full", "state-corrupt", "config-corrupt", "panic:hook", "panic:client",
}

// e2eIdleExitFast keeps every daemon a fault-matrix sub-test's own lazy spawn might start
// short-lived: with QOMPACK_FAULT unset for anything other than daemon-down, a real daemon still
// comes up in the background exactly as production does, and without this it would sit around for
// config.Defaults()'s 1800s idle-exit window as an orphaned process for the rest of the test run.
var e2eIdleExitFastEnv = map[string]string{idleExitSecondsEnvKey: "1"}

// TestHooksExitZeroUnderFaults is task-6-spec.md's 66-combination table: every one of the six hook
// subcommands, under every one of the eleven fault sites, must exit 0 and write valid (possibly
// empty) JSON to stdout. Each combination gets its own fresh, isolated project — several sites
// mutate on-disk state (state-corrupt, config-corrupt, spool-readonly) or a process-wide count
// (spool-full), and sharing a project across combinations would let one fault site's aftermath leak
// into the next.
func TestHooksExitZeroUnderFaults(t *testing.T) {
	bin := Build(t)

	var total int
	for _, hook := range hookSubcommands {
		for _, site := range e2eFaultSites {
			total++
			name := hook.event + "/" + site
			t.Run(name, func(t *testing.T) {
				dir := e2eFaultProject(t)
				payload := payloadFor(t, hook.event, dir)

				env := map[string]string{
					"QOMPACK_PROJECT_ROOT": dir,
					"HOME":                 t.TempDir(),
					qompackFaultEnvKey:     site,
				}
				for k, v := range e2eIdleExitFastEnv {
					env[k] = v
				}

				stdout, stderr, code := Run(t, bin, hook.argv, payload, env)
				require.Equal(t, 0, code,
					"hook %q under fault %q must exit 0 (§2.3)\nstdout:\n%s\nstderr:\n%s",
					hook.event, site, stdout, stderr)

				var out hookio.Output
				require.NoError(t, json.Unmarshal(bytes.TrimSpace(stdout), &out),
					"hook %q under fault %q must write valid JSON, got:\n%s", hook.event, site, stdout)

				// session-start's own preSend seam (internal/cli/sessionstart.go) WAITS for a
				// daemon to become reachable before this call ever returns, and that daemon's one
				// live session never gets a matching flush in this test — so it would never
				// idle-exit on its own and would still hold its own log file open (a Windows
				// delete-blocker) when this subtest's t.TempDir() cleanup runs moments later.
				// Shutting it down explicitly, for every combination (a fast no-op wherever no
				// daemon ever came up), makes that race impossible rather than merely unlikely.
				e2eShutdownIfReachable(t, dir)
			})
		}
	}
	t.Logf("TestHooksExitZeroUnderFaults: %d hooks x %d sites = %d combinations run", len(hookSubcommands), len(e2eFaultSites), total)
}

// e2eFaultProject returns a fresh project root for one fault-matrix combination: a plain temp
// directory (no testutil.Project machinery — a fault-matrix run needs nothing beyond a real,
// existing, isolated root) with a defensive permission-reset registered before t.TempDir()'s own
// cleanup, so the spool-readonly site's deliberately non-writable spool directory can never make
// this test's own cleanup fail.
func e2eFaultProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() { resetPermissionsForCleanup(dir) })
	return dir
}

// resetPermissionsForCleanup best-effort undoes anything a fault site's spool-readonly injection
// (internal/cli/fault_lockdir_unix.go / fault_lockdir_windows.go) did to dir, so t.TempDir()'s own
// RemoveAll is never blocked by a permission this test itself provoked.
func resetPermissionsForCleanup(dir string) {
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	if runtime.GOOS == "windows" {
		_ = exec.Command("icacls", dir, "/reset", "/t", "/c").Run() //nolint:gosec // G204: fixed subcommand, dir is this test's own temp directory
	}
}

// e2eLazySpawnSettleBound and e2eLazySpawnSettleTick bound how long e2eShutdownIfReachable waits
// for a fire-and-forget lazy spawn to actually come up before concluding none is coming: a
// non-session-start hook's own client (internal/ipc/client.go's lazySpawn) never waits for the
// daemon it starts, so "not reachable yet" and "never coming up at all" are indistinguishable at
// the instant Run returns — only a short poll tells them apart.
//
// Basis: daemon.SpawnPollBound is the same question asked from the other side — the window
// EnsureRunning polls a spawn it made before declaring it never arrived. Waiting exactly that long
// (V2-MERGE-25 ②; it was a copied 1500ms literal) means this helper concludes "none is coming" at
// precisely the moment the spawning side would have, and moves with it if that window ever changes.
const (
	e2eLazySpawnSettleBound = daemon.SpawnPollBound
	e2eLazySpawnSettleTick  = 25 * time.Millisecond
)

// e2eShutdownIfReachable dials root's resolved address — polling briefly first, since a
// fire-and-forget lazy spawn may still be in flight — and, only if something answers, sends
// admin.shutdown and waits for it to go away. It is a fast no-op whenever no daemon ever comes up
// at all (every daemon-down row, and most panic:hook rows, which fault before any spawn attempt).
func e2eShutdownIfReachable(t *testing.T, root string) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	if err != nil {
		return
	}

	reachable := ipc.Probe(addr, e2eProbeTimeout)
	if !reachable {
		ticker := time.NewTicker(e2eLazySpawnSettleTick)
		deadline := time.NewTimer(e2eLazySpawnSettleBound)
		defer ticker.Stop()
		defer deadline.Stop()
	settle:
		for {
			select {
			case <-ticker.C:
				if ipc.Probe(addr, e2eProbeTimeout) {
					reachable = true
					break settle
				}
			case <-deadline.C:
				break settle
			}
		}
	}
	if !reachable {
		return
	}

	sp, _ := ipc.NewSpool(paths.Of(root).Spool)
	c := ipc.NewClientWithOptions(addr, sp, nil, nil, ipc.ClientOptions{ProjectRoot: root})
	defer func() { _ = c.Close() }()

	// Client.Send never propagates an error — a failed connect/write/ACK round trip just spools
	// the request instead and returns silently (00-ARCHITECTURE.md §2.4/§12.3), so a single
	// admin.shutdown attempt has no way to know whether the daemon actually received it. Retrying
	// on a ticker until the daemon actually goes away (or the overall bound elapses) is the only
	// way this helper can tell "delivered" from "silently spooled" — a ticker, not time.Sleep,
	// per §6.1's wall-clock-sleep ban (devtool lint's sleepcheck sub-check).
	ticker := time.NewTicker(e2eDaemonDownTick)
	defer ticker.Stop()
	timeout := time.NewTimer(e2eDaemonDownBound)
	defer timeout.Stop()
	for {
		_, _ = c.Send(context.Background(), ipc.Request{
			Op: ipc.OpAdminShutdown, TS: core.NowMilli(core.SystemClock()), Reply: true,
		}, e2eRoundTripDeadline)
		if !ipc.Probe(addr, e2eProbeTimeout) {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			if ipc.Probe(addr, e2eProbeTimeout) {
				t.Logf("e2eShutdownIfReachable: daemon at %s was still reachable after %s of retried admin.shutdown; leaving it running", root, e2eDaemonDownBound)
			}
			return
		}
	}
}

// buildNoInjectOnce guards the single -tags noinject build TestFaultSitesInertWhenUnset performs.
var (
	buildNoInjectOnce sync.Once
	builtNoInjectBin  string
	buildNoInjectDir  string
	buildNoInjectErr  error
)

// buildNoInject compiles ./cmd/qompack with -tags noinject into its own temp directory, so
// TestFaultSitesInertWhenUnset can compare its output against the default build's byte for byte.
func buildNoInject(t *testing.T) string {
	t.Helper()
	buildNoInjectOnce.Do(func() {
		root, err := moduleRoot()
		if err != nil {
			buildNoInjectErr = err
			return
		}
		buildNoInjectDir, buildNoInjectErr = os.MkdirTemp("", "qompack-e2e-noinject-")
		if buildNoInjectErr != nil {
			return
		}
		out := filepath.Join(buildNoInjectDir, "qompack")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		cmd := exec.Command("go", "build", "-tags", "noinject", "-o", out, "./cmd/qompack")
		cmd.Dir = root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			buildNoInjectErr = err
			t.Logf("go build -tags noinject: %v\n%s", err, stderr.String())
			return
		}
		builtNoInjectBin = out
	})
	if buildNoInjectErr != nil {
		t.Fatalf("e2e: building the -tags noinject ./cmd/qompack: %v", buildNoInjectErr)
	}
	t.Cleanup(func() {
		if buildNoInjectDir != "" {
			_ = os.RemoveAll(buildNoInjectDir)
		}
	})
	return builtNoInjectBin
}

// TestFaultSitesInertWhenUnset is task-6-spec.md's e2e table row: with QOMPACK_FAULT unset, a hook
// run against the default build must be byte-identical to the same run against a -tags noinject
// build — the compile-time guarantee that fault injection adds no observable behaviour when it is
// not asked for.
//
// This is the second of the row's own two halves (fix round 1, Minor M-9): "faultActive false for
// all eleven sites with the variable unset" is pinned at the unit level, in
// internal/cli/fault_test.go's TestFaultActive_UnsetIsInertForAllElevenSites — the e2e package
// cannot see that unexported function to assert it directly. This test covers the other half, the
// one only a real build comparison can prove.
func TestFaultSitesInertWhenUnset(t *testing.T) {
	bin := Build(t)
	noinjectBin := buildNoInject(t)

	dir := t.TempDir()
	t.Cleanup(func() { e2eShutdownIfReachable(t, dir) })
	env := map[string]string{
		"QOMPACK_PROJECT_ROOT": dir, "HOME": t.TempDir(),
		// Explicitly cleared, not merely omitted (fix round 1, Minor M-9): Run's own harness
		// inherits os.Environ(), so a developer running this locally with QOMPACK_FAULT already
		// set in their shell would otherwise get a silently meaningless pass.
		qompackFaultEnvKey: "",
	}
	payload := payloadFor(t, "PostToolUse", dir)

	stdoutDefault, _, codeDefault := Run(t, bin, []string{"observe", "tool"}, payload, env)
	require.Equal(t, 0, codeDefault)

	stdoutNoinject, _, codeNoinject := Run(t, noinjectBin, []string{"observe", "tool"}, payload, env)
	require.Equal(t, 0, codeNoinject)

	require.Equal(t, string(stdoutDefault), string(stdoutNoinject),
		"a hook's stdout with QOMPACK_FAULT unset must be byte-identical between the default build and a -tags noinject build")
}

// TestSelfTestIsTheOnlyNonZeroExit is task-6-spec.md's e2e table row: under the daemon-down fault,
// every subcommand SP-05 ships except self-test still exits 0.
//
// The project is pre-degraded first (three consecutive marker-less SessionStarts — see
// TestE2ESelfTestExitsNonZeroOnCritical's own doc comment for why three, not two), BEFORE
// daemon-down is applied to the cases below: a merely daemon-unreachable but otherwise HEALTHY
// project would make self-test itself report only a SevWarn ("daemon reachable or spawnable"
// failed) and still exit 0 — proving nothing about the exit-code policy. Degrading the project for
// real first is what makes "self-test is the only one that may exit non-zero" a meaningful claim
// rather than a vacuous one, since the other ten cases below still have to stay at 0 despite a
// genuine, non-recoverable-by-this-fault critical failure sitting in the project's own history.
//
// Scoped to the subcommands this subplan actually implements — the six hooks (already exit-0 by
// construction), daemon, self-test, version, config print and config schema — rather than the
// whole dispatch table: the not-yet-implemented placeholders (`status`, `recall`, `mcp`, ...)
// report core.ErrNotImplemented and a non-zero exit by design, independent of any fault, and
// asserting otherwise would just be testing a different subplan's TODO.
func TestSelfTestIsTheOnlyNonZeroExit(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	baseEnv := e2eEnv(p)

	for i, sess := range []string{"sess-e2e-only-nonzero-1", "sess-e2e-only-nonzero-2", "sess-e2e-only-nonzero-3"} {
		payload, err := json.Marshal(struct {
			HookEventName string `json:"hook_event_name"`
			SessionID     string `json:"session_id"`
			CWD           string `json:"cwd"`
			Source        string `json:"source"`
		}{"SessionStart", sess, p.Root, "startup"})
		require.NoError(t, err)

		_, stderr, code := Run(t, bin, []string{"session-start"}, payload, baseEnv)
		require.Equal(t, 0, code, "degrading session-start #%d: stderr:\n%s", i, stderr)
		if i == 0 {
			e2eWaitDaemonUp(t, p.Root)
		}
	}
	require.Eventually(t, func() bool {
		h := contract.LoadHistory(contract.HistoryPath(p.Root))
		return h.StartsWithoutMarker >= 2
	}, e2eHistoryConvergeBound, e2eDaemonUpTick, "the project never actually degraded")

	env := e2eEnv(p)
	env[qompackFaultEnvKey] = "daemon-down"
	for k, v := range e2eIdleExitFastEnv {
		env[k] = v
	}

	cases := []struct {
		argv []string
		want int
	}{
		{[]string{"observe", "tool"}, 0},
		{[]string{"observe", "prompt"}, 0},
		{[]string{"observe", "stop"}, 0},
		{[]string{"session-start"}, 0},
		{[]string{"checkpoint"}, 0},
		{[]string{"flush"}, 0},
		{[]string{"version"}, 0},
		{[]string{"config", "print", "--json"}, 0},
		{[]string{"config", "schema"}, 0},
		{[]string{"self-test", "--json"}, 1}, // the one command §2.3 permits a non-zero exit — and, with the project genuinely degraded above, it actually does.
	}

	for _, tc := range cases {
		t.Run(argvName(tc.argv), func(t *testing.T) {
			payload := payloadFor(t, "PostToolUse", p.Root)
			_, stderr, code := Run(t, bin, tc.argv, payload, env)
			require.Equal(t, tc.want, code, "argv=%v stderr:\n%s", tc.argv, stderr)
		})
	}
}

// argvName renders argv as a t.Run-safe subtest name.
func argvName(argv []string) string {
	name := ""
	for i, a := range argv {
		if i > 0 {
			name += "_"
		}
		name += a
	}
	return name
}
