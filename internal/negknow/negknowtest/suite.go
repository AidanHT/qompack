// Package negknowtest is the conformance suite for negknow.Ledger (00-ARCHITECTURE.md §5.22):
// every implementation SP-09 ships must pass RunLedgerSuite. SP-01 ships the suite itself,
// including the behaviour assertions SP-09 inherits (Rule W-1) — only the guarded /behaviour
// block is skipped until a real Ledger lands.
package negknowtest

import (
	"context"
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// probeTarget and probeApproach are the shape block's and the stub probe's Query arguments.
const (
	probeTarget   = "negknowtest-shape-probe-target"
	probeApproach = "negknowtest-shape-probe-approach"
)

// RunLedgerSuite is the conformance suite for negknow.Ledger. name distinguishes multiple
// factories run in the same test binary; factory must return a fresh, ready-to-use Ledger on
// every call.
func RunLedgerSuite(t *testing.T, name string, factory func(t *testing.T) negknow.Ledger) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		l := factory(t)
		require.NotNil(t, l)
		ctx := context.Background()

		// ErrNoEvidence is passed HERE and nowhere else in the block: this is the one probe that
		// can legitimately provoke it (ruling R28, and see requireKnownError). A Query, a Get or a
		// RebuildBloom answering "elimination requires evidence" would be a real defect, and the
		// other probes below must keep failing on it.
		_, err := l.Record(ctx, negknow.Record{})
		requireKnownError(t, err, negknow.ErrNoEvidence)

		_, err = l.Query(ctx, probeTarget, probeApproach, negknow.ScopeSession)
		requireKnownError(t, err)

		_, err = l.Get(ctx, "")
		requireKnownError(t, err)

		_, err = l.Active(ctx, negknow.ScopeSession)
		requireKnownError(t, err)

		_, err = l.All(ctx)
		requireKnownError(t, err)

		err = l.MarkStale(ctx, nil, nil)
		requireKnownError(t, err)

		_, err = refreshStalenessShapeProbe(t, l, ctx)
		requireKnownError(t, err)

		_, _, err = l.RebuildBloom(ctx)
		requireKnownError(t, err)

		_ = l.Health() // no error return; any Health is shape-valid

		requireKnownError(t, l.Close())
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("three_way_absent_active_stale", func(t *testing.T) { runThreeWayAnswerCase(t, factory) })
		t.Run("scope_isolation", func(t *testing.T) { runScopeIsolationCase(t, factory) })
		t.Run("require_evidence", func(t *testing.T) { runRequireEvidenceCase(t, factory) })
		t.Run("stale_note_text", func(t *testing.T) { runStaleNoteTextCase(t, factory) })
		t.Run("bloom_rebuilt_from_active_records_only", func(t *testing.T) { runBloomRebuildActiveOnlyCase(t, factory) })
		t.Run("bloomonly_consistency", func(t *testing.T) { runBloomOnlyConsistencyCase(t, factory) })
		t.Run("bloomonly_on_a_synthetic_false_positive", func(t *testing.T) { runBloomFalsePositiveCase(t, factory) })
		t.Run("maintainer_surface", func(t *testing.T) { runMaintainerSurfaceCase(t, factory) })
	})
}

// refreshStalenessShapeProbe calls l.RefreshStaleness(ctx, nil).
//
// The shape block has no legal way to construct a real store.Store: a <pkg>test package's import
// allow-set is its own base package plus testutil and core (00-ARCHITECTURE.md §3.2), so
// negknowtest cannot import store or config to build one. A well-behaved RefreshStaleness must
// therefore treat a nil store.Store defensively (return an error, never dereference it) — this
// helper turns a violation of that into a clear, named test failure instead of a bare panic, so a
// future real implementation that gets this wrong fails loudly and specifically here rather than
// crashing the whole suite with an unexplained stack trace.
func refreshStalenessShapeProbe(t *testing.T, l negknow.Ledger, ctx context.Context) (ids []string, err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RefreshStaleness(ctx, nil) panicked: %v — a real implementation must treat a "+
				"nil store.Store defensively, since negknowtest cannot construct one (§3.2 import allow-set)", r)
		}
	}()
	return l.RefreshStaleness(ctx, nil)
}

// requireKnownError fails the test unless err is nil or wraps one of the four sentinels every stub
// and every real implementation is allowed to return from an operation (00-ARCHITECTURE.md §5.22;
// §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md), or one of the alsoAllowed
// sentinels the CALLING PROBE nominates.
//
// alsoAllowed is per-probe and not a widening of the set, which is the point. RULING R28 approved
// admitting negknow.ErrNoEvidence, and the reason is specific to ONE probe: the shape block's
// first call is Record(ctx, negknow.Record{}) — a record carrying no evidence hash — and
// eliminations.requireEvidence is TRUE by Appendix C default (internal/config/defaults.go), so a
// ledger built from real configuration refuses it with ErrNoEvidence. Refusing it is the ledger
// obeying §8.3 rather than misbehaving, and negknow_test.go's TestLedgerConformance — which runs
// this suite over testutil.NewProject's loaded defaults — would otherwise fail the shape block on
// correct behaviour.
//
// Nothing about that reasoning applies to Query, Get, Active, All, MarkStale, RefreshStaleness,
// RebuildBloom or Close. A Ledger that answered any of those with "elimination requires evidence"
// would be a real defect, and a sentinel accepted globally could not tell the difference. So the
// exemption is passed at the one call site that earns it and the other eight probes keep failing
// on it.
//
// The four core sentinels cannot express ErrNoEvidence: it is not degradation, not a budget, not a
// missing thing, and certainly not "not implemented" — answering the probe with any of those would
// be a worse lie than admitting a fifth name. What the block actually asserts is that every error a
// conformant Ledger returns is one a caller can switch on BY NAME, and a named, exported,
// documented sentinel nominated by the probe that expects it keeps that property exactly.
func requireKnownError(t *testing.T, err error, alsoAllowed ...error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotImplemented) ||
		errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded)
	for _, sentinel := range alsoAllowed {
		known = known || errors.Is(err, sentinel)
	}
	require.True(t, known, "unexpected error: %v", err)
}

// isStub reports whether factory currently produces a stub Ledger, using Query as the probe
// (plans/OWNERS.tsv: negknow's probe method is Query).
func isStub(t *testing.T, factory func(t *testing.T) negknow.Ledger) bool {
	t.Helper()
	_, err := factory(t).Query(context.Background(), probeTarget, probeApproach, negknow.ScopeSession)
	return core.IsNotImplemented(err)
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Ledger, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) negknow.Ledger) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
