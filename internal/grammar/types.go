package grammar

import "github.com/qompack/qompack/internal/core"

// Symbol is one token of the action stream Sequitur folds into a grammar: a tool name, the
// literal "user" for a user turn, or a test-outcome marker such as "test:pass"/"test:fail"
// (00-ARCHITECTURE.md §5.11).
type Symbol string

// RuleID identifies one grammar rule Sequitur has induced.
type RuleID int

// Rule is one induced grammar rule: its own ID, the digram (or longer body) it was formed from,
// how many times it has been used, and its full expansion back to terminal Symbols.
type Rule struct {
	// ID identifies this rule.
	ID RuleID
	// Body is the rule's immediate right-hand side: a sequence of Symbols and/or other rules'
	// references, exactly as Sequitur's grammar induction produced it.
	Body []Symbol
	// Uses counts how many times this rule has been referenced.
	Uses int
	// Expansion is Body fully expanded back to terminal Symbols.
	Expansion []Symbol
	// Span is the number of terminal Symbols Expansion covers.
	Span int
}

// Warning is one loop-detection result: the thrashing Rule, how many times it repeated, the
// turns it spanned, and a human-readable message. FormatWarning renders it for injection through
// UserPromptSubmit.
type Warning struct {
	// Rule is the thrashing rule.
	Rule Rule
	// Repeats is how many times Rule's expansion repeated.
	Repeats int
	// Message is a short, human-readable suggestion.
	Message string
	// Turns lists the turn indices the repeats spanned.
	Turns []core.TurnIndex
}
