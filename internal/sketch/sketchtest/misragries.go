package sketchtest

import (
	"testing"

	"github.com/qompack/qompack/internal/sketch"
	"github.com/stretchr/testify/require"
)

// misraGriesAddedKeys is the fixture runNoFalsePositivesCase adds before checking Top.
func misraGriesAddedKeys() map[string]int {
	return map[string]int{"alpha": 50, "beta": 30, "gamma": 20, "delta": 5}
}

// RunMisraGriesSuite is the conformance suite for sketch.MisraGries specifically (the
// MisraGries-only methods Add/Top/MergeFrom are not part of the generic sketch.Sketch interface
// RunSketchSuite covers). factory must return a fresh MisraGries on every call.
func RunMisraGriesSuite(t *testing.T, name string, factory func(t *testing.T) *sketch.MisraGries) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		m := factory(t)
		require.NotNil(t, m)

		m.Add("shape probe", 1)
		_ = m.Top(1)

		requireKnownError(t, m.MergeFrom(factory(t)))
	})

	if skipIfStubSketch(t, factory(t)) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("no_false_positives", func(t *testing.T) { runNoFalsePositivesCase(t, factory) })
	})
}

// runNoFalsePositivesCase asserts Misra-Gries's core guarantee (00-ARCHITECTURE.md §5.7): every
// key Top returns really was Added at some point. Misra-Gries may under-report a true heavy
// hitter's count, or omit one entirely (a false negative), but by construction it never invents a
// key that was never seen (a false positive).
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
