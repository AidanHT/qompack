package eval

// ── The live task set: what a real-host trial is asked to do, and how it is graded ──────────────
//
// V6 close-out C5.4. A task is a small repository fixture, a sequence of user messages with at
// least one forced compaction among them, and checks a program can grade after the session ends:
// files on disk, a command's exit status, and the text of named answers. Nothing is graded by a
// model. The file is versioned and hashed into every run's plan, so a task set cannot change after
// outcomes are seen without the change being visible.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// LiveTaskSetVersion is the task-set format version LoadLiveTaskSet accepts.
const LiveTaskSetVersion = 1

// The three outcome classes a check reports into. They are the pre-registered primary outcomes:
// whether the work came out right after the compaction, whether a stated constraint was broken,
// and whether a pre-compaction fact was recovered.
const (
	OutcomeTask       = "task"
	OutcomeConstraint = "constraint"
	OutcomeRecovery   = "recovery"
)

// The check kinds.
const (
	CheckFileMatches      = "file_matches"
	CheckFileNotMatches   = "file_not_matches"
	CheckFileExists       = "file_exists"
	CheckFileAbsent       = "file_absent"
	CheckFileUnchanged    = "file_unchanged"
	CheckFileLines        = "file_lines"
	CheckAnswerMatches    = "answer_matches"
	CheckAnswerNotMatches = "answer_not_matches"
	CheckCommand          = "command"
	// CheckToolNotUsedAfter fails when any main-loop tool call made after the named compaction step
	// matches Pattern, matched against "<tool name> <input JSON>". It is how a task says "without
	// re-running the probe": a recovered fact that was re-derived is not a recovered fact.
	CheckToolNotUsedAfter = "tool_not_used_after"
)

// liveCheckCommands is the only programs a command check may run. A task file is data, and a check
// that could name any program would make a task file a script.
var liveCheckCommands = map[string]bool{"go": true}

// liveIDPattern bounds every identifier a task file declares, because identifiers become directory
// names in a run's output.
var liveIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// LiveTaskSet is one declared task set.
type LiveTaskSet struct {
	Version     int    `json:"version"`
	ID          string `json:"id"`
	Description string `json:"description"`
	// Defaults applies to every task that does not override it.
	Defaults LiveTaskDefaults `json:"defaults"`
	// Analysis is the pre-registered decision rule's parameters. They live in the hashed task file
	// so they cannot move after outcomes exist.
	Analysis LiveAnalysis `json:"analysis"`
	Tasks    []LiveTask   `json:"tasks"`
}

// LiveTaskDefaults is the per-task settings every task inherits.
type LiveTaskDefaults struct {
	// MaxTurns is passed to the host as --max-turns.
	MaxTurns int `json:"max_turns"`
	// AllowedTools is passed to the host as --allowedTools.
	AllowedTools []string `json:"allowed_tools"`
}

// LiveAnalysis is the decision rule's parameters.
type LiveAnalysis struct {
	// Model is the pre-registered model ID every confirmatory trial runs on.
	Model string `json:"model"`
	// Confidence is the two-sided confidence level of every interval, e.g. 0.95.
	Confidence float64 `json:"confidence"`
	// NonInferiorityMargin is how far below stock Qompack's task-success rate may sit and still be
	// called non-inferior, as a proportion.
	NonInferiorityMargin float64 `json:"non_inferiority_margin"`
	// TrialsPerArm is the pre-registered number of trials per task per arm, one count for every arm.
	// A four-arm set gives ArmTrialsPerTask instead; a set gives exactly one of the two.
	TrialsPerArm int `json:"trials_per_arm,omitempty"`
	// ArmTrialsPerTask is the pre-registered trials per task of each Stage A arm, by arm name. It
	// lives here, in the hashed task file, so the counts cannot move after outcomes exist.
	ArmTrialsPerTask map[string]int `json:"arm_trials_per_task,omitempty"`
	// GenericCompactInstructions is text G: what the generic arm's CLAUDE.md carries under its
	// `# Compact instructions` heading. It lives here so the task-set hash covers it.
	GenericCompactInstructions string `json:"generic_compact_instructions,omitempty"`
	// DeltaH1 and DeltaH2 are the pre-registered minimum intention-to-treat effects on recovery of H1
	// (steer over classic) and H2 (steer over generic), and ICCInt the task-by-arm intraclass
	// correlation the sizing inflates n by (plan §9.3 item 2; the sizing step, W6 item 2).
	DeltaH1 float64 `json:"delta_h1,omitempty"`
	DeltaH2 float64 `json:"delta_h2,omitempty"`
	ICCInt  float64 `json:"icc_int,omitempty"`
	// SFallback selects S's pre-registered fallback, the one-sided 80 % lower bound, used only if the
	// owner declined the S-sized run (plan §9.3 item 3, §9.4).
	SFallback bool `json:"s_fallback,omitempty"`
	// Efficiency is amendment 4's pre-registered efficiency block; every four-arm set carries one.
	Efficiency *LiveEfficiency `json:"efficiency,omitempty"`
}

