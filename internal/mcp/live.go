package mcp

import (
	"context"

	"github.com/qompack/qompack/internal/core"
)

// The daemon's live view of its sessions, attached to each dispatched call.
//
// Two answers this package gives depend on where a session is RIGHT NOW, and neither the stdio
// process nor the store can say. The stdio process has no session at all (Claude Code hands an MCP
// server no session_id), and the store's segment log learns a segment's end only when the segment
// closes — an open segment reads StartTurn..StartTurn with 0 tokens for as long as it stays open.
// The daemon's observer holds the truth in memory, so the daemon attaches it here (daemon
// dispatchMCPCall) and this package reads it through liveFrom:
//
//   - the ephemeral record a retrieval files for its own answer is placed at the calling session's
//     current turn, re-resolved at the moment the record is written (recordEphemeral). Filed at a
//     turn resolved from the open segment instead, it landed at turn 0 after hook records of later
//     turns, and fsck's index.tool_use row failed after any session that called a tool (F-UAT01-2
//     of the V6 live lane);
//   - `timeline` reports an open segment's live end turn and running token count rather than the
//     0-0 the segment log holds until close (retrieval D8).
//
// Every field is optional and every reader tolerates the zero Live: a handler invoked outside the
// daemon (a slash-command frontend, a test) answers from the store alone, as it always has.

// SessionProgress is where one live session stands, as the daemon's observer holds it.
type SessionProgress struct {
	// Turn is the session's current turn index.
	Turn core.TurnIndex
	// Segment is the segment the observer is enrolling the session's events into, 0 when none.
	Segment core.SegmentID
	// SegmentTokens is what Segment has accumulated so far.
	SegmentTokens core.Tokens
}

// Live is the daemon's live view, attached to one dispatched call.
type Live struct {
	// Turn re-resolves the calling session's current turn at the moment it is asked. It is set
	// only when the daemon resolved the call's turn itself; a caller that supplied its own turn
	// has it used verbatim, and Turn is nil.
	Turn func() core.TurnIndex
	// Progress reports a live session's position. ok is false for a session the daemon does not
	// hold in memory.
	Progress func(core.SessionID) (SessionProgress, bool)
}

// liveKey is the context key a Live is stored under.
type liveKey struct{}

// WithLive attaches l to ctx.
func WithLive(ctx context.Context, l Live) context.Context {
	return context.WithValue(ctx, liveKey{}, l)
}

// liveFrom returns the Live attached to ctx, or the zero Live.
func liveFrom(ctx context.Context) Live {
	l, _ := ctx.Value(liveKey{}).(Live)
	return l
}

// progressOf reports s's live progress when the daemon attached a view that holds it.
func progressOf(ctx context.Context, s core.SessionID) (SessionProgress, bool) {
	f := liveFrom(ctx).Progress
	if f == nil || s == "" {
		return SessionProgress{}, false
	}
	return f(s)
}
