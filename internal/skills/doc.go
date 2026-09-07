// Package skills implements the L5 compact skill-index seam of 00-ARCHITECTURE.md §5.15
// (Qompack.md §8.6, closes gap G4.4): after a compaction, invoked skill bodies are re-injected by
// Claude Code itself, but the skill *index* — the names and one-line descriptions of every
// available skill — is not, so the model loses awareness of what it could invoke at all. Indexer
// rebuilds a compact index from disk so that awareness returns within a small, config-driven
// token budget (runtime.rehydrate.skillIndexTokens, ~450 tokens by default).
//
// skills is foundation-only (00-ARCHITECTURE.md §3.2): it imports core, paths, config and
// logging, and nothing else. In particular it does NOT import tokens, which is why the index is
// priced by the baseline (len+3)/4 rule here and re-estimated by the caller that owns a real
// estimator.
//
// SP-01 shipped the type set as a real declaration with Indexer's operation stubbed; SP-11 landed
// the real implementation — discovery of both .claude/skills/<name>/SKILL.md and
// .claude/skills/<name>.md, frontmatter name/description extraction with body-line and
// "(no description)" fallbacks, and the budgeted prefix truncation Indexer.Index documents.
package skills
