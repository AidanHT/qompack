package analyzer

import (
	"container/heap"
	"context"
	"fmt"
	"sort"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
)

// This file holds two things: the lazy-greedy engine both selection surfaces run on, and the
// representation objective of plans/sdd/V5-SP-15/contract.md §3 that Propose exposes. Selector
// and its Block-level Select live next door in selector.go and drive the same engine, so there is
// exactly one place where "spend a budget by repeatedly taking the best affordable move" is
// written down and exactly one place a bug in it can hide.

// ----------------------------------------------------------------------------------------------
// The lazy-greedy engine.
// ----------------------------------------------------------------------------------------------

// gainEntry is one candidate move's cached marginal gain inside the priority queue.
//
// epoch is what makes the queue LAZY rather than merely sorted. It records the state of the
// accepted set the gain was measured against; the engine bumps its own epoch on every acceptance,
// so an entry whose epoch has fallen behind is a stale UPPER BOUND rather than a usable value. A
// stale entry that still sorts to the top is re-measured and pushed back, and if it survives that
// it is genuinely the best move — which is the whole of the lazy trick: every move below the top
// is skipped without ever being priced, because its own upper bound already lost.
type gainEntry struct {
	// move indexes the greedyProblem's move set.
	move int
	// gain is the marginal objective gain measured at epoch.
	gain float64
	// epoch is the accepted-set generation gain was measured against.
	epoch int
}

// gainHeap is the max-heap of gainEntry the engine pops from. It implements heap.Interface, and
// the unexported push/pop wrappers keep container/heap's any-typed contract out of the engine.
type gainHeap []gainEntry

// Len implements sort.Interface for heap.Interface.
func (h gainHeap) Len() int { return len(h) }

// Less orders the queue by descending gain, breaking ties by ascending move index.
//
// The tie-break is not cosmetic and not arbitrary: the move set is built in (Item ascending, Kind
// ascending) order, so "lower index first" IS contract §3's declared tie-break rule
// "(gain desc, Item asc, Kind asc)". Leaving equal-gain moves to the heap's internal sift order
// would make two runs on identical inputs disagree, and a Proposal feeds a replay metric — an
// incomparable metric between commits is worse than no metric at all.
func (h gainHeap) Less(i, j int) bool {
	if h[i].gain != h[j].gain {
		return h[i].gain > h[j].gain
	}
	return h[i].move < h[j].move
}

