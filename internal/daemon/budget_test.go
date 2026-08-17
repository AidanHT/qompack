package daemon

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// feedWindow pushes n samples of duration d into b, reporting the transition of the very last
// sample (every window closes on its sampleWindow-th sample; a non-multiple n's tail contributes
// to a not-yet-closed window and always reports NoTransition for its final sample).
func feedWindow(b *breachDetector, n int, d time.Duration) Transition {
	var last Transition
	for i := 0; i < n; i++ {
		last, _ = b.Observe(d)
	}
	return last
}

// TestBreachDetectorTransitionsAfterThreeWindows: limit 15ms, need=3; three consecutive 512-sample
// windows of 20ms each transition NoTransition, NoTransition, ToSpool.
func TestBreachDetectorTransitionsAfterThreeWindows(t *testing.T) {
	t.Parallel()

	b := newBreachDetector(15*time.Millisecond, 3)
	require.Equal(t, NoTransition, feedWindow(b, sampleWindow, 20*time.Millisecond))
	require.Equal(t, NoTransition, feedWindow(b, sampleWindow, 20*time.Millisecond))
	require.Equal(t, ToSpool, feedWindow(b, sampleWindow, 20*time.Millisecond))
}

// TestBreachDetectorResetsOnCleanWindow: a clean window in the middle resets the breach streak,
// so two more breaching windows afterward never reach ToSpool.
func TestBreachDetectorResetsOnCleanWindow(t *testing.T) {
	t.Parallel()

	b := newBreachDetector(15*time.Millisecond, 3)
	require.Equal(t, NoTransition, feedWindow(b, sampleWindow, 20*time.Millisecond))
	require.Equal(t, NoTransition, feedWindow(b, sampleWindow, 1*time.Millisecond)) // clean: resets breaches
	require.Equal(t, NoTransition, feedWindow(b, sampleWindow, 20*time.Millisecond))
	require.Equal(t, NoTransition, feedWindow(b, sampleWindow, 20*time.Millisecond))
}

// TestBreachDetectorRevertsAfterThreeCleanWindows: in spool mode (breaches already tripped),
// three clean windows in a row transition ToSync on the third.
func TestBreachDetectorRevertsAfterThreeCleanWindows(t *testing.T) {
	t.Parallel()

	b := newBreachDetector(15*time.Millisecond, 3)
	require.Equal(t, NoTransition, feedWindow(b, sampleWindow, 1*time.Millisecond))
	require.Equal(t, NoTransition, feedWindow(b, sampleWindow, 1*time.Millisecond))
	require.Equal(t, ToSync, feedWindow(b, sampleWindow, 1*time.Millisecond))
}

// TestBreachDetectorPartialWindowNeverCloses: fewer than sampleWindow samples never closes a
// window, so Observe always reports NoTransition.
func TestBreachDetectorPartialWindowNeverCloses(t *testing.T) {
	t.Parallel()

	b := newBreachDetector(15*time.Millisecond, 1)
	require.Equal(t, NoTransition, feedWindow(b, sampleWindow-1, 20*time.Millisecond))
}

// TestBreachDetectorReset clears the ring and both streaks without touching limit/need.
func TestBreachDetectorReset(t *testing.T) {
	t.Parallel()

	b := newBreachDetector(15*time.Millisecond, 1)
	require.Equal(t, ToSpool, feedWindow(b, sampleWindow, 20*time.Millisecond))
	b.Reset()
	// A fresh window after Reset needs a full sampleWindow of breaching samples again — a
	// partial window right after Reset must not spuriously transition.
	require.Equal(t, NoTransition, feedWindow(b, sampleWindow-1, 20*time.Millisecond))
	tr, closed := b.Observe(20 * time.Millisecond)
	require.Equal(t, ToSpool, tr)
	require.True(t, closed)
}

// TestBreachDetectorObserveReportsClosedOnlyOnTheWindowBoundary pins fix round 1's I-3 contract:
// Observe's second return value is true only on the sampleWindow-th call, never on any of the
// preceding ones — the caller (hotPathWorker) uses this to gate CheckBudgets so it runs once per
// closed window, not once per sample.
func TestBreachDetectorObserveReportsClosedOnlyOnTheWindowBoundary(t *testing.T) {
	t.Parallel()

	b := newBreachDetector(15*time.Millisecond, 3)
	for i := 0; i < sampleWindow-1; i++ {
		_, closed := b.Observe(time.Millisecond)
		require.False(t, closed, "sample %d must not close a window", i)
	}
	_, closed := b.Observe(time.Millisecond)
	require.True(t, closed, "the sampleWindow-th sample must close the window")
}

// TestPercentileDurationP99 pins the nearest-rank ceiling percentile math against a known
// distribution: 512 samples, the top ~1% (samples 507..512, 1-indexed) at a high value, everything
// else low. idx = ceil(0.99*512)-1 = 506 (0-indexed) = the 507th smallest sample.
func TestPercentileDurationP99(t *testing.T) {
	t.Parallel()

	window := make([]time.Duration, sampleWindow)
	for i := range window {
		window[i] = time.Millisecond
	}
	// The 6 largest samples (indices 506..511 once sorted) are set high; idx 506 must land on one
	// of them.
	for i := len(window) - 6; i < len(window); i++ {
		window[i] = 100 * time.Millisecond
	}
	got := percentileDuration(window, 0.99)
	require.Equal(t, 100*time.Millisecond, got)
}
