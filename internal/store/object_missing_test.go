package store

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestGetChunk_AnIndexedObjectGoneEverywhereIsMissingNotPreserved is the store half of F-C49-3: an
// indexed object whose file was removed from outside the store, with no quarantine evidence for it,
// reports ErrObjectMissing — still ErrDamaged (so every caller answers `unavailable`, never absent)
// but distinguishable from an object that was refused and preserved.
func TestGetChunk_AnIndexedObjectGoneEverywhereIsMissingNotPreserved(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	res, err := tp.Store.PutBytes(ctx, bytes.Repeat([]byte("missing object content\n"), 16), PutOptions{Path: "src/gone.txt"})
	require.NoError(t, err)
	h := res.Root.Chunks[0].Hash
	require.NoError(t, os.Remove(paths.Long(tp.Store.objectPath(h))))

	_, err = tp.Store.GetChunk(ctx, h)
	require.ErrorIs(t, err, ErrObjectMissing)
	require.ErrorIs(t, err, ErrDamaged, "a missing indexed object is still a refusal, not an absence")
}

// TestGetChunk_AQuarantinedObjectIsDamagedNotMissing: once the store itself moved a rejected object
// into tmp/quarantine/, the next read finds the index entry and no file in objects/ — the evidence
// exists, so the answer stays "damaged, preserved", not "missing".
func TestGetChunk_AQuarantinedObjectIsDamagedNotMissing(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	res, err := tp.Store.PutBytes(ctx, bytes.Repeat([]byte("quarantined object content\n"), 16), PutOptions{Path: "src/bad.txt"})
	require.NoError(t, err)
	h := res.Root.Chunks[0].Hash
	require.NoError(t, tp.Store.Quarantine(h, "test: damaged on purpose"))

	_, err = tp.Store.GetChunk(ctx, h)
	require.ErrorIs(t, err, ErrDamaged)
	require.NotErrorIs(t, err, ErrObjectMissing, "the quarantine holds this object's evidence")
}
