package store

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// The migration's two durable readers, and what they do with a file they cannot stand behind.

// TestCursor_RefusesACursorItCannotRead.
//
// The cursor is where a resumed import starts, so a cursor this build misreads would re-import or
// skip whatever lies between the true position and the misread one. A MISSING cursor is the zero
// cursor and not an error — an import that never ran and one whose cursor was lost are the same
// starting point, and the mapping log is what keeps them from duplicating. A cursor that is there
// and wrong is a different matter and is refused, with the version mismatch getting its own
// sentinel so a caller can tell "written by a newer build" from "damaged".
func TestCursor_RefusesACursorItCannotRead(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(1))
	p := m.path(importCursorFile)

	cur, err := m.Cursor()
	require.NoError(t, err, "a missing cursor is the zero cursor")
	require.Equal(t, importCursorVersion, cur.Version)
	require.Zero(t, cur.Position)

	require.NoError(t, os.WriteFile(paths.Long(p), []byte("{not json"), 0o600))
	_, err = m.Cursor()
	require.ErrorContains(t, err, "parse import cursor")

	require.NoError(t, os.WriteFile(paths.Long(p), []byte(`{"version":99}`), 0o600))
	_, err = m.Cursor()
	require.ErrorIs(t, err, ErrCursorVersion)
	require.ErrorContains(t, err, "99", "the refusal names the version it found")
}

// TestFrontier_RefusesAMappingLineItCannotRead.
//
// The mapping log is what makes a legacy id keep working after cutover, so a line that will not
// parse — or one whose root is not a hash — cannot be passed over: skipping it would silently turn
// "this legacy id resolves to that object" into "this legacy id is unknown", which reads as a record
// that was never imported. Both shapes stop the read, and LookupLegacy propagates the refusal rather
// than answering "not found".
func TestFrontier_RefusesAMappingLineItCannotRead(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(1))
	p := m.path(importMappingFile)

	byID, order, err := m.Frontier()
	require.NoError(t, err, "a log that has never been written is an empty frontier")
	require.Empty(t, byID)
	require.Empty(t, order)

	_, ok, err := m.LookupLegacy("legacy-1")
	require.NoError(t, err)
	require.False(t, ok, "an id nothing imported is not found, and that is not an error")

	require.NoError(t, os.MkdirAll(paths.Long(m.l.Migrate), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(p), []byte("{not json\n"), 0o600))
	_, _, err = m.Frontier()
	require.ErrorContains(t, err, "parse mapping line")

	require.NoError(t, os.WriteFile(paths.Long(p),
		[]byte(`{"version":1,"legacy_id":"legacy-1","root":"not-a-hash"}`+"\n"), 0o600))
	_, _, err = m.Frontier()
	require.ErrorContains(t, err, "mapping line for legacy-1")

	_, _, err = m.LookupLegacy("legacy-1")
	require.Error(t, err, "a lookup over an unreadable log refuses rather than reporting not found")
}
