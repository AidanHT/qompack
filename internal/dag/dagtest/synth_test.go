package dagtest_test

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/dag/dagtest"
	"github.com/qompack/qompack/internal/logging"
	"github.com/stretchr/testify/require"
)

// synthSmallSpec is the shape most of these tests generate: big enough to contain every node kind,
// every edge kind and a genuine control-only frontier, small enough to dump and diff in a failure.
func synthSmallSpec() dagtest.SynthSpec {
	return dagtest.SynthSpec{
		Turns:                  40,
		ToolsPerTurn:           3,
		Files:                  6,
		SymbolsPerFile:         4,
		ControlOnlyFraction:    0.35,
		ControlCarriedFraction: 0.12,
		Supersessions:          5,
		TokensPerTool:          600,
	}
}

// synthBenchSpec is the shape the coordinator's benchmarks and the thin-versus-full comparison run
// against — §6.4 sizes a session's dependence graph at "a few thousand nodes", and this is that
// graph. TestSynthBenchShapeIsRightSize records what it actually produces.
func synthBenchSpec() dagtest.SynthSpec {
	return dagtest.SynthSpec{
		Turns:                  400,
		ToolsPerTurn:           3,
		Files:                  40,
		SymbolsPerFile:         6,
		ControlOnlyFraction:    0.35,
		ControlCarriedFraction: 0.12,
		Supersessions:          30,
		TokensPerTool:          600,
	}
}

// synthDump renders a generated graph as JSON, which is what makes "identical" a byte comparison
// rather than a structural walk that could silently ignore a field.
func synthDump(t *testing.T, nodes []dag.Node, edges []dag.Edge) []byte {
	t.Helper()
	b, err := json.Marshal(struct {
		Nodes []dag.Node `json:"nodes"`
		Edges []dag.Edge `json:"edges"`
	}{Nodes: nodes, Edges: edges})
	require.NoError(t, err)
	return b
}

// TestSynthIsByteReproducible is the property the whole generator exists for. The benchmarks and the
// thin-versus-full recall numbers are quoted from one seed; if a given seed stopped producing the
// same graph, a recorded baseline would silently start comparing two different things instead of
// failing.
func TestSynthIsByteReproducible(t *testing.T) {
	firstNodes, firstEdges, firstCriteria, firstTruth := dagtest.Synth(7, synthSmallSpec())
	secondNodes, secondEdges, secondCriteria, secondTruth := dagtest.Synth(7, synthSmallSpec())

	require.Equal(t, synthDump(t, firstNodes, firstEdges), synthDump(t, secondNodes, secondEdges),
		"seed 7 must produce a byte-identical node and edge dump on every call")
	require.Equal(t, firstCriteria, secondCriteria)
	require.Equal(t, sortedIDs(firstTruth), sortedIDs(secondTruth))
}

// TestSynthSeedsDiffer asserts the seed is actually threaded through the generator rather than being
// accepted and ignored — the failure mode that would make every "different seed" in the comparison
// measure the same graph eight times.
func TestSynthSeedsDiffer(t *testing.T) {
	oneNodes, oneEdges, _, _ := dagtest.Synth(1, synthSmallSpec())
	twoNodes, twoEdges, _, _ := dagtest.Synth(2, synthSmallSpec())

	require.NotEqual(t, synthDump(t, oneNodes, oneEdges), synthDump(t, twoNodes, twoEdges),
		"two seeds must produce different graphs")
}

// TestSynthIsAcyclic asserts the generated graph reproduces the acyclic §8.1 item 4 chain of D-7.
// A generator that emitted toolresult:<cur> → assistant:<Turn> would close the same three-node cycle
// the builders are forbidden to write, and every slice measured on it would be measuring a graph the
// production code cannot produce.
func TestSynthIsAcyclic(t *testing.T) {
	nodes, edges, _, _ := dagtest.Synth(7, synthSmallSpec())

	cycle := findCycle(nodes, edges)
	require.Nilf(t, cycle, "the generated graph must be acyclic (D-7); found: %s", renderCycle(cycle))
}

// TestSynthEdgeEndpointsExistBeforeTheEdge asserts the stated emission-order invariant: walking the
// returned slices in order, no edge appears before both of its endpoint nodes have. The graph itself
// does not require it (D-6 makes a dangling edge legal), but a consumer replaying the two slices in
// order into something that is not a dag.Graph does, and this is what makes that safe.
func TestSynthEdgeEndpointsExistBeforeTheEdge(t *testing.T) {
	nodes, edges, _, _ := dagtest.Synth(7, synthSmallSpec())

	present := make(map[dag.NodeID]bool, len(nodes))
	for _, n := range nodes {
		require.Falsef(t, present[n.ID], "node %s emitted twice", n.ID)
		present[n.ID] = true
	}
	for i, e := range edges {
		require.Truef(t, present[e.From], "edge %d (%s -> %s) names a From node that was never emitted", i, e.From, e.To)
		require.Truef(t, present[e.To], "edge %d (%s -> %s) names a To node that was never emitted", i, e.From, e.To)
	}
}

