package docs

import (
	"path/filepath"
	"regexp"
	"testing"
)

// commandHeadingRe matches a command page's per-command heading: ## `/qompack:NAME`.
var commandHeadingRe = regexp.MustCompile("^## `/qompack:([a-z0-9-]+)`$")

// toolHeadingRe matches a tool page's per-tool heading: ## `NAME`.
var toolHeadingRe = regexp.MustCompile("^## `([a-z0-9_]+)`$")

// generatedCommandNames returns the slash-command names docs/commands.md documents.
//
// That page is generated from internal/commands.Specs() and CI's `docs` job fails if it drifts, so
// reading the page is reading the shipped command set — which is why nothing in this package
// hard-codes a command list or a count. Later commits assert their prose against this.
func generatedCommandNames(t *testing.T) []string {
	t.Helper()
	return headingNames(t, "docs/commands.md", commandHeadingRe)
}

// generatedToolNames returns the MCP tool names docs/mcp-tools.md documents. Same contract as
// generatedCommandNames: the page is generated from internal/mcp.ToolDefs and gated in CI.
//
// The regexp is what excludes the page's prose headings ("## Tools at a glance"): only a heading
// that is nothing but a backticked identifier is a tool.
func generatedToolNames(t *testing.T) []string {
	t.Helper()
	return headingNames(t, "docs/mcp-tools.md", toolHeadingRe)
}

// headingNames collects the first capture group of re over a page's level-2 headings. It reads the
// RAW lines rather than the scrubbed ones: both patterns require backticks, which scrubbing
// removes. A page with no matching heading fails the test — an inventory that silently came back
// empty would make every assertion built on it vacuously true.
func headingNames(t *testing.T, rel string, re *regexp.Regexp) []string {
	t.Helper()
	path := filepath.Join(repoRoot(t), filepath.FromSlash(rel))
	var names []string
	inFence := false
	for _, l := range readLines(t, path) {
		if fenceRe.MatchString(l) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if m := re.FindStringSubmatch(l); m != nil {
			names = append(names, m[1])
		}
	}
	if len(names) == 0 {
		t.Fatalf("%s: no headings matched %s: the page moved, changed shape, or is empty", rel, re)
	}
	return names
}

// TestGeneratedInventoriesAreNonEmpty exercises both inventory helpers so that a change to either
// generated page's shape fails here, in one obvious place, rather than in whichever later test
// happens to call them first.
func TestGeneratedInventoriesAreNonEmpty(t *testing.T) {
	if got := generatedCommandNames(t); len(got) == 0 {
		t.Error("docs/commands.md: no commands")
	}
	if got := generatedToolNames(t); len(got) == 0 {
		t.Error("docs/mcp-tools.md: no tools")
	}
}
