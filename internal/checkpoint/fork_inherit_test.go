package checkpoint_test

// A fork continues its parent's conversation, so the parent's session-scoped eliminations up to the
// fork point, and the decisions they mint, are the fork's negative knowledge too (coordinator
// decision D49, finding F-C4-UAT06-1 of the candidate 4 live re-run: the fork's checkpoints 0004
// and 0005 carried eliminated [] and decisions []). They stay attributed to the parent session, and
// a later sibling session still does not see them.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
)

const (
	forkElimApproach = "100 requests per minute"
	forkLateApproach = "a token bucket per process"
	forkSibling      = core.SessionID("sess_sp10_sibling")
)

// parentEliminates records a session-scoped elimination for the fork fixture's parent now.
func parentEliminates(t *testing.T, f *fx, turn core.TurnIndex, approach string) string {
	t.Helper()
	ctx := negknow.WithCaller(f.ctx(), negknow.Caller{Session: forkParent, Turn: turn})
	id, err := f.ledger.Record(ctx, negknow.Record{
		Target: "per-client rate limit", Approach: approach, Reason: "superseded by the user's correction",
		Evidence: core.HashBytes(core.DomainChunk, []byte(approach)),
	})
	require.NoError(t, err)
	return id
}

// hasElimination reports whether cp carries the record id, and the session it is attributed to.
func hasElimination(cp checkpoint.Checkpoint, id string) (core.SessionID, bool) {
	for _, r := range cp.Eliminated {
		if r.ID == id {
			return r.Session, true
		}
	}
	return "", false
}

// TestForkCarriesItsParentsEliminationsUpToTheForkPoint: the parent eliminates, compacts and is
// forked; the fork's checkpoint carries the elimination and its decision, attributed to the parent,
// and not what the parent eliminated after the fork. A sibling session carries neither.
func TestForkCarriesItsParentsEliminationsUpToTheForkPoint(t *testing.T) {
	f := newFx(t)
	openSegment(f, forkParent, 0)
	promptAs(f, forkParent, 0, rateAsk)
	before := parentEliminates(t, f, 1, forkElimApproach)
	parentCp := sealed(t, f, f.precompactAs(forkParent))
	require.Len(t, rejected(parentCp, forkElimApproach), 1, "fixture sanity: the parent's own checkpoint has it")
	require.NoError(t, f.w.Abort(f.w.DraftFor(forkParent)), "the parent session ends")

	f.noteFork(f.sess)
	f.p.Clock.Advance(time.Minute)
	after := parentEliminates(t, f, 5, forkLateApproach) // the parent, resumed after the fork

	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, forkFirstPrompt)
	cp := sealed(t, f, f.precompactAs(f.sess))

	who, ok := hasElimination(cp, before)
	require.True(t, ok, "the fork carries its parent's elimination from before the fork: %+v", cp.Eliminated)
	require.Equal(t, forkParent, who, "attributed to the parent session")
	_, ok = hasElimination(cp, after)
	require.False(t, ok, "what the parent eliminated after the fork is not the fork's")
	decs := rejected(cp, forkElimApproach)
	require.Len(t, decs, 1, "and the decision it mints: %+v", cp.Decisions)
	require.Equal(t, rejected(parentCp, forkElimApproach)[0].ID, decs[0].ID, "the parent's decision, same id")
	require.Empty(t, rejected(cp, forkLateApproach))

	openSegment(f, forkSibling, 0)
	promptAs(f, forkSibling, 0, "An unrelated task in the same project.")
	sib := sealed(t, f, f.precompactAs(forkSibling))
	_, ok = hasElimination(sib, before)
	require.False(t, ok, "a sibling session does not inherit the parent's session-scoped record")
	require.Empty(t, rejected(sib, forkElimApproach))
}

// TestAncestryFollowsTheForkChain: a fork of a fork inherits the grandparent's records up to the
// moment its own parent forked, and the parent's up to its own fork point.
func TestAncestryFollowsTheForkChain(t *testing.T) {
	f := newFx(t)
	promptAs(f, forkParent, 0, rateAsk)
	f.noteFork("sess_mid")
	midAt := f.lineageOf("sess_mid").At
	promptAs(f, "sess_mid", 0, forkFirstPrompt)
	f.noteFork(f.sess)
	leafAt := f.lineageOf(f.sess).At

	got := checkpoint.Ancestry(paths.Of(f.p.Root), f.sess)
	require.Equal(t, []negknow.Inherited{
		{Session: "sess_mid", Until: leafAt},
		{Session: forkParent, Until: midAt},
	}, got)
	require.Empty(t, checkpoint.Ancestry(paths.Of(f.p.Root), forkParent), "a session that is no fork inherits nothing")
}
