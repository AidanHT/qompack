package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// This file is gate M5-G15-A's replay half: the representation selector measured on the committed
// held-out trials in testdata/phase5 and, at scale, on the committed 24-session synthetic corpus,
// against the pre-SP-15 complete-record heuristic it replaces.
//
// ── WHAT THESE ROWS PROVE, AND WHAT THEY DO NOT ────────────────────────────────────────────
//
// They prove FEASIBILITY and DISPOSITION, both of which are properties of an output and can be
// checked exactly. On every trial and at every corpus compaction point the proposal carries at
// most one representation per item, delivers the dependency closure of everything it carries,
// spends no more than its budget, contains nothing before p, keeps every qualification the
// candidate offered it under, and reports an overflow explicitly — naming the record and giving it
// an archive recovery path — rather than serializing part of a constraint set and calling it a
// success. Two Propose calls on equal inputs return equal Proposals, Iters included.
//
// They do NOT prove that selection produces a better context. The weights are a DECLARED
// backward-looking prior authored for this measurement, the representation costs are the
// uncalibrated estimates blocker M5-U15-representation-overhead says must stay estimates until the
// assembled model is calibrated against provider-reported usage, and per-item Coverage is a number
// the candidate builder assigns rather than a measured contribution to anything. The
// demand-satisfaction column below is the closest thing here to an external quality signal, and it
// is a count of eval.Demands satisfied on a SYNTHETIC corpus — the same corpus §10 Phase 0's own
// report labels as synthetic — not a measurement of task completion. No number in this file may
// flip a default: the plan says "no automatic default flip from a mock or synthetic score", and
// both SP-15 switches stay false in every configuration these tests touch.
//
// The corpus arm does NOT exercise dependency closure. analyzer.NewCandidates computes Requires
// from a dag.Graph and this arm has none, so it reports core.ErrDegraded alongside a valid
// candidate set — role C's C-2 rule that a failed comparison degrades COVERAGE rather than the
// answer — and the closure is empty everywhere. The corpus test asserts that the error IS reported
// (require.ErrorIs on every point) rather than swallowed, because an empty Requires presented as an
// established one would be the stronger claim made on no evidence. Closure itself is exercised,
// exactly and by hand, in the dependency-closure-and-archive trial.
//
// There is NO SEED. Nothing in this file draws a random number: the fixtures and the corpus are
// committed bytes, the block derivation is a pure function of a session, and the selector is
// deterministic by contract §3. Reproducibility is asserted directly instead, which is a stronger
// statement than quoting a seed would be.

const (
	// p5CorpusDir is the committed synthetic corpus, relative to the repository root. It is the
	// same 24-session corpus §10 Phase 0's number is computed over and phase4_test.go replays.
	p5CorpusDir = "testdata/sessions/synthetic"

	// p5RootTurnPosDomain separates the corpus arm's synthetic evidence roots. Two blocks deriving
	// from the same (turn, prefix position) came from the same bytes — eval.Blocks gives a file
	// block the position and token count of the tool result that produced it — so they share a
	// root and a proposal carrying both genuinely re-delivers one piece of evidence twice. That is
	// the redundancy amendment A1 prices, recovered from the corpus rather than invented.
	p5RootTurnPosDomain = "qompack.replay.phase5.corpus-root.v1"

	// p5TinyBudgetDivisor turns a compaction point's generous budget into its tiny one. A twentieth
	// is small enough that most of the candidate set cannot be carried and large enough that the
	// answer is a selection rather than a rounding artefact.
	p5TinyBudgetDivisor = 20

	// p5RecencyFloor is the weight a block at the very start of the prefix keeps. It is not zero:
	// an old block is weaker evidence, not no evidence, and a zero would make the whole early
	// prefix invisible to both arms at once.
	p5RecencyFloor = 0.5

	// p5ExactRatioDir holds role D's small instances with their brute-forced optima.
	p5ExactRatioDir = "internal/analyzer/testdata/objective"
)

// p5KindWeight is the declared backward-looking prior the corpus arm ranks by, per eval block kind.
//
// It is a PRIOR, not a measurement, and it is stated here so a reader can disagree with it in one
// place. The order follows what a compaction is for: a recorded elimination and a recorded decision
// are cheap, distilled and expensive to rediscover; a file's content is the thing the session is
// actually about; a raw tool result is bulk; a conversational turn is the cheapest thing to
// paraphrase. Nothing about these six numbers is measured, and the whole point of holding them
// FIXED ACROSS BOTH ARMS is that a difference between the arms is then a difference in mechanism
// rather than in scoring.
var p5KindWeight = map[eval.BlockKind]float64{
	eval.BlockElimination: 1.00,
	eval.BlockDecision:    0.90,
	eval.BlockFile:        0.80,
	eval.BlockToolResult:  0.60,
	eval.BlockUserPrompt:  0.50,
	eval.BlockAssistant:   0.45,
}

