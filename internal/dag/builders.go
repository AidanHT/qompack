package dag

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
)

// The §8.1 item 4 edge constructors: the one place in the repository that turns an observation of
// the transcript into nodes and edges.
//
// They exist as free functions over the Graph interface rather than as methods on the concrete
// graph for two reasons. First, observer (§5.7) is the only production caller and it holds a
// dag.Graph, not the concrete type; second, a free function is what lets the whole edge set be
// tested against any implementation, including a future persistent one, without the builders being
// re-implemented per backend.
//
// # D-1: every edge points from earlier/producer to later/consumer
//
// There is no exception. EdgeSupersedes runs superseded → superseding, and EdgeExplains runs
// evidence → decision, even though both read backwards in English. A slice's whole value comes from
// its direction being uniform: BackwardSlice from a criterion means "what did this depend on", and
// one edge family pointing the other way would silently turn part of that answer into its opposite.
//
// # D-7: the §8.1 item 4 chain is ACYCLIC, and getting it wrong is the easy mistake
//
// Qompack.md §8.1 item 4's chain is tool_use → tool_result → assistant_turn → next_tool_use. The
// assistant node in the middle is the reasoning that READ THE PREVIOUS RESULT and emitted THIS tool
// use. It is keyed on the turn of the CURRENT tool use, and its edges are:
//
//	toolresult:<prev> --consumes--> assistant:<Turn> --sequence/control--> tooluse:<cur> --produces--> toolresult:<cur>
//
// The tempting alternative — toolresult:<cur> → assistant:<Turn> alongside assistant:<Turn> →
// tooluse:<cur> — is a three-node CYCLE (tooluse → toolresult → assistant → tooluse). It would make
// every backward slice from a tool use swallow that tool use's own forward chain, corrupting both
// the relevance scores of §8.3 and the segment_coupling counts of §8.4. builders_test.go's
// TestBuilderOutputIsAcyclic is the regression guard, and it names the cycle when it fires.
//
// Two consequences follow from writing it the correct way:
//
//   - toolresult:<cur> gets its consumer edge LATER, when the next tool use is built. Until then it
//     is a leaf, and the consumes edge emitted here starts at toolresult:<prev>, which may not be a
//     node yet — legal under D-6, which makes a dangling edge a normal interleaving, not an error.
//   - Parallel tool calls in one assistant message SHARE a turn index, so the previous tool use
//     hangs off the same assistant node. Emitting toolresult:<prev> → assistant:<Turn> there would
//     re-create the cycle, so the consumes edge is not emitted at all: siblings of one assistant
//     turn are both produced by that turn, neither consuming the other.
//
// Every builder here is idempotent. It comes for free from AddEdge folding an identical
// (From, To, Kind) triple into the stored edge instead of appending a second record, which is what
// keeps dag/deps.jsonl linear in the session's real content when the observer re-derives the same
// shared-state edges on every PostToolUse for a path it has already seen.

// fullWeight is the weight every builder-emitted edge carries. The builders record STRUCTURE — this
// tool use touched that file — and structure is not a confidence judgement, so the per-hop decay of
// SP-07 D-3 (EdgeKind.Multiplier) is where an edge kind's relative worth is expressed. Weighting
// edges here as well would apply the same opinion twice.
const fullWeight float32 = 1

// decisionSummaryMaxBytes is how much of a decision's summary survives into its node's Ref:
// roughly one terminal line, which is enough to recognize a decision in a `/qompack:status` dump
// without pinning an unbounded string in memory for the whole session (the full text lives in the
// checkpoint, and the DecisionID is what `why(decision_id)` resolves against).
//
// 128 rather than a round 120 for one reason worth stating: 120 is in the D11 / §11.6
// forbidden-literal set, and there is no honest way to write it here. The cap is not that
// configuration default, so a //nomagic:allow would be true but would also plant the habit of
// annotating past the check; spelling it as a product to dodge the linter would be worse still,
// since it defeats the check by construction rather than by argument. The exact number carries no
// meaning — it is a display truncation — so the cheapest correct answer is to pick one that says
// what it means and trips nothing.
const decisionSummaryMaxBytes = 128

