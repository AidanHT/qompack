package rehydrate

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/core"
)

// The end-to-end assertions the V4, V5 and V6 verification documents run by name. They sit in
// their own file because they are a contract with those documents rather than with any one
// builder: every other test here may be renamed freely, and these may not.
//
// Two of them also close a hole no unit test covers. TestBuild_SmallerThanStock turns §2.4's
// "50K + 25K" from a sentence in the architecture into a number a test fails on, and
// TestBuild_NoTranscriptRead_ClosesG75 turns G7.5 from a design intention into a checked property
// of the type signature plus the payload.

// hostStockRestoreTokens is the host's own eager restoration that §2.4 measures Qompack against:
// 50K of transcript plus 25K of skill bodies. It is not a Qompack tunable and no config key names
// it — it is the number the whole L5 design exists to beat.
const hostStockRestoreTokens = core.Tokens(50_000 + 25_000)

// TestBuild_ItemOrderInPayload is V4-SP11-06 and V6 1.11.1: the section headings appear at
// strictly increasing byte offsets in the normative §8.6 order, with 6a and 6b between 6 and 7.
//
// It asserts on the RENDERED TEXT rather than on Items, because the text is what the model reads
// and because a renderer that emitted items in order while writing sections out of order would
// pass every Item-level assertion in this package.
func TestBuild_ItemOrderInPayload(t *testing.T) {
	restore := skillBodyTokens
	skillBodyTokens = fakeBodyTokens(goldenSkillBodies())
	t.Cleanup(func() { skillBodyTokens = restore })

	cp := ckFull(t)
	got, err := Build(context.Background(), goldenRequest(cp, maxBudget()), goldenDeps(t, cp))
	require.NoError(t, err)

	headings := []string{
		"## 1. ", "## 2. ", "## 3. ", "## 4. ", "## 5. ", "## 6. ",
		"## 6a. ", "## 6b. ", "## 7. ", "## 8. ",
	}
	prev := -1
	for _, h := range headings {
		at := strings.Index(got.Text, h)
		require.GreaterOrEqual(t, at, 0, "the payload is missing section %q", h)
		require.Greater(t, at, prev,
			"section %q is out of §8.6 order; budget truncation drops from the tail, so the order "+
				"decides what a smaller budget keeps", h)
		prev = at
	}

	// The Items agree with the text: same kinds, same order, no kind emitted twice.
	var kinds []ItemKind
	for _, it := range got.Items {
		kinds = append(kinds, it.Kind)
	}
	require.Equal(t, renderOrder, kinds, "every kind is emitted exactly once, in renderOrder")
}

// TestBuild_RankCountsEmittedItemsFromZero is V4-SP11-06's third assertion.
//
// The V4 row spells it `TestBuild_RankIsOneBasedAmongEmitted`, which is stale: 00-ARCHITECTURE
// §5.15 and ADR 0011 §2 both fix Rank as the ZERO-based position among emitted items, and the
// inherited conformance case runItemOrderCase asserts that. The name here states what the code
// actually guarantees; the plan row is corrected rather than the contract.
func TestBuild_RankCountsEmittedItemsFromZero(t *testing.T) {
	// A checkpoint that omits several kinds, so "counts emitted items" is distinguishable from
	// "counts kinds": the ranks must still be 0..n-1 with no gap where the absent kinds would be.
	cp := ckMinimal()
	got, err := Build(context.Background(), goldenRequest(cp, maxBudget()), fullDeps(t, cp))
	require.NoError(t, err)
	require.Less(t, len(got.Items), len(renderOrder), "this fixture must omit some kinds")

	for i, it := range got.Items {
		require.Equal(t, i, it.Rank,
			"%s: Rank is the 0-based position among EMITTED items, so an omitted kind consumes none",
			it.Kind)
	}
}

