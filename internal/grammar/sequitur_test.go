package grammar

import (
	"fmt"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// These are the SP-15 grammar core's own tests. They sit in package grammar rather than
// grammar_test — unlike formatwarning_test.go next door — for one reason: the properties worth
// asserting about an incremental Sequitur are internal ones. That the digram index holds no stale
// pointer, that the rule-utility net never has to fire, that no map iteration reached an answer:
// none of that is visible from outside the package, and a black-box test that could only watch
// Rules() and Compressed() would be asserting the symptoms of correctness rather than correctness.
//
// grammartest is the black-box conformance suite and it stays the black-box conformance suite;
// this file is the white-box complement to it, plus the one property grammartest does not check at
// all and which everything else rests on: that the grammar still expands to exactly the stream
// that was appended.
//
// A NOTE ON THE OVERLAP EXCEPTION, because it is why checkNoRepeatedDigram below is not a copy of
// grammartest's checkNoDigramTwice. Sequitur's digram-uniqueness invariant is about NON-OVERLAPPING
// occurrences. In "aaa" the pair (a,a) sits at 0-1 and at 1-2 sharing the middle symbol, and no
// rule can replace both: replacing one leaves a rule used exactly once, which rule utility inlines
// straight back. The exception is not this implementation's convenience — for a stream ending
// "u G G G" NO grammar at all satisfies both invariants at once. The three G's must occupy three
// adjacent top-level slots, since the only repeated substring containing a G is "G" itself and a
// rule used twice needs its expansion to occur twice without overlap; and among three adjacent
// slots you cannot avoid a repeated pair without a rule that spans a single symbol, which Sequitur
// never mints. See plans/sdd/V5-SP-15/report-A.md.

// ----------------------------------------------------------------------------------------------
// Streams under test.
// ----------------------------------------------------------------------------------------------

// syms builds a Symbol stream from strings, so the fixtures below read as the action logs they
// stand in for.
func syms(ss ...string) []Symbol {
	out := make([]Symbol, 0, len(ss))
	for _, s := range ss {
		out = append(out, Symbol(s))
	}
	return out
}

// repeatRun returns n copies of one symbol: the adversarial shape, because a run is where digram
// overlap lives and where the binary rule hierarchy either forms or does not.
func repeatRun(n int, s Symbol) []Symbol {
	out := make([]Symbol, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, s)
	}
	return out
}

// repeatBlock returns n copies of a block of symbols: the loop shape the L2 analyzer exists to
// detect (00-ARCHITECTURE.md §8.3 — the same short action sequence over and over).
func repeatBlock(n int, block ...Symbol) []Symbol {
	var out []Symbol
	for i := 0; i < n; i++ {
		out = append(out, block...)
	}
	return out
}

// conformanceStream is a transcription of grammartest.thrashStream. It cannot be imported —
// grammartest imports grammar, so grammar cannot import grammartest — and it is repeated here
// because it is the exact input the conformance suite drives, and this file's job is to say
// precisely how this implementation behaves on it.
func conformanceStream() []Symbol {
	out := repeatBlock(5, "Read", "Edit", "Bash")
	out = append(out, "user")
	out = append(out, repeatBlock(4, "Grep", "Grep")...)
	return out
}

// pseudoRandomStream returns a deterministic stream over a small alphabet. The generator is a
// xorshift written out here rather than math/rand because the point of the fixture is that it is
// the SAME stream on every machine and every Go release: a grammar test that silently changed its
// input would be a test that silently stopped testing what it used to.
func pseudoRandomStream(seed uint64, n int, alphabet ...Symbol) []Symbol {
	x := seed | 1
	out := make([]Symbol, 0, n)
	for i := 0; i < n; i++ {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		out = append(out, alphabet[x%uint64(len(alphabet))])
	}
	return out
}