// ── the held-out trials ─────────────────────────────────────────────────────────────────────

// TestPhase5_HeldOutTrialOutcomes is gate M5-G15-A on the committed trials: every trial at every
// one of contract §3's three budget regimes, with the four feasibility rules, the G6.3
// dispositions and the determinism requirement all checked by the same p5CheckOutcomes the
// driver's phase check runs.
//
// It runs the checks through runP5Trials rather than restating them so that the CI form of this
// gate and its test form cannot drift apart. A gate whose two forms disagree proves whichever of
// them is weaker.
func TestPhase5_HeldOutTrialOutcomes(t *testing.T) {
	// scheduler.PSelectionAvailable is a process-wide atomic, so a case that opens it may not run
	// in parallel with one that does not: t.Parallel() here would make some other test's selector
	// construction succeed or fail depending on scheduling.
	root := repoRootOf(t)
	outcomes, err := runP5Trials(context.Background(), root)
	require.NoError(t, err)
	require.NotEmpty(t, outcomes)

	var lines strings.Builder
	for _, o := range outcomes {
		fmt.Fprintf(&lines, "\n%s", p5Line(o))
	}
	t.Logf("M5-G15-A held-out selection trials (%d rows over %d trials):%s",
		len(outcomes), len(p5RequiredTrials), lines.String())
	writeSP15Artifact(t, "phase5-trials.json", outcomes)
}

// TestPhase5_G63DispositionsAreTheOnesTheContractNames is the G6.3 row read back out of the
// measured outcomes, so the three scenarios the M5-G15-A row names are visible as three named
// assertions rather than as a property buried inside a loop.
//
// Scenario 1: a current authoritative elimination is carried, with its ORIGINAL evidence root, at
// active evidence. Scenario 2: the same record under a budget that cannot hold it produces an
// explicit overflow that names it and gives it a recovery path — never a partial serialization.
// Scenario 3: a stale or uncertain record keeps its qualification, never binds, and therefore
// never forces an overflow even at a budget of zero.
func TestPhase5_G63DispositionsAreTheOnesTheContractNames(t *testing.T) {
	scheduler.EnablePSelection()
	t.Cleanup(scheduler.DisablePSelection)

	root := repoRootOf(t)
	trials, err := loadP5Trials(root)
	require.NoError(t, err)
	byName := make(map[string]p5Trial, len(trials))
	for _, tr := range trials {
		byName[tr.Name] = tr
	}

	t.Run("current_authoritative_is_carried_with_its_evidence", func(t *testing.T) {
		tr := byName["g63-current-authoritative"]
		prop := p5ProposeAt(t, tr, "generous")
		rep, ok := p5Find(prop, "elimination:e1")
		require.True(t, ok, "the binding record must be carried when the budget holds it")
		require.True(t, rep.Prov.Qualification.Active(),
			"a binding record carried at %s evidence is a constraint the consumer must decline to "+
				"treat as binding, so it would be lost in transit while the proposal reported success",
			rep.Prov.Qualification)
		require.Equal(t, p5Root("ev-elim-1"), rep.Prov.Root,
			"Prov.Root names the ORIGINAL evidence; a derivative that overwrote it would break the "+
				"chain back to the first observation")
		require.False(t, prop.Overflow)
	})

	t.Run("the_same_record_overflows_explicitly_with_a_recovery_path", func(t *testing.T) {
		tr := byName["g63-current-authoritative"]
		prop := p5ProposeAt(t, tr, "mandatory-overflow")
		require.True(t, prop.Overflow)
		require.Equal(t, dag.NodeID("elimination:e1"), prop.Item)
		require.Contains(t, prop.Reason, "elimination:e1", "an overflow must name the record it lost")
		require.Equal(t, []dag.NodeID{"elimination:e1"}, prop.Archive,
			"the overflowing record is archived, which is what makes the outcome recoverable rather "+
				"than a silent loss")
		require.Nil(t, prop.Chosen, "an overflow is never a partial serialization")
		require.Zero(t, int(prop.Tokens))
	})

	t.Run("stale_and_uncertain_records_never_become_active_constraints", func(t *testing.T) {
		tr := byName["g63-stale-and-uncertain"]

		zero := p5ProposeAt(t, tr, "mandatory-overflow")
		require.False(t, zero.Overflow,
			"two Mandatory records that carry no active evidence must not be able to force an "+
				"overflow: promoting them would turn a recorded 'we can no longer establish this' "+
				"into a hard 'do not try this'")
		require.Nil(t, zero.Chosen)

		generous := p5ProposeAt(t, tr, "generous")
		for _, item := range []dag.NodeID{
			"elimination:stale-pool-timeout", "elimination:uncertain-retry-budget",
		} {
			rep, ok := p5Find(generous, item)
			require.True(t, ok, "%s must still travel; it is qualified, not suppressed", item)
			require.False(t, rep.Prov.Qualification.Active(),
				"%s was carried as %s, which would make it bind", item, rep.Prov.Qualification)
		}
	})
}

