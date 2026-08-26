package observer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/stretchr/testify/require"
)

// newRealGraphHarness wires the observer to a REAL dag.Graph. Every row about an EDGE KIND runs
// against one, because dag.turnLinkKind decides EdgeSequence vs EdgeControlOnly by reading the
// graph back through Out and In: asserting that against a double would be asserting the double.
func newRealGraphHarness(t *testing.T, mutators ...func(*Options)) (*harness, dag.Graph) {
	t.Helper()
	g, err := dag.Open(t.TempDir(), config.Defaults(), logging.Nop())
	require.NoError(t, err)

	all := append([]func(*Options){func(o *Options) { o.Graph = g }}, mutators...)
	return newHarness(t, all...), g
}

// setTurn moves a session's turn index the way a user prompt or a Stop will once those entry
// points land (resolved decision 4), so a turn-crossing graph row can be written today.
func (h *harness) setTurn(s core.SessionID, turn core.TurnIndex) {
	st := h.obs.session(s)
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Turn = turn
}

// setSegment opens a segment on a session without going through session.go.
func (h *harness) setSegment(s core.SessionID, id core.SegmentID) {
	st := h.obs.session(s)
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Segment = id
}

// allEdges collects every edge leaving every node of g. NodesAfter(0) returns all of them because
// every observer-minted node carries a non-negative Pos.
func allEdges(g dag.Graph) []dag.Edge {
	var out []dag.Edge
	for _, n := range g.NodesAfter(0) {
		out = append(out, g.Out(n.ID)...)
	}
	return out
}

// findEdge returns the edge from → to of kind, and whether it was found.
func findEdge(edges []dag.Edge, from, to dag.NodeID, kind dag.EdgeKind) (dag.Edge, bool) {
	for _, e := range edges {
		if e.From == from && e.To == to && e.Kind == kind {
			return e, true
		}
	}
	return dag.Edge{}, false
}

// turnLink returns the kind of the assistant → tool_use edge for id, and whether it exists.
func turnLink(edges []dag.Edge, turn core.TurnIndex, id core.ToolUseID) (dag.EdgeKind, bool) {
	for _, e := range edges {
		if e.From == dag.AssistantNode(turn) && e.To == dag.ToolUseNode(id) {
			return e.Kind, true
		}
	}
	return dag.EdgeInvalid, false
}

func TestGraph_ToolUseProducesResult(t *testing.T) {
	h, g := newRealGraphHarness(t)

	h.drive(readOf("toolu_1", "src/auth.ts", "alpha\n"))

	use, ok := g.Node(dag.ToolUseNode("toolu_1"))
	require.True(t, ok)
	require.Equal(t, dag.KindToolUse, use.Kind)
	require.Equal(t, "FileRead", use.Ref, "the tool-use node's Ref is the display name")

	result, ok := g.Node(dag.ToolResultNode("toolu_1"))
	require.True(t, ok)
	require.Equal(t, dag.KindToolResult, result.Kind)

	_, ok = findEdge(allEdges(g), dag.ToolUseNode("toolu_1"), dag.ToolResultNode("toolu_1"), dag.EdgeProduces)
	require.True(t, ok, "tool_use --produces--> tool_result")
}

func TestGraph_SequenceChainAcrossTurns(t *testing.T) {
	h, g := newRealGraphHarness(t)

	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
	h.setTurn(testSession, 2) // the prompt and the assistant turn that answered it
	h.drive(toolUse("toolu_2", "Edit",
		`{"file_path":"src/a.ts","old_string":"alpha","new_string":"beta"}`,
		`{"content":"beta\n"}`))

	edges := allEdges(g)
	_, ok := findEdge(edges, dag.ToolResultNode("toolu_1"), dag.AssistantNode(2), dag.EdgeConsumes)
	require.True(t, ok, "the assistant turn CONSUMED the previous result")
	_, ok = findEdge(edges, dag.AssistantNode(2), dag.ToolUseNode("toolu_2"), dag.EdgeSequence)
	require.True(t, ok, "and emitted this call; the shared file makes the link a sequence edge")
}

func TestGraph_ParallelSiblingsShareATurnAndGetNoConsumesEdge(t *testing.T) {
	h, g := newRealGraphHarness(t)

	h.drive(
		readOf("toolu_1", "src/a.ts", "alpha\n"),
		readOf("toolu_2", "src/b.ts", "beta\n"),
	)

	edges := allEdges(g)
	for _, e := range edges {
		if e.Kind == dag.EdgeConsumes && e.From == dag.ToolResultNode("toolu_1") {
			t.Fatalf("a same-turn sibling must get NO consumes edge (D-7 cycle): %+v", e)
		}
	}
	_, ok := findEdge(edges, dag.AssistantNode(0), dag.ToolUseNode("toolu_1"), dag.EdgeSequence)
	require.True(t, ok)
	_, ok = turnLink(edges, 0, "toolu_2")
	require.True(t, ok, "both siblings hang off the same assistant node")
	requireAcyclic(t, edges)
}