// streamCases is the corpus every invariant in this file is checked against.
func streamCases() []struct {
	name   string
	stream []Symbol
} {
	return []struct {
		name   string
		stream []Symbol
	}{
		{"empty", nil},
		{"single", syms("Read")},
		{"no_repetition", syms("Read", "Edit", "Bash", "user", "Grep")},
		{"aaaa", repeatRun(4, "a")},
		{"aaa_odd_run", repeatRun(3, "a")},
		{"run_of_2_to_33", repeatRun(33, "a")},
		{"abab", repeatBlock(2, "a", "b")},
		{"abcabcabc", repeatBlock(3, "a", "b", "c")},
		{"conformance_stream", conformanceStream()},
		{"tight_loop", repeatBlock(12, "Read", "Edit", "Bash", "test:fail")},
		{"loop_with_progress", append(repeatBlock(6, "Read", "Edit", "Bash"), syms("user", "Write", "test:pass")...)},
		{"random_alphabet_2", pseudoRandomStream(1, 300, "a", "b")},
		{"random_alphabet_3", pseudoRandomStream(2, 300, "Read", "Edit", "Bash")},
		{"random_alphabet_8", pseudoRandomStream(3, 500, "Read", "Edit", "Bash", "Grep", "Glob", "user", "test:pass", "test:fail")},
	}
}

// ----------------------------------------------------------------------------------------------
// Invariant checkers.
// ----------------------------------------------------------------------------------------------

// labelled pairs a symbol sequence with a name, for failure messages.
type labelled struct {
	label string
	syms  []Symbol
}

// grammarSequences gathers every sequence digram uniqueness ranges over: the top-level sequence
// plus every rule body. A rule body is grammar too, which is why a digram may not appear once in
// Compressed() and once inside a rule either.
func grammarSequences(g *sequiturGrammar) []labelled {
	out := []labelled{{label: "Compressed()", syms: g.Compressed()}}
	for _, r := range g.Rules() {
		out = append(out, labelled{label: fmt.Sprintf("rule %d body", r.ID), syms: r.Body})
	}
	return out
}

// checkNoRepeatedDigram asserts Sequitur's first invariant with its classical overlap exception:
// no digram occurs twice anywhere in the grammar, except that two occurrences sharing a symbol
// (which can only happen for a pair of equal symbols) count as one.
func checkNoRepeatedDigram(t *testing.T, g *sequiturGrammar, ctx string) {
	t.Helper()
	type where struct {
		seq, pos int
	}
	seen := make(map[[2]Symbol]where, 32)
	seqs := grammarSequences(g)
	for si, ls := range seqs {
		for i := 0; i+1 < len(ls.syms); i++ {
			d := [2]Symbol{ls.syms[i], ls.syms[i+1]}
			prev, dup := seen[d]
			if !dup {
				seen[d] = where{si, i}
				continue
			}
			if prev.seq == si && prev.pos+1 == i {
				continue // overlapping occurrences of a pair of equal symbols
			}
			require.Failf(t, "digram uniqueness violated",
				"%s: digram (%q,%q) occurs at %s[%d] and again at %s[%d]",
				ctx, d[0], d[1], seqs[prev.seq].label, prev.pos, ls.label, i)
		}
	}
}

// checkRuleUtility asserts Sequitur's second invariant: every rule is referenced more than once.
// A rule used once compresses nothing and costs an indirection, and it would make Thrash report a
// "repeat" that never repeated.
func checkRuleUtility(t *testing.T, g *sequiturGrammar, ctx string) {
	t.Helper()
	for _, r := range g.Rules() {
		require.Greaterf(t, r.Uses, 1, "%s: rule %d has Uses=%d, want >1", ctx, r.ID, r.Uses)
	}
}

// checkRuleShape asserts the derived fields agree with the primary ones: Span is the expansion
// length, the expansion is the body expanded, and every reference in a body resolves to a rule
// that exists.
func checkRuleShape(t *testing.T, g *sequiturGrammar, ctx string) {
	t.Helper()
	rules := g.Rules()
	byID := make(map[RuleID]Rule, len(rules))
	for _, r := range rules {
		byID[r.ID] = r
	}
	for i, r := range rules {
		if i > 0 {
			require.Greaterf(t, r.ID, rules[i-1].ID, "%s: Rules() is not ascending by ID", ctx)
		}
		require.NotEmptyf(t, r.Body, "%s: rule %d has an empty body", ctx, r.ID)
		require.Equalf(t, len(r.Expansion), r.Span, "%s: rule %d Span disagrees with Expansion", ctx, r.ID)
		require.Equalf(t, expandThrough(t, byID, r.Body, ctx), r.Expansion,
			"%s: rule %d Expansion is not its Body expanded", ctx, r.ID)
	}
}

