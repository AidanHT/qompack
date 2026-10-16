package commands_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/eval"
)

// stageAAnalysisCmd is a four-arm set's rule: margin 0.10 and amendment 4's block.
func stageAAnalysisCmd() eval.LiveAnalysis {
	return eval.LiveAnalysis{
		Model: "claude-opus-5-5", Confidence: 0.95, NonInferiorityMargin: 0.10,
		ArmTrialsPerTask: map[string]int{"stock": 2, "classic": 4, "generic": 4, "steer": 4},
		Efficiency: &eval.LiveEfficiency{
			Margin: 0.10, Metrics: []string{"cost", "active_time"},
			Guards:        []eval.LiveEfficiencyGuard{{Treated: "steer", Comparator: "classic", Gated: true}},
			StepTimeoutMS: 600000, PilotRedesignRatio: 1.25,
		},
	}
}

// stageATrialsCmd is a deterministic Stage A run over six tasks: on every task classic recovers on
// one of four trials, generic on two and steer on recoveredSteer; every trial succeeds and is
// constraint-clean; steer's cost is steerCost × classic's.
func stageATrialsCmd(recoveredSteer int, steerCost float64) []eval.LiveTrial {
	var out []eval.LiveTrial
	recovered := map[string]int{"stock": 0, "classic": 1, "generic": 2, "steer": recoveredSteer}
	counts := stageAAnalysisCmd().ArmTrialsPerTask
	for i := range 6 {
		for _, arm := range eval.LiveArms {
			for n := 1; n <= counts[arm]; n++ {
				rec, clean := n <= recovered[arm], true
				cost := 1.0
				if arm == "steer" {
					cost = steerCost
				}
				out = append(out, eval.LiveTrial{
					TaskID: fmt.Sprintf("t%d", i), Arm: arm, Trial: n, Completed: true, TaskSuccess: true, Recovered: &rec,
					ConstraintClean: &clean, PreregisteredModel: true, HostCostUSD: cost,
					Steps: []eval.LiveStepRecord{{ID: "s", ResultSubtype: "success", HasResult: true, DurationMS: 60000}},
				})
			}
		}
	}
	return out
}

// stageARun is a confirmatory Stage A run of a test-only four-arm pre-registration.
func stageARun(t *testing.T, trials []eval.LiveTrial) commands.LiveEvalInput {
	t.Helper()
	a := stageAAnalysisCmd()
	eval.LivePreregistrations["qompack-live-stagea-cmd-test"] = eval.LivePreregistration{
		Document: "test-only", TaskSetFile: "testdata/eval/live/none.json", TaskSetSHA256: "aa", FixtureTreeSHA256: "bb",
		Install: "plugin-dir", Model: "claude-opus-5-5", ModelContingency: "opus", RequiredFixed: []string{"C1.12"},
		Arms: eval.LiveArms, ArmTrialsPerTask: a.ArmTrialsPerTask, Bundles: []string{eval.LiveBundleClassic, eval.LiveBundleMods},
	}
	t.Cleanup(func() { delete(eval.LivePreregistrations, "qompack-live-stagea-cmd-test") })
	plan := eval.LivePlan{
		RunID: "20261016T220000Z-abcdef", CreatedAt: "2026-10-16T22:00:00Z", TaskSet: "qompack-live-stagea-cmd-test",
		TaskSetSHA256: "aa", FixtureTreeSHA256: "bb", TaskSetTasks: 6, Model: "claude-opus-5-5",
		PreregisteredModel: "claude-opus-5-5", Arms: eval.LiveArms, ArmTrials: a.ArmTrialsPerTask, Install: "plugin-dir",
		Plugin:           &eval.LivePluginIdentity{Install: "plugin-dir", Version: "0.4.0", Commit: "c0ffee"},
		PluginMods:       &eval.LivePluginIdentity{Install: "plugin-dir", Version: "0.4.0", Commit: "c0ffee", Mods: true},
		KnownDefects:     &eval.LiveDefectAttestation{Open: []string{}, Source: "live-eval --known-open-defects"},
		ClaudeCLIVersion: "2.1.300", RateTableDate: "2026-09-22", HeldOutIncluded: true, Host: "windows/amd64",
		StepTimeoutMS: 600000, Agent: "agent-executed on the real installed host (owner decision D3); not human UAT",
	}
	for _, tr := range trials {
		plan.Trials = append(plan.Trials, eval.LivePlannedTrial{Task: tr.TaskID, Arm: tr.Arm, Trial: tr.Trial})
	}
	return commands.LiveEvalInput{Source: "dist/live-eval/" + plan.RunID, Plan: plan, Summary: eval.SummarizeLive(plan.RunID, a, trials)}
}

