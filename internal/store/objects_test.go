package store

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// TestPutBytes_FanoutLayout asserts every stored chunk lands at objects/<h[0:2]>/<h[2:4]>/<h>.zst
// — the two-level sha256 fanout Qompack.md §7.4 specifies — and that the two directory names are
// literally the first four hex characters of the chunk's own hash.
func TestPutBytes_FanoutLayout(t *testing.T) {
	tp := newTestStore(t)
	payload := bytes.Repeat([]byte("fanout layout probe, 64 KB of prose. "), 1800)

	res, err := tp.Store.PutBytes(context.Background(), payload, PutOptions{Tool: "FileRead", Path: "src/a.ts"})
	require.NoError(t, err)
	require.Greater(t, len(res.Root.Chunks), 1, "fixture sanity: the payload must span several chunks")

	objects := paths.Of(tp.Root).Objects
	for _, c := range res.Root.Chunks {
		hx := hex.EncodeToString(c.Hash[:])
		want := filepath.Join(objects, hx[:2], hx[2:4], hx+objectSuffix)
		_, err := os.Stat(paths.Long(want))
		require.NoError(t, err, "chunk %s must live at its two-level fanout path", c.Hash.Short())
	}
}

// TestPutBytes_CompressionNone asserts store.compression = "none" writes objects with no .zst
// suffix whose bytes are byte-identical to the plaintext chunk.
func TestPutBytes_CompressionNone(t *testing.T) {
	tp := newTestStore(t, withCompressionNone())
	payload := []byte("compression none: this content is stored verbatim, uncompressed.")

	res, err := tp.Store.PutBytes(context.Background(), payload, PutOptions{Path: "src/plain.txt"})
	require.NoError(t, err)
	require.Len(t, res.Root.Chunks, 1)

	for _, rel := range tp.objectPaths(t) {
		require.False(t, strings.HasSuffix(rel, objectSuffix),
			"compression=none must not write a %s suffix, got %s", objectSuffix, rel)
	}

	objects := paths.Of(tp.Root).Objects
	hx := hex.EncodeToString(res.Root.Chunks[0].Hash[:])
	raw, err := os.ReadFile(paths.Long(filepath.Join(objects, hx[:2], hx[2:4], hx)))
	require.NoError(t, err)
	require.Equal(t, payload, raw, "an uncompressed object must be byte-identical to its chunk")
}

