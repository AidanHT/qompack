package eval_test

import (
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// TestLiveSummary_LimitedSensitivity: limited trials are excluded from every analysis, counted per
// arm, and a sensitivity analysis counts them as failures on every outcome (W6 item 8).
func TestLiveSummary_LimitedSensitivity(t *testing.T) {
	rec := true
	scored := trials(eval.ArmClassic, 4, 4)
	for i := range scored {
		scored[i].Recovered = &rec
	}
	stock := trials(eval.ArmStock, 4, 2)
	all := append(append([]eval.LiveTrial{}, scored...), stock...)
	sum := eval.SummarizeLive("r", liveAnalysis(), all)
	before := sum.Arms[eval.ArmClassic]

	limited := []eval.LiveTrial{{
		TaskID: "t0", Arm: eval.ArmClassic, Trial: 2, Completed: true, TaskSuccess: true, Recovered: &rec,
		Limited: "a rate_limit_event rejected the five_hour window in turn 3", PreregisteredModel: true,
	}}
	sum.AddLimited(liveAnalysis(), all, limited)

	require.Equal(t, before, sum.Arms[eval.ArmClassic], "the primary analysis never sees a limited trial")
	require.Equal(t, map[string]int{eval.ArmClassic: 1}, sum.LimitedTrials)
	sens := sum.LimitedSensitivity[eval.ArmClassic]
	require.Equal(t, 5, sens.Trials)
	require.Equal(t, 4, sens.TaskSuccess.K, "the limited trial counts as a failure, however it was graded")
	require.Equal(t, 5, sens.Recovery.N)
	require.Equal(t, 4, sens.Recovery.K)
	require.Equal(t, sum.Arms[eval.ArmStock].TaskSuccess, sum.LimitedSensitivity[eval.ArmStock].TaskSuccess)
	require.Contains(t, strings.Join(sum.Notes, "\n"), "1 classic")
	require.Equal(t, eval.SummarizeLive("r", liveAnalysis(), all).Decision, sum.Decision,
		"the decision is the primary analysis's; the sensitivity analysis is reported beside it")

	none := eval.SummarizeLive("r", liveAnalysis(), all)
	none.AddLimited(liveAnalysis(), all, nil)
	require.Nil(t, none.LimitedTrials)
	require.Nil(t, none.LimitedSensitivity)
}

// TestLiveSummary_RateUse: each arm's per-trial utilization deltas, by window: how much of the
// five-hour and seven-day windows a trial uses, which R2's calendar estimate reads.
func TestLiveSummary_RateUse(t *testing.T) {
	d := func(v float64) eval.LiveRateDelta { return eval.LiveRateDelta{Delta: &v, Basis: eval.RateBasisPrevious} }
	ts := trials(eval.ArmSteer, 3, 3)
	ts[0].RateUse = map[string]eval.LiveRateDelta{"five_hour": d(0.02), "seven_day": d(0.01)}
	ts[1].RateUse = map[string]eval.LiveRateDelta{"five_hour": d(0.04)}
	sum := eval.SummarizeLive("r", liveAnalysis(), ts)
	five := sum.Arms[eval.ArmSteer].RateUse["five_hour"]
	require.Equal(t, 2, five.Trials)
	require.InDelta(t, 0.03, five.Mean, 1e-9)
	require.InDelta(t, 0.04, five.Max, 1e-9)
	require.Equal(t, 1, sum.Arms[eval.ArmSteer].RateUse["seven_day"].Trials)
	require.Nil(t, eval.SummarizeLive("r", liveAnalysis(), trials(eval.ArmStock, 2, 1)).Arms[eval.ArmStock].RateUse)
}

// TestLiveSummary_Pause: a paused run reaches no verdict yet, and says it is a pause to resume, not
// a stop.
func TestLiveSummary_Pause(t *testing.T) {
	sum := eval.SummarizeLive("r", liveAnalysis(), trials(eval.ArmStock, 2, 1))
	sum.Pause(2, 6, "the seven_day window is at 0.62 (limit 0.60)")
	require.Equal(t, "not-applicable", sum.Decision.Verdict)
	require.Contains(t, sum.Decision.Reason, "paused after 2 of the 6 planned trials")
	require.Contains(t, sum.Decision.Reason, "--resume")
	require.Equal(t, "the seven_day window is at 0.62 (limit 0.60)", sum.Paused)
	require.NotContains(t, strings.Join(sum.Notes, "\n"), "stopped early")
}
