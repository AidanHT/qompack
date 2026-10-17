package main

// Tests of W6 task 2 in the live-eval driver: the Stage A confirmatory path (item 14), and what the
// run records and decides for the four arms (items 2, 5 and 6, amendment 4).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/modwire"
)

// registerStageATestEntry adds a test-only four-arm pre-registration of the test task set for the
// length of the test: its hash, its fixture tree, --plugin-dir, claude-opus-5-5 with the alias opus,
// the four arms with the set's own per-arm trials and both bundles.
func registerStageATestEntry(t *testing.T) eval.LiveTaskSet {
	t.Helper()
	ts, raw, err := eval.LoadLiveTaskSet(liveTestTasksV7)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	tree, err := eval.TreeManifestSHA256(filepath.Dir(liveTestTasksV7), ts.FixtureTreeDirs()...)
	require.NoError(t, err)
	eval.LivePreregistrations[ts.ID] = eval.LivePreregistration{
		Document: "test-only", TaskSetFile: "testdata/eval/live/tasks-v7-harness-test.json",
		TaskSetSHA256: hex.EncodeToString(sum[:]), FixtureTreeSHA256: tree, Install: liveInstallPluginDir,
		Model: "claude-opus-5-5", ModelContingency: "opus", RequiredFixed: []string{"C1.12"},
		Arms: eval.LiveArms, ArmTrialsPerTask: ts.Analysis.ArmTrialsPerTask,
		Bundles: []string{eval.LiveBundleClassic, eval.LiveBundleMods},
	}
	t.Cleanup(func() { delete(eval.LivePreregistrations, ts.ID) })
	return ts
}

// TestRunLiveEval_StageAConfirmatoryPath (task 1 review m-1): with a four-arm pre-registration entry,
// a plan of its whole design reports the plan-time preconditions met and --confirmatory accepts it;
// any one departure — an arm left out, an arm's trials per task, the mods bundle missing, the
// held-out tasks left out — makes --confirmatory refuse, naming it.
func TestRunLiveEval_StageAConfirmatoryPath(t *testing.T) {
	registerStageATestEntry(t)
	env := fakeLiveEnv(t, nil)
	classic, mods := fakeBundle(t), fakeModsBundle(t, "deadbeef")
	whole := func() liveOptions {
		return liveOptions{
			tasksFile: liveTestTasksV7, rates: liveTestRates, arms: eval.LiveArms, out: t.TempDir(),
			install: liveInstallPluginDir, bundle: classic, bundleMods: mods, includeHeldOut: true,
			defectsAttested: true, openDefects: []string{}, dryRun: true, idleExit: 1800,
			stepTimeout: liveDefaultStepTimeout,
		}
	}
	var out bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), whole(), env, &out))
	require.Contains(t, out.String(), "live-eval: confirmatory preconditions at plan time: met")
	require.NotContains(t, out.String(), "not the confirmatory design")

	o := whole()
	o.confirmatory = true
	out.Reset()
	require.NoError(t, runLiveEval(context.Background(), o, env, &out), "--confirmatory accepts the whole design")
	require.Contains(t, out.String(), "plan: pilot-codeword/steer/2")

	o = whole()
	o.confirmatory, o.model = true, "opus"
	out.Reset()
	require.NoError(t, runLiveEval(context.Background(), o, env, &out), "the entry's contingency alias is the design's model")

	for name, c := range map[string]struct {
		mutate func(*liveOptions)
		want   string
	}{
		"an arm left out": {func(o *liveOptions) { o.arms = []string{"stock", "classic", "steer"} }, "missing generic"},
		"trials per task": {func(o *liveOptions) { o.trials = 3 }, "ran 3 trials per task on the classic arm, not the pre-registered 2"},
		"held-out left":   {func(o *liveOptions) { o.includeHeldOut = false }, "it excluded the held-out tasks"},
		"no steer bundle": {func(o *liveOptions) { o.arms, o.bundleMods = []string{"stock", "classic", "generic"}, "" }, "names no --bundle-mods"},
	} {
		o := whole()
		c.mutate(&o)
		out.Reset()
		require.NoError(t, runLiveEval(context.Background(), o, env, &out), name)
		require.Contains(t, out.String(), "live-eval: not the confirmatory design: ", name)
		require.Contains(t, out.String(), c.want, name)

		o.confirmatory = true
		out.Reset()
		err := runLiveEval(context.Background(), o, env, &out)
		require.ErrorContains(t, err, "--confirmatory", name)
		require.ErrorContains(t, err, c.want, name)
		require.NotContains(t, out.String(), "plan: ", "%s: nothing is planned", name)
	}
}

