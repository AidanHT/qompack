package sketch

import (
	"fmt"
	"iter"
	"math"
	"math/bits"
	"slices"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// These tests are package sketch, not package sketch_test, for two reasons that both matter.
// TestBloom_ResizeCapsAtMax writes b.words directly — filling a MaxBloomCapacity filter by
// insertion would need ~16.8 M Add calls and ~20 MB of hashing to reach a branch a single loop
// over the word slice reaches in microseconds. And several tests assert on mBits/k/words, which
// are the quantities Appendix A actually specifies; observing them only through the exported
// accessors would leave the sizing arithmetic pinned one indirection away from where it is written.
//
// internal/testutil is unavailable here on purpose: it imports internal/store, which imports this
// package, so an in-package _test.go can never import it. Nothing below needs a project fixture.

// The Appendix C default pair. Every number in this file that is not derived is derived from these
// two, so a reader can recompute any expectation by hand from Appendix A's formulas.
const (
	// appendixACapacity is Appendix A's worked example n.
	appendixACapacity = 10_000
	// appendixAFPRate is Appendix A's worked example p.
	appendixAFPRate = 0.01
)

// The five sizing numbers Appendix A's worked example produces, spelled out rather than computed
// so that this test disagrees with the implementation if either drifts. Appendix A states
// "m ≈ 95_850 bits ≈ 12 KB, k = 7"; the exact ceiling is 95 851 bits, rounded up to a whole 64-bit
// word it is 95 872, and 95 872/8 = 11 984 bytes ≈ 11.7 KB.
const (
	// appendixAMRaw is ⌈−n·ln p/(ln 2)²⌉ before the word-alignment round-up.
	appendixAMRaw = 95_851
	// appendixAMBits is mRaw rounded up to a multiple of 64.
	appendixAMBits = 95_872
	// appendixAK is (m/n)·ln 2, rounded.
	appendixAK = 7
	// appendixAWords is appendixAMBits/64.
	appendixAWords = 1_498
	// appendixABodyBytes is appendixAMBits/8, the marshalled body length.
	appendixABodyBytes = 11_984
)

// TestBloom_AppendixASizing pins the worked example of 00-ARCHITECTURE.md Appendix A —
// "n = 10_000, p = 0.01 → m ≈ 95_850 bits ≈ 12 KB, k = 7" — to the exact integers this
// implementation produces, at every stage of the derivation.
//
// The pre-rounding value is asserted separately from the word-aligned one because they are two
// different decisions: 95 851 is Appendix A's formula, and 95 872 is this package's requirement
// that the bit array be a whole number of uint64 words. A test that only checked the final number
// could not tell a sizing bug from an alignment bug.
func TestBloom_AppendixASizing(t *testing.T) {
	b := NewBloom(appendixACapacity, appendixAFPRate)

	// bloomSizing is the function NewBloom itself calls, so this pins the value the implementation
	// really computed. Recomputing the formula here instead would only bracket mRaw to
	// (95 808, 95 872] — every value in that window rounds up to the same 95 872 — and 95 851 is a
	// number the subplan's Definition of Done states outright.
	mRaw, k, mBits := bloomSizing(appendixACapacity, appendixAFPRate)
	require.Equal(t, float64(appendixAMRaw), mRaw,
		"Appendix A's m = −n·ln(p)/(ln 2)² must ceil to %d", appendixAMRaw)
	require.Equal(t, appendixAK, k)
	require.Equal(t, uint64(appendixAMBits), mBits)

	gotM, gotK := b.Bits()
	require.Equal(t, uint64(appendixAMBits), gotM)
	require.Equal(t, uint8(appendixAK), gotK)
	require.Equal(t, uint64(appendixAMBits), b.mBits)
	require.Zero(t, b.mBits%64, "the bit array must be a whole number of 64-bit words")
	require.Equal(t, uint8(appendixAK), b.k)
	require.Len(t, b.words, appendixAWords)

	data, err := b.MarshalBinary()
	require.NoError(t, err)
	_, body, err := DecodeHeader(data)
	require.NoError(t, err)
	require.Len(t, body, appendixABodyBytes,
		"Appendix A's ≈12 KB is %d bytes of bit array", appendixABodyBytes)

	t.Logf("Appendix A (n=%d, p=%g): mRaw=%.0f m=%d k=%d words=%d body=%d bytes",
		appendixACapacity, appendixAFPRate, mRaw, gotM, gotK, len(b.words), len(body))
}

// TestBloom_SizingTable walks Appendix A's formula across four (n, p) pairs, so the k derivation is
// pinned at a p where it is not 7 as well as at three where it is. The word-alignment invariant is
// asserted on every row, because an m that is not a multiple of 64 would make MarshalBinary's body
// length and the decoder's m/8 expectation disagree.
func TestBloom_SizingTable(t *testing.T) {
	for _, tc := range []struct {
		name     string
		capacity int
		fpRate   float64
		wantK    uint8
	}{
		{"n=100 p=0.01", 100, 0.01, 7},
		{"n=1000 p=0.001", 1_000, 0.001, 10},
		{"n=10000 p=0.01", appendixACapacity, appendixAFPRate, appendixAK},
		{"n=100000 p=0.01", 100_000, 0.01, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBloom(tc.capacity, tc.fpRate)
			m, k := b.Bits()
			require.Equal(t, tc.wantK, k)
			require.Equal(t, tc.wantK, b.k)
			require.Zero(t, m%64, "m must be a multiple of 64 bits")
			require.Equal(t, int(m/64), len(b.words))
		})
	}
}

