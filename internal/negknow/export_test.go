package negknow

import (
	"context"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
)

// Test-only exports for lineage_bench_test.go (package negknow_test), which grades the §11.2 Query
// rows with the composition root's real Deps.Ancestry, checkpoint.LedgerAncestry, wired in. An
// in-package test file cannot import internal/checkpoint (checkpoint imports negknow, so the test
// binary would hold an import cycle), and bench_test.go's own rows open their ledgers with no
// Ancestry at all, which is how a lineage read on every Query went unmeasured.

// The two §11.2 Query budgets, exactly as bench_test.go grades them.
const (
	BudgetQueryHit  = budgetQueryHit
	BudgetQueryMiss = budgetQueryMiss
)

// RequireBudgetOn is requireBudgetOn: the same estimator, both clocks, the same scaling.
func RequireBudgetOn(t *testing.T, name string, fn func(*testing.B), budget time.Duration, coloaded bool) {
	t.Helper()
	requireBudgetOn(t, name, fn, budget, coloaded)
}

// BenchRecordCount is the record count the §11.2 Query rows name.
const BenchRecordCount = benchRecordCount

// BenchRecordAt is the i'th synthetic elimination of the fixed-seed corpus.
func BenchRecordAt(i int) Record { return benchRecordAt(i) }

// BenchDaemonLedger opens the daemon's shape of ledger — no Deps.Session, every call made on behalf
// of the session its context carries — over a fresh project with benchDeps' frozen clock and
// discarding logger, and seeds it through the real Record path with BenchRecordCount records made
// by recorder. ancestry builds Deps.Ancestry from the project root, as the composition root does.
// It returns the ledger and the project root.
func BenchDaemonLedger(tb testing.TB, recorder core.SessionID, ancestry func(root string) func(core.SessionID) []Inherited) (Ledger, string) {
	tb.Helper()
	root, cfg := benchProject(tb)
	d := benchDeps(nil, nil)
	d.Session = ""
	d.Ancestry = ancestry(root)
	l := benchOpen(tb, root, cfg, d)
	ctx := WithCaller(context.Background(), Caller{Session: recorder})
	for i := range benchRecordCount {
		if _, err := l.Record(ctx, benchRecordAt(i)); err != nil {
			tb.Fatalf("seeding record %d: %v", i, err)
		}
	}
	return l, root
}

// BenchQueries is the timed loop of BenchmarkQueryHit and BenchmarkQueryMiss over l, asked on ctx:
// b.N Queries for (target, approach) that must each answer want, bracketed on both clocks.
func BenchQueries(b *testing.B, l Ledger, ctx context.Context, target, approach string, want AnswerState) {
	b.ReportAllocs()
	cpuStart := benchCPU(b)
	b.ResetTimer()
	for range b.N {
		a, err := l.Query(ctx, target, approach, ScopeSession)
		if err != nil {
			b.Fatal(err)
		}
		if a.State != want {
			b.Fatalf("Query: state %v, want %v", a.State, want)
		}
	}
	b.StopTimer()
	reportCPU(b, benchCPU(b)-cpuStart)
}
