package eval_test

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// stageAAnalysis is the test set's pre-registered rule: margin 0.10 for S, and amendment 4's block.
func stageAAnalysis() eval.LiveAnalysis {
	return eval.LiveAnalysis{
		Model: "claude-opus-5-5", Confidence: 0.95, NonInferiorityMargin: 0.10,
		ArmTrialsPerTask: map[string]int{"stock": 1, "classic": 1, "generic": 1, "steer": 1},
		Efficiency: &eval.LiveEfficiency{
			Margin: 0.10, Metrics: []string{"cost", "active_time"},
			Guards: []eval.LiveEfficiencyGuard{
				{Treated: "steer", Comparator: "classic", Gated: true}, {Treated: "classic", Comparator: "stock"},
			},
			StepTimeoutMS: 600000, PilotRedesignRatio: 1.25,
		},
	}
}

// synthArm is one arm of a synthetic run: its recovery and task-success rates, its cost and time
// relative to classic's, and its trials per task.
type synthArm struct {
	recovery, success, cost, time float64
	n                             int
}

// synthRun simulates a Stage A run over k tasks: recovery and task success vary by task (shared by
// the arms), and cost and time follow R5's model (σ_w for cost, 0.35 for time).
func synthRun(seed uint64, k int, sigmaW float64, arms map[string]synthArm) []eval.LiveTrial {
	rng := rand.New(rand.NewPCG(seed, 2))
	logit := func(p float64) float64 { return math.Log(p / (1 - p)) }
	sigmoid := func(x float64) float64 { return 1 / (1 + math.Exp(-x)) }
	var out []eval.LiveTrial
	for i := range k {
		task := fmt.Sprintf("t%02d", i)
		rTask, sTask := 0.5*rng.NormFloat64(), 0.5*rng.NormFloat64()
		aCost, aTime := 0.5*rng.NormFloat64(), 0.5*rng.NormFloat64()
		for _, arm := range []string{"stock", "classic", "generic", "steer"} {
			spec, ok := arms[arm]
			if !ok {
				continue
			}
			dCost, dTime := 0.10*rng.NormFloat64(), 0.10*rng.NormFloat64()
			for j := range spec.n {
				rec := rng.Float64() < sigmoid(logit(spec.recovery)+rTask)
				clean := true
				c := 0.5 * math.Exp(aCost+math.Log(spec.cost)+dCost+sigmaW*rng.NormFloat64())
				tm := 60000 * math.Exp(aTime+math.Log(spec.time)+dTime+0.35*rng.NormFloat64())
				step := int64(tm / 3)
				out = append(out, eval.LiveTrial{
					TaskID: task, Arm: arm, Trial: j + 1, Completed: true, PreregisteredModel: true,
					TaskSuccess: rng.Float64() < sigmoid(logit(spec.success)+sTask), Recovered: &rec, ConstraintClean: &clean,
					HostCostUSD: c, Steps: steps(step, step, step),
				})
			}
		}
	}
	return out
}

// mainRun is the illustrated design at 24 tasks × 14: steer recovers well above classic and
// generic, task success is equal, and steer's cost and time are costRatio and timeRatio × classic's.
func mainRun(seed uint64, costRatio, timeRatio float64) []eval.LiveTrial {
	return synthRun(seed, 24, 0.25, map[string]synthArm{
		"stock":   {recovery: 0.40, success: 0.90, cost: 1, time: 1, n: 3},
		"classic": {recovery: 0.50, success: 0.90, cost: 1, time: 1, n: 14},
		"generic": {recovery: 0.60, success: 0.90, cost: 1, time: 1, n: 10},
		"steer":   {recovery: 0.85, success: 0.90, cost: costRatio, time: timeRatio, n: 14},
	})
}

func label(t *testing.T, s *eval.LiveStageA, id string) string {
	t.Helper()
	got, ok := s.Test(id)
	require.True(t, ok, "no test %s in %+v", id, s.Tests)
	return got.Label
}

