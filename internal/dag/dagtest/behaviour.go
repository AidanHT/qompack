package dagtest

import (
	"strconv"
	"testing"

	"github.com/qompack/qompack/internal/dag"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertions of the dagtest suite (00-ARCHITECTURE.md §5.22 table;
// §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md). Every one of them is gated behind
// the same Rule W-1 stub probe as the rest of the suite, so an implementation inherits the grader
// rather than writing its own.
//
// Two conventions run through the whole file and are worth stating once.
//
// Node ids are built through the dag constructors — dag.FileNode, dag.ToolUseNode — and never by
// concatenating a prefix onto a key. Hand-built ids happen to be correct under the current D-2
// scheme, which is exactly the problem: a suite that hard-codes "file:"+ref would keep passing if
// the constructors and the scheme drifted apart, and the constructors are what every production
// caller actually uses.
//
// Validation failures are asserted against dag.ErrInvalidNode and dag.ErrInvalidEdge rather than
// against the four core sentinels. Those two are the package's own documented contract for "this
// input is malformed", and they are deliberately NOT core sentinels: a malformed node is a caller
// bug in this process, not one of the cross-package conditions every conformance suite probes for.

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

// addFileNode adds one file node at pos and returns its id, so the fixtures below read as a list of
// positions rather than as a list of struct literals.
func addFileNode(t *testing.T, g dag.Graph, ref string, pos int) dag.NodeID {
	t.Helper()
	id := dag.FileNode(ref)
	require.NoError(t, g.AddNode(dag.Node{ID: id, Kind: dag.KindFile, Ref: ref, Pos: pos}))
	return id
}

// chainGraphNodeCount is how many nodes runSliceScoresDescendCase's linear chain fixture has.
const chainGraphNodeCount = 5

// posStep is the position spacing every chain fixture uses. It is deliberately generous, and
// deliberately not derived from any config default, so a real Δ-decay-by-hop scoring function has
// room to produce genuinely distinct scores.
const posStep = 111

// buildChain adds n file nodes to g named "<prefix>-a", "<prefix>-b", … connected head-to-tail by
// edges of kind k, and returns their ids in chain order.
func buildChain(t *testing.T, g dag.Graph, prefix string, n int, k dag.EdgeKind) []dag.NodeID {
	t.Helper()
	ids := make([]dag.NodeID, n)
	for i := 0; i < n; i++ {
		ids[i] = addFileNode(t, g, prefix+"-"+string(rune('a'+i))+".txt", i*posStep)
	}
	for i := 0; i+1 < n; i++ {
		require.NoError(t, g.AddEdge(dag.Edge{From: ids[i], To: ids[i+1], Kind: k, Weight: 1}))
	}
	return ids
}

// runAddNodeValidationCase asserts every malformed node is refused with dag.ErrInvalidNode and
// nothing is stored.
//
// The kind/prefix cross-check is the important row. NodeKind's zero value is KindToolUse rather than
// a sentinel — the frozen fixture testdata/golden/contracts/dag/want/node_line.jsonl pins "kind":4
// to KindFile, so the sentinel has to sit at the END of the iota block (Rule W-2) — which means a
// node whose Kind was never set is indistinguishable from a genuine tool-use node by looking at the
// field. Its ID's prefix says so unambiguously, and that is what AddNode must check.
func runAddNodeValidationCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	cases := []struct {
		name string
		node dag.Node
	}{
		{"kind disagrees with id prefix", dag.Node{ID: dag.FileNode("x.txt"), Kind: dag.KindToolUse}},
		{"empty id", dag.Node{ID: "", Kind: dag.KindFile}},
		{"no colon in id", dag.Node{ID: "nocolon", Kind: dag.KindFile}},
		{"unknown prefix", dag.Node{ID: "zz:x", Kind: dag.KindFile}},
		{"empty key", dag.Node{ID: "file:", Kind: dag.KindFile}},
		{"negative pos", dag.Node{ID: dag.FileNode("x.txt"), Kind: dag.KindFile, Pos: -1}},
		{"negative tokens", dag.Node{ID: dag.FileNode("x.txt"), Kind: dag.KindFile, Tokens: -1}},
		{"sentinel kind", dag.Node{ID: dag.FileNode("x.txt"), Kind: dag.KindInvalid}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := factory(t)
			require.ErrorIs(t, g.AddNode(tc.node), dag.ErrInvalidNode)
			require.Equal(t, 0, g.Stats().Nodes, "a rejected node must not be stored")
		})
	}
}

