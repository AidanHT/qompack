package daemon_test

import (
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/stretchr/testify/require"
)

// The instants every gate test reads against: now sits inside both the grant and the evidence.
const (
	gateNow     = core.UnixMilli(1_000)
	gateExpires = core.UnixMilli(9_000)
)

// twoTrees lays out one repository with two worktrees and returns the two observed scopes.
func twoTrees(t *testing.T) (here, there negknow.ReuseScope) {
	t.Helper()
	base := t.TempDir()
	mainRoot := plainCheckout(t, filepath.Join(base, "repo"), "develop", commitMain)
	sideRoot := worktreeOf(t, mainRoot, filepath.Join(base, "repo-sp16"), "sp16", "feat/sp16", commitSide)

	here, _ = daemon.ObserveScope(sideRoot, "sess-here")
	there, _ = daemon.ObserveScope(mainRoot, "sess-there")
	return here, there
}

// grantFor is a well-formed, unexpired user decision covering repo.
func grantFor(repo negknow.RepositoryID) negknow.Grant {
	return negknow.Grant{
		Repository:  repo,
		MaxRelation: negknow.RelationSameRepository,
		ExpiresAt:   gateExpires,
		Authority:   core.AuthorityExplicitDecision,
	}
}

// candidate is one settled, live, explicitly-expiring elimination from there.
func candidate(id string, there negknow.ReuseScope) daemon.ReuseCandidate {
	return daemon.ReuseCandidate{
		Record:    negknow.Record{ID: id, Target: "src/auth.ts", Approach: "widen pool timeout"},
		Origin:    there,
		Life:      negknow.Lifecycle{ExpiresAt: gateExpires},
		Authority: core.AuthorityToolObservation,
	}
}

// enabled is the phase-7 reuse config with the scoped-candidate switch on. Defaults() has it off,
// which is what every "report-only" assertion below relies on.
var enabled = config.Phase7ReuseCfg{ScopedCandidates: true}

// TestScopedReuse_ReportOnlyIsAStateNotAnAbsence pins the first rollout stage: with the switch off
// the gate still evaluates everything and still fills in the transcript, and returns nothing.
func TestScopedReuse_ReportOnlyIsAStateNotAnAbsence(t *testing.T) {
	t.Parallel()

	here, there := twoTrees(t)
	g := daemon.NewScopedReuse(config.Defaults().Runtime.Phase7.Reuse, here, grantFor(here.Repository), core.Omission{})
	require.False(t, g.Enabled(), "the shipped default is off")

	got, rep := g.Consider(gateNow, []daemon.ReuseCandidate{
		candidate("elim_a", there), candidate("elim_b", there),
	})

	require.Empty(t, got, "nothing reaches a session")
	require.False(t, rep.Enabled)
	require.Equal(t, 2, rep.Considered)
	require.Equal(t, 2, rep.Allowed, "but the report says what reuse WOULD have offered")
	require.Equal(t, 2, rep.ByRelation[negknow.RelationSameRepository])
	require.Empty(t, rep.Omissions)
}

// TestScopedReuse_ADisabledFeatureAndAnEmptyResultAreDifferent pins that a caller can tell them
// apart, since they call for opposite responses.
func TestScopedReuse_ADisabledFeatureAndAnEmptyResultAreDifferent(t *testing.T) {
	t.Parallel()

	here, there := twoTrees(t)

	_, off := daemon.NewScopedReuse(config.Phase7ReuseCfg{}, here, grantFor(here.Repository), core.Omission{}).
		Consider(gateNow, []daemon.ReuseCandidate{candidate("elim_a", there)})
	require.False(t, off.Enabled)
	require.Equal(t, 1, off.Allowed)

	got, on := daemon.NewScopedReuse(enabled, here, grantFor(here.Repository), core.Omission{}).
		Consider(gateNow, nil)
	require.True(t, on.Enabled)
	require.Zero(t, on.Considered)
	require.Empty(t, got)
}

