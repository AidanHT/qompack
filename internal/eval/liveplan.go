package eval

// ── The live run's plan document ─────────────────────────────────────────────────────────────────
//
// V6 close-out C5.4. `devtool live-eval` writes one LivePlan as plan.json before its first session
// starts, and `qompack eval` reads it back beside summary.json to say what kind of run it is
// reporting: who executed it, on which model, against which frozen materials and with how many
// trials. It lives here, beside LiveSummary, so the tool that writes it and the command that reads
// it share one shape.

// LivePreregistration is what a pre-registration froze about one task set: the exact bytes a
// confirmatory run must use and the one way the qompack arm may reach the host.
type LivePreregistration struct {
	// Document is the pre-registration, relative to the repository root.
	Document string
	// TaskSetSHA256 is the task-set file's SHA-256 (the document's section 2).
	TaskSetSHA256 string
	// FixtureTreeSHA256 is TreeManifestSHA256 over the fixtures and hidden tests (amendment A1).
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
}

// LivePreregistrations maps each pre-registered task set's id to what its pre-registration froze.
// `qompack eval` calls a run confirmatory only when its plan matches the entry for its task set; a
// task set absent from here (the pilot set among them) was never pre-registered. Each entry is
// pinned to its document and to the committed materials by a test.
var LivePreregistrations = map[string]LivePreregistration{
	"qompack-live-v1": {
		Document:          "plans/sdd/V6-closeout/eval/preregistration.md",
		TaskSetSHA256:     "14e9ee33ccfff573c00db0a108824853e08d916c3099842c232b624ac5eafff0",
		FixtureTreeSHA256: "30cf769d243776645506eb43b7e0b336d2fed9a6e2ea29b4fb70a1f2b4054276",
		Install:           "plugin-dir",
		Model:             "claude-sonnet-5",
		ModelContingency:  "sonnet",
		RequiredFixed:     []string{"C1.12", "C1.1"},
	},
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
