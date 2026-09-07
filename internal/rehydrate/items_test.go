package rehydrate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rules"
	"github.com/qompack/qompack/internal/skills"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// ── shared test helpers ──

// bg is context.Background, spelled short because every builder call takes one.
func bg() context.Context { return context.Background() }

// dropCmp compares DropEntry slices by value, ignoring order-insensitive noise nowhere: order IS
// asserted, so the only option is the unexported-field guard go-cmp requires on a foreign type.
var dropCmp = cmpopts.EquateEmpty()

// elim builds one negknow.Record by field assignment. The ledger's own constructors are SP-09's
// and mint ids; these fixtures need chosen ids so the score map can name them.
func elim(id, target, approach, reason string) negknow.Record {
	return negknow.Record{
		ID: id, Target: target, Approach: approach, Reason: reason,
		Scope: negknow.ScopeSession, Status: negknow.StatusActive,
	}
}

// at stamps a record's timestamp, for the TS tiebreak.
func at(rec negknow.Record, ts core.UnixMilli) negknow.Record { rec.TS = ts; return rec }

// scoped stamps a record's scope.
func scoped(rec negknow.Record, s negknow.Scope) negknow.Record { rec.Scope = s; return rec }

// stale flips a record to StatusStale.
func stale(rec negknow.Record) negknow.Record { rec.Status = negknow.StatusStale; return rec }

// describedBy attaches the canonical descriptor a proxy score is looked up through.
func describedBy(rec negknow.Record, path, symbol string) negknow.Record {
	rec.Desc.NormalizedPath = path
	rec.Desc.Symbol = symbol
	return rec
}

// depsWith is a Deps carrying only the collaborators a single builder needs, plus a spy logger the
// caller keeps a handle on.
func depsWith(log *spyLogger) Deps { return Deps{Tokens: fakeEstimator{}, Log: log} }

// ── item 1: invariants ──

// TestInvariants_Verbatim pins §8.6 item 1: pinned facts, verbatim, always — in stored order, with
// the source parenthetical, and with the zero DropEntry that marks tier-1 material.
func TestInvariants_Verbatim(t *testing.T) {
	cp := ckFull(t)
	r := requestFor(t, cp, generousTestBudget)

	got := buildInvariants(bg(), r, depsWith(&spyLogger{}))

	require.Equal(t, 2, got.seen)
	require.Equal(t, []string{
		"- [inv_7c1a9e2f4b60] The refresh-token rotation must remain single-use; reuse detection is a security requirement, not a preference. (source: user)\n",
		"- [inv_2d8f0a6c3e15] Never change the pgbouncer pool mode without re-running the connection-exhaustion reproduction. (source: agent)\n",
	}, unitTexts(got), "invariants render verbatim, in stored order")

	for _, u := range got.units {
		require.True(t, isFixedUnit(u), "tier-1 units carry the zero DropEntry: they are never truncated")
	}
	require.Empty(t, got.drops)
}

// TestInvariants_OmitsAnEmptySource asserts the parenthetical disappears rather than rendering
// "(source: )" for an invariant whose source was not recorded.
func TestInvariants_OmitsAnEmptySource(t *testing.T) {
	cp := ckEmpty()
	cp.Invariants = []checkpoint.Invariant{{ID: "inv_x", Text: "Keep it single-use."}}
	r := requestFor(t, cp, generousTestBudget)

	got := buildInvariants(bg(), r, depsWith(&spyLogger{}))

	require.Equal(t, []string{"- [inv_x] Keep it single-use.\n"}, unitTexts(got))
}

// TestInvariants_EmptyOmitsSection asserts a checkpoint with no pins yields no units and therefore
// no section at all — not an empty heading.
func TestInvariants_EmptyOmitsSection(t *testing.T) {
	r := requestFor(t, ckEmpty(), generousTestBudget)

	got := buildInvariants(bg(), r, depsWith(&spyLogger{}))

	require.Empty(t, got.units)
	require.Zero(t, got.seen)
	require.Equal(t, "", itemText(ItemInvariants, 0, 0, unitTexts(got)))
}

// ── item 2: verbatim original user intent ──

// TestUserIntent_FromL0NotCheckpoint is the G7.3/G2.3 closure: when L0's verbatim capture and the
// checkpoint's copy disagree, L0 WINS, the divergence is reported, and it is LOUD — a difference
// here means something regenerated what §8.5 says is never regenerated.
func TestUserIntent_FromL0NotCheckpoint(t *testing.T) {
	cp := ckMinimal()
	cp.UserIntent.Original = "A summarized restatement of the task."
	r := requestFor(t, cp, generousTestBudget)

	log := &spyLogger{}
	d := depsWith(log)
	d.Store = newFakeStore().withPrompt(firstPromptID(cp.Session), cp.Session, 0,
		"Fix the Stripe webhook retry logic; payments are being double-charged under load.")

	got := buildUserIntent(bg(), r, d)

	require.Equal(t,
		"> Fix the Stripe webhook retry logic; payments are being double-charged under load.\n",
		got.units[0].text, "the L0 text is what reaches the model")
	require.NotContains(t, got.units[0].text, "summarized restatement")

	require.Empty(t, cmp.Diff([]checkpoint.DropEntry{{
		Kind: dropKindIntentMismatch, ID: string(cp.Session),
		Detail: "checkpoint user_intent.original differs from the L0 capture; injecting the L0 text",
	}}, got.drops, dropCmp))
	require.Equal(t, 1, log.loud, "a regenerated original intent is a contract violation, not a note")
}

// TestUserIntent_AgreeingSourcesReportNothing asserts the common case is silent: when the
// checkpoint's copy matches L0 byte for byte there is no mismatch drop and no Loud.
func TestUserIntent_AgreeingSourcesReportNothing(t *testing.T) {
	cp := ckMinimal()
	r := requestFor(t, cp, generousTestBudget)

	log := &spyLogger{}
	d := depsWith(log)
	d.Store = newFakeStore().withPrompt(firstPromptID(cp.Session), cp.Session, 0, cp.UserIntent.Original)

	got := buildUserIntent(bg(), r, d)

	require.Empty(t, got.drops)
	require.Zero(t, log.loud)
	require.Zero(t, log.info)
}

// TestUserIntent_ResolvesTheDerivedFirstPromptID is the anti-Search assertion.
//
// Store.Search clamps K and ranks newest-first, so the earliest prompt of a long session is the
// first thing it excludes — and the candidate set is not empty, it holds the session's LATER
// prompts, so a relevance search would inject a mid-session prompt as "the verbatim original user
// intent". The store here holds a later prompt of the same session and a same-turn prompt of a
// different session precisely so a sloppy lookup would find the wrong one.
func TestUserIntent_ResolvesTheDerivedFirstPromptID(t *testing.T) {
	cp := ckEmpty()
	cp.Session = core.SessionID("s1")
	r := requestFor(t, cp, generousTestBudget)

	st := newFakeStore().
		withPrompt("prompt_s1_0", "s1", 0, "the original ask").
		withPrompt("prompt_s1_4", "s1", 4, "a much later, misleading ask").
		withPrompt("prompt_s0_0", "s0", 0, "another session's ask")
	d := depsWith(&spyLogger{})
	d.Store = st

	got := buildUserIntent(bg(), r, d)

	require.Equal(t, []core.ToolUseID{"prompt_s1_0"}, st.toolUseCalls,
		"exactly one exact, O(1) lookup on the derived id")
	require.Zero(t, st.searchCalls, "the original intent is never resolved by relevance search")
	require.Equal(t, []string{"> the original ask\n"}, unitTexts(got))
}

// TestUserIntent_IgnoresOtherSessions asserts the record is sanity-checked before it is
// trusted: a session mismatch means SP-08's id scheme drifted, and injecting a record this slice
// cannot vouch for is worse than injecting the checkpoint copy. Warn, not Loud — a drift, not a
// regeneration.
func TestUserIntent_IgnoresOtherSessions(t *testing.T) {
	cp := ckMinimal()
	r := requestFor(t, cp, generousTestBudget)

	log := &spyLogger{}
	d := depsWith(log)
	d.Store = newFakeStore().withPrompt(firstPromptID(cp.Session), "some-other-session", 0, "not ours")

	got := buildUserIntent(bg(), r, d)

	require.Equal(t, "> "+cp.UserIntent.Original+"\n", got.units[0].text, "the checkpoint copy stands in")
	require.Equal(t, 1, log.warn)
	require.Equal(t, 1, log.info)
	require.Len(t, got.drops, 1)
	require.Equal(t, dropKindUserIntentSource, got.drops[0].Kind)
}

