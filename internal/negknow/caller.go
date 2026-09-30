package negknow

import (
	"context"

	"github.com/qompack/qompack/internal/core"
)

// The caller one ledger operation is made on behalf of.
//
// The resident daemon opens ONE ledger per project and serves every session of that project
// through it, so the session a record belongs to, and the session whose session-scoped records a
// question may see, cannot be fixed at Open the way Deps.Session fixes them. Before this, the
// daemon's ledger was opened with no session at all: every session-scoped elimination was written
// with "session":"" and answered for every later session of the project (retrieval D2 of the V6
// live lane, plans/sdd/V6-closeout/live/report.md).
//
// The caller therefore travels on the context of each call — the channel the daemon already uses
// for request-scoped identity (observer.WithObservation, daemon.ServicesFrom) — and a call that
// carries none falls back to Deps.Session, which is what a ledger bound to one session has always
// used. The §5.10 Ledger interface is unchanged.

// callerKey is the context key a Caller is stored under.
type callerKey struct{}

// Caller is the session and turn one ledger operation is made on behalf of.
type Caller struct {
	// Session is the session the operation belongs to: the session a Record is written for, and
	// the session whose session-scoped records Query, Active and TopActive may answer from.
	Session core.SessionID
	// Turn is the caller's current turn index, 0 when it is not known. Record stamps it on the
	// elimination node it emits into the DAG, which is what places the record in a checkpoint's
	// turn-ordered decision extraction (checkpoint.ExtractDecisions' from-turn cut).
	Turn core.TurnIndex
}

// WithCaller attaches c to ctx. A Caller with no Session leaves ctx unchanged: a caller that does
// not know its session must not erase one an outer layer attached, and "no session" is already
// what an unattached context means.
func WithCaller(ctx context.Context, c Caller) context.Context {
	if c.Session == "" {
		return ctx
	}
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom returns the Caller attached to ctx, and whether one was.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}

// sessionFor is the session ctx's operation is made for: the attached caller's, else the session
// this ledger was opened for.
func (l *ledger) sessionFor(ctx context.Context) core.SessionID {
	if c, ok := CallerFrom(ctx); ok {
		return c.Session
	}
	return l.deps.Session
}

// viewerFor is who ctx's read is made for: sessionFor's session with its inherited ancestry
// (D49). It is resolved once per read, before the ledger lock, because Deps.Ancestry reads the
// lineage records from disk.
func (l *ledger) viewerFor(ctx context.Context) viewer {
	return l.viewerOf(l.sessionFor(ctx))
}

// turnFor is the turn ctx's operation is made at, 0 when no caller says.
func turnFor(ctx context.Context) core.TurnIndex {
	c, _ := CallerFrom(ctx)
	return c.Turn
}