// runAddEdgeValidationCase asserts every malformed edge is refused with dag.ErrInvalidEdge and
// nothing is stored. A missing ENDPOINT is deliberately absent from this list: D-6 makes a dangling
// edge legal, and runDanglingEdgeCase asserts it is accepted.
func runAddEdgeValidationCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	from, to := dag.FileNode("edge-from.txt"), dag.FileNode("edge-to.txt")
	cases := []struct {
		name string
		edge dag.Edge
	}{
		{"self-loop", dag.Edge{From: from, To: from, Kind: dag.EdgeSequence, Weight: 1}},
		{"unparseable from", dag.Edge{From: "nocolon", To: to, Kind: dag.EdgeSequence, Weight: 1}},
		{"unparseable to", dag.Edge{From: from, To: "zz:x", Kind: dag.EdgeSequence, Weight: 1}},
		{"sentinel kind", dag.Edge{From: from, To: to, Kind: dag.EdgeInvalid, Weight: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := factory(t)
			require.ErrorIs(t, g.AddEdge(tc.edge), dag.ErrInvalidEdge)
			require.Equal(t, 0, g.Stats().Edges, "a rejected edge must not be stored")
		})
	}
}

// runUpsertMergeCase asserts a second AddNode for the same id MERGES field-wise rather than
// replacing: a non-zero incoming field overwrites, a zero incoming field keeps what is stored.
//
// It matters because the observer re-derives a node on every observation of it and rarely knows
// every field: a PostToolUse payload carrying only a fresh turn index must not silently erase the
// token count an earlier, richer observation established.
func runUpsertMergeCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)

	const upsertPos, upsertTokens, upsertTurn = 100, 50, 7
	id := dag.ToolUseNode("toolu_upsert")
	require.NoError(t, g.AddNode(dag.Node{ID: id, Kind: dag.KindToolUse, Pos: upsertPos, Tokens: upsertTokens}))
	require.NoError(t, g.AddNode(dag.Node{ID: id, Kind: dag.KindToolUse, Turn: upsertTurn}))

	got, ok := g.Node(id)
	require.True(t, ok)
	require.Equal(t, upsertPos, got.Pos, "a zero incoming Pos must not erase the stored one")
	require.EqualValues(t, upsertTokens, got.Tokens, "a zero incoming Tokens must not erase the stored one")
	require.EqualValues(t, upsertTurn, got.Turn, "a non-zero incoming Turn must overwrite")
	require.Equal(t, 1, g.Stats().Nodes, "an upsert must not add a second node")
}

// The two positions runAnchorEarliestPosCase reads one file at: far enough apart that a merge rule
// taking the later one is unmistakable in a failure message.
//
// The first is 10_500 rather than a round 10_000 because 10000 is in the D11 / §11.6
// forbidden-literal set and this file — a library file in dagtest, not a _test.go — is not exempt
// from that check. Spelling it as a product to slip past the linter would defeat the check by
// construction rather than by argument, and the exact value carries no meaning here, so it is
// simply chosen not to collide.
const (
	anchorFirstPos = 10_500
	anchorLaterPos = 90_000
)

