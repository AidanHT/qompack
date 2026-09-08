package store

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// TestGetChunk_RejectsValidCompressedReplacement makes the replacement a valid zstd frame with
// the same plaintext length. A frame checksum and the roots length can both pass in that shape;
// the content address must still be verified before bytes are returned.
func TestGetChunk_RejectsValidCompressedReplacement(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	res, err := tp.Store.PutBytes(ctx, bytes.Repeat([]byte("original compressed integrity content\n"), 32), PutOptions{Path: "src/original.txt"})
	require.NoError(t, err)
	require.Len(t, res.Root.Chunks, 1)
	victim := res.Root.Chunks[0]
	original, err := tp.Store.GetChunk(ctx, victim.Hash)
	require.NoError(t, err)

	replacement := bytes.Repeat([]byte{0xA5}, len(original))
	require.NotEqual(t, original, replacement, "fixture sanity: replacement must not match the addressed chunk")
	frame, err := Encode(replacement)
	require.NoError(t, err)
	objPath := replaceObject(t, tp, victim.Hash, frame)

	got, err := tp.Store.GetChunk(ctx, victim.Hash)
	require.ErrorIs(t, err, core.ErrNotFound)
	require.Empty(t, got, "a substituted object must not return any plaintext")
	expectQuarantinedObject(t, tp, objPath)
}

// TestGetChunk_RejectsSameLengthUncompressedReplacement covers stores configured to write
// plaintext objects, where there is no frame checksum to catch a same-length substitution.
func TestGetChunk_RejectsSameLengthUncompressedReplacement(t *testing.T) {
	tp := newTestStore(t, withCompressionNone())
	ctx := context.Background()

	res, err := tp.Store.PutBytes(ctx, bytes.Repeat([]byte("original uncompressed integrity content\n"), 32), PutOptions{Path: "src/plain.txt"})
	require.NoError(t, err)
	require.Len(t, res.Root.Chunks, 1)
	victim := res.Root.Chunks[0]
	original, err := tp.Store.GetChunk(ctx, victim.Hash)
	require.NoError(t, err)

	replacement := bytes.Repeat([]byte{0x5A}, len(original))
	require.NotEqual(t, original, replacement, "fixture sanity: replacement must not match the addressed chunk")
	objPath := replaceObject(t, tp, victim.Hash, replacement)

	got, err := tp.Store.GetChunk(ctx, victim.Hash)
	require.ErrorIs(t, err, core.ErrNotFound)
	require.Empty(t, got, "a substituted object must not return any plaintext")
	expectQuarantinedObject(t, tp, objPath)
}

// TestGetChunk_RejectsUnindexedSubstitutedObject covers crash debris: an object can exist before
// any roots.jsonl entry references it. There is no indexed length in that case, so the requested
// chunk hash remains the only integrity binding.
func TestGetChunk_RejectsUnindexedSubstitutedObject(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	original := bytes.Repeat([]byte("unindexed object integrity content\n"), 32)
	h := core.HashBytes(core.DomainChunk, original)

	_, novel, err := tp.Store.putObject(h, original)
	require.NoError(t, err)
	require.True(t, novel)
	tp.Store.mu.RLock()
	_, indexed := tp.Store.chunkSet[h]
	tp.Store.mu.RUnlock()
	require.False(t, indexed, "fixture must not add a roots/index entry")

	replacement := bytes.Repeat([]byte{0xC3}, len(original))
	frame, err := Encode(replacement)
	require.NoError(t, err)
	objPath := replaceObject(t, tp, h, frame)

	got, err := tp.Store.GetChunk(ctx, h)
	require.ErrorIs(t, err, core.ErrNotFound)
	require.Empty(t, got, "an unindexed object still must verify its requested hash")
	expectQuarantinedObject(t, tp, objPath)
}

// TestOpenReaders_RejectSubstitutedChunk proves the stream APIs retain getObject's integrity
// result while preserving their lazy construction: Open and OpenSpan succeed, then the first read
// returns the legacy not-found outcome and no replacement bytes.
func TestOpenReaders_RejectSubstitutedChunk(t *testing.T) {
	openers := map[string]func(context.Context, *FSStore, core.Hash) (io.ReadCloser, error){
		"Open": func(ctx context.Context, s *FSStore, root core.Hash) (io.ReadCloser, error) {
			return s.Open(ctx, root)
		},
		"OpenSpan": func(ctx context.Context, s *FSStore, root core.Hash) (io.ReadCloser, error) {
			return s.OpenSpan(ctx, root, 0, -1)
		},
	}

	for name, open := range openers {
		t.Run(name, func(t *testing.T) {
			tp := newTestStore(t)
			ctx := context.Background()
			res, err := tp.Store.PutBytes(ctx, bytes.Repeat([]byte("stream integrity content\n"), 32), PutOptions{Path: "src/stream.txt"})
			require.NoError(t, err)
			require.Len(t, res.Root.Chunks, 1)
			victim := res.Root.Chunks[0]
			original, err := tp.Store.GetChunk(ctx, victim.Hash)
			require.NoError(t, err)
			frame, err := Encode(bytes.Repeat([]byte{0x9C}, len(original)))
			require.NoError(t, err)
			objPath := replaceObject(t, tp, victim.Hash, frame)

			r, err := open(ctx, tp.Store, res.Root.Hash)
			require.NoError(t, err)
			got, readErr := io.ReadAll(r)
			require.NoError(t, r.Close())
			require.ErrorIs(t, readErr, core.ErrNotFound)
			require.Empty(t, got, "streaming must not expose substituted bytes")
			expectQuarantinedObject(t, tp, objPath)
		})
	}
}

