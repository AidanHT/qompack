package dag

// The text names of §5.9's nine node kinds and eight edge kinds, and the per-hop score multiplier
// each edge kind contributes to a slice (SP-07 D-3).
//
// These names are NOT a wire format. dag/deps.jsonl carries the integer kind — that is exactly what
// the frozen fixtures testdata/golden/contracts/dag/want/{node_line,edge_line}.jsonl pin, and Rule
// W-2 makes those bytes final. The names exist for two other jobs: GraphStats' per-kind map keys
// (00-ARCHITECTURE.md §14.0), where "1 204 shared_symbol edges" is worth reading and "1 204 kind-4
// edges" is not, and log messages about a record the loader could not make sense of.
//
// Deliberately absent, and it must stay that way: MarshalText and UnmarshalText. Node.MarshalJSON
// marshals an alias struct that still contains a NodeKind field, so an encoding.TextMarshaler on
// the kind type would silently flip every emitted node line from "kind":4 to "kind":"file", and the
// matching UnmarshalText would make json.Unmarshal of the frozen fixture fail with "cannot
// unmarshal number into Go struct field". String and ParseNodeKind are plain methods that
// encoding/json never consults, which is the whole reason the text form is spelled this way.
// TestFrozenFixtureKindNumberingUnchanged fails loudly if anyone adds the TextMarshaler pair.

// invalidKindName is what both sentinels render as, and the one name neither ParseNodeKind nor
// ParseEdgeKind will hand back. A caller able to parse "invalid" could construct a Node carrying
// the very kind AddNode exists to reject, so the sentinel is one-way: renderable for a log line,
// never parseable.
const invalidKindName = "invalid"

// nodeKindNames is indexed by NodeKind, KindInvalid included, and is the single source both String
// and ParseNodeKind read — the inverse map below is built from this array in init(), so a tenth
// kind added here cannot be forgotten there.
var nodeKindNames = [...]string{
	KindToolUse:     "tool_use",
	KindToolResult:  "tool_result",
	KindAssistant:   "assistant",
	KindUserPrompt:  "user_prompt",
	KindFile:        "file",
	KindSymbol:      "symbol",
	KindDecision:    "decision",
	KindElimination: "elimination",
	KindSegment:     "segment",
	KindInvalid:     invalidKindName,
}

// edgeKindNames is nodeKindNames' sibling for EdgeKind.
var edgeKindNames = [...]string{
	EdgeSequence:     "seq",
	EdgeProduces:     "produces",
	EdgeConsumes:     "consumes",
	EdgeSharedFile:   "shared_file",
	EdgeSharedSymbol: "shared_symbol",
	EdgeSupersedes:   "supersedes",
	EdgeExplains:     "explains",
	EdgeControlOnly:  "control",
	EdgeInvalid:      invalidKindName,
}