// runAnchorEarliestPosCase asserts a shared-state ANCHOR — a file, symbol or segment node — keeps
// the EARLIEST position it was ever seen at, which is the opposite of the merge rule every other
// kind follows.
//
// A file read at token 10 000 and re-read at token 90 000 must keep Pos=10 000. Otherwise every
// shared-file edge migrates toward the tail as the session grows, and CrossingEdges(p) under-reports
// exactly the pre-p/post-p coupling §8.4 asks it to measure — the graph would report a clean cut
// where a real dependence crosses.
func runAnchorEarliestPosCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)

	id := addFileNode(t, g, "anchor.txt", anchorFirstPos)
	require.NoError(t, g.AddNode(dag.Node{ID: id, Kind: dag.KindFile, Ref: "anchor.txt", Pos: anchorLaterPos}))

	got, ok := g.Node(id)
	require.True(t, ok)
	require.Equal(t, anchorFirstPos, got.Pos, "an anchor's Pos is its FIRST appearance, not its latest")
}

// runEdgeDedupCase asserts an identical (From, To, Kind) triple folds into the stored edge instead
// of appending a second one.
//
// §8.1 item 4's shared-state edges are re-derived on every observation of the same path, so without
// the fold a session that reads one file two hundred times would append two hundred identical
// shared-file records and dag/deps.jsonl would stop being linear in the session's real content. The
// STRONGEST weight wins, so a later, better-evidenced observation is not thrown away by the fold.
func runEdgeDedupCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)

	const weakWeight, strongWeight float32 = 0.5, 1
	from := addFileNode(t, g, "dedup-from.txt", 0)
	to := addFileNode(t, g, "dedup-to.txt", posStep)

	require.NoError(t, g.AddEdge(dag.Edge{From: from, To: to, Kind: dag.EdgeSharedFile, Weight: weakWeight}))
	require.NoError(t, g.AddEdge(dag.Edge{From: from, To: to, Kind: dag.EdgeSharedFile, Weight: strongWeight}))

	require.Equal(t, 1, g.Stats().Edges, "an identical (From, To, Kind) triple must fold")
	out := g.Out(from)
	require.Len(t, out, 1)
	require.Equal(t, strongWeight, out[0].Weight, "the fold must keep the STRONGEST weight seen")
}

// runDanglingEdgeCase asserts an edge whose endpoints are not yet nodes is ACCEPTED (D-6), reported
// as dangling, and stops being dangling the moment the missing node arrives.
//
// This is not leniency. The log interleaves, and an observer legitimately emits an edge to a file
// node before the file node itself; refusing would force every caller to order its writes, and
// dangling-ness is computed rather than cached precisely so the edge repairs itself.
func runDanglingEdgeCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)

	from := addFileNode(t, g, "dangling-from.txt", 0)
	to := dag.FileNode("dangling-to.txt")
	require.NoError(t, g.AddEdge(dag.Edge{From: from, To: to, Kind: dag.EdgeSharedFile, Weight: 1}))

	require.Equal(t, 1, g.Stats().Edges)
	require.Equal(t, 1, g.Stats().Dangling, "an edge with an absent endpoint is dangling, not rejected")
	require.Len(t, g.Out(from), 1, "a caller inspecting shared state must still see the edge")
	require.Equal(t, 0, g.CrossingEdges(posStep/2), "a dangling edge couples nothing that exists, so it crosses nothing")

	require.NoError(t, g.AddNode(dag.Node{ID: to, Kind: dag.KindFile, Ref: "dangling-to.txt", Pos: posStep}))
	require.Equal(t, 0, g.Stats().Dangling, "the edge must stop dangling the moment its endpoint arrives")
	require.Equal(t, 1, g.CrossingEdges(posStep/2), "and must start counting toward the coupling measure")
}

