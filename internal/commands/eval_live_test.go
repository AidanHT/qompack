package commands_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/eval"
)

// liveAnalysis is the pre-registered rule the live fixtures are decided under.
func liveAnalysis() eval.LiveAnalysis {
	return eval.LiveAnalysis{Model: "claude-sonnet-5", Confidence: 0.95, NonInferiorityMargin: 0.2, TrialsPerArm: 2}
}

// liveTrials builds n trials of one arm on n/2 tasks, the first k successful.
func liveTrials(arm string, n, k int) []eval.LiveTrial {
	out := make([]eval.LiveTrial, 0, n)
	for i := range n {
		out = append(out, eval.LiveTrial{
			TaskID: fmt.Sprintf("t%02d", i/2), Arm: arm, Trial: i%2 + 1, Completed: true, TaskSuccess: i < k,
			PluginExpected: arm == eval.ArmQompack, PluginLoaded: arm == eval.ArmQompack, HostReportedPlugins: true,
			PreregisteredModel: true, EstimateComplete: true,
		})
	}
	return out
}

// confirmatoryRun is a run of the whole pre-registered design: both arms, every task including the
// held-out ones, the pre-registered model, a clean bundle, every planned trial run.
func confirmatoryRun(stockK, qompackK int) commands.LiveEvalInput {
	const n = 20
	trials := append(liveTrials(eval.ArmStock, n, stockK), liveTrials(eval.ArmQompack, n, qompackK)...)
	pre := eval.LivePreregistrations["qompack-live-v2"]
	plan := eval.LivePlan{
		RunID: "20260930T120000Z-abcdef", CreatedAt: "2026-09-30T12:00:00Z", TaskSet: "qompack-live-v2",
		TaskSetSHA256: pre.TaskSetSHA256, FixtureTreeSHA256: pre.FixtureTreeSHA256,
		TaskSetTasks: n / 2, Model: "claude-sonnet-5", PreregisteredModel: "claude-sonnet-5",
		Arms: []string{eval.ArmStock, eval.ArmQompack}, TrialsPerArm: 2, Install: "plugin-dir",
		Plugin:           &eval.LivePluginIdentity{Install: "plugin-dir", Version: "0.3.0", Commit: "c0ffee"},
		KnownDefects:     &eval.LiveDefectAttestation{Open: []string{}, Source: "live-eval --known-open-defects"},
		ClaudeCLIVersion: "2.1.280", RateTableDate: "2026-09-22", HeldOutIncluded: true, Host: "windows/amd64",
		Agent: "agent-executed on the real installed host (owner decision D3); not human UAT",
	}
	for _, t := range trials {
		plan.Trials = append(plan.Trials, eval.LivePlannedTrial{Task: t.TaskID, Arm: t.Arm, Trial: t.Trial})
	}
	return commands.LiveEvalInput{
		Source: "dist/live-eval/" + plan.RunID, Plan: plan,
		Summary: eval.SummarizeLive(plan.RunID, liveAnalysis(), trials),
	}
}

func liveOnly(l commands.LiveEvalInput) commands.EvalInput {
	return commands.EvalInput{Live: &l}
}

func liveGate(t *testing.T, gates []commands.EvalGate, id string) commands.EvalGate {
	t.Helper()
	for _, g := range gates {
		if g.ID == id {
			return g
		}
	}
	t.Fatalf("no gate %s in %+v", id, gates)
	return commands.EvalGate{}
}

// TestEval_LiveConfirmatoryRunIsJudgedByThePreregisteredRule: a confirmatory run's verdict is the
// pre-registered decision on the primary outcome, reported with its interval and the run's
// qualification — who executed it, on which model, how many trials.
func TestEval_LiveConfirmatoryRunIsJudgedByThePreregisteredRule(t *testing.T) {
	run := confirmatoryRun(19, 20)
	require.Equal(t, "non-inferior", run.Summary.Decision.Verdict)

	out, err := runWith(t, evalDeps(liveOnly(run), nil), "eval", "--json")
	require.NoError(t, err)
	rep := decodeEval(t, out)
	require.Equal(t, commands.VerdictPass, rep.Verdict)
	require.NotNil(t, rep.Live)
	require.True(t, rep.Live.Confirmatory, "%v", rep.Live.NotConfirmatory)
	require.Contains(t, rep.Live.Qualification, "agent-executed")
	require.Contains(t, rep.Live.Qualification, "not human UAT")
	require.Equal(t, "claude-sonnet-5", rep.Live.Model)
	require.Equal(t, commands.TrialCounts{Planned: 40, Ran: 40}, rep.Live.Trials)
	require.Equal(t, 10, rep.Live.Tasks)
	require.NotNil(t, rep.Live.TaskSuccessDiff)

	g := liveGate(t, rep.Task, "LIVE-T01")
	require.NotNil(t, g.Passed)
	require.True(t, *g.Passed)
	require.NotNil(t, g.Value)
	require.InDelta(t, run.Summary.TaskSuccessDiff.Estimate, *g.Value, 1e-12)
	require.Contains(t, g.Detail, "non-inferior")
	rec := liveGate(t, rep.Recovery, "LIVE-R01")
	require.Nil(t, rec.Passed, "recovery is reported with its interval, never judged (preregistration section 8)")
}

