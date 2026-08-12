package dag

import (
	"context"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
)

// Graph is the dependence DAG (00-ARCHITECTURE.md §5.9): the append-only record of nodes and
// edges backing both compaction selection (backward slicing, p-selection via Node.Pos) and
// retrieval (forward slicing, shared-file/shared-symbol expansion).
type Graph interface {
	AddNode(n Node) error
	AddEdge(e Edge) error
	// Node looks up id, reporting false if it is not present.
	Node(id NodeID) (Node, bool)
	// Out returns every edge leaving id.
	Out(id NodeID) []Edge
	// In returns every edge entering id.
	In(id NodeID) []Edge
	// BackwardSlice walks backward from criteria, scoring and ordering the visited nodes.
	BackwardSlice(criteria []NodeID, o SliceOptions) (Slice, error)
	// ForwardSlice walks forward from criteria, scoring and ordering the visited nodes.
	ForwardSlice(criteria []NodeID, o SliceOptions) (Slice, error)
	// CrossingEdges is segment_coupling(pos): the number of edges whose endpoints straddle token
	// position pos (00-ARCHITECTURE.md §5.9).
	CrossingEdges(pos int) int
	// NodesAfter returns every node whose Pos exceeds pos.
	NodesAfter(pos int) []Node
	// Flush appends any buffered nodes/edges to dag/deps.jsonl.
	Flush(ctx context.Context) error
	// Compact rewrites the on-disk log, dropping GC'd nodes (idle only).
	Compact(ctx context.Context) error
	// Stats summarizes the graph's current size.
	Stats() GraphStats
}

// Open returns a Graph rooted at root. Constructing always succeeds, so wave-0 composition roots
// can wire a dag.Graph today, but every operation is a stub until SP-07 lands the real graph
// (00-ARCHITECTURE.md §5.9): AddNode, AddEdge, BackwardSlice, ForwardSlice, Flush and Compact
// report core.ErrNotImplemented; Node, Out, In, NodesAfter, CrossingEdges and Stats — which have
// no error return — report the documented zero value.
func Open(root string, cfg config.Config, log logging.Logger) (Graph, error) {
	return stubGraph{}, nil
}

// stubGraph is the SP-01 placeholder Graph. SP-07 owns the real implementation.
type stubGraph struct{}

// AddNode always reports core.ErrNotImplemented.
func (stubGraph) AddNode(n Node) error { return core.ErrNotImplemented }

// AddEdge always reports core.ErrNotImplemented.
func (stubGraph) AddEdge(e Edge) error { return core.ErrNotImplemented }

// Node always reports (Node{}, false): Node has no error return, and false — "id is not
// present" — is the only honest answer for a graph that has never stored anything.
func (stubGraph) Node(id NodeID) (Node, bool) { return Node{}, false }

// Out always returns nil: Out has no error return, so nil — Rule 1's documented zero value — is
// the only honest answer: no edges have ever been recorded.
func (stubGraph) Out(id NodeID) []Edge { return nil }

// In always returns nil, for the same reason as Out.
func (stubGraph) In(id NodeID) []Edge { return nil }

// BackwardSlice always reports core.ErrNotImplemented.
func (stubGraph) BackwardSlice(criteria []NodeID, o SliceOptions) (Slice, error) {
	return Slice{}, core.ErrNotImplemented
}

// ForwardSlice always reports core.ErrNotImplemented.
func (stubGraph) ForwardSlice(criteria []NodeID, o SliceOptions) (Slice, error) {
	return Slice{}, core.ErrNotImplemented
}

// CrossingEdges always returns 0. CrossingEdges has no error return, so 0 — Rule 1's documented
// zero value — is the only honest answer: with no edges recorded, none can possibly straddle pos.
func (stubGraph) CrossingEdges(pos int) int { return 0 }

// NodesAfter always returns nil, for the same reason as Out.
func (stubGraph) NodesAfter(pos int) []Node { return nil }

// Flush always reports core.ErrNotImplemented.
func (stubGraph) Flush(ctx context.Context) error { return core.ErrNotImplemented }

// Compact always reports core.ErrNotImplemented.
func (stubGraph) Compact(ctx context.Context) error { return core.ErrNotImplemented }

// Stats always returns the zero GraphStats: Stats has no error return, and zero
// nodes/edges/bytes is the only honest answer for a graph that has never stored anything — not a
// zeroed-but-plausible-looking GraphStats{}, an actual accounting of nothing.
func (stubGraph) Stats() GraphStats { return GraphStats{} }
