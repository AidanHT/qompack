package paths_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// The error branches below are the ones that separate "this directory is not there yet, which is
// normal" from "this directory answered something unexpected, which is not". Every one of them was
// unexecuted until the first CI run on a POSIX host measured internal/paths against its
// 00-ARCHITECTURE.md §6.4 floor and found 89.1% — the floor had only ever been evaluated on
// Windows, where long_windows.go's extra statements and fsyncDir's early return put the same
// package at 91.6%.
//
// They are provoked with ENOTDIR and ELOOP rather than with permissions, deliberately: a chmod-based
// fixture is a no-op for a process running as root, so it would pass by not testing anything
// wherever the suite runs privileged. Both errors below are permission-independent, so these cases
// mean the same thing for every user.

// TestHighestBloomBackupSeq_ReportsAnUnexpectedReadDirError asserts the seam between the two
// failure modes: a sketches directory that does not exist yet is not an error (a project before
// its first bloom rebuild), while a sketches path that exists and is not a directory is. Reading
// the second as the first would report "no backups" for a corrupted layout and let ReplaceBloom
// overwrite a generation it should have preserved.
func TestHighestBloomBackupSeq_ReportsAnUnexpectedReadDirError(t *testing.T) {
	dir := t.TempDir()

	// Absent: not an error, and not a backup either.
	l := paths.Of(dir)
	seq, ok, err := paths.HighestBloomBackupSeq(l)
	require.NoError(t, err, "a sketches directory that does not exist yet is a normal empty project")
	require.False(t, ok)
	require.Zero(t, seq)

	// Present but not a directory: an error, not a silent "no backups".
	//
	// POSIX only, and the reason is a divergence worth naming rather than hiding: os.ReadDir on a
	// regular file fails with ENOTDIR on Linux and macOS, but on Windows it returns an EMPTY
	// listing and no error at all. So on Windows this corruption really does read as "no backups",
	// and the guard below cannot be asserted there. The behaviour is the standard library's, not
	// this package's, and the branch it makes unreachable on Windows is reachable on the two
	// platforms that matter for it.
	if runtime.GOOS == "windows" {
		t.Skip("platform: os.ReadDir on a regular file returns an empty listing rather than ENOTDIR on " +
			"Windows, so the unexpected-error branch cannot be provoked here")
	}
	notADir := filepath.Join(dir, "sketches-is-a-file")
	require.NoError(t, os.WriteFile(notADir, []byte("not a directory"), 0o600))

	l.Sketches = notADir
	_, ok, err = paths.HighestBloomBackupSeq(l)
	require.Error(t, err, "a sketches path that is not a directory must be reported, not read as empty")
	require.False(t, ok)
}

// TestReadManifest_ReportsAnUnexpectedOpenError asserts the same seam for the checkpoint manifest:
// a manifest that has never been written is (nil, nil), because a project with no checkpoints has
// nothing to verify, while a manifest path that cannot be opened for any OTHER reason is an error.
// Collapsing the two would make `qompack fsck` report a clean tree for one it could not read.
func TestReadManifest_ReportsAnUnexpectedOpenError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("platform: Windows maps a traversal through a non-directory to ERROR_PATH_NOT_FOUND, " +
			"which os.IsNotExist reports as absent — the branch under test is POSIX's ENOTDIR")
	}
	dir := t.TempDir()

	l := paths.Of(dir)
	entries, err := paths.ReadManifest(l)
	require.NoError(t, err, "a project with no checkpoints has no manifest and that is not an error")
	require.Nil(t, entries)

	// A checkpoints path that is a regular file makes the open of MANIFEST.jsonl underneath it
	// fail with ENOTDIR, which is not os.IsNotExist.
	notADir := filepath.Join(dir, "checkpoints-is-a-file")
	require.NoError(t, os.WriteFile(notADir, []byte("not a directory"), 0o600))

	l.Checkpoints = notADir
	_, err = paths.ReadManifest(l)
	require.Error(t, err, "a manifest path that cannot be opened must be reported, not read as absent")
	require.False(t, os.IsNotExist(err), "the branch under test is the one IsNotExist does not take")
}

// TestEnsureLayout_ReportsAnUnexpectedStatError asserts that EnsureLayout distinguishes "the
// .gitignore is not written yet, write it" from "stat answered something else". The second arm
// matters because EnsureLayout's whole job is to leave a usable layout behind: swallowing the
// error would return success on a project whose .qompack/.gitignore is unreadable, and §7's
// guarantee that the store never lands in a caller's git index rests on that file existing.
func TestEnsureLayout_ReportsAnUnexpectedStatError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("platform: creating a symlink needs a privilege ordinary Windows processes lack, so the " +
			"ELOOP this case turns on cannot be staged here")
	}
	dir := t.TempDir()
	l := paths.Of(dir)

	// A clean layout first: this is the arm that writes the file.
	require.NoError(t, paths.EnsureLayout(l))
	require.FileExists(t, filepath.Join(l.Dot, ".gitignore"))

	// Replace it with a symlink to itself. os.Stat then fails with ELOOP, which is neither success
	// nor os.IsNotExist, so EnsureLayout must surface it rather than treating it as "absent" and
	// overwriting, or as "present" and returning nil.
	gitignore := filepath.Join(l.Dot, ".gitignore")
	require.NoError(t, os.Remove(gitignore))
	require.NoError(t, os.Symlink(gitignore, gitignore))

	err := paths.EnsureLayout(l)
	require.Error(t, err, "a .gitignore whose stat fails must fail EnsureLayout")
	require.Contains(t, err.Error(), "EnsureLayout", "the error must name the operation that failed")
}
