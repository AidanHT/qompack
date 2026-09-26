package paths_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// TestWriteAtomic_ACaseVariantStoreNameIsStillTheStoreOnWindows keeps the guard where it was on a
// filesystem that folds case: Windows resolves .QOMPACK to the store, and filepath.Rel folds case
// there too, so the protected files under that spelling were refused before and must still be.
func TestWriteAtomic_ACaseVariantStoreNameIsStillTheStoreOnWindows(t *testing.T) {
	l := newLayout(t)
	cp := paths.CheckpointPath(l, 1)
	require.NoError(t, paths.CreateNew(cp, []byte(`{"seq":1}`)))
	variant := filepath.Join(l.Root, ".QOMPACK", "checkpoints", filepath.Base(cp))

	require.ErrorIs(t, paths.WriteAtomic(variant, []byte(`{"seq":1,"tampered":true}`), 0o600), core.ErrAppendOnly)

	got, err := os.ReadFile(cp)
	require.NoError(t, err)
	require.Equal(t, `{"seq":1}`, string(got))
}
