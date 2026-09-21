package docs

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot resolves the module root via `go list`, so the harness works regardless of the
// directory the test binary was started from. This is the same approach test/guards uses
// (test/guards/stubs_test.go); it is copied rather than imported because nothing may import a
// composition root.
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// readLines returns the file's lines with any trailing carriage returns removed, so a checkout
// with CRLF endings behaves like one without.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// fenceRe matches the opening or closing line of a fenced code block. Everything between a pair
// of these is prose to a human and noise to this harness: a JSON schema in docs/mcp-tools.md is
// full of bracket-and-paren shapes that are not links.
var fenceRe = regexp.MustCompile("^[ ]{0,3}(```|~~~)")

// inlineCodeRe matches an inline code span.
var inlineCodeRe = regexp.MustCompile("`[^`]*`")

// defenced returns the file's lines with fenced blocks blanked out and nothing else changed. This
// is the right input for a HEADING scan: "## `recall`" is a heading whose entire text is an inline
// code span, and removing the span would leave nothing to slug.
func defenced(t *testing.T, path string) []string {
	t.Helper()
	lines := readLines(t, path)
	out := make([]string, len(lines))
	inFence := false
	for i, l := range lines {
		if fenceRe.MatchString(l) {
			inFence = !inFence
			out[i] = ""
			continue
		}
		if inFence {
			out[i] = ""
			continue
		}
		out[i] = l
	}
	return out
}

// scrubbed is defenced with inline code spans removed as well, which is the right input for a LINK
// scan: a link written inside backticks is an example, not a reference.
func scrubbed(t *testing.T, path string) []string {
	t.Helper()
	out := defenced(t, path)
	for i, l := range out {
		out[i] = inlineCodeRe.ReplaceAllString(l, "")
	}
	return out
}

// headingRe matches an ATX heading and captures its level and text.
var headingRe = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)

// headingText returns the heading text of an ATX heading line, or "" and false.
func headingText(line string) (string, int, bool) {
	m := headingRe.FindStringSubmatch(line)
	if m == nil {
		return "", 0, false
	}
	text := strings.TrimSpace(strings.TrimRight(m[2], "#"))
	return text, len(m[1]), true
}

// slug is the GitHub heading anchor for a heading's text: lowercase, backticks dropped, spaces
// turned into hyphens, every other punctuation mark removed except '-' and '_'. Duplicate
// headings in one file get "-1", "-2" … suffixes, which anchorsOf applies.
func slug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r == '`':
			// dropped entirely
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 127:
			// Non-ASCII letters keep their identity on GitHub; punctuation above 127 (an em
			// dash, a curly quote) does not. Only letters and digits survive here.
			if isLetterOrDigit(r) {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func isLetterOrDigit(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
		(r >= 0x00C0 && r <= 0x024F) // Latin-1 Supplement and Latin Extended-A/B letters
}

// anchorsOf returns the set of heading anchors a markdown file offers.
func anchorsOf(t *testing.T, path string) map[string]bool {
	t.Helper()
	seen := map[string]int{}
	out := map[string]bool{}
	for _, l := range defenced(t, path) {
		text, _, ok := headingText(l)
		if !ok || text == "" {
			continue
		}
		s := slug(text)
		if s == "" {
			continue
		}
		if n := seen[s]; n > 0 {
			out[s+"-"+itoa(n)] = true
		} else {
			out[s] = true
		}
		seen[s]++
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// markdownFiles returns README.md and every docs/**/*.md, as repo-relative slash paths.
func markdownFiles(t *testing.T, root string) []string {
	t.Helper()
	files := []string{"README.md"}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs/: %v", err)
	}
	return files
}

// firstHeading returns the text of a file's first level-1 heading, or "" if it has none.
func firstHeading(t *testing.T, path string) string {
	t.Helper()
	for _, l := range defenced(t, path) {
		text, level, ok := headingText(l)
		if ok && level == 1 {
			return text
		}
	}
	return ""
}

// adrNumberPrefixRe matches the numbering an ADR title carries before its own words: either
// "ADR 0011" or "12.". titleCore strips it so the index may write the number in its own column
// and the title in the next one.
var adrNumberPrefixRe = regexp.MustCompile(`^(?:ADR\s+)?[0-9]+\.?`)

// titleCore is an ADR title with its leading number and the separator that follows removed.
func titleCore(title string) string {
	s := strings.TrimSpace(adrNumberPrefixRe.ReplaceAllString(strings.TrimSpace(title), ""))
	for _, sep := range []string{"—", "–", "-", ":"} {
		if strings.HasPrefix(s, sep+" ") {
			s = strings.TrimSpace(strings.TrimPrefix(s, sep))
			break
		}
	}
	return s
}
