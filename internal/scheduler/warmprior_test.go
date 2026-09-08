package scheduler_test

import (
	"math"
	"testing"

	"github.com/qompack/qompack/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// authorizedEvidence is history that clears every floor in DefaultWarmStartPolicy, so a test that
// wants to fail exactly one rung can say so instead of restating four fields.
func authorizedEvidence() scheduler.PriorEvidence {
	return scheduler.PriorEvidence{
		Sessions:     5,
		Observations: 200,
		AgeSeconds:   0,
		Authorized:   true,
	}
}

// TestWarmStart_UnauthorizedIsARefusalNotADiscount pins the first rung: consent has no discount
// rate. History the caller could not authorize carries zero weight, whatever else is true of it.
func TestWarmStart_UnauthorizedIsARefusalNotADiscount(t *testing.T) {
	t.Parallel()

	e := authorizedEvidence()
	e.Authorized = false

	got := scheduler.WarmStart(e, scheduler.DefaultWarmStartPolicy())
	require.False(t, got.Apply)
	require.Zero(t, got.Weight)
	require.Contains(t, got.Label, "not authorized")

	// The zero PriorEvidence is unauthorized, so forgetting to fill it in refuses too.
	require.False(t, scheduler.WarmStart(scheduler.PriorEvidence{}, scheduler.DefaultWarmStartPolicy()).Apply)
}

// TestWarmStart_RefusalLadder walks every rung, and pins that a refusal always carries a
// zero weight and a non-empty label.
func TestWarmStart_RefusalLadder(t *testing.T) {
	t.Parallel()

	p := scheduler.DefaultWarmStartPolicy()
	for _, tc := range []struct {
		name       string
		mutate     func(*scheduler.PriorEvidence)
		wantInLabe string
	}{
		{"too few sessions", func(e *scheduler.PriorEvidence) { e.Sessions = 2 }, "below the 3/30 floor"},
		{"too few observations", func(e *scheduler.PriorEvidence) { e.Observations = 29 }, "below the 3/30 floor"},
		{"no sessions at all", func(e *scheduler.PriorEvidence) { e.Sessions = 0 }, "below the 3/30 floor"},
		{"past the horizon", func(e *scheduler.PriorEvidence) { e.AgeSeconds = p.MaxAgeSeconds + 1 }, "past the"},
		{"unknown age", func(e *scheduler.PriorEvidence) { e.AgeSeconds = math.NaN() }, "age of the newest observation is unknown"},
		{"negative age", func(e *scheduler.PriorEvidence) { e.AgeSeconds = -1 }, "age of the newest observation is unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := authorizedEvidence()
			tc.mutate(&e)
			got := scheduler.WarmStart(e, p)
			require.False(t, got.Apply)
			require.Zero(t, got.Weight, "a refusal must carry no weight, so a caller that ignores Apply is still right")
			require.Contains(t, got.Label, tc.wantInLabe)
		})
	}
}

// TestWarmStart_WeightNeverExceedsTheCeiling is the property that keeps history from outvoting
// the present. No policy a caller can construct — including a deliberately hostile one — produces
// a weight above maxWeightCeiling, so the whole of a project's history is never worth more than
// half of one observation the current session made.
func TestWarmStart_WeightNeverExceedsTheCeiling(t *testing.T) {
	t.Parallel()

	for _, p := range []scheduler.WarmStartPolicy{
		scheduler.DefaultWarmStartPolicy(),
		{MinSessions: 1, MinObservations: 1, MaxWeight: 1, MaxAgeSeconds: 0},
		{MinSessions: 1, MinObservations: 1, MaxWeight: 1000, HalfLifeSeconds: -5},
		{MinSessions: -3, MinObservations: -3, MaxWeight: math.Inf(1)},
		{MinSessions: 1, MinObservations: 1, MaxWeight: math.NaN()},
	} {
		got := scheduler.WarmStart(authorizedEvidence(), p)
		require.LessOrEqual(t, got.Weight, 0.5, "policy %+v produced weight %v", p, got.Weight)
		require.Less(t, got.Weight, 1.0, "a prior must never reach parity with one observation")
		require.GreaterOrEqual(t, got.Weight, 0.0)
	}
}

