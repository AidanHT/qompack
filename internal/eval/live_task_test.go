package eval_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// preregistrationFile is the frozen pre-registration of the qompack-live-v1 study.
var preregistrationFile = filepath.Join("..", "..", "plans", "sdd", "V6-closeout", "eval", "preregistration.md")

// TestLiveTaskSet_FrozenMaterialsMatchThePreregistration: every frozen material still hashes to the
// SHA-256 the pre-registration's section 2 table records — read out of the document itself, so the
// test and the document cannot drift apart — the fixture and hidden-test tree hashes to the
// locale-independent manifest value its amendment A1 records (the value `devtool live-eval` writes
// into every run's plan.json), and the section 4 task table names exactly the tasks, variants and
// held-out flags of tasks.json.
func TestLiveTaskSet_FrozenMaterialsMatchThePreregistration(t *testing.T) {
	raw, err := os.ReadFile(preregistrationFile)
	require.NoError(t, err)
	doc := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, file := range []string{"tasks.json", "rates.json", "pilot.json"} {
		row := regexp.MustCompile("(?m)^\\|[^|\\n]*\\|\\s*`testdata/eval/live/" + regexp.QuoteMeta(file) +
			"`\\s*\\|\\s*`([0-9a-f]{64})`\\s*\\|")
		m := row.FindStringSubmatch(doc)
		require.NotNil(t, m, "preregistration section 2 records a SHA-256 for %s", file)
		content, err := os.ReadFile(liveTaskFile(file))
		require.NoError(t, err)
		sum := sha256.Sum256(content)
		require.Equal(t, m[1], hex.EncodeToString(sum[:]), "%s is not the pre-registered file", file)
	}

	tree, err := eval.TreeManifestSHA256(filepath.Dir(liveTaskFile("tasks.json")), "fixtures", "hidden")
	require.NoError(t, err)
	require.Regexp(t, "(?s)### Amendments.*A1.*`"+tree+"`", doc,
		"amendment A1 records the fixture tree's manifest hash")

	ts, _, err := eval.LoadLiveTaskSet(liveTaskFile("tasks.json"))
	require.NoError(t, err)
	taskRow := regexp.MustCompile("(?m)^\\| `([A-Za-z0-9._-]+)` \\| [^|]+ \\| ([a-z-]+) \\| (\\*\\*yes\\*\\*|no) \\|$")
	var got, want []string
	for _, m := range taskRow.FindAllStringSubmatch(doc, -1) {
		got = append(got, fmt.Sprintf("%s %s held=%t", m[1], m[2], m[3] != "no"))
	}
	for _, task := range ts.Tasks {
		want = append(want, fmt.Sprintf("%s %s held=%t", task.ID, task.Variant, task.HeldOut))
	}
	require.Equal(t, want, got, "the section 4 table is the task set, in order")
	require.Contains(t, doc, "`"+ts.Analysis.Model+"`", "section 3 names the pre-registered model")
	require.Contains(t, doc, fmt.Sprintf("margin %.2f", ts.Analysis.NonInferiorityMargin),
		"section 8 names the task set's margin")
	require.Contains(t, doc, fmt.Sprintf("%d%% Newcombe", int(ts.Analysis.Confidence*100)),
		"section 8 names the task set's confidence level")
}

