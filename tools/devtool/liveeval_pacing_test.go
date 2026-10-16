package main

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// liveClock is a test clock a fake wait advances.
type liveClock struct {
	mu    sync.Mutex
	t     time.Time
	slept []time.Duration
}

func (c *liveClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *liveClock) sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slept = append(c.slept, d)
	c.t = c.t.Add(d)
	return nil
}

// fakeNow is fakeLiveEnv's fixed clock: 2026-09-22 12:00 UTC.
var fakeNow = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

// rateLine is a rate_limit_event line shaped like the recorded 2.1.280 ones.
func rateLine(status string, fiveUtil, sevenUtil float64, fiveReset, sevenReset time.Time, overage bool) string {
	return fmt.Sprintf(`{"type":"rate_limit_event","rate_limit_info":{"status":%q,"resetsAt":%d,"rateLimitType":"five_hour",`+
		`"overageStatus":"rejected","overageDisabledReason":"org_level_disabled","isUsingOverage":%t,"unifiedWindows":{"five_hour":{"utilization":%g,"resetsAt":%d},`+
		`"seven_day":{"utilization":%g,"resetsAt":%d}}},"session_id":"s"}`,
		status, fiveReset.Unix(), overage, fiveUtil, fiveReset.Unix(), sevenUtil, sevenReset.Unix())
}

// perSession plays opts(n) on the n-th session (from 1).
func perSession(t *testing.T, home string, opts func(n int, spec liveProcSpec) pilotHostOpts) func(context.Context, liveProcSpec) liveProcResult {
	t.Helper()
	sessions := 0
	return func(ctx context.Context, spec liveProcSpec) liveProcResult {
		sessions++
		return scriptedPilotHostWith(t, home, opts(sessions, spec))(ctx, spec)
	}
}

func pacedEnv(t *testing.T) (*liveEnv, *liveClock, string) {
	t.Helper()
	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	clk := &liveClock{t: fakeNow}
	env.now, env.sleep = clk.now, clk.sleep
	t.Setenv(liveEvalGateEnv, "1")
	return env, clk, home
}

func stockRun(t *testing.T, trials int) liveOptions {
	t.Helper()
	return liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"stock"}, out: t.TempDir(),
		idleExit: 1800, trials: trials,
	}
}

func slotDir(out string, arm string, trial int) string {
	return filepath.Join(out, "trials", "pilot-codeword", arm, fmt.Sprintf("%02d", trial))
}

func TestParseLiveFlags_Pacing(t *testing.T) {
	o, err := parseLiveFlags(nil)
	require.NoError(t, err)
	require.InDelta(t, eval.LiveFiveHourGate, o.fiveHourGate, 0)
	require.InDelta(t, eval.LiveSevenDayCeiling, o.sevenDayCeiling, 0)
	require.False(t, o.night.set, "no night window unless the operator gives one")

	o, err = parseLiveFlags([]string{"--five-hour-gate", "0.8", "--seven-day-ceiling", "0.5", "--night-window", "22:00-07:00"})
	require.NoError(t, err)
	require.InDelta(t, 0.8, o.fiveHourGate, 0)
	require.InDelta(t, 0.5, o.sevenDayCeiling, 0)
	require.Equal(t, "22:00-07:00", o.night.String())
	for _, bad := range [][]string{
		{"--five-hour-gate", "0"}, {"--five-hour-gate", "1.2"}, {"--seven-day-ceiling", "-1"},
		{"--night-window", "22:00"}, {"--night-window", "25:00-07:00"}, {"--night-window", "07:00-07:00"},
	} {
		_, err := parseLiveFlags(bad)
		require.Error(t, err, bad)
	}
}

func TestLiveNightWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 9, 22, h, m, 0, 0, time.UTC) }
	n, err := parseNightWindow("22:00-07:00")
	require.NoError(t, err)
	require.True(t, n.contains(at(23, 0)))
	require.True(t, n.contains(at(3, 0)))
	require.False(t, n.contains(at(7, 0)), "the end is not inside")
	require.False(t, n.contains(at(12, 0)))
	require.Equal(t, at(7, 0).Add(24*time.Hour), n.end(at(23, 0)), "a window that crosses midnight ends tomorrow")
	require.Equal(t, at(7, 0), n.end(at(3, 0)))

	day, err := parseNightWindow("01:30-05:00")
	require.NoError(t, err)
	require.True(t, day.contains(at(1, 30)))
	require.False(t, day.contains(at(5, 0)))
	require.Equal(t, at(5, 0), day.end(at(2, 0)))

	var none liveNightWindow
	require.True(t, none.contains(at(12, 0)), "no window: always inside")
}

