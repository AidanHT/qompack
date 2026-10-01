package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

const (
	// liveTestTasks is the confirmatory set, qompack-live-v2; liveTestTasksV1 is the set it superseded
	// before use (preregistration amendment A7), kept byte-identical as the record.
	liveTestTasks   = "../../testdata/eval/live/tasks-v2.json"
	liveTestTasksV1 = "../../testdata/eval/live/tasks.json"
	liveTestPilot   = "../../testdata/eval/live/pilot.json"
	liveTestRates   = "../../testdata/eval/live/rates.json"
	pilotCodeWord   = "2c1a7a62c3"
)

func TestParseLiveFlags(t *testing.T) {
	o, err := parseLiveFlags([]string{"--arms", "qompack", "--install", "marketplace", "--only", "a, b", "--trials", "3"})
	require.NoError(t, err)
	require.Equal(t, []string{"qompack"}, o.arms)
	require.Equal(t, map[string]bool{"a": true, "b": true}, o.only)
	require.Equal(t, 3, o.trials)

	for _, bad := range [][]string{
		{"--arms", "control"},
		{"--arms", ""},
		{"--install", "copy"},
		{"--trials", "-1"},
		{"stray"},
		{"--daemon-idle-exit", "0"},
		{"--known-open-defects", "none,C1.12"},
		{"--known-open-defects", " , "},
		{"--known-open-defects", "C1.12;rm"},
	} {
		_, err := parseLiveFlags(bad)
		require.Error(t, err, "%v", bad)
	}
}

// TestParseLiveFlags_KnownOpenDefects: preregistration section 9 counts a run as confirmatory only
// on a candidate with no known open defect, and a bundle cannot prove which defects it fixes, so the
// operator states it: "none", or every known defect the bundle still carries. Not saying is
// distinguishable from saying "none".
func TestParseLiveFlags_KnownOpenDefects(t *testing.T) {
	o, err := parseLiveFlags(nil)
	require.NoError(t, err)
	require.False(t, o.defectsAttested, "no flag is no attestation")

	o, err = parseLiveFlags([]string{"--known-open-defects", "none"})
	require.NoError(t, err)
	require.True(t, o.defectsAttested)
	require.Empty(t, o.openDefects)

	o, err = parseLiveFlags([]string{"--known-open-defects", "C1.12, C1.1"})
	require.NoError(t, err)
	require.True(t, o.defectsAttested)
	require.Equal(t, []string{"C1.12", "C1.1"}, o.openDefects)
}

