package sketch

import (
	"math"
	"runtime"
	"strconv"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// These tests are package sketch, not package sketch_test, for the same reason bloom_test.go is:
// several of them assert on c.cells and c.total, which are the quantities Appendix A actually
// specifies, and observing them only through Dims/Total would leave the sizing arithmetic pinned
// one indirection away from where it is written. internal/testutil is unavailable here on purpose
// — it imports internal/store, which imports this package — and nothing below needs it.

// The Appendix C defaults for sketches.cms. Every number in this file that is not stated outright
// is derived from these two by Appendix A's formulas, so a reader can recompute any expectation by
// hand.
const (
	// cmsEpsilon is Appendix C's sketches.cms.epsilon, the ε of the additive error bound ε·N.
	cmsEpsilon = 0.001
	// cmsDelta is Appendix C's sketches.cms.delta, the probability that bound is exceeded.
	cmsDelta = 0.01
)

// The three sizing numbers Appendix A's worked example produces, spelled out rather than computed
// so that this file disagrees with the implementation if either drifts.
//
// ⌈e/ε⌉ = ⌈2718.281828…⌉ = 2719, and ⌈ln(1/δ)⌉ = ⌈4.60517…⌉ = 5. Appendix A writes "2718 × 5 ≈
// 54 KB @ 4-byte counters"; 2718 is the same quantity shown to display precision. The formula is
// normative, the illustration is not.
const (
	// cmsWidth is ⌈e/ε⌉ at ε = 0.001.
	cmsWidth = 2_719
	// cmsDepth is ⌈ln(1/δ)⌉ at δ = 0.01.
	cmsDepth = 5
	// cmsBodyBytes is width × depth × 4, the marshalled body length: 53.1 KiB.
	cmsBodyBytes = 54_380
)

// TestCMS_AppendixASizing pins Appendix A's Count-Min sizing — "width = ⌈e/ε⌉, depth = ⌈ln(1/δ)⌉;
// ε = 0.001, δ = 0.01 → 2718 × 5 ≈ 54 KB @ 4-byte counters" — to the exact integers this
// implementation produces.
//
// ⌈e/ε⌉ = ⌈2718.281828…⌉ = 2719. Appendix A's "2718 × 5" is the same quantity shown to display
// precision — the formula is normative, the illustration is not.
//
// The formulas are re-evaluated here as well as the results asserted, because the two are
// different claims: 2719 is what this build produces, and ⌈e/0.001⌉ is what Appendix A asks for. A
// test that only checked the integer could not tell a sizing bug from a transcription slip.
func TestCMS_AppendixASizing(t *testing.T) {
	c := NewCMS(cmsEpsilon, cmsDelta)

	width, depth := c.Dims()
	require.Equal(t, cmsWidth, width)
	require.Equal(t, cmsDepth, depth)
	require.Equal(t, math.Ceil(math.E/cmsEpsilon), float64(width),
		"Appendix A's width = ⌈e/ε⌉ must ceil to %d, not to the displayed 2718", cmsWidth)
	require.Equal(t, math.Ceil(math.Log(1/cmsDelta)), float64(depth),
		"Appendix A's depth = ⌈ln(1/δ)⌉ must ceil to %d", cmsDepth)
	require.Len(t, c.cells, cmsWidth*cmsDepth)

	data, err := c.MarshalBinary()
	require.NoError(t, err)
	_, body, err := DecodeHeader(data)
	require.NoError(t, err)
	require.Len(t, body, cmsBodyBytes,
		"Appendix A's ≈54 KB is %d bytes of 4-byte counters", cmsBodyBytes)

	t.Logf("Appendix A (ε=%g, δ=%g): width=%d depth=%d cells=%d body=%d bytes frame=%d bytes",
		cmsEpsilon, cmsDelta, width, depth, len(c.cells), len(body), len(data))
}

// TestCMS_SizingTable walks Appendix A's two formulas across three (ε, δ) pairs, so the width
// derivation is pinned an order of magnitude either side of the default and the depth derivation is
// pinned at a δ where it is not 5. Each expectation is the ceiling of a number a reader can compute:
// e/0.01 = 271.83, e/0.001 = 2718.28, e/0.0001 = 27182.82, ln(1/0.001) = 6.91.
func TestCMS_SizingTable(t *testing.T) {
	for _, tc := range []struct {
		name      string
		epsilon   float64
		delta     float64
		wantWidth int
		wantDepth int
	}{
		{"ε=0.01 δ=0.01", 0.01, 0.01, 272, 5},
		{"ε=0.001 δ=0.001", cmsEpsilon, 0.001, cmsWidth, 7},
		{"ε=0.0001 δ=0.01", 0.0001, cmsDelta, 27_183, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewCMS(tc.epsilon, tc.delta)
			width, depth := c.Dims()
			require.Equal(t, tc.wantWidth, width)
			require.Equal(t, tc.wantDepth, depth)
			require.Len(t, c.cells, tc.wantWidth*tc.wantDepth,
				"the counter table must be exactly width×depth cells")
			require.LessOrEqual(t, width*depth, MaxCMSCells, "the ceiling rule (errors.go)")
		})
	}
}

