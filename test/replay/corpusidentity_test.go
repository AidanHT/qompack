package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// ---- Corpus identity, and the delta that has no denominator -----------------------------------
//
// The two things this file pins are one finding seen from two sides.
//
// V4 corrected the evaluation corpus (SP02-D1, SP02-D3): all four demand kinds are now raised at
// 39 of 39 events where only DemandFileContent was before, and the Belady keep budget now binds on
// 26 of 39 where it bound on none. That is a harder and more realistic workload, and the numbers
// measured over it are not a later reading of the numbers measured over the old one. Comparing
// them is a category error — the percentages describe the corpus change, not any policy.
//
// The gate was doing exactly that, and it was RIGHT to refuse: what was wrong was the comparison.
// checkCorpusIdentity makes the wrong comparison impossible to make silently, and the degenerate
// classification makes the half of it that has no denominator unreportable rather than enormous.
//
// ---- REGRESSION MARGINS AND SAMPLE SIZE, DECLARED BEFORE ANY OUTCOME WAS JUDGED ---------------
//
// Margin: the §11.3 threshold is unchanged at 2 % relative, with an absolute floor of 1e-06 below
// which no relative claim is made at all, and absolute tolerances of 0.02 (ratio metrics) and 1.0
// (count metrics) on the rows under that floor. Direction comes from eval.MetricDirection; a move
// in the improving direction is never a regression at any magnitude.
//
// Sample: 24 synthetic sessions, 8 shapes x 3 seeds, 39 compaction events, replayed
// deterministically. test/replay never sets ReplayOptions.Seed and deterministic replay makes no
// pseudo-random draw, so two consecutive runs are byte-identical — the driver checks exactly that
// on every invocation, replaying the corpus twice and diffing the canonical renderings. n = 1 is
// therefore EXACT for this instrument, and averaging runs would add nothing to it.
//
// What that sample supports: a reproducible threshold comparison of a policy against its own
// earlier number over THIS corpus.
//
// What it does NOT support, and no assertion in this package may be read as: any interval, p-value
// or percentage guarantee about real agent sessions. 24 generated sessions are not a random sample
// of any population. Twenty runs of a stochastic instrument could not establish a two-percent
// guarantee either; this instrument is not stochastic, which removes the sampling question and
// does not answer the external-validity one. The 2 % rule is a threshold on a reproducible number,
// never a confidence statement.

// liveCorpusDigest is the digest of the corpus actually committed under testdata/sessions/.
func liveCorpusDigest(t *testing.T) string {
	t.Helper()
	d := manifestDigest(repoPath(t, defaultCorpusPath))
	require.Len(t, d, 64, "the committed corpus must carry a CORPUS.json the gate can identify it by")
	return d
}

// committedBaseline reads one of the committed baseline artifacts.
func committedBaseline(t *testing.T, name string) baselineFile {
	t.Helper()
	var b baselineFile
	raw, err := os.ReadFile(repoPath(t, filepath.ToSlash(filepath.Join("testdata", "baseline", name))))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &b))
	return b
}

// runOver builds the identity half of a DriverReport for a run over the given corpus digest.
func runOver(digest string, sessions int) DriverReport {
	return DriverReport{
		Generator: generatorID, Corpus: defaultCorpusPath, CorpusTier: tierSynthetic,
		CorpusSHA256: digest, Sessions: sessions, Latency: latencyModelled,
	}
}

// repoRootOf is repoRoot, failing the test rather than the caller.
func repoRootOf(t *testing.T) string {
	t.Helper()
	root, err := repoRoot()
	require.NoError(t, err)
	return root
}

// TestCorpusIdentity_TheTwoCommittedBaselinesDescribeTwoDifferentCorpora is the whole finding in
// one assertion: phase0.json and phase0-recall.json are not two readings of one instrument.
//
// Both artifacts are kept. phase0.json is M0-04-protected and stays byte-for-byte as the record of
// what the pre-correction corpus said; phase0-recall.json is the measurement over the corpus in
// use, with every one of the old baseline's known failures carried into its provenance sidecar.
func TestCorpusIdentity_TheTwoCommittedBaselinesDescribeTwoDifferentCorpora(t *testing.T) {
	old := committedBaseline(t, "phase0.json")
	current := committedBaseline(t, "phase0-recall.json")

	require.NotEqual(t, old.CorpusSHA256, current.CorpusSHA256,
		"the V4 corpus correction changed the sessions, so the two baselines measured different workloads")
	require.Equal(t, liveCorpusDigest(t), current.CorpusSHA256,
		"phase0-recall.json is the baseline recorded over the committed corpus; if this fails, the "+
			"corpus moved and a NEW baseline is owed beside this one, never an edit to it")
	require.NotEqual(t, liveCorpusDigest(t), old.CorpusSHA256,
		"phase0.json is the preserved pre-correction baseline and must never match the live corpus")
}

