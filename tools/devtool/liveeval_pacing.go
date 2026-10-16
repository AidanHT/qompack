package main

// Subscription pacing for `devtool live-eval` and `devtool live-check` (V7 W6 item 8, owner decision
// D100): the night window, the pre-trial waits on the newest rate_limit_event, the clean pause that
// `--resume` continues, and the paid-overage stop. The decisions themselves are eval.PaceLive's;
// this file owns the clock, the waiting and the run directory's pacing.json.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/eval"
)

// liveResetMargin is how long after a window's reported reset a waiting run starts its next trial,
// so the host's own clock has passed the reset too.
const liveResetMargin = time.Minute

// livePacingFile holds, in the run directory, the newest rate-limit snapshot the run has seen, so a
// resumed session paces on it before its first trial.
const livePacingFile = "pacing.json"

// livePacingState is pacing.json.
type livePacingState struct {
	UpdatedAt string `json:"updated_at"`
	// Settings is the pacing in force, written when the run's first session starts: every later
	// session of the run paces on it unless the operator overrides it explicitly (review I3).
	Settings *livePacingSettings   `json:"settings,omitempty"`
	Snapshot eval.LiveRateSnapshot `json:"snapshot"`
}

// livePacingSettings is a run's pacing values.
type livePacingSettings struct {
	FiveHourGate    float64 `json:"five_hour_gate"`
	SevenDayCeiling float64 `json:"seven_day_ceiling"`
	// NightWindow is HH:MM-HH:MM, or "none".
	NightWindow string `json:"night_window"`
}

// liveNoNightWindow is the --night-window value that deliberately runs with no window.
const liveNoNightWindow = "none"

// liveNightWindow is the --night-window: trials start only inside it, in the clock's own time zone.
// The zero value has no window, and every time is inside it.
type liveNightWindow struct {
	set              bool
	startMin, endMin int // minutes after midnight; end may be earlier than start (the window crosses midnight)
}

// parseNightWindow reads "HH:MM-HH:MM", or "none" for no window.
func parseNightWindow(v string) (liveNightWindow, error) {
	if v == liveNoNightWindow {
		return liveNightWindow{}, nil
	}
	var sh, sm, eh, em int
	if _, err := fmt.Sscanf(v, "%d:%d-%d:%d", &sh, &sm, &eh, &em); err != nil || len(v) != len("00:00-00:00") {
		return liveNightWindow{}, fmt.Errorf("live-eval: --night-window %q is not HH:MM-HH:MM", v)
	}
	if sh > 23 || eh > 23 || sm > 59 || em > 59 || sh < 0 || eh < 0 || sm < 0 || em < 0 {
		return liveNightWindow{}, fmt.Errorf("live-eval: --night-window %q has no such time of day", v)
	}
	n := liveNightWindow{set: true, startMin: sh*60 + sm, endMin: eh*60 + em}
	if n.startMin == n.endMin {
		return liveNightWindow{}, fmt.Errorf("live-eval: --night-window %q is empty", v)
	}
	return n, nil
}

func (n liveNightWindow) String() string {
	if !n.set {
		return liveNoNightWindow
	}
	return fmt.Sprintf("%02d:%02d-%02d:%02d", n.startMin/60, n.startMin%60, n.endMin/60, n.endMin%60)
}

// contains reports that t is inside the window: from its start, up to but not including its end.
func (n liveNightWindow) contains(t time.Time) bool {
	if !n.set {
		return true
	}
	m := t.Hour()*60 + t.Minute()
	if n.startMin < n.endMin {
		return m >= n.startMin && m < n.endMin
	}
	return m >= n.startMin || m < n.endMin
}

// end is when the window occurrence containing t closes (t must be inside it).
func (n liveNightWindow) end(t time.Time) time.Time {
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	e := midnight.Add(time.Duration(n.endMin) * time.Minute)
	if !e.After(t) {
		e = e.AddDate(0, 0, 1)
	}
	return e
}

// livePacer paces one session of a run on the newest rate-limit snapshot.
type livePacer struct {
	pacing eval.LivePacing
	night  liveNightWindow
	snap   eval.LiveRateSnapshot
	// path is the run directory's pacing.json; "" keeps the snapshot in memory only.
	path string
	// sessionMargin is how long before the night window closes the last trial may start: a trial
	// runs up to the session timeout (review M4).
	sessionMargin time.Duration
	// tool names the command in the pacing's messages: live-eval or live-check (re-review N9).
	tool string
	// warnings are the stored values the flags replaced under --pacing-override, for the caller to print.
	warnings []string
	// holdUntil and holdWhy are a reset a usage-limit result announced (review M5).
	holdUntil time.Time
	holdWhy   string
}

