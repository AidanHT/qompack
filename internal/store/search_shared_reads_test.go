package store

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/obs"
)

// sharedChunkBlock is one whole 64-byte chunk under withChunkSize(64) that every seed below starts
// with, so every seed's root references the same first chunk.
var sharedChunkBlock = bytes.Repeat([]byte("s"), 64)

// seedSharedChunkRoots stores three roots that share their first chunk and differ after it, each
// with a tool-use record whose unique part contains "needle", and returns the shared chunk's hash.
func seedSharedChunkRoots(t *testing.T, tp *testProject) core.Hash {
	t.Helper()
	var seeds []searchSeed
	for _, word := range []string{"alpha", "beta", "gamma"} {
		tail := fmt.Sprintf("needle %s %s\n", word, strings.Repeat(word, 12))
		seeds = append(seeds, searchSeed{
			id: core.ToolUseID("tu-" + word), tool: "FileRead", path: word + ".txt",
			content: append(append([]byte{}, sharedChunkBlock...), tail...),
		})
	}
	res := seedSearch(t, tp, seeds)
	shared := core.HashBytes(core.DomainChunk, sharedChunkBlock)
	for id, r := range res {
		require.Equal(t, shared, r.Root.Chunks[0].Hash, "fixture: %s must start with the shared chunk", id)
	}
	return shared
}

// TestSearch_ReadsASharedChunkOncePerSearch is sharedChunkReads' observable contract. A read-only
// store leaves a rejected object in place and counts every rejection, so the count of rejections
// is the count of reads: three candidates that share one damaged chunk must cost ONE read of it,
// and — exactly as when each read it on its own — none of the three may be returned.
func TestSearch_ReadsASharedChunkOncePerSearch(t *testing.T) {
	tp := newTestStore(t, withChunkSize(64))
	ctx := context.Background()
	shared := seedSharedChunkRoots(t, tp)
	require.NoError(t, tp.Store.Flush(ctx))
	require.NoError(t, tp.Store.Close())

	wrong, err := Encode(bytes.Repeat([]byte("x"), len(sharedChunkBlock)))
	require.NoError(t, err)
	replaceObject(t, tp, shared, wrong)

	m := obs.New(tp.Clock)
	ro, err := OpenReadOnly(tp.Root, tp.Store.cfg, Deps{Metrics: m, Clock: tp.Clock})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ro.Close() })
	fs := ro.(readOnlyStore).fs

	hits, err := fs.Search(ctx, Query{Text: "needle"})
	require.NoError(t, err)
	require.Empty(t, hits, "every candidate needs the damaged chunk, so none may be ranked")
	require.Equal(t, int64(1), m.Counter("store.quarantine_skipped_read_only").Value(),
		"the damaged shared chunk must be read once for the whole Search, not once per candidate")
}

// TestSearch_SharedChunkReadsMatchIndependentReads compares every body materializeAll builds, with
// its reads shared, against materialize reading each candidate on its own: same bytes, same chunk
// bounds, same outcome. The fixture shares chunks across roots AND repeats one root under several
// tool-use records, the two ways content addressing makes candidates overlap.
func TestSearch_SharedChunkReadsMatchIndependentReads(t *testing.T) {
	tp := newTestStore(t, withChunkSize(64))
	ctx := context.Background()
	seedSharedChunkRoots(t, tp)
	repeated := append(append([]byte{}, sharedChunkBlock...), []byte("needle repeated read of one file\n")...)
	seedSearch(t, tp, []searchSeed{
		{id: "tu-r1", tool: "FileRead", path: "same.txt", content: repeated},
		{id: "tu-r2", tool: "FileRead", path: "same.txt", content: repeated},
		{id: "tu-r3", tool: "FileRead", path: "same.txt", content: repeated},
		{id: "tu-solo", tool: "FileRead", path: "solo.txt", content: []byte("needle with nothing shared at all\n")},
	})

	cands := tp.Store.candidates(Query{Text: "needle"})
	require.Len(t, cands, 7)
	reads := newSharedChunkReads(cands)
	require.NotNil(t, reads, "fixture: the candidates must share chunks")

	want := map[ChunkRef]int{}
	for _, c := range cands {
		for _, ref := range c.root.Root.Chunks {
			want[ref]++
		}
	}
	for ref, n := range want {
		_, ok := reads.shared[ref]
		require.Equal(t, n > 1, ok, "chunk %s referenced %d times", ref.Hash.Short(), n)
	}

	got := tp.Store.materializeAll(ctx, cands, true)
	for i, c := range cands {
		content, bounds, err := tp.Store.materialize(c.root)
		require.NoError(t, err)
		require.NoError(t, got[i].err, "candidate %s", c.rec.ID)
		require.Equal(t, content, got[i].content, "candidate %s", c.rec.ID)
		require.Equal(t, bounds, got[i].bounds, "candidate %s", c.rec.ID)
	}
}

// TestNewSharedChunkReads_NothingSharedIsNil keeps the no-overlap case free: with every chunk
// referenced once there is nothing to share and every read goes straight to getObject.
func TestNewSharedChunkReads_NothingSharedIsNil(t *testing.T) {
	tp := newTestStore(t, withChunkSize(64))
	seedSearch(t, tp, []searchSeed{
		{id: "tu-a", tool: "FileRead", path: "a.txt", content: []byte("needle one\n")},
		{id: "tu-b", tool: "FileRead", path: "b.txt", content: []byte("needle two\n")},
	})
	require.Nil(t, newSharedChunkReads(tp.Store.candidates(Query{Text: "needle"})))
}