// TestRunLiveEval_LimitedTrialIsSetAsideAndRerun: a trial whose stream carries a rejected event is
// limited: kept on disk as trial.limited-1.json with its attempt files, excluded from the analysis,
// and its slot runs again from a fresh fixture after the five-hour window resets (W6 item 8).
func TestRunLiveEval_LimitedTrialIsSetAsideAndRerun(t *testing.T) {
	env, clk, home := pacedEnv(t)
	reset := fakeNow.Add(2 * time.Hour)
	week := fakeNow.Add(72 * time.Hour)
	env.run = perSession(t, home, func(n int, _ liveProcSpec) pilotHostOpts {
		if n == 1 {
			return pilotHostOpts{streamExtra: []string{rateLine("rejected", 1, 0.3, reset, week, false)}}
		}
		return pilotHostOpts{streamExtra: []string{rateLine("allowed", 0.05, 0.31, reset.Add(5*time.Hour), week, false)}}
	})
	o := stockRun(t, 1)
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())

	slot := slotDir(o.out, "stock", 1)
	var limited eval.LiveTrial
	readJSON(t, filepath.Join(slot, "trial.limited-1.json"), &limited)
	require.Contains(t, limited.Limited, "rejected the five_hour window")
	require.NotEmpty(t, limited.RateLimits)
	require.FileExists(t, filepath.Join(slot, "stream.limited-1.jsonl"))
	var rec eval.LiveTrial
	readJSON(t, filepath.Join(slot, "trial.json"), &rec)
	require.Empty(t, rec.Limited)
	require.True(t, rec.TaskSuccess)
	require.Len(t, rec.RateLimits, 1)
	require.Contains(t, rec.RateUse, "five_hour", "each trial records its utilization deltas")

	require.Equal(t, []time.Duration{2*time.Hour + liveResetMargin}, clk.slept, "the run waited for the five-hour reset")
	require.Contains(t, log.String(), "limited")
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Equal(t, map[string]int{"stock": 1}, sum.LimitedTrials)
	require.Equal(t, 1, sum.Arms["stock"].Trials)
	require.Equal(t, 2, sum.LimitedSensitivity["stock"].Trials)
	require.Empty(t, sum.Paused)
	require.FileExists(t, filepath.Join(o.out, livePacingFile))
}

// TestRunLiveEval_LimitWithNoResetPauses: a trial a usage-limit result cut short, with no event to
// say when the limit lifts, is set aside and the run pauses cleanly rather than re-running the slot
// into the same limit.
func TestRunLiveEval_LimitWithNoResetPauses(t *testing.T) {
	env, _, home := pacedEnv(t)
	limitResult := `{"type":"result","subtype":"success","is_error":true,"num_turns":0,"result":"Claude AI usage limit reached|1790116800","total_cost_usd":0.003,"usage":{},"modelUsage":{},"permission_denials":[]}`
	env.run = perSession(t, home, func(int, liveProcSpec) pilotHostOpts { return pilotHostOpts{streamTail: limitResult} })
	o := stockRun(t, 2)
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), "a pause is a clean exit\n"+log.String())
	require.FileExists(t, filepath.Join(slotDir(o.out, "stock", 1), "trial.limited-1.json"))
	require.NoFileExists(t, filepath.Join(slotDir(o.out, "stock", 1), "trial.json"))
	require.NoDirExists(t, slotDir(o.out, "stock", 2))
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Contains(t, sum.Paused, "no event says when")
	require.Equal(t, "not-applicable", sum.Decision.Verdict)
	require.Contains(t, log.String(), "--resume")
}

