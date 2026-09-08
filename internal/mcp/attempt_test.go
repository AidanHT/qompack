package mcp_test

import (
	"strconv"
	"sync"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/stretchr/testify/require"
)

// failed is the omission a failing retrieval hands Record, so the tests below can assert it
// survives to Status rather than being replaced by a generic message.
var failed = core.Omission{Reason: "object store unreadable", Recovery: "check disk permissions"}

// TestAttemptBudget_NotTriedAndTriedAndFailedAreDifferentAnswers is the reason this file exists.
// A retrieval that was attempted and failed must never be indistinguishable from one nobody made.
func TestAttemptBudget_NotTriedAndTriedAndFailedAreDifferentAnswers(t *testing.T) {
	t.Parallel()

	b := mcp.NewAttemptBudget(3)

	got, why := b.Status("never-asked")
	require.Equal(t, mcp.AttemptNone, got)
	require.False(t, got.Tried())
	require.Equal(t, core.Omission{}, why)

	require.Equal(t, mcp.AttemptFailed, b.Record("asked", mcp.TriggerChangedContext, core.OutcomeUnavailable, failed))

	got, why = b.Status("asked")
	require.Equal(t, mcp.AttemptFailed, got)
	require.True(t, got.Tried(), "a failed attempt is an attempt")
	require.Equal(t, failed, why, "the reason it failed survives to the reader")
}

// TestAttemptBudget_EveryNonOKOutcomeIsAFailedAttempt pins that SP-20's whole outcome vocabulary —
// including OutcomeAbsent, which is the one most likely to be mistaken for success — records a
// failure rather than a success.
func TestAttemptBudget_EveryNonOKOutcomeIsAFailedAttempt(t *testing.T) {
	t.Parallel()

	for _, outcome := range []core.EvidenceOutcome{
		core.OutcomeAbsent, core.OutcomeUnavailable, core.OutcomeDenied,
		core.OutcomeCorrupt, core.OutcomeExpired, core.OutcomeUncertain, "",
	} {
		t.Run(string(outcome), func(t *testing.T) {
			t.Parallel()
			b := mcp.NewAttemptBudget(3)
			require.Equal(t, mcp.AttemptFailed,
				b.Record("k", mcp.TriggerRepeatedError, outcome, core.Omission{}))
			require.Equal(t, 1, b.Report().Failures)
		})
	}

	b := mcp.NewAttemptBudget(3)
	require.Equal(t, mcp.AttemptOK, b.Record("k", mcp.TriggerRepeatedError, core.OutcomeOK, core.Omission{}))
	require.Zero(t, b.Report().Failures)
}

// TestAttemptBudget_ARecordWithNoOmissionStillExplainsItself pins that a caller that fails to say
// why still produces a readable refusal rather than a blank one.
func TestAttemptBudget_ARecordWithNoOmissionStillExplainsItself(t *testing.T) {
	t.Parallel()

	b := mcp.NewAttemptBudget(3)
	b.Record("k", mcp.TriggerUnknown, core.OutcomeDenied, core.Omission{})

	_, why := b.Status("k")
	require.Contains(t, why.Reason, "denied")
	require.Contains(t, why.Recovery, "not an absent result")
}

// TestAttemptBudget_ExhaustionIsAboutTheSearchNotTheThing pins that running out of attempts
// reports AttemptExhausted, never a fallback to absence, and that further attempts are refused
// without spending budget.
func TestAttemptBudget_ExhaustionIsAboutTheSearchNotTheThing(t *testing.T) {
	t.Parallel()

	b := mcp.NewAttemptBudget(2)
	require.True(t, b.Allow("k"))
	require.Equal(t, mcp.AttemptFailed, b.Record("k", mcp.TriggerChangedContext, core.OutcomeUnavailable, failed))
	require.True(t, b.Allow("k"))
	require.Equal(t, mcp.AttemptExhausted, b.Record("k", mcp.TriggerChangedContext, core.OutcomeUnavailable, failed))
	require.False(t, b.Allow("k"), "the cap is spent")

	require.Equal(t, mcp.AttemptExhausted, b.Record("k", mcp.TriggerChangedContext, core.OutcomeUnavailable, failed))

	got, why := b.Status("k")
	require.Equal(t, mcp.AttemptExhausted, got)
	require.True(t, got.Tried())
	require.Equal(t, failed, why)

	rep := b.Report()
	require.Equal(t, 2, rep.Attempts, "the refused attempt spent no budget")
	require.Equal(t, 2, rep.Failures)
	require.Equal(t, 1, rep.Refused, "but it is still counted, because a caller that keeps asking is a signal")
	require.Equal(t, []string{"k"}, rep.Exhausted)
}

// TestAttemptBudget_SuccessEndsTheSearch pins that a key that succeeded stops consuming budget and
// stays AttemptOK, so a later spurious call cannot demote it.
func TestAttemptBudget_SuccessEndsTheSearch(t *testing.T) {
	t.Parallel()

	b := mcp.NewAttemptBudget(2)
	require.Equal(t, mcp.AttemptOK, b.Record("k", mcp.TriggerChangedContext, core.OutcomeOK, core.Omission{}))
	require.False(t, b.Allow("k"), "there is nothing left to retrieve")
	require.Equal(t, mcp.AttemptOK, b.Record("k", mcp.TriggerChangedContext, core.OutcomeUnavailable, failed))

	got, why := b.Status("k")
	require.Equal(t, mcp.AttemptOK, got)
	require.Equal(t, core.Omission{}, why)
	require.Equal(t, 1, b.Report().Attempts)
}