// TestEval_LivePilotIsNeverJudged: a run that is not the pre-registered design — another model,
// no held-out tasks, one arm — is reported with every interval, but no gate judges it and the
// verdict cannot be pass. The reasons are named.
func TestEval_LivePilotIsNeverJudged(t *testing.T) {
	run := confirmatoryRun(20, 20)
	run.Plan.Model = "claude-haiku-4-5-20251001"
	run.Plan.HeldOutIncluded = false

	out, err := runWith(t, evalDeps(liveOnly(run), nil), "eval", "--json")
	require.NoError(t, err)
	rep := decodeEval(t, out)
	require.Equal(t, commands.VerdictInconclusive, rep.Verdict)
	require.False(t, rep.Live.Confirmatory)
	reasons := strings.Join(rep.Live.NotConfirmatory, "\n")
	require.Contains(t, reasons, "claude-haiku-4-5-20251001")
	require.Contains(t, reasons, "held-out")
	g := liveGate(t, rep.Task, "LIVE-T01")
	require.Nil(t, g.Passed)
	require.Contains(t, g.Detail, "not a confirmatory run")
}

// TestEval_LiveNotConfirmatoryWhenTheDesignWasNarrowed covers the remaining qualification rules one
// at a time: a single arm, a dirty bundle, a subset of the task set, trials planned but never run,
// and a plan that does not say how large its task set is.
func TestEval_LiveNotConfirmatoryWhenTheDesignWasNarrowed(t *testing.T) {
	for name, c := range map[string]struct {
		mutate func(*commands.LiveEvalInput)
		want   string
	}{
		"one arm":      {func(l *commands.LiveEvalInput) { l.Plan.Arms = []string{eval.ArmQompack} }, "both arms"},
		"dirty bundle": {func(l *commands.LiveEvalInput) { l.Plan.Plugin.Dirty = true }, "uncommitted"},
		"subset":       {func(l *commands.LiveEvalInput) { l.Plan.TaskSetTasks = 11 }, "10 of the task set's 11 tasks"},
		"unrun trials": {func(l *commands.LiveEvalInput) {
			l.Plan.Trials = append(l.Plan.Trials, eval.LivePlannedTrial{Task: "t00", Arm: eval.ArmStock, Trial: 3})
		}, "1 planned trial(s) did not run"},
		"unsized plan":   {func(l *commands.LiveEvalInput) { l.Plan.TaskSetTasks = 0 }, "does not record"},
		"trials per arm": {func(l *commands.LiveEvalInput) { l.Plan.TrialsPerArm = 1 }, "trials per arm"},
	} {
		run := confirmatoryRun(19, 20)
		c.mutate(&run)
		out, err := runWith(t, evalDeps(liveOnly(run), nil), "eval", "--json")
		require.NoError(t, err, name)
		rep := decodeEval(t, out)
		require.False(t, rep.Live.Confirmatory, name)
		require.Contains(t, strings.Join(rep.Live.NotConfirmatory, "\n"), c.want, name)
		require.Nil(t, liveGate(t, rep.Task, "LIVE-T01").Passed, name)
		require.NotEqual(t, commands.VerdictPass, rep.Verdict, name)
	}
}

// TestEval_LiveInferiorOrRegressedFails: on a confirmatory run the pre-registered rule can fail the
// evaluation — an inferior primary outcome, or a constraint-clean regression whatever the primary
// verdict.
func TestEval_LiveInferiorOrRegressedFails(t *testing.T) {
	inferior := confirmatoryRun(20, 4)
	require.Equal(t, "inferior", inferior.Summary.Decision.Verdict)
	out, err := runWith(t, evalDeps(liveOnly(inferior), nil), "eval", "--json")
	require.Error(t, err)
	require.Equal(t, commands.VerdictFail, decodeEval(t, out).Verdict)

	regressed := confirmatoryRun(19, 20)
	var trials []eval.LiveTrial
	trials = append(trials, liveTrials(eval.ArmStock, 20, 19)...)
	q := liveTrials(eval.ArmQompack, 20, 20)
	for i := range q[:15] {
		q[i].ConstraintViolations = 1
	}
	trials = append(trials, q...)
	regressed.Summary = eval.SummarizeLive(regressed.Plan.RunID, liveAnalysis(), trials)
	require.NotEmpty(t, regressed.Summary.ConstraintRegression)
	out, err = runWith(t, evalDeps(liveOnly(regressed), nil), "eval", "--json")
	require.Error(t, err)
	rep := decodeEval(t, out)
	require.Equal(t, commands.VerdictFail, rep.Verdict)
	g := liveGate(t, rep.Task, "LIVE-T02")
	require.NotNil(t, g.Passed)
	require.False(t, *g.Passed)
}

