// Package symbolstest is the conformance suite for symbols.Extractor (00-ARCHITECTURE.md §5.22):
// every implementation SP-04 ships must pass RunExtractorSuite. SP-01 ships the suite itself,
// including the behaviour assertions SP-04 inherits (Rule W-1) — only the guarded /behaviour block
// is skipped until a real Extractor lands.
package symbolstest

import (
	"testing"

	"github.com/qompack/qompack/internal/symbols"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// probeSource is a minimal fixture guaranteed to contain at least one symbol under any reasonable
// heuristic: a named top-level function declaration.
const probeSource = "package probe\n\nfunc ProbeSymbol() int {\n\treturn 1\n}\n"

// RunExtractorSuite is the conformance suite for symbols.Extractor. name distinguishes multiple
// factories run in the same test binary; factory must return a fresh, ready-to-use Extractor on
// every call.
func RunExtractorSuite(t *testing.T, name string, factory func(t *testing.T) symbols.Extractor) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		ex := factory(t)
		require.NotNil(t, ex)

		// None of Extract, Enclosing or References has an error return (§5.22b); any value they
		// produce is shape-valid, so this only asserts that calling each does not panic.
		_ = ex.Extract("probe.go", []byte(probeSource))
		_, _ = ex.Enclosing("probe.go", []byte(probeSource), 0)
		_ = ex.References([]byte(probeSource), []string{"ProbeSymbol"})
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("enclosing_returns_smallest_span", func(t *testing.T) { runEnclosingSmallestSpanCase(t, factory) })
		t.Run("extract_stable_under_crlf", func(t *testing.T) { runExtractStableUnderCRLFCase(t, factory) })
	})
}

// isStub reports whether factory currently produces a stub Extractor, using Extract as the probe
// (plans/OWNERS.tsv: symbols's probe method is Extract). Extract has no error return (§5.22b), so
// unlike most suites in this tree, isStub cannot check core.IsNotImplemented: instead it relies
// directly on Rule 1's documented stub contract (the SP-01 stub always returns nil regardless of
// input). probeSource unambiguously declares one top-level function, so any real Extractor must
// find at least one symbol in it.
func isStub(t *testing.T, factory func(t *testing.T) symbols.Extractor) bool {
	t.Helper()
	return len(factory(t).Extract("probe.go", []byte(probeSource))) == 0
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Extractor, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) symbols.Extractor) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
