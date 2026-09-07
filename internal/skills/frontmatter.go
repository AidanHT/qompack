// Duplicated from internal/rules/frontmatter.go by §3.2 import policy — do not "fix".
//
// internal/rules and internal/skills are both foundation-only packages (00-ARCHITECTURE.md §3.2):
// neither may import the other, and hoisting a shared parser into internal/core would edit a
// package a different subplan owns. The two files are therefore deliberate near-copies of one
// parser; this one is narrowed to the only two keys a skill index consumes, `name:` and
// `description:`. Every other key a SKILL.md may legitimately carry (allowed-tools, model, …) is
// read past and discarded.

package skills

import "strings"

// maxFrontmatterBytes caps how far into a file the frontmatter scan reads while looking for the
// closing delimiter. A block that is still open at the end of that window is reported as absent
// rather than as an error, so a pathological file costs a bounded scan and still yields a usable
// Entry from its file name.
const maxFrontmatterBytes = 8192

// frontmatterDelim is the exact line content that both opens and closes a frontmatter block.
const frontmatterDelim = "---"

// keyName and keyDescription are the only frontmatter keys skillMeta retains.
const (
	keyName        = "name"
	keyDescription = "description"
)

// skillMeta is the subset of a skill file's frontmatter the index needs. Both fields are empty
// when the file has no frontmatter, when the block is malformed, or when the key is simply absent
// — the three cases are deliberately indistinguishable to callers, because all three fall back to
// the same place.
type skillMeta struct {
	// Name is the frontmatter `name:` value, unquoted and trimmed.
	Name string
	// Description is the frontmatter `description:` value, unquoted and cut at its first newline.
	Description string
}

// parseFrontmatter returns what a skill file's frontmatter declares and the byte offset at which
// the body begins.
//
// A file must open with a `---` line for a block to exist at all; the scan then runs to the next
// line whose content is exactly `---`. A file with no frontmatter, an unterminated block, or a
// block longer than maxFrontmatterBytes yields a zero skillMeta and a body offset of 0 — never an
// error. An unreadable header is a reason to fall back to the file's own name and first body
// line, not a reason to fail the whole index.
func parseFrontmatter(raw string) (skillMeta, int) {
	head, truncated := raw, false
	if len(head) > maxFrontmatterBytes {
		head, truncated = head[:maxFrontmatterBytes], true
	}

	open, pos, terminated := splitLine(head)
	if !terminated || open != frontmatterDelim {
		return skillMeta{}, 0
	}

	var meta skillMeta
	for pos < len(head) {
		line, n, lineTerminated := splitLine(head[pos:])
		if !lineTerminated && truncated {
			// The window cut a line in half: whatever follows is unknown, so the block counts as
			// unterminated rather than as ending here.
			return skillMeta{}, 0
		}
		pos += n
		if line == frontmatterDelim {
			return meta, pos
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case keyName:
			meta.Name = cleanValue(value)
		case keyDescription:
			meta.Description = cleanValue(value)
		}
	}
	return skillMeta{}, 0
}

// splitLine splits the first line off s: its content with any terminating CR and LF removed, the
// offset in s just past that terminator, and whether a terminator was actually present. The
// returned offset is always at least 1 for a non-empty s, so callers can loop on it safely.
func splitLine(s string) (line string, next int, terminated bool) {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSuffix(s[:i], "\r"), i + 1, true
	}
	return strings.TrimSuffix(s, "\r"), len(s), false
}

// cleanValue normalises one frontmatter scalar: surrounding whitespace removed, then one matching
// pair of double or single quotes stripped, then everything from the first newline on discarded.
// A frontmatter value is always a single line, and a `description:` in particular is what the
// index renders, so a stray newline must never reach an index line.
func cleanValue(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		if q := v[0]; (q == '"' || q == '\'') && v[len(v)-1] == q {
			v = v[1 : len(v)-1]
		}
	}
	if i := strings.IndexByte(v, '\n'); i >= 0 {
		v = v[:i]
	}
	return v
}
