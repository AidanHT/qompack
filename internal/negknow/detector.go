package negknow

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// Detector is negative-knowledge discovery source #3 (00-ARCHITECTURE.md §5.10, §8.3): the
// heuristic that infers an (unreported) elimination from a test-fail -> revert ->
// different-approach pattern in the dependence DAG, producing Records with Source ==
// SourceHeuristic.
//
// 00-ARCHITECTURE.md §5.10 declares this interface without giving it a constructor: unlike Ledger
// (Open) or the other stubbed interfaces in this tree, nothing in wave 0 needs to hold a
// constructed Detector value (daemon.Options names a negknow.Ledger, not a Detector), and the
// heuristic's own construction parameters are not yet decided. SP-01 therefore declares the
// interface — the "real declaration" §14.1 requires — without inventing an unspecified
// constructor function; SP-09 adds one alongside its real implementation.
type Detector interface {
	// Scan walks g looking for the test-fail -> revert -> different-approach pattern at or after
	// since, returning one Record per inferred elimination.
	Scan(ctx context.Context, g dag.Graph, since core.TurnIndex) ([]Record, error)
}

// detectWindowTurns is how many turns after the failing edit Pattern P will still recognise the
// revert and the re-approach. Twelve turns is wide enough to span a test run, a diagnosis and a
// rewrite, and narrow enough that an edit and a revert at opposite ends of a long session are read
// as two separate lines of work rather than as one abandoned approach.
const detectWindowTurns = 12

// The counters source #3 maintains. They are strings, so nothing catches a typo at compile time
// and a second spelling produces a second, silently empty instrument; these three are this
// package's single transcription of the subplan's names.
const (
	counterDetectorCandidates        = "negknow.detector.candidates"
	counterDetectorEmitted           = "negknow.detector.emitted"
	counterDetectorDroppedNoEvidence = "negknow.detector.dropped_no_evidence"
)

// depResolver and metricsSource are the two ledger internals the detector reaches, and it reaches
// them by type-asserting its OWN ObservationSource rather than by asserting *ledger.
//
// That is what keeps NewDetector's declared signature honest. It takes an ObservationSource, so a
// caller may legitimately pass something that is not this package's ledger — a replay harness, a
// test double, SP-08's own buffer — and an implementation that asserted the concrete type would
// either panic on those or silently degrade with no way to tell which. A source implementing
// neither interface gets no dependency fallback and a nopRegistry, which is a detector that still
// finds every pattern and simply contributes no depends_on and no counters.
type (
	depResolver interface {
		resolveDeps(ctx context.Context, explicit []string, target string) ([]core.Dep, []string)
	}
	metricsSource interface {
		registry() obs.Registry
	}
)

// detector is the heuristic implementation of Detector: §8.3 source #3.
type detector struct {
	src  ObservationSource
	sess core.SessionID
	// elim is cfg.Eliminations after every out-of-domain value has been replaced by its Appendix C
	// default, so Scan never has to re-validate configuration mid-pass.
	elim config.EliminationsCfg
	clk  core.Clock
	// dr resolves the fallback depends_on when the DAG cannot; nil when src does not offer it.
	dr depResolver
	m  obs.Registry
	// log is where a dropped candidate is reported. §5.10 declares NewDetector without a logger
	// parameter and Rule W-3 freezes that signature, and the seams onto the source are fixed at
	// depResolver and metricsSource, so this is logging.Nop() in every construction the current
	// interface admits. The call site is written out rather than dropped: it is the one place a
	// future signature change has to reach, and a silent drop with no line to enable is how a
	// heuristic quietly stops proposing anything.
	log logging.Logger
}

// The compile-time assertion that keeps NewDetector's return type and this implementation in sync.
var _ Detector = (*detector)(nil)