// liveMaxWait is the longest wait spent in-process: a five-hour reset is at most five hours away, so
// anything longer (a reset days away) is a pause (review M4).
const liveMaxWait = 5*time.Hour + 10*time.Minute

// hold makes the pacing wait for until, as for a five-hour reset.
func (p *livePacer) hold(until time.Time, why string) {
	if until.After(p.holdUntil) {
		p.holdUntil, p.holdWhy = until, why
	}
}

// verdict is PaceLive's verdict, with a held reset in the future turned into a wait.
func (p *livePacer) verdict(now time.Time) eval.LivePaceVerdict {
	v := eval.PaceLive(p.snap, now, p.pacing)
	if v.Action == eval.PaceGo && p.holdUntil.After(now) {
		v = eval.LivePaceVerdict{Action: eval.PaceWait, Until: p.holdUntil, Reason: p.holdWhy}
	}
	return v
}

// wouldStart reports that the pacing would start a trial at now: nothing to wait for.
func (p *livePacer) wouldStart(now time.Time) bool { return p.verdict(now).Action == eval.PaceGo }

// newLivePacer makes the session's pacer, starting from the snapshot a previous session of the run
// left in dir (none for a new run).
//
// The pacing values come from the run directory when an earlier session stored them, and from the
// flags otherwise; a flag that names a different value than the stored one is refused unless
// o.pacingOverride says it is meant (review I3). The values in force are then stored.
func newLivePacer(o liveOptions, dir string, now time.Time) (*livePacer, error) {
	p := &livePacer{pacing: o.livePacing(), night: o.night, sessionMargin: o.sessionTimeout, tool: o.pacingTool}
	if p.tool == "" {
		p.tool = "live-eval"
	}
	if dir == "" {
		return p, nil
	}
	p.path = filepath.Join(dir, livePacingFile)
	var st livePacingState
	raw, err := os.ReadFile(p.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf(p.tool+": reading %s: %w", p.path, err)
	default:
		if err := json.Unmarshal(raw, &st); err != nil {
			return nil, fmt.Errorf(p.tool+": parsing %s: %w", p.path, err)
		}
	}
	p.snap = st.Snapshot
	if st.Settings == nil && o.resume != "" && !o.pacingGiven["night-window"] {
		// The first session died before it stored its pacing, or the file was removed: never resume
		// with no night window by default (re-review N2).
		return nil, fmt.Errorf(p.tool+": %s holds no stored pacing values; name the night window for the rest of "+
			"the run with --night-window HH:MM-HH:MM (or none)", p.path)
	}
	if s := st.Settings; s != nil {
		stored, err := parseNightWindow(s.NightWindow)
		if err != nil {
			return nil, fmt.Errorf(p.tool+": %s: %w", p.path, err)
		}
		for _, f := range []struct {
			flag          string
			given, stored string
			apply         func()
		}{
			{"five-hour-gate", fmt.Sprint(p.pacing.FiveHourGate), fmt.Sprint(s.FiveHourGate), func() { p.pacing.FiveHourGate = s.FiveHourGate }},
			{"seven-day-ceiling", fmt.Sprint(p.pacing.SevenDayCeiling), fmt.Sprint(s.SevenDayCeiling), func() { p.pacing.SevenDayCeiling = s.SevenDayCeiling }},
			{"night-window", p.night.String(), s.NightWindow, func() { p.night = stored }},
		} {
			switch {
			case !o.pacingGiven[f.flag]:
				f.apply()
			case f.given != f.stored && o.pacingOverride:
				p.warnings = append(p.warnings, fmt.Sprintf("--%s %s replaces the stored %s (--pacing-override)",
					f.flag, f.given, f.stored))
			case f.given != f.stored:
				return nil, fmt.Errorf(p.tool+": --%s %s differs from the %s this run paces on (%s); pass --pacing-override "+
					"to change it for this and later sessions", f.flag, f.given, f.stored, p.path)
			}
		}
	}
	st.Settings = &livePacingSettings{
		FiveHourGate: p.pacing.FiveHourGate, SevenDayCeiling: p.pacing.SevenDayCeiling, NightWindow: p.night.String(),
	}
	st.UpdatedAt = now.UTC().Format(time.RFC3339)
	if err := writeJSONFile(p.path, st); err != nil {
		return nil, err
	}
	return p, nil
}

