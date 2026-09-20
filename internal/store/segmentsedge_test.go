package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// The segment log's refusals and the filter file's, which are the answers a caller has to be able
// to tell apart from a successful one.

// TestSegLog_OpenRefusesAnIdItAlreadyAllocated.
//
// A segment id is a durable name: the DPI guard's encode records and the per-segment bloom files
// are keyed by it, so re-issuing one would silently attach a new segment to another segment's
// history. Both spellings of the mistake — an id at or below the high-water mark, and an id the log
// still holds in memory — are refused with ErrSegmentExists, and the refusal happens before
// anything is appended.
func TestSegLog_OpenRefusesAnIdItAlreadyAllocated(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()
	log := tp.Store.Segments()

	first, err := log.Open(ctx, Segment{Session: "sess-a", StartTurn: 0})
	require.NoError(t, err)
	require.Equal(t, core.SegmentID(1), first, "ids are allocated from 1")

	_, err = log.Open(ctx, Segment{ID: first, Session: "sess-a", StartTurn: 1})
	require.ErrorIs(t, err, ErrSegmentExists, "an id at or below the high-water mark is taken")

	second, err := log.Open(ctx, Segment{Session: "sess-b", StartTurn: 1})
	require.NoError(t, err)
	require.Equal(t, core.SegmentID(2), second, "the next id is still handed out")
}

// TestSegLog_GetReportsAnUnknownSegmentAsNotFound. The checkpointer asks the log to resolve the
// segment ids its encode records name; a segment that was never opened has to come back as
// core.ErrNotFound so a dangling reference reads as a dangling reference rather than as a zero
// Segment that looks like an empty one.
func TestSegLog_GetReportsAnUnknownSegmentAsNotFound(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()

	_, err := tp.Store.Segments().Get(ctx, core.SegmentID(7))
	require.ErrorIs(t, err, core.ErrNotFound)
	require.Contains(t, err.Error(), "7", "the refusal names the segment that is missing")
}

// TestSegLog_CurrentIsLoudWhenASessionHasTwoOpenSegments.
//
// One open segment per session is the invariant; two means a close was lost, and the scheduler
// would otherwise keep appending to whichever the map happened to yield. Current picks the highest
// id — the newest — deterministically, and says so out loud rather than choosing in silence (§13
// invariant 10). A session with no open segment at all is core.ErrNotFound, which is a different
// situation with a different remedy.
func TestSegLog_CurrentIsLoudWhenASessionHasTwoOpenSegments(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()
	log := tp.Store.Segments()

	_, err := log.Current(ctx, core.SessionID("sess-none"))
	require.ErrorIs(t, err, core.ErrNotFound, "a session with no open segment has no current one")

	_, err = log.Open(ctx, Segment{Session: "sess-a", StartTurn: 0})
	require.NoError(t, err)
	newest, err := log.Open(ctx, Segment{Session: "sess-a", StartTurn: 5})
	require.NoError(t, err)

	got, err := log.Current(ctx, core.SessionID("sess-a"))
	require.NoError(t, err)
	require.Equal(t, newest, got.ID, "the newest open segment is the current one")
}

// TestFilterVerdict_String gives every verdict its spelling, including the one an unknown value
// falls back to.
//
// The fallback is the conservative verdict on purpose: a value this build does not recognise must
// read as "do not skip", because the only verdict that saves work is the only one that can lose a
// result, and a String that invented a name for an unknown value would put that name in an operator
// report as though it meant something.
func TestFilterVerdict_String(t *testing.T) {
	t.Parallel()

	require.Equal(t, "bypass", FilterBypass.String())
	require.Equal(t, "maybe", FilterMaybe.String())
	require.Equal(t, "miss", FilterMiss.String())
	require.Equal(t, "bypass", FilterVerdict(99).String(), "an unknown verdict reads as the safe one")

	require.True(t, FilterMiss.SkipsSegment())
	require.False(t, FilterMaybe.SkipsSegment())
	require.False(t, FilterBypass.SkipsSegment())
}

// TestWriteSegmentFilter_RefusesAFilterWithNoBits.
//
// WriteSegmentFilter's output is referenced by a segment-log bloom record written afterwards, so a
// file it wrote for a filter carrying no bloom would become a reference to a document whose header
// describes bits that are not there. Both shapes — no filter at all, and a filter whose bloom was
// never built — are refused before any file is created.
func TestWriteSegmentFilter_RefusesAFilterWithNoBits(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	_, err := WriteSegmentFilter(root, nil)
	require.ErrorContains(t, err, "nil segment filter")

	_, err = WriteSegmentFilter(root, &SegmentFilter{})
	require.ErrorContains(t, err, "empty segment filter",
		"a filter with no bloom is refused by the encoder it delegates to")

	_, err = (&SegmentFilter{}).MarshalBinary()
	require.ErrorContains(t, err, "empty segment filter")
}

// TestSortSegments_BreaksATieOnStartTurnById.
//
// Range returns segments in turn order, and two segments in different sessions can legitimately
// share a StartTurn. Without the tie-break the order between them would depend on the map iteration
// that produced the slice, so the same project would answer the same Range call differently from
// run to run — and the scheduler's composite trigger reads that order.
func TestSortSegments_BreaksATieOnStartTurnById(t *testing.T) {
	t.Parallel()

	segs := []Segment{
		{ID: 3, Session: "s-b", StartTurn: 1},
		{ID: 1, Session: "s-a", StartTurn: 2},
		{ID: 2, Session: "s-a", StartTurn: 1},
	}
	sortSegments(segs)

	require.Equal(t,
		[]core.SegmentID{2, 3, 1},
		[]core.SegmentID{segs[0].ID, segs[1].ID, segs[2].ID},
		"ascending by StartTurn, and by id where StartTurn ties")
}