// NewDetector returns the heuristic elimination detector reading src.
//
// It is the constructor §5.10 deliberately left out of SP-01's declaration, and the intended
// construction from another package is:
//
//	src, ok := led.(negknow.ObservationSource)   // always true for the value Open returns
//	det := negknow.NewDetector(src, sess, cfg, clk)
//
// Out-of-domain configuration is replaced by its Appendix C default here as it is at Open, quietly
// rather than loudly: a detector built from the same config the ledger was built from would
// otherwise shout the same line a second time, which trains an operator to ignore the channel real
// corruption uses.
func NewDetector(src ObservationSource, sess core.SessionID, cfg config.Config, clk core.Clock) Detector {
	if clk == nil {
		clk = core.SystemClock()
	}
	d := &detector{
		src:  src,
		sess: sess,
		elim: normalizeEliminations(cfg.Eliminations, logging.Nop()),
		clk:  clk,
		m:    nopRegistry{},
		log:  logging.Nop(),
	}
	if dr, ok := src.(depResolver); ok {
		d.dr = dr
	}
	if ms, ok := src.(metricsSource); ok {
		if r := ms.registry(); r != nil {
			d.m = r
		}
	}
	return d
}

// Scan walks the observations at or after since for Pattern P and returns one candidate Record per
// match, WITHOUT appending any of them: the caller — SP-08's observer, or SP-12's idle task —
// decides when a proposal becomes a record.
//
// Pattern P, normatively:
//
//  1. e0 = ObsEdit on path p at turn t0, with non-empty Detail (the approach text).
//  2. f1 = ObsTestFail at turn t1 with t0 < t1 <= t0+detectWindowTurns and (f1.Path == p or
//     f1.Path == ""), and no ObsTestPass on p in (t0, t1).
//  3. r2 = ObsRevert on p at turn t2 with t1 < t2 <= t0+detectWindowTurns.
//  4. e3 = ObsEdit on p at turn t3 with t2 < t3 <= t0+detectWindowTurns and
//     ApproachClass(e3.Detail) != ApproachClass(e0.Detail).
//
// A whole-suite failure (f1.Path == "") counts against p because it is still evidence about the
// edit in flight; the ObsTestPass exclusion is path-scoped, because a pass elsewhere says nothing
// about p either way.
//
// Each e0 participates in at most one emission — the earliest satisfying (f1, r2, e3) — so a
// second revert/re-edit cycle after the same failing edit is more of the same elimination rather
// than a second one.
func (d *detector) Scan(ctx context.Context, g dag.Graph, since core.TurnIndex) ([]Record, error) {
	// The source is an interface, so its ordering is a contract this cannot verify; sorting a copy
	// is what makes Pattern P's "sorted by (Turn, Kind, Path)" true for every implementation and
	// not only for the ledger's own ring.
	sorted := sortedObservations(d.src.Since(since))
	links := linkPaths(sorted)
	classes := classMemo{}

	var out []Record
	for i := range sorted {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e0 := &sorted[i]
		if e0.Kind != ObsEdit || e0.Path == "" || e0.Detail == "" || e0.Turn < since {
			continue
		}
		f1, r2, e3, ok := matchPattern(sorted, links, i, classes)
		if !ok {
			continue
		}
		d.m.Counter(counterDetectorCandidates).Add(1)

		rec, ok := d.candidateRecord(ctx, g, *e0, f1, r2, e3, classes)
		if !ok {
			continue
		}
		d.m.Counter(counterDetectorEmitted).Add(1)
		out = append(out, rec)
	}
	return out, nil
}

// compareObservations is Pattern P's scan order: Turn, then Kind, then Path, each ascending.
func compareObservations(a, b *Observation) int {
	if c := cmp.Compare(a.Turn, b.Turn); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
		return c
	}
	return strings.Compare(a.Path, b.Path)
}

