package dagtest

import (
	"testing"

	"github.com/qompack/qompack/internal/dag"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertions of the dagtest suite (00-ARCHITECTURE.md §5.22
// table; §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md): slice scores descend, Thin
// drops EdgeControlOnly, and CrossingEdges counts straddling edges exactly. All three are authored
// now, gated behind the same Rule W-1 stub probe as the rest of the suite, so SP-07 inherits them
// rather than writing its own grader.

// requireOrderScoresDescend asserts sl.Scores[sl.Order[i]] is non-increasing as i increases
// (00-ARCHITECTURE.md §5.9: "Order []NodeID // descending score, stable tiebreak by Turn").
func requireOrderScoresDescend(t *testing.T, sl dag.Slice) {
	t.Helper()
	require.Len(t, sl.Order, len(sl.Scores), "Order must list exactly the nodes Scores covers")
	for i := 0; i+1 < len(sl.Order); i++ {
		cur, next := sl.Scores[sl.Order[i]], sl.Scores[sl.Order[i+1]]
		require.GreaterOrEqual(t, cur, next,
			"Order must be sorted by descending Scores: Order[%d]=%v (score %v) must not score below Order[%d]=%v (score %v)",
			i, sl.Order[i], cur, i+1, sl.Order[i+1], next)
	}
}

// chainGraphNodeCount is how many nodes runSliceScoresDescendCase's linear chain fixture has.
const chainGraphNodeCount = 5

// buildLinearChain adds chainGraphNodeCount file nodes to g, connected in sequence
// (n0 -> n1 -> ... ) by EdgeSequence edges, and returns their IDs in chain order. Position spacing
// is deliberately generous (not derived from any config default) so a real Δ-decay-by-hop scoring
// function has room to produce genuinely distinct scores.
func buildLinearChain(t *testing.T, g dag.Graph) []dag.NodeID {
	t.Helper()
	const posStep = 111
	ids := make([]dag.NodeID, chainGraphNodeCount)
	for i := 0; i < chainGraphNodeCount; i++ {
		ref := "chain-" + string(rune('a'+i)) + ".txt"
		id := dag.NodeID("file:" + ref)
		ids[i] = id
		require.NoError(t, g.AddNode(dag.Node{ID: id, Kind: dag.KindFile, Ref: ref, Pos: i * posStep}))
	}
	for i := 0; i+1 < chainGraphNodeCount; i++ {
		require.NoError(t, g.AddEdge(dag.Edge{From: ids[i], To: ids[i+1], Kind: dag.EdgeSequence, Weight: 1}))
	}
	return ids
}

// runSliceScoresDescendCase asserts both BackwardSlice and ForwardSlice return a Slice whose
// Order is sorted by descending Scores.
func runSliceScoresDescendCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)
	ids := buildLinearChain(t, g)

	back, err := g.BackwardSlice([]dag.NodeID{ids[len(ids)-1]}, dag.SliceOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, back.Order, "fixture sanity: BackwardSlice from the chain's tail must visit something")
	requireOrderScoresDescend(t, back)

	fwd, err := g.ForwardSlice([]dag.NodeID{ids[0]}, dag.SliceOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, fwd.Order, "fixture sanity: ForwardSlice from the chain's head must visit something")
	requireOrderScoresDescend(t, fwd)
}

