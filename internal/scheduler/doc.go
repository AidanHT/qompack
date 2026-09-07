// Package scheduler implements the L3 scheduling seam of 00-ARCHITECTURE.md §5.13: the BOCD
// changepoint detector of Qompack.md §6.6, the Young-Daly optimal-checkpoint-interval formula, the
// ski-rental cache-write threshold, the sliding-TTL cache model, p-selection, and the composite
// trigger that decides when the L4 checkpointer should fire (Qompack.md §8.4).
//
// # Purity contract
//
// scheduler is foundation-only (00-ARCHITECTURE.md §3.2): it imports core, paths, config, logging
// and obs and nothing else — never store, dag, checkpoint, daemon, observer or negknow. Nothing in
// this package performs I/O, reads a clock, starts a goroutine, or holds package-level mutable
// state beyond the single PSelectionAvailable flag mandated by 00-ARCHITECTURE.md §5.12. Evaluate
// is a pure function of its Inputs — the caller samples Inputs.Now — and a Detector is a pure
// function of the observations it has been fed: the same Features sequence always yields the same
// ChangepointState sequence and the same MarshalBinary bytes. That purity is what makes the
// composite trigger unit-testable and replayable without a live daemon.
//
// # The composite trigger (Qompack.md §8.4)
//
// The scheduler answers two questions: when to compact and where to cut. When:
//
//	should_compact  =  tokens > soft_floor
//	                AND ( at_changepoint
//	                      OR elapsed > young_daly_interval
//	                      OR tokens > hard_ceiling
//	                      OR idle_gap > ttl_max            # cache provably cold → cut is free
//	                      OR ( regime_known                # §5.4: fire BEFORE expiry — the
//	                           AND idle_gap > 0.8 · ttl )  # summarization call still reads cache
//	                      OR effort_changed )              # §5.4: the key changed; prefix is gone
//
// soft_floor is the AND-gate, well below Claude Code's own auto-compact threshold; hard_ceiling is
// one turn's headroom below that threshold, so the plugin always gets to checkpoint first. Every
// condition that evaluates true is reported in Decision.Reasons (see TriggerReason). at_changepoint
// is the Detector's thresholded decision: a changepoint is a task boundary, and compacting at one
// is nearly free in distortion because the new segment does not depend on the old segment's
// detail, whereas compacting mid-segment is maximally destructive (Qompack.md §6.6).
//
// # Where Runtime lives
//
// Runtime is declared here as an interface only. Its implementation assembles Inputs.Candidates
// from the dependence DAG and the content-addressed store's tool-use records, neither of which
// this package may import, so the concrete type lives in internal/daemon — a composition root —
// where it also persists the Detector's MarshalBinary bytes to state/bocd.json.
package scheduler
