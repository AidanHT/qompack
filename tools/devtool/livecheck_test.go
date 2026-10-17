package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

const liveTestCheckSuite = "../../testdata/eval/live/checks-v7-test.json"

// liveLongFloat matches a float written with more than nine decimals, keeping the first nine.
var liveLongFloat = regexp.MustCompile(`(\d+\.\d{9})\d+`)

// recordedHost answers every session with a recorded Claude Code 2.1.280 stream: the plugin session
// (smoke2) when the session loads a plugin by --plugin-dir, the stock one (smoke1) otherwise. edit,
// when set, rewrites the stock stream first.
func recordedHost(t *testing.T, specs *[]liveProcSpec, edit func(string) string) func(context.Context, liveProcSpec) liveProcResult {
	t.Helper()
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "eval", "testdata", "live", name))
		require.NoError(t, err)
		return string(raw)
	}
	stock, plugin := read("smoke1-stock.stream.jsonl"), read("smoke2-plugin.stream.jsonl")
	if edit != nil {
		stock = edit(stock)
	}
	return func(_ context.Context, spec liveProcSpec) liveProcResult {
		*specs = append(*specs, spec)
		s := stock
		if slices.Contains(spec.Args, "--plugin-dir") {
			s = plugin
		}
		s, stopped := honourStopOn(spec, s)
		res := liveProcResult{
			Stream: []byte(s), StepsCompleted: len(spec.Messages), StartedAt: time.Unix(0, 0), EndedAt: time.Unix(2, 0),
		}
		if stopped != "" {
			res.Stopped, res.Err = stopped, "stopped by the driver: "+stopped
		}
		return res
	}
}

func checkRun(t *testing.T, tier string) liveCheckOptions {
	t.Helper()
	return liveCheckOptions{
		suite: liveTestCheckSuite, tier: tier, out: t.TempDir(), bundle: fakeBundle(t), idleExit: 1800,
		precompute: liveCheckPrecomputeUnread, fiveHourGate: eval.LiveFiveHourGate, sevenDayCeiling: 0.9,
	}
}

func readCheckRun(t *testing.T, out string) (eval.LiveCheckManifest, eval.LiveCheckResults) {
	t.Helper()
	var m eval.LiveCheckManifest
	var r eval.LiveCheckResults
	readJSON(t, filepath.Join(out, "manifest.json"), &m)
	readJSON(t, filepath.Join(out, "results.json"), &r)
	return m, r
}

