package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// These tests cover SP-20 M1-04 / T20-M1-08: side-by-side legacy import behind a durable cursor
// and an idempotent frontier, a stable single-writer handoff, object/reference/query/semantic
// parity before cutover, and the rollback drill rehearsed both before and after the first
// new-format write.
//
// They live in package store, not store_test, for the same reason helpers_test.go states: the
// fixtures below need newTestStore's in-package fake clock, and internal/testutil cannot be
// imported back into this package.

// passedGate is the legacy-import build gate, constructed here with Passed set.
//
// The production table (config.LegacyImportGate) ships Passed false and
// TestMigrationBuildGates_LegacyImportIsPendingAndHasNoConfigLeaf pins that, so every test that
// actually imports has to say out loud that it is running a gated capability. That is the point
// of taking the gate as a value rather than reading the table inside NewMigrator: there is no
// test-only bypass door for production code to fall through.
func passedGate() config.MigrationGate {
	g := config.LegacyImportGate()
	g.Passed = true
	return g
}

// ── the legacy source double ───────────────────────────────────────────────────────────────

// fakeLegacySource is a declared legacy snapshot held in memory: an ordered run of records, a
// snapshot identity and a frontier. failAt makes Read fail once it would hand out the record at
// that 1-based ordinal, which is how the "interrupted import" tests cut an import in half.
type fakeLegacySource struct {
	id       string
	recs     []LegacyRecord
	frontier int64
	failAt   int
	handed   int
	// moved simulates the legacy writer appending past the declared frontier between the import
	// and the cutover, which must make the handoff unstable.
	moved int64
}

func (f *fakeLegacySource) Snapshot(context.Context) (LegacySnapshot, error) {
	front := f.frontier
	if f.moved > front {
		front = f.moved
	}
	return LegacySnapshot{ID: f.id, Frontier: front, Records: int64(len(f.recs))}, nil
}

func (f *fakeLegacySource) Read(_ context.Context, after int64, limit int) ([]LegacyRecord, error) {
	var out []LegacyRecord
	for _, r := range f.recs {
		if r.Position <= after {
			continue
		}
		if len(out) == limit {
			break
		}
		f.handed++
		if f.failAt > 0 && f.handed >= f.failAt {
			if len(out) == 0 {
				return nil, errors.New("legacy source: simulated read failure")
			}
			return out, nil
		}
		out = append(out, r)
	}
	return out, nil
}

// legacySource builds n records with deterministic content. Every record declares unknown
// fidelity, which is the case the import must never upgrade.
func legacySource(n int) *fakeLegacySource {
	src := &fakeLegacySource{id: "legacy-snapshot-2026-09-01"}
	for i := 1; i <= n; i++ {
		src.recs = append(src.recs, LegacyRecord{
			ID:       fmt.Sprintf("legacy-%03d", i),
			Position: int64(i),
			Tool:     "FileRead",
			Path:     fmt.Sprintf("src/legacy%d.ts", i),
			Session:  "sess-legacy",
			Turn:     core.TurnIndex(i),
			TS:       core.UnixMilli(1_700_000_000_000 + int64(i)),
			Fidelity: core.FidelityUnknown,
			Payload:  []byte(fmt.Sprintf("legacy record %d\ncontent line\n", i)),
		})
	}
	src.frontier = int64(n)
	return src
}

func newMigrator(t *testing.T, tp *testProject, src LegacySource) *Migrator {
	t.Helper()
	m, err := NewMigrator(tp.Store, tp.Root, MigrateOptions{
		Source: src, Gate: passedGate(), Clock: tp.Clock, Batch: 2,
	})
	require.NoError(t, err)
	return m
}

func mappingLines(t *testing.T, root string) []string {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Migrate, importMappingFile)))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ── the gate ───────────────────────────────────────────────────────────────────────────────

// TestNewMigrator_RefusesWhileTheGateIsClosed is the production default: MigrateOptions with no
// gate is the closed gate, and a Migrator cannot be built at all.
func TestNewMigrator_RefusesWhileTheGateIsClosed(t *testing.T) {
	tp := newTestStore(t)

	_, err := NewMigrator(tp.Store, tp.Root, MigrateOptions{Source: legacySource(1)})
	require.ErrorIs(t, err, ErrMigrationGateClosed)

	// And the gate this build actually ships is that closed gate.
	_, err = NewMigrator(tp.Store, tp.Root, MigrateOptions{Source: legacySource(1), Gate: config.LegacyImportGate()})
	require.ErrorIs(t, err, ErrMigrationGateClosed)
	require.Contains(t, err.Error(), config.LegacyImportGateKey)
}

