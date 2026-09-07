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
// pins is foundation-only (00-ARCHITECTURE.md §3.2): it imports core, paths, logging and obs, and
// nothing else — no store, no dag, no checkpoint, and nothing outside internal/ at all.
//
// SP-01 shipped the complete type set as real declarations with every Store operation stubbed to
// core.ErrNotImplemented. SP-10 replaced those stubs with the real *pinStore: a mutex-guarded
// in-memory ordered map replayed once from the log at open, appending through paths.AppendOnly and
// replacing the derived view through paths.ReplacePinsView. A corrupt log LINE is skipped,
// counted and reported once at Warn rather than being fatal — one bad record must not cost a
// project every invariant it ever pinned — whereas a log that cannot be read at all is fatal,
// because answering "no pins" for a file we failed to open would let the next checkpoint silently
// drop tier-1 content.
package pins
