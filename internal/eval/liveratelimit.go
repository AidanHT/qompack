package eval

// ── Subscription limits: the host's rate_limit_event lines and the pacing they drive ──────────────
//
// V7 W6 item 8 (owner decision D100). A live run spends the owner's subscription allowance, which it
// shares with the owner's own use. Claude Code's stream-json prints `rate_limit_event` lines whose
// `rate_limit_info` carries the event's own window (`status` allowed, allowed_warning or rejected,
// `resetsAt` in Unix seconds, `rateLimitType` five_hour, seven_day, seven_day_opus, …, an optional
// `utilization`), the overage state (`isUsingOverage`, `overageInUse`, `overageStatus`) and, in the
// lines 2.1.280 printed, `unifiedWindows`: each window's utilization and reset (binary 2.1.293
// @210378386 for the schema, @210379883 for the overage fields; the recorded smoke sessions for the
// shape). Every field is optional in that schema, so an absent value is unknown, never zero.
//
// The harness keeps every event per trial, folds the newest into a snapshot before each trial and
// decides there: wait for a five-hour reset, pause until a seven-day reset, or stop on paid overage.

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The pacing limits until the pilot measures a trial's share of each window (W6 item 8): the
// five-hour gate (R2 sets it to 1 − k × the largest per-trial share) and the seven-day ceiling, the
// owner's 40 % reserve. Both are live-eval flags.
const (
	LiveFiveHourGate    = 0.90
	LiveSevenDayCeiling = 0.60
)

// The windows the pacing reads by name. Every window whose name starts with liveSevenDayPrefix
// (seven_day, seven_day_opus, seven_day_sonnet) counts against the seven-day ceiling: the account may
// have per-model weekly windows, and the one the run's model draws on is not named in the event.
const (
	liveFiveHourWindow = "five_hour"
	liveSevenDayPrefix = "seven_day"
)

// The rate_limit_info statuses the pacing distinguishes.
const (
	RateStatusRejected = "rejected"
)

// LiveUsageLimitPattern matches the text of a result the host ends a turn with when the
// subscription's limit refused it. The exact form is G0.16's to record; until then this matches the
// forms Claude Code has printed ("Claude AI usage limit reached|<epoch>", "… limit reached ∙ resets
// …", "You've hit your limit") and an exhausted rate-limit retry. It is read only from a result
// marked is_error. It is provisional and broad: "rate limit" also matches a transient API 429 that
// outlasted its retries, which then counts as limited (excluded, run again, reported in the
// sensitivity analysis) rather than scored. Narrowing it to the form G0.16 records is tracked on
// that check's row (review M6).
var LiveUsageLimitPattern = regexp.MustCompile(`(?i)usage limit|limit reached|hit your limit|rate[ _-]?limit`)

// HostRateLimit is one rate_limit_event line's rate_limit_info, with the turn it arrived in.
type HostRateLimit struct {
	// Turn is the 0-based user turn the line arrived in; RecvMS its receive time, when known.
	Turn          int      `json:"turn"`
	RecvMS        int64    `json:"recv_ms,omitempty"`
	Status        string   `json:"status,omitempty"`
	ResetsAt      int64    `json:"resets_at,omitempty"`
	RateLimitType string   `json:"rate_limit_type,omitempty"`
	Utilization   *float64 `json:"utilization,omitempty"`
	// IsUsingOverage and OverageInUse are the two fields that say the account is spending paid
	// overage; nil when the line did not carry the field. OverageStatus is whether overage is allowed
	// at all, and OverageDisabledReason why it is not ("org_level_disabled" in the recorded lines).
	IsUsingOverage        *bool  `json:"is_using_overage"`
	OverageInUse          *bool  `json:"overage_in_use,omitempty"`
	OverageStatus         string `json:"overage_status,omitempty"`
	OverageDisabledReason string `json:"overage_disabled_reason,omitempty"`
	// Windows is unifiedWindows: every window's utilization and reset, by window name.
	Windows map[string]HostRateWindow `json:"windows,omitempty"`
	// Unrecognized is that the line's rate_limit_info yielded no status, no window type and no window:
	// a shape this parser does not know (review I2). The pacing stops on it rather than read it as
	// "no limit".
	Unrecognized bool `json:"unrecognized,omitempty"`
}

