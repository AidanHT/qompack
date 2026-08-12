package scheduler

// Evaluate is the pure decision function behind the composite trigger (Qompack.md §8.4,
// 00-ARCHITECTURE.md §5.13): given a snapshot of Inputs it returns the Decision the scheduler
// would make, deterministically and without reading a clock, performing I/O, or touching any
// package-level state. That purity is what makes the composite trigger unit-testable and
// replayable outside a live daemon.
//
// Evaluate has no error return, so the stub's contract is expressed entirely through its return
// value: SP-01 ships it returning the zero Decision — ShouldCompact false, no Reasons, no
// Background work — which is the honest "do nothing" answer for every Inputs until SP-12
// implements the real composite trigger (soft floor, changepoint, Young-Daly pacing, hard
// ceiling, idle-cold-cache). SP-12 replaces this body; it may not change the signature (§0).
func Evaluate(in Inputs) Decision {
	return Decision{}
}
