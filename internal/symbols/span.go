package symbols

import "bytes"

// Span computation for 00-ARCHITECTURE.md §5.22b.
//
// A Symbol's Offset is always the *start of its declaration line*, never the position of the name
// inside it, and its Len always runs from there to the end of whatever the declaration owns. That
// choice is what makes Enclosing usable as the §8.7 minimal-sufficient-span resolver: SP-13 hands
// the span straight to store.OpenSpan and shows it to a model, and a span that started mid-line
// would render as a fragment.

// lineIndex is the byte offset of every line start in a source buffer, computed once per Extract
// call. Every rule match, every indent comparison and every span end is expressed as a line index
// into it, so the source is scanned for newlines exactly once.
type lineIndex struct {
	src    []byte
	starts []int
}

// newLineIndex builds the line table for b. A trailing newline does not produce a final empty
// line: a file ending in "\n" has as many lines as it has newlines, which is what makes the 1-based
// Line numbers Extract reports agree with every editor.
func newLineIndex(b []byte) lineIndex {
	if len(b) == 0 {
		return lineIndex{src: b}
	}
	starts := make([]int, 1, 1+bytes.Count(b, newlineBytes))
	for i := 0; i < len(b); {
		j := bytes.IndexByte(b[i:], '\n')
		if j < 0 {
			break
		}
		i += j + 1
		if i == len(b) {
			break
		}
		starts = append(starts, i)
	}
	return lineIndex{src: b, starts: starts}
}

// count returns the number of lines.
func (li lineIndex) count() int { return len(li.starts) }

// bounds returns the half-open byte range of line i with its terminator removed — both the LF and,
// under CRLF, the CR before it. Stripping the CR here rather than at each call site is what makes
// every `$`-anchored rule in dialect.go behave identically on a CRLF checkout, which is the whole
// of the "Extract is stable under CRLF" contract.
func (li lineIndex) bounds(i int) (start, end int) {
	start = li.starts[i]
	if i+1 < len(li.starts) {
		end = li.starts[i+1] - 1
	} else {
		end = len(li.src)
		if end > start && li.src[end-1] == '\n' {
			end--
		}
	}
	if end > start && li.src[end-1] == '\r' {
		end--
	}
	return start, end
}

// line returns line i's bytes, terminator excluded.
func (li lineIndex) line(i int) []byte {
	start, end := li.bounds(i)
	return li.src[start:end]
}

// isRegexpSpace mirrors Go's regexp `\s` class, [\t\n\f\r ], exactly. The fidelity matters: it is
// what makes declRule's col0 and trimIndent filters provably equivalent to the `^` and `^\s{2,}`
// prefixes they stand in for, including on the malformed input FuzzExtract generates, where a bare
// CR or form feed can appear anywhere.
func isRegexpSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

// leadingWhitespaceBytes counts the raw leading whitespace *bytes* of line — bytes, not columns,
// because it stands in for a regex `\s` prefix rather than for an indentation comparison.
func leadingWhitespaceBytes(line []byte) int {
	n := 0
	for n < len(line) && isRegexpSpace(line[n]) {
		n++
	}
	return n
}

// leadingWidth returns line's indentation in columns, counting a tab as tabWidth
// (00-ARCHITECTURE.md §5.22b). Columns, not bytes, because a file that mixes tabs and spaces would
// otherwise produce nonsensical nesting: one tab is deeper than two spaces, not shallower.
func leadingWidth(line []byte) int {
	w := 0
	for _, c := range line {
		switch c {
		case ' ':
			w++
		case '\t':
			w += tabWidth
		default:
			return w
		}
	}
	return w
}

// spanEnd returns the exclusive end offset of the declaration starting on line i, per the dialect's
// block style. start and end are line i's own bounds, which the caller already computed. The result
// is always strictly greater than start, because every line that reached here matched a rule and
// therefore has content.
func spanEnd(li lineIndex, i, start, end int, d *dialect) int {
	switch d.style {
	case blockIndent:
		return indentSpanEnd(li, i, d)
	case blockBraces, blockAuto:
		if e, ok := scanBraceSpan(li.src, start, d); ok {
			return e
		}
		if d.style == blockAuto {
			return indentSpanEnd(li, i, d)
		}
	}
	// blockLine, and the blockBraces fallback for a declaration that owns no block: the
	// declaration line alone, newline exclusive.
	return end
}

