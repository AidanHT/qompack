// Command replay is the CI replay gate: it replays the committed corpus under every registered
// policy, scores each against the Belady ceiling, and turns 00-ARCHITECTURE.md §11.3 from a
// sentence into a required check.
//
// It is a composition root. internal/eval is foundation-only so that L7 can be built in the
// earliest parallel wave against no sibling's output; everything eval needs from a later wave —
// store growth samples, negknow bloom health — is declared as a provider type in eval and supplied
// here, which is what keeps that dependency out of the layer being measured.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// Exit codes. Each names a distinct failure so a CI log says what happened without being read.
const (
	exitOK          = 0
	exitGateFailed  = 1
	exitBadInput    = 2
	exitMaxCPU      = 3
	exitPhaseNoSkip = 5
)

// Default flag values.
const (
	defaultCorpusPath   = "testdata/sessions/synthetic"
	defaultBaselinePath = "testdata/baseline/phase0.json"
	defaultOutPath      = "testdata/bench-replay.json"
	defaultPolicies     = "stock,null,oracle"
	defaultMaxCPU       = 2 * time.Minute
)

// deprecatedMaxCPUAlias is the name this budget's flag used to have, kept working because it is
// written into several committed command lines (ci.yml and the V3/V4/V6 verification plans). It
// sets the same budget the new name does, which is now CPU time, so a run that uses it is told so
// rather than left to assume the old meaning still holds.
const deprecatedMaxCPUAlias = "max-wall"

// noDemandCorpusLimit is how much of a corpus may demand nothing before the corpus stops measuring
// anything. Σo = 0 scores every policy 1.0, so a corpus made mostly of those cases would report a
// perfect number for a policy that keeps nothing at all.
const noDemandCorpusLimit = 0.25

// errMaxCPU is the budget this driver gates its own cost on. It reads a CPU clock and not a wall
// clock, and the distinction is the difference between a gate and a coin toss.
//
// The budget exists to catch a replay that has become far more expensive than the corpus justifies
// — the quadratic scorer, the accidental O(n^2) over sessions. Wall clock cannot see that on a
// shared runner, because it also reports how much of the host this process was given: replaying
// the committed corpus is about 3 s of work, and test/bench/hotpath/process.go has the same
// machine inflating a child's wall time 23x (p50) and 124x on one pair under co-load, while the
// same child's CPU time did not move at all. The whole-tree `go test ./...` job drives this driver
// through test/integration/replaygrowth_test.go beside about twenty other package binaries, which
// is exactly that runner.
//
// It is not a weaker bound. The number is unchanged at 2 minutes and CPU time can EXCEED wall time
// on a multi-core host, since it sums every thread, so on the cost dimension this budget is at
// least as strict as the one it replaces.
//
// Neither this check nor the one it replaced is a watchdog: both sample between sessions and after
// the run, so a call that never returns is invisible to both and always was. What is gated is the
// aggregate, and for this driver the aggregate is CPU — internal/eval starts no goroutine, opens no
// socket and holds only a registry mutex, so a replay that stops finishing is a replay that is
// still executing. A deliberate sleep is the one shape a wall clock would see and this one will
// not, and devtool's sleepcheck sub-check already refuses time.Sleep everywhere but test/bench.
//
// The remaining blind spot is named rather than left to be discovered: loadBaseline shells out to
// git when --baseline is a ref (gate.go), and a child's CPU is charged to the child. That step is
// outside the session loop and does not scale with the corpus, and the check it used to sit under
// could not have caught a git that hung either — it runs after loadBaseline returns.
var errMaxCPU = errors.New("replay: CPU budget exceeded")

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// options is the parsed command line.
type options struct {
	corpus, baseline, policies string
	out, signOff               string
	growth, sketch             string
	phase                      int
	regenCorpus, writeBaseline bool
	maxCPU                     time.Duration
	ci                         bool
}

