package chunk_test

import (
	"bytes"
	"sync"
	"testing"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// defaults is the Params every test in this file splits with, spelled once so a test that reads
// "every chunk but the last is in [Min, Max]" is checking the same numbers the assertions use.
func defaults() chunk.Params { return chunk.DefaultParams() }

// TestSplit_Empty asserts both spellings of "nothing to split" produce the documented nil, not an
// empty non-nil slice and not a zero-length chunk. store's roots.jsonl writer distinguishes the
// two — a root over nil chunks is a legitimate record, a root over a phantom empty chunk is not.
func TestSplit_Empty(t *testing.T) {
	t.Parallel()
	c := chunk.New(defaults())
	require.Nil(t, c.Split(nil))
	require.Nil(t, c.Split([]byte{}))
}

// TestSplit_ShorterThanMin asserts input below Min is returned as a single chunk without any
// boundary scan at all. There is no content-defined boundary to find below Min by construction
// (nextCut never tests before Min), so anything else would mean the chunker had invented one.
func TestSplit_ShorterThanMin(t *testing.T) {
	t.Parallel()
	c := chunk.New(defaults())
	data := bytes.Repeat([]byte{0x41}, 900)

	chunks := c.Split(data)
	require.Len(t, chunks, 1)
	require.EqualValues(t, 0, chunks[0].Offset)
	require.Equal(t, 900, chunks[0].Len)
	require.Equal(t, core.HashBytes(chunk.ChunkDomain, data), chunks[0].Hash)
}

// TestSplit_ExactlyMin pins the boundary of that rule: at exactly Min bytes nextCut's `n <= Min`
// guard still fires (the first testable position is Min, which is one past the last byte), so the
// input is one chunk.
func TestSplit_ExactlyMin(t *testing.T) {
	t.Parallel()
	p := defaults()
	c := chunk.New(p)
	data := pseudoRandomBytes(12, p.Min)

	chunks := c.Split(data)
	require.Len(t, chunks, 1)
	require.Equal(t, p.Min, chunks[0].Len)
}

// TestSplit_SizeBounds asserts the §5.5 size contract over a 4 MiB fixture: every chunk except the
// last lies in [Min, Max]. The last is exempt because the stream simply ran out — it is the one
// chunk whose length is not a decision the chunker made.
func TestSplit_SizeBounds(t *testing.T) {
	t.Parallel()
	p := defaults()
	c := chunk.New(p)
	data := pseudoRandomBytes(5, 4<<20)

	chunks := c.Split(data)
	require.Greater(t, len(chunks), 1)
	for i, ch := range chunks[:len(chunks)-1] {
		require.GreaterOrEqual(t, ch.Len, p.Min, "chunk %d is shorter than Min", i)
		require.LessOrEqual(t, ch.Len, p.Max, "chunk %d is longer than Max", i)
	}
	last := chunks[len(chunks)-1]
	require.Positive(t, last.Len)
	require.LessOrEqual(t, last.Len, p.Max)
	requireContiguous(t, chunks, len(data))
}

// TestSplit_Contiguity asserts the covering property across several input shapes at once: no byte
// is skipped, no byte is emitted twice, and the offsets are exactly the running sum of the lengths.
func TestSplit_Contiguity(t *testing.T) {
	t.Parallel()
	c := chunk.New(defaults())
	for _, n := range []int{1, 2, 1023, 1024, 1025, 4095, 4096, 16383, 16384, 16385, 100000, 1 << 20} {
		data := pseudoRandomBytes(6, n)
		chunks := c.Split(data)
		require.NotEmpty(t, chunks, "n=%d", n)
		requireContiguous(t, chunks, n)

		// And the concatenated payloads must reproduce the input byte-for-byte.
		var rebuilt []byte
		for _, ch := range chunks {
			rebuilt = append(rebuilt, data[ch.Offset:int(ch.Offset)+ch.Len]...)
		}
		require.True(t, bytes.Equal(data, rebuilt), "n=%d: chunks do not reassemble the input", n)
	}
}

// TestSplit_AllZeros_HitsMaxOnly asserts the pathological constant-content case force-cuts at Max
// and nothing else. See fastcdc_internal_test.go's TestGearFixedPoint_ConstantByteStream for the
// *reason*: a constant byte stream drives the rolling hash to an algebraic fixed point after the
// 64-byte priming window, so the mask test returns the same answer at every position forever, and
// with this gear seed that answer is "no cut". This test is the black-box half of that claim.
func TestSplit_AllZeros_HitsMaxOnly(t *testing.T) {
	t.Parallel()
	p := defaults()
	c := chunk.New(p)
	data := make([]byte, 1<<20)

	chunks := c.Split(data)
	require.Equal(t, len(data)/p.Max, len(chunks),
		"a constant stream must be cut only by the Max force-cut, so the count is len/Max exactly")
	for i, ch := range chunks {
		require.Equal(t, p.Max, ch.Len, "chunk %d of an all-zero stream must be exactly Max", i)
	}
	requireContiguous(t, chunks, len(data))

	// Every chunk is the same Max bytes of zero, so every hash must be identical — which is the
	// deduplication win this whole package exists for, visible in its simplest possible form.
	for _, ch := range chunks[1:] {
		require.Equal(t, chunks[0].Hash, ch.Hash)
	}
}

// TestSplit_Determinism asserts Split is a pure function of (Params, data): 100 repeats through one
// Chunker and one repeat through an independently constructed second Chunker must agree exactly.
// Nothing about the store's content addressing survives a chunker that carries state between calls.
func TestSplit_Determinism(t *testing.T) {
	t.Parallel()
	c1 := chunk.New(defaults())
	c2 := chunk.New(defaults())
	data := pseudoRandomBytes(8, 300000)

	want := c1.Split(data)
	require.NotEmpty(t, want)
	for i := 0; i < 100; i++ {
		require.Equal(t, want, c1.Split(data), "repeat %d diverged", i)
	}
	require.Equal(t, want, c2.Split(data), "a second Chunker with the same Params diverged")
}

// TestSplit_Concurrent asserts the documented "safe for concurrent use" property under -race. The
// claim is not free: the obvious optimizations for the two-allocation budget — caching a hasher or
// a scratch buffer on the chunker — would each silently break it, and the daemon splits payloads
// from several sessions at once through one shared Chunker, so the corruption would be
// intermittent and would show up as an unreproducible bad root hash rather than as a crash.
func TestSplit_Concurrent(t *testing.T) {
	t.Parallel()
	c := chunk.New(defaults())

	inputs := make([][]byte, 8)
	want := make([][]chunk.Chunk, len(inputs))
	for i := range inputs {
		inputs[i] = pseudoRandomBytes(byte(40+i), 60000+i*1000)
		want[i] = c.Split(inputs[i])
		require.NotEmpty(t, want[i])
	}

	const goroutines = 16
	got := make([][]chunk.Chunk, goroutines)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[g] = c.Split(inputs[g%len(inputs)])
		}()
	}
	wg.Wait()
	for g := 0; g < goroutines; g++ {
		require.Equal(t, want[g%len(inputs)], got[g], "goroutine %d diverged", g)
	}

}

