// V5 §4.9 — complete-record overflow replaces arbitrary byte truncation.
//
// Three producers own the criterion on this tree, and every one of them is driven for real:
//
//   - SP-11's checkpoint.Truncate (the §6.9 cut): whole tool pointers, file pointers, open
//     questions, decisions, the alternatives field, next_step and the narrative are removed one
//     record at a time in the fixed within-tier order, each with exactly one DropEntry; tier 1 is
//     never cut, and a budget too small for tier 1 yields ONE budget_exceeded entry over a
//     byte-identical tier 1 rather than a shorter document.
//   - SP-16's checkpoint.Promote/Apply: demand-promoted pointers move to the head of
//     Pointers.Tools, and because Truncate cuts tail-first that is exactly "this survives the next
//     budget" — a promoted pointer is the LAST tier-3 pointer to go, and it still goes before any
//     tier-2 record does.
//   - SP-15's analyzer.Propose feeding SP-11's rehydrate.Build: a mandatory elimination that cannot
//     be carried at ANY representation is an explicit overflow — no partial selection, no
//     half-rendered record — and the rehydrator files it as one "overflow" drop naming the record.
//
// The historical text of this row (an "action history" block under "tier reserves") names
// producers that do not exist on this tree; plans/sdd/V5-VERIFY/x09-disposition.md records what was
// kept, corrected and retired. Two negative controls close the row: promotion switched off through
// its real config state (runtime.phase7.retrieval.demandPromotion=false is report-only, and the
// formerly promoted pointer is then the FIRST one cut), and selection switched off through its real
// rollback path (a nil Selection, which is what a disabled submodularEnabled hands Build), under
// which no overflow entry exists and the same records render whole.
package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
)

// x9v5Session is this row's session identity.
const x9v5Session = core.SessionID("sess-integration-v5-x09")

// The fixture's record counts. Tool pointers are what promotion reorders and what the cut walks
// tail-first, so there are enough of them for "promoted survives its unpromoted peers" to have
// room to show; the tier-2 counts give every within-tier stage at least one record to remove.
const (
	x9v5ToolPointers  = 6
	x9v5FilePointers  = 4
	x9v5Decisions     = 4
	x9v5OpenQuestions = 3
	x9v5Eliminations  = 3
)

// x9v5Turns is the turn the newest fixture record sits at; older records count down from it. The
// writer keeps tier-2/3 slices in DESCENDING turn order (§8), and Truncate's tail-first cut relies
// on that, so the fixture honours it.
const x9v5Turns = core.TurnIndex(60)

// x9v5PromotedExpansions is the re-expansion count offered for the promoted pointer. It clears
// Promote's default floor (one re-expansion is not a pattern), and every request is matched by a
// useful outcome below so the demand record is INSTRUMENTED — frequency alone cannot promote.
const x9v5PromotedExpansions = 3

// x9v5RecoveryCostMs is the measured re-derivation cost recorded on the promoted key. It is only a
// tie-break input to Promote's ordering; any positive value serves.
const x9v5RecoveryCostMs = 250

// x9v5DeterminismEvery is how often along the budget ladder Truncate is run a second time and
// compared byte for byte: a sample, because the ladder already walks every budget once.
const x9v5DeterminismEvery = core.Tokens(97)

// x9v5FilePerm is the mode the append-only probe opens with; the door refuses before the mode
// matters, so any ordinary file mode serves.
const x9v5FilePerm = 0o644

// x9v5SelectorBudgetShortfall is how far below the cheapest representation of a mandatory record
// the selector's budget is put to force an overflow: one token, the smallest shortfall there is.
const x9v5SelectorBudgetShortfall = core.Tokens(1)

// x9v5Rig is the real collaborator set: one disposable project with the production store, ledger,
// pin store, DAG, checkpoint writer, demand log and token estimator.
type x9v5Rig struct {
	P      *testutil.Project
	Store  store.Store
	Ledger negknow.Ledger
	Graph  dag.Graph
	Writer *checkpoint.FileWriter
	Src    checkpoint.SourceSet
	Est    tokens.Estimator
	Demand *store.DemandLog
}

// x9v5Open composes the rig exactly the way x9v4Open does, plus the SP-16 demand log.
func x9v5Open(t *testing.T) *x9v5Rig {
	t.Helper()

	p := testutil.NewProject(t)
	s := openRealStore(t, p)

	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)
	led, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Store: s, Graph: g, Session: x9v5Session, Log: p.Log, Clock: p.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })

	pinStore, err := pins.OpenWith(p.Root, p.Log, nil, p.Clock)
	require.NoError(t, err)

	w, err := checkpoint.OpenWriter(p.Root, p.Cfg, p.Log, nil, p.Clock)
	require.NoError(t, err)

	est := tokens.NewForProject(p.Cfg, tokens.DefaultCalibPath(), p.Root)
	src := checkpoint.SourceSet{
		Store: s, Segments: s.Segments(), Ledger: led, Pins: pinStore, Graph: g,
		Grammar: grammar.New(),
		Tokens:  est,
	}
	require.NoError(t, src.Validate(), "the SourceSet must be complete before Begin is called")
	require.NoError(t, w.SetSources(src))

	return &x9v5Rig{
		P: p, Store: s, Ledger: led, Graph: g, Writer: w, Src: src, Est: est,
		Demand: store.OpenDemandLog(p.Root),
	}
}

