package checkpoint

import (
	"context"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/tokens"
)

// Truncate applies the §6.9 importance ordering to c against budget: tier 3 (pointers, narrative)
// is dropped first, tier 2 (decisions, open questions, current work) only after tier 3 is
// exhausted, and tier 1 (invariants, user intent, eliminations) never. Which field sits in which
// tier is a config decision, not a constant: t is config.Checkpoint.Tiers.
//
// Truncate returns c unchanged with no drops in this build. It has no error return, so
// core.ErrNotImplemented is not available to it, and the two candidate zero answers are not
// equivalent: returning Checkpoint{} would claim every tier had been truncated away, including
// tier 1, which is a lie about the one thing §6.9 says can never happen. Returning the input
// untouched claims only "nothing was dropped", which is precisely and verifiably what this stub
// did. It is the same non-destructive identity that redact.stubRedactor.Redact returns for the
// same reason (00-ARCHITECTURE.md §14.1 rule 1 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). SP-10 owns the real tier walk.
func Truncate(c Checkpoint, budget core.Tokens, t config.TiersCfg, est tokens.Estimator) (Checkpoint, []DropEntry) {
	return c, nil
}

// ValidatePointers is the ground-truth check of G2.5: every pointer path in p is checked against
// the working tree at root (and its git status), and a DropEntry is returned for each pointer
// that no longer resolves — a checkpoint that points at a deleted file is worse than one that
// admits it dropped the pointer. Always reports core.ErrNotImplemented until SP-10 lands.
func ValidatePointers(ctx context.Context, root string, p Pointers) ([]DropEntry, error) {
	return nil, core.ErrNotImplemented
}
