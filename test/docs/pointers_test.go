package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The pages in ownedDocs name files that do not exist yet as plain text rather than as links —
// "planned (SP-17): docs/install.md" — and docs/cannot-do.md states that convention in as many
// words: plain text means the file is not on this tree, a link means it is. The convention has a
// failure mode that no existing check catches. When the subplan that owns a planned page lands it,
// the pointer keeps saying "planned" and keeps being plain text; TestRelativeLinksResolve is
// silent, because a non-link cannot be a broken link. The page then lies in the one direction the
// harness was built to prevent, and it lies quietly.
//
// TestNoPlannedPointerToAnExistingPage closes that direction: a "planned" pointer whose target
// exists on disk is a failure, reported at the line the target is named on. It is the trigger for
// a conversion, not a style rule — the fix is always to turn that pointer into a link.

// plannedMarkerRe matches the forward-reference marker the pages use: the word "planned",
// optionally followed by the owning subplan in parentheses ("planned (SP-17)", "planned (SP-18
// Commit 5)"). The parenthetical is optional because a page may write "the page is planned:
// docs/x.md" without naming the owner.
var plannedMarkerRe = regexp.MustCompile(`(?i)\bplanned\b(?:[ \t]*\([^)\n]*\))?`)

// pointerPathRe matches a repository documentation path as the pages spell it, with or without the
// surrounding backticks both spellings use (README.md writes `docs/install.md`, troubleshooting.md
// writes docs/install.md bare).
var pointerPathRe = regexp.MustCompile("`?(docs/[A-Za-z0-9._-]+\\.md|README\\.md)`?")

// maxFirstGap bounds the punctuation between the marker and the first path it introduces: a colon,
// bold markers, an opening backtick and whitespace, nothing else.
const maxFirstGap = 8

// maxListGap bounds the text between two paths in one pointer ("a, b and c", "a and\nb, plus the
// packaged bundle and c"). A sentence-ending period stops the list, so the prose that follows a
// pointer is never scanned as part of it.
const maxListGap = 60

// TestNoPlannedPointerToAnExistingPage fails when an owned page still points at a page as
// "planned" after that page has landed. The scan runs over the defenced text with inline links
// blanked out, so a pointer that has already been converted to a link is invisible here and an
// example inside a code fence is not a pointer at all.
func TestNoPlannedPointerToAnExistingPage(t *testing.T) {
	root := repoRoot(t)
	type site struct {
		rel  string
		line int
		path string
	}
	seen := map[site]bool{}
	for _, rel := range ownedDocs {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		text := blankLinks(strings.Join(defenced(t, abs), "\n"))
		for _, m := range plannedMarkerRe.FindAllStringIndex(text, -1) {
			for _, off := range plannedPointerTargets(text, m[1]) {
				path := strings.Trim(text[off[0]:off[1]], "`")
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
					continue // still planned, still absent: the convention is being honoured
				}
				s := site{rel: rel, line: lineOf(text, off[0]), path: path}
				if seen[s] {
					continue
				}
				seen[s] = true
				t.Errorf("%s:%d: names %s as planned, but that file exists: make it a link",
					s.rel, s.line, s.path)
			}
		}
	}
}

// plannedPointerTargets returns the [start,end) offsets of every documentation path a planned
// marker introduces, given the offset just past the marker. It walks the list one path at a time
// rather than matching the whole list with one regular expression, because a path contains the
// period that has to terminate the list ("docs/x.md. Neither file exists yet") and a greedy
// separator would swallow it.
func plannedPointerTargets(text string, pos int) [][]int {
	var out [][]int
	first := true
	for {
		// Look only a little way ahead: a pointer's list is short by construction.
		end := min(pos+maxListGap+64, len(text))
		m := pointerPathRe.FindStringSubmatchIndex(text[pos:end])
		if m == nil {
			return out
		}
		gap := text[pos : pos+m[2]]
		if first {
			if !okFirstGap(gap) {
				return out
			}
		} else if !okListGap(gap) {
			return out
		}
		out = append(out, []int{pos + m[2], pos + m[3]})
		pos += m[1]
		first = false
	}
}

// okFirstGap reports whether the text between the marker and the first path is nothing but the
// punctuation a pointer uses to introduce it.
func okFirstGap(gap string) bool {
	if len(gap) > maxFirstGap || strings.Count(gap, "\n") > 1 {
		return false
	}
	for _, r := range gap {
		if r != ' ' && r != '\t' && r != '\n' && r != ':' && r != '*' && r != '`' {
			return false
		}
	}
	return true
}

// okListGap reports whether the text between two paths in one pointer is a list separator: short,
// inside one paragraph, and with no sentence-ending period.
func okListGap(gap string) bool {
	if len(gap) > maxListGap || strings.Contains(gap, ".") {
		return false
	}
	if strings.Count(gap, "\n") > 1 || strings.Contains(gap, "\n\n") {
		return false
	}
	return true
}

// blankLinks replaces every inline markdown link with the same number of spaces, so that offsets —
// and therefore line numbers — are unchanged while a converted pointer stops being scannable. This
// is what makes the test the trigger for a conversion: the moment the pointer becomes a link it
// stops matching.
func blankLinks(text string) string {
	b := []byte(text)
	for _, m := range inlineLinkRe.FindAllStringIndex(text, -1) {
		for i := m[0]; i < m[1]; i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}
	return string(b)
}

// lineOf returns the 1-based line number of a byte offset.
func lineOf(text string, off int) int {
	return strings.Count(text[:off], "\n") + 1
}