// x9v5Eliminate records one real, active elimination through the production ledger and returns
// the record as the ledger normalized it — id, descriptor and status included.
func x9v5Eliminate(t *testing.T, r *x9v5Rig, i int) negknow.Record {
	t.Helper()
	ctx := context.Background()
	id, err := r.Ledger.Record(ctx, negknow.Record{
		Session:  x9v5Session,
		Target:   fmt.Sprintf("src/pool%d.ts:acquire", i),
		Approach: fmt.Sprintf("v5x09 approach %d: widen the acquire timeout", i),
		Reason:   fmt.Sprintf("v5x09 reason %d: the cliff moves without the lock-hold pattern going", i),
		Evidence: core.HashBytes("v5x09.evidence", []byte(fmt.Sprintf("elim-%d", i))),
		Scope:    negknow.ScopeSession,
		Source:   negknow.SourceSlashCommand,
	})
	require.NoError(t, err)
	rec, err := r.Ledger.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, negknow.StatusActive, rec.Status, "a freshly recorded elimination is active")
	return rec
}

// x9v5Checkpoint builds the row's checkpoint over REAL store content: every tool pointer's hash is
// the root of a real PutBytes and a real RecordToolUse, and the eliminations are the ledger's own
// records. Slices are newest-first, as the writer keeps them.
func x9v5Checkpoint(t *testing.T, r *x9v5Rig) checkpoint.Checkpoint {
	t.Helper()
	ctx := context.Background()

	c := checkpoint.Checkpoint{
		Version:         checkpoint.SchemaVersion,
		Session:         x9v5Session,
		Seq:             1,
		Created:         "2026-09-08T00:00:00.000Z",
		EncodedSegments: []core.SegmentID{},
		Invariants: []checkpoint.Invariant{
			{ID: "inv_v5x09_a", Text: "v5x09 invariant A: the load-test gate stays required.", Source: "user", Pinned: 1},
			{ID: "inv_v5x09_b", Text: "v5x09 invariant B: refresh tokens remain single-use.", Source: "agent", Pinned: 2},
		},
		UserIntent: checkpoint.UserIntent{
			Original:  "v5x09 original intent: fix the intermittent 500s on refresh.",
			Evolution: []string{"v5x09 delta 1: narrowed to pool acquisition.", "v5x09 delta 2: k6 is the gate."},
		},
		CurrentWork: checkpoint.CurrentWork{
			Goal:     "v5x09 goal: land the pool fix behind a flag",
			NextStep: "v5x09 next step: run the k6 gate on the release branch",
		},
		SketchRefs: map[string]string{},
		Dropped:    []checkpoint.DropEntry{},
		Cache:      checkpoint.CacheInfo{TTLState: "warm"},
	}
	for i := 0; i < x9v5Eliminations; i++ {
		c.Eliminated = append(c.Eliminated, x9v5Eliminate(t, r, i))
	}

	for i := 0; i < x9v5ToolPointers; i++ {
		turn := x9v5Turns - core.TurnIndex(i)
		body := []byte(fmt.Sprintf("v5x09 tool result %d\nline two of result %d\n", i, i))
		res, err := r.Store.PutBytes(ctx, body, store.PutOptions{Tool: "Bash"})
		require.NoError(t, err)
		id := core.ToolUseID(fmt.Sprintf("toolu_v5x09_%03d", int(turn)))
		require.NoError(t, r.Store.RecordToolUse(ctx, store.ToolUseRecord{
			ID: id, Session: x9v5Session, Tool: "Bash", Root: res.Root.Hash,
			Bytes: int64(len(body)), Status: store.StatusOK, Turn: turn,
		}))
		c.Pointers.Tools = append(c.Pointers.Tools, checkpoint.ToolPointer{
			ToolUseID: id, Hash: res.Root.Hash,
			Summary: fmt.Sprintf("v5x09 tool summary for turn %d", int(turn)),
		})
	}
	for i := 0; i < x9v5FilePointers; i++ {
		c.Pointers.Files = append(c.Pointers.Files, checkpoint.FilePointer{
			Path: fmt.Sprintf("src/v5x09/f%d.ts", i),
			Hash: core.HashBytes("v5x09.file", []byte(fmt.Sprintf("f%d", i))),
			Why:  fmt.Sprintf("v5x09 file reason %d", i),
		})
	}
	for i := 0; i < x9v5Decisions; i++ {
		c.Decisions = append(c.Decisions, checkpoint.Decision{
			ID:   core.DecisionID(fmt.Sprintf("dec_v5x09_%d", i)),
			What: fmt.Sprintf("v5x09 decision %d: keep transaction pooling", i),
			Why:  fmt.Sprintf("v5x09 decision %d reason: session pooling doubles the connection count", i),
			AlternativesRejected: []string{
				fmt.Sprintf("v5x09 alternative %d.1", i), fmt.Sprintf("v5x09 alternative %d.2", i),
			},
			Evidence: core.HashBytes("v5x09.decision", []byte(fmt.Sprintf("d%d", i))),
			Turn:     x9v5Turns - core.TurnIndex(i),
		})
	}
	for i := 0; i < x9v5OpenQuestions; i++ {
		c.OpenQuestions = append(c.OpenQuestions, fmt.Sprintf("v5x09 open question %d?", i))
	}
	c.Narrative = "v5x09 narrative: prose residue that the cut removes first and whole."
	return c
}