// The efficiency metrics a block may decide (amendment 4).
var liveEfficiencyMetrics = []string{LiveMetricCost, LiveMetricActiveTime}

// LiveEfficiency is amendment 4's efficiency block (plan §9.4, ruling R43): the margin M, the metrics
// decided (cost C and active time T), the guards — treated and comparator arms, gated or a label —,
// whether H3 is a ship condition (an owner option at R2), the pinned step timeout a reached step with
// no result counts as in T, and the pilot's brief redesign ratio.
type LiveEfficiency struct {
	Margin             float64               `json:"margin"`
	Metrics            []string              `json:"metrics"`
	Guards             []LiveEfficiencyGuard `json:"guards"`
	H3ShipCondition    bool                  `json:"h3_ship_condition"`
	StepTimeoutMS      int64                 `json:"step_timeout_ms"`
	PilotRedesignRatio float64               `json:"pilot_redesign_ratio"`
}

// LiveEfficiencyGuard is one guard: "harm shown" when Treated's ratio over Comparator has its lower
// bound above 1 + margin. A gated guard blocks shipping; an ungated one is a label for the docs.
type LiveEfficiencyGuard struct {
	Treated    string `json:"treated"`
	Comparator string `json:"comparator"`
	Gated      bool   `json:"gated"`
}

// liveDefaultStepTimeoutMS is the harness's default --step-timeout, the T cap of a run whose task set
// pins none.
const liveDefaultStepTimeoutMS = int64(10 * 60 * 1000)

// stepTimeoutMS is the step timeout active time counts a reached step with no result as.
func (a LiveAnalysis) stepTimeoutMS() int64 {
	if a.Efficiency != nil && a.Efficiency.StepTimeoutMS > 0 {
		return a.Efficiency.StepTimeoutMS
	}
	return liveDefaultStepTimeoutMS
}

// efficiencyMargin is M, or the plan's 0.10 for reported rows when no block pins it.
func (a LiveAnalysis) efficiencyMargin() float64 {
	if a.Efficiency != nil && a.Efficiency.Margin > 0 {
		return a.Efficiency.Margin
	}
	return 0.10
}

func (e LiveEfficiency) validate() error {
	if !(e.Margin > 0 && e.Margin < 1) {
		return fmt.Errorf("eval: analysis.efficiency.margin %v must lie strictly between 0 and 1", e.Margin)
	}
	if len(e.Metrics) == 0 {
		return fmt.Errorf("eval: analysis.efficiency.metrics names no metric (%v)", liveEfficiencyMetrics)
	}
	seen := map[string]bool{}
	for _, m := range e.Metrics {
		if !slices.Contains(liveEfficiencyMetrics, m) || seen[m] {
			return fmt.Errorf("eval: analysis.efficiency.metrics: %q is not one of %v, or is named twice", m, liveEfficiencyMetrics)
		}
		seen[m] = true
	}
	if len(e.Guards) == 0 {
		return fmt.Errorf("eval: analysis.efficiency.guards names no guard")
	}
	for _, g := range e.Guards {
		_, okT := LiveArmSpec(g.Treated)
		_, okC := LiveArmSpec(g.Comparator)
		if !okT || !okC || g.Treated == g.Comparator {
			return fmt.Errorf("eval: analysis.efficiency.guards: %s vs %s is not a pair of two Stage A arms", g.Treated, g.Comparator)
		}
	}
	if e.StepTimeoutMS <= 0 {
		return fmt.Errorf("eval: analysis.efficiency.step_timeout_ms must be positive")
	}
	if !(e.PilotRedesignRatio > 1) {
		return fmt.Errorf("eval: analysis.efficiency.pilot_redesign_ratio %v must exceed 1", e.PilotRedesignRatio)
	}
	return nil
}

