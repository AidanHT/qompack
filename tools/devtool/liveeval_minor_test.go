package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// TestRunLiveEval_AnOverageStopIsRemembered: an overage stop leaves a marker in the run directory; a
// resume refuses until the operator says --overage-checked, which clears it (review M1).
func TestRunLiveEval_AnOverageStopIsRemembered(t *testing.T) {
	env, _, home := pacedEnv(t)
	env.run = perSession(t, home, func(n int, _ liveProcSpec) pilotHostOpts {
		if n == 1 {
			return pilotHostOpts{streamExtra: []string{rateLine("allowed", 0.1, 0.1, fakeNow.Add(time.Hour), fakeNow.Add(80*time.Hour), true)}}
		}
		return pilotHostOpts{}
	})
	o := stockRun(t, 1)
	var log bytes.Buffer
	require.ErrorContains(t, runLiveEval(context.Background(), o, env, &log), "overage")
	require.FileExists(t, filepath.Join(o.out, liveOverageStopFile))

	require.ErrorContains(t, runLiveEval(context.Background(), resumeRun(t, o.out), env, &log), "--overage-checked")
	require.NoFileExists(t, filepath.Join(slotDir(o.out, "stock", 1), "trial.json"))
	r := resumeRun(t, o.out)
	r.overageChecked = true
	require.NoError(t, runLiveEval(context.Background(), r, env, &log), log.String())
	require.FileExists(t, filepath.Join(slotDir(o.out, "stock", 1), "trial.json"))
	require.NoFileExists(t, filepath.Join(o.out, liveOverageStopFile), "--overage-checked clears the marker")
	_, err := parseLiveFlags([]string{"--resume", o.out, "--overage-checked"})
	require.NoError(t, err)
}

// TestMarkLiveLimited: a limited trial counts as a failure on recovery in the sensitivity analysis even
// when it was cut short before any recovery verdict existed (review M2).
func TestMarkLiveLimited(t *testing.T) {
	task := liveTask(t, liveTestPilot, "pilot-codeword")
	rec := eval.LiveTrial{Limited: "rejected", TaskSuccess: true}
	markLiveLimited(&rec, task)
	require.NotNil(t, rec.Recovered)
	require.False(t, *rec.Recovered)
	require.False(t, rec.TaskSuccess)
}

// TestRunLiveEval_NoTrialStartsTooCloseToTheWindowsEnd: a trial runs up to the session timeout, so none
// starts closer than that to the night window's end; and no wait longer than liveMaxWait is spent
// in-process, window or not (review M4).
func TestRunLiveEval_NoTrialStartsTooCloseToTheWindowsEnd(t *testing.T) {
	env, clk, home := pacedEnv(t)
	env.run = scriptedPilotHost(t, home)
	clk.t = time.Date(2026, 9, 23, 6, 30, 0, 0, time.UTC)
	night, err := parseNightWindow("22:00-07:00")
	require.NoError(t, err)
	o := stockRun(t, 1)
	o.night, o.sessionTimeout = night, 45*time.Minute
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	require.NoDirExists(t, slotDir(o.out, "stock", 1))
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Contains(t, sum.Paused, "closes")

	clk.t = fakeNow
	far := `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":%d,"rateLimitType":"monthly","isUsingOverage":false}}`
	env.run = scriptedPilotHostWith(t, home, pilotHostOpts{streamExtra: []string{fmt.Sprintf(far, fakeNow.Add(48*time.Hour).Unix())}})
	o = stockRun(t, 2)
	sum = eval.LiveSummary{}
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	require.Empty(t, clk.slept, "a two-day wait is a pause, not a sleep")
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Contains(t, sum.Paused, "monthly")
}

// TestRunLiveEval_UsageLimitResultEpochIsWaited: a usage-limit result that carries its reset epoch
// ("…|<epoch>") is waited out like a five-hour reset, then the slot runs again (review M5).
func TestRunLiveEval_UsageLimitResultEpochIsWaited(t *testing.T) {
	env, clk, home := pacedEnv(t)
	limit := fmt.Sprintf(`{"type":"result","subtype":"success","is_error":true,"num_turns":0,`+
		`"result":"Claude AI usage limit reached|%d","total_cost_usd":0.003,"usage":{},"modelUsage":{},"permission_denials":[]}`,
		fakeNow.Add(time.Hour).Unix())
	env.run = perSession(t, home, func(n int, _ liveProcSpec) pilotHostOpts {
		if n == 1 {
			return pilotHostOpts{streamTail: limit}
		}
		return pilotHostOpts{}
	})
	o := stockRun(t, 1)
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	require.Equal(t, []time.Duration{time.Hour + liveResetMargin}, clk.slept)
	require.FileExists(t, filepath.Join(slotDir(o.out, "stock", 1), "trial.limited-1.json"))
	require.FileExists(t, filepath.Join(slotDir(o.out, "stock", 1), "trial.json"))
	reset, ok := eval.LiveUsageLimitReset(mustStream(t, limit))
	require.True(t, ok)
	require.Equal(t, fakeNow.Add(time.Hour).Unix(), reset.Unix())
}

