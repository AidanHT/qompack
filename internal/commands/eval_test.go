package commands_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
)

// goodScore is a run where the task came out right and nothing was re-attempted.
func goodScore() eval.Score {
	return eval.Score{
		FractionOfOPT:    0.91,
		RetrievalHitRate: 0.88,
		Divergence: eval.Divergence{
			SameDecision:         true,
			DecisionPreservation: 0.97,
			FirstDivergenceTurn:  12,
			FileSetJaccard:       0.93,
			ToolEditDistance:     4,
			ReAttempts:           0,
			RedundantReads:       -2,
		},
	}
}

func evalDeps(in commands.EvalInput, err error) commands.Deps {
	d := testDeps()
	d.EvalArtifacts = func(context.Context, string) (commands.EvalInput, error) { return in, err }
	return d
}

func inputWith(s eval.Score, trials commands.TrialCounts, ledger *eval.RequestLedger) commands.EvalInput {
	return commands.EvalInput{
		Report: eval.Report{
			Policies: map[string]eval.Score{"qompack-l3": s, "baseline": {}},
			Baseline: "baseline",
			Sessions: 6,
		},
		Trials: trials,
		Ledger: ledger,
	}
}

func decodeEval(t *testing.T, out string) commands.EvalReport {
	t.Helper()
	env, err := commands.DecodeEnvelope([]byte(out))
	require.NoError(t, err)
	var rep commands.EvalReport
	require.NoError(t, json.Unmarshal(env.Data, &rep))
	return rep
}

// ranTrials is a fully-executed set.
func ranTrials() commands.TrialCounts {
	return commands.TrialCounts{Planned: 6, Ran: 6}
}

// TestEval_TaskAndRecoveryLeadNotFractionOfOPT is the presentation change this commit exists for.
//
// §11.1 made fraction-of-OPT the primary metric. It is retained, because every report already
// recorded uses it and dropping it would break comparison — but it is labelled a diagnostic and it
// no longer decides anything.
func TestEval_TaskAndRecoveryLeadNotFractionOfOPT(t *testing.T) {
	t.Parallel()

	out, err := runWith(t, evalDeps(inputWith(goodScore(), ranTrials(), nil), nil), "eval", "--json")
	require.NoError(t, err)

	rep := decodeEval(t, out)
	require.NotEmpty(t, rep.Task)
	require.NotEmpty(t, rep.Recovery)

	var fraction commands.EvalGate
	for _, g := range rep.Historical {
		if g.ID == "HIST-01" {
			fraction = g
		}
	}
	require.Equal(t, 0.91, *fraction.Value, "the historical value is retained exactly")
	require.Nil(t, fraction.Passed, "a retained diagnostic decides nothing")
	require.Contains(t, fraction.Detail, "historical")

	for _, g := range rep.Task {
		require.NotEqual(t, "fractionOfOpt", g.Metric, "fraction-of-OPT is not a task outcome")
	}
}

