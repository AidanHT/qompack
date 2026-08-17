package sketchtest

import (
	"fmt"
	"testing"

	"github.com/qompack/qompack/internal/sketch"
	"github.com/stretchr/testify/require"
)

// bloomTestKeyCount is how many distinct keys runNoFalseNegativesCase adds — §5.7's "no false
// negatives over 1 000 keys". The suite's factory is expected to size its Bloom well above this so
// the fixture stays a meaningful no-false-negatives check rather than an FP-saturated one.
const bloomTestKeyCount = 1000

// bloomFillProbeCount is how many novel keys runFillRatioCase inserts while watching the fill ratio.
// It is far below bloomTestKeyCount deliberately: the case asserts a STRICT increase per novel key,
// which holds only while the filter is sparse enough that a new key's probes cannot all land on
// bits that are already set. At the suite's documented sizing the fill ratio never passes ≈ 1.5 %
// over this many inserts, so the probability that any one of them fails to set a new bit is under
// 1e-13 — and the key sequence is fixed, so the case is deterministic rather than merely unlikely
// to flake.
const bloomFillProbeCount = 200

// RunBloomSuite is the conformance suite for sketch.Bloom specifically (the Bloom-only methods
// Add/Test/Count/FillRatio/EstimatedFPRate/Capacity/ResizeTarget are not part of the generic
// sketch.Sketch interface RunSketchSuite covers). factory must return a fresh Bloom, sized for at
// least bloomTestKeyCount entries, on every call.
func RunBloomSuite(t *testing.T, name string, factory func(t *testing.T) *sketch.Bloom) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		b := factory(t)
		require.NotNil(t, b)

		// None of Bloom's own methods has an error return; any value they produce, including the
		// documented zero values, is shape-valid, so this only asserts none of them panics.
		b.Add([]byte("shape probe"))
		_ = b.Test([]byte("shape probe"))
		_ = b.Count()
		_ = b.FillRatio()
		_ = b.EstimatedFPRate()
		_, _ = b.Capacity()
		_, _, _ = b.ResizeTarget()
	})

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("no_false_negatives", func(t *testing.T) { runNoFalseNegativesCase(t, factory) })
		t.Run("count_is_distinct_inserts", func(t *testing.T) { runBloomCountCase(t, factory) })
		t.Run("fill_ratio_rises_with_novel_inserts", func(t *testing.T) { runFillRatioCase(t, factory) })
		t.Run("estimated_fp_rate_stays_a_probability", func(t *testing.T) { runFPRateRangeCase(t, factory) })
		t.Run("resize_target_is_monotone_in_fill", func(t *testing.T) { runResizeMonotoneCase(t, factory) })
		t.Run("rebuild_answers_identically", func(t *testing.T) { runRebuildEquivalenceCase(t, factory) })
	})
}

// bloomKey returns the i-th key of the suite's fixed sequence. Every case here uses the same
// sequence so that a failure in one is reproducible from another.
func bloomKey(i int) []byte { return fmt.Appendf(nil, "bloom-key-%d", i) }

// runNoFalseNegativesCase asserts a Bloom filter's core guarantee (00-ARCHITECTURE.md §5.7): Test
// must report true for every key that was ever Add-ed. A bloom filter may false-positive on a key
// that was never added, but it must never false-negative on one that was — §13 invariant 3 lets
// every consumer treat a `false` as authoritative and skip the record lookup entirely, so one false
// negative is a correctness bug in everything downstream rather than a degraded estimate.
func runNoFalseNegativesCase(t *testing.T, factory func(t *testing.T) *sketch.Bloom) {
	t.Helper()
	b := factory(t)

	for i := 0; i < bloomTestKeyCount; i++ {
		b.Add(bloomKey(i))
	}
	for i := 0; i < bloomTestKeyCount; i++ {
		k := bloomKey(i)
		require.True(t, b.Test(k),
			"Bloom must never false-negative on a key it was given to Add: %q", k)
	}
}

// runBloomCountCase asserts Count reports DISTINCT inserts: re-adding a key already present sets no
// new bit and must not move the count. The number is what §11.4's fill-ratio monitoring and §12's
// resize decision are stated against, so a Count that grew with every duplicate Add would trigger
// rebuilds of a filter that was never filling up.
func runBloomCountCase(t *testing.T, factory func(t *testing.T) *sketch.Bloom) {
	t.Helper()
	b := factory(t)
	require.Zero(t, b.Count(), "a fresh filter has counted nothing")

	for i := 0; i < bloomFillProbeCount; i++ {
		b.Add(bloomKey(i))
	}
	distinct := b.Count()
	require.Equal(t, bloomFillProbeCount, distinct,
		"Count must equal the number of distinct keys added")

	for i := 0; i < bloomFillProbeCount; i++ {
		b.Add(bloomKey(i))
	}
	require.Equal(t, distinct, b.Count(), "re-adding known keys must not move Count")
}

// runFillRatioCase asserts the fill ratio starts at zero and rises with every novel insert. It is
// the quantity §11.4 tells operators to monitor and §12's mitigation resizes on, so a fill ratio
// that failed to track the bits actually set would make both blind.
func runFillRatioCase(t *testing.T, factory func(t *testing.T) *sketch.Bloom) {
	t.Helper()
	b := factory(t)
	require.Zero(t, b.FillRatio(), "an empty filter has no bits set")

	prev := 0.0
	for i := 0; i < bloomFillProbeCount; i++ {
		b.Add(bloomKey(i))
		got := b.FillRatio()
		require.Greater(t, got, prev,
			"inserting novel key %d set no new bit: fill ratio stayed at %v", i, got)
		prev = got
	}
	require.Less(t, prev, 1.0, "a filter this sparse must not report itself full")

	// A duplicate sets no bit, so the ratio must hold still rather than drift.
	b.Add(bloomKey(0))
	require.Equal(t, prev, b.FillRatio(), "a duplicate insert must not move the fill ratio")
}