// TestRunLiveEval_TheQompackArmNeedsADefectAttestation: a run with the qompack arm refuses to plan —
// dry run included, since a dry run exists to validate the real run's command — until the operator
// has said which known defects its bundle carries, and plan.json records the statement as the
// operator's, beside the bundle it is about. A stock-only run has no candidate and needs none.
func TestRunLiveEval_TheQompackArmNeedsADefectAttestation(t *testing.T) {
	env := fakeLiveEnv(t, nil)
	var out bytes.Buffer
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"stock", "qompack"}, out: t.TempDir(),
		install: liveInstallPluginDir, bundle: fakeBundle(t), dryRun: true,
	}
	err := runLiveEval(context.Background(), o, env, &out)
	require.ErrorContains(t, err, "--known-open-defects")
	require.ErrorContains(t, err, "section 9")

	o.defectsAttested, o.openDefects = true, []string{"C1.11"}
	out.Reset()
	require.NoError(t, runLiveEval(context.Background(), o, env, &out))
	require.Contains(t, out.String(), "known open defects: C1.11")

	o.arms, o.defectsAttested, o.openDefects = []string{"stock"}, false, nil
	require.NoError(t, runLiveEval(context.Background(), o, env, &out), "a stock-only run carries no candidate")

	home := t.TempDir()
	env.home = home
	env.run = scriptedPilotHost(t, home)
	t.Setenv(liveEvalGateEnv, "1")
	o = liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"qompack"}, out: t.TempDir(),
		install: liveInstallPluginDir, bundle: fakeBundle(t), idleExit: 1, trials: 1, defectsAttested: true,
	}
	require.NoError(t, runLiveEval(context.Background(), o, env, &out), out.String())
	var plan eval.LivePlan
	readJSON(t, filepath.Join(o.out, "plan.json"), &plan)
	require.NotNil(t, plan.KnownDefects)
	require.NotNil(t, plan.KnownDefects.Open, "none is recorded as an empty list, not as no statement")
	require.Empty(t, plan.KnownDefects.Open)
	require.Contains(t, plan.KnownDefects.Source, "--known-open-defects")
	raw, err := os.ReadFile(filepath.Join(o.out, "plan.json"))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"open": []`)
	md, err := os.ReadFile(filepath.Join(o.out, "summary.md"))
	require.NoError(t, err)
	require.Contains(t, string(md), "known open defects: none")
}

// TestRunLiveEval_ASupersededTaskSetIsNeverPlanned: qompack-live-v1 was superseded before any trial
// of it (preregistration amendment A7). Its file stays in the tree as the record, and a run of it
// could never be the confirmatory run, so the driver refuses to plan one — dry run included, since a
// dry run exists to validate the real run's command — and names the set to run instead. The default
// task set is the current one.
func TestRunLiveEval_ASupersededTaskSetIsNeverPlanned(t *testing.T) {
	env := fakeLiveEnv(t, nil)
	var out bytes.Buffer
	o := liveOptions{tasksFile: liveTestTasksV1, rates: liveTestRates, arms: []string{"stock"}, out: t.TempDir(), dryRun: true}
	err := runLiveEval(context.Background(), o, env, &out)
	require.ErrorContains(t, err, "task set qompack-live-v1 was superseded before any trial of it")
	require.ErrorContains(t, err, "amendment A7")
	require.ErrorContains(t, err, "testdata/eval/live/tasks-v2.json")
	require.Empty(t, out.String(), "nothing is planned")

	o.tasksFile = liveTestTasks
	require.NoError(t, runLiveEval(context.Background(), o, env, &out))
	require.Contains(t, out.String(), "task set qompack-live-v2")

	d, err := parseLiveFlags(nil)
	require.NoError(t, err)
	require.Equal(t, "testdata/eval/live/tasks-v2.json", d.tasksFile)
	ts, _, err := eval.LoadLiveTaskSet(filepath.Join("..", "..", filepath.FromSlash(d.tasksFile)))
	require.NoError(t, err)
	require.Contains(t, eval.LivePreregistrations, ts.ID, "the default task set is pre-registered")
	require.Empty(t, eval.LivePreregistrations[ts.ID].SupersededBy, "and current")
}

// TestRunLiveEval_ThePlanSaysWhetherItIsTheConfirmatoryDesign: a confirmatory run is 40 real
// sessions of a budget the owner capped, and whether it can be the confirmatory run is mostly fixed
// when it is planned — the frozen materials, --plugin-dir, the pre-registered model, both arms, all
// ten tasks with the held-out ones, two trials per arm, a clean bundle and the known-defect statement.
// The driver says so before any session starts, dry run included, naming the bundle it would load;
// with --confirmatory it refuses to plan a run that departs from the design, so a departure found by
// `qompack eval` after the sessions cannot happen.
func TestRunLiveEval_ThePlanSaysWhetherItIsTheConfirmatoryDesign(t *testing.T) {
	env := fakeLiveEnv(t, nil)
	confirmatory := func() liveOptions {
		return liveOptions{
			tasksFile: liveTestTasks, rates: liveTestRates, arms: []string{"stock", "qompack"}, out: t.TempDir(),
			install: liveInstallPluginDir, bundle: fakeBundle(t), includeHeldOut: true, maxSessions: 40,
			defectsAttested: true, openDefects: []string{}, dryRun: true, confirmatory: true,
		}
	}
	var out bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), confirmatory(), env, &out))
	pre := eval.LivePreregistrations["qompack-live-v2"]
	for _, want := range []string{
		"40 trial(s) of task set qompack-live-v2 (" + pre.TaskSetSHA256[:12] + ", fixture tree " + pre.FixtureTreeSHA256[:12] +
			") on claude-sonnet-5, arms stock,qompack, install plugin-dir",
		"live-eval: bundle 9.9.9-test at commit deadbeef (dirty=false)",
		"live-eval: confirmatory preconditions at plan time: met",
		"plan: seed-recall/qompack/2",
		"--model claude-sonnet-5",
	} {
		require.Contains(t, out.String(), want)
	}

	for name, c := range map[string]struct {
		mutate func(*liveOptions)
		want   string
	}{
		"held-out excluded": {func(o *liveOptions) { o.includeHeldOut = false }, "it excluded the held-out tasks"},
		"an open defect":    {func(o *liveOptions) { o.openDefects = []string{"C1.15"} }, "known open defect(s) C1.15"},
		"one trial per arm": {func(o *liveOptions) { o.trials = 1 }, "ran 1 trials per arm per task, not the pre-registered 2"},
		"marketplace":       {func(o *liveOptions) { o.install = liveInstallMarketplace }, "not the pre-registered --plugin-dir"},
		"another model":     {func(o *liveOptions) { o.model = "opus" }, "not the pre-registered model claude-sonnet-5"},
		"the pilot":         {func(o *liveOptions) { o.tasksFile = liveTestPilot }, "has no pre-registration"},
	} {
		o := confirmatory()
		o.confirmatory = false
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

	o, err := parseLiveFlags([]string{"--confirmatory"})
	require.NoError(t, err)
	require.True(t, o.confirmatory)
}

// TestPlanLiveTrials_AlternatesArmOrder: across tasks and trials each arm goes first half the time.
func TestPlanLiveTrials_AlternatesArmOrder(t *testing.T) {
	tasks := []eval.LiveTask{{ID: "a"}, {ID: "b"}}
	plan := planLiveTrials(tasks, []string{"stock", "qompack"}, 2)
	require.Len(t, plan, 8)
	first := map[string]int{}
	for i := 0; i < len(plan); i += 2 {
		first[plan[i].Arm]++
		require.Equal(t, plan[i].Task, plan[i+1].Task)
		require.NotEqual(t, plan[i].Arm, plan[i+1].Arm)
	}
	require.Equal(t, map[string]int{"stock": 2, "qompack": 2}, first)
}

func TestSelectLiveTasks_HeldOutNeedsTheConfirmatoryFlag(t *testing.T) {
	ts, _, err := eval.LoadLiveTaskSet(liveTestTasks)
	require.NoError(t, err)
	got, err := selectLiveTasks(ts, liveOptions{})
	require.NoError(t, err)
	require.Len(t, got, 7, "the three held-out tasks are excluded by default")
	got, err = selectLiveTasks(ts, liveOptions{includeHeldOut: true})
	require.NoError(t, err)
	require.Len(t, got, 10)
	_, err = selectLiveTasks(ts, liveOptions{only: map[string]bool{"seed-recall": true}})
	require.ErrorContains(t, err, "held out")
	_, err = selectLiveTasks(ts, liveOptions{only: map[string]bool{"nope": true}})
	require.ErrorContains(t, err, "unknown task")
}

// TestLiveSessionArgs_IdenticalAcrossArmsButThePlugin pins the host command line: streaming input
// and output, hook events, no user settings (so the operator's own plugins and hooks load in
// neither arm), dontAsk with the task's allow-list, and a session id the driver chose.
func TestLiveSessionArgs_IdenticalAcrossArmsButThePlugin(t *testing.T) {
	ts, _, err := eval.LoadLiveTaskSet(liveTestPilot)
	require.NoError(t, err)
	task := ts.Tasks[0]
	stock := liveSessionArgs("m", task, ts.Defaults, "sid", nil)
	plug := liveSessionArgs("m", task, ts.Defaults, "sid", []string{"--plugin-dir", "B"})
	require.Equal(t, stock, plug[:len(stock)])
	require.Equal(t, []string{"--plugin-dir", "B"}, plug[len(stock):])
	joined := strings.Join(stock, " ")
	for _, want := range []string{
		"-p", "--input-format stream-json", "--output-format stream-json", "--verbose", "--include-hook-events",
		"--model m", "--max-turns 6", "--setting-sources project,local", "--permission-mode dontAsk", "--session-id sid",
		"--allowedTools Read,Edit,Write",
	} {
		require.Contains(t, joined, want)
	}
	require.Contains(t, liveSessionEnv(120), "QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120")
	require.Equal(t, []string{
		"Run go run ./cmd/code and tell me the code word it prints, in one line.", "/compact",
		task.Steps[2].Prompt,
	}, liveMessages(task))
	require.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, newUUID())
}

func TestRunLiveEval_DryRunAndTheGate(t *testing.T) {
	env := fakeLiveEnv(t, nil)
	var out bytes.Buffer
	o := liveOptions{tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"stock"}, out: t.TempDir(), dryRun: true}
	require.NoError(t, runLiveEval(context.Background(), o, env, &out))
	require.Contains(t, out.String(), "plan: pilot-codeword/stock/1")
	require.Contains(t, out.String(), "install none", "a run with no plugin arm installs nothing")
	require.Contains(t, out.String(), "no session started")

	o.arms = []string{"stock", "qompack"}
	require.ErrorContains(t, runLiveEval(context.Background(), o, env, &out), "--bundle", "the qompack arm needs a bundle")

	o.arms, o.dryRun = []string{"stock"}, false
	t.Setenv(liveEvalGateEnv, "")
	err := runLiveEval(context.Background(), o, env, &out)
	require.ErrorContains(t, err, liveEvalGateEnv, "no real session without the gate")

	o.maxSessions, o.trials = 1, 2
	t.Setenv(liveEvalGateEnv, "1")
	require.ErrorContains(t, runLiveEval(context.Background(), o, env, &out), "--max-sessions")
}

// TestRunLiveEval_OfflineTrialsBothArms drives the whole driver with a scripted host: the pilot
// task on both arms, the qompack arm through the marketplace flow, grading against the real hidden
// test with the real `go` toolchain, and the guard around every trial.
func TestRunLiveEval_OfflineTrialsBothArms(t *testing.T) {
	home := t.TempDir()
	var cliCalls []string
	env := fakeLiveEnv(t, &cliCalls)
	env.home = home
	env.run = scriptedPilotHost(t, home)
	bundle := fakeBundle(t)
	out := t.TempDir()
	t.Setenv(liveEvalGateEnv, "1")

	// Mirror what 2.1.280 did in the marketplace rehearsal: install copies the bundle into the
	// global plugin cache, and uninstall leaves that copy behind marked .orphaned_at.
	cache := filepath.Join(home, "plugins", "cache", liveMarketplaceName, livePluginName, "9.9.9-test")
	recordCLI := env.cli
	env.cli = func(ctx context.Context, dir string, args ...string) liveCLIResult {
		if len(args) > 1 && args[0] == "plugin" {
			switch args[1] {
			case "install":
				require.NoError(t, copyTree(bundle, cache))
			case "uninstall":
				require.NoError(t, os.WriteFile(filepath.Join(cache, orphanedMarker), []byte("1"), 0o600))
			}
		}
		return recordCLI(ctx, dir, args...)
	}

	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"stock", "qompack"}, out: out,
		install: liveInstallMarketplace, bundle: bundle, idleExit: 1, trials: 1, defectsAttested: true,
	}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())

	for _, arm := range []string{"stock", "qompack"} {
		var rec eval.LiveTrial
		readJSON(t, filepath.Join(out, "trials", "pilot-codeword", arm, "01", "trial.json"), &rec)
		require.Empty(t, rec.HarnessError, arm)
		require.True(t, rec.Completed, "%s: %+v", arm, rec.Steps)
		require.True(t, rec.TaskSuccess, "%s: %+v", arm, rec.Checks)
		require.Zero(t, rec.ConstraintViolations, "%s: %+v", arm, rec.Checks)
		require.NotNil(t, rec.Recovered)
		require.True(t, *rec.Recovered)
		require.True(t, rec.HomeGuard.Checked)
		require.True(t, rec.HomeGuard.Unchanged, "%+v", rec.HomeGuard)
		require.Len(t, rec.Compactions, 1)
		require.NotNil(t, rec.Estimate)
		require.Positive(t, rec.Estimate.Micros)
		require.Equal(t, "2026-09-22", rec.RateTableDate)
		require.NotEmpty(t, rec.TranscriptHookMS, "the host-measured hook durations come from the transcript")
		require.Equal(t, arm == "qompack", rec.PluginLoaded)
		require.False(t, rec.PreregisteredModel == false && rec.Model == "claude-haiku-4-5-20251001")
		require.Equal(t, "claude-haiku-4-5-20251001", rec.HostModel, "the model the host reported at start-up")
		for _, f := range []string{"stream.jsonl", "stderr.txt", "ledger.json", "estimate.json", "transcript-facts.json", "invocation.json"} {
			require.FileExists(t, filepath.Join(out, "trials", "pilot-codeword", arm, "01", f))
		}
		require.NoFileExists(t, filepath.Join(out, "trials", "pilot-codeword", arm, "01", "transcript.raw.jsonl"),
			"the raw transcript is copied only on request")
		if arm == "qompack" {
			require.NotNil(t, rec.Qompack)
			require.Equal(t, "connected", rec.Qompack.MCPServerStatus)
			require.Equal(t, liveInstallMarketplace, rec.Install)
			require.Equal(t, "9.9.9-test", rec.Plugin.Version)
			require.Len(t, rec.HookProblems, 1, "the scripted host rejected PreCompact the way 2.1.280 does")
		} else {
			require.Nil(t, rec.Qompack)
			require.Empty(t, rec.HookProblems)
		}
	}
	require.Equal(t, []string{
		"plugin marketplace add", "plugin install qompack@qompack-live-eval --scope local -y",
		"plugin uninstall qompack@qompack-live-eval --scope local -y", "plugin marketplace remove qompack-live-eval",
	}, cliCalls)

	require.NoDirExists(t, filepath.Join(home, "plugins", "cache", liveMarketplaceName),
		"the orphaned copy of this run's bundle is removed")

	var sum eval.LiveSummary
	readJSON(t, filepath.Join(out, "summary.json"), &sum)
	require.Equal(t, 1, sum.Arms["qompack"].HookProblemTrials)
	require.FileExists(t, filepath.Join(out, "summary.md"))
	require.FileExists(t, filepath.Join(out, "plan.json"))
}

// TestRunLiveEval_GuardFailsClosed: an install that leaves the operator's plugin registry changed
// stops the run after that trial, and the trial record says what changed.
func TestRunLiveEval_GuardFailsClosed(t *testing.T) {
	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	env.run = scriptedPilotHost(t, home)
	env.cli = func(_ context.Context, _ string, args ...string) liveCLIResult {
		if len(args) > 1 && args[1] == "install" {
			require.NoError(t, os.MkdirAll(filepath.Join(home, "plugins"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(home, "plugins", "installed_plugins.json"), []byte(`{"leaked":true}`), 0o600))
		}
		return liveCLIResult{}
	}
	out := t.TempDir()
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"qompack", "stock"}, out: out,
		install: liveInstallMarketplace, bundle: fakeBundle(t), idleExit: 1, trials: 1, defectsAttested: true,
	}
	var log bytes.Buffer
	err := runLiveEval(context.Background(), o, env, &log)
	require.ErrorContains(t, err, "configuration changed")
	require.Contains(t, log.String(), "STOPPING")

	var rec eval.LiveTrial
	readJSON(t, filepath.Join(out, "trials", "pilot-codeword", "qompack", "01", "trial.json"), &rec)
	require.False(t, rec.HomeGuard.Unchanged)
	require.Contains(t, strings.Join(rec.HomeGuard.After, "\n"), "plugins/installed_plugins.json")
	require.NoDirExists(t, filepath.Join(out, "trials", "pilot-codeword", "stock"), "no trial runs after the guard fails")
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(out, "summary.json"), &sum)
	require.Contains(t, strings.Join(sum.Notes, "\n"), "stopped early")
}

// TestRemoveCreatedPluginData: a plugin data directory the trial created is removed when empty and
// named when not; one that existed before is never touched.
func TestRemoveCreatedPluginData(t *testing.T) {
	home := t.TempDir()
	env := &liveEnv{home: home}
	pre := filepath.Join(home, "plugins", "data", "qompack-preexisting")
	require.NoError(t, os.MkdirAll(pre, 0o755))
	before, err := env.guardSnapshot()
	require.NoError(t, err)
	empty := filepath.Join(home, "plugins", "data", "qompack-inline")
	full := filepath.Join(home, "plugins", "data", "qompack-qompack-live-eval")
	require.NoError(t, os.MkdirAll(empty, 0o755))
	require.NoError(t, os.MkdirAll(full, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(full, "x"), []byte("x"), 0o600))

	// A directory absent from the snapshot but older than the trial was made by someone else
	// sharing this configuration (another lane's session): it is named, never removed.
	foreign := filepath.Join(home, "plugins", "data", "qompack-shell-probe-inline")
	require.NoError(t, os.MkdirAll(foreign, 0o755))
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(foreign, old, old))

	left := env.removeCreatedPluginData(before, time.Now().Add(-time.Minute))
	require.NoDirExists(t, empty)
	require.DirExists(t, full)
	require.DirExists(t, pre)
	require.DirExists(t, foreign)
	require.Len(t, left, 2)
	joined := strings.Join(left, "\n")
	require.Contains(t, joined, "qompack-qompack-live-eval")
	require.Contains(t, joined, "qompack-shell-probe-inline")
}

// TestLiveTaskSet_ReferenceSolutionsPassEveryCheck proves every task is gradeable: a correct,
// constraint-respecting project (fixture + reference solution + hidden tests, with the reference
// answers and no tool re-run) passes every check, and the untouched fixture does not pass them all.
// It covers the confirmatory set, the pilot and qompack-live-v1, which stays gradeable as the record
// of the set amendment A7 superseded; the stricter rule every current set must also meet is
// TestLiveTaskSet_AnUntouchedFixtureFailsTheTaskOutcome.
func TestLiveTaskSet_ReferenceSolutionsPassEveryCheck(t *testing.T) {
	for _, file := range []string{liveTestTasks, liveTestPilot, liveTestTasksV1} {
		ts, _, err := eval.LoadLiveTaskSet(file)
		require.NoError(t, err)
		for _, task := range ts.Tasks {
			t.Run(ts.ID+"/"+task.ID, func(t *testing.T) {
				t.Parallel()
				for _, r := range gradeLiveProject(t, file, task, true, true) {
					require.True(t, r.Passed, "reference fails %s: %s", r.ID, r.Detail)
				}
				// The untouched fixture must not already pass every check. A failing non-command
				// check settles it without compiling anything; only when every file and answer check
				// passes do the commands have to run and one of them fail.
				failed := func(res []eval.LiveCheckResult, kinds func(string) bool) bool {
					for _, r := range res {
						if kinds(r.Kind) && !r.Passed {
							return true
						}
					}
					return false
				}
				notCommand := func(k string) bool { return k != eval.CheckCommand }
				if !failed(gradeLiveProject(t, file, task, false, false), notCommand) {
					require.True(t, failed(gradeLiveProject(t, file, task, false, true), func(string) bool { return true }),
						"the untouched fixture must not already pass every check")
				}
			})
		}
	}
}

// gradeLiveProject builds one task's project the way a trial leaves it for grading — the fixture,
// then (withReference) the task's reference solution, then the hidden tests — and grades it with the
// reference answers and no tool call after any compaction. runCommands false leaves every command
// check ungraded (failed), which settles the file and answer checks without compiling anything.
func gradeLiveProject(t *testing.T, file string, task eval.LiveTask, withReference, runCommands bool) []eval.LiveCheckResult {
	t.Helper()
	return gradeLiveProjectWith(t, file, task, withReference, nil, runCommands)
}

// gradeLiveProjectWith is gradeLiveProject with extra files (slash path to content) written over the
// project after the reference solution and before the hidden tests: what a session left that the
// reference did not.
func gradeLiveProjectWith(t *testing.T, file string, task eval.LiveTask, withReference bool, extra map[string]string,
	runCommands bool,
) []eval.LiveCheckResult {
	t.Helper()
	var answers map[string]map[string]string
	readJSON(t, "../../testdata/eval/live/reference/answers.json", &answers)
	fixture := task.FixtureDir(file)
	hashes, err := eval.HashTree(fixture)
	require.NoError(t, err)
	after := map[string][]eval.HostToolUse{}
	for _, s := range task.Steps {
		if s.Compact {
			after[s.ID] = nil
		}
	}
	project := t.TempDir()
	require.NoError(t, copyTree(fixture, project))
	if withReference {
		require.NoError(t, copyTree(filepath.Join(filepath.Dir(file), "reference", task.ID), project))
	}
	for p, body := range extra {
		target := filepath.Join(project, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
		require.NoError(t, os.WriteFile(target, []byte(body), 0o600))
	}
	if task.HiddenFixture != "" {
		require.NoError(t, copyTree(filepath.Join(filepath.Dir(file), filepath.FromSlash(task.HiddenFixture)), project))
	}
	cmds := map[string]eval.CommandOutcome{}
	for _, c := range task.Checks {
		if c.Kind == eval.CheckCommand && runCommands {
			cmds[c.ID] = runLiveCheckCommand(project, c.Argv)
		}
	}
	return eval.GradeLiveTrial(task, project, eval.LiveEvidence{
		Answers: answers[task.ID], Fixture: hashes, Commands: cmds, ToolUsesAfter: after,
	})
}

// liveTask returns the task id of the task set in file.
func liveTask(t *testing.T, file, id string) eval.LiveTask {
	t.Helper()
	ts, _, err := eval.LoadLiveTaskSet(file)
	require.NoError(t, err)
	for _, task := range ts.Tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("%s declares no task %s", file, id)
	return eval.LiveTask{}
}

// liveResults indexes graded checks by id.
func liveResults(res []eval.LiveCheckResult) map[string]eval.LiveCheckResult {
	out := map[string]eval.LiveCheckResult{}
	for _, r := range res {
		out[r.ID] = r
	}
	return out
}

// TestLiveTaskSet_AnUntouchedFixtureFailsTheTaskOutcome: a session that does nothing after the
// compaction must not score a task success. Task success is the primary outcome (preregistration
// section 6, H1), so a task whose task checks the untouched fixture already passes counts every
// trial of it as a success whatever the session did, pushing both arms towards 1 and a
// non-inferior verdict. Owner decision D12: every task of the confirmatory set needs a task check
// the untouched fixture fails — failing some other outcome (a recovery check) is not enough. The
// pilot meets the same rule; qompack-live-v1 did not (tool-output-recall and seed-recall), which is
// why amendment A7 superseded it before use.
func TestLiveTaskSet_AnUntouchedFixtureFailsTheTaskOutcome(t *testing.T) {
	for _, file := range []string{liveTestTasks, liveTestPilot} {
		ts, _, err := eval.LoadLiveTaskSet(file)
		require.NoError(t, err)
		for _, task := range ts.Tasks {
			t.Run(ts.ID+"/"+task.ID, func(t *testing.T) {
				t.Parallel()
				var passedTask []string
				failed := false
				for _, r := range gradeLiveProject(t, file, task, false, true) {
					if r.Outcome != eval.OutcomeTask {
						continue
					}
					if r.Passed {
						passedTask = append(passedTask, r.ID)
					} else {
						failed = true
					}
				}
				require.True(t, failed, "the untouched fixture passes every task check of %s (%v)", task.ID, passedTask)
			})
		}
	}
}

// TestLiveTaskSet_RegressionGuardPinnedGradesOnlyParseList: regression-guard's constraint check
// "pinned" says ParseList's behaviour is unchanged. A trial that never wrote JoinList did not touch
// ParseList, so it must fail the task (the JoinList checks) and still pass "pinned"; a check that
// compiles the hidden JoinList test beside the pinned one fails the constraint too, counting one
// omission in both H1 and H2. The check must still catch what it is for: a ParseList whose
// behaviour changed on a pinned input fails it, with JoinList written correctly.
func TestLiveTaskSet_RegressionGuardPinnedGradesOnlyParseList(t *testing.T) {
	task := liveTask(t, liveTestTasks, "regression-guard")
	got := liveResults(gradeLiveProject(t, liveTestTasks, task, false, true))
	require.True(t, got["pinned"].Passed, "ParseList is untouched, so pinned must pass: %s", got["pinned"].Detail)
	require.False(t, got["join"].Passed, "JoinList was never written: %s", got["join"].Detail)
	require.False(t, got["all"].Passed, "JoinList was never written: %s", got["all"].Detail)

	// A "faster" ParseList that stops trimming tabs: list_test.go still passes, a pinned input does not.
	faster := "package list\n\nimport \"strings\"\n\n// ParseList splits s on commas.\nfunc ParseList(s string) []string {\n" +
		"\tvar out []string\n\tfor _, part := range strings.Split(s, \",\") {\n" +
		"\t\tif p := strings.Trim(part, \" \"); p != \"\" {\n\t\t\tout = append(out, p)\n\t\t}\n\t}\n\treturn out\n}\n"
	got = liveResults(gradeLiveProjectWith(t, liveTestTasks, task, true, map[string]string{"list/list.go": faster}, true))
	require.False(t, got["pinned"].Passed, "a changed ParseList must fail pinned: %s", got["pinned"].Detail)
	require.Contains(t, got["pinned"].Detail, "--- FAIL: TestHiddenEvalParseListPinned")
	require.True(t, got["join"].Passed, "JoinList itself is right: %s", got["join"].Detail)
}

// TestLiveTaskSet_ToolResultTasksGradeTheDeliverable: tool-output-recall's and seed-recall's task
// check is a hidden test of the file the final step asks for, so a session that wrote the wrong
// token, a placeholder, or a variable where the prompt asked for a constant does not score a task
// success, and the reference does.
func TestLiveTaskSet_ToolResultTasksGradeTheDeliverable(t *testing.T) {
	for name, c := range map[string]struct {
		task, path, body string
		pass             bool
	}{
		"token right":          {"tool-output-recall", "TOKEN", "975408daf9b5\n", true},
		"token with blanks":    {"tool-output-recall", "TOKEN", "\n  975408daf9b5  \r\n", true},
		"token wrong":          {"tool-output-recall", "TOKEN", "975408daf9b6\n", false},
		"token in a sentence":  {"tool-output-recall", "TOKEN", "build-token: 975408daf9b5\n", false},
		"seed right":           {"seed-recall", "config/seed.go", "package config\n\nconst Seed = \"5cc87334711f2d40\"\n", true},
		"seed as a variable":   {"seed-recall", "config/seed.go", "package config\n\nvar Seed = \"5cc87334711f2d40\"\n", false},
		"seed placeholder":     {"seed-recall", "config/seed.go", "package config\n\nconst Seed = \"<the seed>\"\n", false},
		"seed typed and right": {"seed-recall", "config/seed.go", "package config\n\nconst Seed string = \"5cc87334711f2d40\"\n", true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			task := liveTask(t, liveTestTasks, c.task)
			got := liveResults(gradeLiveProjectWith(t, liveTestTasks, task, false, map[string]string{c.path: c.body}, true))
			require.Equal(t, c.pass, got["behaviour"].Passed, got["behaviour"].Detail)
		})
	}
}

// ── fakes ────────────────────────────────────────────────────────────────────────────────────────

func fakeLiveEnv(t *testing.T, cliCalls *[]string) *liveEnv {
	t.Helper()
	return &liveEnv{
		claudeBin: "claude-fake",
		home:      t.TempDir(),
		workRoot:  t.TempDir(),
		now:       func() time.Time { return time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC) },
		run: func(context.Context, liveProcSpec) liveProcResult {
			t.Fatal("no session may start in this test")
			return liveProcResult{}
		},
		cli: func(_ context.Context, _ string, args ...string) liveCLIResult {
			if cliCalls != nil && len(args) > 0 && args[0] == "plugin" {
				call := strings.Join(args, " ")
				if args[1] == "marketplace" && args[2] == "add" {
					call = "plugin marketplace add"
				}
				*cliCalls = append(*cliCalls, call)
			}
			if len(args) > 0 && args[0] == "--version" {
				return liveCLIResult{Stdout: "2.1.280 (Claude Code)\n"}
			}
			return liveCLIResult{}
		},
		stopDaemon: func(string, string) (string, error) { return "no daemon lock after the session", nil },
		check:      runLiveCheckCommand,
		git:        runLiveGit,
	}
}

// fakeBundle writes a minimal bundle directory whose BUNDLE.json matches its files.
func fakeBundle(t *testing.T) string {
	t.Helper()
	return fakeBundleIn(t, t.TempDir())
}

// fakeBundleIn is fakeBundle made under parent.
func fakeBundleIn(t *testing.T, parent string) string {
	t.Helper()
	dir := filepath.Join(parent, "qompack-plugin-9.9.9-test-"+runtime.GOOS+"-"+runtime.GOARCH)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755))
	manifest := []byte(`{"name":"qompack","version":"9.9.9-test"}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), manifest, 0o600))
	sum, size, err := hashFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	require.NoError(t, err)
	id := bundleIdentity{
		Name: "qompack", Version: "9.9.9-test", Target: bundleTarget{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Source: bundleSource{Commit: "deadbeef"}, Files: []bundleFile{{Path: ".claude-plugin/plugin.json", SHA256: sum, Bytes: size}},
	}
	raw, err := json.Marshal(id)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, identityFileName), raw, 0o600))
	return dir
}

