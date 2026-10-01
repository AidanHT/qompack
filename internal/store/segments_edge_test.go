package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// Edge rows for the two-phase segment encode and the log's Sync that the reservation tests leave
// unexecuted: an unknown or still-open segment refuses the whole batch, a caller that gave up writes
// nothing, and a read-only log refuses to reserve or commit while its Sync has nothing to make durable
// (w16b-cover, C3.6).

// TestSegmentReservation_RefusesAnUnknownOrOpenSegmentAndACancelledCaller: both halves validate the
// whole batch first: a segment the log does not hold, or one still open, refuses it, and so does a
// context that has ended.
func TestSegmentReservation_RefusesAnUnknownOrOpenSegmentAndACancelledCaller(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	sl := f.s.Segments()
	r, ok := sl.(SegmentReservation)
	require.True(t, ok)
	closed := openClosed(t, sl, segSession, 0, 3)
	open, err := sl.Open(ctx, Segment{Session: segSession, StartTurn: 4})
	require.NoError(t, err)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	for name, op := range map[string]func(context.Context, []core.SegmentID, core.CheckpointSeq) error{
		"reserve": r.ReserveEncoded,
		"commit":  r.CommitEncoded,
	} {
		require.ErrorIs(t, op(ctx, []core.SegmentID{closed, 999}, 1), core.ErrNotFound, name)
		require.ErrorIs(t, op(ctx, []core.SegmentID{closed, open}, 1), ErrSegmentOpen, name)
		require.ErrorIs(t, op(cancelled, []core.SegmentID{closed}, 1), context.Canceled, name)
	}
	got, err := sl.Get(ctx, closed)
	require.NoError(t, err)
	require.False(t, got.EncodedOnce, "a refused batch marks none of its members")
	require.Empty(t, encodeLinesOnDisk(t, f.root), "a refused batch writes nothing")
}

// TestSegmentLog_SyncMakesTheLogDurableAndAReadOnlyLogRefusesToEncode: a writable log's Sync succeeds
// once it has appended, and refuses a caller that gave up; a read-only open's log has nothing to make
// durable, and refuses both halves of the encode.
func TestSegmentLog_SyncMakesTheLogDurableAndAReadOnlyLogRefusesToEncode(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	sl := f.s.Segments()
	id := openClosed(t, sl, segSession, 0, 3)
	syncer, ok := sl.(interface{ Sync(context.Context) error })
	require.True(t, ok)
	require.NoError(t, syncer.Sync(ctx))
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, syncer.Sync(cancelled), context.Canceled)
	require.NoError(t, f.s.Close())

	ro, err := OpenReadOnly(f.root, config.Defaults(), Deps{Clock: f.clk})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ro.Close() })
	rs, ok := ro.(readOnlyStore)
	require.True(t, ok)
	rl := rs.fs.seg
	require.NoError(t, rl.Sync(ctx), "a read-only log appends nothing, so it has nothing to make durable")
	require.ErrorIs(t, rl.ReserveEncoded(ctx, []core.SegmentID{id}, 1), ErrReadOnly)
	require.ErrorIs(t, rl.CommitEncoded(ctx, []core.SegmentID{id}, 1), ErrReadOnly)
}