// ── import, cursor and frontier ────────────────────────────────────────────────────────────

// TestImport_SideBySideWithDurableCursor: a clean import maps every legacy record to a new object
// and a new reference without touching the originals, and commits a durable cursor at the
// declared frontier.
func TestImport_SideBySideWithDurableCursor(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(3)
	m := newMigrator(t, tp, src)
	ctx := context.Background()

	rep, err := m.Import(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, rep.Imported)
	require.Zero(t, rep.Skipped)
	require.True(t, rep.Complete)

	cur, err := m.Cursor()
	require.NoError(t, err)
	require.Equal(t, importCursorVersion, cur.Version)
	require.Equal(t, src.id, cur.SnapshotID)
	require.Equal(t, int64(3), cur.Frontier)
	require.Equal(t, int64(3), cur.Position)
	require.Equal(t, int64(3), cur.Imported)
	require.True(t, cur.Complete)

	// Side by side: each legacy id resolves to a new object whose bytes are the legacy payload,
	// and the legacy id itself is retained.
	for _, r := range src.recs {
		mp, ok, err := m.LookupLegacy(r.ID)
		require.NoError(t, err)
		require.True(t, ok, "legacy id %s must remain resolvable", r.ID)
		require.Equal(t, r.ID, mp.LegacyID)
		requireRootPresent(t, tp.Store, mp.Root)

		rc, err := tp.Store.Open(ctx, mp.Root)
		require.NoError(t, err)
		got, err := readAllClose(rc)
		require.NoError(t, err)
		require.Equal(t, r.Payload, got)

		rec, err := tp.Store.ToolUse(ctx, mp.ToolUseID)
		require.NoError(t, err)
		require.Equal(t, mp.Root, rec.Root)
		require.Equal(t, StatusOK, rec.Status)
	}
	require.Len(t, mappingLines(t, tp.Root), 3)
}

// TestImport_LegacyUnknownFidelityIsNeverUpgraded is the invariant the plan states in one line
// ("Legacy unknown remains legacy unknown") and the one an importer is most tempted to break:
// the bytes round-trip exactly, so calling the result `exact` would look defensible. It is not.
// Exactness is a claim about the CAPTURE, not about the copy, and the legacy store never made it.
func TestImport_LegacyUnknownFidelityIsNeverUpgraded(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(2)
	m := newMigrator(t, tp, src)
	ctx := context.Background()

	_, err := m.Import(ctx)
	require.NoError(t, err)

	for _, r := range src.recs {
		mp, ok, err := m.LookupLegacy(r.ID)
		require.NoError(t, err)
		require.True(t, ok)

		// The bytes came back byte-identical...
		rc, err := tp.Store.Open(ctx, mp.Root)
		require.NoError(t, err)
		got, err := readAllClose(rc)
		require.NoError(t, err)
		require.Equal(t, r.Payload, got)

		// ...and the fidelity is still unknown.
		require.Equal(t, core.FidelityUnknown, mp.Fidelity,
			"a byte-exact copy of a record whose capture fidelity was never known is still unknown")
	}

	// And on disk, not just in the returned struct.
	for _, line := range mappingLines(t, tp.Root) {
		require.Contains(t, line, `"fidelity":"unknown"`)
		require.NotContains(t, line, `"fidelity":"exact"`)
	}
}

// TestImportFidelity_NeverProducesExactFromAnythingElse guards the one function that could ever
// upgrade a fidelity, at its own boundary rather than only through an import.
func TestImportFidelity_NeverProducesExactFromAnythingElse(t *testing.T) {
	cases := []struct {
		in   core.Fidelity
		want core.Fidelity
	}{
		{core.FidelityUnknown, core.FidelityUnknown},
		{core.Fidelity(""), core.FidelityUnknown},
		{core.Fidelity("EXACT"), core.FidelityUnknown},
		{core.Fidelity("nonsense"), core.FidelityUnknown},
		{core.FidelityPrefix, core.FidelityPrefix},
		{core.FidelityPartial, core.FidelityPartial},
		{core.FidelityRedacted, core.FidelityRedacted},
		{core.FidelityTruncated, core.FidelityTruncated},
		{core.FidelityBinary, core.FidelityBinary},
		{core.FidelityFailure, core.FidelityFailure},
		{core.FidelityExact, core.FidelityExact},
	}
	for _, c := range cases {
		got := importFidelity(c.in)
		require.Equal(t, c.want, got, "importFidelity(%q)", c.in)
		if c.in != core.FidelityExact {
			require.NotEqual(t, core.FidelityExact, got,
				"importFidelity must never manufacture exact fidelity from %q", c.in)
		}
	}
}

