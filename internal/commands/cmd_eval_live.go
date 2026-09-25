package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/eval"
)

// LiveEvalInput is one real-host evaluation run, as `devtool live-eval` left it: the plan written
// before its first session and the summary written after its last.
type LiveEvalInput struct {
	// Source is the run directory the two documents were read from.
	Source  string
	Plan    eval.LivePlan
	Summary eval.LiveSummary
}

// LiveEvalReport is the live half of the eval report: the run's outcomes under its pre-registered
// rule, and everything a reader needs to weigh them — who executed the run and how, on which
// model and materials, with how many trials, and whether it was the pre-registered design at all.
type LiveEvalReport struct {
	Source    string `json:"source"`
	RunID     string `json:"run_id"`
	CreatedAt string `json:"created_at"`
	// Qualification is the run's own statement of how it was executed (agent-executed on the real
	// installed host, never human UAT), verbatim from its plan.
	Qualification     string                   `json:"qualification"`
	Host              string                   `json:"host"`
	ClaudeCodeVersion string                   `json:"claude_code_version"`
	Model             string                   `json:"model"`
	PreregModel       string                   `json:"preregistered_model"`
	TaskSet           string                   `json:"task_set"`
	TaskSetSHA256     string                   `json:"task_set_sha256"`
	FixtureTreeSHA256 string                   `json:"fixture_tree_sha256,omitempty"`
	Install           string                   `json:"install"`
	Plugin            *eval.LivePluginIdentity `json:"plugin,omitempty"`
	HeldOutIncluded   bool                     `json:"held_out_included"`
	// Confirmatory reports that the run was the whole pre-registered design; NotConfirmatory says
	// why not. Only a confirmatory run's outcomes are judged.
	Confirmatory    bool     `json:"confirmatory"`
	NotConfirmatory []string `json:"not_confirmatory,omitempty"`
	// Tasks is how many distinct tasks the run planned; TrialsPerArm the trials per task per arm.
	Tasks        int         `json:"tasks"`
	TrialsPerArm int         `json:"trials_per_arm"`
	Trials       TrialCounts `json:"trials"`
	// Confidence and Margin are the pre-registered interval level and non-inferiority margin.
	// MarginKnown is false for a summary written before it carried its analysis.
	Confidence          float64                      `json:"confidence"`
	Margin              float64                      `json:"non_inferiority_margin"`
	MarginKnown         bool                         `json:"non_inferiority_margin_known"`
	Arms                []eval.ArmSummary            `json:"arms"`
	TaskSuccessDiff     *eval.Difference             `json:"task_success_diff,omitempty"`
	ConstraintCleanDiff *eval.Difference             `json:"constraint_clean_diff,omitempty"`
	RecoveryDiff        *eval.Difference             `json:"recovery_diff,omitempty"`
	Decision            eval.LiveDecision            `json:"decision"`
	Regression          string                       `json:"constraint_regression,omitempty"`
	Failed              []string                     `json:"failed,omitempty"`
	Notes               []string                     `json:"notes,omitempty"`
	RateTableDate       string                       `json:"rate_table_date,omitempty"`
	Analysis            eval.LiveAnalysis            `json:"analysis"`
	ByVariant           map[string][]eval.ArmSummary `json:"by_variant,omitempty"`
	TaskSigns           map[string]int               `json:"task_signs,omitempty"`
}