// runFPRateRangeCase asserts the estimated false-positive rate is a probability at every point of a
// filter's life. It is φ^k over the OBSERVED fill, so it is 0 on an empty filter and rises from
// there; a value outside [0, 1] would mean the estimate had stopped being one, and §11.4's "at 10 %
// the agent starts skipping viable approaches" would be compared against a number that is not a
// rate.
func runFPRateRangeCase(t *testing.T, factory func(t *testing.T) *sketch.Bloom) {
	t.Helper()
	b := factory(t)
	require.Zero(t, b.EstimatedFPRate(), "an empty filter cannot false-positive")

	for i := 0; i < bloomTestKeyCount; i++ {
		b.Add(bloomKey(i))
		got := b.EstimatedFPRate()
		require.GreaterOrEqual(t, got, 0.0, "EstimatedFPRate went negative after %d inserts", i)
		require.LessOrEqual(t, got, 1.0, "EstimatedFPRate exceeded 1 after %d inserts", i)
	}
}

// runResizeMonotoneCase asserts ResizeTarget is monotone in fill: once it asks for a rebuild it
// never un-asks, and the capacity it names never shrinks. §12's mitigation is a one-way door — the
// caller rebuilds from eliminated[] at the reported capacity — so an answer that oscillated would
// either rebuild repeatedly or, worse, report `needed` once and then deny it before the caller acted.
//
// The walk runs to the CONFIGURED capacity rather than stopping short, because the ordering is the
// substance of §12's mitigation: the fill ratio passes ResizeFillThreshold at ≈ 9 493 insertions for
// the Appendix A sizing, i.e. BEFORE the configured 10 000 is consumed. A resize signal that only
// arrived after the filter was already full would be a signal that arrived too late, and every
// elimination recorded in the meantime would have been recorded at a degraded false-positive rate.
func runResizeMonotoneCase(t *testing.T, factory func(t *testing.T) *sketch.Bloom) {
	t.Helper()
	b := factory(t)
	configured, _ := b.Capacity()

	capacity, _, needed := b.ResizeTarget()
	require.False(t, needed, "an empty filter cannot need a resize")
	require.Equal(t, configured, capacity,
		"a filter that needs no resize must report the capacity it already has")
	prevCapacity, prevNeeded := capacity, needed

	for i := 0; i < configured; i++ {
		b.Add(bloomKey(i))
		gotCapacity, _, gotNeeded := b.ResizeTarget()
		if prevNeeded {
			require.True(t, gotNeeded,
				"ResizeTarget stopped asking for a resize at insert %d after having asked", i)
		}
		require.GreaterOrEqual(t, gotCapacity, prevCapacity,
			"the resize target shrank at insert %d", i)
		prevCapacity, prevNeeded = gotCapacity, gotNeeded
	}

	require.True(t, prevNeeded,
		"the fill ratio must pass ResizeFillThreshold before the configured capacity of %d is "+
			"consumed (§12: resizing after saturation is too late); fill is %v after %d inserts",
		configured, b.FillRatio(), configured)
	require.Greater(t, prevCapacity, configured,
		"a needed resize must name a LARGER capacity than the one that filled up")
}

// runRebuildEquivalenceCase pins the equivalence §12's resize mitigation rests on: a filter rebuilt
// in one pass from the same key set must answer Test exactly as the incrementally built one. If the
// two ever disagreed, a resize would silently change what the agent believes it has already tried —
// losing negative knowledge at precisely the moment the system decided it needed more of it.
//
// The probe keys were never added, so the comparison covers the false-POSITIVE pattern as well: two
// filters agreeing only on the added keys could still differ on everything else, which would make a
// rebuild observable to its caller.
func runRebuildEquivalenceCase(t *testing.T, factory func(t *testing.T) *sketch.Bloom) {
	t.Helper()

	incremental := factory(t)
	capacity, fpRate := incremental.Capacity()
	keys := make([][]byte, bloomTestKeyCount)
	for i := range keys {
		keys[i] = bloomKey(i)
		incremental.Add(keys[i])
	}

	rebuilt := sketch.RebuildBloom(capacity, fpRate, func(yield func([]byte) bool) {
		for _, k := range keys {
			if !yield(k) {
				return
			}
		}
	})

	gotM, gotK := rebuilt.Bits()
	wantM, wantK := incremental.Bits()
	require.Equal(t, wantM, gotM, "RebuildBloom must size the filter identically")
	require.Equal(t, wantK, gotK, "RebuildBloom must probe the filter identically")

	for i := 0; i < bloomTestKeyCount; i++ {
		k := bloomKey(i)
		require.True(t, rebuilt.Test(k), "the rebuilt filter false-negated %q", k)
	}
	for i := bloomTestKeyCount; i < bloomTestKeyCount*2; i++ {
		k := bloomKey(i)
		require.Equal(t, incremental.Test(k), rebuilt.Test(k),
			"the rebuilt and incremental filters disagree on the never-added key %q", k)
	}
}
