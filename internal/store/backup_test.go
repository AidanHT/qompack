package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
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

// TestBackup_RefusesADeliveryStateThatMovedUnderTheCopy is design risk R10 (SP20-D1), which SP20-D1
// step 2 made reachable: from that commit a running daemon rewrites its delivery-seal sidecars IN
// PLACE — a 480-byte WriteAt into a held 32 KiB A/B image — instead of replacing them by rename, so
// a plain os.ReadFile taken while it seals can capture a slot that is neither the old record nor the
// new one, and the daemon's reader refuses such an image outright. The daemon holds no store writer
// lease, so the lease TakeBackup takes does not exclude it.
//
// The four subtests are the four shapes that matters in: a sidecar rewritten in place at the SAME
// SIZE (which is what a seal write always is, and what a size-only check would miss), a delivery
// file that appears mid-copy because the daemon started, a journal appended under the walk, and the
// control — an unrelated write, which is NOT one of these files and must not fail the backup.
//
// A refused backup writes no manifest, so it can never be verified or restored as though it were
// consistent.
func TestBackup_RefusesADeliveryStateThatMovedUnderTheCopy(t *testing.T) {
	const sealBytes = 32 << 10
	sidecar := "delivery-lease-position.json"

	for _, tc := range []struct {
		name    string
		present map[string]string
		move    func(t *testing.T, state string)
		refused bool
	}{
		{
			name:    "a sidecar resealed in place under the walk",
			present: map[string]string{sidecar: strings.Repeat("a", sealBytes)},
			move: func(t *testing.T, state string) {
				t.Helper()
				resealed := strings.Repeat("a", sealBytes-480) + strings.Repeat("b", 480)
				require.NoError(t, os.WriteFile(filepath.Join(state, sidecar), []byte(resealed), 0o600))
			},
			refused: true,
		},
		{
			name:    "a sidecar that appeared because the daemon started",
			present: map[string]string{},
			move: func(t *testing.T, state string) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(state, "delivery-ack-position.json"),
					[]byte(strings.Repeat("c", sealBytes)), 0o600))
			},
			refused: true,
		},
		{
			name:    "a journal appended under the walk",
			present: map[string]string{"delivery-leases.jsonl": "{\"lease\":1}\n"},
			move: func(t *testing.T, state string) {
				t.Helper()
				f, err := os.OpenFile(filepath.Join(state, "delivery-leases.jsonl"), os.O_WRONLY|os.O_APPEND, 0o600)
				require.NoError(t, err)
				_, werr := f.Write([]byte("{\"lease\":2}\n"))
				require.NoError(t, werr)
				require.NoError(t, f.Close())
			},
			refused: true,
		},
		{
			name:    "a write that is not the daemon's delivery state",
			present: map[string]string{sidecar: strings.Repeat("a", sealBytes)},
			move: func(t *testing.T, state string) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(state, "unrelated.json"), []byte("{}"), 0o600))
			},
			refused: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestStore(t)
			m := newMigrator(t, tp, legacySource(2))
			ctx := context.Background()
			_, err := m.Import(ctx)
			require.NoError(t, err)

			state := paths.Of(tp.Root).State
			for name, content := range tc.present {
				require.NoError(t, os.WriteFile(filepath.Join(state, name), []byte(content), 0o600))
			}
			m.afterBackupWalk = func() { tc.move(t, state) }

			man, err := m.TakeBackup(ctx, "b1")
			if !tc.refused {
				require.NoError(t, err)
				require.True(t, man.Consistent)
				_, verr := m.VerifyBackup("b1")
				require.NoError(t, verr)
				return
			}
			require.ErrorIs(t, err, ErrBackupMoved)
			require.False(t, man.Consistent, "a refused backup returns no manifest to record as consistent")
			_, verr := m.VerifyBackup("b1")
			require.Error(t, verr, "a refused backup has no manifest, so it cannot verify")
		})
	}
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

// ── migration material as GC retention roots ───────────────────────────────────────────────

