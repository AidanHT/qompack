package dag

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/logging"
	"github.com/stretchr/testify/require"
)

// newTestGraph returns a fresh in-memory graph rooted at a temp directory. These are in-package
// tests because Open still hands back the SP-01 stub — see stub.go for why the flip waits for
// scored slicing — so the real graph is reachable only through newGraph until then.
func newTestGraph(t *testing.T) *graph {
	t.Helper()
	return newGraph(t.TempDir(), config.Defaults(), logging.Nop())
}

// TestGraphSatisfiesBothContracts pins the two interfaces the concrete graph must satisfy. SP-05
// and SP-12 reach Maintainer by type-asserting the Graph that Open returns, and a silent failure
// there would surface as a disabled idle compaction rather than as a build error.
func TestGraphSatisfiesBothContracts(t *testing.T) {
	var g Graph = newTestGraph(t)
	m, ok := g.(Maintainer)
	require.True(t, ok, "the concrete graph must satisfy dag.Maintainer")
	require.Equal(t, 0, m.Generation(), "a fresh graph has never been compacted")
	require.False(t, m.NeedsCompaction(), "an empty log is never worth rewriting")
}

// TestAddNodeValidation asserts every rejected shape wraps ErrInvalidNode and mutates nothing.
// The kind/prefix cross-check is the one that replaces a zero-value "unset kind" test: NodeKind's
// zero value is KindToolUse, not a sentinel, because the frozen fixture pins "kind":4 to KindFile.
func TestAddNodeValidation(t *testing.T) {
	cases := []struct {
		name string
		node Node
	}{
		{"kind disagrees with id prefix", Node{ID: "file:x", Kind: KindToolUse}},
		{"empty id", Node{ID: "", Kind: KindFile}},
		{"no colon in id", Node{ID: "nocolon", Kind: KindFile}},
		{"unknown prefix", Node{ID: "zz:x", Kind: KindFile}},
		{"empty key", Node{ID: "file:", Kind: KindFile}},
		{"negative pos", Node{ID: "file:x", Kind: KindFile, Pos: -1}},
		{"negative tokens", Node{ID: "file:x", Kind: KindFile, Tokens: -1}},
		{"sentinel kind", Node{ID: "file:x", Kind: KindInvalid}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newTestGraph(t)
			err := g.AddNode(tc.node)
			require.ErrorIs(t, err, ErrInvalidNode)
			require.Equal(t, 0, g.Stats().Nodes, "a rejected node must not be stored")
			require.Equal(t, 0, g.Stats().PendingRecords, "a rejected node must not queue a record")
		})
	}
}

// TestAddNodeUpsertMerge asserts a second AddNode for the same id merges field-wise rather than
// replacing: a non-zero incoming field overwrites, a zero incoming field keeps what is stored.
func TestAddNodeUpsertMerge(t *testing.T) {
	g := newTestGraph(t)
	require.NoError(t, g.AddNode(Node{ID: "tooluse:a", Kind: KindToolUse, Pos: 100, Tokens: 50}))
	require.NoError(t, g.AddNode(Node{ID: "tooluse:a", Kind: KindToolUse, Turn: 7}))

	got, ok := g.Node("tooluse:a")
	require.True(t, ok)
	require.Equal(t, 100, got.Pos, "a zero incoming Pos must not erase the stored one")
	require.EqualValues(t, 50, got.Tokens, "a zero incoming Tokens must not erase the stored one")
	require.EqualValues(t, 7, got.Turn, "a non-zero incoming Turn must overwrite")

	s := g.Stats()
	require.Equal(t, 1, s.Nodes, "an upsert must not add a second node")
	require.Equal(t, 2, s.PendingRecords, "both writes must be recorded, so a reload replays both")
}

// TestAddNodeEphemeralSticky asserts Ephemeral only ever ratchets true: a §8.7 retrieval result
// stays a first-eviction candidate for the rest of the session however it is later re-observed.
func TestAddNodeEphemeralSticky(t *testing.T) {
	g := newTestGraph(t)
	require.NoError(t, g.AddNode(Node{ID: "tooluse:a", Kind: KindToolUse, Ephemeral: true}))
	require.NoError(t, g.AddNode(Node{ID: "tooluse:a", Kind: KindToolUse, Ephemeral: false}))

	got, ok := g.Node("tooluse:a")
	require.True(t, ok)
	require.True(t, got.Ephemeral)
}

