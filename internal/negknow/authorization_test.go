package negknow_test

import (
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/stretchr/testify/require"
)

// grantExpiry is when every well-formed grant in this file lapses. It sits after atExpiry and
// before farFuture, so one `now` can be inside the grant and another outside it.
const (
	grantExpiry = core.UnixMilli(5_000)
	farFuture   = core.UnixMilli(9_000)
)

// validGrant is the grant every "and then vary one thing" test starts from: a real user decision,
// for repoA, covering the whole repository, not yet lapsed.
func validGrant() negknow.Grant {
	return negknow.Grant{
		Repository:  repoA,
		MaxRelation: negknow.RelationSameRepository,
		ExpiresAt:   grantExpiry,
		Authority:   core.AuthorityExplicitDecision,
	}
}

// crossWorktree is the ordinary cross-scope request: settled evidence recorded in one worktree,
// reused in a sibling worktree of the same repository, with a declared expiry that has not passed.
func crossWorktree() negknow.ReuseRequest {
	return negknow.ReuseRequest{
		From:              scopeAt(treeMain, "develop", "v1", "sess-a"),
		To:                scopeAt(treeSide, "feat/sp16", "v2", "sess-b"),
		Life:              negknow.Lifecycle{ExpiresAt: farFuture},
		EvidenceAuthority: core.AuthorityToolObservation,
		Grant:             validGrant(),
		Now:               beforeExpiry,
	}
}

// reasons collects an Applicability's omission reasons, so a test can assert on the set of gaps
// without depending on the order two independent failures happen to be appended in.
func reasons(a negknow.Applicability) string {
	var b strings.Builder
	for _, o := range a.Omissions {
		b.WriteString(o.Reason)
		b.WriteString("\n")
	}
	return b.String()
}

// TestApplies_TheZeroRequestIsARefusal is the single most important test in the file: a caller
// that fills nothing in gets a refusal, never a licence. Every other rule below is enforcement;
// this is what makes forgetting safe.
func TestApplies_TheZeroRequestIsARefusal(t *testing.T) {
	t.Parallel()

	got := negknow.Applies(negknow.ReuseRequest{})
	require.Equal(t, negknow.ReuseWithheld, got.Reuse)
	require.False(t, got.Allowed())
	require.Equal(t, negknow.RelationUnknown, got.Relation)
	require.Equal(t, negknow.VersionUnobserved, got.Version)
	require.NotEmpty(t, got.Omissions)
	require.Contains(t, reasons(got), "unobserved")
}

// TestReusability_ZeroIsWithheld pins the enum's zero value directly, so a later reorder of the
// constants cannot quietly make ReuseAllowed the default.
func TestReusability_ZeroIsWithheld(t *testing.T) {
	t.Parallel()

	require.Equal(t, negknow.ReuseWithheld, negknow.Reusability(0))
	require.Equal(t, "withheld", negknow.Reusability(0).String())
	require.Equal(t, "allowed", negknow.ReuseAllowed.String())
	require.Equal(t, "denied", negknow.ReuseDenied.String())
	require.Equal(t, "withheld", negknow.Reusability(200).String())
}

// TestApplies_SameSessionNeedsNoGrant pins that evidence which never left the session it was
// recorded in is not crossing anything: no grant, no declared expiry, still allowed.
//
// This is the one case where an undeclared expiry does not withhold, and it is deliberate. §1
// requires an explicit expiry to carry evidence ACROSS a scope; requiring one to read back what
// this very session just wrote would make the ledger unusable for its primary job.
func TestApplies_SameSessionNeedsNoGrant(t *testing.T) {
	t.Parallel()

	same := scopeAt(treeMain, "develop", "v1", "sess-a")
	got := negknow.Applies(negknow.ReuseRequest{
		From: same, To: same,
		EvidenceAuthority: core.AuthorityHypothesis,
		Now:               beforeExpiry,
	})
	require.True(t, got.Allowed())
	require.Equal(t, negknow.RelationSameSession, got.Relation)
	require.Empty(t, got.Omissions)
	require.Equal(t, core.AuthorityHypothesis, got.Attribution,
		"a hypothesis stays a hypothesis even where reuse is allowed")
}

