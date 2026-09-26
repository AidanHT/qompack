package store

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// TestSyncPublication_CountsOnePassPerCall pins the counting seam SP08-D1's pass-count tests read:
// one count per pass that gets past the write guard, a failing pass included, and none for a call
// the guard refuses.
func TestSyncPublication_CountsOnePassPerCall(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "counted publication content")
	require.Zero(t, tp.counter(CounterPublicationSync), "a put alone makes no publication pass")

	require.NoError(t, tp.Store.SyncPublication(ctx, root))
	require.NoError(t, tp.Store.SyncPublication(ctx, root))
	require.Equal(t, int64(2), tp.counter(CounterPublicationSync))

	missing := core.HashBytes(core.DomainChunk, []byte("a root nothing stored"))
	require.ErrorIs(t, tp.Store.SyncPublication(ctx, missing), core.ErrNotFound)
	require.Equal(t, int64(3), tp.counter(CounterPublicationSync), "a pass that fails is still a pass")

	require.NoError(t, tp.Store.Close())
	require.ErrorIs(t, tp.Store.SyncPublication(ctx, root), core.ErrDegraded)
	require.Equal(t, int64(3), tp.counter(CounterPublicationSync), "the write guard's refusal is not a pass")
}

// TestObservationPublish_UnverifiableRootWritesNoIntent: the root an observation-bearing record names
// is verified and made durable BEFORE the publication intent is written, so a root that cannot be
// proven leaves no intent behind. An intent naming content that did not survive can never complete
// (TestObservationPublish_MissingObjectsKeepIntentIncomplete), so writing one would hold its delivery
// unacknowledgeable for good; the caller's retry must instead find the delivery unpublished.
func TestObservationPublish_UnverifiableRootWritesNoIntent(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "a payload whose object goes missing before publication")
	got, err := tp.Store.GetRoot(ctx, root)
	require.NoError(t, err)
	require.NotEmpty(t, got.Chunks)
	require.NoError(t, os.Remove(paths.Long(tp.Store.objectPath(got.Chunks[0].Hash))))

	obs := obsID(t, 701)
	rec := recWithObs("toolu_unverifiable_root", root, obs)
	_, _, err = tp.Store.RecordToolUseSuperseding(ctx, rec, nil)
	require.ErrorIs(t, err, core.ErrDegraded, "a root that cannot be proven is not published")

	_, statErr := os.Stat(paths.Long(obsPath(tp.Root)))
	require.True(t, os.IsNotExist(statErr), "no intent may name a root that was never proven durable")
	_, err = tp.Store.ToolUseByObservation(ctx, obs)
	require.ErrorIs(t, err, core.ErrNotFound, "the delivery is unpublished, not a pending intent")
	_, err = tp.Store.ToolUse(ctx, rec.ID)
	require.ErrorIs(t, err, core.ErrNotFound, "and no record was written")
}
