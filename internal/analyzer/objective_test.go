package analyzer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/stretchr/testify/require"
)

// Gate M5-G15-A's exactness half.
//
// internal/analyzer/testdata/objective/*.json holds small instances — at most twelve candidates,
// so the whole assignment space can be enumerated — together with the optimum of the SAME declared
// objective the heuristic maximizes. Each case brute-forces that optimum here rather than trusting
// the committed number, compares the heuristic against it, and RECORDS THE RATIO.
//
// What it deliberately does NOT do is assert a bound. Contract §3 claims none: the redundancy term
// is subtracted, which does not preserve monotonicity, and the dependency and
// one-representation-per-item constraints change the feasible family, so a (1-1/e) assertion here
// would be a claim about a family of problems this objective does not belong to. The fixtures are
// measurement, not proof. redundancy-nonmonotone.json exists precisely to hold the counterexample
// that makes the point concrete, and its recorded ratio is the honest number rather than a
// flattering one.
//
// The committed "optimum" field is still checked against the brute force. It is not a second
// source of truth but a tripwire: a fixture edited by hand — a cost nudged, a weight retyped —
// silently changes what is being measured, and comparing the two catches that at the moment it
// happens rather than at the moment somebody quotes the ratio.

// objFixtureDir is where the instances live.
const objFixtureDir = "testdata/objective"

// objTolerance is the floating-point slack every value comparison below allows. Every quantity in
// these instances is a short decimal times a small weight, so a real disagreement is orders of
// magnitude larger than this.
const objTolerance = 1e-9

// objFixture is one committed instance.
type objFixture struct {
	// Name is the instance's name; it matches the file's basename.
	Name string `json:"name"`
	// Why records what this instance exists to demonstrate. It is read by people, not by code,
	// which is why it is a fixture field rather than a comment in a Go file nobody opens.
	Why string `json:"why"`
	// P is the compaction point.
	P int `json:"p"`
	// Lambda is the redundancy penalty.
	Lambda float64 `json:"lambda"`
	// Budget is the token budget.
	Budget int `json:"budget"`
	// Candidates is the candidate set.
	Candidates []objCandidate `json:"candidates"`
	// Optimum is the committed exact optimum of the declared objective.
	Optimum float64 `json:"optimum"`
	// ExpectOverflow marks an instance in which no feasible assignment exists at all.
	ExpectOverflow bool `json:"expectOverflow"`
	// ExpectArchive is the exact Archive list the proposal must report, when the instance pins one.
	ExpectArchive []dag.NodeID `json:"expectArchive"`
}

// objCandidate is one candidate in a committed instance.
type objCandidate struct {
	Item      dag.NodeID `json:"item"`
	Pos       int        `json:"pos"`
	Weight    float64    `json:"weight"`
	Mandatory bool       `json:"mandatory"`
	Reps      []objRep   `json:"reps"`
}

// objRep is one representation in a committed instance. Root is a short label rather than 64 hex
// characters: what the objective cares about is whether two representations name the SAME original
// evidence, and a label says that far more legibly than a digest does.
type objRep struct {
	Kind          string       `json:"kind"`
	Coverage      float64      `json:"coverage"`
	AssembledCost int          `json:"assembledCost"`
	Requires      []dag.NodeID `json:"requires"`
	Root          string       `json:"root"`
	Qualification string       `json:"qualification"`
	Derived       bool         `json:"derived"`
}

// objKinds maps the fixture spelling of a representation kind onto the type. The spellings are
// RepresentationKind.String()'s own, so a renamed kind breaks the fixtures loudly rather than
// letting them go on describing something that no longer exists.
var objKinds = map[string]analyzer.RepresentationKind{
	"exact_span":   analyzer.RepExactSpan,
	"capsule":      analyzer.RepCapsule,
	"pointer":      analyzer.RepPointer,
	"archive_only": analyzer.RepArchiveOnly,
}

