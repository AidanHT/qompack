package modwire_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/modwire"
)

// TestTiming_TheLayersNest pins the three timing layers: the mod's timeout, the CLI's record margin
// inside it and the daemon's reply margin inside the CLI's wait. A CLI that started at once leaves
// the daemon a positive budget.
func TestTiming_TheLayersNest(t *testing.T) {
	require.Equal(t, 2000, modwire.TimeoutMs)
	require.Equal(t, 100, modwire.RecordMarginMs)
	require.Equal(t, 200, modwire.DaemonReplyMarginMs)
	require.Positive(t, modwire.TimeoutMs-modwire.RecordMarginMs-modwire.DaemonReplyMarginMs)
}
