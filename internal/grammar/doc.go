// Package grammar implements Sequitur, the L2 grammar-induction analyzer of
// 00-ARCHITECTURE.md §5.11: it folds the session's tool/action symbol stream into a context-free
// grammar incrementally, so a repeated digram collapses into a rule and a thrashing loop (the
// same short action sequence repeated many times) becomes detectable and compressible for the
// checkpoint.
//
// grammar is foundation-only (00-ARCHITECTURE.md §3.2: grammar may import core, paths, config,
// logging, obs and nothing else); concretely it imports only core and the standard library below.
//
// SP-01 ships the complete §5.11 type set as real declarations. FormatWarning is fully specified
// by the architecture and so is implemented for real (00-ARCHITECTURE.md §14.1 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md): SP-08 injects its output through
// UserPromptSubmit and SP-15 asserts it, so the wording is frozen here rather than left to SP-15
// to invent later.
//
// SP-15 lands the real grammar induction: every Sequitur operation now does its own work, and no
// method in this package reports core.ErrNotImplemented any more. Append folds a Symbol in while
// maintaining both classical invariants, Rules/Thrash/Compressed report the induced grammar, and
// MarshalBinary/UnmarshalBinary go through the version-tagged Snapshot codec — whose reader
// refuses a bad magic, a zero or higher version, and a truncated or forged payload with
// core.ErrDegraded, leaving its receiver unchanged rather than yielding a silently empty grammar.
//
// SP-15 also adds the state-aware layer alongside Sequitur: StateSignature, Progress and
// StateWarning (statewarn.go). It is WARNING-ONLY and stays that way — a StateWarning creates no
// elimination, no prohibition and no binding constraint — because the failure mode that matters
// here is the false positive. An ordinary edit-test-edit loop that is changing files or failure
// signatures is progress, not thrashing, and a system that called it thrashing would spend the
// user's trust faster than it saved their context. Warnings are bounded, deduplicated, expiring,
// and excluded from their own input so one can never cause the next.
package grammar