// cmsWeights is the fixed weight pattern the two accuracy tests share. It cycles over the 5 000
// keys of the stream and sums to 200 per 10 keys, so the whole stream is exactly 100 000 weighted
// adds — which is the N that makes ε·N = 100 the bound TestCMS_ErrorBoundHolds asserts. The shape
// is deliberately skewed rather than uniform: a Count-Min's error comes from collisions with OTHER
// keys' mass, so a stream where every key carries the same weight would understate the error a real
// touch-frequency distribution produces.
var cmsWeights = [10]uint32{1, 2, 3, 5, 8, 13, 21, 34, 55, 58}

const (
	// cmsStreamKeys is the number of distinct keys in the shared accuracy stream.
	cmsStreamKeys = 5_000
	// cmsStreamTotal is the stream's total weighted mass, N.
	cmsStreamTotal = 100_000
	// cmsErrorBound is ε·N, the Count-Min additive error guarantee at the Appendix C default.
	cmsErrorBound = 100
	// cmsConfidence is 1 − δ, the fraction of keys that guarantee is required to hold for.
	cmsConfidence = 0.99
)

// cmsStream builds the shared accuracy fixture: a default-sized CMS holding cmsStreamKeys distinct
// keys whose weights sum to exactly cmsStreamTotal, plus the exact truth table to check it against.
// It is a helper rather than duplicated setup so that the never-underestimates test and the
// error-bound test provably observe the SAME stream — the second is only meaningful as a
// refinement of the first.
func cmsStream(t *testing.T) (*CMS, map[string]uint32) {
	t.Helper()

	c := NewCMS(cmsEpsilon, cmsDelta)
	truth := make(map[string]uint32, cmsStreamKeys)
	buf := make([]byte, 0, 32)
	var total uint64
	for i := 0; i < cmsStreamKeys; i++ {
		buf = cmsKey(buf, "cms-", i)
		w := cmsWeights[i%len(cmsWeights)]
		c.Add(buf, w)
		truth[string(buf)] += w
		total += uint64(w)
	}

	require.Equal(t, uint64(cmsStreamTotal), total,
		"fixture sanity: the weight pattern must sum to exactly N")
	require.Len(t, truth, cmsStreamKeys, "fixture sanity: the keys must all be distinct")
	require.Equal(t, uint64(cmsStreamTotal), c.Total())
	return c, truth
}

// TestCMS_EstimateNeverUnderestimates asserts the one guarantee a Count-Min sketch is not allowed
// to break (00-ARCHITECTURE.md §5.7): Estimate may over-count, from collisions with other keys'
// mass, but it must never under-count. Every consumer treats the estimate as an upper bound — SP-08
// feeds it, SP-03's HeavyHitters sharpens Misra-Gries with it — so an under-count would make a hot
// file look cold and be acted on as such.
func TestCMS_EstimateNeverUnderestimates(t *testing.T) {
	c, truth := cmsStream(t)

	buf := make([]byte, 0, 32)
	for i := 0; i < cmsStreamKeys; i++ {
		buf = cmsKey(buf, "cms-", i)
		got := c.Estimate(buf)
		require.GreaterOrEqual(t, got, truth[string(buf)],
			"Estimate must never under-count %q", buf)
	}
}

