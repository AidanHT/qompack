package main

// `devtool live-check` (V7 W6 item 11): the repeatable Phase 0 checks as a benchmark that can be
// re-run, for example after each Claude Code update. It reads a versioned check-suite file
// (eval.LiveCheckSuite), runs each check through the live-eval session driver — one session at a
// time, under the guard over the operator's ~/.claude, with the auto-updater off and the same pacing
// on the subscription's limits — grades it from the host's stream, and writes a manifest and results
// in fixed, versioned schemas. `--compare <a> <b>` sets two runs side by side.
//
// Like live-eval, nothing here starts a session unless QOMPACK_LIVE_EVAL=1 is set; --dry-run
// validates the suite and the bundles and shows each check's command line.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/eval"
)

// liveCheckDefaultOut is where a run's directory goes unless --out says otherwise.
const liveCheckDefaultOut = "dist/live-check"

// liveCheckPrecomputeUnread is the manifest's precompute-flag reading when the operator gave none.
const liveCheckPrecomputeUnread = "not read"

// liveCheckOptions is one live-check invocation's settled flags.
type liveCheckOptions struct {
	suite, tier, out, claude, model string
	bundle, bundleMods              string
	precompute                      string
	idleExit                        int
	stepTimeout, sessionTimeout     time.Duration
	dryRun                          bool
	compare                         []string
	fiveHourGate, sevenDayCeiling   float64
	night                           liveNightWindow
}

func taskLiveCheck(args []string) error {
	o, err := parseLiveCheckFlags(args)
	if err != nil {
		return errors.Join(errUsage, err)
	}
	if len(o.compare) == 2 {
		return compareLiveChecks(o.compare[0], o.compare[1], os.Stdout)
	}
	env, err := newLiveEnv(liveOptions{claude: o.claude, dryRun: o.dryRun})
	if err != nil {
		return err
	}
	return runLiveCheck(context.Background(), o, env, os.Stdout)
}

func parseLiveCheckFlags(args []string) (liveCheckOptions, error) {
	fs := flag.NewFlagSet("live-check", flag.ContinueOnError)
	o := liveCheckOptions{}
	fs.StringVar(&o.suite, "suite", "", "check-suite file (versioned JSON)")
	fs.StringVar(&o.tier, "tier", eval.LiveCheckTierQuick, "quick (each check once, pass or fail) or metrics "+
		"(model-dependent checks repeated, rates with intervals)")
	fs.StringVar(&o.out, "out", "", "output directory (default dist/live-check/<run-id>)")
	fs.StringVar(&o.claude, "claude", "", "Claude Code CLI, the pinned install (default: `claude` on PATH)")
	fs.StringVar(&o.bundle, "bundle", "", "bundle a check with probe \"bundle\" loads by --plugin-dir")
	fs.StringVar(&o.bundleMods, "bundle-mods", "", "--mods bundle a check with probe \"bundle-mods\" loads")
	fs.StringVar(&o.model, "model", "", "run every check on this model instead of the suite's")
	fs.StringVar(&o.precompute, "precompute-flag", liveCheckPrecomputeUnread,
		"the operator's reading of the precompute flag for the account (G0.5), recorded in the manifest")
	fs.IntVar(&o.idleExit, "daemon-idle-exit", liveDefaultIdleExit, "QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS for a probe's daemon")
	fs.DurationVar(&o.stepTimeout, "step-timeout", liveDefaultStepTimeout, "bound on one step's turn")
	fs.DurationVar(&o.sessionTimeout, "session-timeout", liveDefaultSessionTimeout, "bound on one whole session")
	fs.BoolVar(&o.dryRun, "dry-run", false, "validate the suite and bundles and show each check's command; start nothing")
	compare := fs.Bool("compare", false, "compare two run directories given as arguments: --compare <a> <b>")
	fs.Float64Var(&o.fiveHourGate, "five-hour-gate", eval.LiveFiveHourGate, "as live-eval's")
	fs.Float64Var(&o.sevenDayCeiling, "seven-day-ceiling", eval.LiveSevenDayCeiling, "as live-eval's")
	night := fs.String("night-window", "", "as live-eval's: HH:MM-HH:MM, local time (default: no window)")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if *compare {
		if fs.NArg() != 2 {
			return o, errors.New("live-check: --compare takes two run directories")
		}
		o.compare = fs.Args()
		return o, nil
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("live-check: unexpected argument %q", fs.Arg(0))
	}
	if o.suite == "" {
		return o, errors.New("live-check: --suite is required")
	}
	if o.tier != eval.LiveCheckTierQuick && o.tier != eval.LiveCheckTierMetrics {
		return o, fmt.Errorf("live-check: --tier %q is neither quick nor metrics", o.tier)
	}
	if *night != "" {
		n, err := parseNightWindow(*night)
		if err != nil {
			return o, err
		}
		o.night = n
	}
	if o.fiveHourGate <= 0 || o.fiveHourGate > 1 || o.sevenDayCeiling <= 0 || o.sevenDayCeiling > 1 || o.idleExit <= 0 {
		return o, errors.New("live-check: the gate and ceiling must be utilizations in (0, 1] and --daemon-idle-exit positive")
	}
	return o, nil
}