// pilotHostOpts varies what the scripted host reports about its plugins.
type pilotHostOpts struct {
	// source, when set, replaces the source the host reports for an installed Qompack plugin.
	source string
	// extra are further plugin objects (JSON) the host lists as loaded.
	extra []string
}

// scriptedPilotHost plays the pilot task the way a real host would: it prints a stream shaped like
// Claude Code 2.1.280's, writes CODE.txt into the project, and leaves a transcript under home. The
// plugin source it reports is the one 2.1.280 reported for each install path in the pilots:
// "qompack@inline" for --plugin-dir and "qompack@<marketplace>" for the marketplace flow.
func scriptedPilotHost(t *testing.T, home string) func(context.Context, liveProcSpec) liveProcResult {
	t.Helper()
	return scriptedPilotHostWith(t, home, pilotHostOpts{})
}

func scriptedPilotHostWith(t *testing.T, home string, opts pilotHostOpts) func(context.Context, liveProcSpec) liveProcResult {
	t.Helper()
	return func(_ context.Context, spec liveProcSpec) liveProcResult {
		sid := ""
		plugin := false
		for i, a := range spec.Args {
			if a == "--session-id" {
				sid = spec.Args[i+1]
			}
			if a == "--plugin-dir" {
				plugin = true
			}
		}
		if _, err := os.Stat(filepath.Join(spec.Dir, ".git")); err != nil {
			t.Errorf("the project was not a git repository when the session started")
		}
		marketplace := false
		if raw, err := os.ReadFile(filepath.Join(spec.Dir, "..", "marketplace", ".claude-plugin", "marketplace.json")); err == nil {
			marketplace = strings.Contains(string(raw), liveMarketplaceName)
		}
		loaded := plugin || marketplace
		var plugins []string
		if loaded {
			source := livePluginName + "@" + liveInlineSource
			if marketplace {
				source = livePluginName + "@" + liveMarketplaceName
			}
			if opts.source != "" {
				source = opts.source
			}
			plugins = append(plugins, fmt.Sprintf(`{"name":"qompack","path":"x","source":%q,"version":"9.9.9-test"}`, source))
		}
		plugins = append(plugins, `{"name":"agents-md","path":"builtin","source":"agents-md@builtin"}`)
		plugins = append(plugins, opts.extra...)
		require.NoError(t, os.WriteFile(filepath.Join(spec.Dir, "CODE.txt"), []byte(pilotCodeWord+"\n"), 0o600))

		transcript, err := os.ReadFile("../../internal/eval/testdata/live/smoke2-plugin.transcript.redacted.jsonl")
		require.NoError(t, err)
		if !loaded {
			// The stock arm's host ran no PreCompact hook, so its transcript carries no rejection.
			var kept []string
			for _, l := range strings.SplitAfter(string(transcript), "\n") {
				if !strings.Contains(l, "failed: Hook JSON output validation failed") {
					kept = append(kept, l)
				}
			}
			transcript = []byte(strings.Join(kept, ""))
		}
		tdir := filepath.Join(home, "projects", "C--fake-project")
		require.NoError(t, os.MkdirAll(tdir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(tdir, sid+".jsonl"), transcript, 0o600))

		stream := pilotStream(sid, loaded, plugins)
		recv := make([]int64, 0)
		for i := range strings.Count(stream, "\n") {
			recv = append(recv, int64(100*(i+1)))
		}
		return liveProcResult{
			Stream: []byte(stream), RecvMS: recv, StepsCompleted: len(spec.Messages),
			StartedAt: time.Unix(0, 0), EndedAt: time.Unix(3, 0),
		}
	}
}