// buildLiveReport qualifies one run and carries its summary over.
func buildLiveReport(in LiveEvalInput) *LiveEvalReport {
	p, s := in.Plan, in.Summary
	a := s.Analysis
	marginKnown := a.Confidence > 0
	if a.Confidence == 0 {
		// A summary written before it carried its analysis: the interval level is still stated.
		a.Confidence = s.Confidence
	}
	r := &LiveEvalReport{
		Source: in.Source, RunID: p.RunID, CreatedAt: p.CreatedAt,
		Qualification: p.Agent, Host: p.Host, ClaudeCodeVersion: p.ClaudeCLIVersion,
		Model: p.Model, PreregModel: p.PreregisteredModel,
		TaskSet: p.TaskSet, TaskSetSHA256: p.TaskSetSHA256, FixtureTreeSHA256: p.FixtureTreeSHA256,
		Install: p.Install, Plugin: p.Plugin, HeldOutIncluded: p.HeldOutIncluded,
		TrialsPerArm: p.TrialsPerArm, Confidence: a.Confidence, Margin: a.NonInferiorityMargin, MarginKnown: marginKnown,
		TaskSuccessDiff: s.TaskSuccessDiff, ConstraintCleanDiff: s.ConstraintCleanDiff, RecoveryDiff: s.RecoveryDiff,
		Decision: s.Decision, Regression: s.ConstraintRegression, Failed: s.Failed, Notes: s.Notes,
		RateTableDate: p.RateTableDate, Analysis: a, ByVariant: s.ByVariant, TaskSigns: s.TaskSigns,
	}
	if r.Qualification == "" {
		r.Qualification = "how this run was executed is not recorded in its plan"
	}
	tasks := map[string]bool{}
	for _, t := range p.Trials {
		tasks[t.Task] = true
	}
	r.Tasks = len(tasks)
	for _, arm := range liveArmOrder(s.Arms) {
		as := s.Arms[arm]
		r.Arms = append(r.Arms, as)
		r.Trials.Ran += as.Trials
	}
	r.Trials.Planned = len(p.Trials)
	if r.Trials.Planned > r.Trials.Ran {
		r.Trials.Skipped = r.Trials.Planned - r.Trials.Ran
	}
	r.Trials.Failed = len(s.Failed)
	r.NotConfirmatory = notConfirmatory(p, s, r)
	r.Confirmatory = len(r.NotConfirmatory) == 0
	return r
}

// notConfirmatory lists every way the run departs from a confirmatory run of its pre-registered
// design (preregistration sections 3, 7 and 9). Failed trials are not among them: under intention
// to treat they are failures the analysis counts, not a reason to set the run aside.
func notConfirmatory(p eval.LivePlan, s eval.LiveSummary, r *LiveEvalReport) []string {
	var out []string
	out = append(out, notPreregisteredMaterials(p)...)
	if p.PreregisteredModel == "" || p.Model != p.PreregisteredModel {
		out = append(out, fmt.Sprintf("it ran on %s, not the pre-registered model %s",
			orUnknown(p.Model), orUnknown(p.PreregisteredModel)))
	}
	if !(containsString(p.Arms, eval.ArmStock) && containsString(p.Arms, eval.ArmQompack)) {
		out = append(out, fmt.Sprintf("it did not run both arms (arms: %s)", orUnknown(strings.Join(p.Arms, ", "))))
	}
	if !p.HeldOutIncluded {
		out = append(out, "it excluded the held-out tasks")
	}
	switch {
	case p.TaskSetTasks == 0:
		out = append(out, "its plan does not record the task set's size, so a narrowed run cannot be ruled out")
	case r.Tasks != p.TaskSetTasks:
		out = append(out, fmt.Sprintf("it planned %d of the task set's %d tasks", r.Tasks, p.TaskSetTasks))
	}
	if s.Analysis.TrialsPerArm > 0 && p.TrialsPerArm != s.Analysis.TrialsPerArm {
		out = append(out, fmt.Sprintf("it ran %d trials per arm per task, not the pre-registered %d",
			p.TrialsPerArm, s.Analysis.TrialsPerArm))
	}
	if r.Trials.Skipped > 0 {
		out = append(out, fmt.Sprintf("%d planned trial(s) did not run", r.Trials.Skipped))
	}
	switch {
	case p.Plugin == nil:
		out = append(out, "its plan names no plugin bundle")
	case p.Plugin.Dirty:
		out = append(out, "its bundle was assembled from a worktree with uncommitted changes, not a frozen candidate")
	}
	return out
}

