package rules

import (
	"path"
	"strings"
)

// MaxPatternSegments bounds how many path segments a `paths:` glob may contain. The segment
// matcher backtracks across `**`, so an adversarial (or merely careless) rule file could otherwise
// turn one hook invocation into a stall; a pattern past the cap is rejected outright instead. No
// real rule file comes close — 32 is deeper than any project tree Qompack has been pointed at.
const MaxPatternSegments = 32

// Match reports whether pattern matches key. Both are paths.Key form: project-relative,
// forward-slash, cleaned, and case-folded on Windows/macOS by the caller.
//
// Grammar:
//
//	**      matches zero or more whole path segments
//	*       matches zero or more characters within one segment
//	?       matches exactly one character within one segment
//	[a-z]   character class, delegated to path.Match on the segment
//
// A pattern with no '/' is matched against every segment tail as if prefixed with "**/", so
// "*.ts" matches "src/api/routes.ts". The one exception is a separator-free pattern made up
// entirely of wildcard characters: `*` stays single-segment, because lifting it would make it a
// synonym for `**` and erase the grammar's only distinction between a within-segment wildcard and
// a cross-segment one. A trailing "/**" also matches the directory itself, since a pointer may
// name a directory rather than a file.
//
// path/filepath.Match and path.Match both stop at the separator and have no `**` at all, which is
// why this lives in-repo rather than delegating outright.
func Match(pattern, key string) bool {
	pattern = strings.TrimPrefix(path.Clean(pattern), "./")
	key = strings.TrimPrefix(path.Clean(key), "./")
	if !strings.Contains(pattern, "/") && !allWildcard(pattern) {
		pattern = "**/" + pattern
	}
	// The cap is measured on the pattern as written, before collapseStars folds redundant `**`
	// runs away: a 40-deep `**/**/**/...` pattern is exactly the shape the cap exists to reject,
	// and collapsing first would let it through as a two-segment pattern.
	if strings.Count(pattern, "/")+1 > MaxPatternSegments {
		return false
	}
	if trimmed, ok := strings.CutSuffix(pattern, "/**"); ok {
		if matchSegs(collapseStars(strings.Split(trimmed, "/")), strings.Split(key, "/")) {
			return true
		}
	}
	return matchSegs(collapseStars(strings.Split(pattern, "/")), strings.Split(key, "/"))
}

// allWildcard reports whether s is non-empty and made up only of `*` and `?`. Such a pattern is
// the one case Match does not lift to any depth — see Match's doc comment.
func allWildcard(s string) bool {
	if s == "" {
		return false
	}
	return strings.Trim(s, "*?") == ""
}

// collapseStars folds runs of consecutive `**` segments down to one. "zero or more segments"
// twice in a row is still "zero or more segments", so this changes no answer — but it removes the
// only input shape that makes matchSegs' backtracking blow up combinatorially, since each
// surviving `**` is separated from the next by a segment that anchors it.
func collapseStars(pat []string) []string {
	out := pat[:0:0]
	for i, p := range pat {
		if p == "**" && i > 0 && pat[i-1] == "**" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// matchSegs is the standard two-pointer wildcard matcher lifted to whole segments: `**` plays the
// part `*` plays in a character-level matcher, and every other pattern segment consumes exactly
// one key segment via path.Match.
func matchSegs(pat, seg []string) bool {
	if len(pat) == 0 {
		return len(seg) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(seg); i++ {
			if matchSegs(pat[1:], seg[i:]) {
				return true
			}
		}
		return false
	}
	if len(seg) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], seg[0])
	if err != nil || !ok {
		return false
	}
	return matchSegs(pat[1:], seg[1:])
}
