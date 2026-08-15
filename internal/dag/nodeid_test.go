// These tests are in-package (package dag, not dag_test) because they assert facts about
// sanitizeKey and prefixOf directly — in particular that sanitizeKey is idempotent, which is what
// makes a NodeID safe to feed back through its own constructor and is not observable from outside.
package dag

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// nodeIDGolden is the hand-derived NodeID table at testdata/golden/contracts/dag/nodeid.json,
// relative to this package's own directory. Its expected column was written from SP-07 D-2's
// specification, not captured from this implementation's output, so it is a check ON the
// constructors rather than a snapshot OF them.
const nodeIDGolden = "../../testdata/golden/contracts/dag/nodeid.json"

// nodeIDCase is one row of nodeIDGolden. Input2 is set only for SymbolNode, which is the one
// constructor taking two inputs; every other row leaves it empty.
type nodeIDCase struct {
	Kind     string `json:"kind"`
	Input    string `json:"input"`
	Input2   string `json:"input2"`
	Expected string `json:"expected"`
}

// readNodeIDGolden decodes the golden table, failing loudly if it is missing: an absent contract
// fixture is a repository problem, not a not-yet-implemented one.
func readNodeIDGolden(t *testing.T) []nodeIDCase {
	t.Helper()
	b, err := os.ReadFile(nodeIDGolden)
	require.NoError(t, err, "the NodeID contract fixture is missing: %s", nodeIDGolden)

	var cases []nodeIDCase
	require.NoError(t, json.Unmarshal(b, &cases))
	require.GreaterOrEqual(t, len(cases), 20, "the fixture must cover every kind and every sanitization branch")
	return cases
}

// construct dispatches one golden row to the constructor its kind names. It is deliberately a
// switch over every NodeKind with no default fall-through to a generic builder: a tenth kind added
// without a constructor fails here rather than silently producing an unconstructible NodeID.
func construct(t *testing.T, k NodeKind, c nodeIDCase) NodeID {
	t.Helper()
	atoi := func(s string) int {
		n, err := strconv.Atoi(s)
		require.NoError(t, err, "%s rows carry a decimal input", c.Kind)
		return n
	}

	switch k {
	case KindToolUse:
		return ToolUseNode(core.ToolUseID(c.Input))
	case KindToolResult:
		return ToolResultNode(core.ToolUseID(c.Input))
	case KindAssistant:
		return AssistantNode(core.TurnIndex(atoi(c.Input)))
	case KindUserPrompt:
		return UserPromptNode(core.TurnIndex(atoi(c.Input)))
	case KindFile:
		return FileNode(c.Input)
	case KindSymbol:
		return SymbolNode(c.Input, c.Input2)
	case KindDecision:
		return DecisionNode(core.DecisionID(c.Input))
	case KindElimination:
		return EliminationNode(c.Input)
	case KindSegment:
		return SegmentNode(core.SegmentID(atoi(c.Input)))
	case KindInvalid:
		t.Fatalf("the sentinel has no constructor")
	}
	t.Fatalf("no constructor for kind %s", k)
	return ""
}

// TestNodeIDConstructors asserts every constructor reproduces the golden table byte for byte, and
// that the table covers all nine kinds. A NodeID is the join key between dag/deps.jsonl, the
// checkpoint's evidence lists and every MCP retrieval answer, so a constructor that respells one
// silently orphans every record already written under the old spelling.
func TestNodeIDConstructors(t *testing.T) {
	t.Parallel()

	covered := make(map[NodeKind]bool, len(realNodeKinds))
	for _, c := range readNodeIDGolden(t) {
		k, ok := ParseNodeKind(c.Kind)
		require.True(t, ok, "golden row names an unknown kind %q", c.Kind)
		covered[k] = true

		got := construct(t, k, c)
		require.Equal(t, NodeID(c.Expected), got,
			"%s(%q, %q) drifted from testdata/golden/contracts/dag/nodeid.json", c.Kind, c.Input, c.Input2)

		// Whatever a constructor produces must be parseable back to the kind that produced it;
		// AddNode validates a node's Kind against its NodeID prefix exactly this way, because
		// NodeKind's zero value is KindToolUse and therefore cannot mean "unset".
		back, key, ok := ParseNodeID(got)
		require.True(t, ok, "constructor produced an unparseable NodeID: %q", got)
		require.Equal(t, k, back)
		require.NotEmpty(t, key)
	}

	for _, tc := range realNodeKinds {
		require.True(t, covered[tc.kind], "the golden table covers no %s row", tc.name)
	}
}