// before decides, before a trial attempt, whether it may start: it returns "" and nil to start, a
// reason to pause cleanly, or an error that stops the run. A wait for a five-hour reset that falls
// inside the night window is spent here; one that would end after the window closes is a pause.
func (p *livePacer) before(ctx context.Context, env *liveEnv, w io.Writer) (string, error) {
	for {
		now := env.now()
		if !p.night.contains(now) {
			return fmt.Sprintf("%s is outside the night window %s", now.Format("15:04"), p.night), nil
		}
		if end := p.night.end(now); p.night.set && p.sessionMargin > 0 && now.Add(p.sessionMargin).After(end) {
			return fmt.Sprintf("the night window %s closes at %s, within the session timeout %s of now (%s)", p.night,
				end.Format("15:04"), p.sessionMargin, now.Format("15:04")), nil
		}
		v := p.verdict(now)
		switch v.Action {
		case eval.PaceGo:
			return "", nil
		case eval.PaceStop:
			return "", fmt.Errorf(p.tool+": %s", v.Reason)
		case eval.PacePause:
			return pauseReason(v), nil
		}
		until := v.Until.Add(liveResetMargin)
		if until.Sub(now) > liveMaxWait {
			return fmt.Sprintf("%s; it resets at %s, more than %s away", v.Reason, v.Until.Format(time.RFC3339), liveMaxWait), nil
		}
		if p.night.set && until.After(p.night.end(now)) {
			return fmt.Sprintf("%s; it resets at %s, after the night window %s closes", v.Reason,
				v.Until.Format(time.RFC3339), p.night), nil
		}
		fmt.Fprintf(w, p.tool+": pacing: %s; waiting until %s\n", v.Reason, until.Format(time.RFC3339))
		if err := env.sleep(ctx, until.Sub(now)); err != nil {
			return "", fmt.Errorf(p.tool+": waiting for the rate-limit reset: %w", err)
		}
	}
}

func pauseReason(v eval.LivePaceVerdict) string {
	if v.Until.IsZero() {
		return v.Reason + "; no event says when it resets"
	}
	return fmt.Sprintf("%s; it resets at %s", v.Reason, v.Until.Format(time.RFC3339))
}

// observe folds a finished attempt's events into the snapshot and keeps it in pacing.json.
func (p *livePacer) observe(env *liveEnv, evs []eval.HostRateLimit) error {
	if len(evs) == 0 {
		return nil
	}
	for _, ev := range evs {
		p.snap.Apply(ev)
	}
	if p.path == "" {
		return nil
	}
	return writeJSONFile(p.path, livePacingState{
		UpdatedAt: env.now().UTC().Format(time.RFC3339), Snapshot: p.snap,
		Settings: &livePacingSettings{
			FiveHourGate: p.pacing.FiveHourGate, SevenDayCeiling: p.pacing.SevenDayCeiling, NightWindow: p.night.String(),
		},
	})
}

// liveOverageStopFile marks, in the run directory, a stop on paid overage (review M1).
const liveOverageStopFile = "overage-stop.json"

// markLiveOverageStop leaves the overage-stop marker for why, when why is an overage stop.
func markLiveOverageStop(dir string, env *liveEnv, why string) {
	if !strings.Contains(why, "overage") {
		return
	}
	_ = writeJSONFile(filepath.Join(dir, liveOverageStopFile), map[string]string{
		"at": env.now().UTC().Format(time.RFC3339), "why": why,
	})
}

// checkLiveOverageStop refuses a session of a run that stopped on paid overage until the operator
// says --overage-checked, which clears the marker.
func checkLiveOverageStop(o liveOptions) error {
	p := filepath.Join(o.out, liveOverageStopFile)
	if _, err := os.Stat(p); err != nil {
		return nil
	}
	if !o.overageChecked {
		return fmt.Errorf("live-eval: this run stopped on paid overage (%s); check the account cannot spend overage, then "+
			"resume with --overage-checked", p)
	}
	return os.Remove(p)
}

// markLiveLimited makes a limited trial's record a failure on every outcome, recovery included when
// the task declares recovery checks, so the sensitivity analysis counts it as one even when it was cut
// short before any recovery verdict existed (review M2).
func markLiveLimited(rec *eval.LiveTrial, task eval.LiveTask) {
	rec.TaskSuccess, rec.Completed = false, false
	for _, c := range task.Checks {
		if c.Outcome == eval.OutcomeRecovery {
			failed := false
			rec.Recovered = &failed
			return
		}
	}
}

// liveLockFile holds, in the run directory, the session that is running it (review M8).
const liveLockFile = "session.lock"

// liveLock is session.lock.
type liveLock struct {
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
}