// checkLossless asserts the property nothing else here would catch: the grammar still describes
// the stream that was appended. Compressed() expanded through Rules() must be the input, exactly.
// An analyzer that compressed a session into a grammar that no longer expands to what happened
// would be writing fiction into the checkpoint.
func checkLossless(t *testing.T, g *sequiturGrammar, want []Symbol, ctx string) {
	t.Helper()
	byID := make(map[RuleID]Rule)
	for _, r := range g.Rules() {
		byID[r.ID] = r
	}
	got := expandThrough(t, byID, g.Compressed(), ctx)
	if len(want) == 0 {
		require.Emptyf(t, got, "%s: expected an empty expansion", ctx)
		return
	}
	require.Equalf(t, want, got, "%s: the grammar no longer expands to the appended stream", ctx)
}

// expandThrough expands a sequence of encoded symbols to terminals through byID.
func expandThrough(t *testing.T, byID map[RuleID]Rule, seq []Symbol, ctx string) []Symbol {
	t.Helper()
	var out []Symbol
	for _, s := range seq {
		id, isRef := ParseRuleRef(s)
		if !isRef {
			out = append(out, s)
			continue
		}
		r, ok := byID[id]
		require.Truef(t, ok, "%s: sequence references rule %d, which Rules() does not report", ctx, id)
		out = append(out, r.Expansion...)
	}
	return out
}

// checkIndexIntegrity asserts the digram index holds no stale pointer: every entry names a node
// that is still in some body, and the digram that node actually begins is the key it is filed
// under.
//
// This is the check that would catch the class of bug an incremental Sequitur is most exposed to.
// Every splice destroys some digrams and creates others, and an entry left behind pointing at a
// pair that no longer exists there would, several appends later, cause a rule to be minted over
// symbols that were never adjacent — corrupting the grammar in a way Rules() would report with a
// straight face.
func checkIndexIntegrity(t *testing.T, g *sequiturGrammar, ctx string) {
	t.Helper()
	live := make(map[*node]bool)
	walk := func(r *rule) {
		for n := r.guard.next; !n.isGuard(); n = n.next {
			live[n] = true
		}
	}
	walk(g.start)
	for _, id := range g.sortedIDs() {
		walk(g.byID[id])
	}
	for k, n := range g.index {
		require.Truef(t, live[n], "%s: index entry points at a node no body holds", ctx)
		have, ok := digramAt(n)
		require.Truef(t, ok, "%s: index entry points at a node that begins no digram", ctx)
		require.Equalf(t, k, have, "%s: index entry is filed under the wrong digram", ctx)
	}
}

// checkStructural runs every invariant that does not need to know what was appended.
func checkStructural(t *testing.T, g *sequiturGrammar, ctx string) {
	t.Helper()
	checkNoRepeatedDigram(t, g, ctx)
	checkRuleUtility(t, g, ctx)
	checkRuleShape(t, g, ctx)
	checkIndexIntegrity(t, g, ctx)
}

// checkAll runs every invariant against g, including that it still expands to appended.
func checkAll(t *testing.T, g *sequiturGrammar, appended []Symbol, ctx string) {
	t.Helper()
	checkStructural(t, g, ctx)
	checkLossless(t, g, appended, ctx)
}

// ----------------------------------------------------------------------------------------------
// Tests.
// ----------------------------------------------------------------------------------------------

// TestSequitur_InvariantsHoldAfterEveryAppend is the central test of this package, and it checks
// after EVERY append rather than at the end of a batch, for the reason grammartest gives: every
// consumer of this analyzer reads it mid-session, so a grammar that were only correct at
// quiescence would be wrong every time anyone looked at it.
func TestSequitur_InvariantsHoldAfterEveryAppend(t *testing.T) {
	for _, tc := range streamCases() {
		t.Run(tc.name, func(t *testing.T) {
			g := newSequiturGrammar()
			checkStructural(t, g, "before any append")
			for i, s := range tc.stream {
				g.Append(s)
				checkAll(t, g, tc.stream[:i+1], fmt.Sprintf("after append #%d (%q)", i, s))
			}
		})
	}
}

