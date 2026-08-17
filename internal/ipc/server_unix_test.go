//go:build !windows

package ipc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// TestUnixSocketPermissions is the table's row: the bound socket is mode 0600 in a 0700
// directory.
func TestUnixSocketPermissions(t *testing.T) {
	_, addr := newTestServer(t, func(context.Context, Request) Response { return Response{OK: true} })

	fi, err := os.Stat(addr.Path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	dirFi, err := os.Stat(filepath.Dir(addr.Path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dirFi.Mode().Perm())
}

// TestStaleUnixSocketReclaimed is the table's row: a socket file left behind with nothing
// listening on it is removed and rebound successfully, rather than failing with "address already
// in use".
func TestStaleUnixSocketReclaimed(t *testing.T) {
	root := t.TempDir()
	addr, err := Resolve(root)
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(filepath.Dir(addr.Path), dirPerm))
	// Bind and close a real listener at addr.Path, leaving its socket file behind — exactly what a
	// daemon that crashed (SIGKILL, panic before cleanup) would leave. *net.UnixListener unlinks
	// its own socket on an ordinary Close (net/unixsock_posix.go), which would make the file
	// vanish before NewServer ever ran and defeat the point of this test — SetUnlinkOnClose(false)
	// is the documented way to simulate an unclean shutdown instead of a clean one (N-3, fix
	// round 2).
	stale, err := listen(addr, logging.Nop(), MaxLineBytes)
	require.NoError(t, err)
	staleUnix, ok := stale.(*net.UnixListener)
	require.True(t, ok, "listen must return a *net.UnixListener on POSIX")
	staleUnix.SetUnlinkOnClose(false)
	require.NoError(t, staleUnix.Close())

	_, err = os.Stat(addr.Path)
	require.NoError(t, err, "the stale socket file must still exist before NewServer runs")

	srv, err := NewServer(addr, logging.Nop(), obs.New(core.SystemClock()), MaxLineBytes)
	require.NoError(t, err, "NewServer must reclaim a stale socket rather than failing")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, func(context.Context, Request) Response { return Response{OK: true} }) }()
	t.Cleanup(func() { cancel(); _ = srv.Close(); <-done })

	conn := newRawClient(t, addr)
	writeRequest(t, conn, Request{Op: OpAdminPing, Session: "s", TS: 1})
	require.Equal(t, ACK, readByte(t, conn), "a client must be able to reach the rebound server")
}

// TestListenReturnsErrAddrInUse is the CONTROLLER RULING #21 row: a still-live listener at the
// same address is reported through an ipc-local sentinel (errors.Is-able), not a bare string —
// daemon.AcquireLock (Task 3+) maps this into its own ErrLockHeld without ipc ever importing
// daemon.
func TestListenReturnsErrAddrInUse(t *testing.T) {
	_, addr := newTestServer(t, func(context.Context, Request) Response { return Response{OK: true} })

	_, err := NewServer(addr, logging.Nop(), obs.New(core.SystemClock()), MaxLineBytes)
	require.ErrorIs(t, err, ErrAddrInUse)
}
