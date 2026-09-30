package negknow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestHasRecords answers "is there negative knowledge on disk" without opening or creating
// anything: no log and an empty log are no records, one record is records.
func TestHasRecords(t *testing.T) {
	root, cfg := newProject(t)
	has, err := HasRecords(root)
	require.NoError(t, err)
	require.False(t, has, "no log: no records")
	require.NoFileExists(t, paths.Long(logPath(root)), "and asking created nothing")

	l := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	has, err = HasRecords(root)
	require.NoError(t, err)
	require.False(t, has, "the empty log a ledger that recorded nothing leaves: no records")

	recordThenFlip(t, l, "sess-a")
	has, err = HasRecords(root)
	require.NoError(t, err)
	require.True(t, has)
}

// TestHasRecords_UnreadableLogIsNotNothing: a directory where the log belongs is not "no records".
func TestHasRecords_UnreadableLogIsNotNothing(t *testing.T) {
	root, _ := newProject(t)
	require.NoError(t, os.MkdirAll(paths.Long(logPath(root)), 0o700))
	has, err := HasRecords(root)
	require.Error(t, err)
	require.True(t, has, "unknown is answered as possibly holding records")
}

// TestDeferred_StandsInUntilALedgerIsOpen is the stand-in D49's frontier allowance begins a draft
// over: no records while the project holds none, the open ledger as soon as there is one, and a
// refusal, never an empty answer, for records no open ledger serves.
func TestDeferred_StandsInUntilALedgerIsOpen(t *testing.T) {
	root, cfg := newProject(t)
	ctx := context.Background()
	var cell Ledger
	d := Deferred(func() Ledger { return cell }, root)

	recs, err := d.All(ctx)
	require.NoError(t, err, "no ledger and no records: no negative knowledge, not an error")
	require.Empty(t, recs)
	_, err = d.Query(ctx, staleFilterTarget, staleFilterApproach, ScopeSession)
	require.ErrorIs(t, err, ErrNotOpen, "a question only a ledger can answer is refused")
	require.NoError(t, d.Close(), "the stand-in owns nothing")
	require.NoFileExists(t, paths.Long(logPath(root)), "and opens nothing")

	l := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	id := recordThenFlip(t, l, "sess-a")

	recs, err = d.All(ctx)
	require.ErrorIs(t, err, ErrNotOpen, "records on disk that no open ledger serves are never read as none")
	require.Nil(t, recs)

	cell = l
	recs, err = d.All(ctx)
	require.NoError(t, err, "the stand-in reaches the ledger as soon as one is open")
	require.Len(t, recs, 1)
	require.Equal(t, id, recs[0].ID)
	require.NoError(t, d.Close())
	_, err = l.Get(ctx, id)
	require.NoError(t, err, "closing the stand-in leaves the opener's ledger open")
	require.FileExists(t, paths.Long(filepath.Join(paths.Of(root).Records, logFileName)))
}
