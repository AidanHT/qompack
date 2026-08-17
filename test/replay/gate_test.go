package main

import (
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// judgeOne is the single-metric form the 2% rule is easiest to state in.
func judgeOne(t *testing.T, metric string, base, observed float64, signOff string) (eval.Regression, bool) {
	t.Helper()
	return judge("stock", metric, base, observed, signOff)
}

// TestGate_NoRegressionPasses: an unchanged metric is not a regression.
func TestGate_NoRegressionPasses(t *testing.T) {
	_, regressed := judgeOne(t, "fraction_of_opt", 0.5, 0.5, "")
	require.False(t, regressed)
}

// TestGate_TwoPercentBoundaryExclusive pins both sides of §11.3's threshold. "More than 2%" means
// 2% itself passes, and a gate that fires at exactly the boundary would block work the rule allows.
func TestGate_TwoPercentBoundaryExclusive(t *testing.T) {
	_, just := judgeOne(t, "fraction_of_opt", 0.500, 0.4901, "")
	require.False(t, just, "-1.98% is inside the 2% allowance")

	r, over := judgeOne(t, "fraction_of_opt", 0.500, 0.4899, "")
	require.True(t, over, "-2.02% is not")
	require.InDelta(t, -2.02, r.DeltaPct, 0.01)
	require.False(t, r.Allowed)
}

// TestGate_LowerBetterMetricDirection: for a cost metric, going UP is the regression. Getting one
// direction backwards would make the gate celebrate a regression.
func TestGate_LowerBetterMetricDirection(t *testing.T) {
	require.Equal(t, eval.DirLowerBetter, eval.MetricDirection("rewrite_tokens"))

	_, regressed := judgeOne(t, "rewrite_tokens", 1000, 1030, "")
	require.True(t, regressed)
}

// TestGate_ImprovementNeverRegresses: halving a cost metric is not a regression at any magnitude.
func TestGate_ImprovementNeverRegresses(t *testing.T) {
	_, regressed := judgeOne(t, "rewrite_tokens", 1000, 500, "")
	require.False(t, regressed)

	_, regressed = judgeOne(t, "fraction_of_opt", 0.5, 0.9, "")
	require.False(t, regressed)
}

// TestGate_ZeroBaselineUsesAbsoluteTolerance: against a baseline of zero every change is an
// infinite percentage, so the absolute tolerance has to take over.
//
// A ratio metric cannot exercise this rule from 0.0: every ratio metric in MetricsOf is
// DirHigherBetter, so moving away from zero is an improvement and the rule is never reached. Only
// a count metric can, which is why this uses redundant_reads.
func TestGate_ZeroBaselineUsesAbsoluteTolerance(t *testing.T) {
	require.Equal(t, eval.DirLowerBetter, eval.MetricDirection("redundant_reads"))

	_, two := judgeOne(t, "redundant_reads", 0, 2, "")
	require.True(t, two, "|2-0| > 1.0")

	_, one := judgeOne(t, "redundant_reads", 0, 1, "")
	require.False(t, one, "|1-0| is not > 1.0")
}

// TestGate_SignOffAllowsNamedMetricOnly: a trailer authorizes the metric it names and nothing else,
// which is the difference between an explicit trade and a blanket override.
func TestGate_SignOffAllowsNamedMetricOnly(t *testing.T) {
	body := "Some PR description.\n\n" +
		"Sign-off: rewrite_tokens=+3.1% traded for a 9% first-divergence gain\n"

	allowed, _ := judgeOne(t, "rewrite_tokens", 1000, 1031, body)
	require.True(t, allowed.Allowed)

	other, regressed := judgeOne(t, "redundant_reads", 10, 40, body)
	require.True(t, regressed)
	require.False(t, other.Allowed, "the trailer names a different metric")
}

// TestGate_SignOffRejectsShortReason: "wip" is not a reason.
func TestGate_SignOffRejectsShortReason(t *testing.T) {
	require.False(t, signedOff("Sign-off: rewrite_tokens=+3.1% meh\n", "rewrite_tokens"))
	require.True(t, signedOff(
		"Sign-off: rewrite_tokens=+3.1% traded for a large first-divergence gain\n", "rewrite_tokens"))
}

// TestGate_SignOffIsCaseInsensitiveAndAnchored keeps the trailer recognizable without letting a
// mention of it inside prose count as one.
func TestGate_SignOffIsCaseInsensitiveAndAnchored(t *testing.T) {
	require.True(t, signedOff("SIGN-OFF: re_attempts=+5% this is a sufficiently long reason", "re_attempts"))
	require.False(t, signedOff(
		"we could add a Sign-off: re_attempts=+5% this is a sufficiently long reason", "re_attempts"),
		"a mid-line mention is prose, not a trailer")
}

// TestGate_ReportRegressionsBlocksOnlyUnsigned: an entirely signed-off set of regressions is a
// deliberate trade and must not block.
func TestGate_ReportRegressionsBlocksOnlyUnsigned(t *testing.T) {
	var signed strings.Builder
	require.False(t, reportRegressions(&signed, []eval.Regression{
		{Metric: "rewrite_tokens", Policy: "stock", Allowed: true},
	}))
	require.Contains(t, signed.String(), "sign-off trailer")

	var unsigned strings.Builder
	require.True(t, reportRegressions(&unsigned, []eval.Regression{
		{Metric: "rewrite_tokens", Policy: "stock", DeltaPct: 3.1, Allowed: false},
	}))
	require.Contains(t, unsigned.String(), "Sign-off: rewrite_tokens=+3.10%",
		"a gate that tells you how to satisfy it is one people use rather than route around")
}

// TestGate_NoneRegressionsPrintNothing keeps a green run quiet.
func TestGate_NoneRegressionsPrintNothing(t *testing.T) {
	var out strings.Builder
	require.False(t, reportRegressions(&out, nil))
	require.Empty(t, out.String())
}

// TestGate_BloomFPCeiling is §11.4's hard limit, and the message carries the sentence that
// explains why the number matters.
func TestGate_BloomFPCeiling(t *testing.T) {
	require.NoError(t, checkBloomCeiling(eval.SketchHealth{EstFPRate: 0.006}))
	require.NoError(t, checkBloomCeiling(eval.SketchHealth{EstFPRate: 0.10}), "the ceiling is inclusive")

	err := checkBloomCeiling(eval.SketchHealth{EstFPRate: 0.11})
	require.Error(t, err)
	require.Contains(t, err.Error(), "at 10% the agent starts skipping viable approaches")
}

// TestGate_CorpusStaleness turns §11.4's "re-collect sessions periodically" into a schedule the
// build enforces rather than an intention someone remembers.
func TestGate_CorpusStaleness(t *testing.T) {
	fresh := eval.CorpusManifest{RegeneratedAfterPhase: 0}
	require.NoError(t, checkCorpusFreshness(fresh, 0))
	require.NoError(t, checkCorpusFreshness(fresh, 2), "two phases of drift is tolerated")

	err := checkCorpusFreshness(fresh, 3)
	require.Error(t, err)
	require.Contains(t, err.Error(), "corpus stale")
	require.Contains(t, err.Error(), "0003-replay-overfit-recollection.md")
}

// TestGate_GrowthInconclusiveFails: an unmeasurable guardrail is not a passing guardrail.
func TestGate_GrowthInconclusiveFails(t *testing.T) {
	require.NoError(t, checkGrowth("", eval.GrowthResult{}), "no --growth flag means no check")

	err := checkGrowth("some/path.json", eval.GrowthResult{Samples: 3, Reason: "inconclusive: 3 usable samples"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "inconclusive")

	err = checkGrowth("some/path.json", eval.GrowthResult{Samples: 8, Exponent: 1.0, RawSpan: 128})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not sublinear")

	require.NoError(t, checkGrowth("some/path.json", eval.GrowthResult{Sublinear: true, Samples: 8}))
}

// TestGate_WatchForKeysDisjointFromMetricsOf is what lets the baseline's 19-key-per-policy
// assertion stay an exact set equality.
func TestGate_WatchForKeysDisjointFromMetricsOf(t *testing.T) {
	metrics := map[string]bool{}
	for _, name := range eval.MetricNames() {
		metrics[name] = true
	}
	for name := range watchForDirection {
		require.False(t, metrics[name], "%s is both a watch-for and a MetricsOf key", name)
	}
	require.Len(t, watchForDirection, 2)
}

// TestGate_DirectionOfCoversBothTables: the gate judges watch-fors with the same rule as score
// metrics, so it has to know their direction too.
func TestGate_DirectionOfCoversBothTables(t *testing.T) {
	require.Equal(t, eval.DirHigherBetter, directionOf("fraction_of_opt"))
	require.Equal(t, eval.DirLowerBetter, directionOf("bloom_fp_rate"))
	require.Equal(t, eval.DirLowerBetter, directionOf("bloom_fill_ratio"))
}

// TestGate_BaselineFormDetection distinguishes the two --baseline spellings §8 uses.
func TestGate_BaselineFormDetection(t *testing.T) {
	for _, p := range []string{
		"testdata/baseline/phase0.json", `testdata\baseline\phase0.json`, "phase0.json", ".", "..",
	} {
		require.True(t, looksLikePath(p), p)
	}
	for _, ref := range []string{"develop", "main", "HEAD", "v1.2.0"} {
		require.False(t, looksLikePath(ref), ref)
	}
}

// TestGate_UnresolvableBaselineRefNamesBothForms: the error has to teach, because "baseline not
// found" is the least useful thing this could say.
func TestGate_UnresolvableBaselineRefNamesBothForms(t *testing.T) {
	_, err := loadBaseline("no-such-ref-anywhere", t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "neither a readable path nor a resolvable git ref")
	require.Contains(t, err.Error(), "testdata/baseline/phase0.json")
	require.Contains(t, err.Error(), "develop")
}

// TestPhase0_SessionCountFloor is §10 Phase 0's "at least 20" made mechanical.
func TestPhase0_SessionCountFloor(t *testing.T) {
	c := phaseContextFixture()
	c.Report.Sessions = 19

	err := phase0(c)
	require.Error(t, err)
	require.Contains(t, err.Error(), "eval.minSessions")
}

// TestPhase0_RequiresStock: the Phase 0 number IS stock behaviour, so a report without it has not
// answered the question.
func TestPhase0_RequiresStock(t *testing.T) {
	c := phaseContextFixture()
	delete(c.Report.Policies, "stock")

	err := phase0(c)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stock")
}

// TestPhase0_Reproducibility: two full replays of the same corpus must agree exactly, or the
// "single reproducible number" the phase asks for is not a number, it is a sample.
func TestPhase0_Reproducibility(t *testing.T) {
	c := phaseContextFixture()
	require.NoError(t, phase0(c))

	c.CanonicalSecond = []byte(`{"stock":{"fraction_of_opt":0.700000}}`)
	err := phase0(c)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not reproducible")
}

// TestRunPhaseChecks_RunsEveryMergedPhase: once a phase lands, its criterion is re-asserted on
// every pull request forever.
func TestRunPhaseChecks_RunsEveryMergedPhase(t *testing.T) {
	require.Empty(t, runPhaseChecks(phaseContextFixture(), 0))

	c := phaseContextFixture()
	c.Report.Sessions = 1
	require.Len(t, runPhaseChecks(c, 0), 1)
	require.Len(t, runPhaseChecks(c, 6), 1, "no later phase has landed yet")
	require.Empty(t, runPhaseChecks(c, -1), "nothing is asserted below phase 0")
}

// TestFirstDiffLine points at the metric that moved rather than dumping two long strings.
func TestFirstDiffLine(t *testing.T) {
	require.Equal(t, "  (identical)", firstDiffLine([]byte("abc"), []byte("abc")))
	require.Contains(t, firstDiffLine([]byte("abc"), []byte("abd")), "first run")
	require.Contains(t, firstDiffLine([]byte("abc"), []byte("abcd")), "lengths differ")
}

// TestCanonicalMetrics_StableAcrossMapOrder: the canonical rendering walks a fixed metric order and
// sorted policy names, so it cannot vary with Go's map iteration.
func TestCanonicalMetrics_StableAcrossMapOrder(t *testing.T) {
	a := map[string]map[string]float64{
		"stock": {"fraction_of_opt": 0.5}, "null": {"fraction_of_opt": 0},
	}
	b := map[string]map[string]float64{
		"null": {"fraction_of_opt": 0}, "stock": {"fraction_of_opt": 0.5},
	}
	require.Equal(t, string(canonicalMetrics(a)), string(canonicalMetrics(b)))
	require.Contains(t, string(canonicalMetrics(a)), `"fraction_of_opt":0.500000`)
}
