package eval_test

import (
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// Recorded rate_limit_event lines. rlSmoke is copied verbatim from smoke2-plugin.stream.jsonl (Claude
// Code 2.1.280's real output); the others keep its shape and change only what the case is about:
// a rejected five-hour window, an event with no utilization anywhere (the schema makes every field
// optional, binary 2.1.293 @210378386), and paid overage in use.
const (
	rlSmoke     = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1790116800,"rateLimitType":"five_hour","overageStatus":"rejected","overageDisabledReason":"org_level_disabled","isUsingOverage":false,"unifiedWindows":{"five_hour":{"utilization":0.21,"resetsAt":1790116800},"seven_day":{"utilization":0.68,"resetsAt":1790334000}}},"uuid":"8852aee2-d217-47fe-be12-398d8fcae06c","session_id":"97945d67-f32b-4e7d-aa99-fe79ce6bf273"}`
	rlLater     = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1790116800,"rateLimitType":"five_hour","isUsingOverage":false,"unifiedWindows":{"five_hour":{"utilization":0.27,"resetsAt":1790116800},"seven_day":{"utilization":0.70,"resetsAt":1790334000}}},"session_id":"s"}`
	rlRejected  = `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1790116800,"rateLimitType":"five_hour","utilization":1,"isUsingOverage":false},"session_id":"s"}`
	rlNoUtil    = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","rateLimitType":"seven_day"},"session_id":"s"}`
	rlOverage   = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1790116800,"rateLimitType":"five_hour","isUsingOverage":true,"overageStatus":"allowed"},"session_id":"s"}`
	rlOverageIn = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1790116800,"rateLimitType":"five_hour","isUsingOverage":false,"overageInUse":true},"session_id":"s"}`
	rlInit      = `{"type":"system","subtype":"init","session_id":"s","model":"claude-opus-5-5","claude_code_version":"2.1.300","plugins":[],"mcp_servers":[],"tools":[]}`
	rlResult    = `{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"ok","total_cost_usd":0,"usage":{},"modelUsage":{},"permission_denials":[]}`
	// rlLimitResult is a usage-limit error result. Its exact text is G0.16's to record; the harness
	// matches it by eval.LiveUsageLimitPattern.
	rlLimitResult = `{"type":"result","subtype":"success","is_error":true,"num_turns":1,"result":"Claude AI usage limit reached|1790116800","total_cost_usd":0,"usage":{},"modelUsage":{},"permission_denials":[]}`
)

func parseLines(t *testing.T, lines ...string) eval.HostStream {
	t.Helper()
	s, err := eval.ParseHostStream(strings.NewReader(strings.Join(lines, "\n")+"\n"), nil)
	require.NoError(t, err)
	return s
}

func fptr(v float64) *float64 { return &v }

// TestParseHostStream_KeepsRateLimitEvents: every rate_limit_event line is kept, in order, with the
// turn it arrived in and every rate_limit_info field the harness paces on; an event with no
// utilization parses with nil utilizations, not zeros.
func TestParseHostStream_KeepsRateLimitEvents(t *testing.T) {
	s := parseLines(t, rlInit, rlSmoke, rlResult, rlNoUtil, rlLater, rlResult)
	require.Len(t, s.RateLimits, 3)
	first := s.RateLimits[0]
	require.Equal(t, 0, first.Turn)
	require.Equal(t, "allowed", first.Status)
	require.Equal(t, int64(1790116800), first.ResetsAt)
	require.Equal(t, "five_hour", first.RateLimitType)
	require.Nil(t, first.Utilization, "the recorded line has no top-level utilization")
	require.Equal(t, false, *first.IsUsingOverage, "the field was present and false")
	require.Nil(t, first.OverageInUse, "absent, not false")
	require.Equal(t, "org_level_disabled", first.OverageDisabledReason)
	require.Equal(t, "rejected", first.OverageStatus)
	require.Equal(t, fptr(0.21), first.Windows["five_hour"].Utilization)
	require.Equal(t, int64(1790334000), first.Windows["seven_day"].ResetsAt)
	require.Equal(t, fptr(0.68), first.Windows["seven_day"].Utilization)

	noUtil := s.RateLimits[1]
	require.Equal(t, 1, noUtil.Turn)
	require.Equal(t, "allowed_warning", noUtil.Status)
	require.Nil(t, noUtil.Utilization)
	require.Empty(t, noUtil.Windows)
	require.Zero(t, noUtil.ResetsAt)
	require.NotContains(t, s.Other, "rate_limit_event", "a kept line is not an uninterpreted one")

	recorded := parseLiveFixture(t, smoke2Stream)
	require.NotEmpty(t, recorded.RateLimits, "the recorded 2.1.280 session's events are kept")
	for _, ev := range recorded.RateLimits {
		require.Contains(t, ev.Windows, "five_hour")
	}
}

