package canon_test

import (
	"testing"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// The QPKS frame's geometry, from internal/sketch/header.go's layout table and from
// Signature.MarshalBinary's: a frame is a fixed 32-byte prefix, a params block, the body, and a
// trailing CRC32C; a MinHash body is a uint16 permutation count followed by one little-endian
// uint64 minimum per permutation.
//
// They are spelled here rather than exported from sketch because this test is a CONSUMER of the
// frozen bytes and has to be able to disagree with the producer. A helper borrowed from the package
// under test would make the assertion "sketch agrees with itself", which is what Rule W-2 exists to
// stop.
const (
	// qpksCRCLen is the trailing Castagnoli checksum.
	qpksCRCLen = 4
	// sigPermsLen is the width of the leading uint16 permutation count in a MinHash body.
	sigPermsLen = 2
	// sigMinLen is the encoded width of one minimum.
	sigMinLen = 8
)

// wireFixtureBody is the tool output the produced signature is computed over. It is deliberately
// ordinary: the assertion below is about the FRAME, and a document engineered to collide with the
// fixture's would prove nothing about it.
const wireFixtureBody = "=== RUN   TestSignature\n--- PASS: TestSignature (0.03s)\n" +
	"ok  \tgithub.com/qompack/qompack/internal/canon\t0.412s\n" +
	"2024-01-15T10:32:07Z worker pid=41235 finished in 250ms\n"

// TestSignature_WireCompatibleWithFrozenSketchFixture is SP-04's Rule W-2 obligation against
// SP-03's frozen fixtures (V2-MERGE-07).
//
// canon does not implement MinHash — it calls sketch.MinHash and carries the result in
// Result.Signature, which SP-06 then writes into store.ToolUseRecord — so what W-2 asks of this
// package is not that it can recompute SP-03's numbers but that the signature it PRODUCES is the
// same thing on the wire as the one SP-03 froze. That is a real question with a real failure mode:
// a permutation count that drifted from Appendix C's 128, or a frame this build writes and SP-03's
// decoder would refuse, would both be invisible to every other test in this package, because every
// other test compares canon against canon.
//
// The fixture's bytes are load bearing in three separate ways below — decoded, re-encoded, and
// compared against — so this cannot pass with a fixture that is absent, truncated or regenerated.
//
// The two signatures are NOT expected to be equal. sketch's own golden document is hashed at
// shingle width 8 and canon asks for canon.DefaultShingleSize, which is 5 (Appendix C configures no
// shingle key, so the two packages choose independently and deliberately). Equality of the VALUES
// would therefore be a coincidence; equality of the FORMAT is the contract.
func TestSignature_WireCompatibleWithFrozenSketchFixture(t *testing.T) {
	_, frame, frozen := testutil.ContractFixture(t, "sketch", "minhash_128")
	require.True(t, frozen,
		"the minhash_128 fixture is declared frozen in testdata/golden/contracts/sketch; a "+
			"consumer finding it unfrozen means the manifest moved backwards, which is a "+
			"repository problem and not a not-yet-implemented one")
	require.NotEmpty(t, frame)

	// 1. The frozen bytes decode with the decoder this build links against. Rule W-2: a fixture
	//    the real implementation cannot read is a verification failure, not a fixture bug.
	var fixture sketch.SigSketch
	require.NoError(t, fixture.UnmarshalBinary(frame),
		"SP-03's frozen MinHash frame must decode against the sketch this build links")

	// 2. And re-encode to exactly the same bytes, which is what makes every byte of the fixture —
	//    header, params, body and CRC — an assertion rather than a length.
	round, err := fixture.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, frame, round, "the frozen frame must survive a decode/encode round trip")

	// 3. Appendix C's permutation count reaches sketch through canon's OptionsFrom, and it is the
	//    width the frozen frame declares. This is the drift that would otherwise be silent: two
	//    signatures of different widths are not comparable, and Jaccard would refuse every pair.
	cfg := config.Defaults().Store.Canonicalize
	opts := canon.OptionsFrom(cfg, true)
	require.True(t, opts.MinHash.Enabled, "Appendix C enables MinHash by default")
	require.Equal(t, int(fixture.Sig.Perms), opts.MinHash.Permutations,
		"OptionsFrom must carry the same permutation count the frozen frame was written with")
	require.Len(t, fixture.Sig.Mins, opts.MinHash.Permutations)

	// 4. A production canon.Run produces a signature of that width over the canonical bytes.
	res, err := canon.Default(cfg).Run("Bash", "", []byte(wireFixtureBody), opts)
	require.NoError(t, err)
	require.Equal(t, fixture.Sig.Perms, res.Signature.Perms,
		"canon's signature must be as wide as the frozen one")
	require.Len(t, res.Signature.Mins, int(res.Signature.Perms))

	// 5. Framed with the fixture's own Created stamp, canon's signature is the same frame SHAPE:
	//    the same total width, and a byte-identical header — magic, version, kind, count, created,
	//    param block — with only the minima and the checksum over them differing.
	produced, err := (&sketch.SigSketch{Sig: res.Signature, Created: fixture.Created}).MarshalBinary()
	require.NoError(t, err)
	require.Len(t, produced, len(frame),
		"a signature of the same width must frame to the same number of bytes")

	headerLen := len(frame) - qpksCRCLen - (sigPermsLen + sigMinLen*int(fixture.Sig.Perms))
	require.Positive(t, headerLen)
	require.Equal(t, frame[:headerLen], produced[:headerLen],
		"canon's frame must declare exactly what SP-03's frozen frame declares")

	// 6. And it decodes back to the signature canon computed, which is the direction SP-06 will
	//    read a stored record in.
	var decoded sketch.SigSketch
	require.NoError(t, decoded.UnmarshalBinary(produced))
	require.Equal(t, res.Signature, decoded.Sig)

	// 7. Finally the fixture's own decoded signature is put through canon's near-duplicate surface,
	//    so the frozen bytes are consumed as a VALUE and not only as a format. A signature is a
	//    near-duplicate of itself at Jaccard 1, and canon's unrelated document is not one of it —
	//    which is the pair of answers SP-06's store.NearDupInfo is populated from.
	same, j := canon.NearDup(fixture.Sig, fixture.Sig, opts.MinHash.NearDupThreshold)
	require.True(t, same)
	require.InDelta(t, 1.0, j, 0)

	other, jOther := canon.NearDup(res.Signature, fixture.Sig, opts.MinHash.NearDupThreshold)
	require.False(t, other,
		"unrelated documents must not pass the threshold, or the estimator says nothing")
	require.Less(t, jOther, opts.MinHash.NearDupThreshold)
}
