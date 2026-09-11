package negknow

import (
	"sort"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"pgregory.net/rapid"
)

// referenceSortedObservations is the detector's ORIGINAL ordering step, kept verbatim as the
// oracle scanOrder is held to: copy the source's slice, then sort.SliceStable it by
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

// requireScanOrderMatchesStableSort checks that scanOrder is a permutation of signals' indices,
// that viewing signals through it yields exactly the sequence the original copy-and-SliceStable
// produced, and that it left signals itself untouched.
func requireScanOrderMatchesStableSort(fatalf func(string, ...any), signals []Observation) {
	before := append([]Observation(nil), signals...)
	order := scanOrder(signals)
	want := referenceSortedObservations(before)

	if len(order) != len(want) {
		fatalf("len %d, want %d", len(order), len(want))
	}
	seen := make([]bool, len(signals))
	for k, j := range order {
		if j < 0 || j >= len(signals) || seen[j] {
			fatalf("order is not a permutation: position %d names %d", k, j)
		}
		seen[j] = true
		if signals[j] != want[k] {
			fatalf("position %d: %+v, want %+v", k, signals[j], want[k])
		}
	}
	for i := range before {
		if signals[i] != before[i] {
			fatalf("the source's slice was modified at %d", i)
		}
	}
}

// TestScanOrder_MatchesStableSort pins the equivalence the scan order rests on: for any input —
// ordered or not, with or without ties on (Turn, Kind, Path) — viewing the signals through it
// yields exactly the sequence the original copy-and-SliceStable produced.
//
// Ties are the case that matters. Two observations equal on all three keys but differing in
// Detail, Root or ToolUse are distinguishable to Pattern P, so an order that swapped them would
// change which one a scan saw first. The generator draws from deliberately tiny domains so that
// ties are common rather than rare, and it produces both Turn-ordered input (the per-run route)
// and Turn-unordered input (the whole-slice route).
func TestScanOrder_MatchesStableSort(t *testing.T) {
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
		if rapid.Bool().Draw(rt, "turnOrdered") {
			sort.SliceStable(signals, func(i, j int) bool { return signals[i].Turn < signals[j].Turn })
		}

		requireScanOrderMatchesStableSort(rt.Fatalf, signals)
	})
}

// TestScanOrder_BenchCorpus runs the same comparison over the §11.2 detector corpus, the input
// BenchmarkDetectorScan actually orders.
func TestScanOrder_BenchCorpus(t *testing.T) {
	requireScanOrderMatchesStableSort(t.Fatalf, benchObservationCorpus())
}

// referenceMatchPattern is the detector's ORIGINAL matchPattern, kept verbatim as the oracle the
// path-linked one is held to: a plain forward scan over every observation in the window of a
// sorted copy, with e0's approach class computed up front.
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

// requireSameMatches compares matchPattern, reading signals through the scan order, against
// referenceMatchPattern over the original sorted copy, for every edit Scan would hand it — every
// ObsEdit with a non-empty Path, the one precondition matchPattern states — sharing one classMemo
// across the whole scan exactly as Scan does.
func requireSameMatches(fatalf func(string, ...any), signals []Observation) {
	order := scanOrder(signals)
	links := linkPaths(signals, order)
	sorted := referenceSortedObservations(signals)
	classes := classMemo{}
	for i := range order {
		e0 := signals[order[i]]
		if e0.Kind != ObsEdit || e0.Path == "" {
			continue
		}
		f1, r2, e3, ok := matchPattern(signals, order, links, i, classes)
		wf1, wr2, we3, wok := referenceMatchPattern(sorted, i)
		if ok != wok || f1 != wf1 || r2 != wr2 || e3 != we3 {
			fatalf("edit at position %d: got (%+v, %+v, %+v, %v), want (%+v, %+v, %+v, %v)",
				i, f1, r2, e3, ok, wf1, wr2, we3, wok)
		}
	}
}

// TestMatchPattern_MatchesWindowScan pins the equivalence the path-linked scan rests on: for every
// candidate edit it returns exactly the (f1, r2, e3, ok) the original every-observation window
// scan over a sorted copy returned.
//
// The generator is built to reach every branch the argument covers: three paths plus the empty
// one, so that most observations are on OTHER paths (the ones the linked scan skips) and a
// path-less failing test still counts; turns spread wider than detectWindowTurns, so windows end
// both at an observation past the limit and at the end of the scan; approach phrases from
// distinct classes, including two phrasings of one class, so condition 4 both passes and fails;
// and both Turn-ordered and unordered input, so both scanOrder routes feed it.
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
		if rapid.Bool().Draw(rt, "turnOrdered") {
			sort.SliceStable(signals, func(i, j int) bool { return signals[i].Turn < signals[j].Turn })
		}

		requireSameMatches(rt.Fatalf, signals)
	})
}

// TestMatchPattern_BenchCorpus runs the same comparison over the §11.2 detector corpus, the input
// BenchmarkDetectorScan scans.
func TestMatchPattern_BenchCorpus(t *testing.T) {
	requireSameMatches(t.Fatalf, benchObservationCorpus())
}
