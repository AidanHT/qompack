package eval

import (
	"context"
	"sort"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// Belady solver defaults.
//
// Granularity is the token quantum weights are scaled by before the DP; 256 keeps a 40K budget to
// 156 capacity columns, which is what makes the exact solver affordable on a 400-turn session.
// MaxDPCells is the point past which the exact solver is abandoned for the 1/2-approximation.
const (
	defaultGranularity core.Tokens = 256
	defaultMaxDPCells  int         = 20_000_000
)

// DefaultBeladyOptions is the configuration Harness.Belady uses.
func DefaultBeladyOptions() BeladyOptions {
	return BeladyOptions{K: DefaultHorizonK, Granularity: defaultGranularity, MaxDPCells: defaultMaxDPCells}
}

// optItem is one candidate block, reduced to what the solver needs.
type optItem struct {
	id    string
	value int         // demands this block satisfies over the horizon
	w     int         // weight scaled by Granularity, rounded up
	tok   core.Tokens // true weight
	pos   int         // position in the original prefix, for p_min
}

// BeladyDetail computes the retrospective clairvoyant keep-set for a compaction at turn at, and
// reports how it was arrived at.
//
// Clairvoyance means the demand sequence over the horizon is known, so with C = Blocks(s, at) and
// D = Demands(s, at, at+K):
//
//	maximize   Σ_{b ∈ S} |{d ∈ D : d.BlockID == b.ID}|
//	subject to Σ_{b ∈ S} b.Tokens ≤ budget
//
// which is 0/1 knapsack. With unit weights and a slot budget it degenerates to classic Belady —
// keep what is demanded most — so this is the generalization of §6.10's ceiling to heterogeneous
// block sizes rather than a substitute for it.
//
// The result is deterministic: items are ordered by (−value, weight, ID) before the DP and the
// improvement test is strict, so two runs on the same session always produce the identical
// keep-set. That is what lets a baseline number be compared across commits at all.
func BeladyDetail(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens,
	o BeladyOptions,
) (KeepSet, OPTDetail, error) {
	if err := ctx.Err(); err != nil {
		return KeepSet{}, OPTDetail{}, err
	}
	if o.Granularity <= 0 {
		o.Granularity = defaultGranularity
	}
	if o.K <= 0 {
		o.K = DefaultHorizonK
	}
	if o.MaxDPCells <= 0 {
		o.MaxDPCells = defaultMaxDPCells
	}
	if budget <= 0 {
		return KeepSet{}, OPTDetail{Exact: true, BudgetTokens: budget}, nil
	}

	blocks := Blocks(s, at)
	to := at + core.TurnIndex(o.K)
	if int(to) > len(s.Turns) {
		to = core.TurnIndex(len(s.Turns))
	}

	value := make(map[string]int)
	for _, d := range Demands(s, at, to) {
		value[d.BlockID]++
	}

	// Every block nobody demanded can never help, and dropping them is what keeps the DP small
	// enough to stay exact on a real session.
	seen := make(map[string]bool, len(value))
	cands := make([]optItem, 0, len(value))
	for _, b := range blocks {
		v := value[b.ID]
		if v == 0 || seen[b.ID] {
			continue
		}
		seen[b.ID] = true
		cands = append(cands, optItem{id: b.ID, value: v, tok: b.Tokens, pos: b.Pos})
	}
	detail := OPTDetail{Candidates: len(cands), BudgetTokens: budget}

	// Scaling rounds weights UP, so the returned keep-set is never over the true budget.
	capacity := int(budget / o.Granularity)
	items := make([]optItem, 0, len(cands))
	for _, it := range cands {
		it.w = int((it.tok + o.Granularity - 1) / o.Granularity)
		if it.w > capacity {
			continue // cannot fit even alone
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].value != items[j].value {
			return items[i].value > items[j].value
		}
		if items[i].tok != items[j].tok {
			return items[i].tok < items[j].tok
		}
		return items[i].id < items[j].id
	})
	detail.DPCells = len(items) * (capacity + 1)

	var chosen []optItem
	switch {
	case len(items) == 0:
		detail.Exact = true
	case detail.DPCells > o.MaxDPCells:
		chosen = approxKnapsack(items, capacity)
	default:
		var err error
		if chosen, err = exactKnapsack(ctx, items, capacity); err != nil {
			return KeepSet{}, OPTDetail{}, err
		}
		detail.Exact = true
	}

	kept := make(map[string]bool, len(chosen))
	ids := make([]string, 0, len(chosen))
	var tokens core.Tokens
	for _, it := range chosen {
		kept[it.id] = true
		ids = append(ids, it.id)
		tokens += it.tok
		detail.Value += it.value
	}
	sort.Strings(ids)

	return KeepSet{IDs: ids, Tokens: tokens, P: pMin(cands, kept, blocks)}, detail, nil
}

