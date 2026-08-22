package obs_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/obs"
)

// cpuBurnIterations is how much arithmetic one burn step does. It is sized so a step costs a few
// milliseconds on any host this repository builds on, which means a handful of steps clears the
// coarsest clock ProcessCPU reads from — Windows' 15.625 ms scheduler tick.
const cpuBurnIterations = 1 << 22

// cpuBurnSteps caps the burn loop so a clock that never advances fails the test instead of hanging
// it. At a few milliseconds a step this is on the order of a second of CPU, two orders of magnitude
// past the tick the reading has to clear.
const cpuBurnSteps = 200

// cpuSink exists so the compiler cannot delete the burn loop as dead code. It is written and never
// meaningfully read, which is the entire point.
var cpuSink uint64

// burn does cpuBurnIterations of work the optimiser has to keep.
func burn() {
	x := cpuSink | 1
	for i := 0; i < cpuBurnIterations; i++ {
		x = x*6364136223846793005 + 1442695040888963407
	}
	cpuSink = x
}

// TestProcessCPU_NeverGoesBackwards: the reading is cumulative for the life of the process, so two
// consecutive samples can be equal — the clock is quantised — but the second can never be smaller.
// A budget graded on a clock that can run backwards would fail at random.
func TestProcessCPU_NeverGoesBackwards(t *testing.T) {
	first, err := obs.ProcessCPU()
	require.NoError(t, err)
	require.GreaterOrEqual(t, first, time.Duration(0), "a process cannot have burned negative CPU")

	second, err := obs.ProcessCPU()
	require.NoError(t, err)
	require.GreaterOrEqual(t, second, first,
		"ProcessCPU went backwards: %v then %v", first, second)
}

// TestProcessCPU_AdvancesWhenTheProcessBurnsCPU is the assertion every CPU-time budget in this
// repository rests on. A stub that returned a constant would satisfy the monotonicity test above
// and would silently make every one of those budgets pass, so the clock is made to prove it moves
// when — and only when — this process actually executes something.
func TestProcessCPU_AdvancesWhenTheProcessBurnsCPU(t *testing.T) {
	start, err := obs.ProcessCPU()
	require.NoError(t, err)

	var used time.Duration
	for range cpuBurnSteps {
		burn()
		now, err := obs.ProcessCPU()
		require.NoError(t, err)
		used = now - start
		if used > 0 {
			break
		}
	}
	require.Positive(t, used,
		"ProcessCPU did not advance across %d burn steps of %d iterations; the clock is not reading this process",
		cpuBurnSteps, cpuBurnIterations)
}
