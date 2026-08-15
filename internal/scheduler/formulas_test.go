package scheduler_test

import (
	"math"
	"testing"

	"github.com/qompack/qompack/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// TestYoungDaly_Formula pins scheduler.YoungDaly to the closed-form Appendix A definition
// I* = sqrt(2*delta*M), including its two degenerate-input guards. Unlike every other package
// this subagent ships, this function is real (not a stub), so this test runs unconditionally —
// it is never gated behind a Rule W-1 skip.
func TestYoungDaly_Formula(t *testing.T) {
	require.InDelta(t, math.Sqrt(2*30*600), scheduler.YoungDaly(30, 600), 1e-9)
	require.Equal(t, 0.0, scheduler.YoungDaly(0, 600), "delta<=0 must report 0, not NaN")
	require.Equal(t, 0.0, scheduler.YoungDaly(-1, 5), "delta<=0 must report 0, not NaN")
	require.Equal(t, 0.0, scheduler.YoungDaly(30, 0), "mtbf<=0 must report 0, not NaN")
	require.Equal(t, 0.0, scheduler.YoungDaly(30, -5), "mtbf<=0 must report 0, not NaN")
}

// TestSkiRental_ComputedNotLiteral proves the write threshold is computed as w/r rather than
// hardcoded: at r=0.1, w=1.25 the break-even is w/r=12.5, so 13 expected reads should write and
// 12 should not. A non-positive r must never write, since the ratio is then undefined.
func TestSkiRental_ComputedNotLiteral(t *testing.T) {
	require.True(t, scheduler.SkiRentalShouldWrite(13, 0.1, 1.25))
	require.False(t, scheduler.SkiRentalShouldWrite(12, 0.1, 1.25))
	require.False(t, scheduler.SkiRentalShouldWrite(1, 0, 1.25))
}

// TestSkiRental_ThresholdTracksConfig proves the break-even genuinely moves when r/w move,
// rather than being pinned to the §5.1 worked example: at r=0.2, w=1.0 the break-even is w/r=5,
// a different threshold than the 12.5 case above would give the same expectedReads value.
func TestSkiRental_ThresholdTracksConfig(t *testing.T) {
	require.True(t, scheduler.SkiRentalShouldWrite(6, 0.2, 1.0))
	require.False(t, scheduler.SkiRentalShouldWrite(5, 0.2, 1.0))
	require.False(t, scheduler.SkiRentalShouldWrite(-100, 0.2, 1.0), "negative expected reads can never clear a positive threshold")
}

// TestPSelectionAvailable_DefaultsFalse guards the closing-note priority-3 ship order: SP-12 is
// the only subplan permitted to flip this to true, and every build that predates SP-12 must
// report false so analyzer.NewSelector keeps refusing to construct a Selector.
func TestPSelectionAvailable_DefaultsFalse(t *testing.T) {
	require.False(t, scheduler.PSelectionAvailable())
}