// TestCorpusIdentity_AcceptsTheBaselineRecordedOverThisCorpus: the like-for-like pairing passes.
func TestCorpusIdentity_AcceptsTheBaselineRecordedOverThisCorpus(t *testing.T) {
	current := committedBaseline(t, "phase0-recall.json")
	require.NoError(t, checkCorpusIdentity("testdata/baseline/phase0-recall.json", current,
		runOver(liveCorpusDigest(t), current.Sessions), ""))
}

// TestCorpusIdentity_RefusesTheOldCorpusBaseline is the deliverable: the mismatch cannot be made
// silently, and the refusal says which digests disagree and which baseline to use instead.
func TestCorpusIdentity_RefusesTheOldCorpusBaseline(t *testing.T) {
	old := committedBaseline(t, "phase0.json")
	live := liveCorpusDigest(t)

	err := checkCorpusIdentity("testdata/baseline/phase0.json", old, runOver(live, 24), repoRootOf(t))
	require.Error(t, err, "a baseline from another corpus must never be compared against silently")

	msg := err.Error()
	require.Contains(t, msg, old.CorpusSHA256, "the message names the corpus the baseline was recorded over")
	require.Contains(t, msg, live, "and the corpus this run replayed")
	require.Contains(t, msg, "different sessions")
	require.Contains(t, msg, "testdata/baseline/phase0-recall.json",
		"the refusal ends in the baseline that DOES describe this corpus, not in a puzzle")
	require.Contains(t, msg, "M0-04",
		"and says a corpus change is re-baselined beside the old artifact rather than over it")
}

// TestCorpusIdentity_RefusesAnUnidentifiedPairing: "cannot be shown to match" is not permission to
// compare. A baseline with no corpusSHA256, or a run over a corpus with no CORPUS.json, is refused
// on the same terms as a mismatch — otherwise dropping the field would be a way around the check.
func TestCorpusIdentity_RefusesAnUnidentifiedPairing(t *testing.T) {
	current := committedBaseline(t, "phase0-recall.json")
	live := liveCorpusDigest(t)

	anonymous := current
	anonymous.CorpusSHA256 = ""
	require.ErrorContains(t,
		checkCorpusIdentity("anon.json", anonymous, runOver(live, current.Sessions), ""),
		"records no corpusSHA256")

	require.ErrorContains(t,
		checkCorpusIdentity("testdata/baseline/phase0-recall.json", current, runOver("", current.Sessions), ""),
		"recorded no corpusSHA256")

	require.ErrorContains(t,
		checkCorpusIdentity("anon.json", anonymous, runOver("", current.Sessions), ""),
		"neither the baseline nor this run records a corpusSHA256")
}

// TestCorpusIdentity_ReportsEveryDisagreementAtOnce: a reader deciding what to do next needs to
// know whether one field drifted or the whole workload was replaced.
func TestCorpusIdentity_ReportsEveryDisagreementAtOnce(t *testing.T) {
	old := committedBaseline(t, "phase0.json")
	old.CorpusTier = tierRecorded
	old.Sessions = 11
	old.Generator = "test/replay/0"

	err := checkCorpusIdentity("testdata/baseline/phase0.json", old, runOver(liveCorpusDigest(t), 24), "")
	require.Error(t, err)
	msg := err.Error()
	for _, want := range []string{"corpus sha256:", "corpus tier:", "corpus sessions:", "driver generator:"} {
		require.Contains(t, msg, want, "every disagreement is reported, not only the first")
	}
	require.Contains(t, msg, "SAME population",
		"the tier clause keeps the sentence that says why a cross-tier comparison is meaningless")
}

