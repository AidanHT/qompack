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
	// Tokens is this Item's token cost.
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
}

// Result is one rehydration (00-ARCHITECTURE.md §5.15).
type Result struct {
	// Items are the rendered items, in the normative §8.6 order.
	Items []Item
	// Text is the additionalContext payload, injection-tagged with checkpoint.InjectionOpenTag
	// and InjectionCloseTag so that a later read of the transcript can strip it back out and never
	// re-encode it (§8.5, §4.6).
	Text string
	// Tokens is the payload's total token cost, which never exceeds Request.Budget.
	Tokens core.Tokens
	// Dropped is the explicit drop report (G4.5) backing the `dropped` retrieval tool.
	Dropped []DropEntry
	// Degraded reports that the rehydration could not do its full job — a missing checkpoint, a
	// budget too small — and said so rather than failing (§12.3).
	Degraded bool
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
}
