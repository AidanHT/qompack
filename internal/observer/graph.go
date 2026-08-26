package observer

import (
	"context"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/store"
)

// §8.1 item 4 — "Record tool_use → tool_result → assistant_turn → next_tool_use, plus shared-state
// edges keyed on file path and symbol name" — expressed ENTIRELY as one call to dag.BuildToolUse.
//
// This file translates a store.ToolUseRecord into a dag.ObservedTool. It decides no node id, no
// edge direction and no edge kind for the item-4 chain, and it assembles no NodeID from a string:
// internal/dag/nodeid.go is explicit that a component spelling a node differently from another
// does not fail loudly, it silently produces two disconnected halves of the same graph.
//
// Three consequences of going through the builder are worth naming, because each was a bug an
// earlier revision of this plan had:
//
//   - Thin slicing has something to drop. dag.turnLinkKind makes the assistant → tool_use link an
//     EdgeSequence when this call touches the same file or symbol as its predecessor and an
//     EdgeControlOnly when it shares nothing, and traverse.go's thin branch drops EdgeControlOnly
//     and nothing else. SP-08 is the sole production emitter of transcript edges, so a hand-rolled
//     emitter that always wrote EdgeSequence would make Thin a no-op on every real graph.
//   - No parallel-sibling cycle. Parallel tool calls share a turn index (decision 4), and the
//     builder emits the consumes edge only under PrevTurn < Turn. Carrying LastToolUseTurn is the
//     only thing this package has to do to feed that guard.
//   - Shared state is anchored on the FILE, not on the previous tool use. A tooluse → tooluse
//     shared-file edge is not part of the scheme and is never emitted.

// emitToolGraph records one observed tool call in the dependence DAG and enrols both of its nodes
// in the open segment.
//
// The context is taken but deliberately UNUSED, and is named _ so unparam says so out loud. Past
// step 1 the pipeline's bookkeeping is all-or-nothing: a mid-pipeline cancellation check here
// would leave the index entry written while advancePos and LastToolUseID went unadvanced, and
// steps 11-14 would then run on top of that half-advanced state. The top-of-method ctx check and
// the store's own ctx handling are what a cancellation acts on. The parameter stays in the
// signature — same arity, same types — because dag.Graph may one day take a ctx, and because it
// is the shape §5.21's plan pins.
func (o *observer) emitToolGraph(_ context.Context, st *sessionState, rec store.ToolUseRecord,
	body []byte, superseded []core.ToolUseID,
) {
	pos := o.advancePos(st, 0)                // the tool_use block's start position
	resultPos := o.advancePos(st, rec.Tokens) // the tool_result block's — decision 5

	sup := core.ToolUseID("")
	if len(superseded) > 0 {
		sup = superseded[0] // most recent first, per supersede.go
	}

	o.soft(stageDAG, dag.BuildToolUse(o.opt.Graph, dag.ObservedTool{
		ToolUseID:     rec.ID,
		PrevToolUseID: st.LastToolUseID,
		PrevTurn:      st.LastToolUseTurn,
		Supersedes:    sup,
		Turn:          rec.Turn,
		TS:            rec.TS,
		Pos:           pos,
		ResultPos:     resultPos,
		Tool:          rec.Tool,
		PathKey:       rec.Path,
		Writes:        rec.Tool == toolFileEdit || rec.Tool == toolFileWrite,
		Symbols:       o.symbolNames(rec, body),
		Root:          rec.Root,
		Tokens:        rec.Tokens,
		Ephemeral:     rec.Ephemeral,
	}))

	// BuildToolUse carries exactly one supersedes edge. Any FURTHER read this one superseded gets
	// its edge here, in the same D-1 direction the builder uses: superseded → superseding.
	if len(superseded) > 1 {
		for _, older := range superseded[1:] {
			o.soft(stageDAG, o.opt.Graph.AddEdge(dag.Edge{
				From: dag.ToolUseNode(older), To: dag.ToolUseNode(rec.ID),
				Kind: dag.EdgeSupersedes, Weight: edgeWeight, Turn: rec.Turn,
			}))
		}
	}

	o.enrol(st, dag.ToolUseNode(rec.ID))
	o.enrol(st, dag.ToolResultNode(rec.ID))
	st.LastToolUseID, st.LastToolUseTurn = rec.ID, rec.Turn
}

// edgeWeight is the weight every edge this package emits directly carries, matching the builders'
// own: they record STRUCTURE, and per-hop relative worth is expressed by EdgeKind.Multiplier
// (SP-07 D-3) rather than twice.
const edgeWeight float32 = 1

// symbolNames resolves the symbol names a result touched, for dag.ObservedTool.Symbols.
//
// It returns them UNSORTED: BuildToolUse sorts and deduplicates, precisely so dag/deps.jsonl's
// edge order does not depend on how the extractor happened to walk the file.
func (o *observer) symbolNames(rec store.ToolUseRecord, body []byte) []string {
	if o.opt.Symbols == nil || rec.Path == "" ||
		supersedableClass(rec.Tool) != classFileContent || len(body) > symbolScanCap {
		return nil
	}

	names := o.opt.Symbols.Names(rec.Path, body)
	if len(names) == 0 {
		return nil
	}

	out := make([]string, 0, min(len(names), maxSymbolsPerResult))
	seen := make(map[string]bool, len(out))
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
		if len(out) == maxSymbolsPerResult {
			break
		}
	}
	return out
}

// enrol records one segment-membership edge, member → segment.
//
// The direction is dag.BuildSegment's: members are the earlier end under D-1. Emitting these
// incrementally rather than in BuildSegment's Members list is legal by D-6 — an edge may precede
// its endpoints — and is what keeps this package from holding a whole segment's node list in
// memory.
func (o *observer) enrol(st *sessionState, n dag.NodeID) {
	if st.Segment == 0 {
		return
	}
	o.soft(stageDAG, o.opt.Graph.AddEdge(dag.Edge{
		From: n, To: dag.SegmentNode(st.Segment),
		Kind: dag.EdgeSequence, Weight: edgeWeight, Turn: st.Turn,
	}))
}

// advancePos returns the PRE-increment prefix position and advances it by tok (decision 5): every
// node's Pos is its own START position.
func (o *observer) advancePos(st *sessionState, tok core.Tokens) int {
	pos := st.PrefixTokens
	st.PrefixTokens += int(tok)
	return pos
}
