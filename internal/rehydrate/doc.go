// Package rehydrate implements the L5 rehydration seam of 00-ARCHITECTURE.md §5.15: what Qompack
// injects back into the context after a compaction, in what order, and inside what budget. SP-11
// owns the real implementation.
//
// rehydrate may import checkpoint, store, negknow, dag, rules and skills and tokens, plus the
// foundation packages core, paths, config, logging and obs (00-ARCHITECTURE.md §3.2's rehydrate
// allow-set). Nothing may import it: it is a consumer of every layer below and a producer of one
// string.
//
// Two things about that string are the whole design. First, its ORDER is normative: ItemKind's
// constants are the §8.6 importance order, and a rehydration that reorders them is wrong even if
// it fits the budget, because what survives truncation is decided by position. Second, its SIZE
// is deliberately far below what the host restores on its own — §8.6 targets 8-12K against Claude
// Code's 50K + 25K — because the point is that pointers plus retrieval replace eager restoration.
// A rehydrator that "helpfully" injected more would be re-creating the problem the whole system
// exists to solve.
//
// SP-01 ships the complete §5.15 type set — ItemKind and its eight constants, Item, DropEntry,
// Request, Result and Deps — as real declarations, and Build as a stub returning
// core.ErrNotImplemented. The one exception is StandingInstruction: a fixed sentence §5.15 quotes
// verbatim, which §14.1 rule 3 of plans/V1-SP-01-foundation-toolchain-and-contracts.md has SP-01
// implement for real because SP-13's `already_tried` tool and SP-11's item 3 must agree on it
// exactly, and they land in the same wave.
package rehydrate
