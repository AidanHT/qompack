package dag

import "time"

// SliceOptions configures one BackwardSlice or ForwardSlice call (00-ARCHITECTURE.md §5.9).
type SliceOptions struct {
	// Thin drops EdgeControlOnly edges (00-ARCHITECTURE.md §6.4 thin slicing; default true).
	Thin bool
	// MaxDepth bounds the slice's hop depth; 0 means unbounded.
	MaxDepth int
	// MaxNodes hard-caps the slice's node count, setting Slice.Truncated when hit.
	MaxNodes int
	// Decay is the per-hop relevance score decay applied while walking the slice.
	Decay float32
	// Deadline bounds the slice's wall-clock budget.
	Deadline time.Duration
}

// Slice is one BackwardSlice or ForwardSlice result: every visited node's relevance score, a
// descending order to consume them in, and whether the walk was cut short.
type Slice struct {
	// Scores maps every visited NodeID to its relevance score — NOT a binary keep/drop decision
	// (00-ARCHITECTURE.md §8.3).
	Scores map[NodeID]float32
	// Order lists every visited NodeID in descending Scores order, stable-tiebroken by Turn.
	Order []NodeID
	// Truncated reports whether SliceOptions.MaxNodes, MaxDepth or Deadline cut the walk short.
	Truncated bool
	// Visited is the total number of nodes the walk visited.
	Visited int
}

// GraphStats summarizes a Graph's current size (00-ARCHITECTURE.md §14.0 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md: §5.9's Graph.Stats() references this type
// without defining it; SP-01 defines it here).
type GraphStats struct {
	// Nodes is the total node count.
	Nodes int
	// Edges is the total edge count.
	Edges int
	// ByKind counts nodes per NodeKind.
	ByKind map[NodeKind]int
	// Bytes is the on-disk size of dag/deps.jsonl.
	Bytes int64
}
