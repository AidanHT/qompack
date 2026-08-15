package sketch

import (
	"encoding/binary"
	"math"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// These tests are package sketch, not package sketch_test, for the same reason bloom_test.go and
// cms_test.go are: several of them assert on m.counters and m.err directly, which are the
// quantities the Misra-Gries guarantee is actually stated over, and the marshal guards below are
// reachable only by writing a key into the counter map that Add would have truncated. internal/
// testutil is unavailable here on purpose — it imports internal/store, which imports this package —
// and nothing below needs it.

// TestMG_UnderCapacityIsExact pins the case that carries no error at all. While fewer than k
// distinct keys have been seen, no counter is ever decremented, so every count is the true count
// and MaxError is 0 — which is what makes the summary usable as an exact top-k for the small key
// sets SP-08 actually feeds it (a session touches a few hundred files, not a few million).
//
// The constructor's clamps are asserted here too, because K is the quantity this test is about: a
// constructor in this package never panics and never returns an error (util.go), so an out-of-range
// k becomes the nearest legal value and K reports what the caller actually got.
func TestMG_UnderCapacityIsExact(t *testing.T) {
	const k = 8

	m := NewMisraGries(k)
	for i := 0; i < 3; i++ {
		m.Add("a", 1)
	}
	m.Add("b", 1)

	require.Equal(t, []Counted{{Key: "a", Count: 3}, {Key: "b", Count: 1}}, m.Top(0))
	require.Equal(t, int64(0), m.MaxError(), "no decrement can have run below capacity")
	require.Equal(t, int64(4), m.Total())
	require.Equal(t, k, m.K())

	require.Equal(t, 1, NewMisraGries(0).K(), "k below 1 clamps up, it does not panic")
	require.Equal(t, 1, NewMisraGries(-5).K())
	require.Equal(t, MaxMGCounters, NewMisraGries(MaxMGCounters+1).K(),
		"k above the ceiling clamps down (errors.go)")
}

// TestMG_DecrementPhase pins the step that makes the summary bounded, and the two facts that come
// with it: the table never grows past k, and MaxError grows by exactly the amount subtracted.
//
// The two scenarios are separate because they exercise the two halves of the decrement. In the
// first every counter equals d, so the whole table empties and the arriving key is not admitted at
// all — the case where Misra-Gries answers "I know nothing", which is a correct answer for three
// singletons in two counters. In the second a survivor is left, so the "reported ≤ true" and
// "reported ≥ true − MaxError" bounds are asserted against a real count rather than vacuously over
// an empty result.
func TestMG_DecrementPhase(t *testing.T) {
	t.Run("all counters fall together", func(t *testing.T) {
		m := NewMisraGries(2)
		for _, key := range []string{"a", "b", "c"} {
			m.Add(key, 1)
		}

		top := m.Top(0)
		require.LessOrEqual(t, len(top), 2, "the table may never exceed k")
		require.Equal(t, int64(1), m.MaxError(), "one decrement of d = 1 ran")
		require.Equal(t, int64(3), m.Total(), "Total counts the stream, not the survivors")
		// a and b were both at 1 and d is 1, so both are deleted; the arriving c is admitted with
		// n − d = 0, which is no admission at all.
		require.Empty(t, top)
	})

	t.Run("a survivor keeps a lower-bound count", func(t *testing.T) {
		m := NewMisraGries(2)
		m.Add("a", 1)
		m.Add("a", 1)
		m.Add("b", 1)
		m.Add("c", 1)

		require.Equal(t, []Counted{{Key: "a", Count: 1}}, m.Top(0))
		require.Equal(t, int64(1), m.MaxError())
		require.Equal(t, int64(4), m.Total())

		const trueA = 2
		got := m.Top(0)[0].Count
		require.LessOrEqual(t, got, trueA, "a count is never an over-count")
		require.GreaterOrEqual(t, int64(got), trueA-m.MaxError(),
			"a count is never more than MaxError below the truth")
	})
}

// TestMG_WeightedAdd pins the weighted update against a hand-computable trace, because the weighted
// form is where the algorithm stops being obvious: d is the minimum of the ARRIVING weight and every
// stored counter, not merely of the stored counters, and the arriving key is admitted with n − d
// rather than with n.
//
//	Add("a", 10)  →  {a:10}                         total 10, err 0
//	Add("b", 4)   →  {a:10, b:4}                    total 14, err 0   (still below k = 2? no: at k)
//	Add("c", 3)   →  d = min(3, 10, 4) = 3
//	                 a:10−3 = 7, b:4−3 = 1, c admitted with 3−3 = 0, i.e. not at all
//	              →  {a:7, b:1}                     total 17, err 3
func TestMG_WeightedAdd(t *testing.T) {
	m := NewMisraGries(2)
	m.Add("a", 10)
	m.Add("b", 4)
	m.Add("c", 3)

	require.Equal(t, map[string]int{"a": 7, "b": 1}, m.counters)
	require.Equal(t, int64(3), m.MaxError())
	require.Equal(t, int64(17), m.Total())
	require.Equal(t, []Counted{{Key: "a", Count: 7}, {Key: "b", Count: 1}}, m.Top(0))

	// The invariant every bound in this file rests on: the mass removed by a decrement is exactly
	// (k+1)·d, so err can never exceed total/(k+1).
	require.LessOrEqual(t, float64(m.MaxError()), float64(m.Total())/float64(m.K()+1))
	t.Logf("weighted trace: counters=%v total=%d MaxError=%d", m.counters, m.Total(), m.MaxError())

	// A weight large enough to wrap saturates instead. Add takes an int, so two adds near
	// math.MaxInt would previously have driven the counter and Total negative — and Top's documented
	// bound is stated over both: a negative count reports a key as having occurred a negative number
	// of times, and a negative Total makes the retention threshold Total()/(k+1) meaningless.
	// Saturating leaves a count that is still a lower bound on the truth, still positive, and still
	// small enough to encode, which is the only one of the three outcomes a consumer can act on.
	t.Run("a weight that would wrap saturates instead", func(t *testing.T) {
		sat := NewMisraGries(2)
		sat.Add("big", math.MaxInt)
		sat.Add("big", math.MaxInt)
		sat.SetCreated(mgCreated)

		top := sat.Top(0)
		require.Len(t, top, 1)
		require.Equal(t, Counted{Key: "big", Count: maxMGCount}, top[0],
			"the counter saturates at the ceiling rather than wrapping negative")
		require.Positive(t, top[0].Count)
		require.Equal(t, int64(maxMGCount), sat.Total())
		require.Positive(t, sat.Total(), "Total must stay a count, so Total()/(k+1) keeps its meaning")

		// The saturated summary is still writable and still re-readable: saturation keeps every
		// value inside the range decodeV1 accepts, where a wrap would have put a negative count on
		// disk as a 2^64-sized unsigned one.
		frame, err := sat.MarshalBinary()
		require.NoError(t, err)
		got := NewMisraGries(2)
		require.NoError(t, got.UnmarshalBinary(frame))
		require.Equal(t, top, got.Top(0))
		require.Equal(t, sat.Total(), got.Total())
	})
}

// TestMG_NonPositiveWeightIgnored pins the guard on the arriving weight. A zero or negative weight
// carries no occurrence, and letting one through would move Total — which is the N that
// total/(k+1) and every retention claim in this file is stated against — while a negative one would
// additionally push a stored counter below zero, where Top would report a key as having occurred a
// negative number of times.
//
// The zero-value receiver is asserted alongside it because it is the same decision: a MisraGries
// that was never constructed has no counter table, and the only non-panicking answer is to ignore
// the add. §12.3 — a hook that dies takes observability down with it.
func TestMG_NonPositiveWeightIgnored(t *testing.T) {
	m := NewMisraGries(4)
	m.Add("a", 0)
	m.Add("a", -5)

	require.Equal(t, int64(0), m.Total())
	require.Empty(t, m.Top(0))
	require.Equal(t, int64(0), m.MaxError())

	t.Run("the zero MisraGries is inert", func(t *testing.T) {
		var zero MisraGries
		zero.Add("x", 1)

		require.Equal(t, 0, zero.K())
		require.Equal(t, int64(0), zero.Total())
		require.Nil(t, zero.Top(0))
		require.NoError(t, zero.MergeFrom(&MisraGries{}),
			"two unconstructed summaries share a shape, so the merge is a no-op rather than a panic")
	})
}

// TestMG_FrequentItemGuarantee is the classical Misra-Gries promise, and the half that makes the
// structure worth having: ANY key whose true frequency exceeds total/(k+1) is still in the summary.
// It follows from the invariant that each decrement removes exactly (k+1)·d from the stream's
// accounted mass, so err ≤ total/(k+1) and a key with more mass than that cannot have been reduced
// to zero.
//
// The numbers are chosen so the claim is deterministic rather than probabilistic: at k = 16 over
// 10 000 unit adds the threshold is 10 000/17 = 588.2, and "hot" carries 1 200 — so its reported
// count is at least 1 200 − 588 = 612 whatever order the stream arrives in and whatever the map
// iteration produces.
func TestMG_FrequentItemGuarantee(t *testing.T) {
	const (
		k     = 16
		items = 10_000
		// hotEvery/hotRun place 3 hot adds in every 25 positions: 10 000 × 3/25 = 1 200.
		hotEvery = 25
		hotRun   = 3
		wantHot  = items / hotEvery * hotRun
	)

	m := NewMisraGries(k)
	hot := 0
	for i := 0; i < items; i++ {
		if i%hotEvery < hotRun {
			m.Add("hot", 1)
			hot++
			continue
		}
		m.Add("cold-"+strconv.Itoa(i), 1)
	}

	require.Equal(t, wantHot, hot, "fixture sanity: the pattern must place exactly 1 200 hot adds")
	require.Equal(t, int64(items), m.Total())

	threshold := float64(m.Total()) / float64(k+1)
	require.Greater(t, float64(wantHot), threshold,
		"fixture sanity: %d must exceed the retention threshold %.1f", wantHot, threshold)
	require.LessOrEqual(t, float64(m.MaxError()), threshold,
		"err ≤ total/(k+1) is the invariant the guarantee is derived from")

	top := m.Top(k)
	require.LessOrEqual(t, len(top), k)
	found := -1
	for _, c := range top {
		if c.Key == "hot" {
			found = c.Count
		}
	}
	require.NotEqual(t, -1, found, "a key with true frequency above total/(k+1) must be retained")
	require.GreaterOrEqual(t, int64(found), int64(wantHot)-m.MaxError())
	require.LessOrEqual(t, found, wantHot)
	t.Logf("hot: true=%d reported=%d MaxError=%d threshold=%.1f", wantHot, found, m.MaxError(), threshold)
}

// TestMG_NoFalsePositives is the other half of §6.2's "deterministic top-k with no false positives".
// Keys are stored verbatim and a key that was never Added can never enter the map, so every key Top
// reports really occurred in the stream — which is what lets a consumer act on the key itself rather
// than merely on the count.
//
// The two-sided bound is asserted on the same pass, because "no false positives" is only half the
// contract a caller needs: true(x) − MaxError ≤ reported(x) ≤ true(x) says the count is a lower
// bound and names how far below the truth it can be.
func TestMG_NoFalsePositives(t *testing.T) {
	const (
		k     = 4
		items = 1_000
		keys  = 50
		// hotEvery/hotRun put 300 of the 1 000 adds on key-0 and spread the other 700 over the
		// remaining 49. The skew is deliberate: k = 4 counters over a UNIFORM 50-key stream churn
		// until nothing survives, Top comes back empty and every assertion below passes vacuously.
		// At 300 adds key-0 is above the retention threshold 1000/5 = 200 and is guaranteed to be
		// there.
		hotEvery = 10
		hotRun   = 3
	)

	m := NewMisraGries(k)
	truth := make(map[string]int, keys)
	for i := 0; i < items; i++ {
		key := "key-0"
		if i%hotEvery >= hotRun {
			key = "key-" + strconv.Itoa(1+i%(keys-1))
		}
		m.Add(key, 1)
		truth[key]++
	}

	require.Len(t, truth, keys, "fixture sanity: the stream must cover all %d keys", keys)
	require.Equal(t, int64(items), m.Total())

	top := m.Top(0)
	require.NotEmpty(t, top, "fixture sanity: a skewed stream must leave something in the summary")
	for _, c := range top {
		trueCount, seen := truth[c.Key]
		require.True(t, seen, "Top reported %q, which never occurred in the stream", c.Key)
		require.LessOrEqual(t, c.Count, trueCount, "%q: a count is never an over-count", c.Key)
		require.GreaterOrEqual(t, int64(c.Count), int64(trueCount)-m.MaxError(),
			"%q: a count is never more than MaxError below the truth", c.Key)
	}
	t.Logf("k=%d over %d items: %d survivors, MaxError=%d", k, items, len(top), m.MaxError())
}

// TestMG_TopOrdering pins the total order Top imposes: count descending, then key ascending. The
// tie-break is the load-bearing half — two keys with equal counts would otherwise come back in map
// iteration order, and "the three hottest files" would be a different list on every call.
func TestMG_TopOrdering(t *testing.T) {
	m := NewMisraGries(8)
	m.Add("b", 5)
	m.Add("a", 5)
	m.Add("c", 9)

	require.Equal(t, []Counted{
		{Key: "c", Count: 9},
		{Key: "a", Count: 5},
		{Key: "b", Count: 5},
	}, m.Top(0))
}

// TestMG_TopN pins the truncation argument. n above the counter count returns everything rather
// than padding, and n ≤ 0 means "all" rather than "none" — the latter because a caller that has no
// particular limit in mind should get the whole summary, which is what CMS.HeavyHitters relies on
// when it asks for the candidate set.
func TestMG_TopN(t *testing.T) {
	const counters = 10

	m := NewMisraGries(16)
	for i := 0; i < counters; i++ {
		m.Add("k-"+strconv.Itoa(i), i+1)
	}

	require.Len(t, m.Top(3), 3)
	require.Len(t, m.Top(0), counters, "n <= 0 returns every counter")
	require.Len(t, m.Top(-1), counters)
	require.Len(t, m.Top(99), counters, "n above the counter count is not padded")
	require.Equal(t, []Counted{
		{Key: "k-9", Count: 10},
		{Key: "k-8", Count: 9},
		{Key: "k-7", Count: 8},
	}, m.Top(3), "truncation keeps the largest counts, not an arbitrary three")

	require.Nil(t, NewMisraGries(4).Top(0), "an empty summary reports nothing at all")
}

// The shared 5 000-item fixture. It is skewed rather than uniform — eight hot keys carrying about
// one seventh of the mass over a long tail of 400 — because the decrement phase is the only
// order-sensitive code in the summary and a uniform stream would barely reach it.
const (
	// mgStreamItems is the number of adds in the shared fixture.
	mgStreamItems = 5_000
	// mgStreamTailKeys is the size of the long tail.
	mgStreamTailKeys = 400
	// mgStreamHotKeys is the number of hot keys the fixture concentrates mass on.
	mgStreamHotKeys = 8
	// mgStreamHotEvery places one hot add every seventh position.
	mgStreamHotEvery = 7
	// mgStreamK is the counter budget the fixture is replayed under.
	mgStreamK = 64
)

// mgStreamKey maps stream position i to a key. It is a pure function of i so that every replay of
// the fixture — 32 of them in TestMG_DeterministicUnderMapOrder — is provably the same stream.
func mgStreamKey(i int) string {
	if i%mgStreamHotEvery == 0 {
		return "hot-" + strconv.Itoa(i%mgStreamHotKeys)
	}
	return "tail-" + strconv.Itoa(i%mgStreamTailKeys)
}

// mgStream replays the shared fixture into m and returns the exact truth table for it.
func mgStream(m *MisraGries) map[string]int {
	truth := make(map[string]int, mgStreamTailKeys+mgStreamHotKeys)
	for i := 0; i < mgStreamItems; i++ {
		key := mgStreamKey(i)
		m.Add(key, 1)
		truth[key]++
	}
	return truth
}

// TestMG_DeterministicUnderMapOrder is the test that earns §6.2's word "deterministic". Go
// randomizes map iteration order on purpose, and the decrement phase both reads (to find d) and
// writes (to subtract and delete) while iterating the counter map — so if the result depended on
// that order at all, two runs of the same stream would produce two different summaries and the
// on-disk sketch would not be reproducible.
//
// It does not: d is the MINIMUM over the whole map and over the arriving weight, the deleted set is
// exactly {c : c == d}, and every survivor becomes c − d. All three are order-independent by
// construction. The assertion is on MarshalBinary rather than on Top because the marshalled bytes
// are the strictest observable — they also pin the encoder's own key sort, which is what makes the
// golden fixtures of commit 6 and the store's content addressing stable.
func TestMG_DeterministicUnderMapOrder(t *testing.T) {
	const replays = 32

	var first []byte
	var firstErr int64
	for r := 0; r < replays; r++ {
		m := NewMisraGries(mgStreamK)
		mgStream(m)
		m.SetCreated(mgCreated)

		frame, err := m.MarshalBinary()
		require.NoError(t, err)
		if r == 0 {
			first, firstErr = frame, m.MaxError()
			require.Positive(t, m.MaxError(),
				"fixture sanity: the stream must actually reach the decrement phase")
			require.NotEmpty(t, m.Top(0))
			continue
		}
		require.Equal(t, first, frame, "replay %d produced different bytes for the same stream", r)
		require.Equal(t, firstErr, m.MaxError(), "replay %d accumulated a different error bound", r)
	}
	t.Logf("%d replays of the %d-item stream marshalled to %d identical bytes (MaxError=%d)",
		replays, mgStreamItems, len(first), firstErr)
}

// The merge fixture. Two disjoint key sets of the same shape: four clearly separated heavy keys and
// two weight-5 stragglers, so that the merged table exceeds k = 8 and the (k+1)-th-largest
// subtraction actually runs, while the heavy keys stay far enough above the error term that both
// the merged and the single-pass summary retain exactly them.
const mgMergeK = 8

type mgWeighted struct {
	key    string
	weight int
}

var (
	mgMergeStreamA = []mgWeighted{
		{"a1", 500}, {"a2", 400}, {"a3", 300}, {"a4", 200}, {"an1", 5}, {"an2", 5},
	}
	mgMergeStreamB = []mgWeighted{
		{"b1", 450}, {"b2", 350}, {"b3", 250}, {"b4", 150}, {"bn1", 5}, {"bn2", 5},
	}
)

// mgFeed adds every entry of stream to m, in order.
func mgFeed(m *MisraGries, stream []mgWeighted) {
	for _, w := range stream {
		m.Add(w.key, w.weight)
	}
}

// TestMG_MergeFrom pins the mergeable-summary construction SP-16 calls for Phase 7's cross-session
// warm start. Merging is not "add the counters": the pairwise sum can hold up to 2k keys, so the
// (k+1)-th largest count is subtracted from every counter and the non-positives dropped. That
// subtraction is what preserves the error bound — it removes at least (k+1)·d of accounted mass,
// exactly as a decrement during Add does, so err ≤ total/(k+1) still holds afterwards.
//
// The trace, computable by hand:
//
//	m1 = {a1:500, a2:400, a3:300, a4:200, an1:5, an2:5}   total 1410, err 0
//	m2 = {b1:450, b2:350, b3:250, b4:150, bn1:5, bn2:5}   total 1210, err 0
//	pairwise sum: 12 counters; the 9th largest is 5, so d = 5
//	survivors: a1:495 a2:395 a3:295 a4:195 b1:445 b2:345 b3:245 b4:145; the four 5s drop to 0
//	merged total 2620, merged err 5
func TestMG_MergeFrom(t *testing.T) {
	m1, m2 := NewMisraGries(mgMergeK), NewMisraGries(mgMergeK)
	mgFeed(m1, mgMergeStreamA)
	mgFeed(m2, mgMergeStreamB)

	require.Equal(t, int64(1_410), m1.Total())
	require.Equal(t, int64(1_210), m2.Total())
	require.Equal(t, int64(0), m1.MaxError(), "fixture sanity: six keys fit in eight counters")
	require.Equal(t, int64(0), m2.MaxError())

	single := NewMisraGries(mgMergeK)
	mgFeed(single, mgMergeStreamA)
	mgFeed(single, mgMergeStreamB)

	inputErr1, inputErr2 := m1.MaxError(), m2.MaxError()
	require.NoError(t, m1.MergeFrom(m2))

	require.Equal(t, single.Total(), m1.Total(), "a merge must account for every weight in both streams")
	require.Equal(t, int64(2_620), m1.Total())
	require.Equal(t, int64(5), m1.MaxError(), "the 9th largest of the pairwise sum is 5")
	require.GreaterOrEqual(t, m1.MaxError(), inputErr1)
	require.GreaterOrEqual(t, m1.MaxError(), inputErr2)
	require.LessOrEqual(t, float64(m1.MaxError()), float64(m1.Total())/float64(mgMergeK+1),
		"the merge must preserve err ≤ total/(k+1)")

	merged := m1.Top(0)
	require.Len(t, merged, mgMergeK, "the merged table must be trimmed back to k")
	require.Equal(t, Counted{Key: "a1", Count: 495}, merged[0])

	inSingle := make(map[string]int, mgMergeK)
	for _, c := range single.Top(0) {
		inSingle[c.Key] = c.Count
	}
	for _, c := range merged {
		_, ok := inSingle[c.Key]
		require.True(t, ok, "merged summary retained %q, which the single pass dropped", c.Key)
	}
	t.Logf("merged: total=%d MaxError=%d keys=%d; single pass: total=%d MaxError=%d",
		m1.Total(), m1.MaxError(), len(merged), single.Total(), single.MaxError())
}

// TestMG_MergeShapeMismatch asserts a merge across differing counter budgets is refused rather than
// attempted. Two summaries built with different k carry different error bounds — err ≤ total/(k+1)
// is a statement ABOUT k — so a pairwise sum of their counters would satisfy neither bound, and
// every count that came out of it would still look like a plausible frequency. A nil source is the
// same refusal, not a panic.
func TestMG_MergeShapeMismatch(t *testing.T) {
	m := NewMisraGries(mgMergeK)
	require.ErrorIs(t, m.MergeFrom(NewMisraGries(mgMergeK/2)), ErrShapeMismatch, "differing k")
	require.ErrorIs(t, m.MergeFrom(nil), ErrShapeMismatch,
		"a nil source is a shape mismatch, not a panic")
}

// TestMG_KeyTruncation pins the bound on one key. MaxMGKeyBytes exists because a Misra-Gries frame's
// size depends on its KEY LENGTHS as well as on its counter count (doc.go's ceiling-rule exception),
// so an unbounded key would be the one way a legally constructed summary could grow past
// MaxFrameBytes and become unsaveable.
//
// The truncated key must round-trip, because truncation happens on the way IN: what is stored is
// what is reported and what is written to disk, and a decoder that refused its own writer's output
// would make the sketch unloadable.
func TestMG_KeyTruncation(t *testing.T) {
	long := strings.Repeat("p", MaxMGKeyBytes+10)

	m := NewMisraGries(4)
	m.Add(long, 3)
	m.SetCreated(mgCreated)

	top := m.Top(0)
	require.Len(t, top, 1)
	require.Len(t, top[0].Key, MaxMGKeyBytes, "the stored key is truncated to exactly the bound")
	require.Equal(t, long[:MaxMGKeyBytes], top[0].Key)
	require.Equal(t, 3, top[0].Count)

	frame, err := m.MarshalBinary()
	require.NoError(t, err)

	got := NewMisraGries(4)
	require.NoError(t, got.UnmarshalBinary(frame))
	require.Equal(t, top, got.Top(0), "a truncated key must survive the round trip verbatim")
}

// mgCreated is the fixed construction stamp every marshalling test in this file uses, so that the
// bytes under comparison differ only where the test intends them to.
const mgCreated = core.UnixMilli(1_700_000_000_000)

// TestMG_MarshalRoundTrip is the §6.2 promise in one test: a sketch that cannot be re-read is worse
// than no sketch. Re-marshalling must be BYTE-identical, which for this summary means two sorts
// have to hold — EncodeHeader's params sort and the body's strictly-ascending key order — because
// Go randomizes map iteration and neither the counter map nor the params map has an order of its
// own.
//
// MaxError is asserted across the round trip specifically: it travels as a header param rather than
// in the body, and a decoder that dropped it would produce a summary whose counts still looked
// right but whose error bound had silently become 0 — the one field whose loss makes every other
// number in the summary unfalsifiable.
func TestMG_MarshalRoundTrip(t *testing.T) {
	m := NewMisraGries(mgStreamK)
	truth := mgStream(m)
	m.SetCreated(mgCreated)

	frame, err := m.MarshalBinary()
	require.NoError(t, err)

	got := NewMisraGries(1)
	require.NoError(t, got.UnmarshalBinary(frame))

	again, err := got.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, frame, again, "re-marshalling a decoded summary must be byte-identical")

	require.Equal(t, m.Top(0), got.Top(0))
	require.Equal(t, m.Total(), got.Total())
	require.Equal(t, m.MaxError(), got.MaxError())
	require.Equal(t, m.K(), got.K())
	require.Equal(t, m.Header(), got.Header())
	require.Equal(t, mgCreated, got.Header().Created)
	require.Equal(t, uint64(m.Total()), got.Header().Count)

	// The decoded summary still answers within the bound, which is the property a consumer actually
	// reads a loaded sketch for.
	for _, c := range got.Top(0) {
		require.LessOrEqual(t, c.Count, truth[c.Key])
		require.GreaterOrEqual(t, int64(c.Count), int64(truth[c.Key])-got.MaxError())
	}
	t.Logf("k=%d over %d items: %d entries, frame %d bytes, MaxError=%d",
		mgStreamK, mgStreamItems, len(got.Top(0)), len(frame), got.MaxError())
}

