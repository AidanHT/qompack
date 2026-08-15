// Package grammartest is the conformance suite for grammar.Sequitur (00-ARCHITECTURE.md §5.22):
// every implementation SP-15 ships must pass RunSequiturSuite. SP-01 ships the suite itself,
// including the behaviour assertions SP-15 inherits (Rule W-1) — only the guarded /behaviour block
// is skipped until a real Sequitur lands.
package grammartest

import (
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// probeThrashMinUses is an arbitrary, small minUses argument for the shape block's Thrash probe
// call; its value carries no meaning beyond "a valid int".
const probeThrashMinUses = 2

// probeRepeats is how many times isStub repeats a 3-symbol digram to probe for stub-ness.
const probeRepeats = 6

// RunSequiturSuite is the conformance suite for grammar.Sequitur. name distinguishes multiple
// factories run in the same test binary; factory must return a fresh, ready-to-use Sequitur on
// every call.
func RunSequiturSuite(t *testing.T, name string, factory func(t *testing.T) grammar.Sequitur) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		seq := factory(t)
		require.NotNil(t, seq)

		// Append and Reset have no return value at all; Rules, Thrash and Compressed have no
		// error return. Any value they produce, including nil, is shape-valid, so this only
		// asserts that calling each does not panic.
		seq.Append(grammar.Symbol("shape-probe"))
		_ = seq.Rules()
		_ = seq.Thrash(probeThrashMinUses)
		_ = seq.Compressed()
		seq.Reset()

		_, err := seq.MarshalBinary()
		requireKnownError(t, err)
		requireKnownError(t, seq.UnmarshalBinary(nil))
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("invariants_hold_after_every_append", func(t *testing.T) {
			runInvariantsAfterEveryAppendCase(t, factory)
		})
	})
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

// isStub reports whether factory currently produces a stub Sequitur, using Append as the probe
// (plans/OWNERS.tsv: grammar's probe method is Append). Append has no return value at all
// (§5.11) — not even a zero value to inspect — so unlike almost every other suite in this tree,
// isStub cannot observe Append's own result: instead it appends an unambiguously repeating digram
// (A,B repeated several times) and checks Rules() afterward. Any correct Sequitur must induce at
// least one rule from that input; the SP-01 stub, whose Append is a no-op, never does.
func isStub(t *testing.T, factory func(t *testing.T) grammar.Sequitur) bool {
	t.Helper()
	seq := factory(t)
	for i := 0; i < probeRepeats; i++ {
		seq.Append("A")
		seq.Append("B")
		seq.Append("C")
	}
	return len(seq.Rules()) == 0
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Sequitur, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) grammar.Sequitur) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