// TestCMS_ErrorBoundHolds is the quantitative half of the guarantee above: the over-count is not
// merely bounded in principle, it stays inside Appendix A's ε·N for at least 1 − δ of the keys.
// At the Appendix C defaults that is "at most 100 over, for at least 99 % of the keys" over a
// 100 000-weight stream.
//
// The 99 % is the sketch's own δ = 0.01. Asserting the fraction rather than every key is not a
// weakening: Count-Min's guarantee IS probabilistic, and a test demanding it hold for every key
// would be asserting something the structure never promised and would fail on a stream that is
// behaving exactly as specified.
func TestCMS_ErrorBoundHolds(t *testing.T) {
	c, truth := cmsStream(t)

	require.Equal(t, float64(cmsErrorBound), math.Round(cmsEpsilon*cmsStreamTotal),
		"fixture sanity: the bound under test must be ε·N")

	within := 0
	var worst int64
	buf := make([]byte, 0, 32)
	for i := 0; i < cmsStreamKeys; i++ {
		buf = cmsKey(buf, "cms-", i)
		// Signed, and checked for sign before it is used. The subtraction in uint32 would wrap to
		// ~4 billion the moment the never-underestimates invariant broke, and the failure would then
		// be reported as an absurd over-count rather than as the under-count it actually is.
		over := int64(c.Estimate(buf)) - int64(truth[string(buf)])
		require.GreaterOrEqual(t, over, int64(0),
			"Estimate under-counted %q by %d; the ε·N bound below is meaningless if this fails", buf, -over)
		if over > worst {
			worst = over
		}
		if over <= cmsErrorBound {
			within++
		}
	}

	need := int(math.Ceil(cmsConfidence * cmsStreamKeys))
	t.Logf("ε·N = %d: %d/%d keys (%.4f) within the bound; worst over-count %d",
		cmsErrorBound, within, cmsStreamKeys, float64(within)/cmsStreamKeys, worst)
	require.GreaterOrEqual(t, within, need,
		"Appendix A: at least 1−δ = %g of keys must be within ε·N = %d", cmsConfidence, cmsErrorBound)
}

// TestCMS_AbsentKeyEstimate pins the empty sketch's answer. A key that was never added hashes to
// five cells that nothing has incremented, so 0 is exact — and it must be, because §13 invariant 3
// makes "no evidence" the only safe answer a sketch may give without a record behind it.
func TestCMS_AbsentKeyEstimate(t *testing.T) {
	c := NewCMS(cmsEpsilon, cmsDelta)
	require.Equal(t, uint32(0), c.Estimate([]byte("nope")))
	require.Equal(t, uint64(0), c.Total())
}

// TestCMS_AddZeroIsNoop pins the weight-0 guard. A zero-weight add carries no information, and
// letting it through would still move Total — which is the N every error bound above is stated
// against, so a stream of zero-weight adds would silently loosen the sketch's own accuracy claim.
func TestCMS_AddZeroIsNoop(t *testing.T) {
	c := NewCMS(cmsEpsilon, cmsDelta)
	k := []byte("zero-weight")
	c.Add(k, 0)
	require.Equal(t, uint64(0), c.Total())
	require.Equal(t, uint32(0), c.Estimate(k))
}

