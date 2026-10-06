package rehydrate

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/tokens"
)

// The three budget paths no other test in this package reaches: the unset-request min-fill, the
// tier-1 material that does not fit at all, and the clamp that never raises a caller's ask.

// ckLongEvolution is a checkpoint whose user_intent.evolution is long enough that item 2's own
// share cannot hold it, built by FIELD ASSIGNMENT (Rule W-2: never through checkpoint.Writer).
//
// A long evolution list is what makes the min-fill path reachable at all. Item 2's deltas take the
// FIRST share, so they are filled with no carry behind them — every later item's allowance is
// swollen by the carry the items before it did not use, and none of them can truncate on a
// realistic checkpoint. Truncating the first share while the payload as a whole is still far below
// minTokens is exactly the "stopped at 3K with 9K available" case min-fill exists for.
func ckLongEvolution(n int) checkpoint.Checkpoint {
	var cp checkpoint.Checkpoint
	cp.Version = checkpoint.SchemaVersion
	cp.Session = core.SessionID("sess_evo")
	cp.Seq = core.CheckpointSeq(3)
	cp.Created = "2026-01-01T00:12:30.000Z"
	cp.Invariants = []checkpoint.Invariant{{
		ID: "inv_a1b2c3d4e5f6", Text: "Never bypass the connection pool in transaction mode.",
		Source: "user", Pinned: core.UnixMilli(1767225120000),
	}}
	cp.UserIntent.Original = "Fix the intermittent 500s on POST /api/session/refresh."
	for i := 0; i < n; i++ {
		cp.UserIntent.Evolution = append(cp.UserIntent.Evolution,
			"Narrowed the failure window again: "+strings.Repeat("scope note ", 12)+itoa(i))
	}
	return cp
}

// byteEstimator prices one token per byte. It is dear enough that minTokens is reached before the
// host character ceiling (tokens >= characters for any text), which is the regime min-fill was
// written for; the baseline (len+3)/4 estimator now meets the ceiling first (D5).
type byteEstimator struct{ fakeEstimator }

func (byteEstimator) Estimate(b []byte, _ tokens.Class) core.Tokens       { return core.Tokens(len(b)) }
func (byteEstimator) EstimateString(s string, _ tokens.Class) core.Tokens { return core.Tokens(len(s)) }

