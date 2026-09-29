package checkpoint_test

// V6 close-out wave 14 (coordinator decision D46): a checkpoint's decisions rank this session's own
// before the ones derived from OTHER sessions' project-scoped eliminations, and the foreign ones
// fill whatever room the cap (maxDraftDecisions, 64) leaves, newest recorded first. A foreign
// record's DAG node turn is in that session's turn numbering, so it is no measure of recency here.
// And the seal-time decisions (Draft.refreshNegativeKnowledge) emit the same KindDecision node and
// explains edge ExtractDecisions emits, so slice scoring can rank them.

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/pins"
)

// foreignCount is more than the decision cap (64), so the foreign decisions alone could fill it.
const foreignCount = 70

// foreignApproach names foreign record i's approach, which is how a test finds its decision.
func foreignApproach(i int) string { return fmt.Sprintf("foreign approach %02d", i) }

// recordForeign records foreignCount project-scoped eliminations as another session. Record i is
// recorded later than record i-1 (the clock advances between them) but at a LOWER turn of that
// session (569-i), so ranking by foreign turn and ranking by recorded time disagree completely.
func recordForeign(t *testing.T, f *fx) {
	t.Helper()
	for i := range foreignCount {
		f.now()
		ctx := negknow.WithCaller(f.ctx(), negknow.Caller{Session: "sess_other", Turn: core.TurnIndex(569 - i)})
		target := fmt.Sprintf("src/other%02d.go", i)
		_, err := f.ledger.Record(ctx, negknow.Record{
			Scope: negknow.ScopeProject, Target: target, Approach: foreignApproach(i), Reason: "another session's",
			Evidence: core.HashBytes(core.DomainChunk, []byte(target)),
		})
		require.NoError(t, err)
	}
}

// recordOwn records one of this session's eliminations at turn and returns its record id.
func recordOwn(t *testing.T, f *fx, turn core.TurnIndex, approach string) string {
	t.Helper()
	f.now()
	ctx := negknow.WithCaller(f.ctx(), negknow.Caller{Session: f.sess, Turn: turn})
	id, err := f.ledger.Record(ctx, negknow.Record{
		Target: "src/own.go:" + approach, Approach: approach, Reason: "this session's own finding",
		Evidence: core.HashBytes(core.DomainChunk, []byte("own "+approach)),
	})
	require.NoError(t, err)
	return id
}

// requireOwnFirstThenForeignByRecordedTime asserts D46's order on a sealed or persisted
// checkpoint: exactly the own decisions (by rejected approach, or by what for a pin) at the head,
// then foreign decisions filling the rest of the 64 in recorded-time order, newest first. newer
// names foreign approaches recorded after recordForeign's, newest first.
func requireOwnFirstThenForeignByRecordedTime(t *testing.T, cp checkpoint.Checkpoint, own []string, newer ...string) {
	t.Helper()
	require.Len(t, cp.Decisions, 64, "own decisions plus foreign ones filling the remaining room")
	label := func(d checkpoint.Decision) string {
		if len(d.AlternativesRejected) == 1 {
			return d.AlternativesRejected[0]
		}
		return d.What
	}
	var head []string
	for _, d := range cp.Decisions[:len(own)] {
		head = append(head, label(d))
	}
	require.ElementsMatch(t, own, head, "the session's own decisions rank first, all of them")

	want := append([]string(nil), newer...)
	var got []string
	for i := foreignCount - 1; len(want) < 64-len(own); i-- {
		want = append(want, foreignApproach(i))
	}
	for _, d := range cp.Decisions[len(own):] {
		got = append(got, label(d))
	}
	require.Equal(t, want, got,
		"foreign decisions fill the remaining room newest-recorded first, not by the foreign session's turns")
}

// TestAdvanceOwnDecisionsRankBeforeOtherSessionsProjectEliminations: Advance's decision source (b)
// admits every project-scoped elimination for a session's first segment (from turn 0). 70 of them
// from another session, at that session's high turns, must not push this session's own decisions
// out of the 64-decision cap — in the extraction pass or in the draft's merge across passes.
func TestAdvanceOwnDecisionsRankBeforeOtherSessionsProjectEliminations(t *testing.T) {
	f := newFx(t)
	f.pins.invs = []pins.Invariant{{ID: "inv_decision0001", Text: "keep the pool because it is shared", Source: "decision"}}
	recordForeign(t, f)
	recordOwn(t, f, 3, "inline the helper")
	f.tool("tu_d46_a", 2, "Read", "src/own.go", "package own", false)
	f.closedSeg(1, 0, 5)

	d := f.begin()
	f.advance(d, 1)
	_, cp := f.persisted()
	requireOwnFirstThenForeignByRecordedTime(t, cp, []string{"inline the helper", "keep the pool"})

	// A later pass adds one more own decision: the draft's merge keeps every own one and gives up
	// the oldest-recorded foreign decisions for it. The other session meanwhile records one more
	// project-scoped elimination at ITS turn 2, below this segment's first turn: it is the newest
	// foreign decision, and a from-turn cut in the other session's numbering must not hide it.
	recordOwn(t, f, 8, "retry the dial")
	f.now()
	lateCtx := negknow.WithCaller(f.ctx(), negknow.Caller{Session: "sess_other", Turn: 2})
	_, err := f.ledger.Record(lateCtx, negknow.Record{
		Scope: negknow.ScopeProject, Target: "src/late.go", Approach: "late foreign approach", Reason: "recorded last",
		Evidence: core.HashBytes(core.DomainChunk, []byte("src/late.go")),
	})
	require.NoError(t, err)
	f.tool("tu_d46_b", 7, "Read", "src/own.go", "package own // v2", false)
	f.closedSeg(2, 6, 9)
	f.advance(d, 2)
	_, cp = f.persisted()
	requireOwnFirstThenForeignByRecordedTime(t, cp,
		[]string{"retry the dial", "inline the helper", "keep the pool"}, "late foreign approach")
}

