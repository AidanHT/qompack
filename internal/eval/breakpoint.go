package eval

import (
	"context"
	"math"
	"sort"

	"github.com/qompack/qompack/internal/core"
)

// maxBreakpointCandidates bounds the O(markers · C²) DP below. A session that offers more turn
// boundaries than this is strided down to it, and BreakpointPlan.Candidates reports how many
// survived, so the reduction is disclosed rather than silent.
const maxBreakpointCandidates = 256

// BreakpointOPT computes the optimal cache_control marker placement for a session.
//
// This is measurement-and-port material, not a feature. §5.6 and §12 both state that Claude Code
// manages its own cache markers and a plugin cannot place or move them; the analysis is retained
// because it applies verbatim if Qompack is ever ported to a first-party harness on the Messages
// API, and because the Belady setup makes it measurable today. Every plan therefore carries
// NotPluginActionable, and the gate prints it on the line above the number.
//
// The decision variable is a marker set B, and the objective is the cached prefix every API call
// gets to reuse:
//
//	value(B) = Σ_t max{ q ∈ B ∪ {0} : q ≤ cap_t }
//
// where cap_t is how far turn t's cached prefix could possibly extend: its own prefix length,
// or the earliest position edited since the previous call, whichever is smaller.
func BreakpointOPT(s Session, markers int) (BreakpointPlan, error) {
	cand := candidatePositions(s)
	caps, err := breakpointCaps(s)
	if err != nil {
		return BreakpointPlan{}, err
	}
	return breakpointPlan(caps, cand, markers), nil
}

// turnPrefixTokens returns, for each turn index t, the position-advancing tokens preceding it.
// The final entry is the whole prefix, so the slice has len(s.Turns)+1 entries.
func turnPrefixTokens(s Session) []int {
	per := make([]int, len(s.Turns))
	for _, b := range Blocks(s, core.TurnIndex(len(s.Turns))) {
		if isPositionAdvancing(b.Kind) && int(b.Turn) >= 0 && int(b.Turn) < len(per) {
			per[b.Turn] += int(b.Tokens)
		}
	}
	out := make([]int, len(s.Turns)+1)
	run := 0
	for i := range s.Turns {
		out[i] = run
		run += per[i]
	}
	out[len(s.Turns)] = run
	return out
}

// candidatePositions returns the deduplicated, ascending turn-boundary positions a marker could
// occupy, always including 0, capped at maxBreakpointCandidates by an even stride.
func candidatePositions(s Session) []int {
	prefix := turnPrefixTokens(s)
	out := make([]int, 0, len(prefix))
	for i, p := range prefix {
		if i == 0 || p != prefix[i-1] {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = []int{0}
	}
	if len(out) <= maxBreakpointCandidates {
		return out
	}
	stride := (len(out) + maxBreakpointCandidates - 1) / maxBreakpointCandidates
	strided := []int{out[0]}
	for i := stride; i < len(out)-1; i += stride {
		strided = append(strided, out[i])
	}
	return append(strided, out[len(out)-1])
}

// breakpointCaps returns one cap per API call — that is, per assistant turn.
//
// A cap is the prefix length at that turn, lowered to the earliest position edited since the
// previous call. A compaction is the only edit a logged session records, and it contributes the
// stock policy's own p_min, which is 0: a Full Compact rewrites the whole message array, so the
// first call after one can reuse nothing. Taking that number from stockPolicy rather than writing
// a 0 here keeps the two definitions from drifting apart.
func breakpointCaps(s Session) ([]int, error) {
	prefix := turnPrefixTokens(s)
	compactionFloor := make(map[int]int, len(s.CompactionAt))
	for _, a := range s.CompactionAt {
		ks, err := (stockPolicy{}).KeepSet(context.Background(), s, a, DefaultKeepBudget)
		if err != nil {
			return nil, err
		}
		compactionFloor[int(a)] = ks.P
	}

	caps := make([]int, 0, len(s.Turns))
	earliestEdit := math.MaxInt // no edit since the previous call
	for i, turn := range s.Turns {
		if floor, ok := compactionFloor[i]; ok && floor < earliestEdit {
			earliestEdit = floor
		}
		if turn.Role != roleAssistant {
			continue
		}
		caps = append(caps, min(prefix[i], earliestEdit))
		earliestEdit = math.MaxInt
	}
	return caps, nil
}

// negInf marks an unreachable DP state: fewer than j−1 candidates lie below index i.
const negInf = math.MinInt64 / 4

// breakpointPlan is the exact DP over a marker budget, separated from session derivation so a
// test can state an instance in the terms the algorithm reasons about.
//
// g[j][i] is the best value using exactly j markers whose largest is cand[i]. Extending a
// (j−1)-marker set whose largest is cand[k] with a higher marker cand[i] moves every cap at or
// above cand[i] from cand[k] to cand[i], which is the correction term that makes the recurrence
// exact rather than merely plausible.
func breakpointPlan(caps, cand []int, markers int) BreakpointPlan {
	plan := BreakpointPlan{Markers: markers, Candidates: len(cand), Note: NotPluginActionable}
	if markers < 0 {
		plan.Markers = 0
		markers = 0
	}
	markers = min(markers, len(cand))
	if markers == 0 || len(caps) == 0 {
		return plan
	}

	sorted := make([]int, len(caps))
	copy(sorted, caps)
	sort.Ints(sorted)
	// atOrAbove counts the caps a marker at q would serve.
	atOrAbove := func(q int) int64 {
		return int64(len(sorted) - sort.SearchInts(sorted, q))
	}

	g := make([][]int64, markers+1)
	parent := make([][]int, markers+1)
	for j := range g {
		g[j] = make([]int64, len(cand))
		parent[j] = make([]int, len(cand))
		for i := range g[j] {
			g[j][i] = negInf
			parent[j][i] = -1
		}
	}
	for i, q := range cand {
		g[1][i] = int64(q) * atOrAbove(q)
	}
	for j := 2; j <= markers; j++ {
		for i, q := range cand {
			served := atOrAbove(q)
			best := int64(negInf)
			for k := range i {
				if g[j-1][k] == negInf {
					continue
				}
				v := g[j-1][k] - int64(cand[k])*served + int64(q)*served
				if v > best {
					best, parent[j][i] = v, k
				}
			}
			g[j][i] = best
		}
	}

	// Ascending j then ascending i with a strict improvement test makes the tie-break
	// deterministic: the fewest markers, then the earliest position.
	bestVal, bestJ, bestI := int64(negInf), 0, -1
	for j := 1; j <= markers; j++ {
		for i := range cand {
			if g[j][i] != negInf && g[j][i] > bestVal {
				bestVal, bestJ, bestI = g[j][i], j, i
			}
		}
	}
	if bestI < 0 {
		return plan
	}

	positions := make([]int, 0, bestJ)
	for j, i := bestJ, bestI; i >= 0; {
		positions = append(positions, cand[i])
		if j == 1 {
			break
		}
		j, i = j-1, parent[j][i]
	}
	sort.Ints(positions)
	// Position 0 is the implicit q = 0 baseline every call already gets, never a reported marker.
	if len(positions) > 0 && positions[0] == 0 {
		positions = positions[1:]
	}
	if len(positions) == 0 {
		positions = nil
	}

	plan.Positions = positions
	plan.CachedReads = bestVal
	return plan
}
