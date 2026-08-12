// Package skills implements the L5 compact skill-index seam of 00-ARCHITECTURE.md §5.15
// (Qompack.md §8.6, closes gap G4.4): after a compaction, invoked skill bodies are re-injected by
// Claude Code itself, but the skill *index* — the names and one-line descriptions of every
// available skill — is not, so the model loses awareness of what it could invoke at all. Indexer
// rebuilds a compact index from disk so that awareness returns within a small, config-driven
// token budget (runtime.rehydrate.skillIndexTokens, ~450 tokens by default).
//
// skills is foundation-only (00-ARCHITECTURE.md §3.2): it imports core, paths and config and
// nothing else. SP-01 ships the complete type set below as a real declaration and Indexer's
// operation as a stub returning core.ErrNotImplemented; SP-11 owns the real implementation.
package skills