// TestRunLiveEval_PausesAtTheSevenDayCeiling: the recorded seven-day utilization 0.68 is above the
// 0.60 ceiling, so after the trial that reported it the run pauses cleanly instead of starting the
// next one, and keeps the newest snapshot for the resumed session.
func TestRunLiveEval_PausesAtTheSevenDayCeiling(t *testing.T) {
	env, clk, home := pacedEnv(t)
	week := fakeNow.Add(50 * time.Hour)
	env.run = perSession(t, home, func(int, liveProcSpec) pilotHostOpts {
		return pilotHostOpts{streamExtra: []string{rateLine("allowed", 0.21, 0.68, fakeNow.Add(time.Hour), week, false)}}
	})
	o := stockRun(t, 2)
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	require.FileExists(t, filepath.Join(slotDir(o.out, "stock", 1), "trial.json"))
	require.NoDirExists(t, slotDir(o.out, "stock", 2), "no trial starts above the seven-day ceiling")
	require.Empty(t, clk.slept, "a seven-day reset is days away: a pause, not a wait")
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Contains(t, sum.Paused, "seven_day")
	require.Contains(t, sum.Decision.Reason, "paused after 1 of the 2 planned trials")
	var p livePacingState
	readJSON(t, filepath.Join(o.out, livePacingFile), &p)
	require.InDelta(t, 0.68, *p.Snapshot.Windows["seven_day"].Utilization, 1e-9)

	o2 := stockRun(t, 2)
	o2.sevenDayCeiling = 0.7
	env.run = perSession(t, home, func(int, liveProcSpec) pilotHostOpts {
		return pilotHostOpts{streamExtra: []string{rateLine("allowed", 0.21, 0.68, fakeNow.Add(time.Hour), week, false)}}
	})
	require.NoError(t, runLiveEval(context.Background(), o2, env, &log))
	require.FileExists(t, filepath.Join(slotDir(o2.out, "stock", 2), "trial.json"), "the ceiling is the operator's flag")
}

// TestRunLiveEval_OverageStopsTheRun: any event that says paid overage is in use stops the run with a
// message; the trial that reported it is cut short and set aside as limited, and no further trial starts.
func TestRunLiveEval_OverageStopsTheRun(t *testing.T) {
	env, _, home := pacedEnv(t)
	env.run = perSession(t, home, func(int, liveProcSpec) pilotHostOpts {
		return pilotHostOpts{streamExtra: []string{rateLine("allowed", 0.5, 0.2, fakeNow.Add(time.Hour), fakeNow.Add(80*time.Hour), true)}}
	})
	o := stockRun(t, 2)
	var log bytes.Buffer
	err := runLiveEval(context.Background(), o, env, &log)
	require.ErrorContains(t, err, "overage")
	require.FileExists(t, filepath.Join(slotDir(o.out, "stock", 1), "trial.limited-1.json"),
		"the session was stopped on the overage event, so the trial was cut short (review I1)")
	require.NoDirExists(t, slotDir(o.out, "stock", 2))
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Empty(t, sum.Paused, "an overage stop is a stop, not a pause")
	require.Contains(t, strings.Join(sum.Notes, "\n"), "stopped early")
}

// TestRunLiveEval_NightWindow: outside the night window no trial starts (a clean pause), and a
// five-hour wait that would end after the window closes pauses instead of waiting.
func TestRunLiveEval_NightWindow(t *testing.T) {
	env, clk, home := pacedEnv(t)
	env.run = scriptedPilotHost(t, home)
	night, err := parseNightWindow("22:00-07:00")
	require.NoError(t, err)
	o := stockRun(t, 1)
	o.night = night
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	require.FileExists(t, filepath.Join(o.out, "plan.json"))
	require.NoDirExists(t, slotDir(o.out, "stock", 1), "12:00 is outside 22:00-07:00")
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Contains(t, sum.Paused, "night window")

	clk.t = time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC) // inside, three hours before it closes
	env.run = perSession(t, home, func(int, liveProcSpec) pilotHostOpts {
		return pilotHostOpts{streamExtra: []string{rateLine("allowed", 0.95, 0.1, clk.now().Add(4*time.Hour), clk.now().Add(90*time.Hour), false)}}
	})
	o = stockRun(t, 2)
	o.night = night
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	require.FileExists(t, filepath.Join(slotDir(o.out, "stock", 1), "trial.json"))
	require.NoDirExists(t, slotDir(o.out, "stock", 2))
	require.Empty(t, clk.slept, "the five-hour reset at 08:00 is after the window closes at 07:00")
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Contains(t, sum.Paused, "five_hour")
	require.Contains(t, sum.Paused, "after the night window")
}