// notPreregisteredMaterials lists how the run's plan departs from what its task set's
// pre-registration froze: the task-set file, the fixture and hidden-test tree, and the install path
// of the qompack arm. A task set with no pre-registration is never the pre-registered study.
func notPreregisteredMaterials(p eval.LivePlan) []string {
	pre, ok := eval.LivePreregistrations[p.TaskSet]
	if !ok {
		return []string{fmt.Sprintf("its task set %s has no pre-registration", orUnknown(p.TaskSet))}
	}
	var out []string
	if p.TaskSetSHA256 != pre.TaskSetSHA256 {
		out = append(out, fmt.Sprintf("its task set file hashes to %s, not the pre-registered %s (%s)",
			shortHash(p.TaskSetSHA256), shortHash(pre.TaskSetSHA256), pre.Document))
	}
	if p.FixtureTreeSHA256 != pre.FixtureTreeSHA256 {
		out = append(out, fmt.Sprintf("its fixture tree hashes to %s, not the pre-registered %s (%s)",
			shortHash(p.FixtureTreeSHA256), shortHash(pre.FixtureTreeSHA256), pre.Document))
	}
	if p.Install != pre.Install {
		out = append(out, fmt.Sprintf("its qompack arm was installed by %s, not the pre-registered --%s",
			orUnknown(p.Install), pre.Install))
	}
	return out
}

// liveGates are the live run's gates. Only a confirmatory run's are judged, and then only as the
// pre-registered rule decides: the primary outcome by its decision, the constraint outcome only as
// a regression, and recovery never.
func liveGates(r *LiveEvalReport) (task, recovery []EvalGate) {
	notJudged := ""
	if !r.Confirmatory {
		notJudged = "not judged: not a confirmatory run (" + strings.Join(r.NotConfirmatory, "; ") + ")"
	}

	margin := "margin not recorded in this summary"
	if r.MarginKnown {
		margin = fmt.Sprintf("non-inferiority margin %.2f", r.Margin)
	}
	primary := EvalGate{
		ID:     "LIVE-T01",
		Metric: "task success after compaction, qompack − stock (" + margin + ")",
		Value:  diffValue(r.TaskSuccessDiff),
		Detail: fmt.Sprintf("%s; decision %s — %s", diffText(r.TaskSuccessDiff, r.Confidence), r.Decision.Verdict,
			r.Decision.Reason),
	}
	switch {
	case notJudged != "":
		primary.Detail = notJudged + "; " + primary.Detail
	case r.Decision.Verdict == "superior" || r.Decision.Verdict == "non-inferior":
		primary.Passed = boolPtr(true)
	case r.Decision.Verdict == "inferior":
		primary.Passed = boolPtr(false)
	default:
		primary.Detail = "not judged: the pre-registered rule reached no verdict; " + primary.Detail
	}

	constraint := EvalGate{
		ID:     "LIVE-T02",
		Metric: "constraint-clean trials, qompack − stock (regression rule)",
		Value:  diffValue(r.ConstraintCleanDiff),
		Detail: diffText(r.ConstraintCleanDiff, r.Confidence) + "; reported, and failed only as a regression " +
			"(upper bound below −margin)",
	}
	switch {
	case notJudged != "":
		constraint.Detail = notJudged + "; " + constraint.Detail
	case r.Regression != "":
		constraint.Passed = boolPtr(false)
		constraint.Detail = "REGRESSION: " + r.Regression
	}

	rec := EvalGate{
		ID:     "LIVE-R01",
		Metric: "pre-compaction facts recovered, qompack − stock",
		Value:  diffValue(r.RecoveryDiff),
		Detail: diffText(r.RecoveryDiff, r.Confidence) + "; reported with its interval, never judged " +
			"(preregistration section 8)",
	}
	return []EvalGate{primary, constraint}, []EvalGate{rec}
}