// TestBuild_NeverExceedsMaxTokens is V4-SP11-14 and V6 1.11.10: the hard cap holds at every budget
// in and around the §8.6 band, and a caller may not raise it past runtime.rehydrate.maxTokens.
func TestBuild_NeverExceedsMaxTokens(t *testing.T) {
	restore := skillBodyTokens
	skillBodyTokens = fakeBodyTokens(goldenSkillBodies())
	t.Cleanup(func() { skillBodyTokens = restore })

	cp := ckFull(t)
	maxT := maxBudget()
	for _, budget := range []core.Tokens{1, 400, minBudget(), (minBudget() + maxT) / 2, maxT, maxT * 4, 0} {
		got, err := Build(context.Background(), goldenRequest(cp, budget), goldenDeps(t, cp))
		require.NoError(t, err)

		want := budget
		if budget <= 0 || budget > maxT {
			want = maxT // unset fills to the ceiling; an over-cap ask is clamped down to it
		}
		require.LessOrEqual(t, int(got.Tokens), int(want),
			"budget %d: Result.Tokens must never exceed the effective cap", int(budget))

		// The total is exactly the sum over Items, wrapper included — there is no overhead row.
		var sum core.Tokens
		for _, it := range got.Items {
			sum += it.Tokens
		}
		require.Equal(t, int(got.Tokens), int(sum), "budget %d", int(budget))
	}
}

// TestBuild_SmallerThanStock is V4-SP11-14's §2.4 assertion, and the one number that says whether
// this whole layer was worth building.
//
// The host restores 50K of transcript plus 25K of skill bodies after a compaction. L5 replaces
// that with pointers, verbatim non-reconstructible facts, restored instructions and an explicit
// drop report — and the claim that this is CHEAPER is asserted here rather than argued in a
// comment. The margin is large by construction: the cap is 12K.
func TestBuild_SmallerThanStock(t *testing.T) {
	restore := skillBodyTokens
	skillBodyTokens = fakeBodyTokens(goldenSkillBodies())
	t.Cleanup(func() { skillBodyTokens = restore })

	cp := ckFull(t)
	for _, budget := range []core.Tokens{minBudget(), maxBudget(), 0} {
		got, err := Build(context.Background(), goldenRequest(cp, budget), goldenDeps(t, cp))
		require.NoError(t, err)
		require.Less(t, int(got.Tokens), int(hostStockRestoreTokens),
			"budget %d: the injection must cost strictly less than the host's own eager "+
				"restoration, or L5 is a net loss (§2.4)", int(budget))
	}
}

// TestBuild_NonCompactSourceEmitsNothing is V4-SP11-16: only source=compact rehydrates.
//
// startup, resume and clear are handled elsewhere — a fresh session has nothing to restore and a
// clear is a deliberate discard — so a mis-wired caller must not be able to inject a rehydration
// into one. Build refuses rather than trusting its caller, because the cost of being wrong is a
// user who cleared their context getting it back.
func TestBuild_NonCompactSourceEmitsNothing(t *testing.T) {
	cp := ckFull(t)
	for _, source := range []string{"startup", "resume", "clear", "", "Compact", "compact "} {
		r := goldenRequest(cp, maxBudget())
		r.Source = source
		got, err := Build(context.Background(), r, fullDeps(t, cp))
		require.NoError(t, err, "source %q", source)
		require.Empty(t, got.Text, "source %q must inject nothing", source)
		require.Empty(t, got.Items, "source %q", source)
		require.Zero(t, int(got.Tokens), "source %q", source)
		require.Equal(t, cp.Seq, got.Seq, "the sequence is still reported for source %q", source)
	}
}

// TestBuild_ContextCancelled is V4-SP11-16: a cancelled context returns ctx.Err() and nothing else.
//
// Build has no side effects to unwind — it writes no file and mutates no dependency — so the whole
// assertion is that the error is returned and the Result is zero rather than half-built.
func TestBuild_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cp := ckFull(t)
	got, err := Build(ctx, goldenRequest(cp, maxBudget()), fullDeps(t, cp))
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, Result{}, got, "a cancelled build returns no partial payload")

	_, stats, err := BuildWithStats(ctx, goldenRequest(cp, maxBudget()), fullDeps(t, cp))
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, stats)
}

