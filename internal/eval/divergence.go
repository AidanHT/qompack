package eval

import (
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Compare computes §4.2's five bullets between the logged branch and the compacted one: the
// empirical estimator of D that the whole project rests on.
//
// The horizon — which turns count — is read off the COMPACTED run, never the baseline, so a replay
// with a non-default K needs no change on BaselineRun's side. A session that never compacted has
// no branches to compare and returns the identity rather than a zeroed struct, which would read as
// total drift.
func (h *harness) Compare(uncompacted, compacted Run) Divergence {
	k := compacted.Horizon
	if k <= 0 {
		k = DefaultHorizonK
	}
	if compacted.FirstCompactionTurn < 0 {
		return Divergence{
			FirstDivergenceTurn:  k,
			FileSetJaccard:       1,
			SameDecision:         true,
			DecisionPreservation: 1,
		}
	}
	at := compacted.FirstCompactionTurn

	uncHorizon := horizonActions(uncompacted.Actions, at, k)
	cmpHorizon := horizonActions(compacted.Actions, at, k)

	return Divergence{
		FirstDivergenceTurn:  firstDivergence(uncompacted.Actions, compacted.Actions, at, k),
		FileSetJaccard:       jaccard(pathSet(uncHorizon), pathSet(cmpHorizon)),
		ToolEditDistance:     levenshtein(toolNames(uncHorizon), toolNames(cmpHorizon)),
		SameDecision:         lastDecision(uncHorizon) == lastDecision(cmpHorizon),
		DecisionPreservation: decisionPreservation(uncHorizon, cmpHorizon),
		RedundantReads: redundantReads(compacted.Actions, at, k) -
			redundantReads(uncompacted.Actions, at, k),
		ReAttempts: countTool(cmpHorizon, toolReAttempt) - countTool(uncHorizon, toolReAttempt),
	}
}

// horizonActions returns the actions falling in [at+1, at+k], the §4.2 "next K actions".
func horizonActions(actions []Action, at core.TurnIndex, k int) []Action {
	lo, hi := at+1, at+core.TurnIndex(k)
	var out []Action
	for _, a := range actions {
		if a.Turn >= lo && a.Turn <= hi {
			out = append(out, a)
		}
	}
	return out
}

// firstDivergence reports how many turns after the compaction the branches first disagree, and k
// when they never do.
//
// k rather than -1 is deliberate: the metric is DirHigherBetter, so "never diverged" must be the
// largest value it can take, or the 2% gate would score a perfect policy as the worst one.
func firstDivergence(unc, cmp []Action, at core.TurnIndex, k int) int {
	n := min(len(unc), len(cmp))
	idx := -1
	for i := range n {
		if !sameAction(unc[i], cmp[i]) {
			idx = i
			break
		}
	}
	if idx < 0 {
		if len(unc) == len(cmp) {
			return k // identical everywhere
		}
		idx = n // one branch is a strict prefix of the other
	}

	var turn core.TurnIndex
	switch {
	case idx < len(cmp):
		turn = cmp[idx].Turn
	case idx < len(unc):
		turn = unc[idx].Turn
	default:
		return k
	}
	return min(max(int(turn)-int(at), 0), k)
}

// sameAction is the (Tool, Paths, Decision) equality the divergence point is defined on.
func sameAction(a, b Action) bool {
	if a.Tool != b.Tool || a.Decision != b.Decision || len(a.Paths) != len(b.Paths) {
		return false
	}
	for i := range a.Paths {
		if paths.Key(a.Paths[i]) != paths.Key(b.Paths[i]) {
			return false
		}
	}
	return true
}

// pathSet is the set of files an action range touched, keyed the way the store keys them.
func pathSet(actions []Action) map[string]bool {
	out := make(map[string]bool)
	for _, a := range actions {
		for _, p := range a.Paths {
			if p != "" {
				out[paths.Key(p)] = true
			}
		}
	}
	return out
}

// jaccard is |A∩B| / |A∪B|, with two empty sets agreeing perfectly.
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}

// toolNames projects an action range onto the tool-call sequence the edit distance runs over.
func toolNames(actions []Action) []string {
	out := make([]string, len(actions))
	for i, a := range actions {
		out[i] = a.Tool
	}
	return out
}

// levenshtein is the unit-cost edit distance, two rolling rows, O(n·m) time and O(min(n,m)) space.
func levenshtein(a, b []string) int {
	if len(a) < len(b) {
		a, b = b, a
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(min(curr[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// lastDecision is the final decision an action range reached, or "" when it reached none.
func lastDecision(actions []Action) string {
	for i := len(actions) - 1; i >= 0; i-- {
		if actions[i].Decision != "" {
			return actions[i].Decision
		}
	}
	return ""
}

// decisionPreservation is the fraction of the logged branch's decisions the compacted branch still
// reached. With nothing to lose, nothing was lost.
func decisionPreservation(unc, cmp []Action) float64 {
	want := make(map[string]bool)
	for _, a := range unc {
		if a.Decision != "" {
			want[a.Decision] = true
		}
	}
	if len(want) == 0 {
		return 1
	}
	got := make(map[string]bool)
	for _, a := range cmp {
		if a.Decision != "" && want[a.Decision] {
			got[a.Decision] = true
		}
	}
	return float64(len(got)) / float64(len(want))
}

// redundantReads counts reads inside the horizon whose path an EARLIER action of the same branch
// already touched — including actions before the compaction, which is exactly the point: a re-read
// is redundant because the content was already in the transcript once.
func redundantReads(actions []Action, at core.TurnIndex, k int) int {
	lo, hi := at+1, at+core.TurnIndex(k)
	seen := make(map[string]bool)
	count := 0
	for _, a := range actions {
		inHorizon := a.Turn >= lo && a.Turn <= hi
		repeat := false
		for _, p := range a.Paths {
			key := paths.Key(p)
			if key == "" {
				continue
			}
			if seen[key] {
				repeat = true
			}
			seen[key] = true
		}
		if inHorizon && repeat && (a.Tool == toolFileRead || a.Tool == toolRead) {
			count++
		}
	}
	return count
}

// countTool counts actions using one tool.
func countTool(actions []Action, tool string) int {
	n := 0
	for _, a := range actions {
		if a.Tool == tool {
			n++
		}
	}
	return n
}
