package main

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/stretchr/testify/require"
)

// ---- V4-VERIFY §4.4 ---------------------------------------------------------------------------
//
// The scenario the reconciliation map briefs for this file is
// TestV4_FrontierAdvancementKeepsResidualSpanODelta: replay the committed corpus twice, with
// checkpoint.frontier.advanceOnSegmentClose ON and OFF, and show the residual span is bounded and
// smaller with advancement on, without a divergence regression, both P50s recorded.
//
// IT IS NOT WRITTEN, AND THIS FILE IS WHY. The A/B it describes cannot be measured on this tree,
// because the toggle it flips is not an input to anything the replay path computes:
//
//  1. checkpoint.frontier.advanceOnSegmentClose is read in three places, all of them in
//     internal/daemon — the registration of the act.advance_frontier idle task and the two guards
//     inside it. internal/checkpoint does not consult it either; internal/checkpoint's own
//     TestFrontierAdvanceCutsResidualSpan says so in as many words and models the two arms as "the
//     idle task runs" against "the idle task never runs" for exactly that reason.
//  2. The residual span the driver reports, residual_span_p50/p95, is eval.Replay's
//     prefixTokens − KeepSet.P: where the POLICY placed its cut. Neither term is a frontier.
//  3. qompack-rehydrate's own §8.5 residual is Σ tokens over (F, at] where F is frontierOf — the
//     last user turn at or before the compaction, a deterministic function of the session. Its
//     doc comment states plainly that it is not a durable checkpoint frontier and that a real one
//     may be consumed only after a versioned durable seq-to-frontier record exists.
//     qompack-l3 has no frontier in replay at all: it reports the whole prefix as residual.
//
// So both arms of the briefed A/B are the same computation over the same input, and the test would
// be either vacuous — passing because nothing moved, under a name claiming something did — or
// permanently red on its "smaller than with it off" clause. Writing it under either outcome would
// put a false result in the record. A t.Skip would be worse still: a skipped row reads as coverage.
//
// What IS written here is the measurement that establishes all of the above, and the guard that
// says when §4.4 becomes writable. The two arms are two full replays of the committed corpus whose
// configurations differ in that one field and nothing else, and the assertion is that their metrics
// are byte-identical. Both arms' residual-span P50s are recorded on every run, passing or not, as
// the brief requires. If a later change makes the replay path consume the toggle, this test fails,
// and its failure is the notice that the real §4.4 can now be authored — in this package, against
// whichever producer newly reads the flag.
//
// The quantity the toggle really governs is already measured, over a real store, a real SegmentLog
// and a real Finalize, by internal/checkpoint's TestFrontierAdvanceCutsResidualSpan. That is where
// the §10 Phase 4 amortization clause is discharged today; phase4_test.go's p4DischargedBy pointer
// at this scenario is therefore optimistic and is noted in the unit report rather than edited here.

const (
	// x04Policies are the three policies with any residual or frontier behaviour: the stock model,
	// the rehydrator whose A3 residual is frontier-derived, and the L3 policy that has no frontier
	// in replay. If the toggle reached the replay path at all, it would reach one of these.
	x04PolicyStock     = "stock"
	x04PolicyRehydrate = "qompack-rehydrate"
	x04PolicyL3        = "qompack-l3"

	// x04ResidualP50 is the metric the briefed scenario is stated over.
	x04ResidualP50 = "residual_span_p50"
	// x04ResidualP95 is its tail, recorded beside it because a P50 alone hides a bounded claim.
	x04ResidualP95 = "residual_span_p95"
)

// x04DivergenceMetrics are the divergence rows the briefed scenario requires not to regress. They
// are asserted through the byte-identity of the canonical rendering, and logged by name so the
// numbers are in the record either way.
var x04DivergenceMetrics = []string{
	"first_divergence_turn",
	"file_set_jaccard",
	"decision_preservation",
	"redundant_reads",
	"re_attempts",
}

// x04Arm is one full replay of the committed corpus under one configuration.
type x04Arm struct {
	cfg       config.Config
	metrics   map[string]map[string]float64
	canonical []byte
}

