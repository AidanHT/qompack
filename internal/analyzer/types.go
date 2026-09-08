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

// ----------------------------------------------------------------------------------------------
// SP-15 representation selection. Everything above this line is SP-01's frozen §5.12 type set —
// analyzertest, test/guards and test/integration pin it — and SP-15 adds alongside it rather than
// changing it. The frozen contract these declarations implement is plans/sdd/V5-SP-15/contract.md
// §2.
// ----------------------------------------------------------------------------------------------

// RepresentationKind names one way to deliver an item's content. An item may have several
// compatible representations and the selector picks AT MOST ONE of them (contract §2): delivering
// both the exact span and the capsule of one item would double-count its coverage and pay twice
// for it.
//
// The order runs most faithful to least, and that is deliberate rather than cosmetic: it is the
// deterministic tie-break's secondary key (contract §3), so a tie between two representations of
// one item resolves toward the more faithful one.
type RepresentationKind uint8

const (
	// RepExactSpan is the item's verbatim bytes.
	RepExactSpan RepresentationKind = iota
	// RepCapsule is a structured summary capsule: smaller than the span, lossy by construction,
	// and carrying its own provenance back to the original evidence.
	RepCapsule
	// RepPointer is a durable handle only. The content is not delivered; it stays fetchable
	// through the retrieval layer, which is the whole of §4.4's "pointers, never contents".
	RepPointer
	// RepArchiveOnly delivers nothing at all. It is not a drop: the item remains recoverable from
	// the archive, and a Proposal that uses it says so in Archive rather than reporting success
	// over a record that silently went missing.
	RepArchiveOnly
)

// String renders k for logs, drop reports and test failure messages.
func (k RepresentationKind) String() string {
	switch k {
	case RepExactSpan:
		return "exact_span"
	case RepCapsule:
		return "capsule"
	case RepPointer:
		return "pointer"
	case RepArchiveOnly:
		return "archive_only"
	default:
		return "unknown"
	}
}

// Qualification is how much authority a piece of evidence still carries (contract §2). It is the
// analyzer-side, negknow-free spelling of SP-20's applicability: §3.2's allow-set forbids analyzer
// from importing negknow, so the daemon maps a negknow.Record's status onto this enum and the
// selector reasons about the neutral shape.
//
// The distinction is load-bearing for G6.3. A stale or uncertain elimination must keep travelling
// with its qualification and must never become an active constraint — a false "already tried" that
// blocks a now-viable approach inverts negative knowledge from asset to liability, which §12 rates
// High — so the type makes the difference impossible to lose in transit.
type Qualification uint8

// THE ORDER IS DELIBERATE AND THE ZERO VALUE IS THE POINT. QualUncertain is first so that an
// unset Qualification — a struct literal that forgot the field, a map miss read without comma-ok,
// a decoder that skipped it — is NON-BINDING. The obvious ordering, current-stale-uncertain, makes
// the zero value QualCurrent and therefore makes every one of those slips silently mint an
// authoritative constraint, which is precisely the false already_tried that inverts negative
// knowledge from asset to liability. This is not hypothetical: the first draft of block.go read a
// qualification out of a map with a plain index and turned every absent item into QualCurrent.
// Callers should still be explicit, but the type no longer punishes them for not being.
const (
	// QualUncertain marks evidence whose observation coverage is incomplete, so neither current
	// nor stale can be established. It is the ZERO VALUE, so unqualified evidence never binds.
	QualUncertain Qualification = iota
	// QualCurrent marks evidence that is authoritative and applicable right now.
	QualCurrent
	// QualStale marks evidence whose recorded dependency has changed since capture.
	QualStale
)

// Active reports whether evidence with this qualification may act as a binding constraint. Only
// QualCurrent may: stale and uncertain records are still carried, rendered and recoverable, but
// they constrain nothing.
func (q Qualification) Active() bool { return q == QualCurrent }

// String renders q for logs and drop reports.
func (q Qualification) String() string {
	switch q {
	case QualCurrent:
		return "current"
	case QualStale:
		return "stale"
	case QualUncertain:
		return "uncertain"
	default:
		return "unknown"
	}
}

