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
// internal/daemon owns these filenames as constants and store has to spell them as literals, because
// daemon imports store and store cannot import daemon back. Nothing else holds the two spellings
// together: a daemon-side rename would take refuseIfTheProjectMoved's "not copied and not there →
// continue" branch, turn the R10 guard into a silent no-op for the very files it exists for, and leave
// every test in this package passing, since its fixtures write the literals themselves. So the list is
// checked from here for shape, and handed out as a COPY — a caller must not be able to shorten the list
// it is checking itself against.
//
// The set retains the four delivery journal/sidecar rows and adds the generation
// manifest/head and active-segment authority. Exact set membership and duplicate
// checks preserve the contract as these additive migration files are introduced.
func TestBackupWatchedFiles_NamesTheDeliveryStateThisPackageWrites(t *testing.T) {
	t.Parallel()

	want := []string{
		"state/delivery-leases.jsonl",
		"state/delivery-acks.jsonl",
		"state/delivery-lease-position.json",
		"state/delivery-ack-position.json",
		"state/delivery-generations/manifest.jsonl",
		"state/delivery-generations/manifest-head.json",
		"state/delivery-journal.json",
		"state/delivery-journal-log.jsonl",
	}
	got := BackupWatchedFiles()
	require.ElementsMatch(t, want, got, "original journals, generation frontier, and segment authority")

	seen := make(map[string]bool, len(got))
	for _, name := range got {
		require.False(t, seen[name], "%q is watched twice; the set must be duplicate-free", name)
		seen[name] = true
		require.True(t, strings.HasPrefix(name, "state/"),
			"%q is not slash-relative state under .qompack", name)
		require.NotContains(t, name, `\`, "the names are slash-relative, not platform paths")
	}
	require.Len(t, got, 8, "four original files, two generation files, and the segment authority log/head")

	got[0] = "state/nothing.json"
	got = got[:1]
	require.Len(t, got, 1, "the caller's truncated view is shorter")
	require.Equal(t, 8, len(BackupWatchedFiles()), "the list a caller mutated is not the list")
	require.NotContains(t, BackupWatchedFiles(), "state/nothing.json")
}

// TestBackup_RefusesGenerationStateThatMovedUnderTheCopy is the multi-file snapshot-consistency half
// of the SP20-D4 generation-store watch. The store is a directory (append-only manifest, atomic head,
// immutable content-addressed pages), and per-file atomicity does not make a tree copy of it
// consistent: if the daemon commits a generation between the walk capturing the store and the post-walk
// re-read, the head is rewritten and the manifest appended, so the copy holds one frontier's pages and
// another frontier's head. Watching the manifest and head catches exactly that — either moving under
// the copy refuses the backup. A store that is still under the walk copies consistently and verifies.
//
// The pages are not watched and need no watch: a page's name is its content hash, so a captured page
// always equals the live one and cannot move; only the manifest and head change on a commit.
func TestBackup_RefusesGenerationStateThatMovedUnderTheCopy(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	for _, tc := range []struct {
		name string
		move func(t *testing.T, genDir string)
	}{
		{
			name: "the atomic head rewritten under the walk (a new generation committed)",
			move: func(t *testing.T, genDir string) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(genDir, "manifest-head.json"),
					[]byte(`{"v":1,"seq":1,"log_bytes":120}`), 0o600))
			},
		},
		{
			name: "the manifest appended under the walk",
			move: func(t *testing.T, genDir string) {
				t.Helper()
				f, err := os.OpenFile(filepath.Join(genDir, "manifest.jsonl"), os.O_WRONLY|os.O_APPEND, 0o600)
				require.NoError(t, err)
				_, werr := f.Write([]byte("{\"v\":1,\"seq\":1}\n"))
				require.NoError(t, werr)
				require.NoError(t, f.Close())
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestStore(t)
			m := newMigrator(t, tp, legacySource(2))
			ctx := context.Background()
			_, err := m.Import(ctx)
			require.NoError(t, err)

			// Lay down a generation store as an enabled rollover would leave it: a manifest, its atomic
			// head, and the (empty here) pages directory.
			genDir := filepath.Join(paths.Of(tp.Root).State, "delivery-generations")
			require.NoError(t, os.MkdirAll(filepath.Join(genDir, "pages"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(genDir, "manifest.jsonl"),
				[]byte("{\"v\":1,\"seq\":0}\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(genDir, "manifest-head.json"),
				[]byte(`{"v":1,"seq":0,"log_bytes":15}`), 0o600))

			m.afterBackupWalk = func() { tc.move(t, genDir) }
			man, err := m.TakeBackup(ctx, "b1")
			require.ErrorIs(t, err, ErrBackupMoved,
				"a generation store that advanced under the walk is not a consistent snapshot")
			require.False(t, man.Consistent, "a refused backup returns no manifest to record as consistent")
			require.NoDirExists(t, m.backupDir("b1"), "a refused backup removes its incomplete tree")

			// The stable retry — nothing moving under the walk — is consistent and verifies.
			m.afterBackupWalk = nil
			again, aerr := m.TakeBackup(ctx, "b1")
			require.NoError(t, aerr, "a still generation store copies consistently")
			require.True(t, again.Consistent)
			_, verr := m.VerifyBackup("b1")
			require.NoError(t, verr)
		})
	}
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