// x9v5Size is sizeOf as §6.9 defines it: canonical Marshal bytes priced as ClassJSON.
func x9v5Size(t *testing.T, c checkpoint.Checkpoint, est tokens.Estimator) core.Tokens {
	t.Helper()
	b, err := checkpoint.Marshal(c)
	require.NoError(t, err)
	return est.Estimate(b, tokens.ClassJSON)
}

// at1OrTier1 is the tier-1-only view of c: what Truncate hands back once both cuttable tiers are
// exhausted, used to report where on the ladder budget_exceeded begins.
func at1OrTier1(c checkpoint.Checkpoint) checkpoint.Checkpoint {
	c.Decisions, c.OpenQuestions, c.Narrative = nil, nil, ""
	c.CurrentWork.NextStep = ""
	c.Pointers = checkpoint.Pointers{}
	return c
}

// x9v5Tier1JSON marshals only the three never-truncated fields, so "tier 1 is byte-identical" is
// compared on bytes and not on Go equality.
func x9v5Tier1JSON(t *testing.T, c checkpoint.Checkpoint) string {
	t.Helper()
	b, err := json.Marshal(struct {
		Invariants []checkpoint.Invariant `json:"invariants"`
		UserIntent checkpoint.UserIntent  `json:"user_intent"`
		Eliminated []negknow.Record       `json:"eliminated"`
	}{c.Invariants, c.UserIntent, c.Eliminated})
	require.NoError(t, err)
	return string(b)
}

// x9v5Promote runs the real SP-16 gate over the OLDEST tool pointer with a real, instrumented
// demand record read back from the production demand log, and returns the plan.
func x9v5Promote(t *testing.T, r *x9v5Rig, c checkpoint.Checkpoint, enabled bool) checkpoint.PromotionPlan {
	t.Helper()
	oldest := c.Pointers.Tools[len(c.Pointers.Tools)-1]
	key := oldest.Hash.String()

	// Every request matched by a useful outcome: Instrumented() and a KNOWN usefulness rate,
	// which is what separates evidence from frequency in Promote's gate 2.
	for i := 0; i < x9v5PromotedExpansions; i++ {
		ts := core.UnixMilli(r.P.Clock.Now().UnixMilli())
		require.NoError(t, r.Demand.Record(store.DemandObservation{
			Kind: store.DemandRequested, Key: key, TS: ts, Session: x9v5Session,
		}))
		require.NoError(t, r.Demand.Record(store.DemandObservation{
			Kind: store.DemandUseful, Key: key, TS: ts, Session: x9v5Session,
			RecoveryCostMs: x9v5RecoveryCostMs,
		}))
	}
	agg, err := r.Demand.Aggregate()
	require.NoError(t, err)
	demand, ok := agg[key]
	require.True(t, ok, "the demand log must aggregate the key it was fed")
	require.True(t, demand.Instrumented(), "fixture sanity: the record must be instrumented")
	_, known := demand.Usefulness()
	require.True(t, known, "fixture sanity: the usefulness rate must be known")

	return checkpoint.Promote(checkpoint.PromotionRequest{
		Enabled:     enabled,
		ObservedSeq: c.Seq,
		TargetSeq:   c.Seq + 1,
		Candidates: []checkpoint.PromotionCandidate{{
			Pointer: oldest, Expansions: x9v5PromotedExpansions, Demand: demand,
			Authority: core.AuthorityToolObservation,
		}},
		// The whole document's size is an overhead no single pointer can exceed, so the budget
		// gate cannot be what decides this candidate.
		Overhead: x9v5Size(t, c, r.Est),
		Est:      r.Est,
	})
}

// x9v5Kept is the identity set of one truncation outcome: which whole records survived.
type x9v5Kept struct {
	tools, files, decisions, questions []string
	alternatives                       int
	narrative, nextStep                bool
}

func x9v5KeptOf(c checkpoint.Checkpoint) x9v5Kept {
	k := x9v5Kept{narrative: c.Narrative != "", nextStep: c.CurrentWork.NextStep != ""}
	for _, p := range c.Pointers.Tools {
		k.tools = append(k.tools, string(p.ToolUseID))
	}
	for _, p := range c.Pointers.Files {
		k.files = append(k.files, p.Path)
	}
	for _, d := range c.Decisions {
		k.decisions = append(k.decisions, string(d.ID))
		k.alternatives += len(d.AlternativesRejected)
	}
	k.questions = append(k.questions, c.OpenQuestions...)
	return k
}