// TestSequitur_ConformanceStreamInducesRules pins what the conformance suite's own fixture
// produces. The stream is deliberately rich in repeated structure, so an implementation that
// induced nothing from it would be a stub wearing a real implementation's name — which is exactly
// what grammartest's stub probe tests for.
func TestSequitur_ConformanceStreamInducesRules(t *testing.T) {
	g := newSequiturGrammar()
	for _, s := range conformanceStream() {
		g.Append(s)
	}

	rules := g.Rules()
	require.NotEmpty(t, rules, "a stream this repetitive must induce rules")
	checkAll(t, g, conformanceStream(), "conformance stream, fully appended")

	// The five Read/Edit/Bash rounds and the eight Greps both collapse into nested rules, so the
	// top level is a handful of symbols rather than twenty-three.
	require.Less(t, len(g.Compressed()), len(conformanceStream()),
		"the compressed history must be shorter than the raw one")
}

// TestSequitur_Determinism asserts contract §4's determinism requirement: identical Append
// sequences produce identical Rules(), Compressed() and Snapshot(). Go randomizes map iteration
// order on every run, so a single execution of this test is already a fresh draw against every map
// this package holds.
func TestSequitur_Determinism(t *testing.T) {
	for _, tc := range streamCases() {
		t.Run(tc.name, func(t *testing.T) {
			build := func() *sequiturGrammar {
				g := newSequiturGrammar()
				for _, s := range tc.stream {
					g.Append(s)
				}
				return g
			}
			a, b := build(), build()
			require.Equal(t, a.Rules(), b.Rules())
			require.Equal(t, a.Compressed(), b.Compressed())
			require.Equal(t, a.Snapshot(), b.Snapshot())
			for minUses := 0; minUses < 5; minUses++ {
				require.Equal(t, a.Thrash(minUses), b.Thrash(minUses), "Thrash(%d)", minUses)
			}
		})
	}
}

// TestSequitur_Reset asserts that a reset grammar is indistinguishable from a fresh one — rule ids
// included. A retained id high-water mark would make a replayed session produce different ids
// depending on what the process had done beforehand.
func TestSequitur_Reset(t *testing.T) {
	stream := conformanceStream()

	used := newSequiturGrammar()
	for _, s := range stream {
		used.Append(s)
	}
	require.NotEmpty(t, used.Rules())

	used.Reset()
	require.Nil(t, used.Rules(), "Reset must leave no rules")
	require.Nil(t, used.Compressed(), "Reset must leave no sequence")
	require.Nil(t, used.Thrash(0), "Reset must leave nothing to report as thrashing")
	require.Equal(t, newSequiturGrammar().Snapshot(), used.Snapshot(), "a reset grammar is a fresh one")

	fresh := newSequiturGrammar()
	for _, s := range stream {
		used.Append(s)
		fresh.Append(s)
	}
	require.Equal(t, fresh.Snapshot(), used.Snapshot(), "replay after Reset must reproduce the same grammar")
}

// TestSequitur_ThrashThreshold pins both halves of Thrash's filter (00-ARCHITECTURE.md §5.11):
// strictly more uses than minUses, and an expansion of at least two terminals.
func TestSequitur_ThrashThreshold(t *testing.T) {
	g := newSequiturGrammar()
	for _, s := range repeatBlock(8, "Read", "Edit", "Bash") {
		g.Append(s)
	}

	all := g.Rules()
	require.NotEmpty(t, all)

	for minUses := 0; minUses <= 8; minUses++ {
		got := g.Thrash(minUses)
		var want []Rule
		for _, r := range all {
			if r.Uses > minUses && len(r.Expansion) >= 2 {
				want = append(want, r)
			}
		}
		require.Equal(t, want, got, "Thrash(%d)", minUses)
		for _, r := range got {
			require.Greater(t, r.Uses, minUses, "Thrash(%d) returned a rule used %d times", minUses, r.Uses)
			require.GreaterOrEqual(t, r.Span, 2, "Thrash must not report a single-terminal rule")
		}
	}

	// A rule used exactly n times is reported by Thrash(n-1) and withheld by Thrash(n): "exceeds"
	// is strict, so the boundary is worth pinning rather than inferring.
	top := all[len(all)-1]
	require.Contains(t, g.Thrash(top.Uses-1), top)
	require.NotContains(t, g.Thrash(top.Uses), top)
}

