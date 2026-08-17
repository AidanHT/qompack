package daemon

import (
	"sync"
	"time"

	"github.com/qompack/qompack/internal/core"
)

// fakeClock and epoch are daemon's own copies of internal/testutil's FakeClock/Epoch.
//
// internal/testutil cannot be imported here: testutil's own project.go imports internal/cli (for
// (*Project).RunHook's in-process dispatch mode), and — since SP-05's task 6 — internal/cli imports
// internal/daemon (hookclient.go's Spawn/EnsureRunning seam, cli/daemon.go's `qompack daemon`
// subcommand). A daemon _test.go file importing testutil would therefore close a
// daemon -> testutil -> cli -> daemon cycle in the test build graph. internal/cli/dispatch_test.go
// carries this exact same trivial local copy for the identical reason (see its own fakeClock
// doc comment) — this file follows that established convention rather than inventing a new one.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

var _ core.Clock = (*fakeClock)(nil)

// epoch mirrors testutil.Epoch: 2026-01-01T00:00:00Z.
var epoch = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

func newFakeClock(t0 time.Time) *fakeClock {
	return &fakeClock{now: t0.Round(0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Since(t time.Time) time.Duration {
	return c.Now().Sub(t)
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
