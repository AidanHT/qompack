package dag

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
)

// The NodeID scheme of SP-07 D-2: "<prefix>:<stable-key>", split on the FIRST colon.
//
// Every consumer builds IDs through the constructors below rather than concatenating strings,
// because a NodeID is the join key between dag/deps.jsonl, a checkpoint's evidence lists and every
// MCP retrieval answer. One component spelling a file node differently from another does not fail
// loudly — it silently produces two disconnected halves of the same graph.
//
// The prefixes are the long forms ("tooluse", not "tu"). That is what the frozen contract fixture
// testdata/golden/contracts/dag/want/edge_line.jsonl already carries
// ("tooluse:toolu_01A2B3C4D5E6F7G8H9J0K1L2"), and what internal/analyzer and internal/dag/dagtest
// already construct by hand, so the long forms are the ones Rule W-2 fixes.

// nodeIDSep separates a NodeID's prefix from its stable key. The split is on the first occurrence
// only, so a key may contain colons of its own — a drive letter that survived normalization, a
// scope-resolution operator in a symbol name — without making the prefix ambiguous.
const nodeIDSep = ":"

// symbolSep joins a symbol node's path and name into one stable key. A symbol with no path yields
// a key that starts with the separator ("#refreshToken"), which keeps a pathless symbol distinct
// from a file of the same name rather than colliding with it.
const symbolSep = "#"

// keyHashSep marks where an over-length key was truncated. It is a character no path, tool-use id
// or decision id produces, so a reader can tell a truncated key from a short one by inspection.
const keyHashSep = "~"

// nodeIDHashDomain domain-separates the over-length-key digest from every other HashBytes use in
// the codebase (see internal/core/hash.go's domain registry). It is deliberately NOT one of that
// registry's production domains: those key content the store persists, whereas this digest only
// disambiguates two long keys sharing a head and never addresses anything on disk.
const nodeIDHashDomain = "qompack.dag.nodeid"

// maxKeyBytes is the longest stable key a NodeID carries verbatim, and keyHeadBytes is how much of
// a longer key survives in front of the hash suffix.
//
// A NodeID is a map key held for a whole session and repeated on every deps.jsonl line that
// mentions the node, so an unbounded key turns one pathological path into a permanent per-record
// cost. Past the cap the key becomes keyHeadBytes of head, keyHashSep, and 12 hex characters of a
// digest of the WHOLE key — so two paths sharing a 360-byte prefix still get distinct IDs, which a
// plain truncation would not give.
const (
	maxKeyBytes  = 384
	keyHeadBytes = 360
)

// sanitizeReplacement is what a control byte or an invalid UTF-8 byte becomes.
const sanitizeReplacement = byte('_')

// firstPrintableByte and delByte bound the C0 control range and name DEL. Both are stripped from
// keys: a raw newline would split one deps.jsonl record into two unparseable ones, and the rest of
// the range would make a NodeID unreadable in any log or error message that quotes it.
const (
	firstPrintableByte = byte(0x20)
	delByte            = byte(0x7F)
)

// nodeKindPrefixes is indexed by NodeKind, KindInvalid included, and is the ONE table both
// prefixOf and kindOfPrefix read — the inverse map is built from it in init(), so a tenth kind
// added here cannot be added to one direction and forgotten in the other.
//
// The sentinel's entry is the empty string: KindInvalid has no prefix because it can never
// legitimately appear in a NodeID, and an empty prefix is not something ParseNodeID will resolve.
var nodeKindPrefixes = [...]string{
	KindToolUse:     "tooluse",
	KindToolResult:  "toolresult",
	KindAssistant:   "assistant",
	KindUserPrompt:  "userprompt",
	KindFile:        "file",
	KindSymbol:      "symbol",
	KindDecision:    "decision",
	KindElimination: "elimination",
	KindSegment:     "segment",
	KindInvalid:     "",
}

// nodeKindByPrefix is the inverse of nodeKindPrefixes, built in init() from that same table. The
// sentinel is skipped while building, which is what makes kindOfPrefix("") report false without a
// special case in the lookup.
var nodeKindByPrefix map[string]NodeKind

func init() {
	nodeKindByPrefix = make(map[string]NodeKind, len(nodeKindPrefixes)-1)
	for i, prefix := range nodeKindPrefixes {
		if k := NodeKind(i); k != KindInvalid {
			nodeKindByPrefix[prefix] = k
		}
	}
}

// prefixOf returns the NodeID prefix k's nodes carry, or "" for the sentinel.
func prefixOf(k NodeKind) string {
	if int(k) >= len(nodeKindPrefixes) {
		return ""
	}
	return nodeKindPrefixes[k]
}

// kindOfPrefix resolves a NodeID prefix back to its kind, reporting false for anything that is not
// one of the nine.
func kindOfPrefix(prefix string) (NodeKind, bool) {
	k, ok := nodeKindByPrefix[prefix]
	if !ok {
		return KindInvalid, false
	}
	return k, true
}

// newNodeID assembles one NodeID from a kind and an unsanitized key. Every constructor goes through
// here, which is what guarantees sanitizeKey is not something a caller can forget.
func newNodeID(k NodeKind, key string) NodeID {
	return NodeID(prefixOf(k) + nodeIDSep + sanitizeKey(key))
}

// ToolUseNode returns the node id for a tool invocation. The stable key is the host's own
// ToolUseID, verbatim, because that is the only identifier both the PostToolUse payload and the
// stored tool-use record already agree on.
func ToolUseNode(id core.ToolUseID) NodeID { return newNodeID(KindToolUse, string(id)) }