// TestPhase5_HeuristicAgainstExactRatiosRecorded records what role D measured: the heuristic's
// objective value against the brute-forced optimum of the SAME declared objective, on the small
// instances in internal/analyzer/testdata/objective.
//
// IT ASSERTS NO BOUND, and the omission is the finding. Contract §3 claims none — the redundancy
// term is subtracted, which does not preserve monotonicity, and the dependency and
// one-representation-per-item constraints change the feasible family — so a (1−1/e) assertion here
// would be a claim about a family of problems this objective does not belong to. 0.738 on the
// non-monotone instance is the honest number and is the reason no such claim exists anywhere in
// SP-15.
//
// The one thing it does assert is that the heuristic never EXCEEDS the exact optimum, which is not
// an approximation guarantee but a consistency check: a heuristic scoring above the enumerated
// best means one of the two objectives is wrong, and that is worth failing over.
//
// The optima are read from the committed fixtures rather than re-enumerated. Their fidelity is
// established next door — internal/analyzer/objective_test.go brute-forces every one of them on
// every run and fails if a fixture drifted under it — so re-enumerating here would duplicate two
// hundred lines to re-derive a number that already has a live guard.
func TestPhase5_HeuristicAgainstExactRatiosRecorded(t *testing.T) {
	scheduler.EnablePSelection()
	t.Cleanup(scheduler.DisablePSelection)

	root := repoRootOf(t)
	paths, err := filepath.Glob(filepath.Join(root, p5ExactRatioDir, "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "role D's committed instances are the source of these ratios")
	sort.Strings(paths)

	type row struct {
		Instance  string  `json:"instance"`
		Heuristic float64 `json:"heuristic"`
		Exact     float64 `json:"exact"`
		Ratio     float64 `json:"ratio"`
		Tokens    int     `json:"tokens"`
		Budget    int     `json:"budget"`
		Iters     int     `json:"iters"`
		Overflow  bool    `json:"overflow"`
	}
	var rows []row
	var lines strings.Builder
	for _, p := range paths {
		f := p5LoadObjectiveFixture(t, p)
		prop, err := analyzer.Propose(context.Background(), f.P, f.candidates(t), f.Lambda,
			core.Tokens(f.Budget))
		require.NoError(t, err, "%s", f.Name)

		ratio := 1.0
		if f.Optimum > p5Tolerance {
			ratio = prop.Value / f.Optimum
		}
		require.LessOrEqual(t, prop.Value, f.Optimum+p5Tolerance,
			"%s: the heuristic reported %.9f against an exact optimum of %.9f, so one of the two "+
				"objectives is wrong", f.Name, prop.Value, f.Optimum)

		rows = append(rows, row{
			Instance: f.Name, Heuristic: prop.Value, Exact: f.Optimum, Ratio: ratio,
			Tokens: int(prop.Tokens), Budget: f.Budget, Iters: prop.Iters, Overflow: prop.Overflow,
		})
		fmt.Fprintf(&lines, "\n  %-26s heuristic=%.6f exact=%.6f ratio=%.6f tokens=%d/%d iters=%d",
			f.Name, prop.Value, f.Optimum, ratio, int(prop.Tokens), f.Budget, prop.Iters)
	}
	t.Logf("M5-G15-A heuristic-vs-exact ratios, RECORDED and not bounded (%d instances):%s\n"+
		"  no (1-1/e) or any other approximation guarantee is claimed; see the doc comment",
		len(rows), lines.String())
	writeSP15Artifact(t, "phase5-exact-ratios.json", rows)
}

// p5ObjFixture is role D's committed instance shape, read here for its optimum alone.
//
// It is a second, narrower decoder rather than an import because objective_test.go's type lives in
// package analyzer_test and is not importable. The fields this file does not use are declared so
// that DisallowUnknownFields still catches a fixture that grew a field — the tripwire is worth
// more than the six unused lines cost.
type p5ObjFixture struct {
	Name           string  `json:"name"`
	Why            string  `json:"why"`
	P              int     `json:"p"`
	Lambda         float64 `json:"lambda"`
	Budget         int     `json:"budget"`
	Optimum        float64 `json:"optimum"`
	ExpectOverflow bool    `json:"expectOverflow"`
	ExpectArchive  []dag.NodeID
	Candidates     []p5Candidate `json:"candidates"`
}

// candidates converts role D's instance into the real analyzer types, reusing this package's own
// converter so the two files cannot disagree about what a fixture means.
func (f p5ObjFixture) candidates(t *testing.T) []analyzer.Candidate {
	t.Helper()
	cands, err := p5Trial{Name: f.Name, Candidates: f.Candidates}.candidates()
	require.NoError(t, err)
	return cands
}

// p5LoadObjectiveFixture reads one of role D's instances.
func p5LoadObjectiveFixture(t *testing.T, path string) p5ObjFixture {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // a committed test fixture path
	require.NoError(t, err)

	var f p5ObjFixture
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&f), "decoding %s", filepath.Base(path))
	return f
}

