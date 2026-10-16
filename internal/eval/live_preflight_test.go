package eval_test

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// confirmatoryPlan is a plan of the whole pre-registered qompack-live-v2 design: its frozen
// materials, --plugin-dir, the pre-registered model, both arms, all ten tasks with the held-out ones,
// two trials per arm, a clean bundle and the operator's statement that it carries no open defect.
func confirmatoryPlan(t *testing.T) eval.LivePlan {
	t.Helper()
	ts, _, err := eval.LoadLiveTaskSet(liveTaskFile(liveTasksV2))
	require.NoError(t, err)
	pre := eval.LivePreregistrations[ts.ID]
	p := eval.LivePlan{
		TaskSet: ts.ID, TaskSetSHA256: pre.TaskSetSHA256, TaskSetTasks: len(ts.Tasks),
		FixtureTreeSHA256: pre.FixtureTreeSHA256, Model: ts.Analysis.Model, PreregisteredModel: ts.Analysis.Model,
		Arms: []string{eval.ArmStock, eval.ArmQompack}, TrialsPerArm: ts.Analysis.TrialsPerArm, Install: "plugin-dir",
		Plugin:          &eval.LivePluginIdentity{Install: "plugin-dir", Version: "0.3.0", Commit: "c0ffee"},
		KnownDefects:    &eval.LiveDefectAttestation{Open: []string{}},
		HeldOutIncluded: true,
	}
	for n := 1; n <= ts.Analysis.TrialsPerArm; n++ {
		for _, task := range ts.Tasks {
			for _, arm := range p.Arms {
				p.Trials = append(p.Trials, eval.LivePlannedTrial{Task: task.ID, Arm: arm, Trial: n})
			}
		}
	}
	return p
}

// TestLivePlanDepartures_TheWholeDesignHasNone: a plan of the whole pre-registered design departs
// from it in nothing that is fixed before the first trial, on the pre-registered model and on the one
// contingency alias alike (the alias's resolution is only known once its trials have run).
func TestLivePlanDepartures_TheWholeDesignHasNone(t *testing.T) {
	p := confirmatoryPlan(t)
	require.Empty(t, eval.LivePlanDepartures(p, eval.LiveAnalysis{TrialsPerArm: 2}))
	p.Model = eval.LivePreregistrations[p.TaskSet].ModelContingency
	require.Empty(t, eval.LivePlanDepartures(p, eval.LiveAnalysis{TrialsPerArm: 2}), "section 3's contingency alias is the pre-registered model")
}

// TestLivePlanDepartures_NamesEachDeparture: each way a plan can depart from the design, one at a
// time, is named — so `devtool live-eval` can say so before a session is spent, and `qompack eval`
// afterwards, in the same words.
func TestLivePlanDepartures_NamesEachDeparture(t *testing.T) {
	v1 := eval.LivePreregistrations["qompack-live-v1"]
	for name, c := range map[string]struct {
		mutate func(*eval.LivePlan)
		want   []string
	}{
		"unregistered set": {func(p *eval.LivePlan) { p.TaskSet = "qompack-live-pilot-v1" }, []string{"no pre-registration"}},
		"superseded set": {func(p *eval.LivePlan) {
			p.TaskSet, p.TaskSetSHA256, p.FixtureTreeSHA256 = "qompack-live-v1", v1.TaskSetSHA256, v1.FixtureTreeSHA256
		}, []string{"qompack-live-v1 was superseded", "amendment A7", "the confirmatory set is qompack-live-v2"}},
		"edited task set": {func(p *eval.LivePlan) { p.TaskSetSHA256 = strings.Repeat("a", 64) }, []string{"task set file hashes to aaaaaaaaaaaa…"}},
		"edited fixtures": {func(p *eval.LivePlan) { p.FixtureTreeSHA256 = "" }, []string{"fixture tree hashes to unknown"}},
		"marketplace":     {func(p *eval.LivePlan) { p.Install = "marketplace" }, []string{"not the pre-registered --plugin-dir"}},
		"no attestation":  {func(p *eval.LivePlan) { p.KnownDefects = nil }, []string{"does not attest", "C1.12 and C1.1 are fixed"}},
		"open defect": {
			func(p *eval.LivePlan) { p.KnownDefects = &eval.LiveDefectAttestation{Open: []string{"C1.15"}} },
			[]string{"known open defect(s) C1.15"},
		},
		"another model":  {func(p *eval.LivePlan) { p.Model = "opus" }, []string{"it ran on opus, not the pre-registered model claude-sonnet-5"}},
		"one arm":        {func(p *eval.LivePlan) { p.Arms = []string{eval.ArmQompack} }, []string{"did not run both arms"}},
		"no held-out":    {func(p *eval.LivePlan) { p.HeldOutIncluded = false }, []string{"excluded the held-out tasks"}},
		"narrowed":       {func(p *eval.LivePlan) { p.Trials = p.Trials[:2] }, []string{"planned 1 of the task set's 10 tasks"}},
		"unsized":        {func(p *eval.LivePlan) { p.TaskSetTasks = 0 }, []string{"does not record the task set's size"}},
		"trials per arm": {func(p *eval.LivePlan) { p.TrialsPerArm = 1 }, []string{"ran 1 trials per arm per task, not the pre-registered 2"}},
		"no bundle":      {func(p *eval.LivePlan) { p.Plugin = nil }, []string{"names no plugin bundle"}},
		"dirty bundle":   {func(p *eval.LivePlan) { p.Plugin.Dirty = true }, []string{"uncommitted changes"}},
	} {
		p := confirmatoryPlan(t)
		c.mutate(&p)
		got := strings.Join(eval.LivePlanDepartures(p, eval.LiveAnalysis{TrialsPerArm: 2}), "\n")
		for _, want := range c.want {
			require.Contains(t, got, want, name)
		}
	}
}

