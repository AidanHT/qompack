// V5 §4.12 — SP-16's scope/branch/version/expiry/unfinished-intent matrix, driven through the real
// composition-root pieces over one repository laid out on disk, with NO guaranteed warm
// improvement asserted anywhere.
//
// The identifier is retained from the historical row. The historical assertion — that a warm
// start measurably improves the first compaction of the next session, observed through
// state/warmstart.json, a warm-seeded CMS estimate and a populated scheduler breakdown on the
// first evaluation — is retired: none of those producers exist on this tree, and the plan's
// current criterion says in as many words that no warm improvement is guaranteed. What DOES
// exist, and is exercised here with every collaborator real, is the gate that decides whether
// another session's evidence may be reused at all:
//
//   - internal/daemon.ObserveScope reads a real git on-disk layout (a main checkout, sibling
//     worktrees, an unrelated repository) into negknow.ReuseScope values;
//   - internal/negknow.Open persists session A's eliminations and session B reads them back —
//     the real cross-session carry, through records/eliminations.jsonl;
//   - internal/daemon.NewScopedReuse applies negknow.Applies to those records under a supplied
//     grant, the real config switch and the real ledger's dependency-coverage watermark;
//   - internal/scheduler.WarmStart takes the gate's verdict as its Authorized input, exactly as
//     warmprior.go says the composition root must hand it over, and Blend bounds what a prior
//     may do.
//
// It is an in-process integration row rather than an e2e one because no shipped process seam
// carries any of this yet: NewScopedReuse, ObserveScope and WarmStart have no production call
// site, and both runtime.phase7.reuse switches default false. The one thing this row asserts
// about that state is that it is RECORDED as disabled — a disabled gate returns nothing and says
// so in its report — never that it passed.
package integration

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/testutil"
)

// The two sessions of the row. A records; B is the "next session" that would reuse.
const (
	x12SessionA = core.SessionID("sess-integration-v5-x12-a")
	x12SessionB = core.SessionID("sess-integration-v5-x12-b")
)

// The branch and the two object ids the on-disk repository resolves to. Forty hex characters is
// what daemon.ObserveScope's isObjectID accepts for SHA-1; the two differ so a moved tip is
// observable.
const (
	x12Branch     = "develop"
	x12SideBranch = "feat/v5-x12"
	x12CommitMain = "1212121212121212121212121212121212121212"
	x12CommitSide = "3434343434343434343434343434343434343434"
)

// x12Shelf is the shelf life every live candidate and every grant in this row declares, measured
// from the project clock. One hour is arbitrary; what matters is that it is declared (Applies
// withholds cross-scope reuse over an undeclared one) and that the FakeClock can be advanced past
// it.
const x12Shelf = time.Hour

// The two eliminations session A records: one project-scoped (the cross-session carry) and one
// session-scoped (which must NOT reach session B).
const (
	x12ProjectTarget   = "src/auth.ts:refreshToken"
	x12ProjectApproach = "widen pool timeout"
	x12ProjectReason   = "pgbouncer ignores statement_timeout in transaction pooling mode"
	x12SessionTarget   = "src/db/pool.ts"
	x12SessionApproach = "retry on ECONNRESET"
	x12SessionReason   = "the reset is server-initiated; retrying reproduces it"
)

// x12BlendHorizon is how many observations of the present the Blend bound is checked over. Ten
// is where warmprior.go's own doc comment says the prior has moved the answer by under three
// percent of the gap, so the bound is checked across the whole regime it describes.
const x12BlendHorizon = 10

// x12WriteFile writes p with its parents, so a git layout is one line per file.
func x12WriteFile(t *testing.T, p, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
}

// x12PlainCheckout lays out an ordinary repository at root, on branch at commit, in git's
// documented on-disk format — the same three files daemon.ObserveScope reads in production.
func x12PlainCheckout(t *testing.T, root, branch, commit string) string {
	t.Helper()
	git := filepath.Join(root, ".git")
	x12WriteFile(t, filepath.Join(git, "HEAD"), "ref: refs/heads/"+branch+"\n")
	x12WriteFile(t, filepath.Join(git, "refs", "heads", branch), commit+"\n")
	return root
}

