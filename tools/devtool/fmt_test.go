package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fmtTargetsFixture builds a module root shaped like this repository's: a few package
// directories, a top-level Go file, a non-Go top-level file, and the two directories a formatter
// must never be pointed at.
func fmtTargetsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{
		"internal", "tools", ".git",
		filepath.Join(".claude", "worktrees", "agent-1", "tools", "devtool"),
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
	}
	for _, f := range []string{
		"doc.go",
		"go.mod",
		filepath.Join("internal", "a.go"),
		filepath.Join(".claude", "worktrees", "agent-1", "tools", "devtool", "a.go"),
	} {
		require.NoError(t, os.WriteFile(filepath.Join(root, f), []byte("package p\n"), 0o600))
	}
	return root
}

// TestFmtTargets_SkipsTheAgentWorktrees is the whole point of fmtTargets.
//
// gofumpt walks .claude/worktrees like any other directory: fmt-check would report a stale copy of
// every file as its own offender, and fmt — which runs with -w — would rewrite another branch's
// checkout. A formatter that edits a sibling worktree can lose uncommitted work, so this is the
// assertion that must not be relaxed.
func TestFmtTargets_SkipsTheAgentWorktrees(t *testing.T) {
	got, err := fmtTargets(fmtTargetsFixture(t))
	require.NoError(t, err)

	require.NotContains(t, got, "./.claude",
		"gofumpt -w on an agent worktree rewrites a checkout this run does not own")
	require.NotContains(t, got, "./.git")
	require.ElementsMatch(t, []string{"./internal", "./tools", "./doc.go"}, got,
		"every other top-level directory and Go file must still be covered")
}

// TestFmtTargets_CoversANewTopLevelDirectory pins the enumeration against a hardcoded list: a
// formatting gate that silently stops covering a directory fails the same way one that reports
// unactionable files does.
func TestFmtTargets_CoversANewTopLevelDirectory(t *testing.T) {
	root := fmtTargetsFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "newpkg"), 0o755))

	got, err := fmtTargets(root)
	require.NoError(t, err)
	require.Contains(t, got, "./newpkg")
}

// TestFmtTargets_EmptyRootIsAnError keeps a mis-resolved root from reading as "nothing to do":
// gofumpt with no path argument reads standard input and reports success over an empty stream.
func TestFmtTargets_EmptyRootIsAnError(t *testing.T) {
	_, err := fmtTargets(t.TempDir())
	require.ErrorContains(t, err, "no Go sources")
}
