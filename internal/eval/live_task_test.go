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

// The pre-registered task sets: qompack-live-v2 is the confirmatory set, and qompack-live-v1 the set
// it superseded before use (preregistration amendment A7), kept byte-identical as the record.
const (
	liveTasksV1 = "tasks.json"
	liveTasksV2 = "tasks-v2.json"
)

// TestLiveTaskSet_TheFrozenSetIsValidAndShapedAsPreregistered pins each declared set against the
// pre-registration: ten tasks, three held out, two changing-requirement variants, every task with
// a forced compaction between work and graded work, and the analysis parameters it names.
func TestLiveTaskSet_TheFrozenSetIsValidAndShapedAsPreregistered(t *testing.T) {
	for file, id := range map[string]string{liveTasksV1: "qompack-live-v1", liveTasksV2: "qompack-live-v2"} {
		ts, raw, err := eval.LoadLiveTaskSet(liveTaskFile(file))
		require.NoError(t, err)
		require.NotEmpty(t, raw)
		require.Equal(t, id, ts.ID)
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
}

// TestLiveTaskSetV2_DiffersFromV1OnlyAsAmendmentA7States: amendment A7 says qompack-live-v2 is
// qompack-live-v1 with exactly three tasks' grading changed — tool-output-recall and seed-recall each
// gain a hidden test and the task check "behaviour" that runs it, and regression-guard's constraint
// check "pinned" runs the pinned test from its own package — and the id and description. Everything
// a session sees and every other check is the same, so the change cannot have moved anything a
// trial is asked to do.
func TestLiveTaskSetV2_DiffersFromV1OnlyAsAmendmentA7States(t *testing.T) {
	v1, _, err := eval.LoadLiveTaskSet(liveTaskFile(liveTasksV1))
	require.NoError(t, err)
	v2, _, err := eval.LoadLiveTaskSet(liveTaskFile(liveTasksV2))
	require.NoError(t, err)

	require.Equal(t, v1.Version, v2.Version)
	require.Equal(t, v1.Defaults, v2.Defaults)
	require.Equal(t, v1.Analysis, v2.Analysis)
	require.Contains(t, v2.Description, "supersedes qompack-live-v1")
	require.Len(t, v2.Tasks, len(v1.Tasks))

	behaviour := eval.LiveCheck{
		ID: "behaviour", Outcome: eval.OutcomeTask, Kind: eval.CheckCommand, Argv: []string{"go", "test", "./..."},
	}
	for i, old := range v1.Tasks {
		cur := v2.Tasks[i]
		require.Equal(t, old.ID, cur.ID, "the tasks keep their order")
		want := old
		switch old.ID {
		case "tool-output-recall", "seed-recall":
			require.Empty(t, old.HiddenFixture)
			want.HiddenFixture = "hidden-v2/" + old.ID
			// The new check sits just before the module-builds check, which stays.
			last := old.Checks[len(old.Checks)-1]
			require.Equal(t, eval.CheckCommand, last.Kind)
			require.Equal(t, []string{"go", "vet", "./..."}, last.Argv)
			nb := behaviour
			nb.Description = cur.Checks[len(cur.Checks)-2].Description
			want.Checks = append(append(append([]eval.LiveCheck{}, old.Checks[:len(old.Checks)-1]...), nb), last)
		case "regression-guard":
			require.Equal(t, "hidden/regression-guard", old.HiddenFixture)
			want.HiddenFixture = "hidden-v2/regression-guard"
			want.Checks = append([]eval.LiveCheck{}, old.Checks...)
			require.Equal(t, "pinned", want.Checks[0].ID)
			require.Equal(t, []string{"go", "test", "./list", "-run", "^TestHiddenEvalParseListPinned$"}, want.Checks[0].Argv)
			want.Checks[0].Argv = []string{"go", "test", "./pinned", "-run", "^TestHiddenEvalParseListPinned$"}
			want.Checks[0].Description = old.Checks[0].Description + " (compiled apart from the JoinList test)"
		}
		require.Equal(t, want, cur, "task %s differs from qompack-live-v1 beyond amendment A7", old.ID)
	}
}

// TestLiveTaskSet_FixtureTreeDirs: the fixture tree a run's plan names is every top-level directory
// the set's fixtures and hidden fixtures live under — for qompack-live-v1 the tree amendment A1
// hashes, for qompack-live-v2 the tree amendment A7 hashes.
func TestLiveTaskSet_FixtureTreeDirs(t *testing.T) {
	for file, want := range map[string][]string{
		liveTasksV1:  {"fixtures", "hidden"},
		liveTasksV2:  {"fixtures", "hidden", "hidden-v2"},
		"pilot.json": {"fixtures", "hidden"},
	} {
		ts, _, err := eval.LoadLiveTaskSet(liveTaskFile(file))
		require.NoError(t, err)
		require.Equal(t, want, ts.FixtureTreeDirs(), file)
	}
}

// preregistrationFile is the frozen pre-registration of the live study: task set qompack-live-v1,
// superseded before use by qompack-live-v2 in its amendment A7.
var preregistrationFile = filepath.Join("..", "..", "plans", "sdd", "V6-closeout", "eval", "preregistration.md")

// preregistrationDoc returns the pre-registration's text with CRLF normalized. The document is
// maintainer-only: it stays on the maintainer's disk and is not published, so a public checkout
// skips the two document tests. Any other read error still fails, and
// TestLivePreregistrations_MatchTheCommittedMaterials keeps the code-against-materials pins running
// without it.
func preregistrationDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(preregistrationFile)
	if os.IsNotExist(err) {
		t.Skip("platform: plans/sdd/V6-closeout/eval/preregistration.md is maintainer-only and absent from this checkout")
	}
	require.NoError(t, err)
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

// preregisteredMaterialSHA256 is the SHA-256 the pre-registration's section 2 table (and, for
// tasks-v2.json, its amendment A7 table) records for each frozen material, copied out of the
// document so the pins hold in a checkout that does not carry it. Where the document is present,
// TestLiveTaskSet_FrozenMaterialsMatchThePreregistration checks the same files against the
// document itself, so a drift between these copies and the document fails one test or the other.
var preregisteredMaterialSHA256 = map[string]string{
	liveTasksV1:  "14e9ee33ccfff573c00db0a108824853e08d916c3099842c232b624ac5eafff0",
	"rates.json": "97dfb469e316ca27a05957a380be64f689fb8da664a0c1d566fc462f439b0982",
	"pilot.json": "4da30ddf165ad260b2a386872c25e347eb2ebf7e3b39b02a832e1a301b55b1a6",
	liveTasksV2:  "d59dc09d15edb04acb567dd97ac620c41eaf00206093bdc59a5162607619af83",
}

// TestLivePreregistrations_MatchTheCommittedMaterials needs no document. Every frozen material
// still hashes to its pre-registered SHA-256, and the code's record of each pre-registration (the
// task-set file and hash, the fixture tree's manifest hash, the install path, the model rule, the
// defects the candidate must not carry and the supersession) matches the committed materials. It is
// the record `qompack eval` checks a run's plan against before calling it confirmatory.
func TestLivePreregistrations_MatchTheCommittedMaterials(t *testing.T) {
	for file, want := range preregisteredMaterialSHA256 {
		content, err := os.ReadFile(liveTaskFile(file))
		require.NoError(t, err)
		sum := sha256.Sum256(content)
		require.Equal(t, want, hex.EncodeToString(sum[:]), "%s is not the pre-registered file", file)
	}

	require.Len(t, eval.LivePreregistrations, 2, "every pre-registered set is checked here")
	for id, pre := range eval.LivePreregistrations {
		require.True(t, strings.HasPrefix(pre.TaskSetFile, "testdata/eval/live/"), id)
		ts, taskBytes, err := eval.LoadLiveTaskSet(filepath.Join("..", "..", filepath.FromSlash(pre.TaskSetFile)))
		require.NoError(t, err, id)
		require.Equal(t, id, ts.ID, "the recorded file is the task set %s", id)

		sum := sha256.Sum256(taskBytes)
		require.Equal(t, hex.EncodeToString(sum[:]), pre.TaskSetSHA256,
			"%s: the recorded task-set hash is the committed file's", id)
		require.Equal(t, preregisteredMaterialSHA256[filepath.Base(pre.TaskSetFile)], pre.TaskSetSHA256,
			"%s: the recorded task-set hash is the pre-registered one", id)

		tree, err := eval.TreeManifestSHA256(filepath.Dir(liveTaskFile(liveTasksV1)), ts.FixtureTreeDirs()...)
		require.NoError(t, err)
		require.Equal(t, tree, pre.FixtureTreeSHA256, "%s: the recorded fixture tree is the committed tree", id)

		require.Equal(t, "plugin-dir", pre.Install)
		require.Equal(t, ts.Analysis.Model, pre.Model, "the pre-registered model is the task set's")
		require.True(t, pre.RunsPreregisteredModel(ts.Analysis.Model, ts.Analysis.Model))
		require.True(t, pre.RunsPreregisteredModel(ts.Analysis.Model, pre.ModelContingency))
		require.False(t, pre.RunsPreregisteredModel(ts.Analysis.Model, "opus"))
		require.False(t, pre.RunsPreregisteredModel("claude-haiku-4-5", pre.ModelContingency),
			"the alias stands in only for the model the pre-registration froze")
		require.Equal(t, []string{"C1.12", "C1.1"}, pre.RequiredFixed)
		require.Equal(t, "plans/sdd/V6-closeout/eval/preregistration.md", pre.Document)
	}

	v1, v2 := eval.LivePreregistrations["qompack-live-v1"], eval.LivePreregistrations["qompack-live-v2"]
	require.Equal(t, "qompack-live-v2", v1.SupersededBy)
	require.Empty(t, v2.SupersededBy)
}

// TestLiveTaskSet_FrozenMaterialsMatchThePreregistration: every frozen material still hashes to the
// SHA-256 the pre-registration's section 2 table records — read out of the document itself, so the
// test and the document cannot drift apart — the fixture and hidden-test tree hashes to the
// locale-independent manifest value its amendment A1 records (the value `devtool live-eval` writes
// into every run's plan.json), and the section 4 task table names exactly the tasks, variants and
// held-out flags of tasks.json. Amendment A7's table records qompack-live-v2's task-set file and its
// fixture tree (fixtures, hidden, hidden-v2) the same way, and the section 4 table still names its
// tasks, which A7 left unchanged; qompack-live-v1's materials, superseded before use, still match.
func TestLiveTaskSet_FrozenMaterialsMatchThePreregistration(t *testing.T) {
	doc := preregistrationDoc(t)
	for _, file := range []string{liveTasksV1, "rates.json", "pilot.json", liveTasksV2} {
		row := regexp.MustCompile("(?m)^\\|[^|\\n]*\\|\\s*`testdata/eval/live/" + regexp.QuoteMeta(file) +
			"`\\s*\\|\\s*`([0-9a-f]{64})`\\s*\\|")
		m := row.FindStringSubmatch(doc)
		require.NotNil(t, m, "the preregistration records a SHA-256 for %s", file)
		content, err := os.ReadFile(liveTaskFile(file))
		require.NoError(t, err)
		sum := sha256.Sum256(content)
		require.Equal(t, m[1], hex.EncodeToString(sum[:]), "%s is not the pre-registered file", file)
	}
	a7 := strings.Index(doc, "**A7 — ")
	require.Positive(t, a7, "amendment A7 is recorded")
	v2Row := regexp.MustCompile("(?m)^\\|[^|\\n]*\\|\\s*`testdata/eval/live/" + regexp.QuoteMeta(liveTasksV2) + "`")
	require.Greater(t, v2Row.FindStringIndex(doc)[0], a7, "qompack-live-v2's hash is recorded in amendment A7")

	tree, err := eval.TreeManifestSHA256(filepath.Dir(liveTaskFile(liveTasksV1)), "fixtures", "hidden")
	require.NoError(t, err)
	require.Regexp(t, "(?s)### Amendments.*A1.*`"+tree+"`", doc,
		"amendment A1 records the fixture tree's manifest hash")
	tree2, err := eval.TreeManifestSHA256(filepath.Dir(liveTaskFile(liveTasksV2)), "fixtures", "hidden", "hidden-v2")
	require.NoError(t, err)
	treeRow := regexp.MustCompile("(?m)^\\|[^|\\n]*\\|\\s*`testdata/eval/live/\\{fixtures,hidden,hidden-v2\\}/`\\s*\\|\\s*`([0-9a-f]{64})`\\s*\\|")
	m := treeRow.FindStringSubmatchIndex(doc)
	require.NotNil(t, m, "amendment A7 records qompack-live-v2's fixture tree")
	require.Greater(t, m[0], a7)
	require.Equal(t, tree2, doc[m[2]:m[3]], "the fixture tree is not the one amendment A7 froze")

	for _, file := range []string{liveTasksV1, liveTasksV2} {
		ts, _, err := eval.LoadLiveTaskSet(liveTaskFile(file))
		require.NoError(t, err)
		taskRow := regexp.MustCompile("(?m)^\\| `([A-Za-z0-9._-]+)` \\| [^|]+ \\| ([a-z-]+) \\| (\\*\\*yes\\*\\*|no) \\|$")
		var got, want []string
		for _, m := range taskRow.FindAllStringSubmatch(doc, -1) {
			got = append(got, fmt.Sprintf("%s %s held=%t", m[1], m[2], m[3] != "no"))
		}
		for _, task := range ts.Tasks {
			want = append(want, fmt.Sprintf("%s %s held=%t", task.ID, task.Variant, task.HeldOut))
		}
		require.Equal(t, want, got, "the section 4 table is the task set %s, in order", ts.ID)
		require.Contains(t, doc, "`"+ts.Analysis.Model+"`", "section 3 names the pre-registered model")
		require.Contains(t, doc, fmt.Sprintf("margin %.2f", ts.Analysis.NonInferiorityMargin),
			"section 8 names the task set's margin")
		require.Contains(t, doc, fmt.Sprintf("%d%% Newcombe", int(ts.Analysis.Confidence*100)),
			"section 8 names the task set's confidence level")
	}
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
	doc := preregistrationDoc(t)
	flat := strings.Join(strings.Fields(doc), " ")

	// The amendment that froze each set's fixture tree: A1 for qompack-live-v1, A7 for qompack-live-v2.
	treeAmendment := map[string]string{"qompack-live-v1": "A1", "qompack-live-v2": "A7"}
	require.Len(t, eval.LivePreregistrations, len(treeAmendment), "every pre-registered set is checked here")
	for id, pre := range eval.LivePreregistrations {
		require.True(t, strings.HasPrefix(pre.TaskSetFile, "testdata/eval/live/"), id)
		ts, taskBytes, err := eval.LoadLiveTaskSet(filepath.Join("..", "..", filepath.FromSlash(pre.TaskSetFile)))
		require.NoError(t, err, id)
		require.Equal(t, id, ts.ID, "the recorded file is the task set %s", id)

		sum := sha256.Sum256(taskBytes)
		require.Equal(t, hex.EncodeToString(sum[:]), pre.TaskSetSHA256, "%s: the recorded task-set hash is the committed file's", id)
		row := regexp.MustCompile(`(?m)^\|[^|\n]*\|\s*` + "`" + regexp.QuoteMeta(pre.TaskSetFile) + "`" + `\s*\|\s*` + "`" +
			`([0-9a-f]{64})` + "`" + `\s*\|`)
		m := row.FindStringSubmatch(doc)
		require.NotNil(t, m, id)
		require.Equal(t, m[1], pre.TaskSetSHA256, "%s: the recorded task-set hash is the document's", id)

		tree, err := eval.TreeManifestSHA256(filepath.Dir(liveTaskFile(liveTasksV1)), ts.FixtureTreeDirs()...)
		require.NoError(t, err)
		require.Equal(t, tree, pre.FixtureTreeSHA256, "%s: the recorded fixture tree is the committed tree", id)
		require.Regexp(t, "(?s)### Amendments.*"+treeAmendment[id]+".*`"+pre.FixtureTreeSHA256+"`", doc,
			"%s: and amendment %s's value", id, treeAmendment[id])

		require.Equal(t, "plugin-dir", pre.Install)
		require.Contains(t, doc, "loaded from one frozen bundle with `--plugin-dir`", "section 3 names the install path")

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
	}

	// qompack-live-v2 supersedes qompack-live-v1 before use, as amendment A7 records, with the
	// section 9 command naming the new file; v2 itself is current.
	v1, v2 := eval.LivePreregistrations["qompack-live-v1"], eval.LivePreregistrations["qompack-live-v2"]
	require.Equal(t, "qompack-live-v2", v1.SupersededBy)
	require.Contains(t, v1.SupersededWhy, "amendment A7")
	require.Contains(t, v1.SupersededWhy, "D12")
	require.Empty(t, v2.SupersededBy)
	require.Regexp(t, "(?s)\\*\\*A7 — .*`qompack-live-v1` is \\*\\*superseded before use\\*\\*", doc)
	require.Regexp(t, "(?s)\\*\\*A7 — .*--tasks "+regexp.QuoteMeta(v2.TaskSetFile)+" .*--known-open-defects none", doc,
		"amendment A7 gives section 9's command for the new set")

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
	ts, _, err := eval.LoadLiveTaskSet(liveTaskFile(liveTasksV2))
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
