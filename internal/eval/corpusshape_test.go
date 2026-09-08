package eval_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// corpusShape is what one pass over the committed corpus measured.
type corpusShape struct {
	events, binding int
	kinds           map[eval.DemandKind]int
	eventsWith      map[eval.DemandKind]int
	maxCand         core.Tokens
}

// measureCorpus walks every committed session's compaction events and reports, per event, which
// demand kinds it raises and how many tokens the Belady candidate set holds.
func measureCorpus(t *testing.T) corpusShape {
	t.Helper()

	sessions, err := eval.New(eval.Options{}).Load(filepath.Join("..", "..", "testdata", "sessions", "synthetic"))
	require.NoError(t, err)

	sh := corpusShape{kinds: map[eval.DemandKind]int{}, eventsWith: map[eval.DemandKind]int{}}
	for _, s := range sessions {
		for _, at := range s.CompactionAt {
			sh.events++
			to := at + core.TurnIndex(eval.DefaultHorizonK)
			if int(to) > len(s.Turns) {
				to = core.TurnIndex(len(s.Turns))
			}
			demands := eval.Demands(s, at, to)

			here := map[eval.DemandKind]bool{}
			demanded := map[string]bool{}
			for _, d := range demands {
				sh.kinds[d.Kind]++
				here[d.Kind] = true
				demanded[d.BlockID] = true
			}
			for k := range here {
				sh.eventsWith[k]++
			}

			seen := map[string]bool{}
			var cand core.Tokens
			for _, b := range eval.Blocks(s, at) {
				if demanded[b.ID] && !seen[b.ID] {
					seen[b.ID] = true
					cand += b.Tokens
				}
			}
			if cand > sh.maxCand {
				sh.maxCand = cand
			}
			if cand > eval.DefaultKeepBudget {
				sh.binding++
			}
			// The ceiling is only meaningful when the solver was exact.
			_, det, err := eval.BeladyDetail(context.Background(), s, at, eval.DefaultKeepBudget,
				eval.DefaultBeladyOptions())
			require.NoError(t, err)
			require.True(t, det.Exact, "%s at %d fell back to the 1/2-approximation", s.ID, at)
		}
	}
	return sh
}

// TestCorpus_RaisesEveryDemandKind is SP02-D1's acceptance, made mechanical.
//
// Before the recall window, all 177 demands across all 39 events were DemandFileContent, so
// tool_edit_distance, re_attempts and decision_preservation were computed over an input that could
// not exercise them — three metrics whose numbers were real and whose comparisons meant nothing.
// The property is PINNED here rather than observed once, so a generator change that quietly
// stopped raising a kind fails instead of silently re-creating the defect.
//
// The floors are minimums the committed corpus clears with room, not the numbers it happens to
// produce: a floor equal to the observed value turns every corpus regeneration into a test edit.
func TestCorpus_RaisesEveryDemandKind(t *testing.T) {
	t.Parallel()

	sh := measureCorpus(t)
	require.GreaterOrEqual(t, sh.events, 39, "the corpus must keep at least the events it had")
	t.Logf("events=%d file=%d tool=%d decision=%d elimination=%d (events raising each: %d/%d/%d/%d)",
		sh.events, sh.kinds[eval.DemandFileContent], sh.kinds[eval.DemandToolResult],
		sh.kinds[eval.DemandDecision], sh.kinds[eval.DemandElimination],
		sh.eventsWith[eval.DemandFileContent], sh.eventsWith[eval.DemandToolResult],
		sh.eventsWith[eval.DemandDecision], sh.eventsWith[eval.DemandElimination])

	for _, tc := range []struct {
		kind      eval.DemandKind
		name      string
		minEvents int
	}{
		{eval.DemandFileContent, "file content", 30},
		{eval.DemandToolResult, "tool result", 15},
		{eval.DemandDecision, "decision", 10},
		{eval.DemandElimination, "elimination", 10},
	} {
		require.GreaterOrEqualf(t, sh.eventsWith[tc.kind], tc.minEvents,
			"%s demands are raised at only %d of %d compaction events; SP02-D1 is the record of "+
				"what a corpus that cannot raise a demand kind does to the metrics that count it",
			tc.name, sh.eventsWith[tc.kind], sh.events)
	}
}

// TestCorpus_BeladyBudgetBinds is SP02-D3's acceptance.
//
// The Belady keep budget has to actually bind somewhere, or fraction_of_opt grades every policy
// against "keep everything demanded" — a ceiling no policy has to work for. On the pre-recall
// corpus Σ tokens(candidates) peaked at 14 412 against a 40 000 budget: not one event of 39 came
// within a factor of two of binding, and the primary metric of the whole evaluation was measured
// against a trivial ceiling.
//
// The threshold is a STATED FRACTION rather than "every event", because an agent session that
// resumes with a small working set is a real session and a corpus in which every compaction is
// oversubscribed would be its own distortion.
func TestCorpus_BeladyBudgetBinds(t *testing.T) {
	t.Parallel()

	sh := measureCorpus(t)
	require.Positive(t, sh.events)

	const minBindingFraction = 0.25
	got := float64(sh.binding) / float64(sh.events)
	t.Logf("candidate set exceeds the %d-token keep budget at %d of %d events (%.2f); peak %d tokens",
		eval.DefaultKeepBudget, sh.binding, sh.events, got, sh.maxCand)
	require.GreaterOrEqualf(t, got, minBindingFraction,
		"Σ tokens(candidates) exceeds DefaultKeepBudget (%d) at %d of %d compaction events (%.2f); "+
			"below %.2f the Belady ceiling is trivial at too much of the corpus and fraction_of_opt "+
			"stops discriminating. Largest candidate set seen: %d tokens",
		eval.DefaultKeepBudget, sh.binding, sh.events, got, minBindingFraction, sh.maxCand)
}

