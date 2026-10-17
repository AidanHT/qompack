package eval_test

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// TestDecideStageA_ATreatedLimitedPartialCostIsImputed (review R2-M1): a treated limited attempt's
// partial C takes the larger of its lower bound and its task's maximum complete C, as a scored trial's
// does, so an attempt that reached a priced result line never adds less than one that reached none.
// The reviewer's probe: the last result line carries no total_cost_usd, so C is the earlier total,
// 0.02; adding that lower bound read "not shown" at 1.020, and the imputed 1.0 reads indeterminate.
// The comparator's limited attempts stay as observed, a partial C at its lower bound and a missing
// duration at 0.
func TestDecideStageA_ATreatedLimitedPartialCostIsImputed(t *testing.T) {
	trials := ratioRun(6, func(int) float64 { return 1.0 })
	partial := limitedSteer(trials, []eval.LiveStepRecord{
		{ID: "a", ResultSubtype: "success", HasResult: true, DurationMS: 1000},
		{ID: "b", ResultSubtype: "success", HasResult: true, DurationMS: 1000, CostMissing: true},
	})
	for i := range partial {
		partial[i].HostCostUSD = 0.02
	}
	c, status := partial[0].Cost()
	require.Equal(t, eval.LiveCostPartial, status)
	require.InDelta(t, 0.02, c, 1e-12)

	s := eval.DecideStageA(stageAAnalysis(), trials, partial)
	cost, _ := s.Test("harm-cost")
	require.InDelta(t, 2.0, cost.Sensitivity["limited"].Estimate, 1e-12, "max(0.02, the task's maximum complete C 1.0)")
	require.Equal(t, "indeterminate", cost.Label)
	require.Equal(t, eval.LiveVerdictHarm, s.Decision.Verdict)

	// A partial C above the task's maximum keeps its own value.
	high := slices.Clone(partial)
	for i := range high {
		high[i].HostCostUSD = 3.0
	}
	cost, _ = eval.DecideStageA(stageAAnalysis(), trials, high).Test("harm-cost")
	require.InDelta(t, 4.0, cost.Sensitivity["limited"].Estimate, 1e-12)

	// The same attempts on the comparator add their lower bound, and a missing duration there adds 0
	// (review R2-N1: the comparator's T stays as observed).
	var classic []eval.LiveTrial
	for _, l := range partial {
		l.Arm = "classic"
		classic = append(classic, l)
	}
	cost, _ = eval.DecideStageA(stageAAnalysis(), trials, classic).Test("harm-cost")
	require.InDelta(t, 1.0/1.02, cost.Sensitivity["limited"].Estimate, 1e-12)
	noDuration := limitedSteer(trials, []eval.LiveStepRecord{{ID: "s", HasResult: true, DurationMissing: true}})
	for i := range noDuration {
		noDuration[i].Arm = "classic"
	}
	tm, _ := eval.DecideStageA(stageAAnalysis(), trials, noDuration).Test("harm-active_time")
	require.InDelta(t, 1.0, tm.Sensitivity["limited"].Estimate, 1e-12, "the comparator's missing duration adds 0")
	require.Equal(t, "not shown", tm.Label)
}

// TestArmEfficiency_TheEstimateRatioNeedsACompleteC (review R2-N1): a trial whose C is partial does not
// enter the estimate ratio, even with a complete estimate.
func TestArmEfficiency_TheEstimateRatioNeedsACompleteC(t *testing.T) {
	one := &eval.Money{Micros: 1000000}
	ts := []eval.LiveTrial{
		{TaskID: "a", Arm: "classic", Trial: 1, Steps: steps(1000), HostCostUSD: 2.0, Estimate: one, EstimateComplete: true},
		{TaskID: "a", Arm: "classic", Trial: 2, Steps: steps(1000, -1), HostCostUSD: 1.0, Estimate: one, EstimateComplete: true},
	}
	_, status := ts[1].Cost()
	require.Equal(t, eval.LiveCostPartial, status)
	e := eval.SummarizeLive("r", stageAAnalysis(), ts).Arms["classic"].Efficiency
	require.Equal(t, 1, e.EstimateRatioN)
	require.InDelta(t, 0.5, e.EstimateRatio, 1e-12)
	require.False(t, e.EstimateLowerBound)
}

// TestEstimateRatioText (reviews H-NEW-1, R2-N3): the ratio with its count and lower-bound mark; "n/a"
// for an arm with no estimate; and "not recorded" for an arm with estimates but no ratio, which a
// summary written before the ratio existed reads as.
func TestEstimateRatioText(t *testing.T) {
	as := eval.ArmSummary{Efficiency: &eval.LiveArmEfficiency{EstimateRatio: 0.9, EstimateRatioN: 3, EstimateLowerBound: true}}
	require.Equal(t, "0.900 over 3 trial(s), a lower bound", eval.EstimateRatioText(as))
	as.Efficiency.EstimateLowerBound = false
	require.Equal(t, "0.900 over 3 trial(s)", eval.EstimateRatioText(as))
	require.Equal(t, "n/a (no estimate recorded)", eval.EstimateRatioText(eval.ArmSummary{Efficiency: &eval.LiveArmEfficiency{}}))

	var old eval.ArmSummary
	require.NoError(t, json.Unmarshal([]byte(`{"arm":"steer","mean_estimate_micros":900000,"efficiency":{"trials":2}}`), &old))
	text := eval.EstimateRatioText(old)
	require.True(t, strings.HasPrefix(text, "not recorded"), text)
	require.NotContains(t, text, "no estimate recorded")
}