// ── the corpus arm ──────────────────────────────────────────────────────────────────────────

// p5CorpusRow is one compaction point of one corpus session under one budget regime.
type p5CorpusRow struct {
	Session string `json:"session"`
	At      int    `json:"at"`
	Regime  string `json:"regime"`
	Budget  int    `json:"budget"`
	// Candidates is how many items were offered at this point, after the p filter.
	Candidates int `json:"candidates"`
	// Binding is how many of them carried active mandatory evidence.
	Binding int `json:"binding"`
	// Selection and Baseline are the two arms on the shared yardstick.
	Selection p5Arm `json:"selection"`
	Baseline  p5Arm `json:"baseline"`
	// SelectionDemands and BaselineDemands count the post-compaction demands each arm's carried
	// set satisfies, out of Demands. It is an EXTERNAL signal — eval.Demands is the harness's own
	// notion of need and neither arm can see it — and it is recorded, never asserted.
	SelectionDemands int  `json:"selectionDemands"`
	BaselineDemands  int  `json:"baselineDemands"`
	Demands          int  `json:"demands"`
	Overflow         bool `json:"overflow"`
	Iters            int  `json:"iters"`
}

// p5CorpusReport is the whole corpus arm.
type p5CorpusReport struct {
	Corpus   string        `json:"corpus"`
	Sessions int           `json:"sessions"`
	Points   int           `json:"points"`
	Lambda   float64       `json:"lambda"`
	Degraded string        `json:"closureDegradedBecause"`
	Rows     []p5CorpusRow `json:"rows"`
}

// TestPhase5_SelectionAgainstTheCompleteRecordHeuristicOnTheCorpus is the scale half of
// M5-G15-A: the selector and the pre-SP-15 complete-record heuristic over every compaction point
// of the committed 24-session synthetic corpus, at all three of contract §3's budget regimes.
//
// The four feasibility rules are asserted at every point. Everything else is RECORDED: how many
// items each arm carried, what that cost, what the shared objective is worth there, how much
// evidence each arm re-delivered, and how many of the post-compaction demands each arm's set
// happens to satisfy. The demand column is the only external signal here and it is not a gate —
// see this file's header for why a synthetic score may not become one.
func TestPhase5_SelectionAgainstTheCompleteRecordHeuristicOnTheCorpus(t *testing.T) {
	scheduler.EnablePSelection()
	t.Cleanup(scheduler.DisablePSelection)

	cfg := config.Defaults()
	lambda := cfg.Selection.Submodular.Lambda
	sessions := p5LoadCorpus(t, cfg)

	rep := p5CorpusReport{
		Corpus: p5CorpusDir, Sessions: len(sessions), Lambda: lambda,
		Degraded: "analyzer.NewCandidates reported core.ErrDegraded: this arm supplies no dag.Graph, " +
			"so Representation.Requires is empty and dependency closure is not exercised here",
	}
	for _, s := range sessions {
		for _, at := range s.CompactionAt {
			cands, degraded := p5CorpusCandidates(t, s, at)
			require.ErrorIs(t, degraded, core.ErrDegraded,
				"%s@%d: a nil graph must degrade COVERAGE and say so, never report an empty closure "+
					"as an established one", s.ID, at)
			if len(cands) == 0 {
				continue
			}
			rep.Points++
			demands := eval.Demands(s, at, core.TurnIndex(len(s.Turns)))
			for _, rg := range p5CorpusRegimes(cands) {
				rep.Rows = append(rep.Rows, p5RunCorpusPoint(t, s, at, cands, rg, lambda, demands))
			}
		}
	}
	require.Greater(t, rep.Points, 0, "a corpus with no compaction point would make the rows vacuous")

	t.Logf("M5-G15-A corpus arm: %d sessions, %d compaction points, %d rows, lambda=%.3f\n%s",
		rep.Sessions, rep.Points, len(rep.Rows), rep.Lambda, p5CorpusSummary(rep))
	writeSP15Artifact(t, "phase5-corpus.json", rep)
}