// TestGC_CannotCollectMigrationOrRollbackMaterial is SP-20 invariant 9 at the seam where the
// migration unit and the GC unit meet: GC may not collect a root that rollback material needs.
//
// Nothing connected the two. Legacy import writes migrate/mapping.jsonl, the drill writes
// migrate/rollback.jsonl and TakeBackup writes backup/<id>/manifest.json, but none of those three
// files is a GC root file and none of them declared a retention root — so every imported object
// was collectible the moment the retention window passed, and the migration unit's promise that
// "old ids keep working" broke silently the first time GC ran. The control object is what proves
// the pass really collected rather than the policy having quietly spared everything.
func TestGC_CannotCollectMigrationOrRollbackMaterial(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	m := newMigrator(t, tp, legacySource(3))

	_, err := m.Import(ctx)
	require.NoError(t, err)
	_, order, err := m.Frontier()
	require.NoError(t, err)
	require.Len(t, order, 3, "the fixture must have imported something to retain")

	_, err = m.TakeBackup(ctx, "pre-cutover")
	require.NoError(t, err)

	stop := func(context.Context) error { return nil }
	_, err = m.Cutover(ctx, CutoverOptions{BackupID: "pre-cutover", StopLegacyWriter: stop})
	require.NoError(t, err)
	res, err := tp.Store.PutBytes(ctx, []byte("new-format observation envelope\n"),
		PutOptions{Tool: "FileRead", Path: "src/new.ts"})
	require.NoError(t, err)
	_, err = m.RecordNewFormatWrite(ctx, res.Root.Hash, "")
	require.NoError(t, err)

	drill, err := m.RehearseRollback(ctx, RollbackOptions{
		Phase: RollbackAfterFirstNewWrite, BackupID: "pre-cutover",
		RestoreRoot: filepath.Join(t.TempDir(), "restore"), StopWriters: stop,
	})
	require.NoError(t, err)
	require.True(t, drill.OK, "refusal: %s", drill.Refusal)
	require.Len(t, drill.UnreadableByOldReader, 1, "the drill must have enumerated the new-format write")

	// The control: an object nothing declares, so the pass has something to collect.
	doomed := gcSeed(t, tp, "src/doomed.ts", "referenced by nothing at all\n")

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError, "the retention-root file must be readable")
	require.Positive(t, rep.DeletedObjects, "the control object must actually have been collectible")

	for _, mp := range order {
		_, err := tp.Store.GetRoot(ctx, mp.Root)
		require.NoError(t, err, "old id %s must keep working: mapping.jsonl names %s", mp.LegacyID, mp.Root.Short())
	}
	require.Len(t, drill.RetainedRoots, 3, "the drill records what it proved retained")
	for _, s := range drill.RetainedRoots {
		h, perr := core.ParseHash(s)
		require.NoError(t, perr, "rollback record root %q", s)
		_, err := tp.Store.GetRoot(ctx, h)
		require.NoError(t, err, "a root the rollback record names must survive GC")
	}
	requireRootPresent(t, tp.Store, res.Root.Hash)

	_, err = tp.Store.GetRoot(ctx, doomed.Hash)
	require.ErrorIs(t, err, core.ErrNotFound, "an undeclared object must still be collected")
}

// TestMigration_DeclaresRetentionRootsOnDisk pins the mechanism the previous row proves the effect
// of: the migration unit talks to GC through store.AppendRetentionRoot's file and the rollback
// retention class, not through a private arrangement of its own.
func TestMigration_DeclaresRetentionRootsOnDisk(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	m := newMigrator(t, tp, legacySource(2))

	require.NoFileExists(t, RetentionRootsPath(tp.Root), "nothing is declared before an import runs")

	_, err := m.Import(ctx)
	require.NoError(t, err)
	_, order, err := m.Frontier()
	require.NoError(t, err)

	b, err := os.ReadFile(paths.Long(RetentionRootsPath(tp.Root)))
	require.NoError(t, err, "the import must declare its roots where GC reads them")
	body := string(b)
	for _, mp := range order {
		require.Contains(t, body, mp.Root.String(), "mapping root %s must be declared", mp.LegacyID)
	}
	require.Contains(t, body, string(RetentionRollback), "migration material is rollback-class material")
}