// TestSplit_MeanChunkSize asserts the normalized-chunking mask pair actually delivers the chunk
// size it is configured for. FastCDC's normalization (NC=1 here) trades a harder mask below Target for
// an easier one above it, which tightens the size distribution around Target; the mean lands
// somewhat above Target because the Min floor truncates the short tail. A mean far from this band
// would mean the masks were derived from the wrong bit width — a silent, store-wide regression that
// no contiguity or size-bounds check would catch.
func TestSplit_MeanChunkSize(t *testing.T) {
	t.Parallel()
	p := defaults()
	c := chunk.New(p)
	data := pseudoRandomBytes(11, 8<<20)

	chunks := c.Split(data)
	require.NotEmpty(t, chunks)
	mean := float64(len(data)) / float64(len(chunks))
	t.Logf("mean chunk size over %d MiB = %.1f bytes across %d chunks (Min=%d Target=%d Max=%d)",
		len(data)>>20, mean, len(chunks), p.Min, p.Target, p.Max)

	require.GreaterOrEqual(t, mean, 3200.0, "mean chunk size is far below Target")
	require.LessOrEqual(t, mean, 5200.0, "mean chunk size is far above Target")
}

// TestChunkHashMatchesCoreHashBytes proves the streaming digest Split builds by hand — one
// precomputed domain||0x00 prefix written straight into the hasher, then the payload — is
// byte-identical to core.HashBytes(ChunkDomain, payload) for the same bytes. Split does not call
// HashBytes because HashBytes allocates a fresh []byte(domain) and a fresh Sum(nil) on every call,
// which is exactly the per-chunk allocation the 2-allocation budget forbids; this test is what
// makes that optimization safe, because a divergence here would silently re-key every chunk in
// every store.
func TestChunkHashMatchesCoreHashBytes(t *testing.T) {
	t.Parallel()
	c := chunk.New(defaults())
	r := newTestRNG(1234)

	for i := 0; i < 1000; i++ {
		n := r.intn(40000) + 1
		data := make([]byte, n)
		r.fill(data)

		for j, ch := range c.Split(data) {
			payload := data[ch.Offset : int(ch.Offset)+ch.Len]
			require.Equal(t, core.HashBytes(chunk.ChunkDomain, payload), ch.Hash,
				"payload %d chunk %d: Split's digest diverged from core.HashBytes", i, j)
		}
	}
}

