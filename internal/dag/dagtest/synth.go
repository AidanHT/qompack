package dagtest

import (
	"math"
	"sort"
	"strconv"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
)

// The deterministic synthetic graph generator.
//
// It lives here rather than being taken from eval.Synthesize (SP-02) for an architectural reason,
// not a convenience one: 00-ARCHITECTURE.md §3.2 gives dag an import allow-set of "{}" — foundation
// only — so dag may never import eval, and a conformance subpackage that pulled eval in would
// re-open exactly the dependency the layer map exists to prevent. Rule (c) of §3.2 lets
// dag/dagtest see dag, testutil and core, and nothing else; this file stays inside that.
//
// Determinism is the entire point. No time, no map iteration reaching the output, no global
// randomness: a given seed produces byte-identical nodes and edges on every platform and every Go
// version. The benchmarks and the thin-versus-full recall comparison both quote numbers derived from
// one seed, and a generator that drifted would silently invalidate a recorded baseline instead of
// failing.
//
// The randomness comes from synthRand below rather than from math/rand, and that is a deliberate
// substitution rather than a preference. gosec's G404 forbids math/rand outside _test.go files, and
// this file is not one — it is a library other packages' benchmarks import — so a seeded
// math/rand.Source here fails `devtool lint` with no honest way to silence it (a //nolint would be
// asserting an exemption this file has not got). internal/chunk/chunktest and internal/sketch/
// sketchtest hit exactly the same wall and answered it the same way, by generating a deterministic
// stream arithmetically instead of "randomizing". The substitution also STRENGTHENS the property
// being asked for: splitmix64's arithmetic lives in this file, so it cannot drift with a Go release
// the way a standard-library source's internals can.
//
// The generated graph is ACYCLIC, and reproduces the same chain shape the builders emit (D-7):
// toolresult:<prev> → assistant:<Turn> → tooluse:<cur> → toolresult:<cur>, never the tempting
// toolresult:<cur> → assistant:<Turn>, which would close a three-node cycle. Shared state follows
// the builders' D-1 direction rule, with one modelling choice that keeps the whole graph acyclic
// rather than only the chain: every file is WRITTEN at its first touch and only READ afterwards. A
// file that is read and then written later closes a legitimate loop through its file node — the
// Read-then-Edit pattern, a real property of §8.1 item 4's shared-state modelling — and a generator
// that produced one would make "the synthetic graph is acyclic" untestable.

// SynthSpec drives the deterministic generator.
type SynthSpec struct {
	// Turns is how many assistant turns the synthetic session runs for.
	Turns int
	// ToolsPerTurn is how many tool calls each assistant turn makes.
	ToolsPerTurn int
	// Files is how many distinct files the session touches.
	Files int
	// SymbolsPerFile is how many distinct symbols each file has; it also bounds how many symbols
	// one tool call touches.
	SymbolsPerFile int
	// ControlOnlyFraction is the share of assistant → tool_use edges that carry no shared state and
	// are therefore emitted as EdgeControlOnly — the edges §6.4's thin slicing drops.
	ControlOnlyFraction float64
	// ControlCarriedFraction is the share of the TRULY relevant set that is reachable only through
	// an EdgeControlOnly edge. It is the soundness thin slicing trades away, and it is what makes
	// the thin-versus-full recall comparison measure something rather than always reporting 1.
	ControlCarriedFraction float64
	// Supersessions is how many §8.1 item 3 supersedes edges to plant between earlier and later
	// tool uses.
	Supersessions int
	// TokensPerTool is how far Pos advances per tool call, so the generated positions look like a
	// real prefix rather than a dense integer range.
	TokensPerTool int
}

