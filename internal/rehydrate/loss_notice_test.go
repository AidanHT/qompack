package rehydrate

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
)

// A degraded compaction that dropped material is never silent (coordinator decision D59 under D33;
// UAT-05 F-C7-UAT05-1, plans/sdd/V6-closeout/live/rerun-c7/UAT-05/). At runtime.rehydrate.minTokens
// = maxTokens = 150 the candidate 7 compact injection was ONLY the contract-probe line, while the
// state read degraded true with 15 drops, tier-1 first: tier 1 ended at the refused retrieval line,
// and "a payload whose only admitted section would be item 7 is no payload" (ADR 0011 §21) emptied
// what was left. When the budget admits no section but material was dropped, the payload is a
// minimal notice in section 7, counted against the budget like any section: how many items were
// dropped, that dropped() lists them, and the restore pointer for a tier-1 original when one exists.

// requireLossNotice asserts res is the loss notice and nothing else: one section 7 whose count is
// every entry dropped() would list, inside the budget and the host ceiling.
func requireLossNotice(t *testing.T, res Result, budget core.Tokens, sess core.SessionID) string {
	t.Helper()
	require.True(t, res.Degraded)
	require.NotEmpty(t, res.Text, "a compaction that dropped material injects a notice, never nothing")
	body, _, ok := Unwrap(res.Text)
	require.True(t, ok, "the notice is injection-tagged like any payload:\n%s", res.Text)
	require.Len(t, res.Items, 1, "the notice is the only section")
	require.Equal(t, ItemDropReport, res.Items[0].Kind)
	require.Equal(t, res.Tokens, res.Items[0].Tokens, "Result.Tokens is the sum over Items")
	require.LessOrEqual(t, int(res.Tokens), int(budget), "the notice is counted against the budget")
	requireInsideTheHostCeiling(t, res, sess)
	section7 := sectionBody(res.Text, sectionHeading(ItemDropReport))
	require.NotEmpty(t, section7, "the notice is section 7:\n%s", res.Text)
	require.Contains(t, section7, itoa(len(res.Dropped))+" items",
		"the notice counts every entry dropped() lists: %v", res.Dropped)
	require.Contains(t, section7, "call dropped()")
	for _, k := range renderOrder {
		if k != ItemDropReport {
			require.NotContains(t, body, sectionHeading(k), "nothing but the notice is admitted")
		}
	}
	return section7
}

func TestBuild_ATinyBudgetThatDroppedMaterialIsNeverSilent(t *testing.T) {
	cp := ckUAT05()
	const liveBudget = core.Tokens(150)

	res, err := Build(context.Background(), requestFor(t, cp, liveBudget), uat05Deps(t, cp))
	require.NoError(t, err)
	_, refused := dropForKind(res.Dropped, ItemAffordance.String(), "tier1")
	require.True(t, refused, "fixture sanity: at 150 tokens the retrieval line does not fit: %v", res.Dropped)

	section7 := requireLossNotice(t, res, liveBudget, cp.Session)
	require.Contains(t, section7, "/qompack:dropped", "the user's route to the same list is named")
	orig, ok := dropForKind(res.Dropped, ItemUserIntent.String(), "tier1")
	require.True(t, ok, "fixture sanity: the original is a tier-1 overflow: %v", res.Dropped)
	_, pointer, ok := strings.Cut(orig.Detail, "; restore: ")
	require.True(t, ok, "%v", orig)
	require.Contains(t, section7, pointer, "the original's restore pointer is in the notice")

	// D49's other rules are unchanged: the tier-1 prefix still ends at the refused retrieval line.
	require.Equal(t, []bool{false, false, false}, tier1Present(res.Text))
}

// TestBuild_TheLossNoticeShrinksToItsSmallestForm sweeps every budget below the one the retrieval
// line needs. Each one injects the notice whole, its smallest form "N items dropped; call
// dropped()", or — only when even that cannot fit — nothing, recorded as the overflow/payload entry
// "nothing was injected" the wrapper-alone case already writes. Once a budget holds a notice, every
// larger one does.
func TestBuild_TheLossNoticeShrinksToItsSmallestForm(t *testing.T) {
	cp := ckUAT05()
	var sawFull, sawSmallest, sawNothing bool
	noticed := false
	for budget := core.Tokens(1); ; budget++ {
		res, err := Build(context.Background(), requestFor(t, cp, budget), uat05Deps(t, cp))
		require.NoError(t, err)
		if strings.Contains(res.Text, AffordanceNotice()) {
			break // the retrieval line fits: an ordinary payload from here on
		}
		require.LessOrEqual(t, int(res.Tokens), int(budget))
		if res.Text == "" {
			require.False(t, noticed, "budget %d injects nothing although a smaller one held the notice", int(budget))
			e, ok := dropForKind(res.Dropped, dropKindOverflow, "payload")
			require.True(t, ok, "budget %d injected nothing and did not say so: %v", int(budget), res.Dropped)
			require.Contains(t, e.Detail, "nothing was injected")
			sawNothing = true
			continue
		}
		noticed = true
		section7 := requireLossNotice(t, res, budget, cp.Session)
		if strings.Contains(section7, "/qompack:dropped") {
			sawFull = true
		} else {
			require.Contains(t, section7, "- "+itoa(len(res.Dropped))+" items dropped; call dropped()\n")
			sawSmallest = true
		}
	}
	require.True(t, sawNothing && sawSmallest && sawFull,
		"fixture sanity: the sweep crosses every form (nothing %v, smallest %v, full %v)",
		sawNothing, sawSmallest, sawFull)
}

// TestLossNotice_NothingDroppedStaysEmpty keeps ADR 0011's "no payload" clause where it still applies:
// a build that admitted no section and dropped nothing has nothing to say, so it says nothing. Build
// always names a refused retrieval line, so this is the notice's own rule, asserted directly.
func TestLossNotice_NothingDroppedStaysEmpty(t *testing.T) {
	cp := ckUAT05()
	r := requestFor(t, cp, core.Tokens(150))
	limit := cost{tok: r.Budget, chars: PayloadCeilingChars}

	res, stats := lossNotice(r, uat05Deps(t, cp), nil, limit)
	require.Empty(t, res.Text)
	require.Empty(t, res.Items)
	require.Empty(t, stats)
	require.Zero(t, int(res.Tokens))

	res, _ = lossNotice(r, uat05Deps(t, cp), []checkpoint.DropEntry{{Kind: "decision", ID: "dec_1"}}, limit)
	require.Contains(t, res.Text, "- 1 item did not fit", "one entry reads in the singular")
}
