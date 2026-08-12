// Package negknow implements the L2 negative-knowledge seam of 00-ARCHITECTURE.md §5.10: the
// evidence-linked elimination ledger, canonical descriptors, staleness detection against the
// store's file version history, and the tried.bloom membership cache the ledger rebuilds — never
// regenerates from a checkpoint or a summary — from active records only. SP-09 owns the real
// implementation.
//
// negknow may import ONLY sketch, store and dag, plus the foundation packages core, paths, config,
// logging and obs (00-ARCHITECTURE.md §3.2's negknow allow-set). This is the mirror image of
// store's own allow-set: store must not import negknow, so Store.ChangedSince takes []core.Dep
// rather than []negknow.Dep, and negknow.Dep is simply an alias of core.Dep (§4) so a caller
// holding either type is holding the same value.
//
// SP-01 ships the complete §5.10 type set — Scope, Status, SourceKind, Descriptor, Dep, Record,
// AnswerState, Answer, the Ledger interface (10 methods), Health, Deps and Detector — as real
// declarations, and every Ledger operation as a stub returning core.ErrNotImplemented. The one
// exception is Descriptor.Key: a real, fully specified stable-hash function later subplans need
// immediately for bloom keying (00-ARCHITECTURE.md §14.1 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md), so SP-01 implements it for real rather
// than stubbing it. Canonicalize, by contrast, IS a stub: turning free-text target/approach/reason
// strings into a canonical Descriptor is a real classification algorithm with no closed-form
// definition, unlike Key's fixed byte-layout-plus-hash.
package negknow