// objQuals maps the fixture spelling of a qualification onto the type.
var objQuals = map[string]analyzer.Qualification{
	"current":   analyzer.QualCurrent,
	"stale":     analyzer.QualStale,
	"uncertain": analyzer.QualUncertain,
}

// objRequiredFixtures is the coverage contract for the fixture directory: contract §3 and the
// M5-G15-A row name these six shapes, and a directory that lost one of them would still pass every
// other assertion in this file while measuring less than it claims to.
var objRequiredFixtures = []string{
	"plain",
	"redundancy-nonmonotone",
	"dependency-closure",
	"zero-budget",
	"tiny-budget",
	"mandatory-overflow",
}

// TestObjectiveFixtures_CoverEveryRequiredShape fails if a required instance disappears.
func TestObjectiveFixtures_CoverEveryRequiredShape(t *testing.T) {
	present := make(map[string]bool)
	for _, f := range objLoadAll(t) {
		present[f.Name] = true
	}
	for _, name := range objRequiredFixtures {
		require.True(t, present[name], "testdata/objective is missing the %q instance", name)
	}
}

// TestObjective_HeuristicAgainstTheExactOptimum is gate M5-G15-A: for every committed instance,
// check the heuristic's proposal is feasible under all four of contract §3's rules, brute-force
// the optimum of the same declared objective, and record the ratio.
func TestObjective_HeuristicAgainstTheExactOptimum(t *testing.T) {
	withPSelection(t)

	for _, f := range objLoadAll(t) {
		t.Run(f.Name, func(t *testing.T) { objRunFixture(t, f) })
	}
}

// objLoadAll reads every instance in the fixture directory, in filename order.
func objLoadAll(t *testing.T) []objFixture {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(objFixtureDir, "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "no instances in %s", objFixtureDir)
	sort.Strings(paths)

	out := make([]objFixture, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p) //nolint:gosec // a committed test fixture path
		require.NoError(t, err)

		// DisallowUnknownFields is the second half of the tripwire: a fixture that grows a field
		// this loader does not know about is a fixture describing something the test is not
		// measuring, and silently ignoring it would let the ratio go on being quoted.
		var f objFixture
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		require.NoError(t, dec.Decode(&f), "decoding %s", p)

		base := filepath.Base(p)
		require.Equal(t, f.Name+".json", base, "an instance's name must match its filename")
		require.NotEmpty(t, f.Why, "%s must say what it exists to demonstrate", base)
		require.LessOrEqual(t, len(f.Candidates), 12,
			"%s has %d candidates; the brute force enumerates every assignment, so instances stay small",
			base, len(f.Candidates))
		out = append(out, f)
	}
	return out
}

// objCandidates converts a fixture's candidate set into the real types.
func objCandidates(t *testing.T, f objFixture) []analyzer.Candidate {
	t.Helper()

	out := make([]analyzer.Candidate, 0, len(f.Candidates))
	for _, c := range f.Candidates {
		cand := analyzer.Candidate{
			Item:      c.Item,
			Pos:       c.Pos,
			Weight:    c.Weight,
			Mandatory: c.Mandatory,
		}
		for _, r := range c.Reps {
			kind, ok := objKinds[r.Kind]
			require.True(t, ok, "unknown representation kind %q", r.Kind)
			qual, ok := objQuals[r.Qualification]
			require.True(t, ok, "unknown qualification %q", r.Qualification)

			var root core.Hash
			if r.Root != "" {
				root = selRoot(r.Root)
			}
			cand.Reps = append(cand.Reps, analyzer.Representation{
				Item:          c.Item,
				Kind:          kind,
				Coverage:      r.Coverage,
				AssembledCost: core.Tokens(r.AssembledCost),
				Requires:      r.Requires,
				Prov: analyzer.Provenance{
					Origin:        c.Item,
					Root:          root,
					Derived:       r.Derived,
					Qualification: qual,
				},
			})
		}
		out = append(out, cand)
	}
	return out
}

