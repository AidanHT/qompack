// Package rules implements the L5 path-scoped-rule and nested-CLAUDE.md discovery seam of
// 00-ARCHITECTURE.md §5.15 (closes Qompack.md gaps G4.1, G4.2): after a compaction, Claude
// Code's own restoration re-injects the project-root CLAUDE.md and unscoped rules, but rules
// with `paths:` frontmatter and nested CLAUDE.md files are lost until a matching file is
// coincidentally re-read. The rehydrator re-reads both from disk, independently of Claude Code's
// own restoration, and Scanner is how it finds them.
//
// rules is foundation-only (00-ARCHITECTURE.md §3.2): it imports core, paths and config and
// nothing else. SP-01 ships the complete type set below as real declarations and every Scanner
// operation as a stub returning core.ErrNotImplemented; SP-11 owns the real implementation.
package rules