func pilotStream(sid string, plugin bool, pluginList []string) string {
	plugins := "[" + strings.Join(pluginList, ",") + "]"
	mcp := `[]`
	if plugin {
		mcp = `[{"name":"plugin:qompack:qompack","status":"connected","source":"plugin"}]`
	}
	usage := func(in, out, cr, cw int) string {
		return fmt.Sprintf(`{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":%d},"output_tokens_details":{"thinking_tokens":0}}`, in, out, cr, cw, cw)
	}
	model := func(in, out, cr, cw int, cost float64) string {
		return fmt.Sprintf(`{"claude-haiku-4-5-20251001":{"inputTokens":%d,"outputTokens":%d,"cacheReadInputTokens":%d,"cacheCreationInputTokens":%d,"thinkingTokens":0,"costUSD":%g}}`, in, out, cr, cw, cost)
	}
	lines := []string{
		fmt.Sprintf(`{"type":"system","subtype":"init","session_id":%q,"model":"claude-haiku-4-5-20251001","claude_code_version":"2.1.280","plugins":%s,"mcp_servers":%s,"tools":["Bash"]}`, sid, plugins, mcp),
		`{"type":"system","subtype":"hook_started","hook_id":"h1","hook_name":"SessionStart:startup","hook_event":"SessionStart"}`,
		`{"type":"system","subtype":"hook_response","hook_id":"h1","hook_name":"SessionStart:startup","hook_event":"SessionStart","output":"{}","exit_code":0,"outcome":"success"}`,
		`{"type":"assistant","message":{"id":"m1","model":"claude-haiku-4-5-20251001","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"go run ./cmd/code"}}],"usage":` + usage(10, 1, 100, 50) + `},"parent_tool_use_id":null}`,
		`{"type":"assistant","message":{"id":"m2","model":"claude-haiku-4-5-20251001","content":[{"type":"text","text":"The code word is ` + pilotCodeWord + `."}],"usage":` + usage(8, 1, 150, 20) + `},"parent_tool_use_id":null}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"The code word is ` + pilotCodeWord + `.","total_cost_usd":0.001,"usage":` + usage(18, 40, 250, 70) + `,"modelUsage":` + model(18, 40, 250, 70, 0.001) + `,"permission_denials":[]}`,
		`{"type":"system","subtype":"status","status":"compacting"}`,
		`{"type":"system","subtype":"status","status":null,"compact_result":"success"}`,
		`{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"manual","pre_tokens":5000,"post_tokens":900,"duration_ms":4000}}`,
	}
	local := "Compacted "
	if plugin {
		local = `Compacted PreCompact [\"${CLAUDE_PLUGIN_ROOT}/bin/qompack\" checkpoint] failed: Hook JSON output validation failed — hookSpecificOutput.hookEventName: expected one of \"PreToolUse\"`
	}
	lines = append(lines,
		`{"type":"user","message":{"role":"user","content":"<local-command-stdout>`+local+`</local-command-stdout>"}}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":0,"local_command":"compact","result":"","total_cost_usd":0.002,"usage":`+usage(0, 0, 0, 0)+`,"modelUsage":`+model(1018, 240, 250, 90, 0.002)+`,"permission_denials":[]}`,
		`{"type":"assistant","message":{"id":"m3","model":"claude-haiku-4-5-20251001","content":[{"type":"tool_use","id":"tu2","name":"Write","input":{"file_path":"CODE.txt"}}],"usage":`+usage(5, 1, 300, 10)+`},"parent_tool_use_id":null}`,
		`{"type":"assistant","message":{"id":"m4","model":"claude-haiku-4-5-20251001","content":[{"type":"text","text":"Wrote `+pilotCodeWord+` to CODE.txt."}],"usage":`+usage(5, 1, 310, 5)+`},"parent_tool_use_id":null}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"Wrote `+pilotCodeWord+` to CODE.txt.","total_cost_usd":0.003,"usage":`+usage(10, 30, 610, 15)+`,"modelUsage":`+model(1028, 270, 860, 105, 0.003)+`,"permission_denials":[]}`,
	)
	return strings.Join(lines, "\n") + "\n"
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, v))
}

