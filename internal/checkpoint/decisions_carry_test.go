package checkpoint_test

// A decision stays in every later checkpoint of its session while it holds, the way eliminations
// carry (coordinator decision D49 of the V6 close-out, finding F-C4-UAT06-2 of the candidate 4
// live re-run, plans/sdd/V6-closeout/live/rerun-c4/UAT-06/steps/checkpoint-0001.json and
// checkpoint-0002.json).
//
// A draft mints decisions only from what it encodes or refreshes at or after its own frontier, and
// the successor a seal opens (or the draft a restarted daemon begins) starts with none: the
// session's second checkpoint sealed decisions [] beside the elimination the first one had minted
// dec_991dbff588ec from, still carried.

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
)

const (
	carryApproach = "100 requests per minute"
	carryPin      = "keep the limiter in-process because a shared store adds a network hop"
)

// sealFirstWithDecision seals the session's first checkpoint over an elimination recorded at turn
// 12 and a decision pin, and returns it. The segment holding turn 12 closes before the compaction,
// so the first checkpoint encodes it and every later segment of the session starts after it.
func sealFirstWithDecision(t *testing.T, f *fx) checkpoint.Checkpoint {
	t.Helper()
	cp := sealFirstUnder(t, f, core.Tokens(f.p.Cfg.Checkpoint.BudgetTokens))
	require.Len(t, rejected(cp, carryApproach), 1, "fixture sanity: the first checkpoint has the decision")
	require.NotEmpty(t, pinned(cp), "fixture sanity: and the pinned one")
	return cp
}

// sealFirstUnder is sealFirstWithDecision's session sealed under budget, with no assertion about
// what the budget left of the decisions.
func sealFirstUnder(t *testing.T, f *fx, budget core.Tokens) checkpoint.Checkpoint {
	t.Helper()
	f.pins.invs = append(f.pins.invs, pins.Invariant{ID: "inv_carry", Text: carryPin, Source: "decision"})
	seedForPreCompact(t, f)
	ctx := negknow.WithCaller(f.ctx(), negknow.Caller{Session: f.sess, Turn: 12})
	_, err := f.ledger.Record(ctx, negknow.Record{
		Target: "per-client rate limit", Approach: carryApproach, Reason: "superseded by the user's correction",
		Evidence: core.HashBytes(core.DomainChunk, []byte("limits evidence")),
	})
	require.NoError(t, err)
	f.closedSeg(3, 10, 12)

	in := f.precompactInput()
	in.Budget = budget
	res, err := f.w.PreCompact(f.ctx(), in)
	require.NoError(t, err)
	return f.sealed(t, res.Ref.Seq)
}

// pinned returns the decisions minted from the carryPin decision pin.
func pinned(cp checkpoint.Checkpoint) []checkpoint.Decision {
	var out []checkpoint.Decision
	for _, d := range cp.Decisions {
		if d.What == "keep the limiter in-process" {
			out = append(out, d)
		}
	}
	return out
}

// continueSession closes one more segment after the first seal and encodes it into the session's
// live draft (the successor the seal opened, or one a restarted writer begins).
func continueSession(t *testing.T, f *fx) {
	t.Helper()
	f.tool("toolu_carry_0003", 14, "Read", "docs/notes.md", "notes", false)
	f.closedSeg(4, 13, 15)
	f.advance(f.begin(), 4)
}

// TestSecondCheckpointCarriesTheSessionsEarlierDecision is F-C4-UAT06-2: the same session's next
// checkpoint still carries the decision its first one minted, with the same id.
func TestSecondCheckpointCarriesTheSessionsEarlierDecision(t *testing.T) {
	f := newFx(t)
	first := sealFirstWithDecision(t, f)
	want := rejected(first, carryApproach)[0]

	continueSession(t, f)
	res, err := f.w.PreCompact(f.ctx(), f.precompactInput())
	require.NoError(t, err)
	second := f.sealed(t, res.Ref.Seq)
	require.Equal(t, core.CheckpointSeq(2), second.Seq)

	got := rejected(second, carryApproach)
	require.Len(t, got, 1, "the decision holds while its elimination is carried: %+v", second.Decisions)
	require.Equal(t, want.ID, got[0].ID, "and it is the same decision, for why(decision_id)")
	require.Equal(t, want.Turn, got[0].Turn, "at the turn it was made")
	require.Len(t, pinned(second), 1)
}