// TestBloom_CappedBitsRederiveK pins the third clamp, the one Capacity() cannot surface.
//
// bloomSizing derives k from the UNCAPPED m and then holds m itself inside MaxBloomBits (the
// ceiling rule in errors.go). Above capacity ≈ 9.34 M at p = 1e-6 the cap binds, and a k left over
// from the m that was asked for is then wrong in the expensive direction: k = (m/n)·ln 2 minimises
// the false-positive rate at a GIVEN m, so over-probing a filter that is now 1.80x smaller
// (4.82e8 bits requested, 2^28 allocated) raises the rate as well as the cost. The pair really is reachable — MaxBloomCapacity and
// Appendix A's tightest rate are both legal configuration values — which is why this is a
// re-derivation rather than a note in a comment.
//
// The uncapped rows in TestBloom_SizingTable are what pins the other half: the re-derivation must
// not move any filter whose m was never capped.
func TestBloom_CappedBitsRederiveK(t *testing.T) {
	const tightFPRate = 1e-6
	b := NewBloom(MaxBloomCapacity, tightFPRate)

	m, k := b.Bits()
	mRaw, uncappedK, _ := bloomSizing(MaxBloomCapacity, tightFPRate)
	t.Logf("n=%d p=%g: mRaw=%.0f capped m=%d (m/n=%.1f) k=%d",
		MaxBloomCapacity, tightFPRate, mRaw, m, float64(m)/float64(MaxBloomCapacity), k)

	require.Equal(t, MaxBloomBits, m, "fixture sanity: this configuration must actually hit the cap")
	require.Greater(t, mRaw, float64(MaxBloomBits), "and must have asked for more than the cap allows")
	require.Equal(t, uint8(11), k,
		"k must be re-derived from the capped m: m/n = 16, so (m/n)·ln2 = 11.09 → 11")
	require.Equal(t, 11, uncappedK,
		"bloomSizing must return the same k it stored, so no caller can read the pre-cap value")

	// Capacity() still reports the configuration, which the cap has made unachievable. That is the
	// documented behaviour rather than a second defect: the honest number is EstimatedFPRate, which
	// is computed from the bits actually set.
	n, fp := b.Capacity()
	require.Equal(t, MaxBloomCapacity, n)
	require.InDelta(t, tightFPRate, fp, 0)
	require.Zero(t, b.EstimatedFPRate(), "an empty filter's observed rate is 0 whatever it was sized for")
}

