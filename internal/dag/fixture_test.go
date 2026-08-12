package dag_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/stretchr/testify/require"
)

// goldenDAGDir is testdata/golden/contracts/dag/want/, relative to this package's own directory
// (internal/dag). These fixtures are frozen (Rule W-2): this test may never edit them, only
// prove dag.Node and dag.Edge reproduce them.
const goldenDAGDir = "../../testdata/golden/contracts/dag/want"

// readGolden reads one frozen fixture file, failing the test with a clear message if it is
// missing (rather than a bare os.ReadFile error), since a missing frozen fixture is a repository
// problem this test should surface loudly.
func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenDAGDir, name))
	require.NoError(t, err, "frozen contract fixture missing: %s", name)
	require.NotEmpty(t, b)
	return b
}

// TestNode_RoundTripsFrozenFixture asserts dag.Node reproduces
// testdata/golden/contracts/dag/want/node_line.jsonl exactly (Rule W-2): unmarshal the frozen
// line, marshal it back, and compare the two JSON documents for semantic equality.
// require.JSONEq, not a raw byte comparison, is used deliberately: Go's encoding/json renders a
// whole-number float32 (Edge.Weight in the sibling edge fixture, and any node fixture
// hypothetically carrying one) without a decimal point (json.Marshal(float32(1)) produces "1", not
// "1.0"), so a byte-exact comparison would fail on formatting alone even when the decoded value is
// unchanged. JSONEq parses both sides and compares values, which is what "round-trips this fixture"
// actually means here.
func TestNode_RoundTripsFrozenFixture(t *testing.T) {
	golden := readGolden(t, "node_line.jsonl")

	var n dag.Node
	require.NoError(t, json.Unmarshal(golden, &n))

	// Pin the decoded fields against the fixture's own literal values, so a future accidental
	// change to Node's field order or json tags is caught here, not just by the round-trip below.
	require.Equal(t, dag.NodeID("file:src/auth.ts"), n.ID)
	require.Equal(t, dag.KindFile, n.Kind)
	require.Equal(t, core.TurnIndex(61), n.Turn)
	require.Equal(t, core.UnixMilli(1767225480000), n.TS)
	require.Equal(t, 148230, n.Pos)
	require.Equal(t, "src/auth.ts", n.Ref)
	require.Equal(t, "sha256:0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c", n.Root.String())
	require.Equal(t, core.Tokens(683), n.Tokens)
	require.False(t, n.Ephemeral)

	remarshaled, err := json.Marshal(n)
	require.NoError(t, err)
	require.JSONEq(t, string(golden), string(remarshaled))

	// A second structural round trip (decode what we just encoded) confirms MarshalJSON and the
	// default field-tag-driven Unmarshal agree with each other, not only with the fixture.
	var n2 dag.Node
	require.NoError(t, json.Unmarshal(remarshaled, &n2))
	require.Equal(t, n, n2)
}

// TestEdge_RoundTripsFrozenFixture is TestNode_RoundTripsFrozenFixture's sibling for
// testdata/golden/contracts/dag/want/edge_line.jsonl.
func TestEdge_RoundTripsFrozenFixture(t *testing.T) {
	golden := readGolden(t, "edge_line.jsonl")

	var e dag.Edge
	require.NoError(t, json.Unmarshal(golden, &e))

	require.Equal(t, dag.NodeID("tooluse:toolu_01A2B3C4D5E6F7G8H9J0K1L2"), e.From)
	require.Equal(t, dag.NodeID("file:src/auth.ts"), e.To)
	require.Equal(t, dag.EdgeConsumes, e.Kind)
	require.InDelta(t, float32(1.0), e.Weight, 0)
	require.Equal(t, core.TurnIndex(61), e.Turn)

	remarshaled, err := json.Marshal(e)
	require.NoError(t, err)
	require.JSONEq(t, string(golden), string(remarshaled))

	var e2 dag.Edge
	require.NoError(t, json.Unmarshal(remarshaled, &e2))
	require.Equal(t, e, e2)
}

// TestNodeAndEdge_TypeDiscriminatorDistinguishesLines asserts the "type" field is what a reader
// of dag/deps.jsonl uses to tell a node line from an edge line sharing the same append-only log
// (00-ARCHITECTURE.md §5.9), by decoding both fixtures generically and checking "type" differs.
func TestNodeAndEdge_TypeDiscriminatorDistinguishesLines(t *testing.T) {
	var nodeGeneric, edgeGeneric map[string]any
	require.NoError(t, json.Unmarshal(readGolden(t, "node_line.jsonl"), &nodeGeneric))
	require.NoError(t, json.Unmarshal(readGolden(t, "edge_line.jsonl"), &edgeGeneric))

	require.Equal(t, "node", nodeGeneric["type"])
	require.Equal(t, "edge", edgeGeneric["type"])
}
