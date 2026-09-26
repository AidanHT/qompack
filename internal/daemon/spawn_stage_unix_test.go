//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestStageBinary_ReplacesASymlinkAtTheTarget: a symbolic link standing where the staged copy
// belongs — which could point anywhere — is removed and replaced by a verified copy; the file it
// pointed at is left untouched and never executed. (Creating one needs no privilege here; on
// Windows it does, and a directory stands in for it in TestStageBinary_ReplacesANonFileAtTheTarget.)
func TestStageBinary_ReplacesASymlinkAtTheTarget(t *testing.T) {
	t.Parallel()
	self, home := fakeSelf(t), t.TempDir()
	sum, err := fileSHA256(self)
	require.NoError(t, err)
	target := filepath.Join(paths.Global(home), stagedBinDir, sum, stagedBinaryName+filepath.Ext(self))
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o700))
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	require.NoError(t, os.WriteFile(elsewhere, []byte("someone else's program"), 0o700))
	require.NoError(t, os.Symlink(elsewhere, target))

	staged, err := stageBinary(self, home)
	require.NoError(t, err)
	fi, err := os.Lstat(staged)
	require.NoError(t, err)
	require.True(t, fi.Mode().IsRegular(), "the link was replaced by a real copy")
	requireSameBytes(t, self, staged)
	b, err := os.ReadFile(elsewhere)
	require.NoError(t, err)
	require.Equal(t, "someone else's program", string(b), "the link's target is not touched")
}