// TestBuild_MinFillReadmitsUnits pins min-fill in both regimes the payload can be bounded in.
//
// D5 changed which bound an unset build meets first. Under the baseline estimator the host
// character ceiling holds about 2,350 tokens, far below minTokens, so "fill to about minTokens" is
// no longer reachable there by design: min-fill re-admits toward the CEILING instead. Under an
// estimator dear enough that minTokens comes first, the original claim stands unchanged, threshold
// included. The mechanism asserted in both is the same: an unset call re-admits what its shares
// dropped, a named budget is never min-filled, and a re-admitted unit leaves the drop report.
func TestBuild_MinFillReadmitsUnits(t *testing.T) {
	cp := ckLongEvolution(300)
	cfg := testCfg().Runtime.Rehydrate

	t.Run("minTokens_binds_first", func(t *testing.T) {
		d := fullDeps(t, cp)
		d.Tokens = byteEstimator{}

		// An UNSET budget is the daemon's own call. clampBudget answers it with maxTokens, and the
		// min-fill pass then re-admits toward minTokens.
		got, err := Build(context.Background(), requestFor(t, cp, 0), d)
		require.NoError(t, err)
		require.LessOrEqual(t, int(got.Tokens), cfg.MaxTokens, "min-fill may never exceed the hard cap")
		require.LessOrEqual(t, hostChars(got.Text), PayloadCeilingChars, "nor the host ceiling")
		// Re-admission stops at the first pending unit the remaining slack cannot cover, so the total
		// lands just under minTokens rather than exactly on it. One unit of headroom is the whole
		// tolerance this assertion needs.
		require.GreaterOrEqual(t, int(got.Tokens), cfg.MinTokens-128,
			"an unset budget with material to spare must fill to about minTokens, not stop at the share")

		d = fullDeps(t, cp)
		d.Tokens = byteEstimator{}
		stopped, err := Build(context.Background(), requestFor(t, cp, core.Tokens(cfg.MinTokens)), d)
		require.NoError(t, err)
		// The same checkpoint under a NAMED budget of exactly minTokens gets no min-fill: raising a
		// caller's ask is precisely what the hard cap forbids, so it stops at its shares instead.
		require.Less(t, int(stopped.Tokens), int(got.Tokens),
			"a named budget must not be min-filled, so it carries strictly less than the unset call")
		requireMinFillShape(t, got, stopped)
	})

	t.Run("host_ceiling_binds_first", func(t *testing.T) {
		got, err := Build(context.Background(), requestFor(t, cp, 0), fullDeps(t, cp))
		require.NoError(t, err)
		require.Less(t, int(got.Tokens), cfg.MinTokens,
			"fixture sanity: under the baseline estimator the ceiling is met long before minTokens")
		// Filled to the ceiling, to within the one unit the remaining room could not cover.
		unitChars := hostChars(quoteLines(cp.UserIntent.Evolution[len(cp.UserIntent.Evolution)-1]))
		require.LessOrEqual(t, hostChars(got.Text), PayloadCeilingChars)
		require.GreaterOrEqual(t, hostChars(got.Text), PayloadCeilingChars-unitChars,
			"an unset budget with material to spare must fill to the host ceiling, not stop at the share")

		// Criterion change (w15-rehydrate, D49): this half compared the same 300-delta checkpoint
		// under a named budget and counted evolution deltas. D49 gives the room a payload leaves
		// unused to evolution, newest first, in EVERY build (Build step 9a) — the daemon's own call
		// names its budget, and F-C4-UAT06-1 was a correction dropped from a payload with room — so
		// both calls now fill that checkpoint to the ceiling with deltas and the count cannot tell
		// them apart. What min-fill still adds is the OTHER ranked material step 9a does not touch:
		// with few deltas and many decisions, the unset call carries strictly more decisions.
		dcp := ckLongEvolution(3)
		for i := range 120 {
			dcp.Decisions = append(dcp.Decisions, checkpoint.Decision{
				ID:   core.DecisionID(fmt.Sprintf("dec_%012d", i)),
				What: "kept the pool in session mode for the refresh path " + itoa(i),
				Why:  "transaction mode breaks the advisory lock the refresh relies on", Turn: core.TurnIndex(i),
			})
		}
		unset, err := Build(context.Background(), requestFor(t, dcp, 0), fullDeps(t, dcp))
		require.NoError(t, err)
		stopped, err := Build(context.Background(), requestFor(t, dcp, core.Tokens(cfg.MinTokens)), fullDeps(t, dcp))
		require.NoError(t, err)
		for i := range dcp.UserIntent.Evolution {
			require.Contains(t, stopped.Text, quoteLines(dcp.UserIntent.Evolution[i]),
				"fixture sanity: the named call has room for every delta")
		}
		const decision = "- [dec_"
		require.Greater(t, strings.Count(unset.Text, decision), strings.Count(stopped.Text, decision),
			"a named budget must not be min-filled, so it carries strictly fewer re-admitted decisions")
		// requireMinFillShape's shape, for decisions: section 7 names a dropped decision by its id, so
		// the check is on the decision's own rendered line rather than on the id anywhere in the text.
		for _, e := range unset.Dropped {
			if e.Kind == dropKindDecision {
				require.NotContains(t, unset.Text, "- ["+e.ID+"]", "a re-admitted decision left the drop report")
			}
		}
		require.Less(t, len(unset.Dropped), len(stopped.Dropped), "re-admitted units must leave the drop report")
	})
}

