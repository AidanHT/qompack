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
	// session-start polls through EnsureRunningUntil instead: until its hook budget's borrow limit
	// (internal/cli hookBudget, D17b and D21: 8.25 s into the hook) or for this long after its spawn,
	// whichever is later, and never past the last instant a reply could still follow.
	SpawnPollBound = ensureRunningPollBound

	// DrainLineDeadline is the per-line deadline the drainer dispatches each replayed spool or WAL
	// request under. One line's worst case is the unit any "how long may a drain take" bound is
	// built from.
	DrainLineDeadline = drainLineDeadline

	// IdleTickMax is the upper bound on Run's idle-tick cadence. The tick runs its drain only once
	// the whole project has been idle for DetectAfterSeconds (120 s by default), so it is NOT how long
	// a spooled delivery waits while its session is active: that is the client-spool watcher's
	// (ClientSpoolWatchInterval, C1.13). It is exported to be named in failure messages, so a test
	// that times out says which mechanism it was really waiting for.
	IdleTickMax = idleTickMax

	// ClientSpoolWatchInterval is the client-spool watcher's check interval (spool_watch.go, C1.13).
	// While requests keep arriving — and for one interval after the last — a hook's client spool that
	// has stood unchanged for an interval gets a drain pass, and one that pass could not publish is
	// passed again after 2, 4, 8 ... intervals. A delivery that reached only its hook's client spool
	// during an active session is therefore published about two intervals after it was spooled.
	ClientSpoolWatchInterval = spoolCheckInterval

	// StopDrainBound is Stop's bound on draining the in-flight ring — the longest single step of a
	// clean shutdown, and therefore the basis for any bound on a daemon going away.
	StopDrainBound = stopDrainBound

	// StopCleanupBound is how long Run waits for an asynchronously-invoked Stop (admin.shutdown) to
	// finish its ENTIRE cleanup before returning — and so, since internal/cli's runDaemon returns
	// with Run and cmd/qompack is os.Exit(cli.Dispatch(...)), the daemon process's own worst case
	// for going away after being asked to. Any out-of-package wait for "the daemon is gone" has to
	// outlast this, or its timeout cannot tell a wedged daemon from one still finishing.
	StopCleanupBound = stopCleanupBound
)

// CompactAnswerBudget is how long the session.start route waits for a compact rehydration before it
// answers with the deferred note (owner decision D9): one third of the SessionStart manifest
// timeout, 5 s. It is the least reply wait session-start's find/start step leaves the reply when it
// borrows the reply's idle time (D21, internal/cli hookBudget), so a daemon that takes its whole
// bound is still heard. It returns the daemon's own compactAnswerBudget, never a copy of the
// number.
func CompactAnswerBudget() time.Duration { return compactAnswerBudget() }

// compile-time proof the aliases above really are durations, so a future edit that retyped one of
// the underlying constants fails here rather than at an arithmetic expression in test/e2e.
var (
	_ time.Duration = SpawnPollBound
	_ time.Duration = DrainLineDeadline
	_ time.Duration = IdleTickMax
	_ time.Duration = ClientSpoolWatchInterval
	_ time.Duration = StopDrainBound
	_ time.Duration = StopCleanupBound
)
