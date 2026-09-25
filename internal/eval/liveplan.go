package eval

// ── The live run's plan document ─────────────────────────────────────────────────────────────────
//
// V6 close-out C5.4. `devtool live-eval` writes one LivePlan as plan.json before its first session
// starts, and `qompack eval` reads it back beside summary.json to say what kind of run it is
// reporting: who executed it, on which model, against which frozen materials and with how many
// trials. It lives here, beside LiveSummary, so the tool that writes it and the command that reads
// it share one shape.

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
	ClaudeCLI          string              `json:"claude_cli"`
	ClaudeCLIVersion   string              `json:"claude_cli_version"`
	RateTableDate      string              `json:"rate_table_date"`
	RateTableSource    string              `json:"rate_table_source"`
	HeldOutIncluded    bool                `json:"held_out_included"`
	Trials             []LivePlannedTrial  `json:"trials"`
	Host               string              `json:"host"`
	// Agent says who executed the run and in what capacity, e.g. agent-executed on the real
	// installed host under owner decision D3, never human UAT.
	Agent string `json:"agent"`
}
