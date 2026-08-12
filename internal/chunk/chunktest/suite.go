// Package chunktest is the conformance suite for chunk.Chunker (00-ARCHITECTURE.md §5.22): every
// implementation SP-04 ships must pass RunChunkerSuite. SP-01 ships the suite itself, including
// the behaviour assertions SP-04 inherits (Rule W-1) — only the guarded /behaviour block is
// skipped until a real Chunker lands.
package chunktest

import (
	"bytes"
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// probeDataSize is large enough that any correct FastCDC Chunker — under any Min/Target/Max the
// suite's factory happens to configure — must return at least one Chunk for it.
const probeDataSize = 65536

// RunChunkerSuite is the conformance suite for chunk.Chunker. name distinguishes multiple
// factories run in the same test binary; factory must return a fresh, ready-to-use Chunker on
// every call.
func RunChunkerSuite(t *testing.T, name string, factory func(t *testing.T) chunk.Chunker) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		c := factory(t)
		require.NotNil(t, c)

		// Split has no error return; any []Chunk it produces, including nil, is shape-valid — this
		// only asserts that calling it does not panic.
		_ = c.Split([]byte("shape probe"))

		err := c.SplitStream(bytes.NewReader([]byte("shape probe")), func(chunk.Chunk, []byte) error { return nil })
		requireKnownError(t, err)
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("boundary_stability_under_insertion", func(t *testing.T) { runBoundaryStabilityCase(t, factory) })
		t.Run("deterministic", func(t *testing.T) { runDeterminismCase(t, factory) })
		t.Run("sizes_contiguous_and_cover_input", func(t *testing.T) { runSizeBoundsCase(t, factory) })
		t.Run("root_hash_matches_formula", func(t *testing.T) { runRootHashFormulaCase(t, factory) })
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

// isStub reports whether factory currently produces a stub Chunker, using Split as the probe
// (plans/OWNERS.tsv: chunk's probe method is Split). Split has no error return (§5.5), so unlike
// almost every other suite in this tree, isStub cannot probe via core.IsNotImplemented: instead it
// relies directly on Rule 1's documented stub contract (the SP-01 stub always returns nil
// regardless of input). Splitting probeDataSize bytes of non-empty content and getting zero chunks
// back is what "still a stub" looks like; any correct FastCDC Chunker must return at least one
// chunk for input that large, so a real implementation can never trip this check.
func isStub(t *testing.T, factory func(t *testing.T) chunk.Chunker) bool {
	t.Helper()
	probe := bytes.Repeat([]byte{'x'}, probeDataSize)
	return len(factory(t).Split(probe)) == 0
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Chunker, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) chunk.Chunker) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
