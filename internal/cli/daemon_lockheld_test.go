package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
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

// TestFsck_ALockCopiedFromAnotherPathIsNotARunningDaemon: fsck's daemon row dialled the address the
// lock recorded, so a store copied from another path — while that path's daemon ran — reported
// "daemon running" for a project no daemon served (F-UAT03-4's diagnostic half). The dial must be
// to this project's own address, and the row must say whose lock it is.
func TestFsck_ALockCopiedFromAnotherPathIsNotARunningDaemon(t *testing.T) {
	isolateUserGlobal(t)
	p := seedFsckProject(t)

	originalAddr, err := ipc.Resolve(t.TempDir())
	require.NoError(t, err)
	srv, err := ipc.NewServer(originalAddr, logging.Nop(), nil, 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	go func() {
		_ = srv.Serve(ctx, func(context.Context, ipc.Request) ipc.Response { return ipc.Response{OK: true} })
	}()

	run := paths.Of(p.Root).Run
	require.NoError(t, os.MkdirAll(paths.Long(run), 0o700))
	body, err := json.Marshal(daemon.LockInfo{
		PID: os.Getpid(), Started: time.Now().UnixMilli(), Addr: originalAddr.Path, Version: "0.0.0-test",
	})
	require.NoError(t, err)
	require.NoError(t, paths.CreateNew(filepath.Join(run, "daemon.lock"), body))

	_, doc, _ := fsckJSON(t, p.Root)
	require.Equal(t, false, doc["daemon_running"], "no daemon answers at this project's own address")
	require.Contains(t, fsckDetail(fsckRequireRow(t, doc, "daemon")), "written for another project path")
}

// TestFsck_ALockThisProjectWroteAtAnotherAddressIsItsDaemon is the other side of the test above: a
// lock is foreign when it was written for another PROJECT, not whenever its recorded address is one
// this process would not resolve. A daemon of this project started under another environment (on
// POSIX, XDG_RUNTIME_DIR or TMPDIR; anywhere, QOMPACK_IPC_ADDR) records such an address, and fsck
// must dial it and report the daemon running, not call the lock another path's.
func TestFsck_ALockThisProjectWroteAtAnotherAddressIsItsDaemon(t *testing.T) {
	isolateUserGlobal(t)
	p := seedFsckProject(t)

	recorded := ipc.Addr{Kind: ipc.UnixSocket, Path: shortSocketPath(t)}
	if runtime.GOOS == "windows" {
		recorded = ipc.Addr{Kind: ipc.NamedPipe,
			Path: `\\.\pipe\qompack-fsck-lockid-` + strconv.FormatInt(time.Now().UnixNano(), 36)}
	}
	srv, err := ipc.NewServer(recorded, logging.Nop(), nil, 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	go func() {
		_ = srv.Serve(ctx, func(context.Context, ipc.Request) ipc.Response { return ipc.Response{OK: true} })
	}()

	run := paths.Of(p.Root).Run
	require.NoError(t, os.MkdirAll(paths.Long(run), 0o700))
	body, err := json.Marshal(daemon.LockInfo{
		PID: os.Getpid(), Started: time.Now().UnixMilli(), Addr: recorded.Path, Version: "0.0.0-test",
	})
	require.NoError(t, err)
	require.NoError(t, paths.CreateNew(filepath.Join(run, "daemon.lock"), body))

	_, doc, _ := fsckJSON(t, p.Root)
	require.Equal(t, true, doc["daemon_running"], "the recorded address answers")
	require.NotContains(t, fsckDetail(fsckRequireRow(t, doc, "daemon")), "another project path")
}

// shortSocketPath is a Unix socket path private to this test and inside sun_path's 100 bytes, which
// a test-named t.TempDir can outgrow.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "qfl")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.ToSlash(filepath.Join(dir, "o.sock"))
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
