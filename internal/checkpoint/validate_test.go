package checkpoint

// Pointer-validation tests (SP-10 §11, test table "internal/checkpoint — pointer validation
// (G2.5)"). These are in-package tests: they introspect the parsed fixture index (parseIndex) to
// learn the sizes and mtimes real git recorded, then materialize working trees that match or
// deliberately miss them. No test here ever runs git — the fixtures under
// testdata/fixtures/gitindex were generated once, outside this repository (see the README beside
// them).

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

// gitindexFixture returns the path of one committed index fixture.
func gitindexFixture(name string) string {
	return filepath.Join("..", "..", "testdata", "fixtures", "gitindex", name)
}

// readGitindexFixture reads one committed index fixture, failing loudly if it is missing.
func readGitindexFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(gitindexFixture(name))
	require.NoError(t, err, "gitindex fixture missing: %s", name)
	require.NotEmpty(t, b)
	return b
}

// makeGitRoot builds a temp project root whose .git directory holds the named fixture as its
// index and a HEAD on refs/heads/main, and returns the root plus the fixture's entries keyed the
// way the implementation keys them (paths.Key).
func makeGitRoot(t *testing.T, indexFixture string) (string, map[string]indexEntry) {
	t.Helper()
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	require.NoError(t, os.Mkdir(gitDir, 0o755))
	raw := readGitindexFixture(t, indexFixture)
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "index"), raw, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))

	entries, err := parseIndex(raw)
	if err != nil {
		// v4/truncated fixtures do not parse; callers that need entries use v2/v3.
		return root, nil
	}
	m := make(map[string]indexEntry, len(entries))
	for _, e := range entries {
		m[paths.Key(e.Path)] = e
	}
	return root, m
}

// materialize writes rel under root with exactly size bytes and the given mtime second, creating
// parent directories as needed.
func materialize(t *testing.T, root, rel string, size int, mtimeSec uint32) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, make([]byte, size), 0o644))
	mt := time.Unix(int64(mtimeSec), 0)
	require.NoError(t, os.Chtimes(p, mt, mt))
}

// materializeClean writes the file an index entry describes so that neither the size check nor
// the mtime check trips.
func materializeClean(t *testing.T, root string, e indexEntry) {
	t.Helper()
	materialize(t, root, e.Path, int(e.Size), e.MTimeSec)
}

// dropKinds projects a drop list to its kinds, in order.
func dropKinds(drops []DropEntry) []string {
	kinds := make([]string, 0, len(drops))
	for _, d := range drops {
		kinds = append(kinds, d.Kind)
	}
	return kinds
}

func TestValidatePointersMissingFile(t *testing.T) {
	root, idx := makeGitRoot(t, "v2.index")
	require.NotEmpty(t, idx)

	drops, err := ValidatePointers(context.Background(), root, Pointers{
		Files: []FilePointer{{Path: "src/gone.ts", Why: "was being edited"}},
	})
	require.NoError(t, err)
	require.Equal(t, []DropEntry{
		{Kind: "pointer_missing", ID: "src/gone.ts", Detail: "file no longer exists in the working tree"},
	}, drops)
}

func TestValidatePointersDirectory(t *testing.T) {
	root, _ := makeGitRoot(t, "v2.index")
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0o755))

	drops, err := ValidatePointers(context.Background(), root, Pointers{
		Files: []FilePointer{{Path: "src/", Why: "points at a directory"}},
	})
	require.NoError(t, err)
	require.Len(t, drops, 1)
	require.Equal(t, "pointer_invalid", drops[0].Kind)
	require.Equal(t, "src/", drops[0].ID)
	require.Equal(t, "path is a directory", drops[0].Detail)
}

func TestValidatePointersEscape(t *testing.T) {
	root, _ := makeGitRoot(t, "v2.index")

	drops, err := ValidatePointers(context.Background(), root, Pointers{
		Files: []FilePointer{{Path: "../../etc/passwd", Why: "escapes"}},
	})
	require.NoError(t, err)
	require.Equal(t, []DropEntry{
		{Kind: "pointer_invalid", ID: "../../etc/passwd", Detail: "path escapes the project root"},
	}, drops)
}

func TestValidatePointersDirtyAgainstIndex(t *testing.T) {
	root, idx := makeGitRoot(t, "v2.index")
	e, ok := idx[paths.Key("a.txt")]
	require.True(t, ok, "fixture sanity: a.txt is in the v2 index")

	// Same mtime, different size: the size check alone must flag it.
	materialize(t, root, e.Path, int(e.Size)+3, e.MTimeSec)

	drops, err := ValidatePointers(context.Background(), root, Pointers{
		Files: []FilePointer{{Path: "a.txt", Why: "the file being edited"}},
	})
	require.NoError(t, err)
	require.Len(t, drops, 1)
	require.Equal(t, "pointer_dirty", drops[0].Kind)
	require.Equal(t, "a.txt", drops[0].ID)
	require.Contains(t, drops[0].Detail, "main", "the detail names the branch HEAD points at")
	require.Equal(t, "modified since index on main", drops[0].Detail)
}

