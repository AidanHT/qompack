package main

// `devtool live-eval` — V6 close-out C5.4: the real-host evaluation driver.
//
// It runs a declared task set (testdata/eval/live/) as real headless Claude Code sessions, each in a
// disposable project copied from the task's fixture, in two arms: `stock` (no plugin) and `qompack`
// (the plugin loaded from a bundle, by --plugin-dir or through the real marketplace flow). Every
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
	liveDefaultTasks = "testdata/eval/live/tasks.json"
	liveDefaultRates = "testdata/eval/live/rates.json"
	liveDefaultOut   = "dist/live-eval"
)

// The two ways the qompack arm reaches the host.
const (
	liveInstallPluginDir   = "plugin-dir"
	liveInstallMarketplace = "marketplace"
)

// liveMCPToolPrefix is the prefix the host gives the plugin's MCP tools.
const liveMCPToolPrefix = "mcp__plugin_qompack_qompack__"

// liveMCPServerName is the name the host lists the plugin's MCP server under.
const liveMCPServerName = "plugin:qompack:qompack"

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
	liveDefaultIdleExit       = 120
	liveCommandOutputKeep     = 16 << 10
	liveDirPerm               = 0o755
	liveFilePerm              = 0o644
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
	arms := fs.String("arms", eval.ArmStock+","+eval.ArmQompack, "comma-separated arms: stock, qompack")
	fs.IntVar(&o.trials, "trials", 0, "trials per task per arm (default: the task set's pre-registered trials_per_arm)")
	fs.StringVar(&o.model, "model", "", "model ID (default: the task set's pre-registered model)")
	fs.StringVar(&o.out, "out", "", "output directory (default dist/live-eval/<run-id>)")
	fs.StringVar(&o.install, "install", liveInstallPluginDir, "how the qompack arm loads the plugin: plugin-dir or marketplace")
	fs.StringVar(&o.bundle, "bundle", "", "assembled host-target bundle directory (from `devtool bundle`); required for the qompack arm")
	fs.StringVar(&o.claude, "claude", "", "Claude Code CLI (default: `claude` on PATH)")
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
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("live-eval: unexpected argument %q", fs.Arg(0))
	}
	for _, a := range strings.Split(*arms, ",") {
		a = strings.TrimSpace(a)
		switch a {
		case eval.ArmStock, eval.ArmQompack:
			o.arms = append(o.arms, a)
		case "":
		default:
			return o, fmt.Errorf("live-eval: unknown arm %q (stock, qompack)", a)
		}
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
	if o.trials < 0 || o.maxSessions < 0 || o.idleExit <= 0 {
		return o, errors.New("live-eval: --trials and --max-sessions must be non-negative and --daemon-idle-exit positive")
	}
	return o, nil
}

// livePlanned is one planned trial.
type livePlanned struct {
	Task  string `json:"task"`
	Arm   string `json:"arm"`
	Trial int    `json:"trial"`
}

// livePlan is the run's plan document, written before the first session starts.
type livePlan struct {
	RunID              string                   `json:"run_id"`
	CreatedAt          string                   `json:"created_at"`
	TaskSet            string                   `json:"task_set"`
	TaskSetFile        string                   `json:"task_set_file"`
	TaskSetSHA256      string                   `json:"task_set_sha256"`
	Model              string                   `json:"model"`
	PreregisteredModel string                   `json:"preregistered_model"`
	Arms               []string                 `json:"arms"`
	TrialsPerArm       int                      `json:"trials_per_arm"`
	Install            string                   `json:"install"`
	Plugin             *eval.LivePluginIdentity `json:"plugin,omitempty"`
	ClaudeCLI          string                   `json:"claude_cli"`
	ClaudeCLIVersion   string                   `json:"claude_cli_version"`
	RateTableDate      string                   `json:"rate_table_date"`
	RateTableSource    string                   `json:"rate_table_source"`
	HeldOutIncluded    bool                     `json:"held_out_included"`
	Trials             []livePlanned            `json:"trials"`
	Host               string                   `json:"host"`
	Agent              string                   `json:"agent"`
}

