package dag

import (
	"fmt"
	"sort"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// addFile is a shorthand for the position-index tests, which care only about a node's Pos, Turn
// and identity.
func addFile(t *testing.T, g *graph, key string, pos int, turn core.TurnIndex) {
	t.Helper()
	require.NoError(t, g.AddNode(Node{ID: NodeID("file:" + key), Kind: KindFile, Ref: key, Pos: pos, Turn: turn}))
}

// idsOf projects a node slice to its ids, so an ordering assertion reads as the order it means.
func idsOf(nodes []Node) []NodeID {
	if len(nodes) == 0 {
		return nil
	}
	ids := make([]NodeID, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}

// TestCrossingEdgesBoundaries pins the half-open rule at both ends. An edge straddles pos exactly
// when lo < pos <= hi, so a cut exactly AT the earlier endpoint does not cross it — nothing has
// been separated yet — while a cut exactly AT the later endpoint does.
func TestCrossingEdgesBoundaries(t *testing.T) {
	g := newTestGraph(t)
	addFile(t, g, "lo", 100, 1)
	addFile(t, g, "hi", 200, 2)
	require.NoError(t, g.AddEdge(Edge{From: "file:lo", To: "file:hi", Kind: EdgeSharedFile, Weight: 1}))

	for _, tc := range []struct{ pos, want int }{
		{0, 0},
		{1, 0},
		{100, 0},
		{101, 1},
		{150, 1},
		{200, 1},
		{201, 0},
	} {
		require.Equal(t, tc.want, g.CrossingEdges(tc.pos), "CrossingEdges(%d)", tc.pos)
	}
}

// TestCrossingEdgesDirectionIrrelevant asserts an edge pointing from the later position back to
// the earlier one couples the same two regions as one pointing forward: lo and hi are a min/max
// over the endpoints' positions, not a from/to.
func TestCrossingEdgesDirectionIrrelevant(t *testing.T) {
	g := newTestGraph(t)
	addFile(t, g, "early", 100, 1)
	addFile(t, g, "late", 200, 2)
	require.NoError(t, g.AddEdge(Edge{From: "file:late", To: "file:early", Kind: EdgeSharedFile, Weight: 1}))

	require.Equal(t, 1, g.CrossingEdges(150))
	require.Equal(t, 0, g.CrossingEdges(100))
	require.Equal(t, 0, g.CrossingEdges(201))
}

// TestCrossingEdgesExcludesDangling asserts an edge with a missing or tombstoned endpoint couples
// nothing: it has no second position, so it cannot straddle anything (D-6).
func TestCrossingEdgesExcludesDangling(t *testing.T) {
	t.Run("missing endpoint", func(t *testing.T) {
		g := newTestGraph(t)
		addFile(t, g, "present", 100, 1)
		require.NoError(t, g.AddEdge(Edge{From: "file:present", To: "file:absent", Kind: EdgeSharedFile, Weight: 1}))
		require.Equal(t, 1, g.Stats().Dangling, "fixture sanity")

		for _, pos := range []int{1, 50, 100, 150, 1000} {
			require.Equal(t, 0, g.CrossingEdges(pos), "CrossingEdges(%d)", pos)
		}
	})

	t.Run("tombstoned endpoint", func(t *testing.T) {
		g := newTestGraph(t)
		addFile(t, g, "lo", 100, 1)
		addFile(t, g, "hi", 200, 2)
		require.NoError(t, g.AddEdge(Edge{From: "file:lo", To: "file:hi", Kind: EdgeSharedFile, Weight: 1}))
		require.Equal(t, 1, g.CrossingEdges(150), "fixture sanity")

		require.NoError(t, g.Tombstone([]NodeID{"file:hi"}))
		require.Equal(t, 0, g.CrossingEdges(150), "a tombstoned endpoint decouples the edge")
	})
}

// TestCrossingEdgesEqualPositions asserts an edge whose endpoints share a position crosses nothing
// anywhere: lo == hi leaves no pos satisfying lo < pos <= hi.
func TestCrossingEdgesEqualPositions(t *testing.T) {
	g := newTestGraph(t)
	addFile(t, g, "a", 500, 1)
	addFile(t, g, "b", 500, 2)
	require.NoError(t, g.AddEdge(Edge{From: "file:a", To: "file:b", Kind: EdgeSharedFile, Weight: 1}))

	for _, pos := range []int{1, 499, 500, 501, 1000} {
		require.Equal(t, 0, g.CrossingEdges(pos), "CrossingEdges(%d)", pos)
	}
}

// TestCrossingEdgesEmptyGraph asserts the degenerate cases answer 0 rather than panicking on an
// index that has never been built.
func TestCrossingEdgesEmptyGraph(t *testing.T) {
	g := newTestGraph(t)
	require.Equal(t, 0, g.CrossingEdges(0))
	require.Equal(t, 0, g.CrossingEdges(-5))
	require.Equal(t, 0, g.CrossingEdges(1000))
	require.Nil(t, g.NodesAfter(0))
}

// TestNodesAfterOrdering asserts NodesAfter returns a totally ordered, live-only suffix. The order
// is (Pos, Turn, ID) — a total order, so two nodes at the same position never swap between runs,
// which is what the goldens rely on.
func TestNodesAfterOrdering(t *testing.T) {
	g := newTestGraph(t)
	addFile(t, g, "d", 30, 1)
	addFile(t, g, "a", 10, 5)
	addFile(t, g, "c", 20, 1)
	addFile(t, g, "b", 10, 2)
	addFile(t, g, "e", 10, 2) // ties with b on (Pos, Turn); the ID breaks it

	require.Equal(t, []NodeID{"file:b", "file:e", "file:a", "file:c", "file:d"}, idsOf(g.NodesAfter(0)))
	require.Equal(t, []NodeID{"file:c", "file:d"}, idsOf(g.NodesAfter(20)))
	require.Equal(t, []NodeID{"file:d"}, idsOf(g.NodesAfter(21)))
	require.Empty(t, g.NodesAfter(31), "a pos beyond every node yields nothing")

	require.NoError(t, g.Tombstone([]NodeID{"file:c"}))
	require.Equal(t, []NodeID{"file:d"}, idsOf(g.NodesAfter(20)), "a tombstoned node leaves the suffix")
}

// TestNodesAfterReturnsFreshSlice asserts a caller mutating the result cannot reach the graph.
func TestNodesAfterReturnsFreshSlice(t *testing.T) {
	g := newTestGraph(t)
	addFile(t, g, "a", 10, 1)

	got := g.NodesAfter(0)
	require.Len(t, got, 1)
	got[0].Pos = 999

	require.Equal(t, 10, g.NodesAfter(0)[0].Pos)
}

// propGraph builds a graph from rapid-drawn nodes and edges, returning it alongside the plain maps
// a brute-force oracle can walk. Endpoints are drawn from a range one wider than the node set, so
// some edges name a node that was never added — the dangling case D-6 permits.
func propGraph(rt *rapid.T, root string) (*graph, map[NodeID]int, map[edgeKey]bool) {
	g := newGraph(root, config.Defaults(), logging.Nop())

	nodeCount := rapid.IntRange(0, 200).Draw(rt, "nodes")
	positions := make(map[NodeID]int, nodeCount)
	for i := range nodeCount {
		ref := fmt.Sprintf("n%d", i)
		id := NodeID("file:" + ref)
		pos := rapid.IntRange(0, 5000).Draw(rt, "pos"+ref)
		if err := g.AddNode(Node{ID: id, Kind: KindFile, Ref: ref, Pos: pos}); err != nil {
			rt.Fatalf("AddNode: %v", err)
		}
		positions[id] = pos
	}

	edgeCount := rapid.IntRange(0, 600).Draw(rt, "edges")
	edges := make(map[edgeKey]bool, edgeCount)
	for i := range edgeCount {
		from := NodeID(fmt.Sprintf("file:n%d", rapid.IntRange(0, nodeCount).Draw(rt, fmt.Sprintf("from%d", i))))
		to := NodeID(fmt.Sprintf("file:n%d", rapid.IntRange(0, nodeCount).Draw(rt, fmt.Sprintf("to%d", i))))
		if from == to {
			continue // self-loops are rejected by design; not what these properties are about
		}
		if err := g.AddEdge(Edge{From: from, To: to, Kind: EdgeSharedFile, Weight: 1}); err != nil {
			rt.Fatalf("AddEdge: %v", err)
		}
		// AddEdge folds duplicate (From, To, Kind) triples into one stored edge, so the oracle
		// records each triple once too.
		edges[edgeKey{from: from, to: to, kind: EdgeSharedFile}] = true
	}
	return g, positions, edges
}

// TestPropCrossingEdgesMatchesBruteForce asserts the two-binary-search identity agrees with a
// linear scan on every generated graph. That identity is the whole reason CrossingEdges is cheap
// enough for SP-12 to call once per candidate cut point, so it is worth proving rather than
// reasoning about.
func TestPropCrossingEdgesMatchesBruteForce(t *testing.T) {
	root := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		g, positions, edges := propGraph(rt, root)
		pos := rapid.IntRange(-10, 5100).Draw(rt, "probe")

		want := 0
		if pos > 0 {
			for e := range edges {
				pf, okFrom := positions[e.from]
				pt, okTo := positions[e.to]
				if !okFrom || !okTo {
					continue // dangling: excluded from the index too
				}
				lo, hi := pf, pt
				if lo > hi {
					lo, hi = hi, lo
				}
				if lo < pos && pos <= hi {
					want++
				}
			}
		}
		if got := g.CrossingEdges(pos); got != want {
			rt.Fatalf("CrossingEdges(%d) = %d, brute force = %d", pos, got, want)
		}
	})
}

// TestPropNodesAfterMatchesFilter asserts NodesAfter equals the sorted filter of live nodes at or
// beyond pos.
func TestPropNodesAfterMatchesFilter(t *testing.T) {
	root := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		g, positions, _ := propGraph(rt, root)
		pos := rapid.IntRange(-10, 5100).Draw(rt, "probe")

		var want []NodeID
		for id, p := range positions {
			if p >= pos {
				want = append(want, id)
			}
		}
		sort.Slice(want, func(i, j int) bool {
			pi, pj := positions[want[i]], positions[want[j]]
			if pi != pj {
				return pi < pj
			}
			return want[i] < want[j] // every node here carries turn 0, so the ID breaks every tie
		})

		if got := idsOf(g.NodesAfter(pos)); fmt.Sprint(got) != fmt.Sprint(want) {
			rt.Fatalf("NodesAfter(%d) = %v, want %v", pos, got, want)
		}
	})
}
