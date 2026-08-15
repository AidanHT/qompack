package observer

// Signals is the L0 → L3 hand-off of gap G1.5 (00-ARCHITECTURE.md §5.21): the task-boundary
// evidence a hook payload carries, extracted in L0 and consumed by the scheduler's composite
// trigger. It is a plain value rather than a scheduler type precisely because observer may not
// import scheduler (§3.2) — the hand-off crosses that boundary as data, not as a call.
type Signals struct {
	// TodoCompleted reports that a todo item moved to completed in this event.
	TodoCompleted bool
	// TestPassed reports that a test run in this event succeeded.
	TestPassed bool
	// GitCommit reports that this event was a git commit.
	GitCommit bool
	// Paths lists the file paths this event touched, in paths.Key form.
	Paths []string
}

// ExtractSignals reads the task-boundary signals out of one hook payload: a todo transition, a
// passing test run, a git commit, and the paths the event touched. The scheduler treats each as
// evidence that a unit of work closed, which is when compaction is cheapest (Qompack.md §8.4).
//
// It always returns the zero Signals in this build. ExtractSignals has no error return, so
// core.ErrNotImplemented is unavailable to it, and the zero value is the honest answer under
// §14.1 rule 1 of plans/V1-SP-01-foundation-toolchain-and-contracts.md: no signal was detected,
// because nothing was inspected. Unlike Tombstone — a closed-form renderer — recognizing a todo
// transition or a passing test means parsing per-tool payload shapes that only SP-08 fixes, so
// returning a plausible-looking Signals here would be exactly the faked behaviour rule 2 forbids.
func ExtractSignals(e Event) Signals { return Signals{} }
