package eval

// ── The live-check suite: repeatable real-host checks (V7 W6 item 11) ───────────────────────────
//
// `devtool live-check` turns the repeatable Phase 0 checks into a benchmark that can be re-run, for
// example after each Claude Code update. A versioned suite file names, per check, the probe (a
// plugin directory, or one of the run's bundles), the fixture, the steps, the repeat count per tier,
// what is recorded and the pass rule or metric. This file holds the pure parts: the suite format and
// its validation, grading one run from the host's stream, a check's summary at a tier, the results
// and manifest documents (each in a fixed, versioned schema) and the side-by-side comparison. The
// driver in tools/devtool runs the sessions.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// LiveCheckSuiteVersion is the suite format LoadLiveCheckSuite accepts.
const LiveCheckSuiteVersion = 1

// LiveCheckResultsSchema and LiveCheckManifestSchema are the format versions of a live-check run's
// results.json and manifest.json.
const (
	LiveCheckResultsSchema  = 1
	LiveCheckManifestSchema = 1
)

// The tiers: quick runs every check once, pass or fail; metrics repeats the checks whose outcome
// depends on the model and reports rates with intervals.
const (
	LiveCheckTierQuick   = "quick"
	LiveCheckTierMetrics = "metrics"
)

// The probes a check may load besides a plugin directory in the suite's tree: the run's --bundle or
// --bundle-mods.
const (
	LiveCheckProbeBundle     = "bundle"
	LiveCheckProbeBundleMods = "bundle-mods"
)

// The stream-level pass conditions; every LiveCheck kind but file_unchanged is accepted too.
const (
	// CheckCompleted: every step ended in a non-error result and every compaction step compacted.
	CheckCompleted = "completed"
	// CheckPluginLoaded: the host's init line lists the named plugin as loaded.
	CheckPluginLoaded = "plugin_loaded"
	// CheckStreamLines: the stream carries at least Min lines of Line ("type" or "type/subtype").
	CheckStreamLines = "stream_lines"
)

// liveCheckRecords is what a check may ask to be recorded; "files:<glob>" records the size of every
// project file matching the glob after the session.
var liveCheckRecords = map[string]bool{
	"host_model": true, "claude_code_version": true, "plugins": true, "compactions": true, "compact_attempts": true,
	"hooks": true, "rate_limits": true, "stream_lines": true,
}

// LiveCheckFilesRecord prefixes a record entry naming a project glob.
const LiveCheckFilesRecord = "files:"

// The metrics a check may report per run instead of, or beside, a pass rule.
const (
	LiveMetricRateLimitEvents   = "rate_limit_events"
	LiveMetricCompactions       = "compactions"
	LiveMetricCompactPostTokens = "compact_post_tokens"
	LiveMetricWallMS            = "wall_ms"
)

var liveCheckMetrics = map[string]bool{
	LiveMetricRateLimitEvents: true, LiveMetricCompactions: true, LiveMetricCompactPostTokens: true, LiveMetricWallMS: true,
}

var liveEnvName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// LiveCheckSuite is one versioned check-suite file.
type LiveCheckSuite struct {
	Version     int    `json:"version"`
	ID          string `json:"id"`
	Description string `json:"description"`
	// Model is every check's model unless the check names its own.
	Model    string           `json:"model"`
	Defaults LiveTaskDefaults `json:"defaults"`
	Checks   []LiveSuiteCheck `json:"checks"`
}

