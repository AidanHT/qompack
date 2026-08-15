package dag

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// scoreDelta is the tolerance every computed-score assertion in this file uses.
//
// A score is a float32 product of several factors, so float32(0.85)*float32(0.85) is not bit-equal
// to float32(0.7225): the two differ in the last mantissa bit, and asserting equality would be
// asserting a property of IEEE-754 rounding rather than of the traversal. 1e-6 is roughly twenty
// times the ulp of a float32 near 1, which is far tighter than any wrong answer this file is
// hunting (0.255 vs 0.7225, 0 vs 0.425) and far looser than the rounding noise.
//
// The one value compared with a ZERO delta is 1.0: the traversal assigns a criterion the literal
// float32(1) and never computes it, so it is exactly representable and exactly reproduced. That is
// also graph_test.go's own idiom for an exact float assertion (require.InDelta with delta 0).
const scoreDelta = 1e-6

// addSliceNode adds one node, failing the test if the graph rejects it. Kind must agree with the
// id's prefix — AddNode validates the two against each other, since NodeKind's zero value is a real
// kind rather than "unset".
func addSliceNode(t *testing.T, g *graph, id NodeID, kind NodeKind, turn core.TurnIndex) {
	t.Helper()
	require.NoError(t, g.AddNode(Node{ID: id, Kind: kind, Turn: turn}))
}

// addSliceEdge adds one edge, failing the test if the graph rejects it.
func addSliceEdge(t *testing.T, g *graph, from, to NodeID, kind EdgeKind, weight float32) {
	t.Helper()
	require.NoError(t, g.AddEdge(Edge{From: from, To: to, Kind: kind, Weight: weight}))
}

// addFileChain adds n file nodes — "file:<prefix>0" … "file:<prefix>n-1" — and links each to its
// successor with an edge of kind k and weight 1, returning the ids in order. Every edge points
// from the earlier node to the later one, which is D-1's universal direction, so a ForwardSlice
// from ids[0] walks the whole chain and a BackwardSlice from ids[0] sees only itself.
//
// The file kind is structural rather than semantic here: these cases are about the traversal's
// arithmetic, and a file id is the one kind whose stable key can be an arbitrary counter without
// implying a turn index or a tool-use id that does not exist.
func addFileChain(t *testing.T, g *graph, prefix string, n int, k EdgeKind) []NodeID {
	t.Helper()
	ids := make([]NodeID, n)
	for i := range n {
		ids[i] = NodeID(fmt.Sprintf("file:%s%d", prefix, i))
		addSliceNode(t, g, ids[i], KindFile, 0)
	}
	for i := 0; i+1 < n; i++ {
		addSliceEdge(t, g, ids[i], ids[i+1], k, 1)
	}
	return ids
}

// TestBackwardSliceChain pins the per-hop arithmetic of §6.4's relevance walk on the canonical
// tool_use → tool_result → assistant chain of §8.1 item 4: the criterion scores 1, and every hop
// away from it multiplies by Decay and the edge kind's multiplier (D-3). Both hops here are
// full-strength kinds, so the scores are the pure decay powers 1, 0.85 and 0.85².
func TestBackwardSliceChain(t *testing.T) {
	g := newTestGraph(t)
	addSliceNode(t, g, "tooluse:a", KindToolUse, 1)
	addSliceNode(t, g, "toolresult:a", KindToolResult, 1)
	addSliceNode(t, g, "assistant:1", KindAssistant, 1)
	addSliceEdge(t, g, "tooluse:a", "toolresult:a", EdgeProduces, 1)
	addSliceEdge(t, g, "toolresult:a", "assistant:1", EdgeConsumes, 1)

	got, err := g.BackwardSlice([]NodeID{"assistant:1"}, SliceOptions{Thin: true, Decay: DefaultDecay})
	require.NoError(t, err)

	require.InDelta(t, float32(1), got.Scores["assistant:1"], 0, "a criterion scores exactly 1")
	require.InDelta(t, float32(0.85), got.Scores["toolresult:a"], scoreDelta)
	require.InDelta(t, float32(0.7225), got.Scores["tooluse:a"], scoreDelta)
	require.Equal(t, []NodeID{"assistant:1", "toolresult:a", "tooluse:a"}, got.Order)
	require.Equal(t, 3, got.Visited)
	require.False(t, got.Truncated)
}

