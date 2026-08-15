package dag

import (
	"sort"
	"strconv"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// The builder tests are in-package (package dag, not dag_test) for the same reason every other test
// file here is: they reach the concrete graph through newTestGraph, which is what lets them assert
// on the unexported helpers the builders are built from — sharesState and sortedUniqueSymbols —
// rather than only on their observable effect. The exported surface they exercise is the same one an
// out-of-package caller sees, so nothing here depends on being in-package to be true.

// The fixture vocabulary the §8.1 item 4 tests share. builderPath and builderOtherPath are already
// in paths.Key form (forward slashes, project-relative), which is what FileNode requires of its
// caller; builderResultPos sits after builderPos because the tool_result block is emitted into the
// prefix after the tool_use block that produced it.
const (
	builderPath      = "src/auth.ts"
	builderOtherPath = "src/other.ts"
	builderSymbol    = "refreshToken"
	builderTool      = "Read"
	builderTurn      = core.TurnIndex(12)
	builderPrevTurn  = builderTurn - 1
	builderPos       = 4700
	builderResultPos = 4900
	builderTS        = core.UnixMilli(1767225480000)
)

// builderPrevID is the tool use every "there was an earlier call" fixture names.
const builderPrevID = core.ToolUseID("toolu_prev")

// builderID is the tool use under test.
const builderID = core.ToolUseID("toolu_x")

// builderFindEdge returns the from → to edge of kind, reporting whether the graph holds one. It
// reads through the public Out accessor rather than the graph's own storage, so it asserts what a
// consumer of the package can actually observe.
func builderFindEdge(g Graph, from, to NodeID, kind EdgeKind) (Edge, bool) {
	for _, e := range g.Out(from) {
		if e.To == to && e.Kind == kind {
			return e, true
		}
	}
	return Edge{}, false
}

// builderRequireEdge fails unless the graph holds the from → to edge of kind, and returns it.
func builderRequireEdge(t *testing.T, g Graph, from, to NodeID, kind EdgeKind) Edge {
	t.Helper()
	e, ok := builderFindEdge(g, from, to, kind)
	require.Truef(t, ok, "expected a %s edge %s -> %s", kind, from, to)
	return e
}

// builderRequireNoEdge fails if the graph holds the from → to edge of kind.
func builderRequireNoEdge(t *testing.T, g Graph, from, to NodeID, kind EdgeKind) {
	t.Helper()
	_, ok := builderFindEdge(g, from, to, kind)
	require.Falsef(t, ok, "did not expect a %s edge %s -> %s", kind, from, to)
}

// builderRequireNode fails unless id names a live node, and returns it.
func builderRequireNode(t *testing.T, g Graph, id NodeID) Node {
	t.Helper()
	n, ok := g.Node(id)
	require.Truef(t, ok, "expected node %s to exist", id)
	return n
}

// builderSeedPrev seeds the graph with a MINIMAL earlier tool use: its tool-use node plus the one
// shared-state edge recording which file it read.
//
// Its file node and its own tool_result node are deliberately left absent. That is what makes every
// node BuildToolUse is required to add genuinely new (test 54 counts exactly five), and it exercises
// D-6 at the same time: sharesState has to recognize the shared file from an edge whose far endpoint
// has not been observed yet, and step 4's consumes edge legitimately starts at a tool_result node
// that is not in the graph.
func builderSeedPrev(t *testing.T, g Graph, pathKey string) {
	t.Helper()
	require.NoError(t, g.AddNode(Node{
		ID: ToolUseNode(builderPrevID), Kind: KindToolUse,
		Turn: builderPrevTurn, TS: builderTS, Pos: builderPos - 1, Ref: builderTool,
	}))
	require.NoError(t, g.AddEdge(Edge{
		From: FileNode(pathKey), To: ToolUseNode(builderPrevID),
		Kind: EdgeSharedFile, Weight: 1, Turn: builderPrevTurn,
	}))
}

// builderObserved returns the observation tests 54, 55, 62 and their siblings build from: a read of
// builderPath at turn builderTurn, following builderPrevID one turn earlier.
func builderObserved() ObservedTool {
	return ObservedTool{
		ToolUseID:     builderID,
		PrevToolUseID: builderPrevID,
		PrevTurn:      builderPrevTurn,
		Turn:          builderTurn,
		TS:            builderTS,
		Pos:           builderPos,
		ResultPos:     builderResultPos,
		Tool:          builderTool,
		PathKey:       builderPath,
		Symbols:       []string{builderSymbol},
	}
}

// TestBuildToolUseEmitsSection814Edges is the primary §8.1 item 4 assertion: one observed tool call
// with a shared file and a shared symbol produces exactly five nodes and exactly five edges, in the
// acyclic shape D-7 fixes.
//
// The consumes edge is the one to read carefully. It starts at the PREVIOUS tool result, not at this
// one: the assistant node in the middle of tool_use → tool_result → assistant_turn → next_tool_use
// is the reasoning that READ the previous result and emitted THIS tool use, so it is keyed on this
// tool use's turn and consumes the previous turn's output. The tempting alternative —
// toolresult:x → assistant:12 alongside assistant:12 → tooluse:x — closes a three-node cycle, which
// TestBuilderOutputIsAcyclic exists to catch.
func TestBuildToolUseEmitsSection814Edges(t *testing.T) {
	g := newTestGraph(t)
	builderSeedPrev(t, g, builderPath)
	before := g.Stats()

	require.NoError(t, BuildToolUse(g, builderObserved()))

	after := g.Stats()
	require.Equal(t, 5, after.Nodes-before.Nodes, "tooluse, toolresult, assistant, file and symbol")
	require.Equal(t, 5, after.Edges-before.Edges, "produces, consumes, sequence, shared_file, shared_symbol")

	use := ToolUseNode(builderID)
	result := ToolResultNode(builderID)
	assistant := AssistantNode(builderTurn)
	file := FileNode(builderPath)
	symbol := SymbolNode(builderPath, builderSymbol)

	require.Equal(t, NodeID("tooluse:toolu_x"), use, "the D-2 prefixes are the long forms")
	require.Equal(t, NodeID("symbol:src/auth.ts#refreshToken"), symbol)

	useNode := builderRequireNode(t, g, use)
	require.Equal(t, KindToolUse, useNode.Kind)
	require.Equal(t, builderPos, useNode.Pos)
	require.Equal(t, builderTool, useNode.Ref, "a tool-use node's Ref is the tool NAME")
	require.Equal(t, builderTurn, useNode.Turn)
	require.Equal(t, builderTS, useNode.TS)

	resultNode := builderRequireNode(t, g, result)
	require.Equal(t, KindToolResult, resultNode.Kind)
	require.Equal(t, builderResultPos, resultNode.Pos, "the result block follows the tool_use block")
	require.Equal(t, string(builderID), resultNode.Ref, "a tool-result node's Ref is its tool_use id")

	assistantNode := builderRequireNode(t, g, assistant)
	require.Equal(t, KindAssistant, assistantNode.Kind)
	require.Equal(t, builderPos, assistantNode.Pos, "the assistant block precedes the tool_use it contains")

	require.Equal(t, KindFile, builderRequireNode(t, g, file).Kind)
	require.Equal(t, KindSymbol, builderRequireNode(t, g, symbol).Kind)

	produces := builderRequireEdge(t, g, use, result, EdgeProduces)
	require.Equal(t, float32(1), produces.Weight)
	builderRequireEdge(t, g, ToolResultNode(builderPrevID), assistant, EdgeConsumes)
	builderRequireEdge(t, g, assistant, use, EdgeSequence)
	builderRequireEdge(t, g, file, use, EdgeSharedFile)
	builderRequireEdge(t, g, symbol, use, EdgeSharedSymbol)

	// D-7, stated as an assertion rather than only as a comment: this tool use's OWN result must
	// not feed the assistant node that emitted it.
	builderRequireNoEdge(t, g, result, assistant, EdgeConsumes)
}

// TestBuildToolUseParallelSiblingNoConsumesEdge covers the parallel-tool-call case: two tool uses in
// one assistant message share a turn index, so the previous tool use hangs off the SAME assistant
// node. Emitting toolresult:prev → assistant:<Turn> there would re-create exactly the cycle D-7
// forbids (assistant → tooluse:prev → toolresult:prev → assistant), so the consumes edge is not
// emitted at all: siblings of one assistant turn are both PRODUCED by that turn, neither consuming
// the other.
func TestBuildToolUseParallelSiblingNoConsumesEdge(t *testing.T) {
	g := newTestGraph(t)

	first := builderObserved()
	first.ToolUseID = builderPrevID
	first.PrevToolUseID = ""
	first.PrevTurn = 0
	require.NoError(t, BuildToolUse(g, first))

	sibling := builderObserved()
	sibling.PrevTurn = builderTurn // the parallel-call case: same assistant message
	require.NoError(t, BuildToolUse(g, sibling))

	assistant := AssistantNode(builderTurn)
	builderRequireEdge(t, g, assistant, ToolUseNode(builderPrevID), EdgeSequence)
	builderRequireEdge(t, g, assistant, ToolUseNode(builderID), EdgeSequence)

	builderRequireNoEdge(t, g, ToolResultNode(builderPrevID), assistant, EdgeConsumes)
	for _, e := range g.Out(ToolResultNode(builderPrevID)) {
		require.NotEqualf(t, assistant, e.To,
			"a sibling's result must not feed the assistant turn that emitted it (D-7); found a %s edge", e.Kind)
	}
	for _, e := range g.In(assistant) {
		require.NotEqual(t, EdgeConsumes, e.Kind, "no consumes edge may enter a purely-parallel assistant turn")
	}
}

// TestBuildToolUseRejectsFuturePrevTurn asserts a previous tool use dated AFTER this one is refused
// outright rather than silently producing a backwards chain. It is invalid input, not a degraded
// observation: nothing in a transcript can make tool use N's predecessor arrive at a later turn, so
// the only honest response is ErrInvalidEdge with the graph untouched.
func TestBuildToolUseRejectsFuturePrevTurn(t *testing.T) {
	g := newTestGraph(t)
	before := g.Stats()

	o := builderObserved()
	o.PrevTurn = builderTurn + 1

	err := BuildToolUse(g, o)
	require.ErrorIs(t, err, ErrInvalidEdge)

	after := g.Stats()
	require.Equal(t, before.Nodes, after.Nodes, "a rejected observation must not add nodes")
	require.Equal(t, before.Edges, after.Edges, "a rejected observation must not add edges")
	require.Equal(t, before.PendingRecords, after.PendingRecords, "a rejected observation must not queue a record")
}

// TestBuildToolUseFirstOfSession asserts the very first tool call of a session — no predecessor at
// all — still gets its assistant → tool_use edge, and gets it as EdgeSequence rather than
// EdgeControlOnly. Without it the head of the graph would be disconnected, and a thin slice (which
// drops control-only edges) would never reach the session's opening turn.
func TestBuildToolUseFirstOfSession(t *testing.T) {
	g := newTestGraph(t)

	o := builderObserved()
	o.PrevToolUseID = ""
	o.PrevTurn = 0
	require.NoError(t, BuildToolUse(g, o))

	assistant := AssistantNode(builderTurn)
	builderRequireEdge(t, g, assistant, ToolUseNode(builderID), EdgeSequence)
	for _, e := range g.In(assistant) {
		require.NotEqual(t, EdgeConsumes, e.Kind, "there is no previous result for the first tool use to consume")
	}
}

// TestBuildToolUseWriteDirection asserts D-1 for shared state: an Edit/Write/MultiEdit PRODUCES the
// file and the symbols it touched, so both edges reverse relative to a read.
func TestBuildToolUseWriteDirection(t *testing.T) {
	g := newTestGraph(t)
	builderSeedPrev(t, g, builderPath)

	o := builderObserved()
	o.Tool = "Edit"
	o.Writes = true
	require.NoError(t, BuildToolUse(g, o))

	use := ToolUseNode(builderID)
	file := FileNode(builderPath)
	symbol := SymbolNode(builderPath, builderSymbol)

	builderRequireEdge(t, g, use, file, EdgeSharedFile)
	builderRequireEdge(t, g, use, symbol, EdgeSharedSymbol)
	builderRequireNoEdge(t, g, file, use, EdgeSharedFile)
	builderRequireNoEdge(t, g, symbol, use, EdgeSharedSymbol)
}

// TestBuildToolUseControlOnlyWhenNoSharedState asserts the assistant → tool_use edge degrades to
// EdgeControlOnly when this tool call shares no file and no symbol with its predecessor. That is
// precisely the edge §6.4's thin slicing drops: the turns are adjacent in time but nothing flows
// between them, and §6.4 is explicit that adjacency is only a proxy for relevance.
func TestBuildToolUseControlOnlyWhenNoSharedState(t *testing.T) {
	g := newTestGraph(t)
	builderSeedPrev(t, g, builderOtherPath)

	o := builderObserved()
	o.Symbols = nil
	require.NoError(t, BuildToolUse(g, o))

	assistant := AssistantNode(builderTurn)
	use := ToolUseNode(builderID)
	builderRequireEdge(t, g, assistant, use, EdgeControlOnly)
	builderRequireNoEdge(t, g, assistant, use, EdgeSequence)
}

// TestBuildToolUseSharedSymbolAloneIsSharedState asserts sharesState is not file-only: two calls
// that touch different paths but the same symbol are still data-coupled, and their link must stay
// a sequence edge that thin slicing keeps.
func TestBuildToolUseSharedSymbolAloneIsSharedState(t *testing.T) {
	g := newTestGraph(t)
	require.NoError(t, g.AddNode(Node{
		ID: ToolUseNode(builderPrevID), Kind: KindToolUse, Turn: builderPrevTurn, Pos: builderPos - 1,
	}))
	require.NoError(t, g.AddEdge(Edge{
		From: SymbolNode(builderPath, builderSymbol), To: ToolUseNode(builderPrevID),
		Kind: EdgeSharedSymbol, Weight: 1, Turn: builderPrevTurn,
	}))

	require.NoError(t, BuildToolUse(g, builderObserved()))
	builderRequireEdge(t, g, AssistantNode(builderTurn), ToolUseNode(builderID), EdgeSequence)
}

// TestBuildToolUseSupersedes covers §8.1 item 3: a re-read that replaces an earlier read of the same
// content records a supersedes edge from the SUPERSEDED tool use to the superseding one (D-1:
// earlier → later, producer → consumer, with no exception for this kind). The direction is what
// makes the superseded read the first eviction candidate rather than something a slice resurrects.
func TestBuildToolUseSupersedes(t *testing.T) {
	g := newTestGraph(t)
	builderSeedPrev(t, g, builderPath)

	const superseded = core.ToolUseID("toolu_old")
	o := builderObserved()
	o.Supersedes = superseded
	require.NoError(t, BuildToolUse(g, o))

	e := builderRequireEdge(t, g, ToolUseNode(superseded), ToolUseNode(builderID), EdgeSupersedes)
	require.Equal(t, float32(1), e.Weight)
	builderRequireNoEdge(t, g, ToolUseNode(builderID), ToolUseNode(superseded), EdgeSupersedes)
}

// TestBuildToolUseIdempotent asserts replaying one observation changes nothing. It matters because
// the observer legitimately re-derives the same §8.1 item 4 edges on every PostToolUse for a path it
// has already seen; without AddEdge's (From, To, Kind) fold, a session that reads one file two
// hundred times would append two hundred identical shared-file records and dag/deps.jsonl would stop
// being linear in the session's real content.
func TestBuildToolUseIdempotent(t *testing.T) {
	g := newTestGraph(t)
	builderSeedPrev(t, g, builderPath)

	o := builderObserved()
	require.NoError(t, BuildToolUse(g, o))
	first := g.Stats()

	require.NoError(t, BuildToolUse(g, o))
	second := g.Stats()

	require.Equal(t, first.Nodes, second.Nodes, "a replayed observation must not add a node")
	require.Equal(t, first.Edges, second.Edges, "a replayed observation must not add an edge")
	require.Equal(t, first.EdgesByKind, second.EdgesByKind)
	require.Equal(t, first.NodesByKind, second.NodesByKind)
}

// TestBuildSymbolsDeterministicOrder asserts the symbol edges are emitted in ascending name order
// with duplicates folded out. The observer's symbol list arrives in whatever order the extractor
// walked the file in, and dag/deps.jsonl is a byte-compared golden — so an unsorted list would make
// the log's edge order depend on an upstream traversal detail rather than on the session.
func TestBuildSymbolsDeterministicOrder(t *testing.T) {
	g := newTestGraph(t)

	o := builderObserved()
	o.PrevToolUseID = ""
	o.PrevTurn = 0
	o.Symbols = []string{"b", "a", "b"}
	require.NoError(t, BuildToolUse(g, o))

	var names []string
	for _, e := range g.In(ToolUseNode(builderID)) {
		if e.Kind != EdgeSharedSymbol {
			continue
		}
		_, key, ok := ParseNodeID(e.From)
		require.True(t, ok)
		names = append(names, key)
	}
	require.Equal(t, []string{builderPath + "#a", builderPath + "#b"}, names,
		"symbol edges must be appended in ascending name order, with the duplicate folded out")
	require.Equal(t, 2, g.Stats().NodesByKind[KindSymbol.String()])
}

// TestBuildUserPromptEdges asserts the user turn feeds the assistant turn that answered it.
func TestBuildUserPromptEdges(t *testing.T) {
	g := newTestGraph(t)

	require.NoError(t, BuildUserPrompt(g, ObservedPrompt{
		Turn: builderTurn, TS: builderTS, Pos: builderPos, Tokens: 40, Ref: "why is refresh failing?",
	}))

	prompt := builderRequireNode(t, g, UserPromptNode(builderTurn))
	require.Equal(t, KindUserPrompt, prompt.Kind)
	require.Equal(t, builderPos, prompt.Pos)
	require.EqualValues(t, 40, prompt.Tokens)
	builderRequireEdge(t, g, UserPromptNode(builderTurn), AssistantNode(builderTurn), EdgeConsumes)
}

// TestBuildDecisionExplains asserts every evidence node points INTO the decision (D-1: evidence is
// the earlier, explanatory end). §4.4 lists the reasoning behind a decision as non-reconstructible
// content, so this is the highest-value edge family in the graph and its direction has to be the one
// a backward slice from a decision id can actually follow.
func TestBuildDecisionExplains(t *testing.T) {
	g := newTestGraph(t)

	first, second := ToolResultNode("toolu_e1"), ToolResultNode("toolu_e2")
	for _, id := range []NodeID{first, second} {
		require.NoError(t, g.AddNode(Node{ID: id, Kind: KindToolResult, Turn: builderTurn, Pos: builderPos}))
	}

	const decisionID = core.DecisionID("dec_0123456789ab")
	require.NoError(t, BuildDecision(g, DecisionSpec{
		ID: decisionID, Turn: builderTurn, TS: builderTS, Pos: builderPos, Tokens: 90,
		Evidence: []NodeID{first, second},
		Summary:  "refresh tokens are rotated server-side",
	}))

	decision := DecisionNode(decisionID)
	node := builderRequireNode(t, g, decision)
	require.Equal(t, KindDecision, node.Kind)
	require.Equal(t, "refresh tokens are rotated server-side", node.Ref)

	builderRequireEdge(t, g, first, decision, EdgeExplains)
	builderRequireEdge(t, g, second, decision, EdgeExplains)
	require.Len(t, g.In(decision), 2, "exactly one explains edge per evidence node")
	require.Empty(t, g.Out(decision), "a decision explains nothing further; the evidence points at it")
}

// TestBuildDecisionSummaryTruncatedOnRuneBoundary asserts an over-long summary is cut to
// decisionSummaryMaxBytes without splitting a multi-byte rune. A Ref that is not valid UTF-8 would
// come back from dag/deps.jsonl different from the one held in memory, because encoding/json
// rewrites invalid bytes to U+FFFD on the way out.
func TestBuildDecisionSummaryTruncatedOnRuneBoundary(t *testing.T) {
	g := newTestGraph(t)

	// "é" is two bytes and the leading ASCII byte makes the run of them start at an odd offset, so
	// a plain byte-count cut at decisionSummaryMaxBytes lands in the MIDDLE of a rune and the
	// truncation has to back off by one byte to stay valid UTF-8.
	long := "x"
	for len(long) <= decisionSummaryMaxBytes+2 {
		long += "é"
	}

	const decisionID = core.DecisionID("dec_ffffffffffff")
	require.NoError(t, BuildDecision(g, DecisionSpec{ID: decisionID, Turn: builderTurn, Summary: long}))

	ref := builderRequireNode(t, g, DecisionNode(decisionID)).Ref
	require.LessOrEqual(t, len(ref), decisionSummaryMaxBytes)
	require.True(t, len(ref) > decisionSummaryMaxBytes-2, "the cut must take the largest whole-rune prefix that fits")
	require.Equal(t, ref, string([]rune(ref)), "the truncated summary must still be valid UTF-8")
}

// TestBuildEliminationEdges asserts a negative-knowledge record (§5.10) collects its evidence, its
// file and its symbol as INCOMING edges: the elimination is the conclusion, and everything that
// justified it is upstream of it.
func TestBuildEliminationEdges(t *testing.T) {
	g := newTestGraph(t)

	evidence := ToolResultNode("toolu_e1")
	require.NoError(t, g.AddNode(Node{ID: evidence, Kind: KindToolResult, Turn: builderTurn, Pos: builderPos}))

	const recordID = "neg_0001"
	require.NoError(t, BuildElimination(g, EliminationSpec{
		RecordID: recordID, Turn: builderTurn, TS: builderTS, Pos: builderPos,
		PathKey: builderPath, Symbol: builderSymbol, Evidence: []NodeID{evidence},
	}))

	elimination := EliminationNode(recordID)
	require.Equal(t, KindElimination, builderRequireNode(t, g, elimination).Kind)

	builderRequireEdge(t, g, evidence, elimination, EdgeExplains)
	builderRequireEdge(t, g, FileNode(builderPath), elimination, EdgeSharedFile)
	builderRequireEdge(t, g, SymbolNode(builderPath, builderSymbol), elimination, EdgeSharedSymbol)

	in := g.In(elimination)
	require.Len(t, in, 3, "one explains, one shared_file, one shared_symbol")
	require.Empty(t, g.Out(elimination), "every elimination edge points INTO the elimination")
}

// TestBuildSegmentChain asserts closed segments form a chain and collect their members, and that the
// chain edge is visible to CrossingEdges — which is the §8.4 coupling measure the scheduler scores
// candidate cut points with, so a segment boundary that no edge crosses is exactly what it is
// looking for.
func TestBuildSegmentChain(t *testing.T) {
	g := newTestGraph(t)

	// Segment starts at 0 / 1000 / 2000 with each segment's members just past its own start, so the
	// only edge straddling position 1500 is the segment:2 → segment:3 chain edge itself.
	starts := []int{0, 1000, 2000}
	members := make([][]NodeID, len(starts))
	for i, start := range starts {
		id := ToolUseNode(core.ToolUseID("toolu_seg" + strconv.Itoa(i)))
		require.NoError(t, g.AddNode(Node{ID: id, Kind: KindToolUse, Turn: core.TurnIndex(i), Pos: start + 100}))
		members[i] = []NodeID{id}
	}

	var prev core.SegmentID
	for i, start := range starts {
		id := core.SegmentID(i + 1)
		require.NoError(t, BuildSegment(g, SegmentSpec{
			ID: id, PrevID: prev, StartTurn: core.TurnIndex(i), EndTurn: core.TurnIndex(i + 1),
			TS: builderTS, StartPos: start, Tokens: 500, Members: members[i],
		}))
		prev = id
	}

	for i := range starts {
		node := builderRequireNode(t, g, SegmentNode(core.SegmentID(i+1)))
		require.Equal(t, KindSegment, node.Kind)
		require.Equal(t, starts[i], node.Pos)
		builderRequireEdge(t, g, members[i][0], SegmentNode(core.SegmentID(i+1)), EdgeSequence)
	}
	builderRequireEdge(t, g, SegmentNode(1), SegmentNode(2), EdgeSequence)
	builderRequireEdge(t, g, SegmentNode(2), SegmentNode(3), EdgeSequence)
	builderRequireNoEdge(t, g, SegmentNode(2), SegmentNode(1), EdgeSequence)

	require.Equal(t, 1, g.CrossingEdges(1500), "only the segment:2 -> segment:3 chain edge straddles 1500")
}

// TestBuildSegmentNoPrevIsChainHead asserts the first segment of a project gets no chain edge:
// SegmentID is 1-based, so 0 means "none" rather than "segment zero".
func TestBuildSegmentNoPrevIsChainHead(t *testing.T) {
	g := newTestGraph(t)
	require.NoError(t, BuildSegment(g, SegmentSpec{ID: 1, StartTurn: 0, EndTurn: 3, StartPos: 0}))
	require.Empty(t, g.In(SegmentNode(1)), "the chain head has no predecessor and no members here")
}

// builderCycle is one cycle TestBuilderOutputIsAcyclic found, rendered for the failure message.
type builderCycle []NodeID

// String renders a cycle as "a -> b -> c -> a", which is what makes the D-7 regression legible in a
// failure rather than merely detected.
func (c builderCycle) String() string {
	s := ""
	for i, id := range c {
		if i > 0 {
			s += " -> "
		}
		s += string(id)
	}
	return s
}

// builderDFSColour is the three-state mark of an iterative depth-first search.
type builderDFSColour uint8

const (
	builderWhite builderDFSColour = iota // not yet visited
	builderGrey                          // on the current DFS stack
	builderBlack                         // fully explored
)

// builderFindCycle runs an ITERATIVE depth-first search over Out and returns the first directed
// cycle it finds, or nil.
//
// Iterative, not recursive, and deliberately so: the fixture is 200 tool uses — well over a thousand
// nodes — and a recursive walk of a graph that has accidentally become cyclic overflows the stack
// and reports "goroutine stack exceeds limit" instead of naming the cycle. That failure mode would
// be testing Go's stack, not the graph.
//
// A grey successor means the walk has re-entered a node still on the stack, which is the definition
// of a back edge; the path from that node's position in the stack to the current node IS the cycle.
func builderFindCycle(g Graph, ids []NodeID) builderCycle {
	type frame struct {
		id    NodeID
		edges []Edge
		next  int
	}
	colour := make(map[NodeID]builderDFSColour, len(ids))
	depth := make(map[NodeID]int, len(ids))

	for _, start := range ids {
		if colour[start] != builderWhite {
			continue
		}
		colour[start] = builderGrey
		depth[start] = 0
		stack := []frame{{id: start, edges: g.Out(start)}}
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.next >= len(top.edges) {
				colour[top.id] = builderBlack
				stack = stack[:len(stack)-1]
				continue
			}
			next := top.edges[top.next].To
			top.next++
			switch colour[next] {
			case builderGrey:
				cycle := make(builderCycle, 0, len(stack)-depth[next]+1)
				for _, f := range stack[depth[next]:] {
					cycle = append(cycle, f.id)
				}
				return append(cycle, next)
			case builderWhite:
				colour[next] = builderGrey
				depth[next] = len(stack)
				stack = append(stack, frame{id: next, edges: g.Out(next)})
			}
		}
	}
	return nil
}

