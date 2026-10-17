package eval_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

const liveCheckSuiteFile = "../../testdata/eval/live/checks-v7-test.json"

func loadSuite(t *testing.T) eval.LiveCheckSuite {
	t.Helper()
	s, _, err := eval.LoadLiveCheckSuite(liveCheckSuiteFile)
	require.NoError(t, err)
	return s
}

// TestLoadLiveCheckSuite: the test-only suite parses and validates; the per-tier repeat counts read
// as the plan states them (quick runs each check once, metrics repeats only the model-dependent ones).
func TestLoadLiveCheckSuite(t *testing.T) {
	s, raw, err := eval.LoadLiveCheckSuite(liveCheckSuiteFile)
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	require.Equal(t, eval.LiveCheckSuiteVersion, s.Version)
	require.Len(t, s.Checks, 2)
	recall, plugin := s.Checks[0], s.Checks[1]
	require.Equal(t, 1, recall.Runs(eval.LiveCheckTierQuick))
	require.Equal(t, 3, recall.Runs(eval.LiveCheckTierMetrics))
	require.Equal(t, 1, plugin.Runs(eval.LiveCheckTierMetrics))
	require.Equal(t, "claude-opus-5-5", recall.EffectiveModel(s))
	require.Equal(t, filepath.Join("..", "..", "testdata", "eval", "live", "fixtures", "pilot"), recall.FixtureDir(liveCheckSuiteFile))
}

// TestParseLiveCheckSuite_Rejects: a suite that could mislead a re-run is refused before any session.
func TestParseLiveCheckSuite_Rejects(t *testing.T) {
	raw, err := os.ReadFile(liveCheckSuiteFile)
	require.NoError(t, err)
	mutate := func(f func(m map[string]any)) []byte {
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		f(m)
		b, err := json.Marshal(m)
		require.NoError(t, err)
		return b
	}
	obj := func(v any) map[string]any {
		o, ok := v.(map[string]any)
		require.True(t, ok, "%T is not a JSON object", v)
		return o
	}
	check := func(m map[string]any, i int) map[string]any {
		cs, ok := m["checks"].([]any)
		require.True(t, ok, "checks is not a JSON array")
		return obj(cs[i])
	}
	firstStep := func(m map[string]any) map[string]any {
		steps, ok := check(m, 0)["steps"].([]any)
		require.True(t, ok, "steps is not a JSON array")
		return obj(steps[0])
	}
	cases := map[string]func(m map[string]any){
		"version":              func(m map[string]any) { m["version"] = 2 },
		"no checks":            func(m map[string]any) { m["checks"] = []any{} },
		"duplicate id":         func(m map[string]any) { check(m, 1)["id"] = "compact-recall" },
		"quick repeats":        func(m map[string]any) { check(m, 0)["repeat"] = map[string]any{"quick": 2, "metrics": 3} },
		"model-dependent once": func(m map[string]any) { check(m, 0)["repeat"] = map[string]any{"quick": 1, "metrics": 1} },
		"repeated, not model":  func(m map[string]any) { check(m, 1)["repeat"] = map[string]any{"quick": 1, "metrics": 4} },
		"unknown record":       func(m map[string]any) { check(m, 0)["record"] = []any{"everything"} },
		"unclean file record":  func(m map[string]any) { check(m, 1)["record"] = []any{"files:../x"} },
		"unknown condition":    func(m map[string]any) { check(m, 0)["pass"] = []any{map[string]any{"kind": "vibes"}} },
		"answer of no step": func(m map[string]any) {
			check(m, 0)["pass"] = []any{map[string]any{"kind": "answer_matches", "step": "nope", "pattern": "x"}}
		},
		"neither pass nor metric": func(m map[string]any) { delete(check(m, 1), "pass") },
		"unknown metric":          func(m map[string]any) { check(m, 0)["metric"] = "happiness" },
		"bad env name":            func(m map[string]any) { check(m, 1)["env"] = map[string]any{"lower": "x"} },
		"bad probe":               func(m map[string]any) { check(m, 1)["probe"] = "/abs/dir" },
		"slash prompt":            func(m map[string]any) { firstStep(m)["prompt"] = "/compact" },
		"unknown field":           func(m map[string]any) { check(m, 0)["passes"] = true },
		"stream line no min": func(m map[string]any) {
			check(m, 1)["pass"] = []any{map[string]any{"kind": "stream_lines", "line": "x"}}
		},
	}
	_, err = eval.ParseLiveCheckSuite(mutate(func(map[string]any) {}))
	require.NoError(t, err, "the unmutated suite parses, so each case fails for its own reason")
	for name, f := range cases {
		_, err := eval.ParseLiveCheckSuite(mutate(f))
		require.Error(t, err, name)
	}
}

