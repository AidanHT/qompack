package ipc

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Owner decision D18 keeps <home>/.qompack the user-global layer only. Every entry point in
// internal/cli refuses a project root that is the home directory before it spawns anything, and a
// spawned daemon refuses it again at its lock. The spawn claim and the lazy spawn refuse it too
// (V6 close-out w6-borrow), so a future caller that skips the entry check still creates no
// run/spawn.lock and starts no daemon there. Not parallel: t.Setenv.

// spawnHome makes a fake home directory holding a user-global layer, names it through HOME and
// USERPROFILE — the homes the spawners read — and returns it.
func spawnHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(filepath.Join(paths.Global(home), "bin"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(paths.Global(home), "config.json"), []byte("{}\n"), 0o600))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// homeTree lists every path under home — every file with its size and modification time — so a
// row can prove nothing under it was created, removed or rewritten. A directory is listed by name
// only: NTFS updates a directory's own modification time lazily, after the entries inside it.
func homeTree(t *testing.T, home string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(home, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			out = append(out, rel+string(filepath.Separator))
			return nil
		}
		out = append(out, fmt.Sprintf("%s %d %s", rel, info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano)))
		return nil
	}))
	return out
}

// TestClaimSpawn_RefusesTheHomeDirectory: no spawn claim is taken for the home directory — not even
// its run/ directory is created — and the claim reports that it cannot be taken, which makes a
// hook's lazy spawn give up quietly. A project below the home directory still claims.
func TestClaimSpawn_RefusesTheHomeDirectory(t *testing.T) {
	home := spawnHome(t)
	before := homeTree(t, home)

	lock, claim := ClaimSpawn(home, core.SystemClock())
	require.Nil(t, lock)
	require.Equal(t, SpawnUnclaimable, claim, "the home directory's spawn claim cannot be taken")
	require.NoDirExists(t, paths.Of(home).Run, "no run/ in the user-global layer's directory")
	require.Equal(t, before, homeTree(t, home), "nothing under the home directory changed")

	proj := filepath.Join(home, "proj")
	require.NoError(t, os.MkdirAll(proj, 0o700))
	lock, claim = ClaimSpawn(proj, core.SystemClock())
	require.Equal(t, SpawnClaimed, claim, "a project below the home directory still claims")
	lock.Release()
}

// TestLazySpawn_RefusesTheHomeDirectory: a client whose project root is the home directory, and
// whose daemon is unreachable, spools as ever but spawns nothing and creates nothing under the home
// directory. The same client for a project below it spawns.
func TestLazySpawn_RefusesTheHomeDirectory(t *testing.T) {
	home := spawnHome(t)
	before := homeTree(t, home)

	n := sendOnceForLazySpawn(t, home, func(string, string) error { return nil })
	require.Zero(t, n, "no daemon is spawned for the home directory")
	require.NoDirExists(t, paths.Of(home).Run, "no spawn.lock in the user-global layer's directory")
	require.Equal(t, before, homeTree(t, home), "nothing under the home directory changed")

	proj := filepath.Join(home, "proj")
	require.NoError(t, os.MkdirAll(proj, 0o700))
	require.EqualValues(t, 1, sendOnceForLazySpawn(t, proj, func(string, string) error { return nil }),
		"a project below the home directory still spawns its daemon")
}
