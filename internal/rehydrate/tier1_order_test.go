package rehydrate

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/tokens"
)

// F-C4-UAT05-3 (owner decision D49). At the 150-token budget of UAT-05 step 5 the candidate 4 payload
// carried the pinned invariant and section 7's "call dropped()", and named section 8 — the retrieval
// line — as a tier-1 overflow. ADR 0011 §21.2 admits the retrieval line FIRST, because every overflow
// pointer depends on it, and §21.1 admits tier 1 record by record "until the first one that does not
// fit (the §7 prefix rule)". Build applied that prefix per ITEM, so once the retrieval line did not
// fit, the next item's smaller record got in: the invariant at 150 tokens, the retrieval line and no
// invariant at 160. What survives stopped following the admission order, and stopped growing with
// the budget (PropBuild_MonotoneInBudget's claim, broken across tier 1).

// The candidate 4 UAT-05 run-1 records (plans/sdd/V6-closeout/live/rerun-c4/UAT-05/
// run1-checkpoint-0001.json), verbatim where they are tier 1.
const (
	uat05Session  = core.SessionID("042dfca0-5f24-464a-95c1-643603b4e13d")
	uat05PinID    = "inv_8382970dffd4"
	uat05Pin      = "Never write to prod.db from the export code."
	uat05Original = "We are adding a CSV export to the reports module. Requirement: the export must use a " +
		"semicolon (;) as the field delimiter. Use the Read tool to read reports.py and tell me in one " +
		"sentence where the export function should go."
	uat05Correction = "Correction: the export delimiter must be a TAB character, not a semicolon. The " +
		"semicolon requirement is superseded. Acknowledge in one sentence."
	uat05Newest = "Run the Bash command `cat data/meta.txt` and tell me only that you read it; do not " +
		"repeat its contents."
)

// ckUAT05 is that checkpoint, rebuilt by field assignment (Rule W-2): one pin, the verbatim
// original, four later prompts (the TAB correction second), one elimination and its decision,
// current work, and five pointers.
func ckUAT05() checkpoint.Checkpoint {
	var cp checkpoint.Checkpoint
	cp.Version = checkpoint.SchemaVersion
	cp.Session = uat05Session
	cp.Seq = core.CheckpointSeq(1)
	cp.Created = "2026-09-30T01:40:34.115Z"
	cp.Invariants = []checkpoint.Invariant{{
		ID: uat05PinID, Text: uat05Pin, Source: "user", Pinned: core.UnixMilli(1790732418791),
	}}
	cp.UserIntent.Original = uat05Original
	cp.UserIntent.Evolution = []string{
		"/qompack:pin " + uat05Pin,
		uat05Correction,
		`Call mcp__plugin_qompack_qompack__record_eliminated with target "export delimiter", approach ` +
			`"semicolon delimiter" and reason "superseded by the user's correction: TAB is required". ` +
			`Report the raw result in one line.`,
		uat05Newest,
	}
	cp.Eliminated = []negknow.Record{{
		ID: "elim_cb75b399ddfb", Session: cp.Session, TS: core.UnixMilli(1790732427280),
		Target: "export delimiter", Approach: "semicolon delimiter",
		Reason: "superseded by the user's correction: TAB is required",
		Scope:  negknow.ScopeSession, Status: negknow.StatusActive,
	}}
	cp.Decisions = []checkpoint.Decision{{
		ID:   core.DecisionID("dec_dde1ccbde407"),
		What: `rejected "semicolon delimiter" for export delimiter`,
		Why:  "superseded by the user's correction: TAB is required", Turn: 7,
		AlternativesRejected: []string{"semicolon delimiter"},
	}}
	cp.CurrentWork.Goal = uat05Newest
	reports := core.Hash(sha256.Sum256([]byte("reports.py")))
	cp.Pointers.Files = []checkpoint.FilePointer{{Path: "reports.py", Hash: reports, Why: "referenced"}}
	cp.Pointers.Tools = []checkpoint.ToolPointer{
		{ToolUseID: "toolu_01Bx26SG43J2bhPJcfffZSzY", Hash: core.Hash(sha256.Sum256([]byte("meta"))), Summary: "cat data/meta.txt"},
		{
			ToolUseID: "toolu_01VotHCeCRiaZSXpakdft9Xi", Hash: core.Hash(sha256.Sum256([]byte("record"))),
			Summary: `{"approach":"semicolon delimiter","reason":"superseded by the user's correction: TAB is required"}`,
		},
		{
			ToolUseID: "toolu_01LHaPA2VFZiQJEk9PY6wK2L", Hash: core.Hash(sha256.Sum256([]byte("select"))),
			Summary: `{"max_results":1,"query":"select:mcp__plugin_qompack_qompack__record_eliminated"}`,
		},
		{ToolUseID: "toolu_01QL9VxKUUdCU1MMGY2bBtVr", Hash: reports, Summary: "reports.py"},
	}
	return cp
}