// p5CorpusRegime is one budget regime derived from a compaction point's own candidate set.
type p5CorpusRegime struct {
	Name   string
	Budget core.Tokens
}

// p5CorpusRegimes derives contract §3's three regimes from the point's own prices, so that
// "generous" and "tiny" mean the same thing at a 200-block point as at a 20-block one.
//
// The overflow probe is one token under the cheapest single binding record, which guarantees the
// mandatory reserve cannot be met whenever a binding record exists. Where none exists the probe is
// a zero budget, and the required outcome is the OTHER half of contract §3's sentence: Chosen is
// nil and Overflow is false, because a zero budget only overflows if something mandatory was there
// to overflow.
func p5CorpusRegimes(cands []analyzer.Candidate) []p5CorpusRegime {
	var generous core.Tokens
	cheapestBinding, haveBinding := core.Tokens(0), false
	for _, c := range cands {
		for _, r := range c.Reps {
			if r.Kind == analyzer.RepExactSpan {
				generous += r.AssembledCost
			}
		}
		if !p5Binds(c) {
			continue
		}
		for _, r := range c.Reps {
			if r.Kind == analyzer.RepArchiveOnly || !r.Prov.Qualification.Active() {
				continue
			}
			if !haveBinding || r.AssembledCost < cheapestBinding {
				cheapestBinding, haveBinding = r.AssembledCost, true
			}
		}
	}
	probe := core.Tokens(0)
	if haveBinding && cheapestBinding > 0 {
		probe = cheapestBinding - 1
	}
	return []p5CorpusRegime{
		{Name: "generous", Budget: generous},
		{Name: "tiny", Budget: generous / p5TinyBudgetDivisor},
		{Name: "mandatory-overflow", Budget: probe},
	}
}

// p5RunCorpusPoint proposes at one point under one regime, asserts feasibility and returns the row.
func p5RunCorpusPoint(t *testing.T, s eval.Session, at core.TurnIndex, cands []analyzer.Candidate,
	rg p5CorpusRegime, lambda float64, demands []eval.Demand,
) p5CorpusRow {
	t.Helper()
	prop, err := analyzer.Propose(context.Background(), 0, cands, lambda, rg.Budget)
	require.NoError(t, err, "%s@%d/%s", s.ID, at, rg.Name)

	where := fmt.Sprintf("%s@%d/%s", s.ID, at, rg.Name)
	seen := make(map[dag.NodeID]bool, len(prop.Chosen))
	var total core.Tokens
	for _, r := range prop.Chosen {
		require.False(t, seen[r.Item], "%s: item %s appears twice in Chosen", where, r.Item)
		seen[r.Item] = true
		require.NotEqual(t, analyzer.RepArchiveOnly, r.Kind, "%s: archive-only delivers nothing", where)
		total += r.AssembledCost
	}
	require.Equal(t, total, prop.Tokens, "%s: Tokens must be the true sum over Chosen", where)
	require.LessOrEqual(t, int(prop.Tokens), int(rg.Budget), "%s: the proposal overran its budget", where)
	require.Zero(t, p5ClosureViolations(cands, prop.Chosen), "%s: dependency closure", where)
	if prop.Overflow {
		require.Nil(t, prop.Chosen, "%s: an overflow is never a partial serialization", where)
		require.NotEmpty(t, prop.Reason, "%s: an overflow must name the record it lost", where)
	}

	baseline := completeRecordCarry(cands, rg.Budget)
	row := p5CorpusRow{
		Session: s.ID, At: int(at), Regime: rg.Name, Budget: int(rg.Budget),
		Candidates: len(cands), Overflow: prop.Overflow, Iters: prop.Iters,
		Selection: p5Summarize("selection", cands, prop.Chosen, lambda),
		Baseline:  p5Summarize("complete-record", cands, baseline, lambda),
		Demands:   len(demands),
	}
	for _, c := range cands {
		if p5Binds(c) {
			row.Binding++
		}
	}
	row.SelectionDemands = p5DemandsSatisfied(demands, prop.Chosen)
	row.BaselineDemands = p5DemandsSatisfied(demands, baseline)
	return row
}