// TestObjectReader_TriesBothCandidateNames asserts a store whose compression setting changed
// mid-life still reads objects written under the other setting. This is why objectCandidates
// exists at all: without it, flipping store.compression would silently orphan every prior object.
func TestObjectReader_TriesBothCandidateNames(t *testing.T) {
	p := newProject(t)

	// Write uncompressed…
	tpNone := openOver(t, p, withCompressionNone())
	payload := []byte("written uncompressed, read back after the setting flipped to zstd")
	res, err := tpNone.Store.PutBytes(context.Background(), payload, PutOptions{Path: "src/flip.txt"})
	require.NoError(t, err)
	require.NoError(t, tpNone.Store.Close())

	// …reopen with compression ON and read it back.
	tpZstd := openOver(t, p)
	got, err := tpZstd.Store.GetChunk(context.Background(), res.Root.Chunks[0].Hash)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

// TestPutObject_SecondWriteIsNotNovel asserts putObject reports (0, false) for an object already
// on disk. Zero, not the file's size: the caller adds this to Stats.Bytes, and counting an
// existing object twice would understate DedupRatio — the Phase 1 exit criterion.
func TestPutObject_SecondWriteIsNotNovel(t *testing.T) {
	tp := newTestStore(t)
	h := core.HashBytes(core.DomainChunk, []byte("one chunk"))

	n, novel, err := tp.Store.putObject(h, []byte("one chunk"))
	require.NoError(t, err)
	require.True(t, novel)
	require.Positive(t, n)

	n2, novel2, err := tp.Store.putObject(h, []byte("one chunk"))
	require.NoError(t, err)
	require.False(t, novel2)
	require.Zero(t, n2, "an already-stored object must report zero bytes written, not its size")
}

// TestGetChunk_Roundtrip asserts every chunk a Put reported comes back byte-identical, at exactly
// the length the ChunkRef claims.
func TestGetChunk_Roundtrip(t *testing.T) {
	tp := newTestStore(t)
	payload := fixture(t, "fileread-auth-v1.txt")

	res, err := tp.Store.PutBytes(context.Background(), payload, PutOptions{Tool: "FileRead", Path: "src/auth.ts"})
	require.NoError(t, err)

	var rebuilt []byte
	for _, c := range res.Root.Chunks {
		got, err := tp.Store.GetChunk(context.Background(), c.Hash)
		require.NoError(t, err)
		require.Len(t, got, c.Len, "GetChunk must return exactly the Len its ChunkRef reports")
		rebuilt = append(rebuilt, got...)
	}
	require.Equal(t, payload, rebuilt, "concatenating every chunk must reproduce the input")
}

// TestGetChunk_QuarantinesCorruption asserts a corrupt object is detected, moved out of objects/
// into tmp/quarantine, announced on the Loud channel and counted — the §12.3 "store corrupt" row —
// and that the read reports core.ErrNotFound rather than returning damaged bytes.
func TestGetChunk_QuarantinesCorruption(t *testing.T) {
	tp := newTestStore(t)
	payload := bytes.Repeat([]byte("corruption probe payload. "), 200)

	res, err := tp.Store.PutBytes(context.Background(), payload, PutOptions{Path: "src/corrupt.txt"})
	require.NoError(t, err)
	victim := res.Root.Chunks[0].Hash

	// Flip one byte deep inside the compressed frame, past the magic and frame header.
	objects := paths.Of(tp.Root).Objects
	hx := hex.EncodeToString(victim[:])
	objPath := filepath.Join(objects, hx[:2], hx[2:4], hx+objectSuffix)
	raw, err := os.ReadFile(paths.Long(objPath))
	require.NoError(t, err)
	require.Greater(t, len(raw), 12, "fixture sanity: the frame must be long enough to corrupt")
	raw[len(raw)/2] ^= 0xFF
	require.NoError(t, os.WriteFile(paths.Long(objPath), raw, 0o600))

	_, err = tp.Store.GetChunk(context.Background(), victim)
	require.ErrorIs(t, err, core.ErrNotFound, "a corrupt object must read as not-found, never as bytes")

	_, statErr := os.Stat(paths.Long(objPath))
	require.True(t, os.IsNotExist(statErr), "the corrupt object must be moved out of objects/")

	quarantined := filepath.Join(paths.Of(tp.Root).Tmp, quarantineDir, hx+objectSuffix)
	_, statErr = os.Stat(paths.Long(quarantined))
	require.NoError(t, statErr, "the corrupt object must land in tmp/quarantine for inspection")

	require.Equal(t, int64(1), tp.counter("store.quarantined"))
}

// TestHas_NoIO asserts Has answers a known chunk from the in-memory chunk set without touching the
// filesystem, and still answers an unknown one correctly.
func TestHas_NoIO(t *testing.T) {
	tp := newTestStore(t)
	res, err := tp.Store.PutBytes(context.Background(), []byte("has probe"), PutOptions{Path: "src/has.txt"})
	require.NoError(t, err)
	known := res.Root.Chunks[0].Hash

	// Delete the object from disk. Has must STILL report true, which is only possible if it
	// answered from the index rather than by stat'ing the file.
	objects := paths.Of(tp.Root).Objects
	hx := hex.EncodeToString(known[:])
	require.NoError(t, os.Remove(paths.Long(filepath.Join(objects, hx[:2], hx[2:4], hx+objectSuffix))))
	require.True(t, tp.Store.Has(known), "Has must answer from the in-memory chunk set, without I/O")

	require.False(t, tp.Store.Has(core.HashBytes(core.DomainChunk, []byte("never stored"))))
}

// TestHas_FallsBackToDiskForOrphanObject asserts Has finds an object that exists on disk but has
// no index entry — the shape a crash between the object write and the index append leaves behind.
func TestHas_FallsBackToDiskForOrphanObject(t *testing.T) {
	tp := newTestStore(t)
	orphan := core.HashBytes(core.DomainChunk, []byte("orphan"))
	_, _, err := tp.Store.putObject(orphan, []byte("orphan"))
	require.NoError(t, err)

	require.True(t, tp.Store.Has(orphan), "an object present on disk must be found even with no index line")
}

// TestClosedStoreErrors asserts every operation on a closed store reports core.ErrDegraded, that
// Has reports false and Segments stays non-nil (§5.8's two documented exceptions), and that a
// second Close is a no-op rather than an error.
func TestClosedStoreErrors(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	require.NoError(t, tp.Store.Close())

	_, err := tp.Store.PutBytes(ctx, []byte("x"), PutOptions{})
	require.ErrorIs(t, err, core.ErrDegraded)
	_, err = tp.Store.Put(ctx, bytes.NewReader([]byte("x")), PutOptions{})
	require.ErrorIs(t, err, core.ErrDegraded)
	_, err = tp.Store.GetChunk(ctx, core.Hash{})
	require.ErrorIs(t, err, core.ErrDegraded)
	_, err = tp.Store.GetRoot(ctx, core.Hash{})
	require.ErrorIs(t, err, core.ErrDegraded)
	_, err = tp.Store.Open(ctx, core.Hash{})
	require.ErrorIs(t, err, core.ErrDegraded)
	_, err = tp.Store.OpenSpan(ctx, core.Hash{}, 0, 0)
	require.ErrorIs(t, err, core.ErrDegraded)

	require.False(t, tp.Store.Has(core.Hash{}), "Has has no error return, so a closed store answers false")
	require.NotNil(t, tp.Store.Segments(), "Segments must stay non-nil after Close")
	require.NoError(t, tp.Store.Close(), "a second Close must be a no-op")
}