// acquireLiveLock takes the run directory's lock for this session: one session at a time, as the
// guard requires (W6 item 9). A lock whose holder no longer runs, left by a crash, is taken over.
func acquireLiveLock(dir string, now time.Time) (func(), error) {
	p := filepath.Join(dir, liveLockFile)
	b, err := json.Marshal(liveLock{PID: os.Getpid(), StartedAt: now.UTC().Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, liveFilePerm)
		if err == nil {
			_, werr := f.Write(b)
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(p)
				return nil, errors.Join(werr, cerr)
			}
			return func() { _ = os.Remove(p) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("live-eval: taking %s: %w", p, err)
		}
		raw, _ := os.ReadFile(p)
		var held liveLock
		if json.Unmarshal(raw, &held) == nil && held.PID > 0 && liveProcessAlive(held.PID) {
			return nil, fmt.Errorf("live-eval: another session (pid %d, since %s) is running this run directory; one "+
				"session at a time (%s)", held.PID, held.StartedAt, p)
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("live-eval: removing the stale %s: %w", p, err)
		}
	}
	return nil, fmt.Errorf("live-eval: could not take %s", p)
}

// liveOverageStop is the session driver's StopOn: the reason to stop a session at once when a
// rate_limit_event says paid overage is in use (review I1), or "".
func liveOverageStop(line []byte) string {
	ev, err := eval.ParseRateLimitLine(line)
	if err != nil || !ev.Overage() {
		return ""
	}
	return "a rate_limit_event says the account is using paid overage; the session was stopped at once (D100)"
}

// liveOverageNotes are the summary's notes on the account's overage state, as the trials' events
// reported it, and on a run whose trials printed no rate_limit_event at all (review I2).
func liveOverageNotes(recs liveRecords) []string {
	seen := map[string]bool{}
	var states []string
	events := 0
	for _, list := range [][]eval.LiveTrial{recs.scored, recs.limited} {
		for _, t := range list {
			events += len(t.RateLimits)
			if t.OverageStatus == "" && t.OverageDisabledReason == "" {
				continue
			}
			s := fmt.Sprintf("%s (%s)", orUnknown(t.OverageStatus), orUnknown(t.OverageDisabledReason))
			if !seen[s] {
				seen[s] = true
				states = append(states, s)
			}
		}
	}
	var out []string
	if len(states) > 0 {
		sort.Strings(states)
		out = append(out, "the account's overage state, as the trials' rate_limit_events reported it: "+strings.Join(states, ", "))
	}
	if events == 0 && len(recs.scored)+len(recs.limited) > 0 {
		out = append(out, "no trial printed a rate_limit_event, so the pacing had nothing to read and limits showed only as "+
			"usage-limit results (the plan's fallback; G0.16 says how often the host prints them)")
	}
	return out
}

// liveOverage names the first event of evs that says paid overage is in use, or "".
func liveOverage(evs []eval.HostRateLimit) string {
	for _, ev := range evs {
		if ev.Overage() {
			return fmt.Sprintf("a rate_limit_event in turn %d says the account is using paid overage (isUsingOverage %s, "+
				"overageInUse %s); the run never spends overage (D100)", ev.Turn+1, boolText(ev.IsUsingOverage), boolText(ev.OverageInUse))
		}
	}
	return ""
}

// livePacing is the pacing limits in force: the flags, or the provisional values when a caller built
// the options without them.
func (o liveOptions) livePacing() eval.LivePacing {
	p := eval.LivePacing{FiveHourGate: o.fiveHourGate, SevenDayCeiling: o.sevenDayCeiling}
	if p.FiveHourGate == 0 {
		p.FiveHourGate = eval.LiveFiveHourGate
	}
	if p.SevenDayCeiling == 0 {
		p.SevenDayCeiling = eval.LiveSevenDayCeiling
	}
	return p
}

// liveSignalContext is the run's context, cancelled by any of liveSignals (liveeval_signals_*.go):
// each cancels the run's context, the running host's tree is killed through its Cancel, and the run
// stops without scoring the cut attempt (re-review N5).
func liveSignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), liveSignals...)
}

// liveInterrupted is the stop an interrupt makes, or nil while ctx runs. after says what became of
// an attempt the interrupt cut, or is empty when none was running; it claims no kill, since how the
// host ended is the attempt's own record (re-review F8).
func liveInterrupted(ctx context.Context, tool, after string) error {
	if ctx.Err() == nil {
		return nil
	}
	return fmt.Errorf("%s: interrupted (%v)%s; the run stopped and continues with --resume", tool, context.Cause(ctx), after)
}

// liveCutAttempt is liveInterrupted's note for an attempt the interrupt cut.
const liveCutAttempt = "; the cut attempt was stopped, not graded, and set aside as interrupted"

// sleepContext waits d or until ctx ends.
func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// boolText renders an optional flag: "true", "false" or "absent".
func boolText(b *bool) string {
	if b == nil {
		return "absent"
	}
	return fmt.Sprint(*b)
}