// TestLiveRateSnapshot_Apply: the newest event wins per window; an event with no utilization keeps
// the window's last known value while the window's period is the same, and a new period (another
// resetsAt) forgets it; overage follows the newest event.
func TestLiveRateSnapshot_Apply(t *testing.T) {
	s := parseLines(t, rlSmoke, rlLater, rlNoUtil, rlRejected)
	var snap eval.LiveRateSnapshot
	snap.Apply(s.RateLimits[0])
	snap.Apply(s.RateLimits[1])
	require.Equal(t, fptr(0.27), snap.Windows["five_hour"].Utilization)
	require.Equal(t, fptr(0.70), snap.Windows["seven_day"].Utilization)
	snap.Apply(s.RateLimits[2]) // seven_day, no utilization, no resetsAt: same period as far as it says
	require.Equal(t, fptr(0.70), snap.Windows["seven_day"].Utilization)
	require.Equal(t, "allowed_warning", snap.Windows["seven_day"].Status)
	snap.Apply(s.RateLimits[3])
	require.Equal(t, "rejected", snap.Windows["five_hour"].Status)
	require.Equal(t, fptr(1.0), snap.Windows["five_hour"].Utilization)
	require.False(t, snap.Overage)

	next := parseLines(t, `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1790134800,"rateLimitType":"five_hour"}}`)
	snap.Apply(next.RateLimits[0])
	require.Nil(t, snap.Windows["five_hour"].Utilization, "a new five-hour period starts with no known utilization")
	require.Equal(t, "allowed", snap.Windows["five_hour"].Status)

	snap.Apply(parseLines(t, rlOverage).RateLimits[0])
	require.True(t, snap.Overage)
	snap.Apply(parseLines(t, rlLater).RateLimits[0])
	require.False(t, snap.Overage, "overage is what the newest event says")
}