// Swap implements sort.Interface for heap.Interface.
func (h gainHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

// Push implements heap.Interface. It is reached only through this file's push wrapper, which only
// ever passes a gainEntry, so the comma-ok form is defensive: a bare type assertion would panic on
// a caller that does not exist rather than drop a value that cannot arrive.
func (h *gainHeap) Push(x any) {
	if e, ok := x.(gainEntry); ok {
		*h = append(*h, e)
	}
}

// Pop implements heap.Interface, handing back the entry heap.Pop has already sifted to the end.
func (h *gainHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// push adds e to the queue.
func (h *gainHeap) push(e gainEntry) { heap.Push(h, e) }

// pop removes and returns the highest-gain entry, reading it off the front before delegating to
// heap.Pop so container/heap's any-typed return value never reaches the engine.
func (h *gainHeap) pop() gainEntry {
	top := (*h)[0]
	heap.Pop(h)
	return top
}

// greedyProblem is the seam between the engine and an objective. Every field is a closure over the
// objective's own mutable state, which is why the engine itself holds no knowledge of blocks,
// representations, coverage or redundancy: it knows only that moves have gains and prices, that
// some moves stop being legal once others are taken, and that a budget binds.
type greedyProblem struct {
	// moves is the number of candidate moves, indexed 0..moves-1 in a deterministic order the
	// objective fixed before the engine ever ran.
	moves int
	// lazy selects stale-bound pruning over re-pricing every surviving move after each acceptance.
	// It is config.SelectionCfg.Submodular.LazyGreedy, threaded through so the two modes can be
	// compared on one instance rather than argued about.
	lazy bool
	// gain reports move i's marginal objective gain against the CURRENT accepted set.
	gain func(i int) float64
	// cost reports move i's marginal token cost against the CURRENT accepted set. It may FALL as
	// other moves are accepted — a dependency another move already paid for is not paid twice —
	// which is why an unaffordable move is deferred rather than discarded.
	cost func(i int) core.Tokens
	// afford reports whether the objective permits spending cost on move i having already spent
	// spent. It is where a constraint the budget alone cannot express lives: the room that must
	// stay reserved for records which are not optional.
	afford func(i int, cost, spent core.Tokens) bool
	// live reports whether move i is still legal at all: a representation of an item that already
	// has one is not, and neither is one whose dependency closure can never be satisfied.
	live func(i int) bool
	// accept commits move i into the objective's state.
	accept func(i int)
}

// greedyResult is one runLazyGreedy run.
type greedyResult struct {
	// Accepted lists the accepted move indices in acceptance order.
	Accepted []int
	// Spent is the sum of the accepted moves' marginal costs.
	Spent core.Tokens
	// Iters is the number of marginal-gain evaluations performed.
	Iters int
}

// runLazyGreedy spends budget over p's move set, repeatedly taking the highest-gain affordable
// move until nothing with a positive gain still fits.
//
// Three properties are load-bearing, and each one names a failure it exists to prevent:
//
//  1. A move whose marginal gain is NOT STRICTLY POSITIVE is never accepted. Contract §3 declares
//     no approximation bound for this objective — redundancy subtraction does not preserve
//     monotonicity, and the dependency and one-representation-per-item constraints change the
//     feasible family — so the one guarantee the loop can actually make is that it never spends
//     tokens on something that does not improve the declared objective.
//  2. An unaffordable move is DEFERRED, not discarded. Marginal cost falls when another move pays
//     for a shared dependency, so discarding on the first refusal would permanently lose moves
//     that later fit. Deferred moves return to the queue on the next acceptance — the only event
//     that can change a price — which is also why the loop cannot spin: nothing re-enters the
//     queue unless something was accepted, and acceptances are bounded by the move count.
//  3. Iters counts every marginal-gain evaluation, the initial pass included. That is the number
//     which makes lazy pruning OBSERVABLE: run the same instance eagerly and the count rises,
//     which is evidence rather than an assertion written in a comment.
//
// A cancelled context reports core.ErrBudget wrapping ctx.Err(). Cancellation here is a latency
// budget expiring — core.ErrBudget's own definition covers token, byte AND latency budgets — and
// only the four sentinels are legal error values in this tree (00-ARCHITECTURE.md §5.22), so
// inventing a fifth to say "cancelled" would break every conformance suite's requireKnownError.
func runLazyGreedy(ctx context.Context, p greedyProblem, budget core.Tokens) (greedyResult, error) {
	var res greedyResult
	h := &gainHeap{}
	epoch := 0

	for m := 0; m < p.moves; m++ {
		if !p.live(m) {
			continue
		}
		res.Iters++
		h.push(gainEntry{move: m, gain: finiteGain(p.gain(m)), epoch: epoch})
	}

	var deferred []gainEntry
	for h.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return greedyResult{}, fmt.Errorf("%w: selection abandoned: %w", core.ErrBudget, err)
		}

		e := h.pop()
		if !p.live(e.move) {
			continue
		}
		if e.epoch != epoch {
			res.Iters++
			h.push(gainEntry{move: e.move, gain: finiteGain(p.gain(e.move)), epoch: epoch})
			continue
		}
		if e.gain <= 0 {
			// e is fresh and is the queue's maximum, and every other entry's cached gain is an
			// upper bound on its true gain, so nothing left can be positive. The deferred moves
			// were refused at a state nothing has changed since.
			break
		}

		cost := p.cost(e.move)
		if cost < 0 || res.Spent+cost > budget || !p.afford(e.move, cost, res.Spent) {
			deferred = append(deferred, e)
			continue
		}

		p.accept(e.move)
		res.Accepted = append(res.Accepted, e.move)
		res.Spent += cost
		epoch++

		if p.lazy {
			for _, d := range deferred {
				h.push(d)
			}
			deferred = deferred[:0]
			continue
		}
		res.Iters += repriceAll(h, deferred, p, epoch)
		deferred = deferred[:0]
	}
	return res, nil
}