// TestWarmStartPolicy_Clamp pins that a mis-set policy cannot express an unsafe decision, since
// the failure mode of one is silent rather than an error.
func TestWarmStartPolicy_Clamp(t *testing.T) {
	t.Parallel()

	got := scheduler.WarmStartPolicy{
		MinSessions:     -1,
		MinObservations: 0,
		HalfLifeSeconds: -10,
		MaxWeight:       9,
		MaxAgeSeconds:   -1,
	}.Clamp()

	require.Equal(t, 1, got.MinSessions)
	require.Equal(t, 1, got.MinObservations)
	require.Zero(t, got.HalfLifeSeconds)
	require.Equal(t, 0.5, got.MaxWeight)
	require.Zero(t, got.MaxAgeSeconds)

	nan := scheduler.WarmStartPolicy{
		HalfLifeSeconds: math.NaN(), MaxWeight: math.NaN(), MaxAgeSeconds: math.NaN(),
	}.Clamp()
	require.Zero(t, nan.HalfLifeSeconds)
	require.Zero(t, nan.MaxWeight)
	require.Zero(t, nan.MaxAgeSeconds)

	// Clamping is idempotent, so a policy that has already been through it is unchanged.
	require.Equal(t, got, got.Clamp())
}

// TestWarmStart_WeightHalvesEveryHalfLife pins the decay curve itself, so a change to it has to be
// deliberate.
func TestWarmStart_WeightHalvesEveryHalfLife(t *testing.T) {
	t.Parallel()

	p := scheduler.DefaultWarmStartPolicy()
	fresh := scheduler.WarmStart(authorizedEvidence(), p)
	require.True(t, fresh.Apply)
	require.InDelta(t, p.MaxWeight, fresh.Weight, 1e-9, "a same-day prior carries the full cap")

	e := authorizedEvidence()
	e.AgeSeconds = p.HalfLifeSeconds
	require.InDelta(t, p.MaxWeight/2, scheduler.WarmStart(e, p).Weight, 1e-9)

	e.AgeSeconds = 3 * p.HalfLifeSeconds
	require.InDelta(t, p.MaxWeight/8, scheduler.WarmStart(e, p).Weight, 1e-9)

	// Weight is monotonically non-increasing in age, right up to the horizon.
	prev := math.Inf(1)
	for age := 0.0; age <= p.MaxAgeSeconds; age += p.HalfLifeSeconds / 4 {
		e.AgeSeconds = age
		w := scheduler.WarmStart(e, p).Weight
		require.LessOrEqual(t, w, prev, "weight rose at age %v", age)
		prev = w
	}
}

// TestWarmStart_AgeingCanBeDisabledButTheCapStillHolds pins that a zero half-life turns ageing off
// — what a replay over a fixed corpus wants — without lifting the weight ceiling.
func TestWarmStart_AgeingCanBeDisabledButTheCapStillHolds(t *testing.T) {
	t.Parallel()

	p := scheduler.DefaultWarmStartPolicy()
	p.HalfLifeSeconds = 0
	e := authorizedEvidence()
	e.AgeSeconds = p.MaxAgeSeconds

	got := scheduler.WarmStart(e, p)
	require.True(t, got.Apply)
	require.InDelta(t, p.MaxWeight, got.Weight, 1e-9)
	require.LessOrEqual(t, got.Weight, 0.5)
}

