package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/scheduler"
)

// SP-15's replay arm: the measurement engine behind §10 Phase 5 (representation selection) and
// §10 Phase 6 (state-aware loop warnings), plus the two phase-exit checks phases.go registers.
//
// WHY THIS IS NOT A _test.go FILE. `devtool replay` shells out to `go run ./test/replay`, and
// `go run` does not compile test files, so a phase check that lived only in a _test.go file would
// be invisible to the gate: the driver would run, report nothing, and stay green forever. The same
// reasoning put phase3 next to policy_rehydrate.go. Everything both the driver check and the two
// _test.go files need lives here; everything only the tests need (the corpus arm, which wants
// t.TempDir and a testing.T) lives in phase5_test.go beside its assertions.
//
// ── WHAT THESE MEASUREMENTS PROVE, AND WHAT THEY DO NOT ─────────────────────────────────────
//
// PHASE 5. The trials in testdata/phase5 are hand-authored instances, not recordings, and the
// corpus arm in phase5_test.go derives its candidates from the committed 24-session SYNTHETIC
// corpus. Neither establishes that selection helps a real session: the weights are declared, the
// representation costs are the uncalibrated estimates blocker M5-U15-representation-overhead says
// must stay estimates until the assembled model is calibrated against provider-reported usage,
// and coverage is a per-item number the fixture supplies rather than a measured contribution to
// task completion. What the rows DO establish is that the four feasibility rules of contract §3
// hold on every instance, that the G6.3 dispositions are the ones the contract names, and that
// two Propose calls on equal inputs return equal Proposals. Feasibility is a property of the
// output and can be checked exactly; usefulness is not and is not claimed.
//
// The selection-versus-baseline comparison holds the WEIGHTS FIXED across both arms, which is the
// only reason it isolates anything: both the selector and the pre-SP-15 complete-record heuristic
// rank the same items by the same declared weight, so a difference between them is a difference in
// mechanism — representation choice, dependency closure, redundancy pricing — and not in scoring.
// The comparison is still not a quality verdict. No number produced here may flip a default; the
// plan is explicit that there is "no automatic default flip from a mock or synthetic score", and
// both switches stay false in every configuration this file touches.
//
// PHASE 6. The streams in testdata/phase6 are hand-authored held-out cases, thirteen of them, and
// thirteen synthetic streams are a sample size, not a population. The false-alarm and detection
// counts below are exact statements about THIS corpus and are reported as such — including the
// misses, including the arms that lose. They are not an estimate of a false-alarm rate in the
// field, and nothing here calibrates one. The ablation compares three detectors on identical
// input under identical delivery bounds; its resource numbers are recorded and never asserted,
// because wall time on a loaded machine is not a property of the code (memory: two wall-clock
// tests in this tree fail only under co-load).
//
// DETERMINISM AND SEEDS. Neither phase draws a random number. There is no seed to report: the
// fixtures are committed bytes, the selector is deterministic by contract §3, and the detector
// holds no clock and does no I/O. Reproducibility is asserted directly instead — every arm is run
// twice and its decision vector compared — which is a stronger statement than quoting a seed.

// ----------------------------------------------------------------------------------------------
// Phase 5 — representation selection.
// ----------------------------------------------------------------------------------------------

const (
	// p5FixtureDir is where the held-out selection trials live, relative to the repository root.
	p5FixtureDir = "test/replay/testdata/phase5"

	// p5RootDomain is the hash domain that turns a fixture's short evidence-root LABEL into a
	// core.Hash. Labels rather than 64 hex characters, because what the objective cares about is
	// whether two representations name the SAME original evidence, and "ev-pool" says that far
	// more legibly than a digest. It is a package-local domain, which is the house pattern for a
	// digest nobody outside this file consumes (internal/sketch/hash.go, internal/negknow, …).
	p5RootDomain = "qompack.replay.phase5.evidence-root.v1"

	// p5Tolerance is the floating-point slack every objective comparison allows. Every quantity in
	// these instances is a short decimal times a small weight, so a real disagreement is orders of
	// magnitude larger.
	p5Tolerance = 1e-9

	// The four regime outcome classes a trial may declare. They are classes rather than exact
	// expected sets on purpose: the exact set is a function of the greedy's internal ordering, and
	// pinning it everywhere would turn every legitimate tie-break change into a fixture edit. The
	// contract-level statement is the class, and expectCarried/expectArchive pin the exact set in
	// the two instances where the exact set IS the point.
	p5ExpectAll      = "all-carried"
	p5ExpectPartial  = "partial"
	p5ExpectEmpty    = "empty"
	p5ExpectOverflow = "overflow"
)

// p5Rep is one representation in a committed trial.
type p5Rep struct {
	// Kind is the representation kind, spelled as RepresentationKind.String() spells it, so a
	// renamed kind breaks the fixtures loudly rather than letting them describe something that no
	// longer exists.
	Kind string `json:"kind"`
	// Coverage is the per-item saturating contribution, in [0,1].
	Coverage float64 `json:"coverage"`
	// AssembledCost is the delivered cost INCLUDING wrapper, handle and report overhead.
	AssembledCost int `json:"assembledCost"`
	// Requires is the dependency closure this representation drags in.
	Requires []dag.NodeID `json:"requires"`
	// Root is a short label for the ORIGINAL evidence root; two representations sharing a label
	// deliver the same evidence and are what the redundancy term counts.
	Root string `json:"root"`
	// Qualification is how much authority this evidence still carries: current, stale, uncertain.
	Qualification string `json:"qualification"`
	// Derived marks a summary of a summary.
	Derived bool `json:"derived"`
}

// p5Candidate is one candidate item in a committed trial.
type p5Candidate struct {
	Item      dag.NodeID `json:"item"`
	Pos       int        `json:"pos"`
	Weight    float64    `json:"weight"`
	Mandatory bool       `json:"mandatory"`
	Reps      []p5Rep    `json:"reps"`
}

// p5Regime is one budget regime a trial is measured under. Contract §3 names three — generous,
// tiny, and mandatory-overflow — and every trial declares all three, because a trial that only ran
// where it succeeds would measure the easy half of the contract.
type p5Regime struct {
	// Name is the regime's name, one of the three contract §3 names.
	Name string `json:"name"`
	// Budget is the token budget in force.
	Budget int `json:"budget"`
	// Expect is the outcome class required of this regime.
	Expect string `json:"expect"`
	// ExpectCarried, when present, is the exact set of items that must appear in Chosen.
	ExpectCarried []dag.NodeID `json:"expectCarried,omitempty"`
	// ExpectArchive, when present, is the exact Archive list the proposal must report.
	ExpectArchive []dag.NodeID `json:"expectArchive,omitempty"`
}

// p5Trial is one committed held-out selection instance.
type p5Trial struct {
	// Name matches the file's basename.
	Name string `json:"name"`
	// Why records what this instance exists to demonstrate, and where it came from. It is read by
	// people rather than by code, which is exactly why it is a fixture field: a provenance note in
	// a Go file next door is a note nobody opens while reading the fixture.
	Why string `json:"why"`
	// P is the compaction point every candidate must sit at or after.
	P int `json:"p"`
	// Lambda is the redundancy penalty.
	Lambda float64 `json:"lambda"`
	// Candidates is the candidate set.
	Candidates []p5Candidate `json:"candidates"`
	// Regimes are the budget regimes this trial is measured under.
	Regimes []p5Regime `json:"regimes"`
	// MustCarryAtActiveEvidence names the records that BIND: each must be carried at a
	// Qualification.Active() representation in every non-overflow regime, or explicitly overflow.
	MustCarryAtActiveEvidence []dag.NodeID `json:"mustCarryAtActiveEvidence"`
	// MustNeverBind names the records whose Mandatory flag must NOT act as a constraint, because
	// their evidence is stale or uncertain. They may never force an overflow at any budget, and
	// wherever they are carried they keep the qualification they arrived with.
	MustNeverBind []dag.NodeID `json:"mustNeverBind"`
}