func TestGraph_ObserverOutputIsAcyclic(t *testing.T) {
	h, g := newRealGraphHarness(t)

	// A mixed session: parallel siblings inside a turn, and a turn boundary every third call,
	// which is the shape resolved decision 4 produces once prompts and stops advance the index.
	//
	// One shape is deliberately excluded, exactly as in dag.TestBuilderOutputIsAcyclic: a read
	// emits file → tooluse and a write emits tooluse → file, so reading a file and LATER writing it
	// closes a legitimate loop through that file node. That loop is a real property of §8.1 item
	// 4's shared-state modelling — the Read-then-Edit pattern — and not the D-7 mistake this hunts.
	// Every file therefore gets its single writer at its first touch and only readers afterwards,
	// which leaves the tool_use → tool_result → assistant chain as the only thing that can close a
	// cycle here.
	written := make(map[string]bool)
	turn := core.TurnIndex(0)
	for i := range 200 {
		if i%3 == 0 && i > 0 {
			turn += 2
			h.setTurn(testSession, turn)
		}
		id := fmt.Sprintf("toolu_%d", i)
		path := fmt.Sprintf("src/f%d.ts", i%7)
		switch {
		case !written[path]:
			written[path] = true
			h.drive(toolUse(id, "Write",
				fmt.Sprintf(`{"file_path":%q}`, path), `{"content":"written\n"}`))
		case i%3 == 1:
			h.drive(bashOf(id, "go build ./...", "ok\n"))
		case i%3 == 2:
			h.drive(toolUse(id, "Grep",
				`{"pattern":"x","path":"src"}`, `{"content":"src/f1.ts:1:x"}`))
		default:
			h.drive(readOf(id, path, "alpha\n"))
		}
	}

	requireAcyclic(t, allEdges(g))
}

