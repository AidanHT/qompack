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

// New returns an Extractor implementing the heuristic, parser-free extraction of
// 00-ARCHITECTURE.md §5.22b: one ordered list of anchored regexes per language family (dialect.go),
// a brace/indent span scanner (span.go) and a single-pass identifier counter (references.go).
//
// New has no error return, matching every other "computational" constructor in this codebase
// (chunk.New, grammar.New, redact.New): building an Extractor performs no I/O, allocates nothing
// that can fail, and reads no configuration. Every regex is compiled once at package
// initialization, so the returned value is stateless, immutable and safe for concurrent use by any
// number of goroutines — which is what lets SP-06's store, SP-07's dag and SP-13's mcp layer each
// hold one without coordinating.
func New() Extractor {
	return heuristicExtractor{}
}

// heuristicExtractor is the §5.22b implementation. It is an empty struct on purpose: all of its
// state — the dialect table, the compiled regexes, the deny-set — is package-level and immutable,
// so there is nothing per-instance to hold and nothing to guard.
type heuristicExtractor struct{}
