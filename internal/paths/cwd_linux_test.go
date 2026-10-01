//go:build linux

package paths_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestRelativePaths_AnUnresolvableWorkingDirectoryResolvesNothing: a relative path is resolved against
// the working directory, and on Linux a process whose working directory was removed has none
// (getcwd answers ENOENT). Resolve then reports the error rather than guessing a root, IsHome answers
// false rather than matching some other spelling, and OpenFile's protection check, unable to place
// the path in any store, leaves the open to fail on its own (linux only: Windows will not remove a
// directory a process is using as its working directory).
func TestRelativePaths_AnUnresolvableWorkingDirectoryResolvesNothing(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	require.NoError(t, os.Mkdir(gone, 0o700))
	t.Chdir(gone)
	require.NoError(t, os.Remove(gone))
	_, err := os.Getwd()
	require.Error(t, err, "fixture: a removed working directory has no path")

	_, err = paths.Resolve(func(string) string { return "" }, "sub")
	require.Error(t, err, "a relative payload cwd that cannot be made absolute is not a project root")
	require.False(t, paths.IsHome("sub", gone))
	_, err = paths.OpenFile(filepath.Join("checkpoints", "MANIFEST.jsonl"), os.O_RDONLY, 0)
	require.Error(t, err)
}
