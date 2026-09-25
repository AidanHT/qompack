package eval_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

func liveAnalysis() eval.LiveAnalysis {
	return eval.LiveAnalysis{Model: "m", Confidence: 0.95, NonInferiorityMargin: 0.2, TrialsPerArm: 10}
}

// trials builds n trials of one arm, the first k successful.
func trials(arm string, n, k int) []eval.LiveTrial {
	out := make([]eval.LiveTrial, 0, n)
	for i := range n {
		out = append(out, eval.LiveTrial{
			TaskID: fmt.Sprintf("t%d", i%3), Arm: arm, Trial: i + 1, Completed: true, TaskSuccess: i < k,
			PluginExpected: arm == eval.ArmQompack, PluginLoaded: arm == eval.ArmQompack, HostReportedPlugins: true,
			PreregisteredModel: true, EstimateComplete: true,
		})
	}
	return out
}

// TestSummarizeLive_WilsonAndNewcombe pins the interval arithmetic against known values: 5/10 has
// the Wilson 95% interval [0.2366, 0.7634]; 0/0 is the whole unit range; and the difference of two
// equal proportions is centred on zero.
func TestSummarizeLive_WilsonAndNewcombe(t *testing.T) {
	sum := eval.SummarizeLive("r", liveAnalysis(), append(trials(eval.ArmStock, 10, 5), trials(eval.ArmQompack, 10, 5)...))
	st := sum.Arms[eval.ArmStock].TaskSuccess
	require.Equal(t, 5, st.K)
	require.InDelta(t, 0.2366, st.Low, 1e-4)
	require.InDelta(t, 0.7634, st.High, 1e-4)
	require.Equal(t, eval.Proportion{Low: 0, High: 1}, sum.Arms[eval.ArmStock].Recovery, "no recovery checks: undefined, not zero")
	d := sum.TaskSuccessDiff
	require.NotNil(t, d)
	require.InDelta(t, 0, d.Estimate, 1e-12)
	require.InDelta(t, -d.High, d.Low, 1e-12)
	require.Equal(t, "inconclusive", sum.Decision.Verdict, "a ±0.4 interval decides nothing at a 0.2 margin")
	require.Empty(t, sum.Failed)
}

func TestDecideLive_EachBranch(t *testing.T) {
	a := liveAnalysis()
	cases := map[string]struct {
		stock, qompack int
		want           string
	}{
		"superior":     {2, 10, "superior"},
		"non-inferior": {19, 20, "non-inferior"},
		"inferior":     {20, 2, "inferior"},
		"inconclusive": {10, 8, "inconclusive"},
	}
	for name, c := range cases {
		const n = 20
		sum := eval.SummarizeLive("r", a, append(trials(eval.ArmStock, n, c.stock), trials(eval.ArmQompack, n, c.qompack)...))
		require.Equal(t, c.want, sum.Decision.Verdict, "%s: %+v", name, sum.TaskSuccessDiff)
	}

	only := eval.SummarizeLive("r", a, trials(eval.ArmQompack, 5, 5))
	require.Equal(t, "not-applicable", only.Decision.Verdict)
}

// TestSummarizeLive_FailuresAreCountedAndNamed: an incomplete trial, a harness error and a plugin
// that did not load all stay in the denominator and are named; a plugin-state mismatch voids the
// verdict; a hook failure and an off-registration model are reported as notes.
func TestSummarizeLive_FailuresAreCountedAndNamed(t *testing.T) {
	q := trials(eval.ArmQompack, 4, 4)
	q[0].Completed = false
	q[1].HarnessError = "step 2 of 4: no result within 10m0s"
	q[2].PluginLoaded = false
	q[3].HookProblems = []eval.HostHookProblem{{Event: "PreCompact", Kind: eval.HookProblemOutputRejected}}
	s := trials(eval.ArmStock, 4, 2)
	s[0].PreregisteredModel = false
	s[0].Model = "claude-haiku-4-5"

	sum := eval.SummarizeLive("r", liveAnalysis(), append(s, q...))
	require.Equal(t, 4, sum.Arms[eval.ArmQompack].Trials)
	require.Equal(t, 2, sum.Arms[eval.ArmQompack].Completed)
	require.Equal(t, 1, sum.Arms[eval.ArmQompack].PluginMismatch)
	require.Equal(t, 1, sum.Arms[eval.ArmQompack].HookProblemTrials)
	require.Len(t, sum.Failed, 3)
	require.Equal(t, "not-applicable", sum.Decision.Verdict)
	notes := strings.Join(sum.Notes, "\n")
	require.Contains(t, notes, "1 of 4 qompack trial(s) ran with a hook failure")
	require.Contains(t, notes, "not the pre-registered model")
}

