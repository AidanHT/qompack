//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

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
