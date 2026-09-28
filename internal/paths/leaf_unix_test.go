//go:build unix

package paths_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestOpenSharedLeaf_AFIFOOpensWithoutWaiting pins O_NONBLOCK: a FIFO where a file is expected opens
// at once instead of blocking until some writer appears, and the caller's Stat of the handle then
// sees it is not a regular file. Without it, a config.json swapped for a FIFO after the caller's
// Lstat would hang a hook in open(2).
func TestOpenSharedLeaf_AFIFOOpensWithoutWaiting(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, syscall.Mkfifo(p, 0o600))

	f, err := paths.OpenSharedLeaf(p)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	require.NoError(t, err)
	require.Equal(t, os.ModeNamedPipe, fi.Mode().Type())
}