// ---- The degenerate half ----------------------------------------------------------------------

// TestGate_ADeltaWithNoDenominatorIsUnreportable pins the numbers that made this necessary.
//
// stock.re_attempts was 0 on the old corpus and is 24 on the corrected one, because the old corpus
// raised no demand that could produce a re-attempt at all (SP02-D1). Divided by a 1e-9 epsilon that
// printed as +2 400 000 000 000.00 %, and there is no honest rationale to write for it: the figure
// is the size of the denominator. The move is real — 24 re-attempts where there were none — and it
// is reportable as 24, never as a percentage.
func TestGate_ADeltaWithNoDenominatorIsUnreportable(t *testing.T) {
	r, regressed := judge("stock", "re_attempts", 0, 24, "")
	require.True(t, regressed, "24 re-attempts against a baseline of 0 is past the absolute tolerance")
	require.False(t, r.DeltaPctDefined, "a percentage of a zero baseline is an artefact, not a measurement")
	require.Zero(t, r.DeltaPct, "an undefined percentage is not carried as a huge number")
	require.InDelta(t, 24.0, r.AbsDelta, 1e-9)
	require.Equal(t, degenerateDelta, deltaPctCell(r))

	// The oracle's residual span is the other shape of the same artefact: 0 tokens on a corpus
	// where the keep budget never bound, 365 089 on one where it binds at 26 of 39 events.
	o, regressed := judge("oracle", "residual_span_p95", 0, 365089, "")
	require.True(t, regressed)
	require.False(t, o.DeltaPctDefined)
	require.InDelta(t, 365089.0, o.AbsDelta, 1e-9)
}

// TestGate_ARealMoveKeepsItsPercentage: the classification must not swallow genuine movement.
// stock.fraction_of_opt 0.695164 -> 0.258291 is a real -62.84 %, and it has a real denominator.
func TestGate_ARealMoveKeepsItsPercentage(t *testing.T) {
	r, regressed := judge("stock", "fraction_of_opt", 0.695164, 0.258291, "")
	require.True(t, regressed)
	require.True(t, r.DeltaPctDefined)
	require.InDelta(t, -62.84, r.DeltaPct, 0.01)
	require.Equal(t, "-62.84%", deltaPctCell(r))
}

// TestGate_DegenerateRowsPrintAsUnreportableWithTheirAbsoluteValues: the table a human reads must
// not contain the artefact either, and the trailer it suggests must be one a human can honestly
// write — which means absolute units, since the percentage does not exist.
func TestGate_DegenerateRowsPrintAsUnreportableWithTheirAbsoluteValues(t *testing.T) {
	var out strings.Builder
	blocking := reportRegressions(&out, []eval.Regression{
		{Metric: "re_attempts", Policy: "stock", Baseline: 0, Observed: 24, AbsDelta: 24},
		{
			Metric: "fraction_of_opt", Policy: "stock", Baseline: 0.695164, Observed: 0.258291,
			DeltaPct: -62.84, DeltaPctDefined: true, AbsDelta: -0.436873,
		},
	})
	require.True(t, blocking)

	printed := out.String()
	require.Contains(t, printed, degenerateDelta)
	require.NotContains(t, printed, "2400000000000",
		"the artefact must never reach the log, in any column")
	require.Contains(t, printed, "abs delta", "the honest column is present")
	require.Contains(t, printed, "measures the denominator, not the move")
	require.Contains(t, printed, "Sign-off: re_attempts=+24.000000",
		"a degenerate row is signed off in absolute units or not at all")
	require.Contains(t, printed, "Sign-off: fraction_of_opt=-62.84%",
		"a row with a real denominator keeps its percentage")
}

// TestGate_AnAbsoluteSignOffIsAccepted closes the loop: the trailer the gate prints for a
// degenerate row is a trailer the gate's own scanner accepts.
func TestGate_AnAbsoluteSignOffIsAccepted(t *testing.T) {
	body := "Sign-off: re_attempts=+24.000000 the corrected corpus raises re-attempt demands for the first time\n"
	require.True(t, signedOff(body, "re_attempts"))
	require.False(t, signedOff("Sign-off: re_attempts=+24.000000 wip", "re_attempts"),
		"an absolute trailer still has to carry a reason worth reading")
}
