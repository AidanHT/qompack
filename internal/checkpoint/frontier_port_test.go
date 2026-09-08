package checkpoint_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
)

type frontierPortResult struct {
	frontier core.TurnIndex
	err      error
}

// frontierPortWriter controls only the Begin/Advance lifecycle boundary. Real FileWriter tests
// cover encoding; this double makes the otherwise timing-dependent sealed-draft handoff
// deterministic without duplicating that implementation.
type frontierPortWriter struct {
	drafts  []*checkpoint.Draft
	results []frontierPortResult

	beginCalls    int
	advanceCalls  int
	beginSessions []core.SessionID
	beginParents  []core.CheckpointSeq
	advanceDrafts []*checkpoint.Draft
	advanceIDs    [][]core.SegmentID
	beginCancel   context.CancelFunc
}

func (w *frontierPortWriter) Begin(_ context.Context, session core.SessionID, parent core.CheckpointSeq, _ checkpoint.SourceSet) (*checkpoint.Draft, error) {
	w.beginCalls++
	w.beginSessions = append(w.beginSessions, session)
	w.beginParents = append(w.beginParents, parent)
	if w.beginCancel != nil {
		w.beginCancel()
	}
	if len(w.drafts) < w.beginCalls {
		return nil, errors.New("unexpected Begin")
	}
	return w.drafts[w.beginCalls-1], nil
}

func (w *frontierPortWriter) Advance(_ context.Context, draft *checkpoint.Draft, ids []core.SegmentID) (core.TurnIndex, error) {
	w.advanceCalls++
	w.advanceDrafts = append(w.advanceDrafts, draft)
	w.advanceIDs = append(w.advanceIDs, append([]core.SegmentID(nil), ids...))
	if len(w.results) < w.advanceCalls {
		return 0, errors.New("unexpected Advance")
	}
	r := w.results[w.advanceCalls-1]
	return r.frontier, r.err
}

func (*frontierPortWriter) Finalize(context.Context, *checkpoint.Draft, core.Tokens) (checkpoint.Ref, error) {
	return checkpoint.Ref{}, errors.New("unexpected Finalize")
}

func (*frontierPortWriter) Abort(*checkpoint.Draft) error { return errors.New("unexpected Abort") }

func TestFrontierAdvancer_ReusesFileWriterLiveDraft(t *testing.T) {
	f := newFx(t)
	f.closedSeg(1, 0, 3)
	sourceCalls := 0
	advancer := checkpoint.NewFrontierAdvancer(f.w, func() (checkpoint.SourceSet, error) {
		sourceCalls++
		return f.src, nil
	})

	first, err := advancer.Advance(f.ctx(), f.sess, []core.SegmentID{1})
	require.NoError(t, err)
	require.Equal(t, core.TurnIndex(3), first)
	live := f.w.DraftFor(f.sess)
	require.NotNil(t, live)

	f.closedSeg(2, 4, 7)
	second, err := advancer.Advance(f.ctx(), f.sess, []core.SegmentID{2})
	require.NoError(t, err)
	require.Equal(t, core.TurnIndex(7), second)
	require.Same(t, live, f.w.DraftFor(f.sess), "FileWriter owns and reuses the session draft")
	require.Equal(t, 2, sourceCalls, "each call resolves current sources before Begin reuses the live draft")
}

func TestFrontierAdvancer_RetriesOneSealedDraft(t *testing.T) {
	first, successor := new(checkpoint.Draft), new(checkpoint.Draft)
	w := &frontierPortWriter{
		drafts:  []*checkpoint.Draft{first, successor},
		results: []frontierPortResult{{frontier: 4, err: checkpoint.ErrDraftSealed}, {frontier: 9}},
	}
	sourceCalls := 0
	advancer := checkpoint.NewFrontierAdvancer(w, func() (checkpoint.SourceSet, error) {
		sourceCalls++
		return checkpoint.SourceSet{}, nil
	})

	frontier, err := advancer.Advance(context.Background(), "frontier_retry", []core.SegmentID{7, 8})
	require.NoError(t, err)
	require.Equal(t, core.TurnIndex(9), frontier)
	require.Equal(t, 2, sourceCalls)
	require.Equal(t, 2, w.beginCalls, "a sealed draft gets one fresh Begin")
	require.Equal(t, 2, w.advanceCalls)
	require.Equal(t, []core.CheckpointSeq{0, 0}, w.beginParents, "the port always lets FileWriter resolve the latest parent")
	require.Equal(t, []*checkpoint.Draft{first, successor}, w.advanceDrafts)
	require.Equal(t, [][]core.SegmentID{{7, 8}, {7, 8}}, w.advanceIDs)
}

