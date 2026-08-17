package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// segSession is the session every segment test opens its segments under.
const segSession = core.SessionID("sess-7f")

// openClosed opens a segment spanning [start, end] and closes it, returning its ID.
func openClosed(t *testing.T, sl SegmentLog, s core.SessionID, start, end core.TurnIndex) core.SegmentID {
	t.Helper()
	ctx := context.Background()
	id, err := sl.Open(ctx, Segment{Session: s, StartTurn: start})
	require.NoError(t, err)
	require.NoError(t, sl.Close(ctx, id, end, map[string]float64{"tokens": 100}))
	return id
}

// appendRawSegmentLine appends a hand-written record to index/segments.jsonl. It is how the bloom
// test simulates SP-16's future writer without SP-06 growing one.
func appendRawSegmentLine(t *testing.T, root, line string) {
	t.Helper()
	p := filepath.Join(paths.Of(root).Index, segmentsFile)
	f, err := os.OpenFile(paths.Long(p), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(line + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func TestSegment_OpenAllocatesMonotonicIDs(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	for want := core.SegmentID(1); want <= 3; want++ {
		got, err := sl.Open(ctx, Segment{Session: segSession, StartTurn: core.TurnIndex(want)})
		require.NoError(t, err)
		require.Equal(t, want, got, "segment IDs are 1-based and monotonic per project")
		require.NoError(t, sl.Close(ctx, got, core.TurnIndex(want), map[string]float64{"tokens": 1}))
	}
}

func TestSegment_OpenRejectsExistingID(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	id, err := sl.Open(ctx, Segment{Session: segSession, StartTurn: 0})
	require.NoError(t, err)
	require.Equal(t, core.SegmentID(1), id)

	_, err = sl.Open(ctx, Segment{ID: 1, Session: segSession, StartTurn: 5})
	require.ErrorIs(t, err, ErrSegmentExists,
		"reusing an id would make two spans indistinguishable in every checkpoint that references them")
}

func TestSegment_CloseSetsEndTurnAndFeatures(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	id, err := sl.Open(ctx, Segment{Session: segSession, StartTurn: 40})
	require.NoError(t, err)

	feats := map[string]float64{
		"paths": 0.42, "tools": 0.19, "time": 0.03, "todos": 1, "tokens": 18402,
	}
	require.NoError(t, sl.Close(ctx, id, 57, feats))

	got, err := sl.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, got.Closed)
	require.Equal(t, core.TurnIndex(57), got.EndTurn)
	require.Equal(t, core.Tokens(18402), got.Tokens, "Segment.Tokens comes from feats[\"tokens\"]")
	require.Equal(t, map[string]float64{"paths": 0.42, "tools": 0.19, "time": 0.03, "todos": 1}, got.Features,
		"the tokens pseudo-feature is removed from the persisted feature summary")
}

func TestSegment_CloseIdempotent(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	id, err := sl.Open(ctx, Segment{Session: segSession, StartTurn: 0})
	require.NoError(t, err)
	require.NoError(t, sl.Close(ctx, id, 5, map[string]float64{"tokens": 10}))

	before := len(indexLines(t, f.root, segmentsFile))
	require.NoError(t, sl.Close(ctx, id, 5, map[string]float64{"tokens": 10}))
	require.Len(t, indexLines(t, f.root, segmentsFile), before, "a repeated close at the same turn writes nothing")
}

func TestSegment_CloseConflict(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	id, err := sl.Open(ctx, Segment{Session: segSession, StartTurn: 0})
	require.NoError(t, err)
	require.NoError(t, sl.Close(ctx, id, 5, map[string]float64{"tokens": 10}))

	err = sl.Close(ctx, id, 9, map[string]float64{"tokens": 10})
	require.ErrorIs(t, err, core.ErrAppendOnly,
		"a segment's span is fixed once a checkpoint may already have encoded it")

	// An end turn before the start turn is a caller bug, not an append-only violation.
	id2, err := sl.Open(ctx, Segment{Session: segSession, StartTurn: 20})
	require.NoError(t, err)
	err = sl.Close(ctx, id2, 10, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "precedes start turn")

	require.ErrorIs(t, sl.Close(ctx, core.SegmentID(999), 1, nil), core.ErrNotFound)
}

func TestSegment_MarkEncodedSetsFlag(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	id := openClosed(t, sl, segSession, 0, 5)
	require.NoError(t, sl.MarkEncoded(ctx, []core.SegmentID{id}, 7))

	got, err := sl.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, got.EncodedOnce)
	require.Equal(t, core.CheckpointSeq(7), got.CheckpointSeq)
}

func TestSegment_MarkEncodedIdempotentSameSeq(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	id := openClosed(t, sl, segSession, 0, 5)
	require.NoError(t, sl.MarkEncoded(ctx, []core.SegmentID{id}, 7))
	before := len(indexLines(t, f.root, segmentsFile))
	require.NoError(t, sl.MarkEncoded(ctx, []core.SegmentID{id}, 7))
	require.Len(t, indexLines(t, f.root, segmentsFile), before, "exactly one encode record")
}

// TestSegment_MarkEncodedRefusesDifferentSeq is the DPI guard of Qompack.md §4.6 and §8.2: a
// segment already encoded into a checkpoint is never re-encoded into a different one. It is
// re-encoded from the ORIGINAL chunks or not at all — which is the whole mechanism that stops
// "never compress a compression" from being merely a convention.
func TestSegment_MarkEncodedRefusesDifferentSeq(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	id := openClosed(t, sl, segSession, 0, 5)
	require.NoError(t, sl.MarkEncoded(ctx, []core.SegmentID{id}, 7))

	err := sl.MarkEncoded(ctx, []core.SegmentID{id}, 8)
	require.ErrorIs(t, err, core.ErrAlreadyEncoded)

	got, err2 := sl.Get(ctx, id)
	require.NoError(t, err2)
	require.True(t, got.EncodedOnce)
	require.Equal(t, core.CheckpointSeq(7), got.CheckpointSeq,
		"a refused MarkEncoded must not change the segment's recorded CheckpointSeq")
}

// TestSegment_MarkEncodedBatchIsAllOrNothing proves the batch is validated before anything is
// appended: one already-encoded member must not leave its siblings half-marked, because a
// half-applied checkpoint would be exactly the recursive-compression state §4.6 forbids.
func TestSegment_MarkEncodedBatchIsAllOrNothing(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	_ = openClosed(t, sl, segSession, 0, 5)      // id 1
	id2 := openClosed(t, sl, segSession, 6, 10)  // id 2
	id3 := openClosed(t, sl, segSession, 11, 15) // id 3

	require.NoError(t, sl.MarkEncoded(ctx, []core.SegmentID{id3}, 1))
	linesBefore := len(indexLines(t, f.root, segmentsFile))

	err := sl.MarkEncoded(ctx, []core.SegmentID{id2, id3}, 2)
	require.ErrorIs(t, err, core.ErrAlreadyEncoded)

	got2, err := sl.Get(ctx, id2)
	require.NoError(t, err)
	require.False(t, got2.EncodedOnce, "segment 2 must remain unencoded")

	got3, err := sl.Get(ctx, id3)
	require.NoError(t, err)
	require.Equal(t, core.CheckpointSeq(1), got3.CheckpointSeq)

	require.Len(t, indexLines(t, f.root, segmentsFile), linesBefore,
		"a refused batch appends no record at all")
}

func TestSegment_MarkEncodedRejectsOpen(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	id, err := sl.Open(ctx, Segment{Session: segSession, StartTurn: 0})
	require.NoError(t, err)

	require.ErrorIs(t, sl.MarkEncoded(ctx, []core.SegmentID{id}, 1), ErrSegmentOpen)
	require.ErrorIs(t, sl.MarkEncoded(ctx, []core.SegmentID{999}, 1), core.ErrNotFound)
}

// TestSegment_FrontierIsContiguous pins the property a non-contiguous maximum would misstate:
// with segments 1, 2 and 4 encoded, coverage ends where segment 2 ends, because segment 3 exists
// only in the raw log and no checkpoint covers it.
func TestSegment_FrontierIsContiguous(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	id1 := openClosed(t, sl, segSession, 0, 9)
	id2 := openClosed(t, sl, segSession, 10, 19)
	_ = openClosed(t, sl, segSession, 20, 29) // id 3, deliberately left unencoded
	id4 := openClosed(t, sl, segSession, 30, 39)

	require.NoError(t, sl.MarkEncoded(ctx, []core.SegmentID{id1, id2, id4}, 1))

	frontier, err := sl.Frontier(ctx, segSession)
	require.NoError(t, err)
	require.Equal(t, core.TurnIndex(19), frontier,
		"the frontier is segment 2's EndTurn, not segment 4's: a checkpoint covers the session only THROUGH turn 19")
}

func TestSegment_FrontierZeroWhenNoneEncoded(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	openClosed(t, sl, segSession, 0, 9)
	openClosed(t, sl, segSession, 10, 19)
	openClosed(t, sl, segSession, 20, 29)

	frontier, err := sl.Frontier(ctx, segSession)
	require.NoError(t, err)
	require.Equal(t, core.TurnIndex(0), frontier)

	// An unknown session has no coverage either, and reports the same zero.
	frontier, err = sl.Frontier(ctx, "sess-never-seen")
	require.NoError(t, err)
	require.Equal(t, core.TurnIndex(0), frontier)
}

func TestSegment_Unencoded(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	id1 := openClosed(t, sl, segSession, 0, 9)
	id2 := openClosed(t, sl, segSession, 10, 19)
	id3 := openClosed(t, sl, segSession, 20, 29)
	_, err := sl.Open(ctx, Segment{Session: segSession, StartTurn: 30}) // still open
	require.NoError(t, err)

	require.NoError(t, sl.MarkEncoded(ctx, []core.SegmentID{id1, id2}, 1))

	got, err := sl.Unencoded(ctx, segSession)
	require.NoError(t, err)
	require.Len(t, got, 1, "only the closed, unencoded segment; an open one cannot be encoded yet")
	require.Equal(t, id3, got[0].ID)
}

func TestSegment_Current(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	_, err := sl.Current(ctx, segSession)
	require.ErrorIs(t, err, core.ErrNotFound)

	id, err := sl.Open(ctx, Segment{Session: segSession, StartTurn: 0})
	require.NoError(t, err)

	got, err := sl.Current(ctx, segSession)
	require.NoError(t, err)
	require.Equal(t, id, got.ID)

	require.NoError(t, sl.Close(ctx, id, 5, map[string]float64{"tokens": 1}))
	_, err = sl.Current(ctx, segSession)
	require.ErrorIs(t, err, core.ErrNotFound)
}

func TestSegment_Range(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	id1 := openClosed(t, sl, segSession, 0, 10)
	id2 := openClosed(t, sl, segSession, 11, 20)
	id3, err := sl.Open(ctx, Segment{Session: segSession, StartTurn: 21}) // open, no EndTurn yet
	require.NoError(t, err)

	got, err := sl.Range(ctx, 5, 15)
	require.NoError(t, err)
	require.Equal(t, []core.SegmentID{id1, id2}, segIDs(got))

	got, err = sl.Range(ctx, 25, 30)
	require.NoError(t, err)
	require.Equal(t, []core.SegmentID{id3}, segIDs(got),
		"an open segment matches whenever the upper bound reaches its StartTurn")

	got, err = sl.Range(ctx, 0, 100)
	require.NoError(t, err)
	require.Equal(t, []core.SegmentID{id1, id2, id3}, segIDs(got), "ascending by StartTurn")
}

// TestSegment_BloomRefParsed proves SP-16's reservation: SP-06 PARSES a bloom record and surfaces
// it as Segment.BloomRef, but never writes one.
func TestSegment_BloomRefParsed(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()

	id, err := f.s.Segments().Open(ctx, Segment{ID: 12, Session: segSession, StartTurn: 40})
	require.NoError(t, err)
	require.Equal(t, core.SegmentID(12), id)
	require.NoError(t, f.s.Segments().Close(ctx, id, 57, map[string]float64{"tokens": 5}))

	// Nothing SP-06 writes ever produces a bloom record.
	for _, line := range indexLines(t, f.root, segmentsFile) {
		require.NotContains(t, string(line), `"op":"bloom"`)
	}

	require.NoError(t, f.s.Close())
	appendRawSegmentLine(t, f.root, `{"v":1,"op":"bloom","id":12,"ref":"sketches/seg-0012.bloom"}`)

	s2, err := openFS(f.root, config.Defaults(), Deps{Log: logging.Nop(), Clock: f.clk, Metrics: f.reg})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s2.Close() })

	got, err := s2.Segments().Get(ctx, 12)
	require.NoError(t, err)
	require.Equal(t, "sketches/seg-0012.bloom", got.BloomRef)
}