// ObservedTool is one observed tool call, as the observer (§5.7) sees it in a PostToolUse payload
// plus whatever the analyzer already resolved for it.
//
// Symbols arrives as a plain []string, resolved by the CALLER. That is not a convenience: dag's
// import allow-set is foundation-only (00-ARCHITECTURE.md §3.2), so this package may never import
// internal/symbols, and taking the names as strings is what keeps observer and negknow free to
// depend on dag without a cycle.
type ObservedTool struct {
	// ToolUseID is the host's identifier for this call. It keys BOTH the tool-use and the
	// tool-result node — the D-2 prefix is what distinguishes them — so the produces edge can be
	// built from the payload alone with no second identifier to carry.
	ToolUseID core.ToolUseID
	// PrevToolUseID is the call immediately before this one in the session, or "" for the first
	// tool use of the session.
	PrevToolUseID core.ToolUseID
	// PrevTurn is PrevToolUseID's turn. It EQUALS Turn when the two calls are parallel siblings of
	// one assistant message, and a value greater than Turn is invalid input (see BuildToolUse).
	PrevTurn core.TurnIndex
	// Supersedes is an earlier tool use this one replaces — §8.1 item 3's superseded read — or ""
	// when it replaces nothing.
	Supersedes core.ToolUseID
	// Turn is the assistant turn this call was emitted in.
	Turn core.TurnIndex
	// TS is when the call was observed.
	TS core.UnixMilli
	// Pos is the token position of the tool_use block in the prefix. §5.3 makes this the field
	// every p-selection question is answered from, so an observation that leaves it zero is
	// answering "at the very start of the session".
	Pos int
	// ResultPos is the token position of the tool_result block, falling back to Pos when zero.
	ResultPos int
	// Tool is the tool's name ("Read", "Edit", "Grep"); it becomes the tool-use node's Ref.
	Tool string
	// PathKey is the file this call touched, already in paths.Key form, or "" when it touched no
	// path. dag deliberately does not call paths.Key itself — it has no project root, and re-folding
	// a key the caller normalized against a different root would silently fork the graph (§4).
	PathKey string
	// Writes is true for the mutating tools (Edit, Write, MultiEdit) and false for reads and greps.
	// It is what decides the DIRECTION of the shared-state edges, per D-1.
	Writes bool
	// Symbols are the symbol names this call touched, in any order: BuildToolUse sorts and
	// deduplicates them so the emitted edge order does not depend on how the extractor walked the
	// file.
	Symbols []string
	// Root is the content root hash of the result, if any.
	Root core.Hash
	// Tokens is the result's estimated token cost.
	Tokens core.Tokens
	// Ephemeral marks a §8.7 retrieval result: a first-eviction candidate for the rest of the
	// session. It is set on BOTH the tool-use and the tool-result node, because evicting one and
	// keeping the other would leave a produces edge pointing at nothing.
	Ephemeral bool
}

