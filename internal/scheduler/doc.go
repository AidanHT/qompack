// Package scheduler implements the L3 scheduling seam of 00-ARCHITECTURE.md §5.13: the BOCD
// changepoint detector, the Young-Daly optimal-checkpoint-interval formula, the ski-rental
// cache-write threshold, and the composite trigger that decides when the L4 checkpointer should
// fire (Qompack.md §8.4).
//
// scheduler is foundation-only (00-ARCHITECTURE.md §3.2): it imports core, paths and config and
// nothing else. Evaluate in particular must stay a pure function of its Inputs — no I/O, no
// clock reads, no goroutines, no package-level mutable state beyond the ship-order flag
// PSelectionAvailable reports. That purity is what makes the composite trigger unit-testable and
// replayable without a live daemon.
//
// Runtime is declared here as an interface only. Its implementation assembles Inputs.Candidates
// from the dependence DAG and the content-addressed store's tool-use records, neither of which
// this package may import, so the concrete type lives in internal/daemon (a composition root)
// and is owned by SP-12 alongside the rest of this package's real behaviour.
//
// SP-01 ships every type below as a real declaration and every operation as a stub returning
// core.ErrNotImplemented (or a documented zero value, for the handful of methods with no error
// return) — except YoungDaly, SkiRentalShouldWrite and PSelectionAvailable, which have closed-form
// definitions the rest of the system needs immediately and so are implemented for real
// (00-ARCHITECTURE.md §14.1 of plans/V1-SP-01-foundation-toolchain-and-contracts.md). SP-12 owns
// everything else: the real Evaluate body, the real BOCD posterior update, and the Runtime
// implementation in internal/daemon.
package scheduler