// TestSummarizeLive_HarnessFailureFailsEveryOutcome is preregistration §8: a trial the harness could
// not run as designed is scored as a failure on every outcome — never a task success, never
// constraint-clean, never dropped from the recovery denominator — whatever state it left behind.
func TestSummarizeLive_HarnessFailureFailsEveryOutcome(t *testing.T) {
	task := eval.LiveTask{Checks: []eval.LiveCheck{
		{ID: "t", Outcome: eval.OutcomeTask, Kind: eval.CheckFileExists, Path: "a"},
		{ID: "r", Outcome: eval.OutcomeRecovery, Kind: eval.CheckFileExists, Path: "b"},
	}}
	// A trial stopped before any session ran: nothing was graded.
	early := eval.LiveTrial{
		TaskID: "t", Arm: eval.ArmStock, Trial: 1, PreregisteredModel: true,
		HarnessError: "preparing the project: git init: exit status 128",
	}
	early.ApplyHarnessFailure(task)
	require.NotNil(t, early.Recovered, "a task with recovery checks counts the failed trial in the recovery denominator")
	require.False(t, *early.Recovered)
	require.False(t, early.TaskSuccess)

	// A trial the host finished but the harness could not close as designed: its graded state passed.
	yes := true
	graded := eval.LiveTrial{
		TaskID: "t", Arm: eval.ArmStock, Trial: 2, PreregisteredModel: true, Completed: true,
		TaskSuccess: true, Recovered: &yes,
		HarnessError: "the host did not exit within 1m30s of its input closing",
	}
	graded.ApplyHarnessFailure(task)
	require.False(t, graded.TaskSuccess)
	require.False(t, graded.Completed)
	require.False(t, *graded.Recovered)
	require.True(t, yes, "the caller's value is not written through")

	// A task that declares no recovery check keeps a nil recovery verdict: it is outside that
	// denominator for every trial, failed or not.
	noRec := eval.LiveTrial{TaskID: "u", Arm: eval.ArmStock, Trial: 1, HarnessError: "x"}
	noRec.ApplyHarnessFailure(eval.LiveTask{Checks: task.Checks[:1]})
	require.Nil(t, noRec.Recovered)

	// A trial with no harness error is left exactly as graded.
	fine := eval.LiveTrial{TaskID: "t", Arm: eval.ArmStock, Trial: 4, Completed: true, TaskSuccess: true, Recovered: &yes}
	before := fine
	fine.ApplyHarnessFailure(task)
	require.Equal(t, before, fine)

	sum := eval.SummarizeLive("r", liveAnalysis(), []eval.LiveTrial{early, graded})
	st := sum.Arms[eval.ArmStock]
	require.Equal(t, [2]int{0, 2}, [2]int{st.TaskSuccess.K, st.TaskSuccess.N})
	require.Equal(t, [2]int{0, 2}, [2]int{st.ConstraintClean.K, st.ConstraintClean.N}, "a harness failure is not constraint-clean")
	require.Equal(t, [2]int{0, 2}, [2]int{st.Recovery.K, st.Recovery.N})
}

// TestSummarizeLive_HarnessFailureIsNeverASuccess: the summarizer holds preregistration §8's rule on
// its own, for a record no one normalized — a harness failure that happened to leave a passing
// state behind is still not a success, not constraint-clean and not recovered.
func TestSummarizeLive_HarnessFailureIsNeverASuccess(t *testing.T) {
	yes := true
	raw := eval.LiveTrial{
		TaskID: "t", Arm: eval.ArmStock, Trial: 3, PreregisteredModel: true, Completed: true,
		TaskSuccess: true, Recovered: &yes, HarnessError: "parsing the stream: line 9 is not JSON",
	}
	st := eval.SummarizeLive("r", liveAnalysis(), []eval.LiveTrial{raw}).Arms[eval.ArmStock]
	require.Equal(t, [2]int{0, 1}, [2]int{st.TaskSuccess.K, st.TaskSuccess.N})
	require.Equal(t, [2]int{0, 1}, [2]int{st.ConstraintClean.K, st.ConstraintClean.N})
	require.Equal(t, [2]int{0, 1}, [2]int{st.Recovery.K, st.Recovery.N})
}