// TestUserIntent_EarliestTurnOfThisSessionWins is the same guard on the other field: turn 0 is what
// "first prompt" means, and a record at another turn under a turn-0 id is a drifted scheme.
func TestUserIntent_EarliestTurnOfThisSessionWins(t *testing.T) {
	cp := ckMinimal()
	r := requestFor(t, cp, generousTestBudget)

	log := &spyLogger{}
	d := depsWith(log)
	d.Store = newFakeStore().withPrompt(firstPromptID(cp.Session), cp.Session, 7, "a later prompt")

	got := buildUserIntent(bg(), r, d)

	require.Equal(t, "> "+cp.UserIntent.Original+"\n", got.units[0].text)
	require.Equal(t, 1, log.warn)
	require.NotContains(t, got.units[0].text, "a later prompt")
}

// TestUserIntent_FallsBackWhenNotFound covers the ORDINARY case — a session whose first prompt
// predates the store, or whose L0 capture never ran. It is Info, not Loud: §8.5 guarantees the
// checkpoint's copy is itself verbatim-from-L0, so this is a weaker provenance chain, not a wrong
// one.
func TestUserIntent_FallsBackWhenNotFound(t *testing.T) {
	cp := ckMinimal()
	r := requestFor(t, cp, generousTestBudget)

	log := &spyLogger{}
	d := depsWith(log)
	d.Store = newFakeStore()

	got := buildUserIntent(bg(), r, d)

	require.Equal(t, "> "+cp.UserIntent.Original+"\n", got.units[0].text)
	require.Equal(t, 1, log.info)
	require.Zero(t, log.loud, "an absent capture is not a contract violation")
	require.Equal(t, []checkpoint.DropEntry{{
		Kind: dropKindUserIntentSource, ID: "l0",
		Detail: "L0 verbatim capture unavailable; using the checkpoint copy, which §8.5 " +
			"guarantees is itself verbatim-from-L0 and never regenerated",
	}}, got.drops)
}

// TestUserIntent_FallsBackWhenStoreErrors covers the remaining unavailable-source shapes: a store
// error, a record with no root, an Open failure, and no store at all. Every one degrades to the
// checkpoint copy rather than emitting nothing.
func TestUserIntent_FallsBackWhenStoreErrors(t *testing.T) {
	cp := ckMinimal()
	r := requestFor(t, cp, generousTestBudget)
	id := firstPromptID(cp.Session)

	cases := map[string]*fakeStore{
		"tool_use errors": newFakeStore().withToolUseErr(id, errors.New("index corrupt")),
		"no root":         newFakeStore().withRootlessPrompt(id, cp.Session, 0),
		"open errors": newFakeStore().withPrompt(id, cp.Session, 0, "x").
			withOpenErr(id, errors.New("object missing")),
		"empty content": newFakeStore().withPrompt(id, cp.Session, 0, "   \n  "),
	}

	for name, st := range cases {
		t.Run(name, func(t *testing.T) {
			d := depsWith(&spyLogger{})
			d.Store = st

			got := buildUserIntent(bg(), r, d)

			require.Equal(t, "> "+cp.UserIntent.Original+"\n", got.units[0].text)
			require.Len(t, got.drops, 1)
			require.Equal(t, dropKindUserIntentSource, got.drops[0].Kind)
		})
	}

	t.Run("no store at all", func(t *testing.T) {
		got := buildUserIntent(bg(), r, depsWith(&spyLogger{}))

		require.Equal(t, "> "+cp.UserIntent.Original+"\n", got.units[0].text)
		require.Len(t, got.drops, 1)
	})
}

// TestUserIntent_StripsPriorInjections covers the pathological transcript where a previous
// rehydration's own payload was captured as a prompt. §8.5's tagging exists precisely so that
// material is identifiable and ignorable, and never re-encoded (§4.6).
func TestUserIntent_StripsPriorInjections(t *testing.T) {
	cp := ckEmpty()
	cp.Session = core.SessionID("s2")
	r := requestFor(t, cp, generousTestBudget)

	captured := "Fix the retry logic.\n" + Wrap(core.CheckpointSeq(3), "# Qompack rehydration\ninjected") + "\n"
	d := depsWith(&spyLogger{})
	d.Store = newFakeStore().withPrompt(firstPromptID(cp.Session), cp.Session, 0, captured)

	got := buildUserIntent(bg(), r, d)

	require.Equal(t, []string{"> Fix the retry logic.\n"}, unitTexts(got))
	require.NotContains(t, got.units[0].text, "qompack:injected")
	require.NotContains(t, got.units[0].text, "Qompack rehydration")
}

// TestUserIntent_FencedPromptSurvivesVerbatim is the regression guard on the no-contents guard's
// exclusion of item 2.
//
// A real user prompt routinely runs twenty lines and routinely contains a fenced stack trace or a
// pasted diff. Running guardNoContents over item 2 would drop the original task statement for
// containing a code fence, silently re-opening the exact gap this item closes — so the assertion
// is deliberately two-sided: the guard WOULD reject this unit, and the builder emits it anyway.
func TestUserIntent_FencedPromptSurvivesVerbatim(t *testing.T) {
	lines := []string{"Fix the double-charge. Here is the trace:", "```", "Traceback (most recent call last):"}
	for i := 0; i < 26; i++ {
		lines = append(lines, "  at frame "+itoa(i))
	}
	lines = append(lines, "```")
	prompt := strings.Join(lines, "\n")
	require.Greater(t, len(lines), maxUnitLines, "fixture must exceed the guard's line ceiling")

	cp := ckEmpty()
	cp.Session = core.SessionID("s3")
	r := requestFor(t, cp, generousTestBudget)
	d := depsWith(&spyLogger{})
	d.Store = newFakeStore().withPrompt(firstPromptID(cp.Session), cp.Session, 0, prompt)

	got := buildUserIntent(bg(), r, d)

	require.Len(t, got.units, 1)
	require.True(t, guardNoContents(got.units[0]),
		"fixture sanity: the guard would reject this unit if it ever ran over item 2")

	var back []string
	for _, ln := range strings.Split(strings.TrimSuffix(got.units[0].text, "\n"), "\n") {
		back = append(back, strings.TrimPrefix(ln, "> "))
	}
	require.Equal(t, lines, back, "the prompt survives byte-identically inside the blockquote")
}

// TestUserIntent_CapsAtMaxIntentBytes asserts a pasted wall of text is bounded. The tail of a paste
// is not the statement of intent, and an unbounded read would let one prompt consume the payload.
func TestUserIntent_CapsAtMaxIntentBytes(t *testing.T) {
	cp := ckEmpty()
	cp.Session = core.SessionID("s4")
	r := requestFor(t, cp, generousTestBudget)
	d := depsWith(&spyLogger{})
	d.Store = newFakeStore().withPrompt(firstPromptID(cp.Session), cp.Session, 0,
		strings.Repeat("a", 4*maxIntentBytes))

	got := buildUserIntent(bg(), r, d)

	require.Len(t, got.units, 1)
	// "> " + at most maxIntentBytes of body + "\n".
	require.LessOrEqual(t, len(got.units[0].text), maxIntentBytes+3)
	require.Equal(t, "> "+strings.Repeat("a", maxIntentBytes)+"\n", got.units[0].text)
}

// TestUserIntent_EvolutionUnitsFollowTheOriginal pins the unit layout the budget pass depends on:
// units[0] is the never-truncated original, units[1:] are the truncatable deltas, and the
// "Evolution:" header rides on the FIRST delta so that dropping them all removes the header too.
func TestUserIntent_EvolutionUnitsFollowTheOriginal(t *testing.T) {
	cp := ckFull(t)
	r := requestFor(t, cp, generousTestBudget)
	d := depsWith(&spyLogger{})
	d.Store = newFakeStore().withPrompt(firstPromptID(cp.Session), cp.Session, 0, cp.UserIntent.Original)

	got := buildUserIntent(bg(), r, d)

	require.Equal(t, 3, got.seen, "the original plus two deltas")
	require.Len(t, got.units, 3)
	require.True(t, isFixedUnit(got.units[0]), "the original is never truncated")
	require.True(t, strings.HasPrefix(got.units[1].text, "Evolution:\n"))
	require.False(t, strings.Contains(got.units[2].text, "Evolution:"))

	require.Equal(t, checkpoint.DropEntry{Kind: dropKindUserIntentEvolution, ID: "0"}, got.units[1].drop)
	require.Equal(t, checkpoint.DropEntry{Kind: dropKindUserIntentEvolution, ID: "1"}, got.units[2].drop)
}

// ── item 3: eliminations ──