// requireMinFillShape is what min-fill must leave behind in either regime: whatever it re-admitted
// is off the drop report rather than merely rendered — a payload that injects a unit AND reports it
// dropped is telling the agent two different things.
func requireMinFillShape(t *testing.T, got, stopped Result) {
	t.Helper()
	for _, e := range got.Dropped {
		if e.Kind == dropKindUserIntentEvolution {
			continue
		}
		require.NotContains(t, got.Text, e.ID)
	}
	require.Less(t, len(got.Dropped), len(stopped.Dropped), "re-admitted units must leave the drop report")
}

// TestBuild_LatestEvolutionSurvivesTruncation is the current-authority rule item 2 must obey.
// UserIntent.Evolution is append-only and oldest-first (checkpoint/writer.go's
// appendEvolutionLocked), and a tight budget can only admit a handful of its deltas. Filling in
// plain stored order would keep the OLDEST restatements and drop the newest — resurrecting exactly
// the intent a later authorized correction superseded. The LAST restatement — the current
// authorized intent — must survive even when the budget forces older ones out.
func TestBuild_LatestEvolutionSurvivesTruncation(t *testing.T) {
	const n = 50
	cp := ckLongEvolution(n)
	d := fullDeps(t, cp)

	// Small enough that item 2's share cannot hold every delta (TestBuild_MinFillReadmitsUnits
	// shows 300 deltas overflow even minTokens; a budget well under that forces the same
	// truncation here), large enough that tier 1 and a few deltas still fit.
	const tight = core.Tokens(1200)
	got, err := Build(context.Background(), requestFor(t, cp, tight), d)
	require.NoError(t, err)
	require.LessOrEqual(t, int(got.Tokens), int(tight))

	newest := cp.UserIntent.Evolution[n-1]
	require.Contains(t, got.Text, newest, "the most recent, currently authorized restatement must survive")

	// Criterion change (w15-rehydrate, D49): this row required the SECOND-newest delta to be dropped,
	// as its fixture sanity and as its proof that the cut falls on the old side. D49 gives unused room
	// to evolution newest first (Build step 9a), so at this budget a few more deltas arrive and which
	// index the cut falls on is no longer fixed. The row now asserts the property that sentence stood
	// for, at every index: the budget truncates history (the oldest delta is dropped), the newest is
	// never dropped, and what renders is a newest-first prefix — no delta renders while a NEWER one is
	// dropped, so the cut always falls on the old side.
	dropped := make(map[string]bool)
	for _, e := range got.Dropped {
		if e.Kind == dropKindUserIntentEvolution {
			dropped[e.ID] = true
		}
	}
	require.False(t, dropped[itoa(n-1)], "the current authorized intent must never appear in the drop report")
	require.True(t, dropped[itoa(0)], "the budget must actually be tight enough to truncate history (fixture sanity)")
	cut := false
	for i := n - 1; i >= 0; i-- {
		shown := strings.Contains(got.Text, quoteLines(cp.UserIntent.Evolution[i]))
		require.NotEqual(t, shown, dropped[itoa(i)], "delta %d is shown or named, never both or neither", i)
		if cut {
			require.False(t, shown, "delta %d renders although a newer delta was dropped", i)
		}
		cut = cut || !shown
	}
}