// TestBloom_NoFalseNegatives asserts the one guarantee a Bloom filter is not allowed to break
// (00-ARCHITECTURE.md §5.7): Test may report true for a key that was never added, but it must
// never report false for a key that was. A false negative here would make §8.3's negative
// knowledge silently re-propose an approach the agent has already eliminated.
func TestBloom_NoFalseNegatives(t *testing.T) {
	b := NewBloom(appendixACapacity, appendixAFPRate)
	for i := 0; i < appendixACapacity; i++ {
		b.Add(bloomKey("key-", i))
	}
	for i := 0; i < appendixACapacity; i++ {
		k := bloomKey("key-", i)
		require.True(t, b.Test(k), "false negative on %q", k)
	}
}

// TestBloom_EmptyFilterTestsFalse asserts that a filter nothing has been added to answers false to
// everything and reports itself empty on every axis. §13 invariant 3 makes false the only safe
// answer a membership sketch may give without evidence.
func TestBloom_EmptyFilterTestsFalse(t *testing.T) {
	b := NewBloom(1_000, appendixAFPRate)
	require.False(t, b.Test([]byte("x")))
	require.Equal(t, 0, b.Count())
	require.Equal(t, 0.0, b.FillRatio())
	require.Equal(t, 0.0, b.EstimatedFPRate())
	require.False(t, b.Saturated())

	// The zero Bloom is a different animal from an empty one: it has no bit array at all, and it
	// is reachable from outside this package as `var b sketch.Bloom` because every field is
	// unexported. Without the guards in Add/Test/fillRatioOf/estFPRateOf it would divide by zero
	// in idx % b.mBits — a panic, which §12.3 forbids on the observer hot path. These branches are
	// defensive, so they are asserted rather than left to rot: untested defensive code is a
	// comment, not a guarantee.
	t.Run("zero_value_is_inert", func(t *testing.T) {
		var z Bloom

		z.Add([]byte("x"))
		require.False(t, z.Test([]byte("x")), "an unsized filter holds nothing, so false is exact")
		require.Equal(t, 0, z.Count())
		m, k := z.Bits()
		require.Zero(t, m)
		require.Zero(t, k)
		require.Equal(t, 0.0, z.FillRatio(), "0/0 must not surface as NaN")
		require.Equal(t, 0.0, z.EstimatedFPRate(), "math.Pow(0, 0) is 1; k == 0 must not claim 100%")
		require.False(t, z.Saturated())
		require.Equal(t, BloomStats{}, z.Stats())

		capacity, fp, needed := z.ResizeTarget()
		require.Equal(t, 0, capacity)
		require.Equal(t, 0.0, fp)
		require.False(t, needed)

		// The ceiling rule (errors.go) is "every constructible sketch is also marshallable", and
		// marshallable means re-readable. A zero Bloom would encode m = 0 and k = 0, which
		// decodeV1 refuses forever, so writing that frame would put a file on disk that this
		// build's own decoder is guaranteed to reject.
		_, err := z.MarshalBinary()
		require.ErrorIs(t, err, ErrMalformed,
			"an unsized filter must refuse to marshal rather than emit an unreadable frame")
	})
}

// TestBloom_CountIsDistinctInserts pins Count's documented meaning: the number of Add calls that
// flipped at least one bit, not the number of Add calls. Re-adding a key already present is not a
// new item, and a Count that grew on every call would make FillRatio-versus-Count reasoning — and
// therefore the resize decision — wrong for any workload that repeats keys, which §8.3's
// elimination stream certainly does.
func TestBloom_CountIsDistinctInserts(t *testing.T) {
	b := NewBloom(1_000, appendixAFPRate)
	b.Add([]byte("a"))
	b.Add([]byte("a"))
	b.Add([]byte("a"))
	b.Add([]byte("b"))
	require.Equal(t, 2, b.Count())
}