// TestRemoveOrphanedMarketplaceCache: after `plugin uninstall` Claude Code 2.1.280 keeps the
// uninstalled copy under plugins/cache/<marketplace>/ with an .orphaned_at marker for its own later
// sweep (observed in the marketplace rehearsal). The driver removes that copy only when the trial
// created it, the host marked every version orphaned, and every version is this run's bundle.
func TestRemoveOrphanedMarketplaceCache(t *testing.T) {
	bundleJSON := []byte(`{"name":"qompack","version":"1"}`)
	setup := func(t *testing.T, orphaned bool, identity []byte) (*liveEnv, string) {
		t.Helper()
		home := t.TempDir()
		v := filepath.Join(home, "plugins", "cache", liveMarketplaceName, livePluginName, "1")
		require.NoError(t, os.MkdirAll(v, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(v, identityFileName), identity, 0o600))
		if orphaned {
			require.NoError(t, os.WriteFile(filepath.Join(v, ".orphaned_at"), []byte("1790117032484"), 0o600))
		}
		return &liveEnv{home: home}, filepath.Join(home, "plugins", "cache", liveMarketplaceName)
	}
	sum := sha256Hex(bundleJSON)

	env, dir := setup(t, true, bundleJSON)
	require.Empty(t, env.removeOrphanedMarketplaceCache(liveGuardSnap{}, sum))
	require.NoDirExists(t, dir)

	env, dir = setup(t, false, bundleJSON)
	left := env.removeOrphanedMarketplaceCache(liveGuardSnap{}, sum)
	require.Len(t, left, 1)
	require.Contains(t, left[0], "not marked orphaned")
	require.DirExists(t, dir)

	env, dir = setup(t, true, []byte(`{"someone":"else"}`))
	left = env.removeOrphanedMarketplaceCache(liveGuardSnap{}, sum)
	require.Len(t, left, 1)
	require.Contains(t, left[0], "not this run's bundle")
	require.DirExists(t, dir)

	env, dir = setup(t, true, bundleJSON)
	require.Empty(t, env.removeOrphanedMarketplaceCache(liveGuardSnap{"plugins/cache/" + liveMarketplaceName: "present"}, sum))
	require.DirExists(t, dir, "a cache that existed before the trial is never touched")
}