// TestGradeLiveCheckRun_RecordedStreams: the recorded 2.1.280 sessions graded by the suite's checks:
// the stock session passes compact-recall (completed, the codeword recalled), the plugin session
// passes plugin-loads, and each run records what its check asks for.
func TestGradeLiveCheckRun_RecordedStreams(t *testing.T) {
	s := loadSuite(t)
	recall, plugin := s.Checks[0], s.Checks[1]
	stock, plug := liveFixture(t, smoke1Stream), liveFixture(t, smoke2Stream)
	project := t.TempDir()

	run := eval.GradeLiveCheckRun(recall, project, parseLiveFixture(t, smoke1Stream), eval.CountStreamLines(stock), nil, nil, 1234)
	require.True(t, run.Passed, "%v", run.Failures)
	require.Equal(t, "claude-haiku-4-5-20251001", run.Record.HostModel)
	require.Equal(t, "2.1.280", run.Record.ClaudeCodeVersion)
	require.Len(t, run.Record.Compactions, 1)
	require.Len(t, run.Record.RateLimits, 1)
	require.Equal(t, 3, run.Record.StreamLines["system/init"])
	require.Empty(t, run.Record.Plugins, "plugins are recorded only when the check asks")
	require.NotNil(t, run.Metric)
	require.InDelta(t, 1, *run.Metric, 0, "rate_limit_events counts the session's events")

	run = eval.GradeLiveCheckRun(plugin, project, parseLiveFixture(t, smoke2Stream), eval.CountStreamLines(plug), nil,
		map[string]int64{".qompack/checkpoints/MANIFEST.jsonl": 120}, 99)
	require.True(t, run.Passed, "%v", run.Failures)
	require.Contains(t, run.Record.Plugins, "qompack@inline")
	require.NotEmpty(t, run.Record.Hooks)
	require.Equal(t, map[string]int64{".qompack/checkpoints/MANIFEST.jsonl": 120}, run.Record.Files)
	require.Nil(t, run.Metric)

	run = eval.GradeLiveCheckRun(plugin, project, parseLiveFixture(t, smoke1Stream), eval.CountStreamLines(stock), nil, nil, 1)
	require.False(t, run.Passed, "the stock session loads no Qompack and runs no hook")
	require.Len(t, run.Failures, 2)

	cut := strings.ReplaceAll(string(stock), "HELIOTROPE-47 and", "something and")
	run = eval.GradeLiveCheckRun(recall, project, mustParse(t, cut), eval.CountStreamLines([]byte(cut)), nil, nil, 1)
	require.False(t, run.Passed)
	require.Contains(t, strings.Join(run.Failures, "\n"), "answer_matches")
}

func mustParse(t *testing.T, s string) eval.HostStream {
	t.Helper()
	hs, err := eval.ParseHostStream(strings.NewReader(s), nil)
	require.NoError(t, err)
	return hs
}

// TestCountStreamLines counts lines by type and by type/subtype; a line that is not JSON counts under
// "unparsed".
func TestCountStreamLines(t *testing.T) {
	got := eval.CountStreamLines([]byte(rlInit + "\n" + rlSmoke + "\n\n" + rlResult + "\nnot json\n"))
	require.Equal(t, map[string]int{
		"system": 1, "system/init": 1, "rate_limit_event": 1, "result": 1, "result/success": 1, "unparsed": 1,
	}, got)
}

