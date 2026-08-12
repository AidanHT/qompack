package grammartest

import (
	"fmt"
	"testing"

	"github.com/qompack/qompack/internal/grammar"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertion of the grammartest suite (00-ARCHITECTURE.md §5.22
// table; §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md): Sequitur's two classical
// invariants — no digram appears twice, and every rule is used more than once — must hold after
// every single Append call, not just at the end of a batch. It is authored now, gated behind the
// same Rule W-1 stub probe as the rest of the suite, so SP-15 inherits it rather than writing its
// own grader.
//
// Both invariants are checked using only what 00-ARCHITECTURE.md §5.11 documents unambiguously:
// Rule.Uses (for the second invariant) and Symbol-value equality over Compressed() and every
// Rule.Body (for the first). §5.11 does not specify how a Body element that references another
// rule is encoded as a Symbol, so this check does not need to know: it treats every element of
// Compressed() and every Rule.Body as an opaque token and asks only whether the same adjacent
// pair of token values occurs more than once across the whole grammar (the top-level sequence
// plus every rule's own immediate body) — which is exactly the classical Sequitur invariant,
// independent of that encoding choice.

// digram is an adjacent pair of Symbol values, used as a map key.
type digram struct{ a, b grammar.Symbol }

// checkNoDigramTwice scans every sequence in seqs (Compressed() first, then each rule's Body, in
// Rules() order) and reports the first adjacent pair of Symbol values that occurs more than once
// across all of them combined, along with a description of where each occurrence was found.
func checkNoDigramTwice(labeled []labeledSymbols) (ok bool, detail string) {
	seen := make(map[digram]string, 16)
	for _, ls := range labeled {
		for i := 0; i+1 < len(ls.symbols); i++ {
			d := digram{ls.symbols[i], ls.symbols[i+1]}
			if first, dup := seen[d]; dup {
				return false, fmt.Sprintf("digram (%q,%q) appears in both %s and %s",
					d.a, d.b, first, ls.label)
			}
			seen[d] = ls.label
		}
	}
	return true, ""
}

// labeledSymbols pairs a symbol sequence with a human-readable label for failure messages.
type labeledSymbols struct {
	label   string
	symbols []grammar.Symbol
}

// sequencesOf gathers every sequence the no-digram-twice invariant must be checked over: the
// top-level Compressed() sequence, plus every currently-induced rule's own Body.
func sequencesOf(seq grammar.Sequitur) []labeledSymbols {
	out := []labeledSymbols{{label: "Compressed()", symbols: seq.Compressed()}}
	for _, r := range seq.Rules() {
		out = append(out, labeledSymbols{label: fmt.Sprintf("Rule %d's Body", r.ID), symbols: r.Body})
	}
	return out
}

// checkEveryRuleUsedMoreThanOnce reports whether every rule currently in the grammar has
// Uses > 1, and if not, which rule violates it.
func checkEveryRuleUsedMoreThanOnce(rules []grammar.Rule) (ok bool, detail string) {
	for _, r := range rules {
		if r.Uses <= 1 {
			return false, fmt.Sprintf("rule %d has Uses=%d, want >1 (a rule used once or never must be inlined, not kept)", r.ID, r.Uses)
		}
	}
	return true, ""
}

// thrashStream is a stream deliberately rich in repeated structure: a 3-symbol digram-chain
// repeated several times (which any correct Sequitur must factor into rules), a lone user turn,
// and a second, shorter repeated pair — enough to exercise rule creation, reuse, and (for a real
// implementation) rule-of-rules nesting.
func thrashStream() []grammar.Symbol {
	var out []grammar.Symbol
	for i := 0; i < 5; i++ {
		out = append(out, "Read", "Edit", "Bash")
	}
	out = append(out, "user")
	for i := 0; i < 4; i++ {
		out = append(out, "Grep", "Grep")
	}
	return out
}

// runInvariantsAfterEveryAppendCase feeds thrashStream into seq one Symbol at a time, checking
// both of Sequitur's invariants after every single Append call.
func runInvariantsAfterEveryAppendCase(t *testing.T, factory func(t *testing.T) grammar.Sequitur) {
	t.Helper()
	seq := factory(t)

	for i, s := range thrashStream() {
		seq.Append(s)

		okDigram, detailDigram := checkNoDigramTwice(sequencesOf(seq))
		require.True(t, okDigram, "after append #%d (%q): %s", i, s, detailDigram)

		okUses, detailUses := checkEveryRuleUsedMoreThanOnce(seq.Rules())
		require.True(t, okUses, "after append #%d (%q): %s", i, s, detailUses)
	}

	require.NotEmpty(t, seq.Rules(), "fixture sanity: a stream this repetitive must induce at least one rule")
}
