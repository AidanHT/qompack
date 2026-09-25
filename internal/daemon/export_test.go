package daemon

import (
	"context"

	"github.com/qompack/qompack/internal/hookio"
)

// Test-only exports for package daemon_test, whose files cannot reach unexported identifiers (see
// rehydrate_service_test.go's PACKAGE CHOICE note for why those tests are external).

// UndeliveredDropKind is undeliveredDropKind.
const UndeliveredDropKind = undeliveredDropKind

// AbandonedCompactContext is ctx carrying a compact ticket the session.start route has already
// abandoned: the route answered with the deferred note before the rehydration was ready.
func AbandonedCompactContext(ctx context.Context) context.Context {
	t := newCompactTicket()
	t.abandon(undeliveredLate)
	return withCompactTicket(ctx, t)
}

// ReplayedCompactContext is ctx carrying the abandoned ticket a compact SessionStart replayed from
// a hook's spool starts its rehydration with.
func ReplayedCompactContext(ctx context.Context) context.Context {
	t := newCompactTicket()
	t.abandon(undeliveredReplayed)
	return withCompactTicket(ctx, t)
}

// UndeliveredReplayed is undeliveredReplayed.
const UndeliveredReplayed = undeliveredReplayed

// PendingCompactContext is ctx carrying a compact ticket the route is still waiting on, and a
// function reporting what, if anything, was offered through it.
func PendingCompactContext(ctx context.Context) (context.Context, func() (hookio.Output, bool)) {
	t := newCompactTicket()
	return withCompactTicket(ctx, t), func() (hookio.Output, bool) {
		select {
		case <-t.ready:
			out, _ := t.result()
			return out, true
		default:
			return hookio.Output{}, false
		}
	}
}

// RouteRehydratesContext is ctx marked as the observer's bookkeeping call beside a rehydration the
// route is building itself.
func RouteRehydratesContext(ctx context.Context) context.Context { return withRouteRehydrates(ctx) }