// TestSummarizeLive_NewcombeMatchesThePublishedExample pins the difference interval to Newcombe
// (1998), "Interval estimation for the difference between independent proportions", Statistics in
// Medicine 17:873-890, Table II example (a): 56/70 − 48/80 = 0.2000 with method 10 (the hybrid
// score interval without continuity correction) giving 0.0524 to 0.3339.
func TestSummarizeLive_NewcombeMatchesThePublishedExample(t *testing.T) {
	sum := eval.SummarizeLive("r", liveAnalysis(), append(trials(eval.ArmQompack, 70, 56), trials(eval.ArmStock, 80, 48)...))
	d := sum.TaskSuccessDiff
	require.NotNil(t, d)
	require.InDelta(t, 0.2, d.Estimate, 1e-12)
	require.InDelta(t, 0.0524, d.Low, 5e-5)
	require.InDelta(t, 0.3339, d.High, 5e-5)
}

// TestSummarizeLive_ConstraintRegressionIsReported is preregistration §8's H2 rule: a
// constraint-clean difference whose upper bound lies below −margin is reported as a regression
// whatever the primary verdict is.
func TestSummarizeLive_ConstraintRegressionIsReported(t *testing.T) {
	s := trials(eval.ArmStock, 20, 18)
	q := trials(eval.ArmQompack, 20, 18)
	for i := range q[:15] {
		q[i].ConstraintViolations = 1
	}
	sum := eval.SummarizeLive("r", liveAnalysis(), append(s, q...))
	require.NotNil(t, sum.ConstraintCleanDiff)
	require.Less(t, sum.ConstraintCleanDiff.High, -0.2)
	require.NotEmpty(t, sum.ConstraintRegression)
	require.Contains(t, strings.Join(sum.Notes, "\n"), "regression")
	require.Equal(t, liveAnalysis(), sum.Analysis, "the summary carries the rule it was decided under")

	clean := eval.SummarizeLive("r", liveAnalysis(), append(trials(eval.ArmStock, 20, 18), trials(eval.ArmQompack, 20, 18)...))
	require.Empty(t, clean.ConstraintRegression)
}

// TestSummarizeLive_ReportsByVariantAndPerTaskSign is the rest of preregistration §8's analysis:
// results per variant (base, changing-requirement, held-out), the per-task sign of qompack − stock,
// and the clustering limitation stated beside the pooled interval.
func TestSummarizeLive_ReportsByVariantAndPerTaskSign(t *testing.T) {
	mk := func(task, arm, variant string, held, ok bool) eval.LiveTrial {
		return eval.LiveTrial{
			TaskID: task, Arm: arm, Trial: 1, Variant: variant, HeldOut: held, Completed: true, TaskSuccess: ok,
			PluginExpected: arm == eval.ArmQompack, PluginLoaded: arm == eval.ArmQompack, PreregisteredModel: true,
			HostReportedPlugins: true,
		}
	}
	ts := []eval.LiveTrial{
		mk("a", eval.ArmStock, "base", false, true), mk("a", eval.ArmQompack, "base", false, false),
		mk("b", eval.ArmStock, "changing-requirement", true, false), mk("b", eval.ArmQompack, "changing-requirement", true, true),
		mk("c", eval.ArmStock, "base", false, true), mk("c", eval.ArmQompack, "base", false, true),
	}
	sum := eval.SummarizeLive("r", liveAnalysis(), ts)
	require.Equal(t, map[string]int{"a": -1, "b": 1, "c": 0}, sum.TaskSigns)
	require.Len(t, sum.ByVariant["base"], 2)
	require.Len(t, sum.ByVariant["changing-requirement"], 2)
	require.Len(t, sum.ByVariant[eval.VariantHeldOut], 2)
	for _, as := range sum.ByVariant[eval.VariantHeldOut] {
		require.Equal(t, 1, as.Trials)
	}
	require.Contains(t, strings.Join(sum.Notes, "\n"), "clustered within tasks")
}

// TestSummarizeLive_InconsistentAccountsAreNamed: a trial whose usage account broke one of its own
// rules (a running total that reset, a main loop that could not be attributed) still counts in
// every outcome, but its per-category sums and estimate are not reliable, and the summary says how
// many such trials each arm has instead of averaging them in silently.
func TestSummarizeLive_InconsistentAccountsAreNamed(t *testing.T) {
	q := trials(eval.ArmQompack, 3, 3)
	q[0].Account = eval.SessionAccount{Consistent: true}
	q[1].Account = eval.SessionAccount{Consistent: false, Problems: []string{"turn 2: running total decreased"}}
	// q[2] and every stock trial carry the zero account of a trial that produced no stream: no
	// usage, nothing to be inconsistent about.
	s := trials(eval.ArmStock, 3, 3)
	sum := eval.SummarizeLive("r", liveAnalysis(), append(s, q...))
	require.Equal(t, 1, sum.Arms[eval.ArmQompack].AccountInconsistent)
	require.Zero(t, sum.Arms[eval.ArmStock].AccountInconsistent)
	require.Contains(t, strings.Join(sum.Notes, "\n"), "1 of 3 qompack trial(s) have an inconsistent usage account")
}