// liveCheckRun is one live-check run in progress.
type liveCheckRun struct {
	o        liveCheckOptions
	env      *liveEnv
	suite    eval.LiveCheckSuite
	manifest eval.LiveCheckManifest
	// probes is each check's --plugin-dir directory, absolute; absent for a check with no probe.
	probes map[string]string
}

// runLiveCheck runs the suite.
func runLiveCheck(ctx context.Context, o liveCheckOptions, env *liveEnv, w io.Writer) error {
	suite, raw, err := eval.LoadLiveCheckSuite(o.suite)
	if err != nil {
		return err
	}
	runID := liveRunID(env.now())
	if o.out == "" {
		o.out = filepath.Join(liveCheckDefaultOut, runID)
	}
	r := &liveCheckRun{o: o, env: env, suite: suite, probes: map[string]string{}}
	r.manifest = eval.LiveCheckManifest{
		Schema: eval.LiveCheckManifestSchema, RunID: runID, StartedAt: env.now().UTC().Format(time.RFC3339),
		Suite: suite.ID, SuiteFile: filepath.ToSlash(o.suite), SuiteSHA256: sha256Hex(raw), Tier: o.tier,
		Machine: runtime.GOOS + "/" + runtime.GOARCH, PrecomputeFlag: o.precompute, ClaudeCLI: env.claudeBin,
	}
	if err := r.resolveProbes(); err != nil {
		return err
	}
	models := map[string]bool{}
	for _, c := range suite.Checks {
		models[r.model(c)] = true
	}
	r.manifest.Models = slices.Sorted(maps.Keys(models))

	if o.dryRun {
		for _, c := range suite.Checks {
			fmt.Fprintf(w, "  %s: %d run(s) at tier %s\n", c.ID, c.Runs(o.tier), o.tier)
			args := liveSessionArgs(r.model(c), c.Task(suite), suite.Defaults, "<session-id>", r.extraArgs(c))
			fmt.Fprintf(w, "    host: %s %s\n    env: %s\n", orUnknown(env.claudeBin), strings.Join(args, " "),
				strings.Join(r.sessionEnv(c), " "))
		}
		fmt.Fprintln(w, "live-check: --dry-run: no session started")
		return nil
	}
	if os.Getenv(liveEvalGateEnv) != "1" {
		return fmt.Errorf("live-check: refusing to start real sessions without %s=1 (CI never sets it); use --dry-run "+
			"to validate", liveEvalGateEnv)
	}
	r.manifest.ClaudeCodeVersion = env.cliVersion(ctx)
	if r.manifest.ClaudeCodeVersion == liveUnknownVersion {
		return fmt.Errorf("live-check: could not read the host version from %s --version; a run records and pins it", env.claudeBin)
	}
	if err := os.MkdirAll(o.out, liveDirPerm); err != nil {
		return fmt.Errorf("live-check: creating %s: %w", o.out, err)
	}
	if err := writeJSONFile(filepath.Join(o.out, "manifest.json"), r.manifest); err != nil {
		return err
	}
	fmt.Fprintf(w, "live-check: run %s: suite %s (%s) at tier %s on Claude Code %s\n", runID, suite.ID,
		shortHash(r.manifest.SuiteSHA256), o.tier, r.manifest.ClaudeCodeVersion)

	pacer, err := newLivePacer(liveOptions{fiveHourGate: o.fiveHourGate, sevenDayCeiling: o.sevenDayCeiling, night: o.night}, o.out)
	if err != nil {
		return err
	}
	results := eval.LiveCheckResults{
		Schema: eval.LiveCheckResultsSchema, RunID: runID, Suite: suite.ID, SuiteVersion: suite.Version,
		SuiteSHA256: r.manifest.SuiteSHA256, Tier: o.tier,
	}
	hostModels := map[string]bool{}
	runs := map[string][]eval.LiveCheckRun{}
	var stop error
checks:
	for _, c := range suite.Checks {
		for n := 1; n <= c.Runs(o.tier); n++ {
			for {
				if v := env.cliVersion(ctx); v != r.manifest.ClaudeCodeVersion {
					stop = fmt.Errorf("live-check: the host reports Claude Code %s, not %s, before %s run %d", v,
						r.manifest.ClaudeCodeVersion, c.ID, n)
					break checks
				}
				why, paceErr := pacer.before(ctx, env, w)
				if paceErr != nil {
					stop = paceErr
					break checks
				}
				if why != "" {
					results.Paused = why
					break checks
				}
				run, evs, model, guardErr := r.runOnce(ctx, c, n)
				runs[c.ID] = append(runs[c.ID], run)
				if model != "" {
					hostModels[model] = true
				}
				if err := pacer.observe(env, evs); err != nil && guardErr == nil {
					guardErr = err
				}
				fmt.Fprintf(w, "live-check: %s run %d: passed=%t limited=%q harness_error=%q\n", c.ID, n, run.Passed,
					run.Limited, run.HarnessError)
				if guardErr != nil {
					stop = guardErr
					break checks
				}
				if over := liveOverage(evs); over != "" {
					stop = fmt.Errorf("live-check: after %s run %d: %s", c.ID, n, over)
					break checks
				}
				if problem := eval.LiveRateProblem(evs); problem != "" {
					stop = fmt.Errorf("live-check: after %s run %d: %s", c.ID, n, problem)
					break checks
				}
				if run.Limited == "" {
					break
				}
				if eval.PaceLive(pacer.snap, env.now(), pacer.pacing).Action == eval.PaceGo {
					results.Paused = fmt.Sprintf("a limit cut %s run %d short (%s) and no event says when it lifts", c.ID, n, run.Limited)
					break checks
				}
			}
		}
	}
	if stop != nil {
		results.Stopped = stop.Error()
		fmt.Fprintf(w, "live-check: STOPPING: %v\n", stop)
	}
	for _, c := range suite.Checks {
		results.Checks = append(results.Checks, eval.SummarizeLiveCheck(c, o.tier, runs[c.ID]))
	}
	r.manifest.HostModels = slices.Sorted(maps.Keys(hostModels))
	if err := writeJSONFile(filepath.Join(o.out, "manifest.json"), r.manifest); err != nil {
		return errors.Join(stop, err)
	}
	if err := writeJSONFile(filepath.Join(o.out, "results.json"), results); err != nil {
		return errors.Join(stop, err)
	}
	for _, c := range results.Checks {
		fmt.Fprintf(w, "live-check: %s: %s (%d of %d graded run(s) passed)\n", c.ID, c.Verdict, c.Passed, c.Graded)
	}
	if results.Paused != "" {
		fmt.Fprintf(w, "live-check: PAUSED: %s\n", results.Paused)
	}
	fmt.Fprintf(w, "live-check: records under %s\n", o.out)
	return stop
}

