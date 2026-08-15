package symbols

// Extractor discovers symbols in source text (00-ARCHITECTURE.md §5.22b).
type Extractor interface {
	// Extract returns every symbol found in b, sourced from path (used only to select a
	// language-specific heuristic; nothing is read from disk).
	Extract(path string, b []byte) []Symbol
	// Enclosing returns the smallest symbol span containing off — the minimal-sufficient-span
	// resolver behind retrieval.defaultSpan = "minimal" (Qompack.md §8.7).
	Enclosing(path string, b []byte, off int) (Symbol, bool)
	// References counts, for each name in names, how many times it appears as a reference in b.
	References(b []byte, names []string) map[string]int
}

// New returns an Extractor. Constructing always succeeds, so wave-0 composition roots can wire a
// symbols.Extractor today, but every operation is a stub until SP-04 lands the real regex/
// brace-scanning implementation (00-ARCHITECTURE.md §5.22b).
//
// New has no error return, matching every other "computational" constructor in this codebase
// (chunk.New, grammar.New, redact.New): building an Extractor performs no I/O by itself, so there
// is nothing for a stub constructor to fail at.
func New() Extractor {
	return stubExtractor{}
}

// stubExtractor is the SP-01 placeholder Extractor. SP-04 owns the real implementation.
type stubExtractor struct{}

// Extract always returns nil. Extract has no error return, so nil — Rule 1's documented zero
// value for a no-error-return stub method — is the only honest answer until SP-04 lands: a stub
// Extractor has found nothing, and any non-nil result would be exactly the "plausible-looking
// fake data" Rule 2 forbids.
func (stubExtractor) Extract(path string, b []byte) []Symbol { return nil }

// Enclosing always reports (Symbol{}, false). Enclosing has no error return, and false is the
// safe "no enclosing symbol found" answer: reporting true with a fabricated Symbol would actively
// mislead every caller downstream of retrieval.defaultSpan = "minimal" into widening (or
// narrowing) a span around content that was never actually resolved — the same false-positive
// concern documented on sketch.Bloom.Test.
func (stubExtractor) Enclosing(path string, b []byte, off int) (Symbol, bool) { return Symbol{}, false }

// References always returns nil. References has no error return, so nil — the same "found
// nothing" answer as Extract — is the only honest response until SP-04 lands.
func (stubExtractor) References(b []byte, names []string) map[string]int { return nil }
