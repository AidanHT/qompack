package negknow

import (
	"sort"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"pgregory.net/rapid"
)

// referenceSortedObservations is the detector's ORIGINAL ordering step, kept verbatim as the
// oracle sortedObservations is held to: copy the source's slice, then sort.SliceStable it by
// (Turn, Kind, Path).
func referenceSortedObservations(signals []Observation) []Observation {
	sorted := make([]Observation, len(signals))
	copy(sorted, signals)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Turn != sorted[j].Turn {
			return sorted[i].Turn < sorted[j].Turn
		}
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		return sorted[i].Path < sorted[j].Path
	})
	return sorted
}

// TestSortedObservations_MatchesStableSort pins the equivalence the index-permutation sort rests
// on: for any input — ordered or not, with or without ties on (Turn, Kind, Path) — it returns
// exactly the sequence the original copy-and-SliceStable produced, and it leaves the source's own
// slice untouched.
//
// Ties are the case that matters. Two observations equal on all three keys but differing in
// Detail, Root or ToolUse are distinguishable to Pattern P, so an order that swapped them would
// change which one a scan saw first. The generator draws from deliberately tiny domains so that
// ties are common rather than rare.
func TestSortedObservations_MatchesStableSort(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		signals := rapid.SliceOfN(rapid.Custom(func(rt *rapid.T) Observation {
			return Observation{
				Turn:    core.TurnIndex(rapid.IntRange(0, 4).Draw(rt, "turn")),
				Kind:    ObsKind(rapid.IntRange(0, 3).Draw(rt, "kind")),
				Path:    rapid.SampledFrom([]string{"", "a.ts", "b.ts"}).Draw(rt, "path"),
				Detail:  rapid.SampledFrom([]string{"", "x", "y", "z"}).Draw(rt, "detail"),
				ToolUse: core.ToolUseID(rapid.StringN(0, 2, 2).Draw(rt, "tool")),
			}
		}), 0, 64).Draw(rt, "signals")

		before := append([]Observation(nil), signals...)
		got := sortedObservations(signals)
		want := referenceSortedObservations(before)

		if len(got) != len(want) {
			rt.Fatalf("len %d, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				rt.Fatalf("position %d: %+v, want %+v", i, got[i], want[i])
			}
		}
		for i := range before {
			if signals[i] != before[i] {
				rt.Fatalf("the source's slice was reordered at %d", i)
			}
		}
		if len(signals) > 0 && &got[0] == &signals[0] {
			rt.Fatalf("the result aliases the source's slice")
		}
	})
}

// TestSortedObservations_BenchCorpus runs the same comparison over the §11.2 detector corpus, the
// input BenchmarkDetectorScan actually sorts.
func TestSortedObservations_BenchCorpus(t *testing.T) {
	corpus := benchObservationCorpus()
	got := sortedObservations(corpus)
	want := referenceSortedObservations(corpus)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d: %+v, want %+v", i, got[i], want[i])
		}
	}
}
