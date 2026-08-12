// Package eval implements the L7 replay-and-evaluation seam of 00-ARCHITECTURE.md §5.18: a
// deterministic replay harness that re-applies a Policy's keep-set decisions against a logged
// session, a Belady-optimal keep-set as the ceiling every policy is scored against
// (FractionOfOPT, §11.1's primary metric), divergence metrics, and the report the CI replay gate
// consumes.
//
// eval is foundation-only (00-ARCHITECTURE.md §3.2): it imports core, paths and config and
// nothing else. SP-01 ships the complete type set below as real declarations and every Harness
// operation as a stub returning core.ErrNotImplemented (or a documented zero value, for the two
// methods with no error return); SP-02 owns the real implementation, the synthetic-session
// generator, and the 24-session synthetic corpus.
package eval