// p5Kinds maps a fixture's spelling of a representation kind onto the type.
var p5Kinds = map[string]analyzer.RepresentationKind{
	"exact_span":   analyzer.RepExactSpan,
	"capsule":      analyzer.RepCapsule,
	"pointer":      analyzer.RepPointer,
	"archive_only": analyzer.RepArchiveOnly,
}

// p5Quals maps a fixture's spelling of a qualification onto the type.
var p5Quals = map[string]analyzer.Qualification{
	"current":   analyzer.QualCurrent,
	"stale":     analyzer.QualStale,
	"uncertain": analyzer.QualUncertain,
}

// p5RequiredTrials is the coverage contract for the fixture directory. Contract §3's three regimes
// and the M5-G15-A row's G6.3 clauses name these four shapes; a directory that lost one of them
// would still pass every other assertion in this file while measuring less than it claims to.
var p5RequiredTrials = []string{
	"g63-current-authoritative",
	"g63-stale-and-uncertain",
	"dependency-closure-and-archive",
	"tie-determinism",
}

// loadP5Trials reads every committed trial, in filename order.
//
// DisallowUnknownFields is deliberate and is the same tripwire objective_test.go uses: a fixture
// that grows a field this loader does not know about is a fixture describing something the
// measurement is not measuring, and silently ignoring it would let the numbers go on being quoted.
func loadP5Trials(root string) ([]p5Trial, error) {
	paths, err := filepath.Glob(filepath.Join(root, p5FixtureDir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("phase 5: globbing %s: %w", p5FixtureDir, err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("phase 5: no trials in %s", p5FixtureDir)
	}
	sort.Strings(paths)

	out := make([]p5Trial, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p) //nolint:gosec // a committed test fixture path
		if err != nil {
			return nil, fmt.Errorf("phase 5: reading %s: %w", p, err)
		}
		var tr p5Trial
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&tr); err != nil {
			return nil, fmt.Errorf("phase 5: decoding %s: %w", filepath.Base(p), err)
		}
		if base := filepath.Base(p); tr.Name+".json" != base {
			return nil, fmt.Errorf("phase 5: trial %q lives in %s; a trial's name must match its filename",
				tr.Name, base)
		}
		if tr.Why == "" {
			return nil, fmt.Errorf("phase 5: %s must say what it exists to demonstrate", tr.Name)
		}
		if len(tr.Regimes) == 0 {
			return nil, fmt.Errorf("phase 5: %s declares no budget regime", tr.Name)
		}
		out = append(out, tr)
	}

	present := make(map[string]bool, len(out))
	for _, tr := range out {
		present[tr.Name] = true
	}
	for _, name := range p5RequiredTrials {
		if !present[name] {
			return nil, fmt.Errorf("phase 5: %s is missing the %q trial", p5FixtureDir, name)
		}
	}
	return out, nil
}

// p5Root turns a fixture's evidence-root label into the core.Hash the objective compares. An empty
// label stays the ZERO hash, which contract §3a amendment A1 says never counts as a duplicate.
func p5Root(label string) core.Hash {
	if label == "" {
		return core.Hash{}
	}
	return core.HashBytes(p5RootDomain, []byte(label))
}

// candidates converts a trial into the real analyzer types.
func (tr p5Trial) candidates() ([]analyzer.Candidate, error) {
	out := make([]analyzer.Candidate, 0, len(tr.Candidates))
	for _, c := range tr.Candidates {
		cand := analyzer.Candidate{Item: c.Item, Pos: c.Pos, Weight: c.Weight, Mandatory: c.Mandatory}
		for _, r := range c.Reps {
			kind, ok := p5Kinds[r.Kind]
			if !ok {
				return nil, fmt.Errorf("phase 5: %s: unknown representation kind %q", tr.Name, r.Kind)
			}
			qual, ok := p5Quals[r.Qualification]
			if !ok {
				return nil, fmt.Errorf("phase 5: %s: unknown qualification %q", tr.Name, r.Qualification)
			}
			cand.Reps = append(cand.Reps, analyzer.Representation{
				Item:          c.Item,
				Kind:          kind,
				Coverage:      r.Coverage,
				AssembledCost: core.Tokens(r.AssembledCost),
				Requires:      r.Requires,
				Prov: analyzer.Provenance{
					Origin:        c.Item,
					Root:          p5Root(r.Root),
					Derived:       r.Derived,
					Qualification: qual,
				},
			})
		}
		out = append(out, cand)
	}
	return out, nil
}

// p5Arm is one arm's result over one trial at one budget: what it carried, what that cost, and
// what the declared objective is worth there.
type p5Arm struct {
	// Name identifies the arm.
	Name string `json:"arm"`
	// Carried is how many distinct items the arm delivered.
	Carried int `json:"carried"`
	// Tokens is the arm's assembled cost, the true sum over what it delivered.
	Tokens int `json:"tokens"`
	// Value is contract §3's objective evaluated at the arm's set, by this file's own independent
	// restatement (p5Objective) rather than by anything the selector reported.
	Value float64 `json:"value"`
	// Coverage is the arm's saturating coverage term ALONE, before the redundancy subtraction,
	// reported separately so a reader can see which of the two moved.
	Coverage float64 `json:"coverage"`
	// Duplicates is how many chosen representations re-deliver an evidence root another chosen
	// representation already delivered — amendment A1's redundancy count.
	Duplicates int `json:"duplicates"`
	// ClosureViolations counts delivered items whose Requires names a candidate that was NOT
	// delivered. It is always zero for the selector, by feasibility rule 2; it is the number worth
	// looking at on the baseline arm, which has no notion of a dependency at all.
	ClosureViolations int `json:"closureViolations"`
	// Kinds counts what was delivered at each representation kind, by kind name.
	Kinds map[string]int `json:"kinds"`
}

// p5Outcome is one trial replayed at one budget regime: both arms, plus the selector's own
// overflow and archive reporting.
type p5Outcome struct {
	Trial    string       `json:"trial"`
	Regime   string       `json:"regime"`
	Budget   int          `json:"budget"`
	Expect   string       `json:"expect"`
	Class    string       `json:"class"`
	Overflow bool         `json:"overflow"`
	Reason   string       `json:"reason,omitempty"`
	Archive  []dag.NodeID `json:"archive,omitempty"`
	Iters    int          `json:"iters"`
	Selector p5Arm        `json:"selector"`
	Baseline p5Arm        `json:"baseline"`
}

// p5Objective evaluates contract §3's objective at a chosen set, restated here rather than read
// off a Proposal.
//
// The restatement is the point, and it is objective_test.go's objBinds argument applied to a
// different question: a comparison that asked the selector what its own output was worth would
// agree with the selector by construction, and could never catch it being wrong. It also lets the
// baseline arm — which has no Proposal at all — be scored on the same yardstick as the selector,
// which is the only way the two numbers are comparable.
func p5Objective(cands []analyzer.Candidate, chosen []analyzer.Representation, lambda float64,
) (value, coverage float64, dups int) {
	weight := make(map[dag.NodeID]float64, len(cands))
	for _, c := range cands {
		if c.Weight > 0 {
			weight[c.Item] = c.Weight
		}
	}

	cov := make(map[dag.NodeID]float64, len(chosen))
	order := make([]dag.NodeID, 0, len(chosen))
	roots := make(map[core.Hash]int, len(chosen))
	for _, r := range chosen {
		if _, seen := cov[r.Item]; !seen {
			order = append(order, r.Item)
		}
		cov[r.Item] += r.Coverage
		if r.Prov.Root.IsZero() {
			continue
		}
		if roots[r.Prov.Root] > 0 {
			dups++
		}
		roots[r.Prov.Root]++
	}

	// Summed in first-delivery order rather than over the map, so the floating-point result does
	// not depend on Go's randomized map iteration and two runs agree bit for bit.
	for _, item := range order {
		c := cov[item]
		switch {
		case c < 0:
			c = 0
		case c > 1:
			c = 1
		}
		coverage += weight[item] * c
	}
	value = coverage - lambda*float64(dups)
	if value < 0 {
		value = 0
	}
	return value, coverage, dups
}

