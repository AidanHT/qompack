package scheduler_test

import (
	"math"
	"math/rand"
	"testing"

	"github.com/qompack/qompack/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// This file is SP-16 M6-G16-D: the ablation the warm-prior gate asks for, comparing the optional
// policy against the simple baseline on held-out projects.
//
// It is a REPORTING test, and that is deliberate. §4 says a new heuristic "requires declared
// baseline/objective, privacy/resource review, ablation and separate service authorization where
// applicable" and that "lack of benefit leaves them disabled", so a test that asserted the prior
// wins would be asserting the conclusion the ablation exists to reach. What is asserted here is
// only what the DESIGN claims — the advantage is confined to the low-observation regime, and the
// prior is swamped afterwards — plus determinism, so the numbers in the log are reproducible
// evidence rather than a sample of the day's noise.
//
// The objective, declared up front: predict a project's true per-turn cost θ at the START of a
// session, before that session has observed anything of its own, and keep predicting as
// observations arrive. The error measure is mean absolute error against θ.
//
// The baseline is the honest simple one: a single global default for every project until the
// session has an observation of its own, then that session's running mean. It is what the system
// does today with warmPrior off, which is the only baseline a gate decision can be made against.

// The ablation's fixed parameters. They are constants rather than flags so that the numbers in the
// log are the numbers anyone re-running this test gets.
const (
	// ablationSeed fixes the PRNG. Every number this file reports is a function of it.
	ablationSeed = 20260908
	// ablationProjects is how many held-out projects are simulated.
	//
	// It is large because the effect being measured is small. At 200 projects the standard error
	// on each mean absolute error is about 0.026 against a difference of roughly 0.04, so the
	// n=0 comparison came out with the wrong SIGN on the first run — a sampling artefact that
	// would have been quoted as a gate result. A gate decision has to rest on a number whose
	// noise is well under the effect, and this is what that costs.
	ablationProjects = 20000
	// ablationHorizon is how many observations of the current session are simulated per project.
	ablationHorizon = 12
	// ablationNoise is the standard deviation of one observation around the project's true θ.
	ablationNoise = 0.35
	// ablationPriorNoise is how far the prior — itself an estimate from earlier sessions — sits
	// from θ. It is larger than ablationNoise on purpose: a prior that were more accurate than a
	// direct observation would make the whole comparison uninteresting.
	ablationPriorNoise = 0.55
	// ablationGlobalDefault is the one number the baseline knows before it observes anything: the
	// mean of the θ distribution. This is the baseline at its STRONGEST — a real deployment would
	// have to guess it — and beating a strong baseline is the only result worth acting on.
	ablationGlobalDefault = 1.0
	// ablationThetaSpread is the standard deviation of θ across projects. It is what a per-project
	// prior could in principle exploit and a single global default cannot.
	ablationThetaSpread = 0.60
)

// ablationRow is one horizon's result.
type ablationRow struct {
	// N is how many observations the current session has made.
	N int
	// BaselineMAE and WarmMAE are the two mean absolute errors at that N.
	BaselineMAE, WarmMAE float64
}

// Improvement is the fraction of the baseline's error the warm prior removes. It is negative when
// the prior makes things worse, which is a result the gate has to be able to record.
func (r ablationRow) Improvement() float64 {
	if r.BaselineMAE == 0 {
		return 0
	}
	return (r.BaselineMAE - r.WarmMAE) / r.BaselineMAE
}

// runAblation simulates ablationProjects projects and returns one row per horizon.
func runAblation(t *testing.T, p scheduler.WarmStartPolicy, e scheduler.PriorEvidence) []ablationRow {
	t.Helper()

	rng := rand.New(rand.NewSource(ablationSeed)) //nolint:gosec // a fixed seed is the point
	baselineErr := make([]float64, ablationHorizon+1)
	warmErr := make([]float64, ablationHorizon+1)

	d := scheduler.WarmStart(e, p)

	for proj := 0; proj < ablationProjects; proj++ {
		theta := ablationGlobalDefault + rng.NormFloat64()*ablationThetaSpread
		prior := theta + rng.NormFloat64()*ablationPriorNoise

		sum := 0.0
		for n := 0; n <= ablationHorizon; n++ {
			// The baseline: the global default until this session has seen something, then its
			// own running mean.
			baseline := ablationGlobalDefault
			if n > 0 {
				baseline = sum / float64(n)
			}
			warm := scheduler.Blend(prior, baseline, n, d)

			baselineErr[n] += math.Abs(baseline - theta)
			warmErr[n] += math.Abs(warm - theta)

			sum += theta + rng.NormFloat64()*ablationNoise
		}
	}

	rows := make([]ablationRow, 0, ablationHorizon+1)
	for n := 0; n <= ablationHorizon; n++ {
		rows = append(rows, ablationRow{
			N:           n,
			BaselineMAE: baselineErr[n] / ablationProjects,
			WarmMAE:     warmErr[n] / ablationProjects,
		})
	}
	return rows
}

// TestWarmPriorAblation_M6G16D runs the ablation and records it.
//
// The log output IS the gate evidence: plans/V5-SP-16-M6-evidence.md quotes it, and the numbers
// are reproducible because the seed and every parameter are constants in this file.
func TestWarmPriorAblation_M6G16D(t *testing.T) {
	t.Parallel()

	policy := scheduler.DefaultWarmStartPolicy()
	evidence := scheduler.PriorEvidence{Sessions: 5, Observations: 200, AgeSeconds: 0, Authorized: true}

	d := scheduler.WarmStart(evidence, policy)
	require.True(t, d.Apply, "the ablation must actually exercise an applied prior")
	t.Logf("policy: %+v", policy)
	t.Logf("decision: %s", d.Label)
	t.Logf("%-4s %14s %14s %12s", "n", "baseline MAE", "warm MAE", "improvement")

	rows := runAblation(t, policy, evidence)
	for _, r := range rows {
		t.Logf("%-4d %14.4f %14.4f %11.1f%%", r.N, r.BaselineMAE, r.WarmMAE, r.Improvement()*100)
	}

	// What is asserted is the DESIGN's claim, never the gate's verdict.
	//
	// Whether the prior is worth enabling is what the ablation measures, and a test that required
	// it to win would be asserting the answer instead of reporting it — §4's "lack of benefit
	// leaves them disabled" is only meaningful if a lack of benefit can actually be observed here.
	// So the verdict goes to the log and the disposition goes in the evidence document.
	//
	// The design's own claim is falsifiable and is asserted: whatever the prior contributes must
	// FADE as the session observes its own data. That is the "not inherited truth" property in
	// measured form, and it would fail loudly if Blend ever stopped being swamped.
	require.Less(t, math.Abs(rows[ablationHorizon].Improvement()), math.Abs(rows[1].Improvement()),
		"the prior's contribution must shrink as the session observes its own data")
	require.Less(t, math.Abs(rows[ablationHorizon].Improvement()), 0.05,
		"by the end of the horizon the prior must have moved the answer by under five percent")

	t.Logf("M6-G16-D disposition input: improvement at n=0 is %+.1f%%, at n=1 is %+.1f%%, at n=%d is %+.1f%%",
		rows[0].Improvement()*100, rows[1].Improvement()*100,
		ablationHorizon, rows[ablationHorizon].Improvement()*100)
}

// TestWarmPriorAblation_IsDeterministic pins that the numbers quoted as evidence are reproducible.
// An ablation whose result moved between runs could not support a gate decision at all.
func TestWarmPriorAblation_IsDeterministic(t *testing.T) {
	t.Parallel()

	policy := scheduler.DefaultWarmStartPolicy()
	evidence := scheduler.PriorEvidence{Sessions: 5, Observations: 200, Authorized: true}

	require.Equal(t, runAblation(t, policy, evidence), runAblation(t, policy, evidence))
}

// TestWarmPriorAblation_ARefusedPriorIsExactlyTheBaseline is the disabled-alternative arm of the
// ablation, and it is the one that makes the rollback claim testable: with the policy refused,
// every prediction is byte-identical to the baseline's. Turning warmPrior off is therefore not a
// degraded mode, it is the unmodified system.
func TestWarmPriorAblation_ARefusedPriorIsExactlyTheBaseline(t *testing.T) {
	t.Parallel()

	policy := scheduler.DefaultWarmStartPolicy()
	unauthorized := scheduler.PriorEvidence{Sessions: 5, Observations: 200, Authorized: false}
	require.False(t, scheduler.WarmStart(unauthorized, policy).Apply)

	rows := runAblation(t, policy, unauthorized)
	for _, r := range rows {
		require.Equal(t, r.BaselineMAE, r.WarmMAE,
			"at n=%d a refused prior must not move the answer at all", r.N)
		require.Zero(t, r.Improvement())
	}
}

// TestWarmPriorAblation_ThinEvidenceIsAlsoExactlyTheBaseline pins the same property for the other
// refusal ladder rungs, so a project without enough history pays nothing for the feature being
// compiled in.
func TestWarmPriorAblation_ThinEvidenceIsAlsoExactlyTheBaseline(t *testing.T) {
	t.Parallel()

	policy := scheduler.DefaultWarmStartPolicy()
	for _, e := range []scheduler.PriorEvidence{
		{Sessions: 1, Observations: 200, Authorized: true},
		{Sessions: 5, Observations: 2, Authorized: true},
		{Sessions: 5, Observations: 200, Authorized: true, AgeSeconds: policy.MaxAgeSeconds + 1},
	} {
		rows := runAblation(t, policy, e)
		for _, r := range rows {
			require.Equal(t, r.BaselineMAE, r.WarmMAE, "evidence %+v moved the answer at n=%d", e, r.N)
		}
	}
}