// TestGetChunk_RejectsPhysicallyOversizeObjects extends each on-disk object with Truncate rather
// than constructing a large input. The read bound applies before an untrusted object's bytes can
// be materialized, including crash debris that has no index length to constrain it.
func TestGetChunk_RejectsPhysicallyOversizeObjects(t *testing.T) {
	cases := []struct {
		name       string
		opts       []storeOpt
		compressed bool
		orphan     bool
	}{
		{name: "compressed indexed", compressed: true},
		{name: "uncompressed indexed", opts: []storeOpt{withCompressionNone()}},
		{name: "compressed orphan", compressed: true, orphan: true},
		{name: "uncompressed orphan", opts: []storeOpt{withCompressionNone()}, orphan: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestStore(t, tc.opts...)
			ctx := context.Background()
			plain := bytes.Repeat([]byte("bounded physical object content\n"), 32)

			var h core.Hash
			if tc.orphan {
				h = core.HashBytes(core.DomainChunk, plain)
				_, novel, err := tp.Store.putObject(h, plain)
				require.NoError(t, err)
				require.True(t, novel)
			} else {
				res, err := tp.Store.PutBytes(ctx, plain, PutOptions{Path: "src/bounded.txt"})
				require.NoError(t, err)
				require.Len(t, res.Root.Chunks, 1)
				h = res.Root.Chunks[0].Hash
			}

			objPath := tp.Store.objectPath(h)
			require.NoError(t, os.Truncate(paths.Long(objPath), objectPhysicalLimit(t, tc.compressed)+1))

			got, err := tp.Store.GetChunk(ctx, h)
			require.ErrorIs(t, err, core.ErrNotFound)
			require.ErrorContains(t, err, "exceeds size limit",
				"the physical bound must reject this before the object is read or decoded")
			require.Empty(t, got, "an oversize object must not return any plaintext")
			expectQuarantinedObject(t, tp, objPath)
		})
	}
}

// TestGetChunk_QuarantineFailurePreservesObject makes the quarantine root itself a regular file.
// The read must still report the corrupted object as unavailable, while retaining its original
// bytes so a later repaired quarantine path can preserve the evidence instead of losing it.
func TestGetChunk_QuarantineFailurePreservesObject(t *testing.T) {
	tp := newTestStore(t, withCompressionNone())
	ctx := context.Background()
	res, err := tp.Store.PutBytes(ctx, []byte("quarantine preservation content"), PutOptions{Path: "src/preserve.txt"})
	require.NoError(t, err)
	require.Len(t, res.Root.Chunks, 1)
	victim := res.Root.Chunks[0]
	objPath := tp.Store.objectPath(victim.Hash)
	before, err := os.ReadFile(paths.Long(objPath))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(objPath), bytes.Repeat([]byte{0x71}, len(before)), 0o600))

	quarantine := filepath.Join(paths.Of(tp.Root).Tmp, quarantineDir)
	parked := quarantine + ".blocked"
	require.NoError(t, os.Rename(paths.Long(quarantine), paths.Long(parked)))
	t.Cleanup(func() {
		_ = os.Remove(paths.Long(quarantine))
		_ = os.Rename(paths.Long(parked), paths.Long(quarantine))
	})
	require.NoError(t, os.WriteFile(paths.Long(quarantine), []byte("quarantine unavailable"), 0o600))

	got, err := tp.Store.GetChunk(ctx, victim.Hash)
	require.ErrorIs(t, err, core.ErrNotFound)
	require.Empty(t, got, "a corrupted object must not be returned when quarantine is unavailable")
	after, err := os.ReadFile(paths.Long(objPath))
	require.NoError(t, err, "a failed quarantine must retain the original object")
	require.NotEqual(t, before, after, "fixture sanity: the object must remain the substituted evidence")
	require.Equal(t, bytes.Repeat([]byte{0x71}, len(before)), after)
	require.Equal(t, int64(1), tp.counter("store.quarantine_failed"))
}

func replaceObject(t *testing.T, tp *testProject, h core.Hash, contents []byte) string {
	t.Helper()
	p := tp.Store.objectPath(h)
	require.NoError(t, os.WriteFile(paths.Long(p), contents, 0o600))
	return p
}

func expectQuarantinedObject(t *testing.T, tp *testProject, objPath string) {
	t.Helper()
	_, err := os.Stat(paths.Long(objPath))
	require.True(t, os.IsNotExist(err), "rejected object must leave the objects tree")
	attempts, err := os.ReadDir(paths.Long(filepath.Join(paths.Of(tp.Root).Tmp, quarantineDir)))
	require.NoError(t, err)
	for _, attempt := range attempts {
		if attempt.IsDir() {
			quarantined := filepath.Join(paths.Of(tp.Root).Tmp, quarantineDir, attempt.Name(), filepath.Base(objPath))
			if _, err := os.Stat(paths.Long(quarantined)); err == nil {
				return
			}
		}
	}
	t.Fatalf("rejected object %s must remain available in a quarantine attempt directory", filepath.Base(objPath))
}

func objectPhysicalLimit(t *testing.T, compressed bool) int64 {
	t.Helper()
	if !compressed {
		return MaxPutBytes
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	require.NoError(t, err)
	defer enc.Close()
	return int64(enc.MaxEncodedSize(MaxPutBytes))
}
