package main

// `devtool live-eval` — V6 close-out C5.4: the real-host evaluation driver.
//
// It runs a declared task set (testdata/eval/live/) as real headless Claude Code sessions, each in a
// disposable project copied from the task's fixture, in the four Stage A arms (V7 W6): `stock` (no
// plugin), `classic` (the plugin from --bundle), `generic` (classic plus the task set's text G in the
// project's CLAUDE.md) and `steer` (the plugin from --bundle-mods with both mod switches on), each
// loaded by --plugin-dir or through the real marketplace flow. Every arm's state is declared by
// eval.LiveArmSpec and checked per trial; a trial whose configuration contradicts its arm is set
// aside and run again (liveeval_arms.go). Trials run one at a time: the guard over the operator's
// configuration snapshots around each trial, and a second concurrent trial would break it. Every
// task forces a compaction midway with a `/compact` user turn and is graded afterwards by checks a
// program evaluates — files, a hidden test suite, `go` commands, the text of named answers, and
// tool calls the session should not have needed. Per trial it records completion and constraint
// outcomes, per-category usage from the host's own JSON, a dated list-price ESTIMATE, wall time,
// the hook latency the host measured and the plugin's store size.
//
// Everything that is a pure function lives in internal/eval (live*.go) and is unit-tested there
// with recorded fixtures. This file and liveeval_host.go own the side effects: processes, the
// disposable projects, the plugin install/uninstall, and the guard over the operator's real
// Claude Code configuration, which fails the run closed if a guarded file changed.
//
// Nothing here runs unless QOMPACK_LIVE_EVAL=1 is set: CI never sets it, so CI never calls a
// model. --dry-run validates the task set, the bundle and the plan without starting a session.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/eval"
)

// liveEvalGateEnv is the environment gate. A run that would start a real session refuses unless it
// is exactly "1".
const liveEvalGateEnv = "QOMPACK_LIVE_EVAL"

// Defaults, relative to the repository root.
const (
	liveDefaultTasks = "testdata/eval/live/tasks-v2.json"
	liveDefaultRates = "testdata/eval/live/rates.json"
	liveDefaultOut   = "dist/live-eval"
)

// The two ways a Qompack arm reaches the host.
const (
	liveInstallPluginDir   = "plugin-dir"
	liveInstallMarketplace = "marketplace"
)

// liveMCPToolPrefix is the prefix the host gives the plugin's MCP tools.
const liveMCPToolPrefix = eval.LiveQompackToolPrefix

// liveMCPServerName is the name the host lists the plugin's MCP server under.
const liveMCPServerName = "plugin:qompack:qompack"

// liveInlineSource is the marketplace name Claude Code gives a plugin loaded with --plugin-dir: the
// host lists it as "<name>@inline" (2.1.280, observed in the smoke and pilot sessions).
const liveInlineSource = "inline"

// livePluginName is the plugin's name as the host lists it.
const livePluginName = "qompack"

// liveMarketplaceName is the disposable local marketplace the marketplace flow publishes through.
// It is distinctive so the guard can recognise anything it leaves behind.
const liveMarketplaceName = "qompack-live-eval"

// Timeouts and bounds.
const (
	liveDefaultStepTimeout    = 10 * time.Minute
	liveDefaultSessionTimeout = 45 * time.Minute
	liveExitGrace             = 90 * time.Second
	liveCommandTimeout        = 5 * time.Minute
	liveCLITimeout            = 2 * time.Minute
	liveDaemonStopBound       = 60 * time.Second
	// liveDefaultIdleExit is the product's own daemon idle exit: at the old harness default of 120 s a
	// trial silent for a few minutes lost its daemon (W6 item 5). The harness stops each trial's
	// daemon at the end anyway, so nothing is left running.
	liveDefaultIdleExit   = 1800
	liveCommandOutputKeep = 16 << 10
	liveMTimeMargin       = 2 * time.Second
	liveDirPerm           = 0o755
	liveFilePerm          = 0o644
)

// liveOptions is one invocation's settled flags.
type liveOptions struct {
	tasksFile      string
	arms           []string
	trials         int
	model          string
	out            string
	install        string
	bundle         string
	bundleMods     string
	orderSeed      int64
	claude         string
	rates          string
	only           map[string]bool
	includeHeldOut bool
	maxSessions    int
	stepTimeout    time.Duration
	sessionTimeout time.Duration
	idleExit       int
	keepRaw        bool
	dryRun         bool
	// defectsAttested reports that --known-open-defects was given; openDefects is what it named
	// (empty for "none").
	defectsAttested bool
	openDefects     []string
	// confirmatory refuses to plan a run that departs, at plan time, from its task set's
	// pre-registered confirmatory design (eval.LivePlanDepartures).
	confirmatory bool
	// The pacing (W6 item 8): the five-hour gate, the seven-day ceiling and the night window.
	fiveHourGate    float64
	sevenDayCeiling float64
	night           liveNightWindow
	// resume is the run directory --resume continues (W6 item 13).
	resume string
	// describe prints the run's description and starts nothing (W6 item 10).
	describe bool
	// pacingGiven names the pacing flags given on the command line; pacingOverride lets them differ
	// from the values a run directory stored (review I3).
	pacingGiven    map[string]bool
	pacingOverride bool
}

// taskLiveEval implements `devtool live-eval`.
func taskLiveEval(args []string) error {
	o, err := parseLiveFlags(args)
	if err != nil {
		return errors.Join(errUsage, err)
	}
	env, err := newLiveEnv(o)
	if err != nil {
		return err
	}
	return runLiveEval(context.Background(), o, env, os.Stdout)
}

func parseLiveFlags(args []string) (liveOptions, error) {
	fs := flag.NewFlagSet("live-eval", flag.ContinueOnError)
	o := liveOptions{}
	fs.StringVar(&o.tasksFile, "tasks", liveDefaultTasks, "task-set file")
	arms := fs.String("arms", strings.Join(eval.LiveArms, ","), "comma-separated arms: "+strings.Join(eval.LiveArms, ", "))
	fs.IntVar(&o.trials, "trials", 0, "trials per task for every arm (default: each arm's pre-registered count in the task set)")
	fs.StringVar(&o.model, "model", "", "model ID (default: the task set's pre-registered model)")
	fs.StringVar(&o.out, "out", "", "output directory (default dist/live-eval/<run-id>)")
	fs.StringVar(&o.install, "install", liveInstallPluginDir, "how the Qompack arms load the plugin: plugin-dir or marketplace")
	fs.StringVar(&o.bundle, "bundle", "", "assembled host-target bundle directory without the mod (from `devtool bundle`); "+
		"required for the classic and generic arms")
	fs.StringVar(&o.bundleMods, "bundle-mods", "", "assembled bundle with the mod (from `devtool bundle --mods`), built from "+
		"the same commit as --bundle; required for the steer arm")
	fs.Int64Var(&o.orderSeed, "order-seed", 0, "seed of the shuffle within each block of rounds; 0, the default, derives it from the run id "+
		"(plan.json records the seed used either way)")
	fs.StringVar(&o.claude, "claude", "", "Claude Code CLI, the pinned install (default: `claude` on PATH)")
	fs.StringVar(&o.rates, "rates", liveDefaultRates, "dated rate table for the list-price estimate")
	only := fs.String("only", "", "comma-separated task IDs to run (default: every non-held-out task)")
	fs.BoolVar(&o.includeHeldOut, "include-held-out", false, "also run held-out tasks (the confirmatory run only)")
	fs.IntVar(&o.maxSessions, "max-sessions", 0, "refuse a plan that would start more sessions than this (0: no cap)")
	fs.DurationVar(&o.stepTimeout, "step-timeout", liveDefaultStepTimeout, "bound on one step's turn")
	fs.DurationVar(&o.sessionTimeout, "session-timeout", liveDefaultSessionTimeout, "bound on one whole session")
	fs.IntVar(&o.idleExit, "daemon-idle-exit", liveDefaultIdleExit,
		"QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS for the trial's daemon, so it exits after the session")
	fs.BoolVar(&o.keepRaw, "keep-raw-transcripts", false,
		"also copy the host's raw transcript into the trial directory (it carries the operator's identity and system prompt: never commit it)")
	fs.BoolVar(&o.dryRun, "dry-run", false, "validate the task set, bundle and plan; start nothing")
	fs.BoolVar(&o.confirmatory, "confirmatory", false,
		"refuse to plan, dry run included, unless the plan is the task set's pre-registered confirmatory design")
	defects := fs.String("known-open-defects", "",
		"the operator's statement of which known defects the bundle still carries: none, or their checklist IDs "+
			"comma-separated (required with any Qompack arm, and covering both bundles; preregistration section 9)")
	fs.Float64Var(&o.fiveHourGate, "five-hour-gate", eval.LiveFiveHourGate,
		"no trial starts while the five-hour window's utilization is at or above this; the run waits for its reset")
	fs.Float64Var(&o.sevenDayCeiling, "seven-day-ceiling", eval.LiveSevenDayCeiling,
		"the run pauses while the seven-day window's utilization is at or above this (the owner keeps the rest)")
	night := fs.String("night-window", "", "HH:MM-HH:MM, local time: trials start only inside it, and the run pauses "+
		"cleanly outside it (default: no window)")
	fs.StringVar(&o.resume, "resume", "", "continue the run in this directory from its plan.json: verify its hashes and host, "+
		"skip every slot that holds a scored trial, run the rest, and decide over every session's trials")
	fs.BoolVar(&o.pacingOverride, "pacing-override", false, "let the pacing flags change the values a resumed run "+
		"stored at its first session")
	fs.BoolVar(&o.describe, "describe", false, "print, as Markdown, what the run would do (tasks, checks, arms, bundles, "+
		"host pin, trial order, model, flags and pacing); start nothing")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.resume != "" {
		var conflicts []string
		fs.Visit(func(f *flag.Flag) {
			if !liveResumeFlags[f.Name] {
				conflicts = append(conflicts, "--"+f.Name)
			}
		})
		if len(conflicts) > 0 {
			return o, fmt.Errorf("live-eval: --resume continues the plan its directory holds, so %s cannot change it",
				strings.Join(conflicts, ", "))
		}
	}
	o.pacingGiven = map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "five-hour-gate", "seven-day-ceiling", "night-window":
			o.pacingGiven[f.Name] = true
		}
	})
	if *night != "" {
		n, err := parseNightWindow(*night)
		if err != nil {
			return o, err
		}
		o.night = n
	} else if !o.dryRun && !o.describe && o.resume == "" {
		return o, errors.New("live-eval: a run that starts trials names its night window: --night-window HH:MM-HH:MM, " +
			"or --night-window none to run at any hour (D100 runs at night)")
	}
	for _, v := range []struct {
		name string
		val  float64
	}{{"--five-hour-gate", o.fiveHourGate}, {"--seven-day-ceiling", o.sevenDayCeiling}} {
		if v.val <= 0 || v.val > 1 {
			return o, fmt.Errorf("live-eval: %s %g is not a utilization in (0, 1]", v.name, v.val)
		}
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("live-eval: unexpected argument %q", fs.Arg(0))
	}
	for _, a := range strings.Split(*arms, ",") {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if _, ok := eval.LiveArmSpec(a); !ok {
			return o, fmt.Errorf("live-eval: unknown arm %q (%s)", a, strings.Join(eval.LiveArms, ", "))
		}
		if contains(o.arms, a) {
			return o, fmt.Errorf("live-eval: --arms names %s twice", a)
		}
		o.arms = append(o.arms, a)
	}
	if len(o.arms) == 0 {
		return o, errors.New("live-eval: --arms names no arm")
	}
	if o.install != liveInstallPluginDir && o.install != liveInstallMarketplace {
		return o, fmt.Errorf("live-eval: --install %q is neither plugin-dir nor marketplace", o.install)
	}
	if *only != "" {
		o.only = map[string]bool{}
		for _, id := range strings.Split(*only, ",") {
			if id = strings.TrimSpace(id); id != "" {
				o.only[id] = true
			}
		}
	}
	if *defects != "" {
		open, err := parseKnownOpenDefects(*defects)
		if err != nil {
			return o, err
		}
		o.defectsAttested, o.openDefects = true, open
	}
	if o.trials < 0 || o.maxSessions < 0 || o.idleExit <= 0 {
		return o, errors.New("live-eval: --trials and --max-sessions must be non-negative and --daemon-idle-exit positive")
	}
	return o, nil
}