// TestRunLiveEval_HarnessFailureIsScoredAsFailingEveryOutcome: a trial the harness could not even
// prepare (here, git refuses to initialise the project) never reaches grading, and its record must
// still say it failed on every pre-registered outcome — recovery included — so the summary keeps it
// in every denominator (preregistration §8, intention to treat).
func TestRunLiveEval_HarnessFailureIsScoredAsFailingEveryOutcome(t *testing.T) {
	env := fakeLiveEnv(t, nil)
	env.git = func(context.Context, string, ...string) error { return fmt.Errorf("git init: exit status 128") }
	out := t.TempDir()
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"stock"}, out: out, idleExit: 1, trials: 1}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())

	var rec eval.LiveTrial
	readJSON(t, filepath.Join(out, "trials", "pilot-codeword", "stock", "01", "trial.json"), &rec)
	require.Contains(t, rec.HarnessError, "preparing the project")
	require.False(t, rec.Completed)
	require.False(t, rec.TaskSuccess)
	require.NotNil(t, rec.Recovered, "the pilot task declares recovery checks, so the failed trial is in that denominator")
	require.False(t, *rec.Recovered)

	var sum eval.LiveSummary
	readJSON(t, filepath.Join(out, "summary.json"), &sum)
	st := sum.Arms[eval.ArmStock]
	require.Equal(t, [2]int{0, 1}, [2]int{st.Recovery.K, st.Recovery.N})
	require.Equal(t, [2]int{0, 1}, [2]int{st.ConstraintClean.K, st.ConstraintClean.N})
	require.Len(t, sum.Failed, 1)
}

// TestRenderLiveSummary_ShowsEveryPreregisteredReport: summary.md carries everything section 8
// reports beside the decision — the H2 regression, the secondary differences, the per-variant
// table, the per-task signs and the count of trials whose usage account is unreliable.
func TestRenderLiveSummary_ShowsEveryPreregisteredReport(t *testing.T) {
	a := eval.LiveAnalysis{Model: "m", Confidence: 0.95, NonInferiorityMargin: 0.2, TrialsPerArm: 1}
	mk := func(task, arm, variant string, held, ok bool, violations int) eval.LiveTrial {
		return eval.LiveTrial{
			TaskID: task, Arm: arm, Trial: 1, Variant: variant, HeldOut: held, Completed: true, TaskSuccess: ok,
			ConstraintViolations: violations, PluginExpected: arm == eval.ArmQompack,
			PluginLoaded: arm == eval.ArmQompack, PreregisteredModel: true, HostReportedPlugins: true,
		}
	}
	var ts []eval.LiveTrial
	for i := range 12 {
		id := fmt.Sprintf("t%02d", i)
		ts = append(ts, mk(id, eval.ArmStock, "base", i == 0, true, 0), mk(id, eval.ArmQompack, "base", i == 0, i != 1, 1))
	}
	ts[1].Account = eval.SessionAccount{Problems: []string{"turn 1: model x running input total decreased"}}
	sum := eval.SummarizeLive("r", a, ts)
	require.NotEmpty(t, sum.ConstraintRegression)
	md := renderLiveSummary(eval.LivePlan{RunID: "r", TaskSet: "s", TaskSetSHA256: "abc", Model: "m", PreregisteredModel: "m"}, sum)
	for _, want := range []string{
		"**Regression (H2):**",
		"Constraint-clean difference (qompack − stock):",
		"## By variant",
		"| held-out | qompack | 1 |",
		"## Per-task sign",
		"t01: −1",
		"| inconsistent accounts |",
	} {
		require.Contains(t, md, want)
	}
}

// TestRunLiveEval_PlanRecordsTheFixtureTree: plan.json names the task set by its file hash AND the
// fixture and hidden-test tree by its manifest hash (preregistration amendment A1), so a run on
// edited fixtures under an unchanged tasks.json is visibly not a run of the pre-registered set. The
// dry run prints both.
func TestRunLiveEval_PlanRecordsTheFixtureTree(t *testing.T) {
	want, err := eval.TreeManifestSHA256(filepath.Dir(liveTestPilot), "fixtures", "hidden")
	require.NoError(t, err)

	env := fakeLiveEnv(t, nil)
	var out bytes.Buffer
	o := liveOptions{tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"stock"}, out: t.TempDir(), dryRun: true}
	require.NoError(t, runLiveEval(context.Background(), o, env, &out))
	require.Contains(t, out.String(), "fixture tree "+want[:12])

	home := t.TempDir()
	env.home = home
	env.run = scriptedPilotHost(t, home)
	o.dryRun, o.trials, o.idleExit = false, 1, 1
	t.Setenv(liveEvalGateEnv, "1")
	require.NoError(t, runLiveEval(context.Background(), o, env, &out), out.String())
	var plan eval.LivePlan
	readJSON(t, filepath.Join(o.out, "plan.json"), &plan)
	require.Equal(t, want, plan.FixtureTreeSHA256)
	require.Equal(t, []string{"fixtures", "hidden"}, plan.FixtureTreeDirs)
	md, err := os.ReadFile(filepath.Join(o.out, "summary.md"))
	require.NoError(t, err)
	require.Contains(t, string(md), want)
}

