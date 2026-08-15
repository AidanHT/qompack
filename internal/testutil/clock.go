package testutil

import (
	"sync"
	"time"

	"github.com/qompack/qompack/internal/core"
)

// Epoch is the instant every Project's FakeClock starts at unless WithClock overrides it:
// 2026-01-01T00:00:00Z. Fixing it repository-wide is what makes a timestamp inside a golden file
// a stable byte sequence rather than a value that has to be scrubbed before comparison.
var Epoch = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// FakeClock is a core.Clock that only ever moves when a test moves it.
//
// 00-ARCHITECTURE.md §6.1 bans wall-clock sleeps outright, so every package that observes time
// takes a core.Clock and every test drives that seam with this type. Advance is the only way time
// passes: a test that wants to observe a five-minute idle gap advances by five minutes and the
// assertion is exact, instead of sleeping and asserting a tolerance.
//
// It is safe for concurrent use, because the code under test may well read the clock from more
// than one goroutine even when the test advances it from one.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// FakeClock must satisfy the seam every production package actually takes.
var _ core.Clock = (*FakeClock)(nil)

// NewFakeClock returns a clock frozen at t0.
//
// The monotonic reading is stripped (t0.Round(0)) so that a t0 obtained from time.Now() behaves
// identically to one built with time.Date: with a monotonic reading attached, Sub would measure
// elapsed real time rather than the difference between the two wall values, which is precisely
// the non-determinism this type exists to remove.
func NewFakeClock(t0 time.Time) *FakeClock {
	return &FakeClock{now: t0.Round(0)}
}

// Now returns the current fake instant.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Since returns the fake elapsed time between t and now. It is exact: Since(c.Now()) is always
// zero, and Since(before) after Advance(d) is always d.
func (c *FakeClock) Since(t time.Time) time.Duration {
	return c.Now().Sub(t)
}

// Advance moves the clock forward by d. A negative d moves it backward, which is occasionally
// what a test of clock-skew handling wants; nothing here forbids it.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