// TestSummarizeLive_ForeignPluginsAreNamed: both arms are meant to run with no plugin but the
// arm's own (preregistration section 3); a trial whose host loaded another is counted and named in
// the notes rather than passing unremarked.
func TestSummarizeLive_ForeignPluginsAreNamed(t *testing.T) {
	s := trials(eval.ArmStock, 2, 2)
	s[1].ForeignPlugins = []string{"superpowers@claude-plugins-official"}
	sum := eval.SummarizeLive("r", liveAnalysis(), append(s, trials(eval.ArmQompack, 2, 2)...))
	require.Equal(t, 1, sum.Arms[eval.ArmStock].ForeignPluginTrials)
	require.Contains(t, strings.Join(sum.Notes, "\n"),
		"1 of 2 stock trial(s) loaded a plugin other than the arm's own: superpowers@claude-plugins-official")
}

// TestSummarizeLive_NoPluginStateIsAHarnessFailureNotAMismatch: a trial's plugin state is the plugin
// list its host reported when it started. A qompack trial whose host never started — a marketplace
// install that failed, a process that died before its first line — reported none, so it has no
// plugin state to contradict its arm; it is a harness failure and, under intention to treat, a
// failure on every outcome. Treating it as a mismatch instead would void the whole comparison over
// one infrastructure failure on the qompack arm, while the same failure on the stock arm counts as
// a stock failure: an asymmetry that can only ever spare the plugin a failure.
func TestSummarizeLive_NoPluginStateIsAHarnessFailureNotAMismatch(t *testing.T) {
	const n = 20
	q := trials(eval.ArmQompack, n, n)
	q[0].HostReportedPlugins, q[0].PluginLoaded = false, false
	q[0].HarnessError, q[0].Completed, q[0].TaskSuccess = "marketplace install: exit 1", false, false
	sum := eval.SummarizeLive("r", liveAnalysis(), append(trials(eval.ArmStock, n, 19), q...))
	qa := sum.Arms[eval.ArmQompack]
	require.Zero(t, qa.PluginMismatch, "no plugin state was reported, so none contradicted the arm")
	require.Equal(t, n-1, qa.TaskSuccess.K, "the trial is a failure on the primary outcome")
	require.Len(t, sum.Failed, 1, "and it is named")
	require.NotEqual(t, "not-applicable", sum.Decision.Verdict)

	// A host that did report its plugins, without the arm's plugin among them, still voids it.
	q[0].HostReportedPlugins, q[0].HarnessError = true, ""
	sum = eval.SummarizeLive("r", liveAnalysis(), append(trials(eval.ArmStock, n, 19), q...))
	require.Equal(t, 1, sum.Arms[eval.ArmQompack].PluginMismatch)
	require.Equal(t, "not-applicable", sum.Decision.Verdict)
}

// TestSummarizeLive_HookLatencyIsTheHostsOwnPerHook is preregistration section 6: hook latency is
// reported per hook, from the durations the host itself measured and wrote to its transcript
// (durationMs), not the pipe-observed approximation pooled over every hook.
func TestSummarizeLive_HookLatencyIsTheHostsOwnPerHook(t *testing.T) {
	q := trials(eval.ArmQompack, 3, 3)
	q[0].TranscriptHookMS = map[string][]int64{"SessionStart:compact": {400}, "PreCompact": {90}}
	q[1].TranscriptHookMS = map[string][]int64{"SessionStart:compact": {200, 900}}
	q[2].HookLatencyMS = map[string][]int64{"SessionStart:compact": {5000}} // pipe-observed only
	sum := eval.SummarizeLive("r", liveAnalysis(), q)
	got := sum.Arms[eval.ArmQompack].HostHookMS
	require.Equal(t, eval.HookLatency{N: 3, P50: 400, P95: 900, Max: 900}, got["SessionStart:compact"])
	require.Equal(t, eval.HookLatency{N: 1, P50: 90, P95: 90, Max: 90}, got["PreCompact"])
	require.Len(t, got, 2, "only host-measured durations are summarized per hook")
}
