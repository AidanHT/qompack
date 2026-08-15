package store_test

import (
	"bytes"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// maxRoundTripBytes bounds the random slices TestStoreCompress_RoundTrip generates (§14.1 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md: "random slices up to 1 MiB").
const maxRoundTripBytes = 1 << 20

// TestStoreCompress_RoundTrip is the property test §14.1 requires: Decode(Encode(b)) == b for
// random slices up to 1 MiB, including the empty slice.
func TestStoreCompress_RoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, maxRoundTripBytes).Draw(rt, "n")
		b := rapid.SliceOfN(rapid.Byte(), n, n).Draw(rt, "b")

		encoded, err := store.Encode(b)
		require.NoError(rt, err)

		decoded, err := store.Decode(encoded)
		require.NoError(rt, err)
		// bytes.Equal, not require.Equal: EncodeAll's own documented behaviour for empty input is
		// "nothing is returned" (klauspost/compress/zstd), so Decode(Encode(b)) legitimately comes
		// back as a nil []byte when b is empty, even if b itself was a non-nil []byte{} that rapid
		// drew for n=0. require.Equal distinguishes nil from an empty-but-non-nil slice;
		// bytes.Equal correctly treats them as the same content, which is the property this test
		// actually claims ("Decode(Encode(b)) == b" means byte-content equality, not nilness).
		require.True(rt, bytes.Equal(b, decoded), "Decode(Encode(b)) must reproduce b's content: got %v, want %v", decoded, b)
	})
}

// TestStoreCompress_Encode_EmptyInput pins the boundary case rapid's IntRange(0, ...) already
// covers, but names it explicitly so a regression in the empty-input path fails with a targeted
// test name rather than only a shrunk property-test counterexample.
func TestStoreCompress_Encode_EmptyInput(t *testing.T) {
	encoded, err := store.Encode(nil)
	require.NoError(t, err)

	decoded, err := store.Decode(encoded)
	require.NoError(t, err)
	require.Empty(t, decoded)
}

// bombDecodedSize is the decompressed size TestStoreCompress_Decode_RejectsOversizedBomb encodes:
// comfortably over store's 64 MiB decode cap (64<<20 = 67108864 bytes), so the cap is exercised
// with margin rather than at its exact boundary.
const bombDecodedSize = (64 << 20) + (16 << 20) // 80 MiB

// TestStoreCompress_Decode_RejectsOversizedBomb asserts Decode refuses a payload whose
// decompressed size exceeds the 64 MiB cap, returning an error rather than allocating it (§13
// invariant 7). The "bomb" here is a large, all-zero, highly compressible payload: it encodes to
// a tiny blob (asserted below) but declares — and would require — far more than 64 MiB to
// decompress, exactly the allocation-bomb shape the cap defends against. This does not require
// hand-crafting a malicious zstd frame: an honestly-encoded, sufficiently large and compressible
// input already produces the "small on disk, huge decompressed" shape a bomb has.
func TestStoreCompress_Decode_RejectsOversizedBomb(t *testing.T) {
	bomb := make([]byte, bombDecodedSize) // zero-filled by make; highly compressible

	encoded, err := store.Encode(bomb)
	require.NoError(t, err)
	require.Less(t, len(encoded), len(bomb)/100,
		"fixture sanity: an all-zero payload must compress to a small fraction of its size")

	decoded, err := store.Decode(encoded)
	require.Error(t, err, "Decode must refuse a payload whose decompressed size exceeds the 64 MiB cap")
	require.Empty(t, decoded)
	require.ErrorIs(t, err, zstd.ErrDecoderSizeExceeded)
}