func TestSegment_SurvivesReopen(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	sl := f.s.Segments()

	id, err := sl.Open(ctx, Segment{Session: segSession, StartTurn: 40})
	require.NoError(t, err)
	require.NoError(t, sl.Close(ctx, id, 57, map[string]float64{
		"paths": 0.42, "tools": 0.19, "tokens": 18402,
	}))
	require.NoError(t, sl.MarkEncoded(ctx, []core.SegmentID{id}, 7))

	before, err := sl.Get(ctx, id)
	require.NoError(t, err)

	s2 := f.reopen(t)
	after, err := s2.Segments().Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, before, after, "every field round-trips through index/segments.jsonl")

	// A reopened log keeps allocating monotonically from where it left off.
	next, err := s2.Segments().Open(ctx, Segment{Session: segSession, StartTurn: 58})
	require.NoError(t, err)
	require.Equal(t, id+1, next)
}

// TestSegment_DegradesAfterClose pins the documented §5.8 exception: Segments() keeps returning a
// non-nil log after Close, and that log's own methods report core.ErrDegraded.
func TestSegment_DegradesAfterClose(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	sl := f.s.Segments()

	require.NoError(t, f.s.Close())
	require.NotNil(t, f.s.Segments())

	_, err := sl.Open(ctx, Segment{Session: segSession})
	require.ErrorIs(t, err, core.ErrDegraded)
	_, err = sl.Get(ctx, 1)
	require.ErrorIs(t, err, core.ErrDegraded)
	require.ErrorIs(t, sl.MarkEncoded(ctx, []core.SegmentID{1}, 1), core.ErrDegraded)
	_, err = sl.Frontier(ctx, segSession)
	require.ErrorIs(t, err, core.ErrDegraded)
}

