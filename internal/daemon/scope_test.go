package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/stretchr/testify/require"
)

// The two object ids every scope test resolves to. They are 40 hex characters because that is what
// isObjectID accepts, and they differ in the last byte so a test can tell one branch tip from
// another.
const (
	commitMain = "1111111111111111111111111111111111111111"
	commitSide = "2222222222222222222222222222222222222222"
)

// writeFile writes p with its parent directories, so a test can lay out a git tree in one line.
func writeFile(t *testing.T, p, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
}

// plainCheckout lays out an ordinary repository at root, on branch at commit, and returns root.
func plainCheckout(t *testing.T, root, branch, commit string) string {
	t.Helper()
	git := filepath.Join(root, ".git")
	writeFile(t, filepath.Join(git, "HEAD"), "ref: refs/heads/"+branch+"\n")
	writeFile(t, filepath.Join(git, "refs", "heads", branch), commit+"\n")
	return root
}

// worktreeOf lays out a git worktree of the repository whose main checkout is at mainRoot, exactly
// the way git does: the worktree's .git is a FILE, its git directory lives under the main
// repository's worktrees/, and a commondir file points back.
func worktreeOf(t *testing.T, mainRoot, treeRoot, name, branch, commit string) string {
	t.Helper()
	wtGit := filepath.Join(mainRoot, ".git", "worktrees", name)
	writeFile(t, filepath.Join(treeRoot, ".git"), "gitdir: "+filepath.ToSlash(wtGit)+"\n")
	writeFile(t, filepath.Join(wtGit, "commondir"), "../..\n")
	writeFile(t, filepath.Join(wtGit, "HEAD"), "ref: refs/heads/"+branch+"\n")
	writeFile(t, filepath.Join(mainRoot, ".git", "refs", "heads", branch), commit+"\n")
	return treeRoot
}

// TestObserveScope_TwoWorktreesShareARepositoryAndDifferInTree is the case this whole mechanism
// exists for, and the one a path-string identity gets wrong. It is also how this repository is
// actually developed.
func TestObserveScope_TwoWorktreesShareARepositoryAndDifferInTree(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mainRoot := plainCheckout(t, filepath.Join(base, "repo"), "develop", commitMain)
	sideRoot := worktreeOf(t, mainRoot, filepath.Join(base, "repo-sp16"), "sp16", "feat/sp16", commitSide)

	a, aOm := daemon.ObserveScope(mainRoot, "sess-a")
	b, bOm := daemon.ObserveScope(sideRoot, "sess-b")
	require.Empty(t, aOm)
	require.Empty(t, bOm)

	require.Equal(t, a.Repository, b.Repository, "one repository")
	require.NotEqual(t, a.Worktree, b.Worktree, "two working trees")
	require.Equal(t, "develop", a.Branch)
	require.Equal(t, "feat/sp16", b.Branch)
	require.Equal(t, commitMain, a.Version)
	require.Equal(t, commitSide, b.Version)

	require.Equal(t, negknow.RelationSameRepository, negknow.Relate(a, b))
	require.Equal(t, negknow.RelationSameSession, negknow.Relate(a, a))
}

// TestObserveScope_TwoUnrelatedRepositoriesNeverRelate pins the boundary reuse must never cross,
// including when the two share a branch name, which is the common case.
func TestObserveScope_TwoUnrelatedRepositoriesNeverRelate(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	one := plainCheckout(t, filepath.Join(base, "one"), "develop", commitMain)
	two := plainCheckout(t, filepath.Join(base, "two"), "develop", commitMain)

	a, _ := daemon.ObserveScope(one, "sess-a")
	b, _ := daemon.ObserveScope(two, "sess-b")

	require.NotEqual(t, a.Repository, b.Repository)
	require.Equal(t, negknow.RelationUnrelated, negknow.Relate(a, b),
		"a shared branch name and an identical commit do not make two repositories one")
}

// TestObserveScope_SameWorktreeAcrossSessions pins the relation two sessions in one checkout get.
func TestObserveScope_SameWorktreeAcrossSessions(t *testing.T) {
	t.Parallel()

	root := plainCheckout(t, t.TempDir(), "develop", commitMain)
	a, _ := daemon.ObserveScope(root, "sess-a")
	b, _ := daemon.ObserveScope(root, "sess-b")

	require.Equal(t, negknow.RelationSameWorktree, negknow.Relate(a, b))
}

// TestObserveScope_NothingFailsAndEveryGapIsNamed pins the file's second rule: an unobservable
// field is a legitimate state that produces an omission, never an error and never a guess.
func TestObserveScope_NothingFailsAndEveryGapIsNamed(t *testing.T) {
	t.Parallel()

	t.Run("no git at all", func(t *testing.T) {
		t.Parallel()
		got, om := daemon.ObserveScope(t.TempDir(), "sess-a")
		require.True(t, got.Repository.Unknown())
		require.Equal(t, core.SessionID("sess-a"), got.Session)
		require.Len(t, om, 1)
		require.Contains(t, om[0].Reason, "no readable .git")
		require.Equal(t, negknow.RelationUnknown, negknow.Relate(got, got),
			"an unobserved repository relates to nothing, including itself")
	})

	t.Run("no session id", func(t *testing.T) {
		t.Parallel()
		root := plainCheckout(t, t.TempDir(), "develop", commitMain)
		got, om := daemon.ObserveScope(root, "")
		require.Empty(t, got.Session)
		require.Len(t, om, 1)
		require.Contains(t, om[0].Reason, "no session id")
		require.False(t, got.Repository.Unknown(), "the rest is still observed")
	})

	t.Run("unreadable HEAD", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".git", "config"), "\n")
		got, om := daemon.ObserveScope(root, "sess-a")
		require.False(t, got.Repository.Unknown(), "the repository is still identified")
		require.Empty(t, got.Branch)
		require.Empty(t, got.Version)
		require.Len(t, om, 1)
		require.Contains(t, om[0].Reason, "HEAD is unreadable")
	})

	t.Run("a branch with no commit yet", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
		got, om := daemon.ObserveScope(root, "sess-a")
		require.Equal(t, "main", got.Branch, "the branch is real even with no tip")
		require.Empty(t, got.Version)
		require.Len(t, om, 1)
		require.Contains(t, om[0].Reason, "no resolvable commit")
	})

	t.Run("a dangling worktree gitdir", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".git"), "gitdir: "+filepath.ToSlash(filepath.Join(root, "gone"))+"\n")
		got, om := daemon.ObserveScope(root, "sess-a")
		require.False(t, got.Repository.Unknown(),
			"a gitdir that does not exist still identifies a repository by name")
		require.Len(t, om, 1)
		require.Contains(t, om[0].Reason, "HEAD is unreadable")
	})

	t.Run("a .git file that is not a gitdir pointer", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".git"), "this is not a git file\n")
		got, om := daemon.ObserveScope(root, "sess-a")
		require.True(t, got.Repository.Unknown())
		require.Len(t, om, 1)
		require.Contains(t, om[0].Reason, "no readable .git")
	})
}

