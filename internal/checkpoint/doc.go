// Package checkpoint implements the L4 checkpointer seam of 00-ARCHITECTURE.md §5.14: the
// immutable, importance-ordered checkpoint artifact of Qompack.md §8.5, the incrementally
// advancing draft that encodes closed segments exactly once (the §4.6 DPI guard), the tiered
// truncation order of §6.9, ground-truth pointer validation (G2.5), decision extraction — the
// sole producer of core.DecisionID — and the PreCompact focus instruction of §8.5. SP-10 owns
// the real implementation.
//
// checkpoint may import ONLY store, dag, negknow, pins, grammar and tokens, plus the foundation
// packages core, paths, config, logging and obs (00-ARCHITECTURE.md §3.2's checkpoint allow-set).
// It must never be imported by any of those: rehydrate and mcp are its consumers, not its
// dependencies. The Invariant type is DEFINED in pins and aliased here for exactly that reason —
// checkpoint imports pins for SourceSet, so the reverse edge would be a cycle (§5.14).
//
// SP-01 shipped the complete §5.14 type set — Checkpoint, UserIntent, Decision, CurrentWork,
// Pointers, FilePointer, ToolPointer, DropEntry, CacheInfo, Invariant, SourceSet, Draft, Ref, the
// Writer interface (4 methods) and the Reader interface (5 methods) — as real declarations, with
// every operation stubbed to core.ErrNotImplemented (or to the documented zero value, for the two
// package-level functions with no error return). SP-10 replaced those stubs with the real
// implementation: the *FileWriter draft state machine behind Begin/Advance/Finalize/Abort, the
// *fileReader over checkpoints/MANIFEST.jsonl behind Latest/Get/List/Chain/Verify, ExtractDecisions,
// Truncate and the PreCompact focus instruction. Nothing in this package returns
// core.ErrNotImplemented any more.
//
// The one operation SP-01 did NOT stub is StripInjections and its two injection-tag constants: a
// fully specified, closed-form pure function that SP-08 and SP-11 both needed before SP-10's
// writer existed, so §14.1 of plans/V1-SP-01-foundation-toolchain-and-contracts.md had SP-01
// implement it for real. That implementation is still the one this package uses.
//
// The on-disk JSON shape is verbatim from Qompack.md §8.5 and is versioned (SchemaVersion). Any
// change to it bumps the version and adds a migration; testdata/golden/contracts/checkpoint/want/
// 0001.json is the frozen fixture (Rule W-2) every field spelling below is pinned against.
package checkpoint
