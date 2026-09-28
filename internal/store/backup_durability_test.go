package store

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// dirSyncRecord is one directory barrier and what existed when it ran.
type dirSyncRecord struct {
	dir     string
	markers map[string]bool // for each watched path, whether it existed at the barrier
}

// recordDirSyncs returns Barriers whose SyncDir records every barrier, noting which of watch existed
// when it ran, and then performs the real sync.
func recordDirSyncs(t *testing.T, watch ...string) (paths.Barriers, *[]dirSyncRecord) {
	t.Helper()
	var got []dirSyncRecord
	return paths.Barriers{SyncDir: func(dir string) error {
		rec := dirSyncRecord{dir: filepath.Clean(dir), markers: map[string]bool{}}
		for _, w := range watch {
			_, err := os.Lstat(paths.Long(w))
			rec.markers[w] = err == nil
		}
		got = append(got, rec)
		return paths.SyncDir(dir)
	}}, &got
}

// treeDirs lists every directory under root, root included, as filepath.Join(root, rel).
func treeDirs(t *testing.T, root string) []string {
	t.Helper()
	walkRoot := paths.Long(root)
	var out []string
	require.NoError(t, filepath.WalkDir(walkRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			rel, rerr := filepath.Rel(walkRoot, p)
			require.NoError(t, rerr)
			out = append(out, filepath.Clean(filepath.Join(root, rel)))
		}
		return nil
	}))
	return out
}

// TestMaintenance_ABackupIsDurableBeforeItIsCertified: TakeBackup's manifest certifies the tree and
// is durable the moment it is written, so every directory of the tree — the names each copied file
// and each directory the copy made hold — is synced before the manifest exists. The certification
// marker is synced (its name in backup/) before the manifest too, so a power cut cannot keep the
// manifest and lose the marker that says certification is pending; and the marker's removal is
// synced before TakeBackup returns certified.
func TestMaintenance_ABackupIsDurableBeforeItIsCertified(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	seedRoot(t, tp, "src/a.ts", "payload a, backed up durably\n")
	seedRoot(t, tp, "src/b.ts", "payload b, backed up durably\n")

	x := newMaint(t, tp, leaseOK)
	const id = "durable-backup"
	dir := x.m.backupDir(id)
	manifest := filepath.Join(dir, backupManifestFile)
	marker := x.pendingCertification(id)
	bar, syncs := recordDirSyncs(t, manifest, marker)
	x.m.barriers = bar

	_, err := x.TakeBackup(ctx, id)
	require.NoError(t, err)

	syncedBeforeManifest := map[string]bool{}
	for _, s := range *syncs {
		if !s.markers[manifest] {
			syncedBeforeManifest[s.dir] = true
		}
	}
	for _, d := range treeDirs(t, dir) {
		require.Truef(t, syncedBeforeManifest[d], "backup directory %s is synced before the manifest certifies it", d)
	}

	backupDir := filepath.Clean(paths.Of(tp.Root).Backup)
	markerDurableFirst := false
	for _, s := range *syncs {
		if s.dir == backupDir && s.markers[marker] && !s.markers[manifest] {
			markerDurableFirst = true
		}
	}
	require.True(t, markerDurableFirst, "the certification marker's name is durable before the manifest exists")
	last := (*syncs)[len(*syncs)-1]
	require.Equal(t, backupDir, last.dir, "TakeBackup's last barrier is backup/ ...")
	require.False(t, last.markers[marker], "... after the marker's removal, so the certification it reports is durable")
}

// TestMaintenance_ARestoreIsDurableBeforeItIsReported: Restore stages the tree, proves it, and
// publishes it with one directory rename. Every directory of the staged tree is synced before that
// rename, so a power cut after it cannot leave a restored store missing files the proof read, and the
// destination is synced after it, so the rename itself is durable before the restore is reported.
func TestMaintenance_ARestoreIsDurableBeforeItIsReported(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	seedRoot(t, tp, "src/a.ts", "payload a, restored durably\n")
	x := newMaint(t, tp, leaseOK)
	_, err := x.TakeBackup(ctx, "rt")
	require.NoError(t, err)

	dest := filepath.Join(t.TempDir(), "restored")
	published := filepath.Join(dest, ".qompack")
	bar, syncs := recordDirSyncs(t, published)
	x.m.barriers = bar
	proof, err := x.Restore(ctx, "rt", dest)
	require.NoError(t, err, "refusal note: %s", proof.Note)

	// A staged directory is dest/.qompack.restore-<id>-<hex>/.qompack/<rel>; its published twin is
	// dest/.qompack/<rel>.
	stagedBeforePublish := map[string]bool{}
	for _, s := range *syncs {
		rel, rerr := filepath.Rel(dest, s.dir)
		if rerr != nil || s.markers[published] {
			continue
		}
		first, rest, _ := strings.Cut(filepath.ToSlash(rel), "/")
		if strings.HasPrefix(first, ".qompack.restore-") && rest != "" {
			stagedBeforePublish[filepath.Clean(filepath.Join(dest, filepath.FromSlash(rest)))] = true
		}
	}
	for _, d := range treeDirs(t, published) {
		require.Truef(t, stagedBeforePublish[d], "restored directory %s was synced in staging before the publish", d)
	}
	last := (*syncs)[len(*syncs)-1]
	require.Equal(t, filepath.Clean(dest), last.dir, "the destination is synced ...")
	require.True(t, last.markers[published], "... after the rename that publishes the restore")
	require.Equal(t, filepath.Clean(filepath.Dir(dest)), (*syncs)[0].dir,
		"the destination this restore created is synced into its parent first")
}
