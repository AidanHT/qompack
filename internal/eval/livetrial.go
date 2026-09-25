package eval

// ── Live trial records and their summary ─────────────────────────────────────────────────────────
//
// V6 close-out C5.4/C5.5. One LiveTrial is one real headless session of one task under one arm. The
// summary reports the pre-registered primary outcomes per arm with confidence intervals, lists every
// failed or incomplete trial by name, and keeps usage and cost beside the outcomes, never inside
// them: a cheap wrong answer must not look like a result.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// LiveTrialSchema is the trial record format version.
const LiveTrialSchema = 1

// The two arms.
const (
	ArmStock   = "stock"
	ArmQompack = "qompack"
)

// LiveTrial is one trial's record.
type LiveTrial struct {
	Schema   int    `json:"schema"`
	RunID    string `json:"run_id"`
	TaskID   string `json:"task_id"`
	Arm      string `json:"arm"`
	Trial    int    `json:"trial"`
	Category string `json:"category"`
	Variant  string `json:"variant"`
	HeldOut  bool   `json:"held_out"`
	Model    string `json:"model"`
	// Install is how the plugin reached the host: "none", "plugin-dir" or "marketplace".
	Install           string `json:"install"`
	ClaudeCodeVersion string `json:"claude_code_version"`
	SessionID         string `json:"session_id"`
	StartedAt         string `json:"started_at"`
	WallMS            int64  `json:"wall_ms"`
	ExitCode          int    `json:"exit_code"`
	// HarnessError is a failure of the driver or the process, not of the model: a timeout, a
	// process that would not start, a stream that would not parse.
	HarnessError string           `json:"harness_error,omitempty"`
	Steps        []LiveStepRecord `json:"steps"`
	// Completed reports that every step ran and ended in a non-error result.
	Completed bool `json:"completed"`
	// PluginExpected and PluginLoaded are the arm's intent and what the host reported. A qompack
	// trial whose plugin did not load is not a qompack trial, and the summary says so.
	PluginExpected bool              `json:"plugin_expected"`
	PluginLoaded   bool              `json:"plugin_loaded"`
	Checks         []LiveCheckResult `json:"checks"`
	// TaskSuccess is every task check passing. An incomplete trial is graded on whatever state it
	// left, and counts in the denominator.
	TaskSuccess bool `json:"task_success"`
	// ConstraintViolations counts failed constraint checks.
	ConstraintViolations int `json:"constraint_violations"`
	// Recovered is every recovery check passing; nil when the task declares none.
	Recovered   *bool            `json:"recovered"`
	Compactions []HostCompaction `json:"compactions"`
	Account     SessionAccount   `json:"account"`
	// HostCostUSD is the host's own client-side list-price estimate for this session.
	HostCostUSD float64 `json:"host_cost_usd"`
	// Estimate is this repository's estimate from the dated rate table in force; nil when no rate
	// covered the model. Neither figure is a charge: the sessions run on a subscription.
	Estimate         *Money          `json:"estimate,omitempty"`
	EstimateComplete bool            `json:"estimate_complete"`
	EstimateMissing  []UsageCategory `json:"estimate_missing,omitempty"`
	RateTableDate    string          `json:"rate_table_date,omitempty"`
	// HookLatencyMS is the pipe-observed latency of each hook, by hook name.
	HookLatencyMS map[string][]int64 `json:"hook_latency_ms,omitempty"`
	// TranscriptHookMS is the host's own recorded hook durations from the transcript, by hook name.
	TranscriptHookMS map[string][]int64 `json:"transcript_hook_ms,omitempty"`
	Store            *LiveStoreStats    `json:"store,omitempty"`
	// Categories is the trial's per-category usage from its request ledger, with evidence counts.
	Categories map[UsageCategory]CategorySum `json:"categories,omitempty"`
	// CompactAttempts is every compaction outcome the host reported, failed ones included.
	CompactAttempts []HostCompactAttempt `json:"compact_attempts,omitempty"`
	// HookProblems is every hook failure the host reported, from the stream, the transcript and
	// the host's stderr. On the qompack arm each one is a plugin defect the trial ran with.
	HookProblems []HostHookProblem `json:"hook_problems,omitempty"`
	// Qompack is the plugin-side evidence of a qompack-arm trial; nil on the stock arm.
	Qompack *LiveQompackEvidence `json:"qompack,omitempty"`
	// Plugin says exactly which plugin build a qompack-arm trial ran; nil on the stock arm.
	Plugin *LivePluginIdentity `json:"plugin,omitempty"`
	// HomeGuard is the operator-configuration guard's verdict around this trial.
	HomeGuard *LiveHomeGuard `json:"home_guard,omitempty"`
	// ForeignPlugins names every non-builtin plugin the host loaded beyond the arm's own (on the
	// stock arm, every non-builtin plugin). Both arms are meant to run with nothing else.
	ForeignPlugins []string `json:"foreign_plugins,omitempty"`
	// PreregisteredModel reports whether Model is the task set's pre-registered model.
	PreregisteredModel bool     `json:"preregistered_model"`
	Notes              []string `json:"notes,omitempty"`
}

