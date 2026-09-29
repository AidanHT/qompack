package checkpoint_test

// The checkpoint half of the V6 live lane's retrieval D5: a checkpoint sealed after an active
// elimination must carry it and its rejected-alternative decision, and a session's decisions come
// only from the eliminations that session's checkpoint carries. internal/cli's
// TestLivePreCompactCarriesEliminationAndDecision drives the same through the daemon.

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
)

// sealed reads the checkpoint a PreCompact sealed.
func (f *fx) sealed(t *testing.T, seq core.CheckpointSeq) checkpoint.Checkpoint {
	t.Helper()
	r, err := checkpoint.OpenReader(f.p.Root, f.p.Log, obs.New(f.p.Clock))
	require.NoError(t, err)
	cp, _, err := r.Get(f.ctx(), seq)
	require.NoError(t, err)
	return cp
}

// rejected returns the decisions whose rejected alternative is approach.
func rejected(cp checkpoint.Checkpoint, approach string) []checkpoint.Decision {
	var out []checkpoint.Decision
	for _, d := range cp.Decisions {
		if len(d.AlternativesRejected) == 1 && d.AlternativesRejected[0] == approach {
			out = append(out, d)
		}
	}
	return out
}

// TestPreCompactSealsEliminationsRecordedAfterTheDraftBegan: a live draft that has not re-read the
// ledger since it began (no segment closed since) still seals what the ledger holds at the seal.
func TestPreCompactSealsEliminationsRecordedAfterTheDraftBegan(t *testing.T) {
	f := newFx(t)
	seedForPreCompact(t, f) // begins the live draft and encodes both closed segments

	ctx := negknow.WithCaller(f.ctx(), negknow.Caller{Session: f.sess, Turn: 12})
	id, err := f.ledger.Record(ctx, negknow.Record{
		Target: "src/pool.go:DialPool", Approach: "widen pool timeout", Reason: "max_idle caps it",
		Evidence: core.HashBytes(core.DomainChunk, []byte("pool evidence")),
	})
	require.NoError(t, err)

	res, err := f.w.PreCompact(f.ctx(), f.precompactInput())
	require.NoError(t, err)
	cp := f.sealed(t, res.Ref.Seq)

	var carried bool
	for _, r := range cp.Eliminated {
		carried = carried || r.ID == id
	}
	require.True(t, carried, "the elimination recorded before the seal must be sealed: %+v", cp.Eliminated)
	decs := rejected(cp, "widen pool timeout")
	require.Len(t, decs, 1, "and so must the decision it is: %+v", cp.Decisions)
	require.Equal(t, core.TurnIndex(12), decs[0].Turn, "placed at the turn it was recorded at")
	require.Equal(t, "max_idle caps it", decs[0].Why)
}

// TestAdvanceDecisionsComeOnlyFromTheSessionsOwnEliminations: another session's session-scoped
// elimination is not this session's negative knowledge, so it is neither carried nor a decision;
// a project-scoped one is both.
func TestAdvanceDecisionsComeOnlyFromTheSessionsOwnEliminations(t *testing.T) {
	f := newFx(t)
	f.elim(f.sess, negknow.ScopeSession, "src/a.go", "inline the helper", "it recurses")
	f.elim("sess_other", negknow.ScopeProject, "src/b.go", "drop the cache", "load-bearing")
	f.elim("sess_other", negknow.ScopeSession, "src/c.go", "retry forever", "it never converges")
	f.tool("tu_d5", 2, "Read", "src/a.go", "package a", false)
	f.closedSeg(1, 0, 5)

	d := f.begin()
	f.advance(d, 1)
	_, cp := f.persisted()

	require.Len(t, rejected(cp, "inline the helper"), 1, "the session's own elimination is a decision")
	require.Len(t, rejected(cp, "drop the cache"), 1, "a project-scoped elimination is a decision")
	require.Empty(t, rejected(cp, "retry forever"),
		"another session's session-scoped elimination must not become this session's decision")
}

// TestPreCompactDecisionsAreTheSessionsOwnNotEveryCarriedElimination: the seal-time refresh adds
// the decisions an Advance over the unencoded range would have — this session's own eliminations
// at or after the draft's frontier — and no more. Every project-scoped elimination another session
// recorded is carried in eliminated[], but its node turn is in THAT session's numbering, so minting
// all of them as decisions at every seal let 64 foreign ones at high turns fill the Turn-descending
// cap (maxDraftDecisions) and push the session's own decision out of the sealed checkpoint.
func TestPreCompactDecisionsAreTheSessionsOwnNotEveryCarriedElimination(t *testing.T) {
	f := newFx(t)
	seedForPreCompact(t, f) // begins the live draft; the frontier is turn 9

	const foreign = 70 // more than maxDraftDecisions (64)
	foreignIDs := map[string]bool{}
	for i := range foreign {
		ctx := negknow.WithCaller(f.ctx(), negknow.Caller{Session: "sess_other", Turn: core.TurnIndex(500 + i)})
		target := fmt.Sprintf("src/other%02d.go", i)
		id, err := f.ledger.Record(ctx, negknow.Record{
			Scope: negknow.ScopeProject, Target: target, Approach: "foreign approach", Reason: "another session's",
			Evidence: core.HashBytes(core.DomainChunk, []byte(target)),
		})
		require.NoError(t, err)
		foreignIDs[id] = true
	}
	ctx := negknow.WithCaller(f.ctx(), negknow.Caller{Session: f.sess, Turn: 12})
	_, err := f.ledger.Record(ctx, negknow.Record{
		Target: "src/pool.go:DialPool", Approach: "widen pool timeout", Reason: "max_idle caps it",
		Evidence: core.HashBytes(core.DomainChunk, []byte("pool evidence")),
	})
	require.NoError(t, err)

	// A token budget wide enough that the decision cap, not Truncate, is what decides which
	// decisions are sealed: at the default budget the 70 foreign decisions overflow it and Truncate
	// drops every decision, which hides the same loss behind a different mechanism.
	in := f.precompactInput()
	in.Budget = 1 << 20
	res, err := f.w.PreCompact(f.ctx(), in)
	require.NoError(t, err)
	cp := f.sealed(t, res.Ref.Seq)

	carried := 0
	for _, r := range cp.Eliminated {
		if foreignIDs[r.ID] {
			carried++
		}
	}
	require.Equal(t, foreign, carried, "every project-scoped elimination is still carried as negative knowledge")
	require.Len(t, rejected(cp, "widen pool timeout"), 1,
		"the session's own decision must survive the seal: %d decisions sealed", len(cp.Decisions))
	require.Empty(t, rejected(cp, "foreign approach"),
		"another session's eliminations are not minted as this session's decisions at the seal")
}