// TestEval_TextOutputLeadsWithOutcomes puts the same ordering where a person reads it.
func TestEval_TextOutputLeadsWithOutcomes(t *testing.T) {
	t.Parallel()

	out, err := runWith(t, evalDeps(inputWith(goodScore(), ranTrials(), nil), nil), "eval")
	require.NoError(t, err)

	task := indexOf(out, "task outcomes")
	recovery := indexOf(out, "evidence recovery")
	cost := indexOf(out, "cost —")
	historical := indexOf(out, "historical diagnostics")

	require.Less(t, task, recovery)
	require.Less(t, recovery, cost)
	require.Less(t, cost, historical, "the retained diagnostics come last")
	require.Contains(t, out, "it does not decide the verdict")
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestEval_CheapWrongResultCannotPass is the SP14-M7-03 gate.
//
// A run that cost almost nothing and reached a different decision must fail. Cost is not an input
// to the verdict at any weight, so no amount of cheapness can offset a task outcome.
func TestEval_CheapWrongResultCannotPass(t *testing.T) {
	t.Parallel()

	wrong := goodScore()
	wrong.Divergence.SameDecision = false
	wrong.FractionOfOPT = 0.99 // and it looked excellent by the old primary metric

	cheap := &eval.RequestLedger{
		Version:  eval.RequestLedgerVersion,
		Schedule: &eval.RateSchedule{},
		Records: []eval.RequestRecord{{
			ID: "r1", Kind: eval.KindTurn, Provider: "none", PricingMode: eval.PricingNone,
		}},
	}

	out, err := runWith(t, evalDeps(inputWith(wrong, ranTrials(), cheap), nil), "eval", "--json")
	require.Error(t, err, "a wrong answer is a failure however cheap it was")
	require.Equal(t, commands.ExitError, commands.ExitCode(err))

	rep := decodeEval(t, out)
	require.Equal(t, commands.VerdictFail, rep.Verdict)

	var same commands.EvalGate
	for _, g := range rep.Task {
		if g.ID == "TASK-01" {
			same = g
		}
	}
	require.NotNil(t, same.Passed)
	require.False(t, *same.Passed)
}

// TestEval_ReAttemptOfAnEliminatedApproachFails: the elimination was recorded and not respected,
// which is a constraint violation the artifact decides on its own.
func TestEval_ReAttemptOfAnEliminatedApproachFails(t *testing.T) {
	t.Parallel()

	s := goodScore()
	s.Divergence.ReAttempts = 1

	_, err := runWith(t, evalDeps(inputWith(s, ranTrials(), nil), nil), "eval")
	require.Error(t, err)
}

// TestEval_SkippedOrFailedTrialsAreInconclusiveNotPass keeps a gate from passing on an evaluation
// that did not run.
func TestEval_SkippedOrFailedTrialsAreInconclusiveNotPass(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		trials commands.TrialCounts
	}{
		{"skipped", commands.TrialCounts{Planned: 6, Ran: 4, Skipped: 2}},
		{"failed", commands.TrialCounts{Planned: 6, Ran: 5, Failed: 1}},
		{"none ran", commands.TrialCounts{Planned: 6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runWith(t, evalDeps(inputWith(goodScore(), tc.trials, nil), nil), "eval", "--json")
			require.NoError(t, err, "inconclusive is not a failure")

			rep := decodeEval(t, out)
			require.Equal(t, commands.VerdictInconclusive, rep.Verdict)
			require.Equal(t, tc.trials, rep.Trials, "the counts are reported, not summarized away")
		})
	}
}

// TestEval_MissingUsageIsUnknownNotZero is the SP14-M7-02 rule applied to cost.
func TestEval_MissingUsageIsUnknownNotZero(t *testing.T) {
	t.Parallel()

	out, err := runWith(t, evalDeps(inputWith(goodScore(), ranTrials(), nil), nil), "eval", "--json")
	require.NoError(t, err)

	rep := decodeEval(t, out)
	require.False(t, rep.Cost.Available)
	require.Nil(t, rep.Cost.Estimate, "an unpriced run has no amount, not an amount of zero")
	require.NotEmpty(t, rep.Cost.Reason)

	text, err := runWith(t, evalDeps(inputWith(goodScore(), ranTrials(), nil), nil), "eval")
	require.NoError(t, err)
	require.Contains(t, text, "unavailable")
	require.NotContains(t, text, "amount: 0 micros")
}

