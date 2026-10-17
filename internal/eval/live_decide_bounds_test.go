package eval_test

import (
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// ratioRun is a deterministic run over k tasks, four trials per task per arm: steer recovers on every
// trial and classic and generic on none, so H1 and H2 are shown; classic costs 1.0 on every trial and
// steer costs steerCost(task) on each trial of its task, so the bootstrap resamples only the task
// ratios.
func ratioRun(k int, steerCost func(task int) float64) []eval.LiveTrial {
	return detTrials(k, 4, []string{"classic", "generic", "steer"}, func(arm string, task, _ int) (bool, bool, float64) {
		if arm == "steer" {
			return true, true, steerCost(task)
		}
		return false, true, 1.0
	})
}

// TestDecideStageA_GuardsReadTheirBoundsNotTheirEstimates (review TESTS-01): the cost guard shows harm
// only when the ratio's lower bound, not its estimate, exceeds 1.10, and H3 holds only when the upper
// bound, not the estimate, is below 1.10.
func TestDecideStageA_GuardsReadTheirBoundsNotTheirEstimates(t *testing.T) {
	spread := func(centre, half float64) func(int) float64 {
		return func(task int) float64 { return centre + half*float64(2*(task%2)-1) }
	}
	wide := eval.DecideStageA(stageAAnalysis(), ratioRun(12, spread(1.117, 0.30)), nil)
	cost, _ := wide.Test("harm-cost")
	require.Greater(t, cost.Interval.Estimate, 1.10)
	require.LessOrEqual(t, cost.Interval.Low, 1.10)
	require.Equal(t, "not shown", cost.Label, "an estimate above the margin is not harm while L is below it")

	tight := eval.DecideStageA(stageAAnalysis(), ratioRun(12, spread(1.13, 0.01)), nil)
	cost, _ = tight.Test("harm-cost")
	require.Greater(t, cost.Interval.Low, 1.10)
	require.Less(t, cost.Interval.Low, 1.13)
	require.Equal(t, "harm shown", cost.Label)

	h3run := eval.DecideStageA(stageAAnalysis(), ratioRun(12, spread(1.05, 0.15)), nil)
	h3, _ := h3run.Test("H3")
	require.Less(t, h3.Interval.Estimate, 1.10)
	require.GreaterOrEqual(t, h3.Interval.High, 1.10)
	require.Equal(t, "not shown", h3.Label, "an estimate below the margin is not non-inferiority while U is above it")
	require.Equal(t, "shown", label(t, eval.DecideStageA(stageAAnalysis(), ratioRun(12, spread(1.05, 0.01)), nil), "H3"))
}

// TestDecideStageA_H3NeedsItsCompletedAndLimitedChecks (review TESTS-04): H3 is not shown when it fails
// on trials whose task completed, or when the limited-trial sensitivity flips it, though the primary
// interval holds.
func TestDecideStageA_H3NeedsItsCompletedAndLimitedChecks(t *testing.T) {
	// Steer's one incomplete trial per task is cheap (0.1) and its three completed ones dear (1.3): the
	// primary ratio is 1.0, the completed-only ratio 1.3.
	trials := ratioRun(6, func(int) float64 { return 1.3 })
	for i := range trials {
		if trials[i].Arm == "steer" && trials[i].Trial == 4 {
			trials[i].HostCostUSD, trials[i].Completed = 0.1, false
		}
	}
	s := eval.DecideStageA(stageAAnalysis(), trials, nil)
	h3, _ := s.Test("H3")
	require.Less(t, h3.Interval.High, 1.10)
	require.GreaterOrEqual(t, h3.Sensitivity["completed"].High, 1.10)
	require.Equal(t, "not shown", h3.Label)
	require.Contains(t, h3.Reason, "does not hold on trials whose task completed")

	// Equal costs, but each steer slot also spent 0.5 on a limited attempt.
	trials = ratioRun(6, func(int) float64 { return 1.0 })
	var limited []eval.LiveTrial
	for _, tr := range trials {
		if tr.Arm == "steer" {
			limited = append(limited, eval.LiveTrial{
				TaskID: tr.TaskID, Arm: "steer", Trial: tr.Trial, HostCostUSD: 0.5, Steps: steps(1000, -1), Limited: "rejected",
			})
		}
	}
	s = eval.DecideStageA(stageAAnalysis(), trials, limited)
	h3, _ = s.Test("H3")
	require.Less(t, h3.Interval.High, 1.10)
	require.GreaterOrEqual(t, h3.Sensitivity["limited"].High, 1.10)
	require.Equal(t, "not shown", h3.Label)
	require.Contains(t, h3.Reason, "the limited-trial sensitivity flips it")
}

// TestDecideStageA_AnUncomputableGuardFailsClosed (coordinator ruling on review STATS-4): when the
// treated arm has no complete C, the adverse imputation has nothing to impute from; the gated cost
// guard stays fail-closed as "indeterminate", saying the measurement needs investigating, and nothing
// ships.
func TestDecideStageA_AnUncomputableGuardFailsClosed(t *testing.T) {
	trials := ratioRun(4, func(int) float64 { return 1.0 })
	for i := range trials {
		if trials[i].Arm == "steer" {
			trials[i].Steps = steps(-1)
		}
	}
	s := eval.DecideStageA(stageAAnalysis(), trials, nil)
	cost, _ := s.Test("harm-cost")
	require.Nil(t, cost.Interval)
	require.Equal(t, "indeterminate", cost.Label)
	require.Contains(t, cost.Reason, "could not be computed: investigate the measurement")
	require.Equal(t, eval.LiveVerdictHarm, s.Decision.Verdict)
}

// TestDecideStageA_MissingDurationsCountAtTheCapForTheTreated (review STATS-5): a steer result line with
// no duration_ms counts at the cap in the decided T ratio and a classic one as observed (0); a scored
// steer trial with no step record counts at the cap too; and the cap is the run's own step timeout,
// recorded on the trial, not the analysis block's.
func TestDecideStageA_MissingDurationsCountAtTheCapForTheTreated(t *testing.T) {
	const capMS = 120000
	trials := ratioRun(2, func(int) float64 { return 1.0 })
	for i := range trials {
		trials[i].StepTimeoutMS = capMS
		if trials[i].Trial == 1 && (trials[i].Arm == "steer" || trials[i].Arm == "classic") {
			trials[i].Steps = steps(1000, 1000, 1000)
			trials[i].Steps[1].DurationMissing, trials[i].Steps[1].DurationMS = true, 0
		}
	}
	var noSteps eval.LiveTrial
	for _, tr := range trials {
		if tr.Arm == "steer" && tr.TaskID == "t00" && tr.Trial == 2 {
			noSteps = tr
		}
	}
	noSteps.Trial, noSteps.Steps = 5, nil
	trials = append(trials, noSteps)
	var steer1 eval.LiveTrial
	for _, tr := range trials {
		if tr.Arm == "steer" && tr.Trial == 1 {
			steer1 = tr
		}
	}
	require.Equal(t, int64(2000), steer1.ActiveMS(capMS), "observed T counts a missing duration as 0")
	require.Equal(t, int64(2000+capMS), steer1.ActiveMSAdverse(capMS))
	require.Equal(t, int64(capMS), noSteps.ActiveMSAdverse(capMS))
	require.Zero(t, noSteps.ActiveMS(capMS))

	s := eval.DecideStageA(stageAAnalysis(), trials, nil)
	tm, _ := s.Test("harm-active_time")
	// Steer, per task: {2000 + cap, 3000, 3000, 3000}, plus one trial at the cap in task t00.
	// Classic, per task: {2000, 3000, 3000, 3000}.
	steer := float64(2*(2000+capMS+3*3000)+capMS) / 9
	classic := float64(2*(2000+3*3000)) / 8
	require.InDelta(t, steer/classic, tm.Interval.Estimate, 1e-9)
	e := eval.SummarizeLive("r", stageAAnalysis(), trials).Arms["steer"].Efficiency
	require.Equal(t, 2, e.DurationMissing)
}

// TestSummary_AStopOrPauseMakesTheStageARecordInterim (review I-2): a stopped or paused run's Stage A
// record follows the summary's not-applicable decision and its outcome row says no verdict was
// reached, so it can never read "ships on".
func TestSummary_AStopOrPauseMakesTheStageARecordInterim(t *testing.T) {
	ship := func() eval.LiveSummary {
		sum := eval.SummarizeLive("r", stageAAnalysis(), ratioRun(6, func(int) float64 { return 1.0 }))
		require.Equal(t, eval.LiveVerdictShip, sum.Decision.Verdict, sum.Decision.Reason)
		return sum
	}
	for name, mark := range map[string]func(*eval.LiveSummary){
		"stopped": func(s *eval.LiveSummary) { s.StopEarly(72, 80, "three configuration errors") },
		"paused":  func(s *eval.LiveSummary) { s.Pause(72, 80, "the seven-day window is at 0.62") },
	} {
		sum := ship()
		mark(&sum)
		require.Equal(t, eval.LiveVerdictNotApplicable, sum.Decision.Verdict, name)
		require.Equal(t, sum.Decision, sum.StageA.Decision, name)
		require.Contains(t, sum.StageA.Outcome, "no verdict: the run "+name+" after 72 of the 80 planned trials", name)
		require.Contains(t, sum.StageA.Outcome, "interim, not decided", name)
		require.NotContains(t, sum.StageA.Outcome, "ships on", name)
	}
}

// TestSummarizeLive_RoutesOnTheDesign (review WIRING-9): a four-arm task set's run is a Stage A run
// whatever arms it ran; a stock-only run reaches the Stage A rule's not-applicable, not the two-arm
// verdict's wording.
func TestSummarizeLive_RoutesOnTheDesign(t *testing.T) {
	trials := detTrials(3, 2, []string{"stock"}, func(string, int, int) (bool, bool, float64) { return true, true, 1 })
	sum := eval.SummarizeLive("r", stageAAnalysis(), trials)
	require.NotNil(t, sum.StageA)
	require.Equal(t, eval.LiveVerdictNotApplicable, sum.Decision.Verdict)
	require.Contains(t, sum.Decision.Reason, "compare steer with classic")
	require.NotContains(t, sum.Decision.Reason, "both arms are needed")

	legacy := eval.SummarizeLive("r", liveAnalysis(), trials)
	require.Nil(t, legacy.StageA, "a set without per-arm counts and no Stage A arm keeps the two-arm rule")
}