// TestSequitur_ThrashIgnoresUnrepeatedWork asserts the negative case that makes the warning worth
// injecting at all: a session that never repeated a pattern produces no thrash candidates.
func TestSequitur_ThrashIgnoresUnrepeatedWork(t *testing.T) {
	g := newSequiturGrammar()
	for _, s := range syms("user", "Read", "Edit", "Bash", "test:pass", "Write", "Glob") {
		g.Append(s)
	}
	require.Empty(t, g.Thrash(1), "an unrepeated session must report no thrashing")
}

// TestSequitur_SmallGrammarsAreExact pins the whole grammar for inputs small enough to derive by
// hand, which is the only way to catch an implementation that keeps its invariants while inducing
// the wrong grammar.
func TestSequitur_SmallGrammarsAreExact(t *testing.T) {
	cases := []struct {
		name       string
		stream     []Symbol
		wantRules  []Rule
		wantSeqLen int
	}{
		{
			name:   "aaaa_pairs_up",
			stream: repeatRun(4, "a"),
			wantRules: []Rule{
				{ID: 1, Body: syms("a", "a"), Uses: 2, Expansion: syms("a", "a"), Span: 2},
			},
			wantSeqLen: 2,
		},
		{
			name:   "abab_folds_the_pair",
			stream: repeatBlock(2, "a", "b"),
			wantRules: []Rule{
				{ID: 1, Body: syms("a", "b"), Uses: 2, Expansion: syms("a", "b"), Span: 2},
			},
			wantSeqLen: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newSequiturGrammar()
			for _, s := range tc.stream {
				g.Append(s)
			}
			require.Equal(t, tc.wantRules, g.Rules())
			require.Len(t, g.Compressed(), tc.wantSeqLen)
			checkAll(t, g, tc.stream, tc.name)
		})
	}
}

// TestSequitur_RetiresUnderUsedRules asserts rule utility where it actually bites: "abcabcabc"
// mints an intermediate rule for (a,b) and then retires it when the third round folds the whole
// block into one rule, so the surviving grammar names the block and nothing else.
func TestSequitur_RetiresUnderUsedRules(t *testing.T) {
	g := newSequiturGrammar()
	for _, s := range repeatBlock(3, "a", "b", "c") {
		g.Append(s)
	}

	rules := g.Rules()
	require.Len(t, rules, 1, "only the block rule survives; the intermediate pair rule is inlined")
	require.Equal(t, syms("a", "b", "c"), rules[0].Expansion)
	require.Equal(t, 3, rules[0].Uses)
	require.Len(t, g.Compressed(), 3, "three references to the one block rule")
	checkAll(t, g, repeatBlock(3, "a", "b", "c"), "abcabcabc")

	// The retired rule's id is spent, not recycled: ids are minted monotonically so that two runs
	// over one stream agree, and reusing a retired id would make an id mean two different rules
	// within one session.
	require.Greater(t, g.Snapshot().NextID, rules[0].ID)
}

// TestSequitur_UtilitySweepIsANet records what the end-of-append rule-utility sweep is for. The
// in-cascade repair inherited from the original algorithm covers one site; the sweep covers the
// invariant. If this ever fails, the sweep has stopped being a net and become load-bearing, which
// is worth knowing rather than discovering through a conformance failure.
func TestSequitur_UtilitySweepIsANet(t *testing.T) {
	for _, tc := range streamCases() {
		t.Run(tc.name, func(t *testing.T) {
			g := newSequiturGrammar()
			for _, s := range tc.stream {
				g.Append(s)
			}
			require.Zero(t, g.utilitySweeps,
				"the in-cascade repair left %d rule(s) for the sweep to inline", g.utilitySweeps)
		})
	}
}

