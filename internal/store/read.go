package store

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// GetChunk returns one chunk's plaintext bytes.
//
// Integrity rests on zstd's per-frame content checksum, which klauspost enables by default and
// which DecodeAll verifies: a flipped byte anywhere in the frame fails the decode, and getObject
// quarantines the object and reports core.ErrNotFound.
//
// It also cross-checks the decoded length against the length index/roots.jsonl recorded, which
// costs one map lookup because chunkSet is a chunk-hash → length map rather than a set (see
// fsstore.go). A hash with no index entry — an object left on disk by a crash between its write
// and its index append — is still readable; there is simply no recorded length to check it
// against, so the checksum stands alone for that one case.
func (s *FSStore) GetChunk(ctx context.Context, h core.Hash) ([]byte, error) {
	if err := s.use(); err != nil {
		return nil, err
	}
	// A chunk read decompresses up to a whole zstd frame, so it is real work under the caller's
	// budget — unlike GetRoot, which answers from the in-memory index and is deliberately exempt.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	n, known := s.chunkSet[h]
	s.mu.RUnlock()
	if !known {
		// No index entry for this hash: an object left on disk by a crash between its write and
		// its index append is readable, it just has no recorded length to check against.
		return s.getObject(h, -1)
	}
	return s.getObject(h, int(n))
}

// GetRoot returns root's description. A root that was never stored, or that GC has since
// tombstoned, reports core.ErrNotFound.
func (s *FSStore) GetRoot(ctx context.Context, root core.Hash) (Root, error) {
	if err := s.use(); err != nil {
		return Root{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.rootIndex[root]
	if !ok {
		return Root{}, fmt.Errorf("%w: root %s", core.ErrNotFound, root.Short())
	}
	return e.Root, nil
}

// Has reports whether h is present in the object store.
//
// It answers from the in-memory chunk set without touching the filesystem in the common case, and
// falls back to a stat only when the index says no — which tolerates an object left on disk by a
// crash between its write and its index append. Has has no error return, so a closed store answers
// false rather than reporting core.ErrDegraded (00-ARCHITECTURE.md §5.8's documented exception).
func (s *FSStore) Has(h core.Hash) bool {
	if s.closed.Load() {
		return false
	}
	s.mu.RLock()
	_, ok := s.chunkSet[h]
	s.mu.RUnlock()
	if ok {
		return true
	}
	return s.objectExists(h)
}

// Open returns the full content of root as a lazily decompressed stream.
//
// One chunk is decompressed at a time, in Root.Chunks order, so re-materializing a 40 MB tool
// result never allocates 40 MB: the reader holds exactly one chunk's plaintext at a time.
func (s *FSStore) Open(ctx context.Context, root core.Hash) (io.ReadCloser, error) {
	if err := s.use(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := s.GetRoot(ctx, root)
	if err != nil {
		return nil, err
	}
	return s.newChunkReader(r.Chunks, 0, totalLen(r.Chunks)), nil
}

// OpenSpan returns the [off, off+n) byte span of root's content.
//
// This is the minimal-sufficient-span read Qompack.md §8.7 makes the DEFAULT for retrieval: "the
// matching function or hunk, not the file". Only the chunks the span actually intersects are
// decompressed. Out-of-range arguments clamp rather than error, because the callers are retrieval
// tools whose job is to return the best available answer: off < 0 becomes 0, off at or past the
// end yields an empty reader with no error, n <= 0 means "to the end", and off+n past the end
// clamps to the end.
func (s *FSStore) OpenSpan(ctx context.Context, root core.Hash, off, n int64) (io.ReadCloser, error) {
	if err := s.use(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := s.GetRoot(ctx, root)
	if err != nil {
		return nil, err
	}

	total := totalLen(r.Chunks)
	if off < 0 {
		off = 0
	}
	if off >= total {
		return io.NopCloser(eofReader{}), nil
	}
	if n <= 0 || off+n > total {
		n = total - off
	}
	return s.newChunkReader(r.Chunks, off, n), nil
}

// totalLen sums a chunk list's lengths: the root's canonical content size.
func totalLen(chunks []ChunkRef) int64 {
	var total int64
	for _, c := range chunks {
		total += int64(c.Len)
	}
	return total
}

// eofReader is an always-empty io.Reader, returned for a span that starts past the end.
type eofReader struct{}

// Read always reports io.EOF.
func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

// chunkReader streams a byte span out of a root, decompressing one chunk at a time.
type chunkReader struct {
	s      *FSStore
	chunks []ChunkRef
	// i is the index of the next chunk to fetch.
	i int
	// buf holds the current chunk's plaintext, already trimmed to the part this span wants.
	buf []byte
	// skip is how many bytes of the NEXT fetched chunk to discard before emitting, which is
	// non-zero only for the first chunk of a span that starts mid-chunk.
	skip int64
	// remain is how many bytes are still to be emitted.
	remain int64
	err    error
}

// newChunkReader builds a reader over the [off, off+n) span of chunks.
func (s *FSStore) newChunkReader(chunks []ChunkRef, off, n int64) io.ReadCloser {
	// Walk the chunk lengths to find the first chunk the span touches, so chunks entirely before
	// the span are never fetched, let alone decompressed.
	start, pos := 0, int64(0)
	for start < len(chunks) {
		clen := int64(chunks[start].Len)
		if pos+clen > off {
			break
		}
		pos += clen
		start++
	}
	return &chunkReader{s: s, chunks: chunks, i: start, skip: off - pos, remain: n}
}

// Read fills p from the current chunk, fetching the next one when the current is exhausted.
func (r *chunkReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.remain <= 0 {
		return 0, io.EOF
	}
	for len(r.buf) == 0 {
		if r.i >= len(r.chunks) {
			return 0, io.EOF
		}
		c := r.chunks[r.i]
		r.i++
		plain, err := r.s.getObject(c.Hash, c.Len)
		if err != nil {
			r.err = err
			return 0, err
		}
		if r.skip > 0 {
			if r.skip >= int64(len(plain)) {
				r.skip -= int64(len(plain))
				continue
			}
			plain = plain[r.skip:]
			r.skip = 0
		}
		r.buf = plain
	}

	n := copy(p, r.buf)
	if int64(n) > r.remain {
		n = int(r.remain)
	}
	r.buf = r.buf[n:]
	r.remain -= int64(n)
	if r.remain <= 0 {
		r.buf = nil
	}
	return n, nil
}

// Close releases the reader's held chunk. There is no file handle to close: each chunk is read
// whole and the handle released before Read returns.
func (r *chunkReader) Close() error {
	r.buf, r.chunks = nil, nil
	return nil
}

// statObject is objectExists's error-reporting sibling, used by tests and by GC's sweep.
func (s *FSStore) statObject(h core.Hash) (os.FileInfo, bool) {
	for _, p := range s.objectCandidates(h) {
		if fi, err := os.Stat(paths.Long(p)); err == nil {
			return fi, true
		}
	}
	return nil, false
}
