package paths_test

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestOpenSharedLeaf_ReadsAPlainFile pins the ordinary case: a regular file opens and reads whole.
func TestOpenSharedLeaf_ReadsAPlainFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(p, []byte(`{"a":1}`), 0o600))

	f, err := paths.OpenSharedLeaf(p)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(f)
	require.NoError(t, err)
	require.Equal(t, `{"a":1}`, string(b))
}

// TestOpenSharedLeaf_MissingIsNotExist pins the error shape a caller branches on for "no file".
func TestOpenSharedLeaf_MissingIsNotExist(t *testing.T) {
	_, err := paths.OpenSharedLeaf(filepath.Join(t.TempDir(), "absent.json"))
	require.Error(t, err)
	require.True(t, errors.Is(err, fs.ErrNotExist), "a missing file must read as fs.ErrNotExist: %v", err)
	var pe *os.PathError
	require.True(t, errors.As(err, &pe), "errors keep os.Open's *os.PathError shape: %T", err)
}

// TestOpenSharedLeaf_RefusesAFinalLink is the half of the contract that replaces the old Lstat /
// os.SameFile identity check: a link at the final element is refused by the open itself, whatever
// it points to, so no swap between a check and the open can slip one through.
//
// makeFileLink makes a file symlink where the host allows one and, on a Windows host that does not,
// an NTFS junction (to a directory), which is a name-surrogate reparse point all the same.
func TestOpenSharedLeaf_RefusesAFinalLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere.json")
	require.NoError(t, os.WriteFile(target, []byte(`{"from":"a link"}`), 0o600))
	link := filepath.Join(dir, "config.json")
	require.NoError(t, makeFileLink(t, link, target))

	f, err := paths.OpenSharedLeaf(link)
	if f != nil {
		_ = f.Close()
	}
	require.Error(t, err, "a final link must never be followed")
	require.True(t, errors.Is(err, paths.ErrNotLeaf), "the refusal names itself: %v", err)
}

// TestOpenSharedLeaf_ADirectoryComesBackForTheCallerToJudge pins what the function does not do: it
// refuses links, not other kinds of file, so the caller's Stat of the handle is what refuses a
// directory.
func TestOpenSharedLeaf_ADirectoryComesBackForTheCallerToJudge(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.Mkdir(dir, 0o700))

	f, err := paths.OpenSharedLeaf(dir)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	require.NoError(t, err)
	require.True(t, fi.IsDir())
}

// TestOpenSharedLeaf_AReplaceLandsUnderAnOpenHandle is the delete-sharing half: a WriteAtomic replace
// of the file lands while the handle is open, and the handle keeps reading the bytes it opened. It
// is TestOpenSharedLetsWriteAtomicLandUnderAnOpenReader's assertion for this opener.
func TestOpenSharedLeaf_AReplaceLandsUnderAnOpenHandle(t *testing.T) {
	const before, after = "the version a reader opened", "the version an editor saved"
	l := newLayout(t)
	p := filepath.Join(l.Dot, "config.json")
	require.NoError(t, os.WriteFile(p, []byte(before), 0o600))

	f, err := paths.OpenSharedLeaf(p)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	require.NoError(t, paths.WriteAtomic(p, []byte(after), 0o600),
		"a POSIX-semantics replace must land while an OpenSharedLeaf handle is open")
	held, err := io.ReadAll(f)
	require.NoError(t, err)
	require.Equal(t, before, string(held), "the open handle keeps the file it opened")
	now, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, after, string(now), "and the path names the replacement")
}

// makeFileLink links link to target: a symlink where the host allows one, else (Windows only) an
// NTFS junction to target's directory.
func makeFileLink(t *testing.T, link, target string) error {
	t.Helper()
	if err := os.Symlink(target, link); err == nil || runtime.GOOS != "windows" {
		return err
	}
	return makeDirLink(link, filepath.Dir(target))
}