// TestApplies_DifferentRepositoryIsDeniedByEveryGrant pins that cross-repository reuse is not a
// permission this system can express. Granting the broadest relation, from a user correction,
// still does not carry a conclusion into another repository.
func TestApplies_DifferentRepositoryIsDeniedByEveryGrant(t *testing.T) {
	t.Parallel()

	req := crossWorktree()
	req.To = negknow.ReuseScope{
		Repository: repoB, Worktree: treeMain, Branch: "develop", Version: "v1", Session: "sess-b",
	}
	req.Grant = negknow.Grant{
		Repository:     repoB,
		MaxRelation:    negknow.RelationSameRepository,
		ExpiresAt:      grantExpiry,
		Authority:      core.AuthorityUserCorrection,
		AllowUnsettled: true,
	}

	got := negknow.Applies(req)
	require.Equal(t, negknow.ReuseDenied, got.Reuse)
	require.Equal(t, negknow.RelationUnrelated, got.Relation)
	require.Contains(t, reasons(got), "different repository")

	// And the grant itself cannot even name the relation that would be needed.
	require.False(t, negknow.RelationUnrelated.Narrower(negknow.RelationSameRepository))
}

// TestApplies_AccessOutcomeIsNeverReportedAsAbsence pins §"a retrieval error is never not tried":
// a denied or faulted read refuses the reuse, and does so BEFORE any other gap is reported, so a
// caller cannot mistake the fault for a missing elimination.
func TestApplies_AccessOutcomeIsNeverReportedAsAbsence(t *testing.T) {
	t.Parallel()

	for _, outcome := range []core.EvidenceOutcome{
		core.OutcomeDenied, core.OutcomeUnavailable, core.OutcomeCorrupt,
		core.OutcomeExpired, core.OutcomeUncertain, core.OutcomeAbsent,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			t.Parallel()
			req := crossWorktree()
			req.Access = outcome
			got := negknow.Applies(req)
			require.Equal(t, negknow.ReuseDenied, got.Reuse)
			require.Len(t, got.Omissions, 1, "a failed attempt is final; further gaps are not the obstacle")
			require.Contains(t, got.Omissions[0].Reason, string(outcome))
			require.Contains(t, got.Omissions[0].Recovery, "not an absent elimination")
		})
	}

	// An unreported attempt and a successful one both proceed.
	for _, outcome := range []core.EvidenceOutcome{"", core.OutcomeOK} {
		req := crossWorktree()
		req.Access = outcome
		require.True(t, negknow.Applies(req).Allowed(), string(outcome))
	}
}

// TestGrant_Valid_Table walks every reason a grant authorizes nothing. It is a separate exported
// method precisely so a configured grant can be rejected at load time rather than at reuse time.
func TestGrant_Valid_Table(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		mutate     func(*negknow.Grant)
		now        core.UnixMilli
		wantReason string
	}{
		{
			name:       "unknown repository",
			mutate:     func(g *negknow.Grant) { g.Repository = "" },
			wantReason: "names no observed repository",
		},
		{
			name:       "missing authority",
			mutate:     func(g *negknow.Grant) { g.Authority = "" },
			wantReason: "no recognized authority",
		},
		{
			name:       "unrecognized authority",
			mutate:     func(g *negknow.Grant) { g.Authority = "sudo" },
			wantReason: "no recognized authority",
		},
		{
			name:       "a hypothesis cannot authorize itself",
			mutate:     func(g *negknow.Grant) { g.Authority = core.AuthorityHypothesis },
			wantReason: "cannot authorize reuse",
		},
		{
			name:       "a tool observation cannot authorize reuse either",
			mutate:     func(g *negknow.Grant) { g.Authority = core.AuthorityToolObservation },
			wantReason: "cannot authorize reuse",
		},
		{
			name:       "a grant covering no reusable relation",
			mutate:     func(g *negknow.Grant) { g.MaxRelation = negknow.RelationUnknown },
			wantReason: "covers no reusable relation",
		},
		{
			name:       "a grant cannot name the unrelated relation",
			mutate:     func(g *negknow.Grant) { g.MaxRelation = negknow.RelationUnrelated },
			wantReason: "covers no reusable relation",
		},
		{
			name:       "an unbounded grant",
			mutate:     func(g *negknow.Grant) { g.ExpiresAt = 0 },
			wantReason: "declares no expiry",
		},
		{
			name:       "a lapsed grant",
			mutate:     func(g *negknow.Grant) {},
			now:        farFuture,
			wantReason: "expired at 5000",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := validGrant()
			tc.mutate(&g)
			now := tc.now
			if now == 0 {
				now = beforeExpiry
			}
			ok, why := g.Valid(now)
			require.False(t, ok)
			require.Contains(t, why.Reason, tc.wantReason)
			require.NotEmpty(t, why.Recovery)
		})
	}

	ok, why := validGrant().Valid(beforeExpiry)
	require.True(t, ok)
	require.Equal(t, core.Omission{}, why)

	// Grant expiry is inclusive, the same way evidence expiry is.
	ok, _ = validGrant().Valid(grantExpiry)
	require.False(t, ok, "a grant lapses at its expiry instant, not one millisecond later")
}