// p5DemandsSatisfied counts the post-compaction demands whose block was delivered.
//
// It is the one number here that neither arm can see while it decides, which is exactly what makes
// it worth recording: everything else in a row is the arm grading its own homework. It is still
// not a quality verdict — eval.Demands is the harness's own model of need over a synthetic corpus,
// and the header says why no number of that kind may become a gate.
func p5DemandsSatisfied(demands []eval.Demand, chosen []analyzer.Representation) int {
	delivered := make(map[dag.NodeID]bool, len(chosen))
	for _, r := range chosen {
		delivered[r.Item] = true
	}
	n := 0
	for _, d := range demands {
		if delivered[p5NodeIDOf(d.BlockID)] {
			n++
		}
	}
	return n
}

// p5LoadCorpus reads the committed synthetic corpus.
func p5LoadCorpus(t *testing.T, cfg config.Config) []eval.Session {
	t.Helper()
	h := eval.New(eval.Options{Cfg: cfg, Log: logging.Nop()})
	sessions, err := h.Load(filepath.Join(repoRootOf(t), p5CorpusDir))
	require.NoError(t, err)
	require.NotEmpty(t, sessions)
	return sessions
}

// p5CorpusCandidates derives one compaction point's candidate set through role C's real candidate
// builder, so the corpus arm measures the production path rather than a candidate shape this file
// invented.
//
// p is the MEDIAN block position of the prefix and the set is filtered to blocks at or after it.
// The daemon assembles candidates against a p the scheduler chose; taking the median stands in for
// that, keeps the Pos >= p precondition non-trivial, and does not require this test to reimplement
// a compaction-point policy it is not measuring.
//
// Every elimination the prefix recorded is marked mandatory and QUALIFIED CURRENT. There is no
// negknow ledger in a replay, so nothing here can establish that an elimination is still
// applicable; marking them all current is therefore an ASSUMPTION, and it is the most demanding
// one available — it is what makes the mandatory reserve and the overflow path run at corpus
// scale. A real ledger would qualify some of them stale, which would only make the selector's job
// easier.
func p5CorpusCandidates(t *testing.T, s eval.Session, at core.TurnIndex) ([]analyzer.Candidate, error) {
	t.Helper()

	blocks := eval.Blocks(s, at)
	if len(blocks) == 0 {
		return nil, core.ErrDegraded
	}
	positions := make([]int, 0, len(blocks))
	for _, b := range blocks {
		positions = append(positions, b.Pos)
	}
	sort.Ints(positions)
	p := positions[len(positions)/2]

	nodes := make([]dag.Node, 0, len(blocks))
	weights := make(map[dag.NodeID]float64, len(blocks))
	mandatory := make(map[dag.NodeID]bool)
	quals := make(map[dag.NodeID]analyzer.Qualification)
	for _, b := range blocks {
		if b.Pos < p {
			continue
		}
		id := p5NodeIDOf(b.ID)
		if id == "" {
			continue
		}
		nodes = append(nodes, dag.Node{
			ID: id, Kind: p5NodeKind(b.Kind), Turn: b.Turn, Pos: b.Pos, Ref: b.ID,
			Root:   core.HashBytes(p5RootTurnPosDomain, fmt.Appendf(nil, "%d|%d", int(b.Turn), b.Pos)),
			Tokens: b.Tokens, Ephemeral: b.Ephemeral,
		})
		weights[id] = p5Weight(b, at)
		if b.Kind == eval.BlockElimination {
			mandatory[id] = true
			quals[id] = analyzer.QualCurrent
		}
	}
	if len(nodes) == 0 {
		return nil, core.ErrDegraded
	}

	cands, err := analyzer.NewCandidates(nodes, analyzer.CandidateOptions{
		Weights: weights, Mandatory: mandatory, Qualification: quals,
	})
	return cands, err
}