// x12WorktreeOf lays out a git worktree of the repository whose main checkout is at mainRoot,
// exactly the way `git worktree add` does: the tree's .git is a FILE holding a gitdir pointer, its
// git directory lives under the main repository's worktrees/, and a commondir file points back.
// commit may be "" for a branch that has no tip yet.
func x12WorktreeOf(t *testing.T, mainRoot, treeRoot, name, branch, commit string) string {
	t.Helper()
	wtGit := filepath.Join(mainRoot, ".git", "worktrees", name)
	x12WriteFile(t, filepath.Join(treeRoot, ".git"), "gitdir: "+filepath.ToSlash(wtGit)+"\n")
	x12WriteFile(t, filepath.Join(wtGit, "commondir"), "../..\n")
	x12WriteFile(t, filepath.Join(wtGit, "HEAD"), "ref: refs/heads/"+branch+"\n")
	if commit != "" {
		x12WriteFile(t, filepath.Join(mainRoot, ".git", "refs", "heads", branch), commit+"\n")
	}
	return treeRoot
}

// x12Rig is the row's real collaborator set after session A has ended and session B has begun.
type x12Rig struct {
	P *testutil.Project
	// Origin is session A's observed scope at the main checkout: where the evidence came from.
	Origin negknow.ReuseScope
	// Carried is the one record session B's reopened ledger sees at project scope.
	Carried negknow.Record
	// LedgerB is session B's live ledger handle, whose Health supplies the coverage watermark.
	LedgerB negknow.Ledger
	// Trees are the sibling roots the matrix observes "here" from.
	SameBranchTree, SideTree, FreshTree, Unrelated, NoGit string
}

// x12Open lays out the repository, runs session A against a real ledger, ends it, and reopens the
// ledger as session B. Everything a scenario reads is the production type.
func x12Open(t *testing.T) *x12Rig {
	t.Helper()
	ctx := context.Background()

	p := testutil.NewProject(t)
	x12PlainCheckout(t, p.Root, x12Branch, x12CommitMain)

	// ── Session A: two real eliminations through the real ledger ────────────────────────────────
	ledA, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Session: x12SessionA, Log: p.Log, Clock: p.Clock,
	})
	require.NoError(t, err)
	projectID, err := ledA.Record(ctx, negknow.Record{
		Target: x12ProjectTarget, Approach: x12ProjectApproach, Reason: x12ProjectReason,
		Evidence: core.HashBytes(core.DomainChunk, []byte(x12ProjectReason)),
		Scope:    negknow.ScopeProject, Source: negknow.SourceMCP,
	})
	require.NoError(t, err)
	_, err = ledA.Record(ctx, negknow.Record{
		Target: x12SessionTarget, Approach: x12SessionApproach, Reason: x12SessionReason,
		Evidence: core.HashBytes(core.DomainChunk, []byte(x12SessionReason)),
		Scope:    negknow.ScopeSession, Source: negknow.SourceMCP,
	})
	require.NoError(t, err)
	origin, omissions := daemon.ObserveScope(p.Root, x12SessionA)
	require.Empty(t, omissions, "the main checkout must be fully observable")
	require.Equal(t, x12Branch, origin.Branch)
	require.Equal(t, x12CommitMain, origin.Version)
	require.True(t, origin.Observed(), "every scope field of the origin must be observed: %v", origin.Unobserved())
	require.NoError(t, ledA.Close(), "session A ends")

	// ── Session B: the same project, a fresh session id, the ledger reopened from disk ──────────
	ledB, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Session: x12SessionB, Log: p.Log, Clock: p.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ledB.Close() })

	carried, err := ledB.Active(ctx, negknow.ScopeProject)
	require.NoError(t, err)
	require.Len(t, carried, 1, "only the project-scoped elimination crosses sessions")
	require.Equal(t, projectID, carried[0].ID)
	require.Equal(t, x12SessionA, carried[0].Session, "the record stays attributed to the session that made it")
	visible, err := ledB.Active(ctx, negknow.ScopeSession)
	require.NoError(t, err)
	require.Len(t, visible, 1, "session A's session-scoped record must not be visible to session B")
	require.Equal(t, projectID, visible[0].ID)

	base := t.TempDir()
	// The no-repository case is a real directory with no .git in it or above it, not a missing
	// path: ObserveScope must report "nothing observed" for a tree that exists.
	noGit := filepath.Join(base, "no-git")
	require.NoError(t, os.MkdirAll(noGit, 0o700))
	return &x12Rig{
		P:       p,
		Origin:  origin,
		Carried: carried[0],
		LedgerB: ledB,
		SameBranchTree: x12WorktreeOf(t, p.Root, filepath.Join(base, "wt-same"), "wt-same",
			x12Branch, x12CommitMain),
		SideTree: x12WorktreeOf(t, p.Root, filepath.Join(base, "wt-side"), "wt-side",
			x12SideBranch, x12CommitSide),
		FreshTree: x12WorktreeOf(t, p.Root, filepath.Join(base, "wt-fresh"), "wt-fresh",
			"feat/no-tip-yet", ""),
		Unrelated: x12PlainCheckout(t, filepath.Join(base, "other-repo"), x12Branch, x12CommitMain),
		NoGit:     noGit,
	}
}