// mgParams returns the two params a Misra-Gries frame carries.
func mgParams(k, errBound float64) map[string]float64 {
	return map[string]float64{paramMGK: k, paramErr: errBound}
}

// mgFrame assembles a QPKS frame carrying params and count verbatim. It exists so the rejection
// table below states only what is wrong with each row: every frame it builds is structurally
// perfect — correct magic, version, kind, param order and CRC — so the sentinel the decoder returns
// can only be about the Misra-Gries-specific claim under test.
func mgFrame(t *testing.T, params map[string]float64, count uint64, body []byte) []byte {
	t.Helper()
	frame, err := EncodeHeader(Header{
		Ver:    FormatVersion,
		Kind:   KindMisraGries,
		Params: params,
		Count:  count,
	}, body)
	require.NoError(t, err, "fixture sanity: the frame itself must be well-formed")
	return frame
}

// mgEntry is one body entry, with the key length written independently of the key so that a row can
// declare a length its key does not have.
type mgEntry struct {
	key    string
	keyLen int
	count  int64
}

// mgBody assembles a body from a declared entry count and a list of entries. Both are written
// verbatim: the count need not match the number of entries, which is how the "entry count above k"
// and "entries overrun the body" rows are built.
func mgBody(declared uint32, entries ...mgEntry) []byte {
	b := binary.LittleEndian.AppendUint32(nil, declared)
	for _, e := range entries {
		b = binary.LittleEndian.AppendUint16(b, uint16(e.keyLen))
		b = append(b, e.key...)
		b = binary.LittleEndian.AppendUint64(b, uint64(e.count))
	}
	return b
}

