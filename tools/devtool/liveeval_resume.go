package main

// `devtool live-eval --resume <run-dir>` (V7 W6 item 13). A paced run lasts weeks of nights, so it
// has to survive restarts: a night-window or limit pause (a clean exit), a stop, a crash, a reboot.
// --resume re-reads the run's plan.json, verifies that every frozen input still hashes to what the
// plan recorded and that the host is the pinned version, and continues the same plan: every slot
// that holds a scored trial.json is done, every other slot runs (a limited or configuration-
// discarded attempt never fills its slot, coordinator decision D-b), and the verdict is computed over
// the union of every session's trials.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/qompack/qompack/internal/eval"
)

// liveResumeFlags are the flags a resumed session may set: the ones that do not shape the plan.
var liveResumeFlags = map[string]bool{
	"resume": true, "claude": true, "rates": true, "step-timeout": true, "session-timeout": true,
	"keep-raw-transcripts": true, "dry-run": true, "five-hour-gate": true, "seven-day-ceiling": true, "night-window": true,
	"pacing-override": true, "overage-checked": true,
}

// resumeLiveEval continues the run in o.resume.
func resumeLiveEval(ctx context.Context, o liveOptions, env *liveEnv, w io.Writer) error {
	dir := o.resume
	raw, err := os.ReadFile(filepath.Join(dir, "plan.json"))
	if err != nil {
		return fmt.Errorf("live-eval: --resume %s: reading its plan.json: %w", dir, err)
	}
	var plan eval.LivePlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return fmt.Errorf("live-eval: --resume %s: parsing its plan.json: %w", dir, err)
	}
	ts, tasks, rates, err := verifyLiveResume(ctx, &o, env, plan)
	if err != nil {
		return fmt.Errorf("live-eval: --resume %s: %w; nothing was run", dir, err)
	}
	done := 0
	for _, p := range plan.Trials {
		if _, err := os.Stat(filepath.Join(liveSlotDir(dir, p), liveScoredRecord)); err == nil {
			done++
		}
	}
	fmt.Fprintf(w, "live-eval: resuming run %s: %d of %d planned trial(s) already scored; task set %s, fixture tree, "+
		"bundles and host Claude Code %s verified against plan.json\n", plan.RunID, done, len(plan.Trials),
		shortHash(plan.TaskSetSHA256), plan.ClaudeCLIVersion)
	if o.dryRun {
		for _, p := range plan.Trials {
			if _, err := os.Stat(filepath.Join(liveSlotDir(dir, p), liveScoredRecord)); err != nil {
				fmt.Fprintf(w, "  to run: %s/%s/%d\n", p.Task, p.Arm, p.Trial)
			}
		}
		fmt.Fprintln(w, "live-eval: --dry-run: no session started")
		return nil
	}
	if os.Getenv(liveEvalGateEnv) != "1" {
		return fmt.Errorf("live-eval: refusing to start real sessions without %s=1 (CI never sets it); "+
			"use --dry-run to validate", liveEvalGateEnv)
	}
	release, err := acquireLiveLock(dir, env.now())
	if err != nil {
		return err
	}
	defer release()
	return executeLivePlan(ctx, o, env, w, plan, ts, rates, tasks)
}

