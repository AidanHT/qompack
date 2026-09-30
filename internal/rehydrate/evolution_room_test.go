package rehydrate

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
)

// F-C4-UAT06-1 and F-C4-UAT06-3 (owner decision D49). user_intent.evolution holds every later
// prompt, and item 2's evolution took only its fixed tenth of the shares. In the candidate 4 fork's
// first block (plans/sdd/V6-closeout/live/rerun-c4/UAT-06/C-block2-SessionStart-compact.txt) five
// ordinary prompts filled that tenth and the only correction in force — "60 per minute", the oldest
// entry — was named "user_intent_evolution 0 — did not fit" while the block used 2,652 of its 9,400
// characters: the block's only statement of the limit was the superseded original. D49: the newest
// restatement is admitted with the original, ahead of the share; the share stays for the older
// entries; and room the payload leaves unused goes to evolution, newest first, before the payload
// is final. D5 still holds: every entry is emitted whole or named.

// The conversation reuses intent_fork_test.go's UAT-06 constants: uat06Original and
// uat06Correction60.

// ckUAT06 is the fork's checkpoint shape: the original, the correction as the OLDEST evolution
// entry, and the ordinary prompts that followed it (oldest first, as the checkpointer stores them).
func ckUAT06() checkpoint.Checkpoint {
	var cp checkpoint.Checkpoint
	cp.Version = checkpoint.SchemaVersion
	cp.Session = uat06Fork
	cp.Seq = core.CheckpointSeq(4)
	cp.Created = "2026-09-30T02:10:00.000Z"
	cp.UserIntent.Original = uat06Original
	cp.UserIntent.Evolution = []string{
		uat06Correction60,
		`Call mcp__plugin_qompack_qompack__record_eliminated with target "per-client rate limit", ` +
			`approach "100 requests per minute" and reason "superseded by the user's correction: 60 per ` +
			`minute". Report the raw result in one line.`,
		"Use the Read tool to read src/limiter.py and summarize it in one sentence.",
		"/qompack:why dec_991dbff588ec",
		"Use the Read tool to read docs/notes.md and summarize it in one sentence.",
		"What is the current per-client rate limit requirement, and what was the original one? One sentence.",
		"Use the Read tool to read docs/design.md and summarize it in one sentence.",
	}
	cp.CurrentWork.Goal = cp.UserIntent.Evolution[len(cp.UserIntent.Evolution)-1]
	return cp
}

// requireEveryEvolutionEntryWholeOrNamed is D5 for item 2's evolution: each entry is in the payload
// whole, or named by its index as a user_intent_evolution drop — never both, never neither.
func requireEveryEvolutionEntryWholeOrNamed(t *testing.T, cp checkpoint.Checkpoint, res Result) {
	t.Helper()
	for i, ev := range cp.UserIntent.Evolution {
		shown := strings.Contains(res.Text, quoteLines(strings.TrimSpace(ev)))
		_, named := dropForKind(res.Dropped, dropKindUserIntentEvolution, itoa(i))
		require.True(t, shown != named, "evolution[%d] shown=%v named=%v:\n%s", i, shown, named, res.Text)
	}
}

// TestBuild_UnusedRoomGoesToEvolutionNewestFirst is F-C4-UAT06-1 at the daemon's own budget: the
// payload has thousands of characters to spare, so no evolution entry is left out — the correction
// in force among them.
func TestBuild_UnusedRoomGoesToEvolutionNewestFirst(t *testing.T) {
	cp := ckUAT06()
	res, err := Build(context.Background(), requestFor(t, cp, maxBudget()), fullDeps(t, cp))
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)

	require.Contains(t, res.Text, quoteLines(uat06Correction60), "the correction in force was left out:\n%s", res.Text)
	for _, e := range res.Dropped {
		require.NotEqual(t, dropKindUserIntentEvolution, e.Kind,
			"an evolution entry was dropped from a payload with room for it: %+v\n%s", e, res.Text)
	}
	requireEveryEvolutionEntryWholeOrNamed(t, cp, res)

	// Newest first: the entries render in the reverse of their stored order.
	last := -1
	for i := len(cp.UserIntent.Evolution) - 1; i >= 0; i-- {
		at := strings.Index(res.Text, quoteLines(cp.UserIntent.Evolution[i]))
		require.Greater(t, at, last, "evolution[%d] is out of newest-first order", i)
		last = at
	}
}

// TestBuild_NewestRestatementIsAdmittedAheadOfTheShare is F-C4-UAT06-3's other half: the newest
// restatement is admitted with the original, not out of item 2's tenth, so a restatement longer
// than that tenth still arrives while the discretionary sections are cut.
func TestBuild_NewestRestatementIsAdmittedAheadOfTheShare(t *testing.T) {
	cp := ckFull(t)
	newest := "Correction: the refresh endpoint must return 409, not 500, when the session was rotated " +
		"concurrently; " + strings.TrimSpace(strings.Repeat("keep the retry budget unchanged and log the rotation id; ", 8))
	cp.UserIntent.Evolution = append(cp.UserIntent.Evolution, newest)
	const budget = core.Tokens(700)

	res, err := Build(context.Background(), requestFor(t, cp, budget), fullDeps(t, cp))
	require.NoError(t, err)
	require.LessOrEqual(t, int(res.Tokens), int(budget))

	var cut bool
	for _, e := range res.Dropped {
		cut = cut || (e.Kind != dropKindUserIntentEvolution && !Overflowed([]checkpoint.DropEntry{e}) &&
			e.Kind != dropKindUserIntentSource)
	}
	require.True(t, cut, "fixture sanity: the budget cuts a discretionary section: %v", res.Dropped)
	require.Contains(t, res.Text, quoteLines(newest), "the newest restatement did not arrive:\n%s", res.Text)
	requireEveryEvolutionEntryWholeOrNamed(t, cp, res)
}

// TestBuild_OlderDeltasNeverShowWithoutTheNewest: a newest restatement too large for any payload is
// named and passed over (fillTier1), and the older deltas go with it rather than render under "most
// recent first" as though one of them were the current authority. Found by
// TestBuild_NeverExceedsTheHostCeiling at a high check count once item 2 rendered its evolution first.
func TestBuild_OlderDeltasNeverShowWithoutTheNewest(t *testing.T) {
	cp := ckUAT06()
	huge := "Correction: " + strings.Repeat("the limit is now set per tenant, not per client; ", 250)
	cp.UserIntent.Evolution = append(cp.UserIntent.Evolution, huge)

	res, err := Build(context.Background(), requestFor(t, cp, maxBudget()), fullDeps(t, cp))
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)

	require.NotContains(t, res.Text, "per tenant", "whole or absent")
	for i, ev := range cp.UserIntent.Evolution[:len(cp.UserIntent.Evolution)-1] {
		require.NotContains(t, res.Text, quoteLines(ev), "older delta %d renders while the newest is out", i)
	}
	require.Contains(t, res.Text, quoteLines(uat06Original), "the original still arrives")
	requireEveryEvolutionEntryWholeOrNamed(t, cp, res)
	require.Contains(t, res.Text, "\n"+sectionHeading(ItemCurrentWork), "the shares still fill")
}