// p5Weight is the declared backward-looking prior: the block's kind weight, scaled by how late in
// the prefix it arrived. It is a prior and not a measurement — see p5KindWeight — and both arms
// rank by it.
func p5Weight(b eval.Block, at core.TurnIndex) float64 {
	recency := 1.0
	if at > 0 {
		recency = float64(b.Turn) / float64(at)
	}
	if recency > 1 {
		recency = 1
	}
	return p5KindWeight[b.Kind] * (p5RecencyFloor + (1-p5RecencyFloor)*recency)
}

// p5NodeKind maps an eval block kind onto the dag node kind whose NodeID prefix p5NodeIDOf mints.
func p5NodeKind(k eval.BlockKind) dag.NodeKind {
	switch k {
	case eval.BlockToolResult:
		return dag.KindToolResult
	case eval.BlockUserPrompt:
		return dag.KindUserPrompt
	case eval.BlockAssistant:
		return dag.KindAssistant
	case eval.BlockFile:
		return dag.KindFile
	case eval.BlockElimination:
		return dag.KindElimination
	case eval.BlockDecision:
		return dag.KindDecision
	default:
		return dag.KindInvalid
	}
}

// p5NodeIDOf re-keys an eval block id into a dag.NodeID.
//
// The two id spaces are genuinely different — eval writes "tu:", "turn:", "file:", "elim:", "dec:",
// and dag writes "toolresult:", "assistant:", "file:", "elimination:", "decision:" through
// constructors that sanitize the key — so this goes through dag's own constructors rather than
// splicing prefixes. A block whose id matches no known prefix returns the empty id and is skipped:
// inventing a node kind for an id nobody recognizes would put a mislabelled node into the set.
func p5NodeIDOf(blockID string) dag.NodeID {
	switch {
	case strings.HasPrefix(blockID, "tu:"):
		return dag.ToolResultNode(core.ToolUseID(strings.TrimPrefix(blockID, "tu:")))
	case strings.HasPrefix(blockID, "file:"):
		return dag.FileNode(strings.TrimPrefix(blockID, "file:"))
	case strings.HasPrefix(blockID, "elim:"):
		return dag.EliminationNode(strings.TrimPrefix(blockID, "elim:"))
	case strings.HasPrefix(blockID, "dec:"):
		return dag.DecisionNode(core.DecisionID(strings.TrimPrefix(blockID, "dec:")))
	case strings.HasPrefix(blockID, "turn:"):
		// A turn block is a user prompt or an assistant message and the id does not say which.
		// Both are minted from the same turn index and kept apart by the prefix, so the assistant
		// spelling is used consistently on both sides of the map: what matters is that the block
		// and the demand naming it resolve to ONE id, not which of the two names it is.
		var turn core.TurnIndex
		if _, err := fmt.Sscanf(strings.TrimPrefix(blockID, "turn:"), "%d", &turn); err != nil {
			return ""
		}
		return dag.AssistantNode(turn)
	default:
		return ""
	}
}