// TestPaceLive: the pre-trial decision from the newest event (W6 item 8).
func TestPaceLive(t *testing.T) {
	p := eval.LivePacing{FiveHourGate: eval.LiveFiveHourGate, SevenDayCeiling: eval.LiveSevenDayCeiling}
	require.InDelta(t, 0.90, eval.LiveFiveHourGate, 0, "the five-hour gate until the pilot sets it (W6 item 8)")
	require.InDelta(t, 0.60, eval.LiveSevenDayCeiling, 0, "the owner's 40 % reserve (D100)")
	before := time.Unix(1790100000, 0) // before both resets
	snap := func(lines ...string) eval.LiveRateSnapshot {
		var s eval.LiveRateSnapshot
		for _, ev := range parseLines(t, lines...).RateLimits {
			s.Apply(ev)
		}
		return s
	}
	under := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1790116800,"rateLimitType":"five_hour","unifiedWindows":{"five_hour":{"utilization":0.89,"resetsAt":1790116800},"seven_day":{"utilization":0.59,"resetsAt":1790334000}}}}`
	atGate := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1790116800,"rateLimitType":"five_hour","unifiedWindows":{"five_hour":{"utilization":0.90,"resetsAt":1790116800},"seven_day":{"utilization":0.30,"resetsAt":1790334000}}}}`

	require.Equal(t, eval.PaceGo, eval.PaceLive(eval.LiveRateSnapshot{}, before, p).Action, "no event yet: nothing to pace on")
	require.Equal(t, eval.PaceGo, eval.PaceLive(snap(under), before, p).Action)

	v := eval.PaceLive(snap(atGate), before, p)
	require.Equal(t, eval.PaceWait, v.Action, v.Reason)
	require.Equal(t, time.Unix(1790116800, 0), v.Until)
	require.Contains(t, v.Reason, "five_hour")

	v = eval.PaceLive(snap(rlRejected), before, p)
	require.Equal(t, eval.PaceWait, v.Action, "a rejected five-hour window waits for its reset")
	require.Equal(t, time.Unix(1790116800, 0), v.Until)

	v = eval.PaceLive(snap(rlSmoke), before, p)
	require.Equal(t, eval.PacePause, v.Action, "the recorded seven-day 0.68 is above the 0.60 ceiling")
	require.Equal(t, time.Unix(1790334000, 0), v.Until)
	require.Contains(t, v.Reason, "seven_day")

	both := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1790116800,"rateLimitType":"five_hour","unifiedWindows":{"five_hour":{"utilization":0.95,"resetsAt":1790116800},"seven_day_opus":{"utilization":0.61,"resetsAt":1790334000}}}}`
	require.Equal(t, eval.PacePause, eval.PaceLive(snap(both), before, p).Action,
		"the seven-day pause outranks the five-hour wait, and a per-model seven-day window counts")

	after := time.Unix(1790116800+1, 0)
	require.Equal(t, eval.PaceGo, eval.PaceLive(snap(atGate), after, p).Action, "a window past its reset is fresh")
	require.Equal(t, eval.PaceGo, eval.PaceLive(snap(rlNoUtil), before, p).Action,
		"an event with no utilization cannot trip a gate")

	rejectedUnknown := `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","rateLimitType":"five_hour"}}`
	v = eval.PaceLive(snap(rejectedUnknown), before, p)
	require.Equal(t, eval.PacePause, v.Action, "a rejection with no reset time cannot be waited out")

	for _, line := range []string{rlOverage, rlOverageIn} {
		v = eval.PaceLive(snap(line), before, p)
		require.Equal(t, eval.PaceStop, v.Action, line)
		require.Contains(t, v.Reason, "overage")
	}

	custom := eval.LivePacing{FiveHourGate: 0.5, SevenDayCeiling: 0.8}
	require.Equal(t, eval.PaceGo, eval.PaceLive(snap(rlSmoke), before, custom).Action, "both limits are the operator's")
	require.Equal(t, eval.PaceWait, eval.PaceLive(snap(under), before, custom).Action)
}

// TestLiveUsageLimited: a trial is cut short by a limit when any of its events is rejected or a
// result reports a usage limit; an ordinary error result is not a limit.
func TestLiveUsageLimited(t *testing.T) {
	require.Empty(t, eval.LiveUsageLimited(parseLines(t, rlInit, rlSmoke, rlResult)))
	require.Contains(t, eval.LiveUsageLimited(parseLines(t, rlInit, rlSmoke, rlRejected, rlResult)), "rejected")
	require.Contains(t, eval.LiveUsageLimited(parseLines(t, rlInit, rlLimitResult)), "usage limit")
	other := strings.Replace(rlLimitResult, "Claude AI usage limit reached|1790116800", "Tool failed", 1)
	require.Empty(t, eval.LiveUsageLimited(parseLines(t, rlInit, other)))
}