// TestChunkDomains asserts the package's domain constants are aliases of core's registry entries
// rather than re-spelled string literals. core owns the domain registry (internal/core/hash.go);
// a package that spelled "qompack.chunk.v1" itself could drift from it during a version bump and
// re-key every stored object without any test noticing.
func TestChunkDomains(t *testing.T) {
	t.Parallel()
	require.Equal(t, core.DomainChunk, chunk.ChunkDomain)
	require.Equal(t, core.DomainRoot, chunk.RootDomain)
	require.NotEqual(t, chunk.ChunkDomain, chunk.RootDomain)
}

// TestRootHash_DomainSeparation asserts the degenerate one-chunk case: the Merkle root over a
// single chunk must NOT equal that chunk's own hash. This is the entire point of the two-domain
// scheme — without it, a one-chunk object's root and its only chunk would collide in the CAS, and
// a lookup for the root would return the chunk (or vice versa) with no way to tell them apart.
func TestRootHash_DomainSeparation(t *testing.T) {
	t.Parallel()
	c := chunk.New(defaults())
	data := pseudoRandomBytes(9, 500)

	chunks := c.Split(data)
	require.Len(t, chunks, 1)
	root := chunk.RootHash(chunks)
	require.NotEqual(t, chunks[0].Hash, root, "the root of a single chunk must not be that chunk's hash")
	require.Equal(t, core.HashBytes(core.DomainRoot, chunks[0].Hash[:]), root)
}

// TestRefs_RoundTrip asserts Chunk.Ref and Refs project onto core.ChunkRef without reordering,
// dropping or re-deriving anything. store writes exactly this list into index/roots.jsonl, so the
// order and the Len must survive verbatim.
func TestRefs_RoundTrip(t *testing.T) {
	t.Parallel()
	c := chunk.New(defaults())
	data := pseudoRandomBytes(10, 200000)

	chunks := c.Split(data)
	require.NotEmpty(t, chunks)

	refs := chunk.Refs(chunks)
	require.Len(t, refs, len(chunks))
	var total int
	for i, ref := range refs {
		require.Equal(t, chunks[i].Hash, ref.Hash, "ref %d", i)
		require.Equal(t, chunks[i].Len, ref.Len, "ref %d", i)
		require.Equal(t, chunks[i].Ref(), ref, "Refs must agree with Chunk.Ref elementwise")
		total += ref.Len
	}
	require.Equal(t, len(data), total)

	require.Nil(t, chunk.Refs(nil))
	require.Nil(t, chunk.Refs([]chunk.Chunk{}))
}
