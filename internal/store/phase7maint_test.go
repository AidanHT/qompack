package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// seedDemand writes n requested observations at ascending timestamps starting at 1.
func seedDemand(t *testing.T, l *store.DemandLog, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		require.NoError(t, l.Record(obs(store.DemandRequested, "k", int64(i))))
	}
}

// countLines returns how many non-empty lines p holds.
func countLines(t *testing.T, p string) int {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	n := 0
	for _, line := range splitLines(b) {
		if len(line) > 0 {
			n++
		}
	}
	return n
}

// splitLines splits on '\n' without allocating a scanner.
func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	return append(out, b[start:])
}

// TestCompactDemandLog_ExpiresWithoutTruncating pins commit 6's central rule: a record survives
// whole or is dropped whole, and expiry removes exactly what the horizon names.
func TestCompactDemandLog_ExpiresWithoutTruncating(t *testing.T) {
	t.Parallel()

	l, _ := newDemandLog(t)
	seedDemand(t, l, 10)

	rep, err := store.CompactDemandLog(context.Background(), l, store.DemandCompactOptions{ExpireBefore: 6})
	require.NoError(t, err)
	require.True(t, rep.Ran)
	require.Equal(t, 10, rep.Read)
	require.Equal(t, 5, rep.Expired)
	require.Equal(t, 5, rep.Kept)
	require.Zero(t, rep.Dropped)

	require.Equal(t, 5, countLines(t, l.Path()))
	agg, err := l.Aggregate()
	require.NoError(t, err)
	require.Equal(t, 5, agg["k"].Requests)
	require.Zero(t, agg[""].TelemetryGaps, "an expiry the caller asked for is not a gap")
}

// TestCompactDemandLog_ADropIsRecordedAsAGapNotAsAnAbsence pins the overflow rule: records lost to
// a cap are counted in the report AND leave a gap record, so a later reader learns observations
// existed and were not kept.
func TestCompactDemandLog_ADropIsRecordedAsAGapNotAsAnAbsence(t *testing.T) {
	t.Parallel()

	l, _ := newDemandLog(t)
	seedDemand(t, l, 10)

	rep, err := store.CompactDemandLog(context.Background(), l, store.DemandCompactOptions{
		ExpireBefore: 3, MaxRecords: 4,
	})
	require.NoError(t, err)
	require.True(t, rep.Ran)
	require.Equal(t, 2, rep.Expired)
	require.Equal(t, 4, rep.Kept)
	require.Equal(t, 4, rep.Dropped)

	require.Equal(t, 5, countLines(t, l.Path()), "four kept records plus one overflow record")

	agg, err := l.Aggregate()
	require.NoError(t, err)
	require.Equal(t, 4, agg["k"].Requests)
	require.Equal(t, 1, agg[""].TelemetryGaps,
		"the loss is visible as missing telemetry, not as an absence of demand")
}

// TestCompactDemandLog_ACancelledPassChangesNothing pins that there is no state in which some
// records have been dropped and the pass has not finished.
func TestCompactDemandLog_ACancelledPassChangesNothing(t *testing.T) {
	t.Parallel()

	l, _ := newDemandLog(t)
	seedDemand(t, l, 10)
	before := countLines(t, l.Path())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rep, err := store.CompactDemandLog(ctx, l, store.DemandCompactOptions{ExpireBefore: 6})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, rep.Ran)
	require.Contains(t, rep.Declined, "cancelled")

	require.Equal(t, before, countLines(t, l.Path()), "the original log is untouched")
	_, statErr := os.Stat(l.Path() + ".compacting")
	require.True(t, os.IsNotExist(statErr), "no staging file is left behind")
}

// TestCompactDemandLog_AReadBoundAbandonsRatherThanWritingAPrefix pins that a log longer than one
// pass may read is left alone, instead of being replaced by a compaction of its first N records.
func TestCompactDemandLog_AReadBoundAbandonsRatherThanWritingAPrefix(t *testing.T) {
	t.Parallel()

	l, _ := newDemandLog(t)
	seedDemand(t, l, 20)

	rep, err := store.CompactDemandLog(context.Background(), l, store.DemandCompactOptions{
		ExpireBefore: 10, MaxReadRecords: 5,
	})
	require.NoError(t, err, "a bound reached is not an error")
	require.False(t, rep.Ran)
	require.Contains(t, rep.Declined, "longer than this pass may read")
	require.Equal(t, 20, countLines(t, l.Path()))
}

// TestCompactDemandLog_RecoversAStagingFileFromACrashedPass pins that a later pass — from idle,
// from a fresh session, from anywhere — recovers, so SessionEnd is not the only recovery path.
func TestCompactDemandLog_RecoversAStagingFileFromACrashedPass(t *testing.T) {
	t.Parallel()

	l, _ := newDemandLog(t)
	seedDemand(t, l, 10)

	// What a process that died mid-pass leaves behind: a partial staging file.
	staging := l.Path() + ".compacting"
	require.NoError(t, os.WriteFile(staging, []byte("{\"v\":1,\"kind\":\"requested\",\"key\":\"k\"}\n"), 0o600))

	rep, err := store.CompactDemandLog(context.Background(), l, store.DemandCompactOptions{ExpireBefore: 6})
	require.NoError(t, err)
	require.True(t, rep.RecoveredStaging, "the stale staging file was removed, not resumed")
	require.True(t, rep.Ran)
	require.Equal(t, 5, countLines(t, l.Path()), "the pass rebuilt from the intact original")

	_, statErr := os.Stat(staging)
	require.True(t, os.IsNotExist(statErr))
}

