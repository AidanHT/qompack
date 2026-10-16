package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// errKilled is what the fake host's --version panics with to kill a run between two trials, the
// state a crash, a reboot or a closed lid leaves: plan.json and the finished slots, no summary.
var errKilled = fmt.Errorf("killed")

// killAtVersionCall makes the run die on the n-th `claude --version` (the plan reads it once, then
// every trial attempt checks it first).
func killAtVersionCall(env *liveEnv, n int) {
	calls := 0
	inner := env.cli
	env.cli = func(ctx context.Context, dir string, args ...string) liveCLIResult {
		if len(args) > 0 && args[0] == "--version" {
			calls++
			if calls == n {
				panic(errKilled)
			}
		}
		return inner(ctx, dir, args...)
	}
}

// runKilled runs o and requires the run to die of errKilled.
func runKilled(t *testing.T, o liveOptions, env *liveEnv) {
	t.Helper()
	defer func() {
		require.Equal(t, errKilled, recover(), "the run was killed")
	}()
	var log bytes.Buffer
	_ = runLiveEval(context.Background(), o, env, &log)
	t.Fatal("the run was not killed")
}

// armOutcomes is the scripted pilot host with a fixed outcome per arm: stock writes a wrong code word
// (a failed task), every Qompack arm the right one, so two runs of one plan grade identically.
func armOutcomes(t *testing.T, home string) func(context.Context, liveProcSpec) liveProcResult {
	t.Helper()
	return perSession(t, home, func(_ int, spec liveProcSpec) pilotHostOpts {
		return pilotHostOpts{wrongCode: !slices.Contains(spec.Args, "--plugin-dir")}
	})
}

func resumeRun(t *testing.T, out string) liveOptions {
	t.Helper()
	return liveOptions{resume: out, rates: liveTestRates, fiveHourGate: eval.LiveFiveHourGate, sevenDayCeiling: eval.LiveSevenDayCeiling}
}

// TestRunLiveEval_ResumeAfterAKillEqualsAnUninterruptedRun: a run killed after two of its four
// trials, with a half-written attempt left in the next slot, resumes from plan.json: it skips the
// two slots that hold a trial.json, runs the other two, and its summary over the union of both
// sessions' trials equals an uninterrupted run's on the same recorded outcomes (W6 item 13).
func TestRunLiveEval_ResumeAfterAKillEqualsAnUninterruptedRun(t *testing.T) {
	env, _, home := pacedEnv(t)
	env.run = armOutcomes(t, home)
	bundle := fakeBundle(t)
	plan := func(out string) liveOptions {
		return liveOptions{
			tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"stock", "classic"}, out: out,
			install: liveInstallPluginDir, bundle: bundle, idleExit: 1800, trials: 2, defectsAttested: true, orderSeed: 7,
		}
	}
	var log bytes.Buffer
	whole := plan(t.TempDir())
	require.NoError(t, runLiveEval(context.Background(), whole, env, &log), log.String())

	cut := plan(t.TempDir())
	killAtVersionCall(env, 4) // the plan's reading, then trials 1 and 2; dies before trial 3
	sessions := 0
	inner := env.run
	env.run = func(ctx context.Context, spec liveProcSpec) liveProcResult { sessions++; return inner(ctx, spec) }
	runKilled(t, cut, env)
	require.Equal(t, 2, sessions)
	require.NoFileExists(t, filepath.Join(cut.out, "summary.json"))
	var p eval.LivePlan
	readJSON(t, filepath.Join(cut.out, "plan.json"), &p)
	third := p.Trials[2]
	thirdSlot := filepath.Join(cut.out, "trials", third.Task, third.Arm, fmt.Sprintf("%02d", third.Trial))
	require.NoError(t, os.MkdirAll(thirdSlot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(thirdSlot, "stream.jsonl"), []byte(`{"type":"system","subtype":"in`), 0o600))

	env.cli = pinnedCLI(fakeLiveEnv(t, nil).cli)
	sessions = 0
	require.NoError(t, runLiveEval(context.Background(), resumeRun(t, cut.out), env, &log), log.String())
	require.Equal(t, 2, sessions, "only the two unfinished slots run again")
	require.Contains(t, log.String(), "2 of 4 planned trial(s) already scored")

	var a, b eval.LiveSummary
	readJSON(t, filepath.Join(whole.out, "summary.json"), &a)
	readJSON(t, filepath.Join(cut.out, "summary.json"), &b)
	require.NotEqual(t, a.RunID, b.RunID)
	b.RunID = a.RunID
	require.Equal(t, a, b, "the resumed run's summary is the uninterrupted run's")
	require.Equal(t, 0, a.Arms["stock"].TaskSuccess.K)
	require.Equal(t, 2, a.Arms["classic"].TaskSuccess.K)
	require.Equal(t, 2, a.Arms["classic"].Trials)
}