// TestEliminations_TopNFromConfig asserts the cut is runtime.rehydrate.eliminationsTopN, read from
// config rather than hard-coded, and that the trailing note is not one of the N.
func TestEliminations_TopNFromConfig(t *testing.T) {
	recs := make([]negknow.Record, 0, 5)
	for i := 0; i < 5; i++ {
		recs = append(recs, elim("elim_"+itoa(i), "a.ts", "approach", "reason"))
	}
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	r.Cfg.Runtime.Rehydrate.EliminationsTopN = 2

	d := depsWith(&spyLogger{})
	d.Ledger = newFakeLedger().withActive(negknow.ScopeSession, recs...)

	got := buildEliminations(bg(), r, d, nil)

	require.Equal(t, 5, got.seen)
	require.Len(t, got.units, 3, "two records plus the never-truncated note")
	require.Equal(t, 2, eliminationsShown(got))
	require.True(t, isFixedUnit(got.units[2]), "the note is the fixed unit")
}

// TestEliminations_OrderedBySliceScore asserts ranking is by relevance, with the score map built
// through dag.EliminationNode — never a hand-built literal, which would silently score zero.
func TestEliminations_OrderedBySliceScore(t *testing.T) {
	low := elim("elim_low", "a.ts", "x", "r1")
	high := elim("elim_high", "b.ts", "y", "r2")
	mid := elim("elim_mid", "c.ts", "z", "r3")

	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	d := depsWith(&spyLogger{})
	d.Ledger = newFakeLedger().withActive(negknow.ScopeSession, low, high, mid)

	sc := map[dag.NodeID]float32{
		dag.EliminationNode("elim_low"):  0.2,
		dag.EliminationNode("elim_high"): 0.9,
		dag.EliminationNode("elim_mid"):  0.5,
	}

	got := buildEliminations(bg(), r, d, sc)

	require.Equal(t, []string{"b.ts", "c.ts", "a.ts"}, targetsOf(got))
}

// targetsOf projects rendered elimination lines back to their target field, which is the first
// token after "- ".
func targetsOf(b built) []string {
	var out []string
	for i, u := range b.units {
		if i == len(b.units)-1 && isFixedUnit(u) {
			continue // the note
		}
		line := strings.TrimPrefix(u.text, "- ")
		// Cut rather than Index: a line missing the separator yields the whole line, which fails
		// the caller's assertion with something readable instead of panicking on a -1 slice.
		target, _, _ := strings.Cut(line, " — ")
		out = append(out, target)
	}
	return out
}

// TestEliminations_FileProxyScore asserts a record with no node of its own still ranks, through the
// file it is about, at the documented discount.
func TestEliminations_FileProxyScore(t *testing.T) {
	viaFile := describedBy(elim("elim_file", "src/auth.ts:refreshToken", "x", "r"), "src/auth.ts", "")
	direct := elim("elim_direct", "other.ts", "y", "r")

	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	d := depsWith(&spyLogger{})
	d.Ledger = newFakeLedger().withActive(negknow.ScopeSession, direct, viaFile)

	// 0.8 * 0.75 == 0.6, which must beat 0.5 but lose to 0.7.
	sc := map[dag.NodeID]float32{
		dag.FileNode(paths.Key("src/auth.ts")): 0.8,
		dag.EliminationNode("elim_direct"):     0.5,
	}
	require.InDelta(t, 0.6, recordScore(sc, viaFile), 1e-6)
	require.InDelta(t, 0.5, recordScore(sc, direct), 1e-6)

	got := buildEliminations(bg(), r, d, sc)
	require.Equal(t, []string{"src/auth.ts:refreshToken", "other.ts"}, targetsOf(got))

	sc[dag.EliminationNode("elim_direct")] = 0.7
	got = buildEliminations(bg(), r, d, sc)
	require.Equal(t, []string{"other.ts", "src/auth.ts:refreshToken"}, targetsOf(got))
}

// TestEliminations_SymbolProxyScore asserts the symbol proxy uses dag.SymbolNode's real key shape,
// pathKey + "#" + name — including the pathless "symbol:#name" form dag mints for a symbol whose
// defining file is not known. "symbol:" + name matches neither and can never hit.
func TestEliminations_SymbolProxyScore(t *testing.T) {
	withPath := describedBy(elim("elim_sym", "src/auth.ts:refreshToken", "x", "r"), "src/auth.ts", "refreshToken")
	pathless := describedBy(elim("elim_bare", "refreshToken", "y", "r"), "", "refreshToken")

	sc := map[dag.NodeID]float32{
		dag.SymbolNode(paths.Key("src/auth.ts"), "refreshToken"): 0.8,
	}
	require.InDelta(t, 0.6, recordScore(sc, withPath), 1e-6, "0.8 discounted by the symbol proxy decay")
	require.Zero(t, recordScore(sc, pathless), "the pathless key is a different node")

	bare := map[dag.NodeID]float32{dag.SymbolNode("", "refreshToken"): 0.8}
	require.InDelta(t, 0.6, recordScore(bare, pathless), 1e-6, "symbol:#refreshToken is a real lookup")
	require.Zero(t, recordScore(bare, withPath))
}

// TestEliminations_TieBreakByTSThenID asserts the total order: score descending, then TS
// descending (newest evidence first), then ID ascending. Without a total order the digest would
// differ across replays and divergence measurement would be meaningless.
func TestEliminations_TieBreakByTSThenID(t *testing.T) {
	old := at(elim("elim_b", "b.ts", "x", "r"), 100)
	newer := at(elim("elim_c", "c.ts", "x", "r"), 200)
	sameTSLowID := at(elim("elim_a", "a.ts", "x", "r"), 200)

	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	d := depsWith(&spyLogger{})
	d.Ledger = newFakeLedger().withActive(negknow.ScopeSession, old, newer, sameTSLowID)

	got := buildEliminations(bg(), r, d, nil)

	require.Equal(t, []string{"a.ts", "c.ts", "b.ts"}, targetsOf(got),
		"TS 200 before TS 100; within TS 200, elim_a before elim_c")
}

// PropEliminations_OrderIsDeterministic asserts the ordering is a genuine total order: the same
// record set in any input permutation renders the same lines. Rehydration output is compared
// across replays to measure divergence, so a nondeterministic tiebreak would make that
// measurement meaningless.
func PropEliminations_OrderIsDeterministic(t *rapid.T) {
	n := rapid.IntRange(1, 8).Draw(t, "n")
	recs := make([]negknow.Record, 0, n)
	for i := 0; i < n; i++ {
		recs = append(recs, at(elim("elim_"+itoa(i),
			rapid.SampledFrom([]string{"a.ts", "b.ts", "c.ts"}).Draw(t, "target"),
			"x", "r"), core.UnixMilli(rapid.IntRange(0, 3).Draw(t, "ts"))))
	}
	perm := rapid.Permutation(recs).Draw(t, "perm")

	cp := ckEmpty()
	r := requestFor(&testing.T{}, cp, generousTestBudget)
	r.Cfg.Runtime.Rehydrate.EliminationsTopN = n

	base := buildEliminations(bg(), r, Deps{Ledger: newFakeLedger().withActive(negknow.ScopeSession, recs...)}, nil)
	shuf := buildEliminations(bg(), r, Deps{Ledger: newFakeLedger().withActive(negknow.ScopeSession, perm...)}, nil)

	if diff := cmp.Diff(unitTexts(base), unitTexts(shuf)); diff != "" {
		t.Fatalf("ordering is not a total order (-base +shuffled):\n%s", diff)
	}
}

func TestEliminations_OrderIsDeterministic(t *testing.T) {
	rapid.Check(t, PropEliminations_OrderIsDeterministic)
}

// TestEliminations_NoteCountsTheRest pins the honesty clause: the note names how many were held
// back and carries the standing instruction that makes the whole item actionable.
func TestEliminations_NoteCountsTheRest(t *testing.T) {
	recs := make([]negknow.Record, 0, 23)
	for i := 0; i < 23; i++ {
		recs = append(recs, elim("elim_"+itoa(100+i), "a.ts", "x", "r"))
	}
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	r.Cfg.Runtime.Rehydrate.EliminationsTopN = 8

	d := depsWith(&spyLogger{})
	d.Ledger = newFakeLedger().withActive(negknow.ScopeSession, recs...)

	got := buildEliminations(bg(), r, d, nil)

	require.Equal(t,
		"15 further eliminations are recorded and not shown. "+StandingInstruction()+"\n",
		got.units[len(got.units)-1].text)
	require.Equal(t,
		"## 3. Approaches already eliminated (top 8 of 23 by slice relevance)",
		eliminationsHeading(eliminationsShown(got), got.seen),
		"the heading and the note are rendered from the same pair and always agree")
}

