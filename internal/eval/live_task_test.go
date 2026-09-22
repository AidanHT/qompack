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

func liveTaskFile(name string) string {
	return filepath.Join("..", "..", "testdata", "eval", "live", name)
}

// TestLiveTaskSet_TheFrozenSetIsValidAndShapedAsPreregistered pins the declared set against the
// pre-registration: ten tasks, three held out, two changing-requirement variants, every task with
// a forced compaction between work and graded work, and the analysis parameters it names.
func TestLiveTaskSet_TheFrozenSetIsValidAndShapedAsPreregistered(t *testing.T) {
	ts, raw, err := eval.LoadLiveTaskSet(liveTaskFile("tasks.json"))
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	require.Equal(t, "qompack-live-v1", ts.ID)
	require.Len(t, ts.Tasks, 10)
	require.Equal(t, eval.LiveAnalysis{Model: "claude-sonnet-5", Confidence: 0.95, NonInferiorityMargin: 0.2, TrialsPerArm: 2}, ts.Analysis)

	var heldOut, changing int
	outcomes := map[string]int{}
	for _, task := range ts.Tasks {
		if task.HeldOut {
			heldOut++
		}
		if task.Variant == "changing-requirement" {
			changing++
		}
		compacts := 0
		for _, s := range task.Steps {
			if s.Compact {
				compacts++
				require.Equal(t, "/compact", s.Message(), "both arms send the identical compaction turn")
			}
		}
		require.Equal(t, 1, compacts, "task %s forces exactly one compaction", task.ID)
		for _, c := range task.Checks {
			outcomes[c.Outcome]++
		}
	}
	require.Equal(t, 3, heldOut)
	require.Equal(t, 2, changing)
	for _, o := range []string{eval.OutcomeTask, eval.OutcomeConstraint, eval.OutcomeRecovery} {
		require.Positive(t, outcomes[o], "the set grades %s", o)
	}

	pilot, _, err := eval.LoadLiveTaskSet(liveTaskFile("pilot.json"))
	require.NoError(t, err)
	for _, p := range pilot.Tasks {
		for _, task := range ts.Tasks {
			require.NotEqual(t, task.ID, p.ID, "the pilot shares no task with the confirmatory set")
			require.NotEqual(t, task.Fixture, p.Fixture, "the pilot shares no fixture with the confirmatory set")
		}
	}
}

// TestLiveTaskSet_ValidationRefusesEveryMalformedShape: each mutation breaks one rule, and each is
// refused before any session could be spent on it.
func TestLiveTaskSet_ValidationRefusesEveryMalformedShape(t *testing.T) {
	base := func() map[string]any {
		raw, err := os.ReadFile(liveTaskFile("pilot.json"))
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		return m
	}
	task := func(m map[string]any) map[string]any { return m["tasks"].([]any)[0].(map[string]any) }
	check := func(m map[string]any, i int) map[string]any { return task(m)["checks"].([]any)[i].(map[string]any) }

	cases := map[string]struct {
		mutate func(m map[string]any)
		want   string
	}{
		"unknown key":      {func(m map[string]any) { task(m)["chekcs"] = []any{} }, "unknown field"},
		"no model":         {func(m map[string]any) { m["analysis"].(map[string]any)["model"] = "" }, "pre-registered model"},
		"escaping fixture": {func(m map[string]any) { task(m)["fixture"] = "../outside" }, "stay beneath"},
		"compaction first": {func(m map[string]any) {
			steps := task(m)["steps"].([]any)
			task(m)["steps"] = []any{steps[1], steps[0], steps[2]}
		}, "nothing to compact"},
		"no compaction": {func(m map[string]any) {
			steps := task(m)["steps"].([]any)
			task(m)["steps"] = []any{steps[0], steps[2]}
		}, "no compaction step"},
		"slash prompt":         {func(m map[string]any) { task(m)["steps"].([]any)[0].(map[string]any)["prompt"] = "/clear" }, "may not start with '/'"},
		"answer on compact":    {func(m map[string]any) { check(m, 0)["step"] = "compact" }, "not a prompt step"},
		"tool check on prompt": {func(m map[string]any) { check(m, 3)["step"] = "run" }, "not a compaction step"},
		"arbitrary command":    {func(m map[string]any) { check(m, 5)["argv"] = []any{"rm", "-rf", "."} }, "may run only"},
		"bad regex":            {func(m map[string]any) { check(m, 1)["pattern"] = "(" }, "missing closing"},
		"unknown outcome":      {func(m map[string]any) { check(m, 1)["outcome"] = "vibes" }, "not task, constraint or recovery"},
		"duplicate check": {func(m map[string]any) {
			checks := task(m)["checks"].([]any)
			task(m)["checks"] = append(checks, checks[0])
		}, "declared twice"},
	}
	for name, c := range cases {
		m := base()
		c.mutate(m)
		raw, err := json.Marshal(m)
		require.NoError(t, err)
		_, err = eval.ParseLiveTaskSet(raw)
		require.Error(t, err, name)
		require.Contains(t, err.Error(), c.want, name)
	}
}

func TestLiveStep_Message(t *testing.T) {
	require.Equal(t, "/compact", eval.LiveStep{Compact: true}.Message())
	require.Equal(t, "/compact keep the rules", eval.LiveStep{Compact: true, CompactInstructions: "keep the rules"}.Message())
	require.Equal(t, "hi", eval.LiveStep{Prompt: "hi"}.Message())
}