// TestBackwardSliceTakesMaxPath is the case that separates a best-first (max-product) relaxation
// from a plain breadth-first walk. tooluse:t is reachable from the criterion two ways: a strong
// two-hop produces/consumes path worth 0.85·0.85 = 0.7225, and a weak one-hop supersedes path worth
// 0.85·0.30 = 0.255. §8.3 says the answer is a SCORE, and the score must be the maximum over all
// paths — not the first path found, and not the sum.
//
// The supersedes edge is added FIRST on purpose. Adjacency is kept in insertion order, so an
// insertion-ordered queue reaches tooluse:t through the weak edge before it has finalized
// toolresult:a, finalizes 0.255, and never revisits. This test going green is what says the
// implementation is a max-heap.
func TestBackwardSliceTakesMaxPath(t *testing.T) {
	g := newTestGraph(t)
	addSliceNode(t, g, "tooluse:t", KindToolUse, 1)
	addSliceNode(t, g, "toolresult:a", KindToolResult, 1)
	addSliceNode(t, g, "assistant:1", KindAssistant, 1)

	addSliceEdge(t, g, "tooluse:t", "assistant:1", EdgeSupersedes, 1)
	addSliceEdge(t, g, "tooluse:t", "toolresult:a", EdgeProduces, 1)
	addSliceEdge(t, g, "toolresult:a", "assistant:1", EdgeConsumes, 1)

	got, err := g.BackwardSlice([]NodeID{"assistant:1"}, SliceOptions{Decay: DefaultDecay})
	require.NoError(t, err)

	// 0.7225 is the strong path. The two wrong answers this pins out are 0.255 (the weak path,
	// which is what a FIFO queue finalizes) and 0.9775 (their sum, which is what an additive
	// relaxation would produce).
	require.InDelta(t, float32(0.7225), got.Scores["tooluse:t"], scoreDelta)
	require.InDelta(t, float32(0.85), got.Scores["toolresult:a"], scoreDelta)
	require.Equal(t, 3, got.Visited, "a node is finalized exactly once, however many paths reach it")
}

// TestForwardSliceDirection pins D-1: every edge points from producer to consumer, so ForwardSlice
// walks the Out lists (with the arrows) and BackwardSlice walks the In lists (against them). From
// the head of a chain the forward walk sees everything downstream and the backward walk sees
// nothing but the criterion itself.
func TestForwardSliceDirection(t *testing.T) {
	g := newTestGraph(t)
	addSliceNode(t, g, "tooluse:a", KindToolUse, 1)
	addSliceNode(t, g, "toolresult:a", KindToolResult, 1)
	addSliceNode(t, g, "assistant:1", KindAssistant, 1)
	addSliceEdge(t, g, "tooluse:a", "toolresult:a", EdgeProduces, 1)
	addSliceEdge(t, g, "toolresult:a", "assistant:1", EdgeConsumes, 1)

	fwd, err := g.ForwardSlice([]NodeID{"tooluse:a"}, SliceOptions{Decay: DefaultDecay})
	require.NoError(t, err)
	require.InDelta(t, float32(1), fwd.Scores["tooluse:a"], 0)
	require.InDelta(t, float32(0.85), fwd.Scores["toolresult:a"], scoreDelta)
	require.InDelta(t, float32(0.7225), fwd.Scores["assistant:1"], scoreDelta)
	require.Len(t, fwd.Scores, 3)

	back, err := g.BackwardSlice([]NodeID{"tooluse:a"}, SliceOptions{Decay: DefaultDecay})
	require.NoError(t, err)
	require.Len(t, back.Scores, 1, "nothing produces the head of the chain")
	require.InDelta(t, float32(1), back.Scores["tooluse:a"], 0)
	require.Equal(t, []NodeID{"tooluse:a"}, back.Order)
}