// objRunFixture is one instance's whole check: feasibility, then the exact comparison.
func objRunFixture(t *testing.T, f objFixture) {
	t.Helper()

	cands := objCandidates(t, f)
	got, err := analyzer.Propose(context.Background(), f.P, cands, f.Lambda, core.Tokens(f.Budget))
	require.NoError(t, err, "a well-formed instance is never an error; overflow is an outcome")

	exact, feasible := objBruteForce(f, cands)

	if f.ExpectOverflow {
		require.True(t, got.Overflow, "instance %s must overflow", f.Name)
		require.NotEmpty(t, got.Reason, "an overflow must say which record was lost")
		require.Nil(t, got.Chosen, "an overflow is never a partial serialization")
		require.Zero(t, int(got.Tokens))
		require.False(t, feasible,
			"instance %s claims an overflow, so no assignment may satisfy every binding record", f.Name)
	} else {
		require.False(t, got.Overflow, "instance %s must not overflow: %s", f.Name, got.Reason)
		require.True(t, feasible, "instance %s must have at least one feasible assignment", f.Name)
	}
	if f.ExpectArchive != nil {
		require.Equal(t, f.ExpectArchive, got.Archive)
	}

	objRequireFeasible(t, f, cands, got)

	require.InDelta(t, f.Optimum, exact, objTolerance,
		"the committed optimum of %s no longer matches the brute force; the instance changed under it",
		f.Name)
	require.LessOrEqual(t, got.Value, exact+objTolerance,
		"the heuristic reported %.9f against an exact optimum of %.9f: one of the two objectives is wrong",
		got.Value, exact)

	ratio := 1.0
	if exact > objTolerance {
		ratio = got.Value / exact
	}
	t.Logf("M5-G15-A %s: heuristic=%.6f exact=%.6f ratio=%.6f tokens=%d/%d iters=%d chosen=%d",
		f.Name, got.Value, exact, ratio, int(got.Tokens), f.Budget, got.Iters, len(got.Chosen))
}

// objRequireFeasible asserts all four of contract §3's feasibility rules against a real Proposal,
// independently of anything the selector believes about its own output.
func objRequireFeasible(t *testing.T, f objFixture, cands []analyzer.Candidate, got analyzer.Proposal) {
	t.Helper()

	byItem := make(map[dag.NodeID]analyzer.Candidate, len(cands))
	for _, c := range cands {
		byItem[c.Item] = c
	}

	// Rule 1: at most one representation per item.
	seen := make(map[dag.NodeID]bool, len(got.Chosen))
	var total core.Tokens
	for _, r := range got.Chosen {
		require.False(t, seen[r.Item], "item %s appears twice in Chosen", string(r.Item))
		seen[r.Item] = true
		require.NotEqual(t, analyzer.RepArchiveOnly, r.Kind, "archive-only delivers nothing and is never chosen")
		total += r.AssembledCost

		// Rule 4: nothing before p. The constructor already makes a pre-p candidate impossible to
		// hand over, so this asserts the selection did not invent one.
		require.GreaterOrEqual(t, byItem[r.Item].Pos, f.P, "item %s precedes p", string(r.Item))
	}

	// Rule 2: dependency closure.
	for _, r := range got.Chosen {
		for _, d := range r.Requires {
			if _, isCandidate := byItem[d]; !isCandidate {
				continue // not a candidate: read as already present in the prefix
			}
			require.True(t, seen[d], "item %s requires %s, which was not delivered",
				string(r.Item), string(d))
		}
	}

	// Rule 3: the assembled cost fits, and Tokens is the true sum rather than a counter.
	require.Equal(t, total, got.Tokens, "Tokens must be the sum of Chosen's AssembledCost")
	require.LessOrEqual(t, int(got.Tokens), f.Budget, "the proposal overran its budget")

	// G6.3: a candidate that binds is carried, and carried at active evidence.
	for _, c := range cands {
		if !objBinds(c) {
			continue
		}
		if got.Overflow {
			continue
		}
		require.True(t, seen[c.Item], "binding record %s was neither carried nor overflowed", string(c.Item))
	}
	for _, r := range got.Chosen {
		if objBinds(byItem[r.Item]) {
			require.True(t, r.Prov.Qualification.Active(),
				"binding record %s was carried at %s evidence", string(r.Item), r.Prov.Qualification)
		}
	}
}

