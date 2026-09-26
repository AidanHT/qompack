package eval_test

import (
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
	require.Empty(t, eval.LivePlanDepartures(p, 2))
	p.Model = eval.LivePreregistrations[p.TaskSet].ModelContingency
	require.Empty(t, eval.LivePlanDepartures(p, 2), "section 3's contingency alias is the pre-registered model")
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
		got := strings.Join(eval.LivePlanDepartures(p, 2), "\n")
		for _, want := range c.want {
			require.Contains(t, got, want, name)
		}
	}
}