// TestSummarizeLiveCheck: quick passes or fails on its one run; metrics reports a rate with a 95 %
// Wilson interval; a harness error or a limited run is never a pass, and a limited run is not counted.
func TestSummarizeLiveCheck(t *testing.T) {
	s := loadSuite(t)
	recall := s.Checks[0]
	m := func(v float64) *float64 { return &v }
	runs := []eval.LiveCheckRun{
		{Run: 1, Passed: true, Metric: m(1)},
		{Run: 2, Passed: false, Failures: []string{"x"}, Metric: m(3)},
		{Run: 2, Limited: "rejected", Metric: m(9)},
		{Run: 3, Passed: true, Metric: m(2)},
	}
	out := eval.SummarizeLiveCheck(recall, eval.LiveCheckTierMetrics, runs)
	require.Equal(t, 3, out.Planned)
	require.Equal(t, 3, out.Graded)
	require.Equal(t, 2, out.Passed)
	require.Equal(t, 1, out.Limited)
	require.Equal(t, eval.LiveCheckVerdictRate, out.Verdict)
	require.NotNil(t, out.PassRate)
	require.InDelta(t, 2.0/3, out.PassRate.Rate, 1e-9)
	require.Less(t, out.PassRate.Low, out.PassRate.Rate)
	require.NotNil(t, out.MetricMean)
	require.InDelta(t, 2, *out.MetricMean, 1e-9, "the limited run's metric is not counted")

	q := eval.SummarizeLiveCheck(recall, eval.LiveCheckTierQuick, runs[:1])
	require.Equal(t, eval.LiveCheckVerdictPass, q.Verdict)
	q = eval.SummarizeLiveCheck(recall, eval.LiveCheckTierQuick, runs[1:2])
	require.Equal(t, eval.LiveCheckVerdictFail, q.Verdict)
	q = eval.SummarizeLiveCheck(recall, eval.LiveCheckTierQuick, []eval.LiveCheckRun{{Run: 1, Passed: true, HarnessError: "timeout"}})
	require.Equal(t, eval.LiveCheckVerdictFail, q.Verdict, "a harness error is never a pass")
	q = eval.SummarizeLiveCheck(recall, eval.LiveCheckTierMetrics, runs[:1])
	require.Equal(t, eval.LiveCheckVerdictIncomplete, q.Verdict, "fewer graded runs than planned")
}

// TestRenderLiveCheckCompare: two runs side by side, one row per check, saying what changed.
func TestRenderLiveCheckCompare(t *testing.T) {
	a := eval.LiveCheckRunFiles{
		Manifest: eval.LiveCheckManifest{RunID: "run-a", ClaudeCodeVersion: "2.1.300", Tier: eval.LiveCheckTierQuick},
		Results: eval.LiveCheckResults{Checks: []eval.LiveCheckOutcome{
			{ID: "compact-recall", Verdict: eval.LiveCheckVerdictPass, Passed: 1, Graded: 1, Planned: 1},
			{ID: "plugin-loads", Verdict: eval.LiveCheckVerdictPass, Passed: 1, Graded: 1, Planned: 1},
			{ID: "gone", Verdict: eval.LiveCheckVerdictPass, Passed: 1, Graded: 1, Planned: 1},
		}},
	}
	b := eval.LiveCheckRunFiles{
		Manifest: eval.LiveCheckManifest{RunID: "run-b", ClaudeCodeVersion: "2.1.301", Tier: eval.LiveCheckTierQuick},
		Results: eval.LiveCheckResults{Checks: []eval.LiveCheckOutcome{
			{ID: "compact-recall", Verdict: eval.LiveCheckVerdictFail, Graded: 1, Planned: 1},
			{ID: "plugin-loads", Verdict: eval.LiveCheckVerdictPass, Passed: 1, Graded: 1, Planned: 1},
			{ID: "new", Verdict: eval.LiveCheckVerdictFail, Graded: 1, Planned: 1},
		}},
	}
	got := eval.RenderLiveCheckCompare(a, b)
	for _, want := range []string{
		"| check | a: Claude Code 2.1.300, run run-a | b: Claude Code 2.1.301, run run-b | change |",
		"| compact-recall | pass | fail | now fails |",
		"| plugin-loads | pass | pass | same |",
		"| gone | pass | — | only in a |",
		"| new | — | fail | only in b |",
	} {
		require.Contains(t, got, want)
	}
}
