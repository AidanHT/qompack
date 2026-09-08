package analyzer

import (
	"context"
	"fmt"
	"sort"

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
	return newGreedySelector(p, blocks, slice, delta, lambda, lazy), nil
}

// greedySelector is the real §5.12 selector: a lazy-greedy walk over the candidate set, spending a
// token budget on the objective coverage(S) - lambda*redundancy(S). It replaces SP-01's stub, and
// it keeps the constructor's own contract — the p it was built with — because that was never a
// stub in the first place.
//
// Everything it needs is resolved once, in the constructor, and the value is then immutable: Select
// builds its own state per call and mutates nothing on the receiver. That is what lets one Selector
// answer several budgets, and what makes two Selects with the same budget provably identical rather
// than identical-so-far.
type greedySelector struct {
	// p is the compaction point every kept block sits at or after.
	p int
	// blocks is a copy of the candidate set, sorted by ID ascending.
	blocks []Block
	// value holds each block's coverage contribution in [0,1]; see newGreedySelector.
	value map[dag.NodeID]float64
	// lambda is the redundancy penalty.
	lambda float64
	// lazy selects stale-bound pruning over eager re-pricing.
	lazy bool
}

// blockGroup is the coverage bucket a Block saturates against.
//
// Two blocks that share a content root hold the SAME bytes, so delivering both covers nothing the
// first did not already cover; grouping them makes contract §3's min(1, ·) saturation express that
// directly, and is what stops a prefix full of re-read files from spending its whole budget on one
// file's content. A block with no recorded root is its own group — an unknown root is not evidence
// of sameness, and treating every unrooted block as one group would collapse the candidate set.
type blockGroup struct {
	// root is the block's content root, or the zero hash when it has none.
	root core.Hash
	// id is the block's own id, set only when root is zero.
	id dag.NodeID
}

// newGreedySelector resolves each block's coverage contribution once and freezes the candidate set
// into a sorted copy.
//
// The contribution is the Δ-score if the caller scored the block, then the dependence slice's
// relevance score, and otherwise ZERO — and the zero is deliberate. §5.12's Δ-score is a proxy
// measured against the OBSERVED continuation; a block nobody measured has no measured worth, and
// inventing a default for it would let unmeasured content displace content that was actually shown
// to matter. A caller who wants an unscored block considered scores it.
//
// Sorting by id gives the engine's move indices contract §3's "sorted by Item ascending" order, so
// its (gain desc, index asc) tie-break is the declared one; the copy means Select never mutates the
// caller's slice.
func newGreedySelector(p int, blocks []Block, slice dag.Slice, delta map[dag.NodeID]float64,
	lambda float64, lazy bool,
) greedySelector {
	s := greedySelector{
		p:      p,
		blocks: append([]Block(nil), blocks...),
		value:  make(map[dag.NodeID]float64, len(blocks)),
		lambda: lambda,
		lazy:   lazy,
	}
	sort.SliceStable(s.blocks, func(a, b int) bool { return s.blocks[a].ID < s.blocks[b].ID })

	for _, b := range s.blocks {
		if v, ok := delta[b.ID]; ok {
			s.value[b.ID] = satur(v)
			continue
		}
		if v, ok := slice.Scores[b.ID]; ok {
			s.value[b.ID] = satur(float64(v))
			continue
		}
		s.value[b.ID] = 0
	}
	return s
}

// P returns the compaction point this Selector was constructed with. The constructor already
// validated every block against it, so this is a fact the selector genuinely knows rather than a
// value it repeats.
func (s greedySelector) P() int { return s.p }

// blockState is one Select call's running set: how much coverage each group has accumulated, and
// how many blocks it has delivered.
type blockState struct {
	// cov accumulates a group's delivered contribution, before saturation.
	cov map[blockGroup]float64
	// count is how many kept blocks belong to each group.
	count map[blockGroup]int
}

// groupOf returns b's coverage bucket.
func groupOf(b Block) blockGroup {
	if b.Root.IsZero() {
		return blockGroup{id: b.ID}
	}
	return blockGroup{root: b.Root}
}