// TestRestartedWriterCarriesTheSessionsEarlierDecision: the daemon restarts between the two
// compactions (the --resume of UAT-06 may land on a new daemon) and the successor draft file is
// gone, so the next PreCompact begins its draft cold from the session's newest checkpoint.
func TestRestartedWriterCarriesTheSessionsEarlierDecision(t *testing.T) {
	f := newFx(t)
	first := sealFirstWithDecision(t, f)
	want := rejected(first, carryApproach)[0]
	require.NoError(t, os.Remove(paths.Long(f.draftPath())))

	w, err := checkpoint.OpenWriter(f.p.Root, f.p.Cfg, f.p.Log, obs.New(f.p.Clock), f.p.Clock)
	require.NoError(t, err)
	f.w = w
	f.tool("toolu_carry_0004", 14, "Read", "docs/notes.md", "notes", false)
	f.closedSeg(4, 13, 15)
	res := f.precompactAs(f.sess)
	second := f.sealed(t, res.Ref.Seq)

	got := rejected(second, carryApproach)
	require.Len(t, got, 1, "the decision survives a daemon restart: %+v", second.Decisions)
	require.Equal(t, want.ID, got[0].ID)
}

// TestCarriedDecisionEndsWhenItsPinIsRemoved: "while it holds" — a decision pinned as one is not
// carried into a draft begun after the pin is gone, while the elimination's decision is. The draft
// is begun cold (a restarted daemon), which is where the carry reads the pin set.
func TestCarriedDecisionEndsWhenItsPinIsRemoved(t *testing.T) {
	f := newFx(t)
	sealFirstWithDecision(t, f)
	require.NoError(t, os.Remove(paths.Long(f.draftPath())))
	f.pins.invs = nil // unpinned

	w, err := checkpoint.OpenWriter(f.p.Root, f.p.Cfg, f.p.Log, obs.New(f.p.Clock), f.p.Clock)
	require.NoError(t, err)
	f.w = w
	f.tool("toolu_carry_0005", 14, "Read", "docs/notes.md", "notes", false)
	f.closedSeg(4, 13, 15)
	second := f.sealed(t, f.precompactAs(f.sess).Ref.Seq)
	require.Empty(t, pinned(second), "a removed decision pin no longer holds: %+v", second.Decisions)
	require.Len(t, rejected(second, carryApproach), 1)
}

// TestColdDraftCarriesDecisionsPastAnotherSessionsSeal: the session's successor draft file is gone
// (removed, or set aside as unreadable or claimed after a crash) and ANOTHER session sealed the
// project's newest checkpoint in between, so the cold draft's derived parent is not this session's.
// The decision carry reads the session's own newest checkpoint then, as the intent seed already
// does, rather than carrying nothing.
func TestColdDraftCarriesDecisionsPastAnotherSessionsSeal(t *testing.T) {
	f := newFx(t)
	first := sealFirstWithDecision(t, f)
	want := rejected(first, carryApproach)[0]

	const other = core.SessionID("sess_carry_other")
	between := f.sealed(t, f.precompactAs(other).Ref.Seq)
	require.Equal(t, other, between.Session, "fixture sanity: the project's newest seal is another session's")
	require.NoError(t, os.Remove(paths.Long(f.draftPath())))

	w, err := checkpoint.OpenWriter(f.p.Root, f.p.Cfg, f.p.Log, obs.New(f.p.Clock), f.p.Clock)
	require.NoError(t, err)
	f.w = w
	f.tool("toolu_carry_0006", 14, "Read", "docs/notes.md", "notes", false)
	f.closedSeg(4, 13, 15)
	second := f.sealed(t, f.precompactAs(f.sess).Ref.Seq)
	require.Equal(t, f.sess, second.Session)

	got := rejected(second, carryApproach)
	require.Len(t, got, 1, "the decision holds past another session's seal: %+v", second.Decisions)
	require.Equal(t, want.ID, got[0].ID)
	require.Len(t, pinned(second), 1)
}

// ── review round 2 (wave 15 fix seat) ───────────────────────────────────────────────────────────
//
// The carry reads the previous checkpoint as it was SEALED, and Finalize truncates before it seals:
// at budget, alternatives_rejected is emptied on every decision, and then whole decisions go
// tail-first (§10). A decision's source is therefore not read off its sealed shape. An
// elimination's or a pin's decision is derived again from the record or pin that still mints it,
// and an explains decision is recognised by its explains edge in the DAG.

// carryDecisionID is the id the carry fixture's elimination mints its decision under.
func carryDecisionID() core.DecisionID {
	return checkpoint.MintDecisionID(
		fmt.Sprintf("rejected %q for %s", carryApproach, "per-client rate limit"),
		"superseded by the user's correction", core.HashBytes(core.DomainChunk, []byte("limits evidence")))
}

// dropped returns the ids of cp's drop entries of kind.
func dropped(cp checkpoint.Checkpoint, kind string) []string {
	var out []string
	for _, e := range cp.Dropped {
		if e.Kind == kind {
			out = append(out, e.ID)
		}
	}
	return out
}

