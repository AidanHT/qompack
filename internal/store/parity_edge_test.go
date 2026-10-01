package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Edge rows for import parity and the backup's delivery-segment watch list that their main tests leave
// unexecuted (w16b-cover, C3.6). Parity's checks are driven by forged mapping lines: the frontier
// reader keeps the LAST line for a legacy id, which is what a buggy importer would have written, so
// each forged line stands for one way an import can be wrong while every object is still on disk.

// TestParity_NamesEveryWayAMappingCanDisagreeWithTheStore: a mapped root that is not in the store, a
// mapped size the object does not re-read at, a reference that does not resolve or names another
// root, tool or path, a mapping the declared snapshot does not hold and a snapshot record that was
// never imported each fail the check they belong to, with a mismatch naming it.
func TestParity_NamesEveryWayAMappingCanDisagreeWithTheStore(t *testing.T) {
	tp := newTestStore(t)
	src := legacySource(5)
	m := newMigrator(t, tp, src)
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)

	forge := func(legacyID string, mutate func(*ImportMapping)) {
		t.Helper()
		mp, ok, lerr := m.LookupLegacy(legacyID)
		require.NoError(t, lerr)
		require.True(t, ok)
		mutate(&mp)
		require.NoError(t, paths.AppendJSONL(filepath.Join(paths.Of(tp.Root).Migrate, importMappingFile), mp.wire()))
	}
	forge("legacy-001", func(mp *ImportMapping) { mp.Root = core.HashBytes(core.DomainChunk, []byte("never stored")) })
	forge("legacy-002", func(mp *ImportMapping) { mp.CanonBytes++ })
	forge("legacy-003", func(mp *ImportMapping) { mp.ToolUseID = "toolu_never_recorded" })
	forge("legacy-004", func(mp *ImportMapping) { mp.Path = "src/elsewhere.ts" })
	forge("legacy-005", func(mp *ImportMapping) { mp.LegacyID = "legacy-999" })
	src.recs = append(src.recs, LegacyRecord{
		ID: "legacy-006", Position: 6, Tool: "FileRead", Path: "src/legacy6.ts", Session: "sess-legacy",
		Turn: 6, TS: 1_700_000_000_006, Fidelity: core.FidelityUnknown, Payload: []byte("declared later\n"),
	})
	src.frontier = 6

	rep, err := m.Parity(ctx)
	require.NoError(t, err)
	require.False(t, rep.OK)
	mismatches := func(name string) string {
		c := checkByName(t, rep, name)
		require.False(t, c.OK, name)
		return strings.Join(c.Mismatches, "\n")
	}
	object := mismatches(parityObject)
	require.Contains(t, object, "legacy-001: object")
	require.Contains(t, object, "is absent")
	require.Contains(t, object, "legacy-002: object")
	require.Contains(t, object, "re-read")
	reference := mismatches(parityReference)
	require.Contains(t, reference, "legacy-001: reference")
	require.Contains(t, reference, "points at")
	require.Contains(t, reference, "legacy-003: reference toolu_never_recorded does not resolve")
	require.Contains(t, reference, "legacy-004: reference")
	require.Contains(t, reference, "has tool/path")
	query := mismatches(parityQuery)
	require.Contains(t, query, "legacy-003: reference toolu_never_recorded is not reachable")
	require.Contains(t, query, "legacy-004: reference")
	semantic := mismatches(paritySemantic)
	require.Contains(t, semantic, "legacy-006: declared by the snapshot but never imported")
	require.Contains(t, semantic, "legacy-999: imported but not in the declared snapshot")
}

// TestTakeBackup_RefusesADeliverySegmentTreeItCannotWatch: the backup watches every segment's mutable
// journals and seals for change while it copies. A delivery-segments that is not a directory, or a
// directory nested inside a segment, is not a tree it can watch, so the backup is refused as moved
// before anything is copied; a file in a segment that is not one of its journals is simply not watched.
func TestTakeBackup_RefusesADeliverySegmentTreeItCannotWatch(t *testing.T) {
	segDir := func(tp *testProject) string { return filepath.Join(paths.Of(tp.Root).State, deliverySegmentsDirectory) }
	cases := map[string]func(t *testing.T, tp *testProject){
		"not a directory": func(t *testing.T, tp *testProject) {
			require.NoError(t, os.WriteFile(paths.Long(segDir(tp)), nil, 0o600))
		},
		"nested directory": func(t *testing.T, tp *testProject) {
			require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(segDir(tp), "00000000000000000001", "nested")), 0o700))
		},
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			tp := newTestStore(t)
			m := newMigrator(t, tp, legacySource(1))
			seedRoot(t, tp, "src/a.ts", "a\n")
			plant(t, tp)
			_, err := m.TakeBackup(context.Background(), "w1")
			require.ErrorIs(t, err, ErrBackupMoved)
			require.NoDirExists(t, m.backupDir("w1"), "a refused backup copies nothing")
		})
	}

	t.Run("unwatched file", func(t *testing.T) {
		tp := newTestStore(t)
		m := newMigrator(t, tp, legacySource(1))
		seedRoot(t, tp, "src/a.ts", "a\n")
		seg := filepath.Join(segDir(tp), "00000000000000000001")
		require.NoError(t, os.MkdirAll(paths.Long(seg), 0o700))
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(seg, "notes.txt")), []byte("x"), 0o600))
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(seg, deliveryLeaseFile)), nil, 0o600))
		man, err := m.TakeBackup(context.Background(), "w2")
		require.NoError(t, err)
		require.NotEmpty(t, man.Files)
	})
}