// TestSizeLive_FixRound3 (reviews R2-N1, R2-N4): a Sonnet pilot with no steer brief outcomes keeps its
// labelled u note; a steer pilot arm with no known C gives no cost ratio, and the redesign rule reads
// the time ratio alone.
func TestSizeLive_FixRound3(t *testing.T) {
	a := stageAAnalysis()
	a.DeltaH1, a.DeltaH2, a.ICCInt = 0.25, 0.15, 0.10
	pace := eval.LiveSizingPacing{FiveHourGate: 0.9, SevenDayCeiling: 0.6}

	sonnet := pilotSummary(0)
	st := sonnet.Arms["steer"]
	st.Brief = nil
	sonnet.Arms["steer"] = st
	sz, err := eval.SizeLive(a, 36, pilotSummary(0), &sonnet, pace)
	require.NoError(t, err)
	require.Contains(t, strings.Join(sz.Notes, "\n"),
		"the Sonnet check: the Sonnet pilot records no steer brief outcomes, so the unserved share u is taken as 0")

	p := pilotSummary(0)
	ps := p.Arms["steer"]
	e := *ps.Efficiency
	e.CostComplete, e.CostPartial, e.CostUnknown, e.MeanCostUSD = 0, 0, 100, 0
	e.MeanActiveMS = 600000 * 1.3
	ps.Efficiency = &e
	p.Arms["steer"] = ps
	sz, err = eval.SizeLive(a, 36, p, nil, pace)
	require.NoError(t, err)
	require.NotNil(t, sz.Redesign)
	require.False(t, sz.Redesign.CostKnown, "steer has no known C, so its mean C of 0 is no ratio")
	require.True(t, sz.Redesign.TimeKnown)
	require.True(t, sz.Redesign.Fires, "on T alone")
	require.Contains(t, strings.Join(sz.Notes, "\n"), "ratio of mean C is not computable and of mean T 1.300")
}

// TestSummary_AnInterimRecordWithNoTestsSaysNoTests (review R2-N5): a stopped record that has no tests
// does not say "the tests below are interim"; one with tests still does.
func TestSummary_AnInterimRecordWithNoTestsSaysNoTests(t *testing.T) {
	stockOnly := detTrials(2, 1, []string{"stock"}, func(string, int, int) (bool, bool, float64) { return true, true, 1 })
	na := eval.SummarizeLive("r", stageAAnalysis(), stockOnly)
	require.Empty(t, na.StageA.Tests)
	na.StopEarly(2, 4, "guard")
	require.NotContains(t, na.StageA.Outcome, "tests below")
	require.Contains(t, na.StageA.Outcome, "no verdict: the run stopped after 2 of the 4 planned trials")

	sum := eval.SummarizeLive("r", stageAAnalysis(), ratioRun(4, func(int) float64 { return 1.0 }))
	sum.StopEarly(2, 4, "guard")
	require.Contains(t, sum.StageA.Outcome, "the tests below are interim, not decided")
}

// TestSummarizeLive_AV6TwoArmSummaryGainsNoZeroKey (review R2-N6): the efficiency fields the fix rounds
// added are omitted when zero, so a V6 two-arm summary whose arms have one trial per task and no
// estimate carries exactly the efficiency keys it carried before them (at 51954459).
func TestSummarizeLive_AV6TwoArmSummaryGainsNoZeroKey(t *testing.T) {
	v6 := func(arm string) []eval.LiveTrial {
		out := trials(arm, 3, 2)
		for i := range out {
			out[i].Steps, out[i].HostCostUSD = steps(1000), 0.5
		}
		return out
	}
	sum := eval.SummarizeLive("r", liveAnalysis(), append(v6(eval.ArmStock), v6(eval.ArmQompack)...))
	require.Nil(t, sum.StageA)
	raw, err := json.Marshal(sum)
	require.NoError(t, err)
	for _, added := range []string{
		"estimate_ratio", "estimate_ratio_n", "estimate_lower_bound", "duration_missing", "log_sd_within_cost_df",
		"log_sd_within_time_df", "limited_not_computable", "interim", "not_measured",
	} {
		require.False(t, strings.Contains(string(raw), `"`+added+`"`), "summary.json carries %q", added)
	}
	var arms struct {
		Arms map[string]struct {
			Efficiency map[string]json.RawMessage `json:"efficiency"`
		} `json:"arms"`
	}
	require.NoError(t, json.Unmarshal(raw, &arms))
	before := []string{
		"compaction_ms", "compactions", "cost_complete", "cost_partial", "cost_unknown", "log_sd_within_cost",
		"log_sd_within_time", "mean_active_ms", "mean_api_ms", "mean_cost_usd", "mean_local_ms", "post_tokens",
		"timed_out_steps", "trials",
	}
	for _, arm := range []string{eval.ArmStock, eval.ArmQompack} {
		eff := arms.Arms[arm].Efficiency
		require.NotNil(t, eff, arm)
		require.Equal(t, before, slices.Sorted(maps.Keys(eff)), arm)
	}
}