func TestBuild_Tier1ThatCannotFitIsDroppedWhole(t *testing.T) {
	cp := ckFull(t)
	log := &spyLogger{}
	d := fullDeps(t, cp)
	d.Log = log

	// Large enough for the injection wrapper, far too small for any tier-1 item. This is the §12.3
	// boundary: the rehydration cannot do its job and says so rather than failing the hook.
	const tiny = core.Tokens(60)
	got, err := Build(context.Background(), requestFor(t, cp, tiny), d)
	require.NoError(t, err, "a budget too small is a degraded rehydration, never an error")
	require.True(t, got.Degraded)
	require.LessOrEqual(t, int(got.Tokens), int(tiny))
	// Criterion change (w19-rehydrate, D59): no tier-1 item fits, so no section is emitted — and a
	// compaction that dropped material is never silent, so the payload is the loss notice alone,
	// never an empty tagged wrapper and never nothing.
	require.Len(t, got.Items, 1, "no tier-1 item fits, so only the loss notice is emitted")
	require.Equal(t, ItemDropReport, got.Items[0].Kind)
	require.NotContains(t, got.Text, AffordanceNotice(), "no tier-1 record is emitted")
	for _, inv := range cp.Invariants {
		require.NotContains(t, got.Text, "- ["+inv.ID+"] ", "no tier-1 record is emitted")
	}
	require.Contains(t, got.Text, itoa(len(got.Dropped))+" items",
		"the notice counts everything the drop report names")
	require.Contains(t, got.Text, "call dropped()")
	require.Positive(t, log.loud, "tier-1 material forced out of the payload must be Loud")

	// Each excluded tier-1 item is NAMED. Tier-1 units carry no drop of their own, so the report
	// has to mint one — without it the agent would lose its invariants silently.
	kinds := map[string]bool{}
	for _, e := range got.Dropped {
		kinds[e.Kind] = true
		if e.ID == "tier1" {
			require.Contains(t, e.Detail, "emitted whole or not at all")
		}
	}
	require.True(t, kinds[ItemInvariants.String()], "the pinned invariants must be named: %v", got.Dropped)
	require.True(t, kinds[ItemAffordance.String()], "the retrieval affordance must be named: %v", got.Dropped)
	require.True(t, Overflowed(got.Dropped),
		"a single oversized critical record must produce a NAMED, reportable overflow (T11-BUDGET-02)")
}

// TestBuild_SingleOversizedCriticalRecordOverflows is T11-BUDGET-02's second required case in
// isolation from TestBuild_Tier1ThatCannotFitIsDroppedWhole: the budget is otherwise generous —
// every OTHER item has ample room — and exactly ONE tier-1 record (an invariant) is, by itself,
// larger than the whole budget. Qompack.md forbids silently cutting it "merely because it crosses
// a local boundary": it must be emitted whole or reported as an explicit, named overflow, never
// silently shrunk to fit.
func TestBuild_SingleOversizedCriticalRecordOverflows(t *testing.T) {
	cp := ckFull(t)
	giant := strings.Repeat("this invariant is a single sentence repeated many times over. ", 400)
	cp.Invariants = append(cp.Invariants, checkpoint.Invariant{
		ID: "inv_oversized00", Text: giant, Source: "user",
	})
	log := &spyLogger{}
	d := fullDeps(t, cp)
	d.Log = log

	// Generous by the §8.6 band's own standard — everything but the giant invariant fits with
	// room to spare — yet still smaller than the giant invariant alone.
	const generousButNotForOneRecord = core.Tokens(2000)
	got, err := Build(context.Background(), requestFor(t, cp, generousButNotForOneRecord), d)
	require.NoError(t, err)
	require.LessOrEqual(t, int(got.Tokens), int(generousButNotForOneRecord))
	require.True(t, got.Degraded, "an essential record that cannot fit degrades the rehydration")
	require.True(t, Overflowed(got.Dropped), "the oversized invariant must be a NAMED overflow")

	var found bool
	for _, e := range got.Dropped {
		if e.Kind == ItemInvariants.String() && e.ID == "tier1" {
			found = true
			require.Contains(t, e.Detail, "OVERFLOW")
		}
	}
	require.True(t, found, "the oversized invariant itself must be named in the drop report: %v", got.Dropped)
	require.NotContains(t, got.Text, giant,
		"the oversized record must not be silently shrunk to fit; it is either whole or absent")
}

