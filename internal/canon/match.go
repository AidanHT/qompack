package canon

import "fmt"

// Match is one volatile span a Matcher found in the ORIGINAL input, together with the token that
// replaces it. It is the unit Registry.Run composes over.
//
// Composition is a single pass over the original bytes rather than a chain of byte transforms.
// Chaining would make Delta coordinates ambiguous — a later stage shifts an earlier stage's
// offsets — and would make Restore order-dependent. With matches, every offset in every Match is
// expressed in one coordinate system (the input's), Run resolves overlaps once, and non-growth
// becomes a structural property of the accept loop rather than something each canonicalizer has
// to remember.
type Match struct {
	// Offset is the span's byte offset in the input passed to Matches.
	Offset int
	// Len is the span's length in bytes in that same input. A zero-length Match is an insertion
	// and is always rejected.
	Len int
	// Token is the replacement bytes. It must be no longer than Len; a longer Token is rejected
	// by the non-growing guard rather than applied. Token may be empty, which deletes the span.
	Token []byte
	// Class is the category this Match belongs to. It must be one of KnownClasses: Run gates
	// accepted matches on Options.Strip by Class, so a Match carrying the zero Class is dropped.
	Class Class
}

// End returns Offset+Len, the first byte after the match.
func (m Match) End() int { return m.Offset + m.Len }

// Matcher is the half of a built-in Canonicalizer that Registry.Run composes over. Every
// canonicalizer Default registers implements it.
//
// Matches reports every span its rules find in in, with no regard for Options.Strip: gating on
// Strip is Run's job and happens once, centrally, in one accept loop. Doing it here instead would
// let a canonicalizer registered by a later subplan bypass the user's store.canonicalize.strip
// setting, and would spread one decision across fourteen implementations.
//
// Matches may return spans in any order and may return overlapping spans; Run sorts and resolves
// them. It must not return spans outside in.
type Matcher interface {
	Matches(in []byte, o Options) []Match
}

// MatchesOf returns c's matches over in, or ErrNotMatcher when c does not implement Matcher.
//
// Registry.Run calls this for every applicable Canonicalizer and skips the ones that report
// ErrNotMatcher, so a third-party Canonicalizer registered through Register neither breaks Run
// nor silently participates in composition it cannot express.
func MatchesOf(c Canonicalizer, in []byte, o Options) ([]Match, error) {
	m, ok := c.(Matcher)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotMatcher, c.Name())
	}
	return m.Matches(in, o), nil
}
