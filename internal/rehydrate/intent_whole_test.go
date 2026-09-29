package rehydrate

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
)

// F-UAT04-1 (Phase 4 live lane, D5 whole records): a real 17,774-character first prompt reached the
// model as its first 8,192 bytes, cut mid-word under the heading that calls it verbatim, with no
// overflow entry, a state file saying truncated:false, and a spurious intent_mismatch logged Loud at
// every compaction. The checkpoint and the L0 capture both held all of it. These rows pin the
// whole-record rule on item 2's original: injected whole when it fits, named as an overflow with
// the call that restores it when it does not, never a prefix, and never a mismatch between two
// copies that agree.

// ospreyBrief renders a first prompt shaped like the UAT-04 brief: a header line and n numbered
// requirement lines of about a hundred characters each. It is deterministic, so a failure names
// the same bytes every run.
func ospreyBrief(n int) string {
	verbs := []string{"validate", "normalise", "hash", "audit"}
	trees := []string{"poplar", "walnut", "elm", "cedar", "larch", "quince", "hazel", "fir", "spruce", "nutmeg"}
	cadence := []string{"manual", "nightly", "hourly"}
	var b strings.Builder
	b.WriteString("Project brief for the Osprey ledger service. This first message is deliberately long: " +
		"it carries the full requirement appendix. Appendix:\n")
	for i := range n {
		fmt.Fprintf(&b, "R%03d: the ledger must %s the %s batch field before %s reconciliation, and log the %s outcome.\n",
			i+1, verbs[i%len(verbs)], trees[i%len(trees)], cadence[i%len(cadence)], trees[(i*7+3)%len(trees)])
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// briefOver returns the shortest ospreyBrief whose UTF-8 length exceeds bytes.
func briefOver(bytes int) string {
	for n := 1; ; n++ {
		if s := ospreyBrief(n); len(s) > bytes {
			return s
		}
	}
}

// uat04Brief is the UAT-04 first prompt's size class: 17,774 characters.
const uat04BriefChars = 17774

// TestUserIntent_FirstPromptPastEightKiBIsReadWhole: an L0 original longer than 8,192 bytes that
// agrees with the checkpoint copy is item 2's unit whole, and nothing reports a mismatch.
func TestUserIntent_FirstPromptPastEightKiBIsReadWhole(t *testing.T) {
	cp := ckMinimal()
	cp.UserIntent.Original = briefOver(8200)
	r := requestFor(t, cp, generousTestBudget)

	log := &spyLogger{}
	d := depsWith(log)
	d.Store = newFakeStore().withPrompt(firstPromptID(cp.Session), cp.Session, 0, cp.UserIntent.Original)

	got := buildUserIntent(bg(), r, d)

	require.NotEmpty(t, got.units)
	require.Equal(t, quoteLines(cp.UserIntent.Original), got.units[0].text,
		"the verbatim original is one whole record, never its first 8 KiB")
	require.Empty(t, got.drops, "an L0 capture that agrees with the checkpoint copy reports nothing")
	require.Zero(t, log.loud, "no intent_mismatch between two copies of the same prompt")
}

// TestBuild_FirstPromptPastEightKiBThatFitsIsInjectedWhole: the same original, through Build, reaches
// the payload whole — it fits the host ceiling, so it is not an overflow either.
func TestBuild_FirstPromptPastEightKiBThatFitsIsInjectedWhole(t *testing.T) {
	cp := ckMinimal()
	cp.UserIntent.Original = briefOver(8200)
	require.Less(t, hostChars(quoteLines(cp.UserIntent.Original)), PayloadCeilingChars-1000,
		"premise: this original fits the ceiling with room for the wrapper and item 8")
	d := fullDeps(t, cp)

	res, err := Build(context.Background(), requestFor(t, cp, 0), d)
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)
	require.Contains(t, res.Text, quoteLines(cp.UserIntent.Original), "the whole record is in the payload")
	for _, e := range res.Dropped {
		require.NotEqual(t, dropKindIntentMismatch, e.Kind, "no spurious intent_mismatch: %v", res.Dropped)
		require.NotEqual(t, "tier1", e.ID, "a record that fits is not an overflow: %v", res.Dropped)
	}
}

// TestBuild_FirstPromptLongerThanTheCeilingIsNamedNotCut is F-UAT04-1 itself: a 17,774-character
// first prompt cannot fit the 9,500-character ceiling, so no part of it is injected. It is named as
// an explicit overflow carrying expand(tool_use_id=prompt_<s>_0), the state rows say truncated and
// degraded, and nothing claims the two copies disagree.
func TestBuild_FirstPromptLongerThanTheCeilingIsNamedNotCut(t *testing.T) {
	cp := ckMinimal()
	cp.UserIntent.Original = briefOver(uat04BriefChars - 1)
	cp.UserIntent.Evolution = []string{"Correction: the ledger must audit the cedar field hourly, not nightly."}
	firstLine, _, _ := strings.Cut(cp.UserIntent.Original, "\n")
	d := fullDeps(t, cp)
	log := &spyLogger{}
	d.Log = log

	res, stats, err := BuildWithStats(context.Background(), requestFor(t, cp, 0), d)
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)

	require.NotContains(t, res.Text, firstLine, "no prefix of an oversized original reaches the model")
	require.NotContains(t, res.Text, "R001:", "no prefix of an oversized original reaches the model")
	require.Contains(t, res.Text, cp.UserIntent.Evolution[0], "what fits still arrives")

	e, ok := dropFor(res.Dropped, "tier1")
	require.True(t, ok, "the oversized original must be a named overflow: %v", res.Dropped)
	require.Equal(t, ItemUserIntent.String(), e.Kind)
	require.Contains(t, e.Detail, "the verbatim original user intent")
	require.Contains(t, e.Detail, "expand(tool_use_id="+string(firstPromptID(cp.Session))+")",
		"the pointer is the exact L0 record, which holds the whole prompt")
	require.True(t, res.Degraded, "an essential record that cannot fit degrades the rehydration")
	require.True(t, Overflowed(res.Dropped))

	for _, e := range res.Dropped {
		require.NotEqual(t, dropKindIntentMismatch, e.Kind, "no spurious intent_mismatch: %v", res.Dropped)
	}
	for _, m := range log.msgs {
		require.NotContains(t, m, "differs from the L0 capture", "no spurious Loud mismatch")
	}
	for _, s := range stats {
		if s.Kind == ItemUserIntent.String() {
			require.True(t, s.Truncated, "the state row for item 2 says it was truncated: %+v", s)
		}
	}
	requireSectionSevenAccountsForEveryDrop(t, res)
}

