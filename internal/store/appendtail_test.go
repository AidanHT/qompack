package store

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// TestOpenStore_TornRootsTailDoesNotSwallowTheNextRoot is finding F4-2 at the store choke point.
//
// index/roots.jsonl is written by (*appendFile).write, not paths.AppendJSONL, so a torn tail used
// to glue the next root onto the damaged line. After a mid-line tear, a reopen, an append and a
// second reopen, the new root must load and the torn line must still count as exactly one
// store.index.badline.
func TestOpenStore_TornRootsTailDoesNotSwallowTheNextRoot(t *testing.T) {
	p := newProject(t)
	tp := openOver(t, p)
	ctx := context.Background()

	var kept []core.Hash
	for i := 0; i < 3; i++ {
		res, err := tp.Store.PutBytes(ctx, bytes.Repeat([]byte("kept"), 50+i), PutOptions{Path: "src/t.txt"})
		require.NoError(t, err)
		kept = append(kept, res.Root.Hash)
	}
	require.NoError(t, tp.Store.Close())

	truncateIndexTail(t, p, rootsFile, 40)

	reopened := openOver(t, p)
	after, err := reopened.Store.PutBytes(ctx, bytes.Repeat([]byte("after-tear"), 80), PutOptions{Path: "src/after.txt"})
	require.NoError(t, err)
	require.NoError(t, reopened.Store.Close())

	again := openOver(t, p)
	require.Equal(t, int64(1), again.counter("store.index.badline"),
		"the torn line is still one bad line; the new root must not have been glued onto it")
	for _, h := range kept[:2] {
		_, getErr := again.Store.GetRoot(ctx, h)
		require.NoError(t, getErr, "every complete record before the truncation must survive")
	}
	got, err := again.Store.GetRoot(ctx, after.Root.Hash)
	require.NoError(t, err, "the root appended after a torn tail must load as its own record")
	require.Equal(t, after.Root, got)
}