// The auxiliary emission rates: how often the generator records a decision, an elimination and a
// closed segment alongside the transcript nodes.
//
// They are rates, not a model of how often a real session does any of these things. Their job is to
// reproduce the ~5 000-node / ~15 000-edge shape §6.4 sizes a session's dependence graph at, so that
// the benchmarks and the thin-versus-full comparison run against the graph the budgets were written
// for. The transcript half of the generator (turns, tools, files, symbols) is what SynthSpec
// controls; these fill in the rest of the shape.
const (
	// synthAuxPeriod splits the tool calls between the two auxiliary record kinds: one in every
	// synthAuxPeriod calls records an elimination, and the rest record a decision.
	synthAuxPeriod = 3
	// synthEvidencePerDecision is how many earlier nodes explain each decision.
	synthEvidencePerDecision = 3
	// synthEvidencePerElimination is how many earlier nodes explain each elimination.
	synthEvidencePerElimination = 2
	// synthSegmentTurns is how many turns a closed segment spans.
	synthSegmentTurns = 4
	// synthCriteriaCount is how many criterion nodes Synth hands back.
	synthCriteriaCount = 3
	// synthCriteriaTailDivisor confines the criteria to the last 1/N of the session, which is where
	// a real slice starts: the scheduler asks "what does the CURRENT work depend on".
	synthCriteriaTailDivisor = 10
	// synthReadWindowDivisor sets how far back a call reaches for a file to read: the last 1/N of
	// the files introduced so far.
	synthReadWindowDivisor = 4
	// synthPickAttempts bounds how many draws a distinct-sample loop makes before settling for what
	// it has. It exists so a small graph, where the same node keeps coming up, terminates rather
	// than spinning.
	synthPickAttempts = 4
)

// synthEpochMillis is the wall-clock instant the synthetic session starts at. It is a fixed
// constant rather than time.Now() for the same reason the rest of the generator is seeded: a
// timestamp that moved would make every recorded dump differ from the last one.
const synthEpochMillis = 1767225480000

// Synth generates a deterministic synthetic dependence graph.
//
// It returns the nodes and edges in emission order — every edge appears only after both of its
// endpoint nodes have appeared, which is stricter than the graph requires (D-6 makes a dangling edge
// legal) and is what lets a consumer replay the slices in order into anything, not only into a
// dag.Graph. criteria is a small set of tool-use nodes from the end of the session, and truth is the
// ground-truth relevant set the thin-versus-full comparison measures recall against:
//
//   - every node reachable BACKWARD from criteria through the data-dependence edge kinds
//     (produces, consumes, shared_file, shared_symbol, explains), plus
//   - a ControlCarriedFraction share of the nodes reachable only by crossing an EdgeControlOnly
//     edge, which is precisely the soundness thin slicing gives up.
//
// Backward is the direction that matters because BackwardSlice is what the comparison runs: recall
// answers "of the things this criterion genuinely depends on, how many did the cheap walk find".
func Synth(seed int64, s SynthSpec) (nodes []dag.Node, edges []dag.Edge, criteria []dag.NodeID, truth map[dag.NodeID]bool) {
	s = s.normalized()
	b := &synthBuilder{
		spec:    s,
		rng:     newSynthRand(seed),
		seen:    make(map[dag.NodeID]bool),
		emitted: make(map[synthEdgeKey]bool),
	}
	b.run()
	criteria = b.pickCriteria()
	return b.nodes, b.edges, criteria, b.truthSet(criteria)
}

// Load adds every node and then every edge to g through the public API, failing tb on the first
// error.
//
// It takes testing.TB rather than *testing.T so the benchmarks can load the same graph the tests do;
// nodes go in before edges purely so a Stats() call in between reads the number a caller expects,
// since the graph itself accepts them in any order (D-6).
func Load(tb testing.TB, g dag.Graph, nodes []dag.Node, edges []dag.Edge) {
	tb.Helper()
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			tb.Fatalf("dagtest.Load: AddNode(%s): %v", n.ID, err)
		}
	}
	for _, e := range edges {
		if err := g.AddEdge(e); err != nil {
			tb.Fatalf("dagtest.Load: AddEdge(%s -> %s, %s): %v", e.From, e.To, e.Kind, err)
		}
	}
}

// normalized clamps a spec into the range the generator can actually produce, so a caller that
// leaves a field at zero gets the smallest sensible session instead of a division by zero or an
// empty result that looks like a generator bug.
func (s SynthSpec) normalized() SynthSpec {
	s.Turns = atLeast(s.Turns, 1)
	s.ToolsPerTurn = atLeast(s.ToolsPerTurn, 1)
	s.Files = atLeast(s.Files, 1)
	s.SymbolsPerFile = atLeast(s.SymbolsPerFile, 1)
	s.TokensPerTool = atLeast(s.TokensPerTool, 1)
	s.Supersessions = atLeast(s.Supersessions, 0)
	s.ControlOnlyFraction = clampFraction(s.ControlOnlyFraction)
	s.ControlCarriedFraction = clampFraction(s.ControlCarriedFraction)
	return s
}

