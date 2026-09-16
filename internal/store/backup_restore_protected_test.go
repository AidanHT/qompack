package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Restore of a backup that contains §7.4 protected paths (D8-1 / SP17-M7-05).
//
// TakeBackup copies the whole .qompack tree except backup/ run/ tmp/ logs/ metrics/, so a real
// project's backup holds checkpoints/NNNN.json, pins/*, and sketches/tried.bloom. RestoreBackup
// used to write every file with paths.WriteAtomic, which refuses those paths outright, so any
// project that had sealed a checkpoint, pinned, or built a tried bloom could not be restored.
// paths.CreateNew is the sanctioned create-once writer for those destinations: a restore into a
// fresh root creates them, and a restore never overwrites a checkpoint that is already there.

func TestRestoreBackup_RestoresProtectedPathsWithCreateNew(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(2)
	m := newMigrator(t, tp, src)
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)

	cpBody, pinBody, bloomBody := plantProtectedBackupFiles(t, tp.Root)

	_, err = m.TakeBackup(ctx, "b1")
	require.NoError(t, err)
	_, err = m.VerifyBackup("b1")
	require.NoError(t, err)

	dest := filepath.Join(t.TempDir(), "restored")
	require.NoError(t, m.RestoreBackup("b1", dest))

	dl := paths.Of(dest)
	assertRestoredBytes(t, paths.CheckpointPath(dl, core.CheckpointSeq(1)), cpBody)
	assertRestoredBytes(t, filepath.Join(dl.Pins, "restore-probe"), pinBody)
	assertRestoredBytes(t, filepath.Join(dl.Sketches, "tried.bloom"), bloomBody)

	rs, err := openFS(dest, tp.Cfg, Deps{Log: tp.Log, Clock: tp.Clock})
	require.NoError(t, err)
	defer func() { _ = rs.Close() }()
}

func TestRestoreBackup_RefusesToOverwriteAnExistingCheckpoint(t *testing.T) {
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(2))
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)
	_, _, _ = plantProtectedBackupFiles(t, tp.Root)
	_, err = m.TakeBackup(ctx, "b1")
	require.NoError(t, err)

	dest := filepath.Join(t.TempDir(), "restored")
	dl := paths.Of(dest)
	require.NoError(t, paths.EnsureLayout(dl))
	existing := []byte("pre-existing checkpoint; a restore must not overwrite this\n")
	cp := paths.CheckpointPath(dl, core.CheckpointSeq(1))
	require.NoError(t, paths.CreateNew(cp, existing))

	err = m.RestoreBackup("b1", dest)
	require.ErrorIs(t, err, os.ErrExist)
	require.Contains(t, err.Error(), "0001.json")

	got, rerr := os.ReadFile(paths.Long(cp))
	require.NoError(t, rerr)
	require.Equal(t, existing, got)
}

func plantProtectedBackupFiles(t *testing.T, root string) (checkpoint, pin, bloom []byte) {
	t.Helper()
	l := paths.Of(root)
	checkpoint = []byte("{\"seq\":1,\"kind\":\"sealed-restore-probe\"}\n")
	pin = []byte("pinned-restore-probe\n")
	bloom = []byte("tried-bloom-restore-probe")
	require.NoError(t, paths.CreateNew(paths.CheckpointPath(l, core.CheckpointSeq(1)), checkpoint))
	require.NoError(t, paths.CreateNew(filepath.Join(l.Pins, "restore-probe"), pin))
	require.NoError(t, paths.CreateNew(filepath.Join(l.Sketches, "tried.bloom"), bloom))
	return checkpoint, pin, bloom
}

func assertRestoredBytes(t *testing.T, p string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err)
	require.Equal(t, want, got)
}