// resolveProbes reads each probe a check loads: a run bundle, verified against its BUNDLE.json and
// recorded by that file's hash, or a plugin directory in the suite's tree, recorded by its tree hash.
// It also refuses a check whose environment sets a variable the harness sets itself.
func (r *liveCheckRun) resolveProbes() error {
	harness := map[string]bool{liveEnvMCPServers: true, liveEnvAutoUpdater: true, liveEnvIdleExit: true}
	for _, c := range r.suite.Checks {
		for name := range c.Env {
			if harness[name] {
				return fmt.Errorf("live-check: check %s sets %s, which the harness sets itself", c.ID, name)
			}
		}
		switch c.Probe {
		case "":
		case eval.LiveCheckProbeBundle, eval.LiveCheckProbeBundleMods:
			dir, mods := r.o.bundle, c.Probe == eval.LiveCheckProbeBundleMods
			if mods {
				dir = r.o.bundleMods
			}
			id, err := readLiveBundle(dir, c.Probe, []string{"live-check " + c.ID}, mods)
			if err != nil {
				return err
			}
			if r.manifest.Bundles == nil {
				r.manifest.Bundles = map[string]string{}
			}
			r.manifest.Bundles[c.Probe] = id.BundleSHA256
			r.probes[c.ID] = id.BundleDir
		default:
			base := filepath.Dir(r.o.suite)
			sum, err := eval.TreeManifestSHA256(base, c.Probe)
			if err != nil {
				return fmt.Errorf("live-check: hashing probe %s: %w", c.Probe, err)
			}
			if r.manifest.Probes == nil {
				r.manifest.Probes = map[string]string{}
			}
			r.manifest.Probes[c.Probe] = sum
			abs, err := filepath.Abs(filepath.Join(base, filepath.FromSlash(c.Probe)))
			if err != nil {
				return err
			}
			r.probes[c.ID] = abs
		}
	}
	return nil
}