// BuildToolUse records one observed tool call as the §8.1 item 4 node and edge set.
//
// It emits, in this order: the tool-use node; the tool-result node; tool_use --produces-->
// tool_result; the assistant node, plus toolresult:<prev> --consumes--> assistant when the previous
// call was in an EARLIER turn (D-7 — see the file comment for why the current result must not be
// consumed here, and why a parallel sibling gets no consumes edge at all); assistant --sequence or
// control--> tool_use; the file node and its shared-file edge; one symbol node and shared-symbol
// edge per distinct symbol name, in ascending order; and the supersedes edge of §8.1 item 3.
//
// Invalid INPUT — a missing tool-use id, or a predecessor dated after this call — returns
// immediately with nothing mutated, because neither can arise from a real transcript and continuing
// would write a graph that says something false. Errors from the individual AddNode/AddEdge calls
// are accumulated with errors.Join instead: those are per-record failures, and stopping at the first
// one would leave a half-built chain whose missing half is invisible to the caller.
func BuildToolUse(g Graph, o ObservedTool) error {
	if o.ToolUseID == "" {
		return fmt.Errorf("%w: observed tool call has no ToolUseID", ErrInvalidNode)
	}
	if o.PrevToolUseID != "" && o.PrevTurn > o.Turn {
		return fmt.Errorf("%w: previous tool use %q is at turn %d, after this call's turn %d",
			ErrInvalidEdge, string(o.PrevToolUseID), int(o.PrevTurn), int(o.Turn))
	}

	use := ToolUseNode(o.ToolUseID)
	result := ToolResultNode(o.ToolUseID)
	assistant := AssistantNode(o.Turn)

	resultPos := o.ResultPos
	if resultPos == 0 {
		resultPos = o.Pos
	}

	errs := []error{
		g.AddNode(Node{
			ID: use, Kind: KindToolUse, Turn: o.Turn, TS: o.TS, Pos: o.Pos,
			Ref: o.Tool, Ephemeral: o.Ephemeral,
		}),
		g.AddNode(Node{
			ID: result, Kind: KindToolResult, Turn: o.Turn, TS: o.TS, Pos: resultPos,
			Ref: string(o.ToolUseID), Root: o.Root, Tokens: o.Tokens, Ephemeral: o.Ephemeral,
		}),
		g.AddEdge(Edge{From: use, To: result, Kind: EdgeProduces, Weight: fullWeight, Turn: o.Turn}),
		// The assistant block precedes the tool_use block it contains, so it anchors at the same
		// position: a cut point between them would separate a tool call from the reasoning that
		// emitted it, which is not a boundary §8.4 should ever be able to find.
		g.AddNode(Node{ID: assistant, Kind: KindAssistant, Turn: o.Turn, TS: o.TS, Pos: o.Pos}),
	}

	if o.PrevToolUseID != "" && o.PrevTurn < o.Turn {
		errs = append(errs, g.AddEdge(Edge{
			From: ToolResultNode(o.PrevToolUseID), To: assistant,
			Kind: EdgeConsumes, Weight: fullWeight, Turn: o.Turn,
		}))
	}

	errs = append(errs, g.AddEdge(Edge{
		From: assistant, To: use, Kind: turnLinkKind(g, o), Weight: fullWeight, Turn: o.Turn,
	}))

	if o.PathKey != "" {
		errs = append(errs,
			g.AddNode(Node{
				ID: FileNode(o.PathKey), Kind: KindFile, Turn: o.Turn, TS: o.TS, Pos: o.Pos,
				Ref: o.PathKey,
			}),
			g.AddEdge(sharedStateEdge(FileNode(o.PathKey), use, EdgeSharedFile, o)),
		)
	}

	for _, name := range sortedUniqueSymbols(o.Symbols) {
		symbol := SymbolNode(o.PathKey, name)
		errs = append(errs,
			g.AddNode(Node{
				ID: symbol, Kind: KindSymbol, Turn: o.Turn, TS: o.TS, Pos: o.Pos, Ref: name,
			}),
			g.AddEdge(sharedStateEdge(symbol, use, EdgeSharedSymbol, o)),
		)
	}

	if o.Supersedes != "" {
		// D-1 with no exception: the SUPERSEDED read is the earlier end. §8.1 item 3 makes it the
		// first eviction candidate, so a slice that reaches it must arrive by the low-multiplier
		// supersedes hop rather than have it upstream of everything the new read explains.
		errs = append(errs, g.AddEdge(Edge{
			From: ToolUseNode(o.Supersedes), To: use,
			Kind: EdgeSupersedes, Weight: fullWeight, Turn: o.Turn,
		}))
	}

	return errors.Join(errs...)
}

// turnLinkKind decides whether the assistant → tool_use edge carries data dependence or only
// control flow.
//
// A call that touches the same file or the same symbol as its predecessor is genuinely coupled to
// it, and the link is an EdgeSequence that survives §6.4's thin slicing. A call that shares nothing
// is merely adjacent in time, and §6.4 is explicit that recency is a proxy for relevance rather
// than relevance itself — so the link is an EdgeControlOnly, which is precisely what thin slicing
// drops.
//
// The FIRST tool use of a session has no predecessor to compare against and is still emitted as an
// EdgeSequence, so that the head of the graph is never disconnected: under a thin slice a
// control-only head would make the session's opening turn unreachable from everything that followed
// it.
func turnLinkKind(g Graph, o ObservedTool) EdgeKind {
	if o.PrevToolUseID == "" {
		return EdgeSequence
	}
	if sharesState(g, o.PrevToolUseID, o.PathKey, o.Symbols) {
		return EdgeSequence
	}
	return EdgeControlOnly
}