// TrialsFor is arm's pre-registered trials per task: its own count in a four-arm set, the one count
// in a set that gives one, and 0 for an arm a four-arm set does not name.
func (a LiveAnalysis) TrialsFor(arm string) int {
	if a.ArmTrialsPerTask != nil {
		return a.ArmTrialsPerTask[arm]
	}
	return a.TrialsPerArm
}

// LiveTask is one task.
type LiveTask struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Category names the failure mode the task probes.
	Category string `json:"category"`
	// Variant is "base" or "changing-requirement".
	Variant string `json:"variant"`
	// Domain labels the domain the task comes from (W6 item 6): the summary breaks outcomes down by
	// it, reported and not decided. Every task of a four-arm set names one; the two-arm sets carry none.
	Domain string `json:"domain,omitempty"`
	// HeldOut marks a task reserved for the confirmatory run: it is never run while the harness or
	// the product is being tuned.
	HeldOut bool `json:"held_out"`
	// Fixture is the repository the session starts in, relative to the task file's directory.
	Fixture string `json:"fixture"`
	// HiddenFixture, when set, is a directory (relative to the task file) copied over the project
	// AFTER the session and before any check runs: tests the session never saw, which grade the
	// behaviour it produced rather than the tests it wrote for itself.
	HiddenFixture string `json:"hidden_fixture,omitempty"`
	// MaxTurns and AllowedTools override the defaults when set.
	MaxTurns     int          `json:"max_turns,omitempty"`
	AllowedTools []string     `json:"allowed_tools,omitempty"`
	Steps        []LiveStep   `json:"steps"`
	Checks       []LiveCheck  `json:"checks"`
	Notes        string       `json:"notes,omitempty"`
	Expected     LiveExpected `json:"expected,omitempty"`
}

// LiveExpected documents, for a reader, what a correct run produces. Graders never read it.
type LiveExpected struct {
	Facts []string `json:"facts,omitempty"`
}

// LiveStep is one user message, or a forced compaction.
type LiveStep struct {
	ID string `json:"id"`
	// Prompt is the message sent to the host. Empty for a compaction step.
	Prompt string `json:"prompt,omitempty"`
	// Compact makes this step `/compact`.
	Compact bool `json:"compact,omitempty"`
	// CompactInstructions is appended to /compact when set. Both arms receive the same string.
	CompactInstructions string `json:"compact_instructions,omitempty"`
}

// Message is the text the driver sends for this step.
func (s LiveStep) Message() string {
	if !s.Compact {
		return s.Prompt
	}
	if s.CompactInstructions == "" {
		return "/compact"
	}
	return "/compact " + s.CompactInstructions
}

// LiveCheck is one automatic check.
type LiveCheck struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
	Kind    string `json:"kind"`
	// Path is a slash-separated path relative to the trial's project root.
	Path string `json:"path,omitempty"`
	// Pattern is a Go regular expression.
	Pattern string `json:"pattern,omitempty"`
	// Step names the step whose answer an answer check reads.
	Step string `json:"step,omitempty"`
	// Argv is a command check's program and arguments, run in the project root.
	Argv []string `json:"argv,omitempty"`
	// Lines is the exact non-empty line count a file_lines check expects.
	Lines *int `json:"lines,omitempty"`
	// Description says what the check proves, for a reader of the results.
	Description string `json:"description,omitempty"`
}

// EffectiveMaxTurns is the task's max-turns after defaults.
func (t LiveTask) EffectiveMaxTurns(d LiveTaskDefaults) int {
	if t.MaxTurns > 0 {
		return t.MaxTurns
	}
	return d.MaxTurns
}

// EffectiveAllowedTools is the task's allowed tools after defaults.
func (t LiveTask) EffectiveAllowedTools(d LiveTaskDefaults) []string {
	if len(t.AllowedTools) > 0 {
		return t.AllowedTools
	}
	return d.AllowedTools
}