// atLeast returns v, or floor when v is below it.
func atLeast(v, floor int) int {
	if v < floor {
		return floor
	}
	return v
}

// clampFraction confines f to [0, 1). One is excluded deliberately: ControlCarriedFraction is
// converted to a ratio of f/(1-f), and a share of "all of them" has no finite answer.
func clampFraction(f float64) float64 {
	const almostOne = 0.99
	switch {
	case f < 0:
		return 0
	case f > almostOne:
		return almostOne
	default:
		return f
	}
}

// synthEdgeKey is the deduplication identity of a generated edge, matching the (From, To, Kind)
// triple dag.AddEdge folds on. Without it the returned edge slice would claim more edges than a
// graph loaded from it actually stores, and every count derived from the slice would be wrong.
type synthEdgeKey struct {
	from, to dag.NodeID
	kind     dag.EdgeKind
}

// synthBuilder carries the generator's running state. It exists so the emission helpers can enforce
// the "node before edge" invariant and the edge deduplication in one place rather than at each of
// the two dozen call sites.
type synthBuilder struct {
	spec SynthSpec
	rng  *synthRand

	nodes []dag.Node
	edges []dag.Edge

	seen    map[dag.NodeID]bool
	emitted map[synthEdgeKey]bool

	// writeOrder is the file indexes that have been introduced, in the order they were written.
	// A call may only READ a file already in this list, and a file is written exactly once, which
	// is what makes the file half of the graph a DAG ordered by write order: every shared-file edge
	// runs from an earlier-written file into a later call, or from a call into the file it is
	// introducing, and never the other way round.
	writeOrder []int

	pos     int
	uses    []dag.NodeID // every tool-use node, in creation order
	results []dag.NodeID // every tool-result node, in creation order
}

// run generates the whole session.
func (b *synthBuilder) run() {
	var prevResult dag.NodeID
	prevTurn := core.TurnIndex(-1)
	var prevSegment core.SegmentID
	var segmentMembers []dag.NodeID

	for t := 0; t < b.spec.Turns; t++ {
		turn := core.TurnIndex(t)
		assistant := dag.AssistantNode(turn)
		b.node(assistant, dag.KindAssistant, turn, b.pos, "", 0)

		prompt := dag.UserPromptNode(turn)
		b.node(prompt, dag.KindUserPrompt, turn, b.pos, "prompt "+strconv.Itoa(t), core.Tokens(b.spec.TokensPerTool))
		b.edge(prompt, assistant, dag.EdgeConsumes, turn)

		// D-7: the assistant turn consumes the PREVIOUS turn's last result, never its own.
		if prevResult != "" && prevTurn < turn {
			b.edge(prevResult, assistant, dag.EdgeConsumes, turn)
		}

		for k := 0; k < b.spec.ToolsPerTurn; k++ {
			use, result := b.toolCall(turn, assistant)
			prevResult = result
			segmentMembers = append(segmentMembers, use)
		}
		prevTurn = turn

		if (t+1)%synthSegmentTurns == 0 {
			id := core.SegmentID(t/synthSegmentTurns + 1)
			b.segment(id, prevSegment, turn, segmentMembers)
			prevSegment, segmentMembers = id, nil
		}
	}

	b.supersessions()
}

