package paths_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

func TestWriteAtomic_ReplacesAndSyncs(t *testing.T) {
	l := newLayout(t)
	target := filepath.Join(l.State, "thing.json")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o600))

	require.NoError(t, paths.WriteAtomic(target, []byte("new"), 0o600))

	b, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "new", string(b))

	entries, err := os.ReadDir(l.Tmp)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasPrefix(e.Name(), "wa-"), "leftover staging file: %s", e.Name())
	}
}

func TestWriteAtomic_CreatesWhenAbsent(t *testing.T) {
	l := newLayout(t)
	target := filepath.Join(l.State, "new.json")

	require.NoError(t, paths.WriteAtomic(target, []byte(`{"a":1}`), 0o600))

	b, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, `{"a":1}`, string(b))
}

func TestWriteAtomic_RefusesProtected(t *testing.T) {
	l := newLayout(t)
	cp := paths.CheckpointPath(l, 1)
	require.NoError(t, paths.CreateNew(cp, []byte(`{"seq":1}`)))

	err := paths.WriteAtomic(cp, []byte(`{"seq":1,"tampered":true}`), 0o600)
	require.ErrorIs(t, err, core.ErrAppendOnly)

	// The protected file itself must be untouched.
	b, err := os.ReadFile(cp)
	require.NoError(t, err)
	require.Equal(t, `{"seq":1}`, string(b))
}

func TestWriteAtomic_OutsideProjectFallsBackToDirLocalTmp(t *testing.T) {
	dir := t.TempDir() // no .qompack anywhere above it
	target := filepath.Join(dir, "f.txt")

	require.NoError(t, paths.WriteAtomic(target, []byte("x"), 0o600))

	b, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "x", string(b))
}

func TestWriteAtomic_RetriesOnReadOnlyTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "ro.txt")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o600))
	require.NoError(t, os.Chmod(target, 0o444))

	require.NoError(t, paths.WriteAtomic(target, []byte("new"), 0o600))

	b, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "new", string(b))
}

func TestWriteAtomic_MkdirAllFails(t *testing.T) {
	dir := t.TempDir() // no .qompack above it, so WriteAtomic stages into filepath.Dir(p)
	blocker := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("not a directory"), 0o600))

	// blocker is a file, so filepath.Dir(target) can never be created as a directory.
	target := filepath.Join(blocker, "file.txt")
	err := paths.WriteAtomic(target, []byte("x"), 0o600)
	require.Error(t, err)
}

func TestWriteAtomic_TargetIsExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.MkdirAll(target, 0o700))

	// Renaming a file onto an existing directory must fail even after the read-only retry, since
	// clearing a read-only attribute cannot turn a directory into a valid rename destination for
	// a file.
	err := paths.WriteAtomic(target, []byte("x"), 0o600)
	require.Error(t, err)

	fi, statErr := os.Stat(target)
	require.NoError(t, statErr)
	require.True(t, fi.IsDir(), "the directory must survive the failed WriteAtomic untouched")
}

func TestWriteAtomic_DestinationParentMissing(t *testing.T) {
	l := newLayout(t)
	// l.State exists, but "missing" under it does not: the rename destination's parent directory
	// is absent, so the finishing rename — and the retry's Chmod, since the destination does not
	// exist either — both fail.
	target := filepath.Join(l.State, "missing", "file.json")

	err := paths.WriteAtomic(target, []byte("x"), 0o600)
	require.Error(t, err)
}