// TestEval_LiveFailedTrialsAreCountedInTheDecisionAndListed: preregistration section 8 analyses every
// planned trial (intention to treat): a trial the harness could not run as designed is scored as a
// failure on every outcome and listed by name, nothing is dropped. The run's decision already counts
// its failed trials, so the command reports that decision — here non-inferior, with the harness
// failure costing the qompack arm a task success — rather than overriding it with "inconclusive"
// because a trial failed, and it names each failed trial and how it was counted. When the failures
// move the interval, the verdict moves with the decision.
func TestEval_LiveFailedTrialsAreCountedInTheDecisionAndListed(t *testing.T) {
	withHarnessFailures := func(stockK, qompackFailures int) commands.LiveEvalInput {
		run := confirmatoryRun(stockK, 20)
		trials := liveTrials(eval.ArmStock, 20, stockK)
		q := liveTrials(eval.ArmQompack, 20, 20)
		for i := range q[:qompackFailures] {
			q[i].HarnessError = "step 3 of 4: no result within 10m0s"
			q[i].Completed = false
		}
		run.Summary = eval.SummarizeLive(run.Plan.RunID, liveAnalysis(), append(trials, q...))
		return run
	}

	run := withHarnessFailures(19, 1)
	require.Equal(t, "non-inferior", run.Summary.Decision.Verdict, run.Summary.Decision.Reason)
	out, err := runWith(t, evalDeps(liveOnly(run), nil), "eval", "--json")
	require.NoError(t, err)
	rep := decodeEval(t, out)
	require.True(t, rep.Live.Confirmatory, "%v", rep.Live.NotConfirmatory)
	require.Equal(t, commands.TrialCounts{Planned: 40, Ran: 40, Failed: 1}, rep.Live.Trials)
	require.Equal(t, commands.VerdictPass, rep.Verdict, "the pre-registered decision stands")
	g := liveGate(t, rep.Task, "LIVE-T01")
	require.NotNil(t, g.Passed)
	require.True(t, *g.Passed)
	require.Contains(t, g.Detail, "1 failed trial(s) counted under intention to treat")
	var q eval.ArmSummary
	for _, as := range rep.Live.Arms {
		if as.Arm == eval.ArmQompack {
			q = as
		}
	}
	require.Equal(t, [2]int{19, 20}, [2]int{q.TaskSuccess.K, q.TaskSuccess.N}, "the failed trial is a failure, not dropped")
	require.Len(t, rep.Live.Failed, 1)
	require.True(t, strings.HasPrefix(rep.Live.Failed[0], "t00/qompack/1: "), rep.Live.Failed[0])
	require.Contains(t, rep.Live.FailedTreatment, "intention to treat")
	require.Contains(t, rep.Live.FailedTreatment, "failure on every outcome")

	text, err := runWith(t, evalDeps(liveOnly(run), nil), "eval")
	require.NoError(t, err)
	require.Contains(t, text, "eval: PASS")
	require.Contains(t, text, "failed trials: 1, "+rep.Live.FailedTreatment)
	require.Contains(t, text, "failed trial: t00/qompack/1: ")

	// Four harness failures leave the interval straddling the margin: the decision is inconclusive,
	// and so is the verdict, because the rule reached no verdict.
	run = withHarnessFailures(20, 4)
	require.Equal(t, "inconclusive", run.Summary.Decision.Verdict, run.Summary.Decision.Reason)
	out, err = runWith(t, evalDeps(liveOnly(run), nil), "eval", "--json")
	require.NoError(t, err)
	rep = decodeEval(t, out)
	require.Equal(t, commands.VerdictInconclusive, rep.Verdict)
	g = liveGate(t, rep.Task, "LIVE-T01")
	require.Nil(t, g.Passed)
	require.Contains(t, g.Detail, "the pre-registered rule reached no verdict")
	require.Len(t, rep.Live.Failed, 4)
}

// TestEval_LiveNotConfirmatoryOnASupersededTaskSet: qompack-live-v1 was superseded before any trial
// of it by qompack-live-v2 (preregistration amendment A7, owner decision D12). A run of it on its
// own frozen materials is still not the confirmatory run, and the report says why and names the set
// that replaced it.
func TestEval_LiveNotConfirmatoryOnASupersededTaskSet(t *testing.T) {
	run := confirmatoryRun(19, 20)
	v1 := eval.LivePreregistrations["qompack-live-v1"]
	run.Plan.TaskSet, run.Plan.TaskSetSHA256, run.Plan.FixtureTreeSHA256 = "qompack-live-v1", v1.TaskSetSHA256, v1.FixtureTreeSHA256

	out, err := runWith(t, evalDeps(liveOnly(run), nil), "eval", "--json")
	require.NoError(t, err)
	rep := decodeEval(t, out)
	require.False(t, rep.Live.Confirmatory)
	require.Len(t, rep.Live.NotConfirmatory, 1, "its materials are its own pre-registered ones: %v", rep.Live.NotConfirmatory)
	reason := rep.Live.NotConfirmatory[0]
	for _, want := range []string{"qompack-live-v1", "superseded", "qompack-live-v2", "amendment A7"} {
		require.Contains(t, reason, want)
	}
	require.Nil(t, liveGate(t, rep.Task, "LIVE-T01").Passed)
	require.NotEqual(t, commands.VerdictPass, rep.Verdict)
}