// TestCMS_Saturation asserts every counter update saturates rather than wraps. Wrapping would break
// the sketch's ONLY exact guarantee — Estimate(k) ≥ true(k) — by turning a maximal counter into a
// near-zero one, which is precisely the failure TestCMS_EstimateNeverUnderestimates exists to catch
// and which no consumer could detect from the outside.
//
// Both writing paths are covered, because they are two separate clamps in two separate loops: Add,
// and MergeFrom's cell-wise addition. A fix applied to one and forgotten in the other would leave
// the merge — the O4 warm-start path, where a historical table is added to a live one — as the one
// remaining way to wrap a counter, and warm-started sessions are exactly where the counters are
// largest.
func TestCMS_Saturation(t *testing.T) {
	c := NewCMS(cmsEpsilon, cmsDelta)
	k := []byte("saturating")
	c.Add(k, math.MaxUint32)
	c.Add(k, math.MaxUint32)

	require.Equal(t, uint32(math.MaxUint32), c.Estimate(k), "Add's counters must clamp, not wrap")
	require.Equal(t, uint64(math.MaxUint32)*2, c.Total(),
		"Total is uint64 and has not saturated yet, so it must hold the exact sum")

	// MergeFrom's clamp. Both tables carry a maximal counter for the same key, so every cell that
	// key touches is already at the ceiling when the cell-wise addition runs.
	other := NewCMS(cmsEpsilon, cmsDelta)
	other.Add(k, math.MaxUint32)

	// Total's own saturation cannot be reached by insertion — approaching MaxUint64 would take 2^32
	// maximal Add calls — so the two totals are written directly. This is one of the things the file
	// is package sketch for: the branch is reachable in microseconds from here and not at all from
	// the exported surface.
	c.total = math.MaxUint64 - 1
	other.total = 2

	require.NoError(t, c.MergeFrom(other))
	require.Equal(t, uint32(math.MaxUint32), c.Estimate(k),
		"MergeFrom's counters must clamp, not wrap")
	require.Equal(t, uint64(math.MaxUint64), c.Total(), "Total must saturate, not wrap")
}

// TestCMS_MergeFromIsAdditive is the O4 warm-start test: "Warm-start Count-Min with the project's
// historical hot-file distribution." Merging the historical table into the session's must produce
// exactly the table a single pass over both streams would have produced — otherwise a warm start
// would answer differently from a cold one over the same data, and the frequency signal the
// scheduler reads would depend on when the session began.
//
// The two streams overlap on purpose. Disjoint streams would exercise only the "one side is zero"
// case of the cell-wise add, which is the case that cannot be got wrong.
func TestCMS_MergeFromIsAdditive(t *testing.T) {
	c1, c2, c3 := NewCMS(cmsEpsilon, cmsDelta), NewCMS(cmsEpsilon, cmsDelta), NewCMS(cmsEpsilon, cmsDelta)

	union := make([][]byte, 0, 800)
	add := func(into *CMS, prefix string, n int, w uint32) {
		for i := 0; i < n; i++ {
			k := cmsKey(nil, prefix, i)
			into.Add(k, w)
			c3.Add(k, w)
			union = append(union, k)
		}
	}
	add(c1, "shared-", 200, 3)
	add(c1, "onlya-", 300, 5)
	add(c2, "shared-", 200, 7)
	add(c2, "onlyb-", 300, 2)

	require.NoError(t, c1.MergeFrom(c2))
	require.Equal(t, c3.Total(), c1.Total())
	for _, k := range union {
		require.Equal(t, c3.Estimate(k), c1.Estimate(k),
			"a merged table must answer identically to one built from the union, on %q", k)
	}
	require.Equal(t, c3.cells, c1.cells, "the merge is cell-wise addition, so the tables must match")
}

// TestCMS_MergeFromShapeMismatch asserts a merge across differing dimensions is refused rather than
// attempted. Two Count-Min tables of different widths index the same key to different cells, so
// adding them elementwise would produce a table whose counters describe no stream at all — and
// nothing downstream could tell, because every answer would still look like a plausible frequency.
func TestCMS_MergeFromShapeMismatch(t *testing.T) {
	c := NewCMS(cmsEpsilon, cmsDelta)
	require.ErrorIs(t, c.MergeFrom(NewCMS(0.01, cmsDelta)), ErrShapeMismatch, "differing width")
	require.ErrorIs(t, c.MergeFrom(NewCMS(cmsEpsilon, 0.001)), ErrShapeMismatch, "differing depth")
	require.ErrorIs(t, c.MergeFrom(nil), ErrShapeMismatch, "a nil source is a shape mismatch, not a panic")
}

// cmsWithCounts returns a default-sized CMS holding n keys, each added once with weight w. n is
// kept far below the width so that no two keys collide in all five rows: the decay assertions below
// are exact equalities, and they are only legitimate because every estimate here is exact.
func cmsWithCounts(t *testing.T, n int, w uint32) *CMS {
	t.Helper()
	c := NewCMS(cmsEpsilon, cmsDelta)
	buf := make([]byte, 0, 32)
	for i := 0; i < n; i++ {
		buf = cmsKey(buf, "scale-", i)
		c.Add(buf, w)
	}
	return c
}