// TestBuild_NilDeps is V4-SP11-16: every collaborator absent yields a degraded payload, not an
// error and not a panic.
//
// This is §12.3 at its limit. The session still gets its pinned invariants, its verbatim intent
// and the line telling it retrieval exists; everything that needed a collaborator is named in the
// drop report; and Degraded is true because items 6a and 6b have no substitute — nothing else in
// the system supplies the restored instructions or the skill index.
func TestBuild_NilDeps(t *testing.T) {
	cp := ckFull(t)
	got, err := Build(context.Background(), goldenRequest(cp, maxBudget()), Deps{})
	require.NoError(t, err, "a build with no collaborators degrades; it never errors")
	require.True(t, got.Degraded,
		"the scanner and indexer are unavailable and nothing else supplies items 6a and 6b")

	kinds := map[ItemKind]bool{}
	for _, it := range got.Items {
		kinds[it.Kind] = true
	}
	require.True(t, kinds[ItemInvariants], "item 1 comes from the checkpoint and needs no collaborator")
	require.True(t, kinds[ItemUserIntent], "item 2 falls back to the checkpoint's own verbatim copy")
	require.True(t, kinds[ItemAffordance], "item 8 is a fixed string and must always survive")
	require.False(t, kinds[ItemRestoredInstructions], "no scanner means no item 6a")
	require.False(t, kinds[ItemSkillIndex], "no indexer means no item 6b")

	// Each absent source is NAMED, so the agent can tell "there were none" from "we could not look".
	var sawRule, sawSkill bool
	for _, e := range got.Dropped {
		if e.ID == "unavailable" && e.Kind == dropKindPathRule {
			sawRule = true
		}
		if e.ID == "unavailable" && e.Kind == dropKindSkill {
			sawSkill = true
		}
	}
	require.True(t, sawRule, "the missing rule scanner must be reported: %v", got.Dropped)
	require.True(t, sawSkill, "the missing skill indexer must be reported: %v", got.Dropped)
}

// TestBuild_NoTranscriptRead_ClosesG75 is V4-SP11-17 and V6 1.11.11: rehydration never depends on
// the summarizer having complied.
//
// G7.5 is the failure where the host's compaction summary comes back empty or malformed
// (§2.8's `content: null`). Qompack's answer is that L5 does not read the transcript AT ALL: the
// payload is built from the checkpoint, L0's own capture, the ledger and the filesystem. That is
// enforced by the type signature — neither Request nor Deps carries a transcript path or a reader
// — and this test pins both halves: the surface offers no way to read one, and the payload is
// byte-identical whatever the transcript would have said.
func TestBuild_NoTranscriptRead_ClosesG75(t *testing.T) {
	cp := ckFull(t)

	// The store is the only seam that could reach arbitrary content, and it counts its calls. A
	// summary-reading Build would have to search for the transcript or open a root it was never
	// given; the fake fails both loudly.
	st := newFakeStore().withPrompt(firstPromptID(cp.Session), cp.Session, 0, cp.UserIntent.Original)
	d := fullDeps(t, cp)
	d.Store = st

	first, err := Build(context.Background(), goldenRequest(cp, maxBudget()), d)
	require.NoError(t, err)
	require.Zero(t, st.searchCalls,
		"a rehydration that searched the store could reach a transcript summary; it must not")

	// A second build whose ONLY difference is a transcript that would have carried a summary — the
	// request has nowhere to put one, which is exactly the point — produces the same bytes.
	second, err := Build(context.Background(), goldenRequest(cp, maxBudget()), fullDeps(t, cp))
	require.NoError(t, err)
	require.Equal(t, first.Text, second.Text,
		"the payload must not vary with anything outside (Request, Deps); §2.8's content: null "+
			"costs the session nothing")

	// The surface itself is the durable half of the guarantee: a future field named for the
	// transcript would fail here before anyone could read from it.
	for _, field := range []string{"Transcript", "Summary"} {
		require.NotContains(t, requestFieldNames(), field,
			"Request must carry no transcript or summary field (G7.5)")
		require.NotContains(t, depsFieldNames(), field,
			"Deps must carry no transcript or summary reader (G7.5)")
	}
}

