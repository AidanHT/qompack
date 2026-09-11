package daemon

import (
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestNewDrainerIssuesTheRealSyncsByDefault pins what a drainer's two sync seams hold when no test
// replaces them. The tests of the F6 rule see the drain's syncs through syncHandle and syncDir, so a
// default that did nothing, one that synced no spool file's bytes or no spool directory, would pass
// every one of them (review 1, R2 and R7). Function values compare only to nil, so the check compares
// their code pointers.
func TestNewDrainerIssuesTheRealSyncsByDefault(t *testing.T) {
	dr := newDrainer(DrainConfig{Root: t.TempDir()})
	require.Equal(t, reflect.ValueOf((*os.File).Sync).Pointer(), reflect.ValueOf(dr.syncHandle).Pointer(),
		"the fsync a drainer issues on its handle of a spool file is (*os.File).Sync")
	require.Equal(t, reflect.ValueOf(paths.SyncDir).Pointer(), reflect.ValueOf(dr.syncDir).Pointer(),
		"the sync a drainer issues on the spool directory is paths.SyncDir")
}
