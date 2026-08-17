package chunk_test

import (
	"crypto/sha256"
	"io"
	"testing"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/stretchr/testify/require"
)

// This file holds the deterministic fixtures every other _test.go in package chunk_test shares.
// Content-defined chunking needs non-repeating content to exercise its rolling hash at all, but
// the suite must reproduce byte-for-byte on every platform and every run — a golden boundary list
// and a novelty histogram are only meaningful if the bytes behind them never move. So every
// generator here is a pure function of an explicit seed, exactly as chunktest/behaviour.go's own
// pseudoRandomBytes is, and for the same reason.

// pseudoRandomBytes returns a deterministic, seed-derived byte slice of length n by repeatedly
// hashing a counter. It is byte-for-byte the same generator chunktest/behaviour.go uses, copied
// rather than exported so the conformance suite's fixtures and this package's fixtures can never
// drift apart silently.
func pseudoRandomBytes(seed byte, n int) []byte {
	out := make([]byte, 0, n+sha256.Size)
	block := [5]byte{seed, 0, 0, 0, 0}
	var counter uint32
	for len(out) < n {
		block[1] = byte(counter)
		block[2] = byte(counter >> 8)
		block[3] = byte(counter >> 16)
		block[4] = byte(counter >> 24)
		h := sha256.Sum256(block[:])
		out = append(out, h[:]...)
		counter++
	}
	return out[:n]
}

// testRNG is splitmix64, the same generator gear.go builds its table with, reimplemented here so
// the property and stability tests can draw reproducible pseudo-random numbers without depending
// on math/rand's (version-sensitive) stream or on any unexported symbol of package chunk. Its
// output is fixed by the algorithm rather than by a library, so a Go toolchain upgrade can never
// silently move the trials this package reports numbers for.
type testRNG struct{ state uint64 }

// newTestRNG returns a testRNG seeded with seed.
func newTestRNG(seed uint64) *testRNG { return &testRNG{state: seed} }

// uint64 returns the next value in the splitmix64 stream.
func (r *testRNG) uint64() uint64 {
	r.state += 0x9E37_79B9_7F4A_7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58_476D_1CE4_E5B9
	z = (z ^ (z >> 27)) * 0x94D0_49BB_1331_11EB
	return z ^ (z >> 31)
}

// intn returns a value in [0, n). n must be positive.
func (r *testRNG) intn(n int) int { return int(r.uint64() % uint64(n)) }

// fill overwrites b with the next len(b) bytes of the stream.
func (r *testRNG) fill(b []byte) {
	for i := 0; i < len(b); i += 8 {
		v := r.uint64()
		for j := 0; j < 8 && i+j < len(b); j++ {
			b[i+j] = byte(v >> (8 * j))
		}
	}
}

// patternedBytes returns n deterministic bytes drawn from an alphabet of the given size. alphabet
// is what lets one generator cover both ends of the input spectrum a chunker has to survive:
// alphabet 1 produces a constant run (every chunk is force-cut at Max, the pathological case),
// alphabet 256 produces incompressible noise (boundaries fire at their natural rate), and the
// values in between produce the low-entropy, highly repetitive content that real tool output
// actually looks like.
func patternedBytes(seed uint64, n, alphabet int) []byte {
	out := make([]byte, n)
	r := newTestRNG(seed)
	for i := range out {
		out[i] = byte(r.intn(alphabet))
	}
	return out
}

// chunkedReader wraps a byte slice and never returns more than size bytes from a single Read. It
// is how SplitStream's refill loop is exercised against readers that dribble their input: the one
// property SplitStream promises is that its chunk sequence is independent of how the bytes arrive,
// and that is only testable against a reader whose arrival pattern is under the test's control.
type chunkedReader struct {
	data []byte
	size int
}

// Read copies at most r.size bytes into p, reporting io.EOF once the data is exhausted.
func (r *chunkedReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.size {
		n = r.size
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p[:n], r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

// errReader delivers data and then fails with err. It reports the failure ALONGSIDE the final
// bytes — `return n, err` with n > 0 — because that is the shape a truncated stream actually has
// in Go (a decompressor, a short HTTP body, a partially written file all report their last bytes
// and their error together), and it is precisely the shape SplitStream's io.ErrUnexpectedEOF rule
// is written for. A reader that saved its error for a separate zero-byte call would never exercise
// that branch.
type errReader struct {
	data []byte
	err  error
}

// Read hands back the remaining data, returning the injected error with the final non-empty read
// (or immediately, if there was no data to begin with).
func (r *errReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.err
	}
	return n, nil
}

// collectStream runs SplitStream over data with the given per-Read size and returns the chunks the
// callback saw, with each payload copied — the payload SplitStream hands the callback aliases its
// internal buffer and is only valid for the duration of the call, so a helper that retained it
// would be testing undefined behaviour rather than the contract.
func collectStream(t *testing.T, c chunk.Chunker, data []byte, readSize int) ([]chunk.Chunk, [][]byte) {
	t.Helper()
	var (
		got      []chunk.Chunk
		payloads [][]byte
	)
	var r io.Reader = &chunkedReader{data: data, size: readSize}
	if readSize <= 0 {
		r = &chunkedReader{data: data, size: len(data) + 1}
	}
	err := c.SplitStream(r, func(ch chunk.Chunk, payload []byte) error {
		got = append(got, ch)
		payloads = append(payloads, append([]byte(nil), payload...))
		return nil
	})
	require.NoError(t, err)
	return got, payloads
}

// requireContiguous asserts chunks start at offset 0, each begins exactly where the previous one
// ended, every chunk is non-empty, and together they cover total bytes exactly once. Every test
// that splits anything checks this, because a chunker that loses or duplicates a byte would still
// pass a size-bounds check and would still produce a stable-looking root hash.
func requireContiguous(t *testing.T, chunks []chunk.Chunk, total int) {
	t.Helper()
	var off int64
	for i, c := range chunks {
		require.Equal(t, off, c.Offset, "chunk %d must start where chunk %d ended", i, i-1)
		require.Positive(t, c.Len, "chunk %d must be non-empty", i)
		off += int64(c.Len)
	}
	require.EqualValues(t, total, off, "chunks must cover the input exactly once")
}