// repriceAll is the eager half of runLazyGreedy: it drains the queue, re-measures every surviving
// move against the new accepted set and refills the queue, returning how many evaluations that
// cost. It exists so that config's lazyGreedy=false is a real second implementation to compare
// against rather than a flag which silently changes nothing.
func repriceAll(h *gainHeap, deferred []gainEntry, p greedyProblem, epoch int) int {
	pending := make([]int, 0, h.Len()+len(deferred))
	for h.Len() > 0 {
		pending = append(pending, h.pop().move)
	}
	for _, d := range deferred {
		pending = append(pending, d.move)
	}
	sort.Ints(pending)

	iters := 0
	for _, m := range pending {
		if !p.live(m) {
			continue
		}
		iters++
		h.push(gainEntry{move: m, gain: finiteGain(p.gain(m)), epoch: epoch})
	}
	return iters
}

// maxFiniteGain is the magnitude beyond which finiteGain treats a gain as non-finite. Every real
// objective value here is a weighted sum of coverages in [0,1] minus a lambda-weighted count, so
// nothing legitimate approaches it.
const maxFiniteGain = 1e18

// finiteGain maps a NaN or infinite gain onto zero.
//
// A NaN gain would make gainHeap.Less inconsistent — NaN compares false against everything — and
// an inconsistent Less silently corrupts a heap rather than failing, which would surface as a
// nondeterministic Proposal weeks later. Coverage and Weight are caller-supplied floats, so this
// is a real input rather than a hypothetical one; zero is the honest reading, because a gain
// nobody can order is a gain nobody can justify paying for.
func finiteGain(v float64) float64 {
	if v != v || v > maxFiniteGain || v < -maxFiniteGain {
		return 0
	}
	return v
}