// runFourArmPilot runs the test task set's six trials in all four arms against the scripted pilot
// host, with opts varying what the host does, and returns the run directory.
func runFourArmPilot(t *testing.T, opts pilotHostOpts, mutate func(*liveEnv)) string {
	t.Helper()
	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	env.run = scriptedPilotHostWith(t, home, opts)
	if mutate != nil {
		mutate(env)
	}
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{
		tasksFile: liveTestTasksV7, rates: liveTestRates, arms: eval.LiveArms, out: t.TempDir(),
		install: liveInstallPluginDir, bundle: fakeBundle(t), bundleMods: fakeModsBundle(t, "deadbeef"),
		idleExit: 1800, defectsAttested: true, stepTimeout: liveDefaultStepTimeout,
	}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	return o.out
}

// TestRunLiveEval_RecordsTheTaskDomain: each trial records its task's domain and summary.md breaks
// the outcomes down by it (W6 item 6).
func TestRunLiveEval_RecordsTheTaskDomain(t *testing.T) {
	out := runFourArmPilot(t, pilotHostOpts{}, nil)
	var rec eval.LiveTrial
	readJSON(t, filepath.Join(out, "trials", "pilot-codeword", "steer", "01", "trial.json"), &rec)
	require.Equal(t, "harness-validation", rec.Domain)
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(out, "summary.json"), &sum)
	require.Len(t, sum.ByDomain["harness-validation"], 4)
	md, err := os.ReadFile(filepath.Join(out, "summary.md"))
	require.NoError(t, err)
	require.Contains(t, string(md), "## By domain")
	require.Contains(t, string(md), "| harness-validation | steer | 2 |")
}

// sessionIDOf is the --session-id a host command line carries.
func sessionIDOf(spec liveProcSpec) string {
	for i, a := range spec.Args {
		if a == "--session-id" && i+1 < len(spec.Args) {
			return spec.Args[i+1]
		}
	}
	return ""
}

// withBriefLog wraps a scripted host so each steer session leaves the given brief-outcome log lines
// in its project, as the mod's `qompack mod brief` calls would.
func withBriefLog(t *testing.T, run func(context.Context, liveProcSpec) liveProcResult, lines ...string) func(context.Context, liveProcSpec) liveProcResult {
	t.Helper()
	return func(ctx context.Context, spec liveProcSpec) liveProcResult {
		res := run(ctx, spec)
		if !slices.Contains(spec.Env, liveEnvModSteering+"=true") || len(lines) == 0 {
			return res
		}
		name, err := modwire.BriefRecordName(sessionIDOf(spec))
		require.NoError(t, err)
		dir := filepath.Join(spec.Dir, liveStoreDir, modwire.StateDir)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600))
		return res
	}
}

// TestRunLiveEval_SteerBriefOutcomes: a steer trial records each compaction's brief outcome from its
// session's brief-outcome log, read with the wire contract's parser; the summary gives the steer
// arm's shares and summary.md shows them. A steer session that left no log has every compaction
// "missing" and is flagged as having no served brief, counted as assigned (W6 item 5).
func TestRunLiveEval_SteerBriefOutcomes(t *testing.T) {
	served := `{"v":1,"ts":1,"trigger":"manual","outcome":"served","chars":1234,"ms":80}`
	out := runFourArmPilot(t, pilotHostOpts{}, func(env *liveEnv) {
		env.run = withBriefLog(t, env.run, `{"v":1,"ts":0,"trigger":"precompute","outcome":"cold","chars":0,"ms":3}`,
			served, `not json`)
	})
	var rec eval.LiveTrial
	readJSON(t, filepath.Join(out, "trials", "pilot-codeword", "steer", "01", "trial.json"), &rec)
	require.NotNil(t, rec.Brief)
	require.Equal(t, []eval.LiveBriefCompaction{{Trigger: "manual", Outcome: "served", Chars: 1234, MS: 80}}, rec.Brief.Compactions)
	require.Equal(t, 2, rec.Brief.Records)
	require.Equal(t, 1, rec.Brief.Unmatched)
	require.Equal(t, 1, rec.Brief.Malformed)
	require.False(t, rec.Brief.NoServedBrief)
	var classic eval.LiveTrial
	readJSON(t, filepath.Join(out, "trials", "pilot-codeword", "classic", "01", "trial.json"), &classic)
	require.Nil(t, classic.Brief, "only the steer arm briefs")

	var sum eval.LiveSummary
	readJSON(t, filepath.Join(out, "summary.json"), &sum)
	b := sum.Arms[eval.ArmSteer].Brief
	require.NotNil(t, b)
	require.Equal(t, map[string]int{"served": 2}, b.Outcomes)
	require.Zero(t, b.UnservedShare)
	md, err := os.ReadFile(filepath.Join(out, "summary.md"))
	require.NoError(t, err)
	require.Contains(t, string(md), "## Brief outcomes")
	require.Contains(t, string(md), "| steer | 2 | 2 | 0.00 | 0.00 | 0 |")

	out = runFourArmPilot(t, pilotHostOpts{}, nil)
	var rec2 eval.LiveTrial
	var sum2 eval.LiveSummary
	readJSON(t, filepath.Join(out, "trials", "pilot-codeword", "steer", "02", "trial.json"), &rec2)
	require.Equal(t, []eval.LiveBriefCompaction{{Trigger: "manual", Outcome: "missing"}}, rec2.Brief.Compactions)
	require.True(t, rec2.Brief.NoServedBrief)
	readJSON(t, filepath.Join(out, "summary.json"), &sum2)
	require.InDelta(t, 1.0, sum2.Arms[eval.ArmSteer].Brief.UnservedShare, 1e-12, "missing counts as unserved")
	require.Equal(t, 2, sum2.Arms[eval.ArmSteer].Trials, "a trial with no brief is still scored (intention to treat)")
	require.Contains(t, strings.Join(sum2.Notes, "\n"), "2 of 2 steer trial(s) had no non-empty served brief")
}