// objBinds mirrors the analyzer's own rule for when a Mandatory flag actually binds, restated here
// rather than borrowed: an exact comparison that reused the implementation's own notion of
// feasibility would agree with it by construction and could never catch it being wrong.
func objBinds(c analyzer.Candidate) bool {
	if !c.Mandatory {
		return false
	}
	for _, r := range c.Reps {
		if r.Kind != analyzer.RepArchiveOnly && r.Prov.Qualification == analyzer.QualCurrent {
			return true
		}
	}
	return false
}

// objBruteForce enumerates every assignment of at most one representation to each item and returns
// the best value the declared objective takes on a feasible one, plus whether any feasible
// assignment exists at all.
//
// It is written as an independent restatement of contract §3 — its own saturation, its own
// redundancy count, its own feasibility rules — for the same reason objBinds is: an oracle that
// called into the selector would confirm the selector against itself.
func objBruteForce(f objFixture, cands []analyzer.Candidate) (float64, bool) {
	choices := make([][]analyzer.Representation, len(cands))
	binding := make([]bool, len(cands))
	isCandidate := make(map[dag.NodeID]bool, len(cands))
	for i, c := range cands {
		isCandidate[c.Item] = true
		binding[i] = objBinds(c)
		for _, r := range c.Reps {
			if r.Kind == analyzer.RepArchiveOnly {
				continue
			}
			if binding[i] && !r.Prov.Qualification.Active() {
				continue
			}
			choices[i] = append(choices[i], r)
		}
	}

	pick := make([]int, len(cands))
	best, feasible := 0.0, false

	var walk func(i int)
	walk = func(i int) {
		if i == len(cands) {
			v, ok := objEvaluate(f, cands, choices, pick, isCandidate)
			if ok && (!feasible || v > best) {
				best, feasible = v, true
			}
			return
		}
		// A binding record has no "not carried" option: an assignment that leaves it out is not a
		// cheaper solution, it is a different problem.
		start := -1
		if binding[i] {
			start = 0
		}
		for c := start; c < len(choices[i]); c++ {
			pick[i] = c
			walk(i + 1)
		}
	}
	walk(0)
	return best, feasible
}

// objEvaluate scores one complete assignment, reporting false when it is infeasible.
func objEvaluate(f objFixture, cands []analyzer.Candidate, choices [][]analyzer.Representation,
	pick []int, isCandidate map[dag.NodeID]bool,
) (float64, bool) {
	chosen := make(map[dag.NodeID]analyzer.Representation, len(cands))
	var cost core.Tokens
	for i, c := range cands {
		if pick[i] < 0 {
			continue
		}
		r := choices[i][pick[i]]
		chosen[c.Item] = r
		cost += r.AssembledCost
	}
	if int(cost) > f.Budget {
		return 0, false
	}
	for _, r := range chosen {
		for _, d := range r.Requires {
			if !isCandidate[d] {
				continue
			}
			if _, ok := chosen[d]; !ok {
				return 0, false
			}
		}
	}

	// Walk cands rather than the chosen map so the floating-point sum is order-independent, the
	// same property the implementation buys with its own sorted walk.
	var cov float64
	roots := make(map[core.Hash]int, len(chosen))
	dup := 0
	for i, c := range cands {
		if pick[i] < 0 {
			continue
		}
		r := choices[i][pick[i]]
		w := c.Weight
		if w < 0 {
			w = 0
		}
		s := r.Coverage
		switch {
		case s < 0:
			s = 0
		case s > 1:
			s = 1
		}
		cov += w * s
		if r.Prov.Root.IsZero() {
			continue
		}
		if roots[r.Prov.Root] > 0 {
			dup++
		}
		roots[r.Prov.Root]++
	}

	v := cov - f.Lambda*float64(dup)
	if v < 0 {
		v = 0
	}
	return v, true
}