// runThinDropsControlOnlyCase asserts SliceOptions.Thin=true drops EdgeControlOnly edges
// (00-ARCHITECTURE.md §6.4, §5.9): a node reachable from the slice criteria ONLY via a
// control-only edge must be absent from a Thin slice and present in a non-Thin one.
func runThinDropsControlOnlyCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)

	const rootID, controlOnlyID dag.NodeID = "file:thin-root.txt", "file:thin-control-only.txt"
	require.NoError(t, g.AddNode(dag.Node{ID: rootID, Kind: dag.KindFile, Ref: "thin-root.txt"}))
	require.NoError(t, g.AddNode(dag.Node{ID: controlOnlyID, Kind: dag.KindFile, Ref: "thin-control-only.txt"}))
	require.NoError(t, g.AddEdge(dag.Edge{From: rootID, To: controlOnlyID, Kind: dag.EdgeControlOnly, Weight: 1}))

	thin, err := g.ForwardSlice([]dag.NodeID{rootID}, dag.SliceOptions{Thin: true})
	require.NoError(t, err)
	require.NotContains(t, thin.Order, controlOnlyID,
		"Thin=true must drop EdgeControlOnly, so a node reachable ONLY through one must not appear")

	full, err := g.ForwardSlice([]dag.NodeID{rootID}, dag.SliceOptions{Thin: false})
	require.NoError(t, err)
	require.Contains(t, full.Order, controlOnlyID,
		"fixture sanity: Thin=false must still reach the control-only-linked node")
}

// crossingFixtureNode is one node in runCrossingEdgesExactCase's fixture graph.
type crossingFixtureNode struct {
	id  dag.NodeID
	pos int
}

// buildCrossingFixture adds four nodes at positions 0, 10, 20, 30 and five edges, every one
// directed from a lower Pos to a higher-or-equal Pos: A-B, B-C, C-D, A-D, A-C. Position spacing
// (0/10/20/30) is deliberately simple round numbers for readability in the hand-verified table in
// runCrossingEdgesExactCase's own comment.
func buildCrossingFixture(t *testing.T, g dag.Graph) (a, b, c, d crossingFixtureNode) {
	t.Helper()
	a = crossingFixtureNode{id: "file:crossing-a.txt", pos: 0}
	b = crossingFixtureNode{id: "file:crossing-b.txt", pos: 10}
	c = crossingFixtureNode{id: "file:crossing-c.txt", pos: 20}
	d = crossingFixtureNode{id: "file:crossing-d.txt", pos: 30}
	for _, n := range []crossingFixtureNode{a, b, c, d} {
		ref := string(n.id)[len("file:"):]
		require.NoError(t, g.AddNode(dag.Node{ID: n.id, Kind: dag.KindFile, Ref: ref, Pos: n.pos}))
	}
	for _, e := range [][2]crossingFixtureNode{{a, b}, {b, c}, {c, d}, {a, d}, {a, c}} {
		require.NoError(t, g.AddEdge(dag.Edge{From: e[0].id, To: e[1].id, Kind: dag.EdgeSequence, Weight: 1}))
	}
	return a, b, c, d
}

// runCrossingEdgesExactCase asserts CrossingEdges(pos) counts, exactly, the edges whose two
// endpoints' positions straddle pos (00-ARCHITECTURE.md §5.9: "segment_coupling(p): edges whose
// endpoints straddle token position pos"). Fixture: nodes at Pos 0 (a), 10 (b), 20 (c), 30 (d);
// edges a-b, b-c, c-d, a-d, a-c, every one directed from the lower-Pos node to the higher-Pos
// node. Hand-verified crossing counts, where an edge with endpoint positions [lo,hi] straddles pos
// when lo < pos <= hi:
//
//	pos= 0: none straddle (every edge's lo is already >= 0)                    -> 0
//	pos= 5: a-b[0,10], a-d[0,30], a-c[0,20] straddle; b-c[10,20], c-d[20,30] do not -> 3
//	pos=15: b-c[10,20], a-d[0,30], a-c[0,20] straddle; a-b[0,10], c-d[20,30] do not -> 3
//	pos=35: none straddle (every edge's hi is already < 35)                    -> 0
func runCrossingEdgesExactCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)
	buildCrossingFixture(t, g)

	cases := []struct {
		pos  int
		want int
	}{
		{pos: 0, want: 0},
		{pos: 5, want: 3},
		{pos: 15, want: 3},
		{pos: 35, want: 0},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, g.CrossingEdges(tc.pos), "CrossingEdges(%d)", tc.pos)
	}
}