// TestAnchorNodePosIsEarliest asserts file, symbol and segment nodes keep their FIRST position,
// while an ordinary node moves to its latest. A file read at token 10,000 and re-read at 90,000
// must stay at 10,000, or every shared-file edge migrates toward the tail and CrossingEdges(p)
// under-reports exactly the coupling §8.4 asks it to measure.
func TestAnchorNodePosIsEarliest(t *testing.T) {
	const early, late = 10000, 90000

	anchors := []struct {
		id   NodeID
		kind NodeKind
	}{
		{"file:src/auth.ts", KindFile},
		{"symbol:src/auth.ts#refreshToken", KindSymbol},
		{"segment:14", KindSegment},
	}
	for _, a := range anchors {
		t.Run(string(a.id), func(t *testing.T) {
			g := newTestGraph(t)
			require.NoError(t, g.AddNode(Node{ID: a.id, Kind: a.kind, Pos: early, Turn: 3}))
			require.NoError(t, g.AddNode(Node{ID: a.id, Kind: a.kind, Pos: late, Turn: 9}))

			got, ok := g.Node(a.id)
			require.True(t, ok)
			require.Equal(t, early, got.Pos, "an anchor node keeps its first position")
			require.EqualValues(t, 3, got.Turn, "an anchor node keeps its first turn")
		})
	}

	t.Run("non-anchor moves", func(t *testing.T) {
		g := newTestGraph(t)
		require.NoError(t, g.AddNode(Node{ID: "tooluse:a", Kind: KindToolUse, Pos: early, Turn: 3}))
		require.NoError(t, g.AddNode(Node{ID: "tooluse:a", Kind: KindToolUse, Pos: late, Turn: 9}))

		got, ok := g.Node("tooluse:a")
		require.True(t, ok)
		require.Equal(t, late, got.Pos, "an ordinary node takes its latest position")
		require.EqualValues(t, 9, got.Turn)
	})
}

// TestAddEdgeValidation asserts every rejected edge wraps ErrInvalidEdge and mutates nothing.
func TestAddEdgeValidation(t *testing.T) {
	cases := []struct {
		name string
		edge Edge
	}{
		{"self-loop", Edge{From: "file:a", To: "file:a", Kind: EdgeSequence, Weight: 1}},
		{"sentinel kind", Edge{From: "file:a", To: "file:b", Kind: EdgeInvalid, Weight: 1}},
		{"out-of-range kind", Edge{From: "file:a", To: "file:b", Kind: EdgeKind(200), Weight: 1}},
		{"NaN weight", Edge{From: "file:a", To: "file:b", Kind: EdgeSequence, Weight: float32(math.NaN())}},
		{"Inf weight", Edge{From: "file:a", To: "file:b", Kind: EdgeSequence, Weight: float32(math.Inf(1))}},
		{"unparseable from", Edge{From: "zz:a", To: "file:b", Kind: EdgeSequence, Weight: 1}},
		{"unparseable to", Edge{From: "file:a", To: "nocolon", Kind: EdgeSequence, Weight: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newTestGraph(t)
			err := g.AddEdge(tc.edge)
			require.ErrorIs(t, err, ErrInvalidEdge)
			require.Equal(t, 0, g.Stats().Edges, "a rejected edge must not be stored")
		})
	}
}

// TestAddEdgeWeightNormalized asserts an unset or out-of-range weight becomes 1 — "full strength",
// not "no influence" — and that a legal weight passes through untouched.
func TestAddEdgeWeightNormalized(t *testing.T) {
	cases := []struct{ in, want float32 }{
		{0, 1},
		{-3, 1},
		{2.5, 1},
		{0.5, 0.5},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("weight_%v", tc.in), func(t *testing.T) {
			g := newTestGraph(t)
			from := NodeID(fmt.Sprintf("file:a%d", i))
			require.NoError(t, g.AddEdge(Edge{From: from, To: "file:b", Kind: EdgeSequence, Weight: tc.in}))

			out := g.Out(from)
			require.Len(t, out, 1)
			require.InDelta(t, tc.want, out[0].Weight, 0)
		})
	}
}

