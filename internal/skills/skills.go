package skills

import (
	"context"

	"github.com/qompack/qompack/internal/core"
)

// Entry is one skill's index entry: enough to make the model aware the skill exists and roughly
// what it does, and nothing more — the skill body itself is never part of an Entry
// (00-ARCHITECTURE.md §5.15, Qompack.md §8.6).
type Entry struct {
	// Name is the skill's identifier, as it would be invoked.
	Name string
	// Description is the skill's one-line description.
	Description string
	// Source is the skill file's project-relative path (paths.Key form).
	Source string
}

// Indexer builds the compact skill index re-injected after a compaction (Qompack.md §8.6, gap
// G4.4): names and one-line descriptions only, budgeted, so skill awareness returns without
// paying for full skill bodies.
type Indexer interface {
	// Index returns the skills it could fit, the total token cost of the returned entries, and an
	// error.
	//
	// Entries are ordered by Name (tiebroken by Source) and admitted as a PREFIX of that order:
	// filling stops at the first entry that does not fit rather than skipping ahead to a smaller
	// one, so the index a given budget produces is reproducible and is a prefix of the index any
	// larger budget produces. The returned total never exceeds budget.
	//
	// A budget <= 0 means "no budget": every discovered entry is returned, with its full cost.
	// That is not a degenerate case, it is a documented protocol — the rehydrator calls Index
	// twice, once with 0 to learn the complete set so its drop report can name the skills that
	// did not make the index, and once with the real budget to get the index itself. Callers that
	// want a bounded result must pass a positive budget.
	//
	// A skill file that cannot be read is skipped rather than failing the call: a rehydration that
	// returned no index because one file was unreadable would lose skill awareness entirely to
	// repair nothing (Qompack.md §12.3).
	Index(ctx context.Context, root string, budget core.Tokens) ([]Entry, core.Tokens, error)
}
