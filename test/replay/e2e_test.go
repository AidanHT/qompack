package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// phaseContextFixture is a passing phase-0 Context: 24 sessions, a stock policy, and two identical
// canonical renderings.
func phaseContextFixture() Context {
	canonical := []byte(`{"stock":{"fraction_of_opt":0.695164}}`)
	return Context{
		Report: eval.Report{
			Sessions: 24,
			Policies: map[string]eval.Score{"stock": {FractionOfOPT: 0.695164}},
			Baseline: "stock",
		},
		Cfg:             config.Defaults(),
		CanonicalFirst:  canonical,
		CanonicalSecond: append([]byte(nil), canonical...),
		Corpus:          eval.CorpusManifest{RegeneratedAfterPhase: 0},
	}
}

// driverRun invokes the driver in-process and returns its exit code plus both streams.
func driverRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := run(args, &out, &errw)
	return code, out.String(), errw.String()
}

// repoPath resolves a repo-relative path from the test's working directory (test/replay).
func repoPath(t *testing.T, rel string) string {
	t.Helper()
	root, err := repoRoot()
	require.NoError(t, err)
	return filepath.Join(root, filepath.FromSlash(rel))
}

// TestReplayDriver_EndToEnd runs the driver against the committed corpus and baseline and asserts
// the number it reports is exactly the committed one.
func TestReplayDriver_EndToEnd(t *testing.T) {
	out := filepath.Join(t.TempDir(), "report.json")

	code, stdout, stderr := driverRun(t,
		"--corpus", "testdata/sessions/synthetic",
		"--baseline", "testdata/baseline/phase0.json",
		"--phase", "0",
		"--growth", "testdata/golden/eval/growth/stats-growth.json",
		"--sketch", "testdata/golden/eval/growth/health.json",
		"--out", out,
	)
	require.Equal(t, exitOK, code, "stderr:\n%s", stderr)
	require.Contains(t, stdout, "24 sessions")

	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	var report DriverReport
	require.NoError(t, json.Unmarshal(raw, &report))

	baseRaw, err := os.ReadFile(repoPath(t, "testdata/baseline/phase0.json"))
	require.NoError(t, err)
	var base baselineFile
	require.NoError(t, json.Unmarshal(baseRaw, &base))

	require.Equal(t, base.Policies["stock"]["fraction_of_opt"],
		report.Policies["stock"]["fraction_of_opt"],
		"the committed Phase 0 number must be exactly what a fresh replay produces")
	require.Empty(t, report.Regressions)
	require.Equal(t, tierSynthetic, report.CorpusTier)
	require.Equal(t, latencyModelled, report.Latency)
}

// TestReplayDriver_BaselineIsByteReproducible is the Phase 0 exit criterion's "reproducible" half,
// checked the only way that means anything: write it twice and compare the bytes.
func TestReplayDriver_BaselineIsByteReproducible(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.json")
	second := filepath.Join(dir, "b.json")

	code, _, errw := driverRun(t, "--write-baseline", "--baseline", first, "--out", "")
	require.Equal(t, exitOK, code, errw)
	code, _, errw = driverRun(t, "--write-baseline", "--baseline", second, "--out", "")
	require.Equal(t, exitOK, code, errw)

	a, err := os.ReadFile(first)
	require.NoError(t, err)
	b, err := os.ReadFile(second)
	require.NoError(t, err)
	require.Equal(t, string(a), string(b))
}

// TestReplayDriver_BaselineHasExactlyNineteenKeysPerPolicy: a new metric cannot land without a
// baseline for it, and a stale one cannot linger.
func TestReplayDriver_BaselineHasExactlyNineteenKeysPerPolicy(t *testing.T) {
	raw, err := os.ReadFile(repoPath(t, "testdata/baseline/phase0.json"))
	require.NoError(t, err)
	var base baselineFile
	require.NoError(t, json.Unmarshal(raw, &base))

	want := map[string]bool{}
	for _, name := range eval.MetricNames() {
		want[name] = true
	}
	require.Len(t, want, 19)

	require.NotEmpty(t, base.Policies)
	for policy, metrics := range base.Policies {
		require.Len(t, metrics, 19, "policy %s", policy)
		for name := range metrics {
			require.True(t, want[name], "%s: %s is not a MetricsOf key", policy, name)
		}
	}
	require.Len(t, base.WatchFor, 2, "the watch-fors live beside the policies, never inside one")
	require.Equal(t, tierSynthetic, base.CorpusTier,
		"the committed number is synthetic-corpus and has to say so")
}

// TestReplayDriver_BreakpointDisclaimerPrinted: §5.6 and §12 both say this analysis is not
// something the plugin does, and the output must never read as though it were.
func TestReplayDriver_BreakpointDisclaimerPrinted(t *testing.T) {
	code, stdout, errw := driverRun(t, "--baseline", "", "--out", "")
	require.Equal(t, exitOK, code, errw)

	require.Contains(t, stdout, eval.NotPluginActionable)
	disclaimer := strings.Index(stdout, eval.NotPluginActionable)
	number := strings.Index(stdout, "breakpoint OPT:")
	require.Less(t, disclaimer, number, "the disclaimer is printed above the number, always")
}