// TestThinDropsControlOnly pins D-4 in both directions: thin slicing drops EdgeControlOnly and
// NOTHING ELSE. §6.4 makes control dependence with no data flow the weakest evidence available, so
// the cheap walk skips it entirely; sequence edges are retained and simply carry their low 0.60
// multiplier, because §6.4's point is that recency is a weak proxy, not that it is no evidence.
func TestThinDropsControlOnly(t *testing.T) {
	g := newTestGraph(t)
	addSliceNode(t, g, "assistant:1", KindAssistant, 1)
	addSliceNode(t, g, "tooluse:ctl", KindToolUse, 1)
	addSliceNode(t, g, "tooluse:seq", KindToolUse, 1)
	addSliceEdge(t, g, "tooluse:ctl", "assistant:1", EdgeControlOnly, 1)
	addSliceEdge(t, g, "tooluse:seq", "assistant:1", EdgeSequence, 1)

	thin, err := g.BackwardSlice([]NodeID{"assistant:1"}, SliceOptions{Thin: true, Decay: DefaultDecay})
	require.NoError(t, err)
	require.NotContains(t, thin.Scores, NodeID("tooluse:ctl"), "thin slicing drops control-only edges")
	require.InDelta(t, float32(0.51), thin.Scores["tooluse:seq"], scoreDelta,
		"thin slicing RETAINS sequence edges at their 0.60 multiplier (D-4)")
	require.Len(t, thin.Scores, 2)

	full, err := g.BackwardSlice([]NodeID{"assistant:1"}, SliceOptions{Thin: false, Decay: DefaultDecay})
	require.NoError(t, err)
	require.InDelta(t, float32(0.425), full.Scores["tooluse:ctl"], scoreDelta, "1 · 0.85 · 0.50")
	require.Len(t, full.Scores, 3)
}

// TestSliceMaxNodes pins the MaxNodes cap on a 100-node star whose leaf scores are all distinct, so
// "the ten highest" names exactly one set and the assertion cannot be satisfied by luck.
//
// The leaves are added in DESCENDING index order, which is the exact reverse of their score order.
// That is deliberate: adjacency is kept in insertion order, so a traversal that pops in insertion
// order rather than in score order keeps leaves 98…90 and fails here. Together with
// TestBackwardSliceTakesMaxPath this is what makes the max-heap non-optional.
func TestSliceMaxNodes(t *testing.T) {
	const leaves = 99
	const centre = NodeID("tooluse:c")

	g := newTestGraph(t)
	addSliceNode(t, g, centre, KindToolUse, 1)
	for i := leaves - 1; i >= 0; i-- {
		leaf := NodeID(fmt.Sprintf("file:leaf%d", i))
		addSliceNode(t, g, leaf, KindFile, 1)
		addSliceEdge(t, g, leaf, centre, EdgeSharedFile, 1-float32(i)/1000)
	}

	got, err := g.BackwardSlice([]NodeID{centre}, SliceOptions{MaxNodes: 10, Decay: DefaultDecay})
	require.NoError(t, err)
	require.Len(t, got.Scores, 10)
	require.True(t, got.Truncated, "90 reachable nodes were withheld")
	require.Equal(t, 10, got.Visited)

	want := []NodeID{centre}
	for i := range 9 {
		want = append(want, NodeID(fmt.Sprintf("file:leaf%d", i)))
	}
	require.Equal(t, want, got.Order, "the kept set is the centre plus the nine strongest leaves")
}

