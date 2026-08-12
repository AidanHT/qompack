package contracttest

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertions of the contracttest suites (subplan table, §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md): a not-yet-implemented assertion yields
// OK: true / SevInfo, a SevCritical failure drives ModeDegradedPassive, and two clean runs restore.
//
// Every case is written against the RULE rather than against StandardAssertions' current content.
// That distinction matters: SP-05 replaces the standard Checks one at a time with real
// observations, at which point "every standard assertion reports not-yet-implemented" stops being
// true — but "a result reporting OK/SevInfo never degrades the session" stays true forever, and it
// is what §12.1 actually requires of the monitor.

// runNotYetImplementedNeverDegradesCase is §12.1's central rule: an assertion whose PRODUCER is
// absent from the build DECLARES its real severity — SevCritical here — and still reports
// OK: true, Severity: SevInfo. RunAll must read the observed severity, never the declared one, so
// the session stays ModeFull. Getting this backwards would put every wave-1 and wave-2
// verification run into degraded-passive and silently disable the paths those waves are testing.
func runNotYetImplementedNeverDegradesCase(t *testing.T, factory func(t *testing.T) contract.Monitor) {
	t.Helper()
	m := factory(t)

	require.NoError(t, m.Register(notYetImplementedAssertion(contract.CMCPRegistered)))
	require.NoError(t, m.Register(notYetImplementedAssertion(contract.CAdditionalContext)))

	results, mode := m.RunAll(context.Background(), suiteEnv())

	require.Equal(t, contract.ModeFull, mode,
		"an assertion whose producer is absent from the build must never degrade the session (§12.1)")
	require.Equal(t, contract.ModeFull, m.Mode())
	require.Len(t, results, 2)
	for _, r := range results {
		require.True(t, r.OK)
		require.Equal(t, contract.SevInfo, r.Severity)
		require.Equal(t, notYetImplementedObserved, r.Observed)
		require.Equal(t, core.NowMilli(suiteClock{}), r.TS,
			"a Result must be stamped from Env.Clock, never from wall time (§6.1)")
	}
}

// runCriticalFailureDegradesCase asserts the degradation half of §12.1: one observed SevCritical
// failure is enough, the mode RunAll returns agrees with the mode Mode() reports afterwards, and
// Report() carries the failure so /qompack:status can lead with a banner naming it.
func runCriticalFailureDegradesCase(t *testing.T, factory func(t *testing.T) contract.Monitor) {
	t.Helper()
	m := factory(t)

	require.NoError(t, m.Register(alwaysAssertion(contract.CTranscriptReadable, true, contract.SevWarn)))
	require.NoError(t, m.Register(alwaysAssertion(contract.CSessionStartFires, false, contract.SevCritical)))

	results, mode := m.RunAll(context.Background(), suiteEnv())

	require.Equal(t, contract.ModeDegradedPassive, mode,
		"an observed SevCritical failure must degrade to passive recording (§12.1)")
	require.Equal(t, contract.ModeDegradedPassive, m.Mode(),
		"the mode RunAll returns must be the mode the Monitor is actually in")
	require.Len(t, results, 2)

	report := m.Report()
	require.Len(t, report, 2)
	var found bool
	for _, r := range report {
		if r.ID == contract.CSessionStartFires {
			found = true
			require.False(t, r.OK)
			require.Equal(t, contract.SevCritical, r.Severity)
		}
	}
	require.True(t, found, "Report must carry the failure that degraded the session")
}