// requireAcyclic fails unless a topological sort of edges succeeds. It is the observer-level
// counterpart of dag.TestBuilderOutputIsAcyclic, which guards the builders in isolation.
func requireAcyclic(t *testing.T, edges []dag.Edge) {
	t.Helper()

	indeg := make(map[dag.NodeID]int)
	adj := make(map[dag.NodeID][]dag.NodeID)
	seen := make(map[dag.NodeID]bool)
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		indeg[e.To]++
		seen[e.From], seen[e.To] = true, true
	}

	var queue []dag.NodeID
	for id := range seen {
		if indeg[id] == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		visited++
		for _, next := range adj[id] {
			indeg[next]--
			if indeg[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	require.Equal(t, len(seen), visited,
		"the emitted subgraph has a cycle — the D-7 tooluse → toolresult → assistant → tooluse cycle is the likely one")
}

func TestGraph_TurnLinkIsControlOnlyWhenNothingIsShared(t *testing.T) {
	h, g := newRealGraphHarness(t)

	h.drive(
		readOf("toolu_1", "src/a.ts", "alpha\n"),
		bashOf("toolu_2", "date", "Mon Aug 24\n"),
	)

	kind, ok := turnLink(allEdges(g), 0, "toolu_2")
	require.True(t, ok)
	require.Equal(t, dag.EdgeControlOnly, kind,
		"thin slicing has nothing to drop unless the observer produces control-only links")
}

func TestGraph_TurnLinkIsSequenceWhenAFileIsShared(t *testing.T) {
	h, g := newRealGraphHarness(t)

	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
	h.setTurn(testSession, 2)
	h.drive(toolUse("toolu_2", "Edit",
		`{"file_path":"src/a.ts","old_string":"alpha","new_string":"beta"}`,
		`{"content":"beta\n"}`))

	kind, ok := turnLink(allEdges(g), 2, "toolu_2")
	require.True(t, ok)
	require.Equal(t, dag.EdgeSequence, kind)
}

func TestGraph_FirstToolUseOfASessionIsSequence(t *testing.T) {
	h, g := newRealGraphHarness(t)

	h.drive(bashOf("toolu_1", "date", "Mon Aug 24\n"))

	kind, ok := turnLink(allEdges(g), 0, "toolu_1")
	require.True(t, ok)
	require.Equal(t, dag.EdgeSequence, kind,
		"a control-only head would make the opening turn unreachable under a thin slice")
}

func TestGraph_SharedFileOrientation(t *testing.T) {
	h, g := newRealGraphHarness(t)

	h.drive(
		readOf("toolu_read", "src/a.ts", "alpha\n"),
		toolUse("toolu_write", "Write", `{"file_path":"src/a.ts"}`, `{"content":"beta\n"}`),
	)

	require.Equal(t, 1, countKind(g, dag.KindFile), "one file node per path")
	edges := allEdges(g)
	_, ok := findEdge(edges, dag.FileNode("src/a.ts"), dag.ToolUseNode("toolu_read"), dag.EdgeSharedFile)
	require.True(t, ok, "a read CONSUMES the anchor: file → tool_use (D-1)")
	_, ok = findEdge(edges, dag.ToolUseNode("toolu_write"), dag.FileNode("src/a.ts"), dag.EdgeSharedFile)
	require.True(t, ok, "a write PRODUCES it: tool_use → file (D-1)")
}

func TestGraph_NoToolUseToToolUseSharedFileEdge(t *testing.T) {
	h, g := newRealGraphHarness(t)

	h.drive(
		readOf("toolu_1", "src/a.ts", "alpha\n"),
		readOf("toolu_2", "src/a.ts", "alpha\n"),
	)

	for _, e := range allEdges(g) {
		if e.Kind != dag.EdgeSharedFile {
			continue
		}
		require.False(t, isToolUseNode(e.From) && isToolUseNode(e.To),
			"two calls couple through the shared file node, never directly: %+v", e)
	}
}

// isToolUseNode reports whether id is a tool-use node id, through the constructor rather than a
// hand-written prefix.
func isToolUseNode(id dag.NodeID) bool {
	kind, _, ok := dag.ParseNodeID(id)
	return ok && kind == dag.KindToolUse
}

// countKind returns how many nodes of kind g holds.
func countKind(g dag.Graph, kind dag.NodeKind) int {
	n := 0
	for _, node := range g.NodesAfter(0) {
		if node.Kind == kind {
			n++
		}
	}
	return n
}

func TestGraph_SymbolEdges(t *testing.T) {
	syms := &fakeSymbols{ByPath: map[string][]string{"src/auth.ts": {"refreshToken", "parseJWT"}}}
	h, g := newRealGraphHarness(t, func(o *Options) { o.Symbols = syms })

	h.drive(readOf("toolu_1", "src/auth.ts", "alpha\n"))

	require.True(t, hasNode(g, dag.SymbolNode("src/auth.ts", "parseJWT")))
	require.True(t, hasNode(g, dag.SymbolNode("src/auth.ts", "refreshToken")))
	require.Equal(t, dag.NodeID("symbol:src/auth.ts#refreshToken"), dag.SymbolNode("src/auth.ts", "refreshToken"),
		"the frozen golden testdata/golden/contracts/dag/graph-basic.jsonl spells it this way")

	edges := allEdges(g)
	for _, name := range []string{"parseJWT", "refreshToken"} {
		_, ok := findEdge(edges, dag.SymbolNode("src/auth.ts", name), dag.ToolUseNode("toolu_1"), dag.EdgeSharedSymbol)
		require.True(t, ok, "%s's shared-symbol edge is oriented like the file edge", name)
	}

	// Emission ORDER is a call-order property, so it is asserted against the recording double.
	fh := newHarness(t, func(o *Options) { o.Symbols = syms })
	fh.drive(readOf("toolu_1", "src/auth.ts", "alpha\n"))
	var got []string
	for _, n := range fh.Graph.Nodes {
		if n.Kind == dag.KindSymbol {
			got = append(got, n.Ref)
		}
	}
	require.Equal(t, []string{"parseJWT", "refreshToken"}, got,
		"BuildToolUse sorts, so deps.jsonl does not depend on how the extractor walked the file")
}

// hasNode reports whether g holds id.
func hasNode(g dag.Graph, id dag.NodeID) bool {
	_, ok := g.Node(id)
	return ok
}

func TestGraph_SymbolsSkippedAboveCap(t *testing.T) {
	syms := &fakeSymbols{Fixed: []string{"refreshToken"}}
	h, g := newRealGraphHarness(t, func(o *Options) { o.Symbols = syms })

	h.drive(readOf("toolu_1", "src/auth.ts", strings.Repeat("a", symbolScanCap+1)))

	require.Equal(t, 0, countKind(g, dag.KindSymbol))
	require.Zero(t, syms.Calls, "the extractor is not even consulted above the cap")
}

func TestGraph_SymbolsCappedAt64(t *testing.T) {
	names := make([]string, 200)
	for i := range names {
		names[i] = fmt.Sprintf("sym%03d", i)
	}
	h, g := newRealGraphHarness(t, func(o *Options) { o.Symbols = &fakeSymbols{Fixed: names} })

	h.drive(readOf("toolu_1", "src/auth.ts", "alpha\n"))

	require.Equal(t, maxSymbolsPerResult, countKind(g, dag.KindSymbol))
}

func TestGraph_PosIsMonotoneAndPreIncrement(t *testing.T) {
	h, g := newRealGraphHarness(t)
	h.Store.TokenQueue = []core.Tokens{100, 200, 300}

	h.drive(
		readOf("toolu_1", "src/a.ts", "alpha\n"),
		readOf("toolu_2", "src/b.ts", "beta\n"),
		readOf("toolu_3", "src/c.ts", "gamma\n"),
	)

	for i, want := range []int{0, 100, 300} {
		n, ok := g.Node(dag.ToolResultNode(core.ToolUseID(fmt.Sprintf("toolu_%d", i+1))))
		require.True(t, ok)
		require.Equal(t, want, n.Pos, "Pos is the node's START position (decision 5)")
	}
}

func TestGraph_SegmentMembershipPointsIntoTheSegment(t *testing.T) {
	h, g := newRealGraphHarness(t)
	h.setSegment(testSession, 7)

	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))

	edges := allEdges(g)
	_, ok := findEdge(edges, dag.ToolUseNode("toolu_1"), dag.SegmentNode(7), dag.EdgeSequence)
	require.True(t, ok, "members point INTO the segment, matching dag.BuildSegment (SP-07 D-1)")
	_, ok = findEdge(edges, dag.ToolResultNode("toolu_1"), dag.SegmentNode(7), dag.EdgeSequence)
	require.True(t, ok)
	_, ok = findEdge(edges, dag.SegmentNode(7), dag.ToolUseNode("toolu_1"), dag.EdgeSequence)
	require.False(t, ok, "never segment → member")
}

