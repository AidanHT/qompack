package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
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
