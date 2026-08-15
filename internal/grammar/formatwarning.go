package grammar

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// FormatWarning renders w as the one-line, no-trailing-newline text SP-08 injects through
// UserPromptSubmit and SP-15 asserts byte-for-byte (00-ARCHITECTURE.md §5.11 leaves the wording
// open; SP-01 closes it here). It is fully specified by the architecture, so SP-01 implements it
// for real rather than stubbing it (00-ARCHITECTURE.md §14.1): SP-08 needs a working formatter
// long before SP-15's real Sequitur exists to produce a genuine Warning.
//
// Example: a Warning{Rule: Rule{Expansion: [Read Edit Bash]}, Repeats: 11, Turns: [42..74],
// Message: "consider a different approach"} renders as:
//
//	[qompack] possible loop: Read→Edit→Bash repeated 11× (turns 42–74) — consider a different approach
func FormatWarning(w Warning) string {
	return fmt.Sprintf("[qompack] possible loop: %s repeated %d× (turns %s) — %s",
		strings.Join(symbolStrings(w.Rule.Expansion), "→"), w.Repeats, turnRange(w.Turns), w.Message)
}

// symbolStrings converts syms to their string form, in order, for strings.Join.
func symbolStrings(syms []Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = string(s)
	}
	return out
}

// turnRange renders turns as a human-readable span: "42–74" (en dash, U+2013) when turns covers
// more than one distinct value, or "42" when it names a single turn. It renders the minimum and
// maximum of turns, rather than its first and last elements, so the result is correct regardless
// of the order turns was built in; an empty turns renders as the empty string, a degenerate case
// FormatWarning's own callers are not expected to hit in practice.
func turnRange(turns []core.TurnIndex) string {
	if len(turns) == 0 {
		return ""
	}
	lo, hi := turns[0], turns[0]
	for _, t := range turns[1:] {
		if t < lo {
			lo = t
		}
		if t > hi {
			hi = t
		}
	}
	if lo == hi {
		return strconv.Itoa(int(lo))
	}
	return fmt.Sprintf("%d–%d", lo, hi) // en dash, U+2013 — see FormatWarning's example.
}
