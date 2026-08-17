package sketch

import "math"

// Failure-mode rule for the whole package, and the reason these three helpers exist at all: no
// constructor ever panics and no constructor ever returns an error. Out-of-range, NaN and infinite
// arguments are clamped to the nearest legal value, because a hook that dies takes observability
// down with it (00-ARCHITECTURE.md §12.3, and §11.3's "Behaviour on invalid config is not
// 'crash'"). Every decoder, by contrast, is strict: it returns a sentinel and allocates nothing
// before it has validated the declared sizes against MaxFrameBytes and against the actual
// remaining buffer length.
//
// All three are pure and safe for concurrent use.

// clamp returns v confined to [lo, hi]. NaN is returned as lo, because a NaN sizing parameter must
// never propagate into an allocation size: uint64(math.NaN()) is implementation-defined in Go, so
// a NaN that reaches a make() length is a real crash. ±Inf is handled by the ordinary comparisons.
func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// clampInt is the integer twin. It exists because clamp is float64-typed and every integer sizing
// parameter in this package (capacity, permutations, shingle size, registers, k) would otherwise
// round-trip through float64 at the call site — which silently loses precision above 2^53 and
// reintroduces exactly the NaN hazard clamp was written to close.
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// satAdd64 adds without wrapping. A wrapped Count would make a saturated sketch look empty, and a
// sketch that reports itself empty is one nothing will ever rebuild.
func satAdd64(a, b uint64) uint64 {
	if a > math.MaxUint64-b {
		return math.MaxUint64
	}
	return a + b
}
