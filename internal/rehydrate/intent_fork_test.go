package rehydrate

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
)

// F-UAT06-1 (Phase 4 live lane): after `claude --resume <id> --fork-session`, section 2's
// "Original user intent (verbatim from L0 capture — never summarized)" was the FORK's own first
// prompt, and the checkpoint's true original was overridden as an intent_mismatch. A fork continues
// its parent's task. These rows pin item 2 for a forked session: the parent's original, verified
// against the parent's own L0 capture and labelled with where it came from; the fork's first prompt
// an evolution entry; and the mismatch guard still Loud when the two copies really disagree.

// The UAT-06 conversation (uat/UAT-06/section2-in-order.txt), verbatim.
const (
	uat06Original = "We are building a rate limiter for the Kite API gateway. Requirement: allow 100 requests " +
		"per minute per client. First, run the Bash command `cat config/limits.txt` and tell me how many " +
		"entries it lists. Do not repeat any values."
	uat06Correction60 = "Correction: the limit must be 60 requests per minute per client, not 100. The 100 figure " +
		"is superseded."
	uat06ForkFirst = "We are continuing in a forked session. In one sentence: what is the current per-client " +
		"rate limit requirement?"
)

const (
	uat06Parent = core.SessionID("c8f504cc-2864-4daf-8125-f9ee3851b944")
	uat06Fork   = core.SessionID("30a4a927-b4e7-416a-bfc8-1fc9a9ab930b")
)

// forkedCheckpoint is the fork's checkpoint as the fixed checkpointer writes it: the parent's
// original and correction, then the fork's own first prompt.
func forkedCheckpoint() checkpoint.Checkpoint {
	cp := ckEmpty()
	cp.Session = uat06Fork
	cp.Seq = 4
	cp.UserIntent.Original = uat06Original
	cp.UserIntent.Evolution = []string{uat06Correction60, uat06ForkFirst}
	return cp
}

// forkLineage is the fork's lineage record: it continues checkpoint 0002, sealed by the parent.
func forkLineage() *checkpoint.Lineage {
	return &checkpoint.Lineage{
		Version: 1, Session: uat06Fork, Source: checkpoint.LineageFork,
		ParentSeq: 2, ParentSession: uat06Parent, OriginSession: uat06Parent,
	}
}

// uat06Store holds both sessions' L0 captures.
func uat06Store(parentFirst string) *fakeStore {
	return newFakeStore().
		withPrompt(firstPromptID(uat06Parent), uat06Parent, 0, parentFirst).
		withPrompt("prompt_"+core.ToolUseID(uat06Parent)+"_2", uat06Parent, 2, uat06Correction60).
		withPrompt(firstPromptID(uat06Fork), uat06Fork, 0, uat06ForkFirst)
}

func TestUserIntent_ForkKeepsTheParentsOriginal(t *testing.T) {
	cp := forkedCheckpoint()
	r := requestFor(t, cp, generousTestBudget)
	r.Lineage = forkLineage()

	log := &spyLogger{}
	d := depsWith(log)
	d.Store = uat06Store(uat06Original)

	got := buildUserIntent(bg(), r, d)

	require.NotEmpty(t, got.units)
	require.True(t, isFixedUnit(got.units[0]), "the original is tier 1")
	require.Contains(t, got.units[0].text, quoteLines(uat06Original), "the parent's original, whole")
	require.NotContains(t, got.units[0].text, uat06ForkFirst, "the fork's first prompt is not the original")
	// Criterion change (w15-rehydrate, D50): section 2 renders the evolution above the original, so
	// the original unit now opens with originalRequestLabel; the provenance line follows it.
	require.True(t, strings.HasPrefix(got.units[0].text, originalRequestLabel+"\n(forked session: "),
		"the unit says where its original came from: %q", got.units[0].text)
	require.Contains(t, got.units[0].text, shortSession(uat06Parent))

	var evolution strings.Builder
	for _, u := range got.units[1:] {
		evolution.WriteString(u.text)
	}
	require.Contains(t, evolution.String(), quoteLines(uat06ForkFirst), "the fork's first prompt is an evolution entry")
	require.Less(t, strings.Index(evolution.String(), uat06ForkFirst), strings.Index(evolution.String(), uat06Correction60),
		"newest first: the fork's prompt above the parent's correction")

	for _, e := range got.drops {
		require.NotEqual(t, dropKindIntentMismatch, e.Kind, "no mismatch: the copies agree: %v", got.drops)
	}
	require.Zero(t, log.loud, "nothing disagreed")
	e, ok := dropFor(got.drops, "fork")
	require.True(t, ok, "the provenance is reported where dropped() can read it: %v", got.drops)
	require.Equal(t, dropKindUserIntentSource, e.Kind)
	require.Contains(t, e.Detail, string(uat06Parent))
	require.Contains(t, e.Detail, "expand(tool_use_id="+string(firstPromptID(uat06Parent))+")")
}

