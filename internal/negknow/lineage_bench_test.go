package negknow_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// The §11.2 Query rows, graded with the lineage the daemon's ledger really reads.
//
// Since D49 (F-C4-UAT06-1) a session-scoped record is visible to the session that made it and to
// that session's forks up to the fork point, and the daemon opens its ledger with
// Deps.Ancestry = checkpoint.LedgerAncestry, which reads state/lineage-<session>.json. bench_test.go
// opens its ledgers with no Ancestry, so BenchmarkQueryHit and BenchmarkQueryMiss stayed green while
// every production Query, a bloom miss included, opened that file first: about 114 µs on Windows
// against the 5 µs miss row and the 50 µs hit row. These rows put the production function back in:
//
//   - not_a_fork: the asking session has no lineage record, the common case.
//   - fork: the asking session is a one-level fork, and the records it is asked about are its
//     parent's, inherited up to the fork point.

const (
	lineageBenchParent core.SessionID = "sess-negknow-bench-parent"
	lineageBenchFork   core.SessionID = "sess-negknow-bench-fork"
	// lineageBenchForkAt is when the fork started: after every record the frozen bench clock
	// stamps (2026-01-01), so the fork inherits all of them.
	lineageBenchForkAt core.UnixMilli = 2_000_000_000_000
)

// lineageBenchCase is one asking session: one that is no fork (asking about its own records), or a
// fork of the session that made them.
type lineageBenchCase struct {
	name   string
	asker  core.SessionID
	lineup func(tb testing.TB, root string)
}

var lineageBenchCases = []lineageBenchCase{
	{name: "not_a_fork", asker: lineageBenchParent, lineup: func(testing.TB, string) {}},
	{name: "fork", asker: lineageBenchFork, lineup: writeBenchLineage},
}

// writeBenchLineage writes the record checkpoint.FileWriter.NoteFork writes when lineageBenchFork
// starts as a fork of lineageBenchParent.
func writeBenchLineage(tb testing.TB, root string) {
	tb.Helper()
	b, err := json.Marshal(checkpoint.Lineage{
		Version: 1, Session: lineageBenchFork, Source: checkpoint.LineageFork,
		ParentSession: lineageBenchParent, OriginSession: lineageBenchParent, At: lineageBenchForkAt,
	})
	if err != nil {
		tb.Fatal(err)
	}
	p := filepath.Join(paths.Of(root).State, "lineage-"+string(lineageBenchFork)+".json")
	if err := os.WriteFile(paths.Long(p), b, 0o600); err != nil {
		tb.Fatal(err)
	}
}

// benchLineageQuery returns the benchmark for c: a hit on one of the parent's records when hit is
// true, a miss otherwise.
func benchLineageQuery(c lineageBenchCase, hit bool) func(*testing.B) {
	return func(b *testing.B) {
		l, root := negknow.BenchDaemonLedger(b, lineageBenchParent, checkpoint.LedgerAncestry)
		c.lineup(b, root)
		ctx := negknow.WithCaller(context.Background(), negknow.Caller{Session: c.asker, Turn: 1})
		if hit {
			r := negknow.BenchRecordAt(negknow.BenchRecordCount / 2)
			negknow.BenchQueries(b, l, ctx, r.Target, r.Approach, negknow.AnswerActive)
			return
		}
		negknow.BenchQueries(b, l, ctx, "src/never/recorded.ts:missing", "rewrite the module", negknow.AnswerAbsent)
	}
}

// TestBudget_QueryHitThroughLineage is the "Query (bloom hit, 5 000 records)" row with the daemon's
// lineage reader wired.
func TestBudget_QueryHitThroughLineage(t *testing.T) {
	for _, c := range lineageBenchCases {
		t.Run(c.name, func(t *testing.T) {
			negknow.RequireBudgetOn(t, "BenchmarkQueryHitThroughLineage/"+c.name, benchLineageQuery(c, true),
				negknow.BudgetQueryHit, obs.UnderCoload())
		})
	}
}

// TestBudget_QueryMissThroughLineage is the "Query (bloom miss)" row with the daemon's lineage
// reader wired: a miss answers absent for every asker, so it has no lineage to read.
func TestBudget_QueryMissThroughLineage(t *testing.T) {
	for _, c := range lineageBenchCases {
		t.Run(c.name, func(t *testing.T) {
			negknow.RequireBudgetOn(t, "BenchmarkQueryMissThroughLineage/"+c.name, benchLineageQuery(c, false),
				negknow.BudgetQueryMiss, obs.UnderCoload())
		})
	}
}