func mustStream(t *testing.T, lines ...string) eval.HostStream {
	t.Helper()
	var b bytes.Buffer
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	s, err := eval.ParseHostStream(&b, nil)
	require.NoError(t, err)
	return s
}

// TestRunLiveEval_ResumeRefusesChangedArmsOrderAndModsBundle: the checks that guard the union verdict
// beyond the hashes: the arm states, the trial order and the --bundle-mods identity (review M7).
func TestRunLiveEval_ResumeRefusesChangedArmsOrderAndModsBundle(t *testing.T) {
	env, _, home := pacedEnv(t)
	env.run = scriptedPilotHost(t, home)
	mods := fakeModsBundle(t, "deadbeef")
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"stock", "steer"}, out: t.TempDir(),
		install: liveInstallPluginDir, bundleMods: mods, idleExit: 1800, trials: 1, defectsAttested: true,
	}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	planPath := filepath.Join(o.out, "plan.json")
	orig, err := os.ReadFile(planPath)
	require.NoError(t, err)
	rewrite := func(f func(p *eval.LivePlan)) {
		var p eval.LivePlan
		require.NoError(t, json.Unmarshal(orig, &p))
		f(&p)
		b, err := json.Marshal(p)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(planPath, b, 0o600))
	}
	rewrite(func(p *eval.LivePlan) { p.ArmStates[1].ModSteering = false })
	require.ErrorContains(t, runLiveEval(context.Background(), resumeRun(t, o.out), env, &log), "arm state")
	rewrite(func(p *eval.LivePlan) { p.Trials[0], p.Trials[1] = p.Trials[1], p.Trials[0] })
	require.ErrorContains(t, runLiveEval(context.Background(), resumeRun(t, o.out), env, &log), "trial order")
	require.NoError(t, os.WriteFile(planPath, orig, 0o600))

	idPath := filepath.Join(mods, identityFileName)
	id, err := os.ReadFile(idPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(idPath, append(id, '\n'), 0o600))
	require.ErrorContains(t, runLiveEval(context.Background(), resumeRun(t, o.out), env, &log), "--bundle-mods")
}

// TestRunLiveEval_OneSessionPerRunDirectory: a lock held by a live process refuses a second session
// of the same run directory; a lock left by a process that no longer exists is taken over (review M8).
func TestRunLiveEval_OneSessionPerRunDirectory(t *testing.T) {
	env, _, home := pacedEnv(t)
	env.run = scriptedPilotHost(t, home)
	o := stockRun(t, 2)
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log))
	require.NoFileExists(t, filepath.Join(o.out, liveLockFile), "the lock goes with the session")
	// Re-open one slot so a resume has work to do.
	require.NoError(t, os.Remove(filepath.Join(slotDir(o.out, "stock", 2), "trial.json")))

	write := func(pid int) {
		b, err := json.Marshal(liveLock{PID: pid, StartedAt: "x"})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(o.out, liveLockFile), b, 0o600))
	}
	write(os.Getpid())
	require.ErrorContains(t, runLiveEval(context.Background(), resumeRun(t, o.out), env, &log), "another session")
	require.NoFileExists(t, filepath.Join(slotDir(o.out, "stock", 2), "trial.json"))
	write(2147483000)
	require.NoError(t, runLiveEval(context.Background(), resumeRun(t, o.out), env, &log), "a dead holder's lock is taken over\n"+log.String())
	require.FileExists(t, filepath.Join(slotDir(o.out, "stock", 2), "trial.json"))
}

// TestRunLiveEval_FreshStartTakesTheLockBeforeThePlan: a fresh start whose --out a live session holds
// is refused before it writes plan.json, so it cannot overwrite the running run's plan (re-review M8).
func TestRunLiveEval_FreshStartTakesTheLockBeforeThePlan(t *testing.T) {
	env, _, home := pacedEnv(t)
	env.run = scriptedPilotHost(t, home)
	o := stockRun(t, 1)
	b, err := json.Marshal(liveLock{PID: os.Getpid(), StartedAt: "x"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(o.out, liveLockFile), b, 0o600))
	var log bytes.Buffer
	require.ErrorContains(t, runLiveEval(context.Background(), o, env, &log), "another session")
	require.NoFileExists(t, filepath.Join(o.out, "plan.json"))
}

