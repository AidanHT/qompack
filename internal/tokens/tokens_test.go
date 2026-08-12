package tokens_test

import (
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/tokens"
	"github.com/qompack/qompack/internal/tokens/tokenstest"
)

// TestEstimatorSuite_RealImplementation runs the tokenstest conformance suite against the real
// baseline estimator. Unlike every stubbed §5 interface, tokens.New is real from day one (it is
// not in the "Stubs returning core.ErrNotImplemented" list), so the suite's behaviour block is
// expected to run to completion here rather than skip under Rule W-1 — a skip in THIS specific
// test would itself be a bug (devtool lint's stubskips sub-check treats any Rule-W1 skip inside a
// package plans/OWNERS.tsv assigns to SP-01, which tokens is, as a hard failure).
func TestEstimatorSuite_RealImplementation(t *testing.T) {
	tokenstest.RunEstimatorSuite(t, "baseline", func(t *testing.T) tokens.Estimator {
		return tokens.New(config.Defaults(), "")
	})
}
