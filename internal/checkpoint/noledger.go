package checkpoint

import (
	"context"
	"errors"
	"fmt"

	"github.com/qompack/qompack/internal/negknow"
)

// No ledger yet is not an error for the frontier (coordinator decision D49 of the V6 close-out,
// finding F-C4-C49-3 of the candidate 4 live re-run).
//
// The daemon opens its elimination ledger lazily: on a compaction, on the first already_tried or
// record_eliminated call, or when the project already holds records. In a session with none of
// those the checkpoint SourceSet's ledger accessor answers nil for the daemon's whole life, and
// act.advance_frontier failed on every idle tick with "SourceSet.Ledger is nil: its accessor
// resolved to no ledger" while the frontier never advanced. While the project holds no elimination
// record at all there is no negative knowledge a draft could miss, so the frontier begins its
// draft without a ledger instead of refusing, and reads one as soon as one exists.
//
// The allowance is the FRONTIER's alone. Every other Begin — PreCompact's above all — still
// refuses a set whose ledger could not be resolved, by name, at the top of the call: a compaction
// is exactly when the daemon opens the ledger, and a failed open there is a real gap.

// noLedgerKey is the context key the frontier sets to admit a set whose ledger is not open yet.
type noLedgerKey struct{}

// allowNoLedger marks ctx as the frontier's: Begin may then admit a set whose only gap is the
// ledger (ErrNoLedger), provided the project holds no elimination record.
func allowNoLedger(ctx context.Context) context.Context {
	return context.WithValue(ctx, noLedgerKey{}, true)
}

// noLedgerAllowed reports whether ctx carries allowNoLedger's mark.
func noLedgerAllowed(ctx context.Context) bool {
	v, _ := ctx.Value(noLedgerKey{}).(bool)
	return v
}

// admitNoLedger is Begin's allowance for the frontier: given Resolve's error for src, it answers
// the set a draft may be begun from, or the error to refuse with. Only ErrNoLedger under
// allowNoLedger's mark is admitted, and only while the project at root holds no elimination
// record. The admitted set's ledger is negknow.Deferred over the set's own accessor: it reads the
// accessor again at every call, so the draft reads the ledger as soon as one is open, and it
// answers no records only while the project still holds none.
func admitNoLedger(ctx context.Context, src SourceSet, resolveErr error, root string) (SourceSet, error) {
	if !errors.Is(resolveErr, ErrNoLedger) || !noLedgerAllowed(ctx) {
		return src, resolveErr
	}
	has, err := negknow.HasRecords(root)
	switch {
	case has && err != nil:
		return src, fmt.Errorf("%w: %w", resolveErr, err)
	case has:
		return src, resolveErr
	}
	src.Ledger = negknow.Deferred(src.LedgerFn, root)
	return src, nil
}
