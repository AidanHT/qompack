package eval

import (
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// TestHostAutoCompactThreshold_200K pins §2.5's worked example verbatim:
//
//	effectiveContextWindow = contextWindow − min(maxOutputTokens, 20_000)
//	autoCompactThreshold   = effectiveContextWindow − 13_000
//	"For a 200K model: effective ≈ 180K, threshold ≈ 167K."
//
// 167K is not trivia: it is the residual span the latency model is calibrated at, so if this
// arithmetic ever moves, TestLatencyModel_Anchors is measuring a different host.
func TestHostAutoCompactThreshold_200K(t *testing.T) {
	const contextWindow core.Tokens = 200_000

	require.Equal(t, core.Tokens(180_000), hostEffectiveContextWindow(contextWindow))
	require.Equal(t, core.Tokens(167_000), hostAutoCompactThreshold(contextWindow))
}

// TestHostPadTokens is §2.2's "token estimation pads by 4/3", the coarseness SP-06 later fixes
// and that the stock model must reproduce rather than improve on.
func TestHostPadTokens(t *testing.T) {
	require.Equal(t, core.Tokens(4000), hostPadTokens(3000))
	require.Equal(t, core.Tokens(0), hostPadTokens(0))
	require.Equal(t, core.Tokens(1), hostPadTokens(1), "integer division truncates, as the host's does")
}