func parseFlags(args []string, errw io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(errw)
	fs.StringVar(&o.corpus, "corpus", defaultCorpusPath, "session corpus directory")
	fs.StringVar(&o.baseline, "baseline", defaultBaselinePath,
		"baseline file path or git ref; empty disables comparison")
	fs.StringVar(&o.policies, "policies", defaultPolicies, "comma-separated policy names")
	fs.StringVar(&o.out, "out", defaultOutPath, "write the full driver report here")
	fs.StringVar(&o.signOff, "signoff", "", "file holding the pull-request body, for the trailer scan")
	fs.StringVar(&o.growth, "growth", "", "StatsSample JSON for the sublinear-growth guardrail")
	fs.StringVar(&o.sketch, "sketch", "", "SketchHealth JSON for the bloom watch-for")
	fs.IntVar(&o.phase, "phase", 0, "highest merged phase whose exit criterion must hold")
	fs.BoolVar(&o.regenCorpus, "regen-corpus", false, "regenerate the synthetic corpus and exit")
	fs.BoolVar(&o.writeBaseline, "write-baseline", false, "write --baseline from this run and exit")
	// Both names write the same budget, so an old command line keeps working and a run that uses
	// it is told what changed. If both are given, the later one on the command line wins — which is
	// what the flag package does for any single flag repeated, so it is the least surprising rule
	// available here.
	fs.DurationVar(&o.maxCPU, "max-cpu", defaultMaxCPU,
		"CPU-time budget (user+system) for the whole replay; exceeded → exit 3")
	fs.DurationVar(&o.maxCPU, deprecatedMaxCPUAlias, defaultMaxCPU,
		"former name of -max-cpu, still accepted; the budget is CPU time now, not wall clock")
	fs.BoolVar(&o.ci, "ci", false, "CI mode: phase checks may not be disabled by configuration")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == deprecatedMaxCPUAlias {
			fmt.Fprintf(errw,
				"LOUD: -%s is the former name of -max-cpu and now sets a CPU-TIME budget of %s, "+
					"not a wall-clock one\n", deprecatedMaxCPUAlias, o.maxCPU)
		}
	})

	// The driver takes no positional arguments, so anything left over means flag parsing stopped
	// early — in practice at a bare `--`, which every argument-forwarding wrapper invites somebody
	// to write. Left unchecked, `replay -- --corpus X --ci` discards --corpus and --ci, replays the
	// default corpus, and exits 0: a green gate for a run that never happened. Refusing by name
	// turns the worst outcome a gate can have into its loudest one.
	if rest := fs.Args(); len(rest) > 0 {
		// Printed here rather than by the caller because the flag package writes its own parse
		// errors to errw already, and run() cannot tell the two apart without duplicating one.
		err := fmt.Errorf(
			"unexpected argument %q: replay takes flags only, and a bare %q stops flag parsing, "+
				"so every flag after it is silently ignored — drop the separator", rest[0], "--")
		fmt.Fprintln(errw, "replay:", err)
		return o, err
	}
	return o, nil
}

// cpuUsedSince returns the CPU this process has burned since an obs.ProcessCPU reading taken
// earlier. A clock that cannot be read is an error and never a zero: a budget that silently stops
// being measured is worse than one that fails.
func cpuUsedSince(start time.Duration) (time.Duration, error) {
	now, err := obs.ProcessCPU()
	if err != nil {
		return 0, fmt.Errorf("replay: the CPU clock this run's budget is graded on is unreadable: %w", err)
	}
	return now - start, nil
}