// pMin is §5.2's p: the earliest position among the candidates this keep-set dropped, or the whole
// prefix when it dropped none.
//
// It is measured over the candidate set rather than over every block, and that is a deliberate
// modelling choice. Turn blocks are never demanded, so a block at position 0 is always unkept and
// a whole-prefix reading would pin p_min at 0 for every policy, making the rewrite metric constant
// and therefore useless for comparing policies. Measured over the candidates, p_min answers the
// question §5.2 actually asks — how far back does the rewrite have to start because of what this
// compaction chose to drop — and stock still reports 0, honestly, because a Full Compact rewrites
// the whole message array regardless.
func pMin(cands []optItem, kept map[string]bool, blocks []Block) int {
	p := -1
	for _, it := range cands {
		if kept[it.id] {
			continue
		}
		if p < 0 || it.pos < p {
			p = it.pos
		}
	}
	if p >= 0 {
		return p
	}
	return prefixTokens(blocks)
}

// prefixTokens is n: the position-advancing tokens of a prefix, and the n of §5.2's
// cost = w·(n − p_min).
func prefixTokens(blocks []Block) int {
	n := 0
	for _, b := range blocks {
		if isPositionAdvancing(b.Kind) {
			n += int(b.Tokens)
		}
	}
	return n
}

// exactKnapsack is the standard 1-D DP with a bitset of take decisions, which is what keeps the
// reconstruction exact without a full O(items × capacity) int table.
//
// take bit (i, j) records that item i strictly improved the optimum at capacity j when it was
// processed — equivalently that item i belongs to an optimal solution over items 0..i at capacity
// j — so walking i downward from the last item reconstructs one optimal set.
func exactKnapsack(ctx context.Context, items []optItem, capacity int) ([]optItem, error) {
	words := (capacity + 1 + 63) / 64
	dp := make([]int32, capacity+1)
	take := make([]uint64, len(items)*words)

	for i, it := range items {
		// Re-checked once per item so a cancelled 20M-cell run unwinds promptly.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for j := capacity; j >= it.w; j-- {
			if v := dp[j-it.w] + int32(it.value); v > dp[j] {
				dp[j] = v
				take[i*words+j/64] |= 1 << uint(j%64)
			}
		}
	}

	out := make([]optItem, 0, len(items))
	j := capacity
	for i := len(items) - 1; i >= 0; i-- {
		if take[i*words+j/64]&(1<<uint(j%64)) != 0 {
			out = append(out, items[i])
			j -= items[i].w
		}
	}
	return out, nil
}

// approxKnapsack is the textbook 1/2-approximation: greedy by value density, compared against the
// single best-value item that fits alone. It runs only when the exact DP would exceed MaxDPCells,
// and OPTDetail.Exact reports that it did, so a degraded ceiling is never mistaken for the real
// one — the gate WARNs and names the session.
func approxKnapsack(items []optItem, capacity int) []optItem {
	byDensity := make([]optItem, len(items))
	copy(byDensity, items)
	sort.Slice(byDensity, func(a, b int) bool {
		x, y := byDensity[a], byDensity[b]
		// A zero-weight item is free, so it always leads.
		if (x.w == 0) != (y.w == 0) {
			return x.w == 0
		}
		if x.w != 0 && y.w != 0 {
			if lx, ly := x.value*y.w, y.value*x.w; lx != ly {
				return lx > ly
			}
		}
		if x.w != y.w {
			return x.w < y.w
		}
		return x.id < y.id
	})

	greedy := make([]optItem, 0, len(byDensity))
	used, greedyValue := 0, 0
	for _, it := range byDensity {
		if used+it.w > capacity {
			continue
		}
		greedy = append(greedy, it)
		used += it.w
		greedyValue += it.value
	}

	best, bestValue := optItem{}, 0
	for _, it := range items {
		if it.value > bestValue || (it.value == bestValue && it.id < best.id) {
			best, bestValue = it, it.value
		}
	}
	if bestValue > greedyValue {
		return []optItem{best}
	}
	return greedy
}

// Belady computes the retrospective OPT keep-set with the default options, discarding the detail.
func (h *harness) Belady(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error) {
	defer h.observe("eval.belady.ms", h.clock.Now())
	ks, _, err := BeladyDetail(ctx, s, at, budget, DefaultBeladyOptions())
	return ks, err
}

// oraclePolicy is the ceiling as a Policy, so the harness can score it through exactly the same
// path as every other policy. Its FractionOfOPT must be 1.0 on every session, which is how the
// scorer proves it is self-consistent.
type oraclePolicy struct{}

// NewOraclePolicy returns the clairvoyant ceiling policy.
func NewOraclePolicy(config.Config) Policy { return oraclePolicy{} }

func (oraclePolicy) Name() string { return "oracle" }

func (oraclePolicy) KeepSet(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error) {
	ks, _, err := BeladyDetail(ctx, s, at, budget, DefaultBeladyOptions())
	return ks, err
}

func init() { RegisterPolicy("oracle", NewOraclePolicy) }
