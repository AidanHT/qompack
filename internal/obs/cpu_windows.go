//go:build windows

package obs

import (
	"fmt"
	"syscall"
	"time"
)

// ProcessCPU returns the CPU time this process has consumed since it started: kernel plus user,
// summed over every thread it has ever run. It is the clock a budget reads when the budget has to
// hold on a host that is running other work.
//
// A wall clock cannot do that job. A starved process waits longer; it does not execute more, so
// its wall time carries the host's load and its CPU time does not. test/bench/hotpath/process.go
// measured exactly that on this repository, quiet versus 88 busy threads on 22 cores: the same
// child's wall p50 moved 138.8 ms to 3219.1 ms and its p99 1600.8 ms to 5411.3 ms, while its CPU
// p50/p99 stayed 15.625/46.875 ms, unchanged to the tick.
//
// Resolution: GetProcessTimes reports in 100 ns units but is only credited on the scheduler tick,
// which is 15.625 ms on Windows — the reason every CPU figure quoted above is a whole number of
// ticks. A caller must therefore span enough work that one tick is a small fraction of the limit
// it is grading against, and must treat a zero reading as a failed measurement rather than as a
// fast one: zero is the single value that can only ever make a CPU budget pass.
//
// What this clock cannot see is time the process spent not running its own instructions — sleeping,
// blocking on I/O, or waiting on a child process, whose CPU is charged to the child. A budget that
// has to catch those needs a different instrument; a budget that exists to catch work getting more
// expensive wants this one.
func ProcessCPU() (time.Duration, error) {
	// A pseudo-handle for the current process. It is a constant, not a real handle, so there is
	// nothing to close.
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, fmt.Errorf("obs: GetCurrentProcess: %w", err)
	}
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, fmt.Errorf("obs: GetProcessTimes: %w", err)
	}
	return filetimeDuration(kernel) + filetimeDuration(user), nil
}

// filetimeUnit is the tick a FILETIME counts in.
const filetimeUnit = 100 * time.Nanosecond

// filetimeDuration reads a FILETIME that holds an ELAPSED time rather than a point in time.
//
// syscall.Filetime.Nanoseconds() cannot be used for this and the difference is not subtle: it
// treats the value as an absolute timestamp and subtracts the 1601 epoch, so GetProcessTimes'
// kernel and user fields — which count from zero — come back as a date in the 1600s, about
// -1.35 million hours. Two such readings still subtract to the right interval, because the epoch
// cancels, which is exactly why this is worth a named function and a test rather than an inline
// call somebody later trusts on its own.
func filetimeDuration(ft syscall.Filetime) time.Duration {
	return time.Duration(int64(ft.HighDateTime)<<32|int64(ft.LowDateTime)) * filetimeUnit
}