// segIDs projects segments down to their IDs.
func segIDs(segs []Segment) []core.SegmentID {
	out := make([]core.SegmentID, len(segs))
	for i, s := range segs {
		out[i] = s.ID
	}
	return out
}

// TestPropMarkEncodedNeverDowngrades asserts the DPI guard's monotonicity: once EncodedOnce is
// true, no sequence of MarkEncoded calls can clear it or change the recorded CheckpointSeq.
func TestPropMarkEncodedNeverDowngrades(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	sl := f.s.Segments()

	const segments = 6
	ids := make([]core.SegmentID, 0, segments)
	for i := 0; i < segments; i++ {
		ids = append(ids, openClosed(t, sl, segSession, core.TurnIndex(i*10), core.TurnIndex(i*10+9)))
	}

	// encodedAt tracks the first seq each segment was encoded into; zero means "not yet".
	encodedAt := make(map[core.SegmentID]core.CheckpointSeq, segments)

	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 3).Draw(rt, "batch")
		seq := core.CheckpointSeq(rapid.IntRange(1, 5).Draw(rt, "seq"))

		batch := make([]core.SegmentID, 0, n)
		for i := 0; i < n; i++ {
			batch = append(batch, ids[rapid.IntRange(0, segments-1).Draw(rt, "id")])
		}

		err := sl.MarkEncoded(ctx, batch, seq)
		if err == nil {
			for _, id := range batch {
				if _, seen := encodedAt[id]; !seen {
					encodedAt[id] = seq
				}
			}
		}

		// Whatever happened, no segment may have downgraded.
		for _, id := range ids {
			got, gerr := sl.Get(ctx, id)
			if gerr != nil {
				rt.Fatalf("Get(%d): %v", id, gerr)
			}
			want, wasEncoded := encodedAt[id]
			if wasEncoded {
				if !got.EncodedOnce {
					rt.Fatalf("segment %d lost EncodedOnce", id)
				}
				if got.CheckpointSeq != want {
					rt.Fatalf("segment %d moved from checkpoint %d to %d", id, want, got.CheckpointSeq)
				}
			} else if got.EncodedOnce {
				rt.Fatalf("segment %d became encoded without a successful MarkEncoded", id)
			}
		}
	})
}

