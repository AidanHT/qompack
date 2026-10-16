package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// TestLiveSignals: live-eval and live-check stop on an interrupt and on SIGTERM (re-review N5).
func TestLiveSignals(t *testing.T) {
	require.Contains(t, liveSignalList(), os.Interrupt)
	require.Contains(t, liveSignalList(), os.Signal(syscall.SIGTERM))
}

// TestRunLiveEval_InterruptStopsWithoutScoring: an interrupt during a trial stops the run: the cut
// trial is neither scored nor set aside, so --resume runs its slot again, and no further trial
// starts (re-review N5).
func TestRunLiveEval_InterruptStopsWithoutScoring(t *testing.T) {
	env, _, home := pacedEnv(t)
	scripted := scriptedPilotHost(t, home)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sessions := 0
	env.run = func(c context.Context, spec liveProcSpec) liveProcResult {
		sessions++
		res := scripted(c, spec)
		cancel() // the signal arrives during the session
		res.Err = "the session bound expired"
		return res
	}
	o := stockRun(t, 2)
	var log bytes.Buffer
	err := runLiveEval(ctx, o, env, &log)
	require.ErrorContains(t, err, "interrupted")
	require.Equal(t, 1, sessions)
	slot := slotDir(o.out, "stock", 1)
	require.NoFileExists(t, filepath.Join(slot, "trial.json"))
	// The cut attempt and its files are set aside as interrupted, so a re-run neither overwrites nor
	// reuses them (re-review F6), and it was not graded (F7).
	var cut eval.LiveTrial
	readJSON(t, filepath.Join(slot, "trial.interrupted-1.json"), &cut)
	require.Empty(t, cut.Checks, "an interrupted attempt is not graded")
	require.FileExists(t, filepath.Join(slot, "stream.interrupted-1.jsonl"))
	require.NoFileExists(t, filepath.Join(slot, "stream.jsonl"))
	require.NotContains(t, err.Error(), "tree was killed", "the stop claims no kill it cannot vouch for (F8)")
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Contains(t, strings.Join(sum.Notes, "\n"), "interrupted")
	require.NoFileExists(t, filepath.Join(o.out, liveLockFile))
}

// TestLiveCheck_InterruptStopsWithoutGrading: the same for live-check: the interrupted run is not
// graded and no further run starts (re-review N5).
func TestLiveCheck_InterruptStopsWithoutGrading(t *testing.T) {
	env, _, _ := pacedEnv(t)
	var specs []liveProcSpec
	host := recordedHost(t, &specs, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env.run = func(c context.Context, spec liveProcSpec) liveProcResult {
		res := host(c, spec)
		cancel()
		res.Err = "the session bound expired"
		return res
	}
	o := checkRun(t, "quick")
	var log bytes.Buffer
	require.ErrorContains(t, runLiveCheck(ctx, o, env, &log), "interrupted")
	require.Len(t, specs, 1)
	require.NoFileExists(t, filepath.Join(o.out, "checks", "compact-recall", "01", "run.json"))
	require.FileExists(t, filepath.Join(o.out, "checks", "compact-recall", "01", "run.interrupted-1.json"), "set aside (F6)")
	require.FileExists(t, filepath.Join(o.out, "checks", "compact-recall", "01", "stream.interrupted-1.jsonl"))
	_, r := readCheckRun(t, o.out)
	require.Contains(t, r.Stopped, "interrupted")
	require.Empty(t, r.Checks[0].Runs)
}

// TestLiveCheck_PacingLinesSayLiveCheck: live-check prints the pacing values in force, and its pacing
// errors name live-check (re-review N9).
func TestLiveCheck_PacingLinesSayLiveCheck(t *testing.T) {
	env, _, _ := pacedEnv(t)
	var specs []liveProcSpec
	env.run = recordedHost(t, &specs, nil)
	o := checkRun(t, "quick")
	var log bytes.Buffer
	require.NoError(t, runLiveCheck(context.Background(), o, env, &log), log.String())
	require.Contains(t, log.String(), "live-check: pacing: five-hour gate 0.90, seven-day ceiling 0.90, night window none")

	o2 := checkRun(t, "quick")
	st := livePacingState{Settings: &livePacingSettings{FiveHourGate: 0.9, SevenDayCeiling: 0.95, NightWindow: "none"}}
	b, err := json.Marshal(st)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(o2.out, livePacingFile), b, 0o600))
	o2.pacingGiven = map[string]bool{"seven-day-ceiling": true}
	err = runLiveCheck(context.Background(), o2, env, &log)
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "live-check:"), err.Error())
}