// TestGuardSnapshot_CoversEveryQompackPluginDirectory: the guard records the presence of every
// plugins/{cache,marketplaces,data}/qompack* directory, not only the disposable marketplace's own
// name, so a trial that leaves a Qompack plugin copy anywhere in the operator's plugin store — a
// cache or marketplace entry under another name included — changes the snapshot and stops the run.
// Other plugins' directories are none of the guard's business.
func TestGuardSnapshot_CoversEveryQompackPluginDirectory(t *testing.T) {
	home := t.TempDir()
	env := &liveEnv{home: home}
	for _, rel := range []string{
		"plugins/cache/superpowers-marketplace", "plugins/marketplaces/claude-plugins-official", "plugins/data/rust-analyzer",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, filepath.FromSlash(rel)), 0o755))
	}
	before, err := env.guardSnapshot()
	require.NoError(t, err)

	for _, rel := range []string{
		"plugins/cache/qompack", "plugins/marketplaces/qompack-marketplace", "plugins/data/qompack-inline",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, filepath.FromSlash(rel)), 0o755))
		after, err := env.guardSnapshot()
		require.NoError(t, err)
		require.False(t, before.equal(after), "%s appeared and the guard did not see it", rel)
		require.Contains(t, after.lines(), rel+" present")
		require.NoError(t, os.Remove(filepath.Join(home, filepath.FromSlash(rel))))
	}
	after, err := env.guardSnapshot()
	require.NoError(t, err)
	require.True(t, before.equal(after))
	for _, l := range after.lines() {
		require.NotContains(t, l, "superpowers")
		require.NotContains(t, l, "claude-plugins-official")
		require.NotContains(t, l, "rust-analyzer")
	}
}

// TestRunLiveEval_PluginMustComeFromTheArmsInstall: a qompack trial counts as having its plugin only
// when the host loaded it from the arm's own install. A marketplace-flow trial whose host reports
// Qompack from another source (an inline --plugin-dir copy, an operator install) ran some Qompack,
// not the bundle this arm installed, so its plugin state contradicts the arm; a stock trial whose
// host loaded any non-builtin plugin names it.
func TestRunLiveEval_PluginMustComeFromTheArmsInstall(t *testing.T) {
	run := func(t *testing.T, arms []string, opts pilotHostOpts) (eval.LiveTrial, eval.LiveSummary) {
		t.Helper()
		home := t.TempDir()
		env := fakeLiveEnv(t, nil)
		env.home = home
		env.run = scriptedPilotHostWith(t, home, opts)
		out := t.TempDir()
		t.Setenv(liveEvalGateEnv, "1")
		o := liveOptions{
			tasksFile: liveTestPilot, rates: liveTestRates, arms: arms, out: out,
			install: liveInstallMarketplace, bundle: fakeBundle(t), idleExit: 1, trials: 1, defectsAttested: true,
		}
		var log bytes.Buffer
		require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
		var rec eval.LiveTrial
		readJSON(t, filepath.Join(out, "trials", "pilot-codeword", arms[0], "01", "trial.json"), &rec)
		var sum eval.LiveSummary
		readJSON(t, filepath.Join(out, "summary.json"), &sum)
		return rec, sum
	}

	rec, _ := run(t, []string{"qompack"}, pilotHostOpts{})
	require.True(t, rec.PluginLoaded, "loaded from the disposable marketplace: the arm's own install")
	require.Equal(t, "qompack@"+liveMarketplaceName, rec.Plugin.HostSource)
	require.Empty(t, rec.ForeignPlugins)

	rec, sum := run(t, []string{"qompack"}, pilotHostOpts{source: "qompack@inline"})
	require.False(t, rec.PluginLoaded, "Qompack from another source is not this arm's plugin")
	require.Contains(t, strings.Join(rec.Notes, "\n"), "qompack@inline")
	require.Equal(t, "not-applicable", sum.Decision.Verdict)

	rec, sum = run(t, []string{"stock"}, pilotHostOpts{extra: []string{
		`{"name":"superpowers","path":"p","source":"superpowers@claude-plugins-official"}`,
	}})
	require.False(t, rec.PluginLoaded)
	require.Equal(t, []string{"superpowers@claude-plugins-official"}, rec.ForeignPlugins)
	require.Contains(t, strings.Join(sum.Notes, "\n"), "loaded a plugin other than the arm's own")
}

// TestRunLiveEval_RelativeBundleReachesTheHostWhole: the pre-registration's §9 command names the
// bundle by a path relative to the repository (dist/live-bundle/...), while every host session runs
// in its disposable project directory. A --plugin-dir the driver passed through unchanged would be
// resolved by the host against the project, find nothing there, and every qompack trial would run
// without its plugin. The host must receive a path that names the bundle from wherever it runs.
//
// The bundle is made under the working directory, the package's, where a relative path to it always
// exists: the test's temporary directory may be on another volume, as the hosted Windows runner's
// TEMP (C:) is from its checkout (D:), and no path relative to the one names the other (nightly
// 36820740318). The leading dot keeps the go tool from reading the directory as a package.
func TestRunLiveEval_RelativeBundleReachesTheHostWhole(t *testing.T) {
	wd, err := os.Getwd()
	require.NoError(t, err)
	parent, err := os.MkdirTemp(wd, ".tmp-liveeval-bundle-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(parent)) })
	bundle := fakeBundleIn(t, parent)
	rel, err := filepath.Rel(wd, bundle)
	require.NoError(t, err)
	require.False(t, filepath.IsAbs(rel))

	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	scripted := scriptedPilotHost(t, home)
	var pluginDir, sessionDir string
	env.run = func(ctx context.Context, spec liveProcSpec) liveProcResult {
		for i, a := range spec.Args {
			if a == "--plugin-dir" {
				pluginDir = spec.Args[i+1]
			}
		}
		sessionDir = spec.Dir
		return scripted(ctx, spec)
	}
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"qompack"}, out: t.TempDir(),
		install: liveInstallPluginDir, bundle: rel, idleExit: 1, trials: 1, defectsAttested: true,
	}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	require.NotEmpty(t, pluginDir)
	seen := pluginDir
	if !filepath.IsAbs(seen) {
		seen = filepath.Join(sessionDir, seen) // how the host, running in the project, resolves it
	}
	require.FileExists(t, filepath.Join(seen, identityFileName), "--plugin-dir %q names no bundle from %s", pluginDir, sessionDir)

	var plan eval.LivePlan
	readJSON(t, filepath.Join(o.out, "plan.json"), &plan)
	require.Equal(t, bundle, plan.Plugin.BundleDir)

	// The dry run shows the same resolved command line, so the install path can be checked before
	// any session is spent.
	o.dryRun, o.out = true, t.TempDir()
	log.Reset()
	require.NoError(t, runLiveEval(context.Background(), o, env, &log))
	require.Contains(t, log.String(), "host (qompack, task pilot-codeword): claude-fake -p ")
	require.Contains(t, log.String(), "--plugin-dir "+bundle)
}

// TestLookClaude_RelativePathIsMadeAbsolute: an explicit --claude path is started with the session's
// project as its working directory, so a relative one must be resolved before that.
func TestLookClaude_RelativePathIsMadeAbsolute(t *testing.T) {
	got, err := lookClaude(filepath.Join("bin", "claude"))
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(got), got)
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(wd, "bin", "claude"), got)
}

// TestRunLiveEval_TrialProjectsLiveUnderTheWorkRoot: each trial's disposable project is made under
// the environment's work root, so the offline tests leave nothing behind in the machine's shared
// temporary directory (they used to leave one qompack-live-* project per trial there).
func TestRunLiveEval_TrialProjectsLiveUnderTheWorkRoot(t *testing.T) {
	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	env.run = scriptedPilotHost(t, home)
	root := t.TempDir()
	env.workRoot = root
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"stock"}, out: t.TempDir(), idleExit: 1, trials: 1,
	}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	var rec eval.LiveTrial
	readJSON(t, filepath.Join(o.out, "trials", "pilot-codeword", "stock", "01", "trial.json"), &rec)
	var project string
	for _, n := range rec.Notes {
		if p, ok := strings.CutPrefix(n, "project: "); ok {
			project = p
		}
	}
	rel, err := filepath.Rel(root, project)
	require.NoError(t, err)
	require.False(t, strings.HasPrefix(rel, ".."), "the trial project %s is outside the work root %s", project, root)
}

