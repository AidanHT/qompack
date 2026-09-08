package state_test

import (
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/state"
	"github.com/stretchr/testify/require"
)

// The two scopes every test below uses. They differ only in session, which is exactly the
// distinction invariant 7's scope half is about.
var (
	wt      = "C:/proj"
	scopeA  = state.SessionScope(wt, core.SessionID("sess-a"))
	scopeB  = state.SessionScope(wt, core.SessionID("sess-b"))
	scopeWT = state.WorktreeScope(wt)
)

// rec builds a record that Validate accepts, so that each test can vary the ONE field it is
// about. Every record cites a source observation: a derived record with no immutable evidence
// behind it is not something this package will accept in the first place.
func rec(id string, a core.Authority, sc state.Scope, claim string) state.Record {
	return state.Record{
		Version:          state.RecordVersion,
		ID:               state.RecordID(id),
		Authority:        a,
		Scope:            sc,
		Claim:            claim,
		Dependencies:     state.Dependencies{Coverage: state.DepCoverageComplete},
		Validity:         core.EvidenceValidity{From: 1, Generation: 1},
		Sources:          []core.ObservationID{core.ObservationID("obs-" + id)},
		TransformVersion: state.TransformVersion,
		HashVersion:      core.EvidenceHashVersion,
	}
}

func ids(rs []state.Record) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, string(r.ID))
	}
	return out
}

// Behaviour 1: a later authorized correction supersedes an obsolete instruction, and the
// superseded record stays readable and linked. Nothing is destroyed.
func TestCorrect_SupersedesObsoleteInstructionAndRetainsHistory(t *testing.T) {
	s := state.NewSet()

	old := rec("r1", core.AuthorityExplicitDecision, scopeA, "use tabs")
	require.NoError(t, s.Add(old))

	fix := rec("r2", core.AuthorityUserCorrection, scopeA, "use spaces")
	require.NoError(t, s.Correct(fix, old.ID, 7, "user reversed the earlier decision"))

	// The correction is what applies now.
	require.Equal(t, []string{"r2"}, ids(s.Applicable(scopeA)))

	// The superseded record is still there, still readable, and still says what it said.
	got, ok := s.Get(old.ID)
	require.True(t, ok, "a superseded record must remain readable")
	require.Equal(t, "use tabs", got.Claim)
	require.Equal(t, core.AuthorityExplicitDecision, got.Authority,
		"superseding a record must not rewrite its authority")
	require.True(t, got.Superseded())

	// Linked in both directions.
	require.Equal(t, []state.RecordID{fix.ID}, got.Lineage.SupersededBy)
	require.Equal(t, []state.RecordID{fix.ID}, got.Lineage.CorrectedBy)
	newer, ok := s.Get(fix.ID)
	require.True(t, ok)
	require.Equal(t, []state.RecordID{old.ID}, newer.Lineage.Supersedes)
	require.Equal(t, []state.RecordID{old.ID}, newer.Lineage.Corrects)

	// History is retained on both sides, with the authority that made the transition.
	require.Len(t, got.Lineage.History, 2)
	require.Equal(t, state.LineageSuperseded, got.Lineage.History[0].Kind)
	require.Equal(t, state.LineageCorrected, got.Lineage.History[1].Kind)
	require.Equal(t, core.AuthorityUserCorrection, got.Lineage.History[0].Authority)
	require.Equal(t, core.TurnIndex(7), got.Lineage.History[0].Turn)
	require.Equal(t, "user reversed the earlier decision", got.Lineage.History[0].Note)
	require.Len(t, newer.Lineage.History, 2)

	// Both records survive: All() is the whole history, not just the head.
	require.Equal(t, []string{"r1", "r2"}, ids(s.All()))
}