// TestImport_ResumesFromTheCursorWithoutDuplicating: an import cut off mid-run resumes at the
// committed position and the total mapping set has one line per logical record.
func TestImport_ResumesFromTheCursorWithoutDuplicating(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(5)
	src.failAt = 3 // hands out 2 records, then fails
	m := newMigrator(t, tp, src)
	ctx := context.Background()

	_, err := m.Import(ctx)
	require.Error(t, err, "the interrupted import must surface its failure, not report success")
	cur, err := m.Cursor()
	require.NoError(t, err)
	require.Equal(t, int64(2), cur.Position)
	require.False(t, cur.Complete)
	require.Len(t, mappingLines(t, tp.Root), 2)

	src.failAt, src.handed = 0, 0
	rep, err := m.Import(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, rep.Imported, "only the three records after the cursor are imported")
	require.Zero(t, rep.Skipped)
	require.True(t, rep.Complete)
	require.Len(t, mappingLines(t, tp.Root), 5)
}

// TestImport_LostCursorFallsBackToTheFrontierWithoutDuplicating is the harder half of
// resumability. A cursor is a single small file and can be lost — a torn write, a restored
// backup, an operator deleting it. The mapping log is the durable IMPORT FRONTIER, and it, not
// the cursor, is what makes re-import idempotent.
func TestImport_LostCursorFallsBackToTheFrontierWithoutDuplicating(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(4)
	src.failAt = 4
	m := newMigrator(t, tp, src)
	ctx := context.Background()

	_, err := m.Import(ctx)
	require.Error(t, err)
	require.Len(t, mappingLines(t, tp.Root), 3)

	// Lose the cursor entirely.
	require.NoError(t, os.Remove(paths.Long(filepath.Join(paths.Of(tp.Root).Migrate, importCursorFile))))

	src.failAt, src.handed = 0, 0
	rep, err := m.Import(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Imported)
	require.Equal(t, 3, rep.Skipped, "the three already-mapped records are recognised, not re-imported")
	require.Len(t, mappingLines(t, tp.Root), 4, "no logical record is mapped twice")
}

// TestImport_CompletedImportIsANoOp: re-running a finished import changes nothing at all — not
// the mapping bytes, not the cursor.
func TestImport_CompletedImportIsANoOp(t *testing.T) {
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(3))
	ctx := context.Background()

	_, err := m.Import(ctx)
	require.NoError(t, err)
	before := mappingLines(t, tp.Root)
	curBefore, err := m.Cursor()
	require.NoError(t, err)

	rep, err := m.Import(ctx)
	require.NoError(t, err)
	require.Zero(t, rep.Imported)
	require.Zero(t, rep.Skipped)
	require.True(t, rep.Complete)
	require.Equal(t, before, mappingLines(t, tp.Root))

	curAfter, err := m.Cursor()
	require.NoError(t, err)
	require.Equal(t, curBefore, curAfter)
}

// TestImport_RefusesADifferentSnapshot: the cursor records the source snapshot identity, so an
// import pointed at a different snapshot is refused rather than silently interleaved.
func TestImport_RefusesADifferentSnapshot(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(2)
	m := newMigrator(t, tp, src)
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)

	other := legacySource(2)
	other.id = "some-other-snapshot"
	m2, err := NewMigrator(tp.Store, tp.Root, MigrateOptions{Source: other, Gate: passedGate(), Clock: tp.Clock})
	require.NoError(t, err)
	_, err = m2.Import(ctx)
	require.ErrorIs(t, err, ErrSnapshotMismatch)
}

// ── parity ─────────────────────────────────────────────────────────────────────────────────

// TestParity_AllFourChecksPassAfterACleanImport pins the four declared checks by name, so a
// future edit cannot quietly drop one and still report parity.
func TestParity_AllFourChecksPassAfterACleanImport(t *testing.T) {
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(3))
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)

	rep, err := m.Parity(ctx)
	require.NoError(t, err)
	require.True(t, rep.OK, "parity must pass after a clean import: %+v", rep.Checks)
	require.NotEmpty(t, rep.Digest)

	var names []string
	for _, c := range rep.Checks {
		names = append(names, c.Name)
		require.True(t, c.OK, "%s: %v", c.Name, c.Mismatches)
		require.Equal(t, int64(3), c.Checked, c.Name)
	}
	require.Equal(t, []string{parityObject, parityReference, parityQuery, paritySemantic}, names)
}