// TestCMS_ScaleDecays pins the other half of O4. A warm-started table must weight history BELOW the
// current session, or a project's historical hot files would outvote what the agent is touching
// right now for the whole of the session. Scale is the exponential decay that does it.
//
// The three degenerate factors are asserted alongside the working one because each is a distinct
// decision: 1 is the identity fast path, 0 is "discard history entirely", and a negative factor is
// clamped to 0 rather than producing negative counters or an undefined float→uint32 conversion.
func TestCMS_ScaleDecays(t *testing.T) {
	const (
		keys   = 10
		weight = 100
	)
	key := func(i int) []byte { return cmsKey(nil, "scale-", i) }

	c := cmsWithCounts(t, keys, weight)
	for i := 0; i < keys; i++ {
		require.Equal(t, uint32(weight), c.Estimate(key(i)),
			"fixture sanity: %d keys in a %d-wide table must not collide", keys, cmsWidth)
	}
	require.Equal(t, uint64(keys*weight), c.Total())

	c.Scale(0.5)
	for i := 0; i < keys; i++ {
		require.Equal(t, uint32(weight/2), c.Estimate(key(i)))
	}
	require.Equal(t, uint64(keys*weight/2), c.Total())

	c.Scale(1)
	for i := 0; i < keys; i++ {
		require.Equal(t, uint32(weight/2), c.Estimate(key(i)), "Scale(1) must be the identity")
	}
	require.Equal(t, uint64(keys*weight/2), c.Total())

	zeroed := cmsWithCounts(t, keys, weight)
	zeroed.Scale(0)
	for i := 0; i < keys; i++ {
		require.Equal(t, uint32(0), zeroed.Estimate(key(i)), "Scale(0) must discard history")
	}
	require.Equal(t, uint64(0), zeroed.Total())

	negative := cmsWithCounts(t, keys, weight)
	negative.Scale(-1)
	require.Equal(t, zeroed.cells, negative.cells, "a negative factor must behave as Scale(0)")
	require.Equal(t, uint64(0), negative.Total())

	// A NaN factor must behave as Scale(0) too, which is what Scale's doc comment claims. clamp
	// returns the floor for NaN precisely so that a NaN can never reach the float→uint conversions
	// below it: uint32(NaN) and uint64(NaN) are both implementation-defined in Go.
	nan := cmsWithCounts(t, keys, weight)
	nan.Scale(math.NaN())
	require.Equal(t, zeroed.cells, nan.cells, "a NaN factor must behave as Scale(0)")
	require.Equal(t, uint64(0), nan.Total())

	// The two upward clamps, which nothing else in this file reaches. A factor of 1e30 drives every
	// occupied cell past MaxUint32 and the total past maxTotalFloat — and the total's ceiling is the
	// whole reason maxTotalFloat exists, since a direct comparison against math.MaxUint64 would be
	// made against 2^64 after rounding and uint64(2^64) is undefined in Go.
	huge := cmsWithCounts(t, keys, weight)
	huge.Scale(1e30)
	for i := 0; i < keys; i++ {
		require.Equal(t, uint32(math.MaxUint32), huge.Estimate(key(i)),
			"a factor past the ceiling must clamp each cell at MaxUint32, not convert out of range")
	}
	require.Equal(t, uint64(1)<<62, huge.Total(),
		"Total must clamp at maxTotalFloat = 2^62, the largest value that round-trips float64 exactly")
}

// The heavy-hitters fixture. Every number in it is load-bearing and none of it may be
// "simplified" — see the two paragraphs on the test itself.
const (
	// hhCountA, hhCountB and hhCountC are the three heavy hitters' true frequencies.
	hhCountA = 500
	hhCountB = 300
	hhCountC = 100
	// hhSingletons is the number of one-off keys the three are hidden among.
	hhSingletons = 200
	// hhK is the Misra-Gries counter budget.
	hhK = 64
	// hhTotal is the stream's length: 500 + 300 + 100 + 200.
	hhTotal = hhCountA + hhCountB + hhCountC + hhSingletons
)