// x04Replay replays the whole committed corpus under the given toggle setting.
//
// It uses the driver's own replayCorpus, so the arms are what the gate itself would measure and
// not a re-implementation that could diverge from it.
func x04Replay(t *testing.T, advanceOnSegmentClose bool) x04Arm {
	t.Helper()

	cfg := config.Defaults()
	cfg.Checkpoint.Frontier.AdvanceOnSegmentClose = advanceOnSegmentClose

	root, err := repoRoot()
	require.NoError(t, err)
	sessions, err := eval.New(eval.Options{Cfg: cfg, Log: logging.Nop()}).
		Load(filepath.Join(root, filepath.FromSlash(defaultCorpusPath)))
	require.NoError(t, err)
	require.NotEmpty(t, sessions)

	// A budget generous enough never to be the reason this test fails: the point of the run is the
	// comparison, and a starved runner must not turn it into a false finding.
	startCPU, err := obs.ProcessCPU()
	require.NoError(t, err)
	b := budgets{
		maxCPU: defaultMaxCPU * 4, maxWall: defaultMaxWall,
		startCPU: startCPU, started: time.Now(),
	}

	res, err := replayCorpus(context.Background(), cfg, sessions,
		[]string{x04PolicyStock, x04PolicyRehydrate, x04PolicyL3}, b, io.Discard)
	require.NoError(t, err)

	return x04Arm{cfg: cfg, metrics: res.policies, canonical: canonicalMetrics(res.policies)}
}

// TestV4_FrontierToggleIsNotConsumedByTheReplayPath is V4-VERIFY §4.4's negative control, and the
// standing evidence that the scenario itself is not writable here.
//
// Non-vacuity: the two arms differ in checkpoint.frontier.advanceOnSegmentClose and in nothing
// else — same corpus, same policies, same deterministic replay options — and the configurations are
// compared field by field to prove it rather than asserted to be so.
func TestV4_FrontierToggleIsNotConsumedByTheReplayPath(t *testing.T) {
	require.True(t, config.Defaults().Checkpoint.Frontier.AdvanceOnSegmentClose,
		"the shipped default is advancement ON (§8.5), so the ON arm is the shipped one")

	on := x04Replay(t, true)
	off := x04Replay(t, false)

	// Non-vacuity, checked and not claimed: flip the field back and the two configurations are the
	// same value, so the toggle is the only difference between the arms.
	require.NotEqual(t,
		on.cfg.Checkpoint.Frontier.AdvanceOnSegmentClose,
		off.cfg.Checkpoint.Frontier.AdvanceOnSegmentClose,
		"the arms must actually differ in the toggle")
	normalized := off.cfg
	normalized.Checkpoint.Frontier.AdvanceOnSegmentClose = true
	require.Equal(t, on.cfg, normalized,
		"the arms must differ in the toggle ONLY; any other difference invalidates the comparison")

	// Both arms' numbers are recorded on every run, passing or failing. A comparison whose numbers
	// only appear when it fails cannot be checked by a reader afterwards.
	for _, policy := range []string{x04PolicyStock, x04PolicyRehydrate, x04PolicyL3} {
		require.Contains(t, on.metrics, policy)
		require.Contains(t, off.metrics, policy)
		t.Logf("%-18s residual_span_p50 on=%12.0f off=%12.0f   p95 on=%12.0f off=%12.0f",
			policy,
			on.metrics[policy][x04ResidualP50], off.metrics[policy][x04ResidualP50],
			on.metrics[policy][x04ResidualP95], off.metrics[policy][x04ResidualP95])
		for _, m := range x04DivergenceMetrics {
			t.Logf("%-18s %-24s on=%14.6f off=%14.6f",
				policy, m, on.metrics[policy][m], off.metrics[policy][m])
		}
	}

	require.Equal(t, string(on.canonical), string(off.canonical),
		"checkpoint.frontier.advanceOnSegmentClose is not an input to anything the replay path "+
			"computes, so both arms produce the same metrics to the byte. If this assertion has "+
			"started failing, the toggle now reaches replay and V4-VERIFY §4.4 "+
			"(TestV4_FrontierAdvancementKeepsResidualSpanODelta) can and must be authored against "+
			"whichever producer began reading it. Until then §4.4's quantity is measured by "+
			"internal/checkpoint's TestFrontierAdvanceCutsResidualSpan, over a real store, a real "+
			"SegmentLog and a real Finalize.\n"+
			"  first difference: "+firstDiffLine(on.canonical, off.canonical))
}
