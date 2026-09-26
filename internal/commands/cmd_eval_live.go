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
	// Notes is what the reader of the run's artifacts found beside it, such as a newer run that has
	// a plan and no summary.
	Notes []string
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
	Qualification     string `json:"qualification"`
	Host              string `json:"host"`
	ClaudeCodeVersion string `json:"claude_code_version"`
	Model             string `json:"model"`
	PreregModel       string `json:"preregistered_model"`
	// HostModels is every model the trials' hosts reported at start-up: what Model resolved to.
	HostModels        []string                 `json:"host_models,omitempty"`
	TaskSet           string                   `json:"task_set"`
	TaskSetSHA256     string                   `json:"task_set_sha256"`
	FixtureTreeSHA256 string                   `json:"fixture_tree_sha256,omitempty"`
	Install           string                   `json:"install"`
	Plugin            *eval.LivePluginIdentity `json:"plugin,omitempty"`
	// KnownDefects is the operator's statement of which known defects the bundle still carries,
	// verbatim from the plan, and DefectPrecondition says what it establishes: preregistration
	// section 9's precondition rests on that statement, which nothing here can check.
	KnownDefects       *eval.LiveDefectAttestation `json:"known_defects,omitempty"`
	DefectPrecondition string                      `json:"defect_precondition,omitempty"`
	HeldOutIncluded    bool                        `json:"held_out_included"`
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
	Confidence          float64           `json:"confidence"`
	Margin              float64           `json:"non_inferiority_margin"`
	MarginKnown         bool              `json:"non_inferiority_margin_known"`
	Arms                []eval.ArmSummary `json:"arms"`
	TaskSuccessDiff     *eval.Difference  `json:"task_success_diff,omitempty"`
	ConstraintCleanDiff *eval.Difference  `json:"constraint_clean_diff,omitempty"`
	RecoveryDiff        *eval.Difference  `json:"recovery_diff,omitempty"`
	Decision            eval.LiveDecision `json:"decision"`
	Regression          string            `json:"constraint_regression,omitempty"`
	// Failed names every failed trial, and FailedTreatment says how the decision counted them: under
	// intention to treat (preregistration section 8) each is in every denominator, a harness failure
	// scored as a failure on every outcome and any other trial graded by its checks.
	Failed          []string                     `json:"failed,omitempty"`
	FailedTreatment string                       `json:"failed_treatment,omitempty"`
	Notes           []string                     `json:"notes,omitempty"`
	RateTableDate   string                       `json:"rate_table_date,omitempty"`
	Analysis        eval.LiveAnalysis            `json:"analysis"`
	ByVariant       map[string][]eval.ArmSummary `json:"by_variant,omitempty"`
	TaskSigns       map[string]int               `json:"task_signs,omitempty"`
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
		Model: p.Model, PreregModel: p.PreregisteredModel, HostModels: s.HostModels,
		TaskSet: p.TaskSet, TaskSetSHA256: p.TaskSetSHA256, FixtureTreeSHA256: p.FixtureTreeSHA256,
		Install: p.Install, Plugin: p.Plugin, HeldOutIncluded: p.HeldOutIncluded,
		KnownDefects: p.KnownDefects, DefectPrecondition: defectPrecondition(p),
		TrialsPerArm: p.TrialsPerArm, Confidence: a.Confidence, Margin: a.NonInferiorityMargin, MarginKnown: marginKnown,
		TaskSuccessDiff: s.TaskSuccessDiff, ConstraintCleanDiff: s.ConstraintCleanDiff, RecoveryDiff: s.RecoveryDiff,
		Decision: s.Decision, Regression: s.ConstraintRegression, Failed: s.Failed,
		Notes:         append(append([]string(nil), in.Notes...), s.Notes...),
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
	if r.Trials.Failed > 0 {
		r.FailedTreatment = liveFailedTreatment
	}
	r.NotConfirmatory = notConfirmatory(p, s, r)
	r.Confirmatory = len(r.NotConfirmatory) == 0
	if contingencyRun(p) && len(s.HostModels) > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("the run used the pre-registered contingency alias %s in place of %s, "+
			"which the hosts resolved to %s; preregistration section 3 permits it only after the host rejected %s in "+
			"the first confirmatory session and the change was appended to section 9 before the restart, and those "+
			"preconditions are not machine-checked", p.Model, p.PreregisteredModel, strings.Join(s.HostModels, ", "),
			p.PreregisteredModel))
	}
	return r
}

// liveFailedTreatment says how a live run's decision counted its failed trials. The summary's
// decision applies preregistration section 8's intention-to-treat rule itself (eval.SummarizeLive,
// eval.DecideLive), so the command reports that decision and does not override it for them.
const liveFailedTreatment = "each is in every denominator of the decision (intention to treat, preregistration " +
	"section 8): a harness failure is scored as a failure on every outcome, any other failed trial is graded by " +
	"its checks, and a trial whose plugin state contradicted its arm makes the decision not-applicable"