// TestEval_CostCategoriesRetriesAndPricingSourcesSurvive is the rest of SP14-M7-03: the ledger's
// own distinctions must reach the display rather than being summed into one number.
func TestEval_CostCategoriesRetriesAndPricingSourcesSurvive(t *testing.T) {
	t.Parallel()

	ledger := &eval.RequestLedger{
		Version:  eval.RequestLedgerVersion,
		Schedule: &eval.RateSchedule{},
		Records: []eval.RequestRecord{
			{
				ID: "r1", Kind: eval.KindTurn, Provider: "anthropic", PricingMode: eval.PricingEstimate,
				Reported: map[eval.UsageCategory]eval.TokenCount{
					eval.CategoryInput:  eval.KnownTokens(1000),
					eval.CategoryOutput: eval.KnownTokens(200),
				},
			},
			{
				ID: "r2", ParentID: "r1", Kind: eval.KindRetry, Provider: "anthropic",
				PricingMode: eval.PricingEstimate,
				Reported: map[eval.UsageCategory]eval.TokenCount{
					eval.CategoryInput: eval.KnownTokens(1000),
				},
			},
			{
				ID: "r3", Kind: eval.KindCompaction, Provider: "anthropic", PricingMode: eval.PricingInvoice,
			},
			{
				ID: "r4", Kind: eval.KindAbort, Provider: "anthropic", PricingMode: eval.PricingSubscription,
			},
		},
	}

	out, err := runWith(t, evalDeps(inputWith(goodScore(), ranTrials(), ledger), nil), "eval", "--json")
	require.NoError(t, err)

	rep := decodeEval(t, out)
	require.True(t, rep.Cost.Available)

	require.Equal(t, 1, rep.Cost.Kinds[eval.KindRetry], "a retry stays attributed as a retry")
	require.Equal(t, 1, rep.Cost.Kinds[eval.KindAbort], "an abort was billed and stays visible")
	require.Equal(t, 1, rep.Cost.Kinds[eval.KindCompaction])

	require.Equal(t, 1, rep.Cost.PricingModes[eval.PricingInvoice])
	require.Equal(t, 2, rep.Cost.PricingModes[eval.PricingEstimate])
	require.Equal(t, 1, rep.Cost.PricingModes[eval.PricingSubscription],
		"an invoice and an estimate are different observations and are not merged")

	input := rep.Cost.Categories[eval.CategoryInput]
	require.Equal(t, core.Tokens(2000), input.Known)
	require.Equal(t, 2, input.KnownRecords)
	require.Equal(t, 2, input.UnknownRecords, "the records that reported nothing are counted")
	require.False(t, input.Complete())

	textOut, err := runWith(t, evalDeps(inputWith(goodScore(), ranTrials(), ledger), nil), "eval")
	require.NoError(t, err)
	require.Contains(t, textOut, "retry=1")
	require.Contains(t, textOut, "abort=1")
	require.Contains(t, textOut, "invoice=1")
	require.Contains(t, textOut, "INCOMPLETE")
}

// TestEval_UnjudgedMetricsAreNotCountedAsPassing keeps SP-14 out of policy. A metric with no
// declared threshold is shown and left undecided; treating it as satisfied would put a gate's
// bound in the renderer, where no plan reviewed it.
func TestEval_UnjudgedMetricsAreNotCountedAsPassing(t *testing.T) {
	t.Parallel()

	out, err := runWith(t, evalDeps(inputWith(goodScore(), ranTrials(), nil), nil), "eval", "--json")
	require.NoError(t, err)

	rep := decodeEval(t, out)
	var unjudged int
	for _, g := range append(append([]commands.EvalGate{}, rep.Task...), rep.Recovery...) {
		if g.Passed == nil {
			unjudged++
			require.Contains(t, g.Detail, "no threshold")
		}
	}
	require.Positive(t, unjudged, "some metrics genuinely have no declared bound")
}

// TestEval_NoScoresIsInconclusive: an empty report has demonstrated nothing.
func TestEval_NoScoresIsInconclusive(t *testing.T) {
	t.Parallel()

	out, err := runWith(t, evalDeps(commands.EvalInput{Trials: ranTrials()}, nil), "eval", "--json")
	require.NoError(t, err)

	rep := decodeEval(t, out)
	require.Equal(t, commands.VerdictInconclusive, rep.Verdict)
	require.Contains(t, rep.Notes, "no policy scores were reported, so nothing was evaluated")
}

// TestEval_WithoutArtifactsIsUnavailable keeps a build with no evaluation from reporting a pass.
func TestEval_WithoutArtifactsIsUnavailable(t *testing.T) {
	t.Parallel()

	_, err := runWith(t, testDeps(), "eval")
	require.ErrorIs(t, err, commands.ErrUnavailable)
	require.Equal(t, commands.ErrorKindUnavailable, commands.KindOf(err))
}

// TestEval_ArtifactErrorIsReported keeps a corpus that could not be read from reading as a pass.
func TestEval_ArtifactErrorIsReported(t *testing.T) {
	t.Parallel()

	_, err := runWith(t, evalDeps(commands.EvalInput{}, errors.New("corpus: no such directory")), "eval")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no such directory")
	require.Equal(t, commands.ExitError, commands.ExitCode(err))
}