// indentSpanEnd implements the indent block style: the span runs to the start of the first later
// line that is non-blank, is not a comment, and is indented no further than the declaration —
// exclusive — or to EOF when there is none.
//
// Blank and comment lines are skipped rather than treated as terminators because both routinely
// sit at column zero between two members of the same class, and treating either as the end of the
// block would truncate every Python class at its first blank line.
func indentSpanEnd(li lineIndex, i int, d *dialect) int {
	declWidth := leadingWidth(li.line(i))
	for j := i + 1; j < li.count(); j++ {
		text := li.line(j)
		trimmed := bytes.TrimSpace(text)
		if len(trimmed) == 0 || d.isLineComment(trimmed) {
			continue
		}
		if leadingWidth(text) > declWidth {
			continue
		}
		// Ruby's `end` closes the block it terminates, so it belongs inside the span rather than
		// after it. Without this the span of every `def` would stop one line short of its own
		// closing keyword, and SP-13 would show a model a method body with no terminator.
		if d.rubyEnd && isRubyEnd(trimmed) {
			if j+1 < li.count() {
				return li.starts[j+1]
			}
			return len(li.src)
		}
		return li.starts[j]
	}
	return len(li.src)
}

// isRubyEnd reports whether a trimmed line is a bare `end` or an `end` followed by something that
// cannot continue an identifier (`end if …`, `end # comment`) — as opposed to `endpoint`, which is
// an ordinary expression and closes nothing.
func isRubyEnd(trimmed []byte) bool {
	const kw = "end"
	if !bytes.HasPrefix(trimmed, []byte(kw)) {
		return false
	}
	if len(trimmed) == len(kw) {
		return true
	}
	return !isIdentPart(trimmed[len(kw)])
}

// scanBraceSpan implements the braces block style. It walks forward from start with a small
// lexical state machine — strings, character literals, line comments and block comments are all
// skipped — and reports the offset just past the brace that closes the first unquoted, uncommented
// `{` it finds.
//
// It reports false in exactly one case: no opening brace within braceLookaheadLines lines of the
// declaration, meaning the declaration owns no block (a `#define`, a const, an interface member).
// An opening brace that is never closed is *not* that case: the file simply ends mid-block, and the
// span runs to EOF, which is what makes a truncated tool-output capture still produce a usable span.
func scanBraceSpan(b []byte, start int, d *dialect) (int, bool) {
	depth, lines := 0, 0
	i := start
	for i < len(b) {
		c := b[i]
		// The overwhelming majority of source bytes are none of the eight the scanner cares about.
		// A table lookup is what keeps the inner loop to one indexed load per byte; everything
		// below this point runs at most once per token, not once per byte.
		if !scanSignificant[c] {
			i++
			continue
		}
		switch c {
		case '\n':
			i++
			// The lookahead window applies only before the block opens: once inside, a declaration
			// may be as long as it likes.
			if depth == 0 {
				lines++
				if lines > braceLookaheadLines {
					return 0, false
				}
			}
			continue
		case '{':
			depth++
			i++
			continue
		case '}':
			i++
			if depth > 0 {
				depth--
				if depth == 0 {
					return i, true
				}
			}
			continue
		}

		// A skipped construct — comment, string, character literal — may cross newlines, so those
		// are counted from the skipped range rather than seen by the '\n' case above.
		prev := i
		switch {
		case c == '/' && d.comments&commentSlash != 0 && i+1 < len(b) && b[i+1] == '/':
			i = skipLineComment(b, i)
		case c == '/' && d.blockComment && i+1 < len(b) && b[i+1] == '*':
			i = skipBlockComment(b, i+2)
		case c == '#' && d.comments&commentHash != 0:
			i = skipLineComment(b, i)
		case c == '"':
			i = skipStringLiteral(b, i)
		case c == '`':
			i = skipRawString(b, i)
		case c == '\'' && d.singleQuote == sqString:
			i = skipStringLiteral(b, i)
		case c == '\'':
			i = skipCharLiteral(b, i)
		default:
			// A significant byte this dialect assigns no meaning to (a `#` in Go, a `/` that opens
			// no comment) is ordinary code.
			i++
			continue
		}
		if depth == 0 {
			lines += bytes.Count(b[prev:i], newlineBytes)
			if lines > braceLookaheadLines {
				return 0, false
			}
		}
	}
	if depth > 0 {
		return len(b), true
	}
	return 0, false
}

