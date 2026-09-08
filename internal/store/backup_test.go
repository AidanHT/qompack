package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// Backup, verified restore and the rehearsed rollback drill (SP-20 M1-04 / T20-M1-08). The
// fixtures these use — legacySource, newMigrator, passedGate, readAllClose — live in
// migrate_test.go, which is the same package.

// ── the rollback drill ─────────────────────────────────────────────────────────────────────

// TestRollbackDrill_BeforeAndAfterTheFirstNewFormatWrite is T20-M1-08's rehearsal, executed.
//
// The drill is not a document: it stops writers, verifies the backup by re-hashing it, restores
// it into a scratch root, opens a real Store over the restore and re-reads every legacy id
// through its old identity, enumerates the writes an older binary could not read, and refuses to
// claim an automatic downgrade. It runs once before the first new-format write and again after
// one, against the actual new artifact.
func TestRollbackDrill_BeforeAndAfterTheFirstNewFormatWrite(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(3)
	m := newMigrator(t, tp, src)
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)
	_, err = m.TakeBackup(ctx, "pre-cutover")
	require.NoError(t, err)

	stop := func(context.Context) error { return nil }

	// ── before the first new-format write ──
	pre, err := m.RehearseRollback(ctx, RollbackOptions{
		Phase: RollbackBeforeFirstNewWrite, BackupID: "pre-cutover",
		RestoreRoot: filepath.Join(t.TempDir(), "restore-pre"), StopWriters: stop,
	})
	require.NoError(t, err)
	require.True(t, pre.OK, "refusal: %s", pre.Refusal)
	require.True(t, pre.WritersStopped)
	require.True(t, pre.BackupVerified)
	require.True(t, pre.ReaderProved, "the restored backup must actually read back")
	require.Len(t, pre.RetainedLegacyIDs, 3)
	require.Empty(t, pre.UnreadableByOldReader)
	require.False(t, pre.AutomaticDowngrade, "no drill may promise an automatic downgrade")
	require.True(t, pre.EvidenceRetained)

	// ── cut over and make the first new-format write ──
	_, err = m.Cutover(ctx, CutoverOptions{BackupID: "pre-cutover", StopLegacyWriter: stop})
	require.NoError(t, err)

	res, err := tp.Store.PutBytes(ctx, []byte("new-format observation envelope\n"), PutOptions{Tool: "FileRead", Path: "src/new.ts"})
	require.NoError(t, err)
	nw, err := m.RecordNewFormatWrite(ctx, res.Root.Hash, "")
	require.NoError(t, err)
	require.True(t, nw.First)

	h, err := m.Handoff()
	require.NoError(t, err)
	require.NotZero(t, h.FirstNewWriteAt)

	// ── after the first new-format write, against the actual new artifact ──
	post, err := m.RehearseRollback(ctx, RollbackOptions{
		Phase: RollbackAfterFirstNewWrite, BackupID: "pre-cutover",
		RestoreRoot: filepath.Join(t.TempDir(), "restore-post"), StopWriters: stop,
	})
	require.NoError(t, err)
	require.True(t, post.OK, "refusal: %s", post.Refusal)
	require.True(t, post.BackupVerified)
	require.True(t, post.ReaderProved)
	require.Len(t, post.RetainedLegacyIDs, 3, "rolling back must not cost a single legacy id")
	require.Len(t, post.UnreadableByOldReader, 1,
		"the write made after the backup is exactly what an older binary cannot read")
	require.Equal(t, res.Root.Hash.String(), post.UnreadableByOldReader[0].Root)
	require.False(t, post.AutomaticDowngrade)
	require.True(t, post.EvidenceRetained, "the drill must not delete evidence")

	// Nothing the drill did removed the new artifact either.
	requireRootPresent(t, tp.Store, res.Root.Hash)

	// Both rehearsals are recorded append-only, so a later drill cannot erase an earlier one.
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(tp.Root).Migrate, rollbackDrillFile)))
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(strings.TrimSpace(string(b)), "\n")+1)
}

// TestRollbackDrill_RefusesTheBeforePhaseOnceANewFormatWriteExists: the phase is a claim about
// the world, and the drill checks it instead of taking the caller's word.
func TestRollbackDrill_RefusesTheBeforePhaseOnceANewFormatWriteExists(t *testing.T) {
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(2))
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)
	_, err = m.TakeBackup(ctx, "b1")
	require.NoError(t, err)
	_, err = m.Cutover(ctx, CutoverOptions{BackupID: "b1", StopLegacyWriter: func(context.Context) error { return nil }})
	require.NoError(t, err)

	res, err := tp.Store.PutBytes(ctx, []byte("first new write\n"), PutOptions{Tool: "FileRead", Path: "src/n.ts"})
	require.NoError(t, err)
	_, err = m.RecordNewFormatWrite(ctx, res.Root.Hash, "")
	require.NoError(t, err)

	drill, err := m.RehearseRollback(ctx, RollbackOptions{
		Phase: RollbackBeforeFirstNewWrite, BackupID: "b1",
		RestoreRoot: filepath.Join(t.TempDir(), "r"), StopWriters: func(context.Context) error { return nil },
	})
	require.NoError(t, err)
	require.False(t, drill.OK)
	require.Contains(t, drill.Refusal, "new-format write")
}