// runTombstoneCase asserts a tombstoned node vanishes from every read path.
//
// Tombstone is how the store's GC says a node is gone, so a caller that could still read one would
// be looking at content the object store may already have collected. The subtest is skipped — by
// RETURNING, never by t.Skip — when the factory's Graph does not implement dag.Maintainer: that
// interface is deliberately not folded into Graph so that every existing consumer keeps satisfying
// §5.9 unchanged, which makes "does not implement it" a legal shape rather than a failure. A t.Skip
// here would also be a Rule W-1 violation, since its reason would match none of the three permitted
// messages.
func runTombstoneCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)

	m, ok := g.(dag.Maintainer)
	if !ok {
		return
	}

	live := addFileNode(t, g, "tombstone-live.txt", 0)
	dead := addFileNode(t, g, "tombstone-dead.txt", posStep)
	require.NoError(t, g.AddEdge(dag.Edge{From: live, To: dead, Kind: dag.EdgeSharedFile, Weight: 1}))
	require.Equal(t, 1, g.CrossingEdges(posStep/2), "fixture sanity: the edge straddles the midpoint")

	require.NoError(t, m.Tombstone([]dag.NodeID{dead}))

	_, found := g.Node(dead)
	require.False(t, found, "a tombstoned node reports absent, exactly as an unknown one does")
	require.Empty(t, g.Out(dead))
	require.Empty(t, g.In(dead))
	require.Empty(t, g.Out(live), "an edge incident to a tombstoned node is hidden from both ends")
	require.Equal(t, 0, g.CrossingEdges(posStep/2), "and couples nothing")
	require.Equal(t, 1, g.Stats().Nodes, "only the live node remains countable")

	for _, n := range g.NodesAfter(0) {
		require.NotEqual(t, dead, n.ID, "NodesAfter must not offer a tombstoned node as a candidate")
	}

	sl, err := g.ForwardSlice([]dag.NodeID{live}, dag.SliceOptions{})
	require.NoError(t, err)
	require.NotContains(t, sl.Order, dead, "no slice may reach a tombstoned node")
}

// crossingFixtureNode is one node in runCrossingEdgesExactCase's fixture graph.
type crossingFixtureNode struct {
	id  dag.NodeID
	pos int
}

// buildCrossingFixture adds four nodes at positions 0, 10, 20, 30 and five edges, every one directed
// from a lower Pos to a higher-or-equal Pos: A-B, B-C, C-D, A-D, A-C. The spacing is deliberately
// simple round numbers so the hand-verified table in runCrossingEdgesExactCase's comment can be
// checked by eye.
func buildCrossingFixture(t *testing.T, g dag.Graph) (a, b, c, d crossingFixtureNode) {
	t.Helper()
	a = crossingFixtureNode{id: addFileNode(t, g, "crossing-a.txt", 0), pos: 0}
	b = crossingFixtureNode{id: addFileNode(t, g, "crossing-b.txt", 10), pos: 10}
	c = crossingFixtureNode{id: addFileNode(t, g, "crossing-c.txt", 20), pos: 20}
	d = crossingFixtureNode{id: addFileNode(t, g, "crossing-d.txt", 30), pos: 30}
	for _, e := range [][2]crossingFixtureNode{{a, b}, {b, c}, {c, d}, {a, d}, {a, c}} {
		require.NoError(t, g.AddEdge(dag.Edge{From: e[0].id, To: e[1].id, Kind: dag.EdgeSequence, Weight: 1}))
	}
	return a, b, c, d
}

// runCrossingEdgesExactCase asserts CrossingEdges(pos) counts, exactly, the edges whose two
// endpoints' positions straddle pos (00-ARCHITECTURE.md §5.9: "segment_coupling(p): edges whose
// endpoints straddle token position pos"). Fixture: nodes at Pos 0 (a), 10 (b), 20 (c), 30 (d);
// edges a-b, b-c, c-d, a-d, a-c, every one directed from the lower-Pos node to the higher-Pos node.
// Hand-verified crossing counts, where an edge with endpoint positions [lo,hi] straddles pos when
// lo < pos <= hi:
//
//	pos= 0: none straddle (every edge's lo is already >= 0)                          -> 0
//	pos= 5: a-b[0,10], a-d[0,30], a-c[0,20] straddle; b-c[10,20], c-d[20,30] do not  -> 3
//	pos=15: b-c[10,20], a-d[0,30], a-c[0,20] straddle; a-b[0,10], c-d[20,30] do not  -> 3
//	pos=35: none straddle (every edge's hi is already < 35)                          -> 0
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