// TestScopedReuse_OffersASiblingWorktreesEliminationWithItsAttribution pins the happy path, and
// that the decision travels with the record so a consumer can render where it came from.
func TestScopedReuse_OffersASiblingWorktreesEliminationWithItsAttribution(t *testing.T) {
	t.Parallel()

	here, there := twoTrees(t)
	got, rep := daemon.NewScopedReuse(enabled, here, grantFor(here.Repository), core.Omission{}).
		Consider(gateNow, []daemon.ReuseCandidate{candidate("elim_a", there)})

	require.Len(t, got, 1)
	require.Equal(t, "elim_a", got[0].Record.ID)
	require.Equal(t, negknow.RelationSameRepository, got[0].Applicability.Relation)
	require.Equal(t, there, got[0].Applicability.Origin,
		"the claim stays attributed to the worktree it came from")
	require.Equal(t, core.AuthorityToolObservation, got[0].Applicability.Attribution)
	require.Equal(t, 1, rep.Allowed)
	require.Zero(t, rep.Withheld+rep.Denied)
}

// TestScopedReuse_NoGrantOffersNothingEvenWithTheSwitchOn pins that the switch is not the
// authorization: the zero Grant authorizes nothing, so forgetting to pass one is safe.
func TestScopedReuse_NoGrantOffersNothingEvenWithTheSwitchOn(t *testing.T) {
	t.Parallel()

	here, there := twoTrees(t)
	got, rep := daemon.NewScopedReuse(enabled, here, negknow.Grant{}, core.Omission{}).
		Consider(gateNow, []daemon.ReuseCandidate{candidate("elim_a", there)})

	require.Empty(t, got)
	require.Zero(t, rep.Allowed)
	require.Equal(t, 1, rep.Withheld)
	require.Contains(t, joinReasons(rep), "names no observed repository")
}

// TestScopedReuse_WithheldAndDeniedAreCountedApart pins the distinction that tells a caller
// whether observing more could change the answer.
func TestScopedReuse_WithheldAndDeniedAreCountedApart(t *testing.T) {
	t.Parallel()

	here, there := twoTrees(t)

	// Denied: a corrected record, and one whose access was refused.
	corrected := candidate("elim_corrected", there)
	corrected.Life = negknow.Lifecycle{Event: negknow.LifecycleCorrected, By: "the user"}
	denied := candidate("elim_denied", there)
	denied.Access = core.OutcomeDenied

	// Withheld: a hypothesis the grant does not cover, and one with no declared expiry.
	guess := candidate("elim_guess", there)
	guess.Authority = core.AuthorityHypothesis
	unbounded := candidate("elim_unbounded", there)
	unbounded.Life = negknow.Lifecycle{}

	got, rep := daemon.NewScopedReuse(enabled, here, grantFor(here.Repository), core.Omission{}).
		Consider(gateNow, []daemon.ReuseCandidate{corrected, denied, guess, unbounded})

	require.Empty(t, got)
	require.Equal(t, 4, rep.Considered)
	require.Equal(t, 2, rep.Denied, "a correction and a refused read cannot be re-observed away")
	require.Equal(t, 2, rep.Withheld, "an uncovered guess and a missing expiry can be")

	all := joinReasons(rep)
	require.Contains(t, all, "corrected")
	require.Contains(t, all, "denied")
	require.Contains(t, all, "unsettled")
	require.Contains(t, all, "declares no expiry")
}

// TestScopedReuse_CrossRepositoryIsDeniedByTheBroadestGrant pins the boundary a switch cannot open.
func TestScopedReuse_CrossRepositoryIsDeniedByTheBroadestGrant(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	hereRoot := plainCheckout(t, filepath.Join(base, "here"), "develop", commitMain)
	thereRoot := plainCheckout(t, filepath.Join(base, "there"), "develop", commitMain)
	here, _ := daemon.ObserveScope(hereRoot, "sess-here")
	there, _ := daemon.ObserveScope(thereRoot, "sess-there")

	g := grantFor(here.Repository)
	g.AllowUnsettled = true
	g.Authority = core.AuthorityUserCorrection

	got, rep := daemon.NewScopedReuse(enabled, here, g, core.Omission{}).
		Consider(gateNow, []daemon.ReuseCandidate{candidate("elim_a", there)})

	require.Empty(t, got)
	require.Equal(t, 1, rep.Denied)
	require.Equal(t, 1, rep.ByRelation[negknow.RelationUnrelated])
	require.Contains(t, joinReasons(rep), "different repository")
}

