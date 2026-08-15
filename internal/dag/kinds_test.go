// These tests are in-package (package dag, not dag_test) because TestNodeKindTablesAligned asserts
// facts about the unexported prefix and name tables themselves — that they are the SAME table, of
// the same length, with no kind present in one and missing from the other. Asserting that from
// outside the package would only be able to observe the round trip, which is exactly the property
// that keeps holding while the tables silently drift apart on a kind nobody parses yet.
package dag

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// realNodeKinds is the nine node kinds 00-ARCHITECTURE.md §5.9 defines, paired with the text name
// each renders as. The names are the GraphStats map keys and the log-message spelling; they are
// deliberately NOT what dag/deps.jsonl carries, which is the integer (see
// TestFrozenFixtureKindNumberingUnchanged).
var realNodeKinds = []struct {
	kind NodeKind
	name string
}{
	{KindToolUse, "tool_use"},
	{KindToolResult, "tool_result"},
	{KindAssistant, "assistant"},
	{KindUserPrompt, "user_prompt"},
	{KindFile, "file"},
	{KindSymbol, "symbol"},
	{KindDecision, "decision"},
	{KindElimination, "elimination"},
	{KindSegment, "segment"},
}

// realEdgeKinds is realNodeKinds' sibling for the eight edge kinds.
var realEdgeKinds = []struct {
	kind EdgeKind
	name string
}{
	{EdgeSequence, "seq"},
	{EdgeProduces, "produces"},
	{EdgeConsumes, "consumes"},
	{EdgeSharedFile, "shared_file"},
	{EdgeSharedSymbol, "shared_symbol"},
	{EdgeSupersedes, "supersedes"},
	{EdgeExplains, "explains"},
	{EdgeControlOnly, "control"},
}

// TestNodeKindTextRoundTrip asserts every real node kind renders as its documented name and parses
// back to itself, and that the KindInvalid sentinel is a one-way street: it renders as "invalid" so
// a log line can say something useful about a corrupt kind, but ParseNodeKind refuses to hand the
// sentinel back, because a caller that could parse "invalid" could construct a Node whose Kind is
// the very value AddNode exists to reject.
func TestNodeKindTextRoundTrip(t *testing.T) {
	t.Parallel()

	require.Len(t, realNodeKinds, 9, "§5.9 fixes the node-kind set at nine")

	for _, tc := range realNodeKinds {
		require.Equal(t, tc.name, tc.kind.String())

		got, ok := ParseNodeKind(tc.name)
		require.True(t, ok, "%s must parse", tc.name)
		require.Equal(t, tc.kind, got)
	}

	require.Equal(t, "invalid", KindInvalid.String())
	require.Equal(t, "invalid", NodeKind(200).String(), "an out-of-range kind renders as the sentinel")

	_, ok := ParseNodeKind("invalid")
	require.False(t, ok, "the sentinel's own name is not a parseable kind")

	_, ok = ParseNodeKind("no_such_kind")
	require.False(t, ok)

	_, ok = ParseNodeKind("")
	require.False(t, ok)
}

// TestEdgeKindTextRoundTrip is TestNodeKindTextRoundTrip's sibling for the eight edge kinds.
func TestEdgeKindTextRoundTrip(t *testing.T) {
	t.Parallel()

	require.Len(t, realEdgeKinds, 8, "§5.9 fixes the edge-kind set at eight")

	for _, tc := range realEdgeKinds {
		require.Equal(t, tc.name, tc.kind.String())

		got, ok := ParseEdgeKind(tc.name)
		require.True(t, ok, "%s must parse", tc.name)
		require.Equal(t, tc.kind, got)
	}

	require.Equal(t, "invalid", EdgeInvalid.String())
	require.Equal(t, "invalid", EdgeKind(200).String())

	_, ok := ParseEdgeKind("invalid")
	require.False(t, ok)

	_, ok = ParseEdgeKind("no_such_kind")
	require.False(t, ok)

	_, ok = ParseEdgeKind("")
	require.False(t, ok)
}

// TestEdgeKindMultiplierTable pins SP-07 D-3's per-hop score multipliers exactly.
//
// require.Equal, not require.InDelta: these are not measurements with tolerance, they are the
// constants a slice's score is multiplied by at every hop, and a change of 0.01 in EdgeSharedFile
// reorders retrieval results. The comparison is exact on purpose.
func TestEdgeKindMultiplierTable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		kind EdgeKind
		want float32
	}{
		{EdgeProduces, 1.00},
		{EdgeConsumes, 1.00},
		{EdgeExplains, 1.00},
		{EdgeSharedSymbol, 0.95},
		{EdgeSharedFile, 0.88},
		{EdgeSequence, 0.60},
		{EdgeControlOnly, 0.50},
		{EdgeSupersedes, 0.30},
	} {
		require.Equal(t, tc.want, tc.kind.Multiplier(), "%s multiplier", tc.kind)
	}

	require.Equal(t, float32(0), EdgeInvalid.Multiplier(), "the sentinel is never traversed")
	require.Equal(t, float32(0), EdgeKind(200).Multiplier(), "an out-of-range kind is never traversed")
}