// TestNodeIDLongKeyHashSuffix pins SP-07 D-2's over-length arithmetic.
//
// The budget matters because a NodeID is a map key held for the whole session and written to every
// deps.jsonl line that mentions the node: an unbounded key turns one pathological path into a
// permanent per-record cost. 384 bytes is the cap; past it the key becomes 360 bytes of head, a
// "~", and 12 hex characters of a domain-separated digest of the WHOLE key — so two paths sharing
// a 360-byte prefix still get distinct IDs.
func TestNodeIDLongKeyHashSuffix(t *testing.T) {
	t.Parallel()

	path := "src/" + strings.Repeat("x", 493) + ".ts"
	require.Len(t, path, 500, "the fixture path is exactly 500 ASCII bytes")

	id := FileNode(path)
	// 5 ("file:") + 360 (head) + 1 ("~") + 12 (Hash.Short) = 378. Asserted as a number, not as a
	// formula, so a change to any of the four terms has to be made deliberately here too.
	require.Len(t, string(id), 378)
	require.True(t, strings.HasPrefix(string(id), "file:src/xxx"))
	require.Contains(t, string(id), "~")

	// Determinism: the same path twice is the same NodeID, or nothing in deps.jsonl joins up.
	require.Equal(t, id, FileNode(path))

	// Idempotence: re-sanitizing an already-sanitized key is a no-op, so feeding a NodeID's own key
	// back through its constructor is stable rather than hashing a hash.
	_, key, ok := ParseNodeID(id)
	require.True(t, ok)
	require.Equal(t, key, sanitizeKey(key))
	require.Equal(t, id, FileNode(key))
	require.Equal(t, sanitizeKey(path), sanitizeKey(sanitizeKey(path)))

	// Two paths that share the 360-byte head must not collide: the digest covers the whole key.
	other := "src/" + strings.Repeat("x", 400) + strings.Repeat("y", 93) + ".ts"
	require.Len(t, other, 500)
	require.NotEqual(t, id, FileNode(other))

	// A key at exactly the cap is passed through untouched; only past it does the hash kick in.
	atCap := strings.Repeat("z", 384)
	require.Equal(t, NodeID("file:"+atCap), FileNode(atCap))
	require.NotEqual(t, NodeID("file:"+atCap+"z"), FileNode(atCap+"z"))
}

// TestNodeIDControlCharsSanitized asserts the two hazards D-2's sanitization exists to remove.
//
// A raw newline in a NodeID would split one dag/deps.jsonl record into two unparseable ones, and a
// key that is not valid UTF-8 would be rewritten to U+FFFD by encoding/json on the way out — so the
// ID read back from disk would no longer equal the ID held in memory, and every edge referencing it
// would dangle.
func TestNodeIDControlCharsSanitized(t *testing.T) {
	t.Parallel()

	require.Equal(t, NodeID("file:src/a_b.ts"), FileNode("src/a\nb.ts"))
	require.Equal(t, NodeID("file:src/a_b.ts"), FileNode("src/a\tb.ts"))
	require.Equal(t, NodeID("file:src/a_b.ts"), FileNode("src/a"+string(rune(0x7F))+"b.ts"))

	invalid := FileNode("src/a" + string([]byte{0xFF}) + "b.ts")
	require.True(t, utf8.ValidString(string(invalid)), "a NodeID must be valid UTF-8")
	require.NotContains(t, string(invalid), "\n")
	require.NotContains(t, string(invalid), string(rune(0xFFFD)), "the byte is replaced, not decoded")

	// The round trip encoding/json would otherwise silently break.
	b, err := json.Marshal(Node{ID: invalid, Kind: KindFile, Ref: "src/a?b.ts"})
	require.NoError(t, err)
	var back Node
	require.NoError(t, json.Unmarshal(b, &back))
	require.Equal(t, invalid, back.ID, "the NodeID must survive a deps.jsonl round trip unchanged")

	// Sanitization runs on the joined symbol key too, not only on file paths.
	require.Equal(t, NodeID("symbol:src/a_b.ts#do_it"), SymbolNode("src/a\nb.ts", "do\rit"))
}

// TestParseNodeID asserts the split-on-first-colon rule and the three ok=false cases.
//
// Splitting on the FIRST colon is what lets a stable key contain colons of its own — a Windows
// drive letter that survived normalization, or a symbol name with a scope resolution operator —
// without the prefix becoming ambiguous.
func TestParseNodeID(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		id   NodeID
		kind NodeKind
		key  string
	}{
		{"tooluse:x", KindToolUse, "x"},
		{"symbol:p#n", KindSymbol, "p#n"},
		{"file:src/auth.ts", KindFile, "src/auth.ts"},
		{"segment:14", KindSegment, "14"},
		{"file:c:/tmp/x.ts", KindFile, "c:/tmp/x.ts"},
	} {
		kind, key, ok := ParseNodeID(tc.id)
		require.True(t, ok, "%q must parse", tc.id)
		require.Equal(t, tc.kind, kind)
		require.Equal(t, tc.key, key)
	}

	for _, bad := range []NodeID{
		"zz:x",       // unknown prefix
		"nocolon",    // no separator at all
		"tooluse:",   // empty key
		"",           // empty id
		":x",         // empty prefix
		"invalid:x",  // the sentinel's name is not a prefix
		"tool_use:x", // the text name is not the prefix; the prefix is "tooluse"
	} {
		kind, key, ok := ParseNodeID(bad)
		require.False(t, ok, "%q must not parse", bad)
		require.Equal(t, KindInvalid, kind, "a rejected NodeID reports the sentinel, never a real kind")
		require.Empty(t, key)
	}
}
