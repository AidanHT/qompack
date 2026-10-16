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
	"path/filepath"
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
	UpdatedAt string                `json:"updated_at"`
	Snapshot  eval.LiveRateSnapshot `json:"snapshot"`
}

// liveNightWindow is the --night-window: trials start only inside it, in the clock's own time zone.
// The zero value has no window, and every time is inside it.
type liveNightWindow struct {
	set              bool
	startMin, endMin int // minutes after midnight; end may be earlier than start (the window crosses midnight)
}

// parseNightWindow reads "HH:MM-HH:MM".
func parseNightWindow(v string) (liveNightWindow, error) {
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
		return "none"
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
}

// newLivePacer makes the session's pacer, starting from the snapshot a previous session of the run
// left in dir (none for a new run).
func newLivePacer(o liveOptions, dir string) (*livePacer, error) {
	p := &livePacer{pacing: o.livePacing(), night: o.night}
	if dir == "" {
		return p, nil
	}
	p.path = filepath.Join(dir, livePacingFile)
	raw, err := os.ReadFile(p.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return p, nil
	case err != nil:
		return nil, fmt.Errorf("live-eval: reading %s: %w", p.path, err)
	}
	var st livePacingState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("live-eval: parsing %s: %w", p.path, err)
	}
	p.snap = st.Snapshot
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
		v := eval.PaceLive(p.snap, now, p.pacing)
		switch v.Action {
		case eval.PaceGo:
			return "", nil
		case eval.PaceStop:
			return "", fmt.Errorf("live-eval: %s", v.Reason)
		case eval.PacePause:
			return pauseReason(v), nil
		}
		until := v.Until.Add(liveResetMargin)
		if p.night.set && until.After(p.night.end(now)) {
			return fmt.Sprintf("%s; it resets at %s, after the night window %s closes", v.Reason,
				v.Until.Format(time.RFC3339), p.night), nil
		}
		fmt.Fprintf(w, "live-eval: pacing: %s; waiting until %s\n", v.Reason, until.Format(time.RFC3339))
		if err := env.sleep(ctx, until.Sub(now)); err != nil {
			return "", fmt.Errorf("live-eval: waiting for the rate-limit reset: %w", err)
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
	return writeJSONFile(p.path, livePacingState{UpdatedAt: env.now().UTC().Format(time.RFC3339), Snapshot: p.snap})
}

// liveOverage names the first event of evs that says paid overage is in use, or "".
func liveOverage(evs []eval.HostRateLimit) string {
	for _, ev := range evs {
		if ev.Overage() {
			return fmt.Sprintf("a rate_limit_event in turn %d says the account is using paid overage (isUsingOverage %t, "+
				"overageInUse %t); the run never spends overage (D100)", ev.Turn+1, ev.IsUsingOverage, ev.OverageInUse)
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