// satur applies contract §3's min(1, ·) saturation, and clamps below at zero so that a negative
// Coverage cannot manufacture value by making some later representation look like an improvement.
func satur(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

// ----------------------------------------------------------------------------------------------
// Representation selection — contract §3.
// ----------------------------------------------------------------------------------------------

// Propose selects at most one representation of each candidate item, spending budget to maximize
// the declared objective of plans/sdd/V5-SP-15/contract.md §3:
//
//	F(S) = sum_i w_i * min(1, sum of Coverage over the chosen representations of item i)
//	       - lambda * redundancy(S)
//
// reported as max(0, F(S)). It is a FREE FUNCTION rather than a method on Selector, and that is a
// deliberate choice with three reasons behind it.
//
// First, Selector is constructed over []Block and its shape is pinned by analyzertest, test/guards
// and test/integration; a Propose method would have to ignore the blocks its receiver was built
// from and read a completely different candidate set out of its arguments, which is a surface that
// misdescribes itself. Second, the consumer contract §1 names is a composition root — internal/
// daemon assembles candidates, calls the selector once and hands the result to rehydrate through a
// rehydrate-local provider — so there is nothing for a long-lived Selector value to hold onto
// between calls. Third, and decisively, a free function can still be made to run BOTH of
// NewSelector's guards, and it does: Propose constructs a Selector over the candidates' Block
// projection and fails with whatever that construction failed with. So §13 invariant 4 (nothing
// before p) and the closing note's priority 3 (no submodular selection before p-selection) hold
// for this surface through the same code, in the same order, as they do for Select — not through
// a copy of the checks that could drift out of step with them.
//
// The guard is deliberately the STRONGER structural form the constructor already implements: a
// candidate positioned before p does not merely fail to be chosen, it refuses the whole call with
// core.ErrBudget. Filtering it out silently would satisfy feasibility rule 4 while hiding a caller
// that is assembling candidates against the wrong compaction point.
//
// Overflow is an OUTCOME, not an error. When a mandatory candidate cannot be carried the caller
// needs the reason and the archive-recovery path, and an error return would throw both away; SP-11
// then takes its explicit overflow branch instead of serializing a partial record. Correspondingly
// no partial selection is ever returned alongside Overflow: Chosen is nil, because a constraint
// set that is only partly satisfied but reported as a success is exactly the failure §12 rates
// High.
//
// NO approximation bound is claimed or claimable here; see runLazyGreedy and contract §3.
func Propose(ctx context.Context, p int, cands []Candidate, lambda float64,
	budget core.Tokens,
) (Proposal, error) {
	if err := ctx.Err(); err != nil {
		return Proposal{}, fmt.Errorf("%w: selection abandoned: %w", core.ErrBudget, err)
	}

	// Both NewSelector guards, on this path, through the constructor itself. The p the solver
	// reasons with is read back off the Selector rather than from the argument, so the value it
	// uses is the one the guards actually validated.
	sel, err := NewSelector(p, candidateBlocks(cands), dag.Slice{}, nil, lambda, true)
	if err != nil {
		return Proposal{}, err
	}

	s := newRepSolver(sel.P(), cands, lambda, budget)

	// Contract §3's overflow test, run BEFORE anything optional is bought: price the cheapest way
	// to carry every binding mandatory candidate, with dependencies shared between them, against
	// the whole budget. That is precisely "after every optional item has been dropped", and doing
	// it first is what makes the answer honest — discovering the overflow after the budget had
	// gone on optional items would report an overflow the selector itself caused.
	if _, offender, ok := s.reserve(newRepState(0), nil, budget); !ok {
		return s.overflow(offender), nil
	}

	res, err := runLazyGreedy(ctx, s.problem(), budget)
	if err != nil {
		return Proposal{}, err
	}

	// A mandatory candidate the greedy never reached — because its own marginal gain was zero, as
	// a zero-weight item's is — is carried here at its cheapest qualified representation. The
	// reserve above kept room for exactly this, so it fits; the second check is not expected to
	// fire, and exists because reporting an overflow is always better than overrunning a budget.
	for i := range s.cands {
		if !s.binding[i] {
			continue
		}
		item := s.cands[i].Item
		if _, ok := s.st.chosen[item]; ok {
			continue
		}
		bundle, cost, ok := s.cheapestBundle(i, s.st)
		if !ok || res.Spent+cost > budget {
			return s.overflow(item), nil
		}
		s.st.apply(bundle)
		res.Spent += cost
	}

	return s.proposal(res.Iters), nil
}

// candidateBlocks projects cands onto the []Block shape NewSelector's guards read. Only ID and Pos
// are consulted by the guards; Tokens carries the candidate's cheapest deliverable price so the
// projection is not merely a shim but an honest, if coarse, Block view of the same item.
func candidateBlocks(cands []Candidate) []Block {
	if len(cands) == 0 {
		return nil
	}
	blocks := make([]Block, 0, len(cands))
	for _, c := range cands {
		var cheapest core.Tokens
		first := true
		for _, r := range c.Reps {
			if r.Kind == RepArchiveOnly {
				continue
			}
			if first || r.AssembledCost < cheapest {
				cheapest, first = r.AssembledCost, false
			}
		}
		blocks = append(blocks, Block{ID: c.Item, Pos: c.Pos, Tokens: cheapest})
	}
	return blocks
}

// repMove is one thing Propose may do: deliver candidate cand at its representation rep, together
// with whatever dependency closure that pulls in.
type repMove struct {
	// cand indexes repSolver.cands.
	cand int
	// rep indexes that candidate's Reps.
	rep int
}

// repState is the mutable half of one Propose call. It is three maps rather than a slice scan
// because every marginal-gain evaluation reads it; none of them is ever RANGED over to build an
// output, which is contract §3's "no result depends on map iteration order" enforced by habit
// rather than by review.
type repState struct {
	// chosen maps an item to the representation it is being delivered at. Its keys ARE the
	// one-representation-per-item constraint: an item already present here has no live moves left.
	chosen map[dag.NodeID]Representation
	// cov accumulates an item's delivered Coverage, before saturation.
	cov map[dag.NodeID]float64
	// roots counts how many chosen representations deliver each ORIGINAL evidence root.
	roots map[core.Hash]int
}

// newRepState returns an empty state sized for n candidates.
func newRepState(n int) *repState {
	return &repState{
		chosen: make(map[dag.NodeID]Representation, n),
		cov:    make(map[dag.NodeID]float64, n),
		roots:  make(map[core.Hash]int, n),
	}
}

// apply commits every representation in bundle.
func (st *repState) apply(bundle []Representation) {
	for _, r := range bundle {
		st.chosen[r.Item] = r
		st.cov[r.Item] += r.Coverage
		if !r.Prov.Root.IsZero() {
			st.roots[r.Prov.Root]++
		}
	}
}

// repSolver holds one Propose call's immutable problem plus its running state.
type repSolver struct {
	// p is the compaction point the constructor validated the candidate set against. It is
	// reported in an overflow Reason so a caller can tell "the budget was too small" apart from
	// "this set was assembled against the wrong prefix".
	p int
	// cands is a normalized copy of the caller's candidate set, sorted by Item ascending.
	cands []Candidate
	// index maps an item back to its position in cands.
	index map[dag.NodeID]int
	// minRep holds, per candidate, the index of its cheapest ALLOWED deliverable representation,
	// or -1 when it has none.
	minRep []int
	// binding marks the candidates whose Mandatory flag actually binds; see bindsAsConstraint.
	binding []bool
	// moves is every legal (candidate, representation) pair, ordered by (Item asc, Kind asc).
	moves []repMove
	// dead marks the moves whose dependency closure can never be satisfied.
	dead []bool
	// lambda is the redundancy penalty.
	lambda float64
	// budget is the token budget the proposal has to fit inside.
	budget core.Tokens
	// st is the running selection.
	st *repState
}

// newRepSolver normalizes cands and precomputes everything the greedy loop needs.
//
// Normalization is what makes determinism structural rather than incidental. Candidates are sorted
// by Item ascending and each candidate's representations by (Kind asc, AssembledCost asc, Coverage
// desc, input order), so the move set's index order IS contract §3's tie-break; and each
// representation's Requires is copied and sorted, so a caller that hands over an unsorted
// dependency list gets the same Proposal as one that does not. The copies also mean this package
// never mutates a caller's slice, which matters because a candidate set is assembled once by the
// daemon and may be proposed over more than once.
func newRepSolver(p int, cands []Candidate, lambda float64, budget core.Tokens) *repSolver {
	s := &repSolver{
		p:      p,
		cands:  make([]Candidate, 0, len(cands)),
		index:  make(map[dag.NodeID]int, len(cands)),
		lambda: lambda,
		budget: budget,
		st:     newRepState(len(cands)),
	}

	for _, c := range cands {
		norm := c
		norm.Reps = make([]Representation, len(c.Reps))
		for i, r := range c.Reps {
			r.Requires = append([]dag.NodeID(nil), r.Requires...)
			sort.Slice(r.Requires, func(a, b int) bool { return r.Requires[a] < r.Requires[b] })
			norm.Reps[i] = r
		}
		sort.SliceStable(norm.Reps, func(a, b int) bool { return repLess(norm.Reps[a], norm.Reps[b]) })
		s.cands = append(s.cands, norm)
	}
	sort.SliceStable(s.cands, func(a, b int) bool { return s.cands[a].Item < s.cands[b].Item })

	for i, c := range s.cands {
		if _, dup := s.index[c.Item]; !dup {
			s.index[c.Item] = i
		}
	}

	s.binding = make([]bool, len(s.cands))
	s.minRep = make([]int, len(s.cands))
	for i := range s.cands {
		s.binding[i] = bindsAsConstraint(s.cands[i])
		s.minRep[i] = -1
		for j := range s.cands[i].Reps {
			if !s.allowed(i, j) {
				continue
			}
			if s.minRep[i] < 0 ||
				s.cands[i].Reps[j].AssembledCost < s.cands[i].Reps[s.minRep[i]].AssembledCost {
				s.minRep[i] = j
			}
		}
	}

	for i := range s.cands {
		for j := range s.cands[i].Reps {
			if s.allowed(i, j) {
				s.moves = append(s.moves, repMove{cand: i, rep: j})
			}
		}
	}

	s.dead = make([]bool, len(s.moves))
	empty := newRepState(len(s.cands))
	for m := range s.moves {
		if _, _, ok := s.bundleFor(s.moves[m], empty); !ok {
			s.dead[m] = true
		}
	}
	return s
}

// repLess is the total order representations of one item are held in: most faithful kind first,
// then cheapest, then highest coverage. Kind leads because contract §3 names Kind as the
// tie-break's secondary key, and RepresentationKind is declared most-faithful-first precisely so
// that a tie between two ways of delivering one item resolves toward the more faithful one.
func repLess(a, b Representation) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.AssembledCost != b.AssembledCost {
		return a.AssembledCost < b.AssembledCost
	}
	return a.Coverage > b.Coverage
}