// Behaviour 2: a hypothesis or candidate extraction never automatically becomes an authoritative
// instruction. Only a user correction or an explicit decision may supersede an authoritative
// record; everything else that tries is refused.
func TestSupersede_OnlyAuthoritativeSourcesMaySupersedeAuthoritativeState(t *testing.T) {
	for _, tc := range []struct {
		name     string
		newer    core.Authority
		existing core.Authority
		want     bool
	}{
		{"hypothesis over decision", core.AuthorityHypothesis, core.AuthorityExplicitDecision, false},
		{"candidate over decision", core.AuthorityCandidateExtraction, core.AuthorityExplicitDecision, false},
		{"tool observation over decision", core.AuthorityToolObservation, core.AuthorityExplicitDecision, false},
		{"tool observation over correction", core.AuthorityToolObservation, core.AuthorityUserCorrection, false},
		{"correction over decision", core.AuthorityUserCorrection, core.AuthorityExplicitDecision, true},
		{"decision over correction", core.AuthorityExplicitDecision, core.AuthorityUserCorrection, true},
		{"hypothesis over candidate", core.AuthorityHypothesis, core.AuthorityCandidateExtraction, false},
		{"candidate over hypothesis", core.AuthorityCandidateExtraction, core.AuthorityHypothesis, true},
		{"tool observation over hypothesis", core.AuthorityToolObservation, core.AuthorityHypothesis, true},
		{"conflict over hypothesis", core.AuthorityConflict, core.AuthorityHypothesis, false},
		{"unknown label over hypothesis", core.Authority("promoted"), core.AuthorityHypothesis, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, state.CanSupersede(tc.newer, tc.existing))
		})
	}

	// And the rule is enforced by the set, not merely advertised by the predicate.
	s := state.NewSet()
	decision := rec("d1", core.AuthorityExplicitDecision, scopeA, "auth lives in pkg/auth")
	require.NoError(t, s.Add(decision))

	guess := rec("h1", core.AuthorityHypothesis, scopeA, "auth lives in pkg/session")
	err := s.Supersede(guess, decision.ID, 3, "model guessed")
	require.ErrorIs(t, err, core.ErrContract)
	require.ErrorContains(t, err, "hypothesis")

	// The refusal leaves the set untouched: no half-applied supersession, no smuggled record.
	require.Equal(t, []string{"d1"}, ids(s.All()))
	still, ok := s.Get(decision.ID)
	require.True(t, ok)
	require.False(t, still.Superseded())

	// Correct is stricter still: only the two authoritative labels may correct anything.
	require.ErrorIs(t, s.Correct(rec("t1", core.AuthorityToolObservation, scopeA, "grep says pkg/session"),
		decision.ID, 4, ""), core.ErrContract)
}

// Behaviour 3 (required invariant 7): derived state cannot silently acquire user authority, and
// it cannot overwrite the immutable evidence it was derived from. Evidence identities are
// referenced; they are never rewritten.
func TestSet_CannotAcquireUserAuthorityOrRewriteEvidence(t *testing.T) {
	s := state.NewSet()

	// An unlabelled record is not user authority by default: the zero Authority is refused.
	blank := rec("z1", "", scopeA, "unlabelled")
	require.ErrorIs(t, s.Add(blank), core.ErrContract)

	// A producer may not assert its own lineage either: only Supersede/Correct/Conflict/Resolve
	// create links, and each of those checks authority first.
	forged := rec("f1", core.AuthorityHypothesis, scopeA, "I supersede the user")
	forged.Lineage.Supersedes = []state.RecordID{"d1"}
	require.ErrorIs(t, s.Add(forged), core.ErrContract)

	// Evidence identities survive Add unchanged, and the caller's slice is not aliased by the
	// stored record: mutating it afterwards cannot rewrite what the record cites.
	sources := []core.ObservationID{"obs-1", "obs-2"}
	h := rec("h1", core.AuthorityHypothesis, scopeA, "maybe")
	h.Sources = sources
	require.NoError(t, s.Add(h))
	sources[0] = "obs-forged"

	stored, ok := s.Get("h1")
	require.True(t, ok)
	require.Equal(t, []core.ObservationID{"obs-1", "obs-2"}, stored.Sources)

	// Nor does a reader's copy alias the set.
	stored.Sources[1] = "obs-forged"
	stored.Authority = core.AuthorityUserCorrection
	again, ok := s.Get("h1")
	require.True(t, ok)
	require.Equal(t, []core.ObservationID{"obs-1", "obs-2"}, again.Sources)
	require.Equal(t, core.AuthorityHypothesis, again.Authority,
		"a record's authority is fixed at Add; nothing may promote it in place")

	// A record id is written once. Re-adding it is an append-only violation, not an update,
	// which is what stops a later low-authority record from overwriting an earlier one.
	require.ErrorIs(t, s.Add(rec("h1", core.AuthorityUserCorrection, scopeA, "now authoritative")),
		core.ErrAppendOnly)

	// A superseding record cites its own evidence and leaves the target's citations alone.
	require.NoError(t, s.Supersede(rec("c1", core.AuthorityCandidateExtraction, scopeA, "extracted"),
		"h1", 2, ""))
	after, ok := s.Get("h1")
	require.True(t, ok)
	require.Equal(t, []core.ObservationID{"obs-1", "obs-2"}, after.Sources)
	require.Equal(t, "maybe", after.Claim)
}