// budgetThatEmptiesAlternatives is the smallest budget at which Truncate keeps every decision of
// cp, found by bisection (decisions are cut only when emptying the alternatives was not enough, so
// "a decision is cut" is monotone in the budget). cp is a checkpoint sealed with room to spare.
func budgetThatEmptiesAlternatives(t *testing.T, f *fx, cp checkpoint.Checkpoint) core.Tokens {
	t.Helper()
	cuts := func(b core.Tokens) bool {
		_, drops := checkpoint.Truncate(cp, b, f.p.Cfg.Checkpoint.Tiers, f.src.Tokens)
		for _, e := range drops {
			if e.Kind == "decision" {
				return true
			}
		}
		return false
	}
	lo, hi := core.Tokens(1), core.Tokens(f.p.Cfg.Checkpoint.BudgetTokens)
	require.False(t, cuts(hi), "fixture sanity: the configured budget keeps every decision")
	for lo < hi {
		mid := lo + (hi-lo)/2
		if cuts(mid) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// TestCarriedDecisionRegainsItsAlternativeAfterATruncatedSeal: the first checkpoint is sealed under
// a budget that empties alternatives_rejected; the next checkpoint, with room, carries the decision
// as its elimination mints it, alternative included, and not the degraded copy.
func TestCarriedDecisionRegainsItsAlternativeAfterATruncatedSeal(t *testing.T) {
	twin := newFx(t)
	budget := budgetThatEmptiesAlternatives(t, twin, sealFirstWithDecision(t, twin))

	f := newFx(t)
	first := sealFirstUnder(t, f, budget)
	require.Contains(t, dropped(first, "alternatives"), string(carryDecisionID()),
		"fixture sanity: the first seal emptied the decision's alternatives: %+v", first.Dropped)
	require.Empty(t, dropped(first, "decision"), "fixture sanity: and cut no decision")
	require.Empty(t, rejected(first, carryApproach))

	continueSession(t, f)
	second := f.sealed(t, f.precompactAs(f.sess).Ref.Seq)
	require.Equal(t, core.CheckpointSeq(2), second.Seq)
	got := rejected(second, carryApproach)
	require.Len(t, got, 1, "the decision's alternative is back once the budget has room: %+v", second.Decisions)
	require.Equal(t, carryDecisionID(), got[0].ID)
}

// TestDecisionTruncatedAwayIsCarriedAgain: the first seal's budget cut the decisions outright. They
// still hold, so the session's next checkpoint carries them again, under the same cap and ranking.
func TestDecisionTruncatedAwayIsCarriedAgain(t *testing.T) {
	f := newFx(t)
	first := sealFirstUnder(t, f, 1)
	require.Contains(t, dropped(first, "decision"), string(carryDecisionID()),
		"fixture sanity: the first seal cut the decision: %+v", first.Dropped)
	require.Empty(t, first.Decisions)

	continueSession(t, f)
	second := f.sealed(t, f.precompactAs(f.sess).Ref.Seq)
	got := rejected(second, carryApproach)
	require.Len(t, got, 1, "a decision cut at budget is carried again while it holds: %+v", second.Decisions)
	require.Equal(t, carryDecisionID(), got[0].ID)
	require.Len(t, pinned(second), 1)
}

// explainsDecision adds an explains edge at turn to the graph (an assistant turn explaining a change
// to src/limiter.go) and returns the id of the decision it mints.
func explainsDecision(t *testing.T, f *fx, turn core.TurnIndex) core.DecisionID {
	t.Helper()
	const text = "Moved the limiter into the gateway process. A shared store would add a network hop."
	root := putText(t, f.src, text)
	a := dag.AssistantNode(turn)
	file := dag.FileNode("src/limiter.go")
	addNode(t, f.graph, dag.Node{ID: a, Kind: dag.KindAssistant, Turn: turn, Root: root, Pos: f.nextPos()})
	addNode(t, f.graph, dag.Node{ID: file, Kind: dag.KindFile, Turn: turn, Ref: "src/limiter.go", Pos: f.nextPos()})
	addEdge(t, f.graph, dag.Edge{From: a, To: file, Kind: dag.EdgeExplains, Weight: 1, Turn: turn})
	return checkpoint.MintDecisionID("changed src/limiter.go", text, root)
}

// hasDecision reports whether cp holds decision id.
func hasDecision(cp checkpoint.Checkpoint, id core.DecisionID) bool {
	for _, d := range cp.Decisions {
		if d.ID == id {
			return true
		}
	}
	return false
}

// TestSecondCheckpointCarriesTheSessionsExplainsDecision: an explains decision (source a) holds, so
// the session's next checkpoint carries it although the successor draft's frontier is past it.
func TestSecondCheckpointCarriesTheSessionsExplainsDecision(t *testing.T) {
	f := newFx(t)
	seedForPreCompact(t, f)
	want := explainsDecision(t, f, 11)
	f.closedSeg(3, 10, 12)
	f.advance(f.begin(), 3)
	first := f.sealed(t, f.precompactAs(f.sess).Ref.Seq)
	require.True(t, hasDecision(first, want), "fixture sanity: the first checkpoint has it: %+v", first.Decisions)

	continueSession(t, f)
	second := f.sealed(t, f.precompactAs(f.sess).Ref.Seq)
	require.True(t, hasDecision(second, want), "the explains decision is carried: %+v", second.Decisions)
}