// runLiveEval is the whole run: validate, plan, gate, execute, summarize.
func runLiveEval(ctx context.Context, o liveOptions, env *liveEnv, w io.Writer) error {
	ts, raw, err := eval.LoadLiveTaskSet(o.tasksFile)
	if err != nil {
		return err
	}
	rates, err := loadLiveRates(o.rates)
	if err != nil {
		return err
	}
	if o.model == "" {
		o.model = ts.Analysis.Model
	}
	if o.trials == 0 {
		o.trials = ts.Analysis.TrialsPerArm
	}
	tasks, err := selectLiveTasks(ts, o)
	if err != nil {
		return err
	}
	runID := liveRunID(env.now())
	if o.out == "" {
		o.out = filepath.Join(liveDefaultOut, runID)
	}

	plan := livePlan{
		RunID: runID, CreatedAt: env.now().UTC().Format(time.RFC3339), TaskSet: ts.ID,
		TaskSetFile: filepath.ToSlash(o.tasksFile), TaskSetSHA256: sha256Hex(raw),
		Model: o.model, PreregisteredModel: ts.Analysis.Model, Arms: o.arms, TrialsPerArm: o.trials,
		Install: o.install, RateTableDate: rates.Date, RateTableSource: rates.Source,
		HeldOutIncluded: o.includeHeldOut, Host: runtime.GOOS + "/" + runtime.GOARCH,
		Agent: "agent-executed on the real installed host (owner decision D3); not human UAT",
	}
	if !contains(o.arms, eval.ArmQompack) {
		// --install describes how the plugin reaches the host; a run with no plugin arm has none.
		o.install, plan.Install = "none", "none"
	} else {
		id, idErr := readLiveBundle(o.bundle)
		if idErr != nil {
			return idErr
		}
		id.Install = o.install
		plan.Plugin = &id
	}
	plan.Trials = planLiveTrials(tasks, o.arms, o.trials)
	if o.maxSessions > 0 && len(plan.Trials) > o.maxSessions {
		return fmt.Errorf("live-eval: the plan starts %d session(s), above --max-sessions %d", len(plan.Trials), o.maxSessions)
	}

	fmt.Fprintf(w, "live-eval: run %s: %d trial(s) of task set %s (%s) on %s, arms %s, install %s\n",
		runID, len(plan.Trials), ts.ID, plan.TaskSetSHA256[:12], o.model, strings.Join(o.arms, ","), o.install)
	if o.dryRun {
		for _, p := range plan.Trials {
			fmt.Fprintf(w, "  plan: %s/%s/%d\n", p.Task, p.Arm, p.Trial)
		}
		fmt.Fprintln(w, "live-eval: --dry-run: no session started")
		return nil
	}
	if os.Getenv(liveEvalGateEnv) != "1" {
		return fmt.Errorf("live-eval: refusing to start real sessions without %s=1 (CI never sets it); "+
			"use --dry-run to validate", liveEvalGateEnv)
	}

	plan.ClaudeCLI, plan.ClaudeCLIVersion = env.claudeBin, env.cliVersion(ctx)
	if err := os.MkdirAll(o.out, liveDirPerm); err != nil {
		return fmt.Errorf("live-eval: creating %s: %w", o.out, err)
	}
	if err := writeJSONFile(filepath.Join(o.out, "plan.json"), plan); err != nil {
		return err
	}

	byID := map[string]eval.LiveTask{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	var trials []eval.LiveTrial
	var abort error
	for _, p := range plan.Trials {
		lt := liveTrialRun{
			opts: o, env: env, set: ts, task: byID[p.Task], arm: p.Arm, trial: p.Trial,
			runID: runID, rates: rates, plugin: plan.Plugin,
		}
		rec, guardErr := lt.run(ctx)
		trials = append(trials, rec)
		fmt.Fprintf(w, "live-eval: %s/%s/%d: completed=%t task_success=%t violations=%d harness_error=%q\n",
			p.Task, p.Arm, p.Trial, rec.Completed, rec.TaskSuccess, rec.ConstraintViolations, rec.HarnessError)
		if guardErr != nil {
			abort = guardErr
			fmt.Fprintf(w, "live-eval: STOPPING: %v\n", guardErr)
			break
		}
	}

	sum := eval.SummarizeLive(runID, ts.Analysis, trials)
	if abort != nil {
		sum.Notes = append(sum.Notes, "the run was stopped early: "+abort.Error())
	}
	if err := writeJSONFile(filepath.Join(o.out, "summary.json"), sum); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(o.out, "summary.md"), []byte(renderLiveSummary(plan, sum)), liveFilePerm); err != nil {
		return fmt.Errorf("live-eval: writing summary.md: %w", err)
	}
	fmt.Fprintf(w, "live-eval: decision %s — %s\nlive-eval: records under %s\n", sum.Decision.Verdict, sum.Decision.Reason, o.out)
	return abort
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

// planLiveTrials orders trials trial-major, task by task, alternating which arm goes first so a
// drift over the run's wall-clock (a rate limit, a busy machine) does not always land on one arm.
func planLiveTrials(tasks []eval.LiveTask, arms []string, trials int) []livePlanned {
	var out []livePlanned
	for n := 1; n <= trials; n++ {
		for i, t := range tasks {
			order := append([]string(nil), arms...)
			if (n+i)%2 == 0 {
				for l, r := 0, len(order)-1; l < r; l, r = l+1, r-1 {
					order[l], order[r] = order[r], order[l]
				}
			}
			for _, a := range order {
				out = append(out, livePlanned{Task: t.ID, Arm: a, Trial: n})
			}
		}
	}
	return out
}

// liveTrialRun is one trial's inputs.
type liveTrialRun struct {
	opts   liveOptions
	env    *liveEnv
	set    eval.LiveTaskSet
	task   eval.LiveTask
	arm    string
	trial  int
	runID  string
	rates  eval.LiveRateTable
	plugin *eval.LivePluginIdentity
}

// run executes one trial and returns its record. The error is non-nil only when the guard over the
// operator's configuration failed, which stops the whole run.
func (lt liveTrialRun) run(ctx context.Context) (eval.LiveTrial, error) {
	o, env, task := lt.opts, lt.env, lt.task
	rec := eval.LiveTrial{
		Schema: eval.LiveTrialSchema, RunID: lt.runID, TaskID: task.ID, Arm: lt.arm, Trial: lt.trial,
		Category: task.Category, Variant: task.Variant, HeldOut: task.HeldOut, Model: o.model,
		Install: "none", StartedAt: env.now().UTC().Format(time.RFC3339),
		PluginExpected:     lt.arm == eval.ArmQompack,
		PreregisteredModel: o.model == lt.set.Analysis.Model,
	}
	trialDir := filepath.Join(o.out, "trials", task.ID, lt.arm, fmt.Sprintf("%02d", lt.trial))
	if err := os.MkdirAll(trialDir, liveDirPerm); err != nil {
		rec.HarnessError = err.Error()
		return rec, nil
	}
	defer func() { _ = writeJSONFile(filepath.Join(trialDir, "trial.json"), rec) }()

	before, err := env.guardSnapshot()
	if err != nil {
		rec.HarnessError = "guard: " + err.Error()
		return rec, fmt.Errorf("live-eval: the configuration guard could not read the operator's files: %w", err)
	}
	guard := &eval.LiveHomeGuard{Checked: true, Before: before.lines()}
	rec.HomeGuard = guard

	work, err := os.MkdirTemp("", "qompack-live-")
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
		err = env.gitInit(ctx, project)
	}
	if err != nil {
		rec.HarnessError = "preparing the project: " + err.Error()
		return rec, nil
	}

	var cleanup []func() []string
	extraArgs := []string{}
	if lt.arm == eval.ArmQompack {
		id := *lt.plugin
		rec.Plugin = &id
		rec.Install = o.install
		switch o.install {
		case liveInstallPluginDir:
			extraArgs = append(extraArgs, "--plugin-dir", o.bundle)
		case liveInstallMarketplace:
			undo, instErr := env.marketplaceInstall(ctx, project, o.bundle, work)
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
			Env:            liveSessionEnv(o.idleExit),
			Messages:       liveMessages(task),
			StepTimeout:    o.stepTimeout,
			SessionTimeout: o.sessionTimeout,
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

	if lt.arm == eval.ArmQompack {
		how, stopErr := env.stopDaemon(project, filepath.Join(trialDir, "harness-spool"))
		rec.Notes = append(rec.Notes, "trial daemon: "+how)
		if stopErr != nil {
			rec.Notes = append(rec.Notes, "trial daemon stop: "+stopErr.Error())
		}
	}
	for i := len(cleanup) - 1; i >= 0; i-- {
		guard.Leftovers = append(guard.Leftovers, cleanup[i]()...)
	}
	if lt.arm == eval.ArmQompack && o.install == liveInstallMarketplace {
		guard.Leftovers = append(guard.Leftovers, env.removeOrphanedMarketplaceCache(before, lt.plugin.BundleSHA256)...)
	}
	guard.Leftovers = append(guard.Leftovers, env.removeCreatedPluginData(before)...)

	after, err := env.guardSnapshot()
	var guardErr error
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
	if stream.Init != nil {
		rec.SessionID = stream.Init.SessionID
		rec.ClaudeCodeVersion = stream.Init.ClaudeCodeVersion
		rec.PluginLoaded = stream.PluginLoaded(livePluginName)
		if stream.Init.Model != "" && stream.Init.Model != rec.Model {
			rec.Notes = append(rec.Notes, "host reported model "+stream.Init.Model)
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
	if lt.arm == eval.ArmQompack {
		q := &eval.LiveQompackEvidence{MCPToolCalls: eval.MCPToolCalls(stream, liveMCPToolPrefix)}
		if stream.Init != nil {
			for _, m := range stream.Init.MCPServers {
				if m.Name == liveMCPServerName {
					q.MCPServerStatus = m.Status
				}
			}
			for _, pl := range stream.Init.Plugins {
				if pl.Name == livePluginName && rec.Plugin != nil {
					rec.Plugin.HostVersion = pl.Version
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

// liveSessionEnv is the environment both arms add. ENABLE_CLAUDEAI_MCP_SERVERS=false keeps the
// operator's claude.ai-hosted MCP servers out of both arms alike; the idle-exit setting is inert on
// the stock arm and makes the qompack arm's daemon exit on its own after the session.
func liveSessionEnv(idleExit int) []string {
	return []string{
		"ENABLE_CLAUDEAI_MCP_SERVERS=false",
		fmt.Sprintf("QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=%d", idleExit),
	}
}

// liveMessages is each step's user message.
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
func readLiveBundle(dir string) (eval.LivePluginIdentity, error) {
	if dir == "" {
		return eval.LivePluginIdentity{}, errors.New("live-eval: the qompack arm needs --bundle <dir> " +
			"(assemble one with `go run ./tools/devtool bundle --target " + runtime.GOOS + "/" + runtime.GOARCH + "`)")
	}
	raw, err := os.ReadFile(filepath.Join(dir, identityFileName))
	if err != nil {
		return eval.LivePluginIdentity{}, fmt.Errorf("live-eval: reading the bundle identity: %w", err)
	}
	var id bundleIdentity
	if err := json.Unmarshal(raw, &id); err != nil {
		return eval.LivePluginIdentity{}, fmt.Errorf("live-eval: parsing %s: %w", identityFileName, err)
	}
	if id.Target.OS != runtime.GOOS || id.Target.Arch != runtime.GOARCH {
		return eval.LivePluginIdentity{}, fmt.Errorf("live-eval: bundle is for %s/%s, this host is %s/%s",
			id.Target.OS, id.Target.Arch, runtime.GOOS, runtime.GOARCH)
	}
	for _, f := range id.Files {
		sum, _, hashErr := hashFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if hashErr != nil || sum != f.SHA256 {
			return eval.LivePluginIdentity{}, fmt.Errorf("live-eval: bundle file %s does not match BUNDLE.json", f.Path)
		}
	}
	abs, _ := filepath.Abs(dir)
	return eval.LivePluginIdentity{
		BundleDir: abs, Version: id.Version, Commit: id.Source.Commit, Dirty: id.Source.Dirty,
		BundleSHA256: sha256Hex(raw),
	}, nil
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

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("live-eval: encoding %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, append(b, '\n'), liveFilePerm); err != nil {
		return fmt.Errorf("live-eval: writing %s: %w", path, err)
	}
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
func renderLiveSummary(p livePlan, s eval.LiveSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Live evaluation %s\n\n", p.RunID)
	fmt.Fprintf(&b, "Task set `%s` (sha256 `%s`), model `%s` (pre-registered `%s`), arms %s, install `%s`, "+
		"Claude Code %s. %s.\n\n", p.TaskSet, p.TaskSetSHA256, p.Model, p.PreregisteredModel,
		strings.Join(p.Arms, ", "), p.Install, p.ClaudeCLIVersion, p.Agent)
	fmt.Fprintf(&b, "Every cost figure is a list-price-equivalent ESTIMATE from the %s rate table; the sessions ran on a "+
		"subscription, which has no per-token cash charge.\n\n", p.RateTableDate)
	fmt.Fprintf(&b, "**Decision (pre-registered rule, primary outcome):** %s — %s\n\n", s.Decision.Verdict, s.Decision.Reason)
	b.WriteString("| arm | trials | completed | task success (95% CI) | constraint-clean (95% CI) | violations | recovery (95% CI) | mean host cost USD | hook-problem trials |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	arms := make([]string, 0, len(s.Arms))
	for a := range s.Arms {
		arms = append(arms, a)
	}
	sort.Strings(arms)
	for _, a := range arms {
		as := s.Arms[a]
		fmt.Fprintf(&b, "| %s | %d | %d | %s | %s | %d | %s | %.4f | %d |\n", a, as.Trials, as.Completed,
			fmtProportion(as.TaskSuccess), fmtProportion(as.ConstraintClean), as.ConstraintViolations,
			fmtProportion(as.Recovery), as.MeanHostCost, as.HookProblemTrials)
	}
	if d := s.TaskSuccessDiff; d != nil {
		fmt.Fprintf(&b, "\nTask-success difference (qompack − stock): %.3f [%.3f, %.3f]\n", d.Estimate, d.Low, d.High)
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

func fmtProportion(p eval.Proportion) string {
	if p.N == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d/%d = %.2f [%.2f, %.2f]", p.K, p.N, p.Rate, p.Low, p.High)
}

// lookClaude resolves the host CLI.
func lookClaude(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	p, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("live-eval: the Claude Code CLI is not on PATH (pass --claude): %w", err)
	}
	return p, nil
}
