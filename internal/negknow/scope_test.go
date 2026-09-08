package negknow_test

import (
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/stretchr/testify/require"
)

// The two repositories and the three worktrees every scope test below is built from. They are
// minted through the real constructors rather than written as literals, because what the tests
// need to hold is that two worktrees of ONE repository agree on the repository id — which is a
// property of NewRepositoryID, not of a hand-picked string.
var (
	repoA = negknow.NewRepositoryID(negknow.RepositoryEvidence{
		CommonDir:  "c:/proj/qompack/.git",
		RootCommit: "9e3363d0000000000000000000000000000000aa",
	})
	repoB = negknow.NewRepositoryID(negknow.RepositoryEvidence{
		CommonDir:  "c:/proj/other/.git",
		RootCommit: "9e3363d0000000000000000000000000000000bb",
	})
	treeMain = negknow.NewWorktreeID(repoA, "c:/proj/qompack")
	treeSide = negknow.NewWorktreeID(repoA, "c:/proj/qompack-sp16")
)

// scopeAt builds a fully observed scope in repoA, so a test that wants to vary ONE field can say
// so instead of restating five.
func scopeAt(tree negknow.WorktreeID, branch, version string, sess core.SessionID) negknow.ReuseScope {
	return negknow.ReuseScope{
		Repository: repoA,
		Worktree:   tree,
		Branch:     branch,
		Version:    version,
		Session:    sess,
	}
}

// TestNewRepositoryID_IdentityIsObservedNotGuessed pins the property the whole file rests on: a
// repository's identity comes from evidence about the REPOSITORY, so two worktrees of it agree,
// and it is unknown — not merely different — when nothing was observed.
func TestNewRepositoryID_IdentityIsObservedNotGuessed(t *testing.T) {
	t.Parallel()

	ev := negknow.RepositoryEvidence{CommonDir: "c:/proj/qompack/.git", RootCommit: "abc123"}
	require.Equal(t, negknow.NewRepositoryID(ev), negknow.NewRepositoryID(ev),
		"the same evidence must mint the same id")

	// A worktree resolves to the SAME common directory as its main checkout, which is exactly why
	// the id is minted from that rather than from the working-tree path.
	fromWorktree := negknow.NewRepositoryID(negknow.RepositoryEvidence{
		CommonDir: "c:/proj/qompack/.git", RootCommit: "abc123",
	})
	require.Equal(t, negknow.NewRepositoryID(ev), fromWorktree)

	require.NotEqual(t, repoA, repoB, "different repositories must not collide")

	require.True(t, negknow.NewRepositoryID(negknow.RepositoryEvidence{}).Unknown(),
		"no evidence must yield the unknown id, never a digest of two blanks")
	require.False(t, negknow.RepositoryEvidence{}.Observed())
	require.True(t, negknow.RepositoryEvidence{RootCommit: "abc"}.Observed(),
		"either half on its own is still evidence")
}

// TestNewRepositoryID_RootCommitCaseFolds pins that a root commit written in upper case is the
// same repository as one written in lower case; git prints both.
func TestNewRepositoryID_RootCommitCaseFolds(t *testing.T) {
	t.Parallel()

	lower := negknow.NewRepositoryID(negknow.RepositoryEvidence{RootCommit: "9e3363dabc"})
	upper := negknow.NewRepositoryID(negknow.RepositoryEvidence{RootCommit: "9E3363DABC"})
	require.Equal(t, lower, upper)
}

// TestNewWorktreeID_ScopedToItsRepository pins that the same relative layout under two different
// repositories does not collide, and that an unknown repository or an empty root yields the
// unknown worktree id rather than a digest.
func TestNewWorktreeID_ScopedToItsRepository(t *testing.T) {
	t.Parallel()

	require.NotEqual(t, treeMain, treeSide, "two trees of one repository must differ")
	require.NotEqual(t,
		negknow.NewWorktreeID(repoA, "c:/proj/x"),
		negknow.NewWorktreeID(repoB, "c:/proj/x"),
		"one path under two repositories must not collide")

	require.True(t, negknow.NewWorktreeID("", "c:/proj/x").Unknown())
	require.True(t, negknow.NewWorktreeID(repoA, "").Unknown())
}

