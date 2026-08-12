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
