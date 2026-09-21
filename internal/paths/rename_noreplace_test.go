package paths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenameDirectoryNoReplace(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "stage"), filepath.Join(root, "destination")
	require.NoError(t, os.Mkdir(src, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(src, "proof"), []byte("retained"), 0o600))
	require.NoError(t, os.Mkdir(dst, 0o700))
	// This is the important POSIX case: ordinary rename would replace this
	// empty directory even after the caller's earlier absence check passed.
	require.Error(t, RenameDirectoryNoReplace(src, dst))
	b, err := os.ReadFile(filepath.Join(src, "proof"))
	require.NoError(t, err)
	require.Equal(t, "retained", string(b))
	require.NoError(t, os.Remove(dst))
	require.NoError(t, RenameDirectoryNoReplace(src, dst))
	b, err = os.ReadFile(filepath.Join(dst, "proof"))
	require.NoError(t, err)
	require.Equal(t, "retained", string(b))
}