// TestSequitur_SnapshotRestoreRoundTrip asserts the A-side of the codec seam (contract §4) without
// going through the codec at all: Snapshot and Restore have to be a faithful pair on their own, or
// a round-trip failure would be impossible to attribute between the two roles.
func TestSequitur_SnapshotRestoreRoundTrip(t *testing.T) {
	for _, tc := range streamCases() {
		t.Run(tc.name, func(t *testing.T) {
			src := newSequiturGrammar()
			for _, s := range tc.stream {
				src.Append(s)
			}
			snap := src.Snapshot()

			dst := newSequiturGrammar()
			dst.Append("unrelated")
			dst.Append("prior state")
			require.NoError(t, dst.Restore(snap))

			require.Equal(t, src.Rules(), dst.Rules())
			require.Equal(t, src.Compressed(), dst.Compressed())
			require.Equal(t, src.Snapshot(), dst.Snapshot())
			for minUses := 0; minUses < 5; minUses++ {
				require.Equal(t, src.Thrash(minUses), dst.Thrash(minUses), "Thrash(%d)", minUses)
			}
			checkAll(t, dst, tc.stream, "restored grammar")

			// A restored grammar is a working grammar, not a read-only record: appending to it must
			// produce exactly what appending to the original would have.
			more := syms("Read", "Edit", "Bash", "Read", "Edit", "Bash")
			for _, s := range more {
				src.Append(s)
				dst.Append(s)
			}
			require.Equal(t, src.Snapshot(), dst.Snapshot(), "restored grammar diverged on further appends")
			checkAll(t, dst, append(append([]Symbol{}, tc.stream...), more...), "restored grammar, extended")
		})
	}
}

// TestSequitur_SnapshotIsACopy asserts a snapshot is a value, not a window: mutating what Snapshot
// handed back must not reach the live grammar the next Append splices.
func TestSequitur_SnapshotIsACopy(t *testing.T) {
	g := newSequiturGrammar()
	for _, s := range repeatBlock(4, "Read", "Edit") {
		g.Append(s)
	}
	before := g.Snapshot()

	snap := g.Snapshot()
	snap.Sequence[0] = "tampered"
	snap.Rules[0].Body[0] = "tampered"
	snap.Rules[0].Expansion[0] = "tampered"
	snap.NextID = 999

	require.Equal(t, before, g.Snapshot(), "mutating a snapshot must not reach the grammar")
}

// TestSequitur_RestoreRefusesCorruptSnapshots asserts the compatibility behaviour contract §4
// requires: an impossible snapshot reports core.ErrDegraded and leaves the receiver EXACTLY as it
// was. Silently accepting any of these would put a grammar into the session that no induction
// could have produced, and a half-restored one would look like a session that did half the work.
func TestSequitur_RestoreRefusesCorruptSnapshots(t *testing.T) {
	cases := []struct {
		name string
		snap Snapshot
	}{
		{
			name: "reference to a rule that does not exist",
			snap: Snapshot{Sequence: []Symbol{RuleRef(1), RuleRef(1)}, NextID: 1},
		},
		{
			name: "rule id below the first induced id",
			snap: Snapshot{
				Rules:    []Rule{{ID: 0, Body: syms("a", "b")}},
				Sequence: []Symbol{RuleRef(0), RuleRef(0)},
				NextID:   1,
			},
		},
		{
			name: "rule ids not ascending",
			snap: Snapshot{
				Rules: []Rule{
					{ID: 2, Body: syms("a", "b")},
					{ID: 1, Body: syms("c", "d")},
				},
				Sequence: []Symbol{RuleRef(1), RuleRef(2), RuleRef(1), RuleRef(2)},
				NextID:   3,
			},
		},
		{
			name: "duplicate rule ids",
			snap: Snapshot{
				Rules: []Rule{
					{ID: 1, Body: syms("a", "b")},
					{ID: 1, Body: syms("c", "d")},
				},
				Sequence: []Symbol{RuleRef(1), RuleRef(1)},
				NextID:   2,
			},
		},
		{
			name: "empty rule body",
			snap: Snapshot{
				Rules:    []Rule{{ID: 1}},
				Sequence: []Symbol{RuleRef(1), RuleRef(1)},
				NextID:   2,
			},
		},
		{
			name: "NextID would collide with an existing rule",
			snap: Snapshot{
				Rules:    []Rule{{ID: 1, Body: syms("a", "b")}},
				Sequence: []Symbol{RuleRef(1), RuleRef(1)},
				NextID:   1,
			},
		},
		{
			name: "rule nothing references",
			snap: Snapshot{
				Rules:    []Rule{{ID: 1, Body: syms("a", "b")}},
				Sequence: syms("c", "d"),
				NextID:   2,
			},
		},
		{
			name: "self-referential rule",
			snap: Snapshot{
				Rules:    []Rule{{ID: 1, Body: []Symbol{"a", RuleRef(1)}}},
				Sequence: []Symbol{RuleRef(1), RuleRef(1)},
				NextID:   2,
			},
		},
		{
			name: "mutually referential rules",
			snap: Snapshot{
				Rules: []Rule{
					{ID: 1, Body: []Symbol{"a", RuleRef(2)}},
					{ID: 2, Body: []Symbol{"b", RuleRef(1)}},
				},
				Sequence: []Symbol{RuleRef(1), RuleRef(1)},
				NextID:   3,
			},
		},
	}

	intact := func() *sequiturGrammar {
		g := newSequiturGrammar()
		for _, s := range repeatBlock(3, "Read", "Edit") {
			g.Append(s)
		}
		return g
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := intact()
			before := g.Snapshot()

			err := g.Restore(tc.snap)
			require.Error(t, err)
			require.ErrorIs(t, err, core.ErrDegraded, "the only legal refusal is ErrDegraded")
			require.Equal(t, before, g.Snapshot(), "a refused restore must leave the receiver unchanged")

			// And the grammar still works afterwards, which is the point of refusing rather than
			// half-applying: a degraded read costs the checkpoint, not the session.
			g.Append("Read")
			checkStructural(t, g, "after a refused restore")
		})
	}
}

