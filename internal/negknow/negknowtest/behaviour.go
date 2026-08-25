package negknowtest

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertions of the negknowtest suite (subplan table, §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md): the three-way absent/active/stale
// answer, tried.bloom rebuilt from active records only, and BloomOnly's consistency contract. All
// are authored now, gated behind the same Rule W-1 stub probe as the rest of the suite, so SP-09
// inherits them rather than writing its own grader.

// testEvidenceHash mints a deterministic, non-zero core.Hash for a test Record's Evidence field.
// A real Ledger opened with config.EliminationsCfg.RequireEvidence == true (the default,
// internal/config/defaults.go) may legitimately refuse a Record whose Evidence is the zero Hash,
// so every behaviour-block Record below supplies one rather than leaving it unset.
func testEvidenceHash(seed string) core.Hash {
	return core.HashBytes("negknowtest.evidence", []byte(seed))
}

// runThreeWayAnswerCase asserts Query's three-way response (00-ARCHITECTURE.md §5.10, §8.3):
// AnswerAbsent before anything is recorded, AnswerActive immediately after Record, and
// AnswerStale after MarkStale — with Query's Answer.Record populated in both the active and
// stale cases.
func runThreeWayAnswerCase(t *testing.T, factory func(t *testing.T) negknow.Ledger) {
	t.Helper()
	l := factory(t)
	ctx := context.Background()

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
		reason   = "pgbouncer 1.18 ignores statement_timeout in transaction pooling mode"
	)
	desc := negknow.Canonicalize(target, approach, reason)

	absent, err := l.Query(ctx, target, approach, negknow.ScopeProject)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerAbsent, absent.State, "nothing has been recorded yet")
	require.Nil(t, absent.Record)

	id, err := l.Record(ctx, negknow.Record{
		Target: target, Approach: approach, Reason: reason, Desc: desc,
		Evidence: testEvidenceHash("three-way"),
		Scope:    negknow.ScopeProject, Status: negknow.StatusActive, Source: negknow.SourceMCP,
	})
	require.NoError(t, err)
	require.NotEmpty(t, id, "Record must return a non-empty id")

	active, err := l.Query(ctx, target, approach, negknow.ScopeProject)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerActive, active.State)
	require.NotNil(t, active.Record)
	require.Equal(t, id, active.Record.ID)

	require.NoError(t, l.MarkStale(ctx, []string{id}, []string{"dependency changed"}))

	stale, err := l.Query(ctx, target, approach, negknow.ScopeProject)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerStale, stale.State)
	require.NotNil(t, stale.Record)
	require.Equal(t, id, stale.Record.ID)
}

// runBloomRebuildActiveOnlyCase asserts RebuildBloom rebuilds tried.bloom from ACTIVE RECORDS
// ONLY (00-ARCHITECTURE.md §3.3, §13 invariant 2): an active record's descriptor key must be
// present in the rebuilt bloom, and a stale record's descriptor key must be absent from it — the
// mechanism that lets a stale elimination "reopen" the question it once closed.
//
// The rebuilt bloom's *sketch.Bloom methods (Test) are called on the value RebuildBloom returns
// without this file importing internal/sketch: negknowtest's import allow-set is its own base
// package plus testutil and core only (00-ARCHITECTURE.md §3.2), but Go does not require an
// import to call an exported method on a value whose type was inferred from another package's
// already-imported function signature — only to spell the type name explicitly, which this file
// never needs to do.
func runBloomRebuildActiveOnlyCase(t *testing.T, factory func(t *testing.T) negknow.Ledger) {
	t.Helper()
	l := factory(t)
	ctx := context.Background()

	activeDesc := negknow.Canonicalize("rebuild-active-target", "rebuild-active-approach", "still holds")
	_, err := l.Record(ctx, negknow.Record{
		Target: "rebuild-active-target", Approach: "rebuild-active-approach", Reason: "still holds",
		Desc: activeDesc, Evidence: testEvidenceHash("rebuild-active"),
		Scope: negknow.ScopeProject, Status: negknow.StatusActive, Source: negknow.SourceMCP,
	})
	require.NoError(t, err)

	staleDesc := negknow.Canonicalize("rebuild-stale-target", "rebuild-stale-approach", "dependency changed")
	staleID, err := l.Record(ctx, negknow.Record{
		Target: "rebuild-stale-target", Approach: "rebuild-stale-approach", Reason: "dependency changed",
		Desc: staleDesc, Evidence: testEvidenceHash("rebuild-stale"),
		Scope: negknow.ScopeProject, Status: negknow.StatusActive, Source: negknow.SourceMCP,
	})
	require.NoError(t, err)
	require.NoError(t, l.MarkStale(ctx, []string{staleID}, []string{"dependency changed"}))

	bloom, health, err := l.RebuildBloom(ctx)
	require.NoError(t, err)
	require.NotNil(t, bloom)
	require.True(t, bloom.Test(activeDesc.Key()),
		"RebuildBloom's bloom must contain every currently-active record's descriptor key")
	require.False(t, bloom.Test(staleDesc.Key()),
		"RebuildBloom must EXCLUDE stale records: it rebuilds from active records only, never a checkpoint or a summary (§3.3)")
	require.GreaterOrEqual(t, health.Active, 1, "Health.Active must count at least the one active record this case recorded")
}