// runCrossingEdgesEqualPosCase asserts an edge between two nodes at the SAME position never
// straddles anything.
//
// It is the boundary case the lo < pos <= hi rule is easiest to get wrong: with lo == hi there is no
// pos satisfying both halves, so the answer is zero at that position and everywhere else. It matters
// in practice because BuildToolUse anchors an assistant node and the tool_use block it contains at
// the same Pos on purpose — a cut point between them would separate a tool call from the reasoning
// that emitted it, and §8.4 must never be able to find that boundary "free".
func runCrossingEdgesEqualPosCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)

	const samePos = 500
	from := addFileNode(t, g, "equal-from.txt", samePos)
	to := addFileNode(t, g, "equal-to.txt", samePos)
	require.NoError(t, g.AddEdge(dag.Edge{From: from, To: to, Kind: dag.EdgeSequence, Weight: 1}))

	for _, pos := range []int{samePos - 1, samePos, samePos + 1} {
		require.Equal(t, 0, g.CrossingEdges(pos),
			"an edge whose endpoints share a position straddles nothing: CrossingEdges(%d)", pos)
	}
}

// runNodesAfterOrderingCase asserts NodesAfter(pos) returns exactly the live nodes at or beyond pos,
// in ascending (Pos, Turn, ID) order.
//
// This is §5.3's and §5.5's suffix-constrained candidate set: an arbitrary subset of a cached prefix
// is a worst-case edit, so the only nodes selection may legally consider are the ones after the
// chosen cut point. The (Pos, Turn, ID) triple is a TOTAL order, which is what makes the answer
// identical on every platform — a map-iteration tiebreak would make the goldens quietly flaky.
func runNodesAfterOrderingCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)

	// Two nodes share a position so the ID tiebreak is exercised, not merely present.
	const lowPos, midPos, highPos = 0, 100, 200
	low := addFileNode(t, g, "after-low.txt", lowPos)
	midA := addFileNode(t, g, "after-mid-a.txt", midPos)
	midB := addFileNode(t, g, "after-mid-b.txt", midPos)
	high := addFileNode(t, g, "after-high.txt", highPos)

	all := g.NodesAfter(0)
	require.Equal(t, []dag.NodeID{low, midA, midB, high}, idsOf(all),
		"NodesAfter(0) returns every node in ascending (Pos, Turn, ID) order")

	require.Equal(t, []dag.NodeID{midA, midB, high}, idsOf(g.NodesAfter(midPos)),
		"the bound is inclusive: a node exactly AT pos is in the suffix")
	require.Equal(t, []dag.NodeID{high}, idsOf(g.NodesAfter(midPos+1)))
	require.Empty(t, g.NodesAfter(highPos+1), "a pos beyond every node yields an empty candidate set")

	for _, n := range g.NodesAfter(midPos) {
		require.GreaterOrEqual(t, n.Pos, midPos)
	}
}

// idsOf renders a node slice as its ids, so an ordering assertion reads as a list of names.
func idsOf(nodes []dag.Node) []dag.NodeID {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]dag.NodeID, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}

// runSliceScoresDescendCase asserts both BackwardSlice and ForwardSlice return a Slice whose Order
// is sorted by descending Scores, and that every criterion scores exactly 1.
func runSliceScoresDescendCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)
	ids := buildChain(t, g, "chain", chainGraphNodeCount, dag.EdgeSequence)

	back, err := g.BackwardSlice([]dag.NodeID{ids[len(ids)-1]}, dag.SliceOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, back.Order, "fixture sanity: BackwardSlice from the chain's tail must visit something")
	requireOrderScoresDescend(t, back)
	require.Equal(t, float32(1), back.Scores[ids[len(ids)-1]], "a criterion scores exactly 1")

	fwd, err := g.ForwardSlice([]dag.NodeID{ids[0]}, dag.SliceOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, fwd.Order, "fixture sanity: ForwardSlice from the chain's head must visit something")
	requireOrderScoresDescend(t, fwd)
	require.Equal(t, float32(1), fwd.Scores[ids[0]])
}

