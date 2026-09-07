package rules

import (
	"bytes"
	"strings"
)

// maxFrontmatterBytes is how far into a file Parse looks for the closing `---`. A rule file whose
// frontmatter has not terminated inside 8 KiB is not a rule file with frontmatter; it is a
// markdown document that happens to open with a horizontal rule, and scanning the rest of it
// looking for a terminator that may never arrive is work the hot path cannot afford.
const maxFrontmatterBytes = 8192

// frontDelim is the fence that opens and closes a frontmatter block.
const frontDelim = "---"

// Front is the subset of a rule file's YAML frontmatter that Qompack models. Every other key is
// ignored rather than rejected, so a rule file written for a newer Claude Code still scopes
// correctly here.
type Front struct {
	// Paths is the `paths:` globs the rule is scoped to, in file order, unquoted but otherwise
	// verbatim — original case included, because Rule.Globs reports them back to the caller.
	Paths []string
	// Description is the `description:` value, unquoted and single-line.
	Description string
	// Present reports whether a well-formed frontmatter block was found: opened with `---` on the
	// very first line and closed with `---` inside maxFrontmatterBytes.
	Present bool
}

// Parse reads the frontmatter block at the head of b and returns it plus the body offset.
// It never returns an error: a malformed block yields Front{Present:false} and body=0, which
// makes the file an unscoped rule that Claude Code re-injects itself (§2.7) — correctly ignored.
//
// The parse is deliberately a line scanner rather than a YAML unmarshal. internal/rules is
// foundation-only, the four shapes below are the complete set Claude Code rule files use, and a
// real YAML parser would turn every hand-edited rule file's stray tab into a hard error where the
// contract here is to degrade to "unscoped" instead.
func Parse(b []byte) (Front, int) {
	open := openerLen(b)
	if open == 0 {
		return Front{}, 0
	}
	limit := min(len(b), maxFrontmatterBytes)

	var f Front
	// inList is set by a `paths:` key with no inline value, and cleared by the first line that is
	// not a `- item` continuation. It is what separates the block-sequence form from the two
	// single-line forms without a lookahead.
	inList := false
	for pos := open; pos < limit; {
		line, next := nextLine(b, pos, limit)
		trimmed := strings.TrimSpace(line)

		// The terminator has to be tested before the list continuation: "---" trimmed also looks
		// like a `-` item whose value is "--".
		if trimmed == frontDelim {
			f.Present = true
			return f, next
		}
		if inList {
			if item, ok := listItem(trimmed); ok {
				f.Paths = appendGlob(f.Paths, item)
				pos = next
				continue
			}
			inList = false
		}

		key, value, ok := strings.Cut(trimmed, ":")
		if ok {
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "paths":
				inList = parsePaths(&f, strings.TrimSpace(value))
			case "description":
				f.Description = unquote(strings.TrimSpace(value))
			}
		}
		pos = next
	}
	return Front{}, 0
}

// openerLen returns the length of b's opening `---` line, or 0 when b does not open one. The
// fence must be the very first thing in the file: a leading blank line or an indented `---` is
// markdown, not frontmatter.
func openerLen(b []byte) int {
	switch {
	case bytes.HasPrefix(b, []byte(frontDelim+"\r\n")):
		return len(frontDelim) + 2
	case bytes.HasPrefix(b, []byte(frontDelim+"\n")):
		return len(frontDelim) + 1
	default:
		return 0
	}
}

// nextLine returns the line starting at pos (without its terminator) and the offset of the line
// after it, both bounded by limit.
func nextLine(b []byte, pos, limit int) (string, int) {
	if i := bytes.IndexByte(b[pos:limit], '\n'); i >= 0 {
		return string(b[pos : pos+i]), pos + i + 1
	}
	return string(b[pos:limit]), limit
}

// parsePaths applies the `paths:` value to f and reports whether a block sequence follows: an
// empty value opens one, `[a, b]` is a flow sequence, and anything else is a single glob.
func parsePaths(f *Front, value string) bool {
	switch {
	case value == "":
		return true
	case strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]"):
		for _, part := range strings.Split(value[1:len(value)-1], ",") {
			f.Paths = appendGlob(f.Paths, part)
		}
	default:
		f.Paths = appendGlob(f.Paths, value)
	}
	return false
}

// listItem returns the value of a `- item` block-sequence entry in an already-trimmed line, and
// reports whether the line was one. A bare `-` with no value is not.
func listItem(trimmed string) (string, bool) {
	rest, ok := strings.CutPrefix(trimmed, "-")
	if !ok {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	return rest, rest != ""
}

// appendGlob unquotes raw and appends it to globs unless it is empty. Empty elements are dropped
// rather than kept, because an empty glob matches nothing and would only ever make a scoped rule
// look scoped to one more thing than it is.
func appendGlob(globs []string, raw string) []string {
	if v := unquote(strings.TrimSpace(raw)); v != "" {
		return append(globs, v)
	}
	return globs
}

// unquote strips surrounding whitespace and then ONE matching pair of single or double quotes.
// The inside of the quotes is returned verbatim: a glob may legitimately contain leading or
// trailing spaces, and quoting it is exactly how an author says so.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