// PropBuild_MonotoneInBudget is V4-SP11-14's §6.9 embedded-coding property: a smaller budget
// yields, per item, a PREFIX of what a larger budget yielded — and a SUPERSET of what it dropped.
//
// This is what makes every budget cut automatically near-optimal instead of merely legal. It also
// rules out the two failure modes a per-budget example test cannot: an item that reorders its
// units under pressure, and a cheapest-first fill that would keep a different subset rather than a
// shorter one.
func PropBuild_MonotoneInBudget(t *rapid.T) {
	lo := core.Tokens(rapid.IntRange(600, 4000).Draw(t, "lo"))
	hi := lo + core.Tokens(rapid.IntRange(1, 7500).Draw(t, "delta"))

	cp := ckLongEvolution(rapid.IntRange(0, 60).Draw(t, "evolution"))
	small, err := Build(context.Background(), requestFor(&testing.T{}, cp, lo), fullDeps(&testing.T{}, cp))
	if err != nil {
		t.Fatalf("small build: %v", err)
	}
	large, err := Build(context.Background(), requestFor(&testing.T{}, cp, hi), fullDeps(&testing.T{}, cp))
	if err != nil {
		t.Fatalf("large build: %v", err)
	}

	if small.Tokens > large.Tokens {
		t.Fatalf("budget %d produced %d tokens, more than budget %d's %d",
			lo, small.Tokens, hi, large.Tokens)
	}

	byKind := map[ItemKind]string{}
	for _, it := range large.Items {
		byKind[it.Kind] = it.Text
	}
	for _, it := range small.Items {
		if it.Kind == ItemDropReport {
			// Item 7 is the COMPLEMENT of the payload, so it is the one section that legitimately
			// grows as the budget shrinks. Its own monotonicity is asserted below, in the opposite
			// direction, which is the stronger statement: a bigger budget may rescue a drop and may
			// never introduce one.
			continue
		}
		big, ok := byKind[it.Kind]
		if !ok {
			t.Fatalf("kind %s survived budget %d but not budget %d", it.Kind, lo, hi)
		}
		// Prefix on LINES rather than bytes: the item's heading carries counts that legitimately
		// differ between builds, and it is the admitted units that must nest. Item 2 renders its
		// evolution above its original (D50), so a larger budget's extra deltas land BETWEEN the
		// smaller one's deltas and the original: its evolution must nest as a prefix and its
		// original must be the same (criterion change, w15-rehydrate — the admitted records still
		// nest exactly as before; only where item 2 renders its original moved).
		nests := linesArePrefix(it.Text, big)
		if it.Kind == ItemUserIntent {
			nests = intentNests(it.Text, big)
		}
		if !nests {
			t.Fatalf("kind %s at budget %d is not a prefix of the same kind at budget %d:\n%s\n---\n%s",
				it.Kind, lo, hi, it.Text, big)
		}
	}

	// The drop report's half of the property, in the opposite direction: everything the LARGER
	// budget still had to drop must also have been dropped by the smaller one. Raising a budget can
	// only ever rescue material, never lose it.
	dropped := map[string]bool{}
	for _, e := range small.Dropped {
		dropped[e.Kind+"\x00"+e.ID] = true
	}
	for _, e := range large.Dropped {
		if !dropped[e.Kind+"\x00"+e.ID] {
			t.Fatalf("budget %d dropped %s/%s but budget %d did not; a smaller budget must drop a "+
				"superset", hi, e.Kind, e.ID, lo)
		}
	}
}

func TestBuild_MonotoneInBudget(t *testing.T) { rapid.Check(t, PropBuild_MonotoneInBudget) }

// requestFieldNames and depsFieldNames are the exported field names of the two types Build takes.
// They exist so the G7.5 guarantee is asserted against the SURFACE and not only against today's
// behaviour: a transcript path added to either type would fail the test that reads them before any
// code could read from it.
func requestFieldNames() []string { return exportedFieldNames(Request{}) }
func depsFieldNames() []string    { return exportedFieldNames(Deps{}) }

func exportedFieldNames(v any) []string {
	t := reflect.TypeOf(v)
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		if f := t.Field(i); f.IsExported() {
			out = append(out, f.Name)
		}
	}
	return out
}

// linesArePrefix reports whether small's unit lines are a prefix of big's, ignoring the heading
// line each section starts with.
// intentNests is linesArePrefix for item 2: the evolution above originalRequestLabel nests as a
// prefix, and everything from the label on (the original) is identical.
func intentNests(small, big string) bool {
	label := "\n" + originalRequestLabel + "\n"
	sEvo, sOrig, sOK := strings.Cut(small, label)
	bEvo, bOrig, bOK := strings.Cut(big, label)
	if !sOK || !bOK {
		return linesArePrefix(small, big)
	}
	return sOrig == bOrig && linesArePrefix(sEvo+"\n", bEvo+"\n")
}

func linesArePrefix(small, big string) bool {
	s := strings.Split(strings.TrimSuffix(small, "\n"), "\n")
	b := strings.Split(strings.TrimSuffix(big, "\n"), "\n")
	if len(s) == 0 || len(b) == 0 {
		return true
	}
	s, b = s[1:], b[1:] // drop the heading, whose counts vary by build
	if len(s) > len(b) {
		return false
	}
	for i := range s {
		if s[i] != b[i] {
			return false
		}
	}
	return true
}