// TestEval_ReplayAndLiveAreReportedTogether: with both artifacts the replay gates and the live gates
// sit side by side, and the text report qualifies the live run before any number of it.
func TestEval_ReplayAndLiveAreReportedTogether(t *testing.T) {
	in := inputWith(goodScore(), ranTrials(), nil)
	live := confirmatoryRun(19, 20)
	in.Live = &live

	out, err := runWith(t, evalDeps(in, nil), "eval", "--json")
	require.NoError(t, err)
	rep := decodeEval(t, out)
	require.Equal(t, commands.VerdictPass, rep.Verdict)
	liveGate(t, rep.Task, "TASK-01")
	liveGate(t, rep.Task, "LIVE-T01")

	text, err := runWith(t, evalDeps(in, nil), "eval")
	require.NoError(t, err)
	for _, want := range []string{
		"live evaluation — agent-executed on the real installed host (owner decision D3); not human UAT",
		"confirmatory: yes",
		"model claude-sonnet-5 (pre-registered claude-sonnet-5)",
		"task success after compaction, qompack − stock",
		"decision (pre-registered rule): non-inferior",
		"ESTIMATE",
		"TASK-01",
	} {
		require.Contains(t, text, want)
	}
	require.Less(t, strings.Index(text, "agent-executed"), strings.Index(text, "decision (pre-registered rule)"),
		"the qualification comes before the numbers")
}

// TestEval_AReplayCannotPassAConfirmatoryRunThatReachedNoDecision: a confirmatory live run is the
// pre-registered evaluation, so when its rule reaches no verdict on the primary outcome — a trial
// whose plugin state contradicted its arm makes the decision not-applicable, an interval that
// straddles the margin makes it inconclusive — the verdict is inconclusive, and a passing replay read
// beside it (plain `qompack eval` reads both) cannot turn that into a pass. A constraint regression
// still fails such a run: it is failed whatever the primary verdict.
func TestEval_AReplayCannotPassAConfirmatoryRunThatReachedNoDecision(t *testing.T) {
	withQompackTrials := func(stockK int, mutate func([]eval.LiveTrial)) commands.LiveEvalInput {
		run := confirmatoryRun(stockK, 20)
		q := liveTrials(eval.ArmQompack, 20, 20)
		mutate(q)
		run.Summary = eval.SummarizeLive(run.Plan.RunID, liveAnalysis(),
			append(liveTrials(eval.ArmStock, 20, stockK), q...))
		return run
	}
	mismatch := withQompackTrials(19, func(q []eval.LiveTrial) { q[0].PluginLoaded = false })
	require.Equal(t, "not-applicable", mismatch.Summary.Decision.Verdict, mismatch.Summary.Decision.Reason)
	straddles := confirmatoryRun(20, 16)
	require.Equal(t, "inconclusive", straddles.Summary.Decision.Verdict, straddles.Summary.Decision.Reason)
	regressed := withQompackTrials(19, func(q []eval.LiveTrial) {
		q[0].PluginLoaded = false
		for i := range q[:15] {
			q[i].ConstraintViolations = 1
		}
	})
	require.Equal(t, "not-applicable", regressed.Summary.Decision.Verdict, regressed.Summary.Decision.Reason)
	require.NotEmpty(t, regressed.Summary.ConstraintRegression)

	for name, c := range map[string]struct {
		run  commands.LiveEvalInput
		want commands.EvalVerdict
	}{
		"plugin mismatch":       {mismatch, commands.VerdictInconclusive},
		"interval straddles":    {straddles, commands.VerdictInconclusive},
		"regression, no ruling": {regressed, commands.VerdictFail},
	} {
		for _, replayed := range []bool{false, true} {
			in := liveOnly(c.run)
			if replayed {
				in = inputWith(goodScore(), ranTrials(), nil)
				in.Live = &c.run
			}
			out, _ := runWith(t, evalDeps(in, nil), "eval", "--json")
			rep := decodeEval(t, out)
			require.True(t, rep.Live.Confirmatory, "%s: %v", name, rep.Live.NotConfirmatory)
			require.Nil(t, liveGate(t, rep.Task, "LIVE-T01").Passed, name)
			require.Equal(t, c.want, rep.Verdict, "%s (replay read: %t)", name, replayed)
		}
	}
}

// ── the file-backed provider ─────────────────────────────────────────────────────────────────────

// writeLiveRun writes run as <dir>/plan.json and <dir>/summary.json.
func writeLiveRun(t *testing.T, dir string, run commands.LiveEvalInput) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for name, v := range map[string]any{"plan.json": run.Plan, "summary.json": run.Summary} {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), b, 0o600))
	}
}

// writeReplayReport writes the replay driver's report envelope, reduced to what the provider reads.
func writeReplayReport(t *testing.T, path string, s eval.Score) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	b, err := json.Marshal(map[string]any{
		"generator": "test/replay", "corpus": "testdata/sessions/synthetic", "corpusTier": "synthetic",
		"corpusSHA256": strings.Repeat("c", 64), "sessions": 6, "latency": "modelled",
		"phaseChecksSkipped": false,
		"report":             eval.Report{Policies: map[string]eval.Score{"qompack-l3": s, "baseline": {}}, Baseline: "baseline", Sessions: 6},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o600))
}

