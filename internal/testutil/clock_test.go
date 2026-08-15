package testutil

import (
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// TestFakeClock_Deterministic asserts the three properties every test in this repository leans on:
// Advance is the only thing that moves Now, Since is exact rather than approximate, and no method
// ever consults the wall clock.
func TestFakeClock_Deterministic(t *testing.T) {
	c := NewFakeClock(Epoch)

	var _ core.Clock = c

	require.Equal(t, Epoch, c.Now(), "a new FakeClock reads exactly the instant it was built at")

	// No wall-clock reads: repeated reads with no Advance in between are byte-identical, and stay
	// identical across arbitrary intervening work.
	first := c.Now()
	for range 1000 {
		_ = c.Since(Epoch)
	}
	require.Equal(t, first, c.Now(), "Now must not drift; a FakeClock reads no wall clock")

	// Since is exact.
	require.Zero(t, c.Since(c.Now()))

	const step = 90 * time.Second
	c.Advance(step)
	require.Equal(t, Epoch.Add(step), c.Now())
	require.Equal(t, step, c.Since(Epoch), "Since must be exactly the advanced duration, not a tolerance")

	c.Advance(step)
	require.Equal(t, 2*step, c.Since(Epoch), "advances accumulate")

	// A backward advance is legal: clock-skew handling is a thing tests need to provoke.
	c.Advance(-2 * step)
	require.Equal(t, Epoch, c.Now())

	// A clock built from a wall-clock reading carries a monotonic component; NewFakeClock strips
	// it, so Sub measures the difference between wall values rather than elapsed real time.
	wall := NewFakeClock(time.Now())
	before := wall.Now()
	wall.Advance(time.Hour)
	require.Equal(t, time.Hour, wall.Since(before))
}

// TestFakeClock_ConcurrentReads asserts a FakeClock may be read from several goroutines while it
// is being advanced, which is what happens whenever the code under test observes time off the
// caller's goroutine. It is the assertion `go test -race` turns into a real check.
func TestFakeClock_ConcurrentReads(t *testing.T) {
	c := NewFakeClock(Epoch)

	const readers = 8
	done := make(chan struct{}, readers)
	for range readers {
		go func() {
			for range 100 {
				_ = c.Now()
				_ = c.Since(Epoch)
			}
			done <- struct{}{}
		}()
	}
	for range 100 {
		c.Advance(time.Millisecond)
	}
	for range readers {
		<-done
	}
	require.Equal(t, Epoch.Add(100*time.Millisecond), c.Now())
}