// The shape of TestBuilderOutputIsAcyclic's session: how many tool uses it observes, how many
// distinct files they touch, and how often it forces a parallel sibling, a supersession, a decision,
// an elimination and a segment. None of these is a tuning knob — they exist to make one fixture
// exercise every builder at once.
const (
	acyclicToolUses      = 200
	acyclicFiles         = 7
	acyclicSiblingEvery  = 3
	acyclicSupersedeGap  = 10
	acyclicDecisionEvery = 25
	acyclicElimEvery     = 30
	acyclicSegmentEvery  = 50
	acyclicPosStep       = 600
)

// TestBuilderOutputIsAcyclic is the D-7 regression guard.
//
// It observes a 200-call session through the builders — sequential turns, parallel siblings sharing
// one assistant turn, supersessions, user prompts, decisions, eliminations and a segment chain — and
// asserts an iterative DFS over Out finds no directed cycle. It fails loudly, naming the cycle, if
// step 4 of BuildToolUse is ever rewritten to consume the CURRENT tool result rather than the
// previous one: that rewrite closes tooluse:x → toolresult:x → assistant:t → tooluse:x, which would
// make every backward slice from a tool use swallow that tool use's own forward chain and would
// corrupt both the relevance scores and CrossingEdges.
//
// One shape is deliberately excluded, and it is worth stating why rather than leaving it implicit.
// A read of a file produces file → tooluse and a write produces tooluse → file, so reading a file
// and LATER writing it closes a legitimate loop through that file node (tooluse_read →  … →
// tooluse_write → file → tooluse_read). That loop is a real property of §8.1 item 4's shared-state
// modelling — the Read-then-Edit pattern — and not the mistake this test hunts. So the fixture gives
// every file a single writer at its first touch and only readers afterwards, which leaves the
// tool_use → tool_result → assistant chain as the only thing that can possibly close a cycle here.
func TestBuilderOutputIsAcyclic(t *testing.T) {
	g := newTestGraph(t)

	written := make(map[string]bool, acyclicFiles)
	turn := core.TurnIndex(1)
	var prevID core.ToolUseID
	prevTurn := core.TurnIndex(0)
	var prevSegment core.SegmentID
	var members []NodeID

	require.NoError(t, BuildUserPrompt(g, ObservedPrompt{Turn: turn, Pos: 0, Ref: "start"}))

	for i := 0; i < acyclicToolUses; i++ {
		// Every acyclicSiblingEvery-th call is a parallel sibling of the one before it: same turn,
		// same assistant node, no consumes edge.
		if i > 0 && i%acyclicSiblingEvery != 0 {
			turn++
			require.NoError(t, BuildUserPrompt(g, ObservedPrompt{Turn: turn, Pos: i * acyclicPosStep}))
		}

		path := "src/pkg" + strconv.Itoa(i%acyclicFiles) + ".ts"
		o := ObservedTool{
			ToolUseID:     core.ToolUseID("toolu_" + strconv.Itoa(i)),
			PrevToolUseID: prevID,
			PrevTurn:      prevTurn,
			Turn:          turn,
			TS:            builderTS + core.UnixMilli(i),
			Pos:           i * acyclicPosStep,
			ResultPos:     i*acyclicPosStep + 1,
			Tool:          builderTool,
			PathKey:       path,
			Writes:        !written[path],
			Symbols:       []string{"sym" + strconv.Itoa(i%acyclicFiles), builderSymbol},
		}
		written[path] = true
		if i >= acyclicSupersedeGap && i%acyclicSupersedeGap == 0 {
			o.Supersedes = core.ToolUseID("toolu_" + strconv.Itoa(i-acyclicSupersedeGap))
		}
		require.NoError(t, BuildToolUse(g, o))

		members = append(members, ToolUseNode(o.ToolUseID))
		prevID, prevTurn = o.ToolUseID, turn

		if i > 0 && i%acyclicDecisionEvery == 0 {
			require.NoError(t, BuildDecision(g, DecisionSpec{
				ID: core.DecisionID("dec_" + strconv.Itoa(i)), Turn: turn, Pos: i * acyclicPosStep,
				Evidence: []NodeID{ToolResultNode(o.ToolUseID), ToolUseNode(o.ToolUseID)},
				Summary:  "decision " + strconv.Itoa(i),
			}))
		}
		if i > 0 && i%acyclicElimEvery == 0 {
			require.NoError(t, BuildElimination(g, EliminationSpec{
				RecordID: "neg_" + strconv.Itoa(i), Turn: turn, Pos: i * acyclicPosStep,
				PathKey: path, Symbol: builderSymbol,
				Evidence: []NodeID{ToolResultNode(o.ToolUseID)},
			}))
		}
		if i > 0 && i%acyclicSegmentEvery == 0 {
			id := core.SegmentID(i / acyclicSegmentEvery)
			require.NoError(t, BuildSegment(g, SegmentSpec{
				ID: id, PrevID: prevSegment, StartTurn: turn, EndTurn: turn,
				StartPos: i * acyclicPosStep, Members: members,
			}))
			prevSegment, members = id, nil
		}
	}

	all := g.NodesAfter(0)
	require.Greater(t, len(all), acyclicToolUses, "fixture sanity: the session must have built a real graph")

	cycle := builderFindCycle(g, idsOf(all))
	require.Nilf(t, cycle, "the builder output must be acyclic (D-7); found: %s", cycle)

	// Fixture sanity: the mix really did exercise every shape the guard claims to cover.
	s := g.Stats()
	for _, kind := range []EdgeKind{
		EdgeProduces, EdgeConsumes, EdgeSequence, EdgeControlOnly,
		EdgeSharedFile, EdgeSharedSymbol, EdgeSupersedes, EdgeExplains,
	} {
		require.NotZerof(t, s.EdgesByKind[kind.String()], "fixture sanity: no %s edge was built", kind)
	}
	require.NotZero(t, s.NodesByKind[KindSegment.String()])
	require.NotZero(t, s.NodesByKind[KindElimination.String()])
	require.NotZero(t, s.NodesByKind[KindDecision.String()])
	require.NotZero(t, s.NodesByKind[KindUserPrompt.String()])
}

