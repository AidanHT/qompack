package grammartest

import (
	"testing"

	"github.com/qompack/qompack/internal/grammar"
	"github.com/stretchr/testify/require"
)

// This file exists for one reason: SP-15 amended checkNoDigramTwice, and an amended grader is
// worthless unless someone proves it still bites.
//
// The amendment forgives an occurrence that OVERLAPS the previous one, because Sequitur's
// invariant is over non-overlapping occurrences and the original check was therefore unsatisfiable
// (see the note on checkNoDigramTwice). The risk of any such loosening is that it quietly becomes
// a check that accepts everything. These cases pin both halves: what must still fail, and what was
// only ever failing because the grader was wrong.

func seqOf(label string, syms ...grammar.Symbol) labeledSymbols {
	return labeledSymbols{label: label, symbols: syms}
}

// TestCheckNoDigramTwice_StillRejects is the load-bearing half. Every case here is a genuine
// invariant violation that a broken implementation could produce, and the amended checker must
// still catch all of them.
func TestCheckNoDigramTwice_StillRejects(t *testing.T) {
	cases := []struct {
		name string
		in   []labeledSymbols
	}{
		{
			// The plainest violation: one sequence, two disjoint occurrences, nothing factored.
			"two disjoint occurrences in one sequence",
			[]labeledSymbols{seqOf("Compressed()", "a", "b", "c", "a", "b")},
		},
		{
			// FOUR identical symbols hold two DISJOINT (x,x) pairs, at index 0 and index 2. A
			// correct Sequitur factors them; the amendment must not forgive this the way it
			// forgives three. This is the case that separates "non-overlapping" from "anything
			// adjacent".
			"four identical symbols hold two disjoint pairs",
			[]labeledSymbols{seqOf("Compressed()", "x", "x", "x", "x")},
		},
		{
			"six identical symbols",
			[]labeledSymbols{seqOf("Compressed()", "x", "x", "x", "x", "x", "x")},
		},
		{
			// Across sequences no occurrence can overlap another, so the same pair in the
			// top-level sequence and in a rule body is always a violation.
			"the same digram in Compressed() and in a rule body",
			[]labeledSymbols{
				seqOf("Compressed()", "a", "b", "z"),
				seqOf("Rule 1's Body", "q", "a", "b"),
			},
		},
		{
			"the same digram in two different rule bodies",
			[]labeledSymbols{
				seqOf("Rule 1's Body", "a", "b"),
				seqOf("Rule 2's Body", "a", "b"),
			},
		},
		{
			// An adjacent repeat in a DIFFERENT sequence is not an overlap: the indices are not
			// comparable across sequences, and the amendment must not confuse position with
			// identity.
			"adjacent indices in different sequences are not an overlap",
			[]labeledSymbols{
				seqOf("Rule 1's Body", "x", "x"),
				seqOf("Rule 2's Body", "x", "x"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, detail := checkNoDigramTwice(tc.in)
			require.False(t, ok, "this is a real invariant violation and must be reported")
			require.NotEmpty(t, detail, "a rejection must say which digram and where")
		})
	}
}

// TestCheckNoDigramTwice_ForgivesOnlyOverlap is the other half: exactly the states a CORRECT
// Sequitur legitimately reaches, and nothing more.
func TestCheckNoDigramTwice_ForgivesOnlyOverlap(t *testing.T) {
	cases := []struct {
		name string
		in   []labeledSymbols
	}{
		{"empty grammar", nil},
		{"a single symbol has no digram", []labeledSymbols{seqOf("Compressed()", "a")}},
		{
			// THE case from the fixture: "Read Edit Bash" three times induces S -> R R R, and the
			// two (R,R) pairs overlap at index 1. There is no other grammar for this input.
			"three identical rule references overlap and are one occurrence",
			[]labeledSymbols{
				seqOf("Compressed()", "\x00R2", "\x00R2", "\x00R2"),
				seqOf("Rule 2's Body", "Read", "Edit", "Bash"),
			},
		},
		{"three identical terminals overlap", []labeledSymbols{seqOf("Compressed()", "x", "x", "x")}},
		{"all-distinct symbols", []labeledSymbols{seqOf("Compressed()", "a", "b", "c", "d")}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, detail := checkNoDigramTwice(tc.in)
			require.True(t, ok, "a correct grammar must not be reported: %s", detail)
		})
	}
}

// TestCheckNoDigramTwice_OverlapDoesNotAdvanceTheSite pins the subtle half of the implementation.
//
// An occurrence that is forgiven as an overlap must NOT become the new reference point. If it did,
// each x in a long run would only ever be compared with the x immediately before it, every
// occurrence would look like an overlap, and a run of any length would pass — which is precisely
// the "check that accepts everything" failure this file exists to prevent. Measuring from the last
// COUNTED occurrence is what keeps "x x x" passing while "x x x x" fails.
func TestCheckNoDigramTwice_OverlapDoesNotAdvanceTheSite(t *testing.T) {
	run := []grammar.Symbol{"x", "x", "x"}
	ok, _ := checkNoDigramTwice([]labeledSymbols{seqOf("Compressed()", run...)})
	require.True(t, ok, "three is one countable occurrence")

	for n := 4; n <= 9; n++ {
		syms := make([]grammar.Symbol, n)
		for i := range syms {
			syms[i] = "x"
		}
		ok, detail := checkNoDigramTwice([]labeledSymbols{seqOf("Compressed()", syms...)})
		require.False(t, ok, "a run of %d holds disjoint occurrences and must be reported", n)
		require.NotEmpty(t, detail)
	}
}