// LiveQompackEvidence is what a qompack-arm trial shows the plugin did.
type LiveQompackEvidence struct {
	// Checkpoints is the number of entries in .qompack/checkpoints/MANIFEST.jsonl: the evidence that
	// PreCompact ran, since the host prints no stream event for a PreCompact hook.
	Checkpoints int `json:"checkpoints"`
	// Injections is every Qompack rehydration block the host recorded as injected context, and
	// InjectedBytes their total size.
	Injections    []TranscriptContext `json:"injections,omitempty"`
	InjectedBytes int                 `json:"injected_bytes"`
	// MCPServerStatus is the qompack MCP server's status in the host's first init line.
	MCPServerStatus string `json:"mcp_server_status,omitempty"`
	// MCPToolCalls counts the session's calls to each Qompack MCP tool.
	MCPToolCalls map[string]int `json:"mcp_tool_calls,omitempty"`
	// IndexLines counts lines per .qompack/index/*.jsonl file after the session.
	IndexLines map[string]int `json:"index_lines,omitempty"`
}

// LivePluginIdentity says exactly which plugin build a trial ran and how it reached the host.
type LivePluginIdentity struct {
	// Install is "plugin-dir" or "marketplace".
	Install string `json:"install"`
	// BundleDir is the bundle directory the host loaded or installed from.
	BundleDir string `json:"bundle_dir"`
	// Version and Commit are read from the bundle's BUNDLE.json; Dirty says whether it was
	// assembled from a worktree with uncommitted changes.
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Dirty   bool   `json:"dirty"`
	// BundleSHA256 is the SHA-256 of BUNDLE.json, which itself lists every file's hash.
	BundleSHA256 string `json:"bundle_json_sha256"`
	// HostVersion is the plugin version the host reported loading.
	HostVersion string `json:"host_version,omitempty"`
	// HostSource and HostPath are where the host said it loaded the plugin from: the source must be
	// the arm's own install ("qompack@inline" for --plugin-dir, "qompack@<marketplace>" for the
	// marketplace flow) for the trial to count as having its plugin.
	HostSource string `json:"host_source,omitempty"`
	HostPath   string `json:"host_path,omitempty"`
}

// LiveHomeGuard is the verdict of the guard over the operator's real Claude Code configuration.
type LiveHomeGuard struct {
	// Checked reports that a before/after comparison was made.
	Checked bool `json:"checked"`
	// Unchanged reports that every guarded file hashed identically before and after.
	Unchanged bool `json:"unchanged"`
	// Before and After are the guarded files' fingerprints ("<path> <sha256|absent>").
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
	// Leftovers names run-created paths that the cleanup could not remove.
	Leftovers []string `json:"leftovers,omitempty"`
}

// ToolUsesAfterSteps maps every compaction step of task that the session reached to every tool
// call made in the turns after it, subagents' included: a fact re-derived by a subagent was not
// recovered either. A compaction step the stream never produced a result for is absent.
func ToolUsesAfterSteps(task LiveTask, s HostStream) map[string][]HostToolUse {
	out := map[string][]HostToolUse{}
	for i, step := range task.Steps {
		if !step.Compact || i >= len(s.Turns) || s.Turns[i].Result == nil {
			continue
		}
		uses := []HostToolUse{}
		for _, t := range s.Turns[i+1:] {
			for _, r := range t.Requests {
				uses = append(uses, r.ToolUses...)
			}
		}
		out[step.ID] = uses
	}
	return out
}