// TestRunLiveEval_InstallFailureIsAHarnessFailureNotAMismatch: a qompack trial whose marketplace
// install fails never starts a host, so no plugin list was reported. The trial is a harness failure,
// failing every outcome, and does not turn the whole run's verdict into not-applicable.
func TestRunLiveEval_InstallFailureIsAHarnessFailureNotAMismatch(t *testing.T) {
	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	env.run = scriptedPilotHost(t, home)
	env.cli = func(_ context.Context, _ string, args ...string) liveCLIResult {
		if len(args) > 1 && args[0] == "plugin" && args[1] == "install" {
			return liveCLIResult{Code: 1, Stderr: "install failed"}
		}
		return liveCLIResult{}
	}
	out := t.TempDir()
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"qompack", "stock"}, out: out,
		install: liveInstallMarketplace, bundle: fakeBundle(t), idleExit: 1, trials: 1, defectsAttested: true,
	}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	var rec eval.LiveTrial
	readJSON(t, filepath.Join(out, "trials", "pilot-codeword", "qompack", "01", "trial.json"), &rec)
	require.Contains(t, rec.HarnessError, "marketplace install")
	require.False(t, rec.HostReportedPlugins)
	require.False(t, rec.TaskSuccess)
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(out, "summary.json"), &sum)
	require.Zero(t, sum.Arms[eval.ArmQompack].PluginMismatch)
	require.NotEqual(t, "not-applicable", sum.Decision.Verdict, sum.Decision.Reason)
	require.Len(t, sum.Failed, 1)
}

// TestRunLiveEval_StoppedRunReachesNoVerdict: when the guard stops a run part-way, the trials that
// ran are summarized and named, but the pre-registered rule is applied to all planned trials
// (intention to treat), and those that never ran cannot be analysed. The summary therefore carries
// no verdict — not-applicable, saying how many of the planned trials ran — however the partial
// numbers look.
func TestRunLiveEval_StoppedRunReachesNoVerdict(t *testing.T) {
	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	scripted := scriptedPilotHost(t, home)
	sessions := 0
	env.run = func(ctx context.Context, spec liveProcSpec) liveProcResult {
		sessions++
		if sessions == 3 {
			// Something changes the operator's plugin registry during the third trial.
			require.NoError(t, os.MkdirAll(filepath.Join(home, "plugins"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(home, "plugins", "installed_plugins.json"), []byte(`{}`), 0o600))
		}
		return scripted(ctx, spec)
	}
	out := t.TempDir()
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"qompack", "stock"}, out: out,
		install: liveInstallPluginDir, bundle: fakeBundle(t), idleExit: 1, trials: 2, defectsAttested: true,
	}
	var log bytes.Buffer
	require.ErrorContains(t, runLiveEval(context.Background(), o, env, &log), "configuration changed")

	var sum eval.LiveSummary
	readJSON(t, filepath.Join(out, "summary.json"), &sum)
	require.Contains(t, sum.Arms, eval.ArmStock)
	require.Contains(t, sum.Arms, eval.ArmQompack, "both arms ran before the stop")
	require.Equal(t, "not-applicable", sum.Decision.Verdict, sum.Decision.Reason)
	require.Contains(t, sum.Decision.Reason, "3 of the 4 planned trials")
	md, err := os.ReadFile(filepath.Join(out, "summary.md"))
	require.NoError(t, err)
	require.Contains(t, string(md), "not-applicable")
}

// TestRenderLiveSummary_ShowsTheHostsHookLatency: summary.md reports each arm's per-hook latency as
// the host measured it, the measure preregistration section 6 names.
func TestRenderLiveSummary_ShowsTheHostsHookLatency(t *testing.T) {
	a := eval.LiveAnalysis{Model: "m", Confidence: 0.95, NonInferiorityMargin: 0.2, TrialsPerArm: 1}
	q := eval.LiveTrial{
		TaskID: "t", Arm: eval.ArmQompack, Trial: 1, Completed: true, PluginExpected: true, PluginLoaded: true,
		HostReportedPlugins: true, PreregisteredModel: true,
		TranscriptHookMS: map[string][]int64{"SessionStart:compact": {1200, 300}},
	}
	md := renderLiveSummary(eval.LivePlan{RunID: "r"}, eval.SummarizeLive("r", a, []eval.LiveTrial{q}))
	require.Contains(t, md, "## Hook latency, as the host measured it")
	require.Contains(t, md, "| qompack | SessionStart:compact | 2 | 300 | 1200 | 1200 |")
}

// TestRunLiveEval_SessionsRunAtThePluginsDefaults: the host, and through it every Qompack hook and
// the trial daemon, inherits the driver's environment. A QOMPACK_* variable the operator happened
// to have set (a kill switch, a runtime mode, a state path) would silently change the plugin under
// test on the qompack arm only, so the arms would differ by more than the plugin. Inherited
// QOMPACK_* variables are removed from every session, the driver's own settings are kept, and the
// trial records which names were removed — never their values.
func TestRunLiveEval_SessionsRunAtThePluginsDefaults(t *testing.T) {
	t.Setenv("QOMPACK_RUNTIME__MODE", "off")
	t.Setenv("QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS", "999999")

	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	scripted := scriptedPilotHost(t, home)
	var spec liveProcSpec
	env.run = func(ctx context.Context, s liveProcSpec) liveProcResult {
		spec = s
		return scripted(ctx, s)
	}
	out := t.TempDir()
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"qompack"}, out: out,
		install: liveInstallPluginDir, bundle: fakeBundle(t), idleExit: 7, trials: 1, defectsAttested: true,
	}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())

	got := liveProcessEnv(os.Environ(), spec)
	var qompack []string
	for _, kv := range got {
		if strings.HasPrefix(strings.ToUpper(kv), "QOMPACK_") {
			qompack = append(qompack, kv)
		}
	}
	require.Equal(t, []string{"QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=7"}, qompack,
		"only the driver's own setting reaches the session")
	require.Contains(t, spec.Unset, "QOMPACK_RUNTIME__MODE")

	raw, err := os.ReadFile(filepath.Join(out, "trials", "pilot-codeword", "qompack", "01", "invocation.json"))
	require.NoError(t, err)
	require.Contains(t, string(raw), "QOMPACK_RUNTIME__MODE")
	require.NotContains(t, string(raw), "999999", "a removed variable's value is never recorded")
	var rec eval.LiveTrial
	readJSON(t, filepath.Join(out, "trials", "pilot-codeword", "qompack", "01", "trial.json"), &rec)
	require.Contains(t, strings.Join(rec.Notes, "\n"), "QOMPACK_RUNTIME__MODE")
}

// TestWriteJSONFile_ReplacesTheFileNeverRewritesIt: `qompack eval` may read a run's plan.json and
// summary.json while the run is finishing, or after it crashed mid-write. A file rewritten in place
// is visible half-written for as long as the write takes, and forever after a crash; one written
// beside it and renamed over it is seen whole or not at all. The hard link shows which happened: a
// rewrite in place changes the bytes behind the old name too, a rename leaves them.
func TestWriteJSONFile_ReplacesTheFileNeverRewritesIt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "summary.json")
	require.NoError(t, os.WriteFile(p, []byte("old\n"), 0o600))
	alias := filepath.Join(dir, "alias")
	require.NoError(t, os.Link(p, alias))

	require.NoError(t, writeJSONFile(p, map[string]int{"a": 1}))

	old, err := os.ReadFile(alias)
	require.NoError(t, err)
	require.Equal(t, "old\n", string(old), "the file was rewritten in place, so a reader could see a prefix of it")
	var got map[string]int
	readJSON(t, p, &got)
	require.Equal(t, map[string]int{"a": 1}, got)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	require.ElementsMatch(t, []string{"alias", "summary.json"}, names, "no staging file is left behind")
}

// TestLiveRunsPreregisteredModel: a trial is on the pre-registered model when it asked for the task
// set's own model, or — for a pre-registered task set — for the one alias section 3 lets a run use
// in its place. The pilot set has no pre-registration and so no contingency.
func TestLiveRunsPreregisteredModel(t *testing.T) {
	ts, _, err := eval.LoadLiveTaskSet(liveTestTasks)
	require.NoError(t, err)
	pre := eval.LivePreregistrations[ts.ID]
	require.True(t, liveRunsPreregisteredModel(ts, ts.Analysis.Model))
	require.True(t, liveRunsPreregisteredModel(ts, pre.ModelContingency))
	require.False(t, liveRunsPreregisteredModel(ts, "opus"))

	pilot, _, err := eval.LoadLiveTaskSet(liveTestPilot)
	require.NoError(t, err)
	require.True(t, liveRunsPreregisteredModel(pilot, pilot.Analysis.Model))
	require.False(t, liveRunsPreregisteredModel(pilot, pre.ModelContingency))
}
