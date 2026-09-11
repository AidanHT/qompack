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

// referenceMatchPattern is the detector's ORIGINAL matchPattern, kept verbatim as the oracle the
// path-linked one is held to: a plain forward scan over every observation in the window, with
// e0's approach class computed up front.
func referenceMatchPattern(sorted []Observation, i int) (f1, r2, e3 Observation, ok bool) {
	e0 := sorted[i]
	p := e0.Path
	limit := e0.Turn + detectWindowTurns
	class0 := ApproachClass(e0.Detail)

	var haveF1, haveR2 bool
	for _, o := range sorted[i+1:] {
		if o.Turn > limit {
			break
		}
		switch {
		case !haveF1:
			if o.Kind == ObsTestPass && o.Path == p && o.Turn > e0.Turn {
				return Observation{}, Observation{}, Observation{}, false
			}
			if o.Kind == ObsTestFail && o.Turn > e0.Turn && (o.Path == p || o.Path == "") {
				f1, haveF1 = o, true
			}
		case !haveR2:
			if o.Kind == ObsRevert && o.Path == p && o.Turn > f1.Turn {
				r2, haveR2 = o, true
			}
		default:
			if o.Kind == ObsEdit && o.Path == p && o.Turn > r2.Turn &&
				ApproachClass(o.Detail) != class0 {
				return f1, r2, o, true
			}
		}
	}
	return Observation{}, Observation{}, Observation{}, false
}

// requireSameMatches compares matchPattern against referenceMatchPattern for every edit Scan
// would hand it — every ObsEdit with a non-empty Path, the one precondition matchPattern states —
// on one sorted slice, sharing one classMemo across the whole slice exactly as Scan does.
func requireSameMatches(fatalf func(string, ...any), sorted []Observation) {
	links := linkPaths(sorted)
	classes := classMemo{}
	for i := range sorted {
		if sorted[i].Kind != ObsEdit || sorted[i].Path == "" {
			continue
		}
		f1, r2, e3, ok := matchPattern(sorted, links, i, classes)
		wf1, wr2, we3, wok := referenceMatchPattern(sorted, i)
		if ok != wok || f1 != wf1 || r2 != wr2 || e3 != we3 {
			fatalf("edit at %d: got (%+v, %+v, %+v, %v), want (%+v, %+v, %+v, %v)",
				i, f1, r2, e3, ok, wf1, wr2, we3, wok)
		}
	}
}

// TestMatchPattern_MatchesWindowScan pins the equivalence the path-linked scan rests on: for every
// candidate edit in a sorted corpus it returns exactly the (f1, r2, e3, ok) the original
// every-observation window scan returned.
//
// The generator is built to reach every branch the argument covers: three paths plus the empty
// one, so that most observations are on OTHER paths (the ones the linked scan skips) and a
// path-less failing test still counts; turns spread wider than detectWindowTurns, so windows end
// both at an observation past the limit and at the end of the slice; and approach phrases from
// distinct classes, including two phrasings of one class, so condition 4 both passes and fails.
func TestMatchPattern_MatchesWindowScan(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		signals := rapid.SliceOfN(rapid.Custom(func(rt *rapid.T) Observation {
			return Observation{
				Turn: core.TurnIndex(rapid.IntRange(0, 3*detectWindowTurns).Draw(rt, "turn")),
				Kind: ObsKind(rapid.IntRange(0, 3).Draw(rt, "kind")),
				Path: rapid.SampledFrom([]string{"", "a.ts", "b.ts", "c.ts"}).Draw(rt, "path"),
				Detail: rapid.SampledFrom([]string{
					"", "widen pool timeout", "increase the pool timeout", "cache the parsed schema", "retry",
				}).Draw(rt, "detail"),
				ToolUse: core.ToolUseID(rapid.StringN(0, 2, 2).Draw(rt, "tool")),
			}
		}), 0, 80).Draw(rt, "signals")

		requireSameMatches(rt.Fatalf, sortedObservations(signals))
	})
}

// TestMatchPattern_BenchCorpus runs the same comparison over the §11.2 detector corpus, the input
// BenchmarkDetectorScan scans.
func TestMatchPattern_BenchCorpus(t *testing.T) {
	requireSameMatches(t.Fatalf, sortedObservations(benchObservationCorpus()))
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