// MCPToolCalls counts the stream's calls to tools whose name starts with prefix, keyed by the
// name with the prefix removed.
func MCPToolCalls(s HostStream, prefix string) map[string]int {
	out := map[string]int{}
	for _, t := range s.Turns {
		for _, r := range t.Requests {
			for _, u := range r.ToolUses {
				if name, ok := strings.CutPrefix(u.Name, prefix); ok {
					out[name]++
				}
			}
		}
	}
	return out
}

// LiveStepRecord is one step's outcome.
type LiveStepRecord struct {
	ID                string   `json:"id"`
	Compact           bool     `json:"compact"`
	ResultSubtype     string   `json:"result_subtype"`
	IsError           bool     `json:"is_error"`
	NumTurns          int      `json:"num_turns"`
	WallMS            int64    `json:"wall_ms"`
	PermissionDenials int      `json:"permission_denials"`
	Answer            string   `json:"answer,omitempty"`
	LocalOutput       []string `json:"local_output,omitempty"`
}

// LiveStoreStats is the size of a trial's .qompack/ after the session.
type LiveStoreStats struct {
	Bytes       int64 `json:"bytes"`
	Files       int64 `json:"files"`
	Objects     int64 `json:"objects"`
	Checkpoints int64 `json:"checkpoints"`
}

// AnswersByStep maps each prompt step to the answer its turn ended with, assuming the driver sent
// one message per step in order and each produced exactly one result. Steps the stream never
// reached are absent, so a grader sees "no answer" rather than an empty string.
func AnswersByStep(task LiveTask, s HostStream) map[string]string {
	out := map[string]string{}
	for i, step := range task.Steps {
		if i >= len(s.Turns) || step.Compact || s.Turns[i].Result == nil {
			continue
		}
		out[step.ID] = s.Turns[i].Answer()
	}
	return out
}

// StepRecords pairs the task's steps with the turns that answered them.
func StepRecords(task LiveTask, s HostStream) ([]LiveStepRecord, bool) {
	complete := len(s.Turns) >= len(task.Steps)
	out := make([]LiveStepRecord, 0, len(task.Steps))
	for i, step := range task.Steps {
		rec := LiveStepRecord{ID: step.ID, Compact: step.Compact}
		if i < len(s.Turns) {
			t := s.Turns[i]
			rec.LocalOutput = t.LocalOutput
			if t.LastRecvMS > t.FirstRecvMS {
				rec.WallMS = t.LastRecvMS - t.FirstRecvMS
			}
			if t.Result != nil {
				rec.ResultSubtype = t.Result.Subtype
				rec.IsError = t.Result.IsError
				rec.NumTurns = t.Result.NumTurns
				rec.PermissionDenials = t.Result.PermissionDenials
				rec.Answer = t.Answer()
			}
		}
		if rec.ResultSubtype == "" || rec.IsError {
			complete = false
		}
		if step.Compact && !turnCompacted(s, i) {
			complete = false
		}
		out = append(out, rec)
	}
	return out, complete
}

// turnCompacted reports that turn i was the /compact the task sent and that it compacted. An
// automatic compaction inside an ordinary turn is not the forced compaction a compact step asked
// for, and a /compact the host refused ("too_few_groups") left nothing compacted.
func turnCompacted(s HostStream, i int) bool {
	if i >= len(s.Turns) || s.Turns[i].Result == nil {
		return false
	}
	t := s.Turns[i]
	return t.Result.LocalCommand == compactLocalCommand && len(t.Compactions) > 0
}

// compactLocalCommand is the local_command a /compact turn's result carries.
const compactLocalCommand = "compact"