// p5ClosureViolations counts delivered items whose Requires names a candidate that was not itself
// delivered. An entry naming an item outside the candidate set is NOT a violation: feasibility
// rule 2 reads it as content the prefix still holds, which is the only reading under which a
// candidate set assembled from a suffix can ever be feasible.
func p5ClosureViolations(cands []analyzer.Candidate, chosen []analyzer.Representation) int {
	isCandidate := make(map[dag.NodeID]bool, len(cands))
	for _, c := range cands {
		isCandidate[c.Item] = true
	}
	delivered := make(map[dag.NodeID]bool, len(chosen))
	for _, r := range chosen {
		delivered[r.Item] = true
	}

	violations := 0
	for _, r := range chosen {
		for _, d := range r.Requires {
			if isCandidate[d] && !delivered[d] {
				violations++
			}
		}
	}
	return violations
}

// p5Summarize scores one arm's delivered set on the shared yardstick.
func p5Summarize(name string, cands []analyzer.Candidate, chosen []analyzer.Representation,
	lambda float64,
) p5Arm {
	arm := p5Arm{Name: name, Kinds: map[string]int{}}
	items := make(map[dag.NodeID]bool, len(chosen))
	for _, r := range chosen {
		items[r.Item] = true
		arm.Tokens += int(r.AssembledCost)
		arm.Kinds[r.Kind.String()]++
	}
	arm.Carried = len(items)
	arm.Value, arm.Coverage, arm.Duplicates = p5Objective(cands, chosen, lambda)
	arm.ClosureViolations = p5ClosureViolations(cands, chosen)
	return arm
}

// completeRecordCarry is the PRE-SP-15 heuristic, restated exactly enough to be a fair baseline.
//
// Before this subplan, internal/rehydrate/items.go ranked the eligible elimination records by
// slice score descending, then timestamp descending, then id ascending, and carried each one
// COMPLETE until the budget ran out. There was no choice of representation (a record was carried
// or it was not), no dependency closure, and no price on re-delivering evidence something else
// already delivered. This function is that: rank by (weight desc, item asc), take each item's
// exact span, keep it if it fits, stop at nothing.
//
// The declared weight is the stand-in for the slice score, and the two arms share it. That
// sharing is what makes the comparison mean anything: give the baseline a different ranking and
// any difference between the arms could be the ranking rather than the mechanism.
//
// An item with no exact span is skipped rather than carried at a cheaper kind, because "carry the
// complete record" is precisely what this heuristic did and a baseline that quietly learned to
// downgrade would be the new selector wearing the old name.
func completeRecordCarry(cands []analyzer.Candidate, budget core.Tokens) []analyzer.Representation {
	type ranked struct {
		item   dag.NodeID
		weight float64
		rep    analyzer.Representation
		ok     bool
	}
	rows := make([]ranked, 0, len(cands))
	for _, c := range cands {
		row := ranked{item: c.Item, weight: c.Weight}
		for _, r := range c.Reps {
			if r.Kind == analyzer.RepExactSpan {
				row.rep, row.ok = r, true
				break
			}
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].weight != rows[j].weight {
			return rows[i].weight > rows[j].weight
		}
		return rows[i].item < rows[j].item
	})

	var out []analyzer.Representation
	var spent core.Tokens
	for _, row := range rows {
		if !row.ok || spent+row.rep.AssembledCost > budget {
			continue
		}
		out = append(out, row.rep)
		spent += row.rep.AssembledCost
	}
	return out
}

// p5Class reads a proposal back as one of the four declared outcome classes.
func p5Class(prop analyzer.Proposal, candidates int) string {
	switch {
	case prop.Overflow:
		return p5ExpectOverflow
	case len(prop.Chosen) == 0:
		return p5ExpectEmpty
	case len(prop.Chosen) == candidates:
		return p5ExpectAll
	default:
		return p5ExpectPartial
	}
}

// runP5Trial proposes over one trial at one regime and scores both arms.
//
// It returns the proposal alongside the outcome because the feasibility checks need the real
// Proposal — Chosen's Requires, its Kinds, its Prov — and reconstructing them from the summary
// would be checking the summary rather than the selector.
func runP5Trial(ctx context.Context, tr p5Trial, rg p5Regime) (p5Outcome, analyzer.Proposal, error) {
	cands, err := tr.candidates()
	if err != nil {
		return p5Outcome{}, analyzer.Proposal{}, err
	}
	prop, err := analyzer.Propose(ctx, tr.P, cands, tr.Lambda, core.Tokens(rg.Budget))
	if err != nil {
		return p5Outcome{}, analyzer.Proposal{}, fmt.Errorf(
			"phase 5: %s/%s: a well-formed instance is never an error, and overflow is an outcome: %w",
			tr.Name, rg.Name, err)
	}

	out := p5Outcome{
		Trial: tr.Name, Regime: rg.Name, Budget: rg.Budget, Expect: rg.Expect,
		Class:    p5Class(prop, len(cands)),
		Overflow: prop.Overflow, Reason: prop.Reason, Archive: prop.Archive, Iters: prop.Iters,
		Selector: p5Summarize("selection", cands, prop.Chosen, tr.Lambda),
		Baseline: p5Summarize("complete-record", cands,
			completeRecordCarry(cands, core.Tokens(rg.Budget)), tr.Lambda),
	}
	return out, prop, nil
}

