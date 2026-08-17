package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
)

// TestDaemon_ExitsZeroOnCleanStop asserts runDaemon returns nil (mapped to exit 0 by Dispatch) once
// its context is cancelled — the ordinary "signalled to stop" path.
func TestDaemon_ExitsZeroOnCleanStop(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan int, 1)
	go func() {
		var out, errw bytes.Buffer
		code := Dispatch(ctx, []Cmd{{Name: "daemon", Run: runDaemon}}, []string{"qompack", "daemon", "--project", dir}, Env{
			Getenv: noEnv, Stdin: bytes.NewReader(nil), Clock: testClock(),
		}, &out, &errw)
		done <- code
	}()

	require.Eventually(t, func() bool {
		addr, err := ipc.Resolve(dir)
		return err == nil && ipc.Probe(addr, 50*time.Millisecond)
	}, 5*time.Second, 20*time.Millisecond, "the daemon never became reachable")

	cancel()

	select {
	case code := <-done:
		require.Equal(t, ExitOK, code)
	case <-time.After(30 * time.Second):
		t.Fatal("runDaemon did not return after its context was cancelled")
	}
}

// TestDaemon_ExitsZeroWhenLockHeld asserts that a second daemon over the same project — which
// AcquireLock refuses — still exits 0 rather than surfacing the lock contention as a failure.
func TestDaemon_ExitsZeroWhenLockHeld(t *testing.T) {
	dir := t.TempDir()

	addr, err := ipc.Resolve(dir)
	require.NoError(t, err)
	lock, err := daemon.AcquireLock(dir, addr, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), []Cmd{{Name: "daemon", Run: runDaemon}},
		[]string{"qompack", "daemon", "--project", dir}, Env{
			Getenv: noEnv, Stdin: bytes.NewReader(nil), Clock: testClock(),
		}, &out, &errw)

	require.Equal(t, ExitOK, code, "a lock already held by another daemon must not be a non-zero exit")
}

// TestDaemon_LayoutFailureExitsZero asserts a daemon that cannot even create its own .qompack/
// layout still exits 0 (§2.3's "a daemon spawned lazily must never surface non-zero" applies to
// every failure this subcommand can reach, not only the ones after Run starts).
//
// The premise deliberately avoids ever exercising resolveProjectRoot's own process-cwd fallback
// (paths.Resolve's §3.3 last resort, "the payload cwd itself" / here, the process's own cwd): a
// `--project` flag with a NON-empty value skips that fallback entirely (runDaemon reads *project
// directly), which matters in this test binary specifically — the process cwd during `go test
// ./internal/cli/...` is inside this very repository checkout, so a root-resolution fallback that
// reached it would have a REAL daemon start rooted at the actual repo tree.
func TestDaemon_LayoutFailureExitsZero(t *testing.T) {
	parent := t.TempDir()
	// A regular FILE where .qompack/ must go: os.MkdirAll fails with ENOTDIR on every platform
	// (an os.Chmod-based "read-only directory" would not, on Windows).
	require.NoError(t, os.WriteFile(filepath.Join(parent, ".qompack"), []byte("not a dir"), 0o600))

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), []Cmd{{Name: "daemon", Run: runDaemon}},
		[]string{"qompack", "daemon", "--project", parent}, Env{
			Getenv: noEnv, Stdin: bytes.NewReader(nil), Clock: testClock(),
		}, &out, &errw)

	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())
}

// TestDaemon_ForegroundFlagParses asserts --foreground is accepted (and, since it is purely
// informational, changes nothing about the exit code).
func TestDaemon_ForegroundFlagParses(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan int, 1)
	go func() {
		var out, errw bytes.Buffer
		code := Dispatch(ctx, []Cmd{{Name: "daemon", Run: runDaemon}},
			[]string{"qompack", "daemon", "--project", dir, "--foreground"}, Env{
				Getenv: noEnv, Stdin: bytes.NewReader(nil), Clock: testClock(),
			}, &out, &errw)
		done <- code
	}()

	require.Eventually(t, func() bool {
		addr, err := ipc.Resolve(dir)
		return err == nil && ipc.Probe(addr, 50*time.Millisecond)
	}, 5*time.Second, 20*time.Millisecond)

	cancel()
	select {
	case code := <-done:
		require.Equal(t, ExitOK, code)
	case <-time.After(30 * time.Second):
		t.Fatal("runDaemon did not return")
	}
}
