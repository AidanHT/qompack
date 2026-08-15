package rules

import (
	"context"

	"github.com/qompack/qompack/internal/core"
)

// Rule is one discovered rule file: either a `paths:`-scoped rule or a nested CLAUDE.md, both of
// which are lost after compaction until a matching file is coincidentally re-read
// (Qompack.md §4, gaps G4.1/G4.2; 00-ARCHITECTURE.md §5.15).
type Rule struct {
	// Path is the rule file's project-relative path (paths.Key form).
	Path string
	// Globs is the set of `paths:` frontmatter glob patterns this rule is scoped to. Empty for a
	// nested CLAUDE.md, which is scoped by directory rather than by glob.
	Globs []string
	// Body is the rule's body text, verbatim, with frontmatter stripped.
	Body string
	// Tokens is Body's estimated token cost.
	Tokens core.Tokens
	// Nested reports whether this Rule came from NestedClaudeMD rather than PathScoped.
	Nested bool
}

// Scanner discovers path-scoped rules and nested CLAUDE.md files from disk, independently of
// Claude Code's own restoration (Qompack.md §4, gaps G4.1/G4.2).
type Scanner interface {
	// PathScoped returns every rule under root whose `paths:` frontmatter glob matches any path
	// in pointers.
	PathScoped(ctx context.Context, root string, pointers []string) ([]Rule, error)
	// NestedClaudeMD returns every CLAUDE.md under root that lives in a directory containing (or
	// ancestor to) a path in pointers.
	NestedClaudeMD(ctx context.Context, root string, pointers []string) ([]Rule, error)
}

// New returns a stub Scanner: constructing it always succeeds so wave-0 composition roots can
// wire a rules.Scanner today, but every operation reports core.ErrNotImplemented until SP-11
// lands the real frontmatter and directory-walk logic (00-ARCHITECTURE.md §5.15).
func New() Scanner {
	return stubScanner{}
}

// stubScanner is the SP-01 placeholder Scanner. SP-11 owns the real implementation.
type stubScanner struct{}

// PathScoped always reports core.ErrNotImplemented.
func (stubScanner) PathScoped(ctx context.Context, root string, pointers []string) ([]Rule, error) {
	return nil, core.ErrNotImplemented
}

// NestedClaudeMD always reports core.ErrNotImplemented.
func (stubScanner) NestedClaudeMD(ctx context.Context, root string, pointers []string) ([]Rule, error) {
	return nil, core.ErrNotImplemented
}
