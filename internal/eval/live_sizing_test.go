package eval_test

import (
	"math"
	"strings"
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
	eff := &eval.LiveArmEfficiency{
		LogSDWithinCost: 0.25, LogSDWithinTime: 0.35, LogSDWithinCostDF: 12, LogSDWithinTimeDF: 12,
		MeanCostUSD: 2.0, MeanActiveMS: 600000,
	}
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
	sz, err := eval.SizeLive(a, 36, pilotSummary(0), nil, pace)
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
	// The §9.8 check, sized from this pilot as a stand-in (no Sonnet pilot given), joins the calendar:
	// classic and steer at n_H1 each (review STATS-6).
	require.NotNil(t, sz.Sonnet)
	require.False(t, sz.Sonnet.FromSonnetPilot)
	require.Equal(t, eval.RoundToTasks(h1.N, 36), sz.Sonnet.N)
	require.Contains(t, strings.Join(sz.Notes, "\n"), "sized from this pilot's rates as a planning stand-in")
	main := sz.ArmN["stock"] + sz.ArmN["classic"] + sz.ArmN["generic"] + sz.ArmN["steer"]
	total := main + 2*sz.Sonnet.N
	require.Equal(t, total, sz.Calendar.Trials)
	require.Equal(t, 2*sz.Sonnet.N, sz.Calendar.SonnetTrials)
	require.InDelta(t, float64(total)*0.25, sz.Calendar.Hours, 1e-9)
	require.InDelta(t, sz.Calendar.Hours/9, sz.Calendar.Nights, 1e-9)
	require.InDelta(t, float64(total)*0.02/0.90, sz.Calendar.FiveHourWindows, 1e-9)
	require.InDelta(t, float64(total)*0.005/0.60, sz.Calendar.SevenDayWeeks, 1e-9)
	require.InDelta(t, math.Max(sz.Calendar.Nights/7, sz.Calendar.SevenDayWeeks), sz.Calendar.Weeks, 1e-12)

	// σ_w on 24 df (12 per arm): the pilot's own figure is used.
	require.Len(t, sz.Efficiency, 2)
	cost := sz.Efficiency[0]
	require.Equal(t, "cost", cost.Metric)
	require.False(t, cost.Planning)
	require.Equal(t, 24, cost.PilotDF)
	require.InDelta(t, 0.25, cost.PilotSigmaW, 1e-12)
	require.Len(t, cost.Rows, 1)
	se := math.Sqrt(2*0.25*0.25/float64(sz.ArmN["steer"]) + 0.01/36)
	require.InDelta(t, se, cost.Rows[0].SE, 1e-12)
	z := 1.959963984540054
	require.InDelta(t, 0.5*math.Erfc(-((math.Log(1.25)-math.Log(1.1))/se-z)/math.Sqrt2), cost.Rows[0].HarmAt125, 1e-9)
	require.Less(t, cost.Rows[0].FalseHarmAt1, 0.01)
	require.Greater(t, cost.Rows[0].NIAt1, 0.5)
	require.Equal(t, "active_time", sz.Efficiency[1].Metric)
	require.Greater(t, sz.Efficiency[1].Rows[0].SE, cost.Rows[0].SE, "time is noisier than cost")
	require.Len(t, sz.Sonnet.Efficiency, 2, "the guards' power at the Sonnet n is given too")
	require.Equal(t, sz.Sonnet.N, sz.Sonnet.Efficiency[0].N)
	require.Greater(t, sz.Sonnet.Efficiency[0].Rows[0].SE, cost.Rows[0].SE, "fewer trials, a wider interval")

	inflated, err := eval.SizeLive(a, 36, pilotSummary(0.2), nil, pace)
	require.NoError(t, err)
	require.InDelta(t, 0.2, inflated.U, 1e-12, "served-empty counts as unserved")
	require.Equal(t, int(math.Ceil(float64(h1.N0)/0.64)), inflated.Tests[0].NU)

	_, err = eval.SizeLive(eval.LiveAnalysis{NonInferiorityMargin: 0.1}, 36, pilotSummary(0), nil, pace)
	require.ErrorContains(t, err, "delta_h1")
	_, err = eval.SizeLive(a, 6, pilotSummary(0), nil, pace)
	require.ErrorContains(t, err, "add tasks")
}