// verifyLiveResume checks plan against the files and host a resumed session would use, and fills o
// with the plan's own settings: the task set, fixture tree, rate table and both bundles must hash as
// the plan recorded, the plan's trial order must be the one its seed gives, and the host must report
// the pinned version.
func verifyLiveResume(ctx context.Context, o *liveOptions, env *liveEnv, plan eval.LivePlan) (
	eval.LiveTaskSet, []eval.LiveTask, eval.LiveRateTable, error,
) {
	var none eval.LiveRateTable
	o.tasksFile = filepath.FromSlash(plan.TaskSetFile)
	ts, raw, err := eval.LoadLiveTaskSet(o.tasksFile)
	if err != nil {
		return ts, nil, none, err
	}
	if got := sha256Hex(raw); got != plan.TaskSetSHA256 {
		return ts, nil, none, fmt.Errorf("the task set %s now hashes to %s, not the plan's %s", plan.TaskSetFile,
			shortHash(got), shortHash(plan.TaskSetSHA256))
	}
	tree, err := eval.TreeManifestSHA256(filepath.Dir(o.tasksFile), plan.FixtureTreeDirs...)
	if err != nil {
		return ts, nil, none, fmt.Errorf("hashing the fixture tree: %w", err)
	}
	if tree != plan.FixtureTreeSHA256 {
		return ts, nil, none, fmt.Errorf("the fixture tree %v now hashes to %s, not the plan's %s", plan.FixtureTreeDirs,
			shortHash(tree), shortHash(plan.FixtureTreeSHA256))
	}
	rates, err := loadLiveRates(o.rates)
	if err != nil {
		return ts, nil, none, err
	}
	if rates.Date != plan.RateTableDate {
		return ts, nil, none, fmt.Errorf("the rate table %s is dated %s, not the plan's %s (pass --rates)", o.rates,
			rates.Date, plan.RateTableDate)
	}

	if plan.StepTimeoutMS > 0 {
		// The step timeout is where active time caps a step with no result; a task set's efficiency
		// block pins it, so a resumed session keeps the plan's (amendment 4).
		switch got := o.stepTimeout.Milliseconds(); {
		case !o.stepTimeoutGiven:
			o.stepTimeout = time.Duration(plan.StepTimeoutMS) * time.Millisecond
		case ts.Analysis.Efficiency != nil && got != plan.StepTimeoutMS:
			return ts, nil, none, fmt.Errorf("--step-timeout %s is not the plan's %d ms, which the task set's efficiency "+
				"block pins", o.stepTimeout, plan.StepTimeoutMS)
		}
	}
	o.arms, o.model, o.install, o.out = plan.Arms, plan.Model, plan.Install, o.resume
	o.idleExit, o.includeHeldOut, o.orderSeed = plan.DaemonIdleExitSeconds, plan.HeldOutIncluded, plan.OrderSeed
	if len(plan.ArmStates) != len(plan.Arms) {
		return ts, nil, none, errors.New("the plan records no arm states: it was written before the Stage A arms and " +
			"cannot be resumed by this harness")
	}
	for i, s := range plan.ArmStates {
		want, ok := eval.LiveArmSpec(plan.Arms[i])
		if !ok || !reflect.DeepEqual(want, s) {
			return ts, nil, none, fmt.Errorf("the plan's %s arm state is not this harness's", plan.Arms[i])
		}
	}
	counts := plan.ArmTrials
	if counts == nil {
		counts = map[string]int{}
		for _, a := range plan.Arms {
			counts[a] = plan.TrialsPerArm
		}
	}
	o.only = map[string]bool{}
	for _, p := range plan.Trials {
		o.only[p.Task] = true
	}
	tasks, err := selectLiveTasks(ts, *o)
	if err != nil {
		return ts, nil, none, err
	}
	if again := planLiveTrials(tasks, plan.Arms, counts, plan.OrderSeed); !reflect.DeepEqual(again, plan.Trials) {
		return ts, nil, none, errors.New("the plan's trial order is not the one its arms, counts and order seed give")
	}
	for _, b := range []struct {
		flag string
		id   *eval.LivePluginIdentity
		dst  *string
	}{{eval.LiveBundleClassic, plan.Plugin, &o.bundle}, {eval.LiveBundleMods, plan.PluginMods, &o.bundleMods}} {
		if b.id == nil {
			continue
		}
		cur, err := readLiveBundle(b.id.BundleDir, b.flag, liveArmsUsing(plan.ArmStates, b.flag), b.id.Mods)
		if err != nil {
			return ts, nil, none, err
		}
		if cur.BundleSHA256 != b.id.BundleSHA256 {
			return ts, nil, none, fmt.Errorf("the --%s bundle at %s now hashes to %s, not the plan's %s", b.flag,
				b.id.BundleDir, shortHash(cur.BundleSHA256), shortHash(b.id.BundleSHA256))
		}
		*b.dst = b.id.BundleDir
	}
	if v := env.cliVersion(ctx); v != plan.ClaudeCLIVersion {
		return ts, nil, none, fmt.Errorf("the host reports Claude Code %s, not the plan's pinned %s (pass --claude <pinned path>)",
			v, plan.ClaudeCLIVersion)
	}
	return ts, tasks, rates, nil
}