// bloomAtCapacity returns a (10 000, 0.01) filter holding exactly 10 000 distinct keys — the state
// Appendix A sizes for. Two tests need it and building it costs 10 000 SHA-256 calls.
func bloomAtCapacity(t *testing.T) *Bloom {
	t.Helper()
	b := NewBloom(appendixACapacity, appendixAFPRate)
	for i := 0; i < appendixACapacity; i++ {
		b.Add(bloomKey("key-", i))
	}
	return b
}

// TestBloom_FillRatioAtCapacity checks the observed fill against the analytic prediction
// 1 − e^(−kn/m) = 1 − e^(−7·10 000/95 872) = 0.5182. The band is deliberately wider than the
// hashing noise: what is being asserted is that the filter is sized right, not that SHA-256 is
// uniform to four decimal places.
func TestBloom_FillRatioAtCapacity(t *testing.T) {
	b := bloomAtCapacity(t)
	fill := b.FillRatio()
	require.GreaterOrEqual(t, fill, 0.51)
	require.LessOrEqual(t, fill, 0.53)
	t.Logf("FillRatio at capacity = %.6f (analytic 1-e^(-7*10000/95872) = %.6f)",
		fill, 1-math.Exp(-appendixAK*float64(appendixACapacity)/appendixAMBits))
}

// TestBloom_EstimatedFPRateMatchesEmpirical is the test that makes EstimatedFPRate worth having:
// it measures the real false-positive rate over 100 000 keys that were never inserted and requires
// the estimate derived from the OBSERVED fill ratio to agree with it. §11.4 asks the operator to
// act on this number ("at 10 % the agent starts skipping viable approaches"), so an estimate that
// did not track reality would be worse than none.
func TestBloom_EstimatedFPRateMatchesEmpirical(t *testing.T) {
	const probes = 100_000
	b := bloomAtCapacity(t)

	fp := 0
	for i := 0; i < probes; i++ {
		if b.Test(bloomKey("probe-", i)) {
			fp++
		}
	}
	empirical := float64(fp) / float64(probes)
	est := b.EstimatedFPRate()
	t.Logf("empirical FP over %d never-inserted probes = %.6f; EstimatedFPRate() = %.6f; |Δ| = %.6f",
		probes, empirical, est, math.Abs(est-empirical))

	require.GreaterOrEqual(t, empirical, 0.008)
	require.LessOrEqual(t, empirical, 0.013)
	require.GreaterOrEqual(t, est, 0.009)
	require.LessOrEqual(t, est, 0.012)
	require.LessOrEqual(t, math.Abs(est-empirical), 0.004)
}

// TestBloom_ResizeFiresBeforeCapacity pins the §12 mitigation ("Monitor fill ratio; resize with a
// rebuild from eliminated[] in checkpoints"). The crossing is analytic: FillRatio reaches
// ResizeFillThreshold = 0.5 at n = −m·ln(0.5)/k = −95 872·ln(0.5)/7 ≈ 9 493 insertions, i.e.
// BEFORE the configured capacity of 10 000 is consumed. That ordering is the whole point —
// resizing only once the filter is already saturated is too late, because every elimination added
// in the meantime was recorded at a degraded false-positive rate.
func TestBloom_ResizeFiresBeforeCapacity(t *testing.T) {
	b := NewBloom(appendixACapacity, appendixAFPRate)

	for i := 0; i < 5_000; i++ {
		b.Add(bloomKey("key-", i))
	}
	capacity, fp, needed := b.ResizeTarget()
	require.Equal(t, appendixACapacity, capacity)
	require.Equal(t, appendixAFPRate, fp)
	require.False(t, needed, "at half capacity the fill ratio is ~0.31, well under the 0.5 threshold")

	// The analytic crossing. Either side of it is legitimate — hashing noise moves the exact
	// insertion by a handful — so this only records where it happened.
	crossing := -1
	for i := 5_000; i < appendixACapacity; i++ {
		b.Add(bloomKey("key-", i))
		if _, _, n := b.ResizeTarget(); n && crossing < 0 {
			crossing = i + 1
		}
	}
	t.Logf("ResizeTarget first reported needed=true at %d insertions (analytic crossing n = %.0f)",
		crossing, -appendixAMBits*math.Log(1-ResizeFillThreshold)/appendixAK)
	require.Positive(t, crossing, "the fill ratio never crossed 0.5 within the configured capacity")
	require.Less(t, crossing, appendixACapacity,
		"§12: the resize signal must fire BEFORE the configured capacity is consumed")

	capacity, fp, needed = b.ResizeTarget()
	require.Equal(t, appendixACapacity*ResizeGrowthFactor, capacity)
	require.Equal(t, appendixAFPRate, fp)
	require.True(t, needed)
}

