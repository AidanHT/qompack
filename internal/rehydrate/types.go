package rehydrate

import (
	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/rules"
	"github.com/qompack/qompack/internal/skills"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
)

// ItemKind identifies one of the ten things a rehydration may inject (00-ARCHITECTURE.md §5.15,
// Qompack.md §8.6): the eight numbered §8.6 items, plus the two kinds §8.6's "Instruction
// restoration" clause calls for, which render between items 6 and 7.
//
// THE ORDER IS NORMATIVE. These constants are the §8.6 importance order, most important first,
// and Build emits Items in it. That is not a formatting preference: budget truncation drops from
// the tail, so an implementation that reorders these silently changes what survives a small
// budget. Do not reorder them. Inserting a kind requires a §0 amendment to 00-ARCHITECTURE §5.15,
// because the values are what budget truncation drops from the tail of.
type ItemKind uint8

const (
	// ItemInvariants is item 1: pinned facts, verbatim, always.
	ItemInvariants ItemKind = iota
	// ItemUserIntent is item 2: the verbatim original intent, from L0 (G2.3).
	ItemUserIntent
	// ItemEliminations is item 3: the top-N eliminations by slice score, plus a note that
	// `already_tried` covers the rest. StandingInstruction is its companion.
	ItemEliminations
	// ItemDecisions is item 4: decisions with their rationale.
	ItemDecisions
	// ItemCurrentWork is item 5: the current goal and next step.
	ItemCurrentWork
	// ItemPointers is item 6: pointers, never contents (§4.4).
	ItemPointers
	// ItemRestoredInstructions is item 6a: the `paths:`-scoped rules and nested CLAUDE.md files
	// the rehydrator re-reads from disk because the host does not restore them (G4.1, G4.2).
	ItemRestoredInstructions
	// ItemSkillIndex is item 6b: the compact skill index, names and one-line descriptions only,
	// which the host does not re-inject at all (G4.4).
	ItemSkillIndex
	// ItemDropReport is item 7: the explicit drop report (G4.5).
	ItemDropReport
	// ItemAffordance is item 8: one line saying that recall, re_read and already_tried exist.
	// It is, and must remain, the LAST constant: the conformance suite bounds every emitted kind
	// by it.
	ItemAffordance
)

// Item is one rendered piece of the rehydrated context.
type Item struct {
	// Kind identifies which of the eight §8.6 items this is.
	Kind ItemKind
	// Rank is this Item's position in the emitted order, ascending from the most important.
	Rank int
	// Tokens is this Item's share of the payload's cost. It is an ACCOUNTING ALLOCATION, not an
	// independent tokenization of Text: Result.Tokens is the estimator's price for the COMPLETE
	// assembled payload (wrapper and inter-section separators included), and the per-item Tokens are
	// re-charged to sum to that total exactly (V6 §5, inventory 1.6.18). Do not read a row as the
	// additive token count of its own bytes.
	Tokens core.Tokens
	// Text is the rendered text.
	Text string
	// Truncated reports whether this Item was cut to fit the budget.
	Truncated bool
}

// DropEntry aliases checkpoint.DropEntry (00-ARCHITECTURE.md §5.15): a drop report is a drop
// report whether the checkpointer or the rehydrator produced it, and a caller holding either type
// is holding the same value.
type DropEntry = checkpoint.DropEntry

