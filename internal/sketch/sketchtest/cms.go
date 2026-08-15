package sketchtest

import (
	"testing"

	"github.com/qompack/qompack/internal/sketch"
	"github.com/stretchr/testify/require"
)

// cmsTrueCounts is the known-count fixture runEstimateAtLeastTrueCountCase adds before checking
// Estimate.
func cmsTrueCounts() map[string]uint32 {
	return map[string]uint32{"alpha": 10, "beta": 3, "gamma": 27, "delta": 1}
}

// cmsMismatchEpsilon and cmsMismatchDelta build the shape-mismatch operand. They are far from any
// plausible factory setting so that ⌈e/ε⌉ and ⌈ln(1/δ)⌉ land on a different width AND a different
// depth — a mismatch in only one of the two would still be a mismatch, but would leave the other
// half of the check untested.
const (
	cmsMismatchEpsilon = 0.45
	cmsMismatchDelta   = 0.35
)

// cmsHeavyHitterN is the n runHeavyHittersNilCase asks for. Its value is irrelevant to the
// assertion — nil in, nil out — and it is named only so the call reads as a real request rather
// than a degenerate one.
const cmsHeavyHitterN = 5

// RunCMSSuite is the conformance suite for sketch.CMS specifically (the CMS-only methods
// Add/Estimate/MergeFrom/Scale/HeavyHitters are not part of the generic sketch.Sketch interface
// RunSketchSuite covers). factory must return a fresh CMS on every call.
func RunCMSSuite(t *testing.T, name string, factory func(t *testing.T) *sketch.CMS) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		c := factory(t)
		require.NotNil(t, c)

		c.Add([]byte("shape probe"), 1)
		_ = c.Estimate([]byte("shape probe"))
		c.Scale(1)
		_ = c.HeavyHitters(sketch.NewMisraGries(1), 1)

		require.NoError(t, c.MergeFrom(factory(t)),
			"two sketches from the same factory have the same shape and must merge")
	})

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("estimate_at_least_true_count", func(t *testing.T) { runEstimateAtLeastTrueCountCase(t, factory) })
		t.Run("merge_of_a_different_shape_is_refused", func(t *testing.T) { runCMSShapeMismatchCase(t, factory) })
		t.Run("scale_one_is_identity", func(t *testing.T) { runScaleIdentityCase(t, factory) })
		t.Run("scale_zero_empties_the_table", func(t *testing.T) { runScaleZeroCase(t, factory) })
		t.Run("heavy_hitters_without_candidates_is_nil", func(t *testing.T) { runHeavyHittersNilCase(t, factory) })
	})
}

// runEstimateAtLeastTrueCountCase asserts a Count-Min sketch's core guarantee
// (00-ARCHITECTURE.md §5.7): Estimate never under-counts a key's true added count. It may
// over-count, from hash collisions with other keys, but never under-count — and that direction is
// what makes the sketch safe to act on, since the scheduler treats a high estimate as evidence to
// check while an under-count would let a hot file read as cold with nothing downstream able to tell.
func runEstimateAtLeastTrueCountCase(t *testing.T, factory func(t *testing.T) *sketch.CMS) {
	t.Helper()
	c := factory(t)

	trueCounts := cmsTrueCounts()
	for k, n := range trueCounts {
		c.Add([]byte(k), n)
	}
	for k, n := range trueCounts {
		got := c.Estimate([]byte(k))
		require.GreaterOrEqual(t, got, n, "CMS.Estimate must never under-count %q (true count %d)", k, n)
	}
}

// runCMSShapeMismatchCase asserts that merging tables of different dimensions is refused with
// ErrShapeMismatch rather than performed on the overlap. Cell i of a 2 719-column table and cell i
// of a 7-column one are the counters of different key sets, so a partial merge would produce a table
// whose every estimate looked plausible and none of which was bounded by anything.
func runCMSShapeMismatchCase(t *testing.T, factory func(t *testing.T) *sketch.CMS) {
	t.Helper()
	c := factory(t)

	other := sketch.NewCMS(cmsMismatchEpsilon, cmsMismatchDelta)
	wantW, wantD := c.Dims()
	gotW, gotD := other.Dims()
	require.False(t, wantW == gotW && wantD == gotD,
		"fixture sanity: the mismatch operand must not accidentally have the factory's shape "+
			"(%dx%d)", wantW, wantD)

	require.ErrorIs(t, c.MergeFrom(other), sketch.ErrShapeMismatch)
}

// runScaleIdentityCase asserts Scale(1) changes nothing. Phase 7's warm start (O4) merges a
// historical table and then decays it, and the decay factor is configuration: a factor of 1 means
// "do not decay", and an implementation that rounded its way through the table anyway would lose a
// count on every cell holding an odd value, silently, on every session start that chose not to decay.
func runScaleIdentityCase(t *testing.T, factory func(t *testing.T) *sketch.CMS) {
	t.Helper()
	c := factory(t)

	trueCounts := cmsTrueCounts()
	for k, n := range trueCounts {
		c.Add([]byte(k), n)
	}
	before := map[string]uint32{}
	for k := range trueCounts {
		before[k] = c.Estimate([]byte(k))
	}
	total := c.Total()

	c.Scale(1)

	for k, want := range before {
		require.Equal(t, want, c.Estimate([]byte(k)), "Scale(1) changed the estimate for %q", k)
	}
	require.Equal(t, total, c.Total(), "Scale(1) changed the stream total")
}

// runScaleZeroCase asserts Scale(0) empties the table: every estimate goes to zero and so does the
// total. It is the extreme of the same decay path, and the one a caller reaches for to forget a
// project's history entirely; a Scale(0) that left residue would make "forget" mean "mostly forget",
// which is not a state any consumer can reason about.
func runScaleZeroCase(t *testing.T, factory func(t *testing.T) *sketch.CMS) {
	t.Helper()
	c := factory(t)

	trueCounts := cmsTrueCounts()
	for k, n := range trueCounts {
		c.Add([]byte(k), n)
	}
	c.Scale(0)

	for k := range trueCounts {
		require.Zero(t, c.Estimate([]byte(k)), "Scale(0) left an estimate behind for %q", k)
	}
	require.Zero(t, c.Total(), "Scale(0) left a stream total behind")
}

// runHeavyHittersNilCase asserts HeavyHitters(nil, n) is nil. A Count-Min table stores no keys at
// all, so the Misra-Gries summary is the ONLY source of them: without a candidate set there is
// nothing to report, and returning an empty-but-non-nil slice, or worse a fabricated one, would let
// a caller believe it had asked a question this pairing can answer alone.
func runHeavyHittersNilCase(t *testing.T, factory func(t *testing.T) *sketch.CMS) {
	t.Helper()
	c := factory(t)
	for k, n := range cmsTrueCounts() {
		c.Add([]byte(k), n)
	}
	require.Nil(t, c.HeavyHitters(nil, cmsHeavyHitterN),
		"a Count-Min sketch stores no keys, so without a candidate set there is nothing to report")
}