// TestReplayDriver_MaxCPUExceeded: a replay that costs more than its budget allows fails loudly.
//
// 1ns is below the resolution of every clock ProcessCPU reads, so which of the driver's two check
// points fires is not fixed: on a host whose CPU clock is credited on a 15.625 ms tick the first
// per-session check can still read zero, and the whole-run check catches it instead. Both carry
// errMaxCPU's sentence and both exit 3, so the assertions name those and not a line number.
//
// -max-wall is left at its default here on purpose. The two limits are independent, and a test for
// one of them that could be satisfied by the other would not be a test for either.
func TestReplayDriver_MaxCPUExceeded(t *testing.T) {
	code, _, errw := driverRun(t, "--max-cpu", "1ns", "--baseline", "", "--out", "")
	require.Equal(t, exitBudget, code, errw)
	require.Contains(t, errw, "CPU budget exceeded")
	require.NotContains(t, errw, "wall-clock ceiling exceeded",
		"the CPU budget is what was breached; naming the wall ceiling too would misdirect the reader")
}

// TestReplayDriver_MaxWallExceeded: a replay that takes longer than its ceiling allows fails loudly
// rather than hanging a CI job, however little CPU it spent getting there.
//
// This is the liveness half, and it is the half no CPU clock can see: everything that makes this
// process WAIT rather than work — a slow filesystem, a blocking read in the session loop, the git
// child loadBaseline shells out to for a --baseline ref — costs wall time and no CPU at all. The
// budget flag is left at its 2 minute default so nothing but the wall ceiling can produce this
// failure.
func TestReplayDriver_MaxWallExceeded(t *testing.T) {
	code, _, errw := driverRun(t, "--max-wall", "1ns", "--baseline", "", "--out", "")
	require.Equal(t, exitBudget, code, errw)
	require.Contains(t, errw, "wall-clock ceiling exceeded")
	require.Contains(t, errw, "blocked or starved rather than expensive")
}

// TestReplayDriver_BothLimitsHoldOnTheCommittedCorpus is the pass-side companion to the two above:
// a healthy run of the real corpus must clear BOTH limits at their real defaults, so neither
// failure test is passing merely because its limit is unreachably tight in normal use.
func TestReplayDriver_BothLimitsHoldOnTheCommittedCorpus(t *testing.T) {
	code, _, errw := driverRun(t,
		"--corpus", "testdata/sessions/synthetic",
		"--baseline", "",
		"--out", filepath.Join(t.TempDir(), "report.json"))
	require.Equal(t, exitOK, code, errw)
	require.NotContains(t, errw, "CPU budget exceeded")
	require.NotContains(t, errw, "wall-clock ceiling exceeded")
}

// TestReplayDriver_SessionFloorFails builds a 19-session corpus and asserts the phase-0 criterion
// catches it.
func TestReplayDriver_SessionFloorFails(t *testing.T) {
	small := t.TempDir()
	entries, err := os.ReadDir(repoPath(t, "testdata/sessions/synthetic"))
	require.NoError(t, err)

	copied := 0
	for _, e := range entries {
		if e.IsDir() || e.Name() == "CORPUS.json" || copied == 19 {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(repoPath(t, "testdata/sessions/synthetic"), e.Name()))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(small, e.Name()), raw, 0o600))
		copied++
	}
	require.Equal(t, 19, copied)

	code, _, errw := driverRun(t, "--corpus", small, "--baseline", "", "--out", "", "--phase", "0")
	require.Equal(t, exitGateFailed, code)
	require.Contains(t, errw, "eval.minSessions")
}

// TestReplayDriver_MissingBaselineWarnsButStillChecksPhase: a missing baseline disables comparison
// and says so; it does not disable the phase criterion, which does not need one.
func TestReplayDriver_MissingBaselineWarnsButStillChecksPhase(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")

	code, _, errw := driverRun(t, "--baseline", missing, "--out", "", "--phase", "0")
	require.Equal(t, exitOK, code, errw)
	require.Contains(t, errw, "no baseline")
}

// TestReplayDriver_EmptyCorpusIsBadInput distinguishes "the corpus is wrong" from "the gate found
// a regression", because they call for completely different responses.
func TestReplayDriver_EmptyCorpusIsBadInput(t *testing.T) {
	code, _, errw := driverRun(t, "--corpus", t.TempDir(), "--baseline", "", "--out", "")
	require.Equal(t, exitBadInput, code)
	require.Contains(t, errw, "no sessions")
}