// TestRelate_Table is the relation algebra: the narrowest relation observation establishes, with
// unknown and unrelated at the ends.
//
// The zero-scope row is the reason the type exists. Two ReuseScopes nobody observed are equal as
// Go values, and a naive implementation would call that RelationSameSession and hand a caller
// permission to reuse anything anywhere.
func TestRelate_Table(t *testing.T) {
	t.Parallel()

	const (
		sessA = core.SessionID("sess-a")
		sessB = core.SessionID("sess-b")
	)

	for _, tc := range []struct {
		name     string
		from, to negknow.ReuseScope
		want     negknow.Relation
	}{
		{
			name: "two unobserved scopes are unknown, not identical",
			want: negknow.RelationUnknown,
		},
		{
			name: "an unobserved origin is unknown even against a fully observed target",
			to:   scopeAt(treeMain, "develop", "v1", sessA),
			want: negknow.RelationUnknown,
		},
		{
			name: "different repositories are unrelated",
			from: scopeAt(treeMain, "develop", "v1", sessA),
			to: negknow.ReuseScope{
				Repository: repoB, Worktree: treeMain, Branch: "develop", Version: "v1", Session: sessA,
			},
			want: negknow.RelationUnrelated,
		},
		{
			name: "one session is the narrowest relation",
			from: scopeAt(treeMain, "develop", "v1", sessA),
			to:   scopeAt(treeMain, "develop", "v1", sessA),
			want: negknow.RelationSameSession,
		},
		{
			name: "one worktree, two sessions",
			from: scopeAt(treeMain, "develop", "v1", sessA),
			to:   scopeAt(treeMain, "develop", "v1", sessB),
			want: negknow.RelationSameWorktree,
		},
		{
			name: "two worktrees sharing a branch name",
			from: scopeAt(treeMain, "develop", "v1", sessA),
			to:   scopeAt(treeSide, "develop", "v2", sessB),
			want: negknow.RelationSameBranch,
		},
		{
			name: "one repository, nothing narrower agrees",
			from: scopeAt(treeMain, "develop", "v1", sessA),
			to:   scopeAt(treeSide, "feat/sp16", "v2", sessB),
			want: negknow.RelationSameRepository,
		},
		{
			name: "blank sessions do not match each other",
			from: scopeAt(treeMain, "develop", "v1", ""),
			to:   scopeAt(treeSide, "feat/sp16", "v2", ""),
			want: negknow.RelationSameRepository,
		},
		{
			name: "blank worktrees do not match each other",
			from: scopeAt("", "develop", "v1", "sess-a"),
			to:   scopeAt("", "feat/sp16", "v2", "sess-b"),
			want: negknow.RelationSameRepository,
		},
		{
			name: "blank branches do not match each other",
			from: scopeAt(treeMain, "", "v1", "sess-a"),
			to:   scopeAt(treeSide, "", "v2", "sess-b"),
			want: negknow.RelationSameRepository,
		},
		{
			name: "a moved version does not change the structural relation",
			from: scopeAt(treeMain, "develop", "v1", sessA),
			to:   scopeAt(treeMain, "develop", "v99", sessA),
			want: negknow.RelationSameSession,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, negknow.Relate(tc.from, tc.to))
			require.Equal(t, tc.want, negknow.Relate(tc.to, tc.from), "Relate must be symmetric")
		})
	}
}

// TestRelation_Narrower_RefusesTheTwoNonRelations pins that neither end of the ordering can be
// used as a grant: "at least unrelated" is not a question with a useful answer, and returning
// true for it would let a broad grant authorize crossing a repository boundary.
func TestRelation_Narrower_RefusesTheTwoNonRelations(t *testing.T) {
	t.Parallel()

	every := []negknow.Relation{
		negknow.RelationUnknown, negknow.RelationUnrelated, negknow.RelationSameRepository,
		negknow.RelationSameBranch, negknow.RelationSameWorktree, negknow.RelationSameSession,
	}
	for _, min := range every {
		require.False(t, negknow.RelationUnknown.Narrower(min), "unknown is narrower than nothing")
		require.False(t, negknow.RelationUnrelated.Narrower(min), "unrelated is narrower than nothing")
	}
	for _, rel := range every {
		require.False(t, rel.Narrower(negknow.RelationUnknown))
		require.False(t, rel.Narrower(negknow.RelationUnrelated))
	}

	require.True(t, negknow.RelationSameSession.Narrower(negknow.RelationSameRepository),
		"the narrowest relation is covered by the broadest grant")
	require.True(t, negknow.RelationSameBranch.Narrower(negknow.RelationSameBranch),
		"a relation covers itself")
	require.False(t, negknow.RelationSameRepository.Narrower(negknow.RelationSameWorktree),
		"a broader relation is not covered by a narrower grant")
}

// TestRelation_String_Table pins the transcript spellings, including the fallback for a value no
// version of this package mints.
func TestRelation_String_Table(t *testing.T) {
	t.Parallel()

	for rel, want := range map[negknow.Relation]string{
		negknow.RelationUnknown:        "unknown",
		negknow.RelationUnrelated:      "unrelated",
		negknow.RelationSameRepository: "same-repository",
		negknow.RelationSameBranch:     "same-branch",
		negknow.RelationSameWorktree:   "same-worktree",
		negknow.RelationSameSession:    "same-session",
		negknow.Relation(200):          "unknown",
	} {
		require.Equal(t, want, rel.String())
	}
}

// TestVersions_Table pins that an unobserved version on either side is never an agreeing one.
func TestVersions_Table(t *testing.T) {
	t.Parallel()

	same := negknow.Versions(scopeAt(treeMain, "d", "v1", "s"), scopeAt(treeMain, "d", "v1", "s"))
	require.Equal(t, negknow.VersionSame, same)

	moved := negknow.Versions(scopeAt(treeMain, "d", "v1", "s"), scopeAt(treeMain, "d", "v2", "s"))
	require.Equal(t, negknow.VersionMoved, moved)

	for _, tc := range []struct{ from, to negknow.ReuseScope }{
		{scopeAt(treeMain, "d", "", "s"), scopeAt(treeMain, "d", "v1", "s")},
		{scopeAt(treeMain, "d", "v1", "s"), scopeAt(treeMain, "d", "", "s")},
		{scopeAt(treeMain, "d", "", "s"), scopeAt(treeMain, "d", "", "s")},
	} {
		require.Equal(t, negknow.VersionUnobserved, negknow.Versions(tc.from, tc.to),
			"two blank versions are not an agreement")
	}
}

// TestReuseScope_Unobserved_NamesEveryGap pins that a transcript can say WHICH observation is
// missing, in a stable order, rather than only that coverage was incomplete.
func TestReuseScope_Unobserved_NamesEveryGap(t *testing.T) {
	t.Parallel()

	require.Equal(t,
		[]string{"branch", "repository", "session", "version", "worktree"},
		negknow.ReuseScope{}.Unobserved())

	require.Empty(t, scopeAt(treeMain, "develop", "v1", "sess").Unobserved())
	require.True(t, scopeAt(treeMain, "develop", "v1", "sess").Observed())
	require.False(t, negknow.ReuseScope{}.Observed())

	require.Equal(t, []string{"branch", "version"},
		scopeAt(treeMain, "", "", "sess").Unobserved())
}
