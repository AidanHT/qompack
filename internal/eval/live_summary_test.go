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
			PluginExpected: arm == eval.ArmQompack, PluginLoaded: arm == eval.ArmQompack,
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