// TestSliceMaxNodesExactFitNotTruncated pins the distinction Truncated actually draws: it means an
// answer was WITHHELD, not that the cap was touched. A graph with exactly MaxNodes reachable nodes
// is a complete slice, and a consumer that treated it as partial would re-run a walk that had
// nothing left to find.
//
// The graph ends in a diamond (n7 → n9 and n8 → n9) so that the last node finalized still has a
// stale, lower-scored duplicate of itself sitting in the queue at the moment the cap is reached.
// That is the case a naive "is the queue empty?" check gets wrong.
func TestSliceMaxNodesExactFitNotTruncated(t *testing.T) {
	g := newTestGraph(t)
	ids := addFileChain(t, g, "n", 9, EdgeProduces) // n0 → … → n8
	last := NodeID("file:n9")
	addSliceNode(t, g, last, KindFile, 0)
	addSliceEdge(t, g, ids[8], last, EdgeProduces, 1)
	addSliceEdge(t, g, ids[7], last, EdgeProduces, 0.5) // the weaker route, queued first

	got, err := g.ForwardSlice([]NodeID{ids[0]}, SliceOptions{MaxNodes: 10, Decay: DefaultDecay})
	require.NoError(t, err)
	require.Len(t, got.Scores, 10, "the whole graph fits in the cap exactly")
	require.False(t, got.Truncated, "reaching the cap is not the same as withholding an answer")
}

// TestSliceMaxDepth pins the hop bound: a node AT the depth limit is scored, but is not expanded,
// so a MaxDepth of 2 yields the criterion plus two hops. A depth bound is not a truncation — the
// caller asked for a bounded neighbourhood and got exactly that — so Truncated stays false.
func TestSliceMaxDepth(t *testing.T) {
	g := newTestGraph(t)
	ids := addFileChain(t, g, "d", 6, EdgeProduces)

	got, err := g.ForwardSlice([]NodeID{ids[0]}, SliceOptions{MaxDepth: 2, Decay: DefaultDecay})
	require.NoError(t, err)
	require.Len(t, got.Scores, 3, "the criterion and two hops")
	require.Equal(t, []NodeID{ids[0], ids[1], ids[2]}, got.Order)
	require.False(t, got.Truncated, "a depth bound is the answer that was asked for, not a partial one")
}

// TestSliceMinScoreFloor pins the traversal floor. A chain of sequence edges decays by
// Decay · 0.60 = 0.51 per hop, so it crosses minScore long before its fortieth node and the walk
// simply stops caring: §6.4's whole claim is that relevance, not adjacency, decides how far a slice
// reaches. Crucially Truncated stays FALSE — the walk reached irrelevance, it did not withhold an
// answer, and a caller must not retry it with a bigger budget.
//
// The expected count is derived here with the same left-associative float32 arithmetic the
// traversal uses, rather than transcribed from a run: score = ((s · Decay) · Multiplier) · Weight.
func TestSliceMinScoreFloor(t *testing.T) {
	const hops = 40

	g := newTestGraph(t)
	ids := addFileChain(t, g, "s", hops+1, EdgeSequence)

	const weight float32 = 1 // every edge in the chain carries the normalized full weight
	want := 1                // the criterion itself
	for s := float32(1); ; want++ {
		s = s * DefaultDecay * EdgeSequence.Multiplier() * weight
		if s < minScore {
			break
		}
	}

	got, err := g.ForwardSlice([]NodeID{ids[0]}, SliceOptions{Decay: DefaultDecay})
	require.NoError(t, err)
	require.Equal(t, want, len(got.Scores))
	require.Less(t, len(got.Scores), hops, "the floor stops the walk well short of the chain's end")
	require.False(t, got.Truncated, "a score floor is not a truncation")
	for _, id := range got.Order {
		require.GreaterOrEqual(t, got.Scores[id], minScore)
	}
}