// TestFileEvalArtifacts_ReadsTheLatestRunAndTheReplayReport: with no --corpus the provider reads
// the newest live-eval run under dist/live-eval (by the plan's creation time) and the replay
// driver's report at testdata/bench-replay.json, both relative to the project root.
func TestFileEvalArtifacts_ReadsTheLatestRunAndTheReplayReport(t *testing.T) {
	root := t.TempDir()
	older := confirmatoryRun(20, 4)
	older.Plan.RunID, older.Plan.CreatedAt = "b-older", "2026-09-29T12:00:00Z"
	newer := confirmatoryRun(19, 20)
	newer.Plan.RunID, newer.Plan.CreatedAt = "a-newer", "2026-09-30T12:00:00Z"
	writeLiveRun(t, filepath.Join(root, "dist", "live-eval", "b-older"), older)
	writeLiveRun(t, filepath.Join(root, "dist", "live-eval", "a-newer"), newer)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dist", "live-eval", "not-a-run"), 0o755))
	writeReplayReport(t, filepath.Join(root, "testdata", "bench-replay.json"), goodScore())

	in, err := commands.FileEvalArtifacts(root)(context.Background(), "")
	require.NoError(t, err)
	require.NotNil(t, in.Live)
	require.Equal(t, "a-newer", in.Live.Plan.RunID, "the newest run by creation time, not by name")
	require.Equal(t, filepath.Join(root, "dist", "live-eval", "a-newer"), in.Live.Source)
	require.Equal(t, "baseline", in.Report.Baseline)
	require.Equal(t, commands.TrialCounts{Planned: 6, Ran: 6}, in.Trials)
	require.Contains(t, strings.Join(in.Notes, "\n"), "synthetic")

	out, err := runWith(t, evalDeps(in, nil), "eval", "--json")
	require.NoError(t, err)
	require.Equal(t, commands.VerdictPass, decodeEval(t, out).Verdict)
}

// TestFileEvalArtifacts_CorpusNamesARunARunsDirectoryOrAReplayReport: --corpus is resolved against
// the project root and may name one run, a directory of runs, or a replay report file.
func TestFileEvalArtifacts_CorpusNamesARunARunsDirectoryOrAReplayReport(t *testing.T) {
	root := t.TempDir()
	run := confirmatoryRun(19, 20)
	writeLiveRun(t, filepath.Join(root, "runs", "r1"), run)
	writeReplayReport(t, filepath.Join(root, "out", "replay.json"), goodScore())
	provide := commands.FileEvalArtifacts(root)

	in, err := provide(context.Background(), "runs/r1")
	require.NoError(t, err)
	require.NotNil(t, in.Live)
	require.Empty(t, in.Report.Policies, "naming one artifact reads only that artifact")

	in, err = provide(context.Background(), filepath.Join(root, "runs"))
	require.NoError(t, err)
	require.NotNil(t, in.Live)

	in, err = provide(context.Background(), "out/replay.json")
	require.NoError(t, err)
	require.Nil(t, in.Live)
	require.Equal(t, "baseline", in.Report.Baseline)

	_, err = provide(context.Background(), "nowhere")
	require.ErrorIs(t, err, commands.ErrUnavailable)
}

// TestFileEvalArtifacts_NothingThereIsUnavailable: a project with no evaluation artifacts gets an
// unavailable answer that names where it looked and how an evaluation is produced, and the command
// exits 1 with it rather than inventing a result.
func TestFileEvalArtifacts_NothingThereIsUnavailable(t *testing.T) {
	root := t.TempDir()
	_, err := commands.FileEvalArtifacts(root)(context.Background(), "")
	require.ErrorIs(t, err, commands.ErrUnavailable)
	require.Contains(t, err.Error(), filepath.Join("dist", "live-eval"))
	require.Contains(t, err.Error(), "devtool live-eval")

	d := testDeps()
	d.EvalArtifacts = commands.FileEvalArtifacts(root)
	_, runErr := runWith(t, d, "eval", "--json")
	require.ErrorIs(t, runErr, commands.ErrUnavailable)
	entries, readErr := os.ReadDir(root)
	require.NoError(t, readErr)
	require.Empty(t, entries, "the provider only reads")
}

// TestFileEvalArtifacts_RefusesWhatItCannotRead: a malformed summary is an error, not an empty run,
// and a summary from a newer schema is unsupported rather than decoded into zeroes.
func TestFileEvalArtifacts_RefusesWhatItCannotRead(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "r")
	writeLiveRun(t, dir, confirmatoryRun(19, 20))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "summary.json"), []byte("{not json"), 0o600))
	_, err := commands.FileEvalArtifacts(root)(context.Background(), "r")
	require.Error(t, err)
	require.False(t, errors.Is(err, commands.ErrUnavailable), "a broken artifact is a failure to report, not an absence")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "summary.json"),
		[]byte(fmt.Sprintf(`{"schema":%d}`, eval.LiveSummarySchema+1)), 0o600))
	_, err = commands.FileEvalArtifacts(root)(context.Background(), "r")
	require.ErrorIs(t, err, commands.ErrUnsupported)
}