func (r *liveCheckRun) model(c eval.LiveSuiteCheck) string {
	if r.o.model != "" {
		return r.o.model
	}
	return c.EffectiveModel(r.suite)
}

func (r *liveCheckRun) extraArgs(c eval.LiveSuiteCheck) []string {
	if dir, ok := r.probes[c.ID]; ok {
		return []string{"--plugin-dir", dir}
	}
	return nil
}

// sessionEnv is a check's session environment: what every live-eval session has (the auto-updater
// off, the claude.ai MCP servers off, the daemon idle exit), then the check's own, in name order.
func (r *liveCheckRun) sessionEnv(c eval.LiveSuiteCheck) []string {
	env := liveSessionEnv(eval.LiveArmState{Arm: "live-check"}, r.o.idleExit)
	for _, name := range slices.Sorted(maps.Keys(c.Env)) {
		env = append(env, name+"="+c.Env[name])
	}
	return env
}

// liveCheckAttemptFiles are what one run attempt writes into its directory.
var liveCheckAttemptFiles = []string{"stream.jsonl", "stderr.txt", "invocation.json", "run.json", "harness-spool"}

// runOnce runs check c's n-th run once: a disposable project from the fixture, the session through the
// live-eval driver, the guard around it, the probe's daemon stopped after it, and the grade. It
// returns the run, the session's rate-limit events, the model the host reported and a guard error,
// which stops the run. A limited attempt is set aside in the run's directory as run.limited-<k>.json.
func (r *liveCheckRun) runOnce(ctx context.Context, c eval.LiveSuiteCheck, n int) (eval.LiveCheckRun, []eval.HostRateLimit, string, error) {
	env := r.env
	run := eval.LiveCheckRun{Run: n}
	dir := filepath.Join(r.o.out, "checks", c.ID, fmt.Sprintf("%02d", n))
	if err := os.MkdirAll(dir, liveDirPerm); err != nil {
		run.HarnessError = err.Error()
		return run, nil, "", nil
	}
	trialStart := env.now().Add(-liveMTimeMargin)
	before, err := env.guardSnapshot()
	if err != nil {
		return run, nil, "", fmt.Errorf("live-check: the configuration guard could not read the operator's files: %w", err)
	}
	var stream eval.HostStream
	var lines map[string]int
	var proc liveProcResult
	project := ""
	work, err := os.MkdirTemp(env.workRoot, "qompack-live-check-")
	if err == nil {
		project = filepath.Join(work, "project")
		err = copyTree(c.FixtureDir(r.o.suite), project)
	}
	if err == nil {
		err = env.gitInit(ctx, project)
	}
	if err != nil {
		run.HarnessError = "preparing the project: " + err.Error()
	} else {
		task := c.Task(r.suite)
		spec := liveProcSpec{
			Bin: env.claudeBin, Args: liveSessionArgs(r.model(c), task, r.suite.Defaults, newUUID(), r.extraArgs(c)),
			Dir: project, Env: r.sessionEnv(c), Messages: liveMessages(task),
			StepTimeout: r.o.stepTimeout, SessionTimeout: r.o.sessionTimeout, StopOn: liveOverageStop,
		}
		spec.Unset = liveUnsetNames(os.Environ(), spec.Env)
		_ = writeJSONFile(filepath.Join(dir, "invocation.json"), spec)
		proc = env.run(ctx, spec)
		run.WallMS = proc.EndedAt.Sub(proc.StartedAt).Milliseconds()
		run.HarnessError = proc.Err
		_ = os.WriteFile(filepath.Join(dir, "stream.jsonl"), proc.Stream, liveFilePerm)
		_ = os.WriteFile(filepath.Join(dir, "stderr.txt"), proc.Stderr, liveFilePerm)
		if _, ok := r.probes[c.ID]; ok {
			if _, stopErr := env.stopDaemon(project, filepath.Join(dir, "harness-spool")); stopErr != nil {
				run.Failures = append(run.Failures, "probe daemon stop: "+stopErr.Error())
			}
		}
	}
	leftovers := env.removeCreatedPluginData(before, trialStart)
	after, err := env.guardSnapshot()
	var guardErr error
	switch {
	case err != nil:
		guardErr = fmt.Errorf("live-check: the configuration guard could not re-read the operator's files: %w", err)
	case !before.equal(after) || len(leftovers) > 0:
		guardErr = fmt.Errorf("live-check: the operator's Claude Code configuration changed during %s run %d (before %v, "+
			"after %v, leftovers %v); restore it before running again", c.ID, n, before.lines(), after.lines(), leftovers)
	}

	if project != "" {
		parsed, perr := eval.ParseHostStream(strings.NewReader(string(proc.Stream)), proc.RecvMS)
		if perr != nil {
			run.HarnessError = joinErr(run.HarnessError, "parsing the stream: "+perr.Error())
		}
		stream, lines = parsed, eval.CountStreamLines(proc.Stream)
		commands := map[string]eval.CommandOutcome{}
		for i, p := range c.Pass {
			if p.Kind == eval.CheckCommand {
				commands[fmt.Sprintf("pass-%d", i+1)] = env.runCheckCommand(project, p.Argv)
			}
		}
		graded := eval.GradeLiveCheckRun(c, project, stream, lines, commands, liveCheckFiles(project, c.Record), run.WallMS)
		graded.Run, graded.HarnessError = n, run.HarnessError
		graded.Failures = append(run.Failures, graded.Failures...)
		graded.Passed = graded.Passed && graded.HarnessError == "" && len(run.Failures) == 0
		run = graded
		run.Limited = eval.LiveUsageLimited(stream)
		if proc.Stopped != "" {
			run.Limited = "the session was stopped: " + proc.Stopped
		}
	}
	model := ""
	if stream.Init != nil {
		model = stream.Init.Model
	}
	if run.Limited != "" {
		if _, err := liveSetAsideRecord(dir, liveSlotLimited, "run.json", liveCheckAttemptFiles, run); err != nil && guardErr == nil {
			guardErr = err
		}
	} else {
		_ = writeJSONFile(filepath.Join(dir, "run.json"), run)
	}
	return run, stream.RateLimits, model, guardErr
}

