package eval_test

import (
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// steps builds step records: a positive duration is a step that ended in a result line taking that
// many milliseconds (a fifth of them local), and -1 a reached step with no result.
func steps(durations ...int64) []eval.LiveStepRecord {
	out := make([]eval.LiveStepRecord, 0, len(durations))
	for _, d := range durations {
		if d < 0 {
			out = append(out, eval.LiveStepRecord{ID: "s"})
			continue
		}
		out = append(out, eval.LiveStepRecord{ID: "s", ResultSubtype: "success", HasResult: true, DurationMS: d, DurationAPIMS: d * 4 / 5})
	}
	return out
}

// TestStepRecords_CopyTheHostDurations (amendment 4 item 1): each step record copies its result
// line's duration_ms and duration_api_ms and says it had one; a step the stream never closed has none.
func TestStepRecords_CopyTheHostDurations(t *testing.T) {
	task := eval.LiveTask{Steps: []eval.LiveStep{{ID: "a", Prompt: "p"}, {ID: "b", Compact: true}, {ID: "c", Prompt: "q"}}}
	s := eval.HostStream{Turns: []eval.HostTurn{
		{Result: &eval.HostResult{Subtype: "success", DurationMS: 12000, DurationAPIMS: 9000}},
		{Result: &eval.HostResult{Subtype: "success", LocalCommand: "compact", DurationMS: 30000, DurationAPIMS: 29000}},
		{},
	}}
	recs, complete := eval.StepRecords(task, s)
	require.False(t, complete)
	require.True(t, recs[0].HasResult)
	require.Equal(t, int64(12000), recs[0].DurationMS)
	require.Equal(t, int64(9000), recs[0].DurationAPIMS)
	require.Equal(t, int64(30000), recs[1].DurationMS)
	require.False(t, recs[2].HasResult)
	require.Zero(t, recs[2].DurationMS)
}

// TestLiveTrial_CostAndActiveTime (amendment 4 item 2): C is complete when every step has a result
// line, a lower bound when the last reached step has none, and unknown with no result at all; T sums
// the reached steps' duration_ms and counts a reached step with no result at the pinned step
// timeout, and a step never reached as 0. A hang on the first step counts the cap, with C unknown.
func TestLiveTrial_CostAndActiveTime(t *testing.T) {
	const capMS = 600000
	for name, c := range map[string]struct {
		steps        []eval.LiveStepRecord
		cost         float64
		status       string
		active, api  int64
		timedOutStep int
	}{
		"complete":         {steps(1000, 2000, 3000), 0.5, eval.LiveCostComplete, 6000, 4800, 0},
		"cut in step 3":    {steps(1000, 2000, -1), 0.5, eval.LiveCostPartial, 3000 + capMS, 2400, 1},
		"hang on step 1":   {steps(-1, -1, -1), 0, eval.LiveCostUnknown, capMS, 0, 1},
		"never started":    {nil, 0, eval.LiveCostUnknown, 0, 0, 0},
		"old record shape": {[]eval.LiveStepRecord{{ResultSubtype: "success", DurationMS: 7}}, 0.5, eval.LiveCostComplete, 7, 0, 0},
	} {
		tr := eval.LiveTrial{Steps: c.steps, HostCostUSD: 0.5}
		cost, status := tr.Cost()
		require.Equal(t, c.status, status, name)
		require.InDelta(t, c.cost, cost, 1e-12, name)
		require.Equal(t, c.active, tr.ActiveMS(capMS), name)
		require.Equal(t, c.api, tr.APIMS(), name)
		require.Equal(t, c.timedOutStep, tr.TimedOutSteps(), name)
	}
}

// TestSummarizeLive_AMissingCostIsNeverZero (amendment 4 item 2): the arm's mean cost is over the
// trials whose C is known, a lower bound counted beside it; a trial with no result line is counted
// as unknown and left out, not added as zero.
func TestSummarizeLive_AMissingCostIsNeverZero(t *testing.T) {
	ts := []eval.LiveTrial{
		{TaskID: "a", Arm: eval.ArmClassic, Trial: 1, Steps: steps(1000), HostCostUSD: 0.40},
		{TaskID: "a", Arm: eval.ArmClassic, Trial: 2, Steps: steps(1000, -1), HostCostUSD: 0.20},
		{TaskID: "a", Arm: eval.ArmClassic, Trial: 3, Steps: steps(-1), HostCostUSD: 0},
	}
	as := eval.SummarizeLive("r", liveAnalysis(), ts).Arms[eval.ArmClassic]
	require.InDelta(t, 0.30, as.MeanHostCost, 1e-12, "the unknown trial is not a zero")
	require.Equal(t, 1, as.CostPartial)
	require.Equal(t, 1, as.CostUnknown)
	e := as.Efficiency
	require.NotNil(t, e)
	require.Equal(t, 1, e.CostComplete)
	require.Equal(t, 1, e.CostPartial)
	require.Equal(t, 1, e.CostUnknown)
	require.InDelta(t, 0.30, e.MeanCostUSD, 1e-12)
	require.Equal(t, 2, e.TimedOutSteps)
	require.InDelta(t, float64(1000+(1000+600000)+600000)/3, e.MeanActiveMS, 1e-9)
}

// TestSummarizeLive_StageAConstraintCleanIsOverDeclaringTrials (plan §9.4, revision 4 C-m7): in a
// Stage A run the constraint-clean rate is over the trials whose task declares a constraint check;
// a two-arm run keeps its every-trial rule.
func TestSummarizeLive_StageAConstraintCleanIsOverDeclaringTrials(t *testing.T) {
	clean, dirty := true, false
	ts := []eval.LiveTrial{
		{TaskID: "a", Arm: eval.ArmClassic, Trial: 1, ConstraintClean: &clean},
		{TaskID: "a", Arm: eval.ArmClassic, Trial: 2, ConstraintClean: &dirty, ConstraintViolations: 1},
		{TaskID: "b", Arm: eval.ArmClassic, Trial: 1}, // its task declares none
		{TaskID: "b", Arm: eval.ArmClassic, Trial: 2},
	}
	cc := eval.SummarizeLive("r", liveAnalysis(), ts).Arms[eval.ArmClassic].ConstraintClean
	require.Equal(t, 1, cc.K)
	require.Equal(t, 2, cc.N, "the two trials whose task declares no constraint check are not counted clean")

	var legacy []eval.LiveTrial
	for _, tr := range ts {
		tr.Arm = eval.ArmStock
		legacy = append(legacy, tr)
	}
	legacy = append(legacy, eval.LiveTrial{TaskID: "a", Arm: eval.ArmQompack, Trial: 1})
	require.Equal(t, 4, eval.SummarizeLive("r", liveAnalysis(), legacy).Arms[eval.ArmStock].ConstraintClean.N)
}

// TestApplyGrades_ConstraintClean: grading records constraint-cleanness only when the task declares a
// constraint check, and a harness failure fails it, as it fails recovery.
func TestApplyGrades_ConstraintClean(t *testing.T) {
	var tr eval.LiveTrial
	tr.ApplyGrades([]eval.LiveCheckResult{{Outcome: eval.OutcomeConstraint, Passed: true}, {Outcome: eval.OutcomeTask, Passed: true}})
	require.NotNil(t, tr.ConstraintClean)
	require.True(t, *tr.ConstraintClean)
	tr = eval.LiveTrial{}
	tr.ApplyGrades([]eval.LiveCheckResult{{Outcome: eval.OutcomeTask, Passed: true}})
	require.Nil(t, tr.ConstraintClean)

	task := eval.LiveTask{Checks: []eval.LiveCheck{{Outcome: eval.OutcomeConstraint}, {Outcome: eval.OutcomeRecovery}}}
	tr = eval.LiveTrial{HarnessError: "no result"}
	tr.ApplyHarnessFailure(task)
	require.NotNil(t, tr.ConstraintClean)
	require.False(t, *tr.ConstraintClean)
	require.False(t, *tr.Recovered)
}
