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

// hllMonotoneKeyCount is how far runMonotoneCase walks. It is chosen to cross the small-range
// boundary for any register count a factory is likely to use: the linear-counting correction applies
// while the raw estimate is under 2.5·m, so at Appendix C's 2 048 registers the switch happens near
// 5 120 and this count carries the walk well past it. Monotonicity across that switch is the whole
// point — inside either regime it is trivial, since registers only ever rise.
const hllMonotoneKeyCount = 7000

// hllMergeHalves is how many keys each disjoint half of runMergeIsUnionCase holds.
const hllMergeHalves = 5000

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

		require.NoError(t, h.MergeFrom(factory(t)),
			"two sketches from the same factory have the same shape and must merge")
	})

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("empty_cardinality_is_zero", func(t *testing.T) { runEmptyCardinalityCase(t, factory) })
		t.Run("cardinality_within_tolerance_on_100k_keys", func(t *testing.T) {
			runCardinalityWithinToleranceCase(t, factory)
		})
		t.Run("cardinality_is_monotone_under_add", func(t *testing.T) { runMonotoneCase(t, factory) })
		t.Run("merge_of_a_different_shape_is_refused", func(t *testing.T) { runHLLShapeMismatchCase(t, factory) })
		t.Run("merge_of_disjoint_halves_is_the_whole", func(t *testing.T) { runMergeIsUnionCase(t, factory) })
	})
}

// hllKey returns the i-th key of the suite's fixed sequence.
func hllKey(i int) []byte { return fmt.Appendf(nil, "hll-key-%d", i) }

// runEmptyCardinalityCase asserts an empty sketch reports exactly 0. Every register is zero, so the
// linear-counting branch reduces to m·ln(m/m) = 0; an estimator that reported the raw harmonic mean
// instead would answer ≈ 0.7·m for a session that has explored nothing, and §5.7's exploration
// count would start every session already wrong.
func runEmptyCardinalityCase(t *testing.T, factory func(t *testing.T) *sketch.HLL) {
	t.Helper()
	require.Zero(t, factory(t).Cardinality(), "an empty HyperLogLog has seen nothing")
}

// runCardinalityWithinToleranceCase adds hllTestKeyCount distinct keys and asserts Cardinality
// reports within hllErrorMultiplier×hllBaseErrorRate of the true count (00-ARCHITECTURE.md §5.7;
// §15 behaviour table).
func runCardinalityWithinToleranceCase(t *testing.T, factory func(t *testing.T) *sketch.HLL) {
	t.Helper()
	h := factory(t)

	for i := 0; i < hllTestKeyCount; i++ {
		h.Add(hllKey(i))
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

// runMonotoneCase asserts Cardinality never falls as keys are added. Registers only ever take a
// maximum, so the estimate is monotone inside either estimation regime; the case walks far enough to
// cross the small-range boundary, where a discontinuity between the linear-counting correction and
// the raw harmonic mean would show up as an exploration count that went DOWN while the session was
// still exploring. §5.7 reports this number to the agent, and a count that retreats reads as work
// being undone.
func runMonotoneCase(t *testing.T, factory func(t *testing.T) *sketch.HLL) {
	t.Helper()
	h := factory(t)

	prev := h.Cardinality()
	for i := 0; i < hllMonotoneKeyCount; i++ {
		h.Add(hllKey(i))
		got := h.Cardinality()
		require.GreaterOrEqual(t, got, prev,
			"Cardinality fell from %d to %d at insert %d of %d (registers = %d)",
			prev, got, i, hllMonotoneKeyCount, h.Registers())
		prev = got
	}
}

// runHLLShapeMismatchCase asserts merging sketches of different register counts is refused with
// ErrShapeMismatch. Register i of a 2 048-register sketch and register i of a 64-register one are
// fed by different slices of the hash, so a partial merge would take maxima across unrelated
// buckets and produce a cardinality bounded by nothing.
func runHLLShapeMismatchCase(t *testing.T, factory func(t *testing.T) *sketch.HLL) {
	t.Helper()
	h := factory(t)

	other := sketch.NewHLL(sketch.MinHLLRegisters)
	if other.Registers() == h.Registers() {
		other = sketch.NewHLL(sketch.MaxHLLRegisters)
	}
	require.NotEqual(t, h.Registers(), other.Registers(),
		"fixture sanity: the mismatch operand must not have the factory's register count")

	require.ErrorIs(t, h.MergeFrom(other), sketch.ErrShapeMismatch)
}

// runMergeIsUnionCase asserts the merge is EXACT, in registers rather than in estimates: merging two
// disjoint halves must produce the register array of a sketch that saw the whole key set.
//
// The comparison is over the marshalled body, which for a HyperLogLog frame IS the register array
// (header.go's layout, hll.go's body). Asserting on Cardinality instead would be a far weaker claim:
// two register arrays differing in a handful of positions produce estimates well inside the 2.3 %
// standard error, so a merge that dropped registers would pass an estimate-level check and quietly
// lose exploration history at every session boundary.
func runMergeIsUnionCase(t *testing.T, factory func(t *testing.T) *sketch.HLL) {
	t.Helper()

	left, right, whole := factory(t), factory(t), factory(t)
	for i := 0; i < hllMergeHalves; i++ {
		left.Add(hllKey(i))
		whole.Add(hllKey(i))
	}
	for i := hllMergeHalves; i < hllMergeHalves*2; i++ {
		right.Add(hllKey(i))
		whole.Add(hllKey(i))
	}

	require.NoError(t, left.MergeFrom(right))
	require.Equal(t, hllRegisterBytes(t, whole), hllRegisterBytes(t, left),
		"MergeFrom must be a register-wise maximum: the merged registers must equal the union's")
}

// hllRegisterBytes returns a sketch's register array, read back out of its own frame. The registers
// are unexported, and this suite is an external package on purpose — it is the contract every
// implementation must meet, not a window into one of them — so the frame is the sanctioned way to
// observe them.
func hllRegisterBytes(t *testing.T, h *sketch.HLL) []byte {
	t.Helper()
	frame, err := h.MarshalBinary()
	require.NoError(t, err)
	_, body, err := sketch.DecodeHeader(frame)
	require.NoError(t, err)
	require.Len(t, body, h.Registers(),
		"a HyperLogLog body is exactly its register array, one byte per register")
	return body
}
