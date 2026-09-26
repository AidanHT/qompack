package eval

// ── The live run's plan document ─────────────────────────────────────────────────────────────────
//
// V6 close-out C5.4. `devtool live-eval` writes one LivePlan as plan.json before its first session
// starts, and `qompack eval` reads it back beside summary.json to say what kind of run it is
// reporting: who executed it, on which model, against which frozen materials and with how many
// trials. It lives here, beside LiveSummary, so the tool that writes it and the command that reads
// it share one shape.

import (
	"sort"
	"strings"
)

// LivePreregistration is what a pre-registration froze about one task set: the exact bytes a
// confirmatory run must use and the one way the qompack arm may reach the host.
type LivePreregistration struct {
	// Document is the pre-registration, relative to the repository root.
	Document string
	// TaskSetFile is the task-set file, relative to the repository root.
	TaskSetFile string
	// TaskSetSHA256 is the task-set file's SHA-256 (the document's section 2 for qompack-live-v1,
	// amendment A7 for qompack-live-v2).
	TaskSetSHA256 string
	// FixtureTreeSHA256 is TreeManifestSHA256 over the task set's FixtureTreeDirs: the fixtures and
	// hidden tests (amendment A1 for qompack-live-v1, amendment A7 for qompack-live-v2).
	FixtureTreeSHA256 string
	// Install is how the qompack arm loads the plugin: "plugin-dir" (section 3).
	Install string
	// Model is the pre-registered model ID and ModelContingency the one host alias section 3 lets a
	// run use in its place: if the host rejects Model, the run restarts on the alias and records the
	// model the host resolves it to (amendment A6).
	Model            string
	ModelContingency string
	// RequiredFixed names, by checklist ID, the known defects section 9 requires the candidate to no
	// longer carry. Section 9 also sets any run on a candidate with a known open defect aside, so a
	// confirmatory run's plan attests no open defect at all (amendment A5).
	RequiredFixed []string
	// SupersededBy names the task set that replaced this one before any of its trials ran. A
	// superseded set stays recorded, byte for byte, but no run of it is ever the confirmatory run and
	// `devtool live-eval` refuses to plan one.
	SupersededBy string
	// SupersededWhy says why, and where the replacement was recorded.
	SupersededWhy string
}

// LivePreregistrations maps each pre-registered task set's id to what its pre-registration froze.
// `qompack eval` calls a run confirmatory only when its plan matches the entry for its task set and
// the set was not superseded; a task set absent from here (the pilot set among them) was never
// pre-registered. Each entry is pinned to its document and to the committed materials by a test.
var LivePreregistrations = map[string]LivePreregistration{
	"qompack-live-v1": {
		Document:          "plans/sdd/V6-closeout/eval/preregistration.md",
		TaskSetFile:       "testdata/eval/live/tasks.json",
		TaskSetSHA256:     "14e9ee33ccfff573c00db0a108824853e08d916c3099842c232b624ac5eafff0",
		FixtureTreeSHA256: "30cf769d243776645506eb43b7e0b336d2fed9a6e2ea29b4fb70a1f2b4054276",
		Install:           "plugin-dir",
		Model:             "claude-sonnet-5",
		ModelContingency:  "sonnet",
		RequiredFixed:     []string{"C1.12", "C1.1"},
		SupersededBy:      "qompack-live-v2",
		SupersededWhy: "superseded before any trial of it (preregistration amendment A7, owner decision D12): the untouched " +
			"fixture already passed every task check of tool-output-recall and seed-recall, and regression-guard's " +
			"constraint check pinned failed whenever JoinList was missing",
	},
	"qompack-live-v2": {
		Document:          "plans/sdd/V6-closeout/eval/preregistration.md",
		TaskSetFile:       "testdata/eval/live/tasks-v2.json",
		TaskSetSHA256:     "d59dc09d15edb04acb567dd97ac620c41eaf00206093bdc59a5162607619af83",
		FixtureTreeSHA256: "a5f57b1e2045378d1b38a629f75f830755e95e3582513ea8e3d3fb0d452ec3d2",
		Install:           "plugin-dir",
		Model:             "claude-sonnet-5",
		ModelContingency:  "sonnet",
		RequiredFixed:     []string{"C1.12", "C1.1"},
	},
}

