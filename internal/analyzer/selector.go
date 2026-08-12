package analyzer

import (
	"context"
	"fmt"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/scheduler"
)

// Selector spends a token budget over a candidate set that was fixed at construction time
// (00-ARCHITECTURE.md §5.12). It is CONSTRUCTED with p, and NewSelector filters the candidate set
// in the constructor, so it is structurally impossible to select a block before p — §13 invariant
// 4 is enforced by the type's construction rather than by a rule anyone has to remember.
type Selector interface {
	// P reports the compaction point this Selector was constructed with.
	P() int
	// Select maximizes coverage(S) - lambda*redundancy(S) over the candidate set, subject to
	// budget.
	Select(ctx context.Context, budget core.Tokens) (Selection, error)
}

// NewSelector constructs a Selector over blocks at compaction point p, ranked by the dependence
// slice sl and the Δ-scores delta, penalizing redundancy by lambda and using lazy-greedy
// evaluation when lazy is set.
//
// Both of its guards are real in every build, stub or not (§14.1 rule 3 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md), and test/guards asserts them in wave 0:
//
//  1. §13 invariant 4 / §5.3: any block whose Pos precedes p is refused with core.ErrBudget.
//     Nothing scattered before p may be selected, so the candidate set is filtered here, in the
//     constructor, rather than checked later where it could be forgotten.
//  2. Closing note 3: submodular selection must not ship before p-selection, so construction is
//     refused with core.ErrNotImplemented while scheduler.PSelectionAvailable() reports false.
//     An arbitrary subset of a cached prefix is a worst-case edit; shipping this first would make
//     the system measurably more expensive while looking smarter.
//
// The order matters and is normative: the Pos check runs FIRST, so a build in which both
// conditions hold reports the invariant-4 violation rather than masking it behind the ship-order
// one. SP-15 replaces Select and may not remove either check.
func NewSelector(p int, blocks []Block, slice dag.Slice, delta map[dag.NodeID]float64,
	lambda float64, lazy bool,
) (Selector, error) {
	for _, b := range blocks {
		if b.Pos < p {
			return nil, fmt.Errorf("%w: block %s at pos %d precedes p=%d (§5.3, §13 invariant 4)",
				core.ErrBudget, b.ID, b.Pos, p)
		}
	}
	if !scheduler.PSelectionAvailable() {
		return nil, fmt.Errorf("%w: submodular selection requires p-selection (closing note 3)",
			core.ErrNotImplemented)
	}
	return stubSelector{p: p}, nil
}

// stubSelector is the SP-01 placeholder Selector. It remembers p — the constructor's own
// contract, which is real — and stubs the selection itself. SP-15 owns the real lazy-greedy
// implementation.
type stubSelector struct{ p int }

// P returns the compaction point this Selector was constructed with. This is real, not a stub:
// the constructor already validated every block against it, so reporting it is a fact the stub
// genuinely knows.
func (s stubSelector) P() int { return s.p }

// Select always reports core.ErrNotImplemented.
func (stubSelector) Select(ctx context.Context, budget core.Tokens) (Selection, error) {
	return Selection{}, core.ErrNotImplemented
}