// sharesState reports whether prev's tool-use node is already linked to the same file node, or to
// any of the same symbol nodes, as this observation.
//
// It reads the graph through the public Out and In accessors so it works for any Graph
// implementation, and it looks at BOTH directions because the anchor's side depends on whether the
// previous call read the file (file → tool_use) or wrote it (tool_use → file) — the two are equally
// good evidence of coupling, and only one of them is reachable from Out.
//
// It deliberately tolerates an anchor that is not a node yet. An edge may legally precede its
// endpoints (D-6), so the check is on the edge's far ENDPOINT ID rather than on a node lookup; a
// coupling that exists only as a dangling edge is still a coupling.
func sharesState(g Graph, prev core.ToolUseID, pathKey string, symbols []string) bool {
	if pathKey == "" && len(symbols) == 0 {
		return false
	}

	prevUse := ToolUseNode(prev)
	anchors := make(map[NodeID]bool)
	collect := func(edges []Edge, backward bool) {
		for _, e := range edges {
			if e.Kind != EdgeSharedFile && e.Kind != EdgeSharedSymbol {
				continue
			}
			if backward {
				anchors[e.From] = true
				continue
			}
			anchors[e.To] = true
		}
	}
	collect(g.Out(prevUse), false)
	collect(g.In(prevUse), true)

	if pathKey != "" && anchors[FileNode(pathKey)] {
		return true
	}
	for _, name := range symbols {
		if name == "" {
			continue
		}
		if anchors[SymbolNode(pathKey, name)] {
			return true
		}
	}
	return false
}

// sharedStateEdge orients one shared-state edge between an anchor (a file or symbol node) and the
// tool use that touched it, per D-1: a read CONSUMES the anchor, so the anchor is the earlier end
// and the edge runs anchor → tool_use; a write PRODUCES it, so the edge runs tool_use → anchor.
func sharedStateEdge(anchor, use NodeID, kind EdgeKind, o ObservedTool) Edge {
	from, to := anchor, use
	if o.Writes {
		from, to = use, anchor
	}
	return Edge{From: from, To: to, Kind: kind, Weight: fullWeight, Turn: o.Turn}
}