func TestValidatePointersDirtyOnMTimeAlone(t *testing.T) {
	root, idx := makeGitRoot(t, "v2.index")
	e, ok := idx[paths.Key("b/c.txt")]
	require.True(t, ok)

	// Same size, mtime one second later: the mtime check alone must flag it.
	materialize(t, root, e.Path, int(e.Size), e.MTimeSec+1)

	drops, err := ValidatePointers(context.Background(), root, Pointers{
		Files: []FilePointer{{Path: "b/c.txt", Why: "same bytes, new mtime"}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"pointer_dirty"}, dropKinds(drops))
}

func TestValidatePointersUntracked(t *testing.T) {
	root, _ := makeGitRoot(t, "v2.index")
	materialize(t, root, "notes.md", 9, 1767225480)

	drops, err := ValidatePointers(context.Background(), root, Pointers{
		Files: []FilePointer{{Path: "notes.md", Why: "scratch notes"}},
	})
	require.NoError(t, err)
	require.Equal(t, []DropEntry{
		{Kind: "pointer_untracked", ID: "notes.md", Detail: "not tracked by git"},
	}, drops)
}

func TestValidatePointersCleanFileProducesNoDrop(t *testing.T) {
	root, idx := makeGitRoot(t, "v2.index")
	e, ok := idx[paths.Key("a.txt")]
	require.True(t, ok)
	materializeClean(t, root, e)

	drops, err := ValidatePointers(context.Background(), root, Pointers{
		Files: []FilePointer{{Path: "a.txt", Why: "clean"}},
	})
	require.NoError(t, err)
	require.Empty(t, drops, "a file matching its index entry's size and mtime is not reported")
}

func TestGitDirAsFileWorktree(t *testing.T) {
	// tmp/real/.git is a full git dir; tmp/wt is a worktree whose .git is a FILE pointing at it.
	tmp := t.TempDir()
	realGit := filepath.Join(tmp, "real", ".git")
	require.NoError(t, os.MkdirAll(realGit, 0o755))
	raw := readGitindexFixture(t, "v2.index")
	require.NoError(t, os.WriteFile(filepath.Join(realGit, "index"), raw, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(realGit, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))

	root := filepath.Join(tmp, "wt")
	require.NoError(t, os.Mkdir(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: ../real/.git\n"), 0o644))

	entries, err := parseIndex(raw)
	require.NoError(t, err)
	var a indexEntry
	for _, e := range entries {
		if e.Path == "a.txt" {
			a = e
		}
	}
	materializeClean(t, root, a)

	drops, err := ValidatePointers(context.Background(), root, Pointers{
		Files: []FilePointer{{Path: "a.txt", Why: "clean via the worktree gitdir"}},
	})
	require.NoError(t, err)
	require.Empty(t, drops, "the gitdir: file resolves and the real index is read")
}

func TestValidatePointersNoGitDir(t *testing.T) {
	root := t.TempDir()
	materialize(t, root, "present.ts", 12, 1767225480)

	drops, err := ValidatePointers(context.Background(), root, Pointers{
		Files: []FilePointer{
			{Path: "present.ts", Why: "on disk"},
			{Path: "gone.ts", Why: "not on disk"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"pointer_missing", "pointer_git_unavailable"}, dropKinds(drops),
		"working-tree checks still run; the git gap is one visible entry, not a silent skip")
	last := drops[len(drops)-1]
	require.Empty(t, last.ID)
	require.NotEmpty(t, last.Detail, "the reason is carried in the detail")
}

func TestValidatePointersUnreadableHeadDegradesBranchOnly(t *testing.T) {
	root, idx := makeGitRoot(t, "v2.index")
	require.NoError(t, os.Remove(filepath.Join(root, ".git", "HEAD")))
	e := idx[paths.Key("a.txt")]
	materialize(t, root, e.Path, int(e.Size)+3, e.MTimeSec)

	drops, err := ValidatePointers(context.Background(), root, Pointers{
		Files: []FilePointer{{Path: "a.txt", Why: "dirty, branch unknown"}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"pointer_dirty"}, dropKinds(drops),
		"a missing HEAD does not disable the index check")
	require.Equal(t, "modified since index on unknown", drops[0].Detail)
}

func TestValidatePointersCancelledContext(t *testing.T) {
	root, _ := makeGitRoot(t, "v2.index")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ValidatePointers(ctx, root, Pointers{
		Files: []FilePointer{{Path: "a.txt", Why: "never checked"}},
	})
	require.ErrorIs(t, err, context.Canceled,
		"a cancelled ctx is the only error ValidatePointers may return")
}
