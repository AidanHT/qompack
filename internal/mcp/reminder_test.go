package mcp_test

import (
	"strconv"
	"sync"
	"testing"

	"github.com/qompack/qompack/internal/mcp"
	"github.com/stretchr/testify/require"
)

// TestReminderBudget_CapsThePerSessionTotal pins that the bound is on what a reader experiences
// across the whole session, not per trigger: forty touched files must not become forty reminders.
func TestReminderBudget_CapsThePerSessionTotal(t *testing.T) {
	t.Parallel()

	b := mcp.NewReminderBudget(3)
	require.Equal(t, 3, b.Remaining())

	for i := 0; i < 3; i++ {
		got, why := b.Offer("key"+strconv.Itoa(i), mcp.TriggerChangedContext)
		require.Equal(t, mcp.SuppressNone, why)
		require.Equal(t, "key"+strconv.Itoa(i), got.Key)
		require.Equal(t, mcp.TriggerChangedContext, got.Trigger)
		require.Equal(t, 2-i, got.Remaining, "the reminder carries its own countdown")
	}

	require.Zero(t, b.Remaining())
	got, why := b.Offer("key3", mcp.TriggerChangedContext)
	require.Equal(t, mcp.SuppressOverBudget, why)
	require.Zero(t, got, "a suppressed offer returns the zero Reminder")
}

// TestReminderBudget_NeverRemindsTwiceAboutOneThing pins that a repeat costs no budget and carries
// no information: repetition is how a warning stops being read.
func TestReminderBudget_NeverRemindsTwiceAboutOneThing(t *testing.T) {
	t.Parallel()

	b := mcp.NewReminderBudget(3)
	_, why := b.Offer("k", mcp.TriggerRepeatedError)
	require.Equal(t, mcp.SuppressNone, why)

	for i := 0; i < 5; i++ {
		_, why = b.Offer("k", mcp.TriggerRepeatedError)
		require.Equal(t, mcp.SuppressDuplicate, why)
	}
	require.Equal(t, 2, b.Remaining(), "five duplicates spent nothing")
	require.Equal(t, 5, b.Report().Suppressed[mcp.SuppressDuplicate])
}

// TestReminderBudget_NeverRemindsAboutItsOwnOutput is the feedback-loop cut §2 names. It is the
// most important test in the file: reminders become context, changed context is a trigger, and a
// trigger produces reminders.
func TestReminderBudget_NeverRemindsAboutItsOwnOutput(t *testing.T) {
	t.Parallel()

	b := mcp.NewReminderBudget(3)
	b.MarkOwnOutput("qompack-injection")

	got, why := b.Offer("qompack-injection", mcp.TriggerChangedContext)
	require.Equal(t, mcp.SuppressOwnOutput, why)
	require.Zero(t, got)
	require.Equal(t, 3, b.Remaining(), "the loop is cut before any budget is spent")

	// Marking after the fact does not un-emit, but it does stop the next one.
	_, why = b.Offer("other", mcp.TriggerChangedContext)
	require.Equal(t, mcp.SuppressNone, why)
	b.MarkOwnOutput("other")
	_, why = b.Offer("other", mcp.TriggerChangedContext)
	require.Equal(t, mcp.SuppressOwnOutput, why,
		"own-output is checked before duplication, so the loop reason is the one reported")
}

// TestReminderBudget_OwnOutputIsCheckedFirstEvenWhenOverBudget pins the check order end to end: a
// spent budget must not mask the loop, because the reason a reader is told matters.
func TestReminderBudget_OwnOutputIsCheckedFirstEvenWhenOverBudget(t *testing.T) {
	t.Parallel()

	b := mcp.NewReminderBudget(0)
	b.MarkOwnOutput("mine")

	_, why := b.Offer("mine", mcp.TriggerChangedContext)
	require.Equal(t, mcp.SuppressOwnOutput, why)

	_, why = b.Offer("theirs", mcp.TriggerChangedContext)
	require.Equal(t, mcp.SuppressOverBudget, why)
}