// TestEliminations_NoteIsJustTheInstructionWhenNothingIsHeldBack asserts the count clause
// disappears when everything fit, rather than rendering "0 further eliminations".
func TestEliminations_NoteIsJustTheInstructionWhenNothingIsHeldBack(t *testing.T) {
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	d := depsWith(&spyLogger{})
	d.Ledger = newFakeLedger().withActive(negknow.ScopeSession, elim("elim_1", "a.ts", "x", "r"))

	got := buildEliminations(bg(), r, d, nil)

	require.Equal(t, StandingInstruction()+"\n", got.units[len(got.units)-1].text)
	require.Empty(t, got.drops)
}

// TestEliminations_EmptyOmitsItemAndInstruction is the inherited contract runStandingInstructionCase
// executes: with no eliminations there is no item 3, and the standing instruction must then appear
// NOWHERE. It is item 3's companion, not a free-floating banner — telling an agent to call
// already_tried when nothing has been eliminated is noise that costs budget and trains it to
// ignore the line.
func TestEliminations_EmptyOmitsItemAndInstruction(t *testing.T) {
	r := requestFor(t, ckEmpty(), generousTestBudget)
	d := depsWith(&spyLogger{})
	d.Ledger = newFakeLedger()

	got := buildEliminations(bg(), r, d, nil)

	require.Empty(t, got.units)
	require.Zero(t, got.seen)
	require.Equal(t, "", itemText(ItemEliminations, 0, 0, unitTexts(got)))
	for _, u := range got.units {
		require.NotContains(t, u.text, StandingInstruction())
	}
}

// TestEliminations_StaleRendersTheVerbatimNote asserts a stale record is shown, with §8.3's note
// verbatim. Hiding it would re-open G6.2: deciding whether changed evidence overturns an
// elimination is the agent's job, and it cannot do that for a record it never sees.
func TestEliminations_StaleRendersTheVerbatimNote(t *testing.T) {
	cp := ckEmpty()
	cp.Eliminated = []negknow.Record{stale(elim("elim_s", "src/webhooks/retry.ts", "client-side backoff",
		"duplicates the charge under concurrent delivery"))}
	r := requestFor(t, cp, generousTestBudget)
	require.Equal(t, "flag", r.Cfg.Eliminations.StaleResponse, "the Appendix C default includes stale records")

	got := buildEliminations(bg(), r, depsWith(&spyLogger{}), nil)

	require.Equal(t,
		"- src/webhooks/retry.ts — \"client-side backoff\" — duplicates the charge under concurrent delivery "+
			"[stale: previously eliminated, but the evidence has changed since — re-verification may be warranted]\n",
		got.units[0].text)
}

// TestEliminations_StaleDroppedWhenConfigured asserts eliminations.staleResponse == "drop" excludes
// them from the candidate set entirely, so they are neither rendered nor counted in "N further".
func TestEliminations_StaleDroppedWhenConfigured(t *testing.T) {
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	r.Cfg.Eliminations.StaleResponse = "drop"

	d := depsWith(&spyLogger{})
	d.Ledger = newFakeLedger().withActive(negknow.ScopeSession,
		elim("elim_a", "a.ts", "x", "r"), stale(elim("elim_b", "b.ts", "y", "r")))

	got := buildEliminations(bg(), r, d, nil)

	require.Equal(t, 1, got.seen, "a dropped stale record is not a candidate")
	require.Equal(t, []string{"a.ts"}, targetsOf(got))
}

// TestEliminations_BothScopesRead is the G6.2 assertion. eliminations.defaultScope governs the
// scope NEW records are written with; it is not a read filter. §8.3 says project-scoped
// eliminations "persist and warm-start future sessions", so reading only the default scope would
// drop exactly the longest-lived negative knowledge in the store.
func TestEliminations_BothScopesRead(t *testing.T) {
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	require.Equal(t, "session", r.Cfg.Eliminations.DefaultScope, "fixture uses the Appendix C default")
	r.Cfg.Runtime.Rehydrate.EliminationsTopN = 10

	led := newFakeLedger().
		withActive(negknow.ScopeSession,
			elim("elim_s1", "s1.ts", "x", "r"), elim("elim_s2", "s2.ts", "x", "r")).
		withActive(negknow.ScopeProject,
			scoped(elim("elim_p1", "p1.ts", "x", "r"), negknow.ScopeProject),
			scoped(elim("elim_p2", "p2.ts", "x", "r"), negknow.ScopeProject),
			scoped(elim("elim_p3", "p3.ts", "x", "r"), negknow.ScopeProject))
	d := depsWith(&spyLogger{})
	d.Ledger = led

	got := buildEliminations(bg(), r, d, nil)

	require.Equal(t, []negknow.Scope{negknow.ScopeSession, negknow.ScopeProject}, led.scopesRead)
	require.Equal(t, 5, got.seen, "two session-scoped plus three project-scoped candidates")
}

// TestEliminations_NotShownProducesDropEntry asserts the single largest omission in the payload is
// reported. Its EMPTY ID is what makes item 7's collapse rule render it as one counted line.
func TestEliminations_NotShownProducesDropEntry(t *testing.T) {
	recs := make([]negknow.Record, 0, 4)
	for i := 0; i < 4; i++ {
		recs = append(recs, elim("elim_"+itoa(i), "a.ts", "x", "r"))
	}
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	r.Cfg.Runtime.Rehydrate.EliminationsTopN = 1

	d := depsWith(&spyLogger{})
	d.Ledger = newFakeLedger().withActive(negknow.ScopeSession, recs...)

	got := buildEliminations(bg(), r, d, nil)

	require.Equal(t, []checkpoint.DropEntry{{
		Kind: dropKindElimination, ID: "",
		Detail: "3 of 4 not shown; call already_tried(target, approach) or dropped()",
	}}, got.drops)
}

// TestEliminations_LedgerErrorFallsBackToCheckpoint asserts a broken ledger degrades to the
// checkpoint's frozen copy and says so, rather than emitting nothing.
func TestEliminations_LedgerErrorFallsBackToCheckpoint(t *testing.T) {
	cp := ckFull(t)
	r := requestFor(t, cp, generousTestBudget)

	led := newFakeLedger().
		withActiveErr(negknow.ScopeSession, errors.New("ledger closed")).
		withActiveErr(negknow.ScopeProject, errors.New("ledger closed"))
	log := &spyLogger{}
	d := depsWith(log)
	d.Ledger = led

	got := buildEliminations(bg(), r, d, nil)

	require.Equal(t, 1, got.seen, "the checkpoint's own copy still stands")
	require.Equal(t, []string{"src/auth.ts:refreshToken"}, targetsOf(got))
	require.Len(t, got.drops, 2, "one elimination_source drop per failed scope")
	require.Equal(t, dropKindEliminationSource, got.drops[0].Kind)
	require.Equal(t, "ledger", got.drops[0].ID)
	require.Equal(t, 2, log.warn)
}

// TestEliminations_OneScopeErrorUsesTheOther asserts a half-broken ledger still contributes what it
// can: a failure at one scope must not discard the other scope's records.
func TestEliminations_OneScopeErrorUsesTheOther(t *testing.T) {
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)

	led := newFakeLedger().
		withActiveErr(negknow.ScopeSession, errors.New("session log truncated")).
		withActive(negknow.ScopeProject, scoped(elim("elim_p", "p.ts", "x", "r"), negknow.ScopeProject))
	d := depsWith(&spyLogger{})
	d.Ledger = led

	got := buildEliminations(bg(), r, d, nil)

	require.Equal(t, 1, got.seen)
	require.Equal(t, []string{"p.ts"}, targetsOf(got))
	require.Len(t, got.drops, 1)
}

// TestEliminations_NilLedgerReportsTheAbsence asserts an unwired ledger is a reported degradation,
// not a silent one.
func TestEliminations_NilLedgerReportsTheAbsence(t *testing.T) {
	cp := ckFull(t)
	r := requestFor(t, cp, generousTestBudget)

	got := buildEliminations(bg(), r, depsWith(&spyLogger{}), nil)

	require.Equal(t, 1, got.seen, "the checkpoint's copy is used")
	require.Len(t, got.drops, 1)
	require.Equal(t, dropKindEliminationSource, got.drops[0].Kind)
}

