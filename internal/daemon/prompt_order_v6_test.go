package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/observer"
)

// acceptPrompt WALs and leases a prompt request through the real ingest path, enqueuing its worker
// job on the ring for a test to dispatch by hand (no background workers run under wireTestDaemon).
func acceptPrompt(t *testing.T, dd *daemon, req ipc.Request) {
	t.Helper()
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	require.NoError(t, dd.ing.Accept(req, line))
}

// SP08-D3 issue 1 — the ORDERING hazard PromptFrontier does not close. The follow-up review
// (prompt-critical-review-followup.md §2) confirms st.mu + PromptFrontier prevent turn COLLISION,
// but they do not prove turn ORDER: turn 0 goes to whichever leased arrival is PUBLISHED first, which
// is worker/processing order, not the host arrival order the delivery journal recorded. A later
// arrival published first therefore becomes prompt_<s>_0 — the id the rehydrator serves as the
// verbatim original.
//
// These tests use the real store, real leases (real arrival sequence) and real sidecars. They are
// AUTHORED but not run until dist/v6-remediation/parallel-tests-ready exists.

// TestPromptOrder_V6_OrderingGateGivesTurnZeroToEarliestLeasedArrival is the inverted defect test
// (original id TestPromptOrder_V6_LaterLeasedArrivalPublishedFirstTakesTurnZero), asserting the
// DESIRED behaviour at the ACTUAL dispatch layer (ingest.dispatch's ordering gate). Two same-session
// prompts leased in host order (P0 arrival N, P1 arrival N+1); the later arrival's worker dispatches
// FIRST, but the gate DEFERS it because its earlier same-session arrival is unacknowledged — so it
// publishes nothing and never takes turn 0. The earlier arrival takes turn 0; the deferred later one
// is retried (its WAL bytes untouched) and takes turn 1.
func TestPromptOrder_V6_OrderingGateGivesTurnZeroToEarliestLeasedArrival(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })

	ctx := context.Background()
	const sess core.SessionID = "sess-order"
	acceptPrompt(t, dd, spD3Prompt(dd, root, sess, testDeliveryToken('a'), "first"))  // arrival N
	acceptPrompt(t, dd, spD3Prompt(dd, root, sess, testDeliveryToken('b'), "second")) // arrival N+1

	// Accept enqueues jobs in arrival order; a test dispatches them by hand (no background workers).
	jobEarly := <-dd.ing.ring
	jobLate := <-dd.ing.ring
	require.Less(t, jobEarly.lease.ArrivalSeq, jobLate.lease.ArrivalSeq, "the first accept is the earlier arrival")

	// The later arrival's worker wins the race and dispatches FIRST — the gate defers it.
	dd.ing.dispatch(ctx, dd.runIngested, jobLate)
	_, err := o.Store.ToolUse(ctx, observer.VerbatimPromptID(sess, 0))
	require.ErrorIs(t, err, core.ErrNotFound, "the deferred later arrival did not take turn 0")
	require.Positive(t, dd.m.Counter(counterOrderingDeferred).Value(), "the block is counted, not spun on")

	// The earlier arrival dispatches and takes turn 0, acknowledging arrival N.
	dd.ing.dispatch(ctx, dd.runIngested, jobEarly)
	require.Equal(t, "first", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 0)))

	// The deferred later arrival, retried once its predecessor is acknowledged, takes turn 1 — never 0.
	dd.ing.dispatch(ctx, dd.runIngested, jobLate)
	require.Equal(t, "second", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 1)))
	require.Equal(t, "first", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 0)),
		"turn 0 holds the earliest leased arrival, not whichever worker published first")
}

// TestPromptOrder_V6_UnleasedEarlierSpooledIsSeparateUncertainty documents the SEPARATE
// order/coverage uncertainty the task calls out: an earlier prompt that was SPOOLED but not yet
// leased is invisible to the daemon, so a later LIVE prompt leased and published first legitimately
// takes turn 0. The daemon cannot infer host order from worker order here, and no per-arrival gate
// can help because the earlier delivery has no arrival sequence yet. This pins the current behaviour
// as a KNOWN, unprovable-order case — NOT something the fail-closed fix resolves.
func TestPromptOrder_V6_UnleasedEarlierSpooledIsSeparateUncertainty(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })

	ctx := context.Background()
	const sess core.SessionID = "sess-unleased"

	// The host-later prompt is live: leased and published now.
	live := spD3Prompt(dd, root, sess, testDeliveryToken('c'), "later-live")
	lLive, ok := dd.ing.leaseDelivery(ctx, live)
	require.True(t, ok)
	require.NoError(t, publishCapture(root, live, lLive))
	require.True(t, dd.runIngested(observer.WithObservation(ctx, lLive.ObservationID), live).OK)

	// The host-EARLIER prompt was only spooled; the daemon never saw it before the live one published.
	require.Equal(t, "later-live", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 0)),
		"an unleased earlier prompt cannot claim turn 0: the daemon cannot infer host order from worker order")
}
