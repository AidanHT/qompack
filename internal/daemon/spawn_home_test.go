package daemon

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// Owner decision D18 keeps <home>/.qompack the user-global layer only. internal/cli refuses a
// project root that is the home directory before it starts a daemon, and a daemon refuses it at its
// lock (AcquireLock). EnsureRunning and SpawnDetached refuse it themselves as well (V6 close-out
// w6-borrow), so a future caller that skips the entry check still claims no spawn lock, stages no
// binary and starts no process for it. Not parallel: t.Setenv.

// spawnerHome makes a fake home directory holding a user-global layer, names it through HOME and
// USERPROFILE — the homes the spawners and the lock read — and returns it.
func spawnerHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(filepath.Join(paths.Global(home), "bin"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(paths.Global(home), "config.json"), []byte("{}\n"), 0o600))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// spawnerHomeTree lists every path under home — every file with its size and modification time —
// so a row can prove nothing under it was created, removed or rewritten. A directory is listed by
// name only: NTFS updates a directory's own modification time lazily, after the entries inside it.
func spawnerHomeTree(t *testing.T, home string) []string {
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

// TestEnsureRunning_RefusesTheHomeDirectory: EnsureRunning, and session-start's EnsureRunningUntil,
// refuse the home directory with paths.ErrHomeRoot before they dial, claim or spawn. A project
// below the home directory still gets its daemon.
func TestEnsureRunning_RefusesTheHomeDirectory(t *testing.T) {
	home := spawnerHome(t)
	before := spawnerHomeTree(t, home)
	clk := newFakeClock(epoch)
	f := newFakeDaemons(t)
	missing := filepath.Join(t.TempDir(), "never-spawned")

	spawned, err := ensureRunning(home, "self", logging.Nop(), clk, pollBound{after: spawnLockTestBound}, f.spawnUp)
	require.ErrorIs(t, err, paths.ErrHomeRoot)
	require.False(t, spawned)
	require.Zero(t, f.calls.Load(), "nothing is spawned for the home directory")

	spawned, err = EnsureRunning(home, missing, logging.Nop(), clk)
	require.ErrorIs(t, err, paths.ErrHomeRoot)
	require.False(t, spawned)
	now := time.Now()
	spawned, err = EnsureRunningUntil(home, missing, logging.Nop(), clk, now.Add(time.Second), now.Add(2*time.Second))
	require.ErrorIs(t, err, paths.ErrHomeRoot)
	require.False(t, spawned)

	require.NoDirExists(t, paths.Of(home).Run, "no spawn.lock in the user-global layer's directory")
	require.Equal(t, before, spawnerHomeTree(t, home), "nothing under the home directory changed")

	proj := filepath.Join(home, "proj")
	require.NoError(t, os.MkdirAll(proj, 0o700))
	spawned, err = ensureRunning(proj, "self", logging.Nop(), clk, pollBound{after: spawnLockTestBound}, f.spawnUp)
	require.NoError(t, err, "a project below the home directory still gets its daemon")
	require.True(t, spawned)
	require.EqualValues(t, 1, f.calls.Load())
}

// TestSpawnDetached_RefusesTheHomeDirectory: SpawnDetached starts no process for the home directory
// and stages nothing, answering paths.ErrHomeRoot. For a project below the home directory it goes
// on to start the program, which here does not exist, so it fails for that reason instead.
func TestSpawnDetached_RefusesTheHomeDirectory(t *testing.T) {
	home := spawnerHome(t)
	before := spawnerHomeTree(t, home)
	missing := filepath.Join(t.TempDir(), "never-spawned")

	require.ErrorIs(t, SpawnDetached(home, missing), paths.ErrHomeRoot)
	require.Equal(t, before, spawnerHomeTree(t, home), "nothing under the home directory changed")

	proj := filepath.Join(home, "proj")
	require.NoError(t, os.MkdirAll(proj, 0o700))
	err := SpawnDetached(proj, missing)
	require.Error(t, err, "the program does not exist")
	require.NotErrorIs(t, err, paths.ErrHomeRoot, "a project below the home directory is not refused")
}