// uat05Deps is fullDeps for ckUAT05 with its elimination served at the scope the live ledger held
// it at, and the estimator the live daemon priced the payload with.
func uat05Deps(t *testing.T, cp checkpoint.Checkpoint) Deps {
	t.Helper()
	d := fullDeps(t, cp)
	d.Ledger = newFakeLedger().withActive(negknow.ScopeSession, cp.Eliminated...)
	d.Tokens = tokens.New(config.Defaults(), "")
	return d
}

// tier1Present reports which of ckUAT05's tier-1 records text carries, in ADR 0011's tier-1
// admission order: item 8's retrieval line, the pinned invariant and the verbatim original.
func tier1Present(text string) []bool {
	return []bool{
		strings.Contains(text, AffordanceNotice()),
		strings.Contains(text, "- ["+uat05PinID+"] "+uat05Pin),
		strings.Contains(text, quoteLines(uat05Original)),
	}
}

// shareFilledSections are the sections filled from a share or a reserve rather than in tier 1.
var shareFilledSections = []ItemKind{
	ItemEliminations, ItemDecisions, ItemCurrentWork, ItemPointers, ItemRestoredInstructions, ItemSkillIndex,
}

// TestBuild_Tier1FollowsTheAdmissionOrderAtTinyBudgets sweeps every budget from nothing fitting to all
// of tier 1 fitting, under the baseline estimator, the shipped estimator the live daemon priced UAT-05
// with, and an estimator that forces the hard-cap eviction loop to fire. At every budget:
//
//   - the tier-1 records present are a PREFIX of the admission order: no invariant without the
//     retrieval line, no original without the invariant, whether the fill left a record out or the
//     hard-cap loop evicted it;
//   - while the retrieval line is out, nothing filled from a share is present: every share-filled
//     section is records plus a call the model was never told exists (F-C4-UAT05-3);
//   - while tier 1 is incomplete, no evolution entry is present: a smaller item-2 record never takes
//     the place of a tier-1 record the budget could not hold (§7). The shares themselves stay open
//     once the retrieval line is in (criterion change, w15-rehydrate review: closing them on any
//     tier-1 refusal emptied sections 3-6 of a payload with room — see
//     TestBuild_ARefusedNewestRestatementLeavesTheSharesTheirRoom);
//   - the tier-1 prefix never shrinks as the budget grows.
func TestBuild_Tier1FollowsTheAdmissionOrderAtTinyBudgets(t *testing.T) {
	cp := ckUAT05()
	sweeps := []struct {
		name     string
		est      tokens.Estimator
		max, per core.Tokens
	}{
		{"baseline", fakeEstimator{}, 700, 1},
		{"shipped", tokens.New(config.Defaults(), ""), 700, 1},
		// Every newline costs perSeparatorCharge tokens, so every record costs at least that much and
		// the sweep can be wider and coarser; the assembled payload overruns what the fill priced, so
		// the hard-cap loop is what removes records here.
		{"separator-charging", separatorChargingEstimator{}, 16 * perSeparatorCharge * 4, 5},
	}
	for _, sw := range sweeps {
		t.Run(sw.name, func(t *testing.T) {
			prev := 0
			for budget := sw.per; budget <= sw.max; budget += sw.per {
				d := uat05Deps(t, cp)
				d.Tokens = sw.est
				res, err := Build(context.Background(), requestFor(t, cp, budget), d)
				require.NoError(t, err)

				present := tier1Present(res.Text)
				n := 0
				for n < len(present) && present[n] {
					n++
				}
				for i := n; i < len(present); i++ {
					require.False(t, present[i],
						"budget %d: tier-1 record %d is present although record %d before it in the "+
							"admission order is not:\n%s", int(budget), i, n, res.Text)
				}
				if n == 0 {
					for _, k := range shareFilledSections {
						require.NotContains(t, res.Text, "\n"+sectionHeading(k),
							"budget %d: section %s was admitted without the retrieval line:\n%s",
							int(budget), k, res.Text)
					}
				}
				if n < len(present) {
					require.NotContains(t, res.Text, "Evolution (most recent first):",
						"budget %d: an evolution entry while tier 1 is incomplete:\n%s", int(budget), res.Text)
				}
				require.GreaterOrEqual(t, n, prev,
					"budget %d holds a shorter tier-1 prefix (%d) than budget %d did (%d)",
					int(budget), n, int(budget-sw.per), prev)
				prev = n
			}
			require.Len(t, tier1Present(""), prev,
				"fixture sanity: the sweep reaches a budget that holds all of tier 1")
		})
	}
}