// liveDefectID is the shape of a checklist ID such as C1.12.
var liveDefectID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(\.[A-Za-z0-9]+)*$`)

// parseKnownOpenDefects reads --known-open-defects: "none", or the comma-separated checklist IDs of
// every known defect the bundle still carries. The result is never nil, so "none" is recorded as an
// empty list rather than as no statement.
func parseKnownOpenDefects(v string) ([]string, error) {
	if strings.TrimSpace(v) == liveNoOpenDefects {
		return []string{}, nil
	}
	open := []string{}
	for _, id := range strings.Split(v, ",") {
		id = strings.TrimSpace(id)
		switch {
		case id == "":
		case id == liveNoOpenDefects:
			return nil, fmt.Errorf("live-eval: --known-open-defects %q names defects and %q at once", v, liveNoOpenDefects)
		case !liveDefectID.MatchString(id):
			return nil, fmt.Errorf("live-eval: --known-open-defects: %q is not a checklist ID such as C1.12", id)
		default:
			open = append(open, id)
		}
	}
	if len(open) == 0 {
		return nil, fmt.Errorf("live-eval: --known-open-defects %q names no defect; say %q if the bundle carries none",
			v, liveNoOpenDefects)
	}
	return open, nil
}

// liveNoOpenDefects is the --known-open-defects value that attests the bundle carries no known defect.
const liveNoOpenDefects = "none"

// liveDefectSource is how plan.json says where its defect attestation came from.
const liveDefectSource = "the operator, by live-eval --known-open-defects when the run was planned"

// knownDefectsText renders an attestation for the run's output and summary.md.
func knownDefectsText(a *eval.LiveDefectAttestation) string {
	switch {
	case a == nil:
		return "not attested"
	case len(a.Open) == 0:
		return liveNoOpenDefects
	default:
		return strings.Join(a.Open, ", ")
	}
}

// runLiveEval is the whole run: validate, plan, gate, execute, summarize.
func runLiveEval(ctx context.Context, o liveOptions, env *liveEnv, w io.Writer) error {
	if o.resume != "" {
		return resumeLiveEval(ctx, o, env, w)
	}
	ts, raw, err := eval.LoadLiveTaskSet(o.tasksFile)
	if err != nil {
		return err
	}
	if pre, ok := eval.LivePreregistrations[ts.ID]; ok && pre.SupersededBy != "" {
		// A dry run is refused too: it exists to validate the real run's command.
		next := eval.LivePreregistrations[pre.SupersededBy]
		return fmt.Errorf("live-eval: task set %s was %s; no run of it can be the confirmatory run, so none is "+
			"planned. Run %s (%s) instead", ts.ID, pre.SupersededWhy, pre.SupersededBy, next.TaskSetFile)
	}
	rates, err := loadLiveRates(o.rates)
	if err != nil {
		return err
	}
	if o.model == "" {
		o.model = ts.Analysis.Model
	}
	counts, err := liveArmCounts(ts, o)
	if err != nil {
		return err
	}
	stateList := make([]eval.LiveArmState, 0, len(o.arms))
	for _, a := range o.arms {
		s, _ := eval.LiveArmSpec(a)
		stateList = append(stateList, s)
	}
	if contains(o.arms, eval.ArmGeneric) && strings.TrimSpace(ts.Analysis.GenericCompactInstructions) == "" {
		return fmt.Errorf("live-eval: the generic arm needs the task set's text G (analysis.generic_compact_instructions), "+
			"and %s declares none", ts.ID)
	}
	tasks, err := selectLiveTasks(ts, o)
	if err != nil {
		return err
	}
	treeDirs := ts.FixtureTreeDirs()
	tree, err := eval.TreeManifestSHA256(filepath.Dir(o.tasksFile), treeDirs...)
	if err != nil {
		return fmt.Errorf("live-eval: hashing the fixture tree: %w", err)
	}
	runID := liveRunID(env.now())
	if o.out == "" {
		o.out = filepath.Join(liveDefaultOut, runID)
	}

	plan := eval.LivePlan{
		RunID: runID, CreatedAt: env.now().UTC().Format(time.RFC3339), TaskSet: ts.ID,
		TaskSetFile: filepath.ToSlash(o.tasksFile), TaskSetSHA256: sha256Hex(raw),
		TaskSetTasks: len(ts.Tasks), FixtureTreeSHA256: tree, FixtureTreeDirs: treeDirs,
		Model: o.model, PreregisteredModel: ts.Analysis.Model, Arms: o.arms,
		TrialsPerArm: liveUniformCount(counts, o.arms), ArmTrials: counts, ArmStates: stateList,
		OrderSeed: liveOrderSeed(o.orderSeed, runID), DaemonIdleExitSeconds: o.idleExit,
		Install: o.install, RateTableDate: rates.Date, RateTableSource: rates.Source,
		HeldOutIncluded: o.includeHeldOut, Host: runtime.GOOS + "/" + runtime.GOARCH,
		Agent: "agent-executed on the real installed host (owner decision D3); not human UAT",
	}
	if err := planLiveBundles(&o, &plan, stateList); err != nil {
		return err
	}
	plan.Trials = planLiveTrials(tasks, o.arms, counts, plan.OrderSeed)
	if o.maxSessions > 0 && len(plan.Trials) > o.maxSessions {
		return fmt.Errorf("live-eval: the plan starts %d session(s), above --max-sessions %d", len(plan.Trials), o.maxSessions)
	}
	if o.describe {
		departures := eval.LivePlanDepartures(plan, ts.Analysis)
		if o.confirmatory && len(departures) > 0 {
			return fmt.Errorf("live-eval: --confirmatory: this plan is not the pre-registered confirmatory design: %s",
				strings.Join(departures, "; "))
		}
		// Reading the version runs `claude --version`, a subcommand: no session starts.
		plan.ClaudeCLI = env.claudeBin
		d := liveDescription{
			opts: o, plan: plan, set: ts, tasks: tasks, hostVersion: env.cliVersion(ctx), departures: departures,
		}
		_, err := io.WriteString(w, d.render())
		return err
	}

	fmt.Fprintf(w, "live-eval: run %s: %d trial(s) of task set %s (%s, fixture tree %s) on %s, arms %s, install %s\n",
		runID, len(plan.Trials), ts.ID, plan.TaskSetSHA256[:12], plan.FixtureTreeSHA256[:12], o.model,
		strings.Join(o.arms, ","), o.install)
	fmt.Fprintf(w, "live-eval: trials per task: %s (order seed %d)\n", liveCountsText(counts, o.arms), plan.OrderSeed)
	if plan.KnownDefects != nil {
		fmt.Fprintf(w, "live-eval: known open defects: %s (%s; preregistration section 9)\n",
			knownDefectsText(plan.KnownDefects), liveDefectSource)
	}
	for _, b := range []struct {
		name string
		id   *eval.LivePluginIdentity
	}{{eval.LiveBundleClassic, plan.Plugin}, {eval.LiveBundleMods, plan.PluginMods}} {
		if p := b.id; p != nil {
			fmt.Fprintf(w, "live-eval: %s (%s) %s at commit %s (dirty=%t), %s sha256 %s, at %s\n",
				b.name, strings.Join(liveArmsUsing(stateList, b.name), ", "), p.Version, p.Commit, p.Dirty,
				identityFileName, p.BundleSHA256, p.BundleDir)
		}
	}
	// What makes this the confirmatory run is mostly fixed now; say it before any session is spent.
	departures := eval.LivePlanDepartures(plan, ts.Analysis)
	if len(departures) == 0 {
		fmt.Fprintln(w, "live-eval: confirmatory preconditions at plan time: met. Still to be shown by the trials: "+
			"every planned trial runs, no plugin but the arm's own loads on either arm, and a contingency alias "+
			"resolves to one model; the section 9 known-defect statement is the operator's, not machine-checked")
	} else {
		fmt.Fprintf(w, "live-eval: not the confirmatory design: %s\n", strings.Join(departures, "; "))
		if o.confirmatory {
			doc := "its task set has no pre-registration"
			if pre, ok := eval.LivePreregistrations[ts.ID]; ok {
				doc = pre.Document
			}
			return fmt.Errorf("live-eval: --confirmatory: this plan is not the pre-registered confirmatory design "+
				"(%s), so no session is planned: %s", doc, strings.Join(departures, "; "))
		}
	}
	if o.dryRun {
		for _, p := range plan.Trials {
			fmt.Fprintf(w, "  plan: %s/%s/%d\n", p.Task, p.Arm, p.Trial)
		}
		for _, line := range liveDryRunHost(o, env, ts.Defaults, tasks[0]) {
			fmt.Fprintln(w, "  "+line)
		}
		fmt.Fprintln(w, "live-eval: --dry-run: no session started")
		return nil
	}
	if os.Getenv(liveEvalGateEnv) != "1" {
		return fmt.Errorf("live-eval: refusing to start real sessions without %s=1 (CI never sets it); "+
			"use --dry-run to validate", liveEvalGateEnv)
	}

	// The host is pinned (W6 item 12): the plan records the version, and no trial starts on another.
	plan.ClaudeCLI, plan.ClaudeCLIVersion = env.claudeBin, env.cliVersion(ctx)
	if plan.ClaudeCLIVersion == liveUnknownVersion {
		return fmt.Errorf("live-eval: could not read the host version from %s --version; a run pins its host "+
			"version, so nothing is planned", env.claudeBin)
	}
	fmt.Fprintf(w, "live-eval: host pinned at Claude Code %s (%s), %s=1 in every session\n",
		plan.ClaudeCLIVersion, plan.ClaudeCLI, liveEnvAutoUpdater)
	if err := os.MkdirAll(o.out, liveDirPerm); err != nil {
		return fmt.Errorf("live-eval: creating %s: %w", o.out, err)
	}
	if err := writeJSONFile(filepath.Join(o.out, "plan.json"), plan); err != nil {
		return err
	}

	return executeLivePlan(ctx, o, env, w, plan, ts, rates, tasks)
}

// liveSlotDir is a planned trial's slot directory.
func liveSlotDir(out string, p eval.LivePlannedTrial) string {
	return filepath.Join(out, "trials", p.Task, p.Arm, fmt.Sprintf("%02d", p.Trial))
}

// executeLivePlan runs every slot of plan that does not yet hold a scored trial, one at a time, then
// summarizes every scored trial the run directory holds: a fresh run and a resumed one (W6 item 13)
// reach their verdict the same way, over the union of all sessions' trials, read in plan order.
//
// A slot runs until it holds a scored trial: an attempt whose configuration contradicted its arm is
// set aside in the slot (trial.discarded-<n>.json) and the slot runs again, until this session's
// third such error stops the run; an attempt a subscription limit cut short is set aside as
// trial.limited-<n>.json and the slot runs again once the pacing allows (W6 item 8). Before every
// attempt the host version is checked against the pin and the pacing decides: start, wait for a
// five-hour reset, pause cleanly (a clean exit that --resume continues, not a stop), or stop on paid
// overage.
func executeLivePlan(ctx context.Context, o liveOptions, env *liveEnv, w io.Writer, plan eval.LivePlan,
	ts eval.LiveTaskSet, rates eval.LiveRateTable, tasks []eval.LiveTask,
) error {
	byID := map[string]eval.LiveTask{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	states := map[string]eval.LiveArmState{}
	for _, s := range plan.ArmStates {
		states[s.Arm] = s
	}
	pacer, err := newLivePacer(o, o.out, env.now())
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "live-eval: pacing: five-hour gate %.2f, seven-day ceiling %.2f, night window %s\n",
		pacer.pacing.FiveHourGate, pacer.pacing.SevenDayCeiling, pacer.night)
	var abort error
	paused := ""
	configErrors := 0
	stop := func(err error) {
		abort = err
		fmt.Fprintf(w, "live-eval: STOPPING: %v\n", err)
	}
slots:
	for _, p := range plan.Trials {
		slot := liveSlotDir(o.out, p)
		if _, err := os.Stat(filepath.Join(slot, liveScoredRecord)); err == nil {
			continue // scored in an earlier session of this run
		}
		for {
			if v := env.cliVersion(ctx); v != plan.ClaudeCLIVersion {
				stop(fmt.Errorf("live-eval: the host reports Claude Code %s, not the pinned %s, before %s/%s/%d; "+
					"no trial starts on another host version", v, plan.ClaudeCLIVersion, p.Task, p.Arm, p.Trial))
				break slots
			}
			why, paceErr := pacer.before(ctx, env, w)
			if paceErr != nil {
				stop(paceErr)
				break slots
			}
			if why != "" {
				paused = why
				break slots
			}
			lt := liveTrialRun{
				opts: o, env: env, set: ts, task: byID[p.Task], arm: p.Arm, state: states[p.Arm], trial: p.Trial,
				runID: plan.RunID, rates: rates, plan: &plan, rateBefore: pacer.snap,
			}
			rec, guardErr := lt.run(ctx)
			if err := pacer.observe(env, rec.RateLimits); err != nil && guardErr == nil {
				guardErr = err
			}
			switch {
			case rec.ConfigurationError != "":
				configErrors++
				fmt.Fprintf(w, "live-eval: %s/%s/%d: configuration error %d of the run: %s; set aside, the slot runs again\n",
					p.Task, p.Arm, p.Trial, configErrors, rec.ConfigurationError)
			case rec.Limited != "":
				fmt.Fprintf(w, "live-eval: %s/%s/%d: limited: %s; set aside, the slot runs again from a fresh fixture\n",
					p.Task, p.Arm, p.Trial, rec.Limited)
			default:
				fmt.Fprintf(w, "live-eval: %s/%s/%d: completed=%t task_success=%t violations=%d harness_error=%q\n",
					p.Task, p.Arm, p.Trial, rec.Completed, rec.TaskSuccess, rec.ConstraintViolations, rec.HarnessError)
			}
			if rec.OverageStatus != "" || rec.OverageDisabledReason != "" {
				fmt.Fprintf(w, "live-eval: %s/%s/%d: the account reports overage %s (%s)\n", p.Task, p.Arm, p.Trial,
					orUnknown(rec.OverageStatus), orUnknown(rec.OverageDisabledReason))
			}
			if guardErr != nil {
				stop(guardErr)
				break slots
			}
			if over := liveOverage(rec.RateLimits); over != "" {
				stop(fmt.Errorf("live-eval: after %s/%s/%d: %s", p.Task, p.Arm, p.Trial, over))
				break slots
			}
			if problem := eval.LiveRateProblem(rec.RateLimits); problem != "" {
				stop(fmt.Errorf("live-eval: after %s/%s/%d: %s", p.Task, p.Arm, p.Trial, problem))
				break slots
			}
			if rec.ConfigurationError == "" && rec.Limited == "" {
				break
			}
			if configErrors >= liveConfigErrorLimit {
				stop(fmt.Errorf("live-eval: three configuration errors in this run, the last on %s/%s/%d (%s); "+
					"stopping for investigation", p.Task, p.Arm, p.Trial, rec.ConfigurationError))
				break slots
			}
			if rec.Limited != "" && eval.PaceLive(pacer.snap, env.now(), pacer.pacing).Action == eval.PaceGo {
				// Nothing says when the limit lifts: running the slot again now would meet it again.
				paused = "a limit cut " + fmt.Sprintf("%s/%s/%d", p.Task, p.Arm, p.Trial) + " short (" + rec.Limited +
					") and no event says when it lifts"
				break slots
			}
		}
	}
	return summarizeLiveRun(o, w, plan, ts, abort, paused)
}

// summarizeLiveRun writes summary.json and summary.md over every scored trial in the run directory
// and returns abort: a stop is an error, a pause a clean exit.
func summarizeLiveRun(o liveOptions, w io.Writer, plan eval.LivePlan, ts eval.LiveTaskSet, abort error, paused string) error {
	recs, err := collectLiveRecords(o.out, plan)
	if err != nil {
		return errors.Join(abort, err)
	}
	sum := eval.SummarizeLive(plan.RunID, ts.Analysis, recs.scored)
	sum.AddLimited(ts.Analysis, recs.scored, recs.limited)
	sum.Notes = append(sum.Notes, liveOverageNotes(recs)...)
	if recs.discarded > 0 {
		sum.Notes = append(sum.Notes, fmt.Sprintf("%d configuration-discarded attempt(s) were set aside in their slots "+
			"as trial.discarded-<n>.json and the slots run again: harness errors, never scored", recs.discarded))
	}
	switch {
	case abort != nil:
		sum.StopEarly(len(recs.scored), len(plan.Trials), abort.Error())
	case paused != "":
		sum.Pause(len(recs.scored), len(plan.Trials), paused)
	}
	if err := writeJSONFile(filepath.Join(o.out, "summary.json"), sum); err != nil {
		return errors.Join(abort, err)
	}
	if err := writeFileAtomic(filepath.Join(o.out, "summary.md"), []byte(renderLiveSummary(plan, sum))); err != nil {
		return errors.Join(abort, fmt.Errorf("live-eval: writing summary.md: %w", err))
	}
	if paused != "" && abort == nil {
		fmt.Fprintf(w, "live-eval: PAUSED: %s. %d of %d planned trial(s) are scored; continue with "+
			"`devtool live-eval --resume %s`\n", paused, len(recs.scored), len(plan.Trials), o.out)
	}
	fmt.Fprintf(w, "live-eval: decision %s — %s\nlive-eval: records under %s\n", sum.Decision.Verdict, sum.Decision.Reason, o.out)
	return abort
}

// liveRecords is what a run directory's slots hold.
type liveRecords struct {
	scored, limited []eval.LiveTrial
	discarded       int
}

// collectLiveRecords reads every planned slot's records, in plan order: its scored trial.json, its
// limited records and the count of its configuration-discarded ones. A record that cannot be read
// fails the summary closed rather than leaving a trial out of it.
func collectLiveRecords(out string, plan eval.LivePlan) (liveRecords, error) {
	var r liveRecords
	for _, p := range plan.Trials {
		slot := liveSlotDir(out, p)
		var rec eval.LiveTrial
		switch raw, err := os.ReadFile(filepath.Join(slot, liveScoredRecord)); {
		case err == nil:
			if err := json.Unmarshal(raw, &rec); err != nil {
				return r, fmt.Errorf("live-eval: parsing %s: %w", filepath.Join(slot, liveScoredRecord), err)
			}
			r.scored = append(r.scored, rec)
		case !errors.Is(err, os.ErrNotExist):
			return r, fmt.Errorf("live-eval: reading %s: %w", filepath.Join(slot, liveScoredRecord), err)
		}
		limited, _ := filepath.Glob(filepath.Join(slot, "trial."+liveSlotLimited+"-*.json"))
		sort.Strings(limited)
		for _, f := range limited {
			raw, err := os.ReadFile(f)
			if err != nil {
				return r, fmt.Errorf("live-eval: reading %s: %w", f, err)
			}
			var lim eval.LiveTrial
			if err := json.Unmarshal(raw, &lim); err != nil {
				return r, fmt.Errorf("live-eval: parsing %s: %w", f, err)
			}
			r.limited = append(r.limited, lim)
		}
		discarded, _ := filepath.Glob(filepath.Join(slot, "trial."+liveSlotDiscarded+"-*.json"))
		r.discarded += len(discarded)
	}
	return r, nil
}

// liveArmCounts is each selected arm's trials per task: --trials for every arm when given, else the
// arm's pre-registered count in the task set.
func liveArmCounts(ts eval.LiveTaskSet, o liveOptions) (map[string]int, error) {
	counts := make(map[string]int, len(o.arms))
	for _, a := range o.arms {
		n := o.trials
		if n == 0 {
			n = ts.Analysis.TrialsFor(a)
		}
		if n <= 0 {
			return nil, fmt.Errorf("live-eval: task set %s pre-registers no trials for the %s arm; pass --trials", ts.ID, a)
		}
		counts[a] = n
	}
	return counts, nil
}

// liveUniformCount is the one count every arm shares, or 0 when they differ.
func liveUniformCount(counts map[string]int, arms []string) int {
	n := 0
	for i, a := range arms {
		if i > 0 && counts[a] != n {
			return 0
		}
		n = counts[a]
	}
	return n
}

// liveCountsText renders the per-arm counts in arm order: "stock 3, classic 14".
func liveCountsText(counts map[string]int, arms []string) string {
	parts := make([]string, 0, len(arms))
	for _, a := range arms {
		parts = append(parts, fmt.Sprintf("%s %d", a, counts[a]))
	}
	return strings.Join(parts, ", ")
}

// liveArmsUsing names the arms, in order, that load the bundle a live-eval flag supplies.
func liveArmsUsing(states []eval.LiveArmState, bundle string) []string {
	var out []string
	for _, s := range states {
		if s.Bundle == bundle {
			out = append(out, s.Arm)
		}
	}
	return out
}

// planLiveBundles reads the bundles the selected arms need into the plan: --bundle for classic and
// generic, --bundle-mods for steer, both from one commit, each with its mod sources present or absent
// as its shape requires. It records which identity each Qompack arm loads and the operator's
// known-defect statement, which covers both. A run with no Qompack arm installs nothing.
func planLiveBundles(o *liveOptions, plan *eval.LivePlan, states []eval.LiveArmState) error {
	classic, mods := liveArmsUsing(states, eval.LiveBundleClassic), liveArmsUsing(states, eval.LiveBundleMods)
	if len(classic) == 0 && len(mods) == 0 {
		// --install describes how the plugin reaches the host; a run with no plugin arm has none.
		o.install, plan.Install = "none", "none"
		return nil
	}
	if len(classic) > 0 {
		id, err := readLiveBundle(o.bundle, eval.LiveBundleClassic, classic, false)
		if err != nil {
			return err
		}
		id.Install = o.install
		plan.Plugin = &id
		// Every session runs in its disposable project, so the host would resolve a relative
		// --plugin-dir against that project and load nothing. From here on each bundle is named by
		// the absolute path its identity was read from.
		o.bundle = id.BundleDir
	}
	if len(mods) > 0 {
		id, err := readLiveBundle(o.bundleMods, eval.LiveBundleMods, mods, true)
		if err != nil {
			return err
		}
		id.Install = o.install
		plan.PluginMods = &id
		o.bundleMods = id.BundleDir
	}
	if c, m := plan.Plugin, plan.PluginMods; c != nil && m != nil && (c.Commit != m.Commit || c.Version != m.Version) {
		return fmt.Errorf("live-eval: --bundle (%s at %s) and --bundle-mods (%s at %s) must be built from one commit",
			c.Version, c.Commit, m.Version, m.Commit)
	}
	if !o.defectsAttested {
		return fmt.Errorf("live-eval: a Qompack arm needs --known-open-defects: %q, or the checklist IDs of "+
			"every known defect the bundles still carry. Preregistration section 9 counts only a run on a "+
			"candidate with no known open defect as the confirmatory run, and a bundle cannot prove which "+
			"defects it fixes, so the operator states it and plan.json records the statement", liveNoOpenDefects)
	}
	plan.KnownDefects = &eval.LiveDefectAttestation{Open: append([]string{}, o.openDefects...), Source: liveDefectSource}
	plan.ArmPlugins = map[string]*eval.LivePluginIdentity{}
	for _, a := range classic {
		plan.ArmPlugins[a] = plan.Plugin
	}
	for _, a := range mods {
		plan.ArmPlugins[a] = plan.PluginMods
	}
	return nil
}

// liveRunsPreregisteredModel reports whether model is what a run of ts may run on under its
// pre-registration: the model ts declares, or the one contingency alias the pre-registration
// permits in its place (section 3). A task set with no pre-registration has only its own model.
func liveRunsPreregisteredModel(ts eval.LiveTaskSet, model string) bool {
	if pre, ok := eval.LivePreregistrations[ts.ID]; ok {
		return pre.RunsPreregisteredModel(ts.Analysis.Model, model)
	}
	return model == ts.Analysis.Model
}

// liveDryRunHost is what a dry run shows of the host side: each arm's command line for task t (the
// session id is chosen per trial) and environment, and, for the marketplace flow, the plugin
// commands a Qompack arm's trial runs before its session, so the install path can be checked without
// starting anything.
func liveDryRunHost(o liveOptions, env *liveEnv, d eval.LiveTaskDefaults, t eval.LiveTask) []string {
	bin := env.claudeBin
	if bin == "" {
		bin = "claude"
	}
	var out []string
	for _, arm := range o.arms {
		state, _ := eval.LiveArmSpec(arm)
		bundle := o.liveArmBundle(state)
		var extra []string
		if state.Plugin && o.install == liveInstallPluginDir {
			extra = []string{"--plugin-dir", bundle}
		}
		if state.Plugin && o.install == liveInstallMarketplace {
			id := livePluginName + "@" + liveMarketplaceName
			out = append(out,
				fmt.Sprintf("install (%s, per trial): %s plugin marketplace add <trial>/marketplace --scope local "+
					"(a copy of %s)", arm, bin, bundle),
				fmt.Sprintf("install (%s, per trial): %s plugin install %s --scope local -y", arm, bin, id))
		}
		args := liveSessionArgs(o.model, t, d, "<session-id>", extra)
		out = append(out,
			fmt.Sprintf("host (%s, task %s): %s %s", arm, t.ID, bin, strings.Join(args, " ")),
			fmt.Sprintf("env (%s): %s", arm, strings.Join(liveSessionEnv(state, o.idleExit), " ")))
	}
	return out
}

// liveArmBundle is the bundle directory an arm's plugin loads from: --bundle-mods for steer,
// --bundle for the other Qompack arms, none for stock.
func (o liveOptions) liveArmBundle(state eval.LiveArmState) string {
	switch state.Bundle {
	case eval.LiveBundleMods:
		return o.bundleMods
	case eval.LiveBundleClassic:
		return o.bundle
	}
	return ""
}

// selectLiveTasks applies --only and the held-out rule.
func selectLiveTasks(ts eval.LiveTaskSet, o liveOptions) ([]eval.LiveTask, error) {
	known := map[string]bool{}
	var out []eval.LiveTask
	for _, t := range ts.Tasks {
		known[t.ID] = true
		if t.HeldOut && !o.includeHeldOut {
			if o.only[t.ID] {
				return nil, fmt.Errorf("live-eval: task %s is held out; pass --include-held-out (confirmatory run only)", t.ID)
			}
			continue
		}
		if o.only != nil && !o.only[t.ID] {
			continue
		}
		out = append(out, t)
	}
	for id := range o.only {
		if !known[id] {
			return nil, fmt.Errorf("live-eval: --only names unknown task %q", id)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("live-eval: no task selected")
	}
	return out, nil
}

// liveTrialRun is one trial's inputs.
type liveTrialRun struct {
	opts  liveOptions
	env   *liveEnv
	set   eval.LiveTaskSet
	task  eval.LiveTask
	arm   string
	state eval.LiveArmState
	trial int
	runID string
	rates eval.LiveRateTable
	// plan is the run's plan: the pinned host version and each arm's bundle identity.
	plan *eval.LivePlan
	// rateBefore is the rate-limit snapshot of every event before this attempt: the base of its
	// utilization deltas (W6 item 8).
	rateBefore eval.LiveRateSnapshot
}

// plugin is the bundle identity the trial's arm loads; nil for stock.
func (lt liveTrialRun) plugin() *eval.LivePluginIdentity {
	if !lt.state.Plugin {
		return nil
	}
	return lt.plan.ArmPlugins[lt.arm]
}

// run executes one trial and returns its record. The error is non-nil only when the guard over the
// operator's configuration failed, which stops the whole run. Whatever path the trial leaves by, a
// harness failure is scored as a failure on every outcome before the record is returned or written
// (preregistration §8): a trial stopped before grading must still count in every denominator.
//
// A trial whose configuration contradicted its arm (W6 item 4) comes back with ConfigurationError
// set: its record is set aside in the slot as trial.discarded-<n>.json, never written as trial.json,
// and the caller runs the slot again. Setting it aside failing stops the run, so a slot never
// silently loses an attempt.
func (lt liveTrialRun) run(ctx context.Context) (rec eval.LiveTrial, guardErr error) {
	o, env, task, state := lt.opts, lt.env, lt.task, lt.state
	rec = eval.LiveTrial{
		Schema: eval.LiveTrialSchema, RunID: lt.runID, TaskID: task.ID, Arm: lt.arm, Trial: lt.trial,
		Category: task.Category, Variant: task.Variant, HeldOut: task.HeldOut, Model: o.model,
		Install: "none", StartedAt: env.now().UTC().Format(time.RFC3339),
		PluginExpected:     state.Plugin,
		PreregisteredModel: liveRunsPreregisteredModel(lt.set, o.model),
		ArmState:           &state,
	}
	trialDir := filepath.Join(o.out, "trials", task.ID, lt.arm, fmt.Sprintf("%02d", lt.trial))
	if err := os.MkdirAll(trialDir, liveDirPerm); err != nil {
		rec.HarnessError = err.Error()
		rec.ApplyHarnessFailure(task)
		return rec, nil
	}
	defer func() {
		if r := recover(); r != nil {
			// A panic leaves no record at all: a trial.json written now would be an ungraded failure
			// that --resume counts as scored. The slot stays open and the panic goes on.
			panic(r)
		}
		rec.ApplyHarnessFailure(task)
		// A configuration error outranks a limit: the attempt was not the arm's, whatever cut it short.
		kind := ""
		switch {
		case rec.ConfigurationError != "":
			kind = liveSlotDiscarded
		case rec.Limited != "":
			kind = liveSlotLimited
		default:
			_ = writeJSONFile(filepath.Join(trialDir, liveScoredRecord), rec)
			return
		}
		if _, err := liveSetAside(trialDir, kind, rec); err != nil && guardErr == nil {
			guardErr = fmt.Errorf("live-eval: setting the %s attempt of %s/%s/%d aside: %w", kind, task.ID, lt.arm, lt.trial, err)
		}
	}()

	// A filesystem timestamp can trail the wall clock by its resolution; the margin keeps a directory
	// the host creates in the trial's first instant from reading as older than the trial.
	trialStart := env.now().Add(-liveMTimeMargin)
	before, err := env.guardSnapshot()
	if err != nil {
		rec.HarnessError = "guard: " + err.Error()
		return rec, fmt.Errorf("live-eval: the configuration guard could not read the operator's files: %w", err)
	}
	guard := &eval.LiveHomeGuard{Checked: true, Before: before.lines()}
	rec.HomeGuard = guard

	work, err := os.MkdirTemp(env.workRoot, "qompack-live-")
	if err != nil {
		rec.HarnessError = err.Error()
		return rec, nil
	}
	project := filepath.Join(work, "project")
	rec.Notes = append(rec.Notes, "project: "+project)
	fixture := task.FixtureDir(o.tasksFile)
	fixtureHashes, err := eval.HashTree(fixture)
	if err == nil {
		err = copyTree(fixture, project)
	}
	if err == nil {
		// The arm's own project (W6 items 4 and 7) is made before the fixture commit, so `git diff`
		// shows the session's changes only.
		err = applyLiveArmProject(project, state, lt.set.Analysis.GenericCompactInstructions)
	}
	if err == nil {
		err = env.gitInit(ctx, project)
	}
	if err != nil {
		rec.HarnessError = "preparing the project: " + err.Error()
		return rec, nil
	}
	sessionEnv := liveSessionEnv(state, o.idleExit)
	if cfgErr := lt.checkConfiguration(project, fixture, sessionEnv); cfgErr != nil {
		// No host starts, so nothing the guard watches could change: the record carries no guard
		// verdict rather than an unfinished one.
		rec.ConfigurationError = cfgErr.Error()
		rec.HomeGuard = nil
		return rec, nil
	}

	var cleanup []func() []string
	extraArgs := []string{}
	if id := lt.plugin(); id != nil {
		own := *id
		rec.Plugin = &own
		rec.Install = o.install
		switch o.install {
		case liveInstallPluginDir:
			extraArgs = append(extraArgs, "--plugin-dir", id.BundleDir)
		case liveInstallMarketplace:
			undo, instErr := env.marketplaceInstall(ctx, project, id.BundleDir, work)
			cleanup = append(cleanup, undo)
			if instErr != nil {
				rec.HarnessError = "marketplace install: " + instErr.Error()
			}
		}
	}

	var proc liveProcResult
	sessionID := newUUID()
	if rec.HarnessError == "" {
		spec := liveProcSpec{
			Bin:            env.claudeBin,
			Args:           liveSessionArgs(o.model, task, lt.set.Defaults, sessionID, extraArgs),
			Dir:            project,
			Env:            sessionEnv,
			Messages:       liveMessages(task),
			StepTimeout:    o.stepTimeout,
			SessionTimeout: o.sessionTimeout,
			StopOn:         liveOverageStop,
		}
		spec.Unset = liveUnsetNames(os.Environ(), spec.Env)
		if len(spec.Unset) > 0 {
			rec.Notes = append(rec.Notes, "inherited environment variables removed from the session: "+
				strings.Join(spec.Unset, ", "))
		}
		_ = writeJSONFile(filepath.Join(trialDir, "invocation.json"), spec)
		proc = env.run(ctx, spec)
		rec.WallMS = proc.EndedAt.Sub(proc.StartedAt).Milliseconds()
		rec.ExitCode = proc.ExitCode
		if proc.Err != "" {
			rec.HarnessError = proc.Err
		}
		_ = os.WriteFile(filepath.Join(trialDir, "stream.jsonl"), proc.Stream, liveFilePerm)
		_ = os.WriteFile(filepath.Join(trialDir, "stderr.txt"), proc.Stderr, liveFilePerm)
	}

	if state.Plugin {
		how, stopErr := env.stopDaemon(project, filepath.Join(trialDir, "harness-spool"))
		rec.Notes = append(rec.Notes, "trial daemon: "+how)
		if stopErr != nil {
			rec.Notes = append(rec.Notes, "trial daemon stop: "+stopErr.Error())
		}
	}
	for i := len(cleanup) - 1; i >= 0; i-- {
		guard.Leftovers = append(guard.Leftovers, cleanup[i]()...)
	}
	if id := lt.plugin(); id != nil && o.install == liveInstallMarketplace {
		guard.Leftovers = append(guard.Leftovers, env.removeOrphanedMarketplaceCache(before, id.BundleSHA256)...)
	}
	guard.Leftovers = append(guard.Leftovers, env.removeCreatedPluginData(before, trialStart)...)

	after, err := env.guardSnapshot()
	if err != nil {
		guardErr = fmt.Errorf("live-eval: the configuration guard could not re-read the operator's files: %w", err)
	} else {
		guard.After = after.lines()
		guard.Unchanged = before.equal(after) && len(guard.Leftovers) == 0
		if !guard.Unchanged {
			guardErr = fmt.Errorf("live-eval: the operator's Claude Code configuration changed during %s/%s/%d "+
				"(before %v, after %v, leftovers %v); restore it before running again",
				task.ID, lt.arm, lt.trial, guard.Before, guard.After, guard.Leftovers)
		}
	}

	lt.assemble(&rec, proc, project, trialDir, fixtureHashes, sessionID)
	return rec, guardErr
}

// assemble parses what the session left and grades it into rec.
func (lt liveTrialRun) assemble(rec *eval.LiveTrial, proc liveProcResult, project, trialDir string,
	fixtureHashes map[string]string, sessionID string,
) {
	task, env := lt.task, lt.env
	stream, err := eval.ParseHostStream(strings.NewReader(string(proc.Stream)), proc.RecvMS)
	if err != nil {
		rec.HarnessError = joinErr(rec.HarnessError, "parsing the stream: "+err.Error())
	}
	rec.RateLimits = stream.RateLimits
	if use := eval.LiveRateUse(lt.rateBefore, stream.RateLimits); len(use) > 0 {
		rec.RateUse = use
	}
	rec.Limited = eval.LiveUsageLimited(stream)
	if proc.Stopped != "" {
		// The driver cut the session short (review I1): never a scored trial.
		rec.Limited = "the session was stopped: " + proc.Stopped
	}
	rec.OverageStatus, rec.OverageDisabledReason = eval.LiveOverageState(stream.RateLimits)
	rec.HostReportedPlugins = stream.Init != nil
	if stream.Init != nil {
		rec.SessionID = stream.Init.SessionID
		rec.ClaudeCodeVersion = stream.Init.ClaudeCodeVersion
		lt.recordPlugins(rec, stream)
		rec.HostModel = stream.Init.Model
		if stream.Init.Model != "" && stream.Init.Model != rec.Model {
			rec.Notes = append(rec.Notes, "host reported model "+stream.Init.Model)
		}
		if v := stream.Init.ClaudeCodeVersion; v != lt.plan.ClaudeCLIVersion {
			rec.ConfigurationError = joinErr(rec.ConfigurationError, fmt.Sprintf(
				"the host's init line reports Claude Code %s, not the pinned %s", orUnknown(v), lt.plan.ClaudeCLIVersion))
		}
		if problem := lt.pluginListProblem(rec, stream); problem != "" {
			rec.ConfigurationError = joinErr(rec.ConfigurationError, problem)
		}
	}
	if rec.SessionID != "" && rec.SessionID != sessionID {
		rec.Notes = append(rec.Notes, fmt.Sprintf("host session id %s differs from the requested %s", rec.SessionID, sessionID))
	}
	steps, complete := eval.StepRecords(task, stream)
	rec.Steps, rec.Completed = steps, complete && rec.HarnessError == ""
	for _, t := range stream.Turns {
		rec.Compactions = append(rec.Compactions, t.Compactions...)
		rec.CompactAttempts = append(rec.CompactAttempts, t.CompactAttempts...)
	}
	rec.HookLatencyMS = stream.HookLatencies()
	streamProblems := stream.HookProblems()
	stderrProblems := eval.ParseHookFailures(string(proc.Stderr), -1)
	var transcriptProblems []eval.HostHookProblem

	acct := eval.AccountHostStream(stream, eval.AccountBaseline{})
	rec.Account = acct
	rec.HostCostUSD = acct.HostCostUSD
	ledger := eval.RequestLedger{
		Version: eval.RequestLedgerVersion,
		Records: acct.LedgerRecords(fmt.Sprintf("%s-%s-%02d", task.ID, lt.arm, lt.trial), "anthropic",
			eval.PricingSubscription, lt.rates.Date),
	}
	rec.Categories = ledger.Sum()
	est := eval.EstimateLive(ledger, lt.rates)
	rec.Estimate, rec.EstimateComplete = &est.Total, est.Completeness.Complete
	rec.EstimateMissing, rec.RateTableDate = est.Completeness.MissingCategories, est.RateTableDate
	_ = writeJSONFile(filepath.Join(trialDir, "ledger.json"), ledger)
	_ = writeJSONFile(filepath.Join(trialDir, "estimate.json"), est)

	transcriptPath, tr, trErr := env.readTranscript(sessionID, rec.SessionID)
	if trErr != nil {
		rec.Notes = append(rec.Notes, "transcript: "+trErr.Error())
	} else {
		rec.TranscriptHookMS = tr.HookLatencies()
		transcriptProblems = tr.HookProblems()
		_ = writeJSONFile(filepath.Join(trialDir, "transcript-facts.json"), tr)
		rec.Notes = append(rec.Notes, "host transcript: "+transcriptPath)
		if lt.opts.keepRaw {
			_ = copyFile(transcriptPath, filepath.Join(trialDir, "transcript.raw.jsonl"))
		}
	}
	rec.HookProblems = eval.MergeHookProblems(streamProblems, transcriptProblems, stderrProblems)
	if lt.state.ModSteering {
		// A treatment outcome, judged against the session the stream reported (or the one requested
		// when the host never said): counted as assigned whatever it says.
		sid := rec.SessionID
		if sid == "" {
			sid = sessionID
		}
		rec.Hello = judgeLiveHello(project, sid)
	}
	if lt.state.Plugin {
		q := &eval.LiveQompackEvidence{MCPToolCalls: eval.MCPToolCalls(stream, liveMCPToolPrefix)}
		if stream.Init != nil {
			for _, m := range stream.Init.MCPServers {
				if m.Name == liveMCPServerName {
					q.MCPServerStatus = m.Status
				}
			}
		}
		if trErr == nil {
			q.Injections, q.InjectedBytes = tr.QompackInjections()
		}
		st, idx, cps := liveStoreStats(project)
		rec.Store, q.IndexLines, q.Checkpoints = st, idx, cps
		rec.Qompack = q
	}

	if task.HiddenFixture != "" {
		if err := copyTree(filepath.Join(filepath.Dir(lt.opts.tasksFile), filepath.FromSlash(task.HiddenFixture)), project); err != nil {
			rec.HarnessError = joinErr(rec.HarnessError, "overlaying the hidden fixture: "+err.Error())
		}
	}
	commands := map[string]eval.CommandOutcome{}
	for _, c := range task.Checks {
		if c.Kind == eval.CheckCommand {
			commands[c.ID] = env.runCheckCommand(project, c.Argv)
		}
	}
	rec.ApplyGrades(eval.GradeLiveTrial(task, project, eval.LiveEvidence{
		Answers: eval.AnswersByStep(task, stream), Fixture: fixtureHashes, Commands: commands,
		ToolUsesAfter: eval.ToolUsesAfterSteps(task, stream),
	}))
	if rec.HarnessError != "" {
		rec.Completed = false
	}
}

// recordPlugins decides whether the trial ran with its arm's plugin and names anything else the host
// loaded. On a Qompack arm the plugin counts only when the host loaded it from the arm's own
// install — the inline --plugin-dir source or the disposable marketplace — because a Qompack copy
// from anywhere else (an operator install, another lane's marketplace) is not the frozen bundle the
// trial is about. On the stock arm any Qompack plugin, from any source, contradicts the arm.
func (lt liveTrialRun) recordPlugins(rec *eval.LiveTrial, stream eval.HostStream) {
	if !lt.state.Plugin {
		rec.PluginLoaded = stream.PluginLoaded(livePluginName)
		rec.ForeignPlugins = stream.ForeignPlugins("")
		return
	}
	want := lt.ownPluginSource()
	rec.ForeignPlugins = stream.ForeignPlugins(want)
	pl, ok := stream.PluginFrom(livePluginName, want)
	rec.PluginLoaded = ok
	if !ok {
		if other, found := stream.PluginFrom(livePluginName, ""); found {
			rec.Notes = append(rec.Notes, fmt.Sprintf("the host loaded Qompack from %s (%s), not from this "+
				"arm's install (%s): not counted as the arm's plugin", other.Source, other.Path, want))
		}
		return
	}
	if rec.Plugin != nil {
		rec.Plugin.HostVersion, rec.Plugin.HostSource, rec.Plugin.HostPath = pl.Version, pl.Source, pl.Path
		if pl.Version != rec.Plugin.Version {
			rec.Notes = append(rec.Notes, fmt.Sprintf("the host reported plugin version %s; the bundle is %s",
				pl.Version, rec.Plugin.Version))
		}
	}
}

// ownPluginSource is the source the host lists the arm's own Qompack install under.
func (lt liveTrialRun) ownPluginSource() string {
	if lt.opts.install == liveInstallMarketplace {
		return livePluginName + "@" + liveMarketplaceName
	}
	return livePluginName + "@" + liveInlineSource
}

// pluginListProblem is the configuration check on the host's own list of loaded plugins (W6 item 4):
// exactly one Qompack, from the arm's install, on a Qompack arm, and none on stock. "" when it holds.
// It is called only when the host printed that list; a trial whose host never did is a harness
// failure, scored as one.
func (lt liveTrialRun) pluginListProblem(rec *eval.LiveTrial, stream eval.HostStream) string {
	var sources []string
	for _, pl := range stream.Init.Plugins {
		if strings.EqualFold(pl.Name, livePluginName) {
			sources = append(sources, orUnknown(pl.Source))
		}
	}
	switch {
	case !lt.state.Plugin && len(sources) > 0:
		return fmt.Sprintf("the host loaded Qompack (%s) on the %s arm, which loads none", strings.Join(sources, ", "), lt.arm)
	case lt.state.Plugin && (len(sources) != 1 || !rec.PluginLoaded):
		return fmt.Sprintf("the host loaded %d Qompack plugin(s) (%s), not exactly one from the %s arm's install (%s)",
			len(sources), strings.Join(sources, ", "), lt.arm, lt.ownPluginSource())
	}
	return ""
}

// checkConfiguration is the arm-state check made before the session starts (W6 item 4): the arm's
// bundle still hashes to the identity the plan recorded and carries the mod sources exactly when the
// arm is steer, the project is the arm's own (store, CLAUDE.md), and the session environment is the
// arm's. A failure is a configuration error.
func (lt liveTrialRun) checkConfiguration(project, fixture string, sessionEnv []string) error {
	if id := lt.plugin(); id != nil {
		cur, err := readLiveBundle(id.BundleDir, lt.state.Bundle, []string{lt.arm}, lt.state.Bundle == eval.LiveBundleMods)
		if err != nil {
			return err
		}
		if cur.BundleSHA256 != id.BundleSHA256 {
			return fmt.Errorf("the %s arm's bundle now hashes to %s, not the plan's %s", lt.arm,
				shortHash(cur.BundleSHA256), shortHash(id.BundleSHA256))
		}
	}
	if err := checkLiveArmProject(project, fixture, lt.state, lt.set.Analysis.GenericCompactInstructions); err != nil {
		return err
	}
	return checkLiveArmEnv(lt.state, lt.opts.idleExit, sessionEnv)
}

// liveSessionArgs is the host command line for one trial, identical across arms except extra.
func liveSessionArgs(model string, t eval.LiveTask, d eval.LiveTaskDefaults, sessionID string, extra []string) []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-hook-events",
		"--model", model,
		"--max-turns", fmt.Sprint(t.EffectiveMaxTurns(d)),
		"--setting-sources", "project,local",
		"--permission-mode", "dontAsk",
		"--session-id", sessionID,
	}
	if tools := t.EffectiveAllowedTools(d); len(tools) > 0 {
		args = append(args, "--allowedTools", strings.Join(tools, ","))
	}
	return append(args, extra...)
}

// liveMessages is each step's user message. Every arm sends the same ones, compaction instructions
// included (W6 item 3); generic's difference is in its project's CLAUDE.md, not in a message.
func liveMessages(t eval.LiveTask) []string {
	out := make([]string, 0, len(t.Steps))
	for _, s := range t.Steps {
		out = append(out, s.Message())
	}
	return out
}

func loadLiveRates(path string) (eval.LiveRateTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return eval.LiveRateTable{}, fmt.Errorf("live-eval: reading the rate table: %w", err)
	}
	return eval.ParseLiveRateTable(raw)
}

// readLiveBundle verifies a bundle directory against its own BUNDLE.json and returns its identity.
// flag is the live-eval flag that named it and arms the arms that load it, for the messages; mods says
// which shape the arms need: the mod sources all listed (a --mods build), or none of them.
func readLiveBundle(dir, flag string, arms []string, mods bool) (eval.LivePluginIdentity, error) {
	if dir == "" {
		how := "`go run ./tools/devtool bundle --target " + runtime.GOOS + "/" + runtime.GOARCH + "`"
		if mods {
			how = "`go run ./tools/devtool bundle --mods --target " + runtime.GOOS + "/" + runtime.GOARCH + "`"
		}
		return eval.LivePluginIdentity{}, fmt.Errorf("live-eval: the %s arm(s) need --%s <dir> (assemble one with %s)",
			strings.Join(arms, " and "), flag, how)
	}
	raw, err := os.ReadFile(filepath.Join(dir, identityFileName))
	if err != nil {
		return eval.LivePluginIdentity{}, fmt.Errorf("live-eval: reading the --%s identity: %w", flag, err)
	}
	var id bundleIdentity
	if err := json.Unmarshal(raw, &id); err != nil {
		return eval.LivePluginIdentity{}, fmt.Errorf("live-eval: parsing %s: %w", identityFileName, err)
	}
	if id.Target.OS != runtime.GOOS || id.Target.Arch != runtime.GOARCH {
		return eval.LivePluginIdentity{}, fmt.Errorf("live-eval: bundle is for %s/%s, this host is %s/%s",
			id.Target.OS, id.Target.Arch, runtime.GOOS, runtime.GOARCH)
	}
	listed := make(map[string]bool, len(id.Files))
	for _, f := range id.Files {
		sum, _, hashErr := hashFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if hashErr != nil || sum != f.SHA256 {
			return eval.LivePluginIdentity{}, fmt.Errorf("live-eval: bundle file %s does not match BUNDLE.json", f.Path)
		}
		listed[f.Path] = true
	}
	modFiles, err := liveModFiles(runtime.GOOS)
	if err != nil {
		return eval.LivePluginIdentity{}, err
	}
	for _, m := range modFiles {
		switch {
		case mods && !listed[m]:
			return eval.LivePluginIdentity{}, fmt.Errorf("live-eval: --%s lacks the mod source %s, which the %s arm "+
				"loads: assemble it with `devtool bundle --mods`", flag, m, strings.Join(arms, " and "))
		case !mods && listed[m]:
			return eval.LivePluginIdentity{}, fmt.Errorf("live-eval: --%s carries the mod sources (%s), which the %s "+
				"arm(s) must run without: assemble it without --mods", flag, m, strings.Join(arms, " and "))
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return eval.LivePluginIdentity{}, fmt.Errorf("live-eval: resolving the bundle directory: %w", err)
	}
	return eval.LivePluginIdentity{
		BundleDir: abs, Version: id.Version, Commit: id.Source.Commit, Dirty: id.Source.Dirty,
		BundleSHA256: sha256Hex(raw), Mods: mods,
	}, nil
}

// orUnknown renders an empty value as "unknown".
func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// shortHash abbreviates a hex digest for a message.
func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return orUnknown(h)
}

// liveStoreStats sizes a project's .qompack/ tree and counts index lines and checkpoints.
func liveStoreStats(project string) (*eval.LiveStoreStats, map[string]int, int) {
	dir := filepath.Join(project, ".qompack")
	st := &eval.LiveStoreStats{}
	idx := map[string]int{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		// An unreadable entry, or one that vanished under the walk, is not counted: the totals
		// say what was read, and a trial's store is sized after its daemon has stopped.
		if err != nil || d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		st.Files++
		st.Bytes += info.Size()
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "objects/") {
			st.Objects++
		}
		if strings.HasPrefix(rel, "index/") && strings.HasSuffix(rel, ".jsonl") {
			idx[strings.TrimPrefix(rel, "index/")] = countLines(p)
		}
		return nil
	})
	cps := countLines(filepath.Join(dir, "checkpoints", "MANIFEST.jsonl"))
	st.Checkpoints = int64(cps)
	return st, idx, cps
}

func countLines(p string) int {
	raw, err := os.ReadFile(p)
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// copyTree copies every regular file under src into dst, creating directories as needed.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, liveDirPerm)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("fixture entry %s is not a regular file", rel)
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), liveDirPerm); err != nil {
		return err
	}
	return os.WriteFile(dst, raw, liveFilePerm)
}

// writeJSONFile writes v as indented JSON through writeFileAtomic: `qompack eval` reads a run's
// plan.json and summary.json, and may do so while the run is finishing or after it crashed.
func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("live-eval: encoding %s: %w", filepath.Base(path), err)
	}
	if err := writeFileAtomic(path, append(b, '\n')); err != nil {
		return fmt.Errorf("live-eval: writing %s: %w", path, err)
	}
	return nil
}

// writeFileAtomic writes b to a staging file beside path and renames it over path, so a reader sees
// the old file or the whole new one and never a prefix: os.WriteFile truncates and rewrites in
// place, which a concurrent reader sees half-written and a crash leaves half-written for good. The
// staging file is in path's own directory, so the rename never crosses a volume.
func writeFileAtomic(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// os.CreateTemp creates 0o600; the run's records are read by other tools and by the operator.
	if err := os.Chmod(tmp, liveFilePerm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	renamed = true
	return nil
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func liveRunID(now time.Time) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}

// newUUID returns a random RFC 4122 version-4 UUID, the form --session-id requires.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func joinErr(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

// renderLiveSummary is the human-readable summary written beside summary.json.
func renderLiveSummary(p eval.LivePlan, s eval.LiveSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Live evaluation %s\n\n", p.RunID)
	fmt.Fprintf(&b, "Task set `%s` (sha256 `%s`; fixture tree %v manifest sha256 `%s`), model `%s` "+
		"(pre-registered `%s`), arms %s, install `%s`, Claude Code %s. %s.\n\n", p.TaskSet, p.TaskSetSHA256,
		p.FixtureTreeDirs, p.FixtureTreeSHA256, p.Model, p.PreregisteredModel,
		strings.Join(p.Arms, ", "), p.Install, p.ClaudeCLIVersion, p.Agent)
	for _, bundle := range []struct {
		flag string
		id   *eval.LivePluginIdentity
	}{{eval.LiveBundleClassic, p.Plugin}, {eval.LiveBundleMods, p.PluginMods}} {
		if id := bundle.id; id != nil {
			fmt.Fprintf(&b, "--%s: %s at commit `%s` (dirty=%t), %s sha256 `%s`.\n\n", bundle.flag, id.Version, id.Commit,
				id.Dirty, identityFileName, id.BundleSHA256)
		}
	}
	if len(p.ArmTrials) > 0 {
		fmt.Fprintf(&b, "Trials per task: %s; order seed %d; daemon idle exit %d s.\n\n", liveCountsText(p.ArmTrials, p.Arms),
			p.OrderSeed, p.DaemonIdleExitSeconds)
	}
	if p.Plugin != nil || p.PluginMods != nil {
		fmt.Fprintf(&b, "The bundles' known open defects: %s — the operator's statement when the run was planned, not "+
			"machine-checked (preregistration section 9 counts only a run with none as the confirmatory run).\n\n",
			knownDefectsText(p.KnownDefects))
	}
	fmt.Fprintf(&b, "Every cost figure is a list-price-equivalent ESTIMATE from the %s rate table; the sessions ran on a "+
		"subscription, which has no per-token cash charge.\n\n", p.RateTableDate)
	if s.Paused != "" {
		fmt.Fprintf(&b, "**Paused:** %s. The run continues with `devtool live-eval --resume <run-dir>`.\n\n", s.Paused)
	}
	fmt.Fprintf(&b, "**Decision (pre-registered rule, primary outcome):** %s — %s\n\n", s.Decision.Verdict, s.Decision.Reason)
	if s.ConstraintRegression != "" {
		fmt.Fprintf(&b, "**Regression (H2):** %s\n\n", s.ConstraintRegression)
	}
	renderArmTable(&b, s.Arms)
	for _, d := range []struct {
		name string
		diff *eval.Difference
	}{
		{"Task-success", s.TaskSuccessDiff},
		{"Constraint-clean", s.ConstraintCleanDiff},
		{"Recovery", s.RecoveryDiff},
	} {
		if d.diff != nil {
			fmt.Fprintf(&b, "\n%s difference (qompack − stock): %.3f [%.3f, %.3f]\n", d.name, d.diff.Estimate, d.diff.Low, d.diff.High)
		}
	}
	renderHostHookTable(&b, s.Arms)
	renderLiveSubscription(&b, s)
	if len(s.ByVariant) > 0 {
		b.WriteString("\n## By variant\n\nReported, not decided (preregistration section 8).\n\n")
		b.WriteString("| variant | arm | trials | task success (95% CI) | constraint-clean (95% CI) | recovery (95% CI) |\n")
		b.WriteString("|---|---|---|---|---|---|\n")
		for _, v := range liveSortedKeys(s.ByVariant) {
			for _, as := range s.ByVariant[v] {
				fmt.Fprintf(&b, "| %s | %s | %d | %s | %s | %s |\n", v, as.Arm, as.Trials,
					fmtProportion(as.TaskSuccess), fmtProportion(as.ConstraintClean), fmtProportion(as.Recovery))
			}
		}
	}
	if len(s.TaskSigns) > 0 {
		b.WriteString("\n## Per-task sign (qompack − stock task success)\n\nReported, not decided.\n\n")
		for _, id := range liveSortedKeys(s.TaskSigns) {
			fmt.Fprintf(&b, "- %s: %s\n", id, signWord(s.TaskSigns[id]))
		}
	}
	if len(s.Failed) > 0 {
		b.WriteString("\n## Failed or incomplete trials\n\n")
		for _, f := range s.Failed {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	if len(s.Notes) > 0 {
		b.WriteString("\n## Notes\n\n")
		for _, n := range s.Notes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
	}
	return b.String()
}

// renderArmTable writes the per-arm outcome table.
func renderArmTable(b *strings.Builder, arms map[string]eval.ArmSummary) {
	b.WriteString("| arm | trials | completed | task success (95% CI) | constraint-clean (95% CI) | violations | " +
		"recovery (95% CI) | mean host cost USD | hook-problem trials | inconsistent accounts |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")
	for _, a := range liveSortedKeys(arms) {
		as := arms[a]
		fmt.Fprintf(b, "| %s | %d | %d | %s | %s | %d | %s | %.4f | %d | %d |\n", a, as.Trials, as.Completed,
			fmtProportion(as.TaskSuccess), fmtProportion(as.ConstraintClean), as.ConstraintViolations,
			fmtProportion(as.Recovery), as.MeanHostCost, as.HookProblemTrials, as.AccountInconsistent)
	}
}

// renderHostHookTable writes each arm's per-hook latency as the host measured it (transcript
// durationMs), the hook latency preregistration section 6 reports.
func renderHostHookTable(b *strings.Builder, arms map[string]eval.ArmSummary) {
	have := false
	for _, as := range arms {
		have = have || len(as.HostHookMS) > 0
	}
	if !have {
		return
	}
	b.WriteString("\n## Hook latency, as the host measured it\n\nTranscript durationMs per hook, over every trial " +
		"of the arm; reported, not decided (preregistration section 6).\n\n")
	b.WriteString("| arm | hook | runs | p50 ms | p95 ms | max ms |\n|---|---|---|---|---|---|\n")
	for _, arm := range liveSortedKeys(arms) {
		hooks := arms[arm].HostHookMS
		for _, name := range liveSortedKeys(hooks) {
			h := hooks[name]
			fmt.Fprintf(b, "| %s | %s | %d | %d | %d | %d |\n", arm, name, h.N, h.P50, h.P95, h.Max)
		}
	}
}

// renderLiveSubscription writes each arm's per-trial use of the subscription's windows and the
// limited trials with their sensitivity analysis (W6 item 8); nothing when the run has neither.
func renderLiveSubscription(b *strings.Builder, s eval.LiveSummary) {
	have := false
	for _, as := range s.Arms {
		have = have || len(as.RateUse) > 0
	}
	if have {
		b.WriteString("\n## Subscription use per trial\n\nEach trial's utilization delta per window, from the newest " +
			"rate_limit_event before it to its last; the input to the calendar estimate. Reported, not decided.\n\n")
		b.WriteString("| arm | window | trials | mean delta | max delta |\n|---|---|---|---|---|\n")
		for _, arm := range liveSortedKeys(s.Arms) {
			use := s.Arms[arm].RateUse
			for _, w := range liveSortedKeys(use) {
				fmt.Fprintf(b, "| %s | %s | %d | %.4f | %.4f |\n", arm, w, use[w].Trials, use[w].Mean, use[w].Max)
			}
		}
	}
	if len(s.LimitedTrials) > 0 {
		b.WriteString("\n## Limited trials\n\nTrials a subscription limit cut short, set aside and run again; excluded " +
			"from the analysis above. The sensitivity analysis counts each as a failure on every outcome; reported, " +
			"not decided.\n\n")
		b.WriteString("| arm | limited | task success with them as failures | recovery with them as failures |\n|---|---|---|---|\n")
		for _, arm := range liveSortedKeys(s.LimitedSensitivity) {
			sens := s.LimitedSensitivity[arm]
			fmt.Fprintf(b, "| %s | %d | %s | %s |\n", arm, s.LimitedTrials[arm], fmtProportion(sens.TaskSuccess),
				fmtProportion(sens.Recovery))
		}
	}
}

// liveSortedKeys is a map's keys in order.
func liveSortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// signWord renders a task sign.
func signWord(sign int) string {
	switch {
	case sign > 0:
		return "+1"
	case sign < 0:
		return "−1"
	default:
		return "0"
	}
}

func fmtProportion(p eval.Proportion) string {
	if p.N == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d/%d = %.2f [%.2f, %.2f]", p.K, p.N, p.Rate, p.Low, p.High)
}

// lookClaude resolves the host CLI. An explicit path is made absolute: every session starts in its
// disposable project, and a relative program path is resolved against the working directory the
// process starts in, not the one the driver was run from.
func lookClaude(explicit string) (string, error) {
	if explicit != "" {
		if !strings.ContainsAny(explicit, `/\`) {
			return explicit, nil // a bare name: looked up on PATH when the process starts
		}
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", fmt.Errorf("live-eval: resolving --claude %s: %w", explicit, err)
		}
		return abs, nil
	}
	p, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("live-eval: the Claude Code CLI is not on PATH (pass --claude): %w", err)
	}
	return p, nil
}