// LiveSuiteCheck is one check.
type LiveSuiteCheck struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Probe is the plugin the session loads by --plugin-dir: a directory relative to the suite file,
	// LiveCheckProbeBundle or LiveCheckProbeBundleMods for the run's bundles, or empty for none.
	Probe string `json:"probe,omitempty"`
	// Fixture is the project the session starts in, relative to the suite file.
	Fixture      string   `json:"fixture"`
	Model        string   `json:"model,omitempty"`
	MaxTurns     int      `json:"max_turns,omitempty"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// Env is further session environment the check sets (a mod switch, say).
	Env   map[string]string `json:"env,omitempty"`
	Steps []LiveStep        `json:"steps"`
	// Repeat is the runs per tier.
	Repeat LiveSuiteRepeat `json:"repeat"`
	// ModelDependent marks a check whose outcome depends on the model: the metrics tier repeats it.
	ModelDependent bool                 `json:"model_dependent,omitempty"`
	Record         []string             `json:"record"`
	Pass           []LiveSuiteCondition `json:"pass,omitempty"`
	Metric         string               `json:"metric,omitempty"`
}

// LiveSuiteRepeat is a check's runs per tier.
type LiveSuiteRepeat struct {
	Quick   int `json:"quick"`
	Metrics int `json:"metrics"`
}

// LiveSuiteCondition is one pass condition: a LiveCheck kind, with that kind's fields, or one of
// the stream conditions.
type LiveSuiteCondition struct {
	Kind    string   `json:"kind"`
	Path    string   `json:"path,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
	Step    string   `json:"step,omitempty"`
	Argv    []string `json:"argv,omitempty"`
	Lines   *int     `json:"lines,omitempty"`
	// Line and Min are a stream_lines condition's line kind and least count; Name a plugin_loaded's
	// plugin.
	Line        string `json:"line,omitempty"`
	Min         int    `json:"min,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// Runs is how many times the check runs at tier.
func (c LiveSuiteCheck) Runs(tier string) int {
	if tier == LiveCheckTierMetrics {
		return c.Repeat.Metrics
	}
	return c.Repeat.Quick
}

// EffectiveModel is the check's model, else the suite's.
func (c LiveSuiteCheck) EffectiveModel(s LiveCheckSuite) string {
	if c.Model != "" {
		return c.Model
	}
	return s.Model
}

// FixtureDir resolves the check's fixture against the suite file's path.
func (c LiveSuiteCheck) FixtureDir(suiteFile string) string {
	return filepath.Join(filepath.Dir(suiteFile), filepath.FromSlash(c.Fixture))
}

// Task is the check as a LiveTask: its steps and turn limits, and its LiveCheck-kind conditions as
// task checks, so the session driver and the grader of live-eval serve it unchanged.
func (c LiveSuiteCheck) Task(s LiveCheckSuite) LiveTask {
	t := LiveTask{ID: c.ID, Title: c.Title, Fixture: c.Fixture, MaxTurns: c.MaxTurns, AllowedTools: c.AllowedTools, Steps: c.Steps}
	if t.MaxTurns == 0 {
		t.MaxTurns = s.Defaults.MaxTurns
	}
	if len(t.AllowedTools) == 0 {
		t.AllowedTools = s.Defaults.AllowedTools
	}
	for i, p := range c.Pass {
		if lc, ok := p.liveCheck(i); ok {
			t.Checks = append(t.Checks, lc)
		}
	}
	return t
}

// liveCheck is the condition as a task check, when it is one of the LiveCheck kinds.
func (p LiveSuiteCondition) liveCheck(i int) (LiveCheck, bool) {
	switch p.Kind {
	case CheckCompleted, CheckPluginLoaded, CheckStreamLines:
		return LiveCheck{}, false
	}
	return LiveCheck{
		ID: fmt.Sprintf("pass-%d", i+1), Outcome: OutcomeTask, Kind: p.Kind, Path: p.Path, Pattern: p.Pattern,
		Step: p.Step, Argv: p.Argv, Lines: p.Lines, Description: p.Description,
	}, true
}

// ParseLiveCheckSuite decodes and validates a suite. Unknown fields are refused, so a misspelt key
// cannot silently drop a condition.
func ParseLiveCheckSuite(raw []byte) (LiveCheckSuite, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s LiveCheckSuite
	if err := dec.Decode(&s); err != nil {
		return LiveCheckSuite{}, fmt.Errorf("eval: parsing the check suite: %w", err)
	}
	if err := s.Validate(); err != nil {
		return LiveCheckSuite{}, err
	}
	return s, nil
}

// LoadLiveCheckSuite reads and validates a suite file and checks that every fixture and probe
// directory it names exists beneath the file's directory. It returns the raw bytes, so a run can
// hash exactly what it read.
func LoadLiveCheckSuite(file string) (LiveCheckSuite, []byte, error) {
	root, err := os.OpenRoot(filepath.Dir(file))
	if err != nil {
		return LiveCheckSuite{}, nil, fmt.Errorf("eval: opening the check suite's directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	raw, err := root.ReadFile(filepath.Base(file))
	if err != nil {
		return LiveCheckSuite{}, nil, fmt.Errorf("eval: reading check suite %s: %w", file, err)
	}
	s, err := ParseLiveCheckSuite(raw)
	if err != nil {
		return LiveCheckSuite{}, nil, fmt.Errorf("%s: %w", file, err)
	}
	for _, c := range s.Checks {
		dirs := []string{c.Fixture}
		if c.Probe != "" && c.Probe != LiveCheckProbeBundle && c.Probe != LiveCheckProbeBundleMods {
			dirs = append(dirs, c.Probe)
		}
		for _, d := range dirs {
			if info, err := root.Stat(filepath.FromSlash(d)); err != nil || !info.IsDir() {
				return LiveCheckSuite{}, nil, fmt.Errorf("eval: check %s: %s is not a directory beneath %s", c.ID, d, filepath.Dir(file))
			}
		}
	}
	return s, raw, nil
}

// Validate checks every rule a suite must satisfy before a session is spent on it.
func (s LiveCheckSuite) Validate() error {
	if s.Version != LiveCheckSuiteVersion {
		return fmt.Errorf("eval: check suite version %d, want %d", s.Version, LiveCheckSuiteVersion)
	}
	if !liveIDPattern.MatchString(s.ID) {
		return fmt.Errorf("eval: check suite id %q is not a plain identifier", s.ID)
	}
	if s.Defaults.MaxTurns <= 0 {
		return fmt.Errorf("eval: check suite defaults.max_turns must be positive")
	}
	if len(s.Checks) == 0 {
		return fmt.Errorf("eval: check suite %s declares no checks", s.ID)
	}
	seen := map[string]bool{}
	for _, c := range s.Checks {
		if seen[c.ID] {
			return fmt.Errorf("eval: check id %q is declared twice", c.ID)
		}
		seen[c.ID] = true
		if c.EffectiveModel(s) == "" {
			return fmt.Errorf("eval: check %s names no model and the suite has none", c.ID)
		}
		if err := c.validate(); err != nil {
			return fmt.Errorf("eval: check %s: %w", c.ID, err)
		}
	}
	return nil
}

func (c LiveSuiteCheck) validate() error {
	if !liveIDPattern.MatchString(c.ID) {
		return fmt.Errorf("id is not a plain identifier")
	}
	if strings.TrimSpace(c.Title) == "" {
		return fmt.Errorf("title is empty")
	}
	if err := cleanRelative(c.Fixture); err != nil {
		return fmt.Errorf("fixture: %w", err)
	}
	switch c.Probe {
	case "", LiveCheckProbeBundle, LiveCheckProbeBundleMods:
	default:
		if err := cleanRelative(c.Probe); err != nil {
			return fmt.Errorf("probe: %w", err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.Env)) {
		if !liveEnvName.MatchString(name) {
			return fmt.Errorf("env name %q is not an upper-case environment variable name", name)
		}
	}
	prompts, compacts := map[string]bool{}, map[string]bool{}
	if len(c.Steps) == 0 {
		return fmt.Errorf("declares no steps")
	}
	for i, st := range c.Steps {
		if !liveIDPattern.MatchString(st.ID) || prompts[st.ID] || compacts[st.ID] {
			return fmt.Errorf("step %d id %q is not a unique plain identifier", i, st.ID)
		}
		if st.Compact {
			if st.Prompt != "" || strings.ContainsAny(st.CompactInstructions, "\r\n") {
				return fmt.Errorf("step %s: a compaction carries no prompt and one line of instructions", st.ID)
			}
			compacts[st.ID] = true
			continue
		}
		if p := strings.TrimSpace(st.Prompt); p == "" || strings.HasPrefix(p, "/") {
			return fmt.Errorf("step %s needs a prompt that does not start with '/'", st.ID)
		}
		prompts[st.ID] = true
	}
	switch {
	case c.Repeat.Quick != 1:
		return fmt.Errorf("repeat.quick must be 1: the quick tier runs each check once")
	case c.ModelDependent && c.Repeat.Metrics < 2:
		return fmt.Errorf("a model-dependent check repeats in the metrics tier: repeat.metrics must be at least 2")
	case !c.ModelDependent && c.Repeat.Metrics != 1:
		return fmt.Errorf("only a model-dependent check repeats: repeat.metrics must be 1")
	}
	for _, r := range c.Record {
		if glob, ok := strings.CutPrefix(r, LiveCheckFilesRecord); ok {
			if err := cleanRelative(glob); err != nil {
				return fmt.Errorf("record %q: %w", r, err)
			}
			if _, err := path.Match(glob, ""); err != nil {
				return fmt.Errorf("record %q: %w", r, err)
			}
			continue
		}
		if !liveCheckRecords[r] {
			return fmt.Errorf("record %q is not one of %v or files:<glob>", r, keysOfBool(liveCheckRecords))
		}
	}
	if len(c.Pass) == 0 && c.Metric == "" {
		return fmt.Errorf("declares neither a pass rule nor a metric")
	}
	if c.Metric != "" && !liveCheckMetrics[c.Metric] {
		return fmt.Errorf("metric %q is not one of %v", c.Metric, keysOfBool(liveCheckMetrics))
	}
	for i, p := range c.Pass {
		switch p.Kind {
		case CheckCompleted:
		case CheckPluginLoaded:
			if p.Name == "" {
				return fmt.Errorf("pass %d: plugin_loaded needs a name", i+1)
			}
		case CheckStreamLines:
			if p.Line == "" || p.Min < 1 {
				return fmt.Errorf("pass %d: stream_lines needs a line kind and a min of at least 1", i+1)
			}
		case CheckFileUnchanged:
			return fmt.Errorf("pass %d: file_unchanged is not a suite condition (a check keeps no fixture hashes)", i+1)
		default:
			lc, _ := p.liveCheck(i)
			if err := lc.validate(prompts, compacts); err != nil {
				return fmt.Errorf("pass %d: %w", i+1, err)
			}
		}
	}
	return nil
}

// CountStreamLines counts a raw stream's lines by "type" and by "type/subtype"; a line that is not
// JSON counts under "unparsed".
func CountStreamLines(raw []byte) map[string]int {
	out := map[string]int{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxHostLineBytes)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var head rawLine
		if json.Unmarshal(line, &head) != nil || head.Type == "" {
			out["unparsed"]++
			continue
		}
		out[head.Type]++
		if head.Subtype != "" {
			out[head.Type+"/"+head.Subtype]++
		}
	}
	return out
}

// LiveCheckRecord is what one run recorded, as its check asked: a fixed shape, so results from
// different runs and hosts compare field by field.
type LiveCheckRecord struct {
	HostModel         string               `json:"host_model,omitempty"`
	ClaudeCodeVersion string               `json:"claude_code_version,omitempty"`
	Plugins           []string             `json:"plugins,omitempty"`
	Compactions       []HostCompaction     `json:"compactions,omitempty"`
	CompactAttempts   []HostCompactAttempt `json:"compact_attempts,omitempty"`
	// Hooks is every hook the host ran, as "<hook name> <outcome>", in stream order.
	Hooks       []string        `json:"hooks,omitempty"`
	RateLimits  []HostRateLimit `json:"rate_limits,omitempty"`
	StreamLines map[string]int  `json:"stream_lines,omitempty"`
	// Files is the size of every project file a files:<glob> record matched, by slash path.
	Files map[string]int64 `json:"files,omitempty"`
}

// LiveCheckRun is one run of a check.
type LiveCheckRun struct {
	Run int `json:"run"`
	// Passed is every pass condition holding, with no harness error.
	Passed bool `json:"passed"`
	// HarnessError is a failure of the driver or the process; such a run is never a pass.
	HarnessError string `json:"harness_error,omitempty"`
	// Limited says how a subscription limit cut the run short: it is not counted, and runs again.
	Limited string `json:"limited,omitempty"`
	// Stopped is why the harness stopped the session itself (paid overage in use).
	Stopped  string          `json:"stopped,omitempty"`
	Failures []string        `json:"failures,omitempty"`
	WallMS   int64           `json:"wall_ms"`
	Record   LiveCheckRecord `json:"record"`
	Metric   *float64        `json:"metric,omitempty"`
}

// GradeLiveCheckRun grades one session of check: its pass conditions against the stream (s, with
// its line counts), the project left at root, the command outcomes the driver ran and the project
// files its files:<glob> records matched, and records what the check asks for.
func GradeLiveCheckRun(c LiveSuiteCheck, root string, s HostStream, lines map[string]int,
	commands map[string]CommandOutcome, files map[string]int64, wallMS int64,
) LiveCheckRun {
	run := LiveCheckRun{WallMS: wallMS, Record: liveCheckRecord(c, s, lines, files)}
	task := c.Task(LiveCheckSuite{Defaults: LiveTaskDefaults{MaxTurns: 1}})
	results := GradeLiveTrial(task, root, LiveEvidence{
		Answers: AnswersByStep(task, s), Commands: commands, ToolUsesAfter: ToolUsesAfterSteps(task, s),
	})
	byID := map[string]LiveCheckResult{}
	for _, r := range results {
		byID[r.ID] = r
	}
	for i, p := range c.Pass {
		ok, detail := false, ""
		switch p.Kind {
		case CheckCompleted:
			_, ok = StepRecords(task, s)
			detail = "a step ended without a non-error result, or a compaction step did not compact"
		case CheckPluginLoaded:
			ok = s.PluginLoaded(p.Name)
			detail = "the host did not list " + p.Name + " as loaded"
		case CheckStreamLines:
			ok = lines[p.Line] >= p.Min
			detail = fmt.Sprintf("%d %s line(s), want at least %d", lines[p.Line], p.Line, p.Min)
		default:
			r := byID[fmt.Sprintf("pass-%d", i+1)]
			ok, detail = r.Passed, r.Detail
		}
		if !ok {
			run.Failures = append(run.Failures, fmt.Sprintf("pass %d (%s): %s", i+1, p.Kind, detail))
		}
	}
	run.Passed = len(run.Failures) == 0
	if c.Metric != "" {
		v := liveCheckMetric(c.Metric, s, wallMS)
		run.Metric = &v
	}
	return run
}

func liveCheckRecord(c LiveSuiteCheck, s HostStream, lines map[string]int, files map[string]int64) LiveCheckRecord {
	var r LiveCheckRecord
	for _, want := range c.Record {
		switch want {
		case "host_model":
			if s.Init != nil {
				r.HostModel = s.Init.Model
			}
		case "claude_code_version":
			if s.Init != nil {
				r.ClaudeCodeVersion = s.Init.ClaudeCodeVersion
			}
		case "plugins":
			if s.Init != nil {
				for _, p := range s.Init.Plugins {
					r.Plugins = append(r.Plugins, p.Source)
				}
			}
		case "compactions":
			for _, t := range s.Turns {
				r.Compactions = append(r.Compactions, t.Compactions...)
			}
		case "compact_attempts":
			for _, t := range s.Turns {
				r.CompactAttempts = append(r.CompactAttempts, t.CompactAttempts...)
			}
		case "hooks":
			for _, t := range s.Turns {
				for _, h := range t.Hooks {
					r.Hooks = append(r.Hooks, strings.TrimSpace(h.Name+" "+h.Outcome))
				}
			}
		case "rate_limits":
			r.RateLimits = s.RateLimits
		case "stream_lines":
			r.StreamLines = lines
		default:
			if len(files) > 0 && strings.HasPrefix(want, LiveCheckFilesRecord) {
				r.Files = files
			}
		}
	}
	return r
}

func liveCheckMetric(name string, s HostStream, wallMS int64) float64 {
	switch name {
	case LiveMetricRateLimitEvents:
		return float64(len(s.RateLimits))
	case LiveMetricCompactions, LiveMetricCompactPostTokens:
		n, post := 0, int64(0)
		for _, t := range s.Turns {
			n += len(t.Compactions)
			for _, c := range t.Compactions {
				post += c.PostTokens
			}
		}
		if name == LiveMetricCompactions {
			return float64(n)
		}
		return float64(post)
	}
	return float64(wallMS)
}

// The verdicts a check's outcome can carry.
const (
	LiveCheckVerdictPass       = "pass"
	LiveCheckVerdictFail       = "fail"
	LiveCheckVerdictRate       = "rate"
	LiveCheckVerdictMetric     = "metric"
	LiveCheckVerdictIncomplete = "incomplete"
)

// LiveCheckOutcome is one check's result at a tier.
type LiveCheckOutcome struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	ModelDependent bool   `json:"model_dependent"`
	// Planned is the runs the tier asks for; Graded the runs counted (limited runs are not); Passed
	// the graded runs that passed.
	Planned int `json:"planned"`
	Graded  int `json:"graded"`
	Passed  int `json:"passed"`
	Limited int `json:"limited"`
	// Verdict is pass or fail at the quick tier (one run), rate at the metrics tier for a repeated
	// check, metric for a check with no pass rule, and incomplete when fewer runs were graded than
	// planned.
	Verdict string `json:"verdict"`
	// PassRate is Passed of Graded with its 95 % Wilson interval.
	PassRate *Proportion `json:"pass_rate,omitempty"`
	// Metric names the check's metric; MetricMean, MetricMin and MetricMax summarize it over the
	// graded runs.
	Metric     string         `json:"metric,omitempty"`
	MetricMean *float64       `json:"metric_mean,omitempty"`
	MetricMin  *float64       `json:"metric_min,omitempty"`
	MetricMax  *float64       `json:"metric_max,omitempty"`
	Runs       []LiveCheckRun `json:"runs"`
}

// liveCheckConfidence is the confidence level of every live-check pass-rate interval.
const liveCheckConfidence = 0.95

// SummarizeLiveCheck summarizes check's runs at tier.
func SummarizeLiveCheck(c LiveSuiteCheck, tier string, runs []LiveCheckRun) LiveCheckOutcome {
	out := LiveCheckOutcome{
		ID: c.ID, Title: c.Title, ModelDependent: c.ModelDependent, Planned: c.Runs(tier), Metric: c.Metric,
		Runs: append([]LiveCheckRun{}, runs...),
	}
	var metric []float64
	for _, r := range runs {
		if r.Limited != "" {
			out.Limited++
			continue
		}
		out.Graded++
		if r.Passed && r.HarnessError == "" {
			out.Passed++
		}
		if r.Metric != nil {
			metric = append(metric, *r.Metric)
		}
	}
	p := wilson(out.Passed, out.Graded, normalQuantile(1-(1-liveCheckConfidence)/2))
	out.PassRate = &p
	if len(metric) > 0 {
		sort.Float64s(metric)
		total := 0.0
		for _, v := range metric {
			total += v
		}
		mean, lo, hi := total/float64(len(metric)), metric[0], metric[len(metric)-1]
		out.MetricMean, out.MetricMin, out.MetricMax = &mean, &lo, &hi
	}
	switch {
	case out.Graded < out.Planned:
		out.Verdict = LiveCheckVerdictIncomplete
	case len(c.Pass) == 0:
		out.Verdict = LiveCheckVerdictMetric
	case out.Planned > 1:
		out.Verdict = LiveCheckVerdictRate
	case out.Passed == out.Graded:
		out.Verdict = LiveCheckVerdictPass
	default:
		out.Verdict = LiveCheckVerdictFail
	}
	return out
}

// LiveCheckResults is a live-check run's results.json.
type LiveCheckResults struct {
	Schema       int    `json:"schema"`
	RunID        string `json:"run_id"`
	Suite        string `json:"suite"`
	SuiteVersion int    `json:"suite_version"`
	SuiteSHA256  string `json:"suite_sha256"`
	Tier         string `json:"tier"`
	// Paused or Stopped say why the run ended before every planned run was graded.
	Paused  string             `json:"paused,omitempty"`
	Stopped string             `json:"stopped,omitempty"`
	Checks  []LiveCheckOutcome `json:"checks"`
}

// LiveCheckManifest is a live-check run's manifest.json: what it ran on.
type LiveCheckManifest struct {
	Schema    int    `json:"schema"`
	RunID     string `json:"run_id"`
	StartedAt string `json:"started_at"`
	Suite     string `json:"suite"`
	SuiteFile string `json:"suite_file"`
	// SuiteSHA256 is the suite file's SHA-256.
	SuiteSHA256       string `json:"suite_sha256"`
	Tier              string `json:"tier"`
	ClaudeCLI         string `json:"claude_cli"`
	ClaudeCodeVersion string `json:"claude_code_version"`
	// Models is every model the suite asked for; HostModels every model the hosts reported.
	Models     []string `json:"models"`
	HostModels []string `json:"host_models,omitempty"`
	// Bundles is each --bundle/--bundle-mods identity's BUNDLE.json SHA-256, by flag; Probes each
	// probe directory's tree-manifest SHA-256, by its suite path.
	Bundles map[string]string `json:"bundles,omitempty"`
	Probes  map[string]string `json:"probes,omitempty"`
	Machine string            `json:"machine"`
	// PrecomputeFlag is the operator's reading of the precompute flag (G0.5), or "not read".
	PrecomputeFlag string `json:"precompute_flag"`
}

// LiveCheckRunFiles is one run's two documents, as --compare reads them.
type LiveCheckRunFiles struct {
	Manifest LiveCheckManifest
	Results  LiveCheckResults
}

// RenderLiveCheckCompare sets two runs side by side as a Markdown table: one row per check, in a's
// order then b's new ones, with what changed.
func RenderLiveCheckCompare(a, b LiveCheckRunFiles) string {
	var sb strings.Builder
	label := func(f LiveCheckRunFiles) string {
		return fmt.Sprintf("Claude Code %s, run %s", f.Manifest.ClaudeCodeVersion, f.Manifest.RunID)
	}
	fmt.Fprintf(&sb, "# Live-check comparison\n\n- a: %s, suite %s (sha256 %s), tier %s\n- b: %s, suite %s (sha256 %s), tier %s\n\n",
		label(a), a.Manifest.Suite, shortSHA(a.Manifest.SuiteSHA256), a.Manifest.Tier,
		label(b), b.Manifest.Suite, shortSHA(b.Manifest.SuiteSHA256), b.Manifest.Tier)
	if a.Manifest.SuiteSHA256 != b.Manifest.SuiteSHA256 {
		sb.WriteString("The two runs read different suite files: a check with the same id may not be the same check.\n\n")
	}
	fmt.Fprintf(&sb, "| check | a: %s | b: %s | change |\n|---|---|---|---|\n", label(a), label(b))
	inB := map[string]LiveCheckOutcome{}
	for _, c := range b.Results.Checks {
		inB[c.ID] = c
	}
	inA := map[string]bool{}
	for _, ca := range a.Results.Checks {
		inA[ca.ID] = true
		cb, ok := inB[ca.ID]
		if !ok {
			fmt.Fprintf(&sb, "| %s | %s | — | only in a |\n", ca.ID, outcomeCell(ca))
			continue
		}
		fmt.Fprintf(&sb, "| %s | %s | %s | %s |\n", ca.ID, outcomeCell(ca), outcomeCell(cb), outcomeChange(ca, cb))
	}
	for _, cb := range b.Results.Checks {
		if !inA[cb.ID] {
			fmt.Fprintf(&sb, "| %s | — | %s | only in b |\n", cb.ID, outcomeCell(cb))
		}
	}
	return sb.String()
}

func outcomeCell(c LiveCheckOutcome) string {
	switch c.Verdict {
	case LiveCheckVerdictPass, LiveCheckVerdictFail:
		return c.Verdict
	case LiveCheckVerdictRate:
		return fmt.Sprintf("%d/%d = %.2f [%.2f, %.2f]", c.Passed, c.Graded, c.PassRate.Rate, c.PassRate.Low, c.PassRate.High)
	case LiveCheckVerdictMetric:
		if c.MetricMean != nil {
			return fmt.Sprintf("%s mean %g", c.Metric, *c.MetricMean)
		}
		return c.Metric + " not measured"
	}
	return fmt.Sprintf("incomplete (%d of %d runs)", c.Graded, c.Planned)
}

func outcomeChange(a, b LiveCheckOutcome) string {
	switch {
	case a.Verdict == LiveCheckVerdictPass && b.Verdict == LiveCheckVerdictFail:
		return "now fails"
	case a.Verdict == LiveCheckVerdictFail && b.Verdict == LiveCheckVerdictPass:
		return "now passes"
	case a.Verdict == LiveCheckVerdictRate && b.Verdict == LiveCheckVerdictRate:
		if d := b.PassRate.Rate - a.PassRate.Rate; d != 0 {
			return fmt.Sprintf("pass rate %+.2f", d)
		}
		return "same"
	case a.Verdict == LiveCheckVerdictMetric && b.Verdict == LiveCheckVerdictMetric && a.MetricMean != nil && b.MetricMean != nil:
		if d := *b.MetricMean - *a.MetricMean; d != 0 {
			return fmt.Sprintf("mean %+g", d)
		}
		return "same"
	case a.Verdict == b.Verdict:
		return "same"
	}
	return a.Verdict + " → " + b.Verdict
}

func shortSHA(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