// TestFileEvalArtifacts_ReadsTheCommittedPilotRun reads a real run the harness produced — pilot1,
// committed as C5.4 evidence — and reports it for what it is: a single-arm harness-validation run on
// the pilot task, never a confirmatory result.
func TestFileEvalArtifacts_ReadsTheCommittedPilotRun(t *testing.T) {
	pilot, err := filepath.Abs(filepath.Join("..", "..", "plans", "sdd", "V6-closeout", "eval", "runs", "pilot1-plugin-dir"))
	require.NoError(t, err)
	in, err := commands.FileEvalArtifacts(t.TempDir())(context.Background(), pilot)
	require.NoError(t, err)
	require.NotNil(t, in.Live)
	require.Equal(t, "claude-haiku-4-5-20251001", in.Live.Plan.Model)

	out, err := runWith(t, evalDeps(in, nil), "eval", "--json")
	require.NoError(t, err)
	rep := decodeEval(t, out)
	require.Equal(t, commands.VerdictInconclusive, rep.Verdict)
	require.False(t, rep.Live.Confirmatory)
	require.Contains(t, strings.Join(rep.Live.NotConfirmatory, "\n"), "both arms")
	require.Contains(t, rep.Live.Qualification, "agent-executed")
	require.Equal(t, commands.TrialCounts{Planned: 1, Ran: 1}, rep.Live.Trials)
}

// TestEval_LiveNotConfirmatoryUnlessItRanThePreregisteredMaterials: a run is the pre-registered
// study only when its plan names the frozen materials — the task set's recorded file hash and the
// fixture tree's manifest hash — and the install path the pre-registration fixes (--plugin-dir). A
// task set with no pre-registration at all is never confirmatory, whatever else it did.
func TestEval_LiveNotConfirmatoryUnlessItRanThePreregisteredMaterials(t *testing.T) {
	for name, c := range map[string]struct {
		mutate func(*commands.LiveEvalInput)
		want   string
	}{
		"edited task set": {func(l *commands.LiveEvalInput) { l.Plan.TaskSetSHA256 = strings.Repeat("a", 64) }, "task set file"},
		"edited fixtures": {func(l *commands.LiveEvalInput) { l.Plan.FixtureTreeSHA256 = strings.Repeat("b", 64) }, "fixture tree"},
		"unrecorded tree": {func(l *commands.LiveEvalInput) { l.Plan.FixtureTreeSHA256 = "" }, "fixture tree"},
		"marketplace":     {func(l *commands.LiveEvalInput) { l.Plan.Install = "marketplace" }, "--plugin-dir"},
		"unregistered":    {func(l *commands.LiveEvalInput) { l.Plan.TaskSet = "qompack-live-pilot-v1" }, "no pre-registration"},
	} {
		run := confirmatoryRun(19, 20)
		c.mutate(&run)
		out, err := runWith(t, evalDeps(liveOnly(run), nil), "eval", "--json")
		require.NoError(t, err, name)
		rep := decodeEval(t, out)
		require.False(t, rep.Live.Confirmatory, name)
		require.Contains(t, strings.Join(rep.Live.NotConfirmatory, "\n"), c.want, name)
		require.Nil(t, liveGate(t, rep.Task, "LIVE-T01").Passed, name)
		require.NotEqual(t, commands.VerdictPass, rep.Verdict, name)
	}
}

// TestFileEvalArtifacts_NamesANewerRunThatNeverFinished: a live-eval run writes plan.json before its
// first session and summary.json after its last, so a run that crashed or is still going has a plan
// and no summary. "The newest run" read without that caveat would present an older run as the
// latest evidence. The provider still reports the newest finished run, and says that a newer run
// exists without a summary.
func TestFileEvalArtifacts_NamesANewerRunThatNeverFinished(t *testing.T) {
	root := t.TempDir()
	done := confirmatoryRun(19, 20)
	done.Plan.RunID, done.Plan.CreatedAt = "20260929T120000Z-aaaaaa", "2026-09-29T12:00:00Z"
	writeLiveRun(t, filepath.Join(root, "dist", "live-eval", done.Plan.RunID), done)
	unfinished := confirmatoryRun(19, 20)
	unfinished.Plan.RunID, unfinished.Plan.CreatedAt = "20260930T120000Z-bbbbbb", "2026-09-30T12:00:00Z"
	dir := filepath.Join(root, "dist", "live-eval", unfinished.Plan.RunID)
	writeLiveRun(t, dir, unfinished)
	require.NoError(t, os.Remove(filepath.Join(dir, "summary.json")))

	in, err := commands.FileEvalArtifacts(root)(context.Background(), "")
	require.NoError(t, err)
	require.NotNil(t, in.Live)
	require.Equal(t, done.Plan.RunID, in.Live.Plan.RunID, "the newest finished run is what can be reported")

	out, err := runWith(t, evalDeps(in, nil), "eval", "--json")
	require.NoError(t, err)
	rep := decodeEval(t, out)
	require.Contains(t, strings.Join(rep.Live.Notes, "\n"), unfinished.Plan.RunID,
		"the newer run with no summary is named")

	text, err := runWith(t, evalDeps(in, nil), "eval")
	require.NoError(t, err)
	require.Contains(t, text, unfinished.Plan.RunID)
}