// TestRunLiveEval_ResidentDaemonOfAnotherBuildIsAHarnessFault (ruling R42, review M4): a daemon.lock
// already in a Qompack arm's fresh project before its session, recording a build other than the
// arm's bundle, is positive evidence of a daemon that may predate W1a and answer every brief
// "unknown op: mod"; the attempt is a configuration error, set aside and run again, and counted. A
// lock of the arm's own build is recorded and is no fault.
func TestRunLiveEval_ResidentDaemonOfAnotherBuildIsAHarnessFault(t *testing.T) {
	plantLock := func(version string, times int) func(*liveEnv) {
		return func(env *liveEnv) {
			planted := 0
			env.git = func(ctx context.Context, dir string, args ...string) error {
				if err := runLiveGit(ctx, dir, args...); err != nil {
					return err
				}
				if _, err := os.Stat(filepath.Join(dir, liveStoreDir)); err == nil && planted < times && slices.Contains(args, "commit") {
					planted++
					lock := filepath.Join(dir, liveStoreDir, "run", "daemon.lock")
					require.NoError(t, os.MkdirAll(filepath.Dir(lock), 0o755))
					require.NoError(t, os.WriteFile(lock, []byte(`{"pid":4242,"started":1,"addr":"x","version":"`+version+`"}`), 0o600))
				}
				return nil
			}
		}
	}
	out := runFourArmPilot(t, pilotHostOpts{}, plantLock("v0.3.2", 1))
	discarded, err := filepath.Glob(filepath.Join(out, "trials", "pilot-codeword", "*", "*", "trial.discarded-1.json"))
	require.NoError(t, err)
	require.Len(t, discarded, 1, "the first Qompack attempt found the stale daemon")
	var d eval.LiveTrial
	readJSON(t, discarded[0], &d)
	require.Contains(t, d.ConfigurationError, "unknown op: mod")
	require.Contains(t, d.ConfigurationError, "ruling R42")
	require.Equal(t, &eval.LiveResidentDaemon{PID: 4242, Version: "v0.3.2", BundleVersion: "9.9.9-test", Fault: true}, d.ResidentDaemon)
	require.FileExists(t, filepath.Join(filepath.Dir(discarded[0]), "trial.json"), "the slot ran again and was scored")
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(out, "summary.json"), &sum)
	require.Equal(t, 1, sum.ResidentDaemonFaults)
	require.Contains(t, strings.Join(sum.Notes, "\n"), "harness faults, not scored outcomes (ruling R42)")

	out = runFourArmPilot(t, pilotHostOpts{}, plantLock("9.9.9-test", 1))
	discarded, err = filepath.Glob(filepath.Join(out, "trials", "pilot-codeword", "*", "*", "trial.discarded-*.json"))
	require.NoError(t, err)
	require.Empty(t, discarded, "a daemon of the arm's own build is no fault")
	var own eval.LiveSummary
	readJSON(t, filepath.Join(out, "summary.json"), &own)
	require.Zero(t, own.ResidentDaemonFaults)
	scored, err := filepath.Glob(filepath.Join(out, "trials", "pilot-codeword", "*", "*", "trial.json"))
	require.NoError(t, err)
	recorded := 0
	for _, f := range scored {
		var r eval.LiveTrial
		readJSON(t, f, &r)
		if r.ResidentDaemon != nil {
			recorded++
			require.False(t, r.ResidentDaemon.Fault)
			require.Equal(t, "9.9.9-test", r.ResidentDaemon.Version)
		}
	}
	require.Equal(t, 1, recorded, "the own-build daemon is recorded on the trial it was found for")
}