// TestEliminations_LedgerAndCheckpointDedupedByID asserts the LEDGER copy wins on a duplicate id:
// it carries the current Status, where the checkpoint's copy is frozen at write time. Rendering
// the frozen one would show an overturned elimination as still active.
func TestEliminations_LedgerAndCheckpointDedupedByID(t *testing.T) {
	cp := ckFull(t)
	id := cp.Eliminated[0].ID
	require.Equal(t, "elim_3f9b2c7d1a48", id, "fixture sanity")

	// Same id, flipped to stale in the ledger since the checkpoint was written.
	fresh := stale(elim(id, "src/auth.ts:refreshToken", "widen pool timeout", "re-verified: still fails"))
	r := requestFor(t, cp, generousTestBudget)
	d := depsWith(&spyLogger{})
	d.Ledger = newFakeLedger().withActive(negknow.ScopeProject, fresh)

	got := buildEliminations(bg(), r, d, nil)

	require.Equal(t, 1, got.seen, "one record, not two")
	require.Contains(t, got.units[0].text, staleStatusTag, "the ledger's current Status wins")
	require.Contains(t, got.units[0].text, "re-verified: still fails")
}

// TestEliminations_ReasonIsBoundedAndOneLine asserts a long, multi-line reason cannot break the
// list rendering or run away with the budget.
func TestEliminations_ReasonIsBoundedAndOneLine(t *testing.T) {
	cp := ckEmpty()
	cp.Eliminated = []negknow.Record{elim("elim_x", "a.ts", "x", "line one\nline two "+strings.Repeat("z", 400))}
	r := requestFor(t, cp, generousTestBudget)

	got := buildEliminations(bg(), r, depsWith(&spyLogger{}), nil)

	line := got.units[0].text
	require.Equal(t, 1, strings.Count(line, "\n"), "one rendered line")
	require.Contains(t, line, "line one line two")
	require.Contains(t, line, truncMark)
}

// TestEliminations_EvidenceSuffixOmittedWhenZero asserts an elimination with no evidence hash does
// not render "[evidence sha256:0000…]", which would look like a real address.
func TestEliminations_EvidenceSuffixOmittedWhenZero(t *testing.T) {
	cp := ckEmpty()
	cp.Eliminated = []negknow.Record{elim("elim_x", "a.ts", "x", "r")}
	r := requestFor(t, cp, generousTestBudget)

	got := buildEliminations(bg(), r, depsWith(&spyLogger{}), nil)

	require.Equal(t, "- a.ts — \"x\" — r [active]\n", got.units[0].text)
}

// ── slice criteria ──

// TestSliceCriteria_UsesExportedConstructors asserts the criterion set is files, then tools, then
// decisions, deduplicated — and built with dag's own constructors. A mis-spelled prefix does not
// fail, it returns a zero score for every record and degrades ranking to TS/ID with no signal.
func TestSliceCriteria_UsesExportedConstructors(t *testing.T) {
	cp := ckFull(t)

	got := sliceCriteria(cp)

	require.Equal(t, []dag.NodeID{
		dag.FileNode(paths.Key("src/auth.ts")),
		dag.FileNode(paths.Key("docker-compose.yml")),
		dag.ToolUseNode("toolu_01A2B3C4D5E6F7G8H9J0K1L2"),
		dag.DecisionNode("dec_a3f2c9e14b70"),
	}, got)
}

// TestSliceCriteria_Deduplicates asserts a checkpoint pointing at the same file twice yields one
// criterion, so a duplicated pointer cannot skew the slice.
func TestSliceCriteria_Deduplicates(t *testing.T) {
	cp := ckEmpty()
	cp.Pointers.Files = []checkpoint.FilePointer{{Path: "a.ts"}, {Path: "a.ts"}}

	require.Equal(t, []dag.NodeID{dag.FileNode(paths.Key("a.ts"))}, sliceCriteria(cp))
}

// ── item 4: decisions ──

// TestDecisions_RenderWhatWhyRejectedEvidence pins item 4's four lines against the frozen fixture's
// decision, which is what makes the `why(decision_id)` affordance meaningful.
func TestDecisions_RenderWhatWhyRejectedEvidence(t *testing.T) {
	cp := ckFull(t)
	r := requestFor(t, cp, generousTestBudget)

	got := buildDecisions(bg(), r, depsWith(&spyLogger{}), nil)

	require.Equal(t, 1, got.seen)
	require.Equal(t, strings.Join([]string{
		"- [dec_a3f2c9e14b70] (turn 61) Move refresh-token rotation out of the request transaction and into a dedicated short-lived connection.",
		"  why: The rotation holds a row lock for the duration of the outbound identity-provider call, which is what exhausts the pool under concurrent refreshes.",
		"  rejected: Widen the pool timeout — eliminated: pgbouncer ignores it in transaction mode.; Increase pool size — rejected: moves the cliff without removing the lock-hold-across-IO pattern.",
		"  evidence: sha256:c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9",
		"",
	}, "\n"), got.units[0].text)

	require.Equal(t, checkpoint.DropEntry{
		Kind: dropKindDecision, ID: "dec_a3f2c9e14b70",
		Detail: "did not fit the rehydration budget; call why(dec_a3f2c9e14b70)",
	}, got.units[0].drop)
}

// TestDecisions_OmitsEmptyLines asserts the rejected and evidence lines disappear rather than
// rendering empty labels.
func TestDecisions_OmitsEmptyLines(t *testing.T) {
	cp := ckEmpty()
	cp.Decisions = []checkpoint.Decision{{ID: "dec_x", What: "Do the thing", Turn: 3}}
	r := requestFor(t, cp, generousTestBudget)

	got := buildDecisions(bg(), r, depsWith(&spyLogger{}), nil)

	require.Equal(t, "- [dec_x] (turn 3) Do the thing\n", got.units[0].text)
}

// TestDecisions_OrderedBySliceScoreThenTurnThenID pins the total order, built through
// dag.DecisionNode.
func TestDecisions_OrderedBySliceScoreThenTurnThenID(t *testing.T) {
	cp := ckEmpty()
	cp.Decisions = []checkpoint.Decision{
		{ID: "dec_a", What: "a", Turn: 1},
		{ID: "dec_b", What: "b", Turn: 9},
		{ID: "dec_c", What: "c", Turn: 9},
		{ID: "dec_d", What: "d", Turn: 2},
	}
	r := requestFor(t, cp, generousTestBudget)
	sc := map[dag.NodeID]float32{dag.DecisionNode("dec_d"): 0.9}

	got := buildDecisions(bg(), r, depsWith(&spyLogger{}), sc)

	require.Equal(t, []string{"dec_d", "dec_b", "dec_c", "dec_a"}, decisionIDsOf(got))
}

// decisionIDsOf projects rendered decision units back to their ids.
func decisionIDsOf(b built) []string {
	var out []string
	for _, u := range b.units {
		s := strings.TrimPrefix(u.text, "- [")
		out = append(out, s[:strings.IndexByte(s, ']')])
	}
	return out
}

// TestDecisions_GuardRejectsAFencedUnit asserts §13 invariant 5 is enforced at the injection
// boundary: a decision carrying a code fence is dropped and reported, and the rest of the payload
// is emitted normally.
func TestDecisions_GuardRejectsAFencedUnit(t *testing.T) {
	cp := ckEmpty()
	cp.Decisions = []checkpoint.Decision{
		{ID: "dec_ok", What: "fine", Turn: 1},
		{ID: "dec_bad", What: "here is the patch", Why: "```\ndiff --git a b\n```", Turn: 2},
	}
	r := requestFor(t, cp, generousTestBudget)

	got := buildDecisions(bg(), r, depsWith(&spyLogger{}), nil)

	require.Equal(t, 2, got.seen, "a guard rejection is still a candidate that was seen")
	require.Equal(t, []string{"dec_ok"}, decisionIDsOf(got))
	require.Equal(t, []checkpoint.DropEntry{{
		Kind: dropKindDecision, ID: "dec_bad", Detail: guardRejectionDetail,
	}}, got.drops)
	require.True(t, guardTripped(got.drops), "Build Louds once per build on this signal")
}

// ── item 5: current work ──

// TestCurrentWork_BlockedOnNil pins the rendering of the frozen fixture's current work, where
// blocked_on is JSON null: it renders "none" rather than being omitted, so the model is told the
// work is unblocked rather than left to guess.
func TestCurrentWork_BlockedOnNil(t *testing.T) {
	cp := ckFull(t)
	require.Nil(t, cp.CurrentWork.BlockedOn, "fixture sanity")
	r := requestFor(t, cp, generousTestBudget)

	got := buildCurrentWork(bg(), r, depsWith(&spyLogger{}))

	require.Equal(t, strings.Join([]string{
		"goal: Eliminate pool exhaustion on POST /api/session/refresh under 200 concurrent refreshes.",
		"next step: Extract the identity-provider call out of the transaction in refreshToken, then re-run the reproduction.",
		"blocked on: none",
		"",
	}, "\n"), got.units[0].text)
	require.Equal(t, checkpoint.DropEntry{Kind: dropKindCurrentWork, ID: dropKindCurrentWork}, got.units[0].drop)
}