// TestSharesStateEmptyObservationIsNeverShared asserts a tool call that names no path and no symbol
// never counts as sharing state, however much the previous call touched. A grep with no path is the
// real case: it is adjacent in time to whatever came before and coupled to none of it.
func TestSharesStateEmptyObservationIsNeverShared(t *testing.T) {
	g := newTestGraph(t)
	builderSeedPrev(t, g, builderPath)

	require.False(t, sharesState(g, builderPrevID, "", nil))
	require.False(t, sharesState(g, builderPrevID, "", []string{}))
	require.True(t, sharesState(g, builderPrevID, builderPath, nil), "fixture sanity: the path itself is shared")
}

// TestSortedUniqueSymbols pins the helper that makes symbol edge order independent of the order the
// extractor happened to walk a file in.
func TestSortedUniqueSymbols(t *testing.T) {
	got := sortedUniqueSymbols([]string{"b", "a", "b", "", "c", "a"})
	require.Equal(t, []string{"a", "b", "c"}, got, "sorted, deduplicated, and empty names dropped")
	require.True(t, sort.StringsAreSorted(got))
	require.Nil(t, sortedUniqueSymbols(nil))
}

// TestReadThenWriteClosesALegitimateCycle pins a property of the §8.1 item 4 edge model that is
// easy to assume away, and that TestBuilderOutputIsAcyclic deliberately does not cover: the FULL
// graph is not a DAG, and cannot be one, under the most ordinary sequence a coding agent performs.
//
// Read src/a.ts at turn 1, edit it at turn 2. §8.1 item 4 directs a shared-file edge from the file
// INTO a tool use that consumed it and OUT of one that produced it, so those two turns close a loop
// through the file node:
//
//	tooluse:t1 → toolresult:t1 → assistant:2 → tooluse:t2 → file:src/a.ts → tooluse:t1
//
// Every one of those five edges is individually correct. D-7's acyclicity rule is about the
// tool_use → tool_result → assistant chain in isolation — an assistant node must never consume the
// result of the tool use it emitted — and that micro-cycle WOULD be a modelling error, because a
// backward slice from a tool use would swallow that tool use's own forward chain at full score.
// This cycle is not that: it is the Read-then-Edit pattern, faithfully recorded.
//
// The consequence worth writing down is that every consumer walking this graph must tolerate
// cycles. Slicing already does, by construction rather than by a visited-set bolted on afterwards:
// scores strictly decrease along any path, each node is finalized exactly once, and the minScore
// floor bounds the walk independently of either. SP-09's negknow detector, which scans this graph
// for the test-fail → revert → different-approach pattern, inherits the same obligation.
func TestReadThenWriteClosesALegitimateCycle(t *testing.T) {
	g := newTestGraph(t)
	const path = "src/a.ts"

	require.NoError(t, BuildToolUse(g, ObservedTool{
		ToolUseID: "t1", Turn: 1, Pos: 100, ResultPos: 200,
		Tool: "Read", PathKey: path, Writes: false,
	}))
	require.NoError(t, BuildToolUse(g, ObservedTool{
		ToolUseID: "t2", PrevToolUseID: "t1", PrevTurn: 1, Turn: 2, Pos: 300, ResultPos: 400,
		Tool: "Edit", PathKey: path, Writes: true,
	}))

	ids := []NodeID{
		ToolUseNode("t1"), ToolResultNode("t1"),
		ToolUseNode("t2"), ToolResultNode("t2"),
		AssistantNode(1), AssistantNode(2), FileNode(path),
	}
	require.NotNil(t, builderFindCycle(g, ids),
		"read-then-write of one file must close a cycle: if this stops being true the shared-file "+
			"edge direction rule has changed, and §8.1 item 4 no longer says what it said")

	// The point of recording it: a walk over a cyclic graph still terminates with bounded scores.
	for _, criterion := range ids {
		sl, err := g.BackwardSlice([]NodeID{criterion}, SliceOptions{Decay: DefaultDecay})
		require.NoError(t, err)
		require.LessOrEqual(t, len(sl.Scores), len(ids), "a cycle must not inflate the slice")
		for id, score := range sl.Scores {
			require.Greater(t, score, float32(0), "%s scored non-positively", id)
			require.LessOrEqual(t, score, float32(1), "%s scored above the criterion itself", id)
		}
		require.False(t, sl.Truncated, "a seven-node graph is never truncated")
	}
}