// Now is the project clock's current reading — the `now` every gate call in this row passes.
func (r *x12Rig) Now() core.UnixMilli { return core.NowMilli(r.P.Clock) }

// Candidate wraps the carried record as the gate sees it: settled, live, with a declared expiry
// measured from the project clock.
func (r *x12Rig) Candidate() daemon.ReuseCandidate {
	return daemon.ReuseCandidate{
		Record:    r.Carried,
		Origin:    r.Origin,
		Life:      negknow.Lifecycle{ExpiresAt: r.Now() + core.UnixMilli(x12Shelf.Milliseconds())},
		Authority: core.AuthorityToolObservation,
	}
}

// Grant is a well-formed explicit decision covering the observed repository up to rel.
func (r *x12Rig) Grant(rel negknow.Relation) negknow.Grant {
	return negknow.Grant{
		Repository:  r.Origin.Repository,
		MaxRelation: rel,
		ExpiresAt:   r.Now() + core.UnixMilli(x12Shelf.Milliseconds()),
		Authority:   core.AuthorityExplicitDecision,
	}
}

// Here observes root as session B's current scope.
func (r *x12Rig) Here(t *testing.T, root string) negknow.ReuseScope {
	t.Helper()
	here, _ := daemon.ObserveScope(root, x12SessionB)
	return here
}

// Gate composes the gate the way a composition root would: the supplied config, the observed
// current scope, the supplied grant, and the REAL ledger's dependency-coverage watermark.
func (r *x12Rig) Gate(cfg config.Phase7ReuseCfg, here negknow.ReuseScope, g negknow.Grant) *daemon.ScopedReuse {
	return daemon.NewScopedReuse(cfg, here, g, r.LedgerB.Health().DependencyCoverage)
}

// x12Enabled is the switch on. The shipped default is off, and the "recorded as disabled" arm
// asserts that from the real loaded config rather than from this value.
var x12Enabled = config.Phase7ReuseCfg{ScopedCandidates: true}

// x12Reasons flattens a report's omission reasons so an assertion can name the one it expects.
func x12Reasons(rep daemon.ReuseReport) string {
	var b strings.Builder
	for reason := range rep.Omissions {
		b.WriteString(reason)
		b.WriteByte('\n')
	}
	return b.String()
}

// x12EliminationLines returns records/eliminations.jsonl's line count.
func x12EliminationLines(t *testing.T, root string) int {
	t.Helper()
	raw, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Records, "eliminations.jsonl")))
	require.NoError(t, err)
	return len(strings.Split(strings.TrimRight(string(raw), "\n"), "\n"))
}

