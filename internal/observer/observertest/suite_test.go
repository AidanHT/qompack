package observertest_test

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/observer/observertest"
)

// fakeStubObserver mirrors the shape of an SP-01-style stub Observer: every entry point reports
// core.ErrNotImplemented alongside a valid, do-nothing Output, exactly like observer.New's own
// stub does today. It exists so the suite is exercised against a second, independent stub as well
// as against the real one.
type fakeStubObserver struct{}

func (fakeStubObserver) OnToolUse(ctx context.Context, e observer.Event) (observer.Output, error) {
	return observer.Output{}, core.ErrNotImplemented
}

func (fakeStubObserver) OnUserPrompt(ctx context.Context, e observer.Event) (observer.Output, error) {
	return observer.Output{}, core.ErrNotImplemented
}

func (fakeStubObserver) OnStop(ctx context.Context, e observer.Event, subagent bool) (observer.Output, error) {
	return observer.Output{}, core.ErrNotImplemented
}

func (fakeStubObserver) OnSessionStart(ctx context.Context, e observer.Event) (observer.Output, error) {
	return observer.Output{}, core.ErrNotImplemented
}

func (fakeStubObserver) OnSessionEnd(ctx context.Context, e observer.Event) (observer.Output, error) {
	return observer.Output{}, core.ErrNotImplemented
}

// TestObserverSuite_ShapePassesAgainstStub proves the observertest suite's shape block passes
// against the SP-01 stubs — both the real observer.New stub and an independent fake — and that
// the behaviour block is skipped with the exact Rule W-1 message. SP-08 reuses RunObserverSuite
// unchanged, pointed at its real implementation, to flip that skip off.
//
// Each suite runs inside its own t.Run wrapper. That is load-bearing, not cosmetic: Rule W-1's
// t.Skip fires on the *T the suite was handed, so calling several suites directly from one test
// function would let the first stub skip abort the rest of them before they ever ran.
func TestObserverSuite_ShapePassesAgainstStub(t *testing.T) {
	t.Run("fake-stub", func(t *testing.T) {
		observertest.RunObserverSuite(t, "fake-stub", func(t *testing.T) observer.Observer {
			return fakeStubObserver{}
		})
	})

	t.Run("observer-new-stub", func(t *testing.T) {
		observertest.RunObserverSuite(t, "observer.New-stub", func(t *testing.T) observer.Observer {
			o, err := observer.New(observer.Options{
				ProjectRoot: t.TempDir(),
				Cfg:         config.Defaults(),
				Log:         logging.Nop(),
				Metrics:     obs.New(core.SystemClock()),
				Clock:       core.SystemClock(),
			})
			if err != nil {
				t.Fatal(err)
			}
			return o
		})
	})
}
