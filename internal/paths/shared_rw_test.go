package paths_test

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// sharedRWFixture is the content every OpenSharedRW test starts from, and sharedRWPatch what it
// writes over sharedRWPatchAt bytes into it.
const (
	sharedRWFixture = "0123456789"
	sharedRWPatch   = "AB"
	sharedRWPatchAt = 3
	sharedRWPatched = "012AB56789"
)

// heldSharedRW writes sharedRWFixture to a fresh file under l.State and returns its path and a
// handle on it from paths.OpenSharedRW, closed when the test ends.
func heldSharedRW(t *testing.T, l paths.Layout) (string, *os.File) {
	t.Helper()
	target := filepath.Join(l.State, "seal.bin")
	require.NoError(t, os.WriteFile(target, []byte(sharedRWFixture), 0o600))
	held, err := paths.OpenSharedRW(target)
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })
	return target, held
}

// writePatch writes sharedRWPatch through f in place and syncs it with SyncData.
func writePatch(t *testing.T, f *os.File) {
	t.Helper()
	n, err := f.WriteAt([]byte(sharedRWPatch), sharedRWPatchAt)
	require.NoError(t, err)
	require.Equal(t, len(sharedRWPatch), n)
	require.NoError(t, paths.SyncData(f))
}

// TestOpenSharedRW_ReadersStillReadTheHeldFile pins that a held read-write handle obstructs no
// reader. ReadFileShared and a plain os.Open both open the file while it is held, and both see
// what the handle then overwrites in place, which leaves the size alone.
func TestOpenSharedRW_ReadersStillReadTheHeldFile(t *testing.T) {
	target, held := heldSharedRW(t, newLayout(t))

	got, err := paths.ReadFileShared(target)
	require.NoError(t, err)
	require.Equal(t, sharedRWFixture, string(got))

	plain, err := os.Open(target)
	require.NoError(t, err, "os.Open's share mode must still admit a reader beside the held handle")
	defer func() { _ = plain.Close() }()

	writePatch(t, held)

	got, err = paths.ReadFileShared(target)
	require.NoError(t, err)
	require.Equal(t, sharedRWPatched, string(got), "readers of the path see the in-place write")
	viaPlain, err := io.ReadAll(plain)
	require.NoError(t, err)
	require.Equal(t, sharedRWPatched, string(viaPlain), "a reader opened before the write sees it too")
	info, err := os.Lstat(target)
	require.NoError(t, err)
	require.EqualValues(t, len(sharedRWFixture), info.Size())
}

// TestOpenSharedRW_HeldFileCanBeRemovedAndItsNameReused pins the POSIX delete semantics the v2
// seal's tests rely on (SP20-D1 design risk R2). While the handle is held, os.Remove of the path
// succeeds and frees the name at once, so that os.Mkdir can take it; and the handle keeps writing
// and syncing the file it opened, which no path names any more.
func TestOpenSharedRW_HeldFileCanBeRemovedAndItsNameReused(t *testing.T) {
	target, held := heldSharedRW(t, newLayout(t))

	require.NoError(t, os.Remove(target), "FILE_SHARE_DELETE must let the path go while it is held")
	require.NoError(t, os.Mkdir(target, 0o700), "POSIX delete semantics free the name at once")

	writePatch(t, held)

	info, err := os.Lstat(target)
	require.NoError(t, err)
	require.True(t, info.IsDir(), "nothing the handle wrote reached the path")
	buf := make([]byte, len(sharedRWFixture))
	_, err = held.ReadAt(buf, 0)
	require.NoError(t, err)
	require.Equal(t, sharedRWPatched, string(buf), "the unlinked file took the write")
}

// TestOpenSharedRW_WriteAtomicReplacesTheHeldPath pins the other half. WriteAtomic's replace lands
// on a held path, after which the path names a different file from the one the handle holds
// (os.SameFile is false), the handle still reads its own bytes, and nothing it writes reaches the
// path. That is the state the v2 seal's post-seal identity check exists to catch (J-B5).
func TestOpenSharedRW_WriteAtomicReplacesTheHeldPath(t *testing.T) {
	const replacement = "the replacement"
	target, held := heldSharedRW(t, newLayout(t))
	heldInfo, err := held.Stat()
	require.NoError(t, err)
	before, err := os.Lstat(target)
	require.NoError(t, err)
	require.True(t, os.SameFile(before, heldInfo), "guard: before the replace the path names the held file")

	require.NoError(t, paths.WriteAtomic(target, []byte(replacement), 0o600),
		"a held read-write handle must not stall WriteAtomic's replace")

	after, err := os.Lstat(target)
	require.NoError(t, err)
	require.False(t, os.SameFile(after, heldInfo), "the path must now name a different file")

	buf := make([]byte, len(sharedRWFixture))
	_, err = held.ReadAt(buf, 0)
	require.NoError(t, err)
	require.Equal(t, sharedRWFixture, string(buf), "the handle keeps the file it opened")
	writePatch(t, held)
	landed, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, replacement, string(landed), "a write through the stale handle never reaches the path")
}

// TestOpenSharedRW_NeverCreatesAndRefusesWhatItMustNotWrite pins the refusals. A missing path fails
// with fs.ErrNotExist and is still missing afterwards, a directory is refused, and a §7.4
// protected path is refused with core.ErrAppendOnly and keeps its bytes.
func TestOpenSharedRW_NeverCreatesAndRefusesWhatItMustNotWrite(t *testing.T) {
	l := newLayout(t)

	t.Run("a missing path", func(t *testing.T) {
		missing := filepath.Join(l.State, "absent.bin")
		f, err := paths.OpenSharedRW(missing)
		require.ErrorIs(t, err, fs.ErrNotExist)
		require.Nil(t, f)
		_, err = os.Lstat(missing)
		require.ErrorIs(t, err, fs.ErrNotExist, "a failed open must create nothing")
	})

	t.Run("a directory", func(t *testing.T) {
		f, err := paths.OpenSharedRW(l.State)
		require.ErrorIs(t, err, syscall.EISDIR, "os.OpenFile's error for a directory, on every platform")
		require.Nil(t, f)
	})

	t.Run("a protected path", func(t *testing.T) {
		const content = "append-only\n"
		protected := filepath.Join(l.Checkpoints, "MANIFEST.jsonl")
		require.NoError(t, os.WriteFile(protected, []byte(content), 0o600))
		f, err := paths.OpenSharedRW(protected)
		require.ErrorIs(t, err, core.ErrAppendOnly)
		require.Nil(t, f)
		got, err := os.ReadFile(protected)
		require.NoError(t, err)
		require.Equal(t, content, string(got))
	})
}

// TestSyncData_FlushesAnInPlaceOverwrite pins SyncData's contract as far as this host can observe
// it: nil after an in-place overwrite that leaves the size alone, and an error, never a silent
// success, for a closed or a nil file.
func TestSyncData_FlushesAnInPlaceOverwrite(t *testing.T) {
	target, held := heldSharedRW(t, newLayout(t))
	writePatch(t, held)
	info, err := os.Lstat(target)
	require.NoError(t, err)
	require.EqualValues(t, len(sharedRWFixture), info.Size())

	require.NoError(t, held.Close())
	require.Error(t, paths.SyncData(held), "a closed file cannot be synced, and must not claim it was")
	require.ErrorIs(t, paths.SyncData(nil), os.ErrInvalid)
}
