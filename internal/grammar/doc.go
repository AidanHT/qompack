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
// to invent later. Every Sequitur operation is a stub — Append and Reset (which have no return
// value at all) are no-ops, Rules/Thrash/Compressed (which have no error return) report the
// documented nil, and MarshalBinary/UnmarshalBinary report core.ErrNotImplemented — until SP-15
// lands the real grammar induction.
package grammar