// TestLiveCheck_QuickTierOnRecordedStreams: the quick tier runs each check once through the
// live-eval session driver, under the guard and with the auto-updater off, grades it from the
// recorded stream, and writes the manifest and the results in their versioned schemas. The results
// file is pinned by a golden (the run id is the only value replaced).
func TestLiveCheck_QuickTierOnRecordedStreams(t *testing.T) {
	env, _, _ := pacedEnv(t)
	var specs []liveProcSpec
	env.run = recordedHost(t, &specs, nil)
	o := checkRun(t, eval.LiveCheckTierQuick)
	var log bytes.Buffer
	require.NoError(t, runLiveCheck(context.Background(), o, env, &log), log.String())

	require.Len(t, specs, 2, "quick runs each check once")
	for _, s := range specs {
		require.Contains(t, s.Env, "DISABLE_AUTOUPDATER=1")
		require.Contains(t, s.Env, "ENABLE_CLAUDEAI_MCP_SERVERS=false")
		require.Equal(t, "claude-opus-5-5", argAfter(s.Args, "--model"))
	}
	require.Contains(t, specs[1].Env, "QOMPACK_RUNTIME__MOD__ENABLED=false", "the check's own environment")
	require.Equal(t, o.bundle, argAfter(specs[1].Args, "--plugin-dir"))
	require.Equal(t, []string{
		"Remember this: the release codeword is HELIOTROPE-47. Reply with the codeword.", "/compact",
		"What is the release codeword? Answer in one line.",
	}, specs[0].Messages)

	m, r := readCheckRun(t, o.out)
	require.Equal(t, eval.LiveCheckManifestSchema, m.Schema)
	require.Equal(t, "2.1.280", m.ClaudeCodeVersion)
	require.Equal(t, "claude-fake", m.ClaudeCLI)
	require.Equal(t, []string{"claude-opus-5-5"}, m.Models)
	require.Equal(t, []string{"claude-haiku-4-5-20251001"}, m.HostModels)
	require.Equal(t, runtime.GOOS+"/"+runtime.GOARCH, m.Machine)
	require.Equal(t, liveCheckPrecomputeUnread, m.PrecomputeFlag)
	require.Len(t, m.Bundles["bundle"], 64)
	require.Len(t, m.SuiteSHA256, 64)
	require.Equal(t, r.RunID, m.RunID)

	require.Equal(t, eval.LiveCheckResultsSchema, r.Schema)
	require.Len(t, r.Checks, 2)
	for _, c := range r.Checks {
		require.Equal(t, eval.LiveCheckVerdictPass, c.Verdict, "%s: %+v", c.ID, c.Runs)
	}
	for _, f := range []string{"stream.jsonl", "stderr.txt", "run.json", "invocation.json"} {
		require.FileExists(t, filepath.Join(o.out, "checks", "compact-recall", "01", f))
	}

	raw, err := os.ReadFile(filepath.Join(o.out, "results.json"))
	require.NoError(t, err)
	// Interval bounds are cut to nine decimals: an architecture that fuses multiply-adds may differ in
	// the last bits of a float, and the schema, not the sixteenth digit, is what the golden pins.
	got := liveLongFloat.ReplaceAllString(strings.ReplaceAll(string(raw), r.RunID, "<run id>"), "$1")
	requireGolden(t, "live-check-results.golden.json", got)
}

// TestLiveCheck_MetricsTierRepeatsModelDependentChecks: the metrics tier repeats only the checks
// whose outcome depends on the model and reports their pass rate with an interval.
func TestLiveCheck_MetricsTierRepeatsModelDependentChecks(t *testing.T) {
	env, _, _ := pacedEnv(t)
	var specs []liveProcSpec
	env.run = recordedHost(t, &specs, nil)
	o := checkRun(t, eval.LiveCheckTierMetrics)
	var log bytes.Buffer
	require.NoError(t, runLiveCheck(context.Background(), o, env, &log), log.String())
	require.Len(t, specs, 4, "compact-recall three times, plugin-loads once")
	_, r := readCheckRun(t, o.out)
	recall := r.Checks[0]
	require.Equal(t, eval.LiveCheckVerdictRate, recall.Verdict)
	require.Equal(t, 3, recall.Passed)
	require.Equal(t, 3, recall.Graded)
	require.Less(t, recall.PassRate.Low, 1.0)
	require.InDelta(t, 1, *recall.MetricMean, 0, "one rate_limit_event per recorded session")
	require.Equal(t, eval.LiveCheckVerdictPass, r.Checks[1].Verdict)
}

// TestLiveCheck_PacesOnTheRecordedEvents: the recorded sessions report the seven-day window at 0.68,
// above the default 0.60 ceiling, so after the first check the run pauses cleanly; the rest is
// recorded as incomplete.
func TestLiveCheck_PacesOnTheRecordedEvents(t *testing.T) {
	env, _, _ := pacedEnv(t)
	var specs []liveProcSpec
	env.run = recordedHost(t, &specs, nil)
	o := checkRun(t, eval.LiveCheckTierQuick)
	o.sevenDayCeiling = eval.LiveSevenDayCeiling
	var log bytes.Buffer
	require.NoError(t, runLiveCheck(context.Background(), o, env, &log), log.String())
	require.Len(t, specs, 1)
	_, r := readCheckRun(t, o.out)
	require.Contains(t, r.Paused, "seven_day")
	require.Equal(t, eval.LiveCheckVerdictIncomplete, r.Checks[1].Verdict)
}