func run(args []string, out, errw io.Writer) int {
	// Both clocks start before anything else does. startCPU is what the budget is graded on;
	// started is kept because the wall time is still worth PRINTING beside it — a run whose two
	// clocks have diverged tenfold is telling the reader the host was busy, which is information
	// and not a defect.
	started := time.Now()
	startCPU, err := cpuUsedSince(0)
	if err != nil {
		fmt.Fprintf(errw, "%v\n", err)
		return exitBadInput
	}

	o, err := parseFlags(args, errw)
	if err != nil {
		return exitBadInput
	}

	root, err := repoRoot()
	if err != nil {
		fmt.Fprintf(errw, "%v\n", err)
		return exitBadInput
	}
	corpusDir := resolve(root, o.corpus)

	if o.regenCorpus {
		if err := eval.WriteCorpus(corpusDir); err != nil {
			fmt.Fprintf(errw, "%v\n", err)
			return exitBadInput
		}
		fmt.Fprintf(out, "regenerated %d sessions in %s\n", len(eval.CorpusSpecs()), corpusDir)
		return exitOK
	}

	cfg := loadConfig(root)
	ctx := context.Background()

	sessions, err := eval.New(eval.Options{Cfg: cfg}).Load(corpusDir)
	if err != nil {
		fmt.Fprintf(errw, "%v\n", err)
		return exitBadInput
	}
	policyNames := splitPolicies(o.policies)

	first, err := replayCorpus(ctx, cfg, sessions, policyNames, o.maxCPU, startCPU, out)
	if err != nil {
		fmt.Fprintf(errw, "%v\n", err)
		if errors.Is(err, errMaxCPU) {
			return exitMaxCPU
		}
		return exitBadInput
	}
	// A SECOND full replay with a freshly constructed harness. Reusing the loaded sessions but not
	// the harness is deliberate: a stateful bug in the percentile pool shows up here as a diff
	// rather than hiding behind an accumulated-but-consistent number.
	second, err := replayCorpus(ctx, cfg, sessions, policyNames, o.maxCPU, startCPU, io.Discard)
	if err != nil {
		fmt.Fprintf(errw, "%v\n", err)
		if errors.Is(err, errMaxCPU) {
			return exitMaxCPU
		}
		return exitBadInput
	}

	manifest, manifestErr := loadManifest(corpusDir)
	growthSamples, err := loadGrowthSamples(resolveOptional(root, o.growth))
	if err != nil {
		fmt.Fprintf(errw, "%v\n", err)
		return exitBadInput
	}
	health, err := loadSketchHealth(resolveOptional(root, o.sketch))
	if err != nil {
		fmt.Fprintf(errw, "%v\n", err)
		return exitBadInput
	}

	driver := DriverReport{
		Generator:        generatorID,
		Corpus:           o.corpus,
		CorpusTier:       tierOf(sessions),
		CorpusSHA256:     manifestDigest(corpusDir),
		Sessions:         first.report.Sessions,
		Latency:          latencyModelled,
		Policies:         first.policies,
		WatchFor:         watchForMetrics(health),
		RetrievalActions: first.retrievalActions,
		BudgetViolations: first.budgetViolations,
		NoDemandSessions: first.noDemand,
		Breakpoint:       breakpointPlanFor(sessions),
		Growth:           eval.CheckSublinearGrowth(growthSamples),
		PhaseChecked:     o.phase,
		Report:           first.report,
		Sketch:           health,
	}

	printSummary(out, driver, first)

	if o.writeBaseline {
		return writeBaselineFile(out, errw, resolve(root, o.baseline), driver)
	}

	failed := false

	// eval.replayOnPhaseGate is a real key, not decoration: a developer may turn the phase checks
	// off for a fast local loop. Nobody may turn them off for a pull request.
	if !cfg.Eval.ReplayOnPhaseGate {
		driver.PhaseChecksSkipped = true
		fmt.Fprintln(errw, "LOUD: eval.replayOnPhaseGate is false, so the phase-exit criteria were skipped")
		if o.ci {
			fmt.Fprintln(errw,
				"replay: --ci forbids disabling the phase checks; set eval.replayOnPhaseGate back to true")
			_ = writeReport(resolve(root, o.out), driver)
			return exitPhaseNoSkip
		}
	} else {
		phaseCtx := Context{
			Report: first.report, Driver: driver, Cfg: cfg,
			CanonicalFirst:  canonicalMetrics(first.policies),
			CanonicalSecond: canonicalMetrics(second.policies),
			Corpus:          manifest, Growth: driver.Growth, Sketch: health,
		}
		for _, err := range runPhaseChecks(phaseCtx, o.phase) {
			fmt.Fprintf(errw, "FAIL %v\n", err)
			failed = true
		}
	}

	if manifestErr == nil {
		if err := checkCorpusFreshness(manifest, o.phase); err != nil {
			fmt.Fprintf(errw, "FAIL %v\n", err)
			failed = true
		}
	}
	if err := checkGrowth(o.growth, driver.Growth); err != nil {
		fmt.Fprintf(errw, "FAIL %v\n", err)
		failed = true
	}
	if o.sketch != "" {
		if err := checkBloomCeiling(health); err != nil {
			fmt.Fprintf(errw, "FAIL %v\n", err)
			failed = true
		}
	}
	if len(first.noDemand) > int(float64(len(sessions))*noDemandCorpusLimit) {
		fmt.Fprintf(errw,
			"FAIL %d of %d sessions demanded nothing after their compactions; a corpus that "+
				"demands nothing scores every policy 1.0 and measures nothing\n",
			len(first.noDemand), len(sessions))
		failed = true
	}
	if len(first.budgetViolations) > 0 {
		fmt.Fprintf(errw,
			"FAIL policies exceeded the keep budget: %s. A policy that cheats on the budget is "+
				"not comparable to one that does not\n", strings.Join(first.budgetViolations, ", "))
		failed = true
	}

	if o.baseline != "" {
		base, err := loadBaseline(resolve(root, o.baseline), root)
		switch {
		case err != nil && looksLikePath(o.baseline) && os.IsNotExist(errors.Unwrap(err)):
			fmt.Fprintf(errw, "WARN no baseline at %s; comparison disabled\n", o.baseline)
		case err != nil:
			fmt.Fprintf(errw, "%v\n", err)
			return exitBadInput
		default:
			driver.Regressions = compare(base, driver.Policies, driver.WatchFor, readSignOff(o.signOff))
			if reportRegressions(errw, driver.Regressions) {
				failed = true
			}
		}
	}

	if err := writeReport(resolve(root, o.out), driver); err != nil {
		fmt.Fprintf(errw, "%v\n", err)
		return exitBadInput
	}
	used, err := cpuUsedSince(startCPU)
	if err != nil {
		fmt.Fprintf(errw, "%v\n", err)
		return exitBadInput
	}
	if used > o.maxCPU {
		fmt.Fprintf(errw, "FAIL %v: the whole replay burned %s of CPU against the %s budget (%s of wall clock)\n",
			errMaxCPU, used, o.maxCPU, time.Since(started))
		return exitMaxCPU
	}
	if failed {
		return exitGateFailed
	}
	return exitOK
}