// bindsAsConstraint reports whether c's Mandatory flag may act as a binding constraint, and it is
// where G6.3 is decided.
//
// Mandatory alone is not enough. An elimination is only as authoritative as the evidence behind
// it, so a candidate binds only when it can be delivered at a representation whose own Provenance
// is Qualification.Active() — QualCurrent. A Mandatory candidate carrying only QualStale or
// QualUncertain evidence is still selected on its merits and still travels with its qualification,
// but it never forces an overflow and never displaces a current record: promoting it would turn a
// recorded "we can no longer establish this" into a hard "do not try this", which is the false
// "already tried" that inverts negative knowledge from asset to liability (§12, rated High).
//
// The same rule is why allowed() restricts a BINDING candidate to its active representations:
// carrying an authoritative constraint at stale evidence would deliver something the consumer must
// then, correctly, decline to treat as binding — so the constraint would be silently lost in
// transit while the proposal reported success.
func bindsAsConstraint(c Candidate) bool {
	if !c.Mandatory {
		return false
	}
	for _, r := range c.Reps {
		if r.Kind != RepArchiveOnly && r.Prov.Qualification.Active() {
			return true
		}
	}
	return false
}

// allowed reports whether representation j of candidate i is a legal choice.
//
// RepArchiveOnly is never a choice: it delivers nothing, so putting it in Chosen would report an
// injection that did not happen and would make Tokens describe content nobody will read. An item
// whose outcome is archive-only is named in Proposal.Archive instead, which is the field that
// exists to say "not injected, still recoverable" out loud.
func (s *repSolver) allowed(i, j int) bool {
	r := s.cands[i].Reps[j]
	if r.Kind == RepArchiveOnly {
		return false
	}
	return !s.binding[i] || r.Prov.Qualification.Active()
}

