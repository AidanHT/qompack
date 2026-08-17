package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

func fullMode() contract.Mode { return contract.ModeFull }

// TestIdleRunsByPriority pins ascending-priority run order regardless of registration order.
func TestIdleRunsByPriority(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(epoch)
	c := newIdleController(120, clk, logging.Nop(), nil, fullMode)

	var ran []string
	c.Register("c", 30, func(context.Context) error { ran = append(ran, "c"); return nil })
	c.Register("a", 10, func(context.Context) error { ran = append(ran, "a"); return nil })
	c.Register("b", 20, func(context.Context) error { ran = append(ran, "b"); return nil })

	names, err := c.RunOnce(context.Background(), time.Second)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c"}, names)
	require.Equal(t, []string{"a", "b", "c"}, ran)
}

// TestIdleRespectsBudget: task A consumes the whole budget (by advancing the FakeClock itself),
// so B never runs, and RunOnce returns no error.
func TestIdleRespectsBudget(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(epoch)
	c := newIdleController(120, clk, logging.Nop(), nil, fullMode)

	budget := 100 * time.Millisecond
	bRan := false
	c.Register("a", 10, func(context.Context) error {
		clk.Advance(budget) // consumes the entire RunOnce budget
		return nil
	})
	c.Register("b", 20, func(context.Context) error {
		bRan = true
		return nil
	})

	names, err := c.RunOnce(context.Background(), budget)
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, names)
	require.False(t, bRan, "b must never run once the budget is exhausted")
}

// TestIdleTaskPanicIsolated: A panics, B does not; only b appears in ran, the panic counter fires
// once, and RunOnce itself returns a nil error.
func TestIdleTaskPanicIsolated(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(epoch)
	m := obs.New(clk)
	c := newIdleController(120, clk, logging.Nop(), m, fullMode)

	c.Register("a", 10, func(context.Context) error { panic("boom") })
	c.Register("b", 20, func(context.Context) error { return nil })

	names, err := c.RunOnce(context.Background(), time.Second)
	require.NoError(t, err)
	require.Equal(t, []string{"b"}, names)
	require.Equal(t, int64(1), m.Counter(counterIdleTaskPanic).Value())
}

// TestIdleTaskErrorIsNotFatal: a task returning an error is logged and does not stop the rest —
// it still counts as having run.
func TestIdleTaskErrorIsNotFatal(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(epoch)
	c := newIdleController(120, clk, logging.Nop(), nil, fullMode)

	c.Register("a", 10, func(context.Context) error { return errors.New("boom") })
	c.Register("b", 20, func(context.Context) error { return nil })

	names, err := c.RunOnce(context.Background(), time.Second)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, names)
}

// TestIsIdleUsesDetectAfterSeconds: false at +119s, true at +120s.
func TestIsIdleUsesDetectAfterSeconds(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(epoch)
	c := newIdleController(120, clk, logging.Nop(), nil, fullMode)
	c.Notify(core.NowMilli(clk))

	require.False(t, c.IsIdle(core.NowMilli(clk)+119*1000))
	require.True(t, c.IsIdle(core.NowMilli(clk)+120*1000))
}

// TestIdleRegisterReplacesInPlace: registering an existing name again replaces its function and
// priority rather than adding a second entry.
func TestIdleRegisterReplacesInPlace(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(epoch)
	c := newIdleController(120, clk, logging.Nop(), nil, fullMode)

	calls := 0
	c.Register("a", 10, func(context.Context) error { calls++; return nil })
	c.Register("a", 5, func(context.Context) error { calls += 10; return nil })

	require.Equal(t, []string{"a"}, c.Registered())
	names, err := c.RunOnce(context.Background(), time.Second)
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, names)
	require.Equal(t, 10, calls, "the replaced function must run, not the original")
}

// TestIdleActPrefixSkippedWhenNotMayAct pins the mode-enforcement rule this package applies
// exactly once, here: an "act."-prefixed task is skipped (and omitted from ran) when the current
// mode does not MayAct(); every other task always runs.
func TestIdleActPrefixSkippedWhenNotMayAct(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(epoch)
	degraded := func() contract.Mode { return contract.ModeDegradedPassive }
	c := newIdleController(120, clk, logging.Nop(), nil, degraded)

	c.Register("act.checkpoint", 10, func(context.Context) error { return nil })
	c.Register("drain", 20, func(context.Context) error { return nil })

	names, err := c.RunOnce(context.Background(), time.Second)
	require.NoError(t, err)
	require.Equal(t, []string{"drain"}, names, "an act.-prefixed task must be skipped outside ModeFull")
}

// TestIdleActPrefixRunsWhenMayAct: the same act.-prefixed task runs normally under ModeFull.
func TestIdleActPrefixRunsWhenMayAct(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(epoch)
	c := newIdleController(120, clk, logging.Nop(), nil, fullMode)

	c.Register("act.checkpoint", 10, func(context.Context) error { return nil })

	names, err := c.RunOnce(context.Background(), time.Second)
	require.NoError(t, err)
	require.Equal(t, []string{"act.checkpoint"}, names)
}