// runBloomOnlyConsistencyCase asserts the half of BloomOnly's contract (§13 invariant 3: the
// bloom is a cache, never the source of truth) that is testable purely through the Ledger
// interface: BloomOnly must never be set on an answer a real Record backs, and it must not be
// spuriously set on a genuinely absent target either.
//
// The OTHER half — that BloomOnly IS set when the bloom claims a hit with no backing record —
// requires seeding Open's *sketch.Bloom parameter with a key that was never Record()ed, which
// needs types (sketch.Bloom, config.Config) outside negknowtest's import allow-set
// (00-ARCHITECTURE.md §3.2: a <pkg>test package may import only its own base package, testutil
// and core). SP-09 should add a white-box test for that direction in internal/negknow/*_test.go,
// which is unrestricted by that rule, alongside its real negknow.Open implementation.
func runBloomOnlyConsistencyCase(t *testing.T, factory func(t *testing.T) negknow.Ledger) {
	t.Helper()
	l := factory(t)
	ctx := context.Background()

	absent, err := l.Query(ctx, "bloomonly-consistency-absent-target", "bloomonly-consistency-absent-approach", negknow.ScopeProject)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerAbsent, absent.State)
	require.False(t, absent.BloomOnly, "a genuinely absent target must not be flagged BloomOnly")
	require.Nil(t, absent.Record)

	const (
		target   = "bloomonly-consistency-backed-target"
		approach = "bloomonly-consistency-backed-approach"
		reason   = "backed by a real record"
	)
	desc := negknow.Canonicalize(target, approach, reason)
	_, err = l.Record(ctx, negknow.Record{
		Target: target, Approach: approach, Reason: reason, Desc: desc,
		Evidence: testEvidenceHash("bloomonly-consistency"),
		Scope:    negknow.ScopeProject, Status: negknow.StatusActive, Source: negknow.SourceMCP,
	})
	require.NoError(t, err)

	backed, err := l.Query(ctx, target, approach, negknow.ScopeProject)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerActive, backed.State)
	require.NotNil(t, backed.Record)
	require.False(t, backed.BloomOnly,
		"a record-backed answer must never ALSO be flagged BloomOnly (§13 invariant 3): BloomOnly means the bloom claims a hit with no backing record")
}

// staleNoteLiteral is Qompack.md §8.3 item 4's re-verification sentence, transcribed here as a
// LITERAL — em dash and all — rather than referenced through negknow.StaleNote.
//
// The Done checklist requires StaleNote to be "compared against a literal rather than against
// itself", and this is why: an assertion written as require.Equal(negknow.StaleNote, ans.Note)
// passes for ANY string the constant happens to hold, including a paraphrase that silently forked
// the contract SP-11's digest and SP-13's already_tried result both render. This second,
// independent transcription is what makes the comparison say something. If §8.3 ever changes,
// both copies have to move, and a one-sided change fails here.
const staleNoteLiteral = "previously eliminated, but the evidence has changed since — re-verification may be warranted"

