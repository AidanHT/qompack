package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// encodeLinesOnDisk returns every encode record index/segments.jsonl holds, as (id, seq) pairs.
func encodeLinesOnDisk(t *testing.T, root string) map[core.SegmentID]core.CheckpointSeq {
	t.Helper()
	out := map[core.SegmentID]core.CheckpointSeq{}
	for _, line := range indexLines(t, root, segmentsFile) {
		var rec segEncodeRec
		require.NoError(t, json.Unmarshal(line, &rec))
		if rec.Op == segOpEncode {
			out[rec.ID] = rec.Seq
		}
	}
	return out
}

// TestSegment_ReserveEncodedWritesNothingAndDiesWithTheLog is the store half of F-UAT03-2. A
// reservation is MarkEncoded's in-memory effect — the scheduler, Frontier and Unencoded see the
// segment as encoded — with no record appended, so index/segments.jsonl never names a draft's
// sequence number, and a reopened log (a restart, an idle exit) has no reservation at all.
func TestSegment_ReserveEncodedWritesNothingAndDiesWithTheLog(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	sl := f.s.Segments()
	id := openClosed(t, sl, segSession, 0, 7)

	r, ok := sl.(SegmentReservation)
	require.True(t, ok, "the store's own segment log offers the two-phase encode")
	require.NoError(t, r.ReserveEncoded(ctx, []core.SegmentID{id}, 2))

	seg, err := sl.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, seg.EncodedOnce, "a reserved segment reads as encoded in this process")
	require.Equal(t, core.CheckpointSeq(2), seg.CheckpointSeq)
	fr, err := sl.Frontier(ctx, segSession)
	require.NoError(t, err)
	require.Equal(t, core.TurnIndex(7), fr, "the frontier counts a reservation")
	un, err := sl.Unencoded(ctx, segSession)
	require.NoError(t, err)
	require.Empty(t, un, "Unencoded does not offer a reserved segment again")

	require.Empty(t, encodeLinesOnDisk(t, f.root), "a reservation appends no encode record")

	after, err := f.reopen(t).Segments().Get(ctx, id)
	require.NoError(t, err)
	require.False(t, after.EncodedOnce, "a reservation is not durable: the reopened log has none")
}

// TestSegment_ReserveEncodedKeepsTheDPIGuard asserts a reservation guards exactly like a mark: the
// same seq is idempotent, and a different one is refused whether the existing mark is durable or
// is itself a reservation, with the whole batch validated before anything changes.
func TestSegment_ReserveEncodedKeepsTheDPIGuard(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	sl := f.s.Segments()
	r, ok := sl.(SegmentReservation)
	require.True(t, ok)
	a := openClosed(t, sl, segSession, 0, 3)
	b := openClosed(t, sl, segSession, 4, 7)
	c := openClosed(t, sl, segSession, 8, 9)

	require.NoError(t, r.ReserveEncoded(ctx, []core.SegmentID{a}, 2))
	require.NoError(t, r.ReserveEncoded(ctx, []core.SegmentID{a}, 2), "the same seq is idempotent")
	require.ErrorIs(t, r.ReserveEncoded(ctx, []core.SegmentID{a}, 3), core.ErrAlreadyEncoded,
		"a reservation into another seq is the DPI violation it would be for a mark")
	require.ErrorIs(t, sl.MarkEncoded(ctx, []core.SegmentID{a}, 3), core.ErrAlreadyEncoded,
		"MarkEncoded honours a reservation into another seq")

	require.NoError(t, sl.MarkEncoded(ctx, []core.SegmentID{b}, 1))
	require.ErrorIs(t, r.ReserveEncoded(ctx, []core.SegmentID{c, b}, 2), core.ErrAlreadyEncoded)
	got, err := sl.Get(ctx, c)
	require.NoError(t, err)
	require.False(t, got.EncodedOnce, "a refused batch reserves none of its members")
}

// TestSegment_CommitEncodedWritesTheSealedSequence asserts the seal's half: the reserved segments'
// records are written at the sequence the checkpoint was ACTUALLY sealed at (a reservation, never
// written, can follow a bumped seal), a segment the log lost is written too, an id already durable
// at that seq is idempotent, and only a durable mark into ANOTHER seq refuses — the whole batch.
func TestSegment_CommitEncodedWritesTheSealedSequence(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	sl := f.s.Segments()
	r, ok := sl.(SegmentReservation)
	require.True(t, ok)
	a := openClosed(t, sl, segSession, 0, 3)
	b := openClosed(t, sl, segSession, 4, 7)
	c := openClosed(t, sl, segSession, 8, 9)
	d := openClosed(t, sl, segSession, 10, 11)

	require.NoError(t, r.ReserveEncoded(ctx, []core.SegmentID{a}, 2))
	require.NoError(t, r.CommitEncoded(ctx, []core.SegmentID{a, b}, 3),
		"a reservation follows the sequence the seal was written at, and a lost one is written too")
	require.Equal(t, map[core.SegmentID]core.CheckpointSeq{a: 3, b: 3}, encodeLinesOnDisk(t, f.root))
	require.NoError(t, r.CommitEncoded(ctx, []core.SegmentID{a, b}, 3), "committing twice is idempotent")
	require.Len(t, encodeLinesOnDisk(t, f.root), 2)

	require.NoError(t, r.ReserveEncoded(ctx, []core.SegmentID{c}, 4))
	require.ErrorIs(t, r.CommitEncoded(ctx, []core.SegmentID{c, a}, 4), core.ErrAlreadyEncoded,
		"a DURABLE mark into another seq is one-way")
	require.NotContains(t, encodeLinesOnDisk(t, f.root), c, "a refused batch writes none of its members")

	require.NoError(t, r.ReserveEncoded(ctx, []core.SegmentID{d}, 5))
	require.NoError(t, sl.MarkEncoded(ctx, []core.SegmentID{d}, 5),
		"MarkEncoded into the reserved seq writes the reservation")
	require.Equal(t, core.CheckpointSeq(5), encodeLinesOnDisk(t, f.root)[d])

	s2 := f.reopen(t)
	for id, want := range map[core.SegmentID]core.CheckpointSeq{a: 3, b: 3, d: 5} {
		seg, err := s2.Segments().Get(ctx, id)
		require.NoError(t, err)
		require.True(t, seg.EncodedOnce, "segment %d's committed mark survives a reopen", id)
		require.Equal(t, want, seg.CheckpointSeq)
	}
	seg, err := s2.Segments().Get(ctx, c)
	require.NoError(t, err)
	require.False(t, seg.EncodedOnce, "segment %d was only ever reserved", c)
}
