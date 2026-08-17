package store

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// bigPayload builds a deterministic multi-megabyte payload whose every offset is identifiable, so
// a span assertion can point at exactly which bytes came back wrong.
func bigPayload(n int) []byte {
	block := []byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ!@#$%^&*()_+-=[]{};':,./<>?|~` \n")
	out := make([]byte, 0, n+len(block))
	for len(out) < n {
		out = append(out, block...)
	}
	return out[:n]
}

// TestOpen_StreamsFullRoot asserts Open reproduces a multi-megabyte root exactly.
func TestOpen_StreamsFullRoot(t *testing.T) {
	tp := newTestStore(t)
	payload := bigPayload(4 << 20)

	res, err := tp.Store.PutBytes(context.Background(), payload, PutOptions{Tool: "FileRead", Path: "src/big.txt"})
	require.NoError(t, err)
	require.Greater(t, len(res.Root.Chunks), 100, "fixture sanity: a 4 MB root must span many chunks")

	rc, err := tp.Store.Open(context.Background(), res.Root.Hash)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

// TestOpenSpan_Boundaries asserts every clamping rule OpenSpan promises. Out-of-range arguments
// clamp rather than error, because the callers are retrieval tools whose job is to return the best
// available answer, not to reject a query (§8.7).
func TestOpenSpan_Boundaries(t *testing.T) {
	tp := newTestStore(t)
	payload := bigPayload(4 << 20)
	total := int64(len(payload))

	res, err := tp.Store.PutBytes(context.Background(), payload, PutOptions{Path: "src/span.txt"})
	require.NoError(t, err)
	root := res.Root.Hash

	read := func(off, n int64) []byte {
		t.Helper()
		rc, err := tp.Store.OpenSpan(context.Background(), root, off, n)
		require.NoError(t, err)
		defer func() { _ = rc.Close() }()
		b, err := io.ReadAll(rc)
		require.NoError(t, err)
		return b
	}

	require.Equal(t, payload[0:10], read(0, 10), "a span at the very start")
	require.Equal(t, payload[total/2:total/2+4096], read(total/2, 4096), "a span starting mid-chunk")
	require.Equal(t, payload[total-5:], read(total-5, 100), "a span whose length runs past the end clamps")
	require.Empty(t, read(total, 10), "a span starting at the end is empty, with no error")
	require.Equal(t, payload[0:10], read(-5, 10), "a negative offset clamps to 0")
	require.Equal(t, payload, read(0, -1), "a non-positive length means to-the-end")
}

// TestOpenSpan_PastEndIsNotAnError asserts a span starting well beyond the content is an empty
// read rather than a failure.
func TestOpenSpan_PastEndIsNotAnError(t *testing.T) {
	tp := newTestStore(t)
	res, err := tp.Store.PutBytes(context.Background(), []byte("short"), PutOptions{Path: "src/short.txt"})
	require.NoError(t, err)

	rc, err := tp.Store.OpenSpan(context.Background(), res.Root.Hash, 1_000_000, 10)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Empty(t, b)
}

// TestGetRoot_UnknownIsNotFound asserts an unstored root reports core.ErrNotFound.
func TestGetRoot_UnknownIsNotFound(t *testing.T) {
	tp := newTestStore(t)
	_, err := tp.Store.GetRoot(context.Background(), core.HashBytes(core.DomainRoot, []byte("never stored")))
	require.ErrorIs(t, err, core.ErrNotFound)

	_, err = tp.Store.Open(context.Background(), core.HashBytes(core.DomainRoot, []byte("never stored")))
	require.ErrorIs(t, err, core.ErrNotFound)
}

// TestOpen_EmptyPayload asserts a zero-byte put round-trips as zero bytes rather than failing.
func TestOpen_EmptyPayload(t *testing.T) {
	tp := newTestStore(t)
	res, err := tp.Store.PutBytes(context.Background(), nil, PutOptions{Path: "src/empty.txt"})
	require.NoError(t, err)
	require.Empty(t, res.Root.Chunks)

	rc, err := tp.Store.Open(context.Background(), res.Root.Hash)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Empty(t, b)
}

// TestOpen_DoesNotMaterializeWholeRoot asserts the reader is lazy: reading only the first few bytes
// of a large root must not have decompressed every chunk.
//
// It is asserted through the quarantine path, which is the only externally visible proof available:
// a root whose LAST chunk's object has been deleted still serves its first bytes, which is only
// possible if that chunk was never fetched.
func TestOpen_DoesNotMaterializeWholeRoot(t *testing.T) {
	tp := newTestStore(t)
	payload := bigPayload(1 << 20)

	res, err := tp.Store.PutBytes(context.Background(), payload, PutOptions{Path: "src/lazy.txt"})
	require.NoError(t, err)
	require.Greater(t, len(res.Root.Chunks), 10)

	// Remove the final chunk's object entirely.
	last := res.Root.Chunks[len(res.Root.Chunks)-1]
	removeObject(t, tp, last.Hash)

	rc, err := tp.Store.Open(context.Background(), res.Root.Hash)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	head := make([]byte, 64)
	n, err := io.ReadFull(rc, head)
	require.NoError(t, err, "the first chunk must be readable even though the last object is gone")
	require.Equal(t, 64, n)
	require.Equal(t, payload[:64], head)
}

// removeObject deletes h's object file from objects/.
func removeObject(t *testing.T, tp *testProject, h core.Hash) {
	t.Helper()
	for _, p := range tp.Store.objectCandidates(h) {
		if err := osRemove(p); err == nil {
			return
		}
	}
	t.Fatalf("object %s was not present to remove", h.Short())
}

// TestChunkReader_ReportsMissingObject asserts a read that reaches a missing chunk fails with
// core.ErrNotFound rather than silently returning short content.
func TestChunkReader_ReportsMissingObject(t *testing.T) {
	tp := newTestStore(t)
	payload := bigPayload(1 << 20)

	res, err := tp.Store.PutBytes(context.Background(), payload, PutOptions{Path: "src/missing.txt"})
	require.NoError(t, err)
	removeObject(t, tp, res.Root.Chunks[len(res.Root.Chunks)-1].Hash)

	rc, err := tp.Store.Open(context.Background(), res.Root.Hash)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	_, err = io.ReadAll(rc)
	require.ErrorIs(t, err, core.ErrNotFound, "a truncated read must report, never silently short-return")
}

// TestOpenStore_LoadsIndex asserts every root survives a close and reopen, with Stats-visible
// object count and per-root chunk lists intact.
func TestOpenStore_LoadsIndex(t *testing.T) {
	p := newProject(t)
	tp := openOver(t, p)
	ctx := context.Background()

	want := make(map[core.Hash]Root, 50)
	for i := 0; i < 50; i++ {
		res, err := tp.Store.PutBytes(ctx, bytes.Repeat([]byte{byte('a' + i%26)}, 300+i),
			PutOptions{Tool: "FileRead", Path: "src/f.txt"})
		require.NoError(t, err)
		want[res.Root.Hash] = res.Root
	}
	objectsBefore := len(tp.objectPaths(t))
	require.NoError(t, tp.Store.Close())

	reopened := openOver(t, p)
	require.Equal(t, objectsBefore, len(reopened.objectPaths(t)))
	for h, root := range want {
		got, err := reopened.Store.GetRoot(ctx, h)
		require.NoError(t, err, "root %s must survive a reopen", h.Short())
		require.Equal(t, root, got)
	}
}

// TestOpenStore_TruncatedFinalLine asserts a roots.jsonl whose last line was cut off by a crash
// still opens, counts the bad line, and keeps every complete record before it.
func TestOpenStore_TruncatedFinalLine(t *testing.T) {
	p := newProject(t)
	tp := openOver(t, p)
	ctx := context.Background()

	var kept []core.Hash
	for i := 0; i < 3; i++ {
		res, err := tp.Store.PutBytes(ctx, bytes.Repeat([]byte("line"), 50+i), PutOptions{Path: "src/t.txt"})
		require.NoError(t, err)
		kept = append(kept, res.Root.Hash)
	}
	require.NoError(t, tp.Store.Close())

	appendRawIndexLine(t, tp, rootsFile, `{"v":1,"root":"sha256:deadbeef`)

	reopened := openOver(t, p)
	require.Equal(t, int64(1), reopened.counter("store.index.badline"),
		"the truncated final line must be counted, not fatal")
	for _, h := range kept {
		_, err := reopened.Store.GetRoot(ctx, h)
		require.NoError(t, err, "every complete record before the truncation must survive")
	}
}