// TestScopedReuse_DegradedDependencyCoverageWithholdsEverything pins that a ledger which could not
// verify freshness stops cross-scope reuse rather than affirming it.
func TestScopedReuse_DegradedDependencyCoverageWithholdsEverything(t *testing.T) {
	t.Parallel()

	here, there := twoTrees(t)
	degraded := core.Omission{Reason: "the last refresh could not compare dependency hashes"}

	got, rep := daemon.NewScopedReuse(enabled, here, grantFor(here.Repository), degraded).
		Consider(gateNow, []daemon.ReuseCandidate{candidate("elim_a", there), candidate("elim_b", there)})

	require.Empty(t, got)
	require.Equal(t, 2, rep.Withheld)
	require.Equal(t, 2, rep.Omissions[degraded.Reason])
}

// TestScopedReuse_UnobservedScopeWithholdsRatherThanMatching pins the child-failure case: a
// candidate whose origin nobody could observe is refused, because two blank scopes are not a match.
func TestScopedReuse_UnobservedScopeWithholdsRatherThanMatching(t *testing.T) {
	t.Parallel()

	here, _ := twoTrees(t)
	orphan := candidate("elim_orphan", negknow.ReuseScope{})

	got, rep := daemon.NewScopedReuse(enabled, here, grantFor(here.Repository), core.Omission{}).
		Consider(gateNow, []daemon.ReuseCandidate{orphan})

	require.Empty(t, got)
	require.Equal(t, 1, rep.Withheld)
	require.Equal(t, 1, rep.ByRelation[negknow.RelationUnknown])
	require.Contains(t, joinReasons(rep), "unobserved")
}

// TestScopedReuse_OmissionsAreGroupedByReason pins that a hundred records from one unauthorized
// worktree is one line in the transcript, not a hundred.
func TestScopedReuse_OmissionsAreGroupedByReason(t *testing.T) {
	t.Parallel()

	here, there := twoTrees(t)
	var cands []daemon.ReuseCandidate
	for i := 0; i < 100; i++ {
		c := candidate("elim", there)
		c.Life = negknow.Lifecycle{}
		cands = append(cands, c)
	}

	_, rep := daemon.NewScopedReuse(enabled, here, grantFor(here.Repository), core.Omission{}).
		Consider(gateNow, cands)

	require.Equal(t, 100, rep.Withheld)
	require.Len(t, rep.Omissions, 1, "one problem, one line")
	require.Equal(t, 100, rep.Omissions["evidence declares no expiry"])
}

// TestScopedReuse_ADaemonCannotMintItsOwnGrant pins that a grant made from an authority other than
// an explicit user decision authorizes nothing — content cannot promote its own authority.
func TestScopedReuse_ADaemonCannotMintItsOwnGrant(t *testing.T) {
	t.Parallel()

	here, there := twoTrees(t)
	for _, a := range []core.Authority{
		core.AuthorityToolObservation, core.AuthorityCandidateExtraction,
		core.AuthorityHypothesis, core.AuthorityConflict,
	} {
		g := grantFor(here.Repository)
		g.Authority = a
		got, rep := daemon.NewScopedReuse(enabled, here, g, core.Omission{}).
			Consider(gateNow, []daemon.ReuseCandidate{candidate("elim_a", there)})
		require.Empty(t, got, string(a))
		require.Equal(t, 1, rep.Withheld, string(a))
	}
}

// joinReasons flattens a report's omission reasons so a test can assert on the set.
func joinReasons(rep daemon.ReuseReport) string {
	out := ""
	for reason := range rep.Omissions {
		out += reason + "\n"
	}
	return out
}