// TestCurrentWork_RendersABlocker asserts a real blocker is surfaced verbatim on one line.
func TestCurrentWork_RendersABlocker(t *testing.T) {
	blocker := "waiting on the staging pgbouncer upgrade"
	cp := ckEmpty()
	cp.CurrentWork.Goal = "g"
	cp.CurrentWork.BlockedOn = &blocker
	r := requestFor(t, cp, generousTestBudget)

	got := buildCurrentWork(bg(), r, depsWith(&spyLogger{}))

	require.Equal(t, "goal: g\nblocked on: waiting on the staging pgbouncer upgrade\n", got.units[0].text)
}

// TestCurrentWork_AllEmptyOmitsSection asserts an unpopulated current-work block yields no unit at
// all rather than a lone "blocked on: none".
func TestCurrentWork_AllEmptyOmitsSection(t *testing.T) {
	r := requestFor(t, ckEmpty(), generousTestBudget)

	got := buildCurrentWork(bg(), r, depsWith(&spyLogger{}))

	require.Empty(t, got.units)
	require.Zero(t, got.seen)
	require.Equal(t, "", itemText(ItemCurrentWork, 0, 0, unitTexts(got)))
}

// ── item 6: pointers ──

// TestPointers_FilesBeforeTools pins the group order and the two line shapes: a path or a
// tool_use label, the content hash, and a one-line reason. Contents are never restored — that is
// the §4.4 reclamation this whole design rests on.
func TestPointers_FilesBeforeTools(t *testing.T) {
	cp := ckFull(t)
	r := requestFor(t, cp, generousTestBudget)

	got := buildPointers(bg(), r, depsWith(&spyLogger{}), nil)

	require.Equal(t, 3, got.seen)
	require.Equal(t, []string{
		"- docker-compose.yml sha256:1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d — pins the pgbouncer version and pool mode the elimination depends on\n",
		"- src/auth.ts sha256:0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c — refreshToken lives here; the lock-across-IO pattern is at the top of the function\n",
		"- tool_use toolu_01A2B3C4D5E6F7G8H9J0K1L2 sha256:5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d7c0f3b6e9a2d5c8f — load test: 200 concurrent refreshes, 37 failures, all pool acquisition timeouts\n",
	}, unitTexts(got), "files first, each group ordered by score then by path/id ascending")

	require.Equal(t, checkpoint.DropEntry{
		Kind: dropKindPointer, ID: "docker-compose.yml",
		Detail: "did not fit the rehydration budget; call recall or re_read",
	}, got.units[0].drop)
}

// TestPointers_RankedBySliceScore asserts relevance beats the alphabetical tiebreak.
func TestPointers_RankedBySliceScore(t *testing.T) {
	cp := ckFull(t)
	r := requestFor(t, cp, generousTestBudget)
	sc := map[dag.NodeID]float32{dag.FileNode(paths.Key("src/auth.ts")): 0.9}

	got := buildPointers(bg(), r, depsWith(&spyLogger{}), sc)

	require.True(t, strings.HasPrefix(got.units[0].text, "- src/auth.ts "),
		"the scored file leads, ahead of docker-compose.yml")
}

// TestPointers_GuardRejectsMultilineUnit asserts the no-contents guard runs over item 6: a pointer
// whose "reason" is actually a pasted excerpt is dropped and reported, never injected.
func TestPointers_GuardRejectsMultilineUnit(t *testing.T) {
	cp := ckEmpty()
	cp.Pointers.Files = []checkpoint.FilePointer{
		{Path: "ok.ts", Why: "a real one-line reason"},
		{Path: "bad.ts", Why: "```ts\nexport function f() {}\n```"},
	}
	r := requestFor(t, cp, generousTestBudget)

	got := buildPointers(bg(), r, depsWith(&spyLogger{}), nil)

	require.Equal(t, 2, got.seen)
	require.Len(t, got.units, 1)
	require.Contains(t, got.units[0].text, "ok.ts")
	require.Equal(t, []checkpoint.DropEntry{{
		Kind: dropKindPointer, ID: "bad.ts", Detail: guardRejectionDetail,
	}}, got.drops)
}

// TestPointers_WhyIsBoundedToOneLine asserts a long reason is truncated rather than allowed to
// consume the pointer share.
func TestPointers_WhyIsBoundedToOneLine(t *testing.T) {
	cp := ckEmpty()
	cp.Pointers.Files = []checkpoint.FilePointer{{Path: "a.ts", Why: strings.Repeat("w", 500)}}
	r := requestFor(t, cp, generousTestBudget)

	got := buildPointers(bg(), r, depsWith(&spyLogger{}), nil)

	require.Equal(t, "- a.ts — "+strings.Repeat("w", maxOneLineRunes-1)+truncMark+"\n", got.units[0].text)
}

// TestPointers_OmitsAZeroHash asserts a pointer with no recorded hash does not render
// "sha256:0000…", which would read as a real, resolvable address.
func TestPointers_OmitsAZeroHash(t *testing.T) {
	cp := ckEmpty()
	cp.Pointers.Files = []checkpoint.FilePointer{{Path: "a.ts", Why: "why"}}
	r := requestFor(t, cp, generousTestBudget)

	got := buildPointers(bg(), r, depsWith(&spyLogger{}), nil)

	require.Equal(t, "- a.ts — why\n", got.units[0].text)
}

// ── item 6a: restored instructions ──

// rule is a rules.Rule fixture.
func rule(path, body string, globs ...string) rules.Rule {
	return rules.Rule{Path: path, Globs: globs, Body: body}
}

// TestRestored_PathRulesBeforeNested pins the order and both heading shapes. Path rules go first
// because they carry explicit `paths:` intent, where a nested CLAUDE.md is proximity-inferred.
func TestRestored_PathRulesBeforeNested(t *testing.T) {
	cp := ckFull(t)
	r := requestFor(t, cp, generousTestBudget)

	d := depsWith(&spyLogger{})
	sc := &fakeScanner{
		pathScoped: []rules.Rule{
			rule(".claude/rules/db-conventions.md", "Always use the pool helper.", "src/db/**"),
			rule(".claude/rules/api-conventions.md", "All REST handlers return the envelope type; never a bare object.", "src/api/**", "src/routes/**"),
		},
		nested: []rules.Rule{rule("src/webhooks/CLAUDE.md", "Webhook handlers must be idempotent on delivery id.")},
	}
	d.Rules = sc

	got := buildRestoredInstructions(bg(), r, d, nil)

	require.Equal(t, 3, got.seen)
	require.Equal(t, []string{
		"### .claude/rules/api-conventions.md — paths: src/api/**, src/routes/**\nAll REST handlers return the envelope type; never a bare object.\n",
		"### .claude/rules/db-conventions.md — paths: src/db/**\nAlways use the pool helper.\n",
		"### src/webhooks/CLAUDE.md — nested\nWebhook handlers must be idempotent on delivery id.\n",
	}, unitTexts(got), "path rules ascending by path, then nested ascending by path")

	require.Equal(t, []string{"src/auth.ts", "docker-compose.yml"}, sc.pointers[0],
		"the scan is scoped to the checkpoint's own file pointers")

	require.Equal(t, dropKindPathRule, got.units[0].drop.Kind)
	require.Equal(t, dropKindNestedClaudeMD, got.units[2].drop.Kind)
	require.Equal(t, "did not fit the rehydration budget", got.units[2].drop.Detail,
		"a nested CLAUDE.md is directory-scoped: there is no matching glob to name")
}

// TestRestored_DropNamesTheMatchingPointerWhenTheMatcherIsWired asserts the path-rule drop says WHY
// a rule the agent no longer has was relevant — and that an unwired matcher degrades that detail
// rather than inventing one.
func TestRestored_DropNamesTheMatchingPointerWhenTheMatcherIsWired(t *testing.T) {
	cp := ckFull(t)
	r := requestFor(t, cp, generousTestBudget)
	d := depsWith(&spyLogger{})
	d.Rules = &fakeScanner{pathScoped: []rules.Rule{rule(".claude/rules/api.md", "b", "src/*.ts")}}

	unwired := buildRestoredInstructions(bg(), r, d, nil)
	require.Equal(t, "did not fit the rehydration budget", unwired.units[0].drop.Detail)

	// A stand-in for rules.Match, which lands with the real Scanner.
	match := func(pattern, key string) bool { return strings.HasPrefix(key, "src/") && pattern == "src/*.ts" }
	wired := buildRestoredInstructions(bg(), r, d, match)
	require.Equal(t, "matched src/auth.ts; did not fit the rehydration budget", wired.units[0].drop.Detail)
}