// TestDecideStageA_KnownNull (amendment 4): at a true cost and time ratio of 1.0, 24 × 14 and σ_w
// 0.25, with a real recovery effect, no guard shows harm and H3 is shown; H1, H2 and S are shown, so
// F1 ships. The summary carries the decision in place of the two-arm verdict.
func TestDecideStageA_KnownNull(t *testing.T) {
	sum := eval.SummarizeLive("r", stageAAnalysis(), mainRun(41, 1.0, 1.0))
	s := sum.StageA
	require.NotNil(t, s)
	require.Equal(t, eval.LiveBootstrapB, s.B)
	require.Equal(t, 24, s.Tasks)
	require.True(t, s.Decided)
	for id, want := range map[string]string{
		"H1": "shown", "H2": "shown", "S": "shown", "harm-task-success": "not shown", "harm-constraint-clean": "not shown",
		"harm-cost": "not shown", "harm-active_time": "not shown", "H3": "shown",
	} {
		require.Equal(t, want, label(t, s, id), id)
	}
	h3, _ := s.Test("H3")
	require.Contains(t, h3.Reason, "steering's cost increase was shown to be under 10 %")
	require.Contains(t, h3.Sensitivity, "completed")
	require.Contains(t, h3.Sensitivity, "observed")
	require.Contains(t, h3.Sensitivity, "geometric")
	h1, _ := s.Test("H1")
	require.NotNil(t, h1.Newcombe, "Newcombe is reported beside the bootstrap's interval")
	require.Equal(t, eval.LiveVerdictShip, sum.Decision.Verdict, sum.Decision.Reason)
	require.Equal(t, s.Decision, sum.Decision)
	require.Nil(t, sum.TaskSuccessDiff, "the two-arm qompack − stock difference is gone")
	require.Contains(t, s.Outcome, "F1 ships on")
}

// TestDecideStageA_KnownHarm (amendment 4): at a true cost ratio of 1.3, the gated cost guard shows
// harm and nothing ships, whatever the benefit claims say.
func TestDecideStageA_KnownHarm(t *testing.T) {
	s := eval.SummarizeLive("r", stageAAnalysis(), mainRun(42, 1.3, 1.0)).StageA
	require.Equal(t, "harm shown", label(t, s, "harm-cost"))
	require.Equal(t, "not shown", label(t, s, "H3"))
	require.Equal(t, eval.LiveVerdictHarm, s.Decision.Verdict)
	require.Contains(t, s.Decision.Reason, "harm-cost harm shown")
}

// TestDecideStageA_SequenceStops: when H2 fails (steer recovers no better than generic), H3 reads "not
// tested" and nothing ships; when H1 fails, H2 and H3 are not tested.
func TestDecideStageA_SequenceStops(t *testing.T) {
	trials := synthRun(43, 24, 0.25, map[string]synthArm{
		"classic": {recovery: 0.50, success: 0.90, cost: 1, time: 1, n: 14},
		"generic": {recovery: 0.85, success: 0.90, cost: 1, time: 1, n: 14},
		"steer":   {recovery: 0.85, success: 0.90, cost: 1, time: 1, n: 14},
	})
	s := eval.SummarizeLive("r", stageAAnalysis(), trials).StageA
	require.Equal(t, "shown", label(t, s, "H1"))
	require.Equal(t, "not shown", label(t, s, "H2"))
	require.Equal(t, "not tested", label(t, s, "H3"))
	require.Equal(t, eval.LiveVerdictNoShip, s.Decision.Verdict)
	require.Contains(t, s.Outcome, "W7 only")

	trials = synthRun(44, 24, 0.25, map[string]synthArm{
		"classic": {recovery: 0.60, success: 0.90, cost: 1, time: 1, n: 14},
		"generic": {recovery: 0.60, success: 0.90, cost: 1, time: 1, n: 14},
		"steer":   {recovery: 0.60, success: 0.90, cost: 1, time: 1, n: 14},
	})
	s = eval.SummarizeLive("r", stageAAnalysis(), trials).StageA
	require.Equal(t, "not shown", label(t, s, "H1"))
	require.Equal(t, "not tested", label(t, s, "H2"))
	require.Equal(t, "not tested", label(t, s, "H3"))
	require.Contains(t, s.Outcome, "steering was not shown to help")
}