// newlineBytes is the separator every bytes.Count call in this package uses. It is a package-level
// value only so the counting loops do not build it per call; nothing ever writes to it.
var newlineBytes = []byte{'\n'}

// scanSignificant marks the bytes scanBraceSpan has to make a decision about: the brace pair, the
// three quote characters, the two comment introducers, and the newline that drives the lookahead
// window. Every other byte is skipped by a single table lookup.
var scanSignificant = func() (t [256]bool) {
	for _, c := range []byte{'\n', '/', '#', '"', '\'', '`', '{', '}'} {
		t[c] = true
	}
	return t
}()

// skipLineComment returns the offset of the newline that ends the comment starting at i, or len(b).
// It stops *at* the newline so scanBraceSpan's own newline accounting still sees it.
func skipLineComment(b []byte, i int) int {
	if j := bytes.IndexByte(b[i:], '\n'); j >= 0 {
		return i + j
	}
	return len(b)
}

// skipBlockComment returns the offset just past the `*/` that closes a block comment whose body
// starts at i, or len(b) when it is unterminated.
func skipBlockComment(b []byte, i int) int {
	if j := bytes.Index(b[i:], []byte("*/")); j >= 0 {
		return i + j + 2
	}
	return len(b)
}

// skipStringLiteral returns the offset just past the quote closing the literal that opens at i,
// honouring backslash escapes. An unterminated literal is treated as ending at the line break:
// every dialect that reaches here forbids a raw newline inside a `"` or `'` literal, so a quote
// with no partner is far more likely to be an apostrophe in prose than a real string, and letting
// it run to EOF would swallow the rest of the file.
func skipStringLiteral(b []byte, i int) int {
	quote := b[i]
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++
		case '\n':
			return j
		case quote:
			return j + 1
		}
	}
	return len(b)
}

// skipRawString returns the offset just past the backtick closing the literal that opens at i.
// Backticked literals are multi-line by design (Go raw strings, JavaScript template literals,
// shell command substitution) and Go's raw strings process no escapes at all, so neither the
// newline stop nor the backslash handling of skipStringLiteral applies.
func skipRawString(b []byte, i int) int {
	if j := bytes.IndexByte(b[i+1:], '`'); j >= 0 {
		return i + 1 + j + 1
	}
	return len(b)
}

// skipCharLiteral returns the offset just past a character literal opening at i — or i+1, leaving
// the quote treated as ordinary code, when what follows does not look like one.
//
// The "does not look like one" branch is the whole reason this function exists rather than reusing
// skipStringLiteral. In Rust, `&'a str` and `impl<'a>` are lifetimes, not literals; treating that
// apostrophe as a string opener makes the scanner skip to the *next* apostrophe, which in a generic
// function signature is several tokens away and may well be past an opening brace.
func skipCharLiteral(b []byte, i int) int {
	if i+1 < len(b) && b[i+1] == '\\' {
		// An escaped literal: '\n', '\'', '\u{1F600}'. Skip the backslash and the byte it escapes,
		// then look for the closing quote within the bounded window.
		limit := i + charLiteralMaxBytes
		if limit > len(b) {
			limit = len(b)
		}
		for j := i + 3; j < limit; j++ {
			if b[j] == '\n' {
				break
			}
			if b[j] == '\'' {
				return j + 1
			}
		}
		return i + 1
	}
	if i+2 < len(b) && b[i+2] == '\'' {
		return i + 3
	}
	return i + 1
}
