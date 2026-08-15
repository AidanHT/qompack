package rehydrate

import (
	"context"

	"github.com/qompack/qompack/internal/core"
)

// Build renders the rehydrated context for r, reading from d (00-ARCHITECTURE.md §5.15).
//
// It emits Items in the normative §8.6 order, prices each against r.Budget, records everything it
// could not fit in Result.Dropped (G4.5), and wraps the whole payload in the §8.5 injection tags
// so a later read of the transcript can strip it out again rather than re-encoding it (§4.6).
// When it cannot do its full job — no checkpoint, a budget too small — it sets Result.Degraded
// and returns what it could, because a session that starts with less context is recoverable and a
// session that fails to start is not (§12.3).
//
// Always reports core.ErrNotImplemented until SP-11 lands.
func Build(ctx context.Context, r Request, d Deps) (Result, error) {
	return Result{}, core.ErrNotImplemented
}