// renderLive prints the live half of the report, the run's qualification before any of its numbers.
func renderLive(rw *errWriter, r *LiveEvalReport) {
	rw.printf("live evaluation — %s\n", r.Qualification)
	rw.printf("  run %s (created %s), read from %s\n", orUnknown(r.RunID), orUnknown(r.CreatedAt), orUnknown(r.Source))
	rw.printf("  model %s (pre-registered %s), Claude Code %s on %s, plugin install %s\n",
		orUnknown(r.Model), orUnknown(r.PreregModel), orUnknown(r.ClaudeCodeVersion), orUnknown(r.Host),
		orUnknown(r.Install))
	if r.Plugin != nil {
		rw.printf("  bundle %s at commit %s (dirty=%t)\n", orUnknown(r.Plugin.Version), orUnknown(r.Plugin.Commit),
			r.Plugin.Dirty)
	}
	rw.printf("  task set %s (sha256 %s, fixture tree %s)\n", orUnknown(r.TaskSet), shortHash(r.TaskSetSHA256),
		shortHash(r.FixtureTreeSHA256))
	if r.Confirmatory {
		rw.printf("  confirmatory: yes\n")
	} else {
		rw.printf("  confirmatory: no — %s\n", strings.Join(r.NotConfirmatory, "; "))
	}
	rw.printf("  sample: %d task(s) × %d trial(s) per arm; %d planned, %d ran, %d skipped, %d failed\n",
		r.Tasks, r.TrialsPerArm, r.Trials.Planned, r.Trials.Ran, r.Trials.Skipped, r.Trials.Failed)
	level := int(r.Confidence*100 + 0.5)
	rw.printf("  %-8s %6s  %-28s %-28s %-28s\n", "arm", "trials",
		fmt.Sprintf("task success (%d%% CI)", level), fmt.Sprintf("constraint-clean (%d%% CI)", level),
		fmt.Sprintf("recovery (%d%% CI)", level))
	for _, as := range r.Arms {
		rw.printf("  %-8s %6d  %-28s %-28s %-28s\n", as.Arm, as.Trials, proportionText(as.TaskSuccess),
			proportionText(as.ConstraintClean), proportionText(as.Recovery))
	}
	rw.printf("  decision (pre-registered rule): %s — %s\n", orUnknown(r.Decision.Verdict), orUnknown(r.Decision.Reason))
	if r.Regression != "" {
		rw.printf("  REGRESSION (H2): %s\n", r.Regression)
	}
	rw.printf("  cost — list-price-equivalent ESTIMATE from the %s rate table, beside the host's own "+
		"total_cost_usd; the sessions ran on a subscription, which has no per-token charge\n", orUnknown(r.RateTableDate))
	for _, as := range r.Arms {
		rw.printf("    %-8s mean estimate %d micros (%d of %d trial(s) a lower bound), mean host-reported %.4f USD, "+
			"mean wall %d ms\n", as.Arm, as.MeanEstimateMc, as.EstimateIncomplete, as.Trials, as.MeanHostCost, as.MeanWallMS)
	}
	for _, f := range r.Failed {
		rw.printf("  failed trial: %s\n", f)
	}
	for _, n := range r.Notes {
		rw.printf("  note: %s\n", n)
	}
	rw.printf("\n")
}

// liveArmOrder is the arms in a fixed order: qompack, stock, then any other by name.
func liveArmOrder(arms map[string]eval.ArmSummary) []string {
	var out []string
	for _, a := range []string{eval.ArmQompack, eval.ArmStock} {
		if _, ok := arms[a]; ok {
			out = append(out, a)
		}
	}
	var rest []string
	for a := range arms {
		if a != eval.ArmQompack && a != eval.ArmStock {
			rest = append(rest, a)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func diffValue(d *eval.Difference) *float64 {
	if d == nil {
		return nil
	}
	v := d.Estimate
	return &v
}

func diffText(d *eval.Difference, confidence float64) string {
	if d == nil {
		return "no difference: both arms are needed"
	}
	return fmt.Sprintf("%.3f, %d%% interval [%.3f, %.3f]", d.Estimate, int(confidence*100+0.5), d.Low, d.High)
}

func proportionText(p eval.Proportion) string {
	if p.N == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d/%d = %.2f [%.2f, %.2f]", p.K, p.N, p.Rate, p.Low, p.High)
}

func boolPtr(b bool) *bool { return &b }

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