// x9v5RequirePrefix asserts that got carries a PREFIX of in's every tier-2/3 slice, element for
// element byte-identical (alternatives excepted, which are emptied wholesale). This is what
// "complete-record" means against "arbitrary byte truncation": no record is ever shortened, only
// removed. It is also the monotonicity check, applied to two outcomes one token apart.
func x9v5RequirePrefix(t *testing.T, in, got checkpoint.Checkpoint, budget core.Tokens) {
	t.Helper()
	msg := fmt.Sprintf("budget %d", int(budget))

	require.Equal(t, in.Invariants, got.Invariants, "tier 1 is never cut: %s", msg)
	require.Equal(t, in.UserIntent, got.UserIntent, "tier 1 is never cut: %s", msg)
	require.Equal(t, in.Eliminated, got.Eliminated, "tier 1 is never cut: %s", msg)
	require.Equal(t, in.CurrentWork.Goal, got.CurrentWork.Goal, "goal survives next_step: %s", msg)

	require.LessOrEqual(t, len(got.Pointers.Tools), len(in.Pointers.Tools), msg)
	require.Equal(t, in.Pointers.Tools[:len(got.Pointers.Tools)], got.Pointers.Tools,
		"surviving tool pointers are a byte-identical prefix: %s", msg)
	require.LessOrEqual(t, len(got.Pointers.Files), len(in.Pointers.Files), msg)
	require.Equal(t, in.Pointers.Files[:len(got.Pointers.Files)], got.Pointers.Files,
		"surviving file pointers are a byte-identical prefix: %s", msg)
	require.LessOrEqual(t, len(got.OpenQuestions), len(in.OpenQuestions), msg)
	require.Equal(t, in.OpenQuestions[:len(got.OpenQuestions)], got.OpenQuestions,
		"surviving open questions are a byte-identical prefix: %s", msg)
	require.LessOrEqual(t, len(got.Decisions), len(in.Decisions), msg)
	for i, d := range got.Decisions {
		want := in.Decisions[i]
		require.Equal(t, want.ID, d.ID, msg)
		require.Equal(t, want.What, d.What, "a decision's text is never shortened: %s", msg)
		require.Equal(t, want.Why, d.Why, "a decision's reason is never shortened: %s", msg)
		require.Equal(t, want.Evidence, d.Evidence, msg)
		require.Equal(t, want.Turn, d.Turn, msg)
		if len(d.AlternativesRejected) > 0 {
			require.Equal(t, want.AlternativesRejected, d.AlternativesRejected,
				"alternatives are emptied wholesale or kept whole, never partially cut: %s", msg)
		}
	}
	if got.Narrative != "" {
		require.Equal(t, in.Narrative, got.Narrative, "the narrative is dropped whole: %s", msg)
	}
	if got.CurrentWork.NextStep != "" {
		require.Equal(t, in.CurrentWork.NextStep, got.CurrentWork.NextStep, msg)
	}
}

// x9v5RequireDropsAccount asserts that drops name every record removed between in and got exactly
// once, by the id the retrieval tools resolve, and name nothing that survived.
func x9v5RequireDropsAccount(t *testing.T, in, got checkpoint.Checkpoint, drops []checkpoint.DropEntry, budget core.Tokens) {
	t.Helper()
	msg := fmt.Sprintf("budget %d", int(budget))
	type key struct{ kind, id string }
	seen := make(map[key]int, len(drops))
	for _, d := range drops {
		seen[key{d.Kind, d.ID}]++
	}
	want := make(map[key]int)
	for _, p := range in.Pointers.Tools[len(got.Pointers.Tools):] {
		want[key{"tool_pointer", string(p.ToolUseID)}]++
	}
	for _, p := range in.Pointers.Files[len(got.Pointers.Files):] {
		want[key{"file_pointer", p.Path}]++
	}
	for i := len(got.OpenQuestions); i < len(in.OpenQuestions); i++ {
		want[key{"open_question", fmt.Sprintf("oq_%d", i)}]++
	}
	for _, d := range in.Decisions[len(got.Decisions):] {
		want[key{"decision", string(d.ID)}]++
	}
	emptied := 0
	for i, d := range got.Decisions {
		if len(in.Decisions[i].AlternativesRejected) > 0 && len(d.AlternativesRejected) == 0 {
			emptied++
		}
	}
	if emptied > 0 || len(got.Decisions) < len(in.Decisions) {
		// emptyAlternatives runs over EVERY decision in one step before any decision is cut, so
		// once a decision is gone, every input decision with alternatives has an alternatives entry.
		for _, d := range in.Decisions {
			if len(d.AlternativesRejected) > 0 {
				want[key{"alternatives", string(d.ID)}]++
			}
		}
	}
	if in.Narrative != "" && got.Narrative == "" {
		want[key{"narrative", "narrative"}]++
	}
	if in.CurrentWork.NextStep != "" && got.CurrentWork.NextStep == "" {
		want[key{"next_step", "current_work.next_step"}]++
	}
	for _, d := range drops {
		if d.Kind == "budget_exceeded" {
			want[key{"budget_exceeded", "tier1"}]++ // claimed at most once; the caller checks when
		}
	}
	require.Equal(t, want, seen, "the drop report must account for exactly the removed records: %s", msg)
}

