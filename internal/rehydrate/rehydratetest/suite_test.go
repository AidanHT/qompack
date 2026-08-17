package rehydratetest_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/rehydrate/rehydratetest"
	"github.com/qompack/qompack/internal/rules"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/skills"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
)

// buildFunc binds rehydrate.Build to a Deps assembled from the SP-01 stubs, and fills in the
// Request.Cfg the suite may not name (00-ARCHITECTURE.md §3.2). SP-11 binds the same closure
// against real seams.
func buildFunc(t *testing.T) rehydratetest.BuildFunc {
	t.Helper()
	root := t.TempDir()
	cfg := config.Defaults()

	st, err := store.Open(root, cfg, store.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	// As of SP-06 store.Open returns a real store holding open append-only handles. They must be
	// released before the test's TempDir is removed, or RemoveAll fails on Windows.
	t.Cleanup(func() { _ = st.Close() })
	ledger, err := negknow.Open(root, cfg, &sketch.Bloom{}, negknow.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := dag.Open(root, cfg, logging.Nop())
	if err != nil {
		t.Fatal(err)
	}

	deps := rehydrate.Deps{
		Store:  st,
		Ledger: ledger,
		Graph:  graph,
		Rules:  rules.New(),
		Skills: skills.New(),
		Tokens: tokens.New(cfg, filepath.Join(root, "calibration.json")),
		Log:    logging.Nop(),
	}

	return func(ctx context.Context, r rehydrate.Request) (rehydrate.Result, error) {
		r.Cfg = cfg
		if r.ProjectRoot == "" {
			r.ProjectRoot = root
		}
		return rehydrate.Build(ctx, r, deps)
	}
}

// fakeStubBuild mirrors the shape of an SP-01-style stub Build: a zero Result and
// core.ErrNotImplemented, with no Deps involved at all. It exists so the suite is exercised
// against a second, independent stub as well as against the real one.
func fakeStubBuild(t *testing.T) rehydratetest.BuildFunc {
	t.Helper()
	return func(ctx context.Context, r rehydrate.Request) (rehydrate.Result, error) {
		return rehydrate.Result{}, core.ErrNotImplemented
	}
}

// TestRehydrateSuite_ShapePassesAgainstStub proves the rehydratetest suite's shape block passes
// against the SP-01 stubs — the real rehydrate.Build stub wired to real (stub) Deps, and an
// independent fake — and that the behaviour block is skipped with the exact Rule W-1 message.
// SP-11 reuses RunRehydrateSuite unchanged, pointed at its real implementation, to flip that skip
// off.
//
// Each suite runs inside its own t.Run wrapper. That is load-bearing, not cosmetic: Rule W-1's
// t.Skip fires on the *T the suite was handed, so calling several suites directly from one test
// function would let the first stub skip abort the rest of them before they ever ran.
func TestRehydrateSuite_ShapePassesAgainstStub(t *testing.T) {
	t.Run("fake-stub", func(t *testing.T) {
		rehydratetest.RunRehydrateSuite(t, "fake-stub", fakeStubBuild)
	})

	t.Run("rehydrate-build-stub", func(t *testing.T) {
		rehydratetest.RunRehydrateSuite(t, "rehydrate.Build-stub", buildFunc)
	})
}