// BenchmarkMarkEncoded_100 measures one 100-segment batch, which is the shape PreCompact drives
// inside budget B-E (p99 < 2 s). Budget: ≤ 1 ms/op.
func BenchmarkMarkEncoded_100(b *testing.B) {
	const segments = 100
	ctx := context.Background()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		dir := b.TempDir()
		require.NoError(b, os.MkdirAll(paths.Long(dir), 0o700))
		sl, err := openSegLog(filepath.Join(dir, segmentsFile), newIdxClock(), logging.Nop())
		require.NoError(b, err)

		ids := make([]core.SegmentID, 0, segments)
		for j := 0; j < segments; j++ {
			id, oerr := sl.Open(ctx, Segment{Session: segSession, StartTurn: core.TurnIndex(j * 10)})
			require.NoError(b, oerr)
			require.NoError(b, sl.Close(ctx, id, core.TurnIndex(j*10+9), map[string]float64{"tokens": 1}))
			ids = append(ids, id)
		}
		b.StartTimer()

		if err := sl.MarkEncoded(ctx, ids, 1); err != nil {
			b.Fatal(err)
		}

		b.StopTimer()
		sl.degrade()
		b.StartTimer()
	}
}

// TestSegment_MirrorsFrozenDPIGuardCase replays storetest.runMarkEncodedDPIGuardCase's exact
// sequence against the real segLog.
//
// The conformance suite itself cannot reach this implementation until the integration commit
// flips store.Open off the SP-01 stub, and storetest imports store so an internal test cannot run
// it. Mirroring the case here means the frozen assertions are already proven green rather than
// discovered at integration time.
func TestSegment_MirrorsFrozenDPIGuardCase(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	id, err := sl.Open(ctx, Segment{Session: "sess-dpi-guard", StartTurn: 0})
	require.NoError(t, err)
	require.NoError(t, sl.Close(ctx, id, core.TurnIndex(5), map[string]float64{"path_jaccard": 0}))

	const firstSeq = core.CheckpointSeq(1)
	require.NoError(t, sl.MarkEncoded(ctx, []core.SegmentID{id}, firstSeq))
	require.NoError(t, sl.MarkEncoded(ctx, []core.SegmentID{id}, firstSeq))

	got, err := sl.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, got.EncodedOnce)
	require.Equal(t, firstSeq, got.CheckpointSeq)

	const secondSeq = core.CheckpointSeq(2)
	require.ErrorIs(t, sl.MarkEncoded(ctx, []core.SegmentID{id}, secondSeq), core.ErrAlreadyEncoded)

	got, err = sl.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, got.EncodedOnce)
	require.Equal(t, firstSeq, got.CheckpointSeq,
		"a refused MarkEncoded must not change the segment's recorded CheckpointSeq")
}

