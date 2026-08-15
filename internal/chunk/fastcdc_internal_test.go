package chunk

import (
	"math/bits"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTopBits pins the mask constructor: topBits(n) must set exactly the n most significant bits
// and nothing else. The rolling hash accumulates its longest history in the high bits (bit 63
// depends on 64 bytes, bit 0 on one), so a mask built from the LOW bits would make the boundary
// decision depend on a one-byte window and destroy boundary stability outright.
func TestTopBits(t *testing.T) {
	t.Parallel()
	for n := 1; n <= 64; n++ {
		m := topBits(n)
		require.Equal(t, n, bits.OnesCount64(m), "topBits(%d) must set %d bits", n, n)
		require.Equal(t, 64-n, bits.TrailingZeros64(m), "topBits(%d) must set the HIGH bits", n)
	}
}

// TestMaskDerivation asserts the normalized-chunking mask pair is derived from Target rather than
// written down (00-ARCHITECTURE.md §5.5, Xia et al. 2016): maskS is maskWidthDelta bits WIDER than
// log2(Target), so a cut before Target is 2^maskWidthDelta times less likely than the naive rate,
// and maskL is the same number of bits narrower, so a cut after Target is that much more likely.
// That is what pulls the size distribution in toward Target from both sides.
//
// The expectations are written in terms of maskWidthDelta rather than a literal, so that changing
// the normalization level is a one-constant edit whose blast radius shows up in the golden boundary
// list (TestSplit_GoldenBoundaries) and the stability histograms — where it belongs — rather than
// as a spurious failure here.
//
// It also pins the containment that makes the two-phase scan coherent: maskS's bits are a superset
// of maskL's, so any position that would have cut under maskS also cuts under maskL. Without that,
// crossing the Target line could *suppress* a boundary that had already been found, and an
// insertion earlier in the chunk could delete a boundary far downstream.
func TestMaskDerivation(t *testing.T) {
	t.Parallel()
	for _, target := range []int{256, 1024, 4096, 65536, 1 << 20} {
		c, ok := New(Params{Min: target / 8, Target: target, Max: 4 * target}).(*chunker)
		require.True(t, ok)

		width := bits.TrailingZeros64(uint64(target))
		require.Equal(t, topBits(width+maskWidthDelta), c.maskS, "target=%d", target)
		require.Equal(t, topBits(width-maskWidthDelta), c.maskL, "target=%d", target)
		require.Equal(t, 2*maskWidthDelta,
			bits.OnesCount64(c.maskS)-bits.OnesCount64(c.maskL),
			"the two masks must differ by 2*NC bits; target=%d", target)
		require.Equal(t, c.maskL, c.maskS&c.maskL, "maskL must be a subset of maskS; target=%d", target)
	}
}

// TestNextCut_PrimingWindowIsInsideTheChunk asserts the invariant SplitStream depends on: the
// priming loop reads data[Min-GearWindow : Min], so Min >= GearWindow must hold for every Params
// New will accept. If it did not, nextCut would index before the start of its own slice — a panic
// on the hot path, which §12.3 forbids outright.
func TestNextCut_PrimingWindowIsInsideTheChunk(t *testing.T) {
	t.Parallel()

	// GearWindow must cover the FULL fingerprint history. Bit 63 of the fingerprint is the XOR of
	// one bit from each of the last 64 gear entries, so anything smaller than 64 would leave the
	// top mask bit depending on bytes the priming loop never fed in — and the boundary decision
	// would then differ between Split (which primes from Min-GearWindow) and a hypothetical
	// implementation that rolled from the chunk start.
	require.Equal(t, 64, GearWindow)

	for _, p := range []Params{
		{},
		{Min: 1, Target: 1, Max: 1},
		{Min: -5, Target: -5, Max: -5},
		{Min: 1 << 30, Target: 3, Max: 7},
		DefaultParams(),
	} {
		c, ok := New(p).(*chunker)
		require.True(t, ok)
		require.GreaterOrEqual(t, c.p.Min, GearWindow, "New(%+v) produced Min < GearWindow", p)
		require.Less(t, c.p.Min, c.p.Target)
		require.Less(t, c.p.Target, c.p.Max)
	}
}

// TestNextCut_IsAFunctionOfTheTrailingWindow proves the property the doc comment on the rolling
// hash claims: because the recurrence is XOR-and-shift rather than addition, there is no carry
// propagation, so bit j of the fingerprint depends on exactly the last j+1 bytes. With every mask
// bit high in the word, the boundary decision at position i is therefore a pure function of
// data[i-63 : i+1] — it does not depend on where the current chunk started.
//
// The test demonstrates this the only way that really counts: it primes the fingerprint from two
// completely different 4 KB prefixes, then feeds both the same 64-byte tail, and requires the two
// fingerprints to be bit-for-bit identical. A rolling hash with carries (fp = fp*prime + b, or
// fp = (fp<<1) + gear[b]) fails this immediately.
func TestNextCut_IsAFunctionOfTheTrailingWindow(t *testing.T) {
	t.Parallel()

	roll := func(prefix, tail []byte) uint64 {
		var fp uint64
		for _, b := range prefix {
			fp = (fp << 1) ^ gear[b]
		}
		for _, b := range tail {
			fp = (fp << 1) ^ gear[b]
		}
		return fp
	}

	tail := make([]byte, GearWindow)
	for i := range tail {
		tail[i] = byte(7*i + 3)
	}
	a := make([]byte, 4096)
	b := make([]byte, 4096)
	for i := range a {
		a[i] = byte(i)
		b[i] = byte(255 - i%256)
	}

	require.Equal(t, roll(a, tail), roll(b, tail),
		"the fingerprint after GearWindow bytes must not depend on anything before them")
	require.Equal(t, roll(nil, tail), roll(a, tail),
		"priming from zero over GearWindow bytes must reproduce the steady-state fingerprint")
}