// Provenance is where a Representation's content came from and how far from the original evidence
// it has travelled (contract §2).
//
// Root always names the ORIGINAL evidence, never the derivative's own bytes. That is what keeps
// §2's "keep original evidence and derivative provenance" property: a capsule of a capsule is
// allowed — DPI non-increase under its relevant Markov model is not strict loss on every pass, and
// retrieval changes what information is available — but the chain back to the first observation
// has to survive it, so a consumer can always recover what was actually seen.
type Provenance struct {
	// Origin is the observation node this representation derives from.
	Origin dag.NodeID
	// Root is the content root of the ORIGINAL evidence. A derivative never overwrites it.
	Root core.Hash
	// Derived reports that this representation is itself a summary of a summary.
	Derived bool
	// Qualification is how much authority the evidence still carries.
	Qualification Qualification
	// Turn is the turn the original observation was made at.
	Turn core.TurnIndex
}

// Representation is one candidate way to deliver one item (contract §2).
//
// AssembledCost is the whole cost of delivering it — wrapper, handle and drop-report overhead
// included — not the naked content length. That is the difference between a selector that fits its
// budget and one that overruns it at serialization time: per-chunk sums do not establish assembled
// tokens, so the field the objective spends is the assembled one, priced by the same estimator the
// consumer will serialize through.
type Representation struct {
	// Item is the item this represents.
	Item dag.NodeID
	// Kind is how this representation delivers the item.
	Kind RepresentationKind
	// Coverage is this representation's saturating contribution to its item, in [0,1]. An exact
	// span typically covers 1; a capsule or a pointer covers less.
	Coverage float64
	// AssembledCost is the delivered token cost INCLUDING all serialized overhead.
	AssembledCost core.Tokens
	// Requires is this representation's transitively closed dependency set, sorted ascending. A
	// representation is feasible only when every entry is itself delivered.
	Requires []dag.NodeID
	// Prov is the evidence chain behind this representation.
	Prov Provenance
}

// Candidate is one item together with every compatible representation of it (contract §2).
type Candidate struct {
	// Item is the item.
	Item dag.NodeID
	// Pos is the item's token position in the prefix. §13 invariant 4 still applies: a candidate
	// whose Pos precedes p may never be selected.
	Pos int
	// Weight is this item's nonnegative weight in the coverage objective.
	Weight float64
	// Mandatory marks an item the consumer must carry. When no representation of a mandatory
	// candidate fits, the result is an explicit overflow with an archive-recovery path — never a
	// partial serialization, and never a dropped constraint reported as success.
	Mandatory bool
	// Reps are the compatible representations, at least one, of which AT MOST ONE is chosen.
	Reps []Representation
}

// Proposal is one deterministic selection result (contract §2, §3). It is what the daemon hands a
// consumer; the consumer's own accepted contract, not this type, decides what it then does.
type Proposal struct {
	// Chosen holds at most one Representation per Candidate.Item.
	Chosen []Representation
	// Tokens is the sum of Chosen's AssembledCost, which never exceeds the budget passed to
	// Propose.
	Tokens core.Tokens
	// Value is the declared objective at Chosen, clamped at zero. It is a value under THIS
	// objective — not a task-quality score, and not evidence of an approximation bound.
	Value float64
	// Archive lists items delivered as archive-only: not injected, still recoverable. An entry
	// here is an outcome the caller must report, not a silent drop.
	Archive []dag.NodeID
	// Overflow reports that a Mandatory candidate could not be carried at any representation.
	Overflow bool
	// Item is the overflowing item when Overflow is set, and is empty otherwise.
	//
	// It is separate from Reason because the two have different readers. A consumer that files an
	// overflow into a drop report needs an IDENTIFIER for the report's id column and a sentence
	// for its detail column, and it must not have to parse the one out of the other. The first
	// integration of this type did exactly that and put a whole sentence where an id belonged.
	Item dag.NodeID
	// Reason is the human-readable explanation when Overflow is set, and is empty otherwise. It
	// names Item, the budget and p, so a drop report line is self-contained.
	Reason string
	// Iters counts marginal-gain evaluations, which is what makes lazy-greedy pruning observable
	// rather than merely asserted.
	Iters int
}
