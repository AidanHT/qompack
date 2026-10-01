//go:build linux

package hostperm

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// These rows stage source and link shapes on Linux: a managed drop-in directory that is a regular
// file (ENOTDIR, which Windows reports as not found), a settings file that is a link to itself, a
// cycle of directory links, and a relative directory link. They are Linux-only because that is
// where they are verified; creating a symlink on Windows needs privilege.

func TestASettingsFileThatCannotBeExaminedFailsClosed(t *testing.T) {
	e := newDiskEnv(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(e.project()), 0o700))
	// A link to itself exists but cannot be examined: stat fails with ELOOP, not ENOENT.
	require.NoError(t, os.Symlink(e.project(), e.project()))
	_, serr := os.Stat(e.project())
	require.ErrorIs(t, serr, syscall.ELOOP, "fixture: the settings path must loop")

	_, err := e.pol.Check(filepath.Join(e.root, "a.txt"))
	require.ErrorIs(t, err, ErrUnavailable)
	require.ErrorContains(t, err, e.project())
	var transient *transientError
	require.ErrorAs(t, err, &transient, "a source that cannot be examined is reported, never cached")

	require.NoError(t, os.Remove(e.project()))
	e.write(t, e.project(), `{"permissions":{"deny":["Read(./a.txt)"]}}`)
	require.Equal(t, Deny, e.check(t, "a.txt"), "the next request sees the repaired file")
}

func TestALinkCycleInARulesPathNeitherHangsNorDropsTheRule(t *testing.T) {
	e := newDiskEnv(t)
	a, b := filepath.Join(e.home, "a"), filepath.Join(e.home, "b")
	require.NoError(t, os.Symlink(b, a))
	require.NoError(t, os.Symlink(a, b))
	e.write(t, e.user(), `{"permissions":{"deny":["Read(~/a/**)"]}}`)

	// The rule's own directory resolves nowhere, so it gains no alias, but it still applies as
	// written, and a path through the cycle is judged by its own spelling.
	rs, err := e.pol.Snapshot()
	require.NoError(t, err)
	require.Len(t, rs.deny, 1)
	require.Len(t, rs.deny[0].patterns, 1, "a rule whose directory cannot be resolved gains no link alias")
	_, ok := resolveLinks(filepath.Join(a, "x"), nil)
	require.False(t, ok, "fixture: the cycle must exceed maxLinkHops")

	d, err := e.pol.Check(filepath.Join(a, "x"))
	require.NoError(t, err)
	require.Equal(t, Deny, d.Effect)
	d, err = e.pol.Check(filepath.Join(e.home, "c", "x"))
	require.NoError(t, err)
	require.Equal(t, Allow, d.Effect)
}

func TestARuleThroughARelativeDirectoryLinkAppliesAtItsRealLocation(t *testing.T) {
	e := newDiskEnv(t)
	real := filepath.Join(e.home, "real-notes")
	require.NoError(t, os.MkdirAll(real, 0o700))
	// The link's target is relative: it is resolved against the link's own directory.
	require.NoError(t, os.Symlink("real-notes", filepath.Join(e.home, "notes")))
	e.write(t, e.user(), `{"permissions":{"deny":["Read(~/notes/**)"]}}`)

	got, ok := resolveLinks(filepath.Join(e.home, "notes", "diary.md"), nil)
	require.True(t, ok)
	require.Equal(t, filepath.Join(real, "diary.md"), got)

	d, err := e.pol.Check(filepath.Join(real, "diary.md"))
	require.NoError(t, err)
	require.Equal(t, Deny, d.Effect)
	require.Equal(t, e.user(), d.Source)
	d, err = e.pol.Check(filepath.Join(e.home, "other", "diary.md"))
	require.NoError(t, err)
	require.Equal(t, Allow, d.Effect)
}

func TestAManagedDropInDirectoryThatCannotBeListedFailsClosed(t *testing.T) {
	e := newDiskEnv(t)
	d := filepath.Join(e.managed, "managed-settings.d")
	// A regular file where the drop-in directory belongs exists but cannot be listed (ENOTDIR):
	// whatever policy it was meant to hold cannot be read, so nothing is allowed on its account.
	// Windows reports the same shape as a path that is not found, which reads as no drop-ins.
	e.write(t, d, `{"permissions":{"deny":["Read"]}}`)
	_, lerr := os.ReadDir(d)
	require.ErrorIs(t, lerr, syscall.ENOTDIR, "fixture: the drop-in path must not be listable")
	_, err := e.pol.Check(filepath.Join(e.root, "a.txt"))
	require.ErrorIs(t, err, ErrUnavailable)
	require.ErrorContains(t, err, d)
	var transient *transientError
	require.ErrorAs(t, err, &transient, "a listing failure may be passing, so it is not cached")

	require.NoError(t, os.Remove(d))
	require.NoError(t, os.MkdirAll(d, 0o700))
	e.write(t, filepath.Join(d, "10-a.json"), `{"permissions":{"deny":["Read(./a.txt)"]}}`)
	require.Equal(t, Deny, e.check(t, "a.txt"), "the next request lists the directory once it is one")
	require.Equal(t, Allow, e.check(t, "b.txt"))
}
