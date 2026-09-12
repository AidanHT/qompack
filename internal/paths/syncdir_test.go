package paths_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestSyncDir_SyncsAnExistingDirectory: SyncDir succeeds on a directory that exists, on every platform.
func TestSyncDir_SyncsAnExistingDirectory(t *testing.T) {
	require.NoError(t, paths.SyncDir(t.TempDir()))
}

// TestSyncDir_ReportsAMissingDirectoryOffWindows: off Windows, SyncDir opens the directory to fsync
// it, so one that does not exist is an error rather than a silent success. On Windows it is a no-op
// (fsyncDir says why), so it opens nothing and reports nothing.
func TestSyncDir_ReportsAMissingDirectoryOffWindows(t *testing.T) {
	err := paths.SyncDir(filepath.Join(t.TempDir(), "missing"))
	if runtime.GOOS == "windows" {
		require.NoError(t, err, "a no-op on Windows")
		return
	}
	require.Error(t, err)
}