// Behaviour 4: a conflict is rendered as a conflict until an authorized source resolves it. It is
// not silently resolved, not dropped, and not averaged into a third answer.
func TestConflict_IsRenderedUntilResolvedByAnAuthorizedSource(t *testing.T) {
	s := state.NewSet()

	left := rec("a1", core.AuthorityToolObservation, scopeA, "port is 8080")
	right := rec("b1", core.AuthorityCandidateExtraction, scopeA, "port is 9090")
	require.NoError(t, s.Add(left))
	require.NoError(t, s.Add(right))

	marker := rec("x1", core.AuthorityConflict, scopeA, "port disagreement")
	require.ErrorIs(t, s.Add(marker), core.ErrContract,
		"a conflict record is created by Conflict, not asserted through Add")

	require.NoError(t, s.Conflict(marker, []state.RecordID{left.ID, right.ID}, "two ports observed", 5))

	// Neither party is presented as settled state, and no merged third answer appeared.
	require.Empty(t, s.Applicable(scopeA))
	require.Equal(t, []string{"a1", "b1", "x1"}, ids(s.All()), "no party was dropped")

	conflicts := s.Conflicts(scopeA)
	require.Len(t, conflicts, 1)
	require.Equal(t, []state.RecordID{left.ID, right.ID}, conflicts[0].Conflict.Parties)
	require.Equal(t, "two ports observed", conflicts[0].Conflict.Reason)
	require.False(t, conflicts[0].Conflict.Resolved())
	require.Equal(t, core.AuthorityConflict, conflicts[0].Authority)

	// An unauthorized source cannot resolve it — including a party to the conflict itself.
	err := s.Resolve(marker.ID, right.ID, 6, "the extraction picked itself")
	require.ErrorIs(t, err, core.ErrContract)
	require.Len(t, s.Conflicts(scopeA), 1, "a refused resolution leaves the conflict rendered")

	// An authorized source resolves it, and the resolution is recorded rather than assumed.
	fix := rec("u1", core.AuthorityUserCorrection, scopeA, "port is 8080")
	require.NoError(t, s.Add(fix))
	require.NoError(t, s.Resolve(marker.ID, fix.ID, 8, "user confirmed 8080"))

	require.Empty(t, s.Conflicts(scopeA))
	resolved, ok := s.Get(marker.ID)
	require.True(t, ok)
	require.True(t, resolved.Conflict.Resolved())
	require.Equal(t, fix.ID, resolved.Conflict.Resolution.By)
	require.Equal(t, core.AuthorityUserCorrection, resolved.Conflict.Resolution.Authority)
	require.Equal(t, core.TurnIndex(8), resolved.Conflict.Resolution.Turn)

	// The resolution supersedes the losing parties without deleting them.
	require.Equal(t, []string{"u1"}, ids(s.Applicable(scopeA)))
	for _, id := range []state.RecordID{left.ID, right.ID} {
		party, ok := s.Get(id)
		require.True(t, ok)
		require.Equal(t, []state.RecordID{fix.ID}, party.Lineage.SupersededBy)
		require.Equal(t, []state.RecordID{marker.ID}, party.Lineage.ConflictedBy)
	}

	// A conflict is resolved once. A second attempt is refused rather than silently re-decided.
	require.ErrorIs(t, s.Resolve(marker.ID, fix.ID, 9, "again"), core.ErrContract)
}

// Behaviour 5: scope boundaries are preserved. A record scoped to one session or worktree does not
// become applicable elsewhere by default.
func TestScope_DoesNotLeakAcrossSessionsOrWorktrees(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record state.Scope
		query  state.Scope
		want   bool
	}{
		{"same session", scopeA, scopeA, true},
		{"other session, same worktree", scopeA, scopeB, false},
		{"worktree-wide applies to a session in it", scopeWT, scopeA, true},
		{"session-scoped does not answer a worktree-wide question", scopeA, scopeWT, false},
		{"other worktree", scopeA, state.SessionScope("C:/other", "sess-a"), false},
		{"zero record scope applies nowhere", state.Scope{}, scopeA, false},
		{"zero query matches nothing", scopeA, state.Scope{}, false},
		{"zero to zero", state.Scope{}, state.Scope{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.record.AppliesTo(tc.query))
		})
	}

	s := state.NewSet()
	require.NoError(t, s.Add(rec("a1", core.AuthorityUserCorrection, scopeA, "session A rule")))
	require.NoError(t, s.Add(rec("w1", core.AuthorityUserCorrection, scopeWT, "worktree rule")))

	require.Equal(t, []string{"a1", "w1"}, ids(s.Applicable(scopeA)))
	require.Equal(t, []string{"w1"}, ids(s.Applicable(scopeB)))
	require.Empty(t, s.Applicable(state.SessionScope("C:/other", "sess-a")))

	// An unscoped record is refused outright rather than becoming globally applicable.
	require.ErrorIs(t, s.Add(rec("u1", core.AuthorityUserCorrection, state.Scope{}, "no scope")),
		core.ErrContract)

	// And a cross-scope supersession is refused: another session cannot reach in and overrule.
	err := s.Supersede(rec("b1", core.AuthorityUserCorrection, scopeB, "session B disagrees"),
		"a1", 4, "")
	require.ErrorIs(t, err, core.ErrContract)
	require.ErrorContains(t, err, "scope")
}

