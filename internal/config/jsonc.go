package config

// StripJSONC converts JSONC (JSON with "//" line comments, "/* */" block comments, and trailing
// commas before a closing '}' or ']') into strict JSON that encoding/json can parse.
//
// Both passes replace bytes with spaces rather than deleting them, so the output is always the
// same length as the input and every surviving byte keeps its original offset. That is load-
// bearing: load.go re-scans this exact byte slice with a json.Decoder and InputOffset() to
// compute Provenance.Location "<path>:<line>", and a shifted offset would point at the wrong
// line the moment a file contained a comment above the line it is reporting.
func StripJSONC(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	blankComments(out)
	blankTrailingCommas(out)
	return out
}

// blankComments runs a single left-to-right pass with four states — normal, inside a string,
// inside a line comment, inside a block comment — replacing every comment byte (but never a
// newline, so line numbers survive) with a space. String contents, including a literal "//" or
// "/*" inside a JSON string value, are passed through untouched.
func blankComments(b []byte) {
	const (
		stNormal = iota
		stString
		stLineComment
		stBlockComment
	)
	state := stNormal
	escaped := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch state {
		case stNormal:
			switch {
			case c == '"':
				state = stString
			case c == '/' && i+1 < len(b) && b[i+1] == '/':
				b[i] = ' '
				b[i+1] = ' '
				i++
				state = stLineComment
			case c == '/' && i+1 < len(b) && b[i+1] == '*':
				b[i] = ' '
				b[i+1] = ' '
				i++
				state = stBlockComment
			}
		case stString:
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				state = stNormal
			}
		case stLineComment:
			if c == '\n' || c == '\r' {
				state = stNormal
				continue
			}
			b[i] = ' '
		case stBlockComment:
			if c == '*' && i+1 < len(b) && b[i+1] == '/' {
				b[i] = ' '
				b[i+1] = ' '
				i++
				state = stNormal
				continue
			}
			if c != '\n' && c != '\r' {
				b[i] = ' '
			}
		}
	}
}

// blankTrailingCommas runs a second, string-aware pass over the comment-blanked bytes: every
// comma outside a string that is followed only by whitespace before the next '}' or ']' is
// itself replaced with a space.
func blankTrailingCommas(b []byte) {
	const (
		stNormal = iota
		stString
	)
	state := stNormal
	escaped := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch state {
		case stNormal:
			switch {
			case c == '"':
				state = stString
			case c == ',':
				if j, ok := nextNonSpace(b, i+1); ok && (b[j] == '}' || b[j] == ']') {
					b[i] = ' '
				}
			}
		case stString:
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				state = stNormal
			}
		}
	}
}

// nextNonSpace returns the index of the first byte at or after from that is not JSON whitespace.
func nextNonSpace(b []byte, from int) (int, bool) {
	for i := from; i < len(b); i++ {
		switch b[i] {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return i, true
		}
	}
	return 0, false
}