// TestParity_FailsWhenAReferenceIsSuperseded: reference parity is a real check, not a count.
func TestParity_FailsWhenAReferenceIsSuperseded(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(2)
	m := newMigrator(t, tp, src)
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)

	a, ok, err := m.LookupLegacy(src.recs[0].ID)
	require.NoError(t, err)
	require.True(t, ok)
	b, _, err := m.LookupLegacy(src.recs[1].ID)
	require.NoError(t, err)
	require.NoError(t, tp.Store.MarkSuperseded(ctx, a.ToolUseID, b.ToolUseID))

	rep, err := m.Parity(ctx)
	require.NoError(t, err)
	require.False(t, rep.OK)
	require.False(t, checkByName(t, rep, parityReference).OK)
}

// TestParity_FailsWhenTheSnapshotChangedUnderneath: semantic parity compares against the DECLARED
// snapshot, so a source that has quietly changed a record's meaning is caught even though every
// object and reference is still present and readable.
func TestParity_FailsWhenTheSnapshotChangedUnderneath(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(2)
	m := newMigrator(t, tp, src)
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)

	src.recs[1].Tool = "Bash" // the legacy record now means something else

	rep, err := m.Parity(ctx)
	require.NoError(t, err)
	require.False(t, rep.OK)
	require.True(t, checkByName(t, rep, parityObject).OK, "the bytes are still there")
	require.False(t, checkByName(t, rep, paritySemantic).OK)
}

// TestParity_FailsWhenAnImportedFidelityWasUpgraded: semantic parity is also the backstop for the
// unknown-stays-unknown rule — a mapping line that claims exact against an unknown source record
// is a semantic mismatch, not a cosmetic one.
func TestParity_FailsWhenAnImportedFidelityWasUpgraded(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(1)
	m := newMigrator(t, tp, src)
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)

	// Forge an upgraded mapping line by appending a second, contradicting record for the same
	// legacy id. The frontier reader keeps the LAST line for an id, so this is what a buggy
	// importer would have written.
	mp, _, err := m.LookupLegacy(src.recs[0].ID)
	require.NoError(t, err)
	mp.Fidelity = core.FidelityExact
	require.NoError(t, paths.AppendJSONL(
		filepath.Join(paths.Of(tp.Root).Migrate, importMappingFile), mp.wire()))

	rep, err := m.Parity(ctx)
	require.NoError(t, err)
	require.False(t, rep.OK)
	require.False(t, checkByName(t, rep, paritySemantic).OK)
	require.Contains(t, strings.Join(checkByName(t, rep, paritySemantic).Mismatches, " "), "fidelity")
}

func checkByName(t *testing.T, rep ParityReport, name string) ParityCheck {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("parity report has no %q check", name)
	return ParityCheck{}
}

// ── writer handoff and cutover ─────────────────────────────────────────────────────────────

// TestHandoff_LegacyOwnsTheWriterUntilCutover: before cutover the legacy writer owns writing, and
// a new-format write is refused outright.
func TestHandoff_LegacyOwnsTheWriterUntilCutover(t *testing.T) {
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(2))
	ctx := context.Background()

	h, err := m.Handoff()
	require.NoError(t, err)
	require.Equal(t, WriterLegacy, h.Owner)

	_, err = m.RecordNewFormatWrite(ctx, core.HashBytes(core.DomainChunk, []byte("x")), "legacy-001")
	require.ErrorIs(t, err, ErrWriterNotQompack)
}

// TestCutover_RefusesUntilImportParityBackupAndAStableSource enumerates every refusal in order,
// so "cutover is refused if parity fails" is a tested claim and not a comment.
func TestCutover_RefusesUntilImportParityBackupAndAStableSource(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(3)
	m := newMigrator(t, tp, src)
	ctx := context.Background()

	stop := func(context.Context) error { return nil }

	// 1. import not finished
	_, err := m.Cutover(ctx, CutoverOptions{StopLegacyWriter: stop, BackupID: "b1"})
	require.ErrorIs(t, err, ErrImportIncomplete)

	_, err = m.Import(ctx)
	require.NoError(t, err)

	// 2. no way to stop the legacy writer
	_, err = m.Cutover(ctx, CutoverOptions{BackupID: "b1"})
	require.ErrorIs(t, err, ErrWriterNotStopped)

	// 3. stopping the legacy writer failed
	_, err = m.Cutover(ctx, CutoverOptions{BackupID: "b1", StopLegacyWriter: func(context.Context) error {
		return errors.New("legacy writer still running")
	}})
	require.ErrorIs(t, err, ErrWriterNotStopped)

	// 4. no verified backup
	_, err = m.Cutover(ctx, CutoverOptions{BackupID: "b1", StopLegacyWriter: stop})
	require.Error(t, err)
	require.Contains(t, err.Error(), "backup")

	_, err = m.TakeBackup(ctx, "b1")
	require.NoError(t, err)

	// 5. parity fails
	a, _, err := m.LookupLegacy(src.recs[0].ID)
	require.NoError(t, err)
	b, _, err := m.LookupLegacy(src.recs[1].ID)
	require.NoError(t, err)
	require.NoError(t, tp.Store.MarkSuperseded(ctx, a.ToolUseID, b.ToolUseID))
	_, err = m.Cutover(ctx, CutoverOptions{BackupID: "b1", StopLegacyWriter: stop})
	require.ErrorIs(t, err, ErrParityFailed)

	// The refusal is durable: the writer did not move.
	h, err := m.Handoff()
	require.NoError(t, err)
	require.Equal(t, WriterLegacy, h.Owner)
}