// TestSequitur_RestoreAcceptsAnEmptySnapshot asserts the boundary case a fresh checkpoint hits:
// an empty grammar is a legal grammar, not a corrupt one.
func TestSequitur_RestoreAcceptsAnEmptySnapshot(t *testing.T) {
	g := newSequiturGrammar()
	for _, s := range repeatBlock(3, "Read", "Edit") {
		g.Append(s)
	}
	require.NoError(t, g.Restore(Snapshot{NextID: 1}))
	require.Nil(t, g.Rules())
	require.Nil(t, g.Compressed())
	require.Equal(t, newSequiturGrammar().Snapshot(), g.Snapshot())

	// And the zero Snapshot too. A grammar with no rules has nothing for NextID to collide with,
	// so a decoder that did not write the field has not corrupted anything — and refusing it would
	// make the empty grammar the one value the codec seam could fail to round-trip.
	for _, s := range repeatBlock(3, "Read", "Edit") {
		g.Append(s)
	}
	require.NoError(t, g.Restore(Snapshot{}))
	require.Equal(t, newSequiturGrammar().Snapshot(), g.Snapshot())

	// A restored empty grammar is a working one: it mints ids from the start, exactly as a fresh
	// grammar does.
	fresh := newSequiturGrammar()
	for _, s := range repeatBlock(3, "Read", "Edit") {
		g.Append(s)
		fresh.Append(s)
	}
	require.Equal(t, fresh.Snapshot(), g.Snapshot())
}

// TestSequitur_RestoreCarriesAnUnderUsedRuleAndTheNextAppendSettlesIt pins the one place Restore
// deliberately does not re-impose an invariant. A peer that hands over a rule used once has given
// us odd evidence, not corrupt evidence; rewriting another process's record on read would be the
// worse failure. The next Append is where the grammar becomes ours again.
func TestSequitur_RestoreCarriesAnUnderUsedRuleAndTheNextAppendSettlesIt(t *testing.T) {
	g := newSequiturGrammar()
	snap := Snapshot{
		Rules:    []Rule{{ID: 1, Body: syms("a", "b")}},
		Sequence: []Symbol{"x", RuleRef(1), "y"},
		NextID:   2,
	}
	require.NoError(t, g.Restore(snap))
	require.Len(t, g.Rules(), 1)
	require.Equal(t, 1, g.Rules()[0].Uses, "the under-used rule is carried, not rewritten, on read")

	g.Append("z")
	require.Empty(t, g.Rules(), "the next append inlines it")
	require.Equal(t, syms("x", "a", "b", "y", "z"), g.Compressed())
	require.Positive(t, g.utilitySweeps, "the sweep is what settled it")
	checkStructural(t, g, "after settling a restored under-used rule")
}

// TestSequitur_NewReturnsTheRealImplementation guards the seam SP-01 left behind: New must hand
// back the induction, not the stub, because grammartest decides whether to run its behaviour block
// by probing exactly this.
func TestSequitur_NewReturnsTheRealImplementation(t *testing.T) {
	var s Sequitur = New()
	for _, sym := range repeatBlock(4, "Read", "Edit", "Bash") {
		s.Append(sym)
	}
	require.NotEmpty(t, s.Rules(), "New must return a Sequitur that induces rules")
	require.NotEmpty(t, s.Compressed())
}