// TestCMS_HeavyHittersPairsWithMG is the pairing this method exists for: a Count-Min sketch stores
// no keys and a Misra-Gries summary under-counts, so the two are combined — MG supplies the
// candidate keys, which it reports with no false positives, and CMS supplies the sharper count for
// each.
//
// The stream shape is load-bearing, and must not be "simplified" in either direction.
//
// The count of 100 for c is a floor, not an arbitrary third value: Misra-Gries retains any key whose
// true frequency exceeds total/(k+1) = 1100/65 = 16.9, so c at 100 is safely retained where a c at
// 10 would be free to fall out of the summary and the assertion below would be flaky rather than
// deterministic.
//
// The 200 singletons are a ceiling for the same kind of reason. They are what makes the test
// meaningful — the three heavy hitters have to be found among a crowd — but every extra distinct key
// raises the chance that a, b or c collides with something in ALL FIVE rows, which is the only way
// the CMS count could come back above the true one. At 203 distinct keys in a 2 719-wide table that
// probability is about (202/2719)^5 ≈ 2×10⁻⁶; at 2 000 singletons it would be about 4 %, and this
// test would fail roughly one run in twenty-five for a reason that has nothing to do with the code.
func TestCMS_HeavyHittersPairsWithMG(t *testing.T) {
	c := NewCMS(cmsEpsilon, cmsDelta)
	mg := NewMisraGries(hhK)

	feed := func(key string, n int) {
		for i := 0; i < n; i++ {
			c.Add([]byte(key), 1)
			mg.Add(key, 1)
		}
	}
	feed("a", hhCountA)
	feed("b", hhCountB)
	feed("c", hhCountC)
	for i := 0; i < hhSingletons; i++ {
		feed("one-"+strconv.Itoa(i), 1)
	}

	require.Equal(t, uint64(hhTotal), c.Total(), "fixture sanity: both sketches see the same stream")
	require.Equal(t, int64(hhTotal), mg.Total())
	require.Greater(t, float64(hhCountC), float64(mg.Total())/float64(hhK+1),
		"fixture sanity: c must clear the Misra-Gries retention threshold")

	require.Equal(t, []Counted{
		{Key: "a", Count: hhCountA},
		{Key: "b", Count: hhCountB},
		{Key: "c", Count: hhCountC},
	}, c.HeavyHitters(mg, 3))

	// The counts come from the CMS, not from the summary: Misra-Gries has already subtracted its
	// accumulated error from c, and reporting that lower bound when a sharper estimate is available
	// is the whole reason the two are paired. The candidate set is snapshotted once — Top sorts the
	// whole counter map on every call, and the assertions below are one observation of one summary,
	// not four.
	candidates := mg.Top(0)
	var mgC int
	for _, e := range candidates {
		if e.Key == "c" {
			mgC = e.Count
		}
	}
	t.Logf("c: true=%d cms=%d misra-gries=%d (MaxError=%d); candidates=%d",
		hhCountC, c.Estimate([]byte("c")), mgC, mg.MaxError(), len(candidates))
	require.LessOrEqual(t, mgC, hhCountC)

	// Asking for more than the candidate set holds returns the candidate set, not padding.
	require.Len(t, c.HeavyHitters(mg, len(candidates)+10), len(candidates))

	// An empty summary is the third way to have no keys to estimate, alongside the nil summary and
	// the non-positive n of TestCMS_HeavyHittersNilAndZero: the table below is populated, but a
	// Count-Min sketch stores no keys of its own to fall back on.
	require.Nil(t, c.HeavyHitters(NewMisraGries(hhK), 3),
		"an empty candidate set leaves nothing to estimate")
}

// TestCMS_HeavyHittersNilAndZero pins the two degenerate arguments. HeavyHitters is the pairing of
// a keyless CMS with a Misra-Gries candidate set — MG supplies the keys, CMS supplies the sharper
// count — so with no candidate set, or with no room in the result, there is nothing it could
// honestly report. nil rather than an empty slice, because that is what every other "nothing to
// say" path in this package returns.
func TestCMS_HeavyHittersNilAndZero(t *testing.T) {
	c, _ := cmsStream(t)
	require.Nil(t, c.HeavyHitters(nil, 5), "no candidate set means no keys to estimate")
	require.Nil(t, c.HeavyHitters(NewMisraGries(64), 0), "n <= 0 leaves no room in the result")
	require.Nil(t, c.HeavyHitters(NewMisraGries(64), -1))
}

