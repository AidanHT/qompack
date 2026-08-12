package chunktest

import (
	"crypto/sha256"
	"testing"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertions of the chunktest suite (00-ARCHITECTURE.md §5.22
// table; §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md): boundary stability under
// insertion, determinism, chunk contiguity, and RootHash's domain-separated formula. All four are
// authored now, gated behind the same Rule W-1 stub probe as the rest of the suite, so SP-04
// inherits them rather than writing its own grader.

// behaviourDataSize is the size of the fixture these cases split: large enough to produce several
// chunks under any reasonable Min/Target/Max, and deliberately not a round number so it cannot be
// mistaken for a config default.
const behaviourDataSize = 262157

// insertionSize is how many bytes runBoundaryStabilityCase inserts into the fixture.
const insertionSize = 3000

// pseudoRandomBytes returns a deterministic, seed-derived byte slice of length n by repeatedly
// hashing a counter. Content-defined chunking needs non-repeating content to exercise its
// rolling-hash boundaries, but the suite must stay reproducible across runs and gosec (G404)
// forbids math/rand outside test files, so this hashes rather than "randomizes" — crypto/sha256 is
// used only as a convenient deterministic byte-stream generator, not for any security property.
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

// runDeterminismCase asserts Split produces byte-identical results across two calls against the
// same input, from two independently constructed Chunkers.
func runDeterminismCase(t *testing.T, factory func(t *testing.T) chunk.Chunker) {
	t.Helper()
	data := pseudoRandomBytes(1, behaviourDataSize)
	c1 := factory(t).Split(data)
	c2 := factory(t).Split(data)
	require.NotEmpty(t, c1, "a real Chunker must produce at least one chunk for non-empty input")
	require.Equal(t, c1, c2, "Split must be deterministic for identical input")
}

// runSizeBoundsCase asserts every chunk is non-empty, chunks are contiguous (each starts exactly
// where the previous one ended), and together they cover the input exactly once
// (00-ARCHITECTURE.md §5.5: Min <= len <= Max for every chunk except the last — the exact Min/Max
// bound is Params the suite's own factory closed over and has no visibility into, so this checks
// the bound-independent structural invariants; SP-04's own package-level tests own the exact
// Min/Max fixture check against a known Params).
func runSizeBoundsCase(t *testing.T, factory func(t *testing.T) chunk.Chunker) {
	t.Helper()
	data := pseudoRandomBytes(2, behaviourDataSize)
	chunks := factory(t).Split(data)
	require.NotEmpty(t, chunks)

	var offset int64
	for i, c := range chunks {
		require.Equal(t, offset, c.Offset, "chunk %d must start where the previous one ended", i)
		require.Positive(t, c.Len, "chunk %d must be non-empty", i)
		offset += int64(c.Len)
	}
	require.EqualValues(t, len(data), offset, "chunks must cover the entire input exactly once")
}

// runRootHashFormulaCase asserts chunk.RootHash over a real split matches the same domain-
// separated formula chunk_test.go pins directly against chunk.RootHash's own unit tests.
func runRootHashFormulaCase(t *testing.T, factory func(t *testing.T) chunk.Chunker) {
	t.Helper()
	data := pseudoRandomBytes(3, behaviourDataSize)
	chunks := factory(t).Split(data)
	require.NotEmpty(t, chunks)

	var buf []byte
	for _, c := range chunks {
		buf = append(buf, c.Hash[:]...)
	}
	want := core.HashBytes(core.DomainRoot, buf)
	require.Equal(t, want, chunk.RootHash(chunks))
}

// runBoundaryStabilityCase asserts FastCDC's defining property (00-ARCHITECTURE.md §5.5): inserting
// bytes at offset k perturbs at most two chunks — every chunk boundary is either untouched
// (present, byte-identical, in both splits) or newly introduced by the edit, and at most two
// chunks on each side of that comparison differ.
func runBoundaryStabilityCase(t *testing.T, factory func(t *testing.T) chunk.Chunker) {
	t.Helper()
	original := pseudoRandomBytes(4, behaviourDataSize)
	before := factory(t).Split(original)
	require.GreaterOrEqual(t, len(before), 4, "fixture must be large enough to produce several chunks")

	insertAt := int(before[2].Offset) + before[2].Len/2
	insertion := pseudoRandomBytes(99, insertionSize)
	edited := make([]byte, 0, len(original)+len(insertion))
	edited = append(edited, original[:insertAt]...)
	edited = append(edited, insertion...)
	edited = append(edited, original[insertAt:]...)

	after := factory(t).Split(edited)
	require.NotEmpty(t, after)

	beforeHashes := make(map[core.Hash]bool, len(before))
	for _, c := range before {
		beforeHashes[c.Hash] = true
	}
	afterHashes := make(map[core.Hash]bool, len(after))
	for _, c := range after {
		afterHashes[c.Hash] = true
	}

	var novel int
	for h := range afterHashes {
		if !beforeHashes[h] {
			novel++
		}
	}
	var orphaned int
	for h := range beforeHashes {
		if !afterHashes[h] {
			orphaned++
		}
	}
	require.LessOrEqual(t, novel, 2, "inserting bytes must introduce at most 2 new chunks (§5.5)")
	require.LessOrEqual(t, orphaned, 2, "inserting bytes must invalidate at most 2 original chunks (§5.5)")
}
