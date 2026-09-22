//go:build windows

package store

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestOpenObjectLeaf_BranchesOnTheHandlesReparseAttribute pins which leaf the Windows fast path
// accepts: a no-follow handle on an ordinary file carries no reparse attribute and is read as it
// is, while a reparse point (an AF_UNIX socket file, the one an unprivileged host can create)
// carries it and so goes to openObjectChecked, whose Lstat decides what it is.
func TestOpenObjectLeaf_BranchesOnTheHandlesReparseAttribute(t *testing.T) {
	dir := t.TempDir()

	regular := filepath.Join(dir, "regular")
	require.NoError(t, os.WriteFile(paths.Long(regular), []byte("plain object bytes"), 0o600))
	requireHandleReparse(t, regular, false)
	f, fi, err := openObjectLeaf(paths.Long(regular))
	require.NoError(t, err)
	require.True(t, fi.Mode().IsRegular())
	require.EqualValues(t, len("plain object bytes"), fi.Size())
	require.NoError(t, f.Close())

	sock := filepath.Join(dir, "s")
	l, err := net.Listen("unix", sock)
	require.NoError(t, err, "fixture: an AF_UNIX socket file must be creatable here")
	t.Cleanup(func() { _ = l.Close() })
	requireHandleReparse(t, sock, true)
	_, _, err = openObjectLeaf(paths.Long(sock))
	require.ErrorIs(t, err, errObjectNotRegular, "a reparse leaf is refused by the checked path's Lstat")
}

// requireHandleReparse opens p the way openObjectLeaf does and asserts whether the handle's own
// Stat reports a reparse point.
func requireHandleReparse(t *testing.T, p string, want bool) {
	t.Helper()
	f, err := os.OpenFile(paths.Long(p), os.O_RDONLY|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	require.NoError(t, err)
	require.Equal(t, want, isReparsePoint(fi), "reparse attribute on the no-follow handle for %s", p)
}