// FixtureTreeDirs is the sorted set of top-level directories, relative to the task file, that the
// task set's fixtures and hidden fixtures live under: the tree TreeManifestSHA256 names in a run's
// plan. For qompack-live-v1 it is fixtures and hidden (amendment A1); for qompack-live-v2 it is
// fixtures, hidden and hidden-v2 (amendment A7).
func (ts LiveTaskSet) FixtureTreeDirs() []string {
	seen := map[string]bool{}
	for _, t := range ts.Tasks {
		for _, dir := range []string{t.Fixture, t.HiddenFixture} {
			if dir == "" {
				continue
			}
			top, _, _ := strings.Cut(dir, "/")
			seen[top] = true
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// RunsPreregisteredModel reports whether model is what a run of the task set with this
// pre-registration may run on: the pre-registered model, or section 3's contingency alias for it.
// preregistered is the model the task set itself declares, whose file the pre-registration froze.
func (p LivePreregistration) RunsPreregisteredModel(preregistered, model string) bool {
	if model == preregistered {
		return true
	}
	return p.ModelContingency != "" && model == p.ModelContingency && preregistered == p.Model
}

// LiveDefectAttestation is the operator's statement, made when a run was planned, of which known
// defects its plugin bundle still carries (preregistration section 9). A bundle cannot prove which
// defects it fixes, so this is an attestation: recorded as the operator's, reported as one, and
// never presented as machine-checked.
type LiveDefectAttestation struct {
	// Open lists, by checklist ID, every known defect the bundle still carries. Empty means the
	// operator attests that it carries none.
	Open []string `json:"open"`
	// Source says where the statement came from, e.g. the live-eval flag that carried it.
	Source string `json:"source"`
}

// LivePlannedTrial is one planned trial.
type LivePlannedTrial struct {
	Task  string `json:"task"`
	Arm   string `json:"arm"`
	Trial int    `json:"trial"`
}

// LivePlan is a live-evaluation run's plan document.
type LivePlan struct {
	RunID         string `json:"run_id"`
	CreatedAt     string `json:"created_at"`
	TaskSet       string `json:"task_set"`
	TaskSetFile   string `json:"task_set_file"`
	TaskSetSHA256 string `json:"task_set_sha256"`
	// TaskSetTasks is how many tasks the task set declares, held-out ones included, so a reader can
	// tell a run of the whole set from one --only or the held-out rule narrowed. Zero in a plan
	// written before it was recorded, which is read as unknown.
	TaskSetTasks int `json:"task_set_tasks,omitempty"`
	// FixtureTreeSHA256 is TreeManifestSHA256 over FixtureTreeDirs, the top-level directories
	// (beside the task file) the task set's fixtures and hidden tests live in: the identity of every
	// byte a trial starts from or is graded against, which the task-set hash alone does not cover.
	FixtureTreeSHA256  string              `json:"fixture_tree_sha256"`
	FixtureTreeDirs    []string            `json:"fixture_tree_dirs"`
	Model              string              `json:"model"`
	PreregisteredModel string              `json:"preregistered_model"`
	Arms               []string            `json:"arms"`
	TrialsPerArm       int                 `json:"trials_per_arm"`
	Install            string              `json:"install"`
	Plugin             *LivePluginIdentity `json:"plugin,omitempty"`
	// KnownDefects is the operator's statement of which known defects Plugin still carries; nil
	// when the run was planned without one (a stock-only run, or a plan written before it existed).
	KnownDefects     *LiveDefectAttestation `json:"known_defects,omitempty"`
	ClaudeCLI        string                 `json:"claude_cli"`
	ClaudeCLIVersion string                 `json:"claude_cli_version"`
	RateTableDate    string                 `json:"rate_table_date"`
	RateTableSource  string                 `json:"rate_table_source"`
	HeldOutIncluded  bool                   `json:"held_out_included"`
	Trials           []LivePlannedTrial     `json:"trials"`
	Host             string                 `json:"host"`
	// Agent says who executed the run and in what capacity, e.g. agent-executed on the real
	// installed host under owner decision D3, never human UAT.
	Agent string `json:"agent"`
}