// sortedUniqueSymbols returns names sorted ascending with duplicates and empty entries removed, or
// nil when nothing survives.
//
// Sorting is not cosmetic. The observer's symbol list arrives in whatever order the extractor
// happened to walk the file in, dag/deps.jsonl's edge order is the order AddEdge saw them, and that
// file is byte-compared against a frozen golden — so an unsorted list would make the log depend on
// an upstream traversal detail rather than on the session. It also copies rather than sorting in
// place, because the caller's slice belongs to the caller.
func sortedUniqueSymbols(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// ObservedPrompt is one observed user turn.
type ObservedPrompt struct {
	// Turn is the turn index; it keys the node and is what pairs the prompt with the assistant turn
	// that answered it.
	Turn core.TurnIndex
	// TS is when the prompt was observed.
	TS core.UnixMilli
	// Pos is the prompt's token position in the prefix.
	Pos int
	// Tokens is the prompt's estimated token cost.
	Tokens core.Tokens
	// Ref is a short referent for the prompt — the first line, a slash command name — for status
	// output and log messages. It is never load-bearing.
	Ref string
}

// BuildUserPrompt records one user turn and links it to the assistant turn that answered it, as an
// EdgeConsumes: the assistant turn read the prompt, so the prompt is the producer end (D-1).
//
// The link matters more than it looks. §4.4 lists the user's own instructions as non-reconstructible
// content, and this edge is the only path by which a backward slice from a tool use deep in a
// session reaches the request that set it off.
func BuildUserPrompt(g Graph, o ObservedPrompt) error {
	prompt := UserPromptNode(o.Turn)
	return errors.Join(
		g.AddNode(Node{
			ID: prompt, Kind: KindUserPrompt, Turn: o.Turn, TS: o.TS, Pos: o.Pos,
			Tokens: o.Tokens, Ref: o.Ref,
		}),
		g.AddEdge(Edge{
			From: prompt, To: AssistantNode(o.Turn),
			Kind: EdgeConsumes, Weight: fullWeight, Turn: o.Turn,
		}),
	)
}

// DecisionSpec is one recorded decision (00-ARCHITECTURE.md §5.14) and the nodes that justify it.
type DecisionSpec struct {
	// ID is the DecisionID core.NewDecisionID minted — the same string the `why(decision_id)` MCP
	// tool is called with, which is what makes a decision the agent asks about resolve to a graph
	// node with no lookup table in between.
	ID core.DecisionID
	// Turn is the turn the decision was taken at.
	Turn core.TurnIndex
	// TS is when the decision was recorded.
	TS core.UnixMilli
	// Pos is the decision's token position in the prefix.
	Pos int
	// Tokens is the decision's estimated token cost.
	Tokens core.Tokens
	// Evidence lists the nodes that justify the decision. Each becomes one EdgeExplains edge
	// pointing INTO the decision.
	Evidence []NodeID
	// Summary is the decision in one line; it becomes the node's Ref, truncated to
	// decisionSummaryMaxBytes on a UTF-8 rune boundary.
	Summary string
}

// BuildDecision records one decision and the evidence that explains it.
//
// Every evidence edge runs evidence → decision (D-1): the evidence is the earlier, explanatory end.
// That direction is what makes a BackwardSlice from a decision id answer "what led to this", which
// is the entire job of the `why(decision_id)` MCP tool — reversed, the same slice would return
// everything the decision later influenced, which is a different and much less useful question.
//
// §4.4 lists the reasoning behind a decision among the four kinds of content that cannot be
// reconstructed from the repository, which is why this is the highest-multiplier edge family in
// SP-07 D-3 alongside produces and consumes.
func BuildDecision(g Graph, d DecisionSpec) error {
	if d.ID == "" {
		return fmt.Errorf("%w: decision has no DecisionID", ErrInvalidNode)
	}
	decision := DecisionNode(d.ID)
	errs := []error{g.AddNode(Node{
		ID: decision, Kind: KindDecision, Turn: d.Turn, TS: d.TS, Pos: d.Pos,
		Tokens: d.Tokens, Ref: truncateOnRuneBoundary(d.Summary, decisionSummaryMaxBytes),
	})}
	for _, evidence := range d.Evidence {
		errs = append(errs, g.AddEdge(Edge{
			From: evidence, To: decision, Kind: EdgeExplains, Weight: fullWeight, Turn: d.Turn,
		}))
	}
	return errors.Join(errs...)
}

// EliminationSpec is one negative-knowledge elimination (00-ARCHITECTURE.md §5.10): a search that
// ruled something out, and the file or symbol it ruled it out of.
type EliminationSpec struct {
	// RecordID is the elimination record's own id. It is a plain string rather than a negknow type
	// because dag's import allow-set is foundation-only (§3.2) — negknow imports dag, not the
	// reverse.
	RecordID string
	// Turn is the turn the elimination was recorded at.
	Turn core.TurnIndex
	// TS is when it was recorded.
	TS core.UnixMilli
	// Pos is its token position in the prefix.
	Pos int
	// PathKey is the file the search ruled something out of, already in paths.Key form, or "".
	PathKey string
	// Symbol is the symbol the search ruled something out of, or "".
	Symbol string
	// Evidence lists the nodes that justify the elimination — the tool results of the searches that
	// came back empty.
	Evidence []NodeID
}

// BuildElimination records one negative-knowledge elimination.
//
// Every edge points INTO the elimination, because the elimination is the CONCLUSION: the searches
// that came back empty explain it, and the file and symbol they searched are the state it is about.
// A backward slice from an elimination therefore returns the work that produced it — which is what
// §8.3's staleness question ("is this still true?") needs to be answerable at all.
//
// It deliberately does NOT mint the file or symbol node its anchor edges name, unlike BuildToolUse.
// An elimination is very often precisely the record that a path holds nothing of interest, and
// minting a file node here would assert that the file was touched at this position when it may only
// have been searched for. The anchor edges are therefore allowed to dangle until the observer
// records the anchor itself (D-6), which in practice has already happened: the search that produced
// the evidence is the thing that observed the path.
func BuildElimination(g Graph, e EliminationSpec) error {
	if e.RecordID == "" {
		return fmt.Errorf("%w: elimination has no RecordID", ErrInvalidNode)
	}
	elimination := EliminationNode(e.RecordID)

	// The node's Ref is the state the elimination is ABOUT, not its own id — the id is already the
	// second half of the NodeID, so repeating it would make the one human-readable field carry no
	// information a status dump does not already show.
	ref := e.PathKey
	if ref == "" {
		ref = e.Symbol
	}

	errs := []error{g.AddNode(Node{
		ID: elimination, Kind: KindElimination, Turn: e.Turn, TS: e.TS, Pos: e.Pos, Ref: ref,
	})}
	for _, evidence := range e.Evidence {
		errs = append(errs, g.AddEdge(Edge{
			From: evidence, To: elimination, Kind: EdgeExplains, Weight: fullWeight, Turn: e.Turn,
		}))
	}
	if e.PathKey != "" {
		errs = append(errs, g.AddEdge(Edge{
			From: FileNode(e.PathKey), To: elimination,
			Kind: EdgeSharedFile, Weight: fullWeight, Turn: e.Turn,
		}))
	}
	if e.Symbol != "" {
		errs = append(errs, g.AddEdge(Edge{
			From: SymbolNode(e.PathKey, e.Symbol), To: elimination,
			Kind: EdgeSharedSymbol, Weight: fullWeight, Turn: e.Turn,
		}))
	}
	return errors.Join(errs...)
}

// SegmentSpec is one closed session segment: a contiguous run of turns the scheduler cut at a
// boundary CrossingEdges found cheap (§8.4).
type SegmentSpec struct {
	// ID is the segment's own 1-based, per-project identifier.
	ID core.SegmentID
	// PrevID is the segment that precedes this one, or 0 for the first segment of a project.
	// SegmentID is 1-based precisely so that 0 can mean "none" rather than "segment zero".
	PrevID core.SegmentID
	// StartTurn and EndTurn bound the segment, inclusive.
	StartTurn, EndTurn core.TurnIndex
	// TS is when the segment was closed.
	TS core.UnixMilli
	// StartPos is the segment's opening token position; it becomes the node's Pos, which is what
	// makes a segment boundary a position CrossingEdges can be asked about.
	StartPos int
	// Tokens is the segment's total estimated token cost.
	Tokens core.Tokens
	// Members lists the nodes belonging to the segment. Each becomes one EdgeSequence edge pointing
	// INTO the segment.
	Members []NodeID
}

// BuildSegment records one closed segment, its membership and its place in the segment chain.
//
// Members point INTO the segment and the chain runs previous → current, both under D-1. The chain
// edge is what makes a segment boundary visible to CrossingEdges: §8.4 scores a candidate cut point
// by how many edges straddle it, and a boundary whose only crossing edge is the chain link itself is
// exactly the cheap cut the scheduler is hunting for.
func BuildSegment(g Graph, s SegmentSpec) error {
	if s.ID == 0 {
		return fmt.Errorf("%w: segment has no SegmentID (SegmentID is 1-based)", ErrInvalidNode)
	}
	segment := SegmentNode(s.ID)

	// The node's Ref is the turn range, because Node carries a single Turn field and a segment
	// spans many: without this the EndTurn a caller supplied would be unrecoverable from the graph.
	ref := strconv.Itoa(int(s.StartTurn)) + "-" + strconv.Itoa(int(s.EndTurn))

	errs := []error{g.AddNode(Node{
		ID: segment, Kind: KindSegment, Turn: s.StartTurn, TS: s.TS, Pos: s.StartPos,
		Tokens: s.Tokens, Ref: ref,
	})}
	for _, member := range s.Members {
		errs = append(errs, g.AddEdge(Edge{
			From: member, To: segment, Kind: EdgeSequence, Weight: fullWeight, Turn: s.StartTurn,
		}))
	}
	if s.PrevID != 0 {
		errs = append(errs, g.AddEdge(Edge{
			From: SegmentNode(s.PrevID), To: segment,
			Kind: EdgeSequence, Weight: fullWeight, Turn: s.StartTurn,
		}))
	}
	return errors.Join(errs...)
}

// truncateOnRuneBoundary returns the longest prefix of s that fits in max bytes without splitting a
// multi-byte rune.
//
// The rune boundary is the whole point. A Ref that is not valid UTF-8 comes back from
// dag/deps.jsonl DIFFERENT from the one held in memory, because encoding/json rewrites invalid
// bytes to U+FFFD on the way out — the same hazard nodeid.go's sanitizeKey guards a NodeID against,
// arriving here by a different route.
func truncateOnRuneBoundary(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