// runScopeIsolationCase asserts §8.3 item 5's visibility rule through Query.
//
//	ScopeSession record, queried at ScopeProject -> not answerable (it belongs to one session
//	                                                and cannot leak across the project)
//	ScopeProject record, queried at either scope -> answerable (the cross-session carry-over)
//
// The ScopeProject-over-a-session-record direction asserts the STATE only. Whether that answer
// also carries BloomOnly is an implementation's own business: tried.bloom is keyed on the
// descriptor and not on the scope, so a filter-backed ledger legitimately reports the invisible
// record as an unbacked bloom hit, while one that never consulted a filter legitimately does not.
// Both are correct, and §13 invariant 3 constrains only that neither hands back a record.
func runScopeIsolationCase(t *testing.T, factory func(t *testing.T) negknow.Ledger) {
	t.Helper()
	l := factory(t)
	ctx := context.Background()

	const (
		sessTarget   = "scope-isolation-session-target"
		sessApproach = "scope-isolation-session-approach"
		sessReason   = "recorded for this session only"
	)
	sessID, err := l.Record(ctx, negknow.Record{
		Target: sessTarget, Approach: sessApproach, Reason: sessReason,
		Desc:     negknow.Canonicalize(sessTarget, sessApproach, sessReason),
		Evidence: testEvidenceHash("scope-session"),
		Scope:    negknow.ScopeSession, Status: negknow.StatusActive, Source: negknow.SourceMCP,
	})
	require.NoError(t, err)
	require.NotEmpty(t, sessID)

	own, err := l.Query(ctx, sessTarget, sessApproach, negknow.ScopeSession)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerActive, own.State,
		"a session-scoped record must answer for the session that recorded it")
	require.NotNil(t, own.Record)
	require.Equal(t, sessID, own.Record.ID)

	crossed, err := l.Query(ctx, sessTarget, sessApproach, negknow.ScopeProject)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerAbsent, crossed.State,
		"a session-scoped record must be INVISIBLE to a project-scoped query (§8.3 item 5)")
	require.Nil(t, crossed.Record,
		"an invisible record must not be handed back through the Answer either")

	const (
		projTarget   = "scope-isolation-project-target"
		projApproach = "scope-isolation-project-approach"
		projReason   = "recorded for the whole project"
	)
	projID, err := l.Record(ctx, negknow.Record{
		Target: projTarget, Approach: projApproach, Reason: projReason,
		Desc:     negknow.Canonicalize(projTarget, projApproach, projReason),
		Evidence: testEvidenceHash("scope-project"),
		Scope:    negknow.ScopeProject, Status: negknow.StatusActive, Source: negknow.SourceMCP,
	})
	require.NoError(t, err)

	for _, scope := range []negknow.Scope{negknow.ScopeProject, negknow.ScopeSession} {
		got, qerr := l.Query(ctx, projTarget, projApproach, scope)
		require.NoError(t, qerr)
		require.Equal(t, negknow.AnswerActive, got.State,
			"a project-scoped record must answer under BOTH scopes; %q did not", scope)
		require.NotNil(t, got.Record)
		require.Equal(t, projID, got.Record.ID)
	}
}

// runRequireEvidenceCase asserts §8.3's evidence discipline: under eliminations.requireEvidence —
// the Appendix C default — a Record carrying the zero Evidence hash is REFUSED with
// negknow.ErrNoEvidence and nothing is appended.
//
// The assertion is deliberately two-branched, because the factory owns the configuration and this
// suite cannot see it: negknowtest may import negknow, testutil and core only (§3.2), so it can
// neither read nor set config.EliminationsCfg.RequireEvidence for the ledger it was handed. A
// factory built on a zero config.Config has requireEvidence false and MUST accept the record; one
// built on the real defaults has it true and MUST refuse it. What this pins is that those are the
// only two outcomes — a refusal is ErrNoEvidence, returns no id, and leaves the ledger unchanged;
// an acceptance returns a usable id the ledger then answers with. A ledger that refused with some
// other error, or that "accepted" the record and handed back an empty id, fails here under either
// configuration.
func runRequireEvidenceCase(t *testing.T, factory func(t *testing.T) negknow.Ledger) {
	t.Helper()
	l := factory(t)
	ctx := context.Background()

	const (
		target   = "require-evidence-target"
		approach = "require-evidence-approach"
		reason   = "no evidence hash was supplied"
	)
	id, err := l.Record(ctx, negknow.Record{
		Target: target, Approach: approach, Reason: reason,
		Desc:  negknow.Canonicalize(target, approach, reason),
		Scope: negknow.ScopeProject, Status: negknow.StatusActive, Source: negknow.SourceMCP,
		// Evidence is deliberately left as the zero core.Hash.
	})
	if err != nil {
		require.ErrorIs(t, err, negknow.ErrNoEvidence,
			"the only legitimate refusal of an evidence-less Record is negknow.ErrNoEvidence")
		require.Empty(t, id, "a refused Record must not report an id: nothing was appended")

		after, qerr := l.Query(ctx, target, approach, negknow.ScopeProject)
		require.NoError(t, qerr)
		require.Equal(t, negknow.AnswerAbsent, after.State,
			"a refused Record must leave the ledger unchanged")
		require.Nil(t, after.Record)
		return
	}

	require.NotEmpty(t, id,
		"a ledger that ACCEPTS an evidence-less Record (requireEvidence off) must still report its id")
	after, err := l.Query(ctx, target, approach, negknow.ScopeProject)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerActive, after.State)
	require.NotNil(t, after.Record)
	require.Equal(t, id, after.Record.ID)
}