// TestAddEdgeDedup asserts an identical (From, To, Kind) triple folds into the stored edge,
// keeping the strongest weight and the earliest turn, and appending no second record. This is what
// keeps the log linear when a shared-file edge is re-emitted on every read of the same path.
func TestAddEdgeDedup(t *testing.T) {
	g := newTestGraph(t)
	for _, e := range []Edge{
		{From: "file:a", To: "file:b", Kind: EdgeSharedFile, Weight: 0.5, Turn: 9},
		{From: "file:a", To: "file:b", Kind: EdgeSharedFile, Weight: 0.9, Turn: 4},
		{From: "file:a", To: "file:b", Kind: EdgeSharedFile, Weight: 0.7, Turn: 6},
	} {
		require.NoError(t, g.AddEdge(e))
	}

	out := g.Out("file:a")
	require.Len(t, out, 1, "three identical triples are one edge")
	require.InDelta(t, float32(0.9), out[0].Weight, 0, "the strongest weight wins")
	require.EqualValues(t, 4, out[0].Turn, "the earliest turn wins")
	require.Equal(t, 1, g.Stats().Edges)
	require.Equal(t, 1, g.Stats().PendingRecords, "a folded edge appends no second record")
}

// TestAddEdgeDanglingEndpoint asserts an edge whose endpoints are not yet nodes is accepted (D-6):
// the log may interleave, and an observer legitimately emits an edge before its endpoint node.
func TestAddEdgeDanglingEndpoint(t *testing.T) {
	g := newTestGraph(t)
	require.NoError(t, g.AddNode(Node{ID: "tooluse:a", Kind: KindToolUse}))
	require.NoError(t, g.AddEdge(Edge{From: "tooluse:a", To: "file:never-added", Kind: EdgeSharedFile, Weight: 1}))

	s := g.Stats()
	require.Equal(t, 1, s.Edges)
	require.Equal(t, 1, s.Dangling, "an edge with a missing endpoint is counted, not rejected")

	// The moment the missing node arrives the edge stops being dangling: danglingness is
	// computed, never cached.
	require.NoError(t, g.AddNode(Node{ID: "file:never-added", Kind: KindFile}))
	require.Equal(t, 0, g.Stats().Dangling)
}

// TestOutInCopies asserts Out and In hand back fresh slices, so a caller mutating the result
// cannot reach into the graph's own storage.
func TestOutInCopies(t *testing.T) {
	g := newTestGraph(t)
	require.NoError(t, g.AddNode(Node{ID: "tooluse:a", Kind: KindToolUse}))
	require.NoError(t, g.AddNode(Node{ID: "toolresult:a", Kind: KindToolResult}))
	require.NoError(t, g.AddEdge(Edge{From: "tooluse:a", To: "toolresult:a", Kind: EdgeProduces, Weight: 1}))

	out := g.Out("tooluse:a")
	require.Len(t, out, 1)
	out[0].Weight = 0.125
	out[0].Kind = EdgeSupersedes

	again := g.Out("tooluse:a")
	require.Len(t, again, 1)
	require.InDelta(t, float32(1), again[0].Weight, 0, "mutating a returned edge must not reach the graph")
	require.Equal(t, EdgeProduces, again[0].Kind)

	in := g.In("toolresult:a")
	require.Len(t, in, 1)
	in[0].Weight = 0.125
	require.InDelta(t, float32(1), g.In("toolresult:a")[0].Weight, 0)
}

// TestTombstoneHidesNode asserts a tombstoned node vanishes from every read path immediately,
// even though its storage is reclaimed only by Compact.
func TestTombstoneHidesNode(t *testing.T) {
	g := newTestGraph(t)
	require.NoError(t, g.AddNode(Node{ID: "tooluse:a", Kind: KindToolUse, Pos: 10}))
	require.NoError(t, g.AddNode(Node{ID: "tooluse:b", Kind: KindToolUse, Pos: 20}))
	require.NoError(t, g.AddEdge(Edge{From: "tooluse:a", To: "tooluse:b", Kind: EdgeSequence, Weight: 1}))

	require.NoError(t, g.Tombstone([]NodeID{"tooluse:a"}))

	_, found := g.Node("tooluse:a")
	require.False(t, found, "a tombstoned node reports absent")
	require.Empty(t, g.In("tooluse:b"), "an edge incident to a tombstoned node is hidden")
	require.Empty(t, g.Out("tooluse:a"))

	s := g.Stats()
	require.Equal(t, 1, s.Tombstoned)
	require.Equal(t, 1, s.Nodes, "the tombstoned node is no longer live")

	// Re-adding a tombstoned id revives it: the store's GC may collect a root and a later turn
	// may legitimately re-read the same file.
	require.NoError(t, g.AddNode(Node{ID: "tooluse:a", Kind: KindToolUse, Pos: 10}))
	_, found = g.Node("tooluse:a")
	require.True(t, found)
	require.Equal(t, 0, g.Stats().Tombstoned)
}

