package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// The refusals a diagnostic meets, and the contract the file-version index enforces before it
// writes anything.
//
// readonly_test.go sweeps the mutating surface through readOnlyRefusals; what is here is the rest
// of the read-only design — the delegate a diagnostic actually reads deltas through, the segment
// log's last-line-of-defence refusal, and the two arguments AppendFileVersion rejects outright.

// TestOpenReadOnly_ReadDeltaAnswersRatherThanRefusing.
//
// ReadOnlyStore is the READ half, so every method on it has to answer: a diagnostic that asked for
// a delta record and got ErrReadOnly would have no way to tell "this store is read-only" from
// "this delta is gone". An absent record is FidelityUnavailable with core.ErrNotFound — the same
// answer the writable store gives — and never FidelityCorrupt, which is a claim about bytes that
// were read.
func TestOpenReadOnly_ReadDeltaAnswersRatherThanRefusing(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()
	_, err := tp.Store.PutBytes(ctx, []byte("package main\n"), PutOptions{Tool: "Read", Path: "a.go"})
	require.NoError(t, err)
	require.NoError(t, tp.Store.Flush(ctx))
	require.NoError(t, tp.Store.Close())

	before := treeSnapshot(t, tp.Root)
	ro, err := OpenReadOnly(tp.Root, config.Defaults(), Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ro.Close() })

	rec, fid, err := ro.ReadDelta(ctx, core.Hash{9})
	require.ErrorIs(t, err, core.ErrNotFound)
	require.NotErrorIs(t, err, ErrReadOnly, "a read is not a refusal")
	require.Equal(t, FidelityUnavailable, fid)
	require.Equal(t, core.Hash{9}, rec.Root, "the answer names the record that is missing")
	require.Equal(t, before, treeSnapshot(t, tp.Root), "reading a delta wrote nothing")
}

// TestSegLog_AppendRefusesAReadOnlyLogAtItsChokePoint.
//
// Every public writer on the segment log consults writable() first, so this refusal is unreachable
// through the seam today — which is exactly why it is worth a test of its own. It is the guard for
// the writer somebody adds next and forgets to gate, and a guard nothing exercises is a guard that
// can be deleted as dead code.
func TestSegLog_AppendRefusesAReadOnlyLogAtItsChokePoint(t *testing.T) {
	t.Parallel()

	l := &segLog{
		byID:           make(map[core.SegmentID]*Segment),
		clk:            newFakeClock(),
		log:            logging.Nop(),
		readOnly:       true,
		warnedNoTokens: make(map[core.SegmentID]bool),
	}

	require.ErrorIs(t, l.append(segOpenRec{V: indexRecordVersion, Op: segOpOpen, ID: 1}), ErrReadOnly,
		"append must refuse before it marshals anything")
	require.NoError(t, l.sync(), "a log with no append handle has nothing to flush and must not fail Flush")
}

// TestAppendFileVersion_RefusesAVersionThatNamesNothing.
//
// A file version is a claim that a path had specific content at a specific moment, so a record with
// no root hash and a record with no path are both unusable — and unusable in a way that only shows
// up later, as a history entry that resolves to nothing or a key nothing can look up. Both are
// refused before the line is appended, so the log never carries one.
func TestAppendFileVersion_RefusesAVersionThatNamesNothing(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()

	err := tp.Store.AppendFileVersion(ctx, "src/a.go", FileVersion{TS: 1, Turn: 1})
	require.ErrorIs(t, err, core.ErrNotFound)
	require.Contains(t, err.Error(), "src/a.go", "the refusal names the path it could not record")

	err = tp.Store.AppendFileVersion(ctx, ".", FileVersion{TS: 1, Root: core.Hash{1}, Turn: 1})
	require.ErrorIs(t, err, core.ErrNotFound, "a bare dot normalizes to no path at all")

	require.NoError(t, tp.Store.Flush(ctx))
	_, statErr := os.Stat(paths.Long(filepath.Join(tp.Store.l.Index, filesViewNam)))
	require.ErrorIs(t, statErr, os.ErrNotExist,
		"nothing was recorded, so Flush had no view to materialize")
}

// TestFileAt_RefusesAClosedStore. FileAt reads from memory, and memory outlives Close; without the
// use() guard a closed store would keep answering file-history questions from indices it has
// already released, which is the class of bug degraded_test.go's sweep exists for.
func TestFileAt_RefusesAClosedStore(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()
	require.NoError(t, tp.Store.AppendFileVersion(ctx, "src/a.go",
		FileVersion{TS: 10, Root: core.Hash{1}, Turn: 1, Bytes: 11}))
	require.NoError(t, tp.Store.Close())

	_, err := tp.Store.FileAt(ctx, "src/a.go", time.Time{})
	require.ErrorIs(t, err, core.ErrDegraded)
}

// TestEncodedObjectLimit_PadsThePlaintextLimitRatherThanRepeatingIt.
//
// fsck's objects row names this limit, and it must be the one readObjectFile applies: a file fsck
// calls acceptable and the store then refuses is a defect the report does not have. The limit is
// deliberately LARGER than MaxPutBytes because zstd framing can grow incompressible input, and a
// bound that forgot the padding would reject valid objects.
func TestEncodedObjectLimit_PadsThePlaintextLimitRatherThanRepeatingIt(t *testing.T) {
	t.Parallel()

	require.Greater(t, EncodedObjectLimit(), int64(MaxPutBytes),
		"the encoded bound must leave room for the encoder's worst-case framing")
	require.Equal(t, encodedObjectLimit(), EncodedObjectLimit(),
		"the exported bound is the one the store's own reader applies")
}
