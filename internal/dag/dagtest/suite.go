// Package dagtest is the conformance suite for dag.Graph (00-ARCHITECTURE.md §5.22): every
// implementation SP-07 ships must pass RunGraphSuite. SP-01 ships the suite itself, including the
// behaviour assertions SP-07 inherits (Rule W-1) — only the guarded /behaviour block is skipped
// until a real Graph lands.
package dagtest

import (
	"context"
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// probeNode is a minimal, otherwise-unused Node the shape block and isStub probe with.
var probeNode = dag.Node{ID: "file:shape-probe.txt", Kind: dag.KindFile, Ref: "shape-probe.txt"}

// RunGraphSuite is the conformance suite for dag.Graph. name distinguishes multiple factories run
// in the same test binary; factory must return a fresh, ready-to-use Graph on every call.
func RunGraphSuite(t *testing.T, name string, factory func(t *testing.T) dag.Graph) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		g := factory(t)
		require.NotNil(t, g)

		requireKnownError(t, g.AddNode(probeNode))
		requireKnownError(t, g.AddEdge(dag.Edge{From: probeNode.ID, To: probeNode.ID, Kind: dag.EdgeSequence}))

		// Node, Out, In, NodesAfter and CrossingEdges have no error return; any value they
		// produce, including the documented zero value, is shape-valid.
		_, _ = g.Node(probeNode.ID)
		_ = g.Out(probeNode.ID)
		_ = g.In(probeNode.ID)
		_ = g.NodesAfter(0)
		_ = g.CrossingEdges(0)
		_ = g.Stats()

		_, err := g.BackwardSlice([]dag.NodeID{probeNode.ID}, dag.SliceOptions{})
		requireKnownError(t, err)
		_, err = g.ForwardSlice([]dag.NodeID{probeNode.ID}, dag.SliceOptions{})
		requireKnownError(t, err)

		ctx := context.Background()
		requireKnownError(t, g.Flush(ctx))
		requireKnownError(t, g.Compact(ctx))
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("slice_scores_descend", func(t *testing.T) { runSliceScoresDescendCase(t, factory) })
		t.Run("thin_drops_control_only_edges", func(t *testing.T) { runThinDropsControlOnlyCase(t, factory) })
		t.Run("crossing_edges_counts_exactly", func(t *testing.T) { runCrossingEdgesExactCase(t, factory) })
	})
}

// requireKnownError fails the test unless err is nil or wraps one of the four sentinels every
// stub and every real implementation is allowed to return from an operation
// (00-ARCHITECTURE.md §5.22; §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md).
func requireKnownError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotImplemented) ||
		errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded)
	require.True(t, known, "unexpected error: %v", err)
}

// isStub reports whether factory currently produces a stub Graph, using AddNode as the probe
// (plans/OWNERS.tsv: dag's probe method is AddNode).
func isStub(t *testing.T, factory func(t *testing.T) dag.Graph) bool {
	t.Helper()
	err := factory(t).AddNode(probeNode)
	return core.IsNotImplemented(err)
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Graph, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) dag.Graph) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
