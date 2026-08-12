// Package redact implements the §13 invariant 7 choke point (00-ARCHITECTURE.md §5.22a,
// `runtime.redact`): secrets must never reach objects/, so redaction is applied once, at
// store.Put/PutBytes, before canonicalization and chunking — never as an after-the-fact audit
// step.
//
// redact is foundation-only (00-ARCHITECTURE.md §3.2: redact may import core, paths, config,
// logging, obs and nothing else); concretely it imports only core and config below.
//
// SP-01 ships the complete §5.22a type set as real declarations. Nop is fully specified by the
// architecture and so is implemented for real (00-ARCHITECTURE.md §14.1 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md): tests throughout the tree need a
// Redactor that is honestly a no-op, not a stub pretending to be one. New's Redactor is a stub —
// Redact and Rules have no error return, so each reports the documented zero-effort answer (the
// input unchanged, with no matches, and no rule names) until SP-06 lands the real rule set.
package redact