func TestBuild_WrapperAloneOverBudgetInjectsNothing(t *testing.T) {
	cp := ckFull(t)
	log := &spyLogger{}
	d := fullDeps(t, cp)
	d.Log = log

	got, err := Build(context.Background(), requestFor(t, cp, 4), d)
	require.NoError(t, err)
	require.True(t, got.Degraded)
	require.Empty(t, got.Text)
	require.Zero(t, int(got.Tokens))
	require.Len(t, got.Dropped, 1)
	require.Equal(t, "payload", got.Dropped[0].ID)
	require.Equal(t, dropKindOverflow, got.Dropped[0].Kind, "a tiny budget is a NAMED overflow, not a plain narrative drop")
	require.True(t, Overflowed(got.Dropped), "Overflowed must recognize the wrapper-alone case")
	require.Equal(t, 1, log.loud)
}

// TestBuild_ZeroBudgetProducesExplicitOverflow is T11-BUDGET-02's first required case: an UNSET
// vs. an explicitly ZERO budget must both be recognized. clampBudget treats <= 0 as "unset, fill
// to the ceiling" for Request.Budget's own semantics — so a genuinely zero DECLARED budget is
// exercised here directly against the wrapper-fit check in Build, at the estimator level, rather
// than through clampBudget's fill-to-ceiling behaviour.
func TestBuild_ZeroBudgetProducesExplicitOverflow(t *testing.T) {
	cp := ckFull(t)
	d := fullDeps(t, cp)

	// clampBudget maps an unset (<=0) Request.Budget to the configured ceiling, so a literal zero
	// cannot reach Build as zero through the public Request path — it is deliberately
	// indistinguishable from "no budget named" (see TestClampBudget_NeverRaisesAndNeverExceedsTheCap).
	// What DOES reach Build as an unrepresentable-even-empty case is the smallest budget
	// clampBudget ever passes through unchanged: 1 token, far below any wrapper. That is the
	// declared-budget-of-effectively-zero case T11-BUDGET-02 asks for.
	got, err := Build(context.Background(), requestFor(t, cp, 1), d)
	require.NoError(t, err, "even a budget of 1 token degrades rather than failing the hook")
	require.True(t, got.Degraded)
	require.Empty(t, got.Text, "nothing can be represented at all; no partial or shrunk payload")
	require.Zero(t, int(got.Tokens))
	require.True(t, Overflowed(got.Dropped), "a 1-token budget must produce a NAMED overflow, not silent emptiness")
}

// TestBuild_OverflowDetectedWithoutACalibratedEstimator asserts overflow detection does not depend
// on a wired tokens.Estimator: budget.go's estimate() falls back to the bare (len+3)/4 formula when
// Deps.Tokens is nil, and that fallback must still correctly identify an overflow rather than
// silently under- or over-counting it away. This is the "verify budget.go actually emits overflow
// rather than falling back to a bare estimator [and losing the overflow signal]" gate.
func TestBuild_OverflowDetectedWithoutACalibratedEstimator(t *testing.T) {
	cp := ckFull(t)
	giant := strings.Repeat("this invariant is a single sentence repeated many times over. ", 400)
	cp.Invariants = append(cp.Invariants, checkpoint.Invariant{
		ID: "inv_oversized01", Text: giant, Source: "user",
	})
	d := fullDeps(t, cp)
	d.Tokens = nil // force the bare estimator fallback

	got, err := Build(context.Background(), requestFor(t, cp, core.Tokens(2000)), d)
	require.NoError(t, err)
	require.True(t, Overflowed(got.Dropped),
		"the bare (len+3)/4 estimator must still catch an oversized critical record")
	require.NotContains(t, got.Text, giant)
}

