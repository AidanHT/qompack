package eval_test

import (
	"math"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// TestSizing_PinsThePlanArithmetic (W6 item 2's sizing test, plan §9.3 item 2, revision-4 review V1,
// R33): the illustration's n₀ — 142 for S at success 0.90, 49 for H1 (0.60 → 0.85), 121 for H2
// (0.70 → 0.85) — and the solved design-effect form with its rounding: 142 → 313 → 336 at 24 tasks
// and 212 → 216 at 36; 121 → 220 → 240 and 49 → 56 → 72 at 24. When n₀·ICC_int reaches K no n is
// enough.
func TestSizing_PinsThePlanArithmetic(t *testing.T) {
	require.Equal(t, 142, eval.SampleSizeNonInferiority(0.90, 0.10))
	require.Equal(t, 49, eval.SampleSizeSuperiority(0.60, 0.85))
	require.Equal(t, 121, eval.SampleSizeSuperiority(0.70, 0.85))
	for _, c := range []struct{ n0, k, solved, rounded int }{
		{142, 24, 313, 336}, {142, 36, 212, 216}, {121, 24, 220, 240}, {49, 24, 56, 72}, {121, 36, 165, 180}, {49, 36, 52, 72},
	} {
		n, ok := eval.SolvedDesignN(c.n0, 0.10, c.k)
		require.True(t, ok)
		require.Equal(t, c.solved, n, "n₀ %d at %d tasks", c.n0, c.k)
		require.Equal(t, c.rounded, eval.RoundToTasks(n, c.k), "n₀ %d at %d tasks", c.n0, c.k)
	}
	_, ok := eval.SolvedDesignN(240, 0.10, 24)
	require.False(t, ok, "n₀·ICC_int = K: add tasks rather than trials per task")
}

// pilotSummary is a pilot's summary: classic recovers 60 of 100, generic 70 of 100, classic succeeds
// on 90 of 100 tasks; steer's unserved share is u; each arm's trial takes 15 minutes and 2 % of the
// five-hour and 0.5 % of the seven-day window; σ_w is 0.25 for cost and 0.35 for time.
func pilotSummary(u float64) eval.LiveSummary {
	eff := &eval.LiveArmEfficiency{LogSDWithinCost: 0.25, LogSDWithinTime: 0.35}
	use := map[string]eval.LiveRateUseStats{"five_hour": {Trials: 10, Mean: 0.02}, "seven_day": {Trials: 10, Mean: 0.005}}
	arm := func(name string, rec, succ eval.Proportion) eval.ArmSummary {
		return eval.ArmSummary{Arm: name, Recovery: rec, TaskSuccess: succ, MeanWallMS: 15 * 60 * 1000, RateUse: use, Efficiency: eff}
	}
	steer := arm("steer", eval.Proportion{K: 85, N: 100}, eval.Proportion{K: 90, N: 100})
	steer.Brief = &eval.LiveBriefStats{Compactions: 100, UnservedShare: u * 0.75, ServedEmptyShare: u * 0.25}
	return eval.LiveSummary{Arms: map[string]eval.ArmSummary{
		"stock":   arm("stock", eval.Proportion{K: 40, N: 100}, eval.Proportion{K: 90, N: 100}),
		"classic": arm("classic", eval.Proportion{K: 60, N: 100}, eval.Proportion{K: 90, N: 100}),
		"generic": arm("generic", eval.Proportion{K: 70, N: 100}, eval.Proportion{K: 90, N: 100}),
		"steer":   steer,
	}}
}

// TestSizeLive_FromAPilotSummary (decision D-d): p₀ is each control arm's 80 % Wilson bound nearer
// 0.5 and p₁ = p₀ + Δ; S's p is classic's task success taken the same way; n₀ is inflated by the
// unserved share (served-empty included) and the solved design effect; each arm runs its largest n
// rounded to the tasks, stock n_H1; the calendar and each efficiency test's power follow.
func TestSizeLive_FromAPilotSummary(t *testing.T) {
	a := stageAAnalysis()
	a.DeltaH1, a.DeltaH2, a.ICCInt = 0.25, 0.15, 0.10
	pace := eval.LiveSizingPacing{FiveHourGate: 0.90, SevenDayCeiling: 0.60, NightHours: 9}
	sz, err := eval.SizeLive(a, 36, pilotSummary(0), pace)
	require.NoError(t, err)
	require.Len(t, sz.Tests, 3)
	byTest := map[string]eval.LiveSizingTest{}
	for _, tt := range sz.Tests {
		byTest[tt.Test] = tt
	}
	// 60/100 at 80 %: Wilson [0.536, 0.661]; the bound nearer 0.5 is 0.536.
	h1 := byTest["H1"]
	require.InDelta(t, 0.5361, h1.P0, 1e-3)
	require.InDelta(t, h1.P0+0.25, h1.P1, 1e-12)
	require.Equal(t, eval.SampleSizeSuperiority(h1.P0, h1.P1), h1.N0)
	require.Equal(t, h1.N0, h1.NU, "u = 0 inflates nothing")
	n, _ := eval.SolvedDesignN(h1.NU, 0.10, 36)
	require.Equal(t, n, h1.N)
	s := byTest["S"]
	require.InDelta(t, 0.8549, s.P0, 1e-3, "90/100 at 80 %: the bound nearer 0.5")
	require.Equal(t, eval.SampleSizeNonInferiority(s.P0, 0.10), s.N0)
	for arm, tests := range map[string][]string{"steer": {"H1", "H2", "S"}, "classic": {"H1", "S"}, "generic": {"H2"}, "stock": {"H1"}} {
		want := 0
		for _, id := range tests {
			want = max(want, eval.RoundToTasks(byTest[id].N, 36))
		}
		require.Equal(t, want, sz.ArmN[arm], arm)
		require.Zero(t, sz.ArmN[arm]%36, arm)
	}
	total := sz.ArmN["stock"] + sz.ArmN["classic"] + sz.ArmN["generic"] + sz.ArmN["steer"]
	require.Equal(t, total, sz.Calendar.Trials)
	require.InDelta(t, float64(total)*0.25, sz.Calendar.Hours, 1e-9)
	require.InDelta(t, sz.Calendar.Hours/9, sz.Calendar.Nights, 1e-9)
	require.InDelta(t, float64(total)*0.02/0.90, sz.Calendar.FiveHourWindows, 1e-9)
	require.InDelta(t, float64(total)*0.005/0.60, sz.Calendar.SevenDayWeeks, 1e-9)
	require.InDelta(t, math.Max(sz.Calendar.Nights/7, sz.Calendar.SevenDayWeeks), sz.Calendar.Weeks, 1e-12)

	require.Len(t, sz.Efficiency, 2)
	cost := sz.Efficiency[0]
	require.Equal(t, "cost", cost.Metric)
	se := math.Sqrt(2*0.25*0.25/float64(sz.ArmN["steer"]) + 0.01/36)
	require.InDelta(t, se, cost.SE, 1e-12)
	z := 1.959963984540054
	require.InDelta(t, 0.5*math.Erfc(-((math.Log(1.25)-math.Log(1.1))/se-z)/math.Sqrt2), cost.HarmAt125, 1e-9)
	require.Less(t, cost.FalseHarmAt1, 0.01)
	require.Greater(t, cost.NIAt1, 0.5)
	require.Equal(t, "active_time", sz.Efficiency[1].Metric)
	require.Greater(t, sz.Efficiency[1].SE, cost.SE, "time is noisier than cost")

	inflated, err := eval.SizeLive(a, 36, pilotSummary(0.2), pace)
	require.NoError(t, err)
	require.InDelta(t, 0.2, inflated.U, 1e-12, "served-empty counts as unserved")
	require.Equal(t, int(math.Ceil(float64(h1.N0)/0.64)), inflated.Tests[0].NU)

	_, err = eval.SizeLive(eval.LiveAnalysis{NonInferiorityMargin: 0.1}, 36, pilotSummary(0), pace)
	require.ErrorContains(t, err, "delta_h1")
	_, err = eval.SizeLive(a, 6, pilotSummary(0), pace)
	require.ErrorContains(t, err, "add tasks")
}
