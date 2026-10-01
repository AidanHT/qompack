package checkpoint_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/tokens"
)

// TestPreCompactPricesTheCallersDropsWithTheDraftsEstimator (V6 close-out D55, wave 16b): a caller
// whose drop report must fit a share of the budget as Truncate measures it hands PreCompact a pricer,
// and PreCompact calls it once with the estimator the seal's Truncate uses, the draft's own
// SourceSet.Tokens, and seals what it returns. The daemon's unreplayed-capture report depends on it:
// priced with any other estimator, its names could overrun their share on a calibrated project.
func TestPreCompactPricesTheCallersDropsWithTheDraftsEstimator(t *testing.T) {
	f := newFx(t)
	seedForPreCompact(t, f)

	var calls int
	var priced tokens.Estimator
	entry := checkpoint.DropEntry{Kind: checkpoint.DropKindUnreplayedCapture, Detail: "priced by the seal"}
	in := f.precompactInput()
	in.PricedDrops = func(est tokens.Estimator) []checkpoint.DropEntry {
		calls++
		priced = est
		return []checkpoint.DropEntry{entry}
	}
	res, err := f.w.PreCompact(f.ctx(), in)
	require.NoError(t, err)

	require.Equal(t, 1, calls)
	require.NotNil(t, priced)
	require.True(t, priced == f.src.Tokens, "the pricer gets the draft's own estimator, the one Truncate uses")
	raw, err := os.ReadFile(paths.Long(res.Ref.Path))
	require.NoError(t, err)
	sealed, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)
	require.Contains(t, sealed.Dropped, entry, "what the pricer returned is sealed with the checkpoint")
}
