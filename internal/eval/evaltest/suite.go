// Package evaltest is the conformance suite for eval.Harness (00-ARCHITECTURE.md §5.22): every
// implementation SP-02 ships must pass RunHarnessSuite. SP-01 ships the suite itself, including
// the behaviour assertions SP-02 inherits (Rule W-1) — only the guarded /behaviour block is
// skipped until a real Harness lands.
package evaltest

import (
	"context"
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// RunHarnessSuite is the conformance suite for eval.Harness. name distinguishes multiple
// factories run in the same test binary; factory must return a fresh, ready-to-use Harness on
// every call.
func RunHarnessSuite(t *testing.T, name string, factory func(t *testing.T) eval.Harness) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		h := factory(t)
		require.NotNil(t, h)

		ctx := context.Background()

		_, err := h.Load(t.TempDir())
		requireKnownError(t, err)

		_, err = h.Replay(ctx, eval.Session{}, noopPolicy{}, eval.ReplayOptions{})
		requireKnownError(t, err)

		// Compare and ScoreRun have no error return; any value they produce is shape-valid, so
		// this only asserts that calling them does not panic.
		_ = h.Compare(eval.Run{}, eval.Run{})
		_ = h.ScoreRun(eval.Run{}, nil)

		_, err = h.Belady(ctx, eval.Session{}, core.TurnIndex(0), core.Tokens(0))
		requireKnownError(t, err)

		_, err = h.Report(ctx, nil)
		requireKnownError(t, err)
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("replay_is_deterministic_for_a_fixed_seed", func(t *testing.T) {
			runDeterministicReplayCase(t, factory)
		})
		t.Run("belady_is_optimal_on_a_six_item_instance", func(t *testing.T) {
			runBeladySixItemCase(t, factory)
		})
	})
}

// noopPolicy is the minimal eval.Policy used only by the shape block's Replay probe call.
type noopPolicy struct{}

func (noopPolicy) Name() string { return "noop" }
func (noopPolicy) KeepSet(ctx context.Context, s eval.Session, at core.TurnIndex, budget core.Tokens) (eval.KeepSet, error) {
	return eval.KeepSet{}, core.ErrNotImplemented
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

// isStub reports whether factory currently produces a stub Harness, using Load as the probe
// (plans/OWNERS.tsv: eval's probe method is Load).
func isStub(t *testing.T, factory func(t *testing.T) eval.Harness) bool {
	t.Helper()
	_, err := factory(t).Load(t.TempDir())
	return core.IsNotImplemented(err)
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Harness, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) eval.Harness) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
