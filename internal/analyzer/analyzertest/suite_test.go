package analyzertest_test

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/analyzer/analyzertest"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
)

// newSelectorFunc binds analyzer.NewSelector to a slice, a lambda and a lazy-greedy setting, and
// re-keys the suite's plain-string Δ map to dag.NodeID — the three things analyzertest may not
// name itself (00-ARCHITECTURE.md §3.2). SP-15 binds the same closure against its own fixtures.
func newSelectorFunc(t *testing.T) analyzertest.NewSelectorFunc {
	t.Helper()
	cfg := config.Defaults()
	return func(p int, blocks []analyzer.Block, delta map[string]float64) (analyzer.Selector, error) {
		d := make(map[dag.NodeID]float64, len(delta))
		for k, v := range delta {
			d[dag.NodeID(k)] = v
		}
		return analyzer.NewSelector(p, blocks, dag.Slice{}, d,
			cfg.Selection.Submodular.Lambda, cfg.Selection.Submodular.LazyGreedy)
	}
}

// redundancyFixture binds analyzer.DetectRedundancy to a store.Store. The store is a typed nil
// here: DetectRedundancy is a stub that never dereferences it, and internal/store's own Open
// would only hand back another stub. SP-15 binds a real one.
func redundancyFixture(t *testing.T) analyzertest.RedundancyFixture {
	t.Helper()
	return analyzertest.RedundancyFixture{
		Detect: func(ctx context.Context, sess core.SessionID) (analyzer.RedundancyReport, error) {
			return analyzer.DetectRedundancy(ctx, nil, sess)
		},
		Session:      core.SessionID("sess_analyzertest"),
		EmptySession: core.SessionID("sess_analyzertest_empty"),
	}
}

// TestAnalyzerSuite_ShapePassesAgainstStub proves all three analyzertest suites' shape blocks
// pass against the SP-01 stubs, that RunSelectorSuite's /constructor block RUNS and passes — it
// is never skipped, because SP-01 implements NewSelector's guards for real — and that every
// /behaviour block is skipped with the exact Rule W-1 message. SP-15 reuses these suites
// unchanged, pointed at its real implementation, to flip those skips off.
//
// Each suite runs inside its own t.Run wrapper. That is load-bearing, not cosmetic: Rule W-1's
// t.Skip fires on the *T the suite was handed, so calling several suites directly from one test
// function would let the first stub skip abort the rest of them before they ever ran.
func TestAnalyzerSuite_ShapePassesAgainstStub(t *testing.T) {
	t.Run("selector", func(t *testing.T) {
		analyzertest.RunSelectorSuite(t, "analyzer.NewSelector-stub", newSelectorFunc)
	})

	t.Run("delta-scorer", func(t *testing.T) {
		analyzertest.RunDeltaScorerSuite(t, "analyzer.NewCheapScorer-stub", func(t *testing.T) analyzer.DeltaScorer {
			return analyzer.NewCheapScorer(nil)
		})
	})

	t.Run("redundancy", func(t *testing.T) {
		analyzertest.RunRedundancySuite(t, "analyzer.DetectRedundancy-stub", redundancyFixture)
	})
}