// TestRunLiveEval_ResumeAfterALimitedTrial: a run that set a limited trial aside and paused at the
// seven-day ceiling resumes after the reset: the limited slot runs again, its limited record stays
// on disk under its other name, and the summary over the union counts it as limited.
func TestRunLiveEval_ResumeAfterALimitedTrial(t *testing.T) {
	env, clk, home := pacedEnv(t)
	week := fakeNow.Add(30 * time.Hour)
	env.run = perSession(t, home, func(n int, _ liveProcSpec) pilotHostOpts {
		if n == 1 {
			return pilotHostOpts{streamExtra: []string{rateLine("rejected", 0.4, 0.65, fakeNow.Add(time.Hour), week, false)}}
		}
		return pilotHostOpts{}
	})
	o := stockRun(t, 2)
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	slot := slotDir(o.out, "stock", 1)
	require.FileExists(t, filepath.Join(slot, "trial.limited-1.json"))
	require.NoFileExists(t, filepath.Join(slot, "trial.json"))
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Contains(t, sum.Paused, "seven_day")

	clk.t = week.Add(time.Minute)
	require.NoError(t, runLiveEval(context.Background(), resumeRun(t, o.out), env, &log), log.String())
	require.FileExists(t, filepath.Join(slot, "trial.json"), "the limited slot ran again")
	require.FileExists(t, filepath.Join(slot, "trial.limited-1.json"), "its limited record stays")
	require.FileExists(t, filepath.Join(slotDir(o.out, "stock", 2), "trial.json"))
	sum = eval.LiveSummary{}
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Empty(t, sum.Paused)
	require.Equal(t, 2, sum.Arms["stock"].Trials)
	require.Equal(t, map[string]int{"stock": 1}, sum.LimitedTrials)
	require.NotContains(t, sum.Decision.Reason, "paused", "the resumed run is decided over the union")
}

// TestRunLiveEval_ResumeAfterThreeConfigurationErrors: a run stopped on its third configuration
// error resumes: the slot runs again in the new session, and its three discarded records stay.
func TestRunLiveEval_ResumeAfterThreeConfigurationErrors(t *testing.T) {
	env, _, home := pacedEnv(t)
	env.run = sessionFault(t, home, 3, pilotHostOpts{source: "qompack@someone-else"})
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"classic"}, out: t.TempDir(),
		install: liveInstallPluginDir, bundle: fakeBundle(t), idleExit: 1800, trials: 1, defectsAttested: true,
	}
	var log bytes.Buffer
	require.ErrorContains(t, runLiveEval(context.Background(), o, env, &log), "three configuration errors")
	slot := slotDir(o.out, "classic", 1)
	require.NoFileExists(t, filepath.Join(slot, "trial.json"))

	require.NoError(t, runLiveEval(context.Background(), resumeRun(t, o.out), env, &log), log.String())
	require.FileExists(t, filepath.Join(slot, "trial.json"))
	for n := 1; n <= 3; n++ {
		require.FileExists(t, filepath.Join(slot, fmt.Sprintf("trial.discarded-%d.json", n)))
	}
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Equal(t, 1, sum.Arms["classic"].Trials)
	require.NotContains(t, sum.Decision.Reason, "stopped")
}

// TestRunLiveEval_ResumeRefusesAChangedRun: --resume re-reads plan.json and refuses to continue when
// the task set, the fixture tree, a bundle or the host version no longer match it, and refuses the
// flags that would re-plan the run.
func TestRunLiveEval_ResumeRefusesAChangedRun(t *testing.T) {
	env, _, home := pacedEnv(t)
	env.run = scriptedPilotHost(t, home)
	bundle := fakeBundle(t)
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"classic"}, out: t.TempDir(),
		install: liveInstallPluginDir, bundle: bundle, idleExit: 1800, trials: 1, defectsAttested: true,
	}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log))
	planPath := filepath.Join(o.out, "plan.json")
	orig, err := os.ReadFile(planPath)
	require.NoError(t, err)
	tamper := func(field, value string) {
		var m map[string]any
		require.NoError(t, json.Unmarshal(orig, &m))
		m[field] = value
		b, err := json.Marshal(m)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(planPath, b, 0o600))
	}
	for field, want := range map[string]string{
		"task_set_sha256":     "task set",
		"fixture_tree_sha256": "fixture tree",
		"claude_cli_version":  "host",
		"rate_table_date":     "rate table",
	} {
		tamper(field, "0000")
		err := runLiveEval(context.Background(), resumeRun(t, o.out), env, &log)
		require.ErrorContains(t, err, want, field)
	}
	require.NoError(t, os.WriteFile(planPath, orig, 0o600))

	idPath := filepath.Join(bundle, identityFileName)
	id, err := os.ReadFile(idPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(idPath, append(id, '\n'), 0o600))
	require.ErrorContains(t, runLiveEval(context.Background(), resumeRun(t, o.out), env, &log), "bundle")
	require.NoError(t, os.WriteFile(idPath, id, 0o600))

	require.ErrorContains(t, runLiveEval(context.Background(), resumeRun(t, t.TempDir()), env, &log), "plan.json")

	for _, conflict := range [][]string{
		{"--arms", "stock"}, {"--tasks", "x.json"}, {"--trials", "3"}, {"--model", "m"}, {"--bundle", "b"},
		{"--order-seed", "3"}, {"--out", "o"}, {"--known-open-defects", "none"},
	} {
		_, err := parseLiveFlags(append([]string{"--resume", o.out}, conflict...))
		require.ErrorContains(t, err, "--resume", conflict)
	}
	r, err := parseLiveFlags([]string{"--resume", o.out, "--night-window", "22:00-07:00", "--seven-day-ceiling", "0.5", "--claude", "c"})
	require.NoError(t, err, "pacing and the host path may change between sessions")
	require.Equal(t, o.out, r.resume)
}