// TestRollbackDrill_RefusesWhenWritersCannotBeStopped and when the backup does not verify: the
// two ways the rehearsal is allowed to fail, both reported rather than thrown.
func TestRollbackDrill_RefusesOnUnstoppableWritersOrATamperedBackup(t *testing.T) {
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(2))
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)
	_, err = m.TakeBackup(ctx, "b1")
	require.NoError(t, err)

	drill, err := m.RehearseRollback(ctx, RollbackOptions{
		Phase: RollbackBeforeFirstNewWrite, BackupID: "b1",
		RestoreRoot: filepath.Join(t.TempDir(), "r1"),
		StopWriters: func(context.Context) error { return errors.New("a hook process is still writing") },
	})
	require.NoError(t, err)
	require.False(t, drill.OK)
	require.False(t, drill.WritersStopped)
	require.Contains(t, drill.Refusal, "writer")

	// Tamper with one backed-up byte.
	man, err := m.VerifyBackup("b1")
	require.NoError(t, err)
	require.NotEmpty(t, man.Files)
	victim := filepath.Join(paths.Of(tp.Root).Backup, "b1", backupTreeDir, filepath.FromSlash(man.Files[0].Name))
	require.NoError(t, paths.WriteAtomic(victim, []byte("tampered"), 0o600))

	drill, err = m.RehearseRollback(ctx, RollbackOptions{
		Phase: RollbackBeforeFirstNewWrite, BackupID: "b1",
		RestoreRoot: filepath.Join(t.TempDir(), "r2"),
		StopWriters: func(context.Context) error { return nil },
	})
	require.NoError(t, err)
	require.False(t, drill.OK)
	require.False(t, drill.BackupVerified)
	require.Contains(t, drill.Refusal, "backup")
}

// TestBackup_VerifyDetectsTamperingAndSizeDrift covers backup.go's own contract directly.
func TestBackup_VerifyDetectsTamperingAndSizeDrift(t *testing.T) {
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(2))
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)

	man, err := m.TakeBackup(ctx, "b1")
	require.NoError(t, err)
	require.Equal(t, backupManifestVersion, man.Version)
	require.True(t, man.Consistent, "a backup taken under the writer lease is consistent")
	require.NotEmpty(t, man.Files)

	got, err := m.VerifyBackup("b1")
	require.NoError(t, err)
	require.Equal(t, man.Files, got.Files)

	// A second backup under the same id is refused rather than silently overwriting evidence.
	_, err = m.TakeBackup(ctx, "b1")
	require.ErrorIs(t, err, os.ErrExist)

	victim := filepath.Join(paths.Of(tp.Root).Backup, "b1", backupTreeDir, filepath.FromSlash(man.Files[0].Name))
	require.NoError(t, paths.WriteAtomic(victim, []byte("tampered"), 0o600))
	_, err = m.VerifyBackup("b1")
	require.ErrorIs(t, err, ErrBackupCorrupt)
}

// TestBackup_RestoreOpensAsARealStore is the "verified backup restore" half of invariant 10: the
// restored tree is not just bytes on disk, it is a store an ordinary reader can open and read.
func TestBackup_RestoreOpensAsARealStore(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(2)
	m := newMigrator(t, tp, src)
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)
	_, err = m.TakeBackup(ctx, "b1")
	require.NoError(t, err)

	dest := filepath.Join(t.TempDir(), "restored")
	require.NoError(t, m.RestoreBackup("b1", dest))

	rs, err := openFS(dest, tp.Cfg, Deps{Log: tp.Log, Clock: tp.Clock})
	require.NoError(t, err)
	defer func() { _ = rs.Close() }()

	for _, r := range src.recs {
		mp, ok, err := m.LookupLegacy(r.ID)
		require.NoError(t, err)
		require.True(t, ok)
		rc, err := rs.Open(ctx, mp.Root)
		require.NoError(t, err)
		got, err := readAllClose(rc)
		require.NoError(t, err)
		require.Equal(t, r.Payload, got, "the restored store must serve the legacy bytes")
	}
}