// TestRenderLiveSummary_ShowsSubscriptionUseAndLimited: summary.md reports each arm's per-trial
// utilization deltas, the limited trials with their sensitivity analysis, and a pause.
func TestRenderLiveSummary_ShowsSubscriptionUseAndLimited(t *testing.T) {
	a := eval.LiveAnalysis{Model: "m", Confidence: 0.95, NonInferiorityMargin: 0.1, TrialsPerArm: 2}
	d := func(v float64) eval.LiveRateDelta {
		return eval.LiveRateDelta{Delta: &v, Basis: eval.RateBasisPrevious}
	}
	ok := true
	ts := []eval.LiveTrial{
		{TaskID: "t", Arm: "steer", Trial: 1, Completed: true, TaskSuccess: true, Recovered: &ok, PreregisteredModel: true,
			RateUse: map[string]eval.LiveRateDelta{"five_hour": d(0.02), "seven_day": d(0.004)}},
		{TaskID: "t", Arm: "steer", Trial: 2, Completed: true, TaskSuccess: true, Recovered: &ok, PreregisteredModel: true,
			RateUse: map[string]eval.LiveRateDelta{"five_hour": d(0.04)}},
	}
	sum := eval.SummarizeLive("r", a, ts)
	sum.AddLimited(a, ts, []eval.LiveTrial{{TaskID: "t", Arm: "steer", Trial: 2, Limited: "rejected", Recovered: &ok}})
	sum.Pause(2, 4, "the seven_day window is at 0.62")
	md := renderLiveSummary(eval.LivePlan{RunID: "r"}, sum)
	for _, want := range []string{
		"## Subscription use per trial",
		"| steer | five_hour | 2 | 0.0300 | 0.0400 |",
		"| steer | seven_day | 1 | 0.0040 | 0.0040 |",
		"## Limited trials",
		"| steer | 1 | 2/3",
		"**Paused:** the seven_day window is at 0.62",
	} {
		require.Contains(t, md, want)
	}
}

// TestRunLiveEval_APanicLeavesNoScoredRecord: a panic inside a trial unwinds through its deferred
// record write without writing trial.json, so the slot stays open for --resume instead of holding an
// ungraded failure (review C1).
func TestRunLiveEval_APanicLeavesNoScoredRecord(t *testing.T) {
	env, _, home := pacedEnv(t)
	env.run = scriptedPilotHost(t, home)
	env.stopDaemon = func(string, string) (string, error) { panic("boom") }
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"classic"}, out: t.TempDir(),
		install: liveInstallPluginDir, bundle: fakeBundle(t), idleExit: 1800, trials: 1, defectsAttested: true,
	}
	func() {
		defer func() { require.Equal(t, "boom", recover()) }()
		var log bytes.Buffer
		_ = runLiveEval(context.Background(), o, env, &log)
	}()
	require.NoFileExists(t, filepath.Join(slotDir(o.out, "classic", 1), "trial.json"))
}

// TestRunLiveEval_OverageStopsTheSessionAtOnce: the session is stopped on the first event that says
// paid overage is in use, not at its end: the host is cancelled, the cut-short trial is set aside as
// limited, and the run stops with the overage message (review I1).
func TestRunLiveEval_OverageStopsTheSessionAtOnce(t *testing.T) {
	env, _, home := pacedEnv(t)
	var stopped []string
	inner := scriptedPilotHostWith(t, home, pilotHostOpts{
		streamExtra: []string{rateLine("allowed", 0.5, 0.2, fakeNow.Add(time.Hour), fakeNow.Add(80*time.Hour), true)},
	})
	env.run = func(ctx context.Context, spec liveProcSpec) liveProcResult {
		res := inner(ctx, spec)
		stopped = append(stopped, res.Stopped)
		return res
	}
	o := stockRun(t, 2)
	var log bytes.Buffer
	err := runLiveEval(context.Background(), o, env, &log)
	require.ErrorContains(t, err, "overage")
	require.Len(t, stopped, 1)
	require.Contains(t, stopped[0], "overage", "the scripted host honoured the driver's stop")
	slot := slotDir(o.out, "stock", 1)
	require.NoFileExists(t, filepath.Join(slot, "trial.json"))
	var lim eval.LiveTrial
	readJSON(t, filepath.Join(slot, "trial.limited-1.json"), &lim)
	require.Contains(t, lim.Limited, "overage")
	require.NoDirExists(t, slotDir(o.out, "stock", 2))
}

// TestRunLiveEval_UnknownRateLimitShapeStops: an event the parser does not recognise, or events that
// never carry an overage field, stop the run before the next trial, naming G0.16 (review I2).
func TestRunLiveEval_UnknownRateLimitShapeStops(t *testing.T) {
	for name, line := range map[string]string{
		"renamed info": strings.Replace(rateLine("allowed", 0.1, 0.1, fakeNow.Add(time.Hour), fakeNow.Add(80*time.Hour), false),
			"rate_limit_info", "rate_limit_details", 1),
		"no overage field": strings.Replace(rateLine("allowed", 0.1, 0.1, fakeNow.Add(time.Hour), fakeNow.Add(80*time.Hour), false),
			`"isUsingOverage":false,`, "", 1),
	} {
		env, _, home := pacedEnv(t)
		env.run = scriptedPilotHostWith(t, home, pilotHostOpts{streamExtra: []string{line}})
		o := stockRun(t, 2)
		var log bytes.Buffer
		err := runLiveEval(context.Background(), o, env, &log)
		require.ErrorContains(t, err, "G0.16", name)
		require.NoDirExists(t, slotDir(o.out, "stock", 2), name)
	}
}