func TestFrontierAdvancer_ReportsRepeatedSealedDraft(t *testing.T) {
	w := &frontierPortWriter{
		drafts:  []*checkpoint.Draft{new(checkpoint.Draft), new(checkpoint.Draft)},
		results: []frontierPortResult{{frontier: 4, err: checkpoint.ErrDraftSealed}, {frontier: 9, err: checkpoint.ErrDraftSealed}},
	}
	advancer := checkpoint.NewFrontierAdvancer(w, func() (checkpoint.SourceSet, error) {
		return checkpoint.SourceSet{}, nil
	})

	frontier, err := advancer.Advance(context.Background(), "frontier_repeat", []core.SegmentID{7})
	require.ErrorIs(t, err, checkpoint.ErrDraftSealed)
	require.Equal(t, core.TurnIndex(9), frontier, "the second writer result remains visible")
	require.Equal(t, 2, w.beginCalls, "the port must not turn a repeated seal into an unbounded retry")
	require.Equal(t, 2, w.advanceCalls)
}

func TestFrontierAdvancer_CancellationAndEmptyBatchDoNotBegin(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range []struct {
		name    string
		ctx     context.Context
		ids     []core.SegmentID
		wantErr error
	}{
		{name: "canceled", ctx: canceled, ids: []core.SegmentID{1}, wantErr: context.Canceled},
		{name: "empty", ctx: context.Background()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &frontierPortWriter{}
			sourceCalls := 0
			advancer := checkpoint.NewFrontierAdvancer(w, func() (checkpoint.SourceSet, error) {
				sourceCalls++
				return checkpoint.SourceSet{}, nil
			})

			frontier, err := advancer.Advance(tc.ctx, "frontier_no_begin", tc.ids)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Zero(t, frontier)
			require.Zero(t, sourceCalls)
			require.Zero(t, w.beginCalls)
			require.Zero(t, w.advanceCalls)
		})
	}
}

func TestFrontierAdvancer_PreservesPartialDPIGuardOutcome(t *testing.T) {
	f := newFx(t)
	f.closedSeg(1, 0, 3)
	f.closedSeg(2, 4, 7)
	require.NoError(t, f.store.Segments().MarkEncoded(f.ctx(), []core.SegmentID{2}, 5))

	sourceCalls := 0
	advancer := checkpoint.NewFrontierAdvancer(f.w, func() (checkpoint.SourceSet, error) {
		sourceCalls++
		return f.src, nil
	})
	frontier, err := advancer.Advance(f.ctx(), f.sess, []core.SegmentID{1, 2})

	require.ErrorIs(t, err, core.ErrAlreadyEncoded)
	require.Equal(t, f.w.DraftFor(f.sess).Frontier(), frontier,
		"the writer's partial-batch frontier must reach the caller unchanged")
	require.Equal(t, 1, f.w.DraftFor(f.sess).EncodedCount(), "the eligible segment remains in the draft")
	require.Equal(t, 1, sourceCalls, "DPI is adjudicated by FileWriter; the port must not retry")
}

func TestFrontierAdvancer_CancellationBetweenLifecycleCalls(t *testing.T) {
	for _, stage := range []string{"sources", "begin"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := &frontierPortWriter{drafts: []*checkpoint.Draft{new(checkpoint.Draft)}}
			if stage == "begin" {
				w.beginCancel = cancel
			}
			advancer := checkpoint.NewFrontierAdvancer(w, func() (checkpoint.SourceSet, error) {
				if stage == "sources" {
					cancel()
				}
				return checkpoint.SourceSet{}, nil
			})
			_, err := advancer.Advance(ctx, "canceled_frontier", []core.SegmentID{1})
			require.ErrorIs(t, err, context.Canceled)
			require.Zero(t, w.advanceCalls)
			if stage == "sources" {
				require.Zero(t, w.beginCalls)
			}
		})
	}
}
