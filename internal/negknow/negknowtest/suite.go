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

		_, err := l.Record(ctx, negknow.Record{})
		requireKnownError(t, err)

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
		t.Run("bloom_rebuilt_from_active_records_only", func(t *testing.T) { runBloomRebuildActiveOnlyCase(t, factory) })
		t.Run("bloomonly_consistency", func(t *testing.T) { runBloomOnlyConsistencyCase(t, factory) })
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

// requireKnownError fails the test unless err is nil or wraps one of the four sentinels every
// stub and every real implementation is allowed to return from an operation
// (00-ARCHITECTURE.md §5.22; §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md).
func requireKnownError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotImplemented) ||
		errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded)
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