// edgeKindMultipliers is SP-07 D-3's per-hop relevance decay, indexed by EdgeKind. A slice
// multiplies a node's inherited score by this factor at every hop, so these numbers decide the
// ORDER retrieval and checkpoint ranking consume a slice in — not merely its size.
//
// The ordering argument, row by row:
//
//   - produces, consumes and explains carry 1.00 because no information is lost across the hop: a
//     tool result IS its tool use's output, the assistant read that result verbatim, and evidence →
//     decision is the highest-value link in §4.4's non-reconstructible list.
//   - shared_symbol (0.95) outranks shared_file (0.88) because symbol identity is a stronger
//     shared-state claim than file identity: two turns touching one symbol are working on the same
//     thing, whereas one file has many independent regions and two turns may share nothing but its
//     name (§8.1 item 4 keys both edge families).
//   - seq (0.60) is adjacency, and §6.4 is explicit that recency is only a proxy for relevance;
//     sequence edges survive thin slicing but must never outweigh a real data-flow hop.
//   - control (0.50) is control dependence with no data flow. §6.4's thin slicing drops these
//     entirely, so the multiplier only matters on a full slice, where they are the weakest evidence
//     available.
//   - supersedes (0.30) is last on purpose. §8.1 item 3 makes superseded reads the FIRST eviction
//     candidates, so reaching one through a slice must not resurrect it: it has to score low even
//     when it is genuinely reachable.
//   - the EdgeInvalid sentinel is 0.00 because it is never traversed at all.
//
// None of these values, and none of the byte counts in nodeid.go, duplicates a configuration
// default, so the D11 / §11.6 nomagic pass is satisfied with no allow-annotation. Do not "round"
// 0.88 to 0.9 or 0.60 to 0.55: both are in that forbidden set, and both values here were chosen to
// avoid it.
var edgeKindMultipliers = [...]float32{
	EdgeSequence:     0.60,
	EdgeProduces:     1.00,
	EdgeConsumes:     1.00,
	EdgeSharedFile:   0.88,
	EdgeSharedSymbol: 0.95,
	EdgeSupersedes:   0.30,
	EdgeExplains:     1.00,
	EdgeControlOnly:  0.50,
	EdgeInvalid:      0.00,
}

// nodeKindByName and edgeKindByName are the inverses of the two name tables. They are built in
// init() rather than written out a second time so the forward and reverse directions cannot drift;
// the sentinel is skipped while building, which is what makes ParseNodeKind("invalid") report
// false without a special case in the lookup itself.
var (
	nodeKindByName map[string]NodeKind
	edgeKindByName map[string]EdgeKind
)

func init() {
	nodeKindByName = make(map[string]NodeKind, len(nodeKindNames)-1)
	for i, name := range nodeKindNames {
		if k := NodeKind(i); k != KindInvalid {
			nodeKindByName[name] = k
		}
	}

	edgeKindByName = make(map[string]EdgeKind, len(edgeKindNames)-1)
	for i, name := range edgeKindNames {
		if k := EdgeKind(i); k != EdgeInvalid {
			edgeKindByName[name] = k
		}
	}
}

// String returns k's text name — "file", "tool_use", "shared_symbol" and so on — for GraphStats map
// keys and log messages. A kind outside the declared range renders as "invalid" rather than
// panicking, because the caller most likely to hold one is the deps.jsonl loader reading a torn or
// hand-edited line, and it needs to report the problem, not become it.
func (k NodeKind) String() string {
	if int(k) >= len(nodeKindNames) {
		return invalidKindName
	}
	return nodeKindNames[k]
}

// ParseNodeKind resolves a text name produced by NodeKind.String back to its kind. It reports false
// for an unknown name AND for "invalid" itself: the sentinel is a way to SAY a kind is unusable, not
// a kind that can be asked for.
func ParseNodeKind(s string) (NodeKind, bool) {
	k, ok := nodeKindByName[s]
	if !ok {
		return KindInvalid, false
	}
	return k, true
}

// String returns k's text name, with the same out-of-range behaviour as NodeKind.String.
func (k EdgeKind) String() string {
	if int(k) >= len(edgeKindNames) {
		return invalidKindName
	}
	return edgeKindNames[k]
}

// ParseEdgeKind resolves a text name produced by EdgeKind.String back to its kind, with the same
// refusal of "invalid" as ParseNodeKind.
func ParseEdgeKind(s string) (EdgeKind, bool) {
	k, ok := edgeKindByName[s]
	if !ok {
		return EdgeInvalid, false
	}
	return k, true
}

// Multiplier returns the factor a slice applies to a node's inherited relevance score when it
// crosses an edge of this kind (SP-07 D-3; see edgeKindMultipliers for why each value is what it
// is). An out-of-range kind returns 0, which drops that branch of the walk rather than letting a
// corrupt record propagate an arbitrary score.
func (k EdgeKind) Multiplier() float32 {
	if int(k) >= len(edgeKindMultipliers) {
		return 0
	}
	return edgeKindMultipliers[k]
}