// TestCompactDemandLog_DeclinesWhenThereIsNothingToDo pins that a pass with no expiry and no cap
// leaves the log alone rather than rewriting it for nothing.
func TestCompactDemandLog_DeclinesWhenThereIsNothingToDo(t *testing.T) {
	t.Parallel()

	l, _ := newDemandLog(t)
	seedDemand(t, l, 3)

	rep, err := store.CompactDemandLog(context.Background(), l, store.DemandCompactOptions{})
	require.NoError(t, err)
	require.False(t, rep.Ran)
	require.Contains(t, rep.Declined, "nothing to expire")
	require.Equal(t, 3, countLines(t, l.Path()))

	// And a project with no log at all is not an error.
	empty := store.OpenDemandLog(t.TempDir())
	rep, err = store.CompactDemandLog(context.Background(), empty, store.DemandCompactOptions{ExpireBefore: 5})
	require.NoError(t, err)
	require.False(t, rep.Ran)
	require.Contains(t, rep.Declined, "no demand log")
}

// TestCompactDemandLog_AnUnreadableLineIsDroppedByCompactionOnly pins that compaction is the one
// pass allowed to remove a damaged record, so it is not carried forward forever.
func TestCompactDemandLog_AnUnreadableLineIsDroppedByCompactionOnly(t *testing.T) {
	t.Parallel()

	l, root := newDemandLog(t)
	require.NoError(t, l.Record(obs(store.DemandRequested, "good", 10)))

	f, err := os.OpenFile(store.DemandPath(root), os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString("not json\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	rep, err := store.CompactDemandLog(context.Background(), l, store.DemandCompactOptions{ExpireBefore: 1})
	require.NoError(t, err)
	require.True(t, rep.Ran)
	require.Equal(t, 1, rep.Expired, "the damaged line is what the pass removed")
	require.Equal(t, 1, rep.Kept)

	agg, err := l.Aggregate()
	require.NoError(t, err)
	require.Equal(t, 1, agg["good"].Requests)
	require.Zero(t, agg[""].TelemetryGaps, "and it is no longer counted on every read")
}

// TestSweepSegmentFilters_NeedsAnExplicitKeepSet pins the guard that stops a transient read failure
// from looking like "delete every filter".
func TestSweepSegmentFilters_NeedsAnExplicitKeepSet(t *testing.T) {
	t.Parallel()

	_, err := store.SweepSegmentFilters(context.Background(), t.TempDir(), nil)
	require.Error(t, err)

	// An EMPTY set is a legitimate instruction and is honoured.
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	rep, err := store.SweepSegmentFilters(context.Background(), root, map[string]bool{})
	require.NoError(t, err)
	require.Zero(t, rep.Found)
}

// TestSweepSegmentFilters_RemovesOnlyWhatNoSegmentNamesAndOnlyWhatItRecognises pins both halves of
// the sweep: an orphan goes, a named filter stays, and an unrecognised file is left alone.
func TestSweepSegmentFilters_RemovesOnlyWhatNoSegmentNamesAndOnlyWhatItRecognises(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	sketches := paths.Of(root).Sketches

	for _, name := range []string{
		"seg-0001.bloom", "seg-0002.bloom", "seg-0003.bloom",
		"tried.bloom", "seg-notanumber.bloom", "seg-0004.bloom.bak", "cms.sketch",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(sketches, name), []byte("x"), 0o600))
	}

	rep, err := store.SweepSegmentFilters(context.Background(), root, map[string]bool{
		"sketches/seg-0002.bloom": true,
	})
	require.NoError(t, err)

	require.Equal(t, 3, rep.Found, "only the three well-formed filter names are candidates")
	require.Equal(t, 1, rep.Kept)
	require.Equal(t, []string{"seg-0001.bloom", "seg-0003.bloom"}, rep.Removed)
	require.Empty(t, rep.Failed)

	for _, name := range []string{"tried.bloom", "seg-notanumber.bloom", "seg-0004.bloom.bak", "cms.sketch"} {
		_, statErr := os.Stat(filepath.Join(sketches, name))
		require.NoError(t, statErr, "%s must be left alone", name)
	}
	_, statErr := os.Stat(filepath.Join(sketches, "seg-0002.bloom"))
	require.NoError(t, statErr, "a filter a live segment names stays")
}

// TestSweepSegmentFilters_CancellationStopsEarlyWithoutHalfRemoving pins that a cancelled sweep is
// still consistent: a delete either happened or did not.
func TestSweepSegmentFilters_CancellationStopsEarlyWithoutHalfRemoving(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	require.NoError(t, os.WriteFile(
		filepath.Join(paths.Of(root).Sketches, "seg-0001.bloom"), []byte("x"), 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rep, err := store.SweepSegmentFilters(ctx, root, map[string]bool{})
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, rep.Cancelled)
	require.Empty(t, rep.Removed)

	_, statErr := os.Stat(filepath.Join(paths.Of(root).Sketches, "seg-0001.bloom"))
	require.NoError(t, statErr)
}

// TestSweepSegmentFilters_AMissingSketchesDirectoryIsNotAnError pins that a project which never
// built a filter sweeps to nothing.
func TestSweepSegmentFilters_AMissingSketchesDirectoryIsNotAnError(t *testing.T) {
	t.Parallel()

	rep, err := store.SweepSegmentFilters(context.Background(), t.TempDir(), map[string]bool{})
	require.NoError(t, err)
	require.Zero(t, rep.Found)
	require.Empty(t, rep.Removed)
}
