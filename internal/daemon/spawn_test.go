package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
)

// TestBuildSpawnEnv_StripsFaultAddsProjectRoot pins SpawnDetached's environment contract: the
// spawned daemon inherits the caller's environment, minus any QOMPACK_FAULT entry, plus
// QOMPACK_PROJECT_ROOT.
func TestBuildSpawnEnv_StripsFaultAddsProjectRoot(t *testing.T) {
	t.Parallel()

	in := []string{"PATH=/usr/bin", "QOMPACK_FAULT=network-timeout", "HOME=/home/x"}
	out := buildSpawnEnv(in, "/proj")

	require.Contains(t, out, "PATH=/usr/bin")
	require.Contains(t, out, "HOME=/home/x")
	require.Contains(t, out, "QOMPACK_PROJECT_ROOT=/proj")
	for _, kv := range out {
		require.NotContains(t, kv, "QOMPACK_FAULT", "the spawned daemon must never inherit fault injection")
	}
}

// TestBuildSpawnEnv_StripIsCaseInsensitive pins Minor 12: Windows environment variable names are
// case-insensitive, so a differently-cased QOMPACK_FAULT entry must be stripped exactly like the
// canonical spelling.
func TestBuildSpawnEnv_StripIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	in := []string{"PATH=/usr/bin", "qompack_fault=network-timeout", "Qompack_Fault=other"}
	out := buildSpawnEnv(in, "/proj")

	require.Contains(t, out, "PATH=/usr/bin")
	for _, kv := range out {
		require.NotContains(t, kv, "network-timeout")
		require.NotContains(t, kv, "other")
	}
}

// TestBuildSpawnEnv_NoFaultEntryIsUnaffected pins that an environment with no QOMPACK_FAULT entry
// is passed through unchanged apart from the appended project root.
func TestBuildSpawnEnv_NoFaultEntryIsUnaffected(t *testing.T) {
	t.Parallel()

	in := []string{"PATH=/usr/bin"}
	out := buildSpawnEnv(in, "/proj")
	require.Equal(t, []string{"PATH=/usr/bin", "QOMPACK_PROJECT_ROOT=/proj"}, out)
}

// TestBuildSpawnCommand_ShapesArgvAndSysProcAttr pins the fixed argv and that the platform
// SysProcAttr is always set.
func TestBuildSpawnCommand_ShapesArgvAndSysProcAttr(t *testing.T) {
	t.Parallel()

	cmd := buildSpawnCommand("/path/to/self", "/proj", []string{"PATH=/usr/bin"})
	require.Equal(t, []string{"/path/to/self", "daemon", "--project", "/proj"}, cmd.Args)
	require.NotNil(t, cmd.SysProcAttr)
	require.Contains(t, cmd.Env, "QOMPACK_PROJECT_ROOT=/proj")
}

// TestEnsureRunning_AlreadyRunningNeverSpawns pins EnsureRunning's fast path: a live listener at
// the resolved address means EnsureRunning returns (false, nil) without ever calling
// SpawnDetached — proven here by handing it a self path that would fail loudly if exec.Command
// ever tried to start it.
//
// Not run under t.Parallel(): EnsureRunning's own liveness check is bounded by the real,
// spec-mandated ensureRunningDialTimeout (20ms), and that budget was observed to occasionally miss
// under -race with many other real-socket tests in this package contending for scheduler time in
// parallel — a test-environment artifact, not a property of EnsureRunning itself. Running this one
// test outside the parallel batch removes that contention.
func TestEnsureRunning_AlreadyRunningNeverSpawns(t *testing.T) {
	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)

	srv, err := ipc.NewServer(addr, logging.Nop(), nil, 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	go func() {
		_ = srv.Serve(ctx, func(context.Context, ipc.Request) ipc.Response {
			return ipc.Response{OK: true}
		})
	}()

	// Confirm the listener is actually reachable before exercising EnsureRunning's own real,
	// spec-mandated ensureRunningDialTimeout (20ms — task-3-spec.md). NewServer binds
	// synchronously, so this is normally instantaneous; require.Eventually only guards against a
	// CPU-starved CI runner (observed under -race with heavy parallel test contention) delaying the
	// goroutine above far enough that a single 20ms probe taken immediately could miss it — that
	// would be a test-environment artifact, not the property this test exists to check.
	require.Eventually(t, func() bool { return ipc.Probe(addr, ensureRunningDialTimeout) },
		2*time.Second, 5*time.Millisecond, "the test server never became reachable")

	clk := newFakeClock(epoch)
	spawned, err := EnsureRunning(root, "/this/path/does/not/exist", logging.Nop(), clk)
	require.NoError(t, err)
	require.False(t, spawned, "a live daemon must never trigger a spawn")
}

// TestEnsureRunning_SpawnFailureReportsImmediately pins that a SpawnDetached failure (here, an
// unexecutable self path) is surfaced promptly rather than spending the full poll budget.
func TestEnsureRunning_SpawnFailureReportsImmediately(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	clk := newFakeClock(epoch)

	spawned, err := EnsureRunning(root, "/this/path/does/not/exist", logging.Nop(), clk)
	require.Error(t, err)
	require.False(t, spawned, "spawned must be false when SpawnDetached itself failed — nothing was actually spawned")
}