// TestLivePlanDepartures_PerArmTrials: a four-arm plan records each arm's trials per task, and each
// arm whose count is not the pre-registered one is named on its own; a plan's two bundles are each
// held to the clean-tree rule, and a plan with neither names no bundle.
func TestLivePlanDepartures_PerArmTrials(t *testing.T) {
	a := eval.LiveAnalysis{ArmTrialsPerTask: map[string]int{"stock": 3, "classic": 14, "generic": 10, "steer": 14}}
	plan := func() eval.LivePlan {
		return eval.LivePlan{
			Arms:       []string{"stock", "classic", "generic", "steer"},
			ArmTrials:  map[string]int{"stock": 3, "classic": 14, "generic": 10, "steer": 14},
			Plugin:     &eval.LivePluginIdentity{Commit: "c0ffee"},
			PluginMods: &eval.LivePluginIdentity{Commit: "c0ffee", Mods: true},
		}
	}
	trialsText := func(p eval.LivePlan) string {
		var out []string
		for _, d := range eval.LivePlanDepartures(p, a) {
			if strings.Contains(d, "trials per") || strings.Contains(d, "bundle") {
				out = append(out, d)
			}
		}
		return strings.Join(out, "\n")
	}
	require.Empty(t, trialsText(plan()))

	p := plan()
	p.ArmTrials["steer"], p.ArmTrials["stock"] = 13, 4
	got := trialsText(p)
	require.Contains(t, got, "it ran 13 trials per task on the steer arm, not the pre-registered 14")
	require.Contains(t, got, "it ran 4 trials per task on the stock arm, not the pre-registered 3")
	require.NotContains(t, got, "classic arm")

	p = plan()
	p.Arms = append(p.Arms, "extra")
	p.ArmTrials["extra"] = 1
	require.Contains(t, trialsText(p), "it ran 1 trials per task on the extra arm, not the pre-registered 0")

	p = plan()
	p.PluginMods.Dirty = true
	require.Contains(t, trialsText(p), "--bundle-mods was assembled from a worktree with uncommitted changes")
	p = plan()
	p.Plugin, p.PluginMods = nil, nil
	require.Contains(t, trialsText(p), "names no plugin bundle")
	p = plan()
	p.Plugin = nil
	require.Empty(t, trialsText(p), "a steer-only plan has the mods bundle alone")
}