// TestUserIntent_ForkedParentMismatchIsStillLoud: the fork's inheritance is checked against the
// PARENT's L0 capture, and a real disagreement is the same Loud, L0-wins mismatch as ever.
func TestUserIntent_ForkedParentMismatchIsStillLoud(t *testing.T) {
	cp := forkedCheckpoint()
	cp.UserIntent.Original = "A summarized restatement of the rate limiter task."
	r := requestFor(t, cp, generousTestBudget)
	r.Lineage = forkLineage()

	log := &spyLogger{}
	d := depsWith(log)
	d.Store = uat06Store(uat06Original)

	got := buildUserIntent(bg(), r, d)

	require.Contains(t, got.units[0].text, quoteLines(uat06Original), "L0 wins")
	require.NotContains(t, got.units[0].text, "summarized restatement")
	_, ok := dropFor(got.drops, string(uat06Fork))
	require.True(t, ok, "an intent_mismatch entry: %v", got.drops)
	require.Equal(t, 1, log.loud, "a regenerated original is Loud, fork or not")
}

// TestUserIntent_ForkWithUnknownParentSaysSo: a fork that started before any checkpoint existed has
// no recorded parent, so its own first prompt stands as its original — and item 2 says so rather
// than presenting it as the task the fork continued.
func TestUserIntent_ForkWithUnknownParentSaysSo(t *testing.T) {
	cp := forkedCheckpoint()
	cp.UserIntent.Original = uat06ForkFirst
	cp.UserIntent.Evolution = nil
	r := requestFor(t, cp, generousTestBudget)
	r.Lineage = &checkpoint.Lineage{Version: 1, Session: uat06Fork, Source: checkpoint.LineageFork}

	log := &spyLogger{}
	d := depsWith(log)
	d.Store = uat06Store(uat06Original)

	got := buildUserIntent(bg(), r, d)

	require.Contains(t, got.units[0].text, quoteLines(uat06ForkFirst))
	e, ok := dropFor(got.drops, "fork")
	require.True(t, ok, "%v", got.drops)
	require.Equal(t, dropKindUserIntentSource, e.Kind)
	require.Contains(t, e.Detail, "parent is unknown")
	require.Zero(t, log.loud)
}

// TestBuild_ForkBlockMatchesUAT06: the block UAT-06 captured after the fork's first compaction
// (block3-C-fork-compact.txt), with the fix: section 2 carries the parent's original, then the
// fork's prompt and the parent's correction, newest first, and section 7 names no mismatch.
func TestBuild_ForkBlockMatchesUAT06(t *testing.T) {
	cp := forkedCheckpoint()
	d := fullDeps(t, cp)
	d.Store = uat06Store(uat06Original)
	r := requestFor(t, cp, 0)
	r.Lineage = forkLineage()

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)

	section2 := sectionBody(res.Text, sectionHeading(ItemUserIntent))
	require.Contains(t, section2, quoteLines(uat06Original))
	iOrig := strings.Index(section2, uat06Original)
	iFork := strings.Index(section2, uat06ForkFirst)
	i60 := strings.Index(section2, uat06Correction60)
	// Criterion change (w15-rehydrate, D50, UAT-05 read literally): the order was original, fork's
	// prompt, older correction. A correction now renders above what it supersedes: the evolution
	// newest first — the fork's prompt, then the older correction — and the original last.
	require.True(t, iFork >= 0 && i60 > iFork && iOrig > i60,
		"the fork's prompt, then the older correction, then the original: %q", section2)
	require.NotContains(t, res.Text, "intent_mismatch")
	require.False(t, res.Degraded, "a fork is not a degradation")
}

// ── review round (wave 13 fix seat) ─────────────────────────────────────────────────────────────

// evolutionText concatenates item 2's evolution units, the original excluded.
func evolutionText(b built) string {
	var out strings.Builder
	for _, u := range b.units[1:] {
		out.WriteString(u.text)
	}
	return out.String()
}