// TestSliceUnknownCriteriaEmpty pins the error contract: a slice returns a non-nil error only when
// the graph is closed. A criterion that was never observed, an empty criterion set and a criterion
// the store's GC has tombstoned are all ordinary answers — an empty, valid, complete slice — because
// the caller asking about a node this graph has never heard of is a normal event in a session whose
// log interleaves (D-6), not a failure.
func TestSliceUnknownCriteriaEmpty(t *testing.T) {
	cases := []struct {
		name     string
		criteria []NodeID
	}{
		{"never observed", []NodeID{"tooluse:nope"}},
		{"nil criteria", nil},
		{"empty criteria", []NodeID{}},
		{"unparseable id", []NodeID{"not-a-node-id"}},
		{"tombstoned", []NodeID{"file:gone"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newTestGraph(t)
			addSliceNode(t, g, "file:gone", KindFile, 1)
			addSliceNode(t, g, "file:live", KindFile, 1)
			addSliceEdge(t, g, "file:gone", "file:live", EdgeSharedFile, 1)
			require.NoError(t, g.Tombstone([]NodeID{"file:gone"}))

			for _, dir := range []string{"backward", "forward"} {
				got, err := g.BackwardSlice(tc.criteria, SliceOptions{Decay: DefaultDecay})
				if dir == "forward" {
					got, err = g.ForwardSlice(tc.criteria, SliceOptions{Decay: DefaultDecay})
				}
				require.NoError(t, err, dir)
				require.Empty(t, got.Scores, dir)
				require.Empty(t, got.Order, dir)
				require.Equal(t, 0, got.Visited, dir)
				require.False(t, got.Truncated, dir)
			}
		})
	}
}

// TestSliceOrderTieBreak pins §5.9's stable order: descending score, ties broken by the LOWER turn
// first, remaining ties by NodeID ascending. Three criteria all score exactly 1, so every
// comparison here falls through to the tiebreak. The order is not cosmetic — it decides which nodes
// survive a MaxNodes cap, and the goldens depend on it being byte-reproducible across platforms.
func TestSliceOrderTieBreak(t *testing.T) {
	g := newTestGraph(t)
	addSliceNode(t, g, "file:c", KindFile, 5)
	addSliceNode(t, g, "file:b", KindFile, 3)
	addSliceNode(t, g, "file:a", KindFile, 3)

	got, err := g.BackwardSlice([]NodeID{"file:c", "file:b", "file:a"}, SliceOptions{Decay: DefaultDecay})
	require.NoError(t, err)
	require.Equal(t, []NodeID{"file:a", "file:b", "file:c"}, got.Order)
	for _, id := range got.Order {
		require.InDelta(t, float32(1), got.Scores[id], 0)
	}
}

// TestDefaultSliceOptionsFromConfig is the mechanical link between Appendix C's
// "selection": { "slicing": "thin" } and SliceOptions.Thin. §5.9 documents thin slicing as the
// production default, but Go's zero value for a bool is false, so the default can only arrive
// through DefaultSliceOptions — this test is what proves that wire is connected and that a
// "full" configuration really does buy the more expensive walk.
func TestDefaultSliceOptionsFromConfig(t *testing.T) {
	thin := DefaultSliceOptions(config.Defaults())
	require.True(t, thin.Thin, `Appendix C ships "slicing": "thin"`)
	require.InDelta(t, DefaultDecay, thin.Decay, 0)
	require.Equal(t, DefaultMaxNodes, thin.MaxNodes)
	require.Equal(t, 5*time.Millisecond, thin.Deadline)
	require.Equal(t, 0, thin.MaxDepth, "the default walk is not depth-bounded")

	cfg := config.Defaults()
	cfg.Selection.Slicing = "full"
	require.False(t, DefaultSliceOptions(cfg).Thin)

	// Anything that is not "thin" is treated as full: a misconfigured key must not silently buy
	// the cheaper walk under a name nobody recognizes.
	cfg.Selection.Slicing = "bogus"
	require.False(t, DefaultSliceOptions(cfg).Thin)
}

