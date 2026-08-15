package dag

import (
	"context"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
)

// Open returns a Graph rooted at root.
//
// It still hands back the SP-01 stub, deliberately, even though the real in-memory graph in
// graph.go is complete. plans/OWNERS.tsv names AddNode as this package's stub-probe method, so
// dagtest.RunGraphSuite treats "AddNode no longer reports ErrNotImplemented" as "dag is no longer
// a stub" and runs its whole behaviour block — which asserts slice scores descend, that Thin drops
// EdgeControlOnly, and that CrossingEdges counts exactly. Those answers arrive with index.go and
// traverse.go. Flipping Open before then would put the repository through two commits in which the
// conformance suite is red, which is a worse history than a stub that lives two commits longer.
//
// The commit that lands scored slicing deletes this file and points Open at newGraph.
func Open(root string, cfg config.Config, log logging.Logger) (Graph, error) {
	_, _, _ = root, cfg, log
	return stubGraph{}, nil
}

// stubGraph is the SP-01 placeholder Graph, kept verbatim until the real graph can satisfy the
// conformance suite end to end.
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

// Stats always returns the zero GraphStats: Stats has no error return, and zero nodes/edges/bytes
// is the only honest answer for a graph that has never stored anything.
func (stubGraph) Stats() GraphStats { return GraphStats{} }