// TestNodeKindTablesAligned asserts prefixOf and kindOfPrefix are two views of ONE table.
//
// The failure this guards against is a new tenth kind added to the name table and forgotten in the
// prefix table (or the reverse): the round trip below still passes for the nine that exist, and the
// new kind silently constructs NodeIDs with an empty prefix that ParseNodeID then rejects — at
// AddNode time, in production, far from here.
func TestNodeKindTablesAligned(t *testing.T) {
	t.Parallel()

	require.Len(t, nodeKindPrefixes, 10, "nine kinds plus the KindInvalid sentinel")
	require.Len(t, nodeKindNames, 10, "the prefix table and the name table must have the same shape")
	require.Len(t, edgeKindNames, 9, "eight kinds plus the EdgeInvalid sentinel")
	require.Len(t, edgeKindMultipliers, 9, "every edge kind, sentinel included, has a multiplier")

	seen := make(map[string]NodeKind, len(realNodeKinds))
	for _, tc := range realNodeKinds {
		prefix := prefixOf(tc.kind)
		require.NotEmpty(t, prefix, "%s has no NodeID prefix", tc.name)
		require.NotContains(t, prefix, ":",
			"%s: a prefix containing the separator would make ParseNodeID ambiguous", tc.name)

		back, ok := kindOfPrefix(prefix)
		require.True(t, ok, "%s: prefix %q does not map back", tc.name, prefix)
		require.Equal(t, tc.kind, back)

		prior, dup := seen[prefix]
		require.False(t, dup, "prefix %q is claimed by both %s and %s", prefix, prior, tc.kind)
		seen[prefix] = tc.kind
	}

	require.Empty(t, prefixOf(KindInvalid), "the sentinel has no prefix; it can never appear in a NodeID")
	_, ok := kindOfPrefix("")
	require.False(t, ok, "an empty prefix must not resolve to the sentinel")
	_, ok = kindOfPrefix("invalid")
	require.False(t, ok)
}

// TestFrozenFixtureKindNumberingUnchanged is the regression guard for the frozen §16 fixtures
// testdata/golden/contracts/dag/want/{node_line,edge_line}.jsonl (Rule W-2).
//
// Those two files pin "kind":4 to KindFile and "kind":2 to EdgeConsumes, which makes the iota
// blocks in node.go and edge.go byte-load-bearing: reordering them, renumbering them, or
// prepending an "invalid" sentinel at position 0 all silently rewrite the on-disk wire format of
// dag/deps.jsonl. That is why KindInvalid and EdgeInvalid are appended LAST rather than declared
// first, at the cost of NodeKind's zero value being KindToolUse rather than "unset".
//
// It also fails loudly if anyone gives NodeKind or EdgeKind a MarshalText/UnmarshalText method.
// Node.MarshalJSON marshals an alias struct that still contains a NodeKind field, so a MarshalText
// on the kind type flips the emitted JSON from "kind":4 to "kind":"file" AND makes json.Unmarshal
// of the frozen fixture fail with "cannot unmarshal number into Go struct field". The kinds
// therefore get String()/ParseNodeKind() — plain methods encoding/json does not consult — and never
// the encoding.TextMarshaler pair.
func TestFrozenFixtureKindNumberingUnchanged(t *testing.T) {
	t.Parallel()

	require.Equal(t, NodeKind(4), KindFile, "node_line.jsonl pins \"kind\":4 to KindFile")
	require.Equal(t, EdgeKind(2), EdgeConsumes, "edge_line.jsonl pins \"kind\":2 to EdgeConsumes")
	require.Equal(t, NodeKind(8), KindSegment, "KindSegment is the last real node kind")
	require.Equal(t, EdgeKind(7), EdgeControlOnly, "EdgeControlOnly is the last real edge kind")
	require.Equal(t, NodeKind(9), KindInvalid, "the node sentinel is appended after KindSegment")
	require.Equal(t, EdgeKind(8), EdgeInvalid, "the edge sentinel is appended after EdgeControlOnly")

	nodeJSON, err := json.Marshal(Node{ID: "file:x", Kind: KindFile})
	require.NoError(t, err)
	require.Contains(t, string(nodeJSON), `"kind":4`,
		"the node wire format carries the integer kind")
	require.NotContains(t, string(nodeJSON), `"kind":"file"`,
		"a MarshalText on NodeKind would produce this and break both frozen fixtures")

	edgeJSON, err := json.Marshal(Edge{From: "file:a", To: "file:b", Kind: EdgeConsumes})
	require.NoError(t, err)
	require.Contains(t, string(edgeJSON), `"kind":2`)
	require.NotContains(t, string(edgeJSON), `"kind":"consumes"`)

	var decoded Node
	require.NoError(t, json.Unmarshal([]byte(`{"kind":4}`), &decoded),
		"an UnmarshalText on NodeKind would fail here: cannot unmarshal number into Go struct field")
	require.Equal(t, KindFile, decoded.Kind)

	var decodedEdge Edge
	require.NoError(t, json.Unmarshal([]byte(`{"kind":2}`), &decodedEdge))
	require.Equal(t, EdgeConsumes, decodedEdge.Kind)
}
