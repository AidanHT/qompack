package eval

// ── Live trial records and their summary ─────────────────────────────────────────────────────────
//
// V6 close-out C5.4/C5.5. One LiveTrial is one real headless session of one task under one arm. The
// summary reports the pre-registered primary outcomes per arm with confidence intervals, lists every
// failed or incomplete trial by name, and keeps usage and cost beside the outcomes, never inside
// them: a cheap wrong answer must not look like a result.

import (
	"fmt"
	"maps"
	"math"
	"slices"
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

// LiveQompackToolPrefix begins the name the host gives every tool of the Qompack plugin's own MCP
// server ("mcp__plugin_qompack_qompack__recall", say).
const LiveQompackToolPrefix = "mcp__plugin_qompack_qompack__"

// LiveTrial is one trial's record.
type LiveTrial struct {
	Schema   int    `json:"schema"`
	RunID    string `json:"run_id"`
	TaskID   string `json:"task_id"`
	Arm      string `json:"arm"`
	Trial    int    `json:"trial"`
	Category string `json:"category"`
	Variant  string `json:"variant"`
	// Domain is the task's domain label (W6 item 6); empty for the two-arm sets.
	Domain  string `json:"domain,omitempty"`
	HeldOut bool   `json:"held_out"`
	Model   string `json:"model"`
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
	PluginExpected bool `json:"plugin_expected"`
	PluginLoaded   bool `json:"plugin_loaded"`
	// HostReportedPlugins reports that the host printed its start-up line listing the plugins it
	// loaded. Only then does the trial have a plugin state that can contradict its arm; a trial
	// whose host never got that far is a harness failure, not a plugin mismatch.
	HostReportedPlugins bool              `json:"host_reported_plugins"`
	Checks              []LiveCheckResult `json:"checks"`
	// TaskSuccess is every task check passing. An incomplete trial is graded on whatever state it
	// left, and counts in the denominator.
	TaskSuccess bool `json:"task_success"`
	// ConstraintViolations counts failed constraint checks.
	ConstraintViolations int `json:"constraint_violations"`
	// Recovered is every recovery check passing; nil when the task declares none.
	Recovered *bool `json:"recovered"`
	// ConstraintClean is every constraint check passing; nil when the task declares none. The Stage A
	// constraint-clean rate is over these trials only (plan §9.4, revision 4 C-m7).
	ConstraintClean *bool            `json:"constraint_clean,omitempty"`
	Compactions     []HostCompaction `json:"compactions"`
	Account         SessionAccount   `json:"account"`
	// HostCostUSD is the host's own client-side list-price estimate for this session.
	HostCostUSD float64 `json:"host_cost_usd"`
	// Estimate is this repository's estimate from the dated rate table in force; nil when no rate
	// covered the model. Neither figure is a charge: the sessions run on a subscription.
	Estimate         *Money          `json:"estimate,omitempty"`
	EstimateComplete bool            `json:"estimate_complete"`
	EstimateMissing  []UsageCategory `json:"estimate_missing,omitempty"`
	// EstimateUnpriced names the models the rate table does not price; their usage adds nothing to
	// Estimate, which is then a lower bound.
	EstimateUnpriced []string `json:"estimate_unpriced,omitempty"`
	// StepTimeoutMS is the run's step timeout (plan.json's step_timeout_ms): the cap active time counts a
	// reached step with no result at. 0 in a record written before it was recorded.
	StepTimeoutMS int64  `json:"step_timeout_ms,omitempty"`
	RateTableDate string `json:"rate_table_date,omitempty"`
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
	// PreregisteredModel reports whether Model is the task set's pre-registered model, or the one
	// alias its pre-registration permits in that model's place (section 3's contingency).
	PreregisteredModel bool `json:"preregistered_model"`
	// HostModel is the model the host reported at start-up (its init line): what Model resolved to.
	HostModel string `json:"host_model,omitempty"`
	// ArmState is the state the trial's arm declares, which the harness set up and checked; nil in a
	// record of the two-arm design.
	ArmState *LiveArmState `json:"arm_state,omitempty"`
	// ConfigurationError says how the trial's configuration contradicted its arm (W6 item 4): the
	// host's plugin list, the arm's bundle, the harness-set environment, the project's CLAUDE.md or
	// store, or the pinned host version. Such a trial is a harness error, never scored: its record is
	// set aside under another name in its slot and the slot is run again.
	ConfigurationError string `json:"configuration_error,omitempty"`
	// Hello is the steer arm's hello file as the session left it: a treatment outcome, counted as
	// assigned whatever it says (intention to treat). nil on every other arm.
	Hello *LiveHelloOutcome `json:"hello,omitempty"`
	// Brief is the steer arm's brief outcome for each compaction (W6 item 5): a treatment outcome,
	// counted as assigned whatever it says. nil on every other arm.
	Brief *LiveBriefOutcomes `json:"brief,omitempty"`
	// ResidentDaemon is the daemon the harness found already resident for the trial's project before
	// its session (ruling R42); nil when none was, the expected state of a fresh project.
	ResidentDaemon *LiveResidentDaemon `json:"resident_daemon,omitempty"`
	// RateLimits is every rate_limit_event the session's stream carried, and RateUse each window's
	// utilization delta over the trial (W6 item 8): what the trial spent of the subscription.
	RateLimits []HostRateLimit          `json:"rate_limits,omitempty"`
	RateUse    map[string]LiveRateDelta `json:"rate_use,omitempty"`
	// OverageStatus and OverageDisabledReason are the account's overage state as the session's
	// events last reported it: "rejected" and "org_level_disabled" say it cannot spend overage at all.
	OverageStatus         string `json:"overage_status,omitempty"`
	OverageDisabledReason string `json:"overage_disabled_reason,omitempty"`
	// Limited says how a subscription limit cut the trial short (W6 item 8). Such a trial is kept on
	// disk under another name in its slot, excluded from every analysis but the limited sensitivity
	// analysis, and its slot is run again.
	Limited string `json:"limited,omitempty"`
	// Stopped is why the harness stopped the session itself (paid overage in use): the stop the run
	// acts on, whatever the cut stream parses to (re-review N1).
	Stopped string `json:"stopped,omitempty"`
	// LimitResetsAt is when the usage-limit result that cut the trial short said the limit resets,
	// in Unix seconds; 0 when it did not say.
	LimitResetsAt int64    `json:"limit_resets_at,omitempty"`
	Notes         []string `json:"notes,omitempty"`
}

// LiveHelloOutcome is what a steer trial's project held of the mod's hello file after the session:
// exactly one state/mod-hello-*.json, for the session the stream reported, from a host at least
// the mod's minimum version, whose served hello said steering true. The harness reads the file with
// the wire contract's parser and hands the values here.
type LiveHelloOutcome struct {
	// Files counts the state/mod-hello-*.json files found.
	Files       int    `json:"files"`
	SessionID   string `json:"session_id,omitempty"`
	HostVersion string `json:"host_version,omitempty"`
	Enabled     bool   `json:"enabled"`
	Steering    bool   `json:"steering"`
	// OK is that every rule held; Problem names the first that did not.
	OK      bool   `json:"ok"`
	Problem string `json:"problem,omitempty"`
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
	// Mods is that the bundle's file list carries the mod sources (a --bundle-mods build); BUNDLE.json
	// has no flag of its own, so this is read from its listing.
	Mods bool `json:"mods,omitempty"`
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
//
// Calls to the Qompack plugin's own MCP tools (LiveQompackToolPrefix) are left out. They look up
// what the plugin archived before the compaction — they run no program and read no file from disk —
// so a lookup is the recovery a tool_not_used_after check asks for, not the re-derivation it
// forbids, and only the qompack arm has such tools to be penalised for (preregistration amendment
// A3). Every other tool, a Bash run of the same command included, still counts.
func ToolUsesAfterSteps(task LiveTask, s HostStream) map[string][]HostToolUse {
	out := map[string][]HostToolUse{}
	for i, step := range task.Steps {
		if !step.Compact || i >= len(s.Turns) || s.Turns[i].Result == nil {
			continue
		}
		uses := []HostToolUse{}
		for _, t := range s.Turns[i+1:] {
			for _, r := range t.Requests {
				for _, u := range r.ToolUses {
					if !strings.HasPrefix(u.Name, LiveQompackToolPrefix) {
						uses = append(uses, u)
					}
				}
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
	// HasResult says the host printed the result line that closed the step's turn; DurationMS and
	// DurationAPIMS are that line's duration_ms and duration_api_ms (amendment 4: active time T is
	// recomputable from the stream, unlike the pipe-measured WallMS).
	HasResult     bool  `json:"has_result,omitempty"`
	DurationMS    int64 `json:"duration_ms,omitempty"`
	DurationAPIMS int64 `json:"duration_api_ms,omitempty"`
	// DurationMissing and CostMissing say the result line carried no duration_ms or no
	// total_cost_usd: unknown values, never zeros (amendment 4).
	DurationMissing bool `json:"duration_missing,omitempty"`
	CostMissing     bool `json:"cost_missing,omitempty"`
}

// result reports that the step's turn ended in a result line. A record written before HasResult
// existed says so by its subtype.
func (r LiveStepRecord) result() bool { return r.HasResult || r.ResultSubtype != "" }

// The status of a trial's cost C (amendment 4): complete when every step ended in a result line that
// carries the running total, a lower bound when the last step the trial reached has none (or its line
// carries no total, an earlier one did), unknown when no result line carries one. Missing usage is
// unknown, never zero (Qompack.md).
const (
	LiveCostComplete = "complete"
	LiveCostPartial  = "partial"
	LiveCostUnknown  = "unknown"
)

// Cost is the trial's cost C — the host's own list-price estimate, HostCostUSD, the last running total
// a result line carried — and its status. The driver sends a step only after the previous step's
// result, so the steps a trial reached are those with a result and, when one lacks it, that first step
// without one.
func (t LiveTrial) Cost() (float64, string) {
	results, priced, last := 0, 0, -1
	for i, s := range t.Steps {
		if s.result() {
			results++
			last = i
			if !s.CostMissing {
				priced++
			}
		}
	}
	switch {
	case priced == 0:
		return 0, LiveCostUnknown
	case results < len(t.Steps) || t.Steps[last].CostMissing:
		return t.HostCostUSD, LiveCostPartial
	}
	return t.HostCostUSD, LiveCostComplete
}

// ActiveMS is the trial's active time T as observed (amendment 4): over the steps it reached, the
// host's duration_ms of each step's result, and capMS — the pinned step timeout — for a reached step
// with no result. Steps never reached count 0, and so does a result line with no duration_ms.
func (t LiveTrial) ActiveMS(capMS int64) int64 { return t.activeMS(capMS, false) }

// ActiveMSAdverse is T as the treated arm of a decision counts it, failing closed (review STATS-5): a
// result line with no duration_ms counts at capMS, and so does a trial with no step record at all (the
// session never ran, so its time is unknown).
func (t LiveTrial) ActiveMSAdverse(capMS int64) int64 { return t.activeMS(capMS, true) }

func (t LiveTrial) activeMS(capMS int64, adverse bool) int64 {
	if len(t.Steps) == 0 && adverse {
		return capMS
	}
	var total int64
	for _, s := range t.Steps {
		switch {
		case !s.result():
			return total + capMS
		case s.DurationMissing && adverse:
			total += capMS
		default:
			total += s.DurationMS
		}
	}
	return total
}

// MissingDurations counts the reached steps whose result line carried no duration_ms.
func (t LiveTrial) MissingDurations() int {
	n := 0
	for _, s := range t.Steps {
		if s.result() && s.DurationMissing {
			n++
		}
	}
	return n
}

// APIMS is the API share of the trial's active time: the reached steps' duration_api_ms.
func (t LiveTrial) APIMS() int64 {
	var total int64
	for _, s := range t.Steps {
		if !s.result() {
			break
		}
		total += s.DurationAPIMS
	}
	return total
}

// TimedOutSteps counts the reached steps that ended with no result line: at most one per trial.
func (t LiveTrial) TimedOutSteps() int {
	for _, s := range t.Steps {
		if !s.result() {
			return 1
		}
	}
	return 0
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
				rec.HasResult = true
				rec.DurationMS, rec.DurationAPIMS = t.Result.DurationMS, t.Result.DurationAPIMS
				rec.DurationMissing, rec.CostMissing = t.Result.DurationMissing, t.Result.CostMissing
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
	haveTask, haveRec, haveConstraint := false, false, false
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
			haveConstraint = true
			if !r.Passed {
				t.ConstraintViolations++
			}
		}
	}
	t.TaskSuccess = haveTask && task
	if haveRec {
		t.Recovered = &rec
	}
	if haveConstraint {
		clean := t.ConstraintViolations == 0
		t.ConstraintClean = &clean
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
		failed := false
		switch c.Outcome {
		case OutcomeRecovery:
			t.Recovered = &failed
		case OutcomeConstraint:
			t.ConstraintClean = &failed
		}
	}
}

// pluginMismatch reports that the host's own list of loaded plugins contradicted the arm: the
// arm's plugin absent on a qompack trial, or present on a stock trial. A trial whose host never
// reported its plugins has no such list and is not a mismatch; its harness error scores it.
func (t LiveTrial) pluginMismatch() bool {
	return t.HostReportedPlugins && t.PluginExpected != t.PluginLoaded
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
	MeanUsage UsageTotals `json:"mean_usage"`
	// MeanHostCost is the mean cost C over the trials whose cost is known; CostPartial counts those of
	// them whose C is a lower bound, and CostUnknown the trials with no result line, left out of the
	// mean rather than added as zero (amendment 4).
	MeanHostCost   float64 `json:"mean_host_cost_usd"`
	CostPartial    int     `json:"cost_partial,omitempty"`
	CostUnknown    int     `json:"cost_unknown,omitempty"`
	MeanEstimateMc int64   `json:"mean_estimate_micros"`
	// EstimateIncomplete counts trials whose estimate is a lower bound.
	EstimateIncomplete int   `json:"estimate_incomplete"`
	MeanWallMS         int64 `json:"mean_wall_ms"`
	MeanStoreBytes     int64 `json:"mean_store_bytes,omitempty"`
	// HookP50MS and HookP95MS pool the pipe-observed latency of every hook (hook_started to
	// hook_response as the driver received them): an approximation, kept for continuity. The
	// pre-registered measure is HostHookMS.
	HookP50MS float64 `json:"hook_p50_ms,omitempty"`
	HookP95MS float64 `json:"hook_p95_ms,omitempty"`
	// HostHookMS is, per hook name, the durations the host itself measured and recorded in its
	// transcript (durationMs), over every trial of the arm: preregistration section 6's hook latency.
	HostHookMS map[string]HookLatency `json:"host_hook_ms,omitempty"`
	// HookProblemTrials counts trials whose host reported at least one hook failure.
	HookProblemTrials int `json:"hook_problem_trials"`
	// AccountInconsistent counts trials whose usage account broke one of its own rules
	// (SessionAccount.Consistent false, with the problems named): their outcomes count, but their
	// category sums and estimate are not reliable.
	AccountInconsistent int `json:"account_inconsistent"`
	// ForeignPluginTrials counts trials whose host loaded a plugin other than the arm's own, and
	// ForeignPlugins names them (sorted): preregistration section 3 makes the arms identical except
	// for the plugin, so a run with any is not the pre-registered comparison.
	ForeignPluginTrials int      `json:"foreign_plugin_trials"`
	ForeignPlugins      []string `json:"foreign_plugins,omitempty"`
	// Categories sums every trial's per-category usage, evidence counts included.
	Categories map[UsageCategory]CategorySum `json:"categories,omitempty"`
	// HelloProblemTrials counts the arm's trials whose hello file was missing or wrong: treatment
	// outcomes, counted and reported, never excluded.
	HelloProblemTrials int `json:"hello_problem_trials,omitempty"`
	// RateUse is, by window, the arm's per-trial utilization deltas: how much of the subscription's
	// five-hour and seven-day windows one trial uses (W6 item 8; R2's calendar estimate reads it).
	RateUse map[string]LiveRateUseStats `json:"rate_use,omitempty"`
	// Brief is the arm's brief outcomes over its compactions (W6 item 5); nil on an arm with no brief.
	Brief *LiveBriefStats `json:"brief,omitempty"`
	// Efficiency is the arm's cost C and active time T (amendment 4) and its compactions; reported here,
	// decided between arms by DecideStageA.
	Efficiency *LiveArmEfficiency `json:"efficiency,omitempty"`
}

// LiveRateUseStats summarizes one window's per-trial utilization deltas over the trials that had one.
type LiveRateUseStats struct {
	Trials int     `json:"trials"`
	Mean   float64 `json:"mean"`
	Max    float64 `json:"max"`
}

// HookLatency is one hook's host-measured durations over an arm's trials, in milliseconds, with
// nearest-rank percentiles.
type HookLatency struct {
	N   int   `json:"n"`
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
	Max int64 `json:"max"`
}

// LiveSummarySchema is the summary document's format version. A summary written before the field
// existed decodes as 0 and is read as version 1, whose shape it has.
const LiveSummarySchema = 1

// LiveSummary is the whole run's report.
type LiveSummary struct {
	Schema     int                     `json:"schema"`
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
	// ByDomain is each arm's outcomes per task domain (W6 item 6), built the way ByVariant is;
	// reported, not decided. nil when no trial's task names a domain.
	ByDomain map[string][]ArmSummary `json:"by_domain,omitempty"`
	// TaskSigns is, for each task both arms ran, the sign of qompack's task-success rate minus
	// stock's: 1, 0 or -1. Reported, not decided.
	TaskSigns map[string]int `json:"task_signs,omitempty"`
	// HostModels is every distinct model the trials' hosts reported at start-up, sorted: what the run
	// actually ran on, which an alias leaves to the host (preregistration section 3's contingency).
	HostModels []string `json:"host_models,omitempty"`
	// LimitedTrials counts, per arm, the trials a subscription limit cut short: set aside, excluded
	// from every analysis above and run again (W6 item 8). LimitedSensitivity is each arm's outcomes
	// with those trials added back as failures on every outcome, reported beside the primary analysis
	// and never decided.
	LimitedTrials      map[string]int        `json:"limited_trials,omitempty"`
	LimitedSensitivity map[string]ArmSummary `json:"limited_sensitivity,omitempty"`
	// StageA is the Stage A decisions (W6 item 2, amendment 4): every pre-registered test with its
	// paired-bootstrap interval and §9.4 label, and the reported rows; nil for a two-arm run.
	StageA *LiveStageA `json:"stage_a,omitempty"`
	// ResidentDaemonFaults counts the attempts set aside because a daemon of another build than the
	// arm's bundle was resident for the trial's project before its session (ruling R42): harness
	// faults, discarded and run again, never scored.
	ResidentDaemonFaults int `json:"resident_daemon_faults,omitempty"`
	// Paused says why the run paused cleanly before its plan was done (a limit or the night window's
	// end, W6 item 8); `devtool live-eval --resume` continues it. Empty for a run that finished or
	// stopped.
	Paused string `json:"paused,omitempty"`
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
//
// A run of the Stage A design (a four-arm task set, or trials of classic, generic or steer; never a
// trial of the two-arm design's qompack arm) is decided by the Stage A rules (DecideStageA: the
// paired task bootstrap, the fixed-sequence chain, S, the harm tests and amendment 4's efficiency
// guards), which replace the two-arm DecideLive; a recorded two-arm run is still decided by DecideLive.
func SummarizeLive(runID string, a LiveAnalysis, trials []LiveTrial) LiveSummary {
	sum := summarizeLive(runID, a, trials)
	if stageARun(a, trials) {
		sum.StageA = DecideStageA(a, trials, nil)
		sum.Decision = sum.StageA.Decision
	}
	return sum
}

// stageARun reports that the trials are a run of the Stage A design: routed on the design (a four-arm
// task set), or on the arms for a set without per-arm counts, and never with a qompack trial, which
// belongs to a recorded two-arm run (review WIRING-9).
func stageARun(a LiveAnalysis, trials []LiveTrial) bool {
	stageA := a.fourArm()
	for _, t := range trials {
		switch t.Arm {
		case ArmQompack:
			return false
		case ArmClassic, ArmGeneric, ArmSteer:
			stageA = true
		}
	}
	return stageA
}

// armRules is how summarizeArm counts: the interval's z, whether constraint-clean is over the trials
// whose task declares a constraint check (Stage A, revision 4 C-m7) or over every trial (the two-arm
// design), and the step timeout a reached step with no result counts as in active time.
type armRules struct {
	z                  float64
	declaredConstraint bool
	capMS              int64
}

// summarizeLive is SummarizeLive without the Stage A decision.
func summarizeLive(runID string, a LiveAnalysis, trials []LiveTrial) LiveSummary {
	rules := armRules{
		z: normalQuantile(1 - (1-a.Confidence)/2), declaredConstraint: stageARun(a, trials), capMS: a.stepTimeoutMS(),
	}
	sum := LiveSummary{
		Schema:     LiveSummarySchema,
		RunID:      runID,
		Confidence: a.Confidence,
		Analysis:   a,
		Arms:       map[string]ArmSummary{},
		ByTask:     map[string][]ArmSummary{},
	}
	byArm := map[string][]LiveTrial{}
	byTaskArm := map[string]map[string][]LiveTrial{}
	byVariantArm := map[string]map[string][]LiveTrial{}
	byDomainArm := map[string]map[string][]LiveTrial{}
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
		if t.Domain != "" {
			group(byDomainArm, t.Domain, t)
		}
		if !t.Completed || t.pluginMismatch() || t.HarnessError != "" {
			sum.Failed = append(sum.Failed, fmt.Sprintf("%s/%s/%d: completed=%t plugin_expected=%t "+
				"plugin_loaded=%t harness_error=%q", t.TaskID, t.Arm, t.Trial, t.Completed,
				t.PluginExpected, t.PluginLoaded, t.HarnessError))
		}
	}
	for arm, ts := range byArm {
		sum.Arms[arm] = summarizeArm(arm, ts, rules)
	}
	sum.ByTask = summarizeGroups(byTaskArm, rules)
	if len(byVariantArm) > 0 {
		sum.ByVariant = summarizeGroups(byVariantArm, rules)
	}
	if len(byDomainArm) > 0 {
		sum.ByDomain = summarizeGroups(byDomainArm, rules)
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
	for _, arm := range slices.Sorted(maps.Keys(sum.Arms)) {
		as := sum.Arms[arm]
		if as.HelloProblemTrials > 0 {
			sum.Notes = append(sum.Notes, fmt.Sprintf(
				"%d of %d %s trial(s) have no valid hello file (missing, for another session, from a host below "+
					"the mod's minimum, or not steering); they are counted as assigned (intention to treat), "+
					"never excluded, and each trial record names the problem", as.HelloProblemTrials, as.Trials, arm))
		}
		if b := as.Brief; b != nil && b.NoServedTrials > 0 {
			sum.Notes = append(sum.Notes, fmt.Sprintf(
				"%d of %d %s trial(s) had no non-empty served brief (every compaction unserved or served-empty); "+
					"they are counted as assigned (intention to treat), never excluded", b.NoServedTrials, b.Trials, arm))
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
	seen := map[string]bool{}
	for _, t := range trials {
		if t.HostModel != "" && !seen[t.HostModel] {
			seen[t.HostModel] = true
			sum.HostModels = append(sum.HostModels, t.HostModel)
		}
	}
	sort.Strings(sum.HostModels)
	for _, t := range trials {
		if t.PreregisteredModel && t.Model != "" && t.Model != a.Model {
			sum.Notes = append(sum.Notes, fmt.Sprintf(
				"the trials ran on %s, the pre-registration's contingency alias for %s, which the hosts resolved "+
					"to %s (preregistration section 3)", t.Model, a.Model, orNone(sum.HostModels)))
			break
		}
	}
	return sum
}

// StopEarly records that the run stopped after ran of its planned trials, and why. The
// pre-registered rule analyses every planned trial (intention to treat); the ones that never ran
// cannot be analysed, so a stopped run reaches no verdict whatever its partial numbers say
// (preregistration amendment A4). Its intervals stay in the summary, as a description.
func (s *LiveSummary) StopEarly(ran, planned int, why string) {
	s.Decision = LiveDecision{
		Verdict: "not-applicable",
		Reason: fmt.Sprintf("the run stopped after %d of the %d planned trials; the pre-registered rule analyses "+
			"every planned trial", ran, planned),
	}
	s.Notes = append(s.Notes, "the run was stopped early: "+why)
	s.interimStageA(fmt.Sprintf("no verdict: the run stopped after %d of the %d planned trials", ran, planned))
}

// Pause records that the run paused cleanly after ran of its planned trials, and why (W6 item 8):
// it reaches no verdict yet, and `devtool live-eval --resume` continues the same plan, whose
// verdict is then computed over every session's trials.
func (s *LiveSummary) Pause(ran, planned int, why string) {
	s.Decision = LiveDecision{
		Verdict: "not-applicable",
		Reason: fmt.Sprintf("the run paused after %d of the %d planned trials; it continues with `devtool live-eval "+
			"--resume <run-dir>`, and the verdict is computed over every session's trials", ran, planned),
	}
	s.Paused = why
	s.Notes = append(s.Notes, "the run paused cleanly: "+why)
	s.interimStageA(fmt.Sprintf("no verdict: the run paused after %d of the %d planned trials", ran, planned))
}

// AddLimited records the limited trials of a run whose scored trials are scored (W6 item 8): their
// count per arm, and the sensitivity analysis that counts each as a failure on every outcome. The
// two-arm primary analysis and its decision are untouched; a Stage A run is decided again with the
// limited trials, whose partial C and T the efficiency guards' limited sensitivity adds to their slots
// (amendment 4). Call it before StopEarly or Pause, which then mark the decision interim.
func (s *LiveSummary) AddLimited(a LiveAnalysis, scored, limited []LiveTrial) {
	if len(limited) == 0 {
		return
	}
	s.LimitedTrials = map[string]int{}
	with := append([]LiveTrial{}, scored...)
	for _, t := range limited {
		s.LimitedTrials[t.Arm]++
		t.HarnessError = joinNonEmpty(t.HarnessError, "limited: "+t.Limited)
		t.TaskSuccess, t.Completed = false, false
		if t.Recovered != nil {
			failed := false
			t.Recovered = &failed
		}
		if t.ConstraintClean != nil {
			failed := false
			t.ConstraintClean = &failed
		}
		with = append(with, t)
	}
	s.LimitedSensitivity = summarizeLive(s.RunID, a, with).Arms
	if s.StageA != nil {
		// Amendment 4's limited sensitivity adds each limited trial's partial C and T to its slot's re-run;
		// a gated efficiency guard it flips reads "indeterminate".
		s.StageA = DecideStageA(a, scored, limited)
		s.Decision = s.StageA.Decision
	}
	var parts []string
	for _, arm := range slices.Sorted(maps.Keys(s.LimitedTrials)) {
		sens := s.LimitedSensitivity[arm]
		parts = append(parts, fmt.Sprintf("%d %s (task success with them as failures %d/%d, recovery %d/%d)",
			s.LimitedTrials[arm], arm, sens.TaskSuccess.K, sens.TaskSuccess.N, sens.Recovery.K, sens.Recovery.N))
	}
	s.Notes = append(s.Notes, "limited trials, set aside and run again, excluded from the analysis above: "+
		strings.Join(parts, "; ")+". The sensitivity analysis counting them as failures is reported, not decided")
}

func joinNonEmpty(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
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
// were not the arms the rule is about. The reason names every such arm, in arm order, so it reads
// the same on every run (D53(a)). A trial's plugin state is the plugin list its host reported
// at start-up; a trial whose host reported none is a harness failure, scored as a failure on every
// outcome like any other (preregistration section 8 and amendment A2), not a mismatch.
func DecideLive(a LiveAnalysis, s LiveSummary) LiveDecision {
	var mismatched []string
	for _, name := range slices.Sorted(maps.Keys(s.Arms)) {
		if arm := s.Arms[name]; arm.PluginMismatch > 0 {
			mismatched = append(mismatched, fmt.Sprintf("%d %s", arm.PluginMismatch, arm.Arm))
		}
	}
	if len(mismatched) > 0 {
		return LiveDecision{
			Verdict: "not-applicable",
			Reason:  strings.Join(mismatched, " and ") + " trial(s) ran with the wrong plugin state",
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
func summarizeGroups(g map[string]map[string][]LiveTrial, rules armRules) map[string][]ArmSummary {
	out := make(map[string][]ArmSummary, len(g))
	for key, byArm := range g {
		arms := make([]string, 0, len(byArm))
		for arm := range byArm {
			arms = append(arms, arm)
		}
		sort.Strings(arms)
		for _, arm := range arms {
			out[key] = append(out[key], summarizeArm(arm, byArm[arm], rules))
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
//
// Its cost mean is over the trials whose cost C is known, a lower bound included and counted: a trial
// with no result line has no cost to add, and adding it as zero would understate the arm (amendment 4).
func summarizeArm(arm string, ts []LiveTrial, rules armRules) ArmSummary {
	z := rules.z
	out := ArmSummary{Arm: arm, Trials: len(ts)}
	var taskK, cleanK, cleanN, recK, recN int
	var usage UsageTotals
	var cost float64
	var est, wall, store int64
	var storeN, costN int
	var hooks []float64
	hostHooks := map[string][]int64{}
	rateUse := map[string][]float64{}
	for _, t := range ts {
		for w, d := range t.RateUse {
			if d.Delta != nil {
				rateUse[w] = append(rateUse[w], *d.Delta)
			}
		}
		// A trial the harness could not run as designed is never complete, whatever its steps say.
		if t.Completed && t.HarnessError == "" {
			out.Completed++
		}
		if t.pluginMismatch() {
			out.PluginMismatch++
		}
		harness := t.HarnessError != ""
		if t.TaskSuccess && !harness {
			taskK++
		}
		switch {
		case !rules.declaredConstraint:
			cleanN++
			if t.ConstraintViolations == 0 && !harness {
				cleanK++
			}
		case t.ConstraintClean != nil:
			cleanN++
			if *t.ConstraintClean && !harness {
				cleanK++
			}
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
		switch c, status := t.Cost(); status {
		case LiveCostUnknown:
			out.CostUnknown++
		case LiveCostPartial:
			out.CostPartial++
			cost += c
			costN++
		default:
			cost += c
			costN++
		}
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
		if t.Hello != nil && !t.Hello.OK {
			out.HelloProblemTrials++
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
		for name, ms := range t.TranscriptHookMS {
			hostHooks[name] = append(hostHooks[name], ms...)
		}
	}
	for name, ms := range hostHooks {
		if len(ms) == 0 {
			continue
		}
		sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
		if out.HostHookMS == nil {
			out.HostHookMS = map[string]HookLatency{}
		}
		out.HostHookMS[name] = HookLatency{
			N: len(ms), P50: nearestRank(ms, 50), P95: nearestRank(ms, 95), Max: ms[len(ms)-1],
		}
	}
	for w, ds := range rateUse {
		st := LiveRateUseStats{Trials: len(ds), Max: ds[0]}
		total := 0.0
		for _, d := range ds {
			total += d
			st.Max = math.Max(st.Max, d)
		}
		st.Mean = total / float64(len(ds))
		if out.RateUse == nil {
			out.RateUse = map[string]LiveRateUseStats{}
		}
		out.RateUse[w] = st
	}
	out.TaskSuccess = wilson(taskK, len(ts), z)
	out.ConstraintClean = wilson(cleanK, cleanN, z)
	out.Recovery = wilson(recK, recN, z)
	if n := int64(len(ts)); n > 0 {
		out.MeanUsage = UsageTotals{
			Input: usage.Input / n, Output: usage.Output / n,
			CacheRead: usage.CacheRead / n, CacheWrite: usage.CacheWrite / n,
		}
		out.MeanEstimateMc = est / n
		out.MeanWallMS = wall / n
	}
	if costN > 0 {
		out.MeanHostCost = cost / float64(costN)
	}
	out.Efficiency = armEfficiency(ts, rules.capMS)
	if storeN > 0 {
		out.MeanStoreBytes = store / int64(storeN)
	}
	if out.ForeignPluginTrials > 0 {
		out.ForeignPlugins = foreignPlugins(ts, arm)
	}
	out.Brief = liveBriefStats(ts)
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

// nearestRank is the nearest-rank percentile of a non-empty ascending slice.
func nearestRank(sorted []int64, p float64) int64 {
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	rank = max(rank, 1)
	rank = min(rank, len(sorted))
	return sorted[rank-1]
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

// orNone joins the models the hosts reported, or says that none was recorded.
func orNone(models []string) string {
	if len(models) == 0 {
		return "a model no trial recorded"
	}
	return strings.Join(models, ", ")
}

// interimStageA marks a stopped or paused run's Stage A record as interim (review I-2): its decision
// follows the summary's not-applicable one, and its outcome row says no verdict was reached, so neither
// the record nor its renderings can read as a decided run. The tests stay, as an interim description.
func (s *LiveSummary) interimStageA(outcome string) {
	if s.StageA == nil {
		return
	}
	s.StageA.Decision = s.Decision
	s.StageA.Outcome = outcome + "; the tests below are interim, not decided"
}
