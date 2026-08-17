package canon_test

import (
	"testing"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// threshold reads Appendix C's store.canonicalize.minhash.nearDupThreshold rather than spelling
// 0.9, mirroring what production callers must do: §11.6 forbids that literal outside
// internal/config/defaults.go, and the point of the rule is that a threshold change lands in one
// place.
func threshold() float64 { return config.Defaults().Store.Canonicalize.MinHash.NearDupThreshold }

// TestDecide_Table pins the delta-versus-full choice of Qompack.md §8.1.
func TestDecide_Table(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		jaccard       float64
		canonLen      int
		priorLen      int
		wantNearDup   bool
		wantStrategy  canon.Strategy
		wantDeltaByte int
	}{
		// A near-identical 10 KB rerun: 5% of 10 200 bytes plus framing is 574, and 1 148 < 10 000,
		// so a delta is worth storing. This is the §8.1 "same test suite, one new failure" case.
		{"near_identical_large", 0.95, 10000, 10200, true, canon.StrategyDelta, 574},
		// Just under the threshold: not a near-duplicate at all, so the delta is never considered.
		{"below_threshold", 0.89, 10000, 10200, false, canon.StrategyFull, 1186},
		// Similar, but the new payload is tiny against a large prior: the delta would be bigger
		// than half of it, so storing the 200 bytes outright is cheaper than a chained record.
		{"small_against_large_prior", 0.95, 200, 10000, true, canon.StrategyFull, 564},
		// Identical content: the delta is pure framing.
		{"identical", 1.0, 10000, 10000, true, canon.StrategyDelta, 64},
		// Degenerate lengths short-circuit before any arithmetic.
		{"zero_canon_len", 0.9, 0, 100, false, canon.StrategyFull, 0},
		{"zero_prior_len", 0.9, 100, 0, false, canon.StrategyFull, 0},
		// Unrelated content.
		{"unrelated", 0.5, 1000, 1000, false, canon.StrategyFull, 564},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := canon.Decide(tc.jaccard, tc.canonLen, tc.priorLen, threshold())
			require.Equal(t, tc.wantNearDup, got.NearDup, "NearDup")
			require.Equal(t, tc.wantStrategy, got.Strategy, "Strategy")
			require.Equal(t, tc.wantDeltaByte, got.EstDeltaBytes, "EstDeltaBytes")
			require.InDelta(t, tc.jaccard, got.Jaccard, 1e-12)
		})
	}
}

// TestDecide_ThresholdIsInclusive asserts the comparison is >=, so a Jaccard exactly at the
// configured threshold counts as a near-duplicate.
func TestDecide_ThresholdIsInclusive(t *testing.T) {
	t.Parallel()

	th := threshold()
	require.True(t, canon.Decide(th, 10000, 10000, th).NearDup)
	require.False(t, canon.Decide(th-1e-9, 10000, 10000, th).NearDup)
}

// TestStrategy_String pins the two rendered names, which reach logs and metrics.
func TestStrategy_String(t *testing.T) {
	t.Parallel()
	require.Equal(t, "full", canon.StrategyFull.String())
	require.Equal(t, "delta", canon.StrategyDelta.String())
}

// TestNearDup_DelegatesToSignature asserts NearDup returns both the boolean and the score, and
// that both come from sketch rather than from a second opinion computed here.
//
// SP-03 owns sketch and is a same-wave sibling, so this asserts the delegation rather than any
// particular similarity value: it holds against SP-03's stub (which reports false and 0) and
// against its real implementation, which is what Rule W-2 requires of a same-wave consumer.
func TestNearDup_DelegatesToSignature(t *testing.T) {
	t.Parallel()

	o := canon.OptionsFrom(config.Defaults().Store.Canonicalize, false)
	a := sketch.MinHash([]byte("ok example.com/pkg <d>\n"), o.MinHash)
	b := sketch.MinHash([]byte("ok example.com/pkg <d>\nFAIL other\n"), o.MinHash)

	isDup, j := canon.NearDup(a, b, threshold())
	require.Equal(t, a.IsNearDup(b, threshold()), isDup)
	require.InDelta(t, a.Jaccard(b), j, 1e-12)
}

// decisionRow is one golden row: the inputs to Decide and everything it concluded.
type decisionRow struct {
	Jaccard       float64 `json:"jaccard"`
	CanonLen      int     `json:"canonLen"`
	PriorLen      int     `json:"priorLen"`
	Threshold     float64 `json:"threshold"`
	NearDup       bool    `json:"nearDup"`
	Strategy      string  `json:"strategy"`
	EstDeltaBytes int     `json:"estDeltaBytes"`
}

// TestDecide_GoldenFixture freezes the decision surface SP-06 builds store.NearDupInfo from.
//
// Decide is a pure function of four numbers, so a golden is the cheapest way to make any change
// to the size model — the (1-j)*max(len) estimate, the framing constant, or the "under half"
// rule — show up as a reviewable diff rather than as a quietly different storage strategy.
func TestDecide_GoldenFixture(t *testing.T) {
	t.Parallel()

	th := threshold()
	rows := make([]decisionRow, 0, 64)
	for _, j := range []float64{0.0, 0.25, 0.5, 0.75, 0.85, 0.89, 0.9, 0.95, 0.99, 1.0} {
		for _, lens := range [][2]int{{200, 10000}, {1000, 1000}, {10000, 10200}, {65536, 65536}} {
			d := canon.Decide(j, lens[0], lens[1], th)
			rows = append(rows, decisionRow{
				Jaccard:       j,
				CanonLen:      lens[0],
				PriorLen:      lens[1],
				Threshold:     th,
				NearDup:       d.NearDup,
				Strategy:      d.Strategy.String(),
				EstDeltaBytes: d.EstDeltaBytes,
			})
		}
	}
	testutil.GoldenJSON(t, "dedup-decisions.json", rows)
}