// TestSegment_MirrorsFrozenRangeNeverShrinksCase replays storetest.runRangeNeverShrinksCase. It
// closes with NIL features on purpose, which is the path that leaves Segment.Tokens at zero.
func TestSegment_MirrorsFrozenRangeNeverShrinksCase(t *testing.T) {
	f := newIdxStore(t)
	sl := f.s.Segments()
	ctx := context.Background()

	firstID, err := sl.Open(ctx, Segment{Session: "sess-range", StartTurn: 0})
	require.NoError(t, err)
	require.NoError(t, sl.Close(ctx, firstID, core.TurnIndex(3), nil))

	before, err := sl.Range(ctx, core.TurnIndex(0), core.TurnIndex(3))
	require.NoError(t, err)
	require.Contains(t, segIDs(before), firstID)

	secondID, err := sl.Open(ctx, Segment{Session: "sess-range", StartTurn: 4})
	require.NoError(t, err)
	require.NoError(t, sl.Close(ctx, secondID, core.TurnIndex(7), nil))

	after, err := sl.Range(ctx, core.TurnIndex(0), core.TurnIndex(7))
	require.NoError(t, err)
	require.Contains(t, segIDs(after), firstID,
		"opening a later segment must not make an earlier one disappear from Range")
	require.Contains(t, segIDs(after), secondID)
}
