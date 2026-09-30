package negknow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// A fork continues its parent's conversation (coordinator decision D49, finding F-C4-UAT06-1 of the
// candidate 4 live re-run, plans/sdd/V6-closeout/live/rerun-c4/UAT-06/notes.txt): the parent's
// session-scoped eliminations up to the fork point are visible to the fork, attributed to the
// parent session, while a later sibling session still does not see them.

const (
	inheritParent  core.SessionID = "sess-parent"
	inheritFork    core.SessionID = "sess-fork"
	inheritSibling core.SessionID = "sess-sibling"
	// inheritForkAt is when the fork started, in the TS the records carry.
	inheritForkAt core.UnixMilli = 1_000_000

	inheritTarget        = "per-client rate limit"
	inheritApproach      = "100 requests per minute"
	inheritLaterApproach = "60 requests per minute"
	inheritReason        = "superseded by the user's correction"
	inheritLaterReason   = "superseded again"
)

// forkAncestry is the Deps.Ancestry a daemon wires from the lineage records: sess-fork continues
// sess-parent's conversation up to inheritForkAt; no other session has an ancestry.
func forkAncestry(s core.SessionID) []Inherited {
	if s == inheritFork {
		return []Inherited{{Session: inheritParent, Until: inheritForkAt}}
	}
	return nil
}

// recordAt records a session-scoped elimination for sess at ts.
func recordAt(t *testing.T, l *ledger, sess core.SessionID, ts core.UnixMilli, approach, reason string) string {
	t.Helper()
	r := newRecord(approach, inheritTarget, approach, reason)
	r.TS = ts
	id, err := l.Record(asCaller(sess, 3), r)
	require.NoError(t, err)
	return id
}

// TestQuery_ForkSeesItsParentsEliminationsUpToTheForkPoint: already_tried from the fork answers the
// parent's record from before the fork (attributed to the parent), and not the one after.
func TestQuery_ForkSeesItsParentsEliminationsUpToTheForkPoint(t *testing.T) {
	root, cfg := newProject(t)
	deps := testDeps("", newMetrics())
	deps.Ancestry = forkAncestry
	l := openLedger(t, root, cfg, nil, deps)

	before := recordAt(t, l, inheritParent, inheritForkAt-1, inheritApproach, inheritReason)
	recordAt(t, l, inheritParent, inheritForkAt+1, inheritLaterApproach, inheritLaterReason)

	got, err := l.Query(asCaller(inheritFork, 9), inheritTarget, inheritApproach, ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerActive, got.State, "the fork's conversation holds the parent's elimination")
	require.NotNil(t, got.Record)
	require.Equal(t, before, got.Record.ID)
	require.Equal(t, inheritParent, got.Record.Session, "attributed to the parent session")

	later, err := l.Query(asCaller(inheritFork, 9), inheritTarget, inheritLaterApproach, ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerAbsent, later.State, "what the parent eliminated after the fork is not the fork's")

	sib, err := l.Query(asCaller(inheritSibling, 9), inheritTarget, inheritApproach, ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerAbsent, sib.State, "a sibling session does not see the parent's session-scoped record")

	proj, err := l.Query(asCaller(inheritFork, 9), inheritTarget, inheritApproach, ScopeProject)
	require.NoError(t, err)
	require.Equal(t, AnswerAbsent, proj.State, "a session-scoped record is still not a project-scoped one")
}

// TestActive_ForkListsItsParentsEliminationsUpToTheForkPoint: the rehydration's section 3 reads
// Active/TopActive for the fork.
func TestActive_ForkListsItsParentsEliminationsUpToTheForkPoint(t *testing.T) {
	root, cfg := newProject(t)
	deps := testDeps("", newMetrics())
	deps.Ancestry = forkAncestry
	l := openLedger(t, root, cfg, nil, deps)

	before := recordAt(t, l, inheritParent, inheritForkAt-1, inheritApproach, inheritReason)
	recordAt(t, l, inheritParent, inheritForkAt+1, inheritLaterApproach, inheritLaterReason)
	own := recordAt(t, l, inheritFork, inheritForkAt+2, "45 requests per minute", "the fork's own")

	ids := func(sess core.SessionID) []string {
		recs, err := l.Active(asCaller(sess, 9), ScopeSession)
		require.NoError(t, err)
		out := make([]string, 0, len(recs))
		for _, r := range recs {
			out = append(out, r.ID)
		}
		return out
	}
	require.Equal(t, []string{before, own}, ids(inheritFork))
	require.Empty(t, ids(inheritSibling))
}

// TestQuery_SessionLedgerOfAForkSeesItsParentsEliminations: a ledger opened FOR the fork (Deps.
// Session) holds the inherited records in its view, so its filter answers them after a restart.
func TestQuery_SessionLedgerOfAForkSeesItsParentsEliminations(t *testing.T) {
	root, cfg := newProject(t)
	parent := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	before := recordAt(t, parent, inheritParent, inheritForkAt-1, inheritApproach, inheritReason)
	require.NoError(t, parent.Close())

	deps := testDeps(inheritFork, newMetrics())
	deps.Ancestry = forkAncestry
	fork := openLedger(t, root, cfg, nil, deps)
	got, err := fork.Query(context.Background(), inheritTarget, inheritApproach, ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerActive, got.State)
	require.Equal(t, before, got.Record.ID)
}
