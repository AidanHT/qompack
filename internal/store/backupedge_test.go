package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// The backup surface's refusals: the watched-file list a daemon checks itself against, and the
// manifests VerifyBackup will not stand behind.

// TestBackupWatchedFiles_NamesTheDeliveryStateThisPackageWrites is the test BackupWatchedFiles' own
// doc comment names.
//
// internal/daemon owns these four filenames as constants and store has to spell them as literals,
// because daemon imports store and store cannot import daemon back. Nothing else holds the two
// spellings together: a daemon-side rename would take refuseIfTheProjectMoved's
// "not copied and not there → continue" branch for both seal sidecars, turn the R10 guard into a
// silent no-op for the very files it exists for, and leave every test in this package passing,
// since its fixtures write the literals themselves. So the list is checked from here for shape, and
// handed out as a COPY — a caller must not be able to shorten the list it is checking itself
// against.
func TestBackupWatchedFiles_NamesTheDeliveryStateThisPackageWrites(t *testing.T) {
	t.Parallel()

	got := BackupWatchedFiles()
	require.Len(t, got, 4, "two delivery journals and two seal sidecars")
	for _, name := range got {
		require.True(t, strings.HasPrefix(name, "state/"),
			"%q is not slash-relative state under .qompack", name)
		require.NotContains(t, name, `\`, "the names are slash-relative, not platform paths")
	}

	got[0] = "state/nothing.json"
	got = got[:1]
	require.Equal(t, 4, len(BackupWatchedFiles()), "the list a caller mutated is not the list")
	require.NotContains(t, BackupWatchedFiles(), "state/nothing.json")
}

// TestVerifyBackup_RefusesEveryManifestItCannotStandBehind.
//
// A backup is rollback material and verification is the only thing standing between an operator and
// restoring a tree that cannot be opened, so every way the manifest itself can be unusable is a
// refusal rather than a partial answer: no id to look up, no manifest there, a manifest that will
// not parse, and one written at a version this build has no reader for. The last is the one worth
// naming — silently reading a future manifest with today's field set is how a restore loses files
// the manifest knew about.
func TestVerifyBackup_RefusesEveryManifestItCannotStandBehind(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(1))
	ctx := context.Background()
	_, err := m.TakeBackup(ctx, "b1")
	require.NoError(t, err)

	_, err = m.VerifyBackup("")
	require.ErrorContains(t, err, "needs a backup id")

	_, err = m.VerifyBackup("no-such-backup")
	require.ErrorContains(t, err, "manifest")

	manifest := filepath.Join(m.backupDir("b1"), backupManifestFile)
	require.NoError(t, os.WriteFile(paths.Long(manifest), []byte("{not json"), 0o600))
	_, err = m.VerifyBackup("b1")
	require.ErrorContains(t, err, "parse backup")

	future, err := json.Marshal(BackupManifest{Version: backupManifestVersion + 1, ID: "b1"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(manifest), future, 0o600))
	_, err = m.VerifyBackup("b1")
	require.ErrorContains(t, err, "is not readable by this build")
}

// TestTakeBackup_NeedsAnId. The id names the directory the copy lands in and the one an operator
// types back to verify or restore it, so an empty one is refused before the writer is quiesced —
// a backup nobody can name is worse than no backup, and quiescing the writer to produce one would
// stop recording for nothing.
func TestTakeBackup_NeedsAnId(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(1))

	_, err := m.TakeBackup(context.Background(), "")
	require.ErrorContains(t, err, "needs a backup id")

	entries, rerr := os.ReadDir(paths.Long(m.l.Backup))
	if rerr == nil {
		require.Empty(t, entries, "the refused backup created no directory")
	}
}
