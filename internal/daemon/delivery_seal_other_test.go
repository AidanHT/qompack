//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// requireOwnerOnlyMode asserts that p carries the permissions savePosition gives a v1 position
// sidecar, 0o600. paths.WriteAtomic chmods its staged file to the mode it was handed before the
// rename, so the mode on disk is that mode exactly, not one a umask narrowed. Windows has no POSIX
// mode bits to assert, and its half of this helper asserts nothing.
func requireOwnerOnlyMode(t *testing.T, p string) {
	t.Helper()
	info, err := os.Lstat(p)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "%s must be readable and writable by its owner alone", p)
}

// requireNoOpenHandle asserts that this process holds no descriptor on p. Linux lists a process's
// descriptors in /proc/self/fd, each a link to the file it holds. darwin has no such listing, and
// the assertion is not made there. p must exist.
func requireNoOpenHandle(t *testing.T, p string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	want, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	entries, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)
	for _, e := range entries {
		held, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err != nil {
			continue // the descriptor ReadDir itself used, closed by now
		}
		require.NotEqual(t, want, held, "descriptor %s still holds %s", e.Name(), p)
	}
}
