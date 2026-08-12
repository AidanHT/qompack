package tokenstest

import (
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/tokens"
)

// stubSkipMsg is the exact, mandatory Rule W-1 skip reason devtool lint's stubskips sub-check
// greps for. Any other wording is a hard lint failure.
const stubSkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// stubProbeBytes is the size of the payload isStub estimates to distinguish a real implementation
// (which must price a non-trivial payload above zero tokens) from a stub returning the documented
// §14.1 zero value for every method with no error return.
const stubProbeBytes = 64

// allClasses is every tokens.Class, in declaration order, for the /shape block's per-class sweep.
var allClasses = []tokens.Class{
	tokens.ClassProse, tokens.ClassCode, tokens.ClassJSON, tokens.ClassDiff,
	tokens.ClassImage, tokens.ClassPDF, tokens.ClassBinary,
}

// RunEstimatorSuite is the conformance suite for tokens.Estimator. Its /shape block always runs:
// every method must be callable and return a sane (non-negative, finite) result. Its /behaviour
// block is skipped with the exact Rule W-1 message when isStub reports the factory's Estimator
// looks like a stub; otherwise it asserts monotonicity in length, that repeated Calibrate calls
// clamp Factor to a stable ceiling and floor, and that EstimateRoot equals the sum over its
// chunks.
func RunEstimatorSuite(t *testing.T, name string, factory func(*testing.T) tokens.Estimator) {
	t.Run(name+"/shape", func(t *testing.T) {
		runShape(t, factory)
	})

	if skipIfStub(t, isStub(t, factory)) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("monotone_in_length", func(t *testing.T) { runMonotoneInLength(t, factory) })
		t.Run("factor_clamped", func(t *testing.T) { runFactorClamped(t, factory) })
		t.Run("estimate_root_sums_chunks", func(t *testing.T) { runEstimateRootSumsChunks(t, factory) })
	})
}

func runShape(t *testing.T, factory func(*testing.T) tokens.Estimator) {
	e := factory(t)
	require.NotNil(t, e)

	for _, c := range allClasses {
		got := e.Estimate([]byte("hello world, this is a shape probe"), c)
		require.GreaterOrEqual(t, int64(got), int64(0), "Estimate must never be negative (class %s)", c)
	}
	require.GreaterOrEqual(t, int64(e.EstimateString("hello", tokens.ClassProse)), int64(0))

	chunks := []core.ChunkRef{{Hash: core.HashBytes("tokenstest.shape", []byte("a")), Len: 10}}
	require.GreaterOrEqual(t, int64(e.EstimateRoot(context.Background(), chunks, tokens.ClassProse)), int64(0))
	require.Equal(t, core.Tokens(0), e.EstimateRoot(context.Background(), nil, tokens.ClassProse),
		"EstimateRoot over zero chunks must be zero")

	f := e.Factor()
	require.False(t, math.IsNaN(f) || math.IsInf(f, 0), "Factor must be finite, got %v", f)
	require.Greater(t, f, 0.0, "Factor must be positive")

	// Calibrate must be callable without panicking, including the estimated==0 no-op case, and
	// must not be observable as an error the caller has to handle (it has no error return at all).
	e.Calibrate(0, 0)
	e.Calibrate(100, 50)
}

// isStub reports whether factory's Estimator behaves like the §14.1 stub pattern: every method
// with no error return produces the zero value, so Estimate of a non-trivial payload is always
// core.Tokens(0). A real estimator prices stubProbeBytes of prose at strictly more than zero
// tokens for any charsPerToken in the validated (0,20] range, so this probe cannot false-positive
// against a real implementation.
func isStub(t *testing.T, factory func(*testing.T) tokens.Estimator) bool {
	t.Helper()
	e := factory(t)
	probe := bytes.Repeat([]byte("x"), stubProbeBytes)
	return e.Estimate(probe, tokens.ClassProse) == 0
}

func skipIfStub(t *testing.T, stub bool) bool {
	t.Helper()
	if stub {
		t.Skip(stubSkipMsg)
		return true
	}
	return false
}

// runMonotoneInLength asserts that, for a fixed class, a longer input never prices below a
// shorter one. Only byte-length-driven classes are meaningful here — an Image or PDF estimate is
// driven by decoded dimensions or page count, not raw length, so a random byte blob of that class
// has no such guarantee.
func runMonotoneInLength(t *testing.T, factory func(*testing.T) tokens.Estimator) {
	e := factory(t)
	rapid.Check(t, func(rt *rapid.T) {
		shortLen := rapid.IntRange(0, 2000).Draw(rt, "shortLen")
		growBy := rapid.IntRange(0, 2000).Draw(rt, "growBy")
		short := bytes.Repeat([]byte("a"), shortLen)
		long := bytes.Repeat([]byte("a"), shortLen+growBy)

		gotShort := e.Estimate(short, tokens.ClassProse)
		gotLong := e.Estimate(long, tokens.ClassProse)
		if gotLong < gotShort {
			rt.Fatalf("longer input priced lower: len=%d -> %v tokens, len=%d -> %v tokens",
				len(short), gotShort, len(long), gotLong)
		}
	})
}

// runFactorClamped drives Calibrate hard in both directions and asserts Factor plateaus rather
// than diverging — proof that a ceiling and floor are in effect without this generic suite needing
// to know their exact configured values (that exact-bound assertion belongs to internal/tokens's
// own test, which does know its config).
func runFactorClamped(t *testing.T, factory func(*testing.T) tokens.Estimator) {
	const calibrateRounds = 50

	up := factory(t)
	for i := 0; i < calibrateRounds; i++ {
		up.Calibrate(1000, 1) // observed >> estimated: pushes the factor up every call
	}
	ceiling := up.Factor()
	require.False(t, math.IsNaN(ceiling) || math.IsInf(ceiling, 0))
	require.Greater(t, ceiling, 0.0)
	up.Calibrate(1000, 1)
	require.Equal(t, ceiling, up.Factor(), "factor must stop increasing once it reaches its ceiling")

	down := factory(t)
	for i := 0; i < calibrateRounds; i++ {
		down.Calibrate(1, 1000) // observed << estimated: pushes the factor down every call
	}
	floor := down.Factor()
	require.False(t, math.IsNaN(floor) || math.IsInf(floor, 0))
	require.Greater(t, floor, 0.0)
	down.Calibrate(1, 1000)
	require.Equal(t, floor, down.Factor(), "factor must stop decreasing once it reaches its floor")

	require.LessOrEqual(t, floor, ceiling)
}

func runEstimateRootSumsChunks(t *testing.T, factory func(*testing.T) tokens.Estimator) {
	e := factory(t)
	a := core.ChunkRef{Hash: core.HashBytes("tokenstest.suite", []byte("chunk-a")), Len: 100}
	b := core.ChunkRef{Hash: core.HashBytes("tokenstest.suite", []byte("chunk-b")), Len: 250}

	sumSeparate := e.EstimateRoot(context.Background(), []core.ChunkRef{a}, tokens.ClassProse) +
		e.EstimateRoot(context.Background(), []core.ChunkRef{b}, tokens.ClassProse)
	together := e.EstimateRoot(context.Background(), []core.ChunkRef{a, b}, tokens.ClassProse)
	require.Equal(t, sumSeparate, together, "EstimateRoot must equal the sum of per-chunk estimates")
}
