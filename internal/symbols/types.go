package symbols

// Symbol is one heuristically discovered source symbol (00-ARCHITECTURE.md §5.22b): a name, a
// coarse kind, and its location.
type Symbol struct {
	// Name is the symbol's identifier.
	Name string
	// Kind is one of "func", "type", "class", "const", or "var".
	Kind string
	// Line is the symbol's 1-based line number.
	Line int
	// Offset is the symbol's byte offset in the source it was extracted from.
	Offset int
	// Len is the symbol's span length in bytes, from Offset to the end of its declaration
	// (function body, type body, …).
	Len int
}
