package dag

import (
	"encoding/json"

	"github.com/qompack/qompack/internal/core"
)

// NodeKind classifies one dag.Node. Values start at 0 and are frozen in this exact order by
// testdata/golden/contracts/dag/want/node_line.jsonl (Rule W-2): that fixture's node is a
// KindFile, and its "kind" field is the literal integer 4, which is only correct if KindFile is
// this const block's fifth value (0-indexed). Do not reorder these.
type NodeKind uint8

// The nine node kinds, in the exact order 00-ARCHITECTURE.md §5.9 lists them.
const (
	// KindToolUse is a tool invocation.
	KindToolUse NodeKind = iota
	// KindToolResult is a tool invocation's result.
	KindToolResult
	// KindAssistant is an assistant turn.
	KindAssistant
	// KindUserPrompt is a user turn.
	KindUserPrompt
	// KindFile is a file touched during the session.
	KindFile
	// KindSymbol is a source symbol (00-ARCHITECTURE.md §5.22b).
	KindSymbol
	// KindDecision is a recorded decision (00-ARCHITECTURE.md §5.14).
	KindDecision
	// KindElimination is a recorded negative-knowledge elimination (00-ARCHITECTURE.md §5.10).
	KindElimination
	// KindSegment is a closed session segment.
	KindSegment
	// KindInvalid is the "not a real kind" sentinel. It is deliberately LAST, not first: the
	// frozen fixture testdata/golden/contracts/dag/want/node_line.jsonl pins "kind":4 to
	// KindFile (Rule W-2), so prepending a sentinel would renumber every kind and break it.
	// The consequence is that NodeKind's zero value is KindToolUse, not "unset" — so AddNode
	// validates a node's kind against its NodeID prefix (which is unambiguous) rather than
	// against the zero value.
	KindInvalid
)

// NodeID identifies one Node: "<kind>:<stable-key>", for example "file:src/auth.ts" or
// "tooluse:toolu_01A2B3C4D5E6F7G8H9J0K1L2".
type NodeID string

// Node is one vertex of the dependence DAG (00-ARCHITECTURE.md §5.9).
//
// The json tags are explicit and frozen: this is the shape
// testdata/golden/contracts/dag/want/node_line.jsonl pins byte-for-byte (Rule W-2), field for
// field, in this exact order. Node itself carries no "type" field — see MarshalJSON for why — so
// the tag on ID is what fixes json's default field-name-based key ("ID") to the fixture's "id".
type Node struct {
	ID   NodeID   `json:"id"`
	Kind NodeKind `json:"kind"`
	// Turn is the turn index this node arose at.
	Turn core.TurnIndex `json:"turn"`
	// TS is when this node arose.
	TS core.UnixMilli `json:"ts"`
	// Pos is this node's token position in the prefix — required for p-selection
	// (00-ARCHITECTURE.md §5.3).
	Pos int `json:"pos"`
	// Ref is this node's referent: a path, symbol name, tool_use_id, decision id, …
	Ref string `json:"ref"`
	// Root is the content root hash this node is associated with, if any.
	Root core.Hash `json:"root"`
	// Tokens is this node's estimated token cost.
	Tokens core.Tokens `json:"tokens"`
	// Ephemeral marks a node as a first-eviction-candidate retrieval result
	// (00-ARCHITECTURE.md §8.7).
	Ephemeral bool `json:"ephemeral"`
}

// nodeLineType is the "type" discriminator dag/deps.jsonl uses to tell a node line from an edge
// line sharing the same append-only log.
const nodeLineType = "node"

// nodeAlias has Node's exact fields and json tags but none of its methods, so MarshalJSON can
// embed it without recursing back into itself.
type nodeAlias Node

// MarshalJSON renders n as one dag/deps.jsonl node line: the "type":"node" discriminator followed
// by every field of Node, in the order testdata/golden/contracts/dag/want/node_line.jsonl freezes
// (id, kind, turn, ts, pos, ref, root, tokens, ephemeral). Node carries no Type field of its own —
// adding one would put a redundant, always-"node" value into every in-memory Node the real Graph
// implementation manipulates — so this is the one place the discriminator is produced, exactly
// the way core.Hash's own MarshalJSON produces a derived wire form its Go struct does not store
// directly.
func (n Node) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		nodeAlias
	}{Type: nodeLineType, nodeAlias: nodeAlias(n)})
}
