package checkpoint_test

// Promotion tests (SP-16 commit 5, gate M6-G16-C: "Promotion changes only actual future Qompack
// delivery with complete record/overhead budgeting; consumer and overflow trace").
//
// The gate has four separable claims and each has its own case here:
//
//   - ONLY FUTURE DELIVERY. Evidence observed serving checkpoint N may change N+1 and never N,
//     because N has been delivered and archived. TestPromote_SameEpoch* and
//     TestPromote_ApplyDoesNotMutateTheArchivedCheckpoint are the two halves of that.
//   - COMPLETE RECORD / OVERHEAD BUDGETING. Promotion spends a bounded overhead, and what does
//     not fit is reported rather than lost: TestPromote_OverflowLeavesADropEntry.
//   - CONSUMER TRACE. Every candidate comes back with a decision, a cost and, when it was not
//     applied, a reason: TestPromote_EveryNonAppliedResultExplainsItself.
//   - AND THE ONE THE DEMAND RECORD EXISTS FOR: frequency is not usefulness.
//     TestPromote_FrequencyWithoutOutcomesIsWithheld is the case that would fail if promotion
//     ever started reading Requests as evidence of benefit.
//
// TestPromotedPointersSurviveTruncation is the end-to-end claim: promotion is expressed purely as
// an ordering, and it changes what is delivered only by way of the truncation budget that was
// already there. If Truncate ever stopped cutting tool pointers tail-first, this file would fail
// rather than quietly promoting nothing.

