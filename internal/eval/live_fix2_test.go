package eval_test

import (
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// limitedSteer is a limited attempt in every steer slot of trials, with the given step records.
func limitedSteer(trials []eval.LiveTrial, st []eval.LiveStepRecord) []eval.LiveTrial {
	var out []eval.LiveTrial
	for _, tr := range trials {
		if tr.Arm == "steer" {
			out = append(out, eval.LiveTrial{
				TaskID: tr.TaskID, Arm: "steer", Trial: tr.Trial, HostCostUSD: 0, Steps: st, Limited: "rejected",
			})
		}
	}
	return out
}

// TestDecideStageA_TheTreatedLimitedAttemptsFailClosed (reviews S-R1-N1, STATS-5, CLI-6): in the
// efficiency guards' limited sensitivity a treated (steer) limited attempt counts like a treated scored
// trial: a result line with no duration_ms at the cap, and an unknown C at its task's maximum complete
// C. Each flips its gated guard to indeterminate, where adding 0 would have hidden it.
func TestDecideStageA_TheTreatedLimitedAttemptsFailClosed(t *testing.T) {
	trials := ratioRun(6, func(int) float64 { return 1.0 })

	noDuration := limitedSteer(trials, []eval.LiveStepRecord{{ID: "s", HasResult: true, DurationMissing: true}})
	s := eval.DecideStageA(stageAAnalysis(), trials, noDuration)
	tm, _ := s.Test("harm-active_time")
	require.Equal(t, "not shown", label(t, eval.DecideStageA(stageAAnalysis(), trials, nil), "harm-active_time"))
	require.InDelta(t, (3000.0+600000)/3000, tm.Sensitivity["limited"].Estimate, 1e-9, "the missing duration counts at the cap")
	require.Equal(t, "indeterminate", tm.Label)

	unknownCost := limitedSteer(trials, []eval.LiveStepRecord{{ID: "s", HasResult: true, DurationMS: 1, CostMissing: true}})
	s = eval.DecideStageA(stageAAnalysis(), trials, unknownCost)
	cost, _ := s.Test("harm-cost")
	require.InDelta(t, 2.0, cost.Sensitivity["limited"].Estimate, 1e-12, "the unknown C takes the task's maximum (1.0)")
	require.Equal(t, "indeterminate", cost.Label)
	require.Equal(t, eval.LiveVerdictHarm, s.Decision.Verdict)

	// The comparator's limited attempts stay as observed: an unknown C adds nothing there.
	var classicLimited []eval.LiveTrial
	for _, l := range unknownCost {
		l.Arm = "classic"
		classicLimited = append(classicLimited, l)
	}
	cost, _ = eval.DecideStageA(stageAAnalysis(), trials, classicLimited).Test("harm-cost")
	require.InDelta(t, 1.0, cost.Sensitivity["limited"].Estimate, 1e-12)
	require.Equal(t, "not shown", cost.Label)
}

// TestDecideStageA_TheUngatedGuardsRuleIsNotHarm (review CLI-7): the classic-vs-stock guard's rule reads
// "overhead shown", like its label, never "harm shown".
func TestDecideStageA_TheUngatedGuardsRuleIsNotHarm(t *testing.T) {
	trials := detTrials(4, 3, []string{"stock", "classic", "steer"}, func(arm string, _, _ int) (bool, bool, float64) {
		return arm == "steer", true, 1.0
	})
	lab, ok := eval.DecideStageA(stageAAnalysis(), trials, nil).Test("label-cost-classic-vs-stock")
	require.True(t, ok)
	require.Contains(t, lab.Rule, "overhead shown when the ratio's lower bound is above 1.10")
	require.NotContains(t, lab.Rule, "harm shown")
}

// TestArmEfficiency_TheEstimateRatio (review H-NEW-1): the estimate over the host's cost is Σ estimate /
// Σ C over the trials with both a recorded estimate and a complete C, with its count and whether one of
// those estimates is a lower bound; with none it records N = 0.
func TestArmEfficiency_TheEstimateRatio(t *testing.T) {
	one := &eval.Money{Micros: 1000000}
	ts := []eval.LiveTrial{
		{TaskID: "a", Arm: "classic", Trial: 1, Steps: steps(1000), HostCostUSD: 2.0, Estimate: one, EstimateComplete: true},
		{TaskID: "a", Arm: "classic", Trial: 2, Steps: steps(1000), HostCostUSD: 2.0, Estimate: one},
		{TaskID: "a", Arm: "classic", Trial: 3, Steps: steps(1000), HostCostUSD: 2.0},
		{TaskID: "a", Arm: "classic", Trial: 4, Steps: steps(-1), HostCostUSD: 0, Estimate: one},
	}
	e := eval.SummarizeLive("r", stageAAnalysis(), ts).Arms["classic"].Efficiency
	require.Equal(t, 2, e.EstimateRatioN)
	require.InDelta(t, 0.5, e.EstimateRatio, 1e-12)
	require.True(t, e.EstimateLowerBound, "trial 2's estimate is incomplete")
	none := eval.SummarizeLive("r", stageAAnalysis(), ts[2:3]).Arms["classic"].Efficiency
	require.Zero(t, none.EstimateRatioN)
	require.Zero(t, none.EstimateRatio)
}

// TestSummary_InterimAndLimitedNotes (reviews D-NEW-2, D-NEW-3): a Stage A run's limited-trial note says
// the efficiency half of that sensitivity is decided; an interim record is marked so, and a record
// that was not-applicable already keeps why when a stop makes it interim.
func TestSummary_InterimAndLimitedNotes(t *testing.T) {
	trials := ratioRun(4, func(int) float64 { return 1.0 })
	sum := eval.SummarizeLive("r", stageAAnalysis(), trials)
	sum.AddLimited(stageAAnalysis(), trials, limitedSteer(trials, steps(1000, -1))[:2])
	require.Contains(t, strings.Join(sum.Notes, "\n"), "efficiency guards' limited sensitivity, which is decided")
	require.False(t, sum.StageA.Interim)
	sum.Pause(10, 20, "night")
	require.True(t, sum.StageA.Interim)

	stockOnly := detTrials(2, 1, []string{"stock"}, func(string, int, int) (bool, bool, float64) { return true, true, 1 })
	na := eval.SummarizeLive("r", stageAAnalysis(), stockOnly)
	require.False(t, na.StageA.Interim, "a record that could not be decided is not interim")
	na.StopEarly(2, 4, "guard")
	require.True(t, na.StageA.Interim)
	require.Contains(t, na.StageA.Outcome, "the Stage A decisions need the steer and classic arms; no verdict: the run stopped")
}

// TestSizeLive_FixRound2 (reviews S-R1-N2, S-R1-N4, S-R1-N5, S-R1-N6): a Sonnet pilot with no classic
// recovery leaves the check unsized with a note and out of the calendar; the planning-band note is
// given once per metric, and a summary with σ_w but no df says so; the df boundary is 10; the redesign
// rule gives the time ratio when the cost ratio cannot be computed.
func TestSizeLive_FixRound2(t *testing.T) {
	a := stageAAnalysis()
	a.DeltaH1, a.DeltaH2, a.ICCInt = 0.25, 0.15, 0.10
	pace := eval.LiveSizingPacing{FiveHourGate: 0.9, SevenDayCeiling: 0.6}

	sonnet := pilotSummary(0)
	cl := sonnet.Arms["classic"]
	cl.Recovery = eval.Proportion{}
	sonnet.Arms["classic"] = cl
	sz, err := eval.SizeLive(a, 36, pilotSummary(0), &sonnet, pace)
	require.NoError(t, err)
	require.Nil(t, sz.Sonnet)
	require.Zero(t, sz.Calendar.SonnetTrials)
	require.Contains(t, strings.Join(sz.Notes, "\n"), "the Sonnet check (plan §9.8) is not sized: its pilot has no classic recovery")

	// σ_w on 0 df in both arms, for the main check and the stand-in Sonnet check: one note per metric.
	pilot := pilotSummary(0)
	for _, arm := range []string{"steer", "classic"} {
		as := pilot.Arms[arm]
		e := *as.Efficiency
		e.LogSDWithinCostDF, e.LogSDWithinTimeDF = 0, 0
		e.LogSDWithinCost, e.LogSDWithinTime = 0, 0
		as.Efficiency = &e
		pilot.Arms[arm] = as
	}
	sz, err = eval.SizeLive(a, 36, pilot, nil, pace)
	require.NoError(t, err)
	notes := strings.Join(sz.Notes, "\n")
	require.Equal(t, 1, strings.Count(notes, "within-task log-SD of cost rests on 0 df"))
	require.Equal(t, 1, strings.Count(notes, "within-task log-SD of active_time rests on 0 df"))

	// A summary written before df was recorded: σ_w present, df absent.
	old := pilotSummary(0)
	for _, arm := range []string{"steer", "classic"} {
		as := old.Arms[arm]
		e := *as.Efficiency
		e.LogSDWithinCostDF, e.LogSDWithinTimeDF = 0, 0
		as.Efficiency = &e
		old.Arms[arm] = as
	}
	sz, err = eval.SizeLive(a, 36, old, nil, pace)
	require.NoError(t, err)
	require.True(t, sz.Efficiency[0].DFUnrecorded)
	require.Contains(t, strings.Join(sz.Notes, "\n"), "gives a within-task log-SD of cost without its df")

	// The boundary: 9 df use the planning band, 10 the pilot's own σ_w.
	for df, planning := range map[int]bool{9: true, 10: false} {
		p := pilotSummary(0)
		for i, arm := range []string{"steer", "classic"} {
			as := p.Arms[arm]
			e := *as.Efficiency
			e.LogSDWithinCostDF = df / 2
			if i == 0 {
				e.LogSDWithinCostDF = df - df/2
			}
			as.Efficiency = &e
			p.Arms[arm] = as
		}
		sz, err = eval.SizeLive(a, 36, p, nil, pace)
		require.NoError(t, err)
		require.Equal(t, df, sz.Efficiency[0].PilotDF)
		require.Equal(t, planning, sz.Efficiency[0].Planning, "df %d", df)
	}

	// The cost ratio cannot be computed (classic's mean C is 0); the time ratio is still given.
	r := pilotSummary(0)
	cs := r.Arms["classic"]
	ce := *cs.Efficiency
	ce.MeanCostUSD = 0
	cs.Efficiency = &ce
	r.Arms["classic"] = cs
	st := r.Arms["steer"]
	se := *st.Efficiency
	se.MeanActiveMS = 600000 * 1.3
	st.Efficiency = &se
	r.Arms["steer"] = st
	sz, err = eval.SizeLive(a, 36, r, nil, pace)
	require.NoError(t, err)
	require.NotNil(t, sz.Redesign)
	require.False(t, sz.Redesign.CostKnown)
	require.True(t, sz.Redesign.TimeKnown)
	require.InDelta(t, 1.3, sz.Redesign.TimeRatio, 1e-12)
	require.True(t, sz.Redesign.Fires)
	require.Contains(t, strings.Join(sz.Notes, "\n"), "ratio of mean C is not computable and of mean T 1.300")
}
