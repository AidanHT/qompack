package rehydrate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/tokens"
)

// V6 assembled-budget correction (V6 plan §5, inventory 1.6.18, SP-11 T11-BUDGET-01).
//
// Result.Tokens must be the estimator applied to the COMPLETE rendered additionalContext — wrapper
// and inter-section separators included — not a running sum of independently-estimated fragments.
// Two things make the fragment sum wrong: the estimator is NON-ADDITIVE (it scans units and rounds,
// so Σ Estimate(fragmentᵢ) ≠ Estimate(concat)), and the blank-line separators renderBody's
// strings.Join inserts between sections are priced by nobody. The per-item Item.Tokens may stay an
// accounting ALLOCATION that sums to the assembled total, but the total itself must be the assembled
// estimate, and the hard cap must be enforced against THAT number.

// effectiveCap mirrors clampBudget's public contract: a caller's budget is honoured up to the
// configured ceiling, and an unset (<=0) or over-cap ask resolves to the ceiling.
func effectiveCap(t *testing.T, budget core.Tokens) core.Tokens {
	t.Helper()
	maxT := core.Tokens(testCfg().Runtime.Rehydrate.MaxTokens)
	if budget <= 0 || budget > maxT {
		return maxT
	}
	return budget
}

// TestBuild_V6_ResultTokensIsTheAssembledEstimate is the core correction, proven with a REAL
// supported estimator (tokens.New, the shipped exact estimator) over a rich multi-section checkpoint
// at several budgets. It fails on the pre-fix build, where Result.Tokens is the fragment sum and
// diverges from Estimate([]byte(res.Text), ClassProse) by the rounding-and-separator gap.
func TestBuild_V6_ResultTokensIsTheAssembledEstimate(t *testing.T) {
	cp := ckFull(t)
	real := tokens.New(config.Defaults(), filepath.Join(t.TempDir(), "calibration.json"))

	// A rich checkpoint renders every §8.6 section, so the payload carries the maximum number of
	// inter-section separators — the bytes the fragment sum silently omits.
	for _, budget := range []core.Tokens{maxBudget(), minBudget(), core.Tokens(1500), 0} {
		d := fullDeps(t, cp)
		d.Tokens = real

		got, err := Build(context.Background(), requestFor(t, cp, budget), d)
		require.NoError(t, err, "budget %d", int(budget))

		if got.Text == "" {
			// An empty wrapper must never report a non-empty cost (binding decision).
			require.Zero(t, int(got.Tokens), "budget %d: empty payload must cost zero", int(budget))
			continue
		}

		// The identity the correction exists to establish: the reported total is exactly the
		// estimator's price for the COMPLETE assembled payload, wrapper and separators included.
		assembled := real.Estimate([]byte(got.Text), tokens.ClassProse)
		require.Equal(t, int(assembled), int(got.Tokens),
			"budget %d: Result.Tokens must equal Estimate([]byte(Text), ClassProse) — the assembled "+
				"estimate, not the sum of per-fragment estimates", int(budget))

		// The hard cap is enforced against that assembled number, at both ends of the band and below.
		require.LessOrEqual(t, int(got.Tokens), int(effectiveCap(t, budget)),
			"budget %d: the assembled payload must never exceed the effective cap", int(budget))

		// The per-item rows remain an accounting allocation whose arithmetic sum is the assembled
		// total exactly — the inherited conformance invariant (verify_test / state golden), preserved
		// without claiming any individual row is an additive tokenization.
		var sum core.Tokens
		for _, it := range got.Items {
			require.GreaterOrEqual(t, int(it.Tokens), 0,
				"budget %d: an accounting allocation may never charge a negative cost", int(budget))
			sum += it.Tokens
		}
		require.Equal(t, int(got.Tokens), int(sum),
			"budget %d: Result.Tokens must be exactly the sum over Items", int(budget))
	}
}

// perSeparatorCharge is large enough that the unpriced inter-section separators alone push a payload
// the fill pass believed fit well past its cap — the exact failure mode the correction addresses.
const perSeparatorCharge = 200