import (
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// promHash returns a distinct, stable hash for n.
func promHash(n int) core.Hash {
	return core.HashBytes("qompack.test.promote", []byte{byte(n)})
}

// promPointer builds the tool pointer for n.
func promPointer(n int) checkpoint.ToolPointer {
	return checkpoint.ToolPointer{
		ToolUseID: core.ToolUseID("tool-" + string(rune('a'+n))),
		Hash:      promHash(n),
		Summary:   "a result worth re-reading",
	}
}

// wellEvidenced is a candidate that passes every evidence check: re-expanded, fully instrumented,
// and measured useful. Cases below vary one field at a time from this baseline so that each test
// names exactly one reason for its outcome.
func wellEvidenced(n int) checkpoint.PromotionCandidate {
	return checkpoint.PromotionCandidate{
		Pointer:    promPointer(n),
		Expansions: 4,
		Demand: store.Demand{
			Key:      promHash(n).String(),
			Requests: 4,
			Useful:   4,
			Failed:   0,
		},
		Authority: core.AuthorityToolObservation,
	}
}

// promRequest is an enabled pass from seq 1 to seq 2 with room for everything.
func promRequest(cands ...checkpoint.PromotionCandidate) checkpoint.PromotionRequest {
	return checkpoint.PromotionRequest{
		Enabled:     true,
		ObservedSeq: 1,
		TargetSeq:   2,
		Candidates:  cands,
		Overhead:    1_000_000,
		Est:         newEstimator(),
	}
}

// TestPromote_SameEpochIsRefused is the gate's first sentence in executable form: evidence
// gathered while serving a checkpoint may not change that same checkpoint.
//
// §10 Phase 7 says "same-epoch delivery does not prove native residency" — the model having asked
// for something while reading checkpoint 2 is not evidence about what checkpoint 2 should have
// contained, because it is evidence produced BY checkpoint 2 having been delivered. Promoting on
// it would be a loop that reads its own output as an observation.
func TestPromote_SameEpochIsRefused(t *testing.T) {
	t.Parallel()

	req := promRequest(wellEvidenced(0), wellEvidenced(1))
	req.ObservedSeq, req.TargetSeq = 2, 2

	plan := checkpoint.Promote(req)

	require.Zero(t, plan.Admitted(), "a same-epoch pass must promote nothing")
	require.Empty(t, plan.Order)
	require.Len(t, plan.Results, 2)
	for _, r := range plan.Results {
		require.Equal(t, checkpoint.PromotionRefused, r.Decision)
		require.Contains(t, r.Why.Reason, "same-epoch delivery does not prove native residency")
		require.NotEmpty(t, r.Why.Recovery, "a refusal must say what still applies")
	}
	require.NotEmpty(t, plan.Omissions)
}

// TestPromote_AnEarlierTargetIsRefused pins the other side of the ordering: rewriting an older
// checkpoint from newer evidence is the archive-corruption shape, and it is refused by the same
// comparison rather than by a separate check that could drift from it.
func TestPromote_AnEarlierTargetIsRefused(t *testing.T) {
	t.Parallel()

	req := promRequest(wellEvidenced(0))
	req.ObservedSeq, req.TargetSeq = 9, 3

	plan := checkpoint.Promote(req)

	require.Zero(t, plan.Admitted())
	require.Equal(t, checkpoint.PromotionRefused, plan.Results[0].Decision)
}

// TestPromote_FrequencyWithoutOutcomesIsWithheld is the reason store.Demand separates Useful and
// Failed from Requests at all.
//
// Forty requests and no recorded outcome is a key something keeps needing and nobody measured. It
// may be the most valuable pointer in the checkpoint or a symptom of a broken retrieval; the
// record cannot tell, and promoting on the count alone would be asserting the first. WITHHELD,
// not refused: nothing has been judged, and a session that instruments it may promote it.
func TestPromote_FrequencyWithoutOutcomesIsWithheld(t *testing.T) {
	t.Parallel()

	c := wellEvidenced(0)
	c.Demand = store.Demand{Key: c.Demand.Key, Requests: 40, Useful: 0, Failed: 0}
	require.False(t, c.Demand.Instrumented(), "the fixture must actually be uninstrumented")

	plan := checkpoint.Promote(promRequest(c))

	require.Equal(t, checkpoint.PromotionWithheld, plan.Results[0].Decision)
	require.Contains(t, plan.Results[0].Why.Reason, "frequency")
	require.Zero(t, plan.Admitted())
}

// TestPromote_AnEmptyDemandRecordIsWithheld is the vacuous-truth corner, and it is the one that
// nearly got through.
//
// store.Demand.Instrumented() asks whether outcomes account for requests. For an all-zero record
// that is TRUE — zero outcomes do account for zero requests, with no gaps — so a candidate can
// arrive with a healthy expansion count from mcp's counter and a demand record nobody ever wrote,
// and pass the instrumentation check on a technicality. Admitting it would promote on expansion
// count alone. The gate therefore also requires the usefulness rate to be KNOWN.
func TestPromote_AnEmptyDemandRecordIsWithheld(t *testing.T) {
	t.Parallel()

	c := wellEvidenced(0)
	c.Demand = store.Demand{}
	require.True(t, c.Demand.Instrumented(),
		"the fixture must pass Instrumented() vacuously, or this test guards nothing")
	_, known := c.Demand.Usefulness()
	require.False(t, known)

	plan := checkpoint.Promote(promRequest(c))

	require.Equal(t, checkpoint.PromotionWithheld, plan.Results[0].Decision)
	require.Contains(t, plan.Results[0].Why.Reason, "not a measurement")
	require.Zero(t, plan.Admitted(), "expansion count alone must never promote")
}

// TestPromote_ATelemetryGapAlsoWithholds pins that a recorder which KNOWS it missed observations
// cannot have its remaining ones read as a complete picture.
func TestPromote_ATelemetryGapAlsoWithholds(t *testing.T) {
	t.Parallel()

	c := wellEvidenced(0)
	c.Demand.TelemetryGaps = 1

	plan := checkpoint.Promote(promRequest(c))

	require.Equal(t, checkpoint.PromotionWithheld, plan.Results[0].Decision)
}

// TestPromote_AMeasuredMissIsRefused is the case where the evidence exists and says no. Unlike a
// gap, this has been measured, so it is a refusal rather than a withholding.
func TestPromote_AMeasuredMissIsRefused(t *testing.T) {
	t.Parallel()

	c := wellEvidenced(0)
	c.Demand = store.Demand{Key: c.Demand.Key, Requests: 5, Useful: 0, Failed: 5}
	rate, known := c.Demand.Usefulness()
	require.True(t, known)
	require.Zero(t, rate)

	plan := checkpoint.Promote(promRequest(c))

	require.Equal(t, checkpoint.PromotionRefused, plan.Results[0].Decision)
	require.Contains(t, plan.Results[0].Why.Reason, "failed to help")
}

// TestPromote_TooFewExpansionsIsWithheld pins the floor: one re-expansion is a coincidence.
func TestPromote_TooFewExpansionsIsWithheld(t *testing.T) {
	t.Parallel()

	c := wellEvidenced(0)
	c.Expansions = 1

	plan := checkpoint.Promote(promRequest(c))

	require.Equal(t, checkpoint.PromotionWithheld, plan.Results[0].Decision)
}

// TestPromote_UnusableEvidenceIsRefused covers the two candidates that could not be acted on at
// all: no hash to expand, and an authority outside SP-20's vocabulary.
func TestPromote_UnusableEvidenceIsRefused(t *testing.T) {
	t.Parallel()

	noHash := wellEvidenced(0)
	noHash.Pointer.Hash = core.Hash{}

	badAuthority := wellEvidenced(1)
	badAuthority.Authority = core.Authority("vibes")

	plan := checkpoint.Promote(promRequest(noHash, badAuthority))

	require.Equal(t, checkpoint.PromotionRefused, plan.Results[0].Decision)
	require.Contains(t, plan.Results[0].Why.Reason, "no content hash")
	require.Equal(t, checkpoint.PromotionRefused, plan.Results[1].Decision)
	require.Contains(t, plan.Results[1].Why.Reason, "authority")
	require.Zero(t, plan.Admitted())
}

// TestPromote_OverflowLeavesADropEntry is the overhead-budget half of the gate.
//
// The budget is set to admit some and not all. What does not fit must come back as
// PromotionOverflowed WITH a drop entry naming it — a promotion that silently did not happen is
// indistinguishable from one that was never wanted, and the whole point of the drop report (G4.5)
// is that a reader can ask for what was left out.
func TestPromote_OverflowLeavesADropEntry(t *testing.T) {
	t.Parallel()

	cands := []checkpoint.PromotionCandidate{
		wellEvidenced(0), wellEvidenced(1), wellEvidenced(2), wellEvidenced(3),
	}
	req := promRequest(cands...)

	// Price one pointer, then allow room for two and a half of them.
	full := checkpoint.Promote(req)
	require.Equal(t, 4, full.Admitted(), "the unbounded pass must admit everything")
	one := full.Results[0].Cost
	require.Positive(t, one, "the estimator must actually price a pointer")

	req.Overhead = one*2 + one/2
	plan := checkpoint.Promote(req)

	require.Equal(t, 2, plan.Admitted(), "exactly the pointers that fit")
	require.LessOrEqual(t, plan.Spent, plan.Overhead, "the budget must bind")
	require.Len(t, plan.Dropped, 2, "every overflow is reported")

	overflowed := 0
	for _, r := range plan.Results {
		if r.Decision == checkpoint.PromotionOverflowed {
			overflowed++
			require.Contains(t, r.Why.Recovery, "expand(hash) still resolves it",
				"an overflowed pointer is still retrievable and must say so")
		}
	}
	require.Equal(t, 2, overflowed)
	for _, d := range plan.Dropped {
		require.Equal(t, "promotion_overflow", d.Kind)
		require.NotEmpty(t, d.ID)
	}
}

// TestPromote_AZeroOverheadPromotesNothing pins that an unset budget is honoured as zero rather
// than treated as unlimited — the same choice ReminderBudget makes for maxRemindersPerSession, and
// for the same reason: a default that means "no limit" is a default that ships unbounded.
func TestPromote_AZeroOverheadPromotesNothing(t *testing.T) {
	t.Parallel()

	req := promRequest(wellEvidenced(0), wellEvidenced(1))
	req.Overhead = 0

	plan := checkpoint.Promote(req)

	require.Zero(t, plan.Admitted())
	require.Len(t, plan.Dropped, 2)
}

// TestPromote_DisabledStillEvaluatesButAppliesNothing pins report-only as a real state.
//
// It is how M6-G16-C's evidence gets collected before the switch is ever turned on: the pass runs,
// every decision is recorded, and Order stays empty so Apply cannot change anything. Skipping
// evaluation when disabled would mean the only way to learn whether promotion helps is to enable
// it first.
func TestPromote_DisabledStillEvaluatesButAppliesNothing(t *testing.T) {
	t.Parallel()

	req := promRequest(wellEvidenced(0), wellEvidenced(1))
	req.Enabled = false

	plan := checkpoint.Promote(req)

	require.False(t, plan.Enabled)
	require.Equal(t, 2, plan.Admitted(), "the evaluation still happens")
	require.Empty(t, plan.Order, "but nothing is queued for application")
	require.NotEmpty(t, plan.Omissions, "and the plan says why it was not applied")

	before := promCheckpoint(4)
	after, _ := plan.Apply(before)
	require.Equal(t, before, after, "a disabled plan changes nothing")
}

// TestPromote_ApplyDoesNotMutateTheArchivedCheckpoint is the "preserve archives" claim.
//
// Apply is handed checkpoints that may have been read straight from the archive. Sorting the
// pointer slice in place would reorder a document other readers still hold — the same backing
// array — which is corruption that no golden test would catch because the file on disk is
// unchanged. The copy in Apply is what prevents it, and this is the test that fails if it goes.
func TestPromote_ApplyDoesNotMutateTheArchivedCheckpoint(t *testing.T) {
	t.Parallel()

	archived := promCheckpoint(4)
	originalOrder := make([]checkpoint.ToolPointer, len(archived.Pointers.Tools))
	copy(originalOrder, archived.Pointers.Tools)

	// Promote the LAST pointer, so applying must move it and any in-place sort would be visible.
	c := wellEvidenced(3)
	plan := checkpoint.Promote(promRequest(c))
	require.Equal(t, 1, plan.Admitted())

	out, _ := plan.Apply(archived)

	require.Equal(t, originalOrder, archived.Pointers.Tools,
		"the input checkpoint's pointer order must be untouched")
	require.Equal(t, promHash(3), out.Pointers.Tools[0].Hash, "the promoted pointer leads the copy")
	require.Len(t, out.Pointers.Tools, len(originalOrder), "promotion drops nothing")
}

// TestPromote_ApplyOrdersPromotedFirstAndKeepsTheRestStable pins both halves of the ordering: the
// plan's order for what it named, and the writer's order for everything else.
func TestPromote_ApplyOrdersPromotedFirstAndKeepsTheRestStable(t *testing.T) {
	t.Parallel()

	// Promote 3 then 1, by giving 3 the better measured usefulness.
	third := wellEvidenced(3)
	first := wellEvidenced(1)
	first.Demand = store.Demand{Key: first.Demand.Key, Requests: 4, Useful: 3, Failed: 1}

	plan := checkpoint.Promote(promRequest(first, third))
	require.Equal(t, []core.Hash{promHash(3), promHash(1)}, plan.Order,
		"the better-measured candidate leads")

	out, _ := plan.Apply(promCheckpoint(5))

	got := make([]core.Hash, 0, len(out.Pointers.Tools))
	for _, p := range out.Pointers.Tools {
		got = append(got, p.Hash)
	}
	require.Equal(t,
		[]core.Hash{promHash(3), promHash(1), promHash(0), promHash(2), promHash(4)},
		got, "promoted in plan order, then the rest in the writer's order")
}

// TestPromote_PromotedPointersSurviveTruncation is the end-to-end claim, and the reason promotion
// is an ordering rather than a new field.
//
// Truncate cuts tool pointers tail-first. Moving a pointer to the front therefore means exactly
// "this survives the next budget", using the mechanism that already exists — no schema change, no
// second budget, nothing for Rule W-2 to catch. A pointer that would have been cut before
// promotion is present after it, and the checkpoint is still within budget.
func TestPromote_PromotedPointersSurviveTruncation(t *testing.T) {
	t.Parallel()

	est := newEstimator()
	tiers := config.Defaults().Checkpoint.Tiers
	base := promCheckpoint(6)
	base.Narrative = "" // narrative is cut before tool pointers; empty it so the budget reaches them

	// The largest budget that actually costs a tool pointer. Stepping down to it rather than
	// guessing an offset keeps the test meaningful if the estimator's constants ever move: a
	// hard-coded budget could silently stop cutting and assert nothing.
	var budget core.Tokens
	var before checkpoint.Checkpoint
	for b := mustSize(base, est); b > 0; b-- {
		before, _ = checkpoint.Truncate(base, b, tiers, est)
		if len(before.Pointers.Tools) < len(base.Pointers.Tools) {
			budget = b
			break
		}
	}
	require.Positive(t, budget, "no budget cut a tool pointer, so this test would prove nothing")

	// The last pointer is one of the ones that went.
	last := promHash(5)
	require.False(t, hasToolHash(before, last), "the fixture's last pointer must be cut without promotion")

	plan := checkpoint.Promote(promRequest(wellEvidenced(5)))
	require.Equal(t, 1, plan.Admitted())
	promoted, drops := plan.Apply(base)
	require.Empty(t, drops)

	after, _ := checkpoint.Truncate(promoted, budget, tiers, est)

	require.True(t, hasToolHash(after, last), "the promoted pointer must survive the same budget")
	require.LessOrEqual(t, mustSize(after, est), budget, "promotion must not exceed the budget")
}

// TestPromote_EveryNonAppliedResultExplainsItself is the consumer-trace claim, asserted across
// every outcome the gate can reach rather than one at a time. A decision with no reason is a
// decision nobody can act on.
func TestPromote_EveryNonAppliedResultExplainsItself(t *testing.T) {
	t.Parallel()

	noHash := wellEvidenced(0)
	noHash.Pointer.Hash = core.Hash{}
	thin := wellEvidenced(1)
	thin.Expansions = 1
	uninstrumented := wellEvidenced(2)
	uninstrumented.Demand = store.Demand{Requests: 9}
	miss := wellEvidenced(3)
	miss.Demand = store.Demand{Requests: 2, Failed: 2}

	req := promRequest(noHash, thin, uninstrumented, miss, wellEvidenced(4), wellEvidenced(5))
	full := checkpoint.Promote(req)
	req.Overhead = full.Results[4].Cost // room for exactly one of the two good ones
	plan := checkpoint.Promote(req)

	seen := map[checkpoint.PromotionDecision]int{}
	for i, r := range plan.Results {
		seen[r.Decision]++
		require.Equal(t, req.Candidates[i].Pointer, r.Pointer, "results join back by index")
		if r.Decision == checkpoint.PromotionAdmitted {
			require.Equal(t, core.Omission{}, r.Why, "an admitted promotion has nothing to explain")
			continue
		}
		require.NotEmpty(t, r.Why.Reason, "%s at %d must say why", r.Decision, i)
		require.NotEmpty(t, r.Why.Recovery, "%s at %d must say what to do", r.Decision, i)
	}
	require.Equal(t, 2, seen[checkpoint.PromotionRefused])
	require.Equal(t, 2, seen[checkpoint.PromotionWithheld])
	require.Equal(t, 1, seen[checkpoint.PromotionAdmitted])
	require.Equal(t, 1, seen[checkpoint.PromotionOverflowed])
	require.Equal(t, 6, plan.Considered)
}

// TestPromote_IsDeterministic pins that equal evidence yields one order. Two candidates identical
// but for their hash must not swap between runs, or a golden checkpoint would be unreproducible.
func TestPromote_IsDeterministic(t *testing.T) {
	t.Parallel()

	req := promRequest(wellEvidenced(2), wellEvidenced(0), wellEvidenced(1))
	first := checkpoint.Promote(req)
	for i := 0; i < 8; i++ {
		require.Equal(t, first, checkpoint.Promote(req))
	}
	require.Len(t, first.Order, 3)
}

// TestPromote_ANilEstimatorReportsItsOwnBlindness pins that promotion does not quietly become
// unbounded when nobody priced it. The pass still runs — refusing would make a missing estimator
// fail a session — but the plan says the budget could not bind.
func TestPromote_ANilEstimatorReportsItsOwnBlindness(t *testing.T) {
	t.Parallel()

	req := promRequest(wellEvidenced(0))
	req.Est = nil

	plan := checkpoint.Promote(req)

	require.Equal(t, 1, plan.Admitted())
	require.Zero(t, plan.Spent)
	found := false
	for _, o := range plan.Omissions {
		if o.Reason != "" && o.Recovery != "" {
			found = true
		}
	}
	require.True(t, found, "the plan must record that it could not price anything")
}

// TestPromote_NoCandidatesIsAnEmptyPlan pins the trivial case: no evidence claims nothing, and
// Apply on an empty plan is the identity.
func TestPromote_NoCandidatesIsAnEmptyPlan(t *testing.T) {
	t.Parallel()

	plan := checkpoint.Promote(promRequest())

	require.Zero(t, plan.Considered)
	require.Empty(t, plan.Order)
	require.Empty(t, plan.Dropped)

	c := promCheckpoint(3)
	out, drops := plan.Apply(c)
	require.Equal(t, c, out)
	require.Empty(t, drops)
}

// promCheckpoint builds a checkpoint carrying n tool pointers, hashes 0..n-1 in order, on top of
// the truncation suite's golden fixture so that every other field is a real one.
func promCheckpoint(n int) checkpoint.Checkpoint {
	c := golden0002()
	tools := make([]checkpoint.ToolPointer, 0, n)
	for i := 0; i < n; i++ {
		tools = append(tools, promPointer(i))
	}
	c.Pointers.Tools = tools
	return c
}

// hasToolHash reports whether c still carries a pointer for h.
func hasToolHash(c checkpoint.Checkpoint, h core.Hash) bool {
	for _, p := range c.Pointers.Tools {
		if p.Hash == h {
			return true
		}
	}
	return false
}