// stageAEntry registers a test-only four-arm pre-registration of the test task set for the length of
// the test (task 1 review m-1): the Stage A entry shape — four arms with per-arm trials, two bundles,
// claude-opus-5-5 with the contingency alias opus.
func stageAEntry(t *testing.T) (eval.LiveTaskSet, eval.LivePlan) {
	t.Helper()
	ts, raw, err := eval.LoadLiveTaskSet(liveTaskFile(liveTasksV7Test))
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	tree, err := eval.TreeManifestSHA256(filepath.Dir(liveTaskFile(liveTasksV7Test)), ts.FixtureTreeDirs()...)
	require.NoError(t, err)
	eval.LivePreregistrations[ts.ID] = eval.LivePreregistration{
		Document: "test-only", TaskSetFile: "testdata/eval/live/" + liveTasksV7Test,
		TaskSetSHA256: hex.EncodeToString(sum[:]), FixtureTreeSHA256: tree, Install: "plugin-dir",
		Model: "claude-opus-5-5", ModelContingency: "opus", RequiredFixed: []string{"C1.12"},
		Arms: eval.LiveArms, ArmTrialsPerTask: ts.Analysis.ArmTrialsPerTask,
		Bundles: []string{eval.LiveBundleClassic, eval.LiveBundleMods},
	}
	t.Cleanup(func() { delete(eval.LivePreregistrations, ts.ID) })
	p := eval.LivePlan{
		TaskSet: ts.ID, TaskSetSHA256: hex.EncodeToString(sum[:]), TaskSetTasks: len(ts.Tasks),
		FixtureTreeSHA256: tree, Model: "claude-opus-5-5", PreregisteredModel: ts.Analysis.Model,
		Arms: eval.LiveArms, ArmTrials: map[string]int{}, Install: "plugin-dir",
		Plugin:          &eval.LivePluginIdentity{Install: "plugin-dir", Version: "0.4.0", Commit: "c0ffee"},
		PluginMods:      &eval.LivePluginIdentity{Install: "plugin-dir", Version: "0.4.0", Commit: "c0ffee", Mods: true},
		KnownDefects:    &eval.LiveDefectAttestation{Open: []string{}},
		HeldOutIncluded: true,
	}
	for arm, n := range ts.Analysis.ArmTrialsPerTask {
		p.ArmTrials[arm] = n
		for k := 1; k <= n; k++ {
			p.Trials = append(p.Trials, eval.LivePlannedTrial{Task: ts.Tasks[0].ID, Arm: arm, Trial: k})
		}
	}
	return ts, p
}

// TestLivePlanDepartures_StageAEntry: with a four-arm pre-registration entry, a plan of its whole
// design departs in nothing (on the model and on its alias), and each Stage A departure is named on
// its own: a missing arm, an arm's trials per task, a missing bundle, two bundles from two commits.
func TestLivePlanDepartures_StageAEntry(t *testing.T) {
	ts, p := stageAEntry(t)
	require.Empty(t, eval.LivePlanDepartures(p, ts.Analysis))
	p.Model = "opus"
	require.Empty(t, eval.LivePlanDepartures(p, ts.Analysis), "the entry's contingency alias is the pre-registered model")

	for name, c := range map[string]struct {
		mutate func(*eval.LivePlan)
		want   string
	}{
		"three arms": {
			func(p *eval.LivePlan) { p.Arms = []string{"stock", "classic", "steer"} },
			"did not run every arm of the design (stock, classic, generic, steer; arms: stock, classic, steer, missing generic)",
		},
		"steer trials":   {func(p *eval.LivePlan) { p.ArmTrials["steer"] = 3 }, "ran 3 trials per task on the steer arm, not the pre-registered 2"},
		"no mods bundle": {func(p *eval.LivePlan) { p.PluginMods = nil }, "names no --bundle-mods"},
		"no bundle":      {func(p *eval.LivePlan) { p.Plugin = nil }, "names no --bundle,"},
		"two commits":    {func(p *eval.LivePlan) { p.PluginMods.Commit = "beef" }, "not built from one commit"},
		"another model":  {func(p *eval.LivePlan) { p.Model = "sonnet" }, "not the pre-registered model claude-opus-5-5"},
	} {
		_, p := stageAEntry(t)
		c.mutate(&p)
		require.Contains(t, strings.Join(eval.LivePlanDepartures(p, ts.Analysis), "\n"), c.want, name)
	}

	// The per-arm n is the entry's: a task set whose analysis disagreed would be departing anyway (its
	// hash), and the entry's counts are what the plan is held to.
	_, p = stageAEntry(t)
	a := ts.Analysis
	a.ArmTrialsPerTask = map[string]int{"stock": 9, "classic": 9, "generic": 9, "steer": 9}
	require.Empty(t, eval.LivePlanDepartures(p, a))
}

// TestLivePlanDepartures_FourArmSetWithoutEntry: a four-arm task set with no pre-registration is held
// to the four Stage A arms, not the two-arm design's stock and qompack.
func TestLivePlanDepartures_FourArmSetWithoutEntry(t *testing.T) {
	ts, _, err := eval.LoadLiveTaskSet(liveTaskFile(liveTasksV7Test))
	require.NoError(t, err)
	p := eval.LivePlan{TaskSet: ts.ID, Arms: eval.LiveArms}
	got := strings.Join(eval.LivePlanDepartures(p, ts.Analysis), "\n")
	require.Contains(t, got, "has no pre-registration")
	require.NotContains(t, got, "both arms")
	require.NotContains(t, got, "every arm of the design")
	p.Arms = []string{"stock", "classic"}
	require.Contains(t, strings.Join(eval.LivePlanDepartures(p, ts.Analysis), "\n"), "missing generic, steer")
}