// TestBloom_ResizeCapsAtMax asserts the doubling is capped rather than applied once capacity is
// already at the ceiling: a filter that answered "resize to 2·MaxBloomCapacity" would send its
// caller straight into a constructor that clamps back, i.e. into an infinite rebuild loop.
//
// The words are written directly rather than filled by insertion. This test is package sketch for
// exactly this: filling a MaxBloomCapacity filter would need ~16.8 M Add calls and ~20 MB of
// hashing, where one loop over the word slice reaches the branch under test in microseconds.
func TestBloom_ResizeCapsAtMax(t *testing.T) {
	b := NewBloom(MaxBloomCapacity, appendixAFPRate)
	for i := range b.words {
		b.words[i] = ^uint64(0)
	}
	require.Equal(t, 1.0, b.FillRatio())

	capacity, fp, needed := b.ResizeTarget()
	require.True(t, needed)
	require.Equal(t, MaxBloomCapacity, capacity, "the doubling must be capped, not applied")
	require.Equal(t, appendixAFPRate, fp)
	require.True(t, b.Saturated())
}

// TestBloom_SaturatedThreshold pins Saturated to §11.4's watch-for line: "At 1 % they are safe; at
// 10 % the agent starts skipping viable approaches." The crossing in fill-ratio terms is
// φ⁷ = 0.10, i.e. φ = 0.7197: 0.72⁷ = 0.1003 is saturated and 0.70⁷ = 0.0824 is not. Inverting
// n = −m·ln(1−φ)/k puts φ = 0.72 at ≈ 17 434 insertions into a filter sized for 10 000 — which is
// what "the filter is past its design point" looks like from the inside.
func TestBloom_SaturatedThreshold(t *testing.T) {
	require.Equal(t, 0.10, FPWarnRate)
	require.Greater(t, math.Pow(0.72, appendixAK), FPWarnRate)
	require.Less(t, math.Pow(0.70, appendixAK), FPWarnRate)

	over := NewBloom(appendixACapacity, appendixAFPRate)
	inserts := 0
	for over.FillRatio() < 0.72 {
		over.Add(bloomKey("sat-", inserts))
		inserts++
	}
	t.Logf("fill reached %.6f after %d inserts (analytic n = %.0f)", over.FillRatio(), inserts,
		-appendixAMBits*math.Log(1-0.72)/appendixAK)
	require.True(t, over.Saturated(), "φ=%.4f, φ^k=%.6f must be >= FPWarnRate",
		over.FillRatio(), over.EstimatedFPRate())

	under := NewBloom(appendixACapacity, appendixAFPRate)
	for i := 0; under.FillRatio() < 0.695; i++ {
		under.Add(bloomKey("sat-", i))
	}
	require.LessOrEqual(t, under.FillRatio(), 0.705,
		"each insert moves the fill by at most k/m, so the band cannot be stepped over")
	require.False(t, under.Saturated(), "φ=%.4f, φ^k=%.6f must be < FPWarnRate",
		under.FillRatio(), under.EstimatedFPRate())
}