// TestSizeLive_EfficiencyPowerOnFewDF (review I-4): at the plan's pilot design, about one trial per task
// per arm, the within-task log-SD rests on 0 df; the power is then given at both ends of the efficiency
// design's planning band (cost 0.25 and 0.40, time 0.35 and 0.55), the pilot's own σ_w beside it with
// its df, and a note says so. It never drops out silently.
func TestSizeLive_EfficiencyPowerOnFewDF(t *testing.T) {
	a := stageAAnalysis()
	a.DeltaH1, a.DeltaH2, a.ICCInt = 0.25, 0.15, 0.10
	pilot := pilotSummary(0)
	for _, arm := range []string{"steer", "classic"} {
		as := pilot.Arms[arm]
		e := *as.Efficiency
		e.LogSDWithinCost, e.LogSDWithinCostDF, e.LogSDWithinTime, e.LogSDWithinTimeDF = 0.02, 2, 0, 0
		as.Efficiency = &e
		pilot.Arms[arm] = as
	}
	sz, err := eval.SizeLive(a, 36, pilot, nil, eval.LiveSizingPacing{FiveHourGate: 0.9, SevenDayCeiling: 0.6})
	require.NoError(t, err)
	require.Len(t, sz.Efficiency, 2)
	cost, tm := sz.Efficiency[0], sz.Efficiency[1]
	require.True(t, cost.Planning)
	require.Equal(t, 4, cost.PilotDF)
	require.InDelta(t, 0.02, cost.PilotSigmaW, 1e-12, "the pilot's figure is kept beside the band")
	require.Len(t, cost.Rows, 2)
	require.Equal(t, 0.25, cost.Rows[0].SigmaW)
	require.Equal(t, 0.40, cost.Rows[1].SigmaW)
	require.Greater(t, cost.Rows[0].HarmAt125, cost.Rows[1].HarmAt125)
	require.True(t, tm.Planning)
	require.Zero(t, tm.PilotDF)
	require.Equal(t, []float64{0.35, 0.55}, []float64{tm.Rows[0].SigmaW, tm.Rows[1].SigmaW})
	notes := strings.Join(sz.Notes, "\n")
	require.Contains(t, notes, "within-task log-SD of cost rests on 4 df")
	require.Contains(t, notes, "planning σ_w band 0.25–0.40")
	require.Contains(t, notes, "within-task log-SD of active_time rests on 0 df")
}

// TestSizeLive_TheBriefRedesignRule (review STATS-1): the sizing always gives the pilot's steer/classic
// ratios of mean C and mean T, and flags the brief redesign rule of plan §9.3 item 1 when either
// exceeds the pre-registered ratio (1.25), on C and on T separately.
func TestSizeLive_TheBriefRedesignRule(t *testing.T) {
	a := stageAAnalysis()
	a.DeltaH1, a.DeltaH2, a.ICCInt = 0.25, 0.15, 0.10
	for name, c := range map[string]struct {
		cost, time float64
		fires      bool
	}{
		"cost 1.24": {1.24, 1.0, false}, "cost 1.26": {1.26, 1.0, true},
		"time 1.24": {1.0, 1.24, false}, "time 1.26": {1.0, 1.26, true},
	} {
		pilot := pilotSummary(0)
		as := pilot.Arms["steer"]
		e := *as.Efficiency
		e.MeanCostUSD, e.MeanActiveMS = 2.0*c.cost, 600000*c.time
		as.Efficiency = &e
		pilot.Arms["steer"] = as
		sz, err := eval.SizeLive(a, 36, pilot, nil, eval.LiveSizingPacing{})
		require.NoError(t, err, name)
		require.NotNil(t, sz.Redesign, name)
		require.InDelta(t, c.cost, sz.Redesign.CostRatio, 1e-12, name)
		require.InDelta(t, c.time, sz.Redesign.TimeRatio, 1e-12, name)
		require.Equal(t, 1.25, sz.Redesign.Threshold, name)
		require.Equal(t, c.fires, sz.Redesign.Fires, name)
		require.Equal(t, c.fires, strings.Contains(strings.Join(sz.Notes, "\n"), "BRIEF REDESIGN RULE FIRES"), name)
	}
}