// TestEval_LiveNotConfirmatoryWithoutTheSection9DefectAttestation: preregistration section 9 makes
// the candidate's defect state part of what the confirmatory run is — "The confirmatory run must be
// on a candidate where C1.12 and C1.1 are fixed; a run on a candidate with a known open defect is
// labelled with that defect and is not the confirmatory run." A plan that does not attest which
// known defects its bundle carries cannot be told apart from one that carries C1.12, so it is not
// confirmatory, and the report names the precondition it could not establish. A plan that attests
// an open defect — C1.12 itself, or any other (amendment A5) — is labelled with it and not judged.
func TestEval_LiveNotConfirmatoryWithoutTheSection9DefectAttestation(t *testing.T) {
	for name, c := range map[string]struct {
		defects *eval.LiveDefectAttestation
		want    []string
	}{
		"not attested":        {nil, []string{"section 9", "C1.12 and C1.1 are fixed", "does not attest"}},
		"C1.12 open":          {&eval.LiveDefectAttestation{Open: []string{"C1.12"}}, []string{"section 9", "C1.12"}},
		"another defect open": {&eval.LiveDefectAttestation{Open: []string{"C1.11"}}, []string{"section 9", "C1.11"}},
	} {
		run := confirmatoryRun(19, 20)
		run.Plan.KnownDefects = c.defects

		out, err := runWith(t, evalDeps(liveOnly(run), nil), "eval", "--json")
		require.NoError(t, err, name)
		rep := decodeEval(t, out)
		require.False(t, rep.Live.Confirmatory, name)
		reasons := strings.Join(rep.Live.NotConfirmatory, "\n")
		for _, want := range c.want {
			require.Contains(t, reasons, want, name)
		}
		require.Nil(t, liveGate(t, rep.Task, "LIVE-T01").Passed, name)
		require.NotEqual(t, commands.VerdictPass, rep.Verdict, name)
	}
}

// TestEval_LiveConfirmatoryRunSaysItsDefectPreconditionIsAttested: a confirmatory run's section 9
// precondition rests on the operator's word, which nothing in the report can check. The report says
// so in the JSON and before any number in the text, rather than printing a bare "confirmatory: yes".
func TestEval_LiveConfirmatoryRunSaysItsDefectPreconditionIsAttested(t *testing.T) {
	run := confirmatoryRun(19, 20)
	out, err := runWith(t, evalDeps(liveOnly(run), nil), "eval", "--json")
	require.NoError(t, err)
	rep := decodeEval(t, out)
	require.True(t, rep.Live.Confirmatory, "%v", rep.Live.NotConfirmatory)
	require.NotNil(t, rep.Live.KnownDefects)
	require.Empty(t, rep.Live.KnownDefects.Open)
	require.Contains(t, rep.Live.DefectPrecondition, "not machine-checked")
	require.Contains(t, rep.Live.DefectPrecondition, "operator")

	text, err := runWith(t, evalDeps(liveOnly(run), nil), "eval")
	require.NoError(t, err)
	require.Contains(t, text, "known open defects (preregistration section 9): none open")
	require.Contains(t, text, "confirmatory: yes — its section 9 known-defect precondition rests on the operator's "+
		"attestation, not on a machine check")
	require.NotContains(t, text, "confirmatory: yes\n", "never an unqualified yes")
	require.Less(t, strings.Index(text, "not machine-checked"), strings.Index(text, "decision (pre-registered rule)"))
}

// TestEval_LiveNotConfirmatoryWhenAForeignPluginLoaded: preregistration section 3 makes the two arms
// identical except for the plugin. A trial whose host loaded some other plugin — on either arm —
// breaks that, so the comparison is not the pre-registered one; the report names the arm and the
// plugin rather than judging the run.
func TestEval_LiveNotConfirmatoryWhenAForeignPluginLoaded(t *testing.T) {
	run := confirmatoryRun(19, 20)
	trials := append(liveTrials(eval.ArmStock, 20, 19), liveTrials(eval.ArmQompack, 20, 20)...)
	trials[0].ForeignPlugins = []string{"superpowers@claude-plugins-official"}
	run.Summary = eval.SummarizeLive(run.Plan.RunID, liveAnalysis(), trials)
	require.Equal(t, 1, run.Summary.Arms[eval.ArmStock].ForeignPluginTrials)

	out, err := runWith(t, evalDeps(liveOnly(run), nil), "eval", "--json")
	require.NoError(t, err)
	rep := decodeEval(t, out)
	require.False(t, rep.Live.Confirmatory)
	reasons := strings.Join(rep.Live.NotConfirmatory, "\n")
	require.Contains(t, reasons, "superpowers@claude-plugins-official")
	require.Contains(t, reasons, "stock")
	require.Nil(t, liveGate(t, rep.Task, "LIVE-T01").Passed)
}