func TestGraph_SegmentNodeAndChainEdgeAtClose(t *testing.T) {
	// session.go's close path lands in a later commit; this pins the SegmentSpec shape the three
	// sessionState.Seg* fields exist to supply, which is the half of the row SP-08 owns.
	g, err := dag.Open(t.TempDir(), config.Defaults(), logging.Nop())
	require.NoError(t, err)

	require.NoError(t, dag.BuildSegment(g, dag.SegmentSpec{
		ID: 6, StartTurn: 20, EndTurn: 32, TS: 1, StartPos: 0,
	}))
	require.NoError(t, dag.BuildSegment(g, dag.SegmentSpec{
		ID: 7, PrevID: 6, StartTurn: 33, EndTurn: 41, TS: 2, StartPos: 104880,
	}))

	n, ok := g.Node(dag.SegmentNode(7))
	require.True(t, ok)
	require.Equal(t, "33-41", n.Ref, "a segment node's Ref is its turn range")
	_, ok = findEdge(allEdges(g), dag.SegmentNode(6), dag.SegmentNode(7), dag.EdgeSequence)
	require.True(t, ok, "the chain runs previous → current")
}

func TestGraph_NilSymbolsTolerated(t *testing.T) {
	h, g := newRealGraphHarness(t, func(o *Options) { o.Symbols = nil })

	require.NotPanics(t, func() { h.drive(readOf("toolu_1", "src/auth.ts", "alpha\n")) })
	require.Equal(t, 0, countKind(g, dag.KindSymbol))
}

func TestGraph_FlushNotCalledPerToolUse(t *testing.T) {
	h := newHarness(t)

	for i := range 10 {
		h.drive(readOf(fmt.Sprintf("toolu_%d", i), "src/a.ts", "alpha\n"))
	}

	require.Zero(t, h.Graph.FlushCalls, "an append+fsync per tool use is not the ingest path")
	require.Zero(t, h.Store.FlushCalls)
}

func TestGraph_IDsComeFromDagConstructors(t *testing.T) {
	syms := &fakeSymbols{Fixed: []string{"refreshToken"}}
	h := newHarness(t, func(o *Options) { o.Symbols = syms })
	h.setSegment(testSession, 7)
	h.setTurn(testSession, 3)

	h.drive(readOf("toolu_1", "src/auth.ts", "alpha\n"))

	want := map[dag.NodeID]bool{
		dag.ToolUseNode("toolu_1"):                    true,
		dag.ToolResultNode("toolu_1"):                 true,
		dag.AssistantNode(3):                          true,
		dag.FileNode("src/auth.ts"):                   true,
		dag.SymbolNode("src/auth.ts", "refreshToken"): true,
	}
	for _, n := range h.Graph.Nodes {
		require.True(t, want[n.ID], "node id %q was not built by a dag constructor", n.ID)
	}
	for _, e := range h.Graph.Edges {
		require.True(t, want[e.From] || e.From == dag.SegmentNode(7), "edge From %q", e.From)
		require.True(t, want[e.To] || e.To == dag.SegmentNode(7), "edge To %q", e.To)
	}

	require.Equal(t, dag.NodeID("assistant:3"), dag.AssistantNode(3), "no session component")
	require.Equal(t, dag.NodeID("userprompt:3"), dag.UserPromptNode(3), "no session component")
}