// TestSizeLive_TheSonnetCheckFromItsOwnPilot (review STATS-6): given the Sonnet pilot's summary, the §9.8
// check is n_H1 from its classic recovery and its own unserved share, its two arms join the calendar at
// its own wall time, and the guards' power is given at its n.
func TestSizeLive_TheSonnetCheckFromItsOwnPilot(t *testing.T) {
	a := stageAAnalysis()
	a.DeltaH1, a.DeltaH2, a.ICCInt = 0.25, 0.15, 0.10
	sonnet := pilotSummary(0.1)
	cl := sonnet.Arms["classic"]
	cl.Recovery, cl.MeanWallMS = eval.Proportion{K: 30, N: 100}, 6*60*1000
	sonnet.Arms["classic"] = cl
	st := sonnet.Arms["steer"]
	st.MeanWallMS = 6 * 60 * 1000
	sonnet.Arms["steer"] = st
	sz, err := eval.SizeLive(a, 36, pilotSummary(0), &sonnet, eval.LiveSizingPacing{NightHours: 9})
	require.NoError(t, err)
	require.True(t, sz.Sonnet.FromSonnetPilot)
	p0 := sz.Sonnet.H1.P0
	require.Less(t, p0, 0.5, "30/100's bound nearer 0.5 is its upper one")
	require.Greater(t, p0, 0.30)
	require.Equal(t, int(math.Ceil(float64(eval.SampleSizeSuperiority(p0, p0+0.25))/0.81)), sz.Sonnet.H1.NU, "its own u = 0.1")
	require.NotContains(t, strings.Join(sz.Notes, "\n"), "planning stand-in")
	main := sz.ArmN["stock"] + sz.ArmN["classic"] + sz.ArmN["generic"] + sz.ArmN["steer"]
	require.InDelta(t, float64(main)*0.25+float64(2*sz.Sonnet.N)*0.1, sz.Calendar.Hours, 1e-9)
	require.Equal(t, sz.Sonnet.N, sz.Sonnet.Efficiency[0].N)
}

// TestSizeLive_EachArmRunsItsLargestTest (review TESTS-07): an arm's n is the largest of its tests', so
// when H1 needs more trials than S, classic and stock run H1's n, not S's.
func TestSizeLive_EachArmRunsItsLargestTest(t *testing.T) {
	a := stageAAnalysis()
	a.DeltaH1, a.DeltaH2, a.ICCInt = 0.10, 0.15, 0.0
	pilot := pilotSummary(0)
	cl := pilot.Arms["classic"]
	cl.TaskSuccess = eval.Proportion{K: 99, N: 100}
	pilot.Arms["classic"] = cl
	sz, err := eval.SizeLive(a, 36, pilot, nil, eval.LiveSizingPacing{})
	require.NoError(t, err)
	by := map[string]int{}
	for _, tt := range sz.Tests {
		by[tt.Test] = eval.RoundToTasks(tt.N, 36)
	}
	require.Greater(t, by["H1"], by["S"], "the fixture makes H1 the largest of classic's tests")
	require.Equal(t, by["H1"], sz.ArmN["classic"])
	require.Equal(t, by["H1"], sz.ArmN["stock"])
	require.Equal(t, max(by["H1"], by["H2"], by["S"]), sz.ArmN["steer"])
	require.Equal(t, by["H2"], sz.ArmN["generic"])
}