// TestAttemptBudget_ACapBelowOneIsRaisedNotHonoured pins that a caller who bypassed config cannot
// produce a budget that reports failure for work it never allowed.
func TestAttemptBudget_ACapBelowOneIsRaisedNotHonoured(t *testing.T) {
	t.Parallel()

	for _, cap := range []int{0, -1} {
		b := mcp.NewAttemptBudget(cap)
		require.True(t, b.Allow("k"))
		require.Equal(t, mcp.AttemptExhausted,
			b.Record("k", mcp.TriggerUnknown, core.OutcomeUnavailable, failed),
			"one attempt is allowed and it is the last")
		require.Equal(t, 1, b.Report().Attempts)
	}
}

// TestAttemptBudget_TrackingOverflowIsVisible pins the missing-telemetry rule: once the budget
// stops keeping track, an unseen key reports AttemptUnknown rather than AttemptNone, and the
// report says its counts undercount.
func TestAttemptBudget_TrackingOverflowIsVisible(t *testing.T) {
	t.Parallel()

	b := mcp.NewAttemptBudget(1)
	// 8192 is maxTrackedKeys; the loop fills it exactly.
	for i := 0; i < 8192; i++ {
		b.Record("k"+strconv.Itoa(i), mcp.TriggerChangedContext, core.OutcomeOK, core.Omission{})
	}
	require.False(t, b.Report().TrackingOverflowed)
	require.False(t, b.Allow("one-too-many"), "there is no room left to track a new key")

	got, _ := b.Status("one-too-many")
	require.Equal(t, mcp.AttemptNone, got, "before the overflow an unseen key is still untried")

	require.Equal(t, mcp.AttemptUnknown,
		b.Record("one-too-many", mcp.TriggerChangedContext, core.OutcomeOK, core.Omission{}))

	rep := b.Report()
	require.True(t, rep.TrackingOverflowed)
	require.Equal(t, 8192, rep.Keys)
	require.Equal(t, 1, rep.Refused)

	got, why := b.Status("some-other-key")
	require.Equal(t, mcp.AttemptUnknown, got,
		"after the overflow the budget can no longer tell 'nobody asked' from 'we stopped writing it down'")
	require.Contains(t, why.Reason, "overflowed")
	require.Contains(t, why.Recovery, "unverified rather than untried")
}

// TestAttemptBudget_ReportSplitsBurdenByTrigger pins that the burden report says WHICH trigger is
// generating the load, which is what M6-G16-B's "usefulness distinct from frequency" needs.
func TestAttemptBudget_ReportSplitsBurdenByTrigger(t *testing.T) {
	t.Parallel()

	b := mcp.NewAttemptBudget(3)
	b.Record("a", mcp.TriggerRepeatedError, core.OutcomeUnavailable, failed)
	b.Record("a", mcp.TriggerRepeatedError, core.OutcomeUnavailable, failed)
	b.Record("b", mcp.TriggerChangedContext, core.OutcomeOK, core.Omission{})

	rep := b.Report()
	require.Equal(t, 2, rep.ByTrigger[mcp.TriggerRepeatedError])
	require.Equal(t, 1, rep.ByTrigger[mcp.TriggerChangedContext])
	require.Equal(t, 3, rep.Attempts)
	require.Equal(t, 2, rep.Failures)
}

// TestTriggerKind_ValidIsAClosedSetThatAdmitsSilence pins that "the caller did not say why" is a
// recordable state while a misspelling is not.
func TestTriggerKind_ValidIsAClosedSetThatAdmitsSilence(t *testing.T) {
	t.Parallel()

	for _, k := range []mcp.TriggerKind{
		mcp.TriggerUnknown, mcp.TriggerChangedContext, mcp.TriggerUnresolvedReference,
		mcp.TriggerApplicableElimination, mcp.TriggerRepeatedError,
	} {
		require.True(t, k.Valid(), string(k))
	}
	require.False(t, mcp.TriggerKind("changedContext").Valid())
	require.False(t, mcp.TriggerKind("vibes").Valid())
}

// TestAttemptStatus_StringAndZero pins the transcript spellings, and that the zero status is the
// untried one.
func TestAttemptStatus_StringAndZero(t *testing.T) {
	t.Parallel()

	require.Equal(t, mcp.AttemptNone, mcp.AttemptStatus(0))
	for s, want := range map[mcp.AttemptStatus]string{
		mcp.AttemptNone:      "not-tried",
		mcp.AttemptOK:        "ok",
		mcp.AttemptFailed:    "failed",
		mcp.AttemptExhausted: "exhausted",
		mcp.AttemptUnknown:   "unknown",
		mcp.AttemptStatus(9): "unknown",
	} {
		require.Equal(t, want, s.String())
	}
}

// TestAttemptBudget_IsSafeForConcurrentUse pins the mutex, since the budget is written from
// whatever daemon goroutine is serving a call. Run with -race it is the real assertion; without
// it, it at least pins that the counts add up.
func TestAttemptBudget_IsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	b := mcp.NewAttemptBudget(100)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				b.Record("shared", mcp.TriggerChangedContext, core.OutcomeUnavailable, failed)
				b.Allow("shared")
				b.Status("shared")
				b.Report()
			}
		}()
	}
	wg.Wait()

	rep := b.Report()
	require.Equal(t, 100, rep.Attempts, "the cap holds under concurrency")
	require.Equal(t, 300, rep.Refused)
}