// TestBloom_RebuildFromIterator covers §8.3's rebuild path: when a dependency hash changes, the
// eliminations that depended on it go stale and tried.bloom is rebuilt from the ACTIVE records
// only. Rebuilding into a larger capacity is the resize half of §12's mitigation, and the fill
// comparison at the end is what proves the larger filter really is the less saturated one rather
// than merely a differently-shaped one.
func TestBloom_RebuildFromIterator(t *testing.T) {
	const keyCount = 3_000
	keys := make([][]byte, keyCount)
	for i := range keys {
		keys[i] = bloomKey("rebuild-", i)
	}

	big := RebuildBloom(2*appendixACapacity, appendixAFPRate, slices.Values(keys))
	require.NotNil(t, big)
	for _, k := range keys {
		require.True(t, big.Test(k), "rebuild dropped %q", k)
	}
	require.Equal(t, keyCount, big.Count())

	capacity, fp := big.Capacity()
	require.Equal(t, 2*appendixACapacity, capacity)
	require.Equal(t, appendixAFPRate, fp)

	small := RebuildBloom(appendixACapacity, appendixAFPRate, slices.Values(keys))
	require.Less(t, big.FillRatio(), small.FillRatio(),
		"the same keys in twice the bits must leave the filter less full")
}

// TestBloom_RebuildEmptyIterator asserts the degenerate rebuild — every elimination went stale —
// produces a usable empty filter rather than a nil one. That case is reachable in production the
// first time a project's whole dependency set changes, and a nil return there would panic the
// idle-window rebuild rather than simply forgetting.
func TestBloom_RebuildEmptyIterator(t *testing.T) {
	var empty iter.Seq[[]byte] = func(func([]byte) bool) {}
	b := RebuildBloom(1_000, appendixAFPRate, empty)
	require.NotNil(t, b)
	require.Equal(t, 0, b.Count())

	data, err := b.MarshalBinary()
	require.NoError(t, err)
	got := NewBloom(1, 0.5)
	require.NoError(t, got.UnmarshalBinary(data))
	require.Equal(t, b.Header(), got.Header())
}

// TestBloom_StatsConsistent asserts Stats is a snapshot of the same numbers the individual
// accessors report, not a second, drifting derivation of them. Stats exists so an observability
// caller can read every figure from ONE pass over the words; that optimisation is only safe if it
// is indistinguishable from calling the accessors one by one.
func TestBloom_StatsConsistent(t *testing.T) {
	b := NewBloom(appendixACapacity, appendixAFPRate)
	for i := 0; i < 5_000; i++ {
		b.Add(bloomKey("stats-", i))
	}

	var popcount uint64
	for _, w := range b.words {
		popcount += uint64(bits.OnesCount64(w))
	}

	m, k := b.Bits()

	s := b.Stats()
	require.Equal(t, popcount, s.SetBits)
	require.Equal(t, b.FillRatio(), s.FillRatio)
	require.Equal(t, b.EstimatedFPRate(), s.EstFPRate)
	require.Equal(t, b.Saturated(), s.Saturated)
	require.Equal(t, m, s.MBits)
	require.Equal(t, k, s.K)
	require.Equal(t, uint8(appendixAK), s.K)
	require.Equal(t, b.Count(), s.Count, "the struct and the accessor must agree, both int")

	_, fp, needed := b.ResizeTarget()
	require.Equal(t, needed, s.NeedsResize)
	require.Equal(t, fp, s.FPRate)
	require.Equal(t, appendixACapacity, s.Capacity)
}