// TestApplies_CrossScopeRequiresAGrantThatCoversIt walks the authorization half of the gate.
func TestApplies_CrossScopeRequiresAGrantThatCoversIt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		mutate     func(*negknow.ReuseRequest)
		wantReason string
	}{
		{
			name:       "no grant at all",
			mutate:     func(r *negknow.ReuseRequest) { r.Grant = negknow.Grant{} },
			wantReason: "names no observed repository",
		},
		{
			name: "a grant for another repository",
			mutate: func(r *negknow.ReuseRequest) {
				r.Grant.Repository = repoB
			},
			wantReason: "authorization is for a different repository",
		},
		{
			name: "a grant too narrow for the relation",
			mutate: func(r *negknow.ReuseRequest) {
				r.Grant.MaxRelation = negknow.RelationSameWorktree
			},
			wantReason: "broader than the authorized same-worktree",
		},
		{
			name: "unsettled evidence without an unsettled grant",
			mutate: func(r *negknow.ReuseRequest) {
				r.EvidenceAuthority = core.AuthorityHypothesis
			},
			wantReason: "evidence is unsettled",
		},
		{
			name: "a candidate extraction is unsettled too",
			mutate: func(r *negknow.ReuseRequest) {
				r.EvidenceAuthority = core.AuthorityCandidateExtraction
			},
			wantReason: "evidence is unsettled",
		},
		{
			name: "evidence whose authority this build cannot read is unsettled",
			mutate: func(r *negknow.ReuseRequest) {
				r.EvidenceAuthority = "vibes"
			},
			wantReason: "evidence is unsettled",
		},
		{
			name: "an undeclared evidence expiry",
			mutate: func(r *negknow.ReuseRequest) {
				r.Life = negknow.Lifecycle{}
			},
			wantReason: "evidence declares no expiry",
		},
		{
			name: "degraded dependency coverage",
			mutate: func(r *negknow.ReuseRequest) {
				r.DependencyCoverage = core.Omission{Reason: "last refresh could not compare hashes"}
			},
			wantReason: "last refresh could not compare hashes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := crossWorktree()
			tc.mutate(&req)
			got := negknow.Applies(req)
			require.Equal(t, negknow.ReuseWithheld, got.Reuse,
				"an unmet requirement withholds; it does not deny")
			require.Contains(t, reasons(got), tc.wantReason)
		})
	}
}

// TestApplies_AccumulatesEveryUnmetRequirement pins that the authorization and coverage steps do
// not stop at the first failure: one transcript tells a caller everything it would have to fix.
func TestApplies_AccumulatesEveryUnmetRequirement(t *testing.T) {
	t.Parallel()

	req := crossWorktree()
	req.Grant.MaxRelation = negknow.RelationSameWorktree
	req.EvidenceAuthority = core.AuthorityHypothesis
	req.Life = negknow.Lifecycle{}
	req.DependencyCoverage = core.Omission{Reason: "coverage degraded"}

	got := negknow.Applies(req)
	require.Equal(t, negknow.ReuseWithheld, got.Reuse)
	require.Len(t, got.Omissions, 4)
	all := reasons(got)
	require.Contains(t, all, "broader than the authorized")
	require.Contains(t, all, "evidence is unsettled")
	require.Contains(t, all, "declares no expiry")
	require.Contains(t, all, "coverage degraded")
}

// TestApplies_AllowUnsettledIsTheOnlyWayToCarryAnotherSessionsGuess pins §1's "cannot silently
// become current user intent": the grant has to say so in as many words, and even then the claim
// keeps its own authority.
func TestApplies_AllowUnsettledIsTheOnlyWayToCarryAnotherSessionsGuess(t *testing.T) {
	t.Parallel()

	req := crossWorktree()
	req.EvidenceAuthority = core.AuthorityHypothesis
	require.False(t, negknow.Applies(req).Allowed())

	req.Grant.AllowUnsettled = true
	got := negknow.Applies(req)
	require.True(t, got.Allowed())
	require.Equal(t, core.AuthorityHypothesis, got.Attribution,
		"reuse never promotes a hypothesis to a decision")
	require.Equal(t, req.From, got.Origin, "the claim stays attributed to where it came from")
}

