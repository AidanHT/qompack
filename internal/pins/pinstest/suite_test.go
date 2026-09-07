package pinstest_test

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/pins/pinstest"
)

// fakeStubStore mirrors the shape of an SP-01-style stub pins.Store: every operation reports
// core.ErrNotImplemented, which is exactly what pins.Open's own stub used to do. It exists so this
// file's subject stays a stub forever, whatever the real implementation grows into.
type fakeStubStore struct{}

func (fakeStubStore) Add(ctx context.Context, inv pins.Invariant) error {
	return core.ErrNotImplemented
}

func (fakeStubStore) Remove(ctx context.Context, id string) error { return core.ErrNotImplemented }

func (fakeStubStore) All(ctx context.Context) ([]pins.Invariant, error) {
	return nil, core.ErrNotImplemented
}

func (fakeStubStore) Materialize(ctx context.Context) error { return core.ErrNotImplemented }

// TestRunPinsSuite_StubIsSkipped proves the suite's shape block passes against a stub Store and
// that its behaviour block is skipped with the exact Rule W-1 message — the literal string
// devtool lint's stubskips sub-check greps test output for.
//
// It used to drive pins.Open, on the ground that Open returned a stub. SP-10 made Open real, so
// that driver stopped proving what its name says: no skip fired, the behaviour block ran for
// real, and this test became a silent duplicate of TestRunPinsSuite_RealStore in internal/pins
// under a name asserting the opposite. It passed, which is why nothing caught it. The fake below
// is a stub by construction and cannot drift the same way — the same resolution
// checkpointtest/suite_test.go's writer-fake-stub and reader-fake-stub drivers already carry.
//
// The real conformance run lives where a real store can be built: TestRunPinsSuite_RealStore in
// internal/pins, which points RunPinsSuite at pins.OpenWith and fails if the skip ever fires
// again.
func TestRunPinsSuite_StubIsSkipped(t *testing.T) {
	pinstest.RunPinsSuite(t, "fake-stub", func(t *testing.T) pins.Store {
		return fakeStubStore{}
	})
}