// TestCMS_MarshalRoundTrip is the §6.2 promise in one test: a sketch that cannot be re-read is
// worse than no sketch. Re-marshalling must be BYTE-identical, because the golden fixtures and the
// content-addressed store both depend on a given logical state having exactly one encoding — Go
// randomizes map iteration, so the params sort in EncodeHeader is what makes that true.
func TestCMS_MarshalRoundTrip(t *testing.T) {
	const created = core.UnixMilli(1_700_000_000_000)

	c, truth := cmsStream(t)
	c.SetCreated(created)

	data, err := c.MarshalBinary()
	require.NoError(t, err)

	got := NewCMS(0.5, 0.5)
	require.NoError(t, got.UnmarshalBinary(data))

	again, err := got.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, data, again, "re-marshalling a decoded sketch must be byte-identical")

	require.Equal(t, c.Total(), got.Total())
	require.Equal(t, created, got.Header().Created)
	require.Equal(t, c.Header(), got.Header())

	width, depth := got.Dims()
	require.Equal(t, cmsWidth, width)
	require.Equal(t, cmsDepth, depth)

	buf := make([]byte, 0, 32)
	for i := 0; i < cmsStreamKeys; i++ {
		buf = cmsKey(buf, "cms-", i)
		require.Equal(t, c.Estimate(buf), got.Estimate(buf), "decoded sketch disagrees on %q", buf)
		require.GreaterOrEqual(t, got.Estimate(buf), truth[string(buf)],
			"a decoded sketch must still never under-count")
	}
}