// TestRunLiveEval_RecordsTheOverageState: each trial records the account's overage status and the
// reason it is disabled, and the summary names them (review I2).
func TestRunLiveEval_RecordsTheOverageState(t *testing.T) {
	env, _, home := pacedEnv(t)
	env.run = scriptedPilotHostWith(t, home, pilotHostOpts{
		streamExtra: []string{rateLine("allowed", 0.1, 0.1, fakeNow.Add(time.Hour), fakeNow.Add(80*time.Hour), false)},
	})
	o := stockRun(t, 1)
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	var rec eval.LiveTrial
	readJSON(t, filepath.Join(slotDir(o.out, "stock", 1), "trial.json"), &rec)
	require.Equal(t, "rejected", rec.OverageStatus)
	require.Equal(t, "org_level_disabled", rec.OverageDisabledReason)
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Contains(t, strings.Join(sum.Notes, "\n"), "org_level_disabled")
	require.Contains(t, log.String(), "overage rejected (org_level_disabled)")
}

// TestParseLiveFlags_NightWindowIsExplicit: a run that starts trials must name its night window, or
// say none; a dry run, a description and a resume (which reuses the stored window) need not (review I3).
func TestParseLiveFlags_NightWindowIsExplicit(t *testing.T) {
	_, err := parseLiveFlags([]string{"--arms", "stock"})
	require.ErrorContains(t, err, "--night-window")
	o, err := parseLiveFlags([]string{"--arms", "stock", "--night-window", "none"})
	require.NoError(t, err)
	require.False(t, o.night.set, "none is the deliberate opt-out")
	for _, ok := range [][]string{{"--dry-run"}, {"--describe"}, {"--resume", "dir"}} {
		_, err := parseLiveFlags(ok)
		require.NoError(t, err, ok)
	}
	_, err = parseLiveCheckFlags([]string{"--suite", "s.json"})
	require.ErrorContains(t, err, "--night-window")
	_, err = parseLiveCheckFlags([]string{"--suite", "s.json", "--dry-run"})
	require.NoError(t, err)
}

// TestRunLiveEval_ResumeReusesThePacing: the pacing values of a run's first session are kept in its
// directory; a resumed session with no pacing flag paces on them, and one that names a different value
// is refused unless it says --pacing-override (review I3).
func TestRunLiveEval_ResumeReusesThePacing(t *testing.T) {
	env, clk, home := pacedEnv(t)
	clk.t = time.Date(2026, 9, 22, 23, 0, 0, 0, time.UTC)
	env.run = scriptedPilotHostWith(t, home, pilotHostOpts{
		streamExtra: []string{rateLine("allowed", 0.1, 0.68, clk.t.Add(time.Hour), clk.t.Add(80*time.Hour), false)},
	})
	night, err := parseNightWindow("22:00-07:00")
	require.NoError(t, err)
	o := stockRun(t, 2)
	o.night, o.sevenDayCeiling = night, 0.7
	killAtVersionCall(env, 3) // dies after the first trial
	runKilled(t, o, env)
	var st livePacingState
	readJSON(t, filepath.Join(o.out, livePacingFile), &st)
	require.Equal(t, &livePacingSettings{FiveHourGate: 0.90, SevenDayCeiling: 0.7, NightWindow: "22:00-07:00"}, st.Settings)

	env.cli = pinnedCLI(fakeLiveEnv(t, nil).cli)
	r := resumeRun(t, o.out)
	r.sevenDayCeiling, r.pacingGiven = 0.5, map[string]bool{"seven-day-ceiling": true}
	var log bytes.Buffer
	require.ErrorContains(t, runLiveEval(context.Background(), r, env, &log), "--pacing-override")
	require.NoDirExists(t, slotDir(o.out, "stock", 2))

	require.NoError(t, runLiveEval(context.Background(), resumeRun(t, o.out), env, &log), log.String())
	require.FileExists(t, filepath.Join(slotDir(o.out, "stock", 2), "trial.json"),
		"the stored ceiling 0.7 lets the 0.68 week through; the default 0.60 would have paused")
	require.Contains(t, log.String(), "seven-day ceiling 0.70, night window 22:00-07:00")
}
