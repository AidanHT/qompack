package eval

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// The exact-optimum cases call the unexported breakpointPlan directly, because stating a
// breakpoint instance in terms of caps and candidates is what makes the optimum hand-verifiable;
// deriving those two slices from a session is covered separately by the exported-API tests.

// TestBreakpointOPT_KnownOptimum is the worked instance from the implementation spec.
//
// caps {100, 100, 100, 900} means three API calls could reuse a cached prefix only up to token
// 100, and one up to token 900. With a single marker, placing it at 900 serves the one long call
// for 900, which beats placing it at 100 (three calls x 100 = 300 plus the long call, which also
// only reaches 100, so 400) and at 500 (only the long call reaches it: 500).
func TestBreakpointOPT_KnownOptimum(t *testing.T) {
	caps := []int{100, 100, 100, 900}
	cand := []int{0, 100, 500, 900}

	one := breakpointPlan(caps, cand, 1)
	require.Equal(t, []int{900}, one.Positions)
	require.Equal(t, int64(900), one.CachedReads)

	two := breakpointPlan(caps, cand, 2)
	require.Equal(t, []int{100, 900}, two.Positions)
	require.Equal(t, int64(1200), two.CachedReads, "100 x 3 calls + 900 x 1 call")
}

// TestBreakpointPlan_MarkersZero: a zero marker budget is a legitimate question whose answer is
// "no markers, no cached reads", not an error.
func TestBreakpointPlan_MarkersZero(t *testing.T) {
	p := breakpointPlan([]int{100, 900}, []int{0, 100, 900}, 0)

	require.Empty(t, p.Positions)
	require.Equal(t, int64(0), p.CachedReads)
	require.Equal(t, 0, p.Markers)
	require.Equal(t, 3, p.Candidates)
	require.Equal(t, NotPluginActionable, p.Note)
}

// TestBreakpointPlan_MoreMarkersNeverWorse: a marker at 0 contributes nothing and can always be
// added, so the value function is monotone in the marker budget. If it ever is not, the DP's
// correction term is wrong.
func TestBreakpointPlan_MoreMarkersNeverWorse(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nCand := rapid.IntRange(1, 24).Draw(rt, "candidates")
		cand := []int{0}
		for range nCand {
			cand = append(cand, cand[len(cand)-1]+rapid.IntRange(1, 5000).Draw(rt, "step"))
		}
		nCaps := rapid.IntRange(1, 24).Draw(rt, "caps")
		caps := make([]int, nCaps)
		for i := range caps {
			caps[i] = rapid.IntRange(0, 120_000).Draw(rt, "cap")
		}

		prev := int64(-1)
		for m := 1; m <= 6; m++ {
			got := breakpointPlan(caps, cand, m).CachedReads
			if got < prev {
				rt.Fatalf("value(%d markers) = %d is worse than value(%d) = %d", m, got, m-1, prev)
			}
			prev = got
		}
	})
}

// TestBreakpointPlan_MatchesBruteForce checks the DP against exhaustive enumeration on instances
// small enough to enumerate, which is the only way to know the correction term is right.
func TestBreakpointPlan_MatchesBruteForce(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nCand := rapid.IntRange(1, 8).Draw(rt, "candidates")
		cand := []int{0}
		for range nCand {
			cand = append(cand, cand[len(cand)-1]+rapid.IntRange(1, 400).Draw(rt, "step"))
		}
		nCaps := rapid.IntRange(1, 8).Draw(rt, "caps")
		caps := make([]int, nCaps)
		for i := range caps {
			caps[i] = rapid.IntRange(0, 3000).Draw(rt, "cap")
		}
		markers := rapid.IntRange(1, 3).Draw(rt, "markers")

		want := bruteForceBreakpointValue(caps, cand, markers)
		got := breakpointPlan(caps, cand, markers).CachedReads
		if got != want {
			rt.Fatalf("breakpointPlan = %d, brute force = %d (caps=%v cand=%v markers=%d)",
				got, want, caps, cand, markers)
		}
	})
}

// bruteForceBreakpointValue enumerates every marker subset of size <= markers and returns the
// best Σ_t max{q ∈ B ∪ {0} : q <= cap_t}.
func bruteForceBreakpointValue(caps, cand []int, markers int) int64 {
	best := int64(0)
	var rec func(start int, chosen []int)
	rec = func(start int, chosen []int) {
		if v := breakpointValue(caps, chosen); v > best {
			best = v
		}
		if len(chosen) == markers {
			return
		}
		for i := start; i < len(cand); i++ {
			rec(i+1, append(chosen, cand[i]))
		}
	}
	rec(0, nil)
	return best
}

// breakpointValue is the definition the DP optimizes, evaluated directly.
func breakpointValue(caps, markers []int) int64 {
	var total int64
	for _, c := range caps {
		best := 0
		for _, q := range markers {
			if q <= c && q > best {
				best = q
			}
		}
		total += int64(best)
	}
	return total
}