// TestApplies_AttributionNeverRises pins that no reuse path raises a claim's standing, and that an
// authority this build cannot read decays to the weakest one rather than to the caller's own.
func TestApplies_AttributionNeverRises(t *testing.T) {
	t.Parallel()

	for in, want := range map[core.Authority]core.Authority{
		core.AuthorityUserCorrection:      core.AuthorityUserCorrection,
		core.AuthorityExplicitDecision:    core.AuthorityExplicitDecision,
		core.AuthorityToolObservation:     core.AuthorityToolObservation,
		core.AuthorityCandidateExtraction: core.AuthorityCandidateExtraction,
		core.AuthorityHypothesis:          core.AuthorityHypothesis,
		core.AuthorityConflict:            core.AuthorityConflict,
		"":                                core.AuthorityHypothesis,
		"root":                            core.AuthorityHypothesis,
	} {
		req := crossWorktree()
		req.EvidenceAuthority = in
		req.Grant.AllowUnsettled = true
		require.Equal(t, want, negknow.Applies(req).Attribution, string(in))
	}
}

// TestApplies_OriginAndObservationsAreReportedOnEveryResult pins that the fields a consumer needs
// in order to render a reused claim honestly are populated whether or not reuse was allowed.
func TestApplies_OriginAndObservationsAreReportedOnEveryResult(t *testing.T) {
	t.Parallel()

	req := crossWorktree()
	req.Access = core.OutcomeDenied // refused at the very first step

	got := negknow.Applies(req)
	require.False(t, got.Allowed())
	require.Equal(t, req.From, got.Origin)
	require.Equal(t, negknow.RelationSameRepository, got.Relation)
	require.Equal(t, negknow.VersionMoved, got.Version)
	require.Equal(t, negknow.DispositionLive, got.Disposition)
	require.Equal(t, core.AuthorityToolObservation, got.Attribution)
}

// TestApplies_AMovedVersionDoesNotBlockReuse pins the separation of place from freshness: which
// commit was checked out is reported, but whether the code an elimination depended on actually
// changed stays depends_on's question.
func TestApplies_AMovedVersionDoesNotBlockReuse(t *testing.T) {
	t.Parallel()

	req := crossWorktree()
	require.Equal(t, negknow.VersionMoved, negknow.Applies(req).Version)
	require.True(t, negknow.Applies(req).Allowed())

	req.From.Version, req.To.Version = "", ""
	got := negknow.Applies(req)
	require.Equal(t, negknow.VersionUnobserved, got.Version)
	require.True(t, got.Allowed(), "an unobserved version is reported, not enforced, here")
}

// TestApplies_UnobservedScopeNamesTheSideThatIsMissing pins that the transcript points at the side
// a caller can act on, preferring the origin because that is the one it can no longer re-observe.
func TestApplies_UnobservedScopeNamesTheSideThatIsMissing(t *testing.T) {
	t.Parallel()

	req := crossWorktree()
	req.From = negknow.ReuseScope{Branch: "develop"}
	got := negknow.Applies(req)
	require.Equal(t, negknow.ReuseWithheld, got.Reuse)
	require.Contains(t, reasons(got), "evidence scope is unobserved")
	require.Contains(t, reasons(got), "repository")

	req = crossWorktree()
	req.To = negknow.ReuseScope{Branch: "feat/sp16"}
	got = negknow.Applies(req)
	require.Equal(t, negknow.ReuseWithheld, got.Reuse)
	require.Contains(t, reasons(got), "current scope is unobserved")
	require.Contains(t, got.Omissions[0].Recovery, "blank fields are not a match")
}

// TestApplies_TheHappyPathIsUnqualified pins that a fully authorized, fully observed, live reuse
// carries no omissions at all — so a consumer can treat a non-empty Omissions list as "something
// is qualified about this" without having to filter noise out of it first.
func TestApplies_TheHappyPathIsUnqualified(t *testing.T) {
	t.Parallel()

	got := negknow.Applies(crossWorktree())
	require.Equal(t, negknow.ReuseAllowed, got.Reuse)
	require.True(t, got.Allowed())
	require.Empty(t, got.Omissions)
	require.Equal(t, negknow.RelationSameRepository, got.Relation)
	require.Equal(t, negknow.DispositionLive, got.Disposition)
}
