package symbols

import "slices"

// MaxExtractBytes is the largest prefix of a source buffer Extract examines: 4 MiB
// (00-ARCHITECTURE.md §5.22b, "Bounds and caps"). Beyond it, Extract silently stops looking rather
// than returning an error, because §5.22b gives it no error return and because the alternative —
// scanning an arbitrarily large tool-output capture on the observer's hot path — is the failure
// mode the cap exists to prevent. A symbol declared past the cut is simply not found; every symbol
// that *is* returned still satisfies Offset+Len <= len(b) against the original, unclipped buffer.
const MaxExtractBytes = 4 << 20

// MaxSymbols is the largest number of symbols Extract returns from one buffer. It is a
// denial-of-service bound on a generated or minified file — 20 000 one-line declarations is already
// far past the point where a symbol index is useful — not a tunable, which is why it is a constant
// here rather than an Appendix C key.
const MaxSymbols = 20000 //nomagic:allow symbol-count cap, not a config value

// initialSymbolCap is the capacity Extract's result slice starts at. Most files have a handful of
// declarations; the slice grows from here for the ones that do not.
const initialSymbolCap = 16

// Extract returns every symbol the dialect selected by path's extension finds in b, sorted by
// Offset ascending and, on a tie, by Len descending — so a class always precedes its own methods
// (00-ARCHITECTURE.md §5.22b).
//
// The result is always non-nil, including for the `none` dialect (Markdown, JSON, YAML, …) and for
// empty input: consumers range over it and index into b with Offset and Len, and a nil-versus-empty
// distinction would be one more thing four packages across three waves each had to get right.
func (heuristicExtractor) Extract(path string, b []byte) []Symbol {
	d := dialectFor(path)
	out := make([]Symbol, 0, initialSymbolCap)
	if len(b) == 0 || (len(d.rules) == 0 && !d.goBlocks) {
		return out
	}

	src := b
	if len(src) > MaxExtractBytes {
		src = src[:MaxExtractBytes]
	}
	li := newLineIndex(src)

	// blockKind is the Kind a Go `var (` / `const (` group's members inherit, or "" outside one.
	// Grouped members are the one Go declaration form no line-anchored rule can recognize on its
	// own: the line `\tdefaultName = "unnamed"` carries no keyword at all, and its Kind lives four
	// lines up on the `const (` that opened the group.
	blockKind := ""

	for i := 0; i < li.count() && len(out) < MaxSymbols; i++ {
		start, end := li.bounds(i)
		line := li.src[start:end]

		if d.goBlocks {
			if blockKind != "" {
				if m := goBlockMemberRe.FindSubmatchIndex(line); m != nil {
					out = appendSymbol(out, li, i, start, end, string(line[m[2]:m[3]]), blockKind, d)
				} else if isGroupClose(line) {
					blockKind = ""
				}
				continue
			}
			if m := goBlockOpenRe.FindSubmatchIndex(line); m != nil {
				blockKind = string(line[m[2]:m[3]])
				continue
			}
		}

		for ri := range d.rules {
			r := &d.rules[ri]
			name, ok := r.match(line)
			if !ok {
				continue
			}
			out = appendSymbol(out, li, i, start, end, name, r.kind, d)
			break
		}
	}

	// Line-ordered iteration already produces the required order, so the common path is the linear
	// IsSortedFunc check and no sort at all. The sort stays because the ordering is a published
	// contract (§5.22b) that four packages rely on, and a future rule that emits more than one
	// symbol per line must not be able to break it silently.
	if !slices.IsSortedFunc(out, compareSymbols) {
		slices.SortStableFunc(out, compareSymbols)
	}
	return out
}

// compareSymbols is §5.22b's result ordering: Offset ascending, then Len descending, so a class
// precedes every method declared inside it.
func compareSymbols(a, b Symbol) int {
	if a.Offset != b.Offset {
		return a.Offset - b.Offset
	}
	return b.Len - a.Len
}

// appendSymbol computes the span for the declaration on line i — whose own bounds the caller
// already has, and passes in rather than recomputing — and appends it, dropping any degenerate
// result. The Len > 0 guard is the last line of defence behind §5.22b's bounds contract: consumers
// slice b[Offset : Offset+Len] unchecked, so a zero-length span would be a silently empty excerpt
// and a negative one a panic.
func appendSymbol(out []Symbol, li lineIndex, i, start, lineEnd int, name, kind string, d *dialect) []Symbol {
	end := spanEnd(li, i, start, lineEnd, d)
	if end <= start {
		return out
	}
	return append(out, Symbol{
		Name:   name,
		Kind:   kind,
		Line:   i + 1,
		Offset: start,
		Len:    end - start,
	})
}

// isGroupClose reports whether a line is the lone `)` that closes a Go grouped declaration.
func isGroupClose(line []byte) bool {
	for _, c := range line {
		switch c {
		case ' ', '\t':
		case ')':
			return true
		default:
			return false
		}
	}
	return false
}

// Enclosing returns the smallest symbol span containing off — the minimal-sufficient-span resolver
// behind retrieval.defaultSpan = "minimal" (Qompack.md §8.7). A tie on Len is broken by the larger
// Offset, which is the later and therefore more specific of two equally sized candidates.
//
// It reports false, with the zero Symbol, when off is outside b or when no declaration covers it —
// an offset in a file's license header or between two functions genuinely has no minimal sufficient
// span, and inventing the nearest one would hand SP-13 a span the user never asked about.
func (h heuristicExtractor) Enclosing(path string, b []byte, off int) (Symbol, bool) {
	if off < 0 || off >= len(b) {
		return Symbol{}, false
	}
	var best Symbol
	found := false
	for _, s := range h.Extract(path, b) {
		if off < s.Offset || off >= s.Offset+s.Len {
			continue
		}
		if !found || s.Len < best.Len || (s.Len == best.Len && s.Offset > best.Offset) {
			best, found = s, true
		}
	}
	return best, found
}
