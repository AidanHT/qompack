//go:build !windows

package obs

import (
	"fmt"
	"syscall"
	"time"
)

// ProcessCPU returns the CPU time this process has consumed since it started: system plus user,
// summed over every thread it has ever run. It is the clock a budget reads when the budget has to
// hold on a host that is running other work.
//
// A wall clock cannot do that job. A starved process waits longer; it does not execute more, so
// its wall time carries the host's load and its CPU time does not. test/bench/hotpath/process.go
// measured exactly that on this repository, quiet versus 88 busy threads on 22 cores: the same
// child's wall p50 moved 138.8 ms to 3219.1 ms and its p99 1600.8 ms to 5411.3 ms, while its CPU
// p50/p99 stayed 15.625/46.875 ms, unchanged to the tick.
//
// Resolution: getrusage reports a timeval, and how finely the kernel actually credits it depends on
// its accounting configuration — microseconds where the clock is per-task, a scheduler tick where
// it is sampled. A caller must therefore span enough work that one tick is a small fraction of the
// limit it is grading against, and must treat a zero reading as a failed measurement rather than as
// a fast one: zero is the single value that can only ever make a CPU budget pass.
//
// RUSAGE_SELF and not RUSAGE_CHILDREN: what is measured is this process, so time the process spent
// not running its own instructions — sleeping, blocking on I/O, or waiting on a child, whose CPU is
// charged to the child — is invisible here. A budget that has to catch those needs a different
// instrument; a budget that exists to catch work getting more expensive wants this one.
func ProcessCPU() (time.Duration, error) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, fmt.Errorf("obs: getrusage(RUSAGE_SELF): %w", err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()), nil
}
