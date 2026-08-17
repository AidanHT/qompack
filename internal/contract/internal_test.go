package contract

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseMode_RoundTripsEveryMode asserts ParseMode is String's exact inverse for all three
// modes: state/contract.json persists the string spelling, so a mode that cannot be read back is a
// degradation that silently disappears on the next SessionStart — the precise failure §12.1 exists
// to prevent.
func TestParseMode_RoundTripsEveryMode(t *testing.T) {
	for _, m := range []Mode{ModeFull, ModeDegradedPassive, ModeOff} {
		got, ok := ParseMode(m.String())
		require.True(t, ok, "ParseMode must recognize %q", m.String())
		require.Equal(t, m, got)
	}
}

// TestParseMode_RejectsUnrecognizedValues asserts an unreadable persisted mode falls back to
// ModeFull with ok == false, so NewMonitor's loader can tell "no state" from "corrupt state" and
// treat both as §12.3's "fail toward do nothing".
func TestParseMode_RejectsUnrecognizedValues(t *testing.T) {
	for _, s := range []string{"", "unknown", "FULL", "degraded", "passive"} {
		got, ok := ParseMode(s)
		require.False(t, ok, "ParseMode must reject %q", s)
		require.Equal(t, ModeFull, got)
	}
}

// TestSeverity_Label asserts the log-line spelling of every severity, including the out-of-range
// case, so a LOUD line can never carry a blank severity.
func TestSeverity_Label(t *testing.T) {
	require.Equal(t, "info", SevInfo.label())
	require.Equal(t, "warn", SevWarn.label())
	require.Equal(t, "critical", SevCritical.label())
	require.Equal(t, "unknown", Severity(99).label())
}

// TestDegradeReason_IsOrderIndependent asserts the persisted reason names every critical failure
// and is identical for the same failure set registered in either order — the reason string ends up
// in state/contract.json and in /qompack:status, so it must not churn with registration order.
func TestDegradeReason_IsOrderIndependent(t *testing.T) {
	a := Result{ID: CSessionStartFires, Severity: SevCritical, Expected: "fires", Observed: "absent"}
	b := Result{ID: CHookPayloadShape, Severity: SevCritical, Expected: "shape", Observed: "missing session_id"}

	forward := degradeReason([]Result{a, b})
	reversed := degradeReason([]Result{b, a})
	require.Equal(t, forward, reversed)
	require.Contains(t, forward, string(CSessionStartFires))
	require.Contains(t, forward, string(CHookPayloadShape))
	require.Contains(t, forward, "missing session_id")
}

// TestDegradeReason_IgnoresPassingAndNonCriticalResults asserts only OBSERVED critical failures
// reach the reason: a passing critical assertion and a failing warn assertion are both excluded,
// because neither is why the session degraded.
func TestDegradeReason_IgnoresPassingAndNonCriticalResults(t *testing.T) {
	reason := degradeReason([]Result{
		{ID: CSessionStartFires, OK: true, Severity: SevCritical},
		{ID: CPreCompactTiming, OK: false, Severity: SevWarn},
	})
	require.Equal(t, "no critical assertion failed", reason)
}

// TestFailedSummary_ListsEveryFailureWithItsSeverity asserts the LOUD line's kv names warn
// failures alongside the critical one that actually degraded the session, sorted so the summary is
// stable.
func TestFailedSummary_ListsEveryFailureWithItsSeverity(t *testing.T) {
	got := failedSummary([]Result{
		{ID: CSessionStartFires, OK: false, Severity: SevCritical},
		{ID: CPreCompactTiming, OK: false, Severity: SevWarn},
		{ID: CMCPRegistered, OK: true, Severity: SevInfo},
	})
	require.Equal(t, "precompact.has_time_to_write/warn,session_start.fires/critical", got)
}

// TestLatestTS_ReturnsTheNewestStamp asserts the transition timestamp is read from the results
// themselves rather than from a clock the monitor owns (§6.1: time enters through a seam).
func TestLatestTS_ReturnsTheNewestStamp(t *testing.T) {
	require.EqualValues(t, 0, latestTS(nil))
	require.EqualValues(t, 7, latestTS([]Result{{TS: 3}, {TS: 7}, {TS: 5}}))
}
