package mcp

import (
	"sync"
	"time"

	"github.com/qompack/qompack/internal/core"
)

// fakeClock and epoch are mcp's own copies of internal/testutil's FakeClock/Epoch.
//
// internal/testutil cannot be imported here, for the same reason internal/daemon and internal/cli
// each carry this copy: testutil's project.go imports internal/cli (for (*Project).RunHook's
// in-process dispatch mode), and since SP-13 internal/cli imports internal/mcp (cmd_mcp.go's
// `qompack mcp` subcommand). An mcp _test.go file importing testutil would therefore close an
// mcp -> testutil -> cli -> mcp cycle in the test build graph — `go vet` reports it as
// "import cycle not allowed in test", which is a build failure and not a lint warning.
//
// This file follows internal/daemon/fakeclock_test.go's established convention rather than
// inventing a new one. §6.1 bans wall-clock sleeps, so every test in this package drives time
// through this seam and asserts exact values instead of tolerances.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

var _ core.Clock = (*fakeClock)(nil)

// epoch mirrors testutil.Epoch: 2026-01-01T00:00:00Z.
var epoch = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// newFakeClock returns a clock frozen at t0, with any monotonic reading stripped.
func newFakeClock(t0 time.Time) *fakeClock {
	return &fakeClock{now: t0.Round(0)}
}

// Now returns the current fake instant.
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Since returns the fake elapsed time between t and now.
func (c *fakeClock) Since(t time.Time) time.Duration {
	return c.Now().Sub(t)
}

// Advance moves the clock forward by d.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
