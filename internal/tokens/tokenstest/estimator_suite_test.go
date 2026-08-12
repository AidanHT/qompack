package tokenstest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/tokens"
)

// stubEstimator is a minimal §14.1-shaped stub: every method that has no error return produces
// the documented zero value. tokens has no stub phase in production, so this hand-built stand-in
// is what actually exercises isStub's detection logic and RunEstimatorSuite's skip path.
type stubEstimator struct{}

func (stubEstimator) Estimate(_ []byte, _ tokens.Class) core.Tokens       { return 0 }
func (stubEstimator) EstimateString(_ string, _ tokens.Class) core.Tokens { return 0 }
func (stubEstimator) EstimateRoot(_ context.Context, _ []core.ChunkRef, _ tokens.Class) core.Tokens {
	return 0
}
func (stubEstimator) Calibrate(_, _ core.Tokens) {}
func (stubEstimator) Factor() float64            { return 0 }

func TestIsStub_DetectsZeroReturningStub(t *testing.T) {
	got := isStub(t, func(*testing.T) tokens.Estimator { return stubEstimator{} })
	require.True(t, got, "isStub must report true for an estimator that always returns zero tokens")
}

func TestIsStub_RealEstimatorIsNotAStub(t *testing.T) {
	got := isStub(t, func(*testing.T) tokens.Estimator {
		return tokens.New(config.Defaults(), "")
	})
	require.False(t, got, "isStub must report false for the real baseline estimator")
}

// skipIfStub(t, true) is deliberately NOT exercised here: doing so would emit a genuine
// "behaviour: implementation is a stub (Rule W-1)" skip from within internal/tokens/tokenstest,
// and devtool lint's stubskips sub-check treats any Rule-W1 skip inside a package
// plans/OWNERS.tsv assigns to SP-01 — which tokens (and therefore tokenstest, via
// packageKeyOf's "tokens/tokenstest" -> "tokens" mapping) is — as a hard failure, on the theory
// that an SP-01-owned package must never itself be a stub. isStub's detection logic is fully
// covered by the two tests above; skipIfStub's true branch is a single documented t.Skip call.
func TestSkipIfStub_DoesNotSkipARealImplementation(t *testing.T) {
	require.False(t, skipIfStub(t, false))
	require.False(t, t.Skipped())
}

// TestStubSkipMsg_IsTheExactRuleW1Wording pins the literal string devtool lint's stubskips
// sub-check greps test output for. Any other wording is a hard lint failure (see
// tools/devtool/stubskips.go).
func TestStubSkipMsg_IsTheExactRuleW1Wording(t *testing.T) {
	require.Equal(t, "behaviour: implementation is a stub (Rule W-1)", stubSkipMsg)
}