// TestSliceOptionsWithDefaults pins the normalization every walk applies before its inner loop, so
// the traversal never has to guard against a nonsensical value per hop. Thin is deliberately absent
// from the table: false is a meaningful choice (a full slice) and withDefaults must never touch it.
func TestSliceOptionsWithDefaults(t *testing.T) {
	cases := []struct {
		name string
		in   SliceOptions
		want SliceOptions
	}{
		{
			"zero value",
			SliceOptions{},
			SliceOptions{Decay: DefaultDecay, MaxNodes: DefaultMaxNodes},
		},
		{
			"negative decay",
			SliceOptions{Decay: -1},
			SliceOptions{Decay: DefaultDecay, MaxNodes: DefaultMaxNodes},
		},
		{
			"decay above one would let a hop amplify",
			SliceOptions{Decay: 1.5},
			SliceOptions{Decay: DefaultDecay, MaxNodes: DefaultMaxNodes},
		},
		{
			"decay of exactly one is legal",
			SliceOptions{Decay: 1},
			SliceOptions{Decay: 1, MaxNodes: DefaultMaxNodes},
		},
		{
			"non-positive max nodes",
			SliceOptions{Decay: 0.5, MaxNodes: -7},
			SliceOptions{Decay: 0.5, MaxNodes: DefaultMaxNodes},
		},
		{
			"negative depth and deadline become zero",
			SliceOptions{Decay: 0.5, MaxNodes: 3, MaxDepth: -1, Deadline: -time.Second},
			SliceOptions{Decay: 0.5, MaxNodes: 3},
		},
		{
			"a zero deadline legally means no deadline",
			SliceOptions{Decay: 0.5, MaxNodes: 3, Deadline: 0},
			SliceOptions{Decay: 0.5, MaxNodes: 3},
		},
		{
			"thin is never modified when true",
			SliceOptions{Thin: true},
			SliceOptions{Thin: true, Decay: DefaultDecay, MaxNodes: DefaultMaxNodes},
		},
		{
			"thin is never modified when false",
			SliceOptions{Thin: false},
			SliceOptions{Thin: false, Decay: DefaultDecay, MaxNodes: DefaultMaxNodes},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.in.withDefaults())
		})
	}
}

// propSliceGraph builds a random graph and a random criterion set from rapid draws. Endpoints are
// drawn from the node set itself, so nothing dangles here — D-6's dangling case has its own
// coverage in graph_test.go, and these properties are about the arithmetic.
func propSliceGraph(rt *rapid.T, root string) (*graph, []NodeID) {
	g := newGraph(root, config.Defaults(), logging.Nop())

	nodeCount := rapid.IntRange(1, 60).Draw(rt, "nodes")
	ids := make([]NodeID, nodeCount)
	for i := range nodeCount {
		kind := NodeKind(rapid.IntRange(0, int(KindSegment)).Draw(rt, fmt.Sprintf("kind%d", i)))
		turn := core.TurnIndex(rapid.IntRange(0, 20).Draw(rt, fmt.Sprintf("turn%d", i)))
		ids[i] = newNodeID(kind, strconv.Itoa(i))
		if err := g.AddNode(Node{ID: ids[i], Kind: kind, Turn: turn, Pos: i}); err != nil {
			rt.Fatalf("AddNode: %v", err)
		}
	}

	edgeCount := rapid.IntRange(0, 200).Draw(rt, "edges")
	for i := range edgeCount {
		from := ids[rapid.IntRange(0, nodeCount-1).Draw(rt, fmt.Sprintf("from%d", i))]
		to := ids[rapid.IntRange(0, nodeCount-1).Draw(rt, fmt.Sprintf("to%d", i))]
		if from == to {
			continue // self-loops are rejected by design; not what these properties are about
		}
		kind := EdgeKind(rapid.IntRange(0, int(EdgeControlOnly)).Draw(rt, fmt.Sprintf("ekind%d", i)))
		weight := rapid.Float32Range(0.01, 1).Draw(rt, fmt.Sprintf("w%d", i))
		if err := g.AddEdge(Edge{From: from, To: to, Kind: kind, Weight: weight}); err != nil {
			rt.Fatalf("AddEdge: %v", err)
		}
	}

	criteria := make([]NodeID, 0, 3)
	for i := range rapid.IntRange(0, 3).Draw(rt, "criteria") {
		criteria = append(criteria, ids[rapid.IntRange(0, nodeCount-1).Draw(rt, fmt.Sprintf("c%d", i))])
	}
	return g, criteria
}