// runWarnFailureDoesNotDegradeCase asserts the severity gate. §12.1 degrades on SevCritical and
// only on SevCritical: a failing SevWarn assertion (PreCompact timing, custom_instructions,
// transcript readability, plugin root) is surfaced but never turns the session passive, because
// none of those failures makes Qompack harmful — only less useful.
func runWarnFailureDoesNotDegradeCase(t *testing.T, factory func(t *testing.T) contract.Monitor) {
	t.Helper()
	m := factory(t)

	require.NoError(t, m.Register(alwaysAssertion(contract.CPreCompactTiming, false, contract.SevWarn)))
	require.NoError(t, m.Register(alwaysAssertion(contract.CMCPRegistered, false, contract.SevInfo)))

	_, mode := m.RunAll(context.Background(), suiteEnv())

	require.Equal(t, contract.ModeFull, mode, "a failing SevWarn or SevInfo assertion must not degrade the session")
	require.Equal(t, contract.ModeFull, m.Mode())
}

// runTwoCleanRunsRestoreCase asserts §12.1's recovery rule, including the part that is easy to get
// wrong: the FIRST clean run after a degradation does not restore. If it did, a flapping host
// contract would toggle the session between acting and not acting on every SessionStart, which is
// worse than staying passive.
func runTwoCleanRunsRestoreCase(t *testing.T, factory func(t *testing.T) contract.Monitor) {
	t.Helper()
	m := factory(t)

	a, set := flippableAssertion(contract.CHookPayloadShape)
	require.NoError(t, m.Register(a))
	ctx := context.Background()

	set(false)
	_, mode := m.RunAll(ctx, suiteEnv())
	require.Equal(t, contract.ModeDegradedPassive, mode)

	set(true)
	_, mode = m.RunAll(ctx, suiteEnv())
	require.Equal(t, contract.ModeDegradedPassive, mode,
		"one clean run is not enough: §12.1 requires TWO consecutive clean runs before Restore")

	_, mode = m.RunAll(ctx, suiteEnv())
	require.Equal(t, contract.ModeFull, mode, "the second consecutive clean run must restore (§12.1)")
	require.Equal(t, contract.ModeFull, m.Mode())

	// And the streak must be consecutive: a further failure followed by one clean run does not
	// restore.
	set(false)
	_, mode = m.RunAll(ctx, suiteEnv())
	require.Equal(t, contract.ModeDegradedPassive, mode)
	set(true)
	_, mode = m.RunAll(ctx, suiteEnv())
	require.Equal(t, contract.ModeDegradedPassive, mode,
		"the clean-run streak must reset on every further failure")
}

// runRegistrationOrderCase asserts results come back in registration order. §12.1 has the monitor
// run on every SessionStart and persist its results; a stable order is what makes two runs
// diffable and what lets /qompack:status render the same banner twice.
func runRegistrationOrderCase(t *testing.T, factory func(t *testing.T) contract.Monitor) {
	t.Helper()
	m := factory(t)

	want := []contract.ID{
		contract.CPluginRootResolves,
		contract.CSessionStartSourceCompact,
		contract.CPreCompactCustomInstr,
	}
	for _, id := range want {
		require.NoError(t, m.Register(alwaysAssertion(id, true, contract.SevInfo)))
	}

	results, _ := m.RunAll(context.Background(), suiteEnv())

	got := make([]contract.ID, len(results))
	for i, r := range results {
		got[i] = r.ID
	}
	require.Equal(t, want, got)
}

// runReportReflectsLastRunCase asserts Report is the most recent run's results, not an accumulating
// log: /qompack:status shows the CURRENT state of the host contract, and a report that grew without
// bound would show a fixed failure forever.
func runReportReflectsLastRunCase(t *testing.T, factory func(t *testing.T) contract.Monitor) {
	t.Helper()
	m := factory(t)

	a, set := flippableAssertion(contract.CSessionStartFires)
	require.NoError(t, m.Register(a))
	ctx := context.Background()

	set(false)
	_, _ = m.RunAll(ctx, suiteEnv())
	require.Len(t, m.Report(), 1)
	require.False(t, m.Report()[0].OK)

	set(true)
	_, _ = m.RunAll(ctx, suiteEnv())
	require.Len(t, m.Report(), 1, "Report is the last run's results, not an accumulating log")
	require.True(t, m.Report()[0].OK, "Report must reflect the most recent run")
}