// TestDecideStageA_TaskSuccessHarmAndS: steer far below classic on task success shows harm and fails S;
// the pre-registered fallback reads the bootstrap's 20th percentile instead of the 2.5th.
func TestDecideStageA_TaskSuccessHarmAndS(t *testing.T) {
	trials := synthRun(45, 24, 0.25, map[string]synthArm{
		"classic": {recovery: 0.50, success: 0.90, cost: 1, time: 1, n: 14},
		"generic": {recovery: 0.60, success: 0.90, cost: 1, time: 1, n: 14},
		"steer":   {recovery: 0.85, success: 0.60, cost: 1, time: 1, n: 14},
	})
	s := eval.SummarizeLive("r", stageAAnalysis(), trials).StageA
	require.Equal(t, "harm shown", label(t, s, "harm-task-success"))
	require.Equal(t, "not shown", label(t, s, "S"))
	require.Equal(t, eval.LiveVerdictHarm, s.Decision.Verdict)

	// A difference whose 2.5th percentile is below -0.10 but whose 20th is above it: S fails by the
	// sized test and passes by the fallback.
	trials = synthRun(46, 24, 0.25, map[string]synthArm{
		"classic": {recovery: 0.50, success: 0.90, cost: 1, time: 1, n: 3},
		"generic": {recovery: 0.60, success: 0.90, cost: 1, time: 1, n: 3},
		"steer":   {recovery: 0.95, success: 0.86, cost: 1, time: 1, n: 3},
	})
	a := stageAAnalysis()
	sized := eval.SummarizeLive("r", a, trials).StageA
	st, _ := sized.Test("S")
	require.Less(t, st.Interval.Low, -0.10)
	require.Greater(t, st.Interval.P20, -0.10)
	require.Equal(t, "not shown", st.Label)
	a.SFallback = true
	fb := eval.SummarizeLive("r", a, trials).StageA
	require.Equal(t, "shown", label(t, fb, "S"))
	if label(t, fb, "H1") == "shown" && label(t, fb, "H2") == "shown" && fb.Decision.Verdict == eval.LiveVerdictShip {
		require.Contains(t, fb.Outcome, "non-inferiority on task success was not shown")
	}
}

// TestDecideStageA_AHangCountsTheCapAndImputesCostAdversely (amendment 4): a steer trial that hung on
// its first step with no result line counts the step timeout in T, and its unknown C takes its task's
// maximum complete C in steer; the observed-only ratio leaves it out.
func TestDecideStageA_AHangCountsTheCapAndImputesCostAdversely(t *testing.T) {
	yes := true
	trial := func(task, arm string, n int, cost float64, st []eval.LiveStepRecord) eval.LiveTrial {
		return eval.LiveTrial{
			TaskID: task, Arm: arm, Trial: n, Completed: true, PreregisteredModel: true, TaskSuccess: true, Recovered: &yes,
			ConstraintClean: &yes, HostCostUSD: cost, Steps: st,
		}
	}
	var ts []eval.LiveTrial
	for _, task := range []string{"a", "b"} {
		ts = append(ts,
			trial(task, "classic", 1, 1.0, steps(1000, 1000, 1000)), trial(task, "classic", 2, 1.0, steps(1000, 1000, 1000)),
			trial(task, "steer", 1, 1.0, steps(1000, 1000, 1000)), trial(task, "steer", 2, 2.0, steps(1000, 1000, 1000)))
	}
	hung := trial("a", "steer", 3, 0, steps(-1, -1, -1))
	hung.Completed = false
	ts = append(ts, hung)
	require.Equal(t, int64(600000), hung.ActiveMS(600000))
	_, status := hung.Cost()
	require.Equal(t, eval.LiveCostUnknown, status)

	s := eval.DecideStageA(stageAAnalysis(), ts, nil)
	cost, _ := s.Test("harm-cost")
	// Steer: a = {1, 2, 2 (imputed: task a's max complete steer C)}, b = {1, 2}; classic all 1.
	require.InDelta(t, (1.0+2+2+1+2)/5, cost.Interval.Estimate, 1e-12)
	require.InDelta(t, (1.0+2+1+2)/4, cost.Sensitivity["observed"].Estimate, 1e-12, "observed-only leaves the unknown out")
	tm, _ := s.Test("harm-active_time")
	require.InDelta(t, (4*3000.0+600000)/5/3000, tm.Interval.Estimate, 1e-12, "T counts the cap")
}