// TestRestored_BodyIsWholeOrAbsent asserts the one place progressive truncation within a unit is
// deliberately not applied: a rule body is included whole, however long, because a partial
// instruction set is worse than an absent one (G4.3).
func TestRestored_BodyIsWholeOrAbsent(t *testing.T) {
	body := strings.Repeat("Every handler must validate the delivery id.\n", 40)
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	d := depsWith(&spyLogger{})
	d.Rules = &fakeScanner{pathScoped: []rules.Rule{rule(".claude/rules/x.md", body, "**")}}

	got := buildRestoredInstructions(bg(), r, d, nil)

	require.Equal(t, "### .claude/rules/x.md — paths: **\n"+strings.TrimRight(body, "\n")+"\n", got.units[0].text)
	require.NotContains(t, got.units[0].text, truncMark)
	require.True(t, guardNoContents(got.units[0]),
		"fixture sanity: the guard would reject this, and item 6a deliberately never runs it")
}

// TestRestored_ScanErrorDegrades asserts a failing half yields no rules and a reported reason,
// while the other half still contributes.
func TestRestored_ScanErrorDegrades(t *testing.T) {
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	log := &spyLogger{}
	d := depsWith(log)
	d.Rules = &fakeScanner{
		pathErr: errors.New("frontmatter unreadable"),
		nested:  []rules.Rule{rule("src/CLAUDE.md", "be idempotent")},
	}

	got := buildRestoredInstructions(bg(), r, d, nil)

	require.Equal(t, 1, got.seen)
	require.Equal(t, []string{"### src/CLAUDE.md — nested\nbe idempotent\n"}, unitTexts(got))
	require.Equal(t, []checkpoint.DropEntry{{
		Kind: dropKindPathRule, ID: "scan", Detail: "frontmatter unreadable",
	}}, got.drops)
	require.Equal(t, 1, log.warn)
}

// TestRestored_NilScanner asserts an unwired scanner reports both halves as unavailable rather
// than silently emitting nothing — G4.1 and G4.2 are about rules the agent no longer has, and a
// silent absence is exactly the failure mode.
func TestRestored_NilScanner(t *testing.T) {
	r := requestFor(t, ckFull(t), generousTestBudget)

	got := buildRestoredInstructions(bg(), r, depsWith(&spyLogger{}), nil)

	require.Empty(t, got.units)
	require.Len(t, got.drops, 2)
	require.Equal(t, dropKindPathRule, got.drops[0].Kind)
	require.Equal(t, dropKindNestedClaudeMD, got.drops[1].Kind)
}

// TestRestored_RuleWithNoGlobsHasNoDanglingClause asserts a path rule that declares no globs
// renders its path alone rather than "— paths: ".
func TestRestored_RuleWithNoGlobsHasNoDanglingClause(t *testing.T) {
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	d := depsWith(&spyLogger{})
	d.Rules = &fakeScanner{pathScoped: []rules.Rule{rule(".claude/rules/x.md", "body")}}

	got := buildRestoredInstructions(bg(), r, d, nil)

	require.Equal(t, "### .claude/rules/x.md\nbody\n", got.units[0].text)
}

// ── item 6b: skill index ──

// skill is a skills.Entry fixture.
func skill(name, desc string) skills.Entry { return skills.Entry{Name: name, Description: desc} }

// TestSkillIndex_RendersTheCompactIndex pins the two-call shape and the line format, and asserts
// the compact budget came from runtime.rehydrate.skillIndexTokens rather than a literal.
func TestSkillIndex_RendersTheCompactIndex(t *testing.T) {
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	idx := &fakeIndexer{
		all:  []skills.Entry{skill("code-review", "Review the current diff for correctness bugs.")},
		kept: []skills.Entry{skill("code-review", "Review the current diff for correctness bugs.")},
	}
	d := depsWith(&spyLogger{})
	d.Skills = idx

	got := buildSkillIndex(bg(), r, d, nil)

	require.Equal(t, []core.Tokens{0, core.Tokens(r.Cfg.Runtime.Rehydrate.SkillIndexTokens)}, idx.budgets,
		"budget 0 is the give-me-everything call; the second is the configured compact budget")
	require.Equal(t, []string{"- code-review: Review the current diff for correctness bugs.\n"}, unitTexts(got))
	require.Equal(t, 1, got.seen)
	require.Empty(t, got.drops)
}

// TestSkillIndex_DropsReportUnindexedSkills asserts a skill the compact index could not afford is
// NAMED. A model told the skill exists can invoke it; a model not told assumes it does not.
func TestSkillIndex_DropsReportUnindexedSkills(t *testing.T) {
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	d := depsWith(&spyLogger{})
	d.Skills = &fakeIndexer{
		all:  []skills.Entry{skill("code-review", "a"), skill("migration-runner", "b"), skill("release", "c")},
		kept: []skills.Entry{skill("code-review", "a")},
	}

	got := buildSkillIndex(bg(), r, d, nil)

	budget := itoa(r.Cfg.Runtime.Rehydrate.SkillIndexTokens)
	require.Equal(t, []checkpoint.DropEntry{
		{Kind: dropKindSkill, ID: "migration-runner", Detail: "not in the compact skill index (budget " + budget + " tokens)"},
		{Kind: dropKindSkill, ID: "release", Detail: "not in the compact skill index (budget " + budget + " tokens)"},
	}, got.drops)
}

// TestSkillIndex_WarnsOnHostHeadTruncation asserts §2.7's per-skill cap is surfaced. The host
// re-injects at most 5000 tokens of a skill body, head-first — so a bigger body comes back PARTIAL,
// and nothing else in the system tells the model that.
func TestSkillIndex_WarnsOnHostHeadTruncation(t *testing.T) {
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	entries := []skills.Entry{skill("code-review", "a"), skill("migration-runner", "b")}
	d := depsWith(&spyLogger{})
	d.Skills = &fakeIndexer{all: entries, kept: entries}

	got := buildSkillIndex(bg(), r, d, fakeBodyTokens(map[string]core.Tokens{
		"code-review": 400, "migration-runner": 7500,
	}))

	require.Equal(t, []checkpoint.DropEntry{{
		Kind: dropKindSkill, ID: "migration-runner",
		Detail: "body ~7500 tokens; the host re-injects at most 5000 per skill, head-first, so treat it as partial (§2.7)",
	}}, got.drops)
}

// TestSkillIndex_WarnsOnHostTotalCap asserts §2.7's 25000-token aggregate cap is surfaced too: past
// it the host drops the OLDEST skill bodies entirely, which is a different and worse failure than
// head truncation.
func TestSkillIndex_WarnsOnHostTotalCap(t *testing.T) {
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	entries := []skills.Entry{skill("a", "a"), skill("b", "b"), skill("c", "c")}
	d := depsWith(&spyLogger{})
	d.Skills = &fakeIndexer{all: entries, kept: entries}

	got := buildSkillIndex(bg(), r, d, fakeBodyTokens(map[string]core.Tokens{
		"a": 4000, "b": 4000, "c": 4000,
	}))
	require.Empty(t, got.drops, "12000 tokens is under the host's aggregate cap")

	got = buildSkillIndex(bg(), r, d, fakeBodyTokens(map[string]core.Tokens{
		"a": 4000, "b": 4000, "c": 20000,
	}))
	require.Equal(t, []checkpoint.DropEntry{
		{Kind: dropKindSkill, ID: "c", Detail: "body ~20000 tokens; the host re-injects at most 5000 per skill, head-first, so treat it as partial (§2.7)"},
		{Kind: dropKindSkill, ID: "*", Detail: "total skill bodies ~28000 tokens exceed the host's 25000-token cap; the oldest are dropped entirely (§2.7)"},
	}, got.drops)
}

// TestSkillIndex_BodyTokensErrorsAreSilent asserts a skill whose body size cannot be measured is
// skipped at Debug rather than reported as dropped: not knowing a size is not the same as knowing
// it is too big.
func TestSkillIndex_BodyTokensErrorsAreSilent(t *testing.T) {
	cp := ckEmpty()
	r := requestFor(t, cp, generousTestBudget)
	entries := []skills.Entry{skill("a", "a"), skill("gone", "b")}
	log := &spyLogger{}
	d := depsWith(log)
	d.Skills = &fakeIndexer{all: entries, kept: entries}

	got := buildSkillIndex(bg(), r, d, fakeBodyTokens(map[string]core.Tokens{"a": 100}))

	require.Empty(t, got.drops)
	require.Equal(t, 1, log.debug)
}