// runStaleNoteTextCase asserts that a stale answer carries §8.3 item 4's re-verification sentence
// verbatim, byte for byte, including the em dash — the string SP-11's digest and SP-13's
// already_tried result both render, and which a paraphrase in either would fork in two.
func runStaleNoteTextCase(t *testing.T, factory func(t *testing.T) negknow.Ledger) {
	t.Helper()
	require.Equal(t, staleNoteLiteral, negknow.StaleNote,
		"negknow.StaleNote must be §8.3 item 4's sentence verbatim, em dash included")

	l := factory(t)
	ctx := context.Background()

	const (
		target   = "stale-note-target"
		approach = "stale-note-approach"
		reason   = "the dependency this rested on has moved"
	)
	id, err := l.Record(ctx, negknow.Record{
		Target: target, Approach: approach, Reason: reason,
		Desc:     negknow.Canonicalize(target, approach, reason),
		Evidence: testEvidenceHash("stale-note"),
		Scope:    negknow.ScopeProject, Status: negknow.StatusActive, Source: negknow.SourceMCP,
	})
	require.NoError(t, err)
	require.NoError(t, l.MarkStale(ctx, []string{id}, []string{"docker-compose.yml changed"}))

	stale, err := l.Query(ctx, target, approach, negknow.ScopeProject)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerStale, stale.State)
	require.Equal(t, staleNoteLiteral, stale.Note,
		"a stale answer's Note is §8.3 item 4's sentence, not a paraphrase of it")

	// MCPResult is the rendering SP-13 hands the agent; the note has to survive it unchanged.
	state, gotReason, note, evidence := stale.MCPResult()
	require.Equal(t, "stale", state)
	require.Equal(t, reason, gotReason)
	require.Equal(t, staleNoteLiteral, note)
	require.NotEmpty(t, evidence, "a stale answer still carries the evidence its record was made on")
}

// runBloomFalsePositiveCase asserts the OTHER half of §13 invariant 3 — the half
// runBloomOnlyConsistencyCase documents itself as unable to reach: when the filter claims a hit
// that no record backs, the answer is AnswerAbsent WITH BloomOnly set. Never AnswerActive, and
// never a silent absence that hides the false positive from its caller.
//
// The false positive is manufactured rather than waited for, and it is manufactured on the value
// RebuildBloom RETURNS: that pointer is the ledger's live filter (§3.3 — the rebuild adopts what
// it built), so adding a key to it leaves exactly the state a genuine hash collision would.
// Calling Add on it needs no import of internal/sketch, for the reason
// runBloomRebuildActiveOnlyCase spells out above: Go requires an import to SPELL a type, not to
// call an exported method on a value whose type came back from an already-imported signature.
func runBloomFalsePositiveCase(t *testing.T, factory func(t *testing.T) negknow.Ledger) {
	t.Helper()
	l := factory(t)
	ctx := context.Background()

	const (
		backedTarget   = "bloom-fp-backed-target"
		backedApproach = "bloom-fp-backed-approach"
		backedReason   = "a real record, so the rebuild has something to build from"
	)
	_, err := l.Record(ctx, negknow.Record{
		Target: backedTarget, Approach: backedApproach, Reason: backedReason,
		Desc:     negknow.Canonicalize(backedTarget, backedApproach, backedReason),
		Evidence: testEvidenceHash("bloom-fp-backed"),
		Scope:    negknow.ScopeProject, Status: negknow.StatusActive, Source: negknow.SourceMCP,
	})
	require.NoError(t, err)

	bloom, _, err := l.RebuildBloom(ctx)
	require.NoError(t, err)
	require.NotNil(t, bloom, "RebuildBloom must hand back the filter it adopted")

	const (
		ghostTarget   = "bloom-fp-never-recorded-target"
		ghostApproach = "bloom-fp-never-recorded-approach"
	)
	ghostKey := negknow.Canonicalize(ghostTarget, ghostApproach, "").MatchKey()
	require.NotEmpty(t, ghostKey)

	bloom.Add(ghostKey)

	got, err := l.Query(ctx, ghostTarget, ghostApproach, negknow.ScopeProject)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerAbsent, got.State,
		"a bloom hit with no record behind it is ABSENT: the filter is a cache, never the source of truth (§13 invariant 3)")
	require.True(t, got.BloomOnly,
		"an unbacked bloom hit must be FLAGGED BloomOnly rather than silently swallowed")
	require.Nil(t, got.Record, "there is no record to hand back — that is what BloomOnly means")
	require.Empty(t, got.Note)

	// And the flag is not sticky: the record-backed pair still answers, and still without it.
	backed, err := l.Query(ctx, backedTarget, backedApproach, negknow.ScopeProject)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerActive, backed.State)
	require.False(t, backed.BloomOnly)
}

