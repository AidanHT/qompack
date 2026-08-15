package dagtest_test

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/dag/dagtest"
	"github.com/qompack/qompack/internal/logging"
)

// fakeStubGraph mirrors the shape of an SP-01-style stub Graph: every operation reports the
// documented zero value or core.ErrNotImplemented, exactly like dag.Open's own stub does today.
// It exists only to exercise RunGraphSuite before SP-07 ships a real Graph.
type fakeStubGraph struct{}

func (fakeStubGraph) AddNode(n dag.Node) error            { return core.ErrNotImplemented }
func (fakeStubGraph) AddEdge(e dag.Edge) error            { return core.ErrNotImplemented }
func (fakeStubGraph) Node(id dag.NodeID) (dag.Node, bool) { return dag.Node{}, false }
func (fakeStubGraph) Out(id dag.NodeID) []dag.Edge        { return nil }
func (fakeStubGraph) In(id dag.NodeID) []dag.Edge         { return nil }

func (fakeStubGraph) BackwardSlice(criteria []dag.NodeID, o dag.SliceOptions) (dag.Slice, error) {
	return dag.Slice{}, core.ErrNotImplemented
}

func (fakeStubGraph) ForwardSlice(criteria []dag.NodeID, o dag.SliceOptions) (dag.Slice, error) {
	return dag.Slice{}, core.ErrNotImplemented
}

func (fakeStubGraph) CrossingEdges(pos int) int         { return 0 }
func (fakeStubGraph) NodesAfter(pos int) []dag.Node     { return nil }
func (fakeStubGraph) Flush(ctx context.Context) error   { return core.ErrNotImplemented }
func (fakeStubGraph) Compact(ctx context.Context) error { return core.ErrNotImplemented }
func (fakeStubGraph) Stats() dag.GraphStats             { return dag.GraphStats{} }

// TestRunGraphSuite_StubIsSkipped proves the suite's shape block passes against a stub Graph and
// that its behaviour block is skipped with the exact Rule W-1 message. SP-07 reuses RunGraphSuite
// unchanged, pointed at its real implementation, to flip that skip off.
func TestRunGraphSuite_StubIsSkipped(t *testing.T) {
	dagtest.RunGraphSuite(t, "fake-stub", func(t *testing.T) dag.Graph {
		return fakeStubGraph{}
	})
}

// TestRunGraphSuite_AgainstQompackStub exercises RunGraphSuite against the real dag.Open stub,
// end to end, so a change to its stub behaviour that breaks the conformance suite is caught here
// rather than only once SP-07 lands.
func TestRunGraphSuite_AgainstQompackStub(t *testing.T) {
	dagtest.RunGraphSuite(t, "dag.Open-stub", func(t *testing.T) dag.Graph {
		g, err := dag.Open(t.TempDir(), config.Config{}, logging.Nop())
		if err != nil {
			t.Fatal(err)
		}
		return g
	})
}