// TestDecideStageA_PartialCostsMoveTowardHarm (amendment 4): a 3 % share of partial costs in steer,
// imputed adversely, moves the cost ratio — and its lower bound — toward harm, never away from it; a
// comparator's unknown C takes its task's minimum, never zero.
func TestDecideStageA_PartialCostsMoveTowardHarm(t *testing.T) {
	trials := mainRun(47, 1.0, 1.0)
	partial := 0
	for i := range trials {
		if trials[i].Arm == "steer" && i%33 == 0 {
			trials[i].Steps = steps(1000, -1) // cut in its second step: C a lower bound
			trials[i].HostCostUSD *= 0.3
			partial++
		}
	}
	require.InDelta(t, 0.03, float64(partial)/(24*14), 0.01)
	s := eval.DecideStageA(stageAAnalysis(), trials, nil)
	cost, _ := s.Test("harm-cost")
	require.Greater(t, cost.Interval.Estimate, cost.Sensitivity["observed"].Estimate)
	require.Greater(t, cost.Interval.Low, cost.Sensitivity["observed"].Low)

	yes := true
	ts := []eval.LiveTrial{
		{TaskID: "a", Arm: "classic", Trial: 1, HostCostUSD: 2, Steps: steps(1), Recovered: &yes, PreregisteredModel: true},
		{TaskID: "a", Arm: "classic", Trial: 2, HostCostUSD: 0, Steps: steps(-1), Recovered: &yes, PreregisteredModel: true},
		{TaskID: "a", Arm: "steer", Trial: 1, HostCostUSD: 2, Steps: steps(1), Recovered: &yes, PreregisteredModel: true},
	}
	cost, _ = eval.DecideStageA(stageAAnalysis(), ts, nil).Test("harm-cost")
	require.InDelta(t, 1.0, cost.Interval.Estimate, 1e-12, "the comparator's unknown C takes its task's minimum (2), not 0")
}

// TestDecideStageA_LimitedSensitivityMakesAGuardIndeterminate (amendment 4): adding each limited
// trial's partial C to its slot's re-run flips the gated cost guard, so it reads "indeterminate",
// which counts as harm shown; without limited trials the guard has no limited sensitivity.
func TestDecideStageA_LimitedSensitivityMakesAGuardIndeterminate(t *testing.T) {
	yes := true
	var scored, limited []eval.LiveTrial
	for _, task := range []string{"a", "b", "c", "d"} {
		for n := 1; n <= 5; n++ {
			for arm, cost := range map[string]float64{"classic": 1.0, "steer": 1.12} {
				scored = append(scored, eval.LiveTrial{
					TaskID: task, Arm: arm, Trial: n, Completed: true, PreregisteredModel: true, TaskSuccess: true,
					Recovered: &yes, ConstraintClean: &yes, HostCostUSD: cost, Steps: steps(1000, 1000, 1000),
				})
			}
		}
		for n := 1; n <= 2; n++ {
			limited = append(limited, eval.LiveTrial{
				TaskID: task, Arm: "classic", Trial: n, HostCostUSD: 0.1, Steps: steps(1000, -1), Limited: "rejected",
			})
		}
	}
	plain := eval.DecideStageA(stageAAnalysis(), scored, nil)
	require.Equal(t, "harm shown", label(t, plain, "harm-cost"), "steer's cost is 1.12 × classic's on every trial")
	cost, _ := plain.Test("harm-cost")
	require.NotContains(t, cost.Sensitivity, "limited")

	s := eval.DecideStageA(stageAAnalysis(), scored, limited)
	cost, _ = s.Test("harm-cost")
	require.InDelta(t, 1.12/1.04, cost.Sensitivity["limited"].Estimate, 1e-12)
	require.Equal(t, "indeterminate", cost.Label)
	require.Equal(t, eval.LiveVerdictHarm, s.Decision.Verdict, "an indeterminate gated guard counts as harm shown")

	// The summary path: AddLimited re-decides with the limited trials.
	sum := eval.SummarizeLive("r", stageAAnalysis(), scored)
	sum.AddLimited(stageAAnalysis(), scored, limited)
	require.Equal(t, "indeterminate", label(t, sum.StageA, "harm-cost"))
	require.Equal(t, eval.LiveVerdictHarm, sum.Decision.Verdict)
}

// TestDecideStageA_ClassicVsStockIsALabel (amendment 4): classic's cost overhead over stock is decided
// as a label for the docs; shown above 10 %, it does not gate F1.
func TestDecideStageA_ClassicVsStockIsALabel(t *testing.T) {
	trials := synthRun(48, 24, 0.25, map[string]synthArm{
		"stock":   {recovery: 0.40, success: 0.90, cost: 1, time: 1, n: 14},
		"classic": {recovery: 0.50, success: 0.90, cost: 1.4, time: 1, n: 14},
		"generic": {recovery: 0.60, success: 0.90, cost: 1.4, time: 1, n: 10},
		"steer":   {recovery: 0.85, success: 0.90, cost: 1.4, time: 1, n: 14},
	})
	s := eval.SummarizeLive("r", stageAAnalysis(), trials).StageA
	lab, ok := s.Test("label-cost-classic-vs-stock")
	require.True(t, ok)
	require.False(t, lab.Gated)
	require.Equal(t, "harm shown", lab.Label)
	require.Contains(t, lab.Reason, "Qompack 0.3.x's cost overhead was shown above 10 %")
	require.Equal(t, eval.LiveVerdictShip, s.Decision.Verdict, "the label does not gate F1")
}