// p5CorpusSummary renders the corpus arm as one aggregate block plus the worst rows, so a reader
// gets the shape without twenty-four sessions of detail.
func p5CorpusSummary(rep p5CorpusReport) string {
	type agg struct {
		rows, carriedSel, carriedBase, tokSel, tokBase, dSel, dBase, demands, overflows int
		valSel, valBase                                                                 float64
	}
	byRegime := map[string]*agg{}
	var order []string
	for _, r := range rep.Rows {
		a, ok := byRegime[r.Regime]
		if !ok {
			a = &agg{}
			byRegime[r.Regime] = a
			order = append(order, r.Regime)
		}
		a.rows++
		a.carriedSel += r.Selection.Carried
		a.carriedBase += r.Baseline.Carried
		a.tokSel += r.Selection.Tokens
		a.tokBase += r.Baseline.Tokens
		a.valSel += r.Selection.Value
		a.valBase += r.Baseline.Value
		a.dSel += r.SelectionDemands
		a.dBase += r.BaselineDemands
		a.demands += r.Demands
		if r.Overflow {
			a.overflows++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "  %s\n", rep.Degraded)
	for _, name := range order {
		a := byRegime[name]
		fmt.Fprintf(&b, "  %-18s rows=%3d  selection[carried=%5d tokens=%9d value=%9.2f demands=%4d] "+
			"complete-record[carried=%5d tokens=%9d value=%9.2f demands=%4d]  of %d demands, overflows=%d\n",
			name, a.rows, a.carriedSel, a.tokSel, a.valSel, a.dSel,
			a.carriedBase, a.tokBase, a.valBase, a.dBase, a.demands, a.overflows)
	}
	return b.String()
}

// ── the two guards ──────────────────────────────────────────────────────────────────────────

// TestPhase5_ShipOrderGateStillRefusesProposal asserts closing note 3 on the new surface: while
// p-selection is unavailable, Propose refuses rather than quietly selecting nothing.
//
// It matters because Propose is a free function and could easily have been written to run its own
// copy of the constructor's guards. It is not: it constructs a Selector over the candidates' Block
// projection and fails with whatever that construction failed with, so the guard cannot drift out
// of step with the one Select runs.
func TestPhase5_ShipOrderGateStillRefusesProposal(t *testing.T) {
	scheduler.DisablePSelection()
	t.Cleanup(scheduler.DisablePSelection)
	require.False(t, scheduler.PSelectionAvailable())

	_, err := analyzer.Propose(context.Background(), 0, []analyzer.Candidate{{
		Item: "file:a", Pos: 0, Weight: 1,
		Reps: []analyzer.Representation{{Item: "file:a", Kind: analyzer.RepExactSpan, Coverage: 1}},
	}}, 0, 1000)
	require.ErrorIs(t, err, core.ErrNotImplemented,
		"submodular selection must not ship before p-selection")
}

// TestPhase5_DefaultsStayOff pins the shipped disposition these tests deliberately depart from:
// both SP-15 switches are false out of the box, and nothing in this file may change that.
//
// It is not a duplicate of TestGuard_SubmodularDefaultsOff. That guard pins the config default;
// this pins the relationship between the two INDEPENDENT switches contract §7 requires — either
// may be off without affecting the other, and both off is exactly today's behaviour.
func TestPhase5_DefaultsStayOff(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	require.False(t, cfg.Runtime.Selection.SubmodularEnabled)
	require.False(t, cfg.Runtime.Selection.LoopWarningsEnabled)
	require.False(t, cfg.Selection.Submodular.Enabled,
		"Selection.Submodular.Enabled is derived from the runtime key and must default with it")
}

// TestPhase5_PhaseCheckIsSelfContained runs the registered phase-5 check against a Context that
// carries no report at all.
//
// That is the property the registration depends on. runPhaseChecks runs every landed phase on
// every pull request, so a phase 5 that needed a particular policy in --policies would fail on
// every invocation that did not include one — and the way that failure gets resolved in practice
// is by turning the check off.
func TestPhase5_PhaseCheckIsSelfContained(t *testing.T) {
	require.NoError(t, phase5(Context{}))
	require.False(t, scheduler.PSelectionAvailable(),
		"the check must close the ship-order gate it opened")
}

// ── helpers ─────────────────────────────────────────────────────────────────────────────────

// p5ProposeAt proposes over one trial at the named regime.
func p5ProposeAt(t *testing.T, tr p5Trial, regime string) analyzer.Proposal {
	t.Helper()
	for _, rg := range tr.Regimes {
		if rg.Name != regime {
			continue
		}
		cands, err := tr.candidates()
		require.NoError(t, err)
		prop, err := analyzer.Propose(context.Background(), tr.P, cands, tr.Lambda, core.Tokens(rg.Budget))
		require.NoError(t, err)
		return prop
	}
	require.FailNowf(t, "no such regime", "trial %s declares no %q regime", tr.Name, regime)
	return analyzer.Proposal{}
}

// p5Find returns the representation a proposal carried item at.
func p5Find(prop analyzer.Proposal, item dag.NodeID) (analyzer.Representation, bool) {
	for _, r := range prop.Chosen {
		if r.Item == item {
			return r, true
		}
	}
	return analyzer.Representation{}, false
}

// writeSP15Artifact writes one JSON artifact under a temporary project's .qompack/eval/ — never
// under the repository — and returns its path. Both SP-15 phase files use it.
//
// It is phase4_test.go's writePhase4Artifact under a different name rather than a call into it,
// because that function is phase 4's and a shared helper is a shared constraint: SP-15's artifacts
// should be free to change shape without a phase-4 row having to agree. The one thing it must
// keep is where it writes — under a temporary directory, never under the repository, because a
// test that leaves measurement output in the working tree turns every run into a dirty checkout.
func writeSP15Artifact(t *testing.T, name string, v any) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".qompack", "eval")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	body, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, append(body, '\n'), 0o600))
	t.Logf("wrote %s (%d bytes)", path, len(body))
	return path
}
