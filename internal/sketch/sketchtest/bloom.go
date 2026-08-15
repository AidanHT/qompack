package sketchtest

import (
	"fmt"
	"testing"

	"github.com/qompack/qompack/internal/sketch"
	"github.com/stretchr/testify/require"
)

// bloomTestKeyCount is how many distinct keys runNoFalseNegativesCase adds; the suite's factory
// is expected to size its Bloom well above this so the fixture stays a meaningful no-false-
// negatives check rather than an FP-saturated one.
const bloomTestKeyCount = 500

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

	if skipIfStubSketch(t, factory(t)) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("no_false_negatives", func(t *testing.T) { runNoFalseNegativesCase(t, factory) })
	})
}

// runNoFalseNegativesCase asserts a Bloom filter's core guarantee (00-ARCHITECTURE.md §5.7): Test
// must report true for every key that was ever Add-ed. A bloom filter may false-positive on a key
// that was never added, but it must never false-negative on one that was.
func runNoFalseNegativesCase(t *testing.T, factory func(t *testing.T) *sketch.Bloom) {
	t.Helper()
	b := factory(t)

	keys := make([][]byte, bloomTestKeyCount)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("bloom-key-%d", i))
	}
	for _, k := range keys {
		b.Add(k)
	}
	for _, k := range keys {
		require.True(t, b.Test(k), "Bloom must never false-negative on a key it was given to Add: %q", k)
	}
}