// TestGradeLiveTrial_EveryKindAndItsUngradedCase: each kind passes on the evidence that satisfies
// it and fails, saying why, when its evidence is absent — an ungraded check is never a pass.
func TestGradeLiveTrial_EveryKindAndItsUngradedCase(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n\nbeta\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "same.txt"), []byte("fixture\n"), 0o600))
	fixture, err := eval.HashTree(root)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n\nbeta\ngamma\n"), 0o600))

	three := 3
	task := eval.LiveTask{
		ID: "t",
		Steps: []eval.LiveStep{
			{ID: "ask", Prompt: "q"}, {ID: "compact", Compact: true}, {ID: "after", Prompt: "q2"},
		},
		Checks: []eval.LiveCheck{
			{ID: "m", Outcome: eval.OutcomeTask, Kind: eval.CheckFileMatches, Path: "a.txt", Pattern: "gamma"},
			{ID: "nm", Outcome: eval.OutcomeConstraint, Kind: eval.CheckFileNotMatches, Path: "a.txt", Pattern: "delta"},
			{ID: "ex", Outcome: eval.OutcomeTask, Kind: eval.CheckFileExists, Path: "a.txt"},
			{ID: "ab", Outcome: eval.OutcomeConstraint, Kind: eval.CheckFileAbsent, Path: "gone.txt"},
			{ID: "un", Outcome: eval.OutcomeConstraint, Kind: eval.CheckFileUnchanged, Path: "same.txt"},
			{ID: "ch", Outcome: eval.OutcomeConstraint, Kind: eval.CheckFileUnchanged, Path: "a.txt"},
			{ID: "ln", Outcome: eval.OutcomeTask, Kind: eval.CheckFileLines, Path: "a.txt", Lines: &three},
			{ID: "am", Outcome: eval.OutcomeRecovery, Kind: eval.CheckAnswerMatches, Step: "after", Pattern: "(?i)owl"},
			{ID: "missing-answer", Outcome: eval.OutcomeRecovery, Kind: eval.CheckAnswerMatches, Step: "ask", Pattern: "x"},
			{ID: "cmd-ok", Outcome: eval.OutcomeTask, Kind: eval.CheckCommand, Argv: []string{"go", "test"}},
			{ID: "cmd-missing", Outcome: eval.OutcomeTask, Kind: eval.CheckCommand, Argv: []string{"go", "vet"}},
			{ID: "tool-ok", Outcome: eval.OutcomeConstraint, Kind: eval.CheckToolNotUsedAfter, Step: "compact", Pattern: "go run"},
			{ID: "path-escape", Outcome: eval.OutcomeTask, Kind: eval.CheckFileExists, Path: "../outside.txt"},
		},
	}
	res := eval.GradeLiveTrial(task, root, eval.LiveEvidence{
		Answers:  map[string]string{"after": "The alias is Tundra-Owl."},
		Fixture:  fixture,
		Commands: map[string]eval.CommandOutcome{"cmd-ok": {ExitCode: 0}},
		ToolUsesAfter: map[string][]eval.HostToolUse{
			"compact": {{Name: "Read", Input: json.RawMessage(`{"file_path":"a.txt"}`)}},
		},
	})
	got := map[string]bool{}
	for _, r := range res {
		got[r.ID] = r.Passed
		require.NotEmpty(t, r.Detail, "check %s explains its verdict", r.ID)
	}
	require.Equal(t, map[string]bool{
		"m": true, "nm": true, "ex": true, "ab": true, "un": true, "ch": false, "ln": true, "am": true,
		"missing-answer": false, "cmd-ok": true, "cmd-missing": false, "tool-ok": true, "path-escape": false,
	}, got)

	// A matching tool call after the compaction fails the check, and an unreached step fails it.
	res = eval.GradeLiveTrial(eval.LiveTask{Checks: task.Checks[11:12]}, root, eval.LiveEvidence{
		ToolUsesAfter: map[string][]eval.HostToolUse{"compact": {{Name: "Bash", Input: json.RawMessage(`{"command":"go run ./cmd/probe"}`)}}},
	})
	require.False(t, res[0].Passed)
	require.Contains(t, res[0].Detail, "go run")
	res = eval.GradeLiveTrial(eval.LiveTask{Checks: task.Checks[11:12]}, root, eval.LiveEvidence{})
	require.False(t, res[0].Passed)
	require.Contains(t, res[0].Detail, "never reached")
}

// TestToolUsesAfterSteps_OnARealStream: on smoke2 (Read, /compact, answer) the compaction was
// reached and nothing ran after it.
func TestToolUsesAfterSteps_OnARealStream(t *testing.T) {
	s := parseLiveFixture(t, smoke2Stream)
	task := eval.LiveTask{Steps: []eval.LiveStep{{ID: "read", Prompt: "p"}, {ID: "compact", Compact: true}, {ID: "ask", Prompt: "q"}}}
	after := eval.ToolUsesAfterSteps(task, s)
	require.Contains(t, after, "compact")
	require.Empty(t, after["compact"])

	answers := eval.AnswersByStep(task, s)
	require.Equal(t, map[string]string{
		"read": "The release codeword is HELIOTROPE-47.",
		"ask":  "The release codeword is HELIOTROPE-47 and the config port must stay 8443.",
	}, answers)
	steps, complete := eval.StepRecords(task, s)
	require.True(t, complete)
	require.Len(t, steps, 3)
	require.True(t, steps[1].Compact)
	require.Len(t, steps[1].LocalOutput, 1)
	require.True(t, strings.HasPrefix(steps[1].LocalOutput[0], "Compacted PreCompact"))

	// A task whose compaction step did not compact is incomplete.
	stock := parseLiveFixture(t, smoke3Stream)
	_, complete = eval.StepRecords(eval.LiveTask{Steps: []eval.LiveStep{{ID: "a", Prompt: "p"}, {ID: "c", Compact: true}}}, stock)
	require.False(t, complete, "smoke3's second turn was an ordinary turn, not the /compact the task asked for")
}
