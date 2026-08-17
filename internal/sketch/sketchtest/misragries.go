package sketchtest

import (
	"fmt"
	"testing"

	"github.com/qompack/qompack/internal/sketch"
	"github.com/stretchr/testify/require"
)

// misraGriesAddedKeys is the fixture runNoFalsePositivesCase adds before checking Top.
func misraGriesAddedKeys() map[string]int {
	return map[string]int{"alpha": 50, "beta": 30, "gamma": 20, "delta": 5}
}

// The shape of the stream the bound cases run against.
//
// The alphabet is deliberately larger than any k a factory is likely to use, so the stream
// OVERFLOWS the counter table and the decrement phase actually runs — a stream of fewer distinct
// keys than counters would make every count exact and every bound below trivially true.
//
// The two hot keys carry mgHotWeight on most rounds against a tail of weight-1 touches, which puts
// them at roughly 65 % and 33 % of the stream mass. That skew is not decoration: the retention
// guarantee only says anything about keys above total/(k+1), and a stream spread evenly over 97 keys
// puts every one of them below that threshold at any realistic k — the guarantee would then be
// vacuous and runRetentionCase would pass without testing it.
const (
	mgStreamRounds   = 2000
	mgStreamAlphabet = 97
	mgHotWeight      = 32
)

// RunMisraGriesSuite is the conformance suite for sketch.MisraGries specifically (the
// MisraGries-only methods Add/Top/MergeFrom/MaxError are not part of the generic sketch.Sketch
// interface RunSketchSuite covers). factory must return a fresh MisraGries on every call.
func RunMisraGriesSuite(t *testing.T, name string, factory func(t *testing.T) *sketch.MisraGries) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		m := factory(t)
		require.NotNil(t, m)

		m.Add("shape probe", 1)
		_ = m.Top(1)

		require.NoError(t, m.MergeFrom(factory(t)),
			"two summaries from the same factory have the same k and must merge")
	})

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("no_false_positives", func(t *testing.T) { runNoFalsePositivesCase(t, factory) })
		t.Run("counts_are_lower_bounds", func(t *testing.T) { runLowerBoundCase(t, factory) })
		t.Run("frequent_items_are_retained", func(t *testing.T) { runRetentionCase(t, factory) })
		t.Run("merge_of_a_different_k_is_refused", func(t *testing.T) { runMGShapeMismatchCase(t, factory) })
	})
}

// mgStream feeds a skewed, table-overflowing stream into m and returns the true counts. Two hot keys
// carry most of the mass while a long tail of one-off touches keeps evicting them — the shape §5.7's
// top-k summary exists for, and the only shape in which the retention threshold has anything to say.
// The hot keys are interleaved with the tail rather than front-loaded, so they are subject to the
// decrement phase throughout rather than being admitted once into an empty table.
func mgStream(m *sketch.MisraGries) map[string]int {
	truth := make(map[string]int, mgStreamAlphabet+2)
	add := func(key string, weight int) {
		m.Add(key, weight)
		truth[key] += weight
	}
	for i := 0; i < mgStreamRounds; i++ {
		add("mg-hot-0", mgHotWeight)
		if i%2 == 0 {
			add("mg-hot-1", mgHotWeight)
		}
		add(fmt.Sprintf("mg-key-%d", i%mgStreamAlphabet), 1)
	}
	return truth
}

// runNoFalsePositivesCase asserts Misra-Gries's core guarantee (00-ARCHITECTURE.md §5.7): every key
// Top returns really was Added at some point. Misra-Gries may under-report a true heavy hitter's
// count, or omit one entirely (a false negative), but by construction it never invents a key that
// was never seen — keys are stored verbatim, and nothing but Add and UnmarshalBinary ever writes one
// into the table.
func runNoFalsePositivesCase(t *testing.T, factory func(t *testing.T) *sketch.MisraGries) {
	t.Helper()
	m := factory(t)

	added := misraGriesAddedKeys()
	for k, n := range added {
		m.Add(k, n)
	}

	top := m.Top(len(added))
	require.NotEmpty(t, top, "fixture sanity: Top must report something for a clearly skewed key distribution")
	for _, c := range top {
		_, wasAdded := added[c.Key]
		require.True(t, wasAdded, "Top must never report a key that was never Added (no false positives, by construction): %q", c.Key)
	}
}