// TestReplayDriver_UnknownPolicyIsBadInput names what is registered, so a typo is self-correcting.
func TestReplayDriver_UnknownPolicyIsBadInput(t *testing.T) {
	code, _, errw := driverRun(t, "--policies", "stock,nosuch", "--baseline", "", "--out", "")
	require.Equal(t, exitBadInput, code)
	require.Contains(t, errw, "no policy named")
	require.Contains(t, errw, "oracle")
}

// TestReplayDriver_PhaseChecksMayNotBeDisabledInCI: a developer may turn the phase checks off for
// a fast local loop. Nobody may turn them off for a pull request.
func TestReplayDriver_PhaseChecksMayNotBeDisabledInCI(t *testing.T) {
	t.Setenv("QOMPACK_EVAL__REPLAYONPHASEGATE", "false")

	code, _, errw := driverRun(t, "--baseline", "", "--out", "", "--phase", "0")
	require.Equal(t, exitOK, code, "locally it is a warning")
	require.Contains(t, errw, "eval.replayOnPhaseGate")

	out := filepath.Join(t.TempDir(), "report.json")
	code, _, errw = driverRun(t, "--baseline", "", "--out", out, "--phase", "0", "--ci")
	require.Equal(t, exitPhaseNoSkip, code)
	require.Contains(t, errw, "--ci forbids disabling the phase checks")

	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	var report DriverReport
	require.NoError(t, json.Unmarshal(raw, &report))
	require.True(t, report.PhaseChecksSkipped, "the report has to admit the checks did not run")
}

// TestReplayDriver_RegressionAgainstMutatedBaseline drives the whole 2% rule end to end, including
// the sign-off trailer, against a baseline deliberately moved out from under the run.
func TestReplayDriver_RegressionAgainstMutatedBaseline(t *testing.T) {
	raw, err := os.ReadFile(repoPath(t, "testdata/baseline/phase0.json"))
	require.NoError(t, err)
	var base baselineFile
	require.NoError(t, json.Unmarshal(raw, &base))

	// Claim stock used to be much better than it is, so this run reads as a regression.
	base.Policies["stock"]["fraction_of_opt"] *= 1.5
	mutated := filepath.Join(t.TempDir(), "phase0.json")
	body, err := encodeJSON(base)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(mutated, body, 0o600))

	code, _, errw := driverRun(t, "--baseline", mutated, "--out", "")
	require.Equal(t, exitGateFailed, code)
	require.Contains(t, errw, "fraction_of_opt")
	require.Contains(t, errw, "Sign-off: fraction_of_opt=")

	signOff := filepath.Join(t.TempDir(), "body.md")
	require.NoError(t, os.WriteFile(signOff,
		[]byte("Sign-off: fraction_of_opt=-33.33% deliberately re-baselined for this test\n"), 0o600))

	code, _, errw = driverRun(t, "--baseline", mutated, "--signoff", signOff, "--out", "")
	require.Equal(t, exitOK, code, errw)
}

// TestReplayDriver_LeftoverArgumentsAreBadInput closes the worst failure this driver can have,
// which is not failing when it should but passing when it did not run.
//
// `flag` stops at a bare `--` and hands everything after it back as positional arguments. A caller
// who writes `devtool replay -- --corpus X --ci` — the shape every tool that forwards arguments
// invites — therefore had every flag silently discarded, replayed the default corpus without --ci,
// and exited 0. The run looks identical to a real one in a CI log. A gate that reports success for
// a run it never performed is worse than no gate, so unconsumed arguments are refused by name.
func TestReplayDriver_LeftoverArgumentsAreBadInput(t *testing.T) {
	code, _, errw := driverRun(t,
		"--", "--corpus", "definitely-not-a-corpus", "--baseline", "", "--out", "", "--ci")
	require.Equal(t, exitBadInput, code,
		"a flag the driver never parsed must never be reported as a passing run")
	require.Contains(t, errw, "unexpected argument")
	require.Contains(t, errw, "--corpus",
		"the message names the first argument that was dropped, so the cause is obvious")
	require.Contains(t, errw, "--",
		"and says the separator is what dropped it")
}

// TestReplayDriver_RegenCorpusIsByteIdentical: regeneration is how the corpus is maintained, so it
// must not perturb a single byte of what is committed.
func TestReplayDriver_RegenCorpusIsByteIdentical(t *testing.T) {
	dir := t.TempDir()
	code, _, errw := driverRun(t, "--regen-corpus", "--corpus", dir)
	require.Equal(t, exitOK, code, errw)

	committed := repoPath(t, "testdata/sessions/synthetic")
	for _, n := range eval.CorpusSpecs() {
		want, err := os.ReadFile(filepath.Join(committed, n.File))
		require.NoError(t, err)
		got, err := os.ReadFile(filepath.Join(dir, n.File))
		require.NoError(t, err)
		require.Equal(t,
			strings.ReplaceAll(string(want), "\r\n", "\n"),
			strings.ReplaceAll(string(got), "\r\n", "\n"), n.File)
	}
}
