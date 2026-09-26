package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
)

// A session.start reaches the drain only from a hook client's spool (drainDispatch): the client
// spooled it because no answer reached it in time — the daemon was not reachable, or its reply
// missed the client's deadline — and the hook had already answered without it (the client's
// deferred note, or {}). So a replayed compact SessionStart's rehydration never reaches the model,
// whatever the replay builds, and its drop report must say so rather than describe it as delivered.
// It also replaces the report a late live answer left, which described as delivered a rehydration
// the client had already given up on.

// drainWithin runs dd.drainDispatch(req) and returns its response, or fails the test with msg if it
// has not answered within bound.
func drainWithin(t *testing.T, dd *daemon, req ipc.Request, bound time.Duration, msg string) ipc.Response {
	t.Helper()
	ch := make(chan ipc.Response, 1)
	go func() { ch <- dd.drainDispatch(context.Background(), req) }()
	select {
	case r := <-ch:
		return r
	case <-time.After(bound):
		require.FailNow(t, msg, "no answer within %s", bound)
		return ipc.Response{}
	}
}

// TestSessionStartCompact_ReplayedRequestRecordsItsRehydrationUndelivered: the replay runs the
// route's side effects and the rehydration, but the rehydration is never delivered — its ticket is
// abandoned before it starts, with the replay as the reason the drop report gives — and the replay
// does not wait for it, since nobody is waiting for the replay's answer either. It is not a deferral
// the daemon answered a host with, so it is not counted or Loud'd as one.
func TestSessionStartCompact_ReplayedRequestRecordsItsRehydrationUndelivered(t *testing.T) {
	type outcome struct {
		delivered bool
		why       string
	}
	got := make(chan outcome, 1)
	f := newCompactFixture(t, func(ctx context.Context, _ *compactFixture) (hookio.Output, error) {
		tk := compactTicketFrom(ctx)
		if tk == nil {
			got <- outcome{delivered: true, why: "no ticket"}
			return hookio.Empty(), nil
		}
		out := hookio.SessionStartOutput("<!-- qompack:injected seq=1 ver=1 -->replayed<!-- /qompack:injected -->")
		delivered := tk.offer(out)
		got <- outcome{delivered: delivered, why: tk.undeliveredReason()}
		return out, nil
	})
	f.dd.compactBudget = compactTestBound + time.Minute // only a replay that does not wait answers in time

	resp := drainWithin(t, f.dd, compactRequest(f.dd.root, "sess-replayed"), compactTestBound,
		"a replayed compact SessionStart must not wait for a rehydration nobody will receive")
	require.True(t, resp.OK)
	ac := additionalContext(resp.Output)
	require.NotContains(t, ac, "replayed<!--", "the replay's answer never carries the rehydration: %q", ac)

	select {
	case o := <-got:
		require.False(t, o.delivered, "a replayed request's rehydration is never delivered")
		require.Equal(t, undeliveredReplayed, o.why, "and its drop report says why")
	case <-time.After(compactTestBound):
		t.Fatal("the replay never ran the rehydration, so nothing corrects the drop report")
	}
	require.Zero(t, f.dd.m.Snapshot().Counters[counterCompactDeferred],
		"a replay's answer reaches no host, so it is not counted as a deferred answer")
	for _, m := range f.log.msgs(logLoud) {
		require.False(t, strings.Contains(m, "answered without its rehydration"), "nor Loud'd as one: %q", m)
	}
}

// TestSessionStartCompact_LiveRequestIsNotAReplay: the same request dispatched live (not through
// the drain) is answered with its rehydration, delivered as before.
func TestSessionStartCompact_LiveRequestIsNotAReplay(t *testing.T) {
	f := newCompactFixture(t, func(ctx context.Context, _ *compactFixture) (hookio.Output, error) {
		return hookio.SessionStartOutput("<!-- qompack:injected seq=1 ver=1 -->live<!-- /qompack:injected -->"), nil
	})
	resp, _ := dispatchWithin(t, f.dd, compactRequest(f.dd.root, "sess-live"), compactTestBound, "no answer")
	require.Contains(t, additionalContext(resp.Output), "live<!--")
}
