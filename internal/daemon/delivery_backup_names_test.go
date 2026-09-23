package daemon

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// TestBackupWatchedFiles_NamesTheDeliveryStateThisPackageWrites holds internal/store's copy of this
// package's top-level delivery filenames to the constants they were copied from (SP20-D1 risk R10):
// the two journals, their two seals, and — since segmented rollover is on by default and a fresh open
// commits the active-0 segment authority (V6 close-out C1.10) — the authority's head and transition
// log, which store already watched as its SP20-D4 rows.
//
// store cannot import daemon — daemon imports store, and 00-ARCHITECTURE.md §3.2 forbids the cycle
// — so store names these files as string literals. That is the sanctioned shape, and it is also a
// duplicated fact with nothing holding the two copies together: rename either sidecar here and
// refuseIfTheProjectMoved's loop takes its "not copied, and still absent" branch for both, the
// consistency guard becomes a no-op for exactly the files it exists for, and every test in both
// packages still passes — store's own fixtures write the literals themselves, so they pin store's
// strings to store's strings.
//
// The assertion runs from this side because this side has the constants. It is written as
// containment rather than equality so that store may watch MORE than the delivery state later
// without this test having to be edited to permit it.
func TestBackupWatchedFiles_NamesTheDeliveryStateThisPackageWrites(t *testing.T) {
	t.Parallel()

	watched := store.BackupWatchedFiles()
	for _, name := range []string{
		"state/" + deliveryLeaseFile,
		"state/" + deliveryAckFile,
		"state/" + deliveryPositionFile,
		"state/" + deliveryAckPositionFile,
		"state/" + deliverySegmentHeadFile,
		"state/" + deliverySegmentLogFile,
	} {
		require.Contains(t, watched, name,
			"internal/store no longer watches %s. Either this package renamed it — in which case "+
				"internal/store/backup.go's literals must move with it, or TakeBackup goes on "+
				"recording a copy the daemon moved under as Consistent — or store dropped the row, "+
				"which is the same outcome by another route.", name)
	}

	// The other direction, and the one a constant rename cannot fake: the names above are the files
	// a real open actually leaves in state/. A fifth delivery file added here would show up as a
	// path this test does not know, which is the moment to decide whether store must watch it too.
	root := t.TempDir()
	_, _ = openSealFormat(t, root, 2)

	entries, err := os.ReadDir(paths.Long(paths.Of(root).State))
	require.NoError(t, err)
	var got []string
	for _, e := range entries {
		if !e.IsDir() {
			got = append(got, "state/"+filepath.ToSlash(e.Name()))
		}
	}
	sort.Strings(got)
	want := []string{
		"state/" + deliveryAckFile,
		"state/" + deliveryAckPositionFile,
		"state/" + deliveryLeaseFile,
		"state/" + deliveryPositionFile,
		"state/" + deliverySegmentHeadFile,
		"state/" + deliverySegmentLogFile,
	}
	sort.Strings(want)
	require.Equal(t, want, got,
		"a fresh format-2 open leaves exactly the delivery state store watches; a file added or "+
			"renamed here needs internal/store/backup.go revisited in the same change")
}