// TestSynthPosNonDecreasingAlongToolChain asserts Pos advances monotonically through the tool-use
// chain. §5.3 makes Pos the field every p-selection question is answered from, and CrossingEdges
// counts edges by the positions of their endpoints — a generator whose positions wandered would make
// both answers meaningless without failing anything.
func TestSynthPosNonDecreasingAlongToolChain(t *testing.T) {
	nodes, _, _, _ := dagtest.Synth(7, synthSmallSpec())

	last := -1
	seen := 0
	for _, n := range nodes {
		if n.Kind != dag.KindToolUse {
			continue
		}
		require.GreaterOrEqualf(t, n.Pos, last, "tool-use positions must not go backwards at %s", n.ID)
		last = n.Pos
		seen++
	}
	require.NotZero(t, seen, "fixture sanity: the generator must emit tool uses")
}

// TestSynthTruthIsNonEmptySubset asserts the ground-truth relevant set is a real, non-empty subset
// of the generated nodes and contains every criterion. It is the denominator of the comparison's
// recall, so an empty or out-of-graph truth set would turn that measurement into a division by zero
// or a number nothing could ever reach.
func TestSynthTruthIsNonEmptySubset(t *testing.T) {
	nodes, _, criteria, truth := dagtest.Synth(7, synthSmallSpec())

	require.NotEmpty(t, criteria)
	require.NotEmpty(t, truth)

	present := make(map[dag.NodeID]bool, len(nodes))
	for _, n := range nodes {
		present[n.ID] = true
	}
	for id := range truth {
		require.Truef(t, present[id], "truth names %s, which is not a generated node", id)
	}
	for _, id := range criteria {
		require.Truef(t, present[id], "criterion %s is not a generated node", id)
		require.Truef(t, truth[id], "a criterion trivially depends on itself and must be in truth: %s", id)
	}
	require.Less(t, len(truth), len(nodes), "truth is a SUBSET: a slice that returned everything would be no slice")
}

// TestSynthCoversEveryKind asserts one generated graph exercises every node kind and every edge kind
// the package declares. A conformance corpus that quietly stopped producing, say, supersedes edges
// would leave the lowest-multiplier hop of D-3 untested by everything built on top of it.
func TestSynthCoversEveryKind(t *testing.T) {
	nodes, edges, _, _ := dagtest.Synth(7, synthSmallSpec())

	nodeKinds := make(map[dag.NodeKind]int, len(nodes))
	for _, n := range nodes {
		nodeKinds[n.Kind]++
	}
	for _, k := range []dag.NodeKind{
		dag.KindToolUse, dag.KindToolResult, dag.KindAssistant, dag.KindUserPrompt,
		dag.KindFile, dag.KindSymbol, dag.KindDecision, dag.KindElimination, dag.KindSegment,
	} {
		require.NotZerof(t, nodeKinds[k], "no %s node was generated", k)
	}

	edgeKinds := make(map[dag.EdgeKind]int, len(edges))
	for _, e := range edges {
		edgeKinds[e.Kind]++
	}
	for _, k := range []dag.EdgeKind{
		dag.EdgeSequence, dag.EdgeProduces, dag.EdgeConsumes, dag.EdgeSharedFile,
		dag.EdgeSharedSymbol, dag.EdgeSupersedes, dag.EdgeExplains, dag.EdgeControlOnly,
	} {
		require.NotZerof(t, edgeKinds[k], "no %s edge was generated", k)
	}
}

