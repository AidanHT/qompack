package analyzer

import (
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
)

// Block is one selectable unit of the prefix: a tool result, an assistant turn, a file read — a
// dependence-DAG node priced in tokens and located at a token position (00-ARCHITECTURE.md
// §5.12). Pos is what makes p-selection expressible: a Block whose Pos precedes the compaction
// point p may never be selected (§13 invariant 4), and NewSelector enforces that structurally.
type Block struct {
	// ID is the dependence-DAG node this block corresponds to.
	ID dag.NodeID
	// Pos is this block's token position in the prefix.
	Pos int
	// Tokens is this block's token cost.
	Tokens core.Tokens
	// Kind classifies the block, mirroring its DAG node's kind.
	Kind dag.NodeKind
	// Root is the content root hash this block's bytes are stored under, if any.
	Root core.Hash
	// Ephemeral marks a retrieval result, born ephemeral: the first eviction candidate
	// (00-ARCHITECTURE.md §8.7).
	Ephemeral bool
	// Superseded marks a block a later tool use on the same path has replaced.
	Superseded bool
}

// DeltaMode names the cost tier of a Δ-scoring implementation. Its values are exactly the
// config.selection.deltaScoring enum (00-ARCHITECTURE.md §5.12, Appendix C).
type DeltaMode string

const (
	// DeltaCheap is token-overlap plus symbol-reference counting: the only tier SP-01 declares a
	// constructor for.
	DeltaCheap DeltaMode = "cheap"
	// DeltaMedium is the intermediate tier.
	DeltaMedium DeltaMode = "medium"
	// DeltaExpensive is the highest-fidelity, highest-cost tier.
	DeltaExpensive DeltaMode = "expensive"
)

// Continuation is the OBSERVED continuation a Δ-score is measured against: what the session
// actually did after the block in question, not what a model predicts it might do
// (00-ARCHITECTURE.md §5.12). Scoring against the observed continuation is what makes the metric
// replayable.
type Continuation struct {
	// Text is the continuation's raw text.
	Text []byte
	// Symbols lists the symbol names the continuation referenced.
	Symbols []string
	// Paths lists the file paths the continuation touched.
	Paths []string
	// FromTurn is the turn the continuation starts at.
	FromTurn core.TurnIndex
}

// RedundancyReport is what DetectRedundancy found: results a later tool use replaced, and results
// that are near-duplicates of one another (00-ARCHITECTURE.md §5.12, §8.1).
type RedundancyReport struct {
	// Superseded lists tool uses a later tool use on the same path replaced.
	Superseded []core.ToolUseID
	// NearDups maps a tool use to the other tool uses whose content is within the configured
	// MinHash Jaccard threshold of it.
	NearDups map[core.ToolUseID][]core.ToolUseID
}

// Selection is one Selector.Select result (00-ARCHITECTURE.md §5.12).
type Selection struct {
	// Keep lists the nodes that survive, all of them at or after p.
	Keep []dag.NodeID
	// Tokens is the total token cost of Keep, which never exceeds the budget passed to Select.
	Tokens core.Tokens
	// Value is the submodular objective's value at Keep: coverage(S) - lambda*redundancy(S).
	Value float64
	// Dropped lists the candidate nodes that did not survive.
	Dropped []dag.NodeID
	// Iters counts lazy-greedy marginal-gain evaluations, which is what makes the (1-1/e)
	// approximation-bound sanity assertion checkable.
	Iters int
}