// TestFileEvalArtifacts_OnlyTheNewestRunMustBeReadable: the provider reports the newest run, so an
// older run's summary — truncated by a crash mid-write, say — is none of its business and must not
// make every default `qompack eval` fail. The newest run's own summary still must be readable: a
// broken newest run is an error, never silently replaced by an older one.
func TestFileEvalArtifacts_OnlyTheNewestRunMustBeReadable(t *testing.T) {
	root := t.TempDir()
	older := confirmatoryRun(20, 4)
	older.Plan.RunID, older.Plan.CreatedAt = "20260929T120000Z-aaaaaa", "2026-09-29T12:00:00Z"
	olderDir := filepath.Join(root, "dist", "live-eval", older.Plan.RunID)
	writeLiveRun(t, olderDir, older)
	newer := confirmatoryRun(19, 20)
	newer.Plan.RunID, newer.Plan.CreatedAt = "20260930T120000Z-bbbbbb", "2026-09-30T12:00:00Z"
	newerDir := filepath.Join(root, "dist", "live-eval", newer.Plan.RunID)
	writeLiveRun(t, newerDir, newer)

	truncate := func(p string) {
		raw, err := os.ReadFile(p)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(p, raw[:len(raw)/2], 0o600))
	}
	truncate(filepath.Join(olderDir, "summary.json"))

	in, err := commands.FileEvalArtifacts(root)(context.Background(), "")
	require.NoError(t, err, "an older run's broken summary does not stop the newest being reported")
	require.NotNil(t, in.Live)
	require.Equal(t, newer.Plan.RunID, in.Live.Plan.RunID)

	truncate(filepath.Join(newerDir, "summary.json"))
	_, err = commands.FileEvalArtifacts(root)(context.Background(), "")
	require.Error(t, err, "the newest run is unreadable: fail closed")
	require.False(t, errors.Is(err, commands.ErrUnavailable), "a broken artifact is a failure, not an absence")
	require.Contains(t, err.Error(), newer.Plan.RunID)
}

// TestEval_LiveTheSection3ModelContingencyCanBeConfirmatory: preregistration section 3 fixes one
// contingency — if the host rejects claude-sonnet-5, the run restarts on the host alias `sonnet`
// and the model the host resolves it to is recorded. That run is the pre-registered design, not a
// departure from it, so it can be confirmatory: when its trials' hosts all reported one resolved
// model, which the report names, and with a note that the contingency's own preconditions are not
// machine-checked. Any other model, an unrecorded resolution, or a split one is not confirmatory.
func TestEval_LiveTheSection3ModelContingencyCanBeConfirmatory(t *testing.T) {
	contingency := func(model string, hostModels ...string) commands.LiveEvalInput {
		run := confirmatoryRun(19, 20)
		run.Plan.Model = model
		trials := append(liveTrials(eval.ArmStock, 20, 19), liveTrials(eval.ArmQompack, 20, 20)...)
		for i := range trials {
			trials[i].Model = model
			if len(hostModels) > 0 {
				trials[i].HostModel = hostModels[i%len(hostModels)]
			}
		}
		run.Summary = eval.SummarizeLive(run.Plan.RunID, liveAnalysis(), trials)
		return run
	}

	run := contingency("sonnet", "claude-sonnet-5-20260915")
	out, err := runWith(t, evalDeps(liveOnly(run), nil), "eval", "--json")
	require.NoError(t, err)
	rep := decodeEval(t, out)
	require.True(t, rep.Live.Confirmatory, "%v", rep.Live.NotConfirmatory)
	require.Equal(t, []string{"claude-sonnet-5-20260915"}, rep.Live.HostModels)
	notes := strings.Join(rep.Live.Notes, "\n")
	require.Contains(t, notes, "contingency")
	require.Contains(t, notes, "not machine-checked")
	require.NotNil(t, liveGate(t, rep.Task, "LIVE-T01").Passed, "the pre-registered contingency run is judged")
	text, err := runWith(t, evalDeps(liveOnly(run), nil), "eval")
	require.NoError(t, err)
	require.Contains(t, text, "host reported claude-sonnet-5-20260915")

	for name, c := range map[string]struct {
		run  commands.LiveEvalInput
		want string
	}{
		"resolution not recorded": {contingency("sonnet"), "does not record"},
		"resolved to two models":  {contingency("sonnet", "claude-sonnet-5-20260915", "claude-sonnet-4-5"), "more than one model"},
		"another alias":           {contingency("opus", "claude-opus-5"), "not the pre-registered model"},
	} {
		out, err := runWith(t, evalDeps(liveOnly(c.run), nil), "eval", "--json")
		require.NoError(t, err, name)
		rep := decodeEval(t, out)
		require.False(t, rep.Live.Confirmatory, name)
		require.Contains(t, strings.Join(rep.Live.NotConfirmatory, "\n"), c.want, name)
		require.Nil(t, liveGate(t, rep.Task, "LIVE-T01").Passed, name)
	}
}
