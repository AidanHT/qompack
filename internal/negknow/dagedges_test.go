package negknow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

// nodeIDGolden is testdata/golden/contracts/negknow/node-ids.json: the two NodeID spellings this
// package mints, pinned so a divergence from SP-07/SP-08's naming is caught at the wave-2
// verification checkpoint rather than at runtime — where it would not fail at all, but silently
// fork the graph into two disconnected halves.
type nodeIDGolden struct {
	Elimination string `json:"elimination"`
	File        string `json:"file"`
}

func TestNodeIDGolden(t *testing.T) {
	var want nodeIDGolden
	require.NoError(t, json.Unmarshal(readContractFile(t, "node-ids.json"), &want))

	// The frozen contract fixture's record id, and the file the same fixture's descriptor names.
	require.Equal(t, want.Elimination, string(NodeIDFor(Record{ID: "elim_3f9b2c7d1a48"})),
		"the long-form prefix internal/dag/nodeid.go fixes")
	require.Equal(t, want.File, string(FileNodeID("src/auth.ts")))

	// The case where a hand-built "file:"+key and the constructor's output actually diverge:
	// dag's sanitizeKey rewrites a key past 384 bytes into a head plus a digest of the whole,
	// which a concatenation would skip.
	long := "src/" + strings.Repeat("deeply-nested-directory/", 20) + "auth.ts"
	require.Greater(t, len(paths.Key(long)), 384)
	require.Equal(t, dag.FileNode(paths.Key(long)), FileNodeID(long),
		"FileNodeID goes through the constructor, never through concatenation")
	require.NotEqual(t, dag.NodeID("file:"+paths.Key(long)), FileNodeID(long),
		"and that matters: the two spellings differ for an over-length key")

	// End to end: recording an elimination on a ledger holding a real graph puts a
	// KindElimination node into it under exactly that id.
	root, cfg := newProject(t)
	g, err := dag.Open(root, cfg, logging.Nop())
	require.NoError(t, err)

	deps := testDeps("sess", newMetrics())
	deps.Graph = g
	l := openLedger(t, root, cfg, nil, deps)

	id := mustRecord(t, l, newRecord("dag", "src/auth.ts:refreshToken", "widen pool timeout", "pgbouncer ignores it"))
	rec, err := l.Get(context.Background(), id)
	require.NoError(t, err)

	n, ok := g.Node(NodeIDFor(rec))
	require.True(t, ok, "Record emits the elimination node")
	require.Equal(t, dag.KindElimination, n.Kind)
	require.Equal(t, rec.TS, n.TS)
	// SP-07's builder sets Ref to the state the elimination is ABOUT, not to the record id — the
	// id is already the second half of the NodeID.
	require.Equal(t, rec.Desc.NormalizedPath, n.Ref)

	// SP-07's builder owns the edge semantics: the file and symbol anchors point INTO the
	// elimination, because the elimination is the conclusion.
	in := g.In(NodeIDFor(rec))
	kinds := map[dag.EdgeKind]bool{}
	for _, e := range in {
		kinds[e.Kind] = true
	}
	require.True(t, kinds[dag.EdgeSharedFile], "file -> elimination")
	require.True(t, kinds[dag.EdgeSharedSymbol], "symbol -> elimination")
}

// errGraph is a dag.Graph whose two mutating methods always fail. It exists for one assertion:
// an elimination that reached the append-only log is REAL whether or not the graph accepted a node
// for it, so failing Record over a graph write would trade the durable half of the feature for the
// derived one.
type errGraph struct{ err error }

var _ dag.Graph = errGraph{}

func (g errGraph) AddNode(dag.Node) error { return g.err }
func (g errGraph) AddEdge(dag.Edge) error { return g.err }

func (errGraph) Node(dag.NodeID) (dag.Node, bool) { return dag.Node{}, false }
func (errGraph) Out(dag.NodeID) []dag.Edge        { return nil }
func (errGraph) In(dag.NodeID) []dag.Edge         { return nil }

func (g errGraph) BackwardSlice([]dag.NodeID, dag.SliceOptions) (dag.Slice, error) {
	return dag.Slice{}, g.err
}

func (g errGraph) ForwardSlice([]dag.NodeID, dag.SliceOptions) (dag.Slice, error) {
	return dag.Slice{}, g.err
}

func (errGraph) CrossingEdges(int) int           { return 0 }
func (errGraph) NodesAfter(int) []dag.Node       { return nil }
func (g errGraph) Flush(context.Context) error   { return g.err }
func (g errGraph) Compact(context.Context) error { return g.err }
func (errGraph) Stats() dag.GraphStats           { return dag.GraphStats{} }

func TestEmitDAG_GraphErrorsAreSwallowed(t *testing.T) {
	root, cfg := newProject(t)
	deps := testDeps("sess", newMetrics())
	deps.Graph = errGraph{err: errors.New("the graph is on fire")}
	l := openLedger(t, root, cfg, nil, deps)

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
	)
	id, err := l.Record(context.Background(), newRecord("errgraph", target, approach, "pgbouncer ignores it"))
	require.NoError(t, err, "DAG emission is best effort; it may never fail a Record")
	require.NotEmpty(t, id)

	// The record is fully live: appended, indexed and answerable.
	require.Equal(t, 1, l.Health().Records)
	require.Equal(t, 1, logLineCount(t, root))
	a := mustQuery(t, l, target, approach, ScopeSession)
	require.Equal(t, AnswerActive, a.State)
	require.Equal(t, id, a.Record.ID)
}

func TestEmitDAG_NilGraphIsANoOp(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))
	require.Nil(t, l.deps.Graph)

	// Recording with no graph must not fail and must not panic: DAG emission is best effort.
	id := mustRecord(t, l, newRecord("nograph", "src/a.ts", "widen pool timeout", "why"))
	require.NotEmpty(t, id)
	require.Equal(t, 1, l.Health().Records)
}
