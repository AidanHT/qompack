package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ownedDocs is the set of hand-written pages SP-18 owns. Later commits in this subplan append to
// it as they add the user guide, troubleshooting, limits and UAT pages; nothing else belongs here,
// because the generated pages (docs/config-reference.md, docs/commands.md, docs/mcp-tools.md) have
// their own drift check in CI's `docs` job and the ADRs have TestADRIndexListsEveryADR.
var ownedDocs = []string{
	"README.md",
	"docs/architecture.md",
	"docs/adr/README.md",
	"docs/user-guide.md",
	"docs/troubleshooting.md",
	"docs/cannot-do.md",
	"docs/upstream-issues.md",
	"docs/uat.md",
}

// TestOwnedDocsExist asserts the shape every owned page shares: it exists, it has content, and it
// opens on a level-1 heading, so a page can never be half-created or left as a stub that other
// documents already link.
func TestOwnedDocsExist(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range ownedDocs {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s: empty", rel)
			continue
		}
		var first string
		for _, l := range readLines(t, abs) {
			if strings.TrimSpace(l) != "" {
				first = strings.TrimSpace(l)
				break
			}
		}
		if !strings.HasPrefix(first, "# ") {
			t.Errorf("%s: first non-blank line is not a level-1 heading: %q", rel, first)
		}
	}
}

// adrFileRe matches an ADR file name: four digits, a hyphen, a slug.
var adrFileRe = regexp.MustCompile(`^[0-9]{4}-.*\.md$`)

// TestADRIndexListsEveryADR keeps docs/adr/README.md honest against the directory it indexes. The
// ADR set is derived from disk — an index that hard-coded a count would go stale on the next ADR
// and would still pass. Each ADR must be linked, each link must resolve, and the row that carries
// the link must name the ADR's own title, so a renamed ADR cannot keep an obsolete description in
// the index.
func TestADRIndexListsEveryADR(t *testing.T) {
	root := repoRoot(t)
	adrDir := filepath.Join(root, "docs", "adr")

	entries, err := os.ReadDir(adrDir)
	if err != nil {
		t.Fatalf("read docs/adr: %v", err)
	}
	var adrs []string
	for _, e := range entries {
		if !e.IsDir() && adrFileRe.MatchString(e.Name()) {
			adrs = append(adrs, e.Name())
		}
	}
	if len(adrs) == 0 {
		t.Fatal("no ADR files found in docs/adr: the scan is looking in the wrong place")
	}

	indexPath := filepath.Join(adrDir, "README.md")
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("docs/adr/README.md: %v", err)
	}
	indexLines := scrubbed(t, indexPath)

	// linked maps an ADR file name to the 1-based line numbers that link it.
	linked := map[string][]int{}
	for i, line := range indexLines {
		for _, m := range inlineLinkRe.FindAllStringSubmatch(line, -1) {
			target, _, _ := strings.Cut(m[1], "#")
			if target == "" || schemeRe.MatchString(target) {
				continue
			}
			name := filepath.Base(filepath.FromSlash(target))
			if adrFileRe.MatchString(name) {
				linked[name] = append(linked[name], i+1)
			}
		}
	}

	for _, name := range adrs {
		lines, ok := linked[name]
		if !ok {
			t.Errorf("docs/adr/README.md: does not link %s", name)
			continue
		}
		want := titleCore(firstHeading(t, filepath.Join(adrDir, name)))
		if want == "" {
			t.Errorf("docs/adr/%s: no level-1 heading to take a title from", name)
			continue
		}
		named := false
		for _, ln := range lines {
			if strings.Contains(indexLines[ln-1], want) {
				named = true
				break
			}
		}
		if !named {
			t.Errorf("docs/adr/README.md:%d: the row linking %s does not name its title %q",
				lines[0], name, want)
		}
	}

	// The other direction: an index that links an ADR which no longer exists. The link test
	// catches a missing file too, but only once the index is already in the scan set; this keeps
	// the ADR-specific message.
	have := map[string]bool{}
	for _, name := range adrs {
		have[name] = true
	}
	for name, lines := range linked {
		if !have[name] {
			t.Errorf("docs/adr/README.md:%d: links %s, which does not exist", lines[0], name)
		}
	}
}
