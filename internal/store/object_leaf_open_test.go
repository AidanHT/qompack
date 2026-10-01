package store

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// The object read's leaf check (openObjectLeaf, object_open_*.go) is per platform: one no-follow
// open whose handle is checked on Windows, an Lstat plus a no-follow, non-blocking open on Linux and
// darwin. These tests pin what every variant must still refuse, on the platforms where the host can
// build the leaf. object_leaf_open_unix_test.go adds the symbolic-link and FIFO leaves, which an
// unprivileged Windows host cannot create.

// TestReadBoundedObject_RefusesASocketLeaf puts an AF_UNIX socket at the object path. On Windows
// that is a reparse point any user can create, so it is the leaf that exercises the checked
// fallback there; on Linux and darwin it is a nonregular leaf the Lstat refuses before any open.
// Either way the refusal must be the not-regular one and never os.IsNotExist, which readObjectFile
// would read as "try the other spelling" or "absent" rather than as a refused object.
func TestReadBoundedObject_RefusesASocketLeaf(t *testing.T) {
	p := filepath.Join(shortSocketDir(t), "s")
	l, err := net.Listen("unix", p)
	require.NoError(t, err, "fixture: an AF_UNIX socket file must be creatable here")
	t.Cleanup(func() { _ = l.Close() })
	fi, err := os.Lstat(paths.Long(p))
	require.NoError(t, err)
	require.False(t, fi.Mode().IsRegular(), "fixture sanity: the socket leaf must not be a regular file")

	_, err = readBoundedObject(p, 1<<20)
	require.ErrorIs(t, err, errObjectNotRegular)
	require.False(t, os.IsNotExist(err))
}

// shortSocketDir returns a fresh, empty directory short enough to bind an AF_UNIX socket in, removed when
// the test ends.
//
// Not t.TempDir: its name embeds the test's name, and sun_path is 104 bytes on darwin and 108 on
// Linux and Windows. macos-latest's /var/folders/<2>/<30>/T/ (49 bytes) plus a test-named directory
// overflowed it ("bind: invalid argument", run 36816905394), and so did windows-latest's
// C:\Users\RUNNER~1\AppData\Local\Temp\ under internal/store's longest socket test name. A short
// prefix under the same temp directory leaves the socket path near 70 bytes on every runner.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "qsk")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestReadBoundedObject_RefusesADirectoryLeafAsNotRegular pins the sentinel the directory refusal
// carries, which readObjectFile's damaged-not-absent mapping depends on.
func TestReadBoundedObject_RefusesADirectoryLeafAsNotRegular(t *testing.T) {
	p := filepath.Join(t.TempDir(), "adir")
	require.NoError(t, os.MkdirAll(paths.Long(p), 0o700))

	_, err := readBoundedObject(p, 1<<20)
	require.ErrorIs(t, err, errObjectNotRegular)
}

// TestGetChunk_ADirectoryAtTheObjectPathIsDamagedNotAbsent drives the leaf refusal end to end: an
// indexed chunk whose object path holds a directory reports ErrDamaged — the read was refused, the
// bytes were never checked — and nothing is moved, because there are no bytes to preserve.
func TestGetChunk_ADirectoryAtTheObjectPathIsDamagedNotAbsent(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	res, err := tp.Store.PutBytes(ctx, bytes.Repeat([]byte("leaf check content\n"), 16), PutOptions{Path: "src/leaf.txt"})
	require.NoError(t, err)
	require.Len(t, res.Root.Chunks, 1)
	h := res.Root.Chunks[0].Hash

	obj := tp.Store.objectPath(h)
	require.NoError(t, os.Remove(paths.Long(obj)))
	require.NoError(t, os.MkdirAll(paths.Long(obj), 0o700))

	got, err := tp.Store.GetChunk(ctx, h)
	require.ErrorIs(t, err, ErrDamaged)
	require.Empty(t, got)
	fi, statErr := os.Lstat(paths.Long(obj))
	require.NoError(t, statErr, "a refused leaf that was never read must stay where it is")
	require.True(t, fi.IsDir())
}