// TestBloom_ClampsIllegalArgs pins the package's failure-mode rule (util.go, §12.3): a constructor
// never panics and never returns an error, it clamps to the nearest legal value. NaN is the case
// that matters most — uint64(math.NaN()) is implementation-defined in Go, so a NaN that reached
// the make() length would be a real crash on the observer hot path.
//
// Every clamped filter is then exercised end to end, because "did not panic" is not the same
// promise as "produced a filter that works".
func TestBloom_ClampsIllegalArgs(t *testing.T) {
	for _, tc := range []struct {
		name         string
		capacity     int
		fpRate       float64
		wantCapacity int
		wantFPRate   float64
	}{
		{"negative capacity, zero rate", -5, 0, 1, 1e-6},
		{"zero capacity, rate above 1", 0, 2, 1, 0.5},
		{"NaN rate clamps to the floor, never into make()", 1, math.NaN(), 1, 1e-6},
		{"+Inf rate", 1, math.Inf(1), 1, 0.5},
		{"-Inf rate", 1, math.Inf(-1), 1, 1e-6},
		{"capacity above the ceiling", math.MaxInt, 0.01, MaxBloomCapacity, 0.01},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBloom(tc.capacity, tc.fpRate)
			require.NotNil(t, b)

			capacity, fp := b.Capacity()
			require.Equal(t, tc.wantCapacity, capacity)
			require.Equal(t, tc.wantFPRate, fp)

			m, k := b.Bits()
			require.Positive(t, m)
			require.Zero(t, m%64)
			require.GreaterOrEqual(t, k, uint8(1))
			require.Equal(t, b.k, k)

			b.Add([]byte("clamped"))
			require.True(t, b.Test([]byte("clamped")))

			data, err := b.MarshalBinary()
			require.NoError(t, err)
			got := NewBloom(1, 0.5)
			require.NoError(t, got.UnmarshalBinary(data))
			require.Equal(t, b.Header(), got.Header())
		})
	}
}

// TestBloom_MarshalRoundTrip is the §6.2 promise in one test: a sketch that cannot be re-read is
// worse than no sketch. Re-marshalling must be BYTE-identical, because the golden fixtures and the
// content-addressed store both depend on a given logical state having exactly one encoding — Go
// randomizes map iteration, so the params sort in EncodeHeader is what makes that true.
func TestBloom_MarshalRoundTrip(t *testing.T) {
	const created = core.UnixMilli(1_700_000_000_000)

	b := NewBloom(appendixACapacity, appendixAFPRate)
	b.SetCreated(created)
	for i := 0; i < appendixACapacity; i++ {
		b.Add(bloomKey("key-", i))
	}

	data, err := b.MarshalBinary()
	require.NoError(t, err)

	got := NewBloom(1, 0.5)
	require.NoError(t, got.UnmarshalBinary(data))

	again, err := got.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, data, again, "re-marshalling a decoded filter must be byte-identical")

	wantM, wantK := b.Bits()
	gotM, gotK := got.Bits()
	require.Equal(t, b.Count(), got.Count())
	require.Equal(t, wantM, gotM)
	require.Equal(t, wantK, gotK)
	require.Equal(t, b.k, got.k)
	require.Equal(t, created, got.Header().Created)
	require.Equal(t, b.Header(), got.Header())

	for i := 0; i < appendixACapacity; i++ {
		require.True(t, got.Test(bloomKey("key-", i)), "decoded filter lost key-%d", i)
	}
	for i := 0; i < 1_000; i++ {
		k := bloomKey("absent-", i)
		require.Equal(t, b.Test(k), got.Test(k), "decoded filter disagrees on %q", k)
	}
}

