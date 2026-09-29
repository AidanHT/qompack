package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

func runLosingDaemon(t *testing.T, root string) (int, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), []Cmd{{Name: "daemon", Run: runDaemon}},
		[]string{"qompack", "daemon", "--project", root, "--foreground"}, Env{
			Getenv: noEnv, Stdin: bytes.NewReader(nil), Clock: testClock(),
		}, &out, &errw)
	return code, errw.String()
}

// TestDaemon_LockHeldExitNamesTheHolder is F-UAT03-4's "at minimum log a named reason": a daemon
// that cannot take the project's lock used to exit 0 with nothing on stdout, stderr or in the log,
// so a store whose lock was held — by a live daemon, or by a lock copied in from another path —
// looked exactly like a daemon that never ran. It still exits 0 (§2.3), and says why, in the day
// log and on --foreground's stderr.
func TestDaemon_LockHeldExitNamesTheHolder(t *testing.T) {
	isolateUserGlobal(t)

	t.Run("a live daemon holds it", func(t *testing.T) {
		dir := t.TempDir()
		addr, err := ipc.Resolve(dir)
		require.NoError(t, err)
		lock, err := daemon.AcquireLock(dir, addr, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = lock.Release() })

		code, stderr := runLosingDaemon(t, dir)
		require.Equal(t, ExitOK, code)
		log := dayLogText(t, dir)
		require.Contains(t, log, "daemon: another daemon holds this project's lock; this one exits")
		require.Contains(t, log, "pid="+itoa(os.Getpid()))
		require.Contains(t, stderr, "another daemon holds this project's lock")
	})

	t.Run("a lock copied in from another path", func(t *testing.T) {
		dir := t.TempDir()
		other, err := ipc.Resolve(t.TempDir())
		require.NoError(t, err)
		run := paths.Of(dir).Run
		require.NoError(t, os.MkdirAll(paths.Long(run), 0o700))
		body, err := json.Marshal(daemon.LockInfo{
			PID: os.Getpid(), Started: time.Now().UnixMilli(), Addr: other.Path, Version: "0.0.0-test",
		})
		require.NoError(t, err)
		require.NoError(t, paths.CreateNew(filepath.Join(run, "daemon.lock"), body))
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(run, "daemon.hb")), nil, 0o600))

		code, stderr := runLosingDaemon(t, dir)
		require.Equal(t, ExitOK, code)
		log := dayLogText(t, dir)
		require.Contains(t, log, "was written for another project path")
		require.Contains(t, stderr, "was written for another project path")
	})
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
