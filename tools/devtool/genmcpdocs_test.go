package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/mcp"
)

// gen-mcp-docs' own tests, in the shape genconfigdocs would use if it had any: there is no
// genconfigdocs_test.go in this tree, so the house pattern followed here is the one the other
// root-dependent tasks use — gencontractfixtures_test.go and importgraph_test.go — which set the
// package-level `root` themselves and restore it in t.Cleanup, because devtool's tests share that
// variable and never run in parallel.
//
// Why a generated page needs a test at all: docs/mcp-tools.md is not documentation in the ordinary
// sense. A model reads a tool's description and input schema to decide whether and how to call it,
// so a page that drifted from mcp.ToolDefs would be a wrong specification of a live API rather
// than a stale README. The CI `docs` job runs the --check half; these rows make sure the --check
// half can actually fail.

// mcpDocsDesignOrder is the §8.7 table's order, written out rather than taken from
// mcp.ToolNames(), so this file compares the tool set against the design document instead of
// against itself. A reordering of ToolNames would break the golden, docs/mcp-tools.md and this
// row together — which is the point.
var mcpDocsDesignOrder = []string{
	"recall", "expand", "re_read", "already_tried",
	"record_eliminated", "timeline", "why", "dropped",
}

// mcpDocsRepoRoot returns this module's root without disturbing the package-level `root`, for the
// rows that only need to READ the committed page.
func mcpDocsRepoRoot(t *testing.T) string {
	t.Helper()
	return testModuleRoot(t)
}

// mcpDocsSandbox points the package-level `root` at a scratch tree containing docs/mcp-tools.md
// with the given contents, and restores it afterwards. It returns the sandbox root.
func mcpDocsSandbox(t *testing.T, page []byte) string {
	t.Helper()

	dir := t.TempDir()
	if page != nil {
		p := filepath.Join(dir, filepath.FromSlash(mcpDocPath))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755), "mkdir for %s", p)
		require.NoError(t, os.WriteFile(p, page, 0o644), "writing %s", p) // #nosec G306 -- test fixture
	}

	prev := root
	root = dir
	t.Cleanup(func() { root = prev })
	return dir
}

// TestMCPToolsDocsUpToDate is the assertion the CI `docs` job exists to run: the committed page is
// byte-for-byte what renderMCPDoc produces from the current mcp.ToolDefs.
//
// Newlines are normalized on the READ side only, exactly as taskGenMCPDocs --check does it, so a
// Windows checkout with autocrlf on does not read as drift.
func TestMCPToolsDocsUpToDate(t *testing.T) {
	want, err := renderMCPDoc()
	require.NoError(t, err, "renderMCPDoc")
	require.NotEmpty(t, want)

	p := filepath.Join(mcpDocsRepoRoot(t), filepath.FromSlash(mcpDocPath))
	got, err := os.ReadFile(p)
	require.NoError(t, err, "%s must be committed; run `go run ./tools/devtool gen-mcp-docs`", mcpDocPath)

	require.True(t, bytes.Equal(want, normalizeNewlines(got)),
		"%s is stale; run `go run ./tools/devtool gen-mcp-docs`", mcpDocPath)

	// The page is generated, so it is only as useful as the content it carries. These two are the
	// things a reader (or a model) came for, and a renderer that silently stopped emitting either
	// would still produce a byte-identical page under --check once regenerated.
	for _, name := range mcpDocsDesignOrder {
		require.Contains(t, string(want), "## `"+name+"`", "the page must document every §8.7 tool")
	}
	require.Contains(t, string(want), mcp.StandingInstruction,
		"the page must carry the standing instruction that closes G6.2, not merely list the tool")
}

// TestPluginValidateSeesEightTools ties the plugin bundle's own count to the tool set.
//
// `plugin-validate` asserts len(mcp.ToolNames()) == wantMCPTools rather than counting the bundle,
// because .mcp.json names a command to launch and `tools/list` is answered at runtime — a bundle
// that is byte-perfect while the binary behind it advertises seven tools is exactly the drift the
// check exists to catch. This row is what makes the constant and the design order agree.
func TestPluginValidateSeesEightTools(t *testing.T) {
	names := mcp.ToolNames()
	require.Len(t, names, wantMCPTools, "§8.7 declares exactly %d tools", wantMCPTools)
	require.Equal(t, mcpDocsDesignOrder, names,
		"ToolNames must return the §8.7 design order: it is what tools/list emits and what the "+
			"golden and docs/mcp-tools.md are both generated from")

	// And the generated page lists them in the same order, top to bottom: a model reading it
	// should meet recall before expand, because expanding something you have not recalled is not
	// a thing you can do.
	page, err := renderMCPDoc()
	require.NoError(t, err)
	at := make([]int, 0, len(names))
	for _, n := range names {
		i := strings.Index(string(page), "## `"+n+"`")
		require.GreaterOrEqual(t, i, 0, "the page must have a section for %q", n)
		at = append(at, i)
	}
	for i := 1; i < len(at); i++ {
		require.Greater(t, at[i], at[i-1],
			"%q must be documented after %q, in §8.7 order", names[i], names[i-1])
	}
}