// TestSkillIndex_NilIndexerReportsTheAbsence asserts an unwired indexer is a reported degradation.
func TestSkillIndex_NilIndexerReportsTheAbsence(t *testing.T) {
	r := requestFor(t, ckEmpty(), generousTestBudget)

	got := buildSkillIndex(bg(), r, depsWith(&spyLogger{}), nil)

	require.Empty(t, got.units)
	require.Equal(t, []checkpoint.DropEntry{{
		Kind: dropKindSkill, ID: "unavailable",
		Detail: "no skill indexer is wired; the compact skill index was not restored",
	}}, got.drops)
}

// ── item 7: drop report ──

// TestDropReport_OrdersPathRulesFirst pins kindRank. Path rules and nested CLAUDE.md files sort
// first because they are operating rules the agent no longer has — precisely the thing G4.5 says
// nothing surfaces today — and an unknown kind sorts last rather than anywhere.
func TestDropReport_OrdersPathRulesFirst(t *testing.T) {
	got := buildDropReport([]checkpoint.DropEntry{
		{Kind: "narrative", ID: "n1"},
		{Kind: dropKindPointer, ID: "p1"},
		{Kind: "a_kind_from_the_future", ID: "z"},
		{Kind: dropKindNestedClaudeMD, ID: "src/CLAUDE.md"},
		{Kind: dropKindSkill, ID: "s1"},
		{Kind: dropKindPathRule, ID: ".claude/rules/db.md", Detail: "matched src/db/pool.ts; did not fit the rehydration budget"},
	})

	require.Equal(t, []string{
		"- path_rule .claude/rules/db.md — matched src/db/pool.ts; did not fit the rehydration budget\n",
		"- nested_claude_md src/CLAUDE.md\n",
		"- skill s1\n",
		"- pointer p1\n",
		"- narrative n1\n",
		"- a_kind_from_the_future z\n",
	}, unitTexts(got))
	require.Equal(t, 6, got.seen)
}

// TestDropReport_SortsByIDWithinAKind asserts the within-kind order is ID ascending, so the report
// is stable across replays.
func TestDropReport_SortsByIDWithinAKind(t *testing.T) {
	got := buildDropReport([]checkpoint.DropEntry{
		{Kind: dropKindSkill, ID: "zeta"},
		{Kind: dropKindSkill, ID: "alpha"},
		{Kind: dropKindSkill, ID: "*"},
	})

	require.Equal(t, []string{"- skill *\n", "- skill alpha\n", "- skill zeta\n"}, unitTexts(got))
}

// TestDropReport_CollapsesSameKindEmptyID asserts the counted-summary rule: entries sharing a Kind
// and carrying no ID are already counted summaries, so repeating them would spend budget saying
// the same thing twice. An entry WITH an id is never collapsed away.
func TestDropReport_CollapsesSameKindEmptyID(t *testing.T) {
	got := buildDropReport([]checkpoint.DropEntry{
		{Kind: dropKindElimination, ID: "", Detail: "15 of 23 not shown; call already_tried(target, approach) or dropped()"},
		{Kind: dropKindElimination, ID: "", Detail: "a second summary of the same kind"},
		{Kind: dropKindElimination, ID: "elim_x", Detail: "did not fit"},
		{Kind: dropKindDecision, ID: "", Detail: "a different kind is not collapsed into it"},
	})

	require.Equal(t, []string{
		"- elimination — 15 of 23 not shown; call already_tried(target, approach) or dropped()\n",
		"- elimination elim_x — did not fit\n",
		"- decision — a different kind is not collapsed into it\n",
	}, unitTexts(got))
}

// TestDropReport_EmptyYieldsNoSection asserts a rehydration that dropped nothing emits no item 7.
func TestDropReport_EmptyYieldsNoSection(t *testing.T) {
	got := buildDropReport(nil)

	require.Empty(t, got.units)
	require.Equal(t, "", itemText(ItemDropReport, 0, 0, unitTexts(got)))
}

// TestMoreDropsUnit_IsThePricedCountedTail asserts the truncated-report tail names the affordance
// that makes it recoverable, and prices itself — the budget pass appends it after pricing.
func TestMoreDropsUnit_IsThePricedCountedTail(t *testing.T) {
	got := moreDropsUnit(Deps{Tokens: fakeEstimator{}}, 12)

	require.Equal(t, "- … and 12 more; call dropped()\n", got.text)
	require.Positive(t, int(got.tokens), "the tail is priced, not free")
}

// ── item 8: affordance ──

// TestAffordance_IsOneFixedUnit asserts item 8 is a single never-truncated line: it is what turns a
// payload full of pointers into something the model can act on.
func TestAffordance_IsOneFixedUnit(t *testing.T) {
	got := buildAffordance(bg(), requestFor(t, ckEmpty(), generousTestBudget), depsWith(&spyLogger{}))

	require.Equal(t, []string{AffordanceNotice() + "\n"}, unitTexts(got))
	require.Equal(t, 1, got.seen)
	require.True(t, isFixedUnit(got.units[0]))
}

// ── the no-contents guard ──

// TestGuardNoContents pins both rejection conditions and the shapes that must pass. §13 invariant 5
// forbids code snippets in a checkpoint; this is the same rule at the injection boundary.
func TestGuardNoContents(t *testing.T) {
	cases := map[string]struct {
		text string
		want bool
	}{
		"one line":                 {"- a.ts — why\n", false},
		"exactly the line ceiling": {strings.Repeat("x\n", maxUnitLines), false},
		"one line over":            {strings.Repeat("x\n", maxUnitLines+1), true},
		"fence at the start":       {"```\ncode\n```\n", true},
		"indented fence":           {"- a.ts\n   ```go\n", true},
		"backticks mid-line":       {"- a.ts — see `foo()` for why\n", false},
		// The regression case. Items 4/5/6 collapse their free-text fields onto one line before
		// the guard ever sees them, so a pasted fence no longer BEGINS a line — a line-prefix test
		// passes it straight through, which is how a pasted diff would have reached the payload.
		"fence flattened onto one line": {"- a.ts — ```ts export function f() {} ```\n", true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, guardNoContents(unit{text: tc.text}))
		})
	}
}

// ── buildAll ──

// TestBuildAll_CoversEveryKindButTheDropReport asserts the aggregator builds every item the drop
// report reports ON, and leaves item 7 to Build, which assembles it last.
func TestBuildAll_CoversEveryKindButTheDropReport(t *testing.T) {
	cp := ckFull(t)
	r := requestFor(t, cp, generousTestBudget)

	all := buildAll(bg(), r, fullDeps(t, cp), nil)

	for _, k := range renderOrder {
		_, ok := all[k]
		if k == ItemDropReport {
			require.False(t, ok, "item 7 is assembled by Build, after everything it reports on")
			continue
		}
		require.True(t, ok, "buildAll must build %s", k)
	}
}

// ── shared helpers ──

// TestTruncRunes_CountsRunesAndKeepsTheMarkInside asserts the mark counts against the limit, so a
// field documented as "at most N runes" really is, and that the cut lands on a rune boundary — a
// half rune reaches the model as U+FFFD.
func TestTruncRunes_CountsRunesAndKeepsTheMarkInside(t *testing.T) {
	require.Equal(t, "abc", truncRunes("abc", 5, truncMark))
	require.Equal(t, "ab"+truncMark, truncRunes("abcdef", 3, truncMark))
	require.Equal(t, "", truncRunes("abc", 0, truncMark))

	got := truncRunes(strings.Repeat("é", 10), 4, truncMark)
	require.Equal(t, strings.Repeat("é", 3)+truncMark, got)
	require.Equal(t, 4, len([]rune(got)))
}

// TestOneLine_CollapsesEveryNewlineForm asserts CRLF, CR and LF all collapse, so a Windows-authored
// checkpoint field cannot break a list rendering.
func TestOneLine_CollapsesEveryNewlineForm(t *testing.T) {
	require.Equal(t, "a b c d", oneLine("a\r\nb\rc\nd"))
	require.Equal(t, "a", oneLine("  a  "))
	require.Equal(t, "", oneLine("\n\n"))
}

// TestQuoteLines_PrefixesEveryLine asserts item 2's blockquote rendering ends in exactly one
// newline, whatever the input's trailing whitespace.
func TestQuoteLines_PrefixesEveryLine(t *testing.T) {
	require.Equal(t, "> a\n> b\n", quoteLines("a\nb"))
	require.Equal(t, "> a\n> b\n", quoteLines("a\nb\n"))
	require.Equal(t, "> \n> a\n", quoteLines("\na"))
}
