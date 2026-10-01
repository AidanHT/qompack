package store_test

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/store"
)

// Edge rows for the all-or-nothing demand compaction that its main tests leave unexecuted: a blank
// line is not a record, a log that cannot be read to its end is left exactly as it was, and a stale
// staging entry that cannot be cleared stops the pass before it reads anything (w16b-cover, C3.6).

// TestCompactDemandLog_SkipsABlankLineAndCountsOnlyRecords: a blank line in the log is neither read
// nor kept; the compacted log holds the surviving records only.
func TestCompactDemandLog_SkipsABlankLineAndCountsOnlyRecords(t *testing.T) {
	t.Parallel()
	l, _ := newDemandLog(t)
	seedDemand(t, l, 4)
	f, err := os.OpenFile(l.Path(), os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString("\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	rep, err := store.CompactDemandLog(context.Background(), l, store.DemandCompactOptions{ExpireBefore: 3})
	require.NoError(t, err)
	require.True(t, rep.Ran)
	require.Equal(t, 4, rep.Read, "the blank line is not a record")
	require.Equal(t, 2, countLines(t, l.Path()))
}

// TestCompactDemandLog_ALogItCannotReadToTheEndIsLeftAlone: a line past the scanner's bound means the
// pass cannot see the whole log, so it declines, reports the read error and changes nothing.
func TestCompactDemandLog_ALogItCannotReadToTheEndIsLeftAlone(t *testing.T) {
	t.Parallel()
	l, _ := newDemandLog(t)
	seedDemand(t, l, 3)
	f, err := os.OpenFile(l.Path(), os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, err = f.Write(append(bytes.Repeat([]byte{'x'}, bufio.MaxScanTokenSize+1), '\n'))
	require.NoError(t, err)
	require.NoError(t, f.Close())
	before, err := os.ReadFile(l.Path())
	require.NoError(t, err)

	rep, err := store.CompactDemandLog(context.Background(), l, store.DemandCompactOptions{ExpireBefore: 3})
	require.Error(t, err)
	require.False(t, rep.Ran)
	require.Equal(t, "the log could not be read to the end", rep.Declined)
	after, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	require.Equal(t, before, after)
}

// TestCompactDemandLog_AStaleStagingEntryThatCannotBeClearedStopsThePass: crash recovery removes a
// stale staging file before anything else; when what sits there cannot be removed (a non-empty
// directory), the pass reports that and leaves the log alone.
func TestCompactDemandLog_AStaleStagingEntryThatCannotBeClearedStopsThePass(t *testing.T) {
	t.Parallel()
	l, _ := newDemandLog(t)
	seedDemand(t, l, 4)
	staging := l.Path() + ".compacting"
	require.NoError(t, os.MkdirAll(filepath.Join(staging, "held"), 0o700))

	rep, err := store.CompactDemandLog(context.Background(), l, store.DemandCompactOptions{ExpireBefore: 3})
	require.ErrorContains(t, err, "stale demand staging file")
	require.False(t, rep.Ran)
	require.Equal(t, 4, countLines(t, l.Path()))
}