// TestClosedGraphRejects asserts the contract of a graph that has marked itself closed.
//
// Only Compact's post-rewrite adjacency consistency check sets that flag, and driving a real graph
// into an inconsistent adjacency would mean corrupting it on purpose, so this test reaches the
// state through the setClosedForTest hook instead. The point is the contract, not the route: every
// operation that can return an error returns ErrClosed, every operation that cannot returns its
// documented zero value, and nothing panics.
func TestClosedGraphRejects(t *testing.T) {
	g := newTestGraph(t)
	require.NoError(t, g.AddNode(Node{ID: "tooluse:a", Kind: KindToolUse, Pos: 10}))
	require.NoError(t, g.AddNode(Node{ID: "toolresult:a", Kind: KindToolResult, Pos: 20}))
	require.NoError(t, g.AddEdge(Edge{From: "tooluse:a", To: "toolresult:a", Kind: EdgeProduces, Weight: 1}))

	g.setClosedForTest()

	ctx := context.Background()
	require.ErrorIs(t, g.AddNode(Node{ID: "tooluse:b", Kind: KindToolUse}), ErrClosed)
	require.ErrorIs(t, g.AddEdge(Edge{From: "tooluse:a", To: "tooluse:b", Kind: EdgeSequence, Weight: 1}), ErrClosed)
	require.ErrorIs(t, g.Flush(ctx), ErrClosed)
	require.ErrorIs(t, g.Compact(ctx), ErrClosed)
	require.ErrorIs(t, g.Tombstone([]NodeID{"tooluse:a"}), ErrClosed)

	_, err := g.BackwardSlice([]NodeID{"tooluse:a"}, SliceOptions{})
	require.ErrorIs(t, err, ErrClosed)
	_, err = g.ForwardSlice([]NodeID{"tooluse:a"}, SliceOptions{})
	require.ErrorIs(t, err, ErrClosed)

	// The operations with no error return report their documented zero value rather than
	// pretending the graph is still readable.
	n, ok := g.Node("tooluse:a")
	require.False(t, ok)
	require.Equal(t, Node{}, n)
	require.Nil(t, g.Out("tooluse:a"))
	require.Nil(t, g.In("toolresult:a"))
	require.Nil(t, g.NodesAfter(0))
	require.Equal(t, 0, g.CrossingEdges(15))
	require.NotPanics(t, func() { _ = g.Stats() })
}

// TestConcurrentMutationAndRead runs writers and readers against one graph with no sleeps (§6.1
// bans wall-clock sleeps outright). Under -race this is what proves the RWMutex discipline holds
// and, once index.go lands, that withIndex's rebuild path never deadlocks against a reader.
func TestConcurrentMutationAndRead(t *testing.T) {
	const writers, readers, iterations = 8, 8, 2000

	g := newTestGraph(t)
	var wg sync.WaitGroup

	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range iterations {
				id := NodeID(fmt.Sprintf("tooluse:w%d-%d", w, i))
				res := NodeID(fmt.Sprintf("toolresult:w%d-%d", w, i))
				if err := g.AddNode(Node{ID: id, Kind: KindToolUse, Pos: w*iterations + i}); err != nil {
					t.Errorf("AddNode: %v", err)
					return
				}
				if err := g.AddNode(Node{ID: res, Kind: KindToolResult, Pos: w*iterations + i}); err != nil {
					t.Errorf("AddNode: %v", err)
					return
				}
				if err := g.AddEdge(Edge{From: id, To: res, Kind: EdgeProduces, Weight: 1}); err != nil {
					t.Errorf("AddEdge: %v", err)
					return
				}
			}
		}(w)
	}

	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range iterations {
				_, _ = g.BackwardSlice([]NodeID{"tooluse:w0-0"}, SliceOptions{})
				_ = g.CrossingEdges(i)
				_ = g.NodesAfter(i)
				_ = g.Stats()
				_ = g.Out("tooluse:w0-0")
			}
		}()
	}

	wg.Wait()
	require.Equal(t, writers*iterations*2, g.Stats().Nodes)
}
