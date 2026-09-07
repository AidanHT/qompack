package scheduler

import (
	"testing"

	"github.com/qompack/qompack/internal/core"
)

// benchCandidateCount is the plan's performance fixture: twice what prepareCandidates keeps, so
// the sort, the cap and the scored sweep all run at their bounds.
const benchCandidateCount = 64

// benchSink keeps the compiler from eliding Evaluate's result.
var benchSink Decision

// evaluateBenchInputs is TestEvaluate_NoAllocationsBeyondBudget's fixture: the warm,
// above-soft-floor scenario with a fired changepoint, a four-entry posterior and 64 round-boundary
// candidates with monotone reclaimable tokens.
func evaluateBenchInputs() Inputs {
	in := fire(baseInputs())
	in.Changepoint.Posterior = []float64{0.4, 0.3, 0.2, 0.1}
	in.Candidates = make([]Candidate, 0, benchCandidateCount)
	for i := range benchCandidateCount {
		in.Candidates = append(in.Candidates, Candidate{
			Pos:               1_000 * (i + 1),
			Turn:              core.TurnIndex(i),
			SegmentID:         core.SegmentID(i / 4),
			RoundBoundary:     true,
			ReclaimableTokens: core.Tokens(100_000 - 1_000*i),
			Coupling:          i,
		})
	}
	return in
}

// BenchmarkEvaluate_64Candidates is 00-ARCHITECTURE §7's named micro-benchmark for the composite
// trigger. Budget: ≤ 50 µs/op and ≤ 8 allocs/op; benchstat fails a >25% regression against
// testdata/bench-baseline.txt. Evaluate is pure, so the same Inputs value is reused across
// iterations and the benchmark measures the decision alone.
func BenchmarkEvaluate_64Candidates(b *testing.B) {
	in := evaluateBenchInputs()
	if got := len(in.Candidates); got != benchCandidateCount {
		b.Fatalf("fixture has %d candidates, want %d", got, benchCandidateCount)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSink = Evaluate(in)
	}
}