// TestPreCompactKeepsOwnDecisionsAheadOfForeignOnes: the seal-time merge applies the same order to
// what the draft already holds, so the sealed checkpoint keeps every own decision — the one the
// seal-time refresh adds included — ahead of the foreign decisions an earlier Advance admitted.
func TestPreCompactKeepsOwnDecisionsAheadOfForeignOnes(t *testing.T) {
	f := newFx(t)
	recordForeign(t, f)
	recordOwn(t, f, 3, "inline the helper")
	f.tool("tu_d46_a", 2, "Read", "src/own.go", "package own", false)
	f.closedSeg(1, 0, 5)
	d := f.begin()
	f.advance(d, 1)

	recordOwn(t, f, 12, "widen pool timeout") // after the frontier: the seal-time refresh mints it
	in := f.precompactInput()
	in.Budget = 1 << 20 // wide enough that the cap, not Truncate, decides what is sealed
	res, err := f.w.PreCompact(f.ctx(), in)
	require.NoError(t, err)
	requireOwnFirstThenForeignByRecordedTime(t, f.sealed(t, res.Ref.Seq),
		[]string{"widen pool timeout", "inline the helper"})
}

// decisionNodes returns every KindDecision node in the graph whose Ref is id.
func decisionNodes(g dag.Graph, id core.DecisionID) []dag.Node {
	var out []dag.Node
	for _, n := range g.NodesAfter(0) {
		if n.Kind == dag.KindDecision && n.Ref == string(id) {
			out = append(out, n)
		}
	}
	return out
}

// explainsInto returns the explains edges entering node id.
func explainsInto(g dag.Graph, id dag.NodeID) []dag.Edge {
	var out []dag.Edge
	for _, e := range g.In(id) {
		if e.Kind == dag.EdgeExplains {
			out = append(out, e)
		}
	}
	return out
}

// TestPreCompactSealTimeDecisionEmitsItsDAGNode: a decision minted at the seal gets the same
// KindDecision node, and the same explains edge from its elimination node, that ExtractDecisions
// gives the decisions it mints; and when a later Advance extracts the same decision, the graph
// still holds that one node and that one edge, unchanged.
func TestPreCompactSealTimeDecisionEmitsItsDAGNode(t *testing.T) {
	f := newFx(t)
	seedForPreCompact(t, f) // the live draft's frontier is turn 9
	recID := recordOwn(t, f, 12, "widen pool timeout")

	res, err := f.w.PreCompact(f.ctx(), f.precompactInput())
	require.NoError(t, err)
	decs := rejected(f.sealed(t, res.Ref.Seq), "widen pool timeout")
	require.Len(t, decs, 1)
	dec := decs[0]

	nodes := decisionNodes(f.graph, dec.ID)
	require.Len(t, nodes, 1, "the seal-time decision is in the DAG, so slice scoring can rank it")
	n := nodes[0]
	require.Equal(t, dag.DecisionNode(dec.ID), n.ID)
	require.Equal(t, core.TurnIndex(12), n.Turn)
	require.Equal(t, dec.Evidence, n.Root)
	require.NotZero(t, n.Tokens)
	edges := explainsInto(f.graph, n.ID)
	require.Len(t, edges, 1)
	require.Equal(t, dag.EliminationNode(recID), edges[0].From, "explained by its elimination node")
	require.Equal(t, core.TurnIndex(12), edges[0].Turn)

	// The successor draft's Advance over the segment holding turn 12 extracts the same decision.
	succ := f.w.DraftFor(f.sess)
	require.NotNil(t, succ)
	f.tool("tu_d46_c", 11, "Read", "src/own.go", "package own", false)
	f.closedSeg(3, 10, 14)
	f.advance(succ, 3)
	_, cp := f.persisted()
	require.Len(t, rejected(cp, "widen pool timeout"), 1, "Advance extracted the same decision")

	again := decisionNodes(f.graph, dec.ID)
	require.Equal(t, []dag.Node{n}, again, "one node, unchanged by the re-extraction")
	require.Equal(t, edges, explainsInto(f.graph, n.ID), "one explains edge, unchanged")
}
