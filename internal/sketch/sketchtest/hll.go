package sketchtest

import (
	"fmt"
	"math"
	"testing"

	"github.com/qompack/qompack/internal/sketch"
	"github.com/stretchr/testify/require"
)

// hllTestKeyCount is "100k keys" from the §15 behaviour table.
const hllTestKeyCount = 100000

// hllErrorMultiplier and hllBaseErrorRate together express the §15 behaviour table's tolerance
// ("within 3×2.3%"): HyperLogLog's standard error is documented at roughly 2.3%, and the
// tolerance this suite allows is 3 times that.
const (
	hllErrorMultiplier = 3
	hllBaseErrorRate   = 0.023
)

// RunHLLSuite is the conformance suite for sketch.HLL specifically (the HLL-only methods
// Add/Cardinality/MergeFrom are not part of the generic sketch.Sketch interface RunSketchSuite
// covers). factory must return a fresh HLL on every call.
func RunHLLSuite(t *testing.T, name string, factory func(t *testing.T) *sketch.HLL) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		h := factory(t)
		require.NotNil(t, h)

		h.Add([]byte("shape probe"))
		_ = h.Cardinality()

		requireKnownError(t, h.MergeFrom(factory(t)))
	})

	if skipIfStubSketch(t, factory(t)) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("cardinality_within_tolerance_on_100k_keys", func(t *testing.T) {
			runCardinalityWithinToleranceCase(t, factory)
		})
	})
}

// runCardinalityWithinToleranceCase adds hllTestKeyCount distinct keys and asserts Cardinality
// reports within hllErrorMultiplier×hllBaseErrorRate of the true count (00-ARCHITECTURE.md §5.7;
// §15 behaviour table).
func runCardinalityWithinToleranceCase(t *testing.T, factory func(t *testing.T) *sketch.HLL) {
	t.Helper()
	h := factory(t)

	for i := 0; i < hllTestKeyCount; i++ {
		h.Add([]byte(fmt.Sprintf("hll-key-%d", i)))
	}

	got := h.Cardinality()
	want := uint64(hllTestKeyCount)
	tolerance := uint64(math.Round(float64(want) * hllErrorMultiplier * hllBaseErrorRate))

	var diff uint64
	if got > want {
		diff = got - want
	} else {
		diff = want - got
	}
	require.LessOrEqual(t, diff, tolerance,
		"Cardinality()=%d must be within %d of the true count %d", got, tolerance, want)
}