// runRegisterRefusesUnusableCase asserts Register's error return does real work. An assertion with
// no ID or no Check could never produce a usable Result; refusing it at registration keeps the
// failure off the SessionStart hot path, where the only alternative is a panic inside RunAll.
func runRegisterRefusesUnusableCase(t *testing.T, factory func(t *testing.T) contract.Monitor) {
	t.Helper()
	m := factory(t)

	require.Error(t, m.Register(contract.Assertion{}), "an assertion with no ID must be refused")
	require.Error(t, m.Register(contract.Assertion{ID: probeID, Severity: contract.SevCritical}),
		"an assertion with no Check must be refused")

	results, mode := m.RunAll(context.Background(), suiteEnv())
	require.Empty(t, results, "a refused assertion must not have been registered")
	require.Equal(t, contract.ModeFull, mode)
}

// runHistoryRecordThenSawCase is History's core promise: an observation that was recorded is one
// the next assertion can see. §12.1's cross-session assertions ("absence across two sessions",
// "evaluated on the FOLLOWING start") are unimplementable without it.
func runHistoryRecordThenSawCase(t *testing.T, factory func(t *testing.T) contract.History) {
	t.Helper()
	h := factory(t)

	require.False(t, h.Saw(contract.CSessionStartFires), "a fresh History must not claim to have seen anything")

	h.Record(contract.CSessionStartFires, core.NowMilli(suiteClock{}))

	require.True(t, h.Saw(contract.CSessionStartFires))
	require.False(t, h.Saw(contract.CMCPRegistered), "recording one ID must not mark every ID as seen")
}

// runHistoryLastSeenCase asserts LastSeen returns the instant that was recorded, and that a repeat
// observation advances it: §12.1's PreCompact-then-SessionStart assertion is evaluated by comparing
// two recorded instants, so a LastSeen frozen at the first observation would make it unanswerable.
func runHistoryLastSeenCase(t *testing.T, factory func(t *testing.T) contract.History) {
	t.Helper()
	h := factory(t)

	first := core.NowMilli(suiteClock{})
	h.Record(contract.CPreCompactTiming, first)

	got, ok := h.LastSeen(contract.CPreCompactTiming)
	require.True(t, ok)
	require.Equal(t, first, got)

	later := first + 1
	h.Record(contract.CPreCompactTiming, later)

	got, ok = h.LastSeen(contract.CPreCompactTiming)
	require.True(t, ok)
	require.Equal(t, later, got, "a later observation must advance LastSeen")
}

// runHistoryUnknownIDCase asserts the absent case is reported as absent rather than as a zero
// timestamp: an assertion that cannot distinguish "never seen" from "seen at the epoch" would
// report a contract violation on every first run.
func runHistoryUnknownIDCase(t *testing.T, factory func(t *testing.T) contract.History) {
	t.Helper()
	h := factory(t)

	require.False(t, h.Saw(contract.CPluginRootResolves))
	ts, ok := h.LastSeen(contract.CPluginRootResolves)
	require.False(t, ok, "LastSeen must report false for an ID it never saw")
	require.Zero(t, ts)
}

// runHistorySessionsMonotoneCase asserts Sessions is a count, not a flag: it is never negative and
// never decreases while a History is alive. §5.19 does not fix what a fresh History reports — SP-05
// decides whether the current session counts before or after it is recorded — so this asserts the
// property every implementation must have rather than a number no section states.
func runHistorySessionsMonotoneCase(t *testing.T, factory func(t *testing.T) contract.History) {
	t.Helper()
	h := factory(t)

	before := h.Sessions()
	require.GreaterOrEqual(t, before, 0)

	h.Record(contract.CSessionStartFires, core.NowMilli(suiteClock{}))
	h.Record(contract.CTranscriptReadable, core.NowMilli(suiteClock{}))

	require.GreaterOrEqual(t, h.Sessions(), before, "Sessions must never decrease")
}