// p5CheckOutcomes asserts everything contract §3 and the M5-G15-A row require of one proposal,
// against the real Proposal and independently of anything the selector believes about itself.
//
// It is a function returning an error rather than a testify assertion so that the driver's phase
// check and the two _test.go files run the SAME checks. A gate whose CI form and whose test form
// have drifted apart is a gate that proves whichever of the two is weaker.
func p5CheckOutcomes(tr p5Trial, rg p5Regime, prop analyzer.Proposal) error {
	cands, err := tr.candidates()
	if err != nil {
		return err
	}
	byItem := make(map[dag.NodeID]analyzer.Candidate, len(cands))
	for _, c := range cands {
		byItem[c.Item] = c
	}
	where := fmt.Sprintf("phase 5 (%s/%s, budget %d)", tr.Name, rg.Name, rg.Budget)

	// Rule 1: at most one representation per item, and archive-only is never one of them.
	seen := make(map[dag.NodeID]bool, len(prop.Chosen))
	var total core.Tokens
	for _, r := range prop.Chosen {
		if seen[r.Item] {
			return fmt.Errorf("%s: item %s appears twice in Chosen", where, r.Item)
		}
		seen[r.Item] = true
		if r.Kind == analyzer.RepArchiveOnly {
			return fmt.Errorf("%s: item %s was chosen at archive_only, which delivers nothing", where, r.Item)
		}
		total += r.AssembledCost

		// Rule 4: nothing before p. The constructor makes a pre-p candidate impossible to hand
		// over at all, so this asserts the selection did not invent one.
		if byItem[r.Item].Pos < tr.P {
			return fmt.Errorf("%s: item %s sits at pos %d, before p=%d",
				where, r.Item, byItem[r.Item].Pos, tr.P)
		}
	}

	// Rule 2: dependency closure, transitively, over the candidate set.
	if v := p5ClosureViolations(cands, prop.Chosen); v != 0 {
		return fmt.Errorf("%s: %d delivered item(s) require a candidate that was not delivered", where, v)
	}

	// Rule 3: the assembled cost fits, and Tokens is the true sum rather than a counter kept
	// alongside it.
	if total != prop.Tokens {
		return fmt.Errorf("%s: Tokens is %d but Chosen sums to %d", where, int(prop.Tokens), int(total))
	}
	if int(prop.Tokens) > rg.Budget {
		return fmt.Errorf("%s: the proposal spent %d tokens", where, int(prop.Tokens))
	}

	// The declared outcome class.
	if got := p5Class(prop, len(cands)); got != rg.Expect {
		return fmt.Errorf("%s: expected the %q outcome, got %q (chosen=%d of %d, overflow=%v)",
			where, rg.Expect, got, len(prop.Chosen), len(cands), prop.Overflow)
	}

	// Overflow is explicit, names the record, and carries the recovery path. A partial
	// serialization reported as a success is the failure §12 rates High, so Chosen must be empty
	// and the overflowing item must appear in Archive — the field that says "not injected, still
	// recoverable" out loud. (The user-facing recovery sentence is
	// internal/rehydrate/selection.go's archiveRecoveryDetail, which is where the drop report is
	// rendered; it is referenced here, never duplicated.)
	if prop.Overflow {
		if len(prop.Chosen) != 0 {
			return fmt.Errorf("%s: an overflow carried %d representations; it is never a partial "+
				"serialization", where, len(prop.Chosen))
		}
		if prop.Reason == "" || prop.Item == "" {
			return fmt.Errorf("%s: an overflow must name the record it lost", where)
		}
		if !containsNode(prop.Archive, prop.Item) {
			return fmt.Errorf("%s: overflowing item %s is not in Archive, so it has no recovery path",
				where, prop.Item)
		}
	}

	// G6.3, first clause: a record that BINDS is carried, and carried at active evidence.
	for _, item := range tr.MustCarryAtActiveEvidence {
		c, ok := byItem[item]
		if !ok {
			return fmt.Errorf("%s: mustCarryAtActiveEvidence names %s, which is not a candidate", where, item)
		}
		if !p5Binds(c) {
			return fmt.Errorf("%s: %s is declared binding but has no mandatory active representation",
				where, item)
		}
		if prop.Overflow {
			continue // the explicit archive-only/overflow disposition, checked above.
		}
		if !seen[item] {
			return fmt.Errorf("%s: binding record %s was neither carried nor overflowed", where, item)
		}
	}
	for _, r := range prop.Chosen {
		if p5Binds(byItem[r.Item]) && !r.Prov.Qualification.Active() {
			return fmt.Errorf("%s: binding record %s was carried at %s evidence",
				where, r.Item, r.Prov.Qualification)
		}
	}

	// G6.3, second clause: a stale or uncertain record never becomes an active constraint. It may
	// never bind, may never be the reason for an overflow, and keeps its qualification wherever it
	// is carried.
	for _, item := range tr.MustNeverBind {
		c, ok := byItem[item]
		if !ok {
			return fmt.Errorf("%s: mustNeverBind names %s, which is not a candidate", where, item)
		}
		if p5Binds(c) {
			return fmt.Errorf("%s: %s carries active evidence and therefore does bind; the fixture "+
				"and the record disagree", where, item)
		}
		if prop.Overflow && prop.Item == item {
			return fmt.Errorf("%s: %s forced an overflow although its evidence is not active", where, item)
		}
	}
	if err := p5CheckQualificationsPreserved(byItem, prop); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}

	// The exact sets, where a trial pins them.
	if rg.ExpectCarried != nil {
		got := make([]dag.NodeID, 0, len(prop.Chosen))
		for _, r := range prop.Chosen {
			got = append(got, r.Item)
		}
		if !sameNodes(got, rg.ExpectCarried) {
			return fmt.Errorf("%s: carried %v, expected exactly %v", where, got, rg.ExpectCarried)
		}
	}
	if rg.ExpectArchive != nil && !sameNodes(prop.Archive, rg.ExpectArchive) {
		return fmt.Errorf("%s: archived %v, expected exactly %v", where, prop.Archive, rg.ExpectArchive)
	}
	return nil
}

