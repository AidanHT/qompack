package chunk

import (
	"math/bits"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file is an INTERNAL test (package chunk, not chunk_test) because everything it asserts is
// about symbols the package deliberately does not export: the gear table, the seed it is derived
// from, and the algebraic fixed point a constant byte stream drives the rolling hash to. Those are
// implementation details for callers but they are the frozen wire format for the store, so they
// need tests that can see them.

// TestSplitMix64_ReferenceVectors pins splitmix64 to the reference implementation (Steele, Lea &
// Flood 2014, as published in java.util.SplittableRandom / Vigna's public-domain C). The gear table
// is generated rather than committed as a 4 KB literal, so the generator itself is the thing that
// has to be pinned: if splitmix64 drifted, every gear entry would move, every boundary would move,
// and every store on disk would silently re-chunk.
//
// The vectors below are the first values splitmix64 produces from state 0, which is the sequence
// every published implementation agrees on.
func TestSplitMix64_ReferenceVectors(t *testing.T) {
	t.Parallel()
	want := []uint64{
		0xE220A8397B1DCDAF,
		0x6E789E6AA1B965F4,
		0x06C45D188009454F,
		0xF88BB8A8724C81EC,
		0x1B39896A51A8749B,
	}
	state := uint64(0)
	for i, w := range want {
		require.Equal(t, w, splitmix64(&state), "splitmix64 output %d", i)
	}
}

// TestGearTable_IsWellFormed asserts the properties the boundary decision actually depends on: 256
// entries, all distinct, and none zero. A duplicate entry would make two byte values
// indistinguishable to the rolling hash; a zero entry would make one byte value contribute nothing,
// so a run of it would freeze the fingerprint outright.
func TestGearTable_IsWellFormed(t *testing.T) {
	t.Parallel()
	require.Len(t, gear, 256)

	seen := make(map[uint64]int, len(gear))
	for i, g := range gear {
		require.NotZero(t, g, "gear[%d] must not be zero", i)
		prev, dup := seen[g]
		require.False(t, dup, "gear[%d] duplicates gear[%d]", i, prev)
		seen[g] = i
	}

	// A gear table whose entries were badly biased in the high bits would skew the mask test, which
	// only ever looks at the bits the widest mask covers. The width is derived from the chunker
	// rather than written down, so this stays honest if maskWidthDelta or the default Target moves.
	c, ok := New(DefaultParams()).(*chunker)
	require.True(t, ok)
	width := bits.OnesCount64(c.maskS)
	shift := 64 - width

	var extremes int
	for _, g := range gear {
		top := g >> shift
		if top == 0 || top == (uint64(1)<<width)-1 {
			extremes++
		}
	}
	require.LessOrEqual(t, extremes, 2,
		"gear's top %d bits are degenerate; the mask test would be biased", width)
}

// TestGearFixedPoint_ConstantByteStream proves *why* an all-constant stream can only be cut by the
// Max force-cut, rather than merely observing that it is.
//
// With the XOR recurrence fp = (fp<<1) ^ gear[b], bit j of fp is the XOR of bit j-t of gear[b_{i-t}]
// for t = 0..j. Feed the same byte b forever and that collapses: bit j of fp becomes the parity of
// the low j+1 bits of gear[b], and it is a genuine fixed point — one more identical byte maps it to
// itself. So after the 64-byte priming window the mask test asks the same question at every
// position and gets the same answer, forever. This test computes that fixed point independently
// from gear[0] and asserts (a) the recurrence really does reach it and (b) for this frozen seed the
// answer is "no cut", under both masks.
//
// If a future gearSeed ever made the fixed point satisfy a mask, an all-zero file would be cut into
// Min-sized chunks instead of Max-sized ones — an 16x increase in chunk count for the single most
// common degenerate input there is. That is why this is asserted rather than assumed.
func TestGearFixedPoint_ConstantByteStream(t *testing.T) {
	t.Parallel()

	for _, b := range []byte{0x00, 0xFF, 'A'} {
		// Independently derived fixed point: bit j = parity of the low j+1 bits of gear[b].
		var want uint64
		for j := 0; j < 64; j++ {
			if bits.OnesCount64(gear[b]&((uint64(1)<<(j+1))-1))&1 == 1 {
				want |= uint64(1) << j
			}
		}

		// The recurrence, run over a long constant run, must land on exactly that value.
		var fp uint64
		for i := 0; i < 4*GearWindow; i++ {
			fp = (fp << 1) ^ gear[b]
		}
		require.Equal(t, want, fp, "byte %#02x: the rolling hash did not reach the predicted fixed point", b)

		// And one more byte must leave it unchanged — that is what "fixed point" means.
		require.Equal(t, fp, (fp<<1)^gear[b], "byte %#02x: the predicted fixed point is not fixed", b)

		c, ok := New(DefaultParams()).(*chunker)
		require.True(t, ok)
		require.NotZero(t, fp&c.maskS, "byte %#02x: the fixed point satisfies maskS; constant runs would be cut at Min", b)
		require.NotZero(t, fp&c.maskL, "byte %#02x: the fixed point satisfies maskL; constant runs would be cut at Target", b)
	}
}