// TestUserIntent_ForkFirstPromptTheCheckpointLeftOutIsNotShownAsNewest: the fork has said more than
// the checkpoint's evolution bounds hold, so its first prompt was left out as one of the OLDEST
// restatements and the checkpoint says so. Re-adding it from L0 as the NEWEST delta would put a
// superseded statement at the top of "Evolution (most recent first)".
func TestUserIntent_ForkFirstPromptTheCheckpointLeftOutIsNotShownAsNewest(t *testing.T) {
	const newest = "Correction: the limit must now be 75 requests per minute per client."
	cp := forkedCheckpoint()
	cp.UserIntent.Evolution = []string{newest}
	cp.Dropped = append(cp.Dropped, checkpoint.DropEntry{
		Kind: dropKindUserIntentEvolution, ID: "elided",
		Detail: "2 earlier restatements were left out to hold the evolution bounds",
	})
	r := requestFor(t, cp, generousTestBudget)
	r.Lineage = forkLineage()
	d := depsWith(&spyLogger{})
	d.Store = uat06Store(uat06Original)

	got := buildUserIntent(bg(), r, d)

	evo := evolutionText(got)
	require.Contains(t, evo, quoteLines(newest))
	require.NotContains(t, evo, uat06ForkFirst,
		"a restatement the checkpoint left out is named by its drop entry, not re-added on top")
}

// TestUserIntent_ForkFirstPromptEqualToTheOriginalIsNotRepeated: the fork's first prompt restates
// the parent's original word for word, so the checkpointer listed it once, as the original.
func TestUserIntent_ForkFirstPromptEqualToTheOriginalIsNotRepeated(t *testing.T) {
	cp := forkedCheckpoint()
	cp.UserIntent.Evolution = []string{uat06Correction60}
	r := requestFor(t, cp, generousTestBudget)
	r.Lineage = forkLineage()
	d := depsWith(&spyLogger{})
	d.Store = newFakeStore().
		withPrompt(firstPromptID(uat06Parent), uat06Parent, 0, uat06Original).
		withPrompt(firstPromptID(uat06Fork), uat06Fork, 0, uat06Original)

	got := buildUserIntent(bg(), r, d)

	require.Contains(t, got.units[0].text, quoteLines(uat06Original))
	require.NotContains(t, evolutionText(got), uat06Original, "the original is shown once")
}

// TestUserIntent_ForkOfAParentWithNoCheckpointNamesNone: the parent never compacted, so the lineage
// names no checkpoint, and the provenance entry must not invent one ("checkpoint 0000").
func TestUserIntent_ForkOfAParentWithNoCheckpointNamesNone(t *testing.T) {
	cp := forkedCheckpoint()
	r := requestFor(t, cp, generousTestBudget)
	l := forkLineage()
	l.ParentSeq = 0
	r.Lineage = l
	d := depsWith(&spyLogger{})
	d.Store = uat06Store(uat06Original)

	got := buildUserIntent(bg(), r, d)

	e, ok := dropFor(got.drops, "fork")
	require.True(t, ok, "%v", got.drops)
	require.Contains(t, e.Detail, string(uat06Parent))
	require.NotContains(t, e.Detail, "checkpoint 0000")
}

// TestUserIntent_ForkFirstPromptMissingFromTheCheckpointIsAddedAsNewest: the one absence the shim is
// for. The fork's first prompt was not yet readable when its checkpoint was sealed, the checkpoint
// left nothing out, and L0 has it now: it is shown as the newest delta, pointing at its own capture.
func TestUserIntent_ForkFirstPromptMissingFromTheCheckpointIsAddedAsNewest(t *testing.T) {
	cp := forkedCheckpoint()
	cp.UserIntent.Evolution = []string{uat06Correction60}
	r := requestFor(t, cp, generousTestBudget)
	r.Lineage = forkLineage()
	d := depsWith(&spyLogger{})
	d.Store = uat06Store(uat06Original)

	got := buildUserIntent(bg(), r, d)

	evo := evolutionText(got)
	require.Contains(t, evo, quoteLines(uat06ForkFirst))
	require.Less(t, strings.Index(evo, uat06ForkFirst), strings.Index(evo, uat06Correction60), "newest first")
	require.Equal(t, string(firstPromptID(uat06Fork)), got.units[1].drop.ID,
		"its drop entry points at its own capture")
}