// TestMG_UnmarshalRejects walks every way a well-framed QPKS blob can still describe a summary this
// build must refuse to build. The frames are assembled through EncodeHeader, so each one has a
// correct CRC and a self-consistent length block: the ONLY thing wrong with them is the row's own
// claim, which is what makes each row isolate one check.
//
// The sentinel split is the one errors.go draws. A structurally intact frame that says something
// impossible — a count of zero, an unsorted key, an err param that is not an integer — is
// ErrMalformed. A frame whose declared lengths do not add up to the body it came with is
// ErrTruncated, the interrupted-write signature.
func TestMG_UnmarshalRejects(t *testing.T) {
	const k = 2
	okParams := mgParams(k, 0)

	for _, tc := range []struct {
		name    string
		params  map[string]float64
		count   uint64
		body    []byte
		wantErr error
	}{
		{
			"entry count above k", okParams, 3,
			mgBody(3,
				mgEntry{key: "a", keyLen: 1, count: 1},
				mgEntry{key: "b", keyLen: 1, count: 1},
				mgEntry{key: "c", keyLen: 1, count: 1}),
			ErrMalformed,
		},
		{
			"keys not ascending", okParams, 2,
			mgBody(2, mgEntry{key: "b", keyLen: 1, count: 1}, mgEntry{key: "a", keyLen: 1, count: 1}),
			ErrMalformed,
		},
		{
			"duplicate key", okParams, 2,
			mgBody(2, mgEntry{key: "a", keyLen: 1, count: 1}, mgEntry{key: "a", keyLen: 1, count: 1}),
			ErrMalformed,
		},
		{"count zero", okParams, 1, mgBody(1, mgEntry{key: "a", keyLen: 1, count: 0}), ErrMalformed},
		{"count negative", okParams, 1, mgBody(1, mgEntry{key: "a", keyLen: 1, count: -1}), ErrMalformed},
		{
			"count above the ceiling", okParams, 1,
			mgBody(1, mgEntry{key: "a", keyLen: 1, count: maxMGCount + 1}), ErrMalformed,
		},
		{"key length zero", okParams, 1, mgBody(1, mgEntry{key: "", keyLen: 0, count: 1}), ErrMalformed},
		{
			"key length above MaxMGKeyBytes", okParams, 1,
			mgBody(1, mgEntry{key: "a", keyLen: MaxMGKeyBytes + 1, count: 1}), ErrMalformed,
		},
		{
			"key runs past the body", okParams, 1,
			mgBody(1, mgEntry{key: "a", keyLen: 64, count: 1}), ErrTruncated,
		},
		{
			"trailing bytes after the last entry", okParams, 1,
			append(mgBody(1, mgEntry{key: "a", keyLen: 1, count: 1}), 0), ErrTruncated,
		},
		{"body shorter than the entry count", okParams, 0, []byte{0, 0, 0}, ErrTruncated},
		{"empty body", okParams, 0, nil, ErrTruncated},
		{"k missing", map[string]float64{paramErr: 0}, 0, mgBody(0), ErrMalformed},
		{"k zero", mgParams(0, 0), 0, mgBody(0), ErrMalformed},
		{"k above MaxMGCounters", mgParams(MaxMGCounters+1, 0), 0, mgBody(0), ErrMalformed},
		{"k not an integer", mgParams(2.5, 0), 0, mgBody(0), ErrMalformed},
		{"err missing", map[string]float64{paramMGK: k}, 0, mgBody(0), ErrMalformed},
		{"err negative", mgParams(k, -1), 0, mgBody(0), ErrMalformed},
		{"err not an integer", mgParams(k, 0.5), 0, mgBody(0), ErrMalformed},
		{"err NaN", mgParams(k, math.NaN()), 0, mgBody(0), ErrMalformed},
		{"err above the ceiling", mgParams(k, math.MaxFloat64), 0, mgBody(0), ErrMalformed},
		{"total above the ceiling", okParams, math.MaxUint64, mgBody(0), ErrMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMisraGries(k)
			m.Add("survivor", 1)
			require.ErrorIs(t, m.UnmarshalBinary(mgFrame(t, tc.params, tc.count, tc.body)), tc.wantErr)
			require.Equal(t, []Counted{{Key: "survivor", Count: 1}}, m.Top(0),
				"a refused frame must not half-replace the summary it was being loaded into")
		})
	}

	// A frame declaring the maximum counter count gets its own case, because it is also where "no
	// allocation before rejection" is measured. MaxMGCounters entries would be a million-key map, so
	// a decoder that sized its make() before walking the body would be an out-of-memory kill of the
	// daemon rather than a returned error. TotalAlloc is cumulative bytes allocated, so its delta
	// across the call bounds what the rejection actually cost.
	t.Run("a million declared entries in a four-byte body", func(t *testing.T) {
		frame := mgFrame(t, mgParams(MaxMGCounters, 0), 0, mgBody(MaxMGCounters))
		m := NewMisraGries(k)

		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		err := m.UnmarshalBinary(frame)
		runtime.ReadMemStats(&after)

		require.ErrorIs(t, err, ErrTruncated)
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("rejecting a %d-entry frame allocated %d bytes", MaxMGCounters, allocated)
		require.Less(t, allocated, uint64(1<<20),
			"the entries must be walked and refused before the counter map is allocated")
	})

	// The wrong Kind is refused before any body is looked at, because a CMS frame's body would
	// otherwise be walked as if it were a list of Misra-Gries entries.
	t.Run("kind mismatch", func(t *testing.T) {
		other, err := NewCMS(0.5, 0.5).MarshalBinary()
		require.NoError(t, err)
		require.ErrorIs(t, NewMisraGries(k).UnmarshalBinary(other), ErrKindMismatch)
	})

	// A nil receiver reports rather than panics: §12.3, a hook that dies takes observability with
	// it. The unsized summary is refused for the reason bloom.go states — marshallable means
	// re-readable, and a frame declaring k = 0 is one this build's own decoder is guaranteed to
	// reject, so refusing to write it is louder and more recoverable than discovering it dead on the
	// next restart.
	var nilMG *MisraGries
	require.ErrorIs(t, nilMG.UnmarshalBinary(nil), ErrMalformed)
	_, err := nilMG.MarshalBinary()
	require.ErrorIs(t, err, ErrMalformed)

	_, err = new(MisraGries).MarshalBinary()
	require.ErrorIs(t, err, ErrMalformed, "an unsized summary must not be written")

	// The key-length ceiling on the way OUT. Add truncates, so this state is reachable only by
	// writing the map directly — and the guard exists because decodeV1 refuses a key longer than
	// MaxMGKeyBytes: without it the frame would be written cleanly and then be unreadable by the
	// build that wrote it, which for a permanent-memory file is the worst outcome available. It is
	// also what makes the encoder's uint16 length conversion provably lossless.
	oversize := NewMisraGries(4)
	oversize.counters[strings.Repeat("q", MaxMGKeyBytes+1)] = 1
	_, err = oversize.MarshalBinary()
	require.ErrorIs(t, err, ErrTooLarge)

	// The two arithmetic ceilings on the way out, which are the same ceilings decodeV1 enforces on
	// the way in. Add saturates, so these states are reachable through MergeFrom — which adds two
	// summaries' counters and totals as they stand, and two frames decoded at the ceiling land past
	// it — or by writing the field. The fields are written here, because building a 2^61-count
	// summary through the exported surface would take longer than the whole suite.
	//
	// Both directions of both guards are covered: past the ceiling, and already negative.
	// maxMGCount+1 is a legal int constant on every platform this ships to (00-ARCHITECTURE §2.6's
	// six release targets are all 64-bit, so maxMGCount is 2^61 rather than math.MaxInt).
	overTotal := NewMisraGries(4)
	overTotal.Add("a", 1)
	overTotal.total = maxMGCount + 1
	_, err = overTotal.MarshalBinary()
	require.ErrorIs(t, err, ErrMalformed, "a total past the ceiling must not be written")

	overTotal.total = -1
	_, err = overTotal.MarshalBinary()
	require.ErrorIs(t, err, ErrMalformed, "nor must a total that has already wrapped")

	overCount := NewMisraGries(4)
	overCount.Add("a", 1)
	overCount.counters["a"] = maxMGCount + 1
	_, err = overCount.MarshalBinary()
	require.ErrorIs(t, err, ErrMalformed, "a counter past the ceiling must not be written")

	overCount.counters["a"] = -1
	_, err = overCount.MarshalBinary()
	require.ErrorIs(t, err, ErrMalformed, "nor must a counter that has already wrapped")
}