// runSliceDirectionCase asserts the two walks are genuinely opposite (D-1: every edge points from
// earlier/producer to later/consumer, with no exceptions).
//
// Walking against the arrows answers "what did this depend on", which is what compaction ranking
// needs; walking with them answers "what depended on this", which is what §8.7 retrieval expands a
// hit set with. A slice that quietly walked both ways would answer neither question.
func runSliceDirectionCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)
	ids := buildChain(t, g, "direction", chainGraphNodeCount, dag.EdgeSharedFile)
	head, tail := ids[0], ids[len(ids)-1]

	fwd, err := g.ForwardSlice([]dag.NodeID{head}, dag.SliceOptions{})
	require.NoError(t, err)
	require.Contains(t, fwd.Order, tail, "a forward slice from the head reaches the tail")

	back, err := g.BackwardSlice([]dag.NodeID{head}, dag.SliceOptions{})
	require.NoError(t, err)
	require.Equal(t, []dag.NodeID{head}, back.Order, "a backward slice from the head reaches only itself")
	require.Equal(t, 1, back.Visited)
}

// runThinDropsControlOnlyCase asserts SliceOptions.Thin=true drops EdgeControlOnly edges
// (00-ARCHITECTURE.md §6.4, §5.9): a node reachable from the slice criteria ONLY via a control-only
// edge must be absent from a Thin slice and present in a non-Thin one.
//
// It also asserts EdgeSequence SURVIVES a thin slice. Thin drops control-only and nothing else:
// §6.4's claim is that adjacency is weak evidence, not that it is no evidence, and a thin walk that
// also dropped sequence edges would disconnect the transcript chain entirely.
func runThinDropsControlOnlyCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)

	root := addFileNode(t, g, "thin-root.txt", 0)
	controlOnly := addFileNode(t, g, "thin-control-only.txt", posStep)
	sequenced := addFileNode(t, g, "thin-sequenced.txt", 2*posStep)
	require.NoError(t, g.AddEdge(dag.Edge{From: root, To: controlOnly, Kind: dag.EdgeControlOnly, Weight: 1}))
	require.NoError(t, g.AddEdge(dag.Edge{From: root, To: sequenced, Kind: dag.EdgeSequence, Weight: 1}))

	thin, err := g.ForwardSlice([]dag.NodeID{root}, dag.SliceOptions{Thin: true})
	require.NoError(t, err)
	require.NotContains(t, thin.Order, controlOnly,
		"Thin=true must drop EdgeControlOnly, so a node reachable ONLY through one must not appear")
	require.Contains(t, thin.Order, sequenced, "Thin drops control-only and nothing else; EdgeSequence survives")

	full, err := g.ForwardSlice([]dag.NodeID{root}, dag.SliceOptions{Thin: false})
	require.NoError(t, err)
	require.Contains(t, full.Order, controlOnly,
		"fixture sanity: Thin=false must still reach the control-only-linked node")
	require.Greater(t, full.Scores[sequenced], full.Scores[controlOnly],
		"a sequence hop must outweigh a control-only hop (SP-07 D-3)")
}

// The star fixture runSliceMaxNodesCase caps: one centre and this many leaves, which is comfortably
// more than the cap so the walk genuinely has something left to withhold.
const (
	starLeafCount   = 30
	starMaxNodes    = 10
	starWeightSteps = 1000
)