// TestBloom_UnmarshalRejects walks every way a well-framed QPKS blob can still describe a filter
// this build must refuse to build. The frames are assembled through EncodeHeader, so each one has
// a correct CRC and a self-consistent length block: the ONLY thing wrong with them is the
// Bloom-specific claim named in the row, which is what makes each row isolate one check.
//
// The distinction between the two sentinels is the one errors.go draws. A param that is out of
// range or not an integer is ErrMalformed — the frame is structurally intact and says something
// impossible. A body whose length disagrees with the declared m is ErrTruncated — the declared
// lengths do not add up, which is the interrupted-write signature.
func TestBloom_UnmarshalRejects(t *testing.T) {
	body := make([]byte, appendixABodyBytes)

	for _, tc := range []struct {
		name     string
		capacity float64
		fpRate   float64
		k        float64
		m        float64
		body     []byte
		wantErr  error
	}{
		{"body shorter than m/8", appendixACapacity, appendixAFPRate, appendixAK, appendixAMBits, body[:len(body)-1], ErrTruncated},
		{"body longer than m/8", appendixACapacity, appendixAFPRate, appendixAK, appendixAMBits, append(append([]byte(nil), body...), 0), ErrTruncated},
		{"m not a multiple of 64", appendixACapacity, appendixAFPRate, appendixAK, appendixAMBits + 1, body, ErrMalformed},
		{"m above MaxBloomBits", appendixACapacity, appendixAFPRate, appendixAK, float64(MaxBloomBits) + 64, body, ErrMalformed},
		{"m zero", appendixACapacity, appendixAFPRate, appendixAK, 0, nil, ErrMalformed},
		{"k zero", appendixACapacity, appendixAFPRate, 0, appendixAMBits, body, ErrMalformed},
		{"k above 64", appendixACapacity, appendixAFPRate, 65, appendixAMBits, body, ErrMalformed},
		{"capacity zero", 0, appendixAFPRate, appendixAK, appendixAMBits, body, ErrMalformed},
		{"fprate 1", appendixACapacity, 1, appendixAK, appendixAMBits, body, ErrMalformed},
		{"fprate 0", appendixACapacity, 0, appendixAK, appendixAMBits, body, ErrMalformed},
		{"fprate NaN", appendixACapacity, math.NaN(), appendixAK, appendixAMBits, body, ErrMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame, err := EncodeHeader(Header{
				Ver:  FormatVersion,
				Kind: KindBloom,
				Params: map[string]float64{
					"capacity": tc.capacity,
					"fprate":   tc.fpRate,
					"k":        tc.k,
					"m":        tc.m,
				},
			}, tc.body)
			require.NoError(t, err, "fixture sanity: the frame itself must be well-formed")

			b := NewBloom(appendixACapacity, appendixAFPRate)
			require.ErrorIs(t, b.UnmarshalBinary(frame), tc.wantErr)
		})
	}

	// A nil receiver reports rather than panics: §12.3, a hook that dies takes observability with
	// it. The same rule covers MarshalBinary, whose only other option would be a nil dereference.
	var nilBloom *Bloom
	require.ErrorIs(t, nilBloom.UnmarshalBinary(nil), ErrMalformed)
	_, err := nilBloom.MarshalBinary()
	require.ErrorIs(t, err, ErrMalformed)
}

// TestBloom_UnmarshalKindMismatch asserts a Count-Min frame loaded into a *Bloom is refused by
// kind, not misread as a Bloom whose body happens to be the right length. Without the check the
// daemon would answer membership questions out of a frequency table's counters.
//
// The frame is built through EncodeHeader rather than through CMS.MarshalBinary, which is still a
// stub until commit 3 — EncodeHeader is the same writer CMS will use, so these are the bytes it
// will emit.
func TestBloom_UnmarshalKindMismatch(t *testing.T) {
	frame, err := EncodeHeader(Header{
		Ver:    FormatVersion,
		Kind:   KindCMS,
		Params: map[string]float64{"delta": 0.01, "depth": 5, "epsilon": 0.001, "width": 2_719},
	}, make([]byte, 64))
	require.NoError(t, err)

	b := NewBloom(appendixACapacity, appendixAFPRate)
	require.ErrorIs(t, b.UnmarshalBinary(frame), ErrKindMismatch)
}

// bloomKey builds the test keys. It is a function rather than an inline Sprintf so that every test
// in this file provably hashes the SAME bytes for a given (prefix, i) — the empirical
// false-positive measurement is only meaningful if the probe keys really are disjoint from the
// inserted ones.
func bloomKey(prefix string, i int) []byte {
	return []byte(fmt.Sprintf("%s%d", prefix, i))
}
