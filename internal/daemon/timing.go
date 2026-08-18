package daemon

import "time"

// The daemon's timing guarantees, exported for out-of-package end-to-end tests.
//
// test/e2e cannot see this package's unexported constants, so before V2-MERGE-25 ② every bound it
// waited under was a hand-picked round number with no stated relationship to the mechanism it was
// waiting on. That is how e2eSpoolDrainBound came to be 10s while the fallback it was racing was
// the 30s idle tick: the bound was silently smaller than the thing it waited for, so a timeout
// there could not tell "broken" from "the answer is twenty more seconds".
//
// Each constant below is an alias for the unexported constant that actually governs the behaviour,
// never a second copy of the number. A change to the daemon's own timing therefore moves every
// derived test bound with it, which is the property a copied literal cannot have.
const (
	// SpawnPollBound is how long EnsureRunning polls a freshly spawned daemon's address before
	// reporting core.ErrNotFound — the spawning side's own definition of "it never came up".
	SpawnPollBound = ensureRunningPollBound

	// DrainLineDeadline is the per-line deadline the drainer dispatches each replayed spool or WAL
	// request under. One line's worst case is the unit any "how long may a drain take" bound is
	// built from.
	DrainLineDeadline = drainLineDeadline

	// IdleTickMax is the upper bound on Run's idle-tick cadence, and so on the FALLBACK drain: the
	// interval a spool entry waits out when nothing more prompt picks it up. It is exported to be
	// named in failure messages, so a test that times out says which mechanism it was really
	// waiting for.
	IdleTickMax = idleTickMax

	// StopDrainBound is Stop's bound on draining the in-flight ring — the longest single step of a
	// clean shutdown, and therefore the basis for any bound on a daemon going away.
	StopDrainBound = stopDrainBound
)

// compile-time proof the aliases above really are durations, so a future edit that retyped one of
// the underlying constants fails here rather than at an arithmetic expression in test/e2e.
var (
	_ time.Duration = SpawnPollBound
	_ time.Duration = DrainLineDeadline
	_ time.Duration = IdleTickMax
	_ time.Duration = StopDrainBound
)
