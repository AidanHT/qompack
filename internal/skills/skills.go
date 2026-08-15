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
	// Index returns every discovered skill's Entry, the total token cost of the returned entries,
	// and an error. The returned Entries and their total token cost must never exceed budget.
	Index(ctx context.Context, root string, budget core.Tokens) ([]Entry, core.Tokens, error)
}

// New returns a stub Indexer: constructing it always succeeds so wave-0 composition roots can
// wire a skills.Indexer today, but Index reports core.ErrNotImplemented until SP-11 lands the
// real discovery and budgeting logic (00-ARCHITECTURE.md §5.15).
func New() Indexer {
	return stubIndexer{}
}

// stubIndexer is the SP-01 placeholder Indexer. SP-11 owns the real implementation.
type stubIndexer struct{}

// Index always reports core.ErrNotImplemented.
func (stubIndexer) Index(ctx context.Context, root string, budget core.Tokens) ([]Entry, core.Tokens, error) {
	return nil, 0, core.ErrNotImplemented
}