// ApplyGrades fills the three outcome fields from graded checks.
func (t *LiveTrial) ApplyGrades(results []LiveCheckResult) {
	t.Checks = results
	task, rec := true, true
	haveTask, haveRec := false, false
	t.ConstraintViolations = 0
	for _, r := range results {
		switch r.Outcome {
		case OutcomeTask:
			haveTask = true
			task = task && r.Passed
		case OutcomeRecovery:
			haveRec = true
			rec = rec && r.Passed
		case OutcomeConstraint:
			if !r.Passed {
				t.ConstraintViolations++
			}
		}
	}
	t.TaskSuccess = haveTask && task
	if haveRec {
		t.Recovered = &rec
	}
}

// ApplyHarnessFailure scores a trial the harness could not run as designed — HarnessError is set —
// as a failure on every pre-registered outcome (preregistration §8, intention to treat): not a task
// success, not complete, and, when the task declares recovery checks, not recovered, so the trial
// stays in the recovery denominator even when nothing was graded. It does nothing to a trial with no
// harness error. SummarizeLive applies the same rule to task success and constraint-cleanness on its
// own; this is what puts a never-graded trial into the recovery denominator.
func (t *LiveTrial) ApplyHarnessFailure(task LiveTask) {
	if t.HarnessError == "" {
		return
	}
	t.TaskSuccess = false
	t.Completed = false
	for _, c := range task.Checks {
		if c.Outcome == OutcomeRecovery {
			failed := false
			t.Recovered = &failed
			break
		}
	}
}

// ── summary ─────────────────────────────────────────────────────────────────────────────────────

