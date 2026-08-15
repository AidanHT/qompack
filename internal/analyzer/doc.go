// Package analyzer implements the L2 analysis seam of 00-ARCHITECTURE.md §5.12: Δ-scoring
// (what a block is worth against the observed continuation), redundancy detection (supersession
// and near-duplicate tool results), and the lazy-greedy submodular selector that spends a token
// budget. SP-15 owns the real implementation.
//
// analyzer may import ONLY store, dag, sketch and scheduler, plus the foundation packages core,
// paths, config, logging and obs (00-ARCHITECTURE.md §3.2's analyzer allow-set). The scheduler
// edge exists for exactly one reason: NewSelector asks scheduler.PSelectionAvailable() before it
// will construct anything.
//
// SP-01 ships the complete §5.12 type set — Block, DeltaMode, Continuation, the DeltaScorer
// interface, RedundancyReport, the Selector interface and Selection — as real declarations, and
// Score, DetectRedundancy and Select as stubs returning core.ErrNotImplemented. NewSelector is
// the deliberate exception: its two constructor guards are IMPLEMENTED, not stubbed (§14.1 rule 3
// of plans/V1-SP-01-foundation-toolchain-and-contracts.md), because they are structural.
//
// The first guard is §13 invariant 4: nothing scattered before p. A block whose Pos precedes the
// compaction point cannot be selected, and NewSelector filters the candidate set in the
// constructor so that it is not merely forbidden but impossible — there is no code path from a
// constructed Selector to a pre-p block. The second is the closing note's priority 3: do not ship
// submodular selection before p-selection, because an arbitrary subset of a cached prefix is a
// worst-case edit that would make the system measurably more expensive while looking smarter. The
// Pos check runs FIRST so the invariant-4 error is never masked by the ship-order one.
package analyzer
