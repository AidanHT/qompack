// Package rules implements the L5 path-scoped-rule and nested-CLAUDE.md discovery seam of
// 00-ARCHITECTURE.md §5.15 (closes Qompack.md gaps G4.1, G4.2): after a compaction, Claude
// Code's own restoration re-injects the project-root CLAUDE.md and unscoped rules, but rules
// with `paths:` frontmatter and nested CLAUDE.md files are lost until a matching file is
// coincidentally re-read. The rehydrator re-reads both from disk, independently of Claude Code's
// own restoration, and Scanner is how it finds them.
//
// rules is foundation-only (00-ARCHITECTURE.md §3.2): it imports core, paths and logging, and
// nothing else. In particular it does NOT import tokens, which is why Rule.Tokens is a baseline
// (len+3)/4 estimate that the rehydrator re-prices with a real estimator before budgeting.
//
// SP-01 shipped the type set as real declarations with every Scanner operation stubbed; SP-11
// landed the real implementation: a "**"-aware glob matcher, a frontmatter reader for the three
// `paths:` spellings Claude Code rule files use, a discovery walk over .claude/rules (recursive)
// and .claude (depth 1), and the ancestor walk NestedClaudeMD performs — which climbs from each
// pointer's directory toward the project root, stopping before the root itself because Claude
// Code re-injects that file on its own (Qompack.md §2.7, widened to "containing, or ancestor to"
// in Qompack.md v1.4).
package rules