// ToolResultNode returns the node id for a tool invocation's result. It keys on the SAME ToolUseID
// as ToolUseNode — the prefix is what distinguishes them — so the §8.1 item 4 edge
// tool_use → tool_result can be built from the payload alone, with no extra identifier to carry.
func ToolResultNode(id core.ToolUseID) NodeID { return newNodeID(KindToolResult, string(id)) }

// AssistantNode returns the node id for an assistant turn, keyed on its decimal turn index.
func AssistantNode(t core.TurnIndex) NodeID {
	return newNodeID(KindAssistant, strconv.Itoa(int(t)))
}

// UserPromptNode returns the node id for a user turn, keyed on its decimal turn index. It shares
// the turn-index key space with AssistantNode and is kept distinct by the prefix, exactly as the
// tool-use/tool-result pair is.
func UserPromptNode(t core.TurnIndex) NodeID {
	return newNodeID(KindUserPrompt, strconv.Itoa(int(t)))
}

// FileNode returns the node id for a file. pathKey MUST already be in paths.Key form: this
// function deliberately does NOT call paths.Key, because doing so would need a project root it
// does not have, and would silently re-fold a key the caller had already normalized against a
// different root (§4).
func FileNode(pathKey string) NodeID { return newNodeID(KindFile, pathKey) }

// SymbolNode returns the node id for a source symbol, keyed on "<pathKey>#<name>"
// (00-ARCHITECTURE.md §5.22b). pathKey may be empty — a symbol whose defining file is not known
// yet — which yields "symbol:#name"; that keeps it distinct from the same symbol once its path IS
// known, rather than having the two silently merge.
func SymbolNode(pathKey, name string) NodeID {
	return newNodeID(KindSymbol, pathKey+symbolSep+name)
}

// DecisionNode returns the node id for a recorded decision (§5.14), keyed on the DecisionID
// core.NewDecisionID minted. That is the same string the `why(decision_id)` MCP tool is called
// with, so a decision the agent asks about resolves to a graph node without a lookup table.
func DecisionNode(id core.DecisionID) NodeID { return newNodeID(KindDecision, string(id)) }

// EliminationNode returns the node id for a negative-knowledge elimination (§5.10), keyed on the
// record's own id. It takes a plain string rather than a negknow type because dag's import allow-set
// is foundation-only (§3.2) — negknow imports dag, not the reverse.
func EliminationNode(recordID string) NodeID { return newNodeID(KindElimination, recordID) }

// SegmentNode returns the node id for a closed session segment, keyed on its decimal SegmentID.
func SegmentNode(id core.SegmentID) NodeID {
	return newNodeID(KindSegment, strconv.Itoa(int(id)))
}

// ParseNodeID splits a NodeID into its kind and stable key, reporting false for a missing colon, an
// unknown prefix, or an empty key.
//
// This is how AddNode validates that a node's declared Kind matches its ID. It has to be: because
// the KindInvalid sentinel sits at the END of node.go's iota block — the frozen fixture
// testdata/golden/contracts/dag/want/node_line.jsonl pins "kind":4 to KindFile, so prepending a
// sentinel would renumber every kind — NodeKind's zero value is KindToolUse, not "unset". A node
// whose Kind field was never set is therefore indistinguishable from a genuine tool-use node by
// inspection of the field alone, while its ID's prefix says so unambiguously.
func ParseNodeID(id NodeID) (kind NodeKind, key string, ok bool) {
	prefix, key, found := strings.Cut(string(id), nodeIDSep)
	if !found || key == "" {
		return KindInvalid, "", false
	}
	kind, ok = kindOfPrefix(prefix)
	if !ok {
		return KindInvalid, "", false
	}
	return kind, key, true
}

// sanitizeKey makes an arbitrary string safe to carry inside a NodeID, and is idempotent — running
// it on an already-sanitized key is a no-op, so a NodeID's own key can be fed back through a
// constructor without hashing a hash.
//
// Three hazards, in order:
//
//   - C0 control bytes and DEL become '_'. A raw newline is the dangerous one: dag/deps.jsonl is
//     newline-delimited, so a NodeID containing one would split a single record into two
//     unparseable fragments.
//   - Invalid UTF-8 becomes '_' as well. encoding/json rewrites invalid bytes to U+FFFD on the way
//     out, so a NodeID that is not valid UTF-8 would come back from disk DIFFERENT from the one
//     held in memory, and every edge referencing it would dangle.
//   - A key over maxKeyBytes is replaced by its head, keyHashSep, and 12 hex characters of a
//     domain-separated digest of the whole key. The head is backed off to a UTF-8 rune boundary,
//     because cutting a multi-byte rune in half would reintroduce the invalid-UTF-8 hazard the step
//     above just removed.
func sanitizeKey(k string) string {
	var b strings.Builder
	b.Grow(len(k))
	for i := 0; i < len(k); i++ {
		c := k[i]
		if c < firstPrintableByte || c == delByte {
			b.WriteByte(sanitizeReplacement)
			continue
		}
		b.WriteByte(c)
	}

	s := strings.ToValidUTF8(b.String(), string(sanitizeReplacement))
	if len(s) <= maxKeyBytes {
		return s
	}

	head := s[:keyHeadBytes]
	for len(head) > 0 && !utf8.RuneStart(s[len(head)]) {
		head = head[:len(head)-1]
	}
	sum := core.HashBytes(nodeIDHashDomain, []byte(s))
	return head + keyHashSep + sum.Short()
}