// TestObserveScope_DetachedHeadHasNoBranch pins the rule that keeps two unrelated detached
// checkouts from reporting RelationSameBranch: the branch is blank, not "HEAD".
func TestObserveScope_DetachedHeadHasNoBranch(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	one, two := filepath.Join(base, "one"), filepath.Join(base, "two")
	writeFile(t, filepath.Join(one, ".git", "HEAD"), commitMain+"\n")
	writeFile(t, filepath.Join(two, ".git", "HEAD"), commitSide+"\n")

	a, aOm := daemon.ObserveScope(one, "sess-a")
	require.Empty(t, a.Branch, "a detached HEAD names no branch")
	require.Equal(t, commitMain, a.Version, "the commit is still known")
	require.Len(t, aOm, 1)
	require.Contains(t, aOm[0].Reason, "detached")

	b, _ := daemon.ObserveScope(two, "sess-b")
	require.Equal(t, negknow.RelationUnrelated, negknow.Relate(a, b),
		"two blank branches are not a match, and these are different repositories anyway")
}

// TestObserveScope_ReadsAPackedRef pins that a repository whose branches have been packed still
// reports a version — the common case in a freshly cloned repository.
func TestObserveScope_ReadsAPackedRef(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/develop\n")
	writeFile(t, filepath.Join(root, ".git", "packed-refs"), strings.Join([]string{
		"# pack-refs with: peeled fully-peeled sorted",
		commitSide + " refs/heads/other",
		commitMain + " refs/heads/develop",
		"^" + commitSide,
		"",
	}, "\n"))

	got, om := daemon.ObserveScope(root, "sess-a")
	require.Empty(t, om)
	require.Equal(t, "develop", got.Branch)
	require.Equal(t, commitMain, got.Version)
}

// TestObserveScope_AWorktreesOwnRefShadowsTheCommonOne pins the lookup order. Per-worktree refs
// live under the worktree's own git directory, so reading the common directory first would report
// the wrong commit for exactly the layout this repository is developed in.
func TestObserveScope_AWorktreesOwnRefShadowsTheCommonOne(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mainRoot := plainCheckout(t, filepath.Join(base, "repo"), "develop", commitMain)
	treeRoot := worktreeOf(t, mainRoot, filepath.Join(base, "wt"), "wt", "shared", commitMain)

	// The worktree has its own tip for the same branch name.
	writeFile(t, filepath.Join(mainRoot, ".git", "worktrees", "wt", "refs", "heads", "shared"),
		commitSide+"\n")

	got, om := daemon.ObserveScope(treeRoot, "sess-a")
	require.Empty(t, om)
	require.Equal(t, commitSide, got.Version, "the worktree's own ref wins")
}

// TestObserveScope_AWorktreeWithNoCommondirLooksLikeItsOwnRepository pins the conservative
// fallback: an unreadable commondir refuses reuse as cross-repository rather than allowing it on a
// guess.
func TestObserveScope_AWorktreeWithNoCommondirLooksLikeItsOwnRepository(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mainRoot := plainCheckout(t, filepath.Join(base, "repo"), "develop", commitMain)

	treeRoot := filepath.Join(base, "wt")
	wtGit := filepath.Join(mainRoot, ".git", "worktrees", "wt")
	writeFile(t, filepath.Join(treeRoot, ".git"), "gitdir: "+filepath.ToSlash(wtGit)+"\n")
	writeFile(t, filepath.Join(wtGit, "HEAD"), "ref: refs/heads/develop\n")
	// No commondir file.

	a, _ := daemon.ObserveScope(mainRoot, "sess-a")
	b, _ := daemon.ObserveScope(treeRoot, "sess-b")
	require.Equal(t, negknow.RelationUnrelated, negknow.Relate(a, b),
		"without a commondir the two are refused as unrelated, not merged on a guess")
}

// TestObserveScope_IsStableAcrossCalls pins that identity is a function of what is on disk, so two
// observations of one unchanged checkout agree — which is what makes the relation algebra usable.
func TestObserveScope_IsStableAcrossCalls(t *testing.T) {
	t.Parallel()

	root := plainCheckout(t, t.TempDir(), "develop", commitMain)
	a, _ := daemon.ObserveScope(root, "sess-a")
	b, _ := daemon.ObserveScope(root, "sess-a")
	require.Equal(t, a, b)
	require.True(t, a.Observed(), "a plain checkout observes every field")
}