// x9v5RequireCutOrder asserts the §10 ordering on one outcome: nothing in tier 2 goes while any of
// tier 3 stands, and within each tier the fixed stage order holds.
func x9v5RequireCutOrder(t *testing.T, in, got checkpoint.Checkpoint, budget core.Tokens) {
	t.Helper()
	msg := fmt.Sprintf("budget %d", int(budget))
	k := x9v5KeptOf(got)
	tier3Gone := !k.narrative && len(k.tools) == 0 && len(k.files) == 0
	tier2Cut := len(k.questions) < len(in.OpenQuestions) || len(k.decisions) < len(in.Decisions) ||
		k.alternatives == 0 || !k.nextStep

	if len(k.tools) < len(in.Pointers.Tools) || len(k.files) < len(in.Pointers.Files) {
		require.False(t, k.narrative, "the narrative goes before any pointer: %s", msg)
	}
	if len(k.files) < len(in.Pointers.Files) {
		require.Empty(t, k.tools, "tool pointers are exhausted before a file pointer goes: %s", msg)
	}
	if tier2Cut {
		require.True(t, tier3Gone, "no tier-2 record goes while tier 3 stands: %s", msg)
	}
	if k.alternatives == 0 {
		require.Empty(t, k.questions, "open questions are exhausted before alternatives are emptied: %s", msg)
	}
	if len(k.decisions) < len(in.Decisions) {
		require.Zero(t, k.alternatives, "alternatives are emptied before a decision goes: %s", msg)
	}
	if !k.nextStep {
		require.Empty(t, k.decisions, "next_step is the last cut, after every decision: %s", msg)
	}
}

// x9v5Candidates mirrors the daemon composition root (internal/daemon/rehydrate_selection.go
// eliminationCandidates): one item per active record with two representations, the rendered line
// and a pointer naming the id, Mandatory because the ledger reports the record active.
func x9v5Candidates(recs []negknow.Record, est tokens.Estimator) []analyzer.Candidate {
	cands := make([]analyzer.Candidate, 0, len(recs))
	for _, rec := range recs {
		prov := analyzer.Provenance{
			Origin: dag.NodeID(rec.ID), Root: rec.Evidence, Qualification: analyzer.QualCurrent,
		}
		cands = append(cands, analyzer.Candidate{
			Item: dag.NodeID(rec.ID), Weight: 1, Mandatory: true,
			Reps: []analyzer.Representation{
				{
					Item: dag.NodeID(rec.ID), Kind: analyzer.RepExactSpan, Coverage: 1,
					AssembledCost: est.EstimateString(rec.Target+" "+rec.Approach+" "+rec.Reason, tokens.ClassProse),
					Prov:          prov,
				},
				{
					Item: dag.NodeID(rec.ID), Kind: analyzer.RepPointer, Coverage: 0.25,
					AssembledCost: est.EstimateString(rec.ID, tokens.ClassProse),
					Prov:          prov,
				},
			},
		})
	}
	return cands
}

// x9v5Outcome is the daemon's total translation of a Proposal onto rehydrate's neutral type.
func x9v5Outcome(prop analyzer.Proposal) *rehydrate.SelectionOutcome {
	out := &rehydrate.SelectionOutcome{
		Keep: make([]dag.NodeID, 0, len(prop.Chosen)), Archive: prop.Archive, Tokens: prop.Tokens,
		Overflow: prop.Overflow, Item: string(prop.Item), Reason: prop.Reason,
	}
	for _, rep := range prop.Chosen {
		out.Keep = append(out.Keep, rep.Item)
	}
	return out
}

// x9v5Build runs the real rehydrator over the real store, ledger, graph and estimator.
func x9v5Build(t *testing.T, r *x9v5Rig, c checkpoint.Checkpoint, sel *rehydrate.SelectionOutcome) rehydrate.Result {
	t.Helper()
	res, err := rehydrate.Build(context.Background(), rehydrate.Request{
		Session: x9v5Session, Source: "compact", ProjectRoot: r.P.Root,
		Budget:     core.Tokens(r.P.Cfg.Runtime.Rehydrate.MaxTokens),
		Checkpoint: c, Ref: checkpoint.Ref{Seq: c.Seq}, Cfg: r.P.Cfg, Selection: sel,
	}, rehydrate.Deps{
		Store: r.Store, Ledger: r.Ledger, Graph: r.Graph, Tokens: r.Est, Log: r.P.Log,
	})
	require.NoError(t, err)
	return res
}

// x9v5DropsOf projects a drop list down to kind→ids.
func x9v5DropsOf(drops []checkpoint.DropEntry) map[string][]string {
	out := make(map[string][]string)
	for _, d := range drops {
		out[d.Kind] = append(out[d.Kind], d.ID)
	}
	return out
}

// TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly is V5-VERIFY §4.9.
func TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly(t *testing.T) {
	r := x9v5Open(t)
	ctx := context.Background()
	tiers := r.P.Cfg.Checkpoint.Tiers
	in := x9v5Checkpoint(t, r)
	oldest := in.Pointers.Tools[len(in.Pointers.Tools)-1]

	// ── SP-16: real demand promotes the oldest pointer to the head ───────────────────────────────
	plan := x9v5Promote(t, r, in, true)
	require.Equal(t, 1, plan.Admitted(), "an instrumented, useful, re-expanded pointer is admitted: %+v", plan.Results)
	require.Equal(t, []core.Hash{oldest.Hash}, plan.Order)
	require.Empty(t, plan.Dropped, "nothing overflowed the promotion overhead")

	promoted, applyDrops := plan.Apply(in)
	require.Empty(t, applyDrops)
	require.Equal(t, oldest, promoted.Pointers.Tools[0], "the promoted pointer leads")
	require.Equal(t, in.Pointers.Tools[:len(in.Pointers.Tools)-1], promoted.Pointers.Tools[1:],
		"everything the plan did not name keeps the writer's order")
	require.Equal(t, oldest, in.Pointers.Tools[len(in.Pointers.Tools)-1], "Apply does not mutate its input")

	full := x9v5Size(t, promoted, r.Est)
	require.Positive(t, int(full))

	// ── SP-11: the cut, at EVERY budget from the full size down to 1 ─────────────────────────────
	//
	// Step 1 over the whole range is what makes the monotonicity and ordering claims claims about
	// the producer and not about a handful of chosen points.
	var (
		prev          checkpoint.Checkpoint
		havePrev      bool
		promotedAlone bool // a budget kept the promoted pointer while cutting an unpromoted one
		promotedGone  core.Tokens
	)
	kept := make(map[core.Tokens]x9v5Kept, int(full)+1)
	for budget := full; budget >= 1; budget-- {
		got, drops := checkpoint.Truncate(promoted, budget, tiers, r.Est)
		gb, err := checkpoint.Marshal(got)
		require.NoError(t, err)
		if budget%x9v5DeterminismEvery == 0 || budget == 1 {
			again, _ := checkpoint.Truncate(promoted, budget, tiers, r.Est)
			ab, err := checkpoint.Marshal(again)
			require.NoError(t, err)
			require.Equal(t, string(gb), string(ab), "Truncate is deterministic at budget %d", int(budget))
		}

		x9v5RequirePrefix(t, promoted, got, budget)
		x9v5RequireDropsAccount(t, promoted, got, drops, budget)
		x9v5RequireCutOrder(t, promoted, got, budget)

		size := r.Est.Estimate(gb, tokens.ClassJSON)
		if dk := x9v5DropsOf(drops); len(dk["budget_exceeded"]) > 0 {
			require.Equal(t, []string{"tier1"}, dk["budget_exceeded"], "budget %d", int(budget))
			require.Empty(t, got.Pointers.Tools, "budget %d", int(budget))
			require.Empty(t, got.Pointers.Files, "budget %d", int(budget))
			require.Empty(t, got.Decisions, "budget %d", int(budget))
			require.Empty(t, got.OpenQuestions, "budget %d", int(budget))
			require.Empty(t, got.Narrative, "budget %d", int(budget))
			require.Empty(t, got.CurrentWork.NextStep, "budget %d", int(budget))
			require.Greater(t, int(size), int(budget),
				"budget_exceeded is claimed only when tier 1 alone is over budget: %d", int(budget))
		} else {
			require.LessOrEqual(t, int(size), int(budget), "the cut lands inside the budget: %d", int(budget))
		}

		if havePrev {
			// Monotone: kept(b) ⊆ kept(b+1), as byte-identical prefixes of the larger outcome.
			x9v5RequirePrefix(t, prev, got, budget)
		}
		prev, havePrev = got, true

		k := x9v5KeptOf(got)
		kept[budget] = k
		hasPromoted := len(k.tools) > 0 && k.tools[0] == string(oldest.ToolUseID)
		if hasPromoted && len(k.tools) < len(promoted.Pointers.Tools) {
			promotedAlone = true
		}
		if !hasPromoted {
			require.Empty(t, k.tools,
				"the promoted pointer is the LAST tool pointer to go: budget %d kept %v", int(budget), k.tools)
			if promotedGone == 0 {
				promotedGone = budget
			}
		}
	}
	require.True(t, promotedAlone,
		"some budget must keep the promoted pointer while cutting an unpromoted one, or the "+
			"promotion had no observable effect on survival")
	require.Positive(t, int(promotedGone), "some budget must remove even the promoted pointer")
	t.Logf("v5 §4.9 cut ladder: full=%d tokens, promoted pointer %s gone at budget %d, tier-1-only from budget %d",
		int(full), oldest.ToolUseID, int(promotedGone), int(x9v5Size(t, at1OrTier1(promoted), r.Est)))
	require.Empty(t, kept[promotedGone].tools)
	require.Equal(t, len(promoted.Decisions), len(kept[promotedGone].decisions),
		"a promoted pointer still goes before any decision does: promotion never lifts tier 3 into tier 2")
	require.Equal(t, len(promoted.OpenQuestions), len(kept[promotedGone].questions))

	// Budget 1: tier 1 byte-identical, one budget_exceeded entry, no error to return.
	at1, drops1 := checkpoint.Truncate(promoted, 1, tiers, r.Est)
	require.Equal(t, x9v5Tier1JSON(t, in), x9v5Tier1JSON(t, at1), "tier 1 is byte-identical to the input at budget 1")
	require.Equal(t, []string{"tier1"}, x9v5DropsOf(drops1)["budget_exceeded"])
	require.Equal(t, x9v5Tier1JSON(t, in), x9v5Tier1JSON(t, promoted), "promotion touched nothing in tier 1")

	// ── SP-11's writer at budget 1: the artifact still lands, whole tier 1 and explicit overflow ─
	d, err := r.Writer.Begin(ctx, x9v5Session, 0, r.Src)
	require.NoError(t, err)
	ref, err := r.Writer.Finalize(ctx, d, 1)
	require.NoError(t, err, "Truncate never fails, so Finalize at budget 1 still writes (G7.4)")
	reader, err := checkpoint.OpenReader(r.P.Root, r.P.Log, nil)
	require.NoError(t, err)
	sealed, _, err := reader.Get(ctx, ref.Seq)
	require.NoError(t, err)
	require.Equal(t, []string{"tier1"}, x9v5DropsOf(sealed.Dropped)["budget_exceeded"],
		"the sealed artifact carries the explicit overflow: %+v", sealed.Dropped)
	require.Len(t, sealed.Eliminated, x9v5Eliminations, "the ledger's active records are written whole at budget 1")
	wantReasons := make([]string, 0, x9v5Eliminations)
	gotReasons := make([]string, 0, x9v5Eliminations)
	for i := range in.Eliminated {
		wantReasons = append(wantReasons, in.Eliminated[i].Reason)
		gotReasons = append(gotReasons, sealed.Eliminated[i].Reason)
	}
	require.ElementsMatch(t, wantReasons, gotReasons, "no elimination is shortened")
	bad, err := reader.Verify(ctx)
	require.NoError(t, err)
	require.Empty(t, bad, "the budget-1 artifact verifies against the manifest")

	// ── SP-15 → SP-11: selection overflow is an explicit record, never a partial one ─────────────
	scheduler.EnablePSelection() // the SP-12 ship-order gate, opened the way analyzer's own tests open it
	t.Cleanup(scheduler.DisablePSelection)
	cands := x9v5Candidates(in.Eliminated, r.Est)
	lambda := r.P.Cfg.Selection.Submodular.Lambda
	rehydrateBudget := core.Tokens(r.P.Cfg.Runtime.Rehydrate.MaxTokens)

	ample, err := analyzer.Propose(ctx, 0, cands, lambda, rehydrateBudget)
	require.NoError(t, err)
	require.False(t, ample.Overflow)
	require.Len(t, ample.Chosen, x9v5Eliminations, "every mandatory record is carried at the full budget")
	res := x9v5Build(t, r, in, x9v5Outcome(ample))
	require.LessOrEqual(t, int(res.Tokens), int(rehydrateBudget))
	require.False(t, rehydrate.Overflowed(res.Dropped), "nothing overflowed: %+v", res.Dropped)
	require.Empty(t, x9v5DropsOf(res.Dropped)["overflow"])
	for _, rec := range in.Eliminated {
		require.Contains(t, res.Text, "- "+rec.Target+" — \""+rec.Approach+"\" — "+rec.Reason+" [active]",
			"an admitted record renders whole, never as a prefix: %s", res.Text)
	}

	// The smallest mandatory pointer representation, minus one token: no representation of that
	// record fits, which is contract §3's overflow — and not a smaller representation of it.
	cheapest := cands[0].Reps[1].AssembledCost
	for _, c := range cands[1:] {
		if c.Reps[1].AssembledCost < cheapest {
			cheapest = c.Reps[1].AssembledCost
		}
	}
	tight := cheapest - x9v5SelectorBudgetShortfall
	require.Positive(t, int(tight), "fixture sanity: the tight budget is still a real budget")
	over, err := analyzer.Propose(ctx, 0, cands, lambda, tight)
	require.NoError(t, err, "overflow is an OUTCOME, not an error")
	require.True(t, over.Overflow)
	require.Empty(t, over.Chosen, "no partial selection is returned alongside an overflow")
	require.Zero(t, int(over.Tokens))
	require.NotEmpty(t, over.Item)
	require.Equal(t, []dag.NodeID{over.Item}, over.Archive, "the overflowed record is archived, not lost")
	require.Contains(t, over.Reason, string(over.Item))
	require.NotContains(t, string(over.Item), " ", "Item is an identifier, not the sentence")

	resOver := x9v5Build(t, r, in, x9v5Outcome(over))
	require.LessOrEqual(t, int(resOver.Tokens), int(rehydrateBudget))
	require.True(t, rehydrate.Overflowed(resOver.Dropped), "the rehydrator files the overflow explicitly: %+v", resOver.Dropped)
	overDrops := x9v5DropsOf(resOver.Dropped)
	require.Equal(t, []string{string(over.Item)}, overDrops["overflow"], "the overflow drop names the record")
	for _, e := range resOver.Dropped {
		if e.Kind == "overflow" {
			require.Contains(t, e.Detail, "OVERFLOW")
			require.Contains(t, e.Detail, "recall(id)", "the overflow carries its recovery path")
			require.Contains(t, e.Detail, over.Reason)
		}
	}
	// Every record is archive-only with a recovery path — the overflowed one included, because
	// Proposal.Archive names it and applySelection reports each Archive entry before it appends the
	// overflow line, so that record is filed under BOTH kinds (a benign double report, pinned here
	// so a change to it is noticed) — and no record is rendered at all, whole or partial.
	archived := make(map[string]bool, len(overDrops["archive_only"]))
	for _, id := range overDrops["archive_only"] {
		archived[id] = true
	}
	for _, rec := range in.Eliminated {
		require.True(t, archived[rec.ID], "every unselected record is reported archive-only, never silently absent: %+v", overDrops)
		require.NotContains(t, resOver.Text, rec.Approach, "no byte of an unselected record is injected")
	}
	require.Len(t, overDrops["archive_only"], x9v5Eliminations)

	// ── NEGATIVE CONTROL 1: promotion switched off through its real config state ────────────────
	//
	// runtime.phase7.retrieval.demandPromotion=false is report-only: the gate still admits, Order
	// stays empty, Apply changes nothing, and the formerly promoted pointer is back at the tail —
	// so at a budget where arm 1 kept it while cutting a peer, it is now the FIRST tool pointer cut.
	require.False(t, r.P.Cfg.Runtime.Phase7.Retrieval.DemandPromotion, "the shipped default is off")
	off := x9v5Promote(t, r, in, r.P.Cfg.Runtime.Phase7.Retrieval.DemandPromotion)
	require.Equal(t, 1, off.Admitted(), "report-only still evaluates and admits")
	require.Empty(t, off.Order, "and applies nothing")
	unpromoted, _ := off.Apply(in)
	require.Equal(t, in.Pointers.Tools, unpromoted.Pointers.Tools, "the order the writer chose stands")

	witness := core.Tokens(0)
	for budget := full; budget >= 1; budget-- {
		k := kept[budget]
		if len(k.tools) > 0 && len(k.tools) < len(promoted.Pointers.Tools) {
			witness = budget
			break
		}
	}
	require.Positive(t, int(witness))
	t.Logf("v5 §4.9 negative control 1: witness budget %d kept %v with promotion on", int(witness), kept[witness].tools)
	ctl, _ := checkpoint.Truncate(unpromoted, witness, tiers, r.Est)
	ctlKept := x9v5KeptOf(ctl)
	require.NotContains(t, ctlKept.tools, string(oldest.ToolUseID),
		"NEGATIVE CONTROL: with promotion off the oldest pointer is cut first at budget %d. If it "+
			"survived, arm 1's 'promoted survives' assertion said nothing about promotion: %v",
		int(witness), ctlKept.tools)
	require.Contains(t, kept[witness].tools, string(oldest.ToolUseID),
		"and arm 1 kept the very same pointer at the very same budget")

	// ── NEGATIVE CONTROL 2: selection switched off through its real rollback path ───────────────
	//
	// A nil Selection is what a disabled runtime.selection.submodularEnabled hands Build. The same
	// checkpoint, ledger and budget must then produce NO overflow entry and render the records
	// whole under the shipped slice-score ranking — proving the overflow drop above came from the
	// selection and not from the rehydrator's own budget arithmetic.
	require.False(t, r.P.Cfg.Runtime.Selection.SubmodularEnabled, "the shipped default is off")
	resNil := x9v5Build(t, r, in, nil)
	require.False(t, rehydrate.Overflowed(resNil.Dropped),
		"NEGATIVE CONTROL: with selection off nothing overflows: %+v", resNil.Dropped)
	require.Empty(t, x9v5DropsOf(resNil.Dropped)["overflow"])
	require.Empty(t, x9v5DropsOf(resNil.Dropped)["archive_only"])
	for _, rec := range in.Eliminated {
		require.Contains(t, resNil.Text, "- "+rec.Target+" — \""+rec.Approach+"\" — "+rec.Reason+" [active]",
			"NEGATIVE CONTROL: the record renders whole with no selection: %s", resNil.Text)
	}

	// Append-only, against THIS run's sealed artifact rather than testutil's seeded 0001.json (which
	// the seal above already claimed): the §7.4 doors must refuse to rewrite or re-create it.
	sealedPath := paths.CheckpointPath(paths.Of(r.P.Root), ref.Seq)
	f, err := paths.OpenFile(sealedPath, os.O_WRONLY|os.O_TRUNC, x9v5FilePerm)
	if f != nil {
		_ = f.Close()
	}
	require.True(t, errors.Is(err, core.ErrAppendOnly) || errors.Is(err, os.ErrExist),
		"a truncating write to the sealed checkpoint must be refused append-only, got %v", err)
	err = paths.CreateNew(sealedPath, []byte("{}"))
	require.True(t, errors.Is(err, core.ErrAppendOnly) || errors.Is(err, os.ErrExist),
		"a second write of the sealed sequence must be refused append-only, got %v", err)
}
