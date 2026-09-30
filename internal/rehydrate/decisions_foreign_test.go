package rehydrate

// V6 close-out wave 14 (coordinator decision D46): the checkpoint ranks a session's own decisions
// before the ones minted from OTHER sessions' project-scoped eliminations, and item 4 must keep
// that rank when it re-sorts by slice score. A foreign decision's Turn is in the other session's
// numbering (569 here against this session's 3 and 8), so an equal-score tie broken by Turn put
// every foreign decision first, and the budget's tail-first cut then dropped this session's own.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/negknow"
)

// foreignDecisionCount is the checkpoint's whole decision cap (64): the foreign decisions alone
// could fill every slot.
const foreignDecisionCount = 64

// rejectedDecision is the decision the checkpoint mints from elimination r at turn: the same what,
// why, evidence and id (checkpoint's eliminationDecision), so a decision and its record pair up the
// way they do in a sealed checkpoint.
func rejectedDecision(r negknow.Record, turn core.TurnIndex) checkpoint.Decision {
	what := fmt.Sprintf("rejected %q for %s", r.Approach, r.Target)
	return checkpoint.Decision{
		ID: checkpoint.MintDecisionID(what, r.Reason, r.Evidence), What: what, Why: r.Reason,
		AlternativesRejected: []string{r.Approach}, Evidence: r.Evidence, Turn: turn,
	}
}

// foreignFixture is a checkpoint of session sess_own holding two own eliminations (turns 3 and 8),
// one decision pin, and 64 project-scoped eliminations of sess_other. Foreign record i is recorded
// later than record i-1 but at a LOWER turn of that session (569-i), so the foreign turns and the
// recorded times disagree completely. The decisions are stored foreign first, a hostile order.
// It returns the checkpoint, the own decision ids in D46's order, and the foreign ids newest
// recorded first.
func foreignFixture() (checkpoint.Checkpoint, []string, []string) {
	cp := ckEmpty()
	cp.Session = "sess_own"
	long := strings.Repeat("the pool is shared with the batch importer, so its size is not ours to change; ", 3)

	var foreignIDs []string
	for i := range foreignDecisionCount {
		target := fmt.Sprintf("src/other%02d.go", i)
		r := negknow.Record{
			ID: fmt.Sprintf("elim_foreign%02d", i), Session: "sess_other", Scope: negknow.ScopeProject,
			Status: negknow.StatusActive, Target: target, Approach: fmt.Sprintf("foreign approach %02d", i),
			Reason: long, Evidence: core.HashBytes(core.DomainChunk, []byte(target)), TS: core.UnixMilli(1000 + i),
		}
		cp.Eliminated = append(cp.Eliminated, r)
		dec := rejectedDecision(r, core.TurnIndex(569-i))
		cp.Decisions = append(cp.Decisions, dec)
		foreignIDs = append([]string{string(dec.ID)}, foreignIDs...)
	}

	var ownIDs []string
	for _, turn := range []core.TurnIndex{8, 3} {
		target := fmt.Sprintf("src/own%d.go", turn)
		r := negknow.Record{
			ID: fmt.Sprintf("elim_own%d", turn), Session: cp.Session, Scope: negknow.ScopeSession,
			Status: negknow.StatusActive, Target: target, Approach: fmt.Sprintf("own approach %d", turn),
			Reason: "this session's own finding", Evidence: core.HashBytes(core.DomainChunk, []byte(target)),
			TS: core.UnixMilli(10 + turn), // recorded BEFORE every foreign record
		}
		cp.Eliminated = append(cp.Eliminated, r)
		dec := rejectedDecision(r, turn)
		cp.Decisions = append(cp.Decisions, dec)
		ownIDs = append(ownIDs, string(dec.ID))
	}
	pin := checkpoint.Decision{ID: "dec_pinned", What: "keep the pool", Why: "it is shared", Turn: 0}
	cp.Decisions = append(cp.Decisions, pin)
	ownIDs = append(ownIDs, string(pin.ID))
	return cp, ownIDs, foreignIDs
}

// TestDecisions_OwnRankBeforeOtherSessionsDecisions pins item 4's order under D46: every own
// decision (by slice score, then Turn, then id) before every foreign one, and the foreign ones by
// their records' recorded time, newest first — even when a foreign decision has the higher slice
// score and a far higher (foreign) turn.
func TestDecisions_OwnRankBeforeOtherSessionsDecisions(t *testing.T) {
	cp, own, foreign := foreignFixture()
	r := requestFor(t, cp, generousTestBudget)
	sc := map[dag.NodeID]float32{dag.DecisionNode(core.DecisionID(foreign[40])): 0.9}

	got := buildDecisions(bg(), r, depsWith(&spyLogger{}), sc)

	require.Equal(t, append(own, foreign...), decisionIDsOf(got))
}

// TestBuild_OwnDecisionsSurviveTheBudgetOverForeignOnes is the injected payload's half of D46: 64
// foreign decisions with long reasons overflow item 4's share of the budget, and what the tail-first
// cut drops is foreign, never one of the session's own decisions.
func TestBuild_OwnDecisionsSurviveTheBudgetOverForeignOnes(t *testing.T) {
	cp, own, foreign := foreignFixture()
	r := requestFor(t, cp, generousTestBudget)

	res, err := Build(bg(), r, fullDeps(t, cp))
	require.NoError(t, err)

	isOwn := map[string]bool{}
	for _, id := range own {
		isOwn[id] = true
		require.Contains(t, res.Text, "- ["+id+"] ", "the session's own decision is injected")
	}
	var dropped []string
	for _, e := range res.Dropped {
		if e.Kind == dropKindDecision {
			dropped = append(dropped, e.ID)
			require.False(t, isOwn[e.ID], "an own decision was dropped for the budget: %s", e.ID)
		}
	}
	require.NotEmpty(t, dropped, "the fixture must overflow item 4's share, or the row proves nothing")
	require.ElementsMatch(t, foreign[len(foreign)-len(dropped):], dropped,
		"the oldest-recorded foreign decisions are the ones dropped")
}
