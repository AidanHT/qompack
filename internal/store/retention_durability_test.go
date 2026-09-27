package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestRetentionRoots_ABackupsDeclarationsAreDurableBeforeItsManifest: TakeBackup declares every
// root the mapping log names as a rollback retention root and then writes backup/<id>/manifest.json
// through paths.WriteAtomic, which is durable. The declarations are what stop GC collecting what the
// backup's rollback needs, and they are made first precisely so a crash between the two over-retains;
// across a power cut that holds only if they are durable before the manifest. The test cuts
// retention-roots.jsonl back to what its barriers made durable and requires every declaration the
// backup made to be in that prefix, synced once for the batch, before the manifest existed.
func TestRetentionRoots_ABackupsDeclarationsAreDurableBeforeItsManifest(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	m := newMigrator(t, tp, legacySource(3))
	_, err := m.Import(ctx)
	require.NoError(t, err)

	const id = "durable-declarations"
	rootsPath := RetentionRootsPath(tp.Root)
	manifest := filepath.Join(m.backupDir(id), backupManifestFile)
	durable := fileSize(t, rootsPath) // the import's own declarations are taken as durable
	var steps []string
	manifestFirst := false
	m.barriers = paths.Barriers{
		SyncFile: func(f *os.File) error {
			steps = append(steps, "file:"+filepath.Base(f.Name()))
			if _, statErr := os.Stat(paths.Long(manifest)); statErr == nil {
				manifestFirst = true
			}
			if err := f.Sync(); err != nil {
				return err
			}
			fi, err := f.Stat()
			require.NoError(t, err)
			durable = fi.Size()
			return nil
		},
		SyncDir: func(dir string) error {
			steps = append(steps, "dir:"+filepath.Base(dir))
			return paths.SyncDir(dir)
		},
	}

	_, err = m.TakeBackup(ctx, id)
	require.NoError(t, err)

	raw, err := os.ReadFile(paths.Long(rootsPath))
	require.NoError(t, err)
	kept := raw[:durable]
	reason := []byte("restorable through backup " + id + "/" + backupManifestFile)
	require.Equal(t, 3, bytes.Count(kept, reason),
		"after a power cut, the backup's three rollback declarations are still on disk")
	require.Equal(t, []string{"file:" + retentionRootsFile}, steps, "one sync for the whole batch")
	require.False(t, manifestFirst, "the declarations are durable before the manifest that depends on them exists")
}
