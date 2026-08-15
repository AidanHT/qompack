// Package pins implements the L4 pins/invariants.json seam of 00-ARCHITECTURE.md §5.14
// (Qompack.md §7.4): user- and agent-pinned facts that are never summarized, truncated, or
// regenerated. The log is append-only — Add appends, Remove writes a tombstone record, and
// Materialize regenerates the pins/invariants.json convenience view from the log, which remains
// the source of truth (00-ARCHITECTURE.md §3.3).
//
// Invariant is defined in this package, not in internal/checkpoint, even though a checkpoint
// embeds a list of them: internal/checkpoint imports internal/pins, so the reverse edge would be
// an import cycle (00-ARCHITECTURE.md §3.2, §5.14). checkpoint.Invariant is a type alias of
// pins.Invariant.
//
// pins is foundation-only (00-ARCHITECTURE.md §3.2): it imports core, paths and config and
// nothing else. SP-01 ships the complete type set below as real declarations and every Store
// operation as a stub returning core.ErrNotImplemented; SP-10 owns the real implementation.
package pins
