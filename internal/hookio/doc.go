// Package hookio provides typed representations of the Claude Code hook wire format: the JSON
// payload every hook subcommand reads from stdin (Event) and the JSON response it writes to
// stdout (Output).
//
// This package is where host drift is absorbed (00-ARCHITECTURE.md §5.3): every field in Event is
// optional-tolerant, unknown fields are preserved verbatim in Extra rather than dropped, and a
// missing or null field never panics. ReadEvent additionally enforces the hot-path payload-size
// budget (core.ErrBudget) and, on malformed JSON, returns the raw bytes alongside a wrapped error
// so a hook subcommand can log the offending payload and still exit 0 (§2.3).
//
// hookio needs almost nothing: its allow-set is foundation-only (00-ARCHITECTURE.md §3.2), and in
// practice it uses nothing but the standard library and internal/core (for the ID types embedded
// in Event and the ErrBudget sentinel).
package hookio