// weightOf returns item's objective weight, clamped at zero. Contract §2 declares Weight >= 0;
// clamping rather than trusting means a negative weight cannot make dropping an item look like a
// gain, which is how one malformed candidate would otherwise empty a whole proposal.
func (s *repSolver) weightOf(item dag.NodeID) float64 {
	i, ok := s.index[item]
	if !ok {
		return 0
	}
	if w := s.cands[i].Weight; w > 0 {
		return w
	}
	return 0
}

// bundleFor expands move m into everything that must be delivered together with it: the chosen
// representation, plus the cheapest deliverable representation of every required item that is not
// already chosen.
//
// Feasibility rule 2 says every Requires entry must be "itself chosen or already present". An
// entry naming an item that is NOT in the candidate set is read as already present — it is content
// the prefix still holds, which is the only reading under which a candidate set assembled from a
// suffix can ever be feasible. A required item that IS a candidate but has no deliverable
// representation makes the whole move infeasible, and newRepSolver marks it dead once rather than
// rediscovering it on every evaluation.
//
// A dependency is deliberately carried at its CHEAPEST representation rather than its best one: it
// is carried for the dependent's sake, so the selector pays the least it can for it. That is a
// documented heuristic choice, not an optimum — the exact comparisons in objective_test.go are
// where its cost is measured rather than assumed.
func (s *repSolver) bundleFor(m repMove, st *repState) ([]Representation, core.Tokens, bool) {
	main := s.cands[m.cand].Reps[m.rep]
	picked := map[dag.NodeID]bool{main.Item: true}
	out := []Representation{main}
	queue := append([]dag.NodeID(nil), main.Requires...)

	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		if picked[d] {
			continue
		}
		if _, ok := st.chosen[d]; ok {
			continue
		}
		di, ok := s.index[d]
		if !ok {
			continue // not a candidate: already present in the prefix
		}
		ri := s.minRep[di]
		if ri < 0 {
			return nil, 0, false
		}
		r := s.cands[di].Reps[ri]
		picked[d] = true
		out = append(out, r)
		queue = append(queue, r.Requires...)
	}

	var cost core.Tokens
	for _, r := range out {
		cost += r.AssembledCost
	}
	return out, cost, true
}

// cheapestBundle expands candidate i at its cheapest allowed representation.
func (s *repSolver) cheapestBundle(i int, st *repState) ([]Representation, core.Tokens, bool) {
	if s.minRep[i] < 0 {
		return nil, 0, false
	}
	return s.bundleFor(repMove{cand: i, rep: s.minRep[i]}, st)
}