// TestRunLiveEval_DriverStopIsTheOverageStop: the run stops on the driver's own record that it
// stopped the session for overage, even when the cut stream does not parse, and the overage marker is
// written even when the guard also failed (re-review N1).
func TestRunLiveEval_DriverStopIsTheOverageStop(t *testing.T) {
	over := rateLine("allowed", 0.1, 0.1, fakeNow.Add(time.Hour), fakeNow.Add(80*time.Hour), true)

	env, _, home := pacedEnv(t)
	env.run = scriptedPilotHostWith(t, home, pilotHostOpts{streamExtra: []string{"{not json", over}})
	o := stockRun(t, 2)
	var log bytes.Buffer
	require.ErrorContains(t, runLiveEval(context.Background(), o, env, &log), "overage")
	require.FileExists(t, filepath.Join(o.out, liveOverageStopFile))
	require.NoDirExists(t, slotDir(o.out, "stock", 2))

	env2, _, home2 := pacedEnv(t)
	inner := scriptedPilotHostWith(t, home2, pilotHostOpts{streamExtra: []string{over}})
	env2.run = func(ctx context.Context, spec liveProcSpec) liveProcResult {
		require.NoError(t, os.MkdirAll(filepath.Join(home2, "plugins"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(home2, "plugins", "installed_plugins.json"), []byte(`{}`), 0o600))
		return inner(ctx, spec)
	}
	o2 := stockRun(t, 2)
	err := runLiveEval(context.Background(), o2, env2, &log)
	require.ErrorContains(t, err, "configuration changed")
	require.FileExists(t, filepath.Join(o2.out, liveOverageStopFile), "the guard failing does not skip the marker")
}

// TestRunLiveEval_ResumeNeedsStoredPacing: a resume whose pacing.json holds no stored settings is
// refused rather than run with no night window, unless the operator names one (re-review N2).
func TestRunLiveEval_ResumeNeedsStoredPacing(t *testing.T) {
	env, _, home := pacedEnv(t)
	env.run = scriptedPilotHost(t, home)
	o := stockRun(t, 2)
	killAtVersionCall(env, 3)
	runKilled(t, o, env)
	env.cli = pinnedCLI(fakeLiveEnv(t, nil).cli)
	require.NoError(t, os.Remove(filepath.Join(o.out, livePacingFile)))
	var log bytes.Buffer
	require.ErrorContains(t, runLiveEval(context.Background(), resumeRun(t, o.out), env, &log), "--night-window")
	require.NoDirExists(t, slotDir(o.out, "stock", 2))

	r := resumeRun(t, o.out)
	r.pacingGiven = map[string]bool{"night-window": true}
	require.NoError(t, runLiveEval(context.Background(), r, env, &log), log.String())
	require.FileExists(t, filepath.Join(slotDir(o.out, "stock", 2), "trial.json"))
}

// TestLiveCheck_StoredPacingDoesNotSilentlyReplaceFlags: a live-check whose --out already stores other
// pacing values refuses the flags it would replace unless --pacing-override is given (re-review N3).
func TestLiveCheck_StoredPacingDoesNotSilentlyReplaceFlags(t *testing.T) {
	env, _, _ := pacedEnv(t)
	var specs []liveProcSpec
	env.run = recordedHost(t, &specs, nil)
	o := checkRun(t, "quick")
	st := livePacingState{Settings: &livePacingSettings{FiveHourGate: 0.9, SevenDayCeiling: 0.95, NightWindow: "none"}}
	b, err := json.Marshal(st)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(o.out, livePacingFile), b, 0o600))
	o.pacingGiven = map[string]bool{"seven-day-ceiling": true}
	var log bytes.Buffer
	require.ErrorContains(t, runLiveCheck(context.Background(), o, env, &log), "--pacing-override")
	require.Empty(t, specs)
	o.pacingOverride = true
	require.NoError(t, runLiveCheck(context.Background(), o, env, &log), log.String())
	require.Contains(t, log.String(), "replaces the stored")
	p, err := parseLiveCheckFlags([]string{"--suite", "s.json", "--night-window", "none", "--seven-day-ceiling", "0.7", "--pacing-override"})
	require.NoError(t, err)
	require.True(t, p.pacingOverride)
	require.True(t, p.pacingGiven["seven-day-ceiling"])
}