// TestGenMCPDocsCheckDetectsDrift drives the task's own --check against a sandbox root, which is
// what makes the CI job's failure mode real rather than assumed.
//
// All three outcomes are covered in one row on purpose: "agrees" and "differs" are the same code
// path with the comparison flipped, and a --check that could not tell them apart would pass CI
// forever. The missing-file case is separate because it produces a different instruction to the
// reader — regenerate, rather than reconcile.
func TestGenMCPDocsCheckDetectsDrift(t *testing.T) {
	want, err := renderMCPDoc()
	require.NoError(t, err, "renderMCPDoc")

	t.Run("an exact copy is up to date", func(t *testing.T) {
		mcpDocsSandbox(t, want)
		require.NoError(t, taskGenMCPDocs([]string{"--check"}),
			"a byte-identical page must not be reported as drift")
	})

	t.Run("a mutated copy is stale", func(t *testing.T) {
		mutated := bytes.Replace(want, []byte("## `recall`"), []byte("## `recall_v2`"), 1)
		require.NotEqual(t, want, mutated, "the mutation must have applied, or this row proves nothing")
		mcpDocsSandbox(t, mutated)

		err := taskGenMCPDocs([]string{"--check"})
		require.Error(t, err, "a page that differs from mcp.ToolDefs must be reported")
		require.Contains(t, err.Error(), "is stale")
		require.Contains(t, err.Error(), mcpDocPath, "the error must name the file to regenerate")
	})

	t.Run("an absent page is missing, not stale", func(t *testing.T) {
		mcpDocsSandbox(t, nil)

		err := taskGenMCPDocs([]string{"--check"})
		require.Error(t, err, "a page that is not there at all must be reported")
		require.Contains(t, err.Error(), "is missing")
	})

	t.Run("without --check the task writes the page", func(t *testing.T) {
		dir := mcpDocsSandbox(t, nil)
		require.NoError(t, taskGenMCPDocs(nil), "gen-mcp-docs must create the page it checks")

		got, readErr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(mcpDocPath)))
		require.NoError(t, readErr)
		require.True(t, bytes.Equal(want, normalizeNewlines(got)),
			"what the task writes must be what --check then accepts")
		require.NoError(t, taskGenMCPDocs([]string{"--check"}), "and it must accept its own output")
	})
}

// TestMCPDocsGlanceLinksResolve is the row the anchor bug got past: the "Tools at a glance" table
// links every tool to its own `## ` heading, and those links have to resolve on GitHub, where the
// anchor is derived from the heading text and nowhere else.
//
// The slug rule is re-derived here from the heading the renderer actually emits, rather than
// calling anchorFor on both sides — a test that asked the generator to agree with itself would
// have passed while the page shipped #re-read for a heading that answers to #re_read. Tool names
// come from mcp.ToolDefs so a new tool is covered the day it is added, and no count is asserted.
func TestMCPDocsGlanceLinksResolve(t *testing.T) {
	page, err := renderMCPDoc()
	require.NoError(t, err, "renderMCPDoc")

	// githubSlug is GitHub's heading slug: lowercase, spaces to "-", "_" and "-" kept as word
	// characters, everything else (the backticks around the name, above all) dropped.
	githubSlug := func(headingText string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(headingText) {
			switch {
			case r == ' ':
				b.WriteRune('-')
			case r == '-' || r == '_':
				b.WriteRune(r)
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				b.WriteRune(r)
			}
		}
		return b.String()
	}

	// The anchors the page offers, read off its own `## ` headings.
	offered := map[string]bool{}
	for _, line := range strings.Split(string(page), "\n") {
		if text, ok := strings.CutPrefix(line, "## "); ok {
			offered[githubSlug(strings.TrimSpace(text))] = true
		}
	}
	require.NotEmpty(t, offered, "the page must have headings, or this row proves nothing")

	defs := mcp.ToolDefs(mcp.ToolDeps{})
	require.NotEmpty(t, defs, "mcp.ToolDefs must return the tool set")
	for _, d := range defs {
		link := "[`" + d.Name + "`](#" + anchorFor(d.Name) + ")"
		require.Contains(t, string(page), link,
			"the glance table must link %q to its section", d.Name)
		require.True(t, offered[anchorFor(d.Name)],
			"the glance-table link for %q points at #%s, which no heading in the page offers; "+
				"anchorFor must produce GitHub's slug of the heading text",
			d.Name, anchorFor(d.Name))
	}
}
