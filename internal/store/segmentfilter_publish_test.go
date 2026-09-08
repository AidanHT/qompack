package store

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// publishFixture opens a store with one closed segment and returns the fixture plus the segment's
// id, so every test below starts from the same shape.
func publishFixture(t *testing.T) (*idxFixture, core.SegmentID) {
	t.Helper()
	f := newIdxStore(t)
	ctx := context.Background()
	id, err := f.s.Segments().Open(ctx, Segment{ID: 12, Session: segSession, StartTurn: 40})
	require.NoError(t, err)
	require.NoError(t, f.s.Segments().Close(ctx, id, 57, map[string]float64{segTokensFeature: 5}))
	return f, id
}

// publisher asserts the segment log satisfies SP-16's optional publication surface, which is
// reached by type assertion rather than through SegmentLog itself.
func publisher(t *testing.T, f *idxFixture) SegmentFilterPublisher {
	t.Helper()
	p, ok := f.s.Segments().(SegmentFilterPublisher)
	require.True(t, ok, "the segment log must satisfy SegmentFilterPublisher")
	return p
}

// TestPublishFilter_IsWhatMakesAFilterVisible pins the second half of publication: a reader learns
// about a filter only through BloomRef, and the record survives a restart because the log is the
// truth.
func TestPublishFilter_IsWhatMakesAFilterVisible(t *testing.T) {
	f, id := publishFixture(t)
	ctx := context.Background()

	before, err := f.s.Segments().Get(ctx, id)
	require.NoError(t, err)
	require.Empty(t, before.BloomRef, "SP-06 never writes one")

	ref := SegmentFilterRef(id)
	require.NoError(t, publisher(t, f).PublishFilter(ctx, id, ref))

	got, err := f.s.Segments().Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, ref, got.BloomRef)

	s2 := f.reopen(t)
	got, err = s2.Segments().Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, ref, got.BloomRef, "the append-only log is the truth")
}

// TestPublishFilter_RepublishingReplacesTheReference pins that a rebuilt filter can be published
// over an older one — the log is append-only, so the replacement is a second record, not a
// rewrite.
func TestPublishFilter_RepublishingReplacesTheReference(t *testing.T) {
	f, id := publishFixture(t)
	ctx := context.Background()
	p := publisher(t, f)

	require.NoError(t, p.PublishFilter(ctx, id, "sketches/seg-0012.bloom"))
	require.NoError(t, p.PublishFilter(ctx, id, "sketches/seg-0012.v2.bloom"))

	s2 := f.reopen(t)
	got, err := s2.Segments().Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "sketches/seg-0012.v2.bloom", got.BloomRef, "the last record wins on replay")
}

// TestPublishFilter_RefusesWhatWouldDangleOrLie walks the refusals: an empty reference, an unknown
// segment, a cancelled context and a closed log.
func TestPublishFilter_RefusesWhatWouldDangleOrLie(t *testing.T) {
	f, id := publishFixture(t)
	ctx := context.Background()
	p := publisher(t, f)

	require.Error(t, p.PublishFilter(ctx, id, ""), "an empty reference names nothing")
	require.ErrorIs(t, p.PublishFilter(ctx, 999, SegmentFilterRef(999)), core.ErrNotFound)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, p.PublishFilter(cancelled, id, SegmentFilterRef(id)), context.Canceled)

	require.NoError(t, f.s.Close())
	require.ErrorIs(t, p.PublishFilter(ctx, id, SegmentFilterRef(id)), core.ErrDegraded)
}

// TestPublishFilter_LeavesNoBloomRecordWhenItRefuses pins that a refused publication writes
// nothing, so a reader never meets a record naming a file that was never written.
func TestPublishFilter_LeavesNoBloomRecordWhenItRefuses(t *testing.T) {
	f, id := publishFixture(t)
	ctx := context.Background()
	p := publisher(t, f)

	require.Error(t, p.PublishFilter(ctx, id, ""))
	require.ErrorIs(t, p.PublishFilter(ctx, 999, "sketches/seg-0999.bloom"), core.ErrNotFound)

	for _, line := range indexLines(t, f.root, segmentsFile) {
		require.NotContains(t, string(line), `"op":"bloom"`)
	}
}
