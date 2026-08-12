package schedulertest_test

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/scheduler/schedulertest"
)

// fakeStubRuntime mirrors the shape of an SP-01-style stub Runtime: every operation reports
// core.ErrNotImplemented, exactly like the scheduler.Evaluate free function does today. It exists
// only to exercise RunSchedulerSuite before SP-12 ships a real Runtime in internal/daemon.
type fakeStubRuntime struct{}

func (fakeStubRuntime) Observe(ctx context.Context, f scheduler.Features, at core.TurnIndex) scheduler.ChangepointState {
	return scheduler.ChangepointState{}
}

func (fakeStubRuntime) Evaluate(ctx context.Context) (scheduler.Decision, error) {
	return scheduler.Decision{}, core.ErrNotImplemented
}

func (fakeStubRuntime) NotifyActivity(ts core.UnixMilli) {}

func (fakeStubRuntime) IdleSince() (core.UnixMilli, bool) { return 0, false }

func (fakeStubRuntime) Persist(ctx context.Context) error { return core.ErrNotImplemented }

// TestRunSchedulerSuite_StubIsSkipped proves the suite's shape block passes against a stub
// Runtime and that its behaviour block is skipped with the exact Rule W-1 message. SP-12 reuses
// RunSchedulerSuite unchanged, pointed at its real internal/daemon Runtime, to flip that skip
// off.
func TestRunSchedulerSuite_StubIsSkipped(t *testing.T) {
	schedulertest.RunSchedulerSuite(t, "fake-stub", func(t *testing.T) scheduler.Runtime {
		return fakeStubRuntime{}
	})
}