// HostRateWindow is one window in unifiedWindows.
type HostRateWindow struct {
	Utilization *float64 `json:"utilization,omitempty"`
	ResetsAt    int64    `json:"resets_at,omitempty"`
}

// Overage reports that the event says paid overage is in use.
func (e HostRateLimit) Overage() bool {
	return (e.IsUsingOverage != nil && *e.IsUsingOverage) || (e.OverageInUse != nil && *e.OverageInUse)
}

// carriesOverage reports that the event carried at least one of the overage-in-use fields.
func (e HostRateLimit) carriesOverage() bool { return e.IsUsingOverage != nil || e.OverageInUse != nil }

type rawRateLimitEvent struct {
	Info struct {
		Status                string                   `json:"status"`
		ResetsAt              float64                  `json:"resetsAt"`
		RateLimitType         string                   `json:"rateLimitType"`
		Utilization           *float64                 `json:"utilization"`
		IsUsingOverage        *bool                    `json:"isUsingOverage"`
		OverageInUse          *bool                    `json:"overageInUse"`
		OverageStatus         string                   `json:"overageStatus"`
		OverageDisabledReason string                   `json:"overageDisabledReason"`
		UnifiedWindows        map[string]rawRateWindow `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
}

type rawRateWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    float64  `json:"resetsAt"`
}

// ParseRateLimitLine decodes one rate_limit_event line. The session driver calls it on each such
// line as it arrives, so it can stop a session the moment paid overage is in use (review I1).
func ParseRateLimitLine(line []byte) (HostRateLimit, error) {
	var raw rawRateLimitEvent
	if err := json.Unmarshal(line, &raw); err != nil {
		return HostRateLimit{}, err
	}
	in := raw.Info
	ev := HostRateLimit{
		Status: in.Status, ResetsAt: int64(in.ResetsAt), RateLimitType: in.RateLimitType, Utilization: in.Utilization,
		IsUsingOverage: in.IsUsingOverage, OverageInUse: in.OverageInUse, OverageStatus: in.OverageStatus,
		OverageDisabledReason: in.OverageDisabledReason,
	}
	for name, w := range in.UnifiedWindows {
		if ev.Windows == nil {
			ev.Windows = map[string]HostRateWindow{}
		}
		ev.Windows[name] = HostRateWindow{Utilization: w.Utilization, ResetsAt: int64(w.ResetsAt)}
	}
	ev.Unrecognized = ev.Status == "" && ev.RateLimitType == "" && len(ev.Windows) == 0
	return ev, nil
}

// rateLimit records one rate_limit_event line on the stream and in its turn.
func (p *streamParser) rateLimit(line []byte, at int64) error {
	ev, err := ParseRateLimitLine(line)
	if err != nil {
		return err
	}
	t := p.turn(at)
	ev.Turn, ev.RecvMS = t.Index, at
	p.out.RateLimits = append(p.out.RateLimits, ev)
	return nil
}

// LiveRateProblem says why a session's events cannot be paced on, or "" when they can (review I2):
// an event of a shape the parser does not know, or events none of which carries an overage field, so
// the overage stop could never fire. Both fail closed. A session with no event at all is the plan's
// fallback (the usage-limit result), not a problem.
func LiveRateProblem(evs []HostRateLimit) string {
	carried := false
	for _, ev := range evs {
		if ev.Unrecognized {
			return fmt.Sprintf("a rate_limit_event in turn %d has a shape that is not recognised (no status, window type "+
				"or window); the pacing cannot read it, so no trial starts until internal/eval/liveratelimit.go follows "+
				"the host's shape (G0.16)", ev.Turn+1)
		}
		carried = carried || ev.carriesOverage()
	}
	if len(evs) > 0 && !carried {
		return fmt.Sprintf("none of the session's %d rate_limit_event(s) carries isUsingOverage or overageInUse, so the "+
			"paid-overage stop cannot work; no trial starts until internal/eval/liveratelimit.go follows the host's "+
			"shape (G0.16)", len(evs))
	}
	return ""
}

// LiveOverageState is the newest overage status and disabled reason among evs ("" when none says).
func LiveOverageState(evs []HostRateLimit) (status, disabledReason string) {
	for _, ev := range evs {
		if ev.OverageStatus != "" {
			status = ev.OverageStatus
		}
		if ev.OverageDisabledReason != "" {
			disabledReason = ev.OverageDisabledReason
		}
	}
	return status, disabledReason
}

// LiveRateWindow is what the newest events say of one window.
type LiveRateWindow struct {
	// Utilization is the window's last reported share used, nil when no event of this period said.
	Utilization *float64 `json:"utilization,omitempty"`
	// ResetsAt is when the window's period ends, in Unix seconds; 0 when unknown.
	ResetsAt int64 `json:"resets_at,omitempty"`
	// Status is the last status an event reported for this window as its own rateLimitType.
	Status string `json:"status,omitempty"`
}

// LiveRateSnapshot folds rate-limit events, oldest first, into what the newest say of each window.
type LiveRateSnapshot struct {
	Windows map[string]LiveRateWindow `json:"windows,omitempty"`
	// Overage is whether the newest event said paid overage is in use. It is not kept on disk: the run
	// stops where it sees overage, and resuming afterwards is the operator's decision.
	Overage bool `json:"-"`
}

// Apply folds ev into the snapshot. A window's reported reset that differs from the one known starts
// a new period and forgets the old period's utilization and status; an event that names no reset
// leaves the period as it was.
func (s *LiveRateSnapshot) Apply(ev HostRateLimit) {
	if s.Windows == nil {
		s.Windows = map[string]LiveRateWindow{}
	}
	period := func(name string, resetsAt int64) LiveRateWindow {
		w := s.Windows[name]
		if resetsAt != 0 && resetsAt != w.ResetsAt {
			w = LiveRateWindow{ResetsAt: resetsAt}
		}
		return w
	}
	for _, name := range slices.Sorted(maps.Keys(ev.Windows)) {
		rw := ev.Windows[name]
		w := period(name, rw.ResetsAt)
		if rw.Utilization != nil {
			w.Utilization = rw.Utilization
		}
		s.Windows[name] = w
	}
	if ev.RateLimitType != "" {
		w := period(ev.RateLimitType, ev.ResetsAt)
		w.Status = ev.Status
		if ev.Utilization != nil {
			w.Utilization = ev.Utilization
		}
		s.Windows[ev.RateLimitType] = w
	}
	s.Overage = ev.Overage()
}

// LivePacing is the pacing limits a run uses.
type LivePacing struct {
	// FiveHourGate is the five-hour utilization at or above which no trial starts until that window
	// resets.
	FiveHourGate float64 `json:"five_hour_gate"`
	// SevenDayCeiling is the seven-day utilization at or above which the run pauses until that window
	// resets: the share the owner keeps for their own use is 1 − SevenDayCeiling.
	SevenDayCeiling float64 `json:"seven_day_ceiling"`
}

// The pre-trial pacing actions.
const (
	// PaceGo starts the trial.
	PaceGo = "go"
	// PaceWait waits until Until, then decides again: a five-hour window resets within hours.
	PaceWait = "wait"
	// PacePause ends the session cleanly; `--resume` continues the plan after Until (zero when no
	// event says when the limit lifts).
	PacePause = "pause"
	// PaceStop stops the run: it would otherwise spend paid overage.
	PaceStop = "stop"
)

// LivePaceVerdict is the pre-trial decision.
type LivePaceVerdict struct {
	Action string
	Until  time.Time
	Reason string
}

// PaceLive decides, from the newest events, whether a trial may start at now (W6 item 8): paid
// overage stops the run; a seven-day window at or above the ceiling, or rejected, pauses it until
// that window resets; a five-hour window at or above the gate, or rejected, waits for its reset; any
// other rejected window waits for its reset too. A window whose reset has passed is fresh. A
// rejection with no known reset pauses, since there is nothing to wait for.
func PaceLive(s LiveRateSnapshot, now time.Time, p LivePacing) LivePaceVerdict {
	if s.Overage {
		return LivePaceVerdict{Action: PaceStop, Reason: "the newest rate_limit_event says the account is using paid overage; " +
			"the run never spends overage (D100)"}
	}
	var pause, wait *LivePaceVerdict
	for _, name := range slices.Sorted(maps.Keys(s.Windows)) {
		w := s.Windows[name]
		if w.ResetsAt != 0 && w.ResetsAt <= now.Unix() {
			continue
		}
		util := "unknown"
		if w.Utilization != nil {
			util = fmt.Sprintf("%.2f", *w.Utilization)
		}
		rejected := w.Status == RateStatusRejected
		var limit *float64
		switch {
		case strings.HasPrefix(name, liveSevenDayPrefix):
			limit = &p.SevenDayCeiling
		case name == liveFiveHourWindow:
			limit = &p.FiveHourGate
		}
		over := limit != nil && w.Utilization != nil && *w.Utilization >= *limit
		if !rejected && !over {
			continue
		}
		why := fmt.Sprintf("the %s window is %s (utilization %s", name, orStatus(w.Status, rejected), util)
		if limit != nil {
			why += fmt.Sprintf(", limit %.2f", *limit)
		}
		why += ")"
		v := LivePaceVerdict{Reason: why}
		if w.ResetsAt != 0 {
			v.Until = time.Unix(w.ResetsAt, 0)
		}
		switch {
		case strings.HasPrefix(name, liveSevenDayPrefix) || w.ResetsAt == 0:
			v.Action = PacePause
			if pause == nil || v.Until.After(pause.Until) {
				pause = &v
			}
		default:
			v.Action = PaceWait
			if wait == nil || v.Until.After(wait.Until) {
				wait = &v
			}
		}
	}
	switch {
	case pause != nil:
		return *pause
	case wait != nil:
		return *wait
	}
	return LivePaceVerdict{Action: PaceGo}
}

func orStatus(status string, rejected bool) string {
	if rejected {
		return RateStatusRejected
	}
	if status == "" {
		return "at its limit"
	}
	return status + " and at its limit"
}

// LiveUsageLimited says why a limit cut the session short, or "" when none did: an event that
// rejected a window, or an error result whose text reports a usage limit (W6 item 8).
func LiveUsageLimited(s HostStream) string {
	for _, ev := range s.RateLimits {
		if ev.Status == RateStatusRejected {
			return fmt.Sprintf("a rate_limit_event rejected the %s window in turn %d", orUnknownWindow(ev.RateLimitType), ev.Turn+1)
		}
	}
	for _, t := range s.Turns {
		if t.Result == nil || !t.Result.IsError {
			continue
		}
		for _, text := range append([]string{t.Result.Result}, t.Result.Errors...) {
			if LiveUsageLimitPattern.MatchString(text) {
				return fmt.Sprintf("turn %d's result reported a usage limit: %s", t.Index+1, clip(text))
			}
		}
	}
	return ""
}

// liveUsageLimitEpoch is the reset epoch some usage-limit results end with: "…|1790116800".
var liveUsageLimitEpoch = regexp.MustCompile(`\|\s*(\d{9,11})\s*$`)

// LiveUsageLimitReset is when a usage-limit result says the limit resets, when one carries the
// "|<Unix seconds>" suffix (review M5; the form is G0.16's to confirm).
func LiveUsageLimitReset(s HostStream) (time.Time, bool) {
	for _, t := range s.Turns {
		if t.Result == nil || !t.Result.IsError {
			continue
		}
		for _, text := range append([]string{t.Result.Result}, t.Result.Errors...) {
			if !LiveUsageLimitPattern.MatchString(text) {
				continue
			}
			if m := liveUsageLimitEpoch.FindStringSubmatch(text); m != nil {
				var sec int64
				if _, err := fmt.Sscan(m[1], &sec); err == nil {
					return time.Unix(sec, 0), true
				}
			}
		}
	}
	return time.Time{}, false
}

func orUnknownWindow(name string) string {
	if name == "" {
		return "unnamed"
	}
	return name
}

// How a trial's utilization delta was based.
const (
	// RateBasisPrevious: the reading before the trial, in the same period.
	RateBasisPrevious = "previous"
	// RateBasisFirstEvent: no earlier reading, so the trial's own first reading; the delta then
	// misses the trial's use up to that event, a lower bound.
	RateBasisFirstEvent = "first-event"
	// RateBasisReset: the window's period changed since the reading before the trial, so the delta
	// counts from zero.
	RateBasisReset = "reset"
	// RateBasisUnknown: one end of the trial has no utilization for the window (a new period was
	// announced without one), so there is no delta.
	RateBasisUnknown = "unknown"
)

// LiveRateDelta is one window's utilization change over a trial.
type LiveRateDelta struct {
	Before *float64 `json:"before,omitempty"`
	After  *float64 `json:"after,omitempty"`
	Delta  *float64 `json:"delta,omitempty"`
	Basis  string   `json:"basis"`
}

// LiveRateUse is each window's utilization delta over a trial whose events are evs, from before (the
// snapshot of every earlier event) to the trial's last reading: the input to R2's calendar estimate.
// A window no event gave a utilization for has no entry.
func LiveRateUse(before LiveRateSnapshot, evs []HostRateLimit) map[string]LiveRateDelta {
	after := LiveRateSnapshot{Windows: maps.Clone(before.Windows)}
	first := map[string]LiveRateWindow{}
	for _, ev := range evs {
		after.Apply(ev)
		var read []string
		for name, w := range ev.Windows {
			if w.Utilization != nil {
				read = append(read, name)
			}
		}
		if ev.RateLimitType != "" && ev.Utilization != nil {
			read = append(read, ev.RateLimitType)
		}
		for _, name := range read {
			if _, ok := first[name]; !ok {
				first[name] = after.Windows[name]
			}
		}
	}
	out := map[string]LiveRateDelta{}
	for name := range first {
		end := after.Windows[name]
		prev, had := before.Windows[name]
		d := LiveRateDelta{After: end.Utilization}
		switch {
		case had && prev.Utilization != nil && prev.ResetsAt == end.ResetsAt:
			d.Before, d.Basis = prev.Utilization, RateBasisPrevious
		case had && prev.ResetsAt != 0 && prev.ResetsAt != end.ResetsAt:
			zero := 0.0
			d.Before, d.Basis = &zero, RateBasisReset
		default:
			f := first[name]
			if f.ResetsAt != end.ResetsAt {
				zero := 0.0
				d.Before, d.Basis = &zero, RateBasisReset
			} else {
				d.Before, d.Basis = f.Utilization, RateBasisFirstEvent
			}
		}
		if d.After == nil || d.Before == nil {
			// A window whose period changed with no utilization reported for it has no delta to give.
			out[name] = LiveRateDelta{After: d.After, Basis: RateBasisUnknown}
			continue
		}
		delta := *d.After - *d.Before
		d.Delta = &delta
		out[name] = d
	}
	return out
}