// separatorChargingEstimator is a non-additive fixture: it prices a string by the baseline byte
// ratio PLUS a heavy per-newline charge. The fill pass reserves each fragment's own trailing
// newline, but the blank-line separators renderBody inserts BETWEEN sections belong only to the
// assembled text — so Estimate(assembledText) exceeds the fragment sum by perSeparatorCharge for
// every join. It forces the assembled payload over budget after the fill pass thought it fit, which
// is what exercises the trim loop's remeasure-and-account path rather than mere arithmetic.
type separatorChargingEstimator struct{}

func (separatorChargingEstimator) Estimate(b []byte, _ tokens.Class) core.Tokens {
	return core.Tokens((len(b)+3)/4 + perSeparatorCharge*strings.Count(string(b), "\n"))
}

func (separatorChargingEstimator) EstimateString(s string, _ tokens.Class) core.Tokens {
	return core.Tokens((len(s)+3)/4 + perSeparatorCharge*strings.Count(s, "\n"))
}

func (separatorChargingEstimator) EstimateRoot(_ context.Context, chunks []core.ChunkRef, _ tokens.Class) core.Tokens {
	var n int
	for _, ch := range chunks {
		n += ch.Len
	}
	return core.Tokens((n + 3) / 4)
}

func (separatorChargingEstimator) Calibrate(observed, estimated core.Tokens) {}
func (separatorChargingEstimator) Factor() float64                           { return 1 }

// TestBuild_V6_UnpricedSeparatorsOverflowIsTrimmedAndAccounted forces the assembled payload over its
// cap using the separator-heavy fixture, and asserts the correction's two obligations under trim:
// the hard cap holds against the REMEASURED assembled estimate, and every whole section the trim
// loop evicted is NAMED in the drop report rather than silently erased.
//
// Pre-fix this fails twice over: Result.Tokens is the fragment sum (so the assembled payload sits
// over the cap uncaught), and the trim loop removes sections without recording a single drop entry.
func TestBuild_V6_UnpricedSeparatorsOverflowIsTrimmedAndAccounted(t *testing.T) {
	cp := ckFull(t)
	d := fullDeps(t, cp)
	d.Tokens = separatorChargingEstimator{}

	// Below the full payload's assembled cost under this fixture, so the fill pass fills toward the
	// cap and the unpriced join separators push the assembled total past it — the trim loop must fire
	// and evict at least one whole section, which is asserted below rather than assumed.
	//
	// Criterion change (w15-rehydrate, D49): the budget was 6000. Item 2 now admits its newest
	// restatement in tier 1 and unused room goes to evolution, and the hard-cap loop cuts section 7
	// to its floor before it evicts a section; at 6000 that cut alone absorbs this fixture's overrun,
	// so no section was evicted and the row's premise silently lapsed. 5000 still overruns by more
	// than section 7 can give back, and the eviction is now asserted, so the premise cannot lapse
	// again unnoticed.
	const budget = core.Tokens(5000)
	got, err := Build(context.Background(), requestFor(t, cp, budget), d)
	require.NoError(t, err)
	require.NotEmpty(t, got.Text, "the wrapper and some tier-1 material still fit at this budget")

	// The cap is enforced against the assembled estimate, not the fragment sum.
	assembled := separatorChargingEstimator{}.Estimate([]byte(got.Text), tokens.ClassProse)
	require.Equal(t, int(assembled), int(got.Tokens),
		"Result.Tokens must be the remeasured assembled estimate after every trim")
	require.LessOrEqual(t, int(got.Tokens), int(budget),
		"the assembled payload must be trimmed to fit the hard cap, separators included")

	// The allocation still sums to the total exactly.
	var sum core.Tokens
	for _, it := range got.Items {
		sum += it.Tokens
	}
	require.Equal(t, int(got.Tokens), int(sum), "the accounting allocation must sum to the total")

	// A trim that removed whole sections must have said so: the payload is degraded and the removal
	// is a NAMED, reportable overflow, never a silent erasure of a current requirement.
	require.True(t, got.Degraded, "a payload trimmed to fit its cap is degraded")
	var evicted bool
	for _, e := range got.Dropped {
		evicted = evicted || e.ID == dropIDEvicted
	}
	require.True(t, evicted, "fixture sanity: the trim loop evicted a whole section: %v", got.Dropped)
	require.True(t, Overflowed(got.Dropped),
		"whole-section eviction under the hard cap must record an explicit, named overflow: %v", got.Dropped)
}
