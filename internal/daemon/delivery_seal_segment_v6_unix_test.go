//go:build unix

package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestDeliverySealSegment_SymlinkedSegmentDirRefused completes the fix for main's #3 on the platforms
// where symlinks are reachable: a segment directory replaced by a STATIC symlink alias is rejected by
// the pinned-child identity check (its ModeSymlink guard) before any reader follows it, even though the
// alias target holds a valid-looking segment.
func TestDeliverySealSegment_SymlinkedSegmentDirRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	active, _ := buildRotatedStore(t, root, "s", 6, false)
	state := paths.Of(root).State

	dir := filepath.Join(state, deliverySegmentsDir, segmentSeqName(active))
	aside := dir + ".real"
	require.NoError(t, os.Rename(paths.Long(dir), paths.Long(aside)))
	require.NoError(t, os.Symlink(aside, dir))

	_, err := checkSeal(t, root)
	require.Error(t, err, "a symlinked segment directory is a static alias and is refused")
}
