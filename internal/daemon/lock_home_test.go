package daemon

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// TestAcquireLock_RefusesTheHomeDirectory pins D18's daemon-side gate. Every daemon start and every
// writer lease passes through AcquireLock, so a root that is the user's home directory — whose
// .qompack is the user-global layer — gets no lock, no run/ directory and no daemon, whoever the
// embedder is; internal/cli refuses the same root first and says why. A project below the home
// still locks, and so does the same home spelled through the process's other home variable.
//
// The home is a fake one under t.TempDir(), named through HOME and USERPROFILE, which is where the
// daemon reads its home from (userHomeDir, the D10 staging). Not parallel: t.Setenv.
func TestAcquireLock_RefusesTheHomeDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(filepath.Join(paths.Global(home), "bin"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(paths.Global(home), "config.json"), []byte("{}\n"), 0o600))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	clk := newFakeClock(epoch)

	addr, err := ipc.Resolve(home)
	require.NoError(t, err)
	lk, err := AcquireLock(home, addr, clk)
	require.ErrorIs(t, err, paths.ErrHomeRoot)
	require.Nil(t, lk)
	_, statErr := os.Stat(paths.Of(home).Run)
	require.ErrorIs(t, statErr, fs.ErrNotExist, "no run/ in the user-global layer's directory")

	// Run takes the same gate, so a daemon constructed for the home directory starts nothing.
	d, err := New(Options{ProjectRoot: home, Log: logging.Nop(), Clock: clk})
	require.NoError(t, err)
	require.ErrorIs(t, d.Run(context.Background()), paths.ErrHomeRoot)
	_, statErr = os.Stat(paths.Of(home).Run)
	require.ErrorIs(t, statErr, fs.ErrNotExist, "Run created nothing either")

	// Only HOME names it now: USERPROFILE points elsewhere, and the refusal still holds.
	t.Setenv("USERPROFILE", t.TempDir())
	_, err = AcquireLock(home, addr, clk)
	require.ErrorIs(t, err, paths.ErrHomeRoot, "either home variable names the home directory")

	proj := filepath.Join(home, "proj")
	require.NoError(t, os.MkdirAll(proj, 0o700))
	projAddr, err := ipc.Resolve(proj)
	require.NoError(t, err)
	lk, err = AcquireLock(proj, projAddr, clk)
	require.NoError(t, err, "a project below the home directory still locks")
	require.NoError(t, lk.Release())
}