// runResult is one full pass over the corpus.
type runResult struct {
	report           eval.Report
	policies         map[string]map[string]float64
	retrievalActions map[string]int
	budgetViolations []string
	noDemand         []string
	inexact          []string
}

// replayCorpus replays every session under every policy and scores each against the Belady
// ceiling.
//
// OPT is computed once per session and shared across policies, because the ceiling is a property
// of the session and not of the policy being measured — and because computing it per policy would
// be the easiest possible way to accidentally score two policies against different ceilings.
//
// Divergence is composed here rather than inside ScoreRun: §5.18 splits Compare and ScoreRun into
// separate operations, and joining them is a composition root's job.
func replayCorpus(ctx context.Context, cfg config.Config, sessions []eval.Session,
	policyNames []string, maxCPU, startCPU time.Duration, out io.Writer,
) (runResult, error) {
	h := eval.New(eval.Options{Cfg: cfg, Log: logging.Nop()})
	res := runResult{
		policies:         map[string]map[string]float64{},
		retrievalActions: map[string]int{},
	}
	scores := map[string][]eval.Score{}
	violated := map[string]bool{}

	opts := eval.ReplayOptions{Deterministic: true, K: eval.DefaultHorizonK, Budget: eval.DefaultKeepBudget}

	for _, s := range sessions {
		used, err := cpuUsedSince(startCPU)
		if err != nil {
			return res, err
		}
		if used > maxCPU {
			return res, fmt.Errorf("%w: %s of CPU burned by session %s, budget %s",
				errMaxCPU, used, s.ID, maxCPU)
		}

		opt, optValue, inexact, err := optimalKeepSets(ctx, s)
		if err != nil {
			return res, err
		}
		if inexact {
			res.inexact = append(res.inexact, s.ID)
			fmt.Fprintf(out, "WARN %s: the exact OPT solver was too large; a 1/2-approximation was used\n", s.ID)
		}
		if optValue == 0 {
			res.noDemand = append(res.noDemand, s.ID)
		}

		baseline := eval.BaselineRun(s)
		for _, name := range policyNames {
			p, ok := eval.PolicyByName(name, cfg)
			if !ok {
				return res, fmt.Errorf("replay: no policy named %q; registered: %v",
					name, eval.PolicyNames())
			}
			r, err := h.Replay(ctx, s, p, opts)
			if err != nil {
				return res, fmt.Errorf("replay: %s on %s: %w", name, s.ID, err)
			}
			for _, k := range r.Keeps {
				if k.Tokens > eval.DefaultKeepBudget {
					violated[name] = true
				}
			}
			score := h.ScoreRun(r, opt)
			score.Divergence = h.Compare(baseline, r)
			scores[name] = append(scores[name], score)
			res.retrievalActions[name] += eval.RetrievalActions(r)
		}
	}

	report, err := h.Report(ctx, scores)
	if err != nil {
		return res, err
	}
	res.report = report
	for name, score := range report.Policies {
		res.policies[name] = eval.MetricsOf(score, cfg)
	}
	for name := range violated {
		res.budgetViolations = append(res.budgetViolations, name)
	}
	sort.Strings(res.budgetViolations)
	return res, nil
}