// TestEval_CorpusFlagReachesTheProducer covers the one documented flag.
func TestEval_CorpusFlagReachesTheProducer(t *testing.T) {
	t.Parallel()

	var got string
	d := testDeps()
	d.EvalArtifacts = func(_ context.Context, corpus string) (commands.EvalInput, error) {
		got = corpus
		return inputWith(goodScore(), ranTrials(), nil), nil
	}

	_, err := runWith(t, d, "eval", "--corpus", "testdata/corpus")
	require.NoError(t, err)
	require.Equal(t, "testdata/corpus", got)
}

// TestEval_RejectsPositionalArguments keeps a mistyped corpus path from being silently ignored.
func TestEval_RejectsPositionalArguments(t *testing.T) {
	t.Parallel()

	_, err := runWith(t, evalDeps(inputWith(goodScore(), ranTrials(), nil), nil), "eval", "testdata/corpus")
	require.ErrorIs(t, err, commands.ErrUsage)
	require.Contains(t, err.Error(), "--corpus")
}

// driverShapedReport is a replay report shaped as test/replay writes it by default: the Qompack
// policy scored beside the three reference policies — stock (the baseline), null (keeps nothing)
// and oracle (the Belady ceiling). The values are the committed synthetic corpus's, rounded.
func driverShapedReport() eval.Report {
	null := goodScore()
	null.FractionOfOPT = 0
	null.Divergence.DecisionPreservation = 0
	null.Divergence.ReAttempts = 24
	stock := null
	stock.FractionOfOPT = 0.26
	oracle := goodScore()
	oracle.FractionOfOPT = 1
	product := goodScore()
	product.FractionOfOPT = 0.85
	return eval.Report{
		Policies: map[string]eval.Score{
			"null": null, "oracle": oracle, "qompack-rehydrate": product, "stock": stock,
		},
		Baseline: "stock",
		Sessions: 24,
	}
}

// TestEval_ReplayReportsQompacksPolicyNotAReference: the replay driver scores Qompack's policy
// beside reference policies that exist only to bound the metric — null keeps nothing, oracle is
// the ceiling, stock is the baseline. The command reports Qompack's own policy; judging a reference
// policy (null re-attempts every eliminated approach by construction) would report a failure of a
// policy no user runs, and would pass or fail the evaluation on it.
func TestEval_ReplayReportsQompacksPolicyNotAReference(t *testing.T) {
	t.Parallel()

	in := commands.EvalInput{Report: driverShapedReport(), Trials: commands.TrialCounts{Planned: 24, Ran: 24}}
	out, err := runWith(t, evalDeps(in, nil), "eval", "--json")
	require.NoError(t, err, "Qompack's policy re-attempted nothing")

	rep := decodeEval(t, out)
	require.Equal(t, "qompack-rehydrate", rep.Policy, "the reported policy is Qompack's")
	require.Equal(t, "stock", rep.Baseline)
	require.Equal(t, commands.VerdictPass, rep.Verdict)
	for _, g := range rep.Historical {
		if g.ID == "HIST-01" {
			require.InDelta(t, 0.85, *g.Value, 1e-9, "the historical value is Qompack's policy's")
		}
	}
}

// TestEval_ReplayWithOnlyReferencePoliciesJudgesNothing: a replay that scored no Qompack policy
// has nothing of Qompack's to report, so no reference policy is promoted in its place.
func TestEval_ReplayWithOnlyReferencePoliciesJudgesNothing(t *testing.T) {
	t.Parallel()

	r := driverShapedReport()
	delete(r.Policies, "qompack-rehydrate")
	in := commands.EvalInput{Report: r, Trials: commands.TrialCounts{Planned: 24, Ran: 24}}
	out, err := runWith(t, evalDeps(in, nil), "eval", "--json")
	require.NoError(t, err, "a replay of reference policies alone is inconclusive, not a failure")

	rep := decodeEval(t, out)
	require.Empty(t, rep.Policy)
	require.Equal(t, commands.VerdictInconclusive, rep.Verdict)
	require.Empty(t, rep.Task, "no reference policy is judged in Qompack's place")
	require.Contains(t, strings.Join(rep.Notes, "\n"), "null, oracle, stock")
}