// TestDecideStageA_AnotherModelDecidesOnlyHarm (plan §9.8): a run on a model other than the
// pre-registered one (the Sonnet check) reports its benefit intervals but decides only harm.
func TestDecideStageA_AnotherModelDecidesOnlyHarm(t *testing.T) {
	trials := mainRun(49, 1.0, 1.0)
	for i := range trials {
		trials[i].PreregisteredModel = false
	}
	s := eval.SummarizeLive("r", stageAAnalysis(), trials).StageA
	require.False(t, s.Decided)
	h1, _ := s.Test("H1")
	require.Equal(t, "not tested", h1.Label)
	require.NotNil(t, h1.Interval, "the interval is still reported")
	require.Equal(t, "not tested", label(t, s, "S"))
	require.Equal(t, "not tested", label(t, s, "H3"))
	require.Equal(t, "not shown", label(t, s, "harm-cost"))
	require.Equal(t, eval.LiveVerdictNoHarm, s.Decision.Verdict)

	harmed := mainRun(50, 1.4, 1.0)
	for i := range harmed {
		harmed[i].PreregisteredModel = false
	}
	require.Equal(t, eval.LiveVerdictHarm, eval.SummarizeLive("r", stageAAnalysis(), harmed).Decision.Verdict)
}

// TestDecideStageA_NeedsSteerAndClassic: a Stage A run without the steer or classic arm reaches no
// verdict, and says which arms it had.
func TestDecideStageA_NeedsSteerAndClassic(t *testing.T) {
	sum := eval.SummarizeLive("r", stageAAnalysis(), synthRun(51, 3, 0.25, map[string]synthArm{
		"stock": {recovery: 0.4, success: 0.9, cost: 1, time: 1, n: 2}, "classic": {recovery: 0.5, success: 0.9, cost: 1, time: 1, n: 2},
	}))
	require.Equal(t, eval.LiveVerdictNotApplicable, sum.Decision.Verdict)
	require.Contains(t, sum.Decision.Reason, "classic, stock")
	require.NotEmpty(t, sum.StageA.Reported, "classic vs stock is still reported")
}

// TestDecideStageA_ReportedRows (plan §9.4 "reported, never decided"): classic vs stock, generic vs
// classic and steer vs stock on the outcomes; time non-inferiority and the cost and time reductions;
// generic vs classic and steer vs stock on C and T; the steer − classic median compaction duration
// with its interval; and the utilization-against-C fit.
func TestDecideStageA_ReportedRows(t *testing.T) {
	trials := mainRun(52, 1.0, 1.0)
	for i := range trials {
		trials[i].Compactions = []eval.HostCompaction{{Trigger: "manual", DurationMS: int64(30000 + 100*i), PostTokens: 900}}
		d := 0.01 * trials[i].HostCostUSD
		trials[i].RateUse = map[string]eval.LiveRateDelta{"five_hour": {Delta: &d}}
	}
	s := eval.SummarizeLive("r", stageAAnalysis(), trials).StageA
	ids := map[string]eval.LiveTest{}
	for _, r := range s.Reported {
		ids[r.ID] = r
		require.Equal(t, "reported", r.Label, r.ID)
		require.False(t, r.Gated, r.ID)
	}
	for _, id := range []string{
		"classic-vs-stock-recovery", "generic-vs-classic-task_success", "steer-vs-stock-constraint_clean",
		"time-non-inferiority", "cost-reduction", "time-reduction", "compaction-duration-median",
		"generic-vs-classic-cost", "steer-vs-stock-active_time",
	} {
		require.Contains(t, ids, id)
		require.NotNil(t, ids[id].Interval, id)
	}
	require.Len(t, s.UtilizationFits, 1)
	f := s.UtilizationFits[0]
	require.Equal(t, "five_hour", f.Window)
	require.InDelta(t, 0.01, f.Slope, 1e-9)
	require.InDelta(t, 1.0, f.R2, 1e-9)
	e := eval.SummarizeLive("r", stageAAnalysis(), trials).Arms["steer"].Efficiency
	require.Equal(t, 14*24, e.Compactions)
	require.Equal(t, 900.0, e.PostTokens.P50)
	require.Greater(t, e.LogSDWithinCost, 0.15, "σ_w of cost is near the simulated 0.25")
	require.Less(t, e.LogSDWithinCost, 0.35)
}