// TestWarmStart_LabelIsNeverEmpty pins the "never silent" rule: a warm-started number must always
// be distinguishable from a measured one.
func TestWarmStart_LabelIsNeverEmpty(t *testing.T) {
	t.Parallel()

	p := scheduler.DefaultWarmStartPolicy()
	for _, e := range []scheduler.PriorEvidence{
		{},
		authorizedEvidence(),
		{Sessions: 99, Observations: 99, Authorized: true, AgeSeconds: math.NaN()},
		{Sessions: 99, Observations: 99, Authorized: true, AgeSeconds: 1e9},
	} {
		require.NotEmpty(t, scheduler.WarmStart(e, p).Label, "%+v", e)
	}

	applied := scheduler.WarmStart(authorizedEvidence(), p)
	require.Contains(t, applied.Label, "warm prior at weight")
	require.Contains(t, applied.Label, "5 session(s)")
	require.Contains(t, applied.Label, "200 observation(s)")
}

// TestBlend_ThePriorIsSwampedByObservation is the "not inherited truth" property expressed
// arithmetically. The prior owns the answer only while the session has nothing of its own, and its
// influence collapses as soon as it does.
func TestBlend_ThePriorIsSwampedByObservation(t *testing.T) {
	t.Parallel()

	d := scheduler.WarmStart(authorizedEvidence(), scheduler.DefaultWarmStartPolicy())
	require.True(t, d.Apply)

	const prior, observed = 100.0, 0.0

	require.InDelta(t, prior, scheduler.Blend(prior, observed, 0, d), 1e-9,
		"with nothing observed, the prior is the answer")

	one := scheduler.Blend(prior, observed, 1, d)
	require.Less(t, one, prior*0.25, "one observation already outweighs the prior fourfold")

	ten := scheduler.Blend(prior, observed, 10, d)
	require.Less(t, ten, prior*0.03, "ten observations move the answer to within 3% of measurement")

	// Influence is monotonically decreasing in the observation count.
	prev := math.Inf(1)
	for n := 0; n <= 50; n++ {
		got := scheduler.Blend(prior, observed, n, d)
		require.LessOrEqual(t, got, prev, "influence rose at n=%d", n)
		prev = got
	}
}

// TestBlend_EveryDegenerateCaseReturnsTheObservedValue pins the single fallback: when the prior
// cannot be used, the answer is exactly the one the caller would have had without this file.
func TestBlend_EveryDegenerateCaseReturnsTheObservedValue(t *testing.T) {
	t.Parallel()

	applied := scheduler.WarmStart(authorizedEvidence(), scheduler.DefaultWarmStartPolicy())
	const prior, observed = 100.0, 7.0

	for _, tc := range []struct {
		name  string
		prior float64
		count int
		d     scheduler.WarmStartDecision
	}{
		{"a refused decision", prior, 3, scheduler.WarmStartDecision{}},
		{"a zero weight", prior, 3, scheduler.WarmStartDecision{Apply: true}},
		{"a negative weight", prior, 3, scheduler.WarmStartDecision{Apply: true, Weight: -1}},
		{"a NaN weight", prior, 3, scheduler.WarmStartDecision{Apply: true, Weight: math.NaN()}},
		{"a negative observation count", prior, -1, applied},
		{"a NaN prior", math.NaN(), 3, applied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, observed, scheduler.Blend(tc.prior, observed, tc.count, tc.d))
		})
	}

	require.True(t, math.IsNaN(scheduler.Blend(prior, math.NaN(), 3, applied)),
		"a NaN observation propagates rather than becoming a fabricated zero")
}

// TestBlend_IsACountWeightedMean pins the arithmetic itself against a hand-computed value, so the
// formula cannot drift while the inequalities above still happen to hold.
func TestBlend_IsACountWeightedMean(t *testing.T) {
	t.Parallel()

	d := scheduler.WarmStartDecision{Apply: true, Weight: 0.25}
	// (10*0.25 + 20*3) / (0.25 + 3) = 62.5 / 3.25
	require.InDelta(t, 62.5/3.25, scheduler.Blend(10, 20, 3, d), 1e-9)
	require.InDelta(t, 10, scheduler.Blend(10, 20, 0, d), 1e-9)
}