// sortedObservations returns a copy of signals STABLY sorted by compareObservations, and never
// reorders signals itself: the slice belongs to the source.
//
// It produces exactly the sequence a stable sort of the copy would, by one of two cheaper routes.
// Both rest on the same fact: a stable sort's output is unique — the elements ordered by key,
// ties kept in input order.
//
// Input whose Turns never decrease — every observer feeds signals in turn order, and the ledger's
// own Since answers fully sorted — is sorted one equal-Turn run at a time. In such input each Turn
// value occupies one contiguous run and the runs already stand in ascending Turn order, which is
// where the stable sort puts them; within a run Turn ties, so the stable sort orders the run by
// (Kind, Path) with ties in input order, which is a stable sort of the run. The runs are a
// handful of signals each, so the pass is linear in practice.
//
// Any other input takes an index permutation sorted under (key, input index). That is a total
// order, no two entries compare equal and any correct sort yields its one answer, and it is the
// stable sort's order by the uniqueness above; the copy is gathered from it. Sorting 4-byte indices
// instead of moving whole Observations is the saving there.
func sortedObservations(signals []Observation) []Observation {
	sorted := make([]Observation, len(signals))
	turnOrdered := true
	for i := 1; i < len(signals); i++ {
		if signals[i].Turn < signals[i-1].Turn {
			turnOrdered = false
			break
		}
	}
	if turnOrdered {
		copy(sorted, signals)
		for lo := 0; lo < len(sorted); {
			hi := lo + 1
			for hi < len(sorted) && sorted[hi].Turn == sorted[lo].Turn {
				hi++
			}
			if hi-lo > 1 {
				slices.SortStableFunc(sorted[lo:hi], func(a, b Observation) int {
					return compareObservations(&a, &b)
				})
			}
			lo = hi
		}
		return sorted
	}

	perm := make([]int32, len(signals))
	for i := range perm {
		perm[i] = int32(i)
	}
	slices.SortFunc(perm, func(a, b int32) int {
		if c := compareObservations(&signals[a], &signals[b]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	for i, j := range perm {
		sorted[i] = signals[j]
	}
	return sorted
}

// classMemo caches ApproachClass for the duration of one Scan. ApproachClass is a pure,
// deterministic function of its argument, so a cached answer is the answer; the cache only stops a
// scan from re-classifying the same handful of approach phrases once per candidate edit.
type classMemo map[string]string

// of returns ApproachClass(approach).
func (m classMemo) of(approach string) string {
	if c, ok := m[approach]; ok {
		return c
	}
	c := ApproachClass(approach)
	m[approach] = c
	return c
}

// pathLinks threads a sorted observation slice by path, so that matchPattern can visit only the
// observations that can matter to an edit on one path.
type pathLinks struct {
	// nextSame[j] is the index of the next observation after j with sorted[j]'s Path, or
	// len(sorted) when there is none.
	nextSame []int
	// nextBlank[j] is the index of the first observation at or after j whose Path is "", or
	// len(sorted) when there is none. It has len(sorted)+1 entries so nextBlank[j+1] is always
	// defined.
	nextBlank []int
}

// linkPaths builds sorted's pathLinks in one backward pass.
func linkPaths(sorted []Observation) pathLinks {
	n := len(sorted)
	links := pathLinks{nextSame: make([]int, n), nextBlank: make([]int, n+1)}
	last := make(map[string]int)
	links.nextBlank[n] = n
	for j := n - 1; j >= 0; j-- {
		p := sorted[j].Path
		if k, ok := last[p]; ok {
			links.nextSame[j] = k
		} else {
			links.nextSame[j] = n
		}
		last[p] = j
		if p == "" {
			links.nextBlank[j] = j
		} else {
			links.nextBlank[j] = links.nextBlank[j+1]
		}
	}
	return links
}

// matchPattern completes conditions 2 to 4 for the edit at sorted[i], returning the earliest
// (f1, r2, e3) that satisfies them.
//
// It is a forward scan bounded by the window: it starts after i and stops as soon as an
// observation falls past t0+detectWindowTurns, with no state carried between candidates.
//
// The scan visits, in slice order, only the observations after i whose Path is e0's path p or is
// "" — links supplies both chains and the loop merges them — and that visits nothing that could
// matter. Every branch below tests o.Path == p, except the failing-test branch, which also admits
// o.Path == ""; p itself is never "" (Scan skips such an e0). So an observation on any other path
// changes no state and returns nothing. The one thing it could do is end the scan by falling past
// the window, but sorted is ordered by Turn, so every later observation is past the window too and
// the scan ends with the same answer at the next relevant observation or at the slice's end.
//
// e0's approach class is needed only by condition 4, so it is computed there, the first time an
// e3 candidate reaches the comparison, rather than for every edit that starts a scan. Most never
// get that far, and ApproachClass is pure, so when it is computed changes no answer.
func matchPattern(sorted []Observation, links pathLinks, i int, classes classMemo) (f1, r2, e3 Observation, ok bool) {
	e0 := &sorted[i]
	p := e0.Path
	limit := e0.Turn + detectWindowTurns
	class0, haveClass0 := "", false

	var haveF1, haveR2 bool
	n := len(sorted)
	same, blank := links.nextSame[i], links.nextBlank[i+1]
	for same < n || blank < n {
		var o *Observation
		if same < blank {
			o, same = &sorted[same], links.nextSame[same]
		} else {
			o, blank = &sorted[blank], links.nextBlank[blank+1]
		}
		if o.Turn > limit {
			break
		}
		switch {
		case !haveF1:
			if o.Kind == ObsTestPass && o.Path == p && o.Turn > e0.Turn {
				// A test that passed on p after the edit means the failure that follows is not
				// evidence against the edit, so this e0 can never complete.
				return Observation{}, Observation{}, Observation{}, false
			}
			if o.Kind == ObsTestFail && o.Turn > e0.Turn && (o.Path == p || o.Path == "") {
				f1, haveF1 = *o, true
			}
		case !haveR2:
			if o.Kind == ObsRevert && o.Path == p && o.Turn > f1.Turn {
				r2, haveR2 = *o, true
			}
		default:
			if o.Kind == ObsEdit && o.Path == p && o.Turn > r2.Turn {
				if !haveClass0 {
					class0, haveClass0 = classes.of(e0.Detail), true
				}
				if classes.of(o.Detail) != class0 {
					return f1, r2, *o, true
				}
			}
		}
	}
	return Observation{}, Observation{}, Observation{}, false
}

// candidateRecord builds the Record one matched pattern proposes, or reports that it was dropped.
//
// The only drop is the evidence one, and it is the §8.3 discipline applied to the least reliable
// of the four sources: a heuristic guess with no stored failing-test output behind it is exactly
// the record that must not reach an append-only log under eliminations.requireEvidence.
func (d *detector) candidateRecord(ctx context.Context, g dag.Graph, e0, f1, r2, e3 Observation, classes classMemo) (Record, bool) {
	if f1.Root.IsZero() && d.elim.RequireEvidence {
		d.m.Counter(counterDetectorDroppedNoEvidence).Add(1)
		d.log.Debug("negknow: dropped a heuristic elimination with no evidence root",
			"path", e0.Path, "turn", int(f1.Turn))
		return Record{}, false
	}

	target := e0.Path
	if e0.Symbol != "" {
		target = e0.Path + ":" + e0.Symbol
	}

	ts := f1.TS
	if ts == 0 {
		ts = core.UnixMilli(d.clk.Now().UnixMilli())
	}

	return Record{
		Session:  d.sess,
		TS:       ts,
		Target:   target,
		Approach: e0.Detail,
		Reason: fmt.Sprintf(
			"test failed at turn %d and the change was reverted at turn %d; "+
				"a different approach (%s) was taken at turn %d",
			int(f1.Turn), int(r2.Turn), classes.of(e3.Detail), int(e3.Turn)),
		Evidence:  f1.Root,
		DependsOn: d.dependsOn(ctx, g, f1, target),
		Scope:     Scope(d.elim.DefaultScope),
		Status:    StatusActive,
		Source:    SourceHeuristic,
	}, true
}

// dependsOn is the DAG's whole contribution to source #3: it answers "which files did the failing
// test actually read", which is exactly what the elimination's reason rests on and therefore
// exactly what its staleness guard must watch.
//
// With no graph, no node for the failing tool use, or a walk that yields nothing, it falls back to
// resolveDeps — the same store-driven default the explicit sources use.
func (d *detector) dependsOn(ctx context.Context, g dag.Graph, f1 Observation, target string) []core.Dep {
	if deps := d.dagDeps(g, f1); len(deps) > 0 {
		return deps
	}
	if d.dr == nil {
		return nil
	}
	deps, _ := d.dr.resolveDeps(ctx, nil, target)
	return deps
}

// dagDeps is the bounded one-hop fan-out around the failing test's tool-use node.
//
// TERMINATION (the wave-1 INHERIT from SP-07, ADR 0007 — stated here rather than assumed).
// The graph this walks IS NOT ACYCLIC and cannot be: §8.1 item 4 directs a shared-file edge INTO a
// tool use that consumed a file and OUT of one that produced it, so the ordinary Read-then-Edit of
// one file closes a legal loop — tooluse:t1 -> toolresult:t1 -> assistant:2 -> tooluse:t2 ->
// file:a -> tooluse:t1 — and internal/dag's TestReadThenWriteClosesALegitimateCycle pins that it
// really is a cycle so it cannot be assumed away. A naive recursive descent over these edges does
// not terminate. This code terminates because: Scan's outer loop is a single forward pass over a
// finite observation slice with each e0 emitting at most once; the graph is touched only by the
// bounded ONE-HOP g.In / g.Out fan-out below, whose results are read for their ENDPOINTS and never
// followed; and there is therefore no recursion and no work queue, so no edge can be traversed a
// second time and the cycle above is simply two of the one-hop neighbours of tooluse:t1. No
// visited set is needed because nothing is followed. If a future change ever needs a walk deeper
// than one hop it carries an explicit visited map[dag.NodeID]struct{} and a hop cap — a recursive
// descent without one is not an option in this package. TestDetector_TerminatesOnCyclicGraph
// builds exactly that cycle and requires Scan to return.
//
// The node id goes through dag.ToolUseNode and never through a hand-built id concatenating the
// tooluse prefix onto the key: concatenation skips dag's sanitizeKey, so a long or control-bearing
// key would yield an id dag.ToolUseNode never produces and would fork the graph into two
// disconnected halves with no error anywhere.
func (d *detector) dagDeps(g dag.Graph, f1 Observation) []core.Dep {
	if g == nil || f1.ToolUse == "" {
		return nil
	}
	n, ok := g.Node(dag.ToolUseNode(f1.ToolUse))
	if !ok {
		return nil
	}

	var deps []core.Dep
	seen := make(map[string]struct{})
	consider := func(edges []dag.Edge) {
		for _, e := range edges {
			if e.Kind != dag.EdgeConsumes && e.Kind != dag.EdgeSharedFile {
				continue
			}
			other := e.To
			if other == n.ID {
				other = e.From
			}
			fn, ok := g.Node(other)
			if !ok || fn.Kind != dag.KindFile || fn.Root.IsZero() {
				continue
			}
			key := paths.Key(fn.Ref)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			deps = append(deps, core.Dep{Path: key, Hash: fn.Root})
		}
	}
	consider(g.In(n.ID))
	consider(g.Out(n.ID))

	sort.SliceStable(deps, func(i, j int) bool { return deps[i].Path < deps[j].Path })
	if len(deps) > maxAutoDeps {
		deps = deps[:maxAutoDeps]
	}
	return deps
}