// optimalKeepSets computes the Belady ceiling at every compaction point of one session.
func optimalKeepSets(ctx context.Context, s eval.Session) (map[core.TurnIndex]eval.KeepSet, int, bool, error) {
	opt := make(map[core.TurnIndex]eval.KeepSet, len(s.CompactionAt))
	total := 0
	inexact := false
	for _, at := range s.CompactionAt {
		ks, detail, err := eval.BeladyDetail(ctx, s, at, eval.DefaultKeepBudget, eval.DefaultBeladyOptions())
		if err != nil {
			return nil, 0, false, fmt.Errorf("replay: OPT for %s at turn %d: %w", s.ID, at, err)
		}
		opt[at] = ks
		total += detail.Value
		if !detail.Exact {
			inexact = true
		}
	}
	return opt, total, inexact, nil
}

// breakpointPlanFor computes the §5.6 optimal marker placement over the longest session, which is
// the one with the most candidate positions to choose between.
func breakpointPlanFor(sessions []eval.Session) eval.BreakpointPlan {
	var longest eval.Session
	for _, s := range sessions {
		if len(s.Turns) > len(longest.Turns) {
			longest = s
		}
	}
	plan, err := eval.BreakpointOPT(longest, cacheControlMarkers)
	if err != nil {
		return eval.BreakpointPlan{Note: eval.NotPluginActionable}
	}
	return plan
}

// watchForMetrics projects the §11.4 bloom numbers into the gate's metric vocabulary.
func watchForMetrics(h eval.SketchHealth) map[string]float64 {
	return map[string]float64{
		"bloom_fp_rate":    h.EstFPRate,
		"bloom_fill_ratio": h.FillRatio,
	}
}