// TestSynthOutputPassesGraphValidation asserts every generated record would survive the real
// AddNode/AddEdge guards: each node's Kind agrees with its NodeID prefix (the cross-check that
// stands in for a zero-value "unset kind" test, since NodeKind's zero value is KindToolUse), no
// position or token count is negative, no edge is a self-loop, and no (From, To, Kind) triple is
// repeated — the last of which is what proves the generator's own deduplication matches AddEdge's
// fold, so a graph loaded from these slices stores exactly as many edges as were handed to it.
func TestSynthOutputPassesGraphValidation(t *testing.T) {
	nodes, edges, _, _ := dagtest.Synth(7, synthSmallSpec())

	for _, n := range nodes {
		kind, key, ok := dag.ParseNodeID(n.ID)
		require.Truef(t, ok, "node id %q does not parse under the D-2 scheme", n.ID)
		require.Equalf(t, kind, n.Kind, "node %s declares kind %s but its prefix implies %s", n.ID, n.Kind, kind)
		require.NotEmpty(t, key)
		require.GreaterOrEqual(t, n.Pos, 0)
		require.GreaterOrEqual(t, int(n.Tokens), 0)
	}

	type edgeKey struct {
		from, to dag.NodeID
		kind     dag.EdgeKind
	}
	seen := make(map[edgeKey]bool, len(edges))
	for _, e := range edges {
		require.NotEqualf(t, e.From, e.To, "self-loop on %s: AddEdge would reject it", e.From)
		require.LessOrEqual(t, e.Kind, dag.EdgeControlOnly, "edge kind must be a real kind")
		require.Greater(t, e.Weight, float32(0))
		require.LessOrEqual(t, e.Weight, float32(1))
		k := edgeKey{from: e.From, to: e.To, kind: e.Kind}
		require.Falsef(t, seen[k], "duplicate edge %s -> %s (%s): AddEdge would fold it and the counts would disagree",
			e.From, e.To, e.Kind)
		seen[k] = true
	}
}

// recordingGraph is a dag.Graph that accepts everything and remembers the order it was called in.
// It exists so Load itself can be tested — every node first, then every edge — without a real graph,
// which this package cannot construct while dag.Open still hands back the SP-01 stub.
type recordingGraph struct {
	nodes []dag.Node
	edges []dag.Edge
}

func (r *recordingGraph) AddNode(n dag.Node) error {
	r.nodes = append(r.nodes, n)
	return nil
}

func (r *recordingGraph) AddEdge(e dag.Edge) error {
	r.edges = append(r.edges, e)
	return nil
}

func (r *recordingGraph) Node(id dag.NodeID) (dag.Node, bool) { return dag.Node{}, false }
func (r *recordingGraph) Out(id dag.NodeID) []dag.Edge        { return nil }
func (r *recordingGraph) In(id dag.NodeID) []dag.Edge         { return nil }

func (r *recordingGraph) BackwardSlice(criteria []dag.NodeID, o dag.SliceOptions) (dag.Slice, error) {
	return dag.Slice{}, nil
}

func (r *recordingGraph) ForwardSlice(criteria []dag.NodeID, o dag.SliceOptions) (dag.Slice, error) {
	return dag.Slice{}, nil
}

func (r *recordingGraph) CrossingEdges(pos int) int         { return 0 }
func (r *recordingGraph) NodesAfter(pos int) []dag.Node     { return nil }
func (r *recordingGraph) Flush(ctx context.Context) error   { return nil }
func (r *recordingGraph) Compact(ctx context.Context) error { return nil }
func (r *recordingGraph) Stats() dag.GraphStats             { return dag.GraphStats{} }

// TestLoadAddsEveryNodeThenEveryEdge pins Load's contract: everything arrives, nothing is reordered
// within its half, and nodes precede edges.
func TestLoadAddsEveryNodeThenEveryEdge(t *testing.T) {
	nodes, edges, _, _ := dagtest.Synth(7, synthSmallSpec())

	var g recordingGraph
	dagtest.Load(t, &g, nodes, edges)

	require.Equal(t, nodes, g.nodes)
	require.Equal(t, edges, g.edges)
}

// TestLoadIntoARealGraph is the end-to-end version: a real Graph loaded from the generated slices
// must store exactly as many nodes and edges as it was handed, with nothing dangling. It is what
// proves the generator's own upsert and (From, To, Kind) deduplication agree with AddNode's merge
// and AddEdge's fold — if they did not, every count derived from the returned slices (the benchmark
// sizes, the comparison's size_ratio denominator) would be quietly wrong.
func TestLoadIntoARealGraph(t *testing.T) {
	nodes, edges, criteria, truth := dagtest.Synth(7, synthSmallSpec())

	g, err := dag.Open(t.TempDir(), config.Defaults(), logging.Nop())
	require.NoError(t, err)
	dagtest.Load(t, g, nodes, edges)

	s := g.Stats()
	require.Equal(t, len(nodes), s.Nodes, "every generated node must survive the upsert merge as a distinct node")
	require.Equal(t, len(edges), s.Edges, "every generated edge must survive AddEdge's fold as a distinct edge")
	require.Zero(t, s.Dangling, "the generator emits no edge before its endpoints, so nothing dangles")

	// And the graph the comparison actually measures is reachable: a full backward slice from the
	// generated criteria must cover the data-dependence half of truth.
	full, err := g.BackwardSlice(criteria, dag.SliceOptions{Thin: false, MaxNodes: len(nodes)})
	require.NoError(t, err)
	found := 0
	for id := range truth {
		if _, ok := full.Scores[id]; ok {
			found++
		}
	}
	require.Equal(t, len(truth), found,
		"a FULL slice sees every kind of edge, so it must reach the whole ground-truth set")
}