// runLowerBoundCase asserts the two inequalities every reported count sits between:
// 0 < reported(x) ≤ true(x) always, and true(x) − MaxError() ≤ reported(x) for any stream below the
// saturation ceiling — which this one is by three orders of magnitude. It also pins MaxError itself
// against total/(k+1), the inequality the retention guarantee is derived from, so that
// runRetentionCase is a consequence rather than a coincidence.
func runLowerBoundCase(t *testing.T, factory func(t *testing.T) *sketch.MisraGries) {
	t.Helper()
	m := factory(t)
	truth := mgStream(m)

	require.LessOrEqual(t, m.MaxError(), m.Total()/int64(m.K()+1),
		"the accumulated decrement must stay within total/(k+1)")

	top := m.Top(0)
	require.LessOrEqual(t, len(top), m.K(), "the summary must never hold more than k counters")
	for _, c := range top {
		trueCount, wasAdded := truth[c.Key]
		require.True(t, wasAdded, "Top reported %q, which was never Added", c.Key)
		require.Positive(t, c.Count, "Top reported %q with a non-positive count", c.Key)
		require.LessOrEqual(t, c.Count, trueCount,
			"Top over-reported %q: %d exceeds the true count %d", c.Key, c.Count, trueCount)
		require.LessOrEqual(t, int64(trueCount-c.Count), m.MaxError(),
			"Top under-reported %q by %d, more than MaxError() = %d",
			c.Key, trueCount-c.Count, m.MaxError())
	}
}

// runRetentionCase asserts the other side of the contract: absence is not evidence in general, but
// it IS evidence above the retention threshold. Every key whose true count exceeds total/(k+1) must
// survive in Top(k), because err ≤ total/(k+1) and a reported count is at least true − err. SP-08's
// "hottest files" list depends on exactly this — without it a genuinely hot path could be evicted by
// a long tail of one-off touches and never reappear.
func runRetentionCase(t *testing.T, factory func(t *testing.T) *sketch.MisraGries) {
	t.Helper()
	m := factory(t)
	truth := mgStream(m)

	reported := make(map[string]int, m.K())
	for _, c := range m.Top(m.K()) {
		reported[c.Key] = c.Count
	}

	threshold := m.Total() / int64(m.K()+1)
	kept := 0
	for key, trueCount := range truth {
		if int64(trueCount) <= threshold {
			continue
		}
		kept++
		_, ok := reported[key]
		require.True(t, ok,
			"%q has a true count of %d, above the retention threshold total/(k+1) = %d, "+
				"but Top(%d) dropped it", key, trueCount, threshold, m.K())
	}
	require.Positive(t, kept,
		"fixture sanity: no key cleared the retention threshold, so the guarantee went untested")
}

// runMGShapeMismatchCase asserts merging summaries built with different counter budgets is refused
// with ErrShapeMismatch. err ≤ total/(k+1) is a statement ABOUT k, so a pairwise sum of two
// summaries with different k would satisfy neither of their bounds — and every count that came out
// of it would still look like a plausible frequency.
func runMGShapeMismatchCase(t *testing.T, factory func(t *testing.T) *sketch.MisraGries) {
	t.Helper()
	m := factory(t)

	other := sketch.NewMisraGries(m.K() + 1)
	require.NotEqual(t, m.K(), other.K(),
		"fixture sanity: the mismatch operand must not have the factory's k")

	require.ErrorIs(t, m.MergeFrom(other), sketch.ErrShapeMismatch)
}