// notConfirmatory lists every way the run departs from a confirmatory run of its pre-registered
// design: what its plan fixed before any trial ran (eval.LivePlanDepartures — its frozen materials
// and install path, a set that was not superseded, its model, both arms, every task, the trials per
// arm, a clean bundle and the candidate's known-defect precondition), and what only its trials show —
// the model a contingency alias resolved to, every planned trial run, and no plugin but the arm's
// own on either arm (sections 3 and 7). Failed trials are not among them: under intention to treat
// they are failures the analysis counts, not a reason to set the run aside.
func notConfirmatory(p eval.LivePlan, s eval.LiveSummary, r *LiveEvalReport) []string {
	out := eval.LivePlanDepartures(p, s.Analysis.TrialsPerArm)
	out = append(out, modelResolutionDepartures(p, s)...)
	if r.Trials.Skipped > 0 {
		out = append(out, fmt.Sprintf("%d planned trial(s) did not run", r.Trials.Skipped))
	}
	for _, arm := range liveArmOrder(s.Arms) {
		as := s.Arms[arm]
		if as.ForeignPluginTrials == 0 {
			continue
		}
		names := "which ones this summary does not name"
		if len(as.ForeignPlugins) > 0 {
			names = strings.Join(as.ForeignPlugins, ", ")
		}
		out = append(out, fmt.Sprintf("%d of %d %s trial(s) loaded a plugin other than the arm's own (%s), so the "+
			"arms differed by more than Qompack (preregistration section 3)", as.ForeignPluginTrials, as.Trials, arm, names))
	}
	return out
}

// contingencyRun reports that the run used the one alias its task set's pre-registration permits in
// place of the pre-registered model (section 3's contingency).
func contingencyRun(p eval.LivePlan) bool { return eval.LivePlanOnContingency(p) }

// modelResolutionDepartures lists how a run on section 3's contingency alias departs from it once its
// trials have run: the model the host resolved the alias to must be recorded, and be one model
// (amendment A6). A resolution that was not recorded, or that differed between trials, leaves the
// run's model unknown or the arms not identical. Whether the plan's model was the pre-registered one
// or the alias at all is eval.LivePlanDepartures'.
func modelResolutionDepartures(p eval.LivePlan, s eval.LiveSummary) []string {
	switch {
	case !contingencyRun(p):
		return nil
	case len(s.HostModels) == 0:
		return []string{fmt.Sprintf("it ran on the contingency alias %s, and its summary does not record the model "+
			"the host resolved it to, which preregistration section 3 requires", p.Model)}
	case len(s.HostModels) > 1:
		return []string{fmt.Sprintf("it ran on the contingency alias %s, which the hosts resolved to more than one "+
			"model (%s), so its trials did not all run on one model", p.Model, strings.Join(s.HostModels, ", "))}
	}
	return nil
}

// defectPrecondition says what the plan establishes about section 9's known-defect precondition,
// and on whose word. A run with no plugin bundle has no candidate, and nothing to say.
func defectPrecondition(p eval.LivePlan) string {
	switch {
	case p.KnownDefects == nil && p.Plugin == nil:
		return ""
	case p.KnownDefects == nil:
		return "not attested: the plan carries no statement of the bundle's known open defects"
	case len(p.KnownDefects.Open) == 0:
		return "none open, on the operator's word when the run was planned; a bundle cannot prove which " +
			"defects it fixes, so this precondition is not machine-checked"
	default:
		return fmt.Sprintf("open: %s, on the operator's word when the run was planned",
			strings.Join(p.KnownDefects.Open, ", "))
	}
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
	if r.Trials.Failed > 0 {
		primary.Detail += fmt.Sprintf("; %d failed trial(s) counted under intention to treat", r.Trials.Failed)
	}
	switch {
	case notJudged != "":
		primary.Detail = notJudged + "; " + primary.Detail
	case !liveDecisionReached(r.Decision):
		primary.Detail = "not judged: the pre-registered rule reached no verdict; " + primary.Detail
	case r.Decision.Verdict == "inferior":
		primary.Passed = boolPtr(false)
	default:
		primary.Passed = boolPtr(true)
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

// liveDecisionReached reports that the pre-registered rule reached a verdict on the primary outcome:
// superior or non-inferior, which pass it, or inferior, which fails it. Inconclusive and
// not-applicable reach none, and neither does anything else a summary might carry.
func liveDecisionReached(d eval.LiveDecision) bool {
	switch d.Verdict {
	case "superior", "non-inferior", "inferior":
		return true
	}
	return false
}

// renderLive prints the live half of the report, the run's qualification before any of its numbers.
func renderLive(rw *errWriter, r *LiveEvalReport) {
	rw.printf("live evaluation — %s\n", r.Qualification)
	rw.printf("  run %s (created %s), read from %s\n", orUnknown(r.RunID), orUnknown(r.CreatedAt), orUnknown(r.Source))
	hostModels := "not recorded"
	if len(r.HostModels) > 0 {
		hostModels = strings.Join(r.HostModels, ", ")
	}
	rw.printf("  model %s (pre-registered %s), host reported %s, Claude Code %s on %s, plugin install %s\n",
		orUnknown(r.Model), orUnknown(r.PreregModel), hostModels, orUnknown(r.ClaudeCodeVersion), orUnknown(r.Host),
		orUnknown(r.Install))
	if r.Plugin != nil {
		rw.printf("  bundle %s at commit %s (dirty=%t)\n", orUnknown(r.Plugin.Version), orUnknown(r.Plugin.Commit),
			r.Plugin.Dirty)
	}
	if r.DefectPrecondition != "" {
		rw.printf("  known open defects (preregistration section 9): %s\n", r.DefectPrecondition)
	}
	rw.printf("  task set %s (sha256 %s, fixture tree %s)\n", orUnknown(r.TaskSet), shortHash(r.TaskSetSHA256),
		shortHash(r.FixtureTreeSHA256))
	if r.Confirmatory {
		rw.printf("  confirmatory: yes — its section 9 known-defect precondition rests on the operator's " +
			"attestation, not on a machine check\n")
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
	if len(r.Failed) > 0 {
		rw.printf("  failed trials: %d, %s\n", len(r.Failed), r.FailedTreatment)
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