// TestV5_WarmStartImprovesTheFirstCompactionOfTheNextSession is V5-VERIFY §4.12.
//
// The negative controls are real switches, not stubs: the shipped config's scopedCandidates=false
// (the gate evaluates and reports but offers nothing), a corrupted HEAD file in a sibling worktree
// (the observed branch vanishes and a branch-scoped grant stops covering the tree), and the
// project clock advanced past a declared expiry (a live candidate becomes a denied one). Each is
// paired with the restored path in the same subtest so the control, not the fixture, is what
// flips the verdict.
func TestV5_WarmStartImprovesTheFirstCompactionOfTheNextSession(t *testing.T) {
	r := x12Open(t)
	linesAfterA := x12EliminationLines(t, r.P.Root)
	require.Equal(t, 2, linesAfterA, "session A appended exactly its two records")

	t.Run("scope relation decides what a grant can cover", func(t *testing.T) {
		grant := r.Grant(negknow.RelationSameRepository)
		cases := []struct {
			name  string
			here  negknow.ReuseScope
			grant negknow.Grant
			rel   negknow.Relation
			want  negknow.Reusability
			why   string
		}{
			{"same worktree, next session, no grant", r.Here(t, r.P.Root), negknow.Grant{},
				negknow.RelationSameWorktree, negknow.ReuseWithheld, "names no observed repository"},
			{"same worktree, next session, with a grant", r.Here(t, r.P.Root), grant,
				negknow.RelationSameWorktree, negknow.ReuseAllowed, ""},
			{"sibling worktree on the same branch", r.Here(t, r.SameBranchTree), grant,
				negknow.RelationSameBranch, negknow.ReuseAllowed, ""},
			{"sibling worktree on another branch", r.Here(t, r.SideTree), grant,
				negknow.RelationSameRepository, negknow.ReuseAllowed, ""},
			{"an unrelated repository with the same branch name and commit", r.Here(t, r.Unrelated), grant,
				negknow.RelationUnrelated, negknow.ReuseDenied, "different repository"},
			{"no repository observed at all", r.Here(t, r.NoGit), grant,
				negknow.RelationUnknown, negknow.ReuseWithheld, "unobserved"},
		}
		for _, tc := range cases {
			got, rep := r.Gate(x12Enabled, tc.here, tc.grant).Consider(r.Now(), []daemon.ReuseCandidate{r.Candidate()})
			require.Equal(t, 1, rep.ByRelation[tc.rel], "%s: relation must be %s, got %v", tc.name, tc.rel, rep.ByRelation)
			switch tc.want {
			case negknow.ReuseAllowed:
				require.Len(t, got, 1, "%s: must be offered; omissions: %v", tc.name, rep.Omissions)
				require.Equal(t, r.Origin, got[0].Applicability.Origin, "%s: the claim stays attributed to where it came from", tc.name)
				require.Equal(t, core.AuthorityToolObservation, got[0].Applicability.Attribution, tc.name)
				require.Equal(t, r.Carried.ID, got[0].Record.ID, tc.name)
			case negknow.ReuseDenied:
				require.Empty(t, got, tc.name)
				require.Equal(t, 1, rep.Denied, tc.name)
				require.Contains(t, x12Reasons(rep), tc.why, tc.name)
			default:
				require.Empty(t, got, tc.name)
				require.Equal(t, 1, rep.Withheld, tc.name)
				require.Contains(t, x12Reasons(rep), tc.why, tc.name)
			}
		}

		// The evidence that never left its session is the one case that needs nothing.
		sameSession, _ := daemon.ObserveScope(r.P.Root, x12SessionA)
		got, rep := r.Gate(x12Enabled, sameSession, negknow.Grant{}).Consider(r.Now(), []daemon.ReuseCandidate{r.Candidate()})
		require.Len(t, got, 1)
		require.Equal(t, 1, rep.ByRelation[negknow.RelationSameSession])
		require.Empty(t, rep.Omissions)

		// The broadest grant this type can express still cannot cross a repository.
		broadest := r.Grant(negknow.RelationSameRepository)
		broadest.AllowUnsettled = true
		broadest.Authority = core.AuthorityUserCorrection
		got, rep = r.Gate(x12Enabled, r.Here(t, r.Unrelated), broadest).Consider(r.Now(), []daemon.ReuseCandidate{r.Candidate()})
		require.Empty(t, got)
		require.Equal(t, 1, rep.Denied, "a grant cannot spell 'any repository'")
	})

	t.Run("branch: a branch-scoped grant covers the branch and the corrupt HEAD control severs it", func(t *testing.T) {
		grant := r.Grant(negknow.RelationSameBranch)

		got, rep := r.Gate(x12Enabled, r.Here(t, r.SameBranchTree), grant).Consider(r.Now(), []daemon.ReuseCandidate{r.Candidate()})
		require.Len(t, got, 1, "the same branch in another tree is inside a same-branch grant")
		require.Equal(t, negknow.RelationSameBranch, got[0].Applicability.Relation)

		got, rep = r.Gate(x12Enabled, r.Here(t, r.SideTree), grant).Consider(r.Now(), []daemon.ReuseCandidate{r.Candidate()})
		require.Empty(t, got, "another branch is broader than a same-branch grant")
		require.Equal(t, 1, rep.Withheld)
		require.Contains(t, x12Reasons(rep), "broader than the authorized")

		// NEGATIVE CONTROL: corrupt the same-branch worktree's HEAD. The repository is still
		// identified (the gitdir pointer and commondir are intact), but the branch is no longer
		// observed, so the relation degrades to same-repository and the branch-scoped grant stops
		// covering a tree it covered a moment ago.
		head := filepath.Join(r.P.Root, ".git", "worktrees", "wt-same", "HEAD")
		intact, err := os.ReadFile(head)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(head, nil, 0o600))
		// Restored on every exit path, so a failure above cannot leak the corruption into the
		// version subtest that observes the same tree.
		defer func() { require.NoError(t, os.WriteFile(head, intact, 0o600)) }()

		corrupted, omissions := daemon.ObserveScope(r.SameBranchTree, x12SessionB)
		require.Equal(t, r.Origin.Repository, corrupted.Repository, "the repository is still identified")
		require.Empty(t, corrupted.Branch, "a corrupt HEAD observes no branch, and blank is not a match")
		require.Len(t, omissions, 1)
		// A zero-byte HEAD is reported as unreadable (the read returns nothing), which is the same
		// honest gap: no branch, no version, no guess.
		require.Contains(t, omissions[0].Reason, "HEAD is unreadable")

		got, rep = r.Gate(x12Enabled, corrupted, grant).Consider(r.Now(), []daemon.ReuseCandidate{r.Candidate()})
		require.Empty(t, got, "with the branch unobserved the same-branch grant must not cover the tree")
		require.Equal(t, 1, rep.ByRelation[negknow.RelationSameRepository])
		require.Equal(t, 1, rep.Withheld)
		require.Contains(t, x12Reasons(rep), "broader than the authorized")

		// Restore, and the same tree under the same grant is covered again: the control was the cause.
		require.NoError(t, os.WriteFile(head, intact, 0o600))
		got, _ = r.Gate(x12Enabled, r.Here(t, r.SameBranchTree), grant).Consider(r.Now(), []daemon.ReuseCandidate{r.Candidate()})
		require.Len(t, got, 1)
	})

	t.Run("version: a moved or unobserved tip is reported, never a verdict", func(t *testing.T) {
		grant := r.Grant(negknow.RelationSameRepository)
		cases := []struct {
			name string
			root string
			want negknow.VersionAgreement
			rel  negknow.Relation
		}{
			{"same tip", r.SameBranchTree, negknow.VersionSame, negknow.RelationSameBranch},
			{"moved tip", r.SideTree, negknow.VersionMoved, negknow.RelationSameRepository},
			{"no tip yet", r.FreshTree, negknow.VersionUnobserved, negknow.RelationSameRepository},
		}
		for _, tc := range cases {
			here, omissions := daemon.ObserveScope(tc.root, x12SessionB)
			if tc.want == negknow.VersionUnobserved {
				require.Len(t, omissions, 1, tc.name)
				require.Contains(t, omissions[0].Reason, "no resolvable commit", tc.name)
			} else {
				require.Empty(t, omissions, tc.name)
			}
			got, rep := r.Gate(x12Enabled, here, grant).Consider(r.Now(), []daemon.ReuseCandidate{r.Candidate()})
			require.Len(t, got, 1, "%s: version agreement never changes the verdict; freshness is depends_on's question: %v", tc.name, rep.Omissions)
			require.Equal(t, tc.want, got[0].Applicability.Version, tc.name)
			require.Equal(t, tc.rel, got[0].Applicability.Relation, tc.name)
		}
	})

	t.Run("expiry: declared shelf lives end reuse, undeclared ones withhold it", func(t *testing.T) {
		here := r.Here(t, r.SideTree)
		grant := r.Grant(negknow.RelationSameRepository)
		live := r.Candidate()

		got, _ := r.Gate(x12Enabled, here, grant).Consider(r.Now(), []daemon.ReuseCandidate{live})
		require.Len(t, got, 1, "inside its shelf life the candidate is offered")
		require.Equal(t, negknow.DispositionLive, got[0].Applicability.Disposition)

		// NEGATIVE CONTROL: the project clock reaches the declared expiry. The same candidate and
		// the same (still valid) grant now deny rather than allow; the comparison is inclusive.
		r.P.Clock.Advance(x12Shelf)
		atExpiry := r.Now()
		require.Equal(t, live.Life.ExpiresAt, atExpiry, "the clock now sits exactly on the declared expiry")
		laterGrant := grant
		laterGrant.ExpiresAt = atExpiry + core.UnixMilli(x12Shelf.Milliseconds())
		got, rep := r.Gate(x12Enabled, here, laterGrant).Consider(atExpiry, []daemon.ReuseCandidate{live})
		require.Empty(t, got)
		require.Equal(t, 1, rep.Denied, "an expiry is a positive end, not a gap")
		require.Contains(t, x12Reasons(rep), "expired")

		// The grant's own expiry is a withholding, not a denial: observing a renewal fixes it.
		fresh := r.Candidate()
		got, rep = r.Gate(x12Enabled, here, grant).Consider(atExpiry, []daemon.ReuseCandidate{fresh})
		require.Empty(t, got)
		require.Equal(t, 1, rep.Withheld)
		require.Contains(t, x12Reasons(rep), "authorization expired")

		// An undeclared expiry is the gap §1 refuses to carry across scopes.
		unbounded := r.Candidate()
		unbounded.Life = negknow.Lifecycle{}
		got, rep = r.Gate(x12Enabled, here, laterGrant).Consider(atExpiry, []daemon.ReuseCandidate{unbounded})
		require.Empty(t, got)
		require.Equal(t, 1, rep.Withheld)
		require.Contains(t, x12Reasons(rep), "declares no expiry")

		// Deletion, supersession and correction end the evidence without touching any file.
		for _, ev := range []negknow.LifecycleEvent{
			negknow.LifecycleDeleted, negknow.LifecycleSuperseded, negknow.LifecycleCorrected,
		} {
			ended := r.Candidate()
			ended.Life.Event, ended.Life.By, ended.Life.At = ev, "x12", atExpiry
			got, rep = r.Gate(x12Enabled, here, laterGrant).Consider(atExpiry, []daemon.ReuseCandidate{ended})
			require.Empty(t, got, string(ev))
			require.Equal(t, 1, rep.Denied, string(ev))
			require.Contains(t, x12Reasons(rep), string(ev), string(ev))
		}

		// An event this build cannot read is uncertain, which is neither live nor a licence.
		unknown := r.Candidate()
		unknown.Life.Event = negknow.LifecycleEvent("archived")
		got, rep = r.Gate(x12Enabled, here, laterGrant).Consider(atExpiry, []daemon.ReuseCandidate{unknown})
		require.Empty(t, got)
		require.Equal(t, 1, rep.Withheld)
		require.Contains(t, x12Reasons(rep), "unrecognized lifecycle event")
	})

	t.Run("unfinished intent: another session's guesses need a grant that says so and never rise", func(t *testing.T) {
		here := r.Here(t, r.SideTree)
		grant := r.Grant(negknow.RelationSameRepository)

		for _, a := range []core.Authority{core.AuthorityHypothesis, core.AuthorityCandidateExtraction, core.Authority("")} {
			guess := r.Candidate()
			guess.Authority = a
			got, rep := r.Gate(x12Enabled, here, grant).Consider(r.Now(), []daemon.ReuseCandidate{guess})
			require.Empty(t, got, "%q must not cross a scope under a grant for settled work", a)
			require.Equal(t, 1, rep.Withheld, string(a))
			require.Contains(t, x12Reasons(rep), "unsettled", string(a))
		}

		explicit := grant
		explicit.AllowUnsettled = true
		guess := r.Candidate()
		guess.Authority = core.AuthorityHypothesis
		got, _ := r.Gate(x12Enabled, here, explicit).Consider(r.Now(), []daemon.ReuseCandidate{guess})
		require.Len(t, got, 1, "an explicit grant may carry a hypothesis")
		require.Equal(t, core.AuthorityHypothesis, got[0].Applicability.Attribution,
			"reuse never promotes authority: the hypothesis stays a hypothesis under an explicit-decision grant")

		unreadable := r.Candidate()
		unreadable.Authority = core.Authority("")
		got, _ = r.Gate(x12Enabled, here, explicit).Consider(r.Now(), []daemon.ReuseCandidate{unreadable})
		require.Len(t, got, 1)
		require.Equal(t, core.AuthorityHypothesis, got[0].Applicability.Attribution,
			"an unrecognized authority is read as the weakest standing, never upgraded")

		// A grant the daemon could have minted from its own observations authorizes nothing.
		minted := grant
		minted.Authority = core.AuthorityToolObservation
		got, rep := r.Gate(x12Enabled, here, minted).Consider(r.Now(), []daemon.ReuseCandidate{r.Candidate()})
		require.Empty(t, got)
		require.Equal(t, 1, rep.Withheld)
		require.Contains(t, x12Reasons(rep), "cannot authorize reuse")
	})

	t.Run("the shipped switches are recorded as disabled, never as passed", func(t *testing.T) {
		shipped := r.P.Cfg.Runtime.Phase7.Reuse
		require.False(t, shipped.ScopedCandidates, "runtime.phase7.reuse.scopedCandidates ships off")
		require.False(t, shipped.WarmPrior, "runtime.phase7.reuse.warmPrior ships off")

		// NEGATIVE CONTROL for the whole matrix above: under the real loaded config the same
		// allowed candidate is evaluated, counted as allowed, and NOT offered — the report-only
		// stage, distinguishable from an empty result by Enabled.
		gate := r.Gate(shipped, r.Here(t, r.SideTree), r.Grant(negknow.RelationSameRepository))
		require.False(t, gate.Enabled())
		got, rep := gate.Consider(r.Now(), []daemon.ReuseCandidate{r.Candidate()})
		require.Empty(t, got, "a disabled gate offers nothing")
		require.False(t, rep.Enabled)
		require.Equal(t, 1, rep.Considered)
		require.Equal(t, 1, rep.Allowed, "the transcript still says what reuse would have offered")

		// The third shipped switch this row's history named is `sketches.cms.warmStartFromProject`
		// (internal/config/config.go CMSCfg, documented in docs/config-reference.md). It ships TRUE
		// and is INERT: nothing outside internal/config reads it on this tree, and the CMS warm
		// seed it describes has no producer. It is recorded from the real loaded config so the
		// record of shipped switches is complete; its value is deliberately not asserted, because
		// neither true nor false would say anything about behavior.
		inert := r.P.Cfg.Sketches.CMS.WarmStartFromProject
		t.Logf("V5 §4.12: scoped reuse and warm prior are DISABLED by default on this tree "+
			"(scopedCandidates=%v warmPrior=%v); recorded as disabled, not as passed. "+
			"sketches.cms.warmStartFromProject=%v ships as an INERT key (no consumer outside "+
			"internal/config, no CMS warm seed producer); recorded, not asserted",
			shipped.ScopedCandidates, shipped.WarmPrior, inert)
	})

	t.Run("warm start: the gate's verdict authorizes, real history is too thin, and no improvement is promised", func(t *testing.T) {
		ctx := context.Background()
		policy := scheduler.DefaultWarmStartPolicy()

		// The Authorized input is the gate's own verdict, handed across as a value — the only way
		// internal/scheduler is allowed to learn it.
		allowed := negknow.Applies(negknow.ReuseRequest{
			From: r.Origin, To: r.Here(t, r.SideTree), Life: r.Candidate().Life,
			EvidenceAuthority: core.AuthorityToolObservation, Grant: r.Grant(negknow.RelationSameRepository),
			DependencyCoverage: r.LedgerB.Health().DependencyCoverage, Now: r.Now(),
		})
		require.True(t, allowed.Allowed(), "%v", allowed.Omissions)
		denied := negknow.Applies(negknow.ReuseRequest{
			From: r.Origin, To: r.Here(t, r.Unrelated), Life: r.Candidate().Life,
			EvidenceAuthority: core.AuthorityToolObservation, Grant: r.Grant(negknow.RelationSameRepository),
			Now: r.Now(),
		})
		require.False(t, denied.Allowed())

		// What the project's REAL history amounts to: one earlier session and two observations,
		// which is below the floor at which "the project tends to" means anything. An authorized
		// warm start over it is refused, and that refusal is the honest answer for a project on
		// its second session.
		all, err := r.LedgerB.All(ctx)
		require.NoError(t, err)
		sessions := map[core.SessionID]struct{}{}
		var newest core.UnixMilli
		for _, rec := range all {
			sessions[rec.Session] = struct{}{}
			newest = max(newest, rec.TS)
		}
		history := scheduler.PriorEvidence{
			Sessions:     len(sessions),
			Observations: len(all),
			AgeSeconds:   float64(r.Now()-newest) / float64(time.Second.Milliseconds()),
			Authorized:   allowed.Allowed(),
		}
		require.Equal(t, 1, history.Sessions)
		thin := scheduler.WarmStart(history, policy)
		require.False(t, thin.Apply, thin.Label)
		require.Zero(t, thin.Weight)
		require.Contains(t, thin.Label, "below the")

		// The same ample history is refused outright when the gate denied it: there is no
		// discount rate for missing consent.
		ample := scheduler.PriorEvidence{
			Sessions: policy.MinSessions, Observations: policy.MinObservations,
			AgeSeconds: 0, Authorized: denied.Allowed(),
		}
		refused := scheduler.WarmStart(ample, policy)
		require.False(t, refused.Apply)
		require.Contains(t, refused.Label, "not authorized")

		// Authorized and ample: a labeled candidate whose weight is capped, and whose influence
		// is bounded by that weight against every observation of the present. Nothing here says
		// the blended answer is BETTER — only that history can never outvote what the session
		// measured, and that a refused prior leaves the observed value byte-identical.
		ample.Authorized = allowed.Allowed()
		applied := scheduler.WarmStart(ample, policy)
		require.True(t, applied.Apply, applied.Label)
		require.LessOrEqual(t, applied.Weight, policy.MaxWeight)
		require.Contains(t, applied.Label, "warm prior")

		const prior, observed = 3.0, 1.0
		require.Equal(t, prior, scheduler.Blend(prior, observed, 0, applied),
			"with nothing observed the answer IS the labeled prior — a candidate, not a measurement")
		// Blend's own weighting is deliberately NOT re-derived here: an equality against
		// Weight/(Weight+n) of the gap would only echo warmprior.go's formula back at itself.
		// The three properties below are what the bound means and hold independently of the
		// exact weighting: the answer stays strictly between the two inputs, one observation of
		// the present already outweighs the whole prior, and every further observation pulls the
		// answer strictly closer to what the session measured.
		last := math.Abs(scheduler.Blend(prior, observed, 0, applied) - observed)
		for n := 1; n <= x12BlendHorizon; n++ {
			blended := scheduler.Blend(prior, observed, n, applied)
			gap := math.Abs(blended - observed)
			require.Greater(t, blended, observed, "at n=%d the prior still has a voice", n)
			require.Less(t, blended, prior, "at n=%d the present has already been heard", n)
			require.Less(t, gap, math.Abs(blended-prior),
				"at n=%d one observation of the present already outweighs the whole prior", n)
			require.Less(t, gap, last,
				"at n=%d another observation pulls the answer strictly closer to the present", n)
			last = gap
		}
		require.Equal(t, observed, scheduler.Blend(prior, observed, 0, refused),
			"a refused prior is exactly the unmodified system, even with nothing observed")
		require.Equal(t, observed, scheduler.Blend(prior, observed, 0, thin))
	})

	// Session B read, considered and refused; it rewrote nothing. The append-only conformance
	// list holds on the project as a whole.
	require.Equal(t, linesAfterA, x12EliminationLines(t, r.P.Root),
		"considering candidates must not touch records/eliminations.jsonl")
	r.P.AssertAppendOnly(t)
}