// TestRunLiveEval_InterruptKeepsTheRateEvidence: an interrupt after a session that reported paid
// overage still writes overage-stop.json, and the cut attempt's events still reach pacing.json, so a
// resumed session neither skips --overage-checked nor paces on a stale reading (re-review F1).
func TestRunLiveEval_InterruptKeepsTheRateEvidence(t *testing.T) {
	for _, overage := range []bool{true, false} {
		env, _, home := pacedEnv(t)
		line := rateLine("allowed", 0.97, 0.1, fakeNow.Add(time.Hour), fakeNow.Add(80*time.Hour), overage)
		scripted := scriptedPilotHostWith(t, home, pilotHostOpts{streamExtra: []string{line}})
		ctx, cancel := context.WithCancel(context.Background())
		env.run = func(c context.Context, spec liveProcSpec) liveProcResult {
			res := scripted(c, spec)
			cancel()
			return res
		}
		checks := 0
		env.check = func(string, []string) eval.CommandOutcome { checks++; return eval.CommandOutcome{} }
		o := stockRun(t, 2)
		var log bytes.Buffer
		err := runLiveEval(ctx, o, env, &log)
		cancel()
		require.ErrorContains(t, err, "interrupted")
		var st livePacingState
		readJSON(t, filepath.Join(o.out, livePacingFile), &st)
		require.InDelta(t, 0.97, *st.Snapshot.Windows["five_hour"].Utilization, 1e-9, "overage=%t", overage)
		if overage {
			require.FileExists(t, filepath.Join(o.out, liveOverageStopFile))
			require.ErrorContains(t, err, "overage")
		} else {
			require.NoFileExists(t, filepath.Join(o.out, liveOverageStopFile))
		}
		require.Zero(t, checks, "the cut attempt's check commands are not run (F7)")
	}
}

