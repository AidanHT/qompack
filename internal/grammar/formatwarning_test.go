package grammar_test

import (
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/stretchr/testify/require"
)

// TestFormatWarning pins grammar.FormatWarning's exact wording (00-ARCHITECTURE.md §5.11, §14.1
// of plans/V1-SP-01-foundation-toolchain-and-contracts.md): SP-08 injects this string through
// UserPromptSubmit and SP-15 asserts it byte-for-byte, so SP-01 freezes it here. Unlike every
// other function in this package, FormatWarning is real (not a stub), so this test runs
// unconditionally — it is never gated behind a Rule W-1 skip.
func TestFormatWarning(t *testing.T) {
	cases := []struct {
		name string
		w    grammar.Warning
		want string
	}{
		{
			name: "span_of_turns_uses_en_dash",
			w: grammar.Warning{
				Rule:    grammar.Rule{Expansion: []grammar.Symbol{"Read", "Edit", "Bash"}},
				Repeats: 11,
				Message: "consider a different approach",
				Turns:   []core.TurnIndex{42, 74},
			},
			// This is the exact golden string 00-ARCHITECTURE.md §14.1 specifies: the "×" after
			// the repeat count is U+00D7 (multiplication sign), "→" joining symbols is U+2192
			// (rightwards arrow), the turn-range dash is U+2013 (en dash), and the dash before the
			// message is U+2014 (em dash).
			want: "[qompack] possible loop: Read→Edit→Bash repeated 11× (turns 42–74) — consider a different approach",
		},
		{
			name: "single_turn_has_no_dash",
			w: grammar.Warning{
				Rule:    grammar.Rule{Expansion: []grammar.Symbol{"Bash"}},
				Repeats: 3,
				Message: "check for a stuck retry",
				Turns:   []core.TurnIndex{7},
			},
			want: "[qompack] possible loop: Bash repeated 3× (turns 7) — check for a stuck retry",
		},
		{
			name: "unordered_turns_still_span_min_to_max",
			w: grammar.Warning{
				Rule:    grammar.Rule{Expansion: []grammar.Symbol{"Grep", "Read"}},
				Repeats: 5,
				Message: "loop detected",
				Turns:   []core.TurnIndex{10, 3, 6},
			},
			want: "[qompack] possible loop: Grep→Read repeated 5× (turns 3–10) — loop detected",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := grammar.FormatWarning(tc.w)
			require.Equal(t, tc.want, got)
			require.NotContains(t, got, "\n", "FormatWarning must be one line with no trailing newline")
		})
	}
}