// toolCall emits one tool call: its tool-use and tool-result nodes, the produces edge between them,
// the assistant → tool_use link, the file and symbol anchors it touched, and whichever of a decision
// or an elimination this call's index calls for.
func (b *synthBuilder) toolCall(turn core.TurnIndex, assistant dag.NodeID) (use, result dag.NodeID) {
	n := len(b.uses)
	id := core.ToolUseID("toolu_" + strconv.Itoa(n))
	use, result = dag.ToolUseNode(id), dag.ToolResultNode(id)

	b.node(use, dag.KindToolUse, turn, b.pos, "Read", 0)
	b.node(result, dag.KindToolResult, turn, b.pos+b.spec.TokensPerTool/2, string(id), core.Tokens(b.spec.TokensPerTool))
	b.edge(use, result, dag.EdgeProduces, turn)

	// The share of links that carry no shared state, exactly as BuildToolUse's turnLinkKind would
	// decide it for a call whose file and symbols its predecessor never touched.
	link := dag.EdgeSequence
	if b.rng.Float64() < b.spec.ControlOnlyFraction {
		link = dag.EdgeControlOnly
	}
	b.edge(assistant, use, link, turn)

	pathKey, symbol := b.touchFiles(use, turn, n)

	b.uses = append(b.uses, use)
	b.results = append(b.results, result)
	b.pos += b.spec.TokensPerTool

	if n%synthAuxPeriod == 0 {
		b.elimination(n, turn, pathKey, symbol)
	} else {
		b.decision(n, turn)
	}
	return use, result
}

// touchFiles gives one tool call its shared state: at most one file it introduces (a write), plus
// one or two files it consumes (reads). It returns the last path and symbol touched, which is what
// an elimination recorded against this call is about.
//
// The read/write split is what gives the graph a data-dependence chain worth slicing. A call that
// introduces a file also reads an EARLIER one, so file → writer → earlier file → its writer → …
// forms a genuine multi-hop data path — which is the thing §6.4's backward slice exists to follow,
// and which a model where every file had a single writer and no other input would not contain at
// all. Restricting reads to files already in writeOrder is what keeps that chain acyclic: a file is
// never read before it is written, so it can never point back at something downstream of it.
func (b *synthBuilder) touchFiles(use dag.NodeID, turn core.TurnIndex, n int) (pathKey, symbol string) {
	readable := b.writeOrder
	if len(b.writeOrder) < b.spec.Files && (len(b.writeOrder) == 0 || n%b.writePeriod() == 0) {
		idx := len(b.writeOrder)
		b.writeOrder = append(b.writeOrder, idx)
		pathKey, symbol = b.anchor(use, idx, turn, true)
	}

	const maxReadsPerCall = 2
	reads := 1 + b.rng.Intn(maxReadsPerCall)
	for i := 0; i < reads && len(readable) > 0; i++ {
		// Reads are drawn from the RECENTLY written tail rather than uniformly from the whole
		// history, because a session works on related files: a call that touches the file
		// introduced forty calls ago and nothing since is not what a transcript looks like. It also
		// makes the data-dependence chain walk back file by file instead of jumping to a random
		// ancestor, which is what gives a backward slice something more than three hops to find.
		window := atLeast(len(readable)/synthReadWindowDivisor, 1)
		idx := readable[len(readable)-1-b.rng.Intn(window)]
		pathKey, symbol = b.anchor(use, idx, turn, false)
	}
	return pathKey, symbol
}

// writePeriod spreads the introduction of Files files evenly across the session's tool calls, so
// the write order is a chain the whole length of the transcript rather than a burst at the start.
func (b *synthBuilder) writePeriod() int {
	return atLeast(b.spec.Turns*b.spec.ToolsPerTurn/b.spec.Files, 1)
}

// anchor emits one file node, its symbol nodes, and the shared-state edges linking them to use in
// the direction writes calls for. It returns the path and its first symbol name.
func (b *synthBuilder) anchor(use dag.NodeID, fileIdx int, turn core.TurnIndex, writes bool) (pathKey, symbol string) {
	pathKey = "src/pkg" + strconv.Itoa(fileIdx) + ".ts"
	file := dag.FileNode(pathKey)
	b.node(file, dag.KindFile, turn, b.pos, pathKey, 0)
	b.sharedEdge(file, use, dag.EdgeSharedFile, writes, turn)

	names := b.symbolsOf(fileIdx)
	for _, name := range names {
		id := dag.SymbolNode(pathKey, name)
		b.node(id, dag.KindSymbol, turn, b.pos, name, 0)
		b.sharedEdge(id, use, dag.EdgeSharedSymbol, writes, turn)
	}
	return pathKey, names[0]
}