// TestCarriedDefect_SP02D4_PMinIsMeasuredOverCandidates pins the narrowing SP02-D4 recorded, so it
// stops being visible only to someone reading belady.go.
//
// The row said §5.2 defined p_min over ALL blocks while the code iterates the candidate set. The
// specification side of that disagreement is gone: Qompack.md v1.3 §5.2 retires rewrite-token
// accounting as total cost and states that "historical benchmark fields keep their original
// definitions and provenance while corrected metrics are added beside them", and
// plans/QOMPACK-ERRATA.md records the p_min argument as superseded analysis. rewrite_tokens is
// exactly such a historical field, so the candidate-set reading is the definition of record.
//
// What is asserted here is WHY the narrowing is the only usable one. Turn blocks are never
// demanded, so a whole-prefix reading finds an unkept block at position 0 for every policy and
// every session: p_min is identically 0, cost = w·(n − 0) = w·n, and rewrite_tokens becomes a
// constant that cannot distinguish two policies. A metric that cannot move is the class of defect
// this whole file exists to close, so matching the code to the retired wording would have created
// one to satisfy a sentence that no longer exists.
func TestCarriedDefect_SP02D4_PMinIsMeasuredOverCandidates(t *testing.T) {
	t.Parallel()

	sessions, err := eval.New(eval.Options{}).Load(filepath.Join("..", "..", "testdata", "sessions", "synthetic"))
	require.NoError(t, err)

	sawUndemandedPositionZero, sawNonZeroP := false, false
	for _, s := range sessions {
		for _, at := range s.CompactionAt {
			keep, _, err := eval.BeladyDetail(context.Background(), s, at, eval.DefaultKeepBudget,
				eval.DefaultBeladyOptions())
			require.NoError(t, err)
			if keep.P != 0 {
				sawNonZeroP = true
			}

			to := at + core.TurnIndex(eval.DefaultHorizonK)
			if int(to) > len(s.Turns) {
				to = core.TurnIndex(len(s.Turns))
			}
			demanded := map[string]bool{}
			for _, d := range eval.Demands(s, at, to) {
				demanded[d.BlockID] = true
			}
			for _, b := range eval.Blocks(s, at) {
				if b.Pos == 0 && !demanded[b.ID] {
					sawUndemandedPositionZero = true
				}
			}
		}
	}

	require.True(t, sawUndemandedPositionZero,
		"a whole-prefix p_min would read 0 here for every policy, which is why the code narrows to "+
			"the candidate set")
	require.True(t, sawNonZeroP,
		"measured over the candidates, p_min is a number that actually varies — that is the whole "+
			"of the deviation SP02-D4 recorded, and it is now pinned by a test rather than by a "+
			"comment")
}

// TestCarriedDefect_SP02D2_FileSetJaccardIsNotStructurallyOne is SP02-D2's acceptance.
//
// The metric used to be 1.0 for every policy on every session, including the null policy that
// keeps nothing, because the only repair the replay loop performed re-read the path key the
// demanding turn already touched: numerator and denominator were the same set by construction. A
// metric that cannot take any other value is not a weak signal, it is not a signal.
//
// It can now differ, and it differs in the right direction — the clairvoyant policy scores above
// the rest. What this does NOT assert is that the metric is a strong signal: the spread is about
// 0.15 %, which orders the policies and says very little about magnitude. That limit is recorded
// in the re-baseline's provenance rather than asserted away here.
func TestCarriedDefect_SP02D2_FileSetJaccardIsNotStructurallyOne(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "baseline", "phase0-recall.json"))
	require.NoError(t, err)
	var artifact struct {
		Policies map[string]map[string]float64 `json:"policies"`
	}
	require.NoError(t, json.Unmarshal(raw, &artifact))
	require.NotEmpty(t, artifact.Policies)

	const metric = "file_set_jaccard"
	values := map[string]float64{}
	belowOne := false
	for policy, metrics := range artifact.Policies {
		v, ok := metrics[metric]
		require.Truef(t, ok, "policy %s does not report %s", policy, metric)
		values[policy] = v
		if v < 1 {
			belowOne = true
		}
	}
	require.True(t, belowOne,
		"%s is 1.0 for every policy in the committed baseline, which is the state SP02-D2 recorded: "+
			"the repair model cannot change the file set, so the metric is inert. Values: %v",
		metric, values)
	require.Greater(t, values["oracle"], values["stock"],
		"the clairvoyant policy must diverge from the logged branch less than stock does, or the "+
			"metric moves without meaning anything. Values: %v", values)
}