// ParseLiveTaskSet decodes and validates a task set. Unknown fields are rejected so a misspelt
// key cannot silently drop a check.
func ParseLiveTaskSet(raw []byte) (LiveTaskSet, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var ts LiveTaskSet
	if err := dec.Decode(&ts); err != nil {
		return LiveTaskSet{}, fmt.Errorf("eval: parsing task set: %w", err)
	}
	if err := ts.Validate(); err != nil {
		return LiveTaskSet{}, err
	}
	return ts, nil
}

// LoadLiveTaskSet reads a task set through an os.Root at the file's directory, validates it, and
// checks that every fixture (and hidden fixture) it names is a directory beneath that root. It
// returns the raw bytes too, so a run can hash exactly what it was given.
func LoadLiveTaskSet(file string) (LiveTaskSet, []byte, error) {
	root, err := os.OpenRoot(filepath.Dir(file))
	if err != nil {
		return LiveTaskSet{}, nil, fmt.Errorf("eval: opening the task set's directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	raw, err := root.ReadFile(filepath.Base(file))
	if err != nil {
		return LiveTaskSet{}, nil, fmt.Errorf("eval: reading task set %s: %w", file, err)
	}
	ts, err := ParseLiveTaskSet(raw)
	if err != nil {
		return LiveTaskSet{}, nil, fmt.Errorf("%s: %w", file, err)
	}
	for _, t := range ts.Tasks {
		for _, dir := range []string{t.Fixture, t.HiddenFixture} {
			if dir == "" {
				continue
			}
			info, statErr := root.Stat(filepath.FromSlash(dir))
			if statErr != nil || !info.IsDir() {
				return LiveTaskSet{}, nil, fmt.Errorf("eval: task %s: fixture %s is not a directory beneath %s",
					t.ID, dir, filepath.Dir(file))
			}
		}
	}
	return ts, raw, nil
}

// FixtureDir resolves a task's fixture against the task file's path.
func (t LiveTask) FixtureDir(taskFile string) string {
	return filepath.Join(filepath.Dir(taskFile), filepath.FromSlash(t.Fixture))
}

// Validate checks every rule a task set must satisfy before any session is spent on it.
func (ts LiveTaskSet) Validate() error {
	if ts.Version != LiveTaskSetVersion {
		return fmt.Errorf("eval: task set version %d, want %d", ts.Version, LiveTaskSetVersion)
	}
	if !liveIDPattern.MatchString(ts.ID) {
		return fmt.Errorf("eval: task set id %q is not a plain identifier", ts.ID)
	}
	if ts.Defaults.MaxTurns <= 0 {
		return fmt.Errorf("eval: task set defaults.max_turns must be positive")
	}
	if err := ts.Analysis.validate(); err != nil {
		return err
	}
	if len(ts.Tasks) == 0 {
		return fmt.Errorf("eval: task set %s declares no tasks", ts.ID)
	}
	seen := map[string]bool{}
	for _, t := range ts.Tasks {
		if seen[t.ID] {
			return fmt.Errorf("eval: task id %q is declared twice", t.ID)
		}
		seen[t.ID] = true
		if err := t.validate(); err != nil {
			return fmt.Errorf("eval: task %s: %w", t.ID, err)
		}
		if ts.Analysis.fourArm() && !t.hasRecoveryCheck() {
			// Stage A decides on recovery, so every trial must carry a recovery verdict (W6 item 3).
			return fmt.Errorf("eval: task %s declares no recovery check; every task of a four-arm set needs one", t.ID)
		}
		if ts.Analysis.fourArm() && t.Domain == "" {
			return fmt.Errorf("eval: task %s declares no domain; every task of a four-arm set names one (W6 item 6)", t.ID)
		}
	}
	return nil
}

// hasRecoveryCheck reports that the task declares at least one recovery check.
func (t LiveTask) hasRecoveryCheck() bool {
	for _, c := range t.Checks {
		if c.Outcome == OutcomeRecovery {
			return true
		}
	}
	return false
}

func (a LiveAnalysis) validate() error {
	if !(a.Confidence > 0 && a.Confidence < 1) {
		return fmt.Errorf("eval: analysis.confidence %v must lie strictly between 0 and 1", a.Confidence)
	}
	if !(a.NonInferiorityMargin >= 0 && a.NonInferiorityMargin < 1) {
		return fmt.Errorf("eval: analysis.non_inferiority_margin %v must lie in [0, 1)", a.NonInferiorityMargin)
	}
	if strings.TrimSpace(a.Model) == "" {
		return fmt.Errorf("eval: analysis.model must name the pre-registered model")
	}
	switch {
	case a.TrialsPerArm < 0:
		return fmt.Errorf("eval: analysis.trials_per_arm must be positive")
	case a.TrialsPerArm == 0 && len(a.ArmTrialsPerTask) == 0:
		return fmt.Errorf("eval: analysis needs trials_per_arm or arm_trials_per_task")
	case a.TrialsPerArm > 0 && a.ArmTrialsPerTask != nil:
		return fmt.Errorf("eval: analysis gives trials_per_arm or arm_trials_per_task, not both")
	}
	for _, arm := range keysOfInt(a.ArmTrialsPerTask) {
		if _, ok := LiveArmSpec(arm); !ok {
			return fmt.Errorf("eval: analysis.arm_trials_per_task names %q, which is not one of %v", arm, LiveArms)
		}
		if a.ArmTrialsPerTask[arm] <= 0 {
			return fmt.Errorf("eval: analysis.arm_trials_per_task[%s] must be positive", arm)
		}
	}
	if a.ArmTrialsPerTask[ArmGeneric] > 0 && strings.TrimSpace(a.GenericCompactInstructions) == "" {
		return fmt.Errorf("eval: the generic arm needs analysis.generic_compact_instructions (text G)")
	}
	for _, v := range []struct {
		name string
		val  float64
	}{{"delta_h1", a.DeltaH1}, {"delta_h2", a.DeltaH2}} {
		if v.val < 0 || v.val >= 1 {
			return fmt.Errorf("eval: analysis.%s %v must lie in [0, 1)", v.name, v.val)
		}
	}
	if a.ICCInt < 0 || a.ICCInt >= 1 {
		return fmt.Errorf("eval: analysis.icc_int %v must lie in [0, 1)", a.ICCInt)
	}
	switch {
	case a.Efficiency != nil:
		return a.Efficiency.validate()
	case a.fourArm():
		return fmt.Errorf("eval: a four-arm set pre-registers analysis.efficiency (amendment 4)")
	}
	return nil
}

// fourArm reports that the set is in the Stage A shape, with per-arm trials.
func (a LiveAnalysis) fourArm() bool { return len(a.ArmTrialsPerTask) > 0 }

func (t LiveTask) validate() error {
	if !liveIDPattern.MatchString(t.ID) {
		return fmt.Errorf("id is not a plain identifier")
	}
	switch t.Variant {
	case "base", "changing-requirement":
	default:
		return fmt.Errorf("variant %q is neither base nor changing-requirement", t.Variant)
	}
	if strings.TrimSpace(t.Category) == "" {
		return fmt.Errorf("category is empty")
	}
	if t.Domain != "" && !liveIDPattern.MatchString(t.Domain) {
		return fmt.Errorf("domain %q is not a plain identifier", t.Domain)
	}
	if err := cleanRelative(t.Fixture); err != nil {
		return fmt.Errorf("fixture: %w", err)
	}
	if t.HiddenFixture != "" {
		if err := cleanRelative(t.HiddenFixture); err != nil {
			return fmt.Errorf("hidden_fixture: %w", err)
		}
	}
	steps, compacts, err := t.validateSteps()
	if err != nil {
		return err
	}
	if len(t.Checks) == 0 {
		return fmt.Errorf("declares no checks")
	}
	ids := map[string]bool{}
	for _, c := range t.Checks {
		if ids[c.ID] {
			return fmt.Errorf("check id %q is declared twice", c.ID)
		}
		ids[c.ID] = true
		if err := c.validate(steps, compacts); err != nil {
			return fmt.Errorf("check %s: %w", c.ID, err)
		}
	}
	return nil
}

// validateSteps enforces the shape every trial needs: work before a compaction, the compaction,
// and work after it. It returns the prompt steps an answer check may name and the compaction steps
// a tool_not_used_after check may name.
func (t LiveTask) validateSteps() (prompts, compacts map[string]bool, err error) {
	prompts, compacts = map[string]bool{}, map[string]bool{}
	seen := map[string]bool{}
	firstCompact, lastCompact := -1, -1
	for i, s := range t.Steps {
		if !liveIDPattern.MatchString(s.ID) {
			return nil, nil, fmt.Errorf("step %d id %q is not a plain identifier", i, s.ID)
		}
		if seen[s.ID] {
			return nil, nil, fmt.Errorf("step id %q is declared twice", s.ID)
		}
		seen[s.ID] = true
		if s.Compact {
			if s.Prompt != "" {
				return nil, nil, fmt.Errorf("step %s is a compaction and also carries a prompt", s.ID)
			}
			if strings.ContainsAny(s.CompactInstructions, "\r\n") {
				return nil, nil, fmt.Errorf("step %s: compact_instructions must be one line", s.ID)
			}
			compacts[s.ID] = true
			if firstCompact < 0 {
				firstCompact = i
			}
			lastCompact = i
			continue
		}
		if strings.TrimSpace(s.Prompt) == "" {
			return nil, nil, fmt.Errorf("step %s has no prompt", s.ID)
		}
		if strings.HasPrefix(strings.TrimSpace(s.Prompt), "/") {
			return nil, nil, fmt.Errorf("step %s: a prompt may not start with '/'; compaction is declared with compact", s.ID)
		}
		prompts[s.ID] = true
	}
	if firstCompact < 0 {
		return nil, nil, fmt.Errorf("declares no compaction step")
	}
	if firstCompact == 0 {
		return nil, nil, fmt.Errorf("the first step is a compaction; there is nothing to compact")
	}
	if lastCompact == len(t.Steps)-1 {
		return nil, nil, fmt.Errorf("the last step is a compaction; nothing after it is graded")
	}
	return prompts, compacts, nil
}

func (c LiveCheck) validate(prompts, compacts map[string]bool) error {
	if !liveIDPattern.MatchString(c.ID) {
		return fmt.Errorf("id is not a plain identifier")
	}
	switch c.Outcome {
	case OutcomeTask, OutcomeConstraint, OutcomeRecovery:
	default:
		return fmt.Errorf("outcome %q is not task, constraint or recovery", c.Outcome)
	}
	needPath := func() error { return cleanRelative(c.Path) }
	needPattern := func() error {
		if c.Pattern == "" {
			return fmt.Errorf("%s needs a pattern", c.Kind)
		}
		_, err := regexp.Compile(c.Pattern)
		return err
	}
	switch c.Kind {
	case CheckFileMatches, CheckFileNotMatches:
		if err := needPath(); err != nil {
			return err
		}
		return needPattern()
	case CheckFileExists, CheckFileAbsent, CheckFileUnchanged:
		return needPath()
	case CheckFileLines:
		if err := needPath(); err != nil {
			return err
		}
		if c.Lines == nil || *c.Lines < 0 {
			return fmt.Errorf("file_lines needs a non-negative lines value")
		}
		return nil
	case CheckAnswerMatches, CheckAnswerNotMatches:
		if !prompts[c.Step] {
			return fmt.Errorf("step %q is not a prompt step of this task", c.Step)
		}
		return needPattern()
	case CheckCommand:
		if len(c.Argv) == 0 || !liveCheckCommands[c.Argv[0]] {
			return fmt.Errorf("command checks may run only %v", keysOfBool(liveCheckCommands))
		}
		return nil
	case CheckToolNotUsedAfter:
		if !compacts[c.Step] {
			return fmt.Errorf("step %q is not a compaction step of this task", c.Step)
		}
		return needPattern()
	default:
		return fmt.Errorf("unknown kind %q", c.Kind)
	}
}

// cleanRelative requires a non-empty, slash-separated path that stays beneath its root.
func cleanRelative(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if strings.Contains(p, `\`) || path.IsAbs(p) || filepath.IsAbs(p) || filepath.VolumeName(p) != "" {
		return fmt.Errorf("path %q must be relative and slash-separated", p)
	}
	if c := path.Clean(p); c != p || c == ".." || strings.HasPrefix(c, "../") {
		return fmt.Errorf("path %q must be clean and stay beneath its root", p)
	}
	return nil
}

// keysOfBool lists m's keys in sorted order, so an error message naming them reads the same on
// every run (D53(a)).
func keysOfBool(m map[string]bool) []string {
	return slices.Sorted(maps.Keys(m))
}

// keysOfInt lists m's keys in sorted order.
func keysOfInt(m map[string]int) []string {
	return slices.Sorted(maps.Keys(m))
}