// TestBuild_TinyBudgetNeverKeepsTheInvariantWithoutTheRetrievalLine is the live observation itself:
// the shipped estimator at the 150-token budget of UAT-05 step 5, where the payload carried the pin
// and "call dropped()" with no section 8 to say what dropped() is.
func TestBuild_TinyBudgetNeverKeepsTheInvariantWithoutTheRetrievalLine(t *testing.T) {
	cp := ckUAT05()
	const liveBudget = core.Tokens(150)

	res, err := Build(context.Background(), requestFor(t, cp, liveBudget), uat05Deps(t, cp))
	require.NoError(t, err)
	require.True(t, res.Degraded)
	require.LessOrEqual(t, int(res.Tokens), int(liveBudget))

	var affordanceOut bool
	for _, e := range res.Dropped {
		affordanceOut = affordanceOut || e.Kind == ItemAffordance.String()
	}
	require.True(t, affordanceOut, "fixture sanity: at 150 tokens the retrieval line does not fit: %v", res.Dropped)
	require.NotContains(t, res.Text, "- ["+uat05PinID+"] ",
		"the retrieval line was left out, so every tier-1 record after it in the admission order is too")

	// Every tier-1 record the budget could not hold is its own named overflow, whatever the payload.
	for _, k := range []ItemKind{ItemAffordance, ItemInvariants, ItemUserIntent} {
		e, ok := dropForKind(res.Dropped, k.String(), "tier1")
		require.True(t, ok, "%s is not named as a tier-1 overflow: %v", k, res.Dropped)
		require.True(t, strings.HasPrefix(e.Detail, "OVERFLOW: "), "%v", e)
	}
}

// dropForKind returns the first entry of drops with this kind and id.
func dropForKind(drops []checkpoint.DropEntry, kind, id string) (checkpoint.DropEntry, bool) {
	for _, e := range drops {
		if e.Kind == kind && e.ID == id {
			return e, true
		}
	}
	return checkpoint.DropEntry{}, false
}

// TestBuild_AnUnrepresentableTier1RecordDoesNotCloseTier1 keeps the prefix rule from turning one
// record too large for ANY payload into a payload of nothing: a record that could not fit even an
// otherwise empty payload is named and passed over, exactly as an L0 capture past intentReadLimit is,
// and the tier-1 records after it and the shares still arrive. Only a record the BUDGET could not
// hold ends tier 1.
func TestBuild_AnUnrepresentableTier1RecordDoesNotCloseTier1(t *testing.T) {
	cp := ckUAT05()
	huge := checkpoint.Invariant{
		ID: "inv_000000000001", Source: "user",
		Text: strings.Repeat("the export must never write outside the reports directory; ", 200),
	}
	cp.Invariants = append([]checkpoint.Invariant{huge}, cp.Invariants...)

	res, err := Build(context.Background(), requestFor(t, cp, maxBudget()), uat05Deps(t, cp))
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)
	require.NotContains(t, res.Text, "outside the reports directory", "whole or absent")
	e, ok := dropForKind(res.Dropped, ItemInvariants.String(), "tier1")
	require.True(t, ok, "%v", res.Dropped)
	require.Contains(t, e.Detail, huge.ID)

	for i, ok := range tier1Present(res.Text) {
		require.True(t, ok, "tier-1 record %d is missing although only an unrepresentable record was out:\n%s", i, res.Text)
	}
	require.Contains(t, res.Text, "\n"+sectionHeading(ItemDecisions), "the shares still fill")
	requireSectionSevenAccountsForEveryDrop(t, res)
}

// TestBuild_ARefusedNewestRestatementLeavesTheSharesTheirRoom keeps tier 1's prefix rule inside tier
// 1 (w15-rehydrate review). Item 2's newest restatement is the LAST tier-1 record admitted, so when a
// long one cannot follow a long original it ends tier 1 with nothing left to refuse there — and
// closing the shares on it as well took sections 3-6 out of a payload with thousands of characters
// unused. The retrieval line was admitted, so every pointer the shares carry is actionable: a
// refused tier-1 record refuses the tier-1 records after it, not the discretionary items.
func TestBuild_ARefusedNewestRestatementLeavesTheSharesTheirRoom(t *testing.T) {
	cp := ckUAT05()
	cp.UserIntent.Original = strings.TrimSpace(strings.Repeat("The export must keep every column of the report in order. ", 76))
	long := strings.TrimSpace(strings.Repeat("Correction: write the export with a TAB between fields, never a semicolon. ", 60))
	cp.UserIntent.Evolution = append(append([]string(nil), cp.UserIntent.Evolution...), long)

	res, err := Build(context.Background(), requestFor(t, cp, maxBudget()), uat05Deps(t, cp))
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)

	require.Contains(t, res.Text, quoteLines(cp.UserIntent.Original), "fixture sanity: the original fits")
	require.NotContains(t, res.Text, "never a semicolon", "fixture sanity: the newest restatement does not")
	e, ok := dropForKind(res.Dropped, "user_intent_evolution", "4")
	require.True(t, ok, "the refused newest restatement is named with its restore pointer: %v", res.Dropped)
	require.Contains(t, e.Detail, "user_intent.evolution[4]", "%v", e)
	require.NotContains(t, res.Text, "Evolution (most recent first):",
		"older deltas never render without the newest restatement above them")

	for _, k := range []ItemKind{ItemEliminations, ItemDecisions, ItemCurrentWork, ItemPointers} {
		require.Contains(t, res.Text, "\n"+sectionHeading(k),
			"section %s was refused although the retrieval line is in and the payload has room:\n%s", k, res.Text)
	}
	requireSectionSevenAccountsForEveryDrop(t, res)
}