// TestLiveCheck_CompareTwoRuns: --compare sets two runs side by side; a host that stops recalling the
// codeword shows as "now fails". The output is pinned by a golden.
func TestLiveCheck_CompareTwoRuns(t *testing.T) {
	env, _, _ := pacedEnv(t)
	var specs []liveProcSpec
	env.run = recordedHost(t, &specs, nil)
	a := checkRun(t, eval.LiveCheckTierQuick)
	var log bytes.Buffer
	require.NoError(t, runLiveCheck(context.Background(), a, env, &log), log.String())
	env.run = recordedHost(t, &specs, func(s string) string { return strings.ReplaceAll(s, "HELIOTROPE-47 and", "something and") })
	b := checkRun(t, eval.LiveCheckTierQuick)
	b.bundle = a.bundle
	require.NoError(t, runLiveCheck(context.Background(), b, env, &log), log.String())

	var w bytes.Buffer
	require.NoError(t, compareLiveChecks(a.out, b.out, &w))
	ma, _ := readCheckRun(t, a.out)
	mb, _ := readCheckRun(t, b.out)
	got := strings.ReplaceAll(strings.ReplaceAll(w.String(), ma.RunID, "<run a>"), mb.RunID, "<run b>")
	require.Contains(t, got, "| compact-recall | pass | fail | now fails |")
	require.Contains(t, got, "| plugin-loads | pass | pass | same |")
	requireGolden(t, "live-check-compare.golden.md", got)

	require.Error(t, compareLiveChecks(a.out, t.TempDir(), &w), "a directory with no run is refused")
}

// TestLiveCheck_DryRunAndFlags: a dry run validates the suite and bundles and prints each check's
// command line and environment without starting anything; the flags are checked.
func TestLiveCheck_DryRunAndFlags(t *testing.T) {
	env := fakeLiveEnv(t, nil)
	o := checkRun(t, eval.LiveCheckTierMetrics)
	o.dryRun = true
	var w bytes.Buffer
	require.NoError(t, runLiveCheck(context.Background(), o, env, &w))
	require.Contains(t, w.String(), "compact-recall: 3 run(s)")
	require.Contains(t, w.String(), "--plugin-dir "+o.bundle)
	require.Contains(t, w.String(), "no session started")
	require.NoFileExists(t, filepath.Join(o.out, "manifest.json"))

	o.dryRun, o.bundle = false, ""
	require.ErrorContains(t, runLiveCheck(context.Background(), o, env, &w), "--bundle")
	o.bundle = fakeBundle(t)
	require.ErrorContains(t, runLiveCheck(context.Background(), o, env, &w), liveEvalGateEnv)

	p, err := parseLiveCheckTest([]string{"--suite", "s.json"})
	require.NoError(t, err)
	require.Equal(t, eval.LiveCheckTierQuick, p.tier)
	require.Equal(t, liveCheckPrecomputeUnread, p.precompute)
	p, err = parseLiveCheckFlags([]string{"--compare", "a", "b"})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, p.compare)
	for _, bad := range [][]string{
		{"--suite", "s.json", "--tier", "slow"}, {}, {"--compare", "a"}, {"--suite", "s", "--seven-day-ceiling", "2"},
	} {
		_, err := parseLiveCheckTest(bad)
		require.Error(t, err, bad)
	}

	// A check may not set the variables the harness sets itself.
	raw, err := os.ReadFile(liveTestCheckSuite)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	checks, ok := m["checks"].([]any)
	require.True(t, ok)
	second, ok := checks[1].(map[string]any)
	require.True(t, ok)
	second["env"] = map[string]any{"DISABLE_AUTOUPDATER": "0"}
	b, err := json.Marshal(m)
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "fixtures", "pilot"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "checks.json"), b, 0o600))
	o.suite, o.dryRun = filepath.Join(dir, "checks.json"), true
	require.ErrorContains(t, runLiveCheck(context.Background(), o, env, &w), "DISABLE_AUTOUPDATER")
}