// TestReminderBudget_ZeroMeansEmitNone pins that a zero cap is honoured rather than raised. It is
// the setting a user picks to turn the channel off while leaving the rest of phase 7 on, so
// emitting even one would override an explicit choice.
func TestReminderBudget_ZeroMeansEmitNone(t *testing.T) {
	t.Parallel()

	for _, limit := range []int{0, -1} {
		b := mcp.NewReminderBudget(limit)
		require.Zero(t, b.Remaining())
		got, why := b.Offer("k", mcp.TriggerChangedContext)
		require.Equal(t, mcp.SuppressOverBudget, why)
		require.Zero(t, got)
		require.Empty(t, b.Report().Emitted)
	}
}

// TestReminderBudget_SuppressionIsCountedNeverSilent pins the report: a session that generated two
// hundred candidates and emitted three is a different thing from one that generated three.
func TestReminderBudget_SuppressionIsCountedNeverSilent(t *testing.T) {
	t.Parallel()

	b := mcp.NewReminderBudget(2)
	b.MarkOwnOutput("own-a")
	b.MarkOwnOutput("own-b")

	b.Offer("own-a", mcp.TriggerChangedContext)
	b.Offer("own-b", mcp.TriggerChangedContext)
	b.Offer("real-1", mcp.TriggerUnresolvedReference)
	b.Offer("real-1", mcp.TriggerUnresolvedReference)
	b.Offer("real-2", mcp.TriggerApplicableElimination)
	b.Offer("real-3", mcp.TriggerRepeatedError)
	b.Offer("real-4", mcp.TriggerRepeatedError)

	rep := b.Report()
	require.Equal(t, 2, rep.Limit)
	require.Len(t, rep.Emitted, 2)
	require.Equal(t, "real-1", rep.Emitted[0].Key, "emission order is preserved")
	require.Equal(t, "real-2", rep.Emitted[1].Key)
	require.Equal(t, mcp.TriggerApplicableElimination, rep.Emitted[1].Trigger)

	require.Equal(t, 2, rep.Suppressed[mcp.SuppressOwnOutput])
	require.Equal(t, 1, rep.Suppressed[mcp.SuppressDuplicate])
	require.Equal(t, 2, rep.Suppressed[mcp.SuppressOverBudget])
	require.Equal(t, 5, rep.TotalSuppressed())
	require.Equal(t, 2, rep.OwnOutputKeys,
		"the own-output count is what makes its suppression count interpretable")

	require.Equal(t, []string{"own-a", "own-b"}, b.OwnOutput())
}

// TestReminderBudget_ReportIsACopy pins that a caller may hold a report while the session
// continues, since the budget keeps being written by other handlers.
func TestReminderBudget_ReportIsACopy(t *testing.T) {
	t.Parallel()

	b := mcp.NewReminderBudget(3)
	b.Offer("a", mcp.TriggerChangedContext)
	rep := b.Report()

	b.Offer("b", mcp.TriggerChangedContext)
	b.Offer("a", mcp.TriggerChangedContext)

	require.Len(t, rep.Emitted, 1, "the held report did not grow")
	require.Empty(t, rep.Suppressed)
	require.Len(t, b.Report().Emitted, 2)
}

// TestReminderBudget_IsSafeForConcurrentUse pins the mutex and, more usefully, that the cap holds
// exactly under contention: the emitted total is the limit, never one more.
func TestReminderBudget_IsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	const limit = 10
	b := mcp.NewReminderBudget(limit)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				b.Offer("k"+strconv.Itoa(worker)+"-"+strconv.Itoa(j), mcp.TriggerChangedContext)
				b.Remaining()
				b.Report()
			}
		}(i)
	}
	wg.Wait()

	rep := b.Report()
	require.Len(t, rep.Emitted, limit, "exactly the cap was emitted, never one more")
	require.Equal(t, 160-limit, rep.Suppressed[mcp.SuppressOverBudget])
	require.Zero(t, b.Remaining())
}