// Request is one rehydration ask (00-ARCHITECTURE.md §5.15).
type Request struct {
	// Session is the session being rehydrated.
	Session core.SessionID
	// Source is the SessionStart source that triggered it: startup, resume, compact or clear.
	Source string
	// ProjectRoot is the project root, used to resolve rules and skills from disk.
	ProjectRoot string
	// Budget is the token budget for the whole injection. §8.6 targets 8-12K, far below the
	// host's own 50K + 25K; the hard cap comes from runtime.rehydrate.maxTokens.
	Budget core.Tokens
	// Checkpoint is the checkpoint to rehydrate from.
	Checkpoint checkpoint.Checkpoint
	// Ref describes that checkpoint's artifact.
	Ref checkpoint.Ref
	// Cfg is the loaded configuration.
	Cfg config.Config
	// Selection is an optional, PRE-COMPUTED representation selection (SP-15; see selection.go).
	// A nil Selection is the rollback path and reproduces the pre-SP-15 build exactly, which is
	// why the field is a pointer rather than a zero-valued struct: "no selection was run" and "a
	// selection ran and chose nothing" are different facts, and only the second one should produce
	// an empty item 3.
	Selection *SelectionOutcome
	// Lineage is the session's lineage record (checkpoint.ReadLineage), or nil for a session that
	// was not forked. For a fork whose parent is recorded, item 2's original is the parent task's
	// — verified against that session's L0 capture and labelled as such — and the fork's own first
	// prompt is an evolution entry (F-UAT06-1). Like Selection it is request data the composition
	// root reads, because Build reads no files.
	Lineage *checkpoint.Lineage
	// Tier1OverflowReported says this session has already logged a tier-1 overflow Loud. The payload
	// and the drop report name every overflow each time either way; the Loud is once per session
	// (D50: UAT-04 logged the same line on each of 15 compactions), and a repeat is logged at Info.
	// The composition root tracks it in memory, because Build keeps no state between calls, so the
	// scope is once per session per daemon (ADR 0011 §23.3): a restarted daemon logs it once more.
	Tier1OverflowReported bool
}

// Result is one rehydration (00-ARCHITECTURE.md §5.15).
type Result struct {
	// Items are the rendered items, in the normative §8.6 order.
	Items []Item
	// Text is the additionalContext payload, injection-tagged with checkpoint.InjectionOpenTag
	// and InjectionCloseTag so that a later read of the transcript can strip it back out and never
	// re-encode it (§8.5, §4.6).
	Text string
	// Tokens is the payload's total token cost, which never exceeds Request.Budget. It is the
	// estimator applied to the COMPLETE assembled Text (wrapper and separators included), zero for an
	// empty payload — an estimate for budgeting, never a claim of exact provider usage — and it equals
	// the sum over Items.Tokens exactly.
	Tokens core.Tokens
	// Dropped is the explicit drop report (G4.5) backing the `dropped` retrieval tool.
	Dropped []DropEntry
	// Degraded reports that the rehydration could not do its full job — a missing checkpoint, a
	// budget too small — and said so rather than failing (§12.3).
	Degraded bool
	// DegradedReason names a degradation the drop report alone would not make plain: today, a
	// rehydration rebuilt from an older checkpoint because a newer one did not verify ("checkpoint
	// 0002 does not verify; rolled back to 0001", D49). Empty otherwise.
	DegradedReason string
	// Tier1Overflow reports that tier-1 material did not fit and is named as an overflow — the
	// condition Build logs Loud unless Request.Tier1OverflowReported.
	Tier1Overflow bool
	// Seq is the checkpoint sequence this rehydration came from.
	Seq core.CheckpointSeq
}

// Deps is the collaborator set Build reads from (00-ARCHITECTURE.md §5.15).
type Deps struct {
	// Store resolves pointer content and re-reads.
	Store store.Store
	// Ledger supplies the eliminations of item 3.
	Ledger negknow.Ledger
	// Graph supplies the slice scores that rank eliminations and pointers.
	Graph dag.Graph
	// Rules discovers path-scoped rules and nested CLAUDE.md files the host does not restore
	// (G4.1, G4.2).
	Rules rules.Scanner
	// Skills builds the compact skill index of §8.6 (G4.4).
	Skills skills.Indexer
	// Tokens prices every item against the budget.
	Tokens tokens.Estimator
	// Log is the logger; a nil Log must be treated as logging.Nop.
	Log logging.Logger
	// HostPaths judges recorded paths against the host's current Read rules, so section 6 never
	// shows one re_read would refuse (D50). Nil applies containment alone; see HostPaths.
	HostPaths HostPaths
}
