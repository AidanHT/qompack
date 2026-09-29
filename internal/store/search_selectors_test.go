package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// The recall selectors as the live lane met them (V6 close-out, live report retrieval D6 and UAT-07
// F1/F2): `path:<glob>` is documented as a glob and matched nothing for `path:src/*.go` or
// `path:*.go`, and `tool:Read` matched nothing because the index stores Qompack's display name
// (FileRead) for a host Read.

// selectorSeeds is the one small project every selector row searches.
func selectorSeeds() []searchSeed {
	return []searchSeed{
		{id: "tu-ledger", tool: "FileRead", path: "src/ledger.go", content: []byte("package ledger\n")},
		{id: "tu-pool", tool: "FileRead", path: "src/pool/pool.go", content: []byte("package pool\n")},
		{id: "tu-readme", tool: "FileRead", path: "README.md", content: []byte("# readme\n")},
		{id: "tu-yaml", tool: "FileEdit", path: "config/pool.yaml", content: []byte("size: 4\n")},
		{id: "tu-bash", tool: "Bash", path: "", content: []byte("ok\n")},
	}
}

// TestSearch_PathSelectorIsAGlob pins path.Match semantics on slash paths, applied to the whole key
// and to every path-segment suffix of it, the way a plain path already matches by suffix.
func TestSearch_PathSelectorIsAGlob(t *testing.T) {
	tp := newTestStore(t)
	seedSearch(t, tp, selectorSeeds())
	ctx := context.Background()

	for _, tc := range []struct {
		glob string
		want []core.ToolUseID
	}{
		{"src/*.go", []core.ToolUseID{"tu-ledger"}},
		{"*.go", []core.ToolUseID{"tu-ledger", "tu-pool"}},
		{"src/*/*.go", []core.ToolUseID{"tu-pool"}},
		{"pool.*", []core.ToolUseID{"tu-pool", "tu-yaml"}},
		{"*.md", []core.ToolUseID{"tu-readme"}},
		{"src/led?er.go", []core.ToolUseID{"tu-ledger"}},
		{"[cs]*/pool.*", []core.ToolUseID{"tu-yaml"}},
		{"*.rs", nil},
	} {
		hits, err := tp.Store.Search(ctx, Query{Path: tc.glob, K: maxK})
		require.NoError(t, err, "glob %q", tc.glob)
		require.ElementsMatch(t, tc.want, hitIDs(hits), "glob %q", tc.glob)
	}
}

// TestSearch_PathGlobWholeKeyOutranksSuffix keeps the ranking the plain-string predicates already
// have: a glob that matches the whole key is an exact statement of intent, one that matches only a
// trailing part of it is a suffix match.
func TestSearch_PathGlobWholeKeyOutranksSuffix(t *testing.T) {
	tp := newTestStore(t)
	seedSearch(t, tp, []searchSeed{
		{id: "tu-deep", tool: "FileRead", path: "web/src/auth.ts", content: []byte("deep\n")},
		{id: "tu-top", tool: "FileRead", path: "src/auth.ts", content: []byte("top\n")},
	})

	hits, err := tp.Store.Search(context.Background(), Query{Path: "src/*.ts"})
	require.NoError(t, err)
	require.Equal(t, []core.ToolUseID{"tu-top", "tu-deep"}, hitIDs(hits))
	require.Greater(t, hits[0].Score, hits[1].Score, "a whole-key glob match must outrank a suffix match")
}

// TestSearch_PlainPathKeepsExactSuffixAndContains pins that a selector with no glob metacharacter
// matches exactly as before: equality, a path-segment suffix, or containment.
func TestSearch_PlainPathKeepsExactSuffixAndContains(t *testing.T) {
	tp := newTestStore(t)
	seedSearch(t, tp, selectorSeeds())
	ctx := context.Background()

	for _, tc := range []struct {
		path string
		want []core.ToolUseID
	}{
		{"src/ledger.go", []core.ToolUseID{"tu-ledger"}},
		{"ledger.go", []core.ToolUseID{"tu-ledger"}},
		{"pool", []core.ToolUseID{"tu-pool", "tu-yaml"}},
	} {
		hits, err := tp.Store.Search(ctx, Query{Path: tc.path, K: maxK})
		require.NoError(t, err, "path %q", tc.path)
		require.ElementsMatch(t, tc.want, hitIDs(hits), "path %q", tc.path)
	}
}

// TestSearch_MalformedPathGlobIsAnError: a pattern path.Match cannot parse is the caller's mistake,
// and answering it with "no hits" would read as "nothing was ever captured there".
func TestSearch_MalformedPathGlobIsAnError(t *testing.T) {
	tp := newTestStore(t)
	seedSearch(t, tp, selectorSeeds())

	_, err := tp.Store.Search(context.Background(), Query{Path: "src/[*.go"})
	require.ErrorIs(t, err, ErrBadPathGlob)
}

// TestSearch_ToolSelectorAcceptsHostNames pins that `tool:` takes the host's own tool name as well
// as Qompack's display name, case-insensitively, and still narrows to exactly that kind of tool.
func TestSearch_ToolSelectorAcceptsHostNames(t *testing.T) {
	tp := newTestStore(t)
	seedSearch(t, tp, selectorSeeds())
	ctx := context.Background()

	reads := []core.ToolUseID{"tu-ledger", "tu-pool", "tu-readme"}
	for _, tc := range []struct {
		tool string
		want []core.ToolUseID
	}{
		{"Read", reads},
		{"read", reads},
		{"FileRead", reads},
		{"fileread", reads},
		{"NotebookRead", reads},
		{"Edit", []core.ToolUseID{"tu-yaml"}},
		{"MultiEdit", []core.ToolUseID{"tu-yaml"}},
		{"Bash", []core.ToolUseID{"tu-bash"}},
		{"Write", nil},
	} {
		hits, err := tp.Store.Search(ctx, Query{Tool: tc.tool, K: maxK})
		require.NoError(t, err, "tool %q", tc.tool)
		require.ElementsMatch(t, tc.want, hitIDs(hits), "tool %q", tc.tool)
	}
}
