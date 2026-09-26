package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// SP08-D1 (owner decision D20): a SyncPublication pass keeps its whole re-proof and is priced here in
// barriers, the unit host load cannot move. CounterPublicationSyncFile counts the pass's file fsyncs,
// CounterPublicationSyncDir its directory fsyncs.

// barrierFixtureLines is how many lines putBarrierFixture writes: about 190 KB of body, which the
// production chunker (1/4/16 KiB) cuts into dozens of objects, so the objects share the objects/
// root and some share a first-level fanout directory.
const barrierFixtureLines = 4096

// putBarrierFixture puts a multi-chunk body whose CRLF line ends canonicalization rewrites, with
// KeepRaw, so its recovery closure spans the content root and its delta root. It returns the root
// and that closure's objects.
func putBarrierFixture(t *testing.T, tp *testProject) (core.Hash, map[core.Hash]bool) {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder
	for i := range barrierFixtureLines {
		fmt.Fprintf(&b, "publication barrier fixture line %d of %d, with its own words\r\n", i, barrierFixtureLines)
	}
	res, err := tp.Store.PutBytes(ctx, []byte(b.String()), PutOptions{Tool: "Bash", KeepRaw: true})
	require.NoError(t, err)
	objects, err := tp.Store.publicationObjects(ctx, res.Root.Hash)
	require.NoError(t, err)
	require.Greater(t, len(objects), len(res.Root.Chunks), "fixture: the closure spans more than the content root")
	return res.Root.Hash, objects
}

// TestSyncPublication_CountsAFileBarrierPerObjectAndIndex pins the pass's file fsyncs: one for each
// object of the root's recovery closure, one for index/roots.jsonl and one for index/tool_use.jsonl,
// on every pass, because each pass re-proves the whole closure.
func TestSyncPublication_CountsAFileBarrierPerObjectAndIndex(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root, objects := putBarrierFixture(t, tp)
	require.Zero(t, tp.counter(CounterPublicationSyncFile), "a put alone issues no publication barrier")

	for pass := 1; pass <= 2; pass++ {
		before := tp.counter(CounterPublicationSyncFile)
		require.NoError(t, tp.Store.SyncPublication(ctx, root))
		require.Equal(t, int64(len(objects)+2), tp.counter(CounterPublicationSyncFile)-before,
			"pass %d: one fsync per closure object and one per index file", pass)
	}
}