// TestRunLiveEval_SummaryCarriesTheStageADecisions (W6 item 2): a four-arm run is decided by the Stage
// A rules, not the two-arm verdict: summary.json carries every test and the decision, and summary.md
// shows the tests, the outcome row, the reported rows and each arm's cost and active time.
func TestRunLiveEval_SummaryCarriesTheStageADecisions(t *testing.T) {
	out := runFourArmPilot(t, pilotHostOpts{}, nil)
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(out, "summary.json"), &sum)
	require.NotNil(t, sum.StageA)
	require.Equal(t, eval.LiveBootstrapB, sum.StageA.B)
	// The scripted host prints duration_ms (review STATS-4), and every arm recovers the code word at
	// the same cost and time: H1 is not shown, no harm is shown, so the run reads exactly "no-ship".
	require.Equal(t, eval.LiveVerdictNoShip, sum.Decision.Verdict, sum.Decision.Reason)
	require.Equal(t, "not shown", stageALabel(t, sum.StageA, "H1"))
	require.Equal(t, "not shown", stageALabel(t, sum.StageA, "harm-active_time"))
	require.Equal(t, "not shown", stageALabel(t, sum.StageA, "harm-cost"))
	require.NotContains(t, sum.Decision.Reason, "both arms are needed", "the two-arm verdict is replaced")
	h1, ok := sum.StageA.Test("H1")
	require.True(t, ok)
	require.NotNil(t, h1.Interval)
	var rec eval.LiveTrial
	readJSON(t, filepath.Join(out, "trials", "pilot-codeword", "steer", "01", "trial.json"), &rec)
	require.True(t, rec.Steps[0].HasResult)
	require.NotNil(t, rec.ConstraintClean, "the pilot task declares constraint checks")
	var plan eval.LivePlan
	readJSON(t, filepath.Join(out, "plan.json"), &plan)
	require.Equal(t, int64(600000), plan.StepTimeoutMS, "the efficiency block's pinned step timeout")

	md, err := os.ReadFile(filepath.Join(out, "summary.md"))
	require.NoError(t, err)
	for _, want := range []string{
		"## Stage A decisions", "B = 10000 resamples of 1 task(s)", "**Outcome (§9.6):**",
		"| H1 | steer vs classic | recovery |", "| harm-cost | steer vs classic | cost |", "| H3 | steer vs classic | cost |",
		"## Reported, never decided", "| classic-vs-stock-recovery | classic vs stock | recovery |",
		"## Cost and active time per arm", "| steer | 2 | 2 / 0 / 0 |",
	} {
		require.Contains(t, string(md), want)
	}
}

// TestRunLiveEval_TheStepTimeoutIsPinned (amendment 4): a task set's efficiency block pins the step
// timeout active time caps a step at. With no --step-timeout the run takes it; another value is a
// plan departure that --confirmatory refuses; and a resumed session keeps the plan's, refusing a
// --step-timeout that differs.
func TestRunLiveEval_TheStepTimeoutIsPinned(t *testing.T) {
	registerStageATestEntry(t)
	env := fakeLiveEnv(t, nil)
	o := liveOptions{
		tasksFile: liveTestTasksV7, rates: liveTestRates, arms: eval.LiveArms, out: t.TempDir(),
		install: liveInstallPluginDir, bundle: fakeBundle(t), bundleMods: fakeModsBundle(t, "deadbeef"), includeHeldOut: true,
		defectsAttested: true, openDefects: []string{}, dryRun: true, idleExit: 1800,
		stepTimeout: 5 * time.Minute, stepTimeoutGiven: true,
	}
	var w bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &w))
	require.Contains(t, w.String(), "it ran with a step timeout of 300000 ms, not the pre-registered 600000 ms")
	o.confirmatory = true
	require.ErrorContains(t, runLiveEval(context.Background(), o, env, &w), "step timeout of 300000 ms")
	o.stepTimeoutGiven, o.confirmatory = false, true
	w.Reset()
	require.NoError(t, runLiveEval(context.Background(), o, env, &w), "without the flag the pinned value is used")

	out := runFourArmPilot(t, pilotHostOpts{}, nil)
	r := liveOptions{resume: out, rates: liveTestRates, dryRun: true, stepTimeout: 5 * time.Minute, stepTimeoutGiven: true}
	require.ErrorContains(t, runLiveEval(context.Background(), r, fakeLiveEnv(t, nil), &w), "is not the plan's 600000 ms")
	r.stepTimeoutGiven = false
	require.NoError(t, runLiveEval(context.Background(), r, fakeLiveEnv(t, nil), &w))
}