func TestClampBudget_NeverRaisesAndNeverExceedsTheCap(t *testing.T) {
	cfg := testCfg().Runtime.Rehydrate
	maxT, minT := core.Tokens(cfg.MaxTokens), core.Tokens(cfg.MinTokens)

	require.Equal(t, maxT, clampBudget(0, cfg), "an unset ask means the cap")
	require.Equal(t, maxT, clampBudget(-1, cfg))
	require.Equal(t, maxT, clampBudget(maxT+1, cfg), "an ask over the cap is clamped down to it")
	require.Equal(t, maxT, clampBudget(maxT, cfg))
	require.Equal(t, minT, clampBudget(minT, cfg))
	// The one that is easy to get wrong: a small ask is NEVER raised to minTokens. minTokens is a
	// fill target for the daemon's own unset call, not a floor imposed on a caller.
	require.Equal(t, core.Tokens(1), clampBudget(1, cfg))
	require.Equal(t, minT/2, clampBudget(minT/2, cfg))

	// A configuration whose min exceeds its max is a misconfiguration, not a crash: min is pinned
	// down to max and the cap still binds.
	bad := cfg
	bad.MinTokens = cfg.MaxTokens * 2
	require.Equal(t, maxT, clampBudget(0, bad))
	require.Equal(t, core.Tokens(1), clampBudget(1, bad))
}

// TestEvictIndex_EvictsInReverseAdmissionOrder pins the hard-cap loop's eviction order: the section
// Build admitted last goes first. Item 8 is tier 1 and sits LAST in renderOrder, so a tail-first
// eviction would take the affordance line before anything discretionary (ADR 0011 §18); and tier 1
// goes in the reverse of §21.2's admission order — item 2, then the invariants, then the retrieval
// line every overflow pointer depends on — with item 7, whose floor is held before anything is
// admitted, last of all (F-C4-UAT05-3).
func TestEvictIndex_EvictsInReverseAdmissionOrder(t *testing.T) {
	items := []Item{
		{Kind: ItemInvariants},
		{Kind: ItemUserIntent},
		{Kind: ItemDecisions},
		{Kind: ItemSkillIndex},
		{Kind: ItemDropReport},
		{Kind: ItemAffordance},
	}
	var order []ItemKind
	for len(items) > 0 {
		i := evictIndex(items)
		order = append(order, items[i].Kind)
		items = append(items[:i], items[i+1:]...)
	}
	require.Equal(t, []ItemKind{
		ItemSkillIndex, ItemDecisions, // the shares and the skill index, last rendered first
		ItemUserIntent, ItemInvariants, ItemAffordance, // tier 1, the reverse of tier1Admission
		ItemDropReport, // its floor was held before anything was admitted
	}, order)

	require.Equal(t, 0, evictIndex([]Item{{Kind: ItemInvariants}}))
}

// TestTier1Units_TakesTheOriginalAndTheNewestRestatement pins item 2's tier-1 slice. Criterion
// change (w15-rehydrate, D49): this row was TestTier1Units_TakesOnlyTheVerbatimOriginal (ADR 0011
// §19). D49 admits the newest restatement — the correction in force — with the original, ahead of
// the share (F-C4-UAT06-3); the older deltas still take the share, which is §19's point.
func TestTier1Units_TakesTheOriginalAndTheNewestRestatement(t *testing.T) {
	b := built{units: []unit{
		{text: "> original\n"},
		{text: "Evolution:\n> newest\n", drop: checkpoint.DropEntry{Kind: dropKindUserIntentEvolution, ID: "1"}},
		{text: "> older\n", drop: checkpoint.DropEntry{Kind: dropKindUserIntentEvolution, ID: "0"}},
	}}
	require.Equal(t, b.units[:2], tier1Units(ItemUserIntent, b),
		"the verbatim original and the newest restatement are tier 1; the older deltas take a share")
	require.Len(t, tier1Units(ItemInvariants, b), 3, "every other kind admits all of its units")
	require.Empty(t, tier1Units(ItemUserIntent, built{}))

	// No original unit to show (an L0 capture named as an overflow): the newest restatement alone.
	noOriginal := built{units: b.units[1:]}
	require.Equal(t, b.units[1:2], tier1Units(ItemUserIntent, noOriginal))
	// No evolution: the original alone.
	require.Equal(t, b.units[:1], tier1Units(ItemUserIntent, built{units: b.units[:1]}))
}