// TestUserIntent_OversizedL0CaptureIsNeverCut: an L0 capture longer than item 2 reads is not
// quoted in part — there is no original unit at all — and it is named as an overflow carrying its
// L0 pointer. The checkpoint copy is not substituted for it: L0 is the source of record whenever it
// answered, and it did.
func TestUserIntent_OversizedL0CaptureIsNeverCut(t *testing.T) {
	cp := ckMinimal()
	cp.UserIntent.Original = briefOver(int(intentReadLimit) + 1)
	r := requestFor(t, cp, generousTestBudget)

	log := &spyLogger{}
	d := depsWith(log)
	d.Store = newFakeStore().withPrompt(firstPromptID(cp.Session), cp.Session, 0, cp.UserIntent.Original)

	got := buildUserIntent(bg(), r, d)

	for _, u := range got.units {
		require.NotContains(t, u.text, "R001:", "no unit quotes part of the oversized capture")
	}
	require.Len(t, got.drops, 1, "%v", got.drops)
	require.Equal(t, checkpoint.DropEntry{
		Kind: ItemUserIntent.String(), ID: "tier1",
		Detail: "OVERFLOW: the verbatim original user intent did not fit the rehydration payload and is " +
			"emitted whole or not at all; restore: expand(tool_use_id=" + string(firstPromptID(cp.Session)) + ")",
	}, got.drops[0])
	require.Zero(t, log.loud, "a capture read in part is compared with nothing, so no mismatch is claimed")
}