func TestValidate_RejectsRecordsThatCannotBeTrusted(t *testing.T) {
	s := state.NewSet()

	bad := func(mut func(r *state.Record)) error {
		r := rec("v1", core.AuthorityToolObservation, scopeA, "claim")
		mut(&r)
		return s.Add(r)
	}

	require.ErrorIs(t, bad(func(r *state.Record) { r.Version = 0 }), core.ErrContract)
	require.ErrorIs(t, bad(func(r *state.Record) { r.ID = "" }), core.ErrContract)
	require.ErrorIs(t, bad(func(r *state.Record) { r.Claim = "" }), core.ErrContract)
	require.ErrorIs(t, bad(func(r *state.Record) { r.Sources = nil }), core.ErrContract)
	require.ErrorIs(t, bad(func(r *state.Record) { r.Sources = []core.ObservationID{""} }), core.ErrContract)
	require.ErrorIs(t, bad(func(r *state.Record) { r.TransformVersion = "" }), core.ErrContract)
	require.ErrorIs(t, bad(func(r *state.Record) { r.HashVersion = "" }), core.ErrContract)
	require.ErrorIs(t, bad(func(r *state.Record) { r.Dependencies.Coverage = "" }), core.ErrContract)
	require.ErrorIs(t, bad(func(r *state.Record) { r.Dependencies.Coverage = "mostly" }), core.ErrContract)
	require.ErrorIs(t, bad(func(r *state.Record) {
		r.Validity = core.EvidenceValidity{From: 9, To: 2}
	}), core.ErrContract)
	require.Zero(t, s.Len(), "no invalid record reached the set")

	// Dependency coverage is carried, not inferred: an incomplete set stays incomplete.
	part := rec("p1", core.AuthorityToolObservation, scopeA, "claim")
	part.Dependencies = state.Dependencies{
		Deps:     []core.Dep{{Path: "src/a.go"}},
		Coverage: state.DepCoveragePartial,
		Reason:   "index drain gap",
	}
	require.NoError(t, s.Add(part))
	got, ok := s.Get("p1")
	require.True(t, ok)
	require.Equal(t, state.DepCoveragePartial, got.Dependencies.Coverage)
	require.Equal(t, "index drain gap", got.Dependencies.Reason)
}

func TestSupersede_RefusesASecondSupersessionOfTheSameRecord(t *testing.T) {
	s := state.NewSet()
	require.NoError(t, s.Add(rec("a1", core.AuthorityHypothesis, scopeA, "first")))
	require.NoError(t, s.Supersede(rec("a2", core.AuthorityToolObservation, scopeA, "second"), "a1", 2, ""))

	err := s.Supersede(rec("a3", core.AuthorityToolObservation, scopeA, "third"), "a1", 3, "")
	require.ErrorIs(t, err, core.ErrContract)
	require.ErrorContains(t, err, "a2")

	require.ErrorIs(t, s.Supersede(rec("a4", core.AuthorityToolObservation, scopeA, "x"), "nope", 4, ""),
		core.ErrNotFound)
}

func TestNewRecordID_IsStableAndRefusesAnUnassignedSequence(t *testing.T) {
	a, err := state.NewRecordID(scopeA, 1)
	require.NoError(t, err)
	again, err := state.NewRecordID(scopeA, 1)
	require.NoError(t, err)
	require.Equal(t, a, again)

	b, err := state.NewRecordID(scopeA, 2)
	require.NoError(t, err)
	require.NotEqual(t, a, b)

	other, err := state.NewRecordID(scopeB, 1)
	require.NoError(t, err)
	require.NotEqual(t, a, other, "the same sequence in another session is another record")

	_, err = state.NewRecordID(scopeA, 0)
	require.ErrorIs(t, err, core.ErrContract)
}