// TestPropThinSliceIsSubsetOfFull asserts that thin slicing can only ever remove reachability and
// lower scores, never add or raise them (D-4): its edge set is a strict subset of the full one, so
// the maximum over its paths is a maximum over fewer paths. A consumer that switches
// selection.slicing from "full" to "thin" is buying a cheaper, weaker answer — never a different
// one — and this is the property that says so.
func TestPropThinSliceIsSubsetOfFull(t *testing.T) {
	root := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		g, criteria := propSliceGraph(rt, root)
		backward := rapid.Bool().Draw(rt, "backward")

		walk := g.ForwardSlice
		if backward {
			walk = g.BackwardSlice
		}
		thin, err := walk(criteria, SliceOptions{Thin: true, Decay: DefaultDecay})
		if err != nil {
			rt.Fatalf("thin slice: %v", err)
		}
		full, err := walk(criteria, SliceOptions{Thin: false, Decay: DefaultDecay})
		if err != nil {
			rt.Fatalf("full slice: %v", err)
		}
		if thin.Truncated || full.Truncated {
			rt.Fatalf("generated graph is smaller than DefaultMaxNodes; nothing may truncate here")
		}

		for id, ts := range thin.Scores {
			fs, ok := full.Scores[id]
			if !ok {
				rt.Fatalf("%q scored %v in the thin slice but is absent from the full one", id, ts)
			}
			if ts > fs {
				rt.Fatalf("%q scored %v thin > %v full", id, ts, fs)
			}
		}
	})
}

// TestPropScoresBoundedAndMonotone asserts the three invariants §8.3's "scores, not keep/drop"
// contract rests on: every score lies in (0,1], every live criterion scores exactly 1, and no hop
// can raise a score, so every non-criterion is at or below Decay. The last one is what makes the
// order meaningful at all — if a hop could amplify, a node's rank would depend on how many edges
// happened to point at it rather than on how close it is to what the caller asked about.
func TestPropScoresBoundedAndMonotone(t *testing.T) {
	root := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		g, criteria := propSliceGraph(rt, root)
		decay := rapid.Float32Range(0.01, 1).Draw(rt, "decay")
		backward := rapid.Bool().Draw(rt, "backward")

		walk := g.ForwardSlice
		if backward {
			walk = g.BackwardSlice
		}
		got, err := walk(criteria, SliceOptions{Decay: decay})
		if err != nil {
			rt.Fatalf("slice: %v", err)
		}

		isCriterion := make(map[NodeID]bool, len(criteria))
		for _, c := range criteria {
			isCriterion[c] = true
		}
		for id, s := range got.Scores {
			if s <= 0 || s > 1 {
				rt.Fatalf("%q scored %v, outside (0,1]", id, s)
			}
			if isCriterion[id] {
				if s != 1 {
					rt.Fatalf("criterion %q scored %v, want exactly 1", id, s)
				}
				continue
			}
			if s > decay {
				rt.Fatalf("%q scored %v above the per-hop decay %v", id, s, decay)
			}
		}
		for _, c := range criteria {
			if _, ok := got.Scores[c]; !ok {
				rt.Fatalf("live criterion %q is missing from its own slice", c)
			}
		}
	})
}

// TestSliceDeadlineTruncates pins §8.4's O3 contract: idle-time slicing runs on the user's think
// time, so a walk that overruns its budget must degrade to a partial answer rather than hold the
// idle worker. MaxNodes is set far above the graph's size on purpose, so the DEADLINE is
// unambiguously what fires. A partial answer says so through Truncated, never through an error.
func TestSliceDeadlineTruncates(t *testing.T) {
	const nodes = 50000

	g := newTestGraph(t)
	ids := addFileChain(t, g, "big", nodes, EdgeProduces)

	start := time.Now()
	got, err := g.ForwardSlice([]NodeID{ids[0]}, SliceOptions{
		MaxNodes: 1_000_000,
		Deadline: time.Microsecond,
		Decay:    1, // no decay, so the score floor cannot be what stops the walk
	})
	elapsed := time.Since(start)

	require.NoError(t, err, "an overrun is a partial answer, not a failure")
	require.True(t, got.Truncated)
	require.Less(t, len(got.Scores), nodes)
	require.NotEmpty(t, got.Scores, "the walk still returns what it managed to reach")
	require.Less(t, elapsed, 50*time.Millisecond)
}