// TestRunLiveEval_SizeFromAPilotSummary (W6 item 2, decision D-d): --size-from starts nothing and
// needs no night window; from the pilot's summary and the task set's Δs and ICC_int it prints each
// test's n before and after inflation, each arm's n rounded to the task count, the Sonnet check's
// n_H1, a calendar estimate and the efficiency tests' power. A set too small for its ICC_int is
// refused with the plan's advice, and --resume cannot take it.
func TestRunLiveEval_SizeFromAPilotSummary(t *testing.T) {
	pilot := eval.LiveSummary{RunID: "pilot-1", Arms: map[string]eval.ArmSummary{}}
	eff := &eval.LiveArmEfficiency{LogSDWithinCost: 0.25, LogSDWithinTime: 0.35, MeanCostUSD: 2, MeanActiveMS: 600000}
	use := map[string]eval.LiveRateUseStats{"five_hour": {Trials: 4, Mean: 0.02}, "seven_day": {Trials: 4, Mean: 0.004}}
	for arm, rec := range map[string]int{"stock": 40, "classic": 60, "generic": 70, "steer": 85} {
		pilot.Arms[arm] = eval.ArmSummary{
			Arm: arm, Recovery: eval.Proportion{K: rec, N: 100}, TaskSuccess: eval.Proportion{K: 90, N: 100},
			MeanWallMS: 15 * 60 * 1000, RateUse: use, Efficiency: eff,
		}
	}
	steer := pilot.Arms["steer"]
	steer.Brief = &eval.LiveBriefStats{Compactions: 40, UnservedShare: 0.05}
	pilot.Arms["steer"] = steer
	dir := t.TempDir()
	summary := filepath.Join(dir, "summary.json")
	raw, err := json.Marshal(pilot)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(summary, raw, 0o600))

	// The test set has one task, too few for ICC_int 0.10; a copy with ICC_int 0 sizes.
	set, err := os.ReadFile(liveTestTasksV7)
	require.NoError(t, err)
	copyDir := t.TempDir()
	for _, d := range []string{"fixtures/pilot", "hidden/pilot"} {
		require.NoError(t, os.MkdirAll(filepath.Join(copyDir, d), 0o755))
	}
	noICC := filepath.Join(copyDir, "tasks.json")
	require.NoError(t, os.WriteFile(noICC, []byte(strings.Replace(string(set), `"icc_int": 0.1`, `"icc_int": 0`, 1)), 0o600))

	o, err := parseLiveFlags([]string{"--size-from", summary, "--tasks", noICC})
	require.NoError(t, err, "the sizing step starts nothing, so it names no night window")
	var w bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, fakeLiveEnv(t, nil), &w))
	for _, want := range []string{
		"live-eval: --size-from " + summary, "nothing is started", "u 0.050",
		"H1 (steer, classic, stock): p₀ 0.536 → p₁ 0.786", "H2 (steer, generic)", "S (steer, classic): p 0.855",
		"per-arm n, each the largest of its tests rounded up to a multiple of 1:",
		"Sonnet check (plan §9.8), from this pilot's rates, a planning stand-in", "calendar (planning figure)",
		"night(s) of 9.0 h", "with the Sonnet check's",
		"efficiency power at the planned steer and classic n", "the pilot summary records no df for its σ_w",
		"power from the planning band (no df recorded)", "σ_w 0.400, SE", "σ_w 0.550, SE",
		"efficiency power of the Sonnet check's guards", "pilot steer/classic ratio of mean C",
	} {
		require.Contains(t, w.String(), want)
	}

	// The Sonnet check from its own pilot's summary (review STATS-6).
	o, err = parseLiveFlags([]string{"--size-from", summary, "--size-sonnet", summary, "--tasks", noICC})
	require.NoError(t, err)
	w.Reset()
	require.NoError(t, runLiveEval(context.Background(), o, fakeLiveEnv(t, nil), &w))
	require.Contains(t, w.String(), "Sonnet check (plan §9.8), from the Sonnet pilot "+summary)
	require.NotContains(t, w.String(), "planning stand-in")
	_, err = parseLiveFlags([]string{"--size-sonnet", summary, "--tasks", noICC, "--night-window", "none"})
	require.ErrorContains(t, err, "--size-sonnet")

	o, err = parseLiveTest([]string{"--size-from", summary, "--tasks", liveTestTasksV7})
	require.NoError(t, err)
	require.ErrorContains(t, runLiveEval(context.Background(), o, fakeLiveEnv(t, nil), &w), "add tasks")
	_, err = parseLiveTest([]string{"--resume", dir, "--size-from", summary})
	require.ErrorContains(t, err, "--size-from")
}