// gainOf is the marginal objective gain of adding bundle to st.
//
// It is computed incrementally instead of as value(after) - value(before) for a reason that is
// about determinism rather than speed: a full recomputation would have to sum coverage over the
// whole chosen set, and floating-point addition is not associative, so the answer would depend on
// the order that set happened to be walked in. Summing only the bundle — in the bundle's own fixed
// order — makes every evaluation reproducible bit for bit.
func (s *repSolver) gainOf(bundle []Representation, st *repState) float64 {
	var g float64
	for _, r := range bundle {
		old := st.cov[r.Item]
		g += s.weightOf(r.Item) * (satur(old+r.Coverage) - satur(old))
	}

	// Redundancy is the count of chosen representations that re-deliver an ORIGINAL evidence root
	// something else already delivers. Prov.Root names the first observation rather than the
	// derivative's own bytes (contract §2), so two capsules of one tool result count as one
	// duplicate even though their contents differ — which is the waste §5.12 asks the selector to
	// price. A zero Root means "no recorded evidence root" and never duplicates anything.
	seen := make(map[core.Hash]int, len(bundle))
	dup := 0
	for _, r := range bundle {
		if r.Prov.Root.IsZero() {
			continue
		}
		if st.roots[r.Prov.Root]+seen[r.Prov.Root] > 0 {
			dup++
		}
		seen[r.Prov.Root]++
	}
	return g - s.lambda*float64(dup)
}

// reserve prices the cheapest way to carry every binding mandatory candidate st has not already
// carried, sharing dependencies between them, and reports the first candidate at which that
// becomes impossible within limit.
//
// extra names the items a hypothetical move is about to deliver, so the greedy can ask "if I buy
// this now, can every mandatory record still be carried afterwards?" — the question that turns
// contract §3's overflow rule into something the loop can enforce while it spends rather than
// discover once it has finished.
func (s *repSolver) reserve(st *repState, extra map[dag.NodeID]bool, limit core.Tokens,
) (core.Tokens, dag.NodeID, bool) {
	have := make(map[dag.NodeID]bool, len(st.chosen)+len(extra))
	for id := range st.chosen {
		have[id] = true
	}
	for id := range extra {
		have[id] = true
	}

	var total core.Tokens
	for i := range s.cands {
		item := s.cands[i].Item
		if !s.binding[i] || have[item] {
			continue
		}
		cost, ok := s.priceInto(i, have)
		if !ok {
			return total, item, false
		}
		total += cost
		if total > limit {
			return total, item, false
		}
	}
	return total, "", true
}

// priceInto adds candidate i's cheapest closure to have and returns what it cost, rolling the
// additions back and reporting false when some required item cannot be delivered at all. Marking
// the items into have is what makes a dependency shared between two mandatory records counted
// once; charging it twice would manufacture an overflow the budget does not actually have.
func (s *repSolver) priceInto(i int, have map[dag.NodeID]bool) (core.Tokens, bool) {
	ri := s.minRep[i]
	if ri < 0 {
		return 0, false
	}
	main := s.cands[i].Reps[ri]
	added := []dag.NodeID{main.Item}
	have[main.Item] = true
	cost := main.AssembledCost
	queue := append([]dag.NodeID(nil), main.Requires...)

	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		if have[d] {
			continue
		}
		di, ok := s.index[d]
		if !ok {
			continue
		}
		dri := s.minRep[di]
		if dri < 0 {
			for _, id := range added {
				delete(have, id)
			}
			return 0, false
		}
		r := s.cands[di].Reps[dri]
		have[d] = true
		added = append(added, d)
		cost += r.AssembledCost
		queue = append(queue, r.Requires...)
	}
	return cost, true
}