// TestLiveCheck_InterruptKeepsTheRateEvidence: the same for live-check: the cut run's events reach
// pacing.json and an overage it reported is named in the stop (re-review F1).
func TestLiveCheck_InterruptKeepsTheRateEvidence(t *testing.T) {
	env, _, _ := pacedEnv(t)
	var specs []liveProcSpec
	host := recordedHost(t, &specs, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env.run = func(c context.Context, spec liveProcSpec) liveProcResult {
		res := host(c, spec)
		cancel()
		return res
	}
	o := checkRun(t, "quick")
	var log bytes.Buffer
	require.ErrorContains(t, runLiveCheck(ctx, o, env, &log), "interrupted")
	var st livePacingState
	readJSON(t, filepath.Join(o.out, livePacingFile), &st)
	require.InDelta(t, 0.68, *st.Snapshot.Windows["seven_day"].Utilization, 1e-9, "the recorded stream's reading is kept")
}

// TestRunLiveEval_InterruptMessages: an interrupt during the --version check or a pacing wait stops
// the run as interrupted, not as a host-version mismatch or a failed wait (re-review F8).
func TestRunLiveEval_InterruptMessages(t *testing.T) {
	env, _, home := pacedEnv(t)
	env.run = scriptedPilotHost(t, home)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	inner := env.cli
	env.cli = func(c context.Context, dir string, args ...string) liveCLIResult {
		if len(args) > 0 && args[0] == "--version" {
			calls++
			if calls == 2 { // the check before the first trial
				cancel()
				return liveCLIResult{Code: -1}
			}
		}
		return inner(c, dir, args...)
	}
	var log bytes.Buffer
	err := runLiveEval(ctx, stockRun(t, 1), env, &log)
	require.ErrorContains(t, err, "interrupted")
	require.NotContains(t, err.Error(), "reports Claude Code")

	env2, clk, home2 := pacedEnv(t)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	env2.run = scriptedPilotHostWith(t, home2, pilotHostOpts{
		streamExtra: []string{rateLine("allowed", 0.95, 0.1, fakeNow.Add(time.Hour), fakeNow.Add(80*time.Hour), false)},
	})
	env2.sleep = func(c context.Context, d time.Duration) error {
		_ = clk.sleep(c, d)
		cancel2()
		return c.Err()
	}
	err = runLiveEval(ctx2, stockRun(t, 2), env2, &log)
	require.ErrorContains(t, err, "interrupted")
	require.NotContains(t, err.Error(), "waiting for the rate-limit reset")
}

// TestRunLiveEval_InterruptDuringGrading: an interrupt that lands while the attempt is graded sets it
// aside as interrupted, graded but never scored, and the stop says so rather than "not graded"
// (re-review 4, G6).
func TestRunLiveEval_InterruptDuringGrading(t *testing.T) {
	env, _, home := pacedEnv(t)
	env.run = scriptedPilotHost(t, home)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env.check = func(string, []string) eval.CommandOutcome { cancel(); return eval.CommandOutcome{} }
	o := stockRun(t, 2)
	var log bytes.Buffer
	err := runLiveEval(ctx, o, env, &log)
	require.ErrorContains(t, err, "interrupted")
	require.NotContains(t, err.Error(), "not graded")
	require.ErrorContains(t, err, "graded")
	var cut eval.LiveTrial
	readJSON(t, filepath.Join(slotDir(o.out, "stock", 1), "trial.interrupted-1.json"), &cut)
	require.NotEmpty(t, cut.Checks)
	require.NoFileExists(t, filepath.Join(slotDir(o.out, "stock", 1), "trial.json"))
}

// TestLiveCutNote: the stop's note on the cut attempt says what really became of it (re-review 4, G6).
func TestLiveCutNote(t *testing.T) {
	require.Contains(t, liveCutNote(false, false), "not graded")
	require.Contains(t, liveCutNote(false, true), "graded before the interrupt")
	require.NotContains(t, liveCutNote(false, true), "not graded")
	require.Contains(t, liveCutNote(true, true), "scored before the interrupt")
	require.NotContains(t, liveCutNote(true, true), "set aside")
}

// TestPrePlanVersionReadUnderInterrupt: an interrupt during the version read before anything is
// planned says interrupted, not that the host version could not be read (re-review 4, G6).
func TestPrePlanVersionReadUnderInterrupt(t *testing.T) {
	env, _, _ := pacedEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env.cli = func(context.Context, string, ...string) liveCLIResult { cancel(); return liveCLIResult{Code: -1} }
	var log bytes.Buffer
	o := stockRun(t, 1)
	err := runLiveEval(ctx, o, env, &log)
	require.ErrorContains(t, err, "interrupted")
	require.NotContains(t, err.Error(), "could not read")
	require.NoFileExists(t, filepath.Join(o.out, "plan.json"))

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	env.cli = func(context.Context, string, ...string) liveCLIResult { cancel2(); return liveCLIResult{Code: -1} }
	err = runLiveCheck(ctx2, checkRun(t, "quick"), env, &log)
	require.ErrorContains(t, err, "interrupted")
	require.NotContains(t, err.Error(), "could not read")
}

// TestLiveCheck_InterruptedRunWritesNoRunJSON: a run whose project could not be made, interrupted, is
// set aside as interrupted rather than written as run.json (re-review 4, G4).
func TestLiveCheck_InterruptedRunWritesNoRunJSON(t *testing.T) {
	env, _, _ := pacedEnv(t)
	env.workRoot = filepath.Join(t.TempDir(), "missing", "dir") // MkdirTemp fails
	o := checkRun(t, "quick")
	suite, _, err := eval.LoadLiveCheckSuite(o.suite)
	require.NoError(t, err)
	r := &liveCheckRun{o: o, env: env, suite: suite, probes: map[string]string{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, gerr := r.runOnce(ctx, suite.Checks[0], 1)
	require.NoError(t, gerr)
	dir := filepath.Join(o.out, "checks", suite.Checks[0].ID, "01")
	require.NoFileExists(t, filepath.Join(dir, "run.json"))
	require.FileExists(t, filepath.Join(dir, "run.interrupted-1.json"))
}


// partialWalk is the harness error a session whose tree kill was only partial carries.
const partialWalk = "the process tree was only partly ended, so a descendant may still run: taskkill /T /F /PID 7: exit 255: " +
	"ERROR: The process with PID 8 (child process of PID 7) could not be terminated."

// TestRunLiveEval_FailedTreeKillReachesTheConsole: when the tree kill of an overage-stopped or
// interrupted session failed, the limited slot line, the STOPPING line and overage-stop.json say a
// host process may still be running (R53, H1).
func TestRunLiveEval_FailedTreeKillReachesTheConsole(t *testing.T) {
	env, _, home := pacedEnv(t)
	over := rateLine("allowed", 0.1, 0.1, fakeNow.Add(time.Hour), fakeNow.Add(80*time.Hour), true)
	scripted := scriptedPilotHostWith(t, home, pilotHostOpts{streamExtra: []string{over}})
	env.run = func(c context.Context, spec liveProcSpec) liveProcResult {
		res := scripted(c, spec)
		res.Err = joinErr(res.Err, partialWalk)
		return res
	}
	o := stockRun(t, 2)
	var log bytes.Buffer
	err := runLiveEval(context.Background(), o, env, &log)
	require.ErrorContains(t, err, "a host process may still be running")
	require.ErrorContains(t, err, "exit 255")
	require.Contains(t, log.String(), "limited: ")
	for _, l := range strings.Split(log.String(), "\n") {
		if strings.Contains(l, "limited: ") {
			require.Contains(t, l, "a host process may still be running", l)
		}
	}
	raw, rerr := os.ReadFile(filepath.Join(o.out, liveOverageStopFile))
	require.NoError(t, rerr)
	require.Contains(t, string(raw), "a host process may still be running")

	env2, _, home2 := pacedEnv(t)
	scripted2 := scriptedPilotHost(t, home2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env2.run = func(c context.Context, spec liveProcSpec) liveProcResult {
		res := scripted2(c, spec)
		cancel()
		res.Err = joinErr("the session bound expired", partialWalk)
		return res
	}
	err = runLiveEval(ctx, stockRun(t, 1), env2, &log)
	require.ErrorContains(t, err, "interrupted")
	require.ErrorContains(t, err, "a host process may still be running")
}

// TestJoinErr_NoTrailingSeparator: joining nothing adds no "; " (R53, N3).
func TestJoinErr_NoTrailingSeparator(t *testing.T) {
	require.Equal(t, "a", joinErr("a", ""))
	require.Equal(t, "b", joinErr("", "b"))
	require.Equal(t, "a; b", joinErr("a", "b"))
}