// symbolsOf returns a sorted, deduplicated sample of the symbol names belonging to one file — the
// same shape BuildToolUse's sortedUniqueSymbols hands the graph, so the generated edge order matches
// what a real observation would produce.
func (b *synthBuilder) symbolsOf(fileIdx int) []string {
	want := 1 + b.rng.Intn(b.spec.SymbolsPerFile)
	picked := make(map[int]bool, want)
	for i := 0; i < want; i++ {
		picked[b.rng.Intn(b.spec.SymbolsPerFile)] = true
	}
	names := make([]string, 0, len(picked))
	for idx := range picked {
		names = append(names, "sym"+strconv.Itoa(fileIdx)+"_"+strconv.Itoa(idx))
	}
	sort.Strings(names)
	return names
}

// decision emits one recorded decision explained by a few earlier nodes.
func (b *synthBuilder) decision(n int, turn core.TurnIndex) {
	id := dag.DecisionNode(core.DecisionID("dec_" + strconv.Itoa(n)))
	b.node(id, dag.KindDecision, turn, b.pos, "decision "+strconv.Itoa(n), core.Tokens(b.spec.TokensPerTool))
	for _, evidence := range b.recentEvidence(synthEvidencePerDecision) {
		b.edge(evidence, id, dag.EdgeExplains, turn)
	}
}

// elimination emits one negative-knowledge record, with its evidence, its file and one of its
// symbols all pointing INTO it — the shape builders.go's BuildElimination produces.
func (b *synthBuilder) elimination(n int, turn core.TurnIndex, pathKey, symbol string) {
	id := dag.EliminationNode("neg_" + strconv.Itoa(n))
	b.node(id, dag.KindElimination, turn, b.pos, pathKey, 0)
	for _, evidence := range b.recentEvidence(synthEvidencePerElimination) {
		b.edge(evidence, id, dag.EdgeExplains, turn)
	}
	b.edge(dag.FileNode(pathKey), id, dag.EdgeSharedFile, turn)
	b.edge(dag.SymbolNode(pathKey, symbol), id, dag.EdgeSharedSymbol, turn)
}

// recentEvidence picks up to want distinct earlier tool-result nodes, newest-biased, as the
// justification for a decision or an elimination. Evidence is always drawn from the PAST, which is
// what keeps every explains edge pointing forward in time and the whole graph acyclic.
func (b *synthBuilder) recentEvidence(want int) []dag.NodeID {
	if len(b.results) == 0 {
		return nil
	}
	window := atLeast(len(b.results)/synthCriteriaTailDivisor, want*synthAuxPeriod)
	if window > len(b.results) {
		window = len(b.results)
	}
	picked := make(map[dag.NodeID]bool, want)
	out := make([]dag.NodeID, 0, want)
	for i := 0; i < want; i++ {
		id := b.results[len(b.results)-1-b.rng.Intn(window)]
		if picked[id] {
			continue
		}
		picked[id] = true
		out = append(out, id)
	}
	return out
}

// segment emits one closed segment, its membership and its place in the segment chain.
func (b *synthBuilder) segment(id, prev core.SegmentID, turn core.TurnIndex, members []dag.NodeID) {
	node := dag.SegmentNode(id)
	startPos := b.pos - len(members)*b.spec.TokensPerTool
	b.node(node, dag.KindSegment, turn, atLeast(startPos, 0), strconv.Itoa(int(id)),
		core.Tokens(len(members)*b.spec.TokensPerTool))
	for _, member := range members {
		b.edge(member, node, dag.EdgeSequence, turn)
	}
	if prev != 0 {
		b.edge(dag.SegmentNode(prev), node, dag.EdgeSequence, turn)
	}
}

// supersessions plants §8.1 item 3's superseded-read edges, always from an EARLIER tool use to a
// later one (D-1). Drawing the pair as "an index and a strictly larger index" rather than as two
// independent draws is what keeps them from ever pointing backwards and closing a cycle.
func (b *synthBuilder) supersessions() {
	if len(b.uses) < 2 {
		return
	}
	for i := 0; i < b.spec.Supersessions; i++ {
		later := 1 + b.rng.Intn(len(b.uses)-1)
		earlier := b.rng.Intn(later)
		b.edge(b.uses[earlier], b.uses[later], dag.EdgeSupersedes, core.TurnIndex(later/b.spec.ToolsPerTurn))
	}
}