// p5CheckQualificationsPreserved asserts that every carried representation still reports the
// qualification the candidate offered it under.
//
// It matters because the qualification is the only thing standing between a recorded "we can no
// longer establish this" and a hard "do not try this". A selector that carried a stale record
// while reporting it current would deliver the false already_tried §12 rates High, and it would do
// it in a field nobody would think to check.
func p5CheckQualificationsPreserved(byItem map[dag.NodeID]analyzer.Candidate,
	prop analyzer.Proposal,
) error {
	for _, r := range prop.Chosen {
		found := false
		for _, offered := range byItem[r.Item].Reps {
			if offered.Kind == r.Kind && offered.Prov.Qualification == r.Prov.Qualification &&
				offered.Prov.Root == r.Prov.Root {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("item %s was carried at kind=%s qualification=%s, which no candidate "+
				"representation offered", r.Item, r.Kind, r.Prov.Qualification)
		}
	}
	return nil
}

// p5Binds restates the analyzer's own rule for when a Mandatory flag actually constrains, rather
// than borrowing it. Borrowing would make the check agree with the implementation by construction
// and unable to catch it being wrong — objective_test.go's objBinds makes the same choice for the
// same reason.
func p5Binds(c analyzer.Candidate) bool {
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

// containsNode reports whether ids contains id.
func containsNode(ids []dag.NodeID, id dag.NodeID) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// sameNodes reports whether two id lists hold the same ids in the same order.
func sameNodes(a, b []dag.NodeID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// p5ProposalKey renders a Proposal as the canonical string two runs are compared on.
//
// Iters is included on purpose. Contract §3 says two Select calls on equal inputs return equal
// Proposals, "Iters included", and Iters is the field that would move first if the walk's ordering
// became dependent on map iteration — which is the failure this key exists to catch.
func p5ProposalKey(prop analyzer.Proposal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tokens=%d value=%.9f iters=%d overflow=%v item=%s reason=%q archive=%v chosen=",
		int(prop.Tokens), prop.Value, prop.Iters, prop.Overflow, prop.Item, prop.Reason, prop.Archive)
	for _, r := range prop.Chosen {
		fmt.Fprintf(&b, "[%s|%s|%.6f|%d|%s|%v|%s]",
			r.Item, r.Kind, r.Coverage, int(r.AssembledCost), r.Prov.Qualification,
			r.Prov.Derived, r.Prov.Root.Short())
	}
	return b.String()
}

// p5Line renders one measured row for a log or a phase-check summary.
func p5Line(o p5Outcome) string {
	return fmt.Sprintf(
		"  %-32s %-18s budget=%6d class=%-12s selection[carry=%d tok=%5d val=%.4f dup=%d] "+
			"complete-record[carry=%d tok=%5d val=%.4f dup=%d closure-violations=%d] iters=%d",
		o.Trial, o.Regime, o.Budget, o.Class,
		o.Selector.Carried, o.Selector.Tokens, o.Selector.Value, o.Selector.Duplicates,
		o.Baseline.Carried, o.Baseline.Tokens, o.Baseline.Value, o.Baseline.Duplicates,
		o.Baseline.ClosureViolations, o.Iters)
}

// runP5Trials proposes over every committed trial at every declared regime, checks each proposal
// and returns the measured rows.
//
// It opens the ship-order gate around its own work and closes it again. scheduler.PSelectionAvailable
// is a process-wide atomic — analyzer.NewSelector refuses to construct while it is false — so a
// measurement of the selector has to open it, and leaving it open afterwards would let some later
// caller construct a selector this process never decided to enable. The deferred close is the whole
// of that decision; the SHIPPED default is untouched, and TestGuard_SubmodularDefaultsOff still
// pins it.
func runP5Trials(ctx context.Context, root string) ([]p5Outcome, error) {
	trials, err := loadP5Trials(root)
	if err != nil {
		return nil, err
	}

	scheduler.EnablePSelection()
	defer scheduler.DisablePSelection()

	var out []p5Outcome
	for _, tr := range trials {
		for _, rg := range tr.Regimes {
			o, prop, err := runP5Trial(ctx, tr, rg)
			if err != nil {
				return nil, err
			}
			if err := p5CheckOutcomes(tr, rg, prop); err != nil {
				return nil, err
			}

			// Determinism, checked where the proposal is made rather than in a test of its own:
			// contract §3 requires equal Proposals for equal inputs, and a second call is the only
			// thing that can establish it.
			_, again, err := runP5Trial(ctx, tr, rg)
			if err != nil {
				return nil, err
			}
			if p5ProposalKey(prop) != p5ProposalKey(again) {
				return nil, fmt.Errorf("phase 5 (%s/%s): two Propose calls on equal inputs disagreed:\n  %s\n  %s",
					tr.Name, rg.Name, p5ProposalKey(prop), p5ProposalKey(again))
			}
			out = append(out, o)
		}
	}
	return out, nil
}

// phase5 is §10 Phase 5's exit criterion as gate M5-G15-A states it: "at most one compatible
// representation, complete dependency closure/overhead, deterministic ties and valid
// zero/tiny/overflow outcomes", with the G6.3 dispositions on top.
//
// It is SELF-CONTAINED — it reads the committed trials and nothing from the driver's report — and
// that is deliberate rather than incidental. The phase-check contract is that a landed phase
// re-asserts itself on every pull request forever; a check that needed a particular policy in
// --policies would instead fail loudly on every run that did not happen to include it, which is
// how a gate ends up disabled. The corpus-scale numbers live in phase5_test.go, where a testing.T
// can supply the temporary project directory they need.
//
// It asserts no quality score and no approximation bound. The heuristic-versus-exact ratios are
// recorded by TestPhase5_HeuristicAgainstExactRatiosRecorded and brute-forced by
// internal/analyzer/objective_test.go; 0.738 on the non-monotone instance is the honest number and
// is the reason no (1−1/e) claim exists anywhere in this subplan.
//
// The driver Context is unused, and the blank parameter name says so: the self-containment above
// is a property of the signature, not a convention this check happens to follow.
func phase5(_ Context) error {
	root, err := repoRoot()
	if err != nil {
		return fmt.Errorf("phase 5: %w", err)
	}
	outcomes, err := runP5Trials(context.Background(), root)
	if err != nil {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "phase 5: %d held-out selection trials, %d (trial, regime) rows, lambda from the fixtures\n",
		len(p5RequiredTrials), len(outcomes))
	for _, o := range outcomes {
		fmt.Fprintln(&b, p5Line(o))
	}
	fmt.Print(b.String())
	return nil
}

// ----------------------------------------------------------------------------------------------
// Phase 6 — state-aware loop warnings, and the M6-G15-B ablation.
// ----------------------------------------------------------------------------------------------

const (
	// p6FixtureDir is where the held-out warning streams live, relative to the repository root.
	p6FixtureDir = "test/replay/testdata/phase6"

	// The two held-out labels. A stream is either ordinary progress — in which case any warning on
	// it is a FALSE ALARM and is counted as one — or genuinely stuck.
	p6LabelProgress = "progress"
	p6LabelStuck    = "stuck"

	// The three ablation arms. M6-G15-B asks for Sequitur compared against state signatures; the
	// third arm exists because the obvious objection to the second is that Sequitur was handicapped
	// by being denied the progress signal. It is not: arm three gives Sequitur that signal and the
	// comparison is repeated.
	p6ArmSignature        = "state-signature"
	p6ArmSequitur         = "sequitur"
	p6ArmSequiturProgress = "sequitur+progress"

	// p6ThrashMinUses is the argument the Sequitur arms pass to Thrash. Thrash reports rules whose
	// multiplicity EXCEEDS minUses (§5.11's "exceeds"), so 2 admits a rule used three times — which
	// is the detector's own MinRepeats of 3. The two arms are compared at the same repeat threshold
	// or they are not compared at all.
	p6ThrashMinUses = 2

	// p6SelfSuppressionTurns is how many turns of fed-back warning text the closed-loop check
	// replays. It is long enough that a detector which counted its own output would cross every
	// threshold it has several times over.
	p6SelfSuppressionTurns = 30
)

// p6Obs is one observation in a committed stream.
type p6Obs struct {
	Turn   int    `json:"turn"`
	Target string `json:"target"`
	Action string `json:"action"`
	// Failure is the canonicalized failure signature, empty when the action did not fail.
	Failure string `json:"failure"`
	// Progress is what was observed to change since the previous occurrence of this state, spelled
	// as Progress.String() spells it: none, observed, unknown.
	Progress string `json:"progress"`
	// PartialCoverage marks an observation whose dependency coverage is incomplete even though
	// progress is known — contract §5's second, independent source of Uncertain.
	PartialCoverage bool `json:"partialCoverage,omitempty"`
	// Symbols is the action stream this observation contributes, which is what the Sequitur arms
	// see. It is declared by the fixture rather than derived here so that both sides of the
	// ablation read the same committed bytes: a symbol stream this file synthesized would be a
	// measurement of my derivation rule, not of Sequitur. Empty means [Action].
	Symbols []string `json:"symbols,omitempty"`
}

// p6Stream is one committed held-out stream.
type p6Stream struct {
	// Name matches the file's basename.
	Name string `json:"name"`
	// Label is the held-out ground truth: progress or stuck.
	Label string `json:"label"`
	// Why records what the stream exists to demonstrate and where it came from.
	Why string `json:"why"`
	// Goal is the goal in force for every observation in the stream.
	Goal string `json:"goal"`
	// Observations are the stream's observations, in turn order.
	Observations []p6Obs `json:"observations"`
}

// p6Progress maps a fixture's spelling of a progress value onto the type.
var p6Progress = map[string]grammar.Progress{
	"none":     grammar.ProgressNone,
	"observed": grammar.ProgressObserved,
	"unknown":  grammar.ProgressUnknown,
}

// p6RequiredStreams is the coverage contract for the stream directory: the held-out set must keep
// both the case the state signature is expected to miss and the case that closes the feedback
// loop, or the ablation and the self-suppression bound would both be measuring a smaller corpus
// than the report claims.
var p6RequiredStreams = []string{
	"long-period-cycle-five-targets",
	"qompack-self-traffic",
	"stuck-with-partial-coverage",
}

// loadP6Streams reads every committed stream, in filename order.
func loadP6Streams(root string) ([]p6Stream, error) {
	paths, err := filepath.Glob(filepath.Join(root, p6FixtureDir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("phase 6: globbing %s: %w", p6FixtureDir, err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("phase 6: no streams in %s", p6FixtureDir)
	}
	sort.Strings(paths)

	out := make([]p6Stream, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p) //nolint:gosec // a committed test fixture path
		if err != nil {
			return nil, fmt.Errorf("phase 6: reading %s: %w", p, err)
		}
		var s p6Stream
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("phase 6: decoding %s: %w", filepath.Base(p), err)
		}
		if base := filepath.Base(p); s.Name+".json" != base {
			return nil, fmt.Errorf("phase 6: stream %q lives in %s; a stream's name must match its filename",
				s.Name, base)
		}
		if s.Label != p6LabelProgress && s.Label != p6LabelStuck {
			return nil, fmt.Errorf("phase 6: %s is labelled %q; the held-out labels are %q and %q",
				s.Name, s.Label, p6LabelProgress, p6LabelStuck)
		}
		if s.Why == "" {
			return nil, fmt.Errorf("phase 6: %s must say what it exists to demonstrate", s.Name)
		}
		if len(s.Observations) == 0 {
			return nil, fmt.Errorf("phase 6: %s has no observations", s.Name)
		}
		for _, o := range s.Observations {
			if _, ok := p6Progress[o.Progress]; !ok {
				return nil, fmt.Errorf("phase 6: %s: unknown progress value %q", s.Name, o.Progress)
			}
		}
		out = append(out, s)
	}

	present := make(map[string]bool, len(out))
	for _, s := range out {
		present[s.Name] = true
	}
	for _, name := range p6RequiredStreams {
		if !present[name] {
			return nil, fmt.Errorf("phase 6: %s is missing the %q stream", p6FixtureDir, name)
		}
	}
	return out, nil
}

// signature builds the four-part state signature for one observation of s.
func (s p6Stream) signature(o p6Obs) grammar.StateSignature {
	return grammar.StateSignature{Goal: s.Goal, Target: o.Target, Action: o.Action, Failure: o.Failure}
}

// symbols returns the action symbols o contributes to a Sequitur arm, defaulting to the action.
func (o p6Obs) symbols() []grammar.Symbol {
	if len(o.Symbols) == 0 {
		return []grammar.Symbol{grammar.Symbol(o.Action)}
	}
	out := make([]grammar.Symbol, len(o.Symbols))
	for i, sym := range o.Symbols {
		out[i] = grammar.Symbol(sym)
	}
	return out
}

// p6StreamOutcome is one arm's verdict on one stream.
type p6StreamOutcome struct {
	Stream string `json:"stream"`
	Label  string `json:"label"`
	// Fired reports whether the arm produced at least one warning on this stream.
	Fired bool `json:"fired"`
	// FirstTurn is the turn the first warning was produced at, or -1 when none was.
	FirstTurn int `json:"firstTurn"`
	// Delivered is how many warnings the arm produced, after dedup and the per-session cap.
	Delivered int `json:"delivered"`
	// Uncertain counts the delivered warnings that carry the uncertain qualification. It is only
	// ever non-zero on the state-signature arm: a grammar over action symbols has no notion of
	// observation coverage and therefore no way to be unsure about it, which is itself part of the
	// ablation's answer.
	Uncertain int `json:"uncertain"`
	// Verdict names the confusion-matrix cell this stream fell in for this arm.
	Verdict string `json:"verdict"`
}

// p6ArmResult is one arm measured over the whole held-out set.
type p6ArmResult struct {
	Arm string `json:"arm"`
	// Streams are the per-stream verdicts, in fixture order.
	Streams []p6StreamOutcome `json:"streams"`
	// Stuck and Progress are the held-out sample sizes: how many streams carried each label.
	Stuck    int `json:"stuckStreams"`
	Progress int `json:"progressStreams"`
	// Detected and Missed partition the stuck streams; FalseAlarms and Quiet partition the
	// progress ones. FALSE ALARMS ARE REPORTED, NOT HIDDEN: the row exists so that a detector
	// cannot be recommended on its detection count alone.
	Detected    int `json:"detected"`
	Missed      int `json:"missed"`
	FalseAlarms int `json:"falseAlarms"`
	Quiet       int `json:"quiet"`
	// Delivered is the total number of warnings produced across every stream.
	Delivered int `json:"delivered"`
	// AllocBytes and Mallocs are the arm's allocation cost over the whole set. They are RECORDED,
	// never asserted: they are stable enough to compare orders of magnitude and not stable enough
	// to gate on.
	AllocBytes uint64 `json:"allocBytes"`
	Mallocs    uint64 `json:"mallocs"`
	// WallMicros is the arm's wall time in microseconds. It is recorded and never asserted, for
	// the plainest of reasons: wall time on a loaded machine is not a property of the code.
	WallMicros int64 `json:"wallMicros"`
	// Rules and CompressedSymbols are the structural cost of the grammar arms — how many rules were
	// induced and how long the compressed sequence ended up. They are zero on the signature arm,
	// which induces no grammar at all, and unlike the timings they are exactly reproducible.
	Rules             int `json:"rules"`
	CompressedSymbols int `json:"compressedSymbols"`
}

// verdictOf names the confusion-matrix cell a (label, fired) pair falls in.
func verdictOf(label string, fired bool) string {
	switch {
	case label == p6LabelStuck && fired:
		return "detected"
	case label == p6LabelStuck:
		return "MISSED"
	case fired:
		return "FALSE-ALARM"
	default:
		return "quiet"
	}
}

// tally folds one stream outcome into the arm's counters.
func (r *p6ArmResult) tally(o p6StreamOutcome) {
	r.Streams = append(r.Streams, o)
	r.Delivered += o.Delivered
	switch o.Verdict {
	case "detected":
		r.Stuck++
		r.Detected++
	case "MISSED":
		r.Stuck++
		r.Missed++
	case "FALSE-ALARM":
		r.Progress++
		r.FalseAlarms++
	default:
		r.Progress++
		r.Quiet++
	}
}

// p6SignatureResult is the state-signature arm's result plus the per-stream detectors, which the
// bounded-delivery and usefulness checks need to read back afterwards.
type p6SignatureResult struct {
	// Result is the arm's confusion matrix and cost.
	Result p6ArmResult
	// Detectors holds each stream's detector, keyed by stream name, after the stream was replayed.
	Detectors map[string]*grammar.Detector
	// Warnings holds each stream's produced warnings, keyed by stream name, in production order.
	Warnings map[string][]grammar.StateWarning
	// Config is the bounds the arm ran under, read back off a detector rather than restated, so
	// the report and the run cannot disagree about the thresholds.
	Config grammar.DetectorConfig
}

// runSignatureArm replays every stream through the shipped Detector.
//
// One detector per stream, because MaxPerSession is per SESSION and a shared detector would report
// the corpus as one very long conversation — which would make the cap, not the streams, the thing
// being measured. The held-out label is fed back through RecordOutcome afterwards so that the
// usefulness and false-alarm counters carry the same verdicts this function computes; that is
// contract §5.6's separation made concrete, and it is one-way by construction (nothing in
// statewarn.go reads an Outcome back into a decision).
func runSignatureArm(streams []p6Stream) p6SignatureResult {
	out := p6SignatureResult{
		Result:    p6ArmResult{Arm: p6ArmSignature},
		Detectors: make(map[string]*grammar.Detector, len(streams)),
		Warnings:  make(map[string][]grammar.StateWarning, len(streams)),
		Config:    grammar.DefaultDetectorConfig(),
	}

	start := time.Now()
	before := heapProfile()
	for _, s := range streams {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		out.Config = d.Config()
		var produced []grammar.StateWarning
		for _, o := range s.Observations {
			w, ok := d.Observe(grammar.Observation{
				Signature:       s.signature(o),
				Turn:            core.TurnIndex(o.Turn),
				Progress:        p6Progress[o.Progress],
				PartialCoverage: o.PartialCoverage,
				Symbols:         o.symbols(),
			})
			if ok {
				produced = append(produced, w)
			}
		}

		outcome := p6StreamOutcome{
			Stream: s.Name, Label: s.Label, Fired: len(produced) > 0,
			FirstTurn: -1, Delivered: len(produced),
		}
		if len(produced) > 0 {
			outcome.FirstTurn = int(produced[0].Turns[len(produced[0].Turns)-1])
		}
		for _, w := range produced {
			if w.Uncertain {
				outcome.Uncertain++
			}
		}
		outcome.Verdict = verdictOf(s.Label, outcome.Fired)

		// The held-out label IS the judgement: a warning on a progress stream is a false alarm and
		// is recorded as one, whichever way that makes the numbers look.
		judgement := grammar.OutcomeUseful
		if s.Label == p6LabelProgress {
			judgement = grammar.OutcomeFalseAlarm
		}
		for _, w := range produced {
			d.RecordOutcome(w.DedupKey, judgement)
		}

		out.Detectors[s.Name] = d
		out.Warnings[s.Name] = produced
		out.Result.tally(outcome)
	}
	out.Result.WallMicros = time.Since(start).Microseconds()
	out.Result.AllocBytes, out.Result.Mallocs = heapDelta(before)
	return out
}

// runSequiturArm replays every stream through a grammar-based detector: append the stream's action
// symbols, and treat a rule Thrash reports as a loop.
//
// It is given the SAME delivery bounds as the signature arm — the same repeat threshold through
// p6ThrashMinUses, one warning per distinct thrashing rule, and the same MaxPerSession cap — so
// that the comparison is between two ways of NOTICING a loop rather than between two volume
// policies. Self-originated observations are excluded here exactly as they are there; bound 5 is
// not one of the things under test.
//
// withProgress is the third arm. It suppresses a warning when anything in the current dedup window
// reported observed progress, which is the closest analogue of bound 2 that a grammar over action
// symbols can be given: the grammar has no signature to attach a latch to, so the latch is
// window-wide. That is not a neutral choice and the report says so — window-wide suppression is
// more aggressive than the signature arm's per-signature latch, which helps this arm on false
// alarms and can only cost it detections. It is included because the obvious objection to arm two
// is that Sequitur was denied a signal it could have had.
func runSequiturArm(streams []p6Stream, withProgress bool, arm string) p6ArmResult {
	res := p6ArmResult{Arm: arm}
	cfg := grammar.DefaultDetectorConfig()

	start := time.Now()
	before := heapProfile()
	for _, s := range streams {
		g := grammar.New()
		warned := make(map[grammar.RuleID]bool, cfg.MaxPerSession)
		progressWindows := make(map[int]bool, len(s.Observations))
		delivered, firstTurn := 0, -1

		if withProgress {
			for _, o := range s.Observations {
				if p6Progress[o.Progress] == grammar.ProgressObserved {
					progressWindows[o.Turn/int(cfg.WindowTurns)] = true
				}
			}
		}

		for _, o := range s.Observations {
			sig := s.signature(o)
			if grammar.IsSelfOriginated(sig) {
				continue
			}
			for _, sym := range o.symbols() {
				g.Append(sym)
			}
			if withProgress && progressWindows[o.Turn/int(cfg.WindowTurns)] {
				continue
			}
			for _, rule := range g.Thrash(p6ThrashMinUses) {
				if warned[rule.ID] || delivered >= cfg.MaxPerSession {
					continue
				}
				warned[rule.ID] = true
				delivered++
				if firstTurn < 0 {
					firstTurn = o.Turn
				}
			}
		}

		res.Rules += len(g.Rules())
		res.CompressedSymbols += len(g.Compressed())

		outcome := p6StreamOutcome{
			Stream: s.Name, Label: s.Label, Fired: delivered > 0,
			FirstTurn: firstTurn, Delivered: delivered,
		}
		outcome.Verdict = verdictOf(s.Label, outcome.Fired)
		res.tally(outcome)
	}
	res.WallMicros = time.Since(start).Microseconds()
	res.AllocBytes, res.Mallocs = heapDelta(before)
	return res
}

// heapProfile takes the allocation reading heapDelta subtracts from.
//
// It forces a GC first so that the reading is not dominated by whatever the previous arm left
// behind. TotalAlloc and Mallocs are cumulative and never decrease, so the difference of two
// readings is the allocation the work between them performed — an approximation, because the
// runtime allocates on its own account too, and reported as one.
func heapProfile() runtime.MemStats {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m
}

// heapDelta returns the bytes and allocation count since before.
func heapDelta(before runtime.MemStats) (bytes, mallocs uint64) {
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc, after.Mallocs - before.Mallocs
}

// p6DecisionKey renders an arm's decisions as the canonical string two runs are compared on. It
// deliberately omits every cost field: allocations and wall time are measurements of the machine
// and do not have to reproduce, while WHICH streams fired and WHEN must.
func p6DecisionKey(r p6ArmResult) string {
	var b strings.Builder
	b.WriteString(r.Arm)
	for _, s := range r.Streams {
		fmt.Fprintf(&b, "|%s:%s:%d:%d:%d", s.Stream, s.Verdict, s.FirstTurn, s.Delivered, s.Uncertain)
	}
	return b.String()
}

// p6BoundedDelivery is the bounded-delivery evidence of contract §5.4 and §5.5, measured over the
// held-out set rather than asserted on a synthetic burst.
type p6BoundedDelivery struct {
	// MaxPerSession is the cap in force, read back off the detector.
	MaxPerSession int `json:"maxPerSession"`
	// MaxDelivered is the largest number of warnings any one stream produced.
	MaxDelivered int `json:"maxDelivered"`
	// DedupCollapsed and CapSuppressed are how many would-be warnings each bound refused, summed
	// across the corpus.
	DedupCollapsed int `json:"dedupCollapsed"`
	CapSuppressed  int `json:"capSuppressed"`
	// ProgressSuppressed and SelfSuppressed are the other two suppression bounds.
	ProgressSuppressed int `json:"progressSuppressed"`
	SelfSuppressed     int `json:"selfSuppressed"`
	// ExpiredAtDelivery is how many produced warnings were refused at delivery time because the
	// session had moved past ExpiresAt.
	ExpiredAtDelivery int `json:"expiredAtDelivery"`
	// RepeatedStates is raw repeated-action frequency, which contract §5.6 requires to be counted
	// SEPARATELY from usefulness. It is here so the two can be seen not to track each other.
	RepeatedStates int `json:"repeatedStates"`
	// Useful and FalseAlarms are the judged outcomes, from the held-out labels.
	Useful      int `json:"useful"`
	FalseAlarms int `json:"falseAlarms"`
	// AmplificationWarnings is how many warnings the closed loop produced when every delivered
	// warning was rendered and fed back in as the session's next input. It must be zero.
	AmplificationWarnings int `json:"amplificationWarnings"`
}

// measureBoundedDelivery reads the delivery bounds back out of a completed signature run and then
// closes the loop.
//
// The closed loop is the part that cannot be established any other way. Every delivered warning is
// rendered through the frozen FormatWarning, and the resulting line is fed back as the goal, the
// target, the action and the failure of thirty further observations — the shape a warning takes
// when it lands in a prompt and comes back around as the next turn's text. Bound 5 says a warning
// can never cause the next warning; the number this returns is what that claim costs if it is
// wrong, and it must be zero.
func measureBoundedDelivery(streams []p6Stream, run p6SignatureResult) p6BoundedDelivery {
	out := p6BoundedDelivery{MaxPerSession: run.Config.MaxPerSession}

	for _, s := range streams {
		d := run.Detectors[s.Name]
		produced := run.Warnings[s.Name]
		if len(produced) > out.MaxDelivered {
			out.MaxDelivered = len(produced)
		}

		// Expiry is enforced at DELIVERY, which is a different moment from production: the
		// detector runs on PostToolUse and the warning is injected on the next UserPromptSubmit.
		// Asking one turn past ExpiresAt is asking the question the injection path asks.
		for _, w := range produced {
			if !d.Deliverable(w, w.ExpiresAt+1) {
				out.ExpiredAtDelivery++
			}
		}

		for _, w := range produced {
			line := grammar.FormatWarning(w.Warning)
			for i := range p6SelfSuppressionTurns {
				turn := core.TurnIndex(len(s.Observations) + i)
				if _, ok := d.Observe(grammar.Observation{
					Signature: grammar.StateSignature{
						Goal: line, Target: line, Action: line, Failure: line,
					},
					Turn: turn,
				}); ok {
					out.AmplificationWarnings++
				}
			}
		}

		st := d.Stats()
		out.DedupCollapsed += st.Deduplicated
		out.CapSuppressed += st.CapSuppressed
		out.ProgressSuppressed += st.ProgressSuppressed
		out.SelfSuppressed += st.SelfSuppressed
		out.RepeatedStates += st.RepeatedStates
		out.Useful += st.Useful
		out.FalseAlarms += st.FalseAlarms
	}
	return out
}

// p6CheckBounds asserts the six bounds of contract §5 that this corpus can establish, and it is
// the whole of what phase 6 GATES on.
//
// Nothing about Sequitur is asserted anywhere. Blocker M6-U15-sequitur-value leaves it optional
// and disabled until it demonstrably earns its place, so a gate that required it to behave would
// be asserting the presence of a thing the project has not adopted. The ablation is reported and
// its verdict recorded; it is not a condition.
func p6CheckBounds(streams []p6Stream, run p6SignatureResult, bd p6BoundedDelivery) error {
	cfg := run.Config
	for _, s := range streams {
		produced := run.Warnings[s.Name]
		if len(produced) > cfg.MaxPerSession {
			return fmt.Errorf("phase 6 (%s): %d warnings delivered, over MaxPerSession=%d",
				s.Name, len(produced), cfg.MaxPerSession)
		}
		seen := make(map[string]bool, len(produced))
		for _, w := range produced {
			if seen[w.DedupKey] {
				return fmt.Errorf("phase 6 (%s): dedup key %s was delivered twice", s.Name, w.DedupKey)
			}
			seen[w.DedupKey] = true
			if w.Progress == grammar.ProgressUnknown && !w.Uncertain {
				return fmt.Errorf("phase 6 (%s): a warning produced from unknown coverage is not "+
					"marked uncertain (bound 3)", s.Name)
			}
			if w.Repeats < cfg.MinRepeats {
				return fmt.Errorf("phase 6 (%s): a warning fired at %d repeats, under MinRepeats=%d",
					s.Name, w.Repeats, cfg.MinRepeats)
			}
		}
	}
	if bd.AmplificationWarnings != 0 {
		return fmt.Errorf("phase 6: the closed loop produced %d warnings; a warning that can cause "+
			"the next warning is the feedback loop bound 5 exists to forbid", bd.AmplificationWarnings)
	}
	if bd.Useful+bd.FalseAlarms != run.Result.Delivered {
		return fmt.Errorf("phase 6: %d warnings were delivered but %d were judged; every delivered "+
			"warning carries a held-out verdict", run.Result.Delivered, bd.Useful+bd.FalseAlarms)
	}
	return nil
}

// p6ArmLine renders one arm's confusion matrix and cost for a log or a phase-check summary.
func p6ArmLine(r p6ArmResult) string {
	return fmt.Sprintf(
		"  %-18s detected=%d/%d false-alarms=%d/%d delivered=%d  alloc=%7dB mallocs=%6d "+
			"wall=%6dus rules=%d compressed=%d",
		r.Arm, r.Detected, r.Detected+r.Missed, r.FalseAlarms, r.FalseAlarms+r.Quiet,
		r.Delivered, r.AllocBytes, r.Mallocs, r.WallMicros, r.Rules, r.CompressedSymbols)
}

// p6Verdict is the M6-G15-B disposition, computed from the measured arms rather than written down
// in advance.
//
// The rule it applies is the one blocker M6-U15-sequitur-value states: Sequitur stays OPTIONAL AND
// DISABLED until it demonstrably earns its place. Earning it means being better on the axis that
// matters — fewer false alarms at no loss of detection, or more detection at no cost in false
// alarms — because a detector that finds more loops by warning more often has not found anything.
// If neither Sequitur arm clears that bar, the disposition is "Sequitur not justified by this
// evidence", and that is a legitimate outcome rather than a failure of the measurement.
func p6Verdict(sig p6ArmResult, arms ...p6ArmResult) string {
	for _, a := range arms {
		betterDetection := a.Detected > sig.Detected && a.FalseAlarms <= sig.FalseAlarms
		fewerAlarms := a.FalseAlarms < sig.FalseAlarms && a.Detected >= sig.Detected
		if betterDetection || fewerAlarms {
			return fmt.Sprintf("Sequitur EARNS its place on this evidence via the %q arm "+
				"(detected %d vs %d, false alarms %d vs %d) — a Main decision, not an automatic "+
				"default flip", a.Arm, a.Detected, sig.Detected, a.FalseAlarms, sig.FalseAlarms)
		}
	}
	return "Sequitur not justified by this evidence: no arm improves on the state signature " +
		"without paying for it elsewhere; it stays OPTIONAL AND DISABLED per M6-U15-sequitur-value"
}

// runP6Arms replays the held-out set through all three arms and returns them in report order.
func runP6Arms(streams []p6Stream) (p6SignatureResult, p6ArmResult, p6ArmResult) {
	return runSignatureArm(streams),
		runSequiturArm(streams, false, p6ArmSequitur),
		runSequiturArm(streams, true, p6ArmSequiturProgress)
}

// phase6 is §10 Phase 6's exit criterion as gates M6-G15-A and M6-G15-B state it: bounded,
// deduplicated, non-amplifying warnings with a held-out false-alarm and usefulness report, and an
// ablation of Sequitur against the simpler state signature.
//
// Like phase5 it is SELF-CONTAINED, for the same reason: a phase check that depended on a policy
// being in --policies would fail on every run that did not include it, which is how a gate gets
// turned off. It gates on the six bounds of contract §5 and on nothing else; the ablation's
// numbers and its disposition are printed, because M6-U15-sequitur-value leaves Sequitur optional
// and disabled and a gate cannot require a thing the project has not adopted.
//
// The driver Context is unused here too, for the reason phase5 gives.
func phase6(_ Context) error {
	root, err := repoRoot()
	if err != nil {
		return fmt.Errorf("phase 6: %w", err)
	}
	streams, err := loadP6Streams(root)
	if err != nil {
		return err
	}

	sig, seq, seqProg := runP6Arms(streams)
	bd := measureBoundedDelivery(streams, sig)
	if err := p6CheckBounds(streams, sig, bd); err != nil {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "phase 6: %d held-out streams (%d stuck, %d progress); "+
		"minRepeats=%d window=%d maxPerSession=%d expiry=%d\n",
		len(streams), sig.Result.Stuck, sig.Result.Progress,
		sig.Config.MinRepeats, int(sig.Config.WindowTurns), sig.Config.MaxPerSession,
		int(sig.Config.ExpiryTurns))
	for _, arm := range []p6ArmResult{sig.Result, seq, seqProg} {
		fmt.Fprintln(&b, p6ArmLine(arm))
	}
	fmt.Fprintf(&b, "  bounds: dedup=%d cap=%d progress=%d self=%d expired=%d "+
		"repeated-states=%d useful=%d false-alarms=%d amplification=%d\n",
		bd.DedupCollapsed, bd.CapSuppressed, bd.ProgressSuppressed, bd.SelfSuppressed,
		bd.ExpiredAtDelivery, bd.RepeatedStates, bd.Useful, bd.FalseAlarms, bd.AmplificationWarnings)
	fmt.Fprintf(&b, "  M6-G15-B: %s\n", p6Verdict(sig.Result, seq, seqProg))
	fmt.Print(b.String())
	return nil
}
