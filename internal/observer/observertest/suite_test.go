package observertest_test

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/observer/observertest"
	"github.com/qompack/qompack/internal/store"
)

// fakeStubObserver mirrors the shape of an SP-01-style stub Observer: every entry point reports
// core.ErrNotImplemented alongside a valid, do-nothing Output. It exists so the suite's shape
// block and its Rule W-1 skip are still exercised against a stub now that observer.New itself
// returns the real implementation.
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
// against an SP-01-style stub and that the behaviour block is skipped with the exact Rule W-1
// message. It is the negative control for the real run below: the skip has to still fire for a
// stub, or "the behaviour block passed" would be an unfalsifiable claim.
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
}

// TestObserverSuite_RealObserver runs the whole suite — shape AND behaviour — against the real
// SP-08 implementation. Landing the real observer.New is what lifts skipIfStub's Rule W-1 skip:
// the probe no longer sees core.ErrNotImplemented from OnToolUse, so the eight behaviour cases
// run for the first time.
func TestObserverSuite_RealObserver(t *testing.T) {
	observertest.RunObserverSuite(t, "observer.New", newRealObserver)
}

// newRealObserver builds a fresh, fully-wired Observer over a real store and a real dependence
// graph on their own temporary root. The suite calls its factory once per case and documents that
// every call must return a ready-to-use Observer, so nothing here is shared between calls.
func newRealObserver(t *testing.T) observer.Observer {
	t.Helper()

	root := t.TempDir()
	cfg := config.Defaults()
	log := logging.Nop()
	clock := core.SystemClock()

	st, err := store.Open(root, cfg, store.Deps{Log: log, Clock: clock})
	if err != nil {
		t.Fatalf("store.Open(%s): %v", root, err)
	}
	t.Cleanup(func() { _ = st.Close() })

	g, err := dag.Open(root, cfg, log)
	if err != nil {
		t.Fatalf("dag.Open(%s): %v", root, err)
	}

	o, err := observer.New(observer.Options{
		ProjectRoot: root,
		Cfg:         cfg,
		Store:       st,
		Graph:       g,
		Log:         log,
		Metrics:     obs.New(clock),
		Clock:       clock,
	})
	if err != nil {
		t.Fatalf("observer.New: %v", err)
	}
	return o
}