// node appends one node, ignoring a repeat of an id already emitted. Repeats are normal: a file node
// is "created" on every touch and only the first one is a new record, exactly as dag.AddNode's
// upsert would fold them.
func (b *synthBuilder) node(id dag.NodeID, kind dag.NodeKind, turn core.TurnIndex, pos int, ref string, tokens core.Tokens) {
	if b.seen[id] {
		return
	}
	b.seen[id] = true
	b.nodes = append(b.nodes, dag.Node{
		ID: id, Kind: kind, Turn: turn, TS: core.UnixMilli(synthEpochMillis + int64(pos)),
		Pos: pos, Ref: ref, Tokens: tokens,
	})
}

// edge appends one edge, skipping a self-loop (which dag.AddEdge rejects) and folding a repeat of an
// identical (From, To, Kind) triple (which dag.AddEdge folds), so that the returned slice is exactly
// what a graph loaded from it would store.
func (b *synthBuilder) edge(from, to dag.NodeID, kind dag.EdgeKind, turn core.TurnIndex) {
	if from == to {
		return
	}
	key := synthEdgeKey{from: from, to: to, kind: kind}
	if b.emitted[key] {
		return
	}
	b.emitted[key] = true
	b.edges = append(b.edges, dag.Edge{From: from, To: to, Kind: kind, Weight: 1, Turn: turn})
}

// sharedEdge orients one shared-state edge exactly as builders.go's sharedStateEdge does: a read
// consumes the anchor (anchor → tool_use), a write produces it (tool_use → anchor).
func (b *synthBuilder) sharedEdge(anchor, use dag.NodeID, kind dag.EdgeKind, writes bool, turn core.TurnIndex) {
	if writes {
		b.edge(use, anchor, kind, turn)
		return
	}
	b.edge(anchor, use, kind, turn)
}

// pickCriteria returns the slice criteria: a few tool uses from the tail of the session, which is
// where a real slice starts — the scheduler asks what the CURRENT work depends on, not what the
// session opened with.
func (b *synthBuilder) pickCriteria() []dag.NodeID {
	if len(b.uses) == 0 {
		return nil
	}
	tail := atLeast(len(b.uses)/synthCriteriaTailDivisor, 1)
	picked := make(map[dag.NodeID]bool, synthCriteriaCount)
	out := make([]dag.NodeID, 0, synthCriteriaCount)
	for i := 0; i < synthCriteriaCount*synthPickAttempts && len(out) < synthCriteriaCount; i++ {
		id := b.uses[len(b.uses)-1-b.rng.Intn(tail)]
		if picked[id] {
			continue
		}
		picked[id] = true
		out = append(out, id)
	}
	return out
}

// synthDataKinds are the edge kinds that carry DATA dependence. They are what the ground-truth
// relevant set is computed over: a node the criteria genuinely depend on is one the data flowed
// from, and sequence, control and supersedes hops are adjacency, control flow and replacement
// respectively — none of them is data.
func synthDataKinds(k dag.EdgeKind) bool {
	switch k {
	case dag.EdgeProduces, dag.EdgeConsumes, dag.EdgeSharedFile, dag.EdgeSharedSymbol, dag.EdgeExplains:
		return true
	default:
		return false
	}
}

// truthSet computes the ground-truth relevant set: everything the criteria depend on through data
// edges, plus a ControlCarriedFraction share of what is reachable ONLY by crossing a control-only
// edge.
//
// The second group is constructed as (reachable through every edge kind) minus (reachable through
// every kind except EdgeControlOnly), which is exactly the set thin slicing cannot see. Sampling it
// down to a share is what makes the comparison's recall a number between 0 and 1 rather than either
// a perfect score or a collapse: §6.4 trades soundness for speed, and the size of that trade is the
// thing worth measuring.
func (b *synthBuilder) truthSet(criteria []dag.NodeID) map[dag.NodeID]bool {
	in := make(map[dag.NodeID][]dag.Edge, len(b.nodes))
	for _, e := range b.edges {
		in[e.To] = append(in[e.To], e)
	}

	data := reachBackward(in, criteria, synthDataKinds)
	full := reachBackward(in, criteria, func(dag.EdgeKind) bool { return true })
	thin := reachBackward(in, criteria, func(k dag.EdgeKind) bool { return k != dag.EdgeControlOnly })

	// Sorted before sampling, so the choice depends on the seed and never on Go's map iteration.
	candidates := make([]dag.NodeID, 0, len(full))
	for id := range full {
		if !thin[id] {
			candidates = append(candidates, id)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })

	f := b.spec.ControlCarriedFraction
	want := int(math.Round(f / (1 - f) * float64(len(data))))
	if want > len(candidates) {
		want = len(candidates)
	}
	b.rng.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })

	truth := make(map[dag.NodeID]bool, len(data)+want)
	for id := range data {
		truth[id] = true
	}
	for _, id := range candidates[:want] {
		truth[id] = true
	}
	return truth
}