// TestCMS_UnmarshalRejects walks every way a well-framed QPKS blob can still describe a table this
// build must refuse to build. The frames are assembled through EncodeHeader, so each one has a
// correct CRC and a self-consistent length block: the ONLY thing wrong with them is the CMS-specific
// claim named in the row, which is what makes each row isolate one check.
//
// The sentinel split is the one errors.go draws. A param that is out of range or not an integer is
// ErrMalformed — the frame is structurally intact and says something impossible. A body whose length
// disagrees with the declared width×depth is ErrTruncated, the interrupted-write signature.
func TestCMS_UnmarshalRejects(t *testing.T) {
	const (
		smallWidth = 272
		smallDepth = 5
	)
	body := make([]byte, smallWidth*smallDepth*counterBytes)

	for _, tc := range []struct {
		name    string
		epsilon float64
		delta   float64
		width   float64
		depth   float64
		body    []byte
		wantErr error
	}{
		{"body shorter than width*depth*4", 0.01, cmsDelta, smallWidth, smallDepth, body[:len(body)-1], ErrTruncated},
		{"body longer than width*depth*4", 0.01, cmsDelta, smallWidth, smallDepth, append(append([]byte(nil), body...), 0), ErrTruncated},
		{"depth zero", 0.01, cmsDelta, smallWidth, 0, nil, ErrMalformed},
		{"depth above MaxCMSDepth", 0.01, cmsDelta, smallWidth, MaxCMSDepth + 1, body, ErrMalformed},
		{"depth not an integer", 0.01, cmsDelta, smallWidth, 4.5, body, ErrMalformed},
		{"width zero", 0.01, cmsDelta, 0, smallDepth, nil, ErrMalformed},
		{"epsilon zero", 0, cmsDelta, smallWidth, smallDepth, body, ErrMalformed},
		{"epsilon NaN", math.NaN(), cmsDelta, smallWidth, smallDepth, body, ErrMalformed},
		{"delta zero", 0.01, 0, smallWidth, smallDepth, body, ErrMalformed},
		{"delta one", 0.01, 1, smallWidth, smallDepth, body, ErrMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := cmsFrame(t, tc.epsilon, tc.delta, tc.width, tc.depth, tc.body)
			c := NewCMS(cmsEpsilon, cmsDelta)
			require.ErrorIs(t, c.UnmarshalBinary(frame), tc.wantErr)
		})
	}

	// width×depth over the ceiling gets its own case, because it is also where "no allocation
	// before rejection" is measured. The table this frame declares is MaxCMSCells × MaxCMSDepth
	// cells — 2 GiB at 4 bytes each — so a decoder that sized its make() before checking the
	// dimensions would be an out-of-memory kill of the daemon rather than a returned error.
	// TotalAlloc is cumulative bytes allocated, so its delta across the call bounds what the
	// rejection actually cost.
	t.Run("width*depth above MaxCMSCells", func(t *testing.T) {
		frame := cmsFrame(t, 0.01, cmsDelta, MaxCMSCells, MaxCMSDepth, body)
		c := NewCMS(cmsEpsilon, cmsDelta)

		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		err := c.UnmarshalBinary(frame)
		runtime.ReadMemStats(&after)

		require.ErrorIs(t, err, ErrMalformed)
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("rejecting a %d-cell frame allocated %d bytes", MaxCMSCells*MaxCMSDepth, allocated)
		require.Less(t, allocated, uint64(1<<20),
			"the dimensions must be refused before the counter table is allocated")

		// The receiver is left exactly as it was: a refused frame must not half-replace the live
		// sketch it was being loaded into.
		width, depth := c.Dims()
		require.Equal(t, cmsWidth, width)
		require.Equal(t, cmsDepth, depth)
	})

	// A nil receiver reports rather than panics: §12.3, a hook that dies takes observability with
	// it. The same rule covers MarshalBinary, whose only other option would be a nil dereference.
	var nilCMS *CMS
	require.ErrorIs(t, nilCMS.UnmarshalBinary(nil), ErrMalformed)
	_, err := nilCMS.MarshalBinary()
	require.ErrorIs(t, err, ErrMalformed)

	// An UNSIZED table is refused for the reason bloom.go and misragries.go state: marshallable
	// means re-readable. Without this guard `Save(p, &CMS{})` wrote a perfectly valid 94-byte frame
	// declaring width = 0 and depth = 0, which this very decoder then refuses forever as
	// "param depth = 0 outside [1, 64]" — a sketch that cannot survive a restart, which doc.go says
	// cannot happen. The assertion is here as well as in RunSketchSuite because the suite states the
	// contract generically and this states which numbers make THIS type unsized.
	t.Run("an unsized table is not written", func(t *testing.T) {
		frame, err := new(CMS).MarshalBinary()
		require.ErrorIs(t, err, ErrMalformed)
		require.Nil(t, frame, "nothing is written when nothing can be read back")

		// The decoder's half of the same statement: had it been written, this is what would have
		// happened on the next restart.
		sized, sizedErr := NewCMS(cmsEpsilon, cmsDelta).MarshalBinary()
		require.NoError(t, sizedErr)
		require.NoError(t, NewCMS(cmsEpsilon, cmsDelta).UnmarshalBinary(sized),
			"fixture sanity: a SIZED table's frame does decode, so the refusal above is about the "+
				"dimensions and not about the encoding")
	})
}

// cmsFrame assembles a QPKS frame carrying the four CMS params verbatim. It exists so the rejection
// table above states only what is wrong with each row: every frame it builds is structurally
// perfect — correct magic, version, kind, param order and CRC — so the sentinel the decoder returns
// can only be about the CMS-specific claim under test.
func cmsFrame(t *testing.T, epsilon, delta, width, depth float64, body []byte) []byte {
	t.Helper()
	frame, err := EncodeHeader(Header{
		Ver:  FormatVersion,
		Kind: KindCMS,
		Params: map[string]float64{
			paramDelta:   delta,
			paramDepth:   depth,
			paramEpsilon: epsilon,
			paramWidth:   width,
		},
	}, body)
	require.NoError(t, err, "fixture sanity: the frame itself must be well-formed")
	return frame
}

// cmsKey appends i to prefix, reusing buf's array when there is room. The accuracy tests build
// 5 000 keys twice over and the merge test builds 1 000 more, so a fmt.Sprintf per key would
// dominate the file's runtime for no benefit; passing nil returns a fresh slice for the cases that
// need to retain the key.
func cmsKey(buf []byte, prefix string, i int) []byte {
	buf = append(buf[:0], prefix...)
	return strconv.AppendInt(buf, int64(i), 10)
}