// TestLiveRateUse: each window's utilization delta over a trial, from the snapshot before it to the
// trial's last event; with no earlier reading the first event is the base, and a window that reset
// in between counts from zero.
func TestLiveRateUse(t *testing.T) {
	var before eval.LiveRateSnapshot
	before.Apply(parseLines(t, rlSmoke).RateLimits[0]) // five_hour 0.21, seven_day 0.68
	use := eval.LiveRateUse(before, parseLines(t, rlLater).RateLimits)
	require.InDelta(t, 0.06, *use["five_hour"].Delta, 1e-9)
	require.InDelta(t, 0.02, *use["seven_day"].Delta, 1e-9)
	require.Equal(t, eval.RateBasisPrevious, use["five_hour"].Basis)

	use = eval.LiveRateUse(eval.LiveRateSnapshot{}, parseLines(t, rlSmoke, rlLater).RateLimits)
	require.InDelta(t, 0.06, *use["five_hour"].Delta, 1e-9)
	require.Equal(t, eval.RateBasisFirstEvent, use["five_hour"].Basis)

	reset := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1790134800,"rateLimitType":"five_hour","unifiedWindows":{"five_hour":{"utilization":0.04,"resetsAt":1790134800}}}}`
	use = eval.LiveRateUse(before, parseLines(t, reset).RateLimits)
	require.InDelta(t, 0.04, *use["five_hour"].Delta, 1e-9)
	require.Equal(t, eval.RateBasisReset, use["five_hour"].Basis)

	require.Empty(t, eval.LiveRateUse(before, parseLines(t, rlNoUtil).RateLimits), "no utilization, no delta")
	require.Empty(t, eval.LiveRateUse(before, nil))
}

// TestLiveRateUse_NeverPanics: schema-valid events that leave a window with no utilization at one
// end — a new period announced without one, or a top-level resetsAt that disagrees with the window's
// own — give an entry with no delta and basis unknown, never a nil dereference (review C1).
func TestLiveRateUse_NeverPanics(t *testing.T) {
	var before eval.LiveRateSnapshot
	before.Apply(parseLines(t, rlSmoke).RateLimits[0])
	newPeriod := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1790134800,"rateLimitType":"five_hour"}}`
	use := eval.LiveRateUse(before, parseLines(t, rlLater, newPeriod).RateLimits)
	require.Nil(t, use["five_hour"].Delta)
	require.Equal(t, eval.RateBasisUnknown, use["five_hour"].Basis)
	require.InDelta(t, 0.02, *use["seven_day"].Delta, 1e-9, "the other window still has its delta")

	disagree := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1790134800,"rateLimitType":"five_hour","unifiedWindows":{"five_hour":{"utilization":0.3,"resetsAt":1790116800}}}}`
	use = eval.LiveRateUse(eval.LiveRateSnapshot{}, parseLines(t, disagree).RateLimits)
	require.Nil(t, use["five_hour"].Delta)
	require.Equal(t, eval.RateBasisUnknown, use["five_hour"].Basis)
	use = eval.LiveRateUse(before, parseLines(t, disagree).RateLimits)
	require.Nil(t, use["five_hour"].Delta)
}

// TestLiveRateProblem: the pacing fails closed on drift (review I2). An event whose rate_limit_info
// yields no status, no window type and no window is a shape the parser does not know; a session
// whose events never carry an overage field cannot be guarded against paid overage. Each names
// G0.16. No events at all is the plan's fallback, not a problem.
func TestLiveRateProblem(t *testing.T) {
	require.Empty(t, eval.LiveRateProblem(nil))
	require.Empty(t, eval.LiveRateProblem(parseLines(t, rlSmoke, rlNoUtil).RateLimits), "one event carries the field")
	renamed := strings.Replace(rlSmoke, "rate_limit_info", "rate_limit_details", 1)
	p := eval.LiveRateProblem(parseLines(t, renamed).RateLimits)
	require.Contains(t, p, "not recognised")
	require.Contains(t, p, "G0.16")
	noOverage := strings.Replace(rlSmoke, `"isUsingOverage":false,`, `"usingOverage":false,`, 1)
	p = eval.LiveRateProblem(parseLines(t, noOverage).RateLimits)
	require.Contains(t, p, "overage")
	require.Contains(t, p, "G0.16")

	ev, err := eval.ParseRateLimitLine([]byte(rlOverage))
	require.NoError(t, err)
	require.True(t, ev.Overage())
	ev, err = eval.ParseRateLimitLine([]byte(rlSmoke))
	require.NoError(t, err)
	require.False(t, ev.Overage())
}