// liveCheckFiles is the size of every project file a check's files:<glob> records match, by slash
// path relative to the project.
func liveCheckFiles(project string, record []string) map[string]int64 {
	var out map[string]int64
	for _, rec := range record {
		glob, ok := strings.CutPrefix(rec, eval.LiveCheckFilesRecord)
		if !ok {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(project, filepath.FromSlash(glob)))
		sort.Strings(matches)
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil || info.IsDir() {
				continue
			}
			rel, err := filepath.Rel(project, m)
			if err != nil {
				continue
			}
			if out == nil {
				out = map[string]int64{}
			}
			out[filepath.ToSlash(rel)] = info.Size()
		}
	}
	return out
}

// compareLiveChecks prints two runs side by side.
func compareLiveChecks(a, b string, w io.Writer) error {
	read := func(dir string) (eval.LiveCheckRunFiles, error) {
		var f eval.LiveCheckRunFiles
		for name, v := range map[string]any{"manifest.json": &f.Manifest, "results.json": &f.Results} {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return f, fmt.Errorf("live-check: --compare: %w", err)
			}
			if err := json.Unmarshal(raw, v); err != nil {
				return f, fmt.Errorf("live-check: --compare: parsing %s: %w", filepath.Join(dir, name), err)
			}
		}
		if f.Manifest.Schema != eval.LiveCheckManifestSchema || f.Results.Schema != eval.LiveCheckResultsSchema {
			return f, fmt.Errorf("live-check: --compare: %s is in schema %d/%d, not %d/%d", dir, f.Manifest.Schema,
				f.Results.Schema, eval.LiveCheckManifestSchema, eval.LiveCheckResultsSchema)
		}
		return f, nil
	}
	fa, err := read(a)
	if err != nil {
		return err
	}
	fb, err := read(b)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, eval.RenderLiveCheckCompare(fa, fb))
	return err
}
