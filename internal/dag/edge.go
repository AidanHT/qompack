package dag

import "github.com/qompack/qompack/internal/core"

// EdgeKind classifies one dag.Edge. Values start at 0 and are frozen in this exact order by
// testdata/golden/contracts/dag/want/edge_line.jsonl (Rule W-2): that fixture's edge is an
// EdgeConsumes, and its "kind" field is the literal integer 2, which is only correct if
// EdgeConsumes is this const block's third value (0-indexed). Do not reorder these.
type EdgeKind uint8

// The eight edge kinds, in the exact order 00-ARCHITECTURE.md §5.9 lists them.
const (
	// EdgeSequence connects consecutive turns in temporal order.
	EdgeSequence EdgeKind = iota
	// EdgeProduces connects a tool use to a file or symbol it wrote.
	EdgeProduces
	// EdgeConsumes connects a tool use to a file or symbol it read.
	EdgeConsumes
	// EdgeSharedFile connects two nodes that touched the same file.
	EdgeSharedFile
	// EdgeSharedSymbol connects two nodes that touched the same symbol.
	EdgeSharedSymbol
	// EdgeSupersedes connects a decision or elimination to the one it replaces.
	EdgeSupersedes
	// EdgeExplains connects a decision to the evidence that justifies it.
	EdgeExplains
	// EdgeControlOnly connects nodes only for control flow, not data dependence; Thin slicing
	// drops these first (00-ARCHITECTURE.md §6.4, §5.9's SliceOptions.Thin).
	EdgeControlOnly
	// EdgeInvalid is the "not a real kind" sentinel. It is deliberately LAST, not first: the
	// frozen fixture testdata/golden/contracts/dag/want/edge_line.jsonl pins "kind":2 to
	// EdgeConsumes (Rule W-2), so prepending a sentinel would renumber every kind and break it.
	// The consequence is that EdgeKind's zero value is EdgeSequence rather than "unset" — which
	// is legal, and is what the existing tests in internal/analyzer and internal/dag/dagtest
	// already rely on when they build an edge without naming a kind.
	EdgeInvalid
)

// Edge is one directed, typed connection between two Nodes (00-ARCHITECTURE.md §5.9).
//
// The json tags are explicit and frozen: this is the shape
// testdata/golden/contracts/dag/want/edge_line.jsonl pins byte-for-byte (Rule W-2), field for
// field, in this exact order. Edge itself carries no "type" field — see MarshalJSON for why.
type Edge struct {
	From NodeID   `json:"from"`
	To   NodeID   `json:"to"`
	Kind EdgeKind `json:"kind"`
	// Weight is this edge's relevance weight, used by slicing's per-hop decay.
	Weight float32 `json:"weight"`
	// Turn is the turn index this edge arose at.
	Turn core.TurnIndex `json:"turn"`
}

// edgeLineType is the "type" discriminator dag/deps.jsonl uses to tell an edge line from a node
// line sharing the same append-only log.
const edgeLineType = "edge"

// edgeAlias has Edge's exact fields and json tags but none of its methods, so MarshalJSON can
// embed it without recursing back into itself.
type edgeAlias Edge

// MarshalJSON renders e as one dag/deps.jsonl edge line: the "type":"edge" discriminator followed
// by every field of Edge, in the order testdata/golden/contracts/dag/want/edge_line.jsonl freezes
// (from, to, kind, weight, turn). See Node.MarshalJSON's comment for why the discriminator is
// produced here rather than stored as an Edge field, and why this goes through marshalLine's
// escape-free encoder rather than json.Marshal: From and To carry NodeIDs whose keys are file
// paths, and an escaped path is a path no `grep` of the log can find.
func (e Edge) MarshalJSON() ([]byte, error) {
	return marshalLine(struct {
		Type string `json:"type"`
		edgeAlias
	}{Type: edgeLineType, edgeAlias: edgeAlias(e)})
}
