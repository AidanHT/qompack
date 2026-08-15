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

		requireKnownError(t, c.MergeFrom(factory(t)))
	})

	if skipIfStubSketch(t, factory(t)) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("estimate_at_least_true_count", func(t *testing.T) { runEstimateAtLeastTrueCountCase(t, factory) })
	})
}

// runEstimateAtLeastTrueCountCase asserts a Count-Min sketch's core guarantee
// (00-ARCHITECTURE.md §5.7): Estimate never under-counts a key's true added count (it may
// over-count, from hash collisions with other keys, but never under-count).
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
