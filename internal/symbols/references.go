package symbols

// maxNameBytes is the longest name References will count. A "name" longer than this is not an
// identifier any of the ten dialects in dialect.go can produce, so it is a caller bug or a
// corrupted record; counting it would cost a full scan and return zero either way.
const maxNameBytes = 256

// References counts, for each name in names, how many times it occurs in b as a whole identifier
// token — never as a substring of a longer one (00-ARCHITECTURE.md §5.22b). It is the cheap Δ
// proxy SP-15's analyzer scores candidates with, so its cost is stated as a contract: one pass over
// b, independent of len(names).
//
// That independence is the reason this is a hand-rolled scanner rather than a regex per name. The
// tokenizer walks b once, splitting it into [A-Za-z_$][A-Za-z0-9_$]* runs, and resolves each run
// through a single map lookup. Matching is case-sensitive, because every language dialect.go covers
// is.
//
// Three input-hygiene rules, all of them "absent", not "zero": an empty name, a name longer than
// maxNameBytes, and — after de-duplication — a repeated name each contribute at most one key. Every
// other requested name is present in the result with a count of 0 when it never occurs, so a caller
// ranging over its own name list never has to distinguish a missing key from an unseen name.
func (heuristicExtractor) References(b []byte, names []string) map[string]int {
	// index maps an accepted name to its slot in counts. Counting into a slice rather than into the
	// result map directly is what keeps the inner loop allocation-free: `index[string(tok)]` is the
	// compiler's non-allocating map-lookup-by-byte-slice form, whereas incrementing a map entry
	// keyed by the same conversion would materialize a string for every token in b.
	index := make(map[string]int, len(names))
	counts := make([]int, 0, len(names))
	for _, n := range names {
		if n == "" || len(n) > maxNameBytes {
			continue
		}
		if _, dup := index[n]; dup {
			continue
		}
		index[n] = len(counts)
		counts = append(counts, 0)
	}

	out := make(map[string]int, len(index))
	if len(index) == 0 {
		return out
	}

	for i := 0; i < len(b); {
		if !isIdentStart(b[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(b) && isIdentPart(b[j]) {
			j++
		}
		if k, ok := index[string(b[i:j])]; ok {
			counts[k]++
		}
		i = j
	}

	for name, k := range index {
		out[name] = counts[k]
	}
	return out
}

// isIdentStart reports whether c can begin an identifier token. `$` is included because JavaScript
// and TypeScript — two of the ten dialects — treat it as an ordinary identifier byte, and jQuery-
// style `$`-prefixed names are exactly the kind of symbol the analyzer wants to count.
func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isIdentPart reports whether c can continue an identifier token.
func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

// leadingIdentifier returns the maximal identifier run at the start of b, or an empty slice when b
// does not start with one. declRule.match uses it to settle a deny-set line without invoking the
// regex engine.
func leadingIdentifier(b []byte) []byte {
	if len(b) == 0 || !isIdentStart(b[0]) {
		return nil
	}
	n := 1
	for n < len(b) && isIdentPart(b[n]) {
		n++
	}
	return b[:n]
}