// The splitmix64 constants. The first is the 64-bit golden-ratio increment that makes the state
// sequence equidistributed; the other two are the avalanche multipliers that decorrelate successive
// states. They are the algorithm's published values and are not tunable — changing one produces a
// different generator, not a differently-tuned one.
const (
	synthGamma = 0x9E3779B97F4A7C15
	synthMixA  = 0xBF58476D1CE4E5B9
	synthMixB  = 0x94D049BB133111EB
)

// synthRand is a splitmix64 pseudo-random generator: a 64-bit counter run through a fixed avalanche
// function. See the file comment for why the standard library's is not used here.
//
// It is emphatically NOT for anything security-bearing, and could not be: the whole point is that
// the sequence is reproducible from a seed a caller wrote down. Its only job is to make one
// synthetic transcript look irregular while staying identical across runs.
type synthRand struct{ state uint64 }

// newSynthRand returns a generator seeded from seed. A seed of zero is as good as any other here:
// splitmix64 advances the state by synthGamma BEFORE mixing, so the first value is already well
// distributed rather than being a hash of nothing.
func newSynthRand(seed int64) *synthRand { return &synthRand{state: uint64(seed)} }

// next returns the generator's next 64-bit value.
func (r *synthRand) next() uint64 {
	r.state += synthGamma
	z := r.state
	z = (z ^ (z >> 30)) * synthMixA
	z = (z ^ (z >> 27)) * synthMixB
	return z ^ (z >> 31)
}

// Intn returns a value in [0, n). It is the modulo reduction rather than a rejection-sampling loop:
// the bias that introduces is on the order of n/2^64, which for the two-digit values this generator
// draws is far below anything a synthetic corpus could notice, and rejection sampling would make the
// number of draws per call data-dependent — which is exactly the kind of thing that makes a seeded
// generator stop being reproducible when an unrelated parameter changes.
func (r *synthRand) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n))
}

// Float64 returns a value in [0, 1). It takes the top 53 bits, which is exactly float64's mantissa
// width, so every representable value in the range is reachable and none is favoured by rounding.
func (r *synthRand) Float64() float64 {
	const mantissaBits = 53
	const shift = 64 - mantissaBits
	return float64(r.next()>>shift) / float64(uint64(1)<<mantissaBits)
}

// Shuffle permutes n elements with a Fisher-Yates pass, calling swap to exchange two of them.
func (r *synthRand) Shuffle(n int, swap func(i, j int)) {
	for i := n - 1; i > 0; i-- {
		swap(i, r.Intn(i+1))
	}
}

// reachBackward returns every node reachable from criteria by walking edges in REVERSE (a node's
// dependencies are the tails of the edges entering it), restricted to the kinds allowed reports true
// for. The criteria themselves are included: a criterion trivially depends on itself, and the
// comparison's recall denominator would otherwise exclude the very nodes the slice starts from.
func reachBackward(in map[dag.NodeID][]dag.Edge, criteria []dag.NodeID, allowed func(dag.EdgeKind) bool) map[dag.NodeID]bool {
	seen := make(map[dag.NodeID]bool, len(in))
	queue := make([]dag.NodeID, 0, len(criteria))
	for _, id := range criteria {
		if !seen[id] {
			seen[id] = true
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, e := range in[id] {
			if !allowed(e.Kind) || seen[e.From] {
				continue
			}
			seen[e.From] = true
			queue = append(queue, e.From)
		}
	}
	return seen
}