// TestTreeManifestSHA256_IsTheSha256sumManifestInByteOrder pins the recipe: one
// "<sha256>  <slash path>\n" line per regular file, paths relative to the root and in byte order,
// hashed — exactly `find <dirs> -type f | LC_ALL=C sort | xargs sha256sum | sha256sum` run from
// the root with GNU coreutils in text mode.
func TestTreeManifestSHA256_IsTheSha256sumManifestInByteOrder(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"b/Z.txt": "z\n", "b/a.txt": "a\n", "a/x/y.go": "package y\n"}
	for p, body := range files {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.Dir(filepath.FromSlash(p))), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(p)), []byte(body), 0o600))
	}
	var manifest strings.Builder
	for _, p := range []string{"a/x/y.go", "b/Z.txt", "b/a.txt"} {
		sum := sha256.Sum256([]byte(files[p]))
		manifest.WriteString(hex.EncodeToString(sum[:]) + "  " + p + "\n")
	}
	want := sha256.Sum256([]byte(manifest.String()))
	got, err := eval.TreeManifestSHA256(root, "b", "a")
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(want[:]), got, "argument order does not matter; byte order of paths does")

	_, err = eval.TreeManifestSHA256(root, "missing")
	require.Error(t, err, "a directory that is not there is not an empty tree")
	_, err = eval.TreeManifestSHA256(root, "../escape")
	require.Error(t, err, "a directory outside the root is refused")
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
	obj := func(v any) map[string]any {
		m, ok := v.(map[string]any)
		require.True(t, ok, "%T is not an object", v)
		return m
	}
	arr := func(v any) []any {
		a, ok := v.([]any)
		require.True(t, ok, "%T is not an array", v)
		return a
	}
	task := func(m map[string]any) map[string]any { return obj(arr(m["tasks"])[0]) }
	check := func(m map[string]any, i int) map[string]any { return obj(arr(task(m)["checks"])[i]) }

	cases := map[string]struct {
		mutate func(m map[string]any)
		want   string
	}{
		"unknown key":      {func(m map[string]any) { task(m)["chekcs"] = []any{} }, "unknown field"},
		"no model":         {func(m map[string]any) { obj(m["analysis"])["model"] = "" }, "pre-registered model"},
		"escaping fixture": {func(m map[string]any) { task(m)["fixture"] = "../outside" }, "stay beneath"},
		"compaction first": {func(m map[string]any) {
			steps := arr(task(m)["steps"])
			task(m)["steps"] = []any{steps[1], steps[0], steps[2]}
		}, "nothing to compact"},
		"no compaction": {func(m map[string]any) {
			steps := arr(task(m)["steps"])
			task(m)["steps"] = []any{steps[0], steps[2]}
		}, "no compaction step"},
		"slash prompt":         {func(m map[string]any) { obj(arr(task(m)["steps"])[0])["prompt"] = "/clear" }, "may not start with '/'"},
		"answer on compact":    {func(m map[string]any) { check(m, 0)["step"] = "compact" }, "not a prompt step"},
		"tool check on prompt": {func(m map[string]any) { check(m, 3)["step"] = "run" }, "not a compaction step"},
		"arbitrary command":    {func(m map[string]any) { check(m, 5)["argv"] = []any{"rm", "-rf", "."} }, "may run only"},
		"bad regex":            {func(m map[string]any) { check(m, 1)["pattern"] = "(" }, "missing closing"},
		"unknown outcome":      {func(m map[string]any) { check(m, 1)["outcome"] = "vibes" }, "not task, constraint or recovery"},
		"duplicate check": {func(m map[string]any) {
			checks := arr(task(m)["checks"])
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

// TestGradeLiveTrial_FileLinesCountsEveryLine: a file_lines check counts every non-empty line of a
// file up to the grader's size bound, however long one line is. A line-scanner with a fixed token
// limit stops at the first long line and under-counts, which can turn a wrong file into a pass.
func TestGradeLiveTrial_FileLinesCountsEveryLine(t *testing.T) {
	root := t.TempDir()
	body := "short\n" + strings.Repeat("x", 100<<10) + "\nthird\r\n\n  \nfourth"
	require.NoError(t, os.WriteFile(filepath.Join(root, "f.txt"), []byte(body), 0o600))
	two, four := 2, 4
	task := eval.LiveTask{Checks: []eval.LiveCheck{
		{ID: "four", Outcome: eval.OutcomeTask, Kind: eval.CheckFileLines, Path: "f.txt", Lines: &four},
		{ID: "two", Outcome: eval.OutcomeTask, Kind: eval.CheckFileLines, Path: "f.txt", Lines: &two},
	}}
	res := eval.GradeLiveTrial(task, root, eval.LiveEvidence{})
	require.True(t, res[0].Passed, "four non-empty lines: %s", res[0].Detail)
	require.False(t, res[1].Passed, "a count cut short at the long line must not pass: %s", res[1].Detail)
}

// TestLivePreregistrations_MatchTheDocumentAndTheMaterials: the code's record of what the
// qompack-live-v1 pre-registration froze — the task set's file hash, the fixture tree's manifest
// hash and the install path of the qompack arm — is what the document records (section 2's task-set
// row, amendment A1's tree value, section 3's --plugin-dir) and what the committed materials hash to
// now. It is the record `qompack eval` checks a run's plan against before calling it confirmatory,
// so a run of an edited task set under the same id cannot be judged as the pre-registered study.
func TestLivePreregistrations_MatchTheDocumentAndTheMaterials(t *testing.T) {
	raw, err := os.ReadFile(preregistrationFile)
	require.NoError(t, err)
	doc := strings.ReplaceAll(string(raw), "\r\n", "\n")

	ts, taskBytes, err := eval.LoadLiveTaskSet(liveTaskFile("tasks.json"))
	require.NoError(t, err)
	pre, ok := eval.LivePreregistrations[ts.ID]
	require.True(t, ok, "the frozen task set %s is pre-registered", ts.ID)

	sum := sha256.Sum256(taskBytes)
	require.Equal(t, hex.EncodeToString(sum[:]), pre.TaskSetSHA256, "the recorded task-set hash is the committed file's")
	row := regexp.MustCompile(`(?m)^\|[^|\n]*\|\s*` + "`" + `testdata/eval/live/tasks\.json` + "`" + `\s*\|\s*` + "`" +
		`([0-9a-f]{64})` + "`" + `\s*\|`)
	m := row.FindStringSubmatch(doc)
	require.NotNil(t, m)
	require.Equal(t, m[1], pre.TaskSetSHA256, "the recorded task-set hash is section 2's")

	tree, err := eval.TreeManifestSHA256(filepath.Dir(liveTaskFile("tasks.json")), "fixtures", "hidden")
	require.NoError(t, err)
	require.Equal(t, tree, pre.FixtureTreeSHA256, "the recorded fixture tree is the committed tree")
	require.Regexp(t, "(?s)### Amendments.*A1.*`"+pre.FixtureTreeSHA256+"`", doc, "and amendment A1's value")

	require.Equal(t, "plugin-dir", pre.Install)
	require.Contains(t, doc, "loaded from one frozen bundle with `--plugin-dir`", "section 3 names the install path")

	flat := strings.Join(strings.Fields(doc), " ")
	require.Equal(t, ts.Analysis.Model, pre.Model, "the pre-registered model is the task set's")
	require.Contains(t, flat, "**Model:** `"+pre.Model+"`, pinned by ID", "section 3 names the model")
	require.Contains(t, flat, "the run is restarted with the host alias `"+pre.ModelContingency+"`",
		"section 3 names the one contingency alias")
	require.Regexp(t, "(?s)### Amendments.*A6.*alias `"+pre.ModelContingency+"`", doc,
		"amendment A6 records when a contingency run is confirmatory")
	require.True(t, pre.RunsPreregisteredModel(ts.Analysis.Model, ts.Analysis.Model))
	require.True(t, pre.RunsPreregisteredModel(ts.Analysis.Model, pre.ModelContingency))
	require.False(t, pre.RunsPreregisteredModel(ts.Analysis.Model, "opus"))
	require.False(t, pre.RunsPreregisteredModel("claude-haiku-4-5", pre.ModelContingency),
		"the alias stands in only for the model the pre-registration froze")

	require.Equal(t, []string{"C1.12", "C1.1"}, pre.RequiredFixed)
	require.Contains(t, flat, "The confirmatory run must be on a candidate where "+
		strings.Join(pre.RequiredFixed, " and ")+" are fixed", "section 9 names the defects the candidate must not carry")
	require.Regexp(t, "(?s)### Amendments.*A5.*--known-open-defects none", doc,
		"amendment A5 records how the section 9 precondition is attested, and the command that attests it")
	require.Equal(t, filepath.ToSlash(filepath.Clean(strings.TrimPrefix(filepath.ToSlash(preregistrationFile), "../../"))),
		pre.Document)

	_, pilot := eval.LivePreregistrations["qompack-live-pilot-v1"]
	require.False(t, pilot, "the pilot set is harness validation, never pre-registered")
}

// TestToolUsesAfterSteps_TheArchiveIsRecoveryNotRederivation: tool-output-recall's no-rerun check
// asks whether the probe's token was re-derived after the compaction — the probe run again, its
// source read, the hash recomputed. Looking the earlier output up in Qompack's own archive through
// its MCP tools is not re-deriving it: it is the recovery the qompack arm exists to provide, and a
// stock host has no such tools to be penalised for. A retrieval query that happens to name the
// command ("go run ./cmd/probe") must not fail the constraint; running the command still must.
func TestToolUsesAfterSteps_TheArchiveIsRecoveryNotRederivation(t *testing.T) {
	ts, _, err := eval.LoadLiveTaskSet(liveTaskFile("tasks.json"))
	require.NoError(t, err)
	var task eval.LiveTask
	for _, tk := range ts.Tasks {
		if tk.ID == "tool-output-recall" {
			task = tk
		}
	}
	require.Len(t, task.Steps, 4)
	var noRerun eval.LiveCheck
	for _, c := range task.Checks {
		if c.ID == "no-rerun" {
			noRerun = c
		}
	}
	require.Equal(t, eval.CheckToolNotUsedAfter, noRerun.Kind)

	turn := func(uses ...eval.HostToolUse) eval.HostTurn {
		return eval.HostTurn{
			Requests: []eval.HostRequest{{MessageID: "m", ToolUses: uses}},
			Result:   &eval.HostResult{Subtype: "success"},
		}
	}
	recall := eval.HostToolUse{
		Name:  eval.LiveQompackToolPrefix + "recall",
		Input: json.RawMessage(`{"query":"build token printed by go run ./cmd/probe"}`),
	}
	write := eval.HostToolUse{Name: "Write", Input: json.RawMessage(`{"file_path":"TOKEN","content":"975408daf9b5\n"}`)}
	grade := func(after ...eval.HostToolUse) eval.LiveCheckResult {
		s := eval.HostStream{Turns: []eval.HostTurn{turn(), turn(), {Result: &eval.HostResult{LocalCommand: "compact"}}, turn(after...)}}
		res := eval.GradeLiveTrial(eval.LiveTask{Checks: []eval.LiveCheck{noRerun}}, t.TempDir(),
			eval.LiveEvidence{ToolUsesAfter: eval.ToolUsesAfterSteps(task, s)})
		return res[0]
	}

	got := grade(recall, write)
	require.True(t, got.Passed, "a lookup in Qompack's archive is recovery: %s", got.Detail)
	got = grade(recall, eval.HostToolUse{Name: "Bash", Input: json.RawMessage(`{"command":"go run ./cmd/probe"}`)}, write)
	require.False(t, got.Passed, "running the probe again is still re-deriving it: %s", got.Detail)
}