// runSliceMaxNodesCase asserts MaxNodes caps the slice and sets Truncated when it does.
//
// Every leaf carries a distinct weight, so the ten highest scores are unambiguous and the test
// cannot pass by accident on a tie. Truncated is what tells a consumer the answer was WITHHELD —
// §8.4's idle precomputation re-runs a truncated slice with a larger budget, so reporting it wrongly
// costs either a wasted walk or a silently short answer.
func runSliceMaxNodesCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)

	centre := addFileNode(t, g, "star-centre.txt", 0)
	for i := 0; i < starLeafCount; i++ {
		leaf := addFileNode(t, g, "star-leaf-"+strconv.Itoa(i)+".txt", (i+1)*posStep)
		require.NoError(t, g.AddEdge(dag.Edge{
			From: centre, To: leaf, Kind: dag.EdgeSharedFile,
			Weight: 1 - float32(i)/starWeightSteps,
		}))
	}

	sl, err := g.ForwardSlice([]dag.NodeID{centre}, dag.SliceOptions{MaxNodes: starMaxNodes})
	require.NoError(t, err)
	require.Len(t, sl.Scores, starMaxNodes, "MaxNodes is a hard cap on the node count")
	require.True(t, sl.Truncated, "a walk with reachable nodes left over must report Truncated")
	requireOrderScoresDescend(t, sl)
	require.Equal(t, centre, sl.Order[0], "the criterion scores 1 and therefore leads the order")
}

// runSliceExactFitCase asserts a graph with exactly MaxNodes reachable nodes yields a COMPLETE
// slice: the cap was reached, but nothing was withheld.
//
// The distinction is the whole reason Truncated cannot be `len(Scores) >= MaxNodes`. A consumer told
// a complete answer was truncated re-runs a walk that has nothing left to find, on the hot path,
// every time.
func runSliceExactFitCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)

	const exactFitCount = 10
	ids := buildChain(t, g, "exact", exactFitCount, dag.EdgeProduces)

	sl, err := g.ForwardSlice([]dag.NodeID{ids[0]}, dag.SliceOptions{MaxNodes: exactFitCount})
	require.NoError(t, err)
	require.Len(t, sl.Scores, exactFitCount)
	require.False(t, sl.Truncated, "the cap was reached but nothing was withheld: that is not a truncation")
}

// runSliceMaxDepthCase asserts MaxDepth bounds the hop count and is likewise not a truncation: the
// caller asked for a bounded neighbourhood and got exactly one.
func runSliceMaxDepthCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)

	const depthChainCount, maxDepth = 6, 2
	ids := buildChain(t, g, "depth", depthChainCount, dag.EdgeProduces)

	sl, err := g.ForwardSlice([]dag.NodeID{ids[0]}, dag.SliceOptions{MaxDepth: maxDepth})
	require.NoError(t, err)
	require.Len(t, sl.Scores, maxDepth+1, "the criterion plus MaxDepth hops")
	require.False(t, sl.Truncated, "a depth bound is a question, not a withheld answer")
	for i := 0; i <= maxDepth; i++ {
		require.Contains(t, sl.Order, ids[i])
	}
	require.NotContains(t, sl.Order, ids[maxDepth+1])
}

// runSliceUnknownCriteriaCase asserts an unknown criterion is an ordinary empty answer, not an
// error.
//
// It has to be. Criteria arrive from a checkpoint's evidence list or an MCP retrieval request, and
// a node the graph never observed — or one the store's GC has since tombstoned — is a normal
// condition on a long-running project. Refusing would turn a partial answer into no answer.
func runSliceUnknownCriteriaCase(t *testing.T, factory func(t *testing.T) dag.Graph) {
	t.Helper()
	g := factory(t)
	addFileNode(t, g, "known.txt", 0)

	for _, direction := range []struct {
		name string
		run  func([]dag.NodeID, dag.SliceOptions) (dag.Slice, error)
	}{
		{"backward", g.BackwardSlice},
		{"forward", g.ForwardSlice},
	} {
		sl, err := direction.run([]dag.NodeID{dag.ToolUseNode("toolu_never_observed")}, dag.SliceOptions{})
		require.NoErrorf(t, err, "%s slice from an unknown criterion is an empty answer, not an error", direction.name)
		require.Emptyf(t, sl.Scores, "%s", direction.name)
		require.Emptyf(t, sl.Order, "%s", direction.name)
		require.Zerof(t, sl.Visited, "%s", direction.name)
		require.Falsef(t, sl.Truncated, "%s", direction.name)
	}
}