// TestCutover_RefusesWhenTheSourceMovedPastTheDeclaredFrontier: "stable handoff" means the legacy
// writer actually stopped. A source whose frontier advanced after the import is not stable.
func TestCutover_RefusesWhenTheSourceMovedPastTheDeclaredFrontier(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(2)
	m := newMigrator(t, tp, src)
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)
	_, err = m.TakeBackup(ctx, "b1")
	require.NoError(t, err)

	src.moved = 9
	_, err = m.Cutover(ctx, CutoverOptions{BackupID: "b1", StopLegacyWriter: func(context.Context) error { return nil }})
	require.ErrorIs(t, err, ErrHandoffUnstable)
}

// TestCutover_TransfersTheWriterAndLeavesExactlyOne: after cutover Qompack owns writing, the
// legacy writer can no longer acquire, and two concurrent acquisitions cannot both succeed.
func TestCutover_TransfersTheWriterAndLeavesExactlyOne(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(3)
	m := newMigrator(t, tp, src)
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)
	_, err = m.TakeBackup(ctx, "b1")
	require.NoError(t, err)

	h, err := m.Cutover(ctx, CutoverOptions{BackupID: "b1", StopLegacyWriter: func(context.Context) error { return nil }})
	require.NoError(t, err)
	require.Equal(t, WriterQompack, h.Owner)
	require.Equal(t, "b1", h.BackupID)
	require.NotEmpty(t, h.ParityDigest)

	// Exactly one writer.
	lease, err := m.AcquireWriter(WriterQompack)
	require.NoError(t, err)
	_, err = m.AcquireWriter(WriterQompack)
	require.ErrorIs(t, err, ErrWriterHeld)
	require.NoError(t, lease.Release())

	// The legacy writer is done for good.
	_, err = m.AcquireWriter(WriterLegacy)
	require.ErrorIs(t, err, ErrWriterNotQompack)

	// Old ids and old readers still work after cutover.
	for _, r := range src.recs {
		mp, ok, err := m.LookupLegacy(r.ID)
		require.NoError(t, err)
		require.True(t, ok)
		requireRootPresent(t, tp.Store, mp.Root)
	}
}

// TestImportCursor_OnDiskShapeIsVersioned pins the cursor's wire shape: a later build must be
// able to tell an old cursor from one it does not understand.
func TestImportCursor_OnDiskShapeIsVersioned(t *testing.T) {
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(1))
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)

	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(tp.Root).Migrate, importCursorFile)))
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(b, &raw))
	for _, k := range []string{
		"version", "snapshot_id", "frontier", "position", "imported", "skipped", "complete", "updated_at",
	} {
		require.Contains(t, raw, k, "cursor field %q", k)
	}
	require.Equal(t, float64(importCursorVersion), raw["version"])

	// A cursor from a newer build is refused, not half-read.
	require.NoError(t, paths.WriteAtomic(
		filepath.Join(paths.Of(tp.Root).Migrate, importCursorFile),
		[]byte(`{"version":99,"snapshot_id":"x"}`), 0o600))
	_, err = m.Cursor()
	require.ErrorIs(t, err, ErrCursorVersion)
}

// requireRootPresent asserts a ROOT is still in the store. Store.Has answers for a chunk hash,
// not a root hash, so the root index — which is also what a GC tombstone removes — is the honest
// question to ask about an object's existence.
func requireRootPresent(t *testing.T, s Store, h core.Hash) {
	t.Helper()
	_, err := s.GetRoot(context.Background(), h)
	require.NoError(t, err, "root %s must still be present", h.Short())
}

// readAllClose drains and closes a reader the store handed out.
func readAllClose(rc io.ReadCloser) ([]byte, error) {
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}