// TestRunLiveEval_DescribeShowsThePreregisteredDecisions: --describe prints the decision rules the
// hashed task set pre-registers — the bootstrap, the sequence and S, the sizing's Δs and ICC_int, and
// amendment 4's efficiency block with each guard — so the Run Book's reader sees them before a run.
func TestRunLiveEval_DescribeShowsThePreregisteredDecisions(t *testing.T) {
	night, err := parseNightWindow("22:00-07:00")
	require.NoError(t, err)
	o := liveOptions{
		tasksFile: liveTestTasksV7, rates: liveTestRates, arms: eval.LiveArms, out: filepath.Join(t.TempDir(), "run"),
		install: liveInstallPluginDir, bundle: fakeBundle(t), bundleMods: fakeModsBundle(t, "deadbeef"), idleExit: 1800,
		defectsAttested: true, openDefects: []string{}, orderSeed: 11, describe: true, night: night,
		fiveHourGate: eval.LiveFiveHourGate, sevenDayCeiling: eval.LiveSevenDayCeiling,
	}
	var w bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, fakeLiveEnv(t, nil), &w))
	for _, want := range []string{
		"## Pre-registered decisions", "B = 10000 resamples, seed 20261016", "the lower bound above -0.10",
		"Δ_H1 0.25, Δ_H2 0.15, ICC_int 0.10", "margin 0.10 on cost and active_time", "caps a step with no result at 600000 ms",
		"steer vs classic: harm shown when the ratio's lower bound exceeds 1.10 (gated",
		"classic vs stock: overhead shown when the ratio's lower bound exceeds 1.10 (a label for the docs, not gating)",
		"a claim, not a ship condition", "redesign ratio 1.25", "- Daemon idle exit: 1800 s; step timeout 10m0s",
		"- Harm (gated, on every run that reached a decision, on either model, the §9.8 Sonnet check included)",
		"decides only harm; its benefit intervals are reported, not decided",
	} {
		require.Contains(t, w.String(), want)
	}

	// Under the S fallback the page never claims non-inferiority (review I-3).
	ts, _, err := eval.LoadLiveTaskSet(liveTestTasksV7)
	require.NoError(t, err)
	ts.Analysis.SFallback = true
	var fb strings.Builder
	renderLiveDescribeDecisions(&fb, ts.Analysis)
	require.Contains(t, fb.String(), "S, the pre-registered fallback: met when the task-success difference's 20th percentile "+
		"is above -0.10; non-inferiority on task success is then not shown")
	require.NotContains(t, fb.String(), "steer non-inferior to classic")
}

// TestRenderLiveSummary_StageARecords (reviews I-2, STATS-7, CLI-4): summary.md shows each ratio's
// sensitivities, the estimate beside the host's cost and the rows the harness does not measure; a
// paused run's tests print as interim, never "ships on".
func TestRenderLiveSummary_StageARecords(t *testing.T) {
	out := runFourArmPilot(t, pilotHostOpts{}, nil)
	var plan eval.LivePlan
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(out, "plan.json"), &plan)
	readJSON(t, filepath.Join(out, "summary.json"), &sum)
	md := renderLiveSummary(plan, sum)
	for _, want := range []string{
		"## Stage A decisions", "**Outcome (§9.6):**", "Sensitivities of the ratios", "| harm-cost | completed |",
		"| harm-cost | observed |", "| harm-cost | geometric |", "estimate / host |", "result lines without duration_ms |",
		"overhead shown when the ratio's lower bound is above 1.10 (a label, not gating)",
		"distrust incidents: not measured by this harness", "-p teardown losses with F4 on: not measured",
	} {
		require.Contains(t, md, want)
	}
	// The estimate ratio is eval's, over the trials with both an estimate and a complete C (H-NEW-1).
	e := sum.Arms["steer"].Efficiency
	var est, cost float64
	for _, n := range []string{"01", "02"} {
		var rec eval.LiveTrial
		readJSON(t, filepath.Join(out, "trials", "pilot-codeword", "steer", n, "trial.json"), &rec)
		require.NotNil(t, rec.Estimate)
		est += float64(rec.Estimate.Micros) / 1e6
		cost += rec.HostCostUSD
	}
	require.Equal(t, 2, e.EstimateRatioN)
	require.InDelta(t, est/cost, e.EstimateRatio, 1e-12)
	require.Contains(t, md, fmt.Sprintf("| %.3f over 2 trial(s)", e.EstimateRatio))
	sum.Pause(3, 6, "the night window ended")
	paused := renderLiveSummary(plan, sum)
	require.Contains(t, paused, "## Stage A tests, interim, not decided")
	require.Contains(t, paused, "**No decision:** no verdict: the run paused after 3 of the 6 planned trials")
	require.NotContains(t, paused, "ships on")
}

// stageALabel is the label of a Stage A test.
func stageALabel(t *testing.T, s *eval.LiveStageA, id string) string {
	t.Helper()
	require.NotNil(t, s)
	got, ok := s.Test(id)
	require.True(t, ok, "no test %s", id)
	return got.Label
}