// Proportion is k successes of n with a Wilson score interval.
type Proportion struct {
	K    int     `json:"k"`
	N    int     `json:"n"`
	Rate float64 `json:"rate"`
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

// Difference is a qompack-minus-stock difference of proportions with a Newcombe hybrid score
// interval (Newcombe 1998, method 10), which stays inside [-1, 1] and behaves at 0 and n.
type Difference struct {
	Estimate float64 `json:"estimate"`
	Low      float64 `json:"low"`
	High     float64 `json:"high"`
}

// ArmSummary is one arm's outcomes and costs.
type ArmSummary struct {
	Arm    string `json:"arm"`
	Trials int    `json:"trials"`
	// Completed counts trials whose every step ended in a non-error result.
	Completed int `json:"completed"`
	// PluginMismatch counts trials whose plugin state contradicted the arm.
	PluginMismatch int        `json:"plugin_mismatch"`
	TaskSuccess    Proportion `json:"task_success"`
	// ConstraintClean is the proportion of trials with no constraint violation.
	ConstraintClean      Proportion `json:"constraint_clean"`
	ConstraintViolations int        `json:"constraint_violations"`
	// Recovery is over trials whose task declares recovery checks.
	Recovery Proportion `json:"recovery"`
	// MeanUsage is the per-trial mean of every model's Total, summed across models.
	MeanUsage      UsageTotals `json:"mean_usage"`
	MeanHostCost   float64     `json:"mean_host_cost_usd"`
	MeanEstimateMc int64       `json:"mean_estimate_micros"`
	// EstimateIncomplete counts trials whose estimate is a lower bound.
	EstimateIncomplete int     `json:"estimate_incomplete"`
	MeanWallMS         int64   `json:"mean_wall_ms"`
	MeanStoreBytes     int64   `json:"mean_store_bytes,omitempty"`
	HookP50MS          float64 `json:"hook_p50_ms,omitempty"`
	HookP95MS          float64 `json:"hook_p95_ms,omitempty"`
	// HookProblemTrials counts trials whose host reported at least one hook failure.
	HookProblemTrials int `json:"hook_problem_trials"`
	// AccountInconsistent counts trials whose usage account broke one of its own rules
	// (SessionAccount.Consistent false, with the problems named): their outcomes count, but their
	// category sums and estimate are not reliable.
	AccountInconsistent int `json:"account_inconsistent"`
	// ForeignPluginTrials counts trials whose host loaded a plugin other than the arm's own.
	ForeignPluginTrials int `json:"foreign_plugin_trials"`
	// Categories sums every trial's per-category usage, evidence counts included.
	Categories map[UsageCategory]CategorySum `json:"categories,omitempty"`
}

// LiveSummary is the whole run's report.
type LiveSummary struct {
	RunID      string                  `json:"run_id"`
	Confidence float64                 `json:"confidence"`
	Arms       map[string]ArmSummary   `json:"arms"`
	ByTask     map[string][]ArmSummary `json:"by_task"`
	// TaskSuccessDiff and RecoveryDiff are qompack minus stock; nil unless both arms ran.
	TaskSuccessDiff     *Difference `json:"task_success_diff,omitempty"`
	RecoveryDiff        *Difference `json:"recovery_diff,omitempty"`
	ConstraintCleanDiff *Difference `json:"constraint_clean_diff,omitempty"`
	// Decision applies the pre-registered rule; see DecideLive.
	Decision LiveDecision `json:"decision"`
	// Failed names every trial that did not complete or whose plugin state contradicted its arm.
	Failed []string `json:"failed"`
	// Notes carries what a reader must know before reading the verdict.
	Notes []string `json:"notes,omitempty"`
	// Analysis is the pre-registered rule the summary was decided under, so a summary read on its own
	// says which confidence level and margin its verdict used.
	Analysis LiveAnalysis `json:"analysis"`
	// ConstraintRegression is set when the constraint-clean difference's interval lies wholly below
	// -margin: preregistration section 8 reports that as a regression whatever the primary verdict is.
	ConstraintRegression string `json:"constraint_regression,omitempty"`
	// ByVariant is each arm's outcomes per task variant ("base", "changing-requirement") and over the
	// held-out tasks (VariantHeldOut); reported, not decided (preregistration section 8).
	ByVariant map[string][]ArmSummary `json:"by_variant,omitempty"`
	// TaskSigns is, for each task both arms ran, the sign of qompack's task-success rate minus
	// stock's: 1, 0 or -1. Reported, not decided.
	TaskSigns map[string]int `json:"task_signs,omitempty"`
}

// VariantHeldOut is the ByVariant key that groups the held-out tasks, whatever their variant.
const VariantHeldOut = "held-out"

// LiveDecision is the pre-registered rule's verdict on the primary outcome.
type LiveDecision struct {
	// Verdict is "superior", "non-inferior", "inferior", "inconclusive" or "not-applicable".
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// SummarizeLive aggregates trials. Every trial counts, complete or not: dropping the failures is
// how an evaluation flatters itself.
func SummarizeLive(runID string, a LiveAnalysis, trials []LiveTrial) LiveSummary {
	z := normalQuantile(1 - (1-a.Confidence)/2)
	sum := LiveSummary{
		RunID:      runID,
		Confidence: a.Confidence,
		Analysis:   a,
		Arms:       map[string]ArmSummary{},
		ByTask:     map[string][]ArmSummary{},
	}
	byArm := map[string][]LiveTrial{}
	byTaskArm := map[string]map[string][]LiveTrial{}
	byVariantArm := map[string]map[string][]LiveTrial{}
	group := func(g map[string]map[string][]LiveTrial, key string, t LiveTrial) {
		if g[key] == nil {
			g[key] = map[string][]LiveTrial{}
		}
		g[key][t.Arm] = append(g[key][t.Arm], t)
	}
	for _, t := range trials {
		byArm[t.Arm] = append(byArm[t.Arm], t)
		group(byTaskArm, t.TaskID, t)
		if t.Variant != "" {
			group(byVariantArm, t.Variant, t)
		}
		if t.HeldOut {
			group(byVariantArm, VariantHeldOut, t)
		}
		if !t.Completed || t.PluginExpected != t.PluginLoaded || t.HarnessError != "" {
			sum.Failed = append(sum.Failed, fmt.Sprintf("%s/%s/%d: completed=%t plugin_expected=%t "+
				"plugin_loaded=%t harness_error=%q", t.TaskID, t.Arm, t.Trial, t.Completed,
				t.PluginExpected, t.PluginLoaded, t.HarnessError))
		}
	}
	for arm, ts := range byArm {
		sum.Arms[arm] = summarizeArm(arm, ts, z)
	}
	sum.ByTask = summarizeGroups(byTaskArm, z)
	if len(byVariantArm) > 0 {
		sum.ByVariant = summarizeGroups(byVariantArm, z)
	}
	for id, arms := range sum.ByTask {
		if sign, ok := taskSign(arms); ok {
			if sum.TaskSigns == nil {
				sum.TaskSigns = map[string]int{}
			}
			sum.TaskSigns[id] = sign
		}
	}
	sort.Strings(sum.Failed)

	q, qok := sum.Arms[ArmQompack]
	s, sok := sum.Arms[ArmStock]
	if qok && sok {
		sum.TaskSuccessDiff = newcombe(q.TaskSuccess, s.TaskSuccess)
		sum.RecoveryDiff = newcombe(q.Recovery, s.Recovery)
		sum.ConstraintCleanDiff = newcombe(q.ConstraintClean, s.ConstraintClean)
	}
	sum.Decision = DecideLive(a, sum)
	if d := sum.ConstraintCleanDiff; d != nil && d.High < -a.NonInferiorityMargin {
		sum.ConstraintRegression = fmt.Sprintf("constraint-clean difference %.3f, interval [%.3f, %.3f], lies "+
			"wholly below -%.3f: a regression, whatever the primary verdict", d.Estimate, d.Low, d.High,
			a.NonInferiorityMargin)
		sum.Notes = append(sum.Notes, "REGRESSION (preregistration section 8, H2): "+sum.ConstraintRegression)
	}
	if sum.TaskSuccessDiff != nil {
		sum.Notes = append(sum.Notes, "trials are clustered within tasks; the pooled intervals treat them as "+
			"independent, which overstates their precision (preregistration section 8)")
	}
	for _, arm := range []string{ArmQompack, ArmStock} {
		as, ok := sum.Arms[arm]
		if !ok {
			continue
		}
		if as.HookProblemTrials > 0 {
			sum.Notes = append(sum.Notes, fmt.Sprintf(
				"%d of %d %s trial(s) ran with a hook failure the host reported; they are counted, "+
					"not dropped, and each trial record names the failure", as.HookProblemTrials, as.Trials, arm))
		}
		if as.ForeignPluginTrials > 0 {
			sum.Notes = append(sum.Notes, fmt.Sprintf(
				"%d of %d %s trial(s) loaded a plugin other than the arm's own: %s; both arms are meant to "+
					"run with nothing else (preregistration section 3)", as.ForeignPluginTrials, as.Trials, arm,
				strings.Join(foreignPlugins(trials, arm), ", ")))
		}
		if as.AccountInconsistent > 0 {
			sum.Notes = append(sum.Notes, fmt.Sprintf(
				"%d of %d %s trial(s) have an inconsistent usage account; their outcomes count, but their "+
					"per-category token sums and cost estimate are not reliable (each trial record's account "+
					"names the problem)", as.AccountInconsistent, as.Trials, arm))
		}
	}
	for _, t := range trials {
		if !t.PreregisteredModel {
			sum.Notes = append(sum.Notes, fmt.Sprintf(
				"trial %s/%s/%d ran on %s, which is not the pre-registered model %s: this run is not "+
					"a confirmatory run", t.TaskID, t.Arm, t.Trial, t.Model, a.Model))
			break
		}
	}
	return sum
}

// DecideLive applies the pre-registered decision rule to the primary outcome, task success after
// compaction:
//
//   - superior when the interval for qompack − stock lies wholly above zero;
//   - non-inferior when its lower bound lies above −margin;
//   - inferior when its upper bound lies below −margin;
//   - inconclusive otherwise.
//
// Any trial whose plugin state contradicted its arm makes the verdict not-applicable: the arms
// were not the arms the rule is about.
func DecideLive(a LiveAnalysis, s LiveSummary) LiveDecision {
	for _, arm := range s.Arms {
		if arm.PluginMismatch > 0 {
			return LiveDecision{
				Verdict: "not-applicable",
				Reason:  fmt.Sprintf("%d %s trial(s) ran with the wrong plugin state", arm.PluginMismatch, arm.Arm),
			}
		}
	}
	d := s.TaskSuccessDiff
	if d == nil {
		return LiveDecision{Verdict: "not-applicable", Reason: "both arms are needed for a comparison"}
	}
	switch {
	case d.Low > 0:
		return LiveDecision{
			Verdict: "superior",
			Reason:  fmt.Sprintf("task-success difference %.3f, interval [%.3f, %.3f] excludes 0", d.Estimate, d.Low, d.High),
		}
	case d.Low > -a.NonInferiorityMargin:
		return LiveDecision{
			Verdict: "non-inferior",
			Reason:  fmt.Sprintf("lower bound %.3f lies above -%.3f", d.Low, a.NonInferiorityMargin),
		}
	case d.High < -a.NonInferiorityMargin:
		return LiveDecision{
			Verdict: "inferior",
			Reason:  fmt.Sprintf("upper bound %.3f lies below -%.3f", d.High, a.NonInferiorityMargin),
		}
	default:
		return LiveDecision{
			Verdict: "inconclusive",
			Reason:  fmt.Sprintf("interval [%.3f, %.3f] straddles -%.3f", d.Low, d.High, a.NonInferiorityMargin),
		}
	}
}

// foreignPlugins is the sorted union of the foreign plugins one arm's trials loaded.
func foreignPlugins(trials []LiveTrial, arm string) []string {
	seen := map[string]bool{}
	for _, t := range trials {
		if t.Arm != arm {
			continue
		}
		for _, p := range t.ForeignPlugins {
			seen[p] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// summarizeGroups summarizes each group's arms, arms in name order.
func summarizeGroups(g map[string]map[string][]LiveTrial, z float64) map[string][]ArmSummary {
	out := make(map[string][]ArmSummary, len(g))
	for key, byArm := range g {
		arms := make([]string, 0, len(byArm))
		for arm := range byArm {
			arms = append(arms, arm)
		}
		sort.Strings(arms)
		for _, arm := range arms {
			out[key] = append(out[key], summarizeArm(arm, byArm[arm], z))
		}
	}
	return out
}

// taskSign is the sign of qompack's task-success rate minus stock's, compared as exact fractions,
// when both arms ran the task.
func taskSign(arms []ArmSummary) (int, bool) {
	var q, s *Proportion
	for i := range arms {
		switch arms[i].Arm {
		case ArmQompack:
			q = &arms[i].TaskSuccess
		case ArmStock:
			s = &arms[i].TaskSuccess
		}
	}
	if q == nil || s == nil || q.N == 0 || s.N == 0 {
		return 0, false
	}
	l, r := q.K*s.N, s.K*q.N
	switch {
	case l > r:
		return 1, true
	case l < r:
		return -1, true
	default:
		return 0, true
	}
}

// summarizeArm aggregates one arm's trials. A trial the harness could not run as designed is a
// failure on every outcome (preregistration §8): it never counts as a task success or as
// constraint-clean, and it counts as not recovered wherever it carries a recovery verdict, whatever
// state it left behind.
func summarizeArm(arm string, ts []LiveTrial, z float64) ArmSummary {
	out := ArmSummary{Arm: arm, Trials: len(ts)}
	var taskK, cleanK, recK, recN int
	var usage UsageTotals
	var cost float64
	var est, wall, store int64
	var storeN int
	var hooks []float64
	for _, t := range ts {
		// A trial the harness could not run as designed is never complete, whatever its steps say.
		if t.Completed && t.HarnessError == "" {
			out.Completed++
		}
		if t.PluginExpected != t.PluginLoaded {
			out.PluginMismatch++
		}
		harness := t.HarnessError != ""
		if t.TaskSuccess && !harness {
			taskK++
		}
		if t.ConstraintViolations == 0 && !harness {
			cleanK++
		}
		out.ConstraintViolations += t.ConstraintViolations
		if t.Recovered != nil {
			recN++
			if *t.Recovered && !harness {
				recK++
			}
		}
		for _, u := range t.Account.Total {
			usage = usage.add(stripSplit(u))
		}
		cost += t.HostCostUSD
		if t.Estimate != nil {
			est += t.Estimate.Micros
		}
		if !t.EstimateComplete {
			out.EstimateIncomplete++
		}
		wall += t.WallMS
		if len(t.HookProblems) > 0 {
			out.HookProblemTrials++
		}
		// A trial that never produced a stream has a zero account with no problems: it has no usage
		// to be wrong about, and is not counted here.
		if !t.Account.Consistent && len(t.Account.Problems) > 0 {
			out.AccountInconsistent++
		}
		if len(t.ForeignPlugins) > 0 {
			out.ForeignPluginTrials++
		}
		for c, cs := range t.Categories {
			if out.Categories == nil {
				out.Categories = map[UsageCategory]CategorySum{}
			}
			acc := out.Categories[c]
			acc.Known += cs.Known
			acc.KnownRecords += cs.KnownRecords
			acc.UnknownRecords += cs.UnknownRecords
			out.Categories[c] = acc
		}
		if t.Store != nil {
			store += t.Store.Bytes
			storeN++
		}
		for _, ms := range t.HookLatencyMS {
			for _, v := range ms {
				hooks = append(hooks, float64(v))
			}
		}
	}
	out.TaskSuccess = wilson(taskK, len(ts), z)
	out.ConstraintClean = wilson(cleanK, len(ts), z)
	out.Recovery = wilson(recK, recN, z)
	if n := int64(len(ts)); n > 0 {
		out.MeanUsage = UsageTotals{
			Input: usage.Input / n, Output: usage.Output / n,
			CacheRead: usage.CacheRead / n, CacheWrite: usage.CacheWrite / n,
		}
		out.MeanHostCost = cost / float64(n)
		out.MeanEstimateMc = est / n
		out.MeanWallMS = wall / n
	}
	if storeN > 0 {
		out.MeanStoreBytes = store / int64(storeN)
	}
	if len(hooks) > 0 {
		sort.Float64s(hooks)
		out.HookP50MS = percentile(hooks, 50)
		out.HookP95MS = percentile(hooks, 95)
	}
	return out
}

// stripSplit drops the optional splits, which do not survive a sum across sessions that did not
// all report them.
func stripSplit(u UsageTotals) UsageTotals {
	return UsageTotals{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
}

// percentile is the nearest-rank percentile of an ascending slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	rank = max(rank, 1)
	rank = min(rank, len(sorted))
	return sorted[rank-1]
}

// wilson is the Wilson score interval for k of n. With n = 0 the rate is undefined and the
// interval is the whole unit range.
func wilson(k, n int, z float64) Proportion {
	p := Proportion{K: k, N: n, Low: 0, High: 1}
	if n == 0 {
		return p
	}
	fn := float64(n)
	phat := float64(k) / fn
	p.Rate = phat
	z2 := z * z
	den := 1 + z2/fn
	centre := (phat + z2/(2*fn)) / den
	half := z * math.Sqrt(phat*(1-phat)/fn+z2/(4*fn*fn)) / den
	p.Low = math.Max(0, centre-half)
	p.High = math.Min(1, centre+half)
	return p
}

// newcombe is the hybrid score interval for p1 − p2 from the two Wilson intervals.
// The Wilson bounds already carry the confidence level, so no z is needed here.
func newcombe(a, b Proportion) *Difference {
	if a.N == 0 || b.N == 0 {
		return nil
	}
	d := a.Rate - b.Rate
	low := d - math.Sqrt(sq(a.Rate-a.Low)+sq(b.High-b.Rate))
	high := d + math.Sqrt(sq(a.High-a.Rate)+sq(b.Rate-b.Low))
	return &Difference{Estimate: d, Low: math.Max(-1, low), High: math.Min(1, high)}
}

func sq(x float64) float64 { return x * x }

// normalQuantile is the standard normal inverse CDF (Acklam's rational approximation, relative
// error below 1.2e-9), so the confidence level is data in the task file rather than a z literal.
func normalQuantile(p float64) float64 {
	a := [6]float64{
		-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02,
		1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00,
	}
	b := [5]float64{
		-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02,
		6.680131188771972e+01, -1.328068155288572e+01,
	}
	c := [6]float64{
		-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00,
		-2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00,
	}
	d := [4]float64{
		7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00,
		3.754408661907416e+00,
	}
	const pLow = 0.02425
	switch {
	case p <= 0:
		return math.Inf(-1)
	case p >= 1:
		return math.Inf(1)
	case p < pLow:
		q := math.Sqrt(-2 * math.Log(p))
		return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p > 1-pLow:
		q := math.Sqrt(-2 * math.Log(1-p))
		return -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	default:
		q := p - 0.5
		r := q * q
		return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q /
			(((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	}
}