// runMaintainerSurfaceCase asserts that the value the factory produces also carries
// negknow.Maintainer, the maintenance/ranking/ingest surface beyond §5.10's Ledger interface.
//
// It is the assertion the plan requires to FAIL rather than skip: Rule W-1's "no t.Skip remains in
// negknowtest" is otherwise satisfiable by handing the suite a weakened factory instead of by
// implementing the behaviour, and a hard type assertion is what closes that door. Everything below
// it is deliberately shape-level — a name, a non-nil function, a ranking that returns what was
// recorded — because the suite must stay runnable against SP-13's daemon-backed ledger, whose
// maintenance policy is its own business.
//
// MaintenanceTask takes a store.Store, which negknowtest may not import (§3.2). It is called with
// an untyped nil, which converts to the interface's zero value without the type ever being
// spelled, and a nil store is a legitimate argument here: the task is returned as a closure, not
// run.
func runMaintainerSurfaceCase(t *testing.T, factory func(t *testing.T) negknow.Ledger) {
	t.Helper()
	led := factory(t)
	ctx := context.Background()

	m, ok := led.(negknow.Maintainer)
	require.True(t, ok,
		"the Ledger this factory produces must also implement negknow.Maintainer: the suite's "+
			"maintenance and ranking assertions have no other way in, and weakening the factory to "+
			"dodge them is exactly what Rule W-1 forbids")

	const (
		target   = "maintainer-surface-target"
		approach = "maintainer-surface-approach"
		reason   = "the ranked entry /qompack:status renders"
	)
	id, err := led.Record(ctx, negknow.Record{
		Target: target, Approach: approach, Reason: reason,
		Desc:     negknow.Canonicalize(target, approach, reason),
		Evidence: testEvidenceHash("maintainer-surface"),
		Scope:    negknow.ScopeSession, Status: negknow.StatusActive, Source: negknow.SourceMCP,
	})
	require.NoError(t, err)

	top, more, err := m.TopActive(ctx, negknow.ScopeSession, 1, nil)
	require.NoError(t, err)
	require.Len(t, top, 1, "one active record was recorded, so the top 1 holds exactly it")
	require.Equal(t, id, top[0].ID)
	require.Equal(t, 0, more, "nothing was left out, so there is no and-N-more to render")

	none, more, err := m.TopActive(ctx, negknow.ScopeSession, 0, nil)
	require.NoError(t, err)
	require.Empty(t, none)
	require.Equal(t, 1, more, "asking for none must still report how many were left out")

	name, _, fn := m.MaintenanceTask(nil)
	require.NotEmpty(t, name, "SP-12's idle controller registers the task by name")
	require.NotNil(t, fn, "MaintenanceTask must hand back the work, not only describe it")

	require.NoError(t, m.Observe(ctx, negknow.Observation{
		Turn: 1, Kind: negknow.ObsEdit, Path: "src/maintainer-surface.ts", Detail: approach,
	}), "Observe is how SP-08's observer feeds the heuristic detector")

	_ = m.NeedsRebuild() // any answer is shape-valid; the policy behind it is the ledger's own
}