// TestRunLiveEval_FourArmResumeWithALimitedSteerTrialEqualsAnUninterruptedRun (review WIRING-8): a
// four-arm run whose first steer attempt a rejected five-hour window cuts short (set aside, waited out,
// run again) and which is killed before its fourth slot resumes to a summary — the Stage A decisions,
// their limited sensitivity and every reported row included — equal to an uninterrupted run's.
func TestRunLiveEval_FourArmResumeWithALimitedSteerTrialEqualsAnUninterruptedRun(t *testing.T) {
	classic, mods := fakeBundle(t), fakeModsBundle(t, "deadbeef")
	run := func(kill bool) liveOptions {
		env, _, home := pacedEnv(t)
		steerSessions := 0
		env.run = perSession(t, home, func(_ int, spec liveProcSpec) pilotHostOpts {
			if !slices.Contains(spec.Env, liveEnvModSteering+"=true") {
				return pilotHostOpts{}
			}
			steerSessions++
			if steerSessions == 1 {
				return pilotHostOpts{streamExtra: []string{
					rateLine("rejected", 0.95, 0.10, fakeNow.Add(time.Hour), fakeNow.Add(100*time.Hour), false),
				}}
			}
			return pilotHostOpts{}
		})
		o := liveOptions{
			tasksFile: liveTestTasksV7, rates: liveTestRates, arms: eval.LiveArms, out: t.TempDir(),
			install: liveInstallPluginDir, bundle: classic, bundleMods: mods, idleExit: 1800, defectsAttested: true,
			orderSeed: 11, fiveHourGate: eval.LiveFiveHourGate, sevenDayCeiling: eval.LiveSevenDayCeiling,
		}
		var log bytes.Buffer
		if !kill {
			require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
			return o
		}
		killAtVersionCall(env, 5) // the plan, steer/1 (limited), steer/1 again, stock/1; dies before generic/1
		runKilled(t, o, env)
		require.NoFileExists(t, filepath.Join(o.out, "summary.json"))
		env.cli = pinnedCLI(fakeLiveEnv(t, nil).cli)
		r := resumeRun(t, o.out)
		require.NoError(t, runLiveEval(context.Background(), r, env, &log), log.String())
		require.Contains(t, log.String(), "2 of 6 planned trial(s) already scored")
		return o
	}
	whole, cut := run(false), run(true)
	require.FileExists(t, filepath.Join(whole.out, "trials", "pilot-codeword", "steer", "01", "trial.limited-1.json"))
	require.FileExists(t, filepath.Join(cut.out, "trials", "pilot-codeword", "steer", "01", "trial.limited-1.json"))
	var a, b eval.LiveSummary
	readJSON(t, filepath.Join(whole.out, "summary.json"), &a)
	readJSON(t, filepath.Join(cut.out, "summary.json"), &b)
	require.Equal(t, map[string]int{"steer": 1}, a.LimitedTrials)
	require.NotNil(t, a.StageA)
	cost, ok := a.StageA.Test("harm-cost")
	require.True(t, ok)
	require.Contains(t, cost.Sensitivity, "limited", "the limited steer attempt feeds the guards' limited sensitivity")
	// Its whole cost and time join steer/1's re-run, so the sensitivity flips both gated guards, which
	// read indeterminate: harm-shown, the same in both runs.
	require.Equal(t, eval.LiveLabelIndeterminate, cost.Label)
	require.Equal(t, eval.LiveVerdictHarm, a.Decision.Verdict)
	require.Equal(t, a.StageA, b.StageA, "the resumed run's Stage A record is the uninterrupted run's")
	b.RunID = a.RunID
	require.Equal(t, a, b, "the resumed run's summary is the uninterrupted run's")
}

// TestRunLiveEval_RatesAndTheStepTimeoutOnEachTrial (coordinator ruling on review CLI-5; reviews
// STATS-5, WIRING-5): a task set with an efficiency block reads rates-v7.json beside it unless --rates
// names another; a model the table does not price is recorded on each trial and noted, and nothing is
// refused; each trial records the run's step timeout, the cap its active time counts; and a
// --step-timeout that is not positive is refused.
func TestRunLiveEval_RatesAndTheStepTimeoutOnEachTrial(t *testing.T) {
	out := runFourArmPilot(t, pilotHostOpts{}, nil)
	var plan eval.LivePlan
	readJSON(t, filepath.Join(out, "plan.json"), &plan)
	require.Equal(t, "2026-10-16", plan.RateTableDate, "the V7 table beside the task set")
	var rec eval.LiveTrial
	readJSON(t, filepath.Join(out, "trials", "pilot-codeword", "steer", "01", "trial.json"), &rec)
	require.Equal(t, int64(600000), rec.StepTimeoutMS)
	require.Empty(t, rec.EstimateUnpriced)

	// A table that prices only another model: the run goes ahead, and the estimate says it is a lower bound.
	only := filepath.Join(t.TempDir(), "rates-only-opus.json")
	require.NoError(t, os.WriteFile(only, []byte(`{"version":1,"date":"2026-10-01","source":"test-only","currency":"USD",`+
		`"models":{"claude-opus-5":{"input_per_mtok_micros":5000000,"output_per_mtok_micros":25000000,`+
		`"cache_read_multiplier":0.1,"cache_write_5m_multiplier":1.25,"cache_write_1h_multiplier":2}}}`), 0o600))
	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	env.run = scriptedPilotHost(t, home)
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{
		tasksFile: liveTestTasksV7, rates: only, ratesGiven: true, arms: []string{"classic", "steer"}, out: t.TempDir(),
		install: liveInstallPluginDir, bundle: fakeBundle(t), bundleMods: fakeModsBundle(t, "deadbeef"), idleExit: 1800,
		defectsAttested: true, trials: 1,
	}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	readJSON(t, filepath.Join(o.out, "plan.json"), &plan)
	require.Equal(t, "2026-10-01", plan.RateTableDate, "an explicit --rates wins")
	readJSON(t, filepath.Join(o.out, "trials", "pilot-codeword", "steer", "01", "trial.json"), &rec)
	require.NotEmpty(t, rec.EstimateUnpriced)
	require.Contains(t, rec.EstimateUnpriced[0], "haiku")
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Contains(t, strings.Join(sum.Notes, "\n"), "the rate table prices no "+rec.EstimateUnpriced[0])

	_, err := parseLiveTest([]string{"--step-timeout", "0s"})
	require.ErrorContains(t, err, "--step-timeout 0s must be positive")
}