// TestSynthBenchShapeIsRightSize records the shape the benchmarks and the thin-versus-full
// comparison depend on. §6.4 sizes a real session's dependence graph at "a few thousand nodes", and
// the SP-07 performance budgets are all quoted against a graph of roughly 5 000 nodes and 15 000
// edges — so this asserts the generator still lands in that region rather than silently shrinking to
// something the sub-millisecond slice budget would be trivial on.
func TestSynthBenchShapeIsRightSize(t *testing.T) {
	nodes, edges, criteria, truth := dagtest.Synth(7, synthBenchSpec())
	t.Logf("bench spec: nodes=%d edges=%d criteria=%d truth=%d", len(nodes), len(edges), len(criteria), len(truth))

	require.Greater(t, len(nodes), 4000)
	require.Less(t, len(nodes), 6000)
	require.Greater(t, len(edges), 12000)
	require.Less(t, len(edges), 18000)
	require.Greater(t, len(truth), 50,
		"the ground-truth set is recall's denominator: too few and the measurement is all quantization")
}

// TestSynthDegenerateSpecStillProducesAGraph asserts a zero-valued spec is clamped into the smallest
// sensible session rather than dividing by zero or returning nothing — the difference between a
// caller's typo failing loudly at the assertion it meant to write and failing obscurely inside the
// generator.
func TestSynthDegenerateSpecStillProducesAGraph(t *testing.T) {
	nodes, edges, criteria, truth := dagtest.Synth(1, dagtest.SynthSpec{})
	require.NotEmpty(t, nodes)
	require.NotEmpty(t, edges)
	require.NotEmpty(t, criteria)
	require.NotEmpty(t, truth)
	require.Nil(t, findCycle(nodes, edges))
}

// sortedIDs renders a node set as a sorted slice, so two sets can be compared without map iteration
// order entering the comparison.
func sortedIDs(set map[dag.NodeID]bool) []dag.NodeID {
	out := make([]dag.NodeID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// dfsColour is the three-state mark of an iterative depth-first search.
type dfsColour uint8

const (
	white dfsColour = iota // not yet visited
	grey                   // on the current DFS stack
	black                  // fully explored
)

// findCycle runs an ITERATIVE depth-first search over the generated adjacency and returns the first
// directed cycle it finds, or nil.
//
// Iterative rather than recursive on purpose: the benchmark spec generates thousands of nodes, and a
// recursive walk of a graph that had accidentally become cyclic would overflow the stack and report
// "goroutine stack exceeds limit" instead of naming the cycle — testing Go's stack rather than the
// graph.
func findCycle(nodes []dag.Node, edges []dag.Edge) []dag.NodeID {
	out := make(map[dag.NodeID][]dag.NodeID, len(nodes))
	for _, e := range edges {
		out[e.From] = append(out[e.From], e.To)
	}

	type frame struct {
		id   dag.NodeID
		next int
	}
	colour := make(map[dag.NodeID]dfsColour, len(nodes))
	depth := make(map[dag.NodeID]int, len(nodes))

	for _, start := range nodes {
		if colour[start.ID] != white {
			continue
		}
		colour[start.ID] = grey
		depth[start.ID] = 0
		stack := []frame{{id: start.ID}}
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			successors := out[top.id]
			if top.next >= len(successors) {
				colour[top.id] = black
				stack = stack[:len(stack)-1]
				continue
			}
			next := successors[top.next]
			top.next++
			switch colour[next] {
			case grey:
				cycle := make([]dag.NodeID, 0, len(stack)-depth[next]+1)
				for _, f := range stack[depth[next]:] {
					cycle = append(cycle, f.id)
				}
				return append(cycle, next)
			case white:
				colour[next] = grey
				depth[next] = len(stack)
				stack = append(stack, frame{id: next})
			}
		}
	}
	return nil
}

// renderCycle renders a cycle as "a -> b -> c -> a", which is what makes a D-7 regression legible in
// a failure message rather than merely detected.
func renderCycle(cycle []dag.NodeID) string {
	s := ""
	for i, id := range cycle {
		if i > 0 {
			s += " -> "
		}
		s += string(id)
	}
	return s
}