// Select spends budget over the candidate set, maximizing
//
//	coverage(S) - lambda*redundancy(S)
//
// where coverage saturates per content-root group (see blockGroup) and redundancy counts the two
// kinds of waste §5.12 and §8.1 name: a block a later tool use on the same path has SUPERSEDED, and
// a block that re-delivers a content root the keep-set already holds. Both are content the caller
// would pay for twice and read once.
//
// Four properties are asserted by analyzertest's behaviour block and hold by construction here:
// Tokens is the true sum over Keep rather than a counter maintained alongside it; Keep and Dropped
// partition the candidate set exactly, so /qompack:dropped can be honest about what was lost
// (G4.5); nothing before p is ever kept, which the constructor already made impossible; and a zero
// budget keeps nothing. Iters reports every marginal-gain evaluation the walk performed, which is
// what makes the lazy pruning observable rather than merely claimed — see runLazyGreedy.
//
// NO approximation bound is claimed. The redundancy term is subtracted, which does not preserve
// monotonicity, so the greedy's only guarantee is the one it enforces directly: it never spends a
// token on a move whose marginal gain is not strictly positive. Value is therefore never negative,
// because it is a sum of strictly positive increments starting from the empty set.
func (s greedySelector) Select(ctx context.Context, budget core.Tokens) (Selection, error) {
	st := &blockState{
		cov:   make(map[blockGroup]float64, len(s.blocks)),
		count: make(map[blockGroup]int, len(s.blocks)),
	}
	kept := make([]bool, len(s.blocks))

	res, err := runLazyGreedy(ctx, greedyProblem{
		moves: len(s.blocks),
		lazy:  s.lazy,
		live:  func(i int) bool { return !kept[i] },
		gain:  func(i int) float64 { return s.gainOf(s.blocks[i], st) },
		cost:  func(i int) core.Tokens { return s.blocks[i].Tokens },
		afford: func(i int, cost, spent core.Tokens) bool {
			// The budget is the only constraint on this surface: unlike Propose there is no
			// mandatory record whose room has to be kept back, so every block that fits is
			// affordable and runLazyGreedy's own budget test is the whole of it.
			return true
		},
		accept: func(i int) {
			b := s.blocks[i]
			g := groupOf(b)
			kept[i] = true
			st.cov[g] += s.value[b.ID]
			st.count[g]++
		},
	}, budget)
	if err != nil {
		return Selection{}, err
	}

	sel := Selection{Iters: res.Iters}
	for _, i := range res.Accepted {
		sel.Keep = append(sel.Keep, s.blocks[i].ID)
		sel.Tokens += s.blocks[i].Tokens
	}
	for i, b := range s.blocks {
		if !kept[i] {
			sel.Dropped = append(sel.Dropped, b.ID)
		}
	}
	sel.Value = s.valueOf(sel.Keep)
	return sel, nil
}

// gainOf is the marginal gain of adding b to st: the saturating coverage it adds, less lambda per
// unit of redundancy it introduces.
func (s greedySelector) gainOf(b Block, st *blockState) float64 {
	g := groupOf(b)
	old := st.cov[g]
	gain := satur(old+s.value[b.ID]) - satur(old)

	red := 0
	if b.Superseded {
		red++
	}
	if st.count[g] > 0 {
		red++
	}
	return gain - s.lambda*float64(red)
}

// valueOf re-evaluates the objective at keep, from the kept ids alone.
//
// It recomputes rather than accumulating the gains the walk already measured, and the difference is
// the point: an independently computed Value cannot drift away from the set it describes, which is
// exactly the bug a separately maintained running total produces the first time a move is skipped
// on a path nobody remembered to decrement. keep is walked in acceptance order, which is
// deterministic, so the floating-point sum is reproducible.
func (s greedySelector) valueOf(keep []dag.NodeID) float64 {
	byID := make(map[dag.NodeID]Block, len(s.blocks))
	for _, b := range s.blocks {
		byID[b.ID] = b
	}

	cov := make(map[blockGroup]float64, len(keep))
	count := make(map[blockGroup]int, len(keep))
	red := 0
	for _, id := range keep {
		b, ok := byID[id]
		if !ok {
			continue
		}
		g := groupOf(b)
		if b.Superseded {
			red++
		}
		if count[g] > 0 {
			red++
		}
		cov[g] += s.value[id]
		count[g]++
	}

	// Summed over the kept ids in their own order rather than over the map, so the result does not
	// depend on Go's randomized map iteration: two Selects on equal inputs must agree bit for bit.
	total := 0.0
	seen := make(map[blockGroup]bool, len(keep))
	for _, id := range keep {
		b, ok := byID[id]
		if !ok {
			continue
		}
		g := groupOf(b)
		if seen[g] {
			continue
		}
		seen[g] = true
		total += satur(cov[g])
	}
	return total - s.lambda*float64(red)
}