// TestParseLiveFlags_ExplicitRates (reviews H-NEW-2, H-NEW-5): --rates sets the table and marks it given,
// so it wins over the V7 default; with no --rates, a task set with an efficiency block whose
// rates-v7.json is missing is refused with the way out named.
func TestParseLiveFlags_ExplicitRates(t *testing.T) {
	o, err := parseLiveTest([]string{"--rates", "x.json"})
	require.NoError(t, err)
	require.True(t, o.ratesGiven)
	require.Equal(t, "x.json", o.rates)
	o, err = parseLiveTest(nil)
	require.NoError(t, err)
	require.False(t, o.ratesGiven)

	set, err := os.ReadFile(liveTestTasksV7)
	require.NoError(t, err)
	dir := t.TempDir()
	for _, d := range []string{"fixtures/pilot", "hidden/pilot"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, d), 0o755))
	}
	tasks := filepath.Join(dir, "tasks.json")
	require.NoError(t, os.WriteFile(tasks, set, 0o600))
	o = liveOptions{
		tasksFile: tasks, rates: liveTestRates, arms: []string{"stock"}, out: t.TempDir(), idleExit: 1800, dryRun: true,
	}
	var w bytes.Buffer
	err = runLiveEval(context.Background(), o, fakeLiveEnv(t, nil), &w)
	require.ErrorContains(t, err, "rates-v7.json")
	require.ErrorContains(t, err, "pass --rates <table>")
	o.ratesGiven = true
	require.NoError(t, runLiveEval(context.Background(), o, fakeLiveEnv(t, nil), &w), "an explicit --rates needs no V7 table")
}

// TestRenderLiveStageA_FixRound3 (reviews R2-N1, R2-N3): summary.md gives a Stage A record that could
// not be decided, and was not stopped, the plain heading and its outcome with no empty table of tests;
// each arm's count of result
// lines without duration_ms in its own column; "n/a" for an arm with no estimate; and "not recorded"
// for one whose summary has estimates but no ratio.
func TestRenderLiveStageA_FixRound3(t *testing.T) {
	na := eval.LiveDecision{Verdict: eval.LiveVerdictNotApplicable}
	sum := eval.LiveSummary{
		Decision: na,
		StageA: &eval.LiveStageA{
			Decision: na, Outcome: "the Stage A decisions need the steer and classic arms", Confidence: 0.95,
			B: eval.LiveBootstrapB, Tasks: 2, Seed: 1,
		},
		Arms: map[string]eval.ArmSummary{
			"stock":   {Arm: "stock", Efficiency: &eval.LiveArmEfficiency{Trials: 2, TimedOutSteps: 1, DurationMissing: 3}},
			"generic": {Arm: "generic", MeanEstimateMc: 900000, Efficiency: &eval.LiveArmEfficiency{Trials: 2}},
		},
	}
	var b strings.Builder
	renderLiveStageA(&b, sum)
	md := b.String()
	require.Contains(t, md, "## Stage A decisions")
	require.Contains(t, md, "**Outcome (§9.6):** the Stage A decisions need the steer and classic arms.")
	require.NotContains(t, md, "interim")
	require.NotContains(t, md, "| test | comparison |", "a record with no tests prints no empty table")
	require.Contains(t, md, "| stock | 2 | 0 / 0 / 0 | 0.0000 | 0.0 | 0.0 | 0.0 | 1 | 3 | 0 | 0 / 0 | 0 | n/a (no estimate recorded) |")
	require.Contains(t, md, "| 0 / 0 | 0 | not recorded (the arm has estimates")
}