// TestEval_LiveStageARunIsJudgedByTheStageADecisions (W6 items 2 and 14): `qompack eval` judges a
// confirmatory Stage A run by its decisions, not by the two-arm verdict: H1, H2 and S shown with no
// harm passes it; a gated cost guard showing harm fails it; a benefit not shown leaves it inconclusive
// ("off, unmeasured"). It prints the per-arm trials of a plan whose arms differ, both bundles, every
// test and each arm's cost and active time, and the failed-trial treatment names no two-arm void.
func TestEval_LiveStageARunIsJudgedByTheStageADecisions(t *testing.T) {
	ship := stageARun(t, stageATrialsCmd(4, 1.0))
	require.Equal(t, eval.LiveVerdictShip, ship.Summary.Decision.Verdict, ship.Summary.Decision.Reason)
	out, err := runWith(t, evalDeps(liveOnly(ship), nil), "eval", "--json")
	require.NoError(t, err)
	rep := decodeEval(t, out)
	require.True(t, rep.Live.Confirmatory, "%v", rep.Live.NotConfirmatory)
	require.Equal(t, commands.VerdictPass, rep.Verdict)
	require.NotNil(t, rep.Live.StageA)
	require.Equal(t, 0, rep.Live.TrialsPerArm)
	require.Equal(t, map[string]int{"stock": 2, "classic": 4, "generic": 4, "steer": 4}, rep.Live.ArmTrials)
	h1 := liveGate(t, rep.Recovery, "LIVE-H1")
	require.NotNil(t, h1.Passed)
	require.True(t, *h1.Passed)
	require.True(t, *liveGate(t, rep.Recovery, "LIVE-H2").Passed)
	require.True(t, *liveGate(t, rep.Task, "LIVE-S").Passed)
	require.Nil(t, liveGate(t, rep.Task, "LIVE-HARM-COST").Passed, "a guard that shows no harm is never a pass")
	require.Nil(t, liveGate(t, rep.Task, "LIVE-H3").Passed, "H3 is a claim, not a ship condition")

	text, err := runWith(t, evalDeps(liveOnly(ship), nil), "eval")
	require.NoError(t, err)
	for _, want := range []string{
		"sample: 6 task(s) × per arm stock 2, classic 4, generic 4, steer 4 trial(s) per task",
		"bundle-mods 0.4.0 at commit c0ffee", "decision (pre-registered rule): ship",
		"Stage A decisions (paired task bootstrap, 95 % percentile intervals, B = 10000 over 6 task(s)",
		"H1                     steer vs classic, recovery: 0.750 [0.750, 0.750] — shown",
		"harm-cost              steer vs classic, cost: 1.000 [1.000, 1.000] — not shown",
		"cost C complete 24, partial 0, unknown 0 (never zero)",
		"reported classic-vs-stock-recovery",
	} {
		require.Contains(t, text, want)
	}

	harm := stageARun(t, stageATrialsCmd(4, 1.5))
	out, err = runWith(t, evalDeps(liveOnly(harm), nil), "eval", "--json")
	require.Error(t, err)
	rep = decodeEval(t, out)
	require.Equal(t, commands.VerdictFail, rep.Verdict)
	g := liveGate(t, rep.Task, "LIVE-HARM-COST")
	require.NotNil(t, g.Passed)
	require.False(t, *g.Passed)

	noShip := stageARun(t, stageATrialsCmd(1, 1.0))
	require.Equal(t, eval.LiveVerdictNoShip, noShip.Summary.Decision.Verdict)
	out, err = runWith(t, evalDeps(liveOnly(noShip), nil), "eval", "--json")
	require.NoError(t, err)
	rep = decodeEval(t, out)
	require.Equal(t, commands.VerdictInconclusive, rep.Verdict, "not shown is off, unmeasured — never a pass, never harm")
	require.Nil(t, liveGate(t, rep.Recovery, "LIVE-H1").Passed)

	failed := stageATrialsCmd(4, 1.0)
	failed[0].HarnessError, failed[0].Completed = "step 1 of 1: no result within 10m0s", false
	out, err = runWith(t, evalDeps(liveOnly(stageARun(t, failed)), nil), "eval", "--json")
	require.NoError(t, err)
	rep = decodeEval(t, out)
	require.Contains(t, rep.Live.FailedTreatment, "set aside in its slot and run again")
	require.NotContains(t, rep.Live.FailedTreatment, "not-applicable")
}

// TestEval_LiveStageAPausedRunAndItsSubscriptionUse (task 3 concern 5): a paused Stage A run reads as
// paused and not yet decided, its limited trials and each arm's per-trial subscription use are shown,
// and the attempts set aside for a resident daemon of another build are counted.
func TestEval_LiveStageAPausedRunAndItsSubscriptionUse(t *testing.T) {
	trials := stageATrialsCmd(4, 1.0)
	d := 0.02
	for i := range trials {
		trials[i].RateUse = map[string]eval.LiveRateDelta{"five_hour": {Delta: &d}}
	}
	run := stageARun(t, trials[:len(trials)-3])
	for _, tr := range trials[len(trials)-3:] {
		run.Plan.Trials = append(run.Plan.Trials, eval.LivePlannedTrial{Task: tr.TaskID, Arm: tr.Arm, Trial: tr.Trial})
	}
	limited := trials[len(trials)-1]
	limited.Limited = "rejected five_hour"
	run.Summary.AddLimited(stageAAnalysisCmd(), trials[:len(trials)-3], []eval.LiveTrial{limited})
	run.Summary.Pause(len(trials)-3, len(trials), "the seven-day window is at 0.62, at or above the ceiling 0.60")
	run.Summary.ResidentDaemonFaults = 1

	out, err := runWith(t, evalDeps(liveOnly(run), nil), "eval", "--json")
	require.NoError(t, err)
	rep := decodeEval(t, out)
	require.Equal(t, commands.VerdictInconclusive, rep.Verdict)
	require.False(t, rep.Live.Confirmatory)
	reasons := strings.Join(rep.Live.NotConfirmatory, "\n")
	require.Contains(t, reasons, "3 planned trial(s) did not run")
	require.Contains(t, reasons, "it paused before its plan was done")
	require.Equal(t, map[string]int{"steer": 1}, rep.Live.LimitedTrials)

	text, err := runWith(t, evalDeps(liveOnly(run), nil), "eval")
	require.NoError(t, err)
	for _, want := range []string{
		"PAUSED: the seven-day window is at 0.62", "--resume",
		"subscription use per trial, five_hour: mean 0.0200, max 0.0200",
		"limited trials: 1, set aside and run again",
		"1 attempt(s) set aside for a resident daemon of another build (ruling R42)",
	} {
		require.Contains(t, text, want)
	}
}