// printSummary writes the human-facing result.
func printSummary(out io.Writer, d DriverReport, res runResult) {
	fmt.Fprintf(out, "corpus %s (%s tier), %d sessions, latency: %s\n",
		d.Corpus, d.CorpusTier, d.Sessions, d.Latency)

	names := make([]string, 0, len(d.Policies))
	for name := range d.Policies {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Fprintf(out, "\n%-9s %15s %10s %14s %12s\n",
		"policy", "fraction_of_opt", "retrieval", "rewrite_tokens", "pause_p95")
	for _, name := range names {
		m := d.Policies[name]
		fmt.Fprintf(out, "%-9s %15.6f %10d %14.0f %12.0f\n",
			name, m["fraction_of_opt"], res.retrievalActions[name],
			m["rewrite_tokens"], m["compaction_pause_ms_p95"])
	}

	// The disclaimer is printed on the line ABOVE the number, always, so a breakpoint figure can
	// never be read as something the plugin does.
	fmt.Fprintf(out, "\n%s\n", eval.NotPluginActionable)
	fmt.Fprintf(out, "breakpoint OPT: %d cached reads at %v (%d markers, %d candidates)\n",
		d.Breakpoint.CachedReads, d.Breakpoint.Positions, d.Breakpoint.Markers, d.Breakpoint.Candidates)

	if d.Growth.Samples > 0 {
		fmt.Fprintf(out, "store growth: exponent %.3f over %d samples, sublinear=%t %s\n",
			d.Growth.Exponent, d.Growth.Samples, d.Growth.Sublinear, d.Growth.Reason)
	}
	for _, name := range names {
		if res.retrievalActions[name] == 0 {
			fmt.Fprintf(out, "note: %s made 0 retrieval calls, so its retrieval_hit_rate of 0.0 "+
				"is an absence of calls rather than a failure\n", name)
		}
	}
}

// writeBaselineFile writes the reproducible subset and exits.
func writeBaselineFile(out, errw io.Writer, path string, d DriverReport) int {
	body, err := encodeJSON(d.toBaseline())
	if err != nil {
		fmt.Fprintf(errw, "%v\n", err)
		return exitBadInput
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		fmt.Fprintf(errw, "replay: creating %s: %v\n", filepath.Dir(path), err)
		return exitBadInput
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		fmt.Fprintf(errw, "replay: writing %s: %v\n", path, err)
		return exitBadInput
	}
	fmt.Fprintf(out, "\nwrote baseline %s\n", path)
	return exitOK
}

// writeReport writes the full envelope.
func writeReport(path string, d DriverReport) error {
	if path == "" {
		return nil
	}
	body, err := encodeJSON(d)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("replay: creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("replay: writing %s: %w", path, err)
	}
	return nil
}

// tierOf reports which §6.3 fidelity tier this corpus is. A synthetic number reported as if it
// were a real-session number is the dishonest measurement §1.3 RC-3 indicts, so the tier travels
// with the number rather than being inferred from the directory name.
func tierOf(sessions []eval.Session) string {
	for _, s := range sessions {
		if !s.Synthetic {
			return tierRecorded
		}
	}
	return tierSynthetic
}

// manifestDigest hashes CORPUS.json, so a report names the exact corpus it measured.
func manifestDigest(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "CORPUS.json")) //nolint:gosec // the corpus manifest
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.ReplaceAll(string(raw), "\r\n", "\n")))
	return hex.EncodeToString(sum[:])
}

// loadManifest reads the corpus manifest, which the freshness check needs.
func loadManifest(dir string) (eval.CorpusManifest, error) {
	var m eval.CorpusManifest
	raw, err := os.ReadFile(filepath.Join(dir, "CORPUS.json")) //nolint:gosec // the corpus manifest
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(raw, &m)
}

// readSignOff reads the pull-request body, if one was supplied.
func readSignOff(path string) string {
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path) //nolint:gosec // an explicitly named sign-off file
	if err != nil {
		return ""
	}
	return string(raw)
}

// loadConfig resolves the project configuration, falling back to defaults when there is no project
// to resolve — the driver has to work from a bare checkout.
func loadConfig(root string) config.Config {
	cfg, _, _, err := config.Load(config.Env{ProjectRoot: root, Getenv: os.Getenv})
	if err != nil {
		return config.Defaults()
	}
	return cfg
}

// splitPolicies parses the --policies list.
func splitPolicies(csv string) []string {
	var out []string
	for _, part := range strings.Split(csv, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// resolve makes a repo-relative flag value absolute.
func resolve(root, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

// resolveOptional is resolve for a flag whose empty value means "not supplied".
func resolveOptional(root, p string) string {
	if p == "" {
		return ""
	}
	return resolve(root, p)
}

// repoRoot walks up from the working directory to the module root.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("replay: resolving the working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("replay: no go.mod above the working directory")
		}
		dir = parent
	}
}