// problem binds the solver's state into the engine's closures.
func (s *repSolver) problem() greedyProblem {
	return greedyProblem{
		moves: len(s.moves),
		lazy:  true,
		live: func(i int) bool {
			if s.dead[i] {
				return false
			}
			_, taken := s.st.chosen[s.cands[s.moves[i].cand].Item]
			return !taken
		},
		gain: func(i int) float64 {
			bundle, _, ok := s.bundleFor(s.moves[i], s.st)
			if !ok {
				return 0
			}
			return s.gainOf(bundle, s.st)
		},
		cost: func(i int) core.Tokens {
			_, cost, ok := s.bundleFor(s.moves[i], s.st)
			if !ok {
				return s.budget + 1
			}
			return cost
		},
		afford: func(i int, cost, spent core.Tokens) bool {
			bundle, _, ok := s.bundleFor(s.moves[i], s.st)
			if !ok {
				return false
			}
			extra := make(map[dag.NodeID]bool, len(bundle))
			for _, r := range bundle {
				extra[r.Item] = true
			}
			remaining := s.budget - spent - cost
			if remaining < 0 {
				return false
			}
			_, _, ok = s.reserve(s.st, extra, remaining)
			return ok
		},
		accept: func(i int) {
			if bundle, _, ok := s.bundleFor(s.moves[i], s.st); ok {
				s.st.apply(bundle)
			}
		},
	}
}

// proposal assembles the final Proposal out of the solver's state.
//
// Chosen is built by walking cands in Item order rather than by ranging over the chosen map, and
// it is left nil when nothing was selected: contract §3 says a zero or tiny budget yields
// Chosen == nil, and an empty non-nil slice would compare unequal to that in a determinism test
// while meaning exactly the same thing.
func (s *repSolver) proposal(iters int) Proposal {
	var chosen []Representation
	var archive []dag.NodeID
	for i := range s.cands {
		c := s.cands[i]
		if r, ok := s.st.chosen[c.Item]; ok {
			chosen = append(chosen, r)
			continue
		}
		// Not injected. If the caller offered an archive-only representation of this item then the
		// outcome is recoverable and has to be reported as such: an item that quietly vanishes
		// between the candidate set and the proposal is the silent drop /qompack:dropped exists to
		// make impossible (G4.5).
		for _, r := range c.Reps {
			if r.Kind == RepArchiveOnly {
				archive = append(archive, c.Item)
				break
			}
		}
	}

	var tokens core.Tokens
	for _, r := range chosen {
		tokens += r.AssembledCost
	}
	return Proposal{
		Chosen:  chosen,
		Tokens:  tokens,
		Value:   s.valueOf(chosen),
		Archive: archive,
		Iters:   iters,
	}
}

// valueOf evaluates contract §3's objective at chosen, reported as max(0, F).
//
// The clamp is not cosmetic. The greedy never accepts a non-positive gain, so anything it chose on
// its own keeps F at or above zero; but a mandatory record is carried whether or not it pays for
// itself, and a zero-weight mandatory item that duplicates an evidence root can push the raw
// objective below zero. Reporting a negative Value would invite a caller to compare it against
// another proposal's and conclude the selection was worse than selecting nothing, which is not
// what a forced constraint means.
func (s *repSolver) valueOf(chosen []Representation) float64 {
	var cov float64
	for i := 0; i < len(chosen); {
		j := i
		var sum float64
		for j < len(chosen) && chosen[j].Item == chosen[i].Item {
			sum += chosen[j].Coverage
			j++
		}
		cov += s.weightOf(chosen[i].Item) * satur(sum)
		i = j
	}

	roots := make(map[core.Hash]int, len(chosen))
	dup := 0
	for _, r := range chosen {
		if r.Prov.Root.IsZero() {
			continue
		}
		if roots[r.Prov.Root] > 0 {
			dup++
		}
		roots[r.Prov.Root]++
	}

	v := cov - s.lambda*float64(dup)
	if v < 0 {
		return 0
	}
	return v
}

// overflow builds the explicit overflow outcome of contract §3 for item.
//
// Nothing is selected. A proposal that carried the items which happened to fit while a mandatory
// record did not would be a partial serialization presented as a result, and the consumer would
// have to notice the Overflow flag to avoid injecting it; SP-11's overflow branch wants the reason
// and the recovery path, not a half-filled record it must then unpick. Archive names the same item
// Reason does, so the two can never disagree about which constraint was lost.
func (s *repSolver) overflow(item dag.NodeID) Proposal {
	return Proposal{
		Archive:  []dag.NodeID{item},
		Overflow: true,
		Item:     item,
		Reason: fmt.Sprintf(
			"mandatory item %s cannot be carried at any qualified representation within %d tokens at p=%d",
			item, s.budget, s.p),
	}
}
