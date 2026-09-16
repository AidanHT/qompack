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
// Artifacts and sketches/tried.bloom still go through paths.CreateNew (0444). The two
// append-only logs (checkpoints/MANIFEST.jsonl, pins/invariants.jsonl) go through
// paths.RestoreLog so a later seal or pin can append. A restore never overwrites.

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

// TestRestoreBackup_RestoredLogsStayAppendable is N1: a restore of a project that has sealed
// and pinned must leave checkpoints/MANIFEST.jsonl and pins/invariants.jsonl appendable, so
// the next seal and the next pin succeed. Artifacts stay read-only; a restore still never
// overwrites.
func TestRestoreBackup_RestoredLogsStayAppendable(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(2)
	m := newMigrator(t, tp, src)
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)

	l := paths.Of(tp.Root)
	cpBody := []byte("{\"seq\":1,\"kind\":\"sealed-restore-probe\"}\n")
	require.NoError(t, paths.CreateNew(paths.CheckpointPath(l, core.CheckpointSeq(1)), cpBody))
	first := paths.ManifestEntry{Seq: 1, SHA256: "aaaaaaaa", Bytes: int64(len(cpBody)), Created: 1}
	require.NoError(t, paths.AppendManifest(l, first))
	pinLog := filepath.Join(l.Pins, "invariants.jsonl")
	require.NoError(t, paths.AppendJSONL(pinLog, map[string]string{"id": "pin-before-backup"}))

	_, err = m.TakeBackup(ctx, "b1")
	require.NoError(t, err)

	dest := filepath.Join(t.TempDir(), "restored")
	require.NoError(t, m.RestoreBackup("b1", dest))

	dl := paths.Of(dest)
	restoredCP := paths.CheckpointPath(dl, core.CheckpointSeq(1))
	assertRestoredBytes(t, restoredCP, cpBody)
	fi, err := os.Stat(paths.Long(restoredCP))
	require.NoError(t, err)
	require.Zero(t, fi.Mode().Perm()&0o222, "the restored checkpoint artifact must stay read-only")

	second := paths.ManifestEntry{Seq: 2, SHA256: "bbbbbbbb", Bytes: 4, Created: 2}
	require.NoError(t, paths.AppendManifest(dl, second),
		"a restored MANIFEST.jsonl must accept the next seal")
	require.NoError(t, paths.AppendJSONL(filepath.Join(dl.Pins, "invariants.jsonl"),
		map[string]string{"id": "pin-after-restore"}),
		"a restored pins log must accept the next pin")

	got, err := paths.ReadManifest(dl)
	require.NoError(t, err)
	require.Equal(t, []paths.ManifestEntry{first, second}, got)

	existing := []byte("pre-existing checkpoint; a restore must not overwrite this\n")
	occupied := filepath.Join(t.TempDir(), "occupied")
	ol := paths.Of(occupied)
	require.NoError(t, paths.EnsureLayout(ol))
	require.NoError(t, paths.CreateNew(paths.CheckpointPath(ol, core.CheckpointSeq(1)), existing))
	err = m.RestoreBackup("b1", occupied)
	require.ErrorIs(t, err, os.ErrExist)
	require.Contains(t, err.Error(), "0001.json")
	gotCP, rerr := os.ReadFile(paths.Long(paths.CheckpointPath(ol, core.CheckpointSeq(1))))
	require.NoError(t, rerr)
	require.Equal(t, existing, gotCP)
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
