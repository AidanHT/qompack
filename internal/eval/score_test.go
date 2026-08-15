package eval_test

import (
	"context"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// scoreRun builds a Run carrying exactly the per-event facts ScoreRun reads, so a scoring rule can
// be stated without going through a whole session.
type eventSpec struct {
	at        int
	demandIDs []string // one demand per entry, at the turn after `at`
	keepIDs   []string
	keepTok   core.Tokens
	p         int
	prefix    core.Tokens
	pauseMS   int
	residual  core.Tokens
	firstMS   int
}

func scoreRun(policy string, events ...eventSpec) eval.Run {
	r := eval.Run{Policy: policy, Session: "score-fixture", Branch: "compacted", Horizon: 20}
	for _, e := range events {
		demands := make([]eval.Demand, 0, len(e.demandIDs))
		for _, id := range e.demandIDs {
			demands = append(demands, eval.Demand{
				Turn: core.TurnIndex(e.at + 1), BlockID: id, Kind: eval.DemandFileContent,
			})
		}
		r.At = append(r.At, core.TurnIndex(e.at))
		r.Demands = append(r.Demands, demands)
		r.PrefixTokens = append(r.PrefixTokens, e.prefix)
		r.Keeps = append(r.Keeps, eval.KeepSet{IDs: e.keepIDs, Tokens: e.keepTok, P: e.p})
		r.PauseMS = append(r.PauseMS, e.pauseMS)
		r.ResidualSpan = append(r.ResidualSpan, e.residual)
		r.FirstTurnAfterMS = append(r.FirstTurnAfterMS, e.firstMS)
	}
	if len(r.At) > 0 {
		r.FirstCompactionTurn = r.At[0]
	}
	return r
}

// optOf builds the OPT map ScoreRun scores against.
func optOf(entries map[int][]string) map[core.TurnIndex]eval.KeepSet {
	out := make(map[core.TurnIndex]eval.KeepSet, len(entries))
	for at, ids := range entries {
		out[core.TurnIndex(at)] = eval.KeepSet{IDs: ids}
	}
	return out
}

// TestScoreRun_FractionIsMicroAveraged: a session's fraction is Σv / Σo, not the mean of the
// per-event fractions, so a large compaction event counts for more than a small one.
func TestScoreRun_FractionIsMicroAveraged(t *testing.T) {
	r := scoreRun("p",
		eventSpec{at: 10, demandIDs: []string{"a", "b", "c", "d"}, keepIDs: []string{"a", "b"}},
		eventSpec{
			at: 20, demandIDs: []string{"e", "f", "g", "h", "i", "j"},
			keepIDs: []string{"e", "f", "g", "h", "i", "j"},
		},
	)
	opt := optOf(map[int][]string{
		10: {"a", "b", "c", "d"},
		20: {"e", "f", "g", "h", "i", "j"},
	})

	got := eval.New(eval.Options{}).ScoreRun(r, opt)

	require.InDelta(t, 0.8, got.FractionOfOPT, 1e-9,
		"(2+6)/(4+6) = 0.8, not mean(0.5, 1.0) = 0.75")
}

// TestScoreRun_NoDemandsIsOne: nothing was demanded, so every keep-set is optimal. The driver
// flags such a session separately, because a corpus of them measures nothing.
func TestScoreRun_NoDemandsIsOne(t *testing.T) {
	r := scoreRun("p", eventSpec{at: 10})
	got := eval.New(eval.Options{}).ScoreRun(r, optOf(map[int][]string{10: nil}))
	require.Equal(t, 1.0, got.FractionOfOPT)
}

// TestScoreRun_ClampedToOne: a policy cannot score above the ceiling; if it appears to, the run is
// inconsistent and the number must not be published as a >1 fraction.
func TestScoreRun_ClampedToOne(t *testing.T) {
	r := scoreRun("greedy", eventSpec{
		at: 10, demandIDs: []string{"a", "b"}, keepIDs: []string{"a", "b"},
	})
	got := eval.New(eval.Options{}).ScoreRun(r, optOf(map[int][]string{10: {"a"}}))
	require.Equal(t, 1.0, got.FractionOfOPT)
}

// TestScoreRun_MissingOPTEntryIsZeroed: scoring against an OPT map that does not cover a
// compaction event is a programming error, and the honest output is nothing rather than a
// partial number that looks like a result.
func TestScoreRun_MissingOPTEntryIsZeroed(t *testing.T) {
	r := scoreRun("p", eventSpec{at: 10, demandIDs: []string{"a"}, keepIDs: []string{"a"}})

	got := eval.New(eval.Options{}).ScoreRun(r, optOf(map[int][]string{99: {"a"}}))

	require.Equal(t, eval.Score{}, got)
}

// TestScoreRun_UsesRunDemandsNotRecomputed proves ScoreRun reads Run.Demands and never reaches for
// a Session it does not have — which is the whole reason those fields were appended to Run.
func TestScoreRun_UsesRunDemandsNotRecomputed(t *testing.T) {
	r := scoreRun("p", eventSpec{
		at: 10, demandIDs: []string{"a"}, keepIDs: []string{"a"},
	})
	got := eval.New(eval.Options{}).ScoreRun(r, optOf(map[int][]string{10: {"a", "b", "c"}}))

	require.Equal(t, 1.0, got.FractionOfOPT,
		"OPT is scored against the run's own single recorded demand, not against its three IDs")
}

// TestScoreRun_RewriteTokensSection52TableA reproduces §5.2's scenario A exactly.
//
// Read that table carefully before changing this: its Rewrite column is the UNWEIGHTED span
// n − p_min, and its "30x" refers to the benefit ratio (60K dropped vs 2K dropped), not to the
// rewrite ratio, which is 157/17 ≈ 9.2x. The harness reports three separately named quantities
// so the two are never conflated again.
func TestScoreRun_RewriteTokensSection52TableA(t *testing.T) {
	cfg := config.Defaults()
	r := scoreRun("stock", eventSpec{at: 10, prefix: 167_000, p: 150_000})

	m := eval.MetricsOf(eval.New(eval.Options{Cfg: cfg}).ScoreRun(r, optOf(map[int][]string{10: nil})), cfg)

	require.InDelta(t, 17_000, m["rewrite_span_tokens"], 1e-6, "the doc's Rewrite column")
	require.InDelta(t, 21_250, m["rewrite_tokens"], 1e-6, "w = 1.25")
	require.InDelta(t, 15_300, m["forfeited_discount_tokens"], 1e-6, "(1 − r) = 0.9")
}

// TestScoreRun_RewriteTokensSection52TableB reproduces §5.2's scenario B, the one the table calls
// "30x the cost, 1/30 the benefit".
func TestScoreRun_RewriteTokensSection52TableB(t *testing.T) {
	cfg := config.Defaults()
	rA := scoreRun("stock", eventSpec{at: 10, prefix: 167_000, p: 150_000})
	rB := scoreRun("stock", eventSpec{at: 10, prefix: 167_000, p: 10_000})
	h := eval.New(eval.Options{Cfg: cfg})

	mA := eval.MetricsOf(h.ScoreRun(rA, optOf(map[int][]string{10: nil})), cfg)
	mB := eval.MetricsOf(h.ScoreRun(rB, optOf(map[int][]string{10: nil})), cfg)

	require.InDelta(t, 157_000, mB["rewrite_span_tokens"], 1e-6)
	require.InDelta(t, 196_250, mB["rewrite_tokens"], 1e-6)
	require.InDelta(t, 141_300, mB["forfeited_discount_tokens"], 1e-6)
	require.InDelta(t, 157.0/17.0, mB["rewrite_span_tokens"]/mA["rewrite_span_tokens"], 1e-6,
		"the rewrite ratio is ~9.2x; the table's 30x is the BENEFIT ratio, 60K dropped vs 2K")
}

// TestScoreRun_NoHardcodedMultiplier proves D11 compliance at runtime, not merely that the nomagic
// pass found no 1.25 in the source.
func TestScoreRun_NoHardcodedMultiplier(t *testing.T) {
	cfg := config.Defaults()
	cfg.Scheduler.Cache.WriteMultiplier = 2.0
	r := scoreRun("stock", eventSpec{at: 10, prefix: 167_000, p: 150_000})

	m := eval.MetricsOf(eval.New(eval.Options{Cfg: cfg}).ScoreRun(r, optOf(map[int][]string{10: nil})), cfg)

	require.InDelta(t, 34_000, m["rewrite_tokens"], 1e-6, "2.0 x 17_000")
	require.InDelta(t, 17_000, m["rewrite_span_tokens"], 1e-6, "the span itself is unweighted")
}

// TestScoreRun_RewriteSumsBeforeRounding: §11.2 defines the metric as a session TOTAL, so rounding
// once at the end is what keeps rewrite_span_tokens = rewrite_tokens / w exactly invertible.
func TestScoreRun_RewriteSumsBeforeRounding(t *testing.T) {
	cfg := config.Defaults()
	r := scoreRun("stock",
		eventSpec{at: 10, prefix: 1001, p: 0},
		eventSpec{at: 20, prefix: 1001, p: 0},
		eventSpec{at: 30, prefix: 1001, p: 0},
	)

	m := eval.MetricsOf(eval.New(eval.Options{Cfg: cfg}).
		ScoreRun(r, optOf(map[int][]string{10: nil, 20: nil, 30: nil})), cfg)

	require.InDelta(t, 3753.75, m["rewrite_tokens"], 0.51, "1.25 x 3003, rounded once")
	require.InDelta(t, m["rewrite_tokens"]/1.25, m["rewrite_span_tokens"], 0.5)
}

// TestScoreRun_PMinClampedToPrefix: a policy reporting a p_min past the end of the prefix would
// otherwise produce a negative rewrite span.
func TestScoreRun_PMinClampedToPrefix(t *testing.T) {
	cfg := config.Defaults()
	r := scoreRun("odd", eventSpec{at: 10, prefix: 1_000, p: 9_999})

	m := eval.MetricsOf(eval.New(eval.Options{Cfg: cfg}).
		ScoreRun(r, optOf(map[int][]string{10: nil})), cfg)

	require.Equal(t, 0.0, m["rewrite_span_tokens"])
	require.Equal(t, 0.0, m["rewrite_tokens"])
}

// TestScoreRun_Percentiles_NearestRank pins the percentile rule so two runs of the same corpus
// cannot disagree about a P95 because of an interpolation choice.
func TestScoreRun_Percentiles_NearestRank(t *testing.T) {
	r := scoreRun("p",
		eventSpec{at: 10, pauseMS: 30}, eventSpec{at: 20, pauseMS: 10},
		eventSpec{at: 30, pauseMS: 40}, eventSpec{at: 40, pauseMS: 20},
	)
	opt := optOf(map[int][]string{10: nil, 20: nil, 30: nil, 40: nil})

	got := eval.New(eval.Options{}).ScoreRun(r, opt).CompactionPauseMS

	require.Equal(t, eval.Percentiles{P50: 20, P95: 40, P99: 40, Max: 40}, got)
}

// TestScoreRun_PercentilesEmpty: a session that never compacted has no latency samples, and zeros
// are the right answer rather than a panic.
func TestScoreRun_PercentilesEmpty(t *testing.T) {
	got := eval.New(eval.Options{}).ScoreRun(eval.Run{Policy: "p"}, nil)

	require.Equal(t, eval.Percentiles{}, got.CompactionPauseMS)
	require.Equal(t, eval.Percentiles{}, got.ResidualSpan)
	require.Equal(t, eval.Percentiles{}, got.FirstTurnAfterMS)
}

// TestScoreRun_RehydrationTokensSummed is §11.2's "tokens spent restoring context".
func TestScoreRun_RehydrationTokensSummed(t *testing.T) {
	r := scoreRun("p",
		eventSpec{at: 10, keepTok: 12_000},
		eventSpec{at: 20, keepTok: 9_500},
	)
	got := eval.New(eval.Options{}).ScoreRun(r, optOf(map[int][]string{10: nil, 20: nil}))
	require.Equal(t, core.Tokens(21_500), got.RehydrationTokens)
}

// TestScoreRun_RetrievalHitRateZeroActions: no retrieval calls means no hit rate, and the driver
// reports retrieval_actions: 0 alongside it so a 0.0 is never mistaken for a failure.
func TestScoreRun_RetrievalHitRateZeroActions(t *testing.T) {
	got := eval.New(eval.Options{}).ScoreRun(scoreRun("p", eventSpec{at: 10}),
		optOf(map[int][]string{10: nil}))
	require.Equal(t, 0.0, got.RetrievalHitRate)
}

// TestScoreRun_RetrievalHitRate counts a retrieval as a hit when it prevented a re-read: the path
// was already known before the compaction, and no plain read of it follows.
func TestScoreRun_RetrievalHitRate(t *testing.T) {
	r := scoreRun("p", eventSpec{at: 2})
	r.Actions = []eval.Action{
		{Turn: 0, Tool: "FileRead", Paths: []string{"src/a.go"}},
		{Turn: 1, Tool: "FileRead", Paths: []string{"src/b.go"}},
		{Turn: 3, Tool: "expand", Paths: []string{"src/a.go"}},  // hit: no read follows
		{Turn: 4, Tool: "re_read", Paths: []string{"src/b.go"}}, // miss: read follows
		{Turn: 5, Tool: "FileRead", Paths: []string{"src/b.go"}},
	}

	got := eval.New(eval.Options{}).ScoreRun(r, optOf(map[int][]string{2: nil}))

	require.InDelta(t, 0.5, got.RetrievalHitRate, 1e-9)
	require.Equal(t, 2, eval.RetrievalActions(r))
}

// TestMetricsOf_CoversEveryDirection is an exact set equality in both directions: a new metric
// cannot land without a direction, and a direction cannot linger for a metric that is gone.
func TestMetricsOf_CoversEveryDirection(t *testing.T) {
	cfg := config.Defaults()
	m := eval.MetricsOf(eval.Score{}, cfg)

	require.Len(t, m, 19, "the baseline file asserts exactly this many keys per policy")
	for name := range m {
		require.Contains(t, eval.MetricNames(), name)
	}
	for _, name := range eval.MetricNames() {
		require.Contains(t, m, name, "%s has a direction but is not a MetricsOf key", name)
	}
}

// TestMetricDirection_Split pins which way each metric is better; getting one backwards would make
// the 2% gate celebrate a regression.
func TestMetricDirection_Split(t *testing.T) {
	higher := []string{
		"fraction_of_opt", "first_divergence_turn", "file_set_jaccard",
		"same_decision", "decision_preservation", "retrieval_hit_rate",
	}
	for _, name := range higher {
		require.Equal(t, eval.DirHigherBetter, eval.MetricDirection(name), name)
	}
	lower := []string{
		"tool_edit_distance", "redundant_reads", "re_attempts", "rewrite_span_tokens",
		"rewrite_tokens", "forfeited_discount_tokens", "rehydration_tokens",
		"compaction_pause_ms_p50", "compaction_pause_ms_p95", "residual_span_p50",
		"residual_span_p95", "first_turn_after_ms_p50", "first_turn_after_ms_p95",
	}
	for _, name := range lower {
		require.Equal(t, eval.DirLowerBetter, eval.MetricDirection(name), name)
	}
	require.Len(t, append(higher, lower...), 19)
}

// TestMetricsOf_RoundedToSixDecimals keeps a baseline file byte-identical across runs.
func TestMetricsOf_RoundedToSixDecimals(t *testing.T) {
	cfg := config.Defaults()
	s := eval.Score{FractionOfOPT: 1.0 / 3.0}
	require.Equal(t, 0.333333, eval.MetricsOf(s, cfg)["fraction_of_opt"])
}

// ── Report ──────────────────────────────────────────────────────────────────────────────────

// TestReport_GeneratedAtUsesClock: the report's timestamp comes from the injected clock, so a
// baseline written twice is byte-identical.
func TestReport_GeneratedAtUsesClock(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	h := eval.New(eval.Options{Clock: testutil.NewFakeClock(at)})
	_ = h.ScoreRun(scoreRun("stock", eventSpec{at: 10}), optOf(map[int][]string{10: nil}))

	rep, err := h.Report(context.Background(), map[string][]eval.Score{"stock": {{}}})
	require.NoError(t, err)
	require.Equal(t, at, rep.GeneratedAt)
}

// TestReport_PercentilesRecomputedNotAveraged is the reason ScoreRun pools raw samples at all: an
// average of P95s is not a P95.
func TestReport_PercentilesRecomputedNotAveraged(t *testing.T) {
	h := eval.New(eval.Options{})
	low := scoreRun("stock",
		eventSpec{at: 10, pauseMS: 1}, eventSpec{at: 20, pauseMS: 2},
		eventSpec{at: 30, pauseMS: 3}, eventSpec{at: 40, pauseMS: 4})
	high := scoreRun("stock",
		eventSpec{at: 10, pauseMS: 100}, eventSpec{at: 20, pauseMS: 200},
		eventSpec{at: 30, pauseMS: 300}, eventSpec{at: 40, pauseMS: 400})
	opt := optOf(map[int][]string{10: nil, 20: nil, 30: nil, 40: nil})

	sLow := h.ScoreRun(low, opt)
	sHigh := h.ScoreRun(high, opt)

	rep, err := h.Report(context.Background(), map[string][]eval.Score{"stock": {sLow, sHigh}})
	require.NoError(t, err)

	// Pooled and sorted: 1 2 3 4 100 200 300 400. Nearest rank at 0.95 -> ceil(7.6)-1 = 7 -> 400.
	require.Equal(t, 400.0, rep.Policies["stock"].CompactionPauseMS.P95)
	meanOfP95s := (sLow.CompactionPauseMS.P95 + sHigh.CompactionPauseMS.P95) / 2
	require.NotEqual(t, meanOfP95s, rep.Policies["stock"].CompactionPauseMS.P95)
}

// TestReport_UnequalSessionCountsError: an unequal corpus across policies makes every comparison
// between them meaningless, so it fails rather than reporting a lopsided table.
func TestReport_UnequalSessionCountsError(t *testing.T) {
	h := eval.New(eval.Options{})
	opt := optOf(map[int][]string{10: nil})
	stock := h.ScoreRun(scoreRun("stock", eventSpec{at: 10}), opt)
	null := h.ScoreRun(scoreRun("null", eventSpec{at: 10}), opt)

	_, err := h.Report(context.Background(), map[string][]eval.Score{
		"stock": {stock, stock},
		"null":  {null},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "null")
}

// TestReport_MissingPoolIsAnError: a caller that never called ScoreRun gets a failure rather than
// a report full of silent zeros.
func TestReport_MissingPoolIsAnError(t *testing.T) {
	_, err := eval.New(eval.Options{}).
		Report(context.Background(), map[string][]eval.Score{"stock": {{}}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "stock")
}

// TestReport_EmptyScoresIsNotAnError: evaltest's shape block calls Report(ctx, nil), and nothing
// to aggregate is a legitimate answer.
func TestReport_EmptyScoresIsNotAnError(t *testing.T) {
	rep, err := eval.New(eval.Options{}).Report(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, "stock", rep.Baseline)
	require.Equal(t, 0, rep.Sessions)
	require.Empty(t, rep.Regressions, "filling Regressions needs a previous report, which eval never has")
}

// TestReport_CountMetricsSumRatioMetricsAverage pins the two aggregation rules.
func TestReport_CountMetricsSumRatioMetricsAverage(t *testing.T) {
	h := eval.New(eval.Options{})
	opt := optOf(map[int][]string{10: {"a", "b"}})
	one := h.ScoreRun(scoreRun("stock",
		eventSpec{at: 10, demandIDs: []string{"a", "b"}, keepIDs: []string{"a"}, prefix: 1000}), opt)
	two := h.ScoreRun(scoreRun("stock",
		eventSpec{at: 10, demandIDs: []string{"a", "b"}, keepIDs: []string{"a", "b"}, prefix: 1000}), opt)

	rep, err := h.Report(context.Background(), map[string][]eval.Score{"stock": {one, two}})
	require.NoError(t, err)

	agg := rep.Policies["stock"]
	require.InDelta(t, 0.75, agg.FractionOfOPT, 1e-9, "mean(0.5, 1.0)")
	require.Equal(t, one.RewriteTokens+two.RewriteTokens, agg.RewriteTokens, "counts sum")
	require.Equal(t, 2, rep.Sessions)
}

// TestReport_ScoreRunIsPureAcrossHarnesses: a fresh harness starts with an empty pool, which is
// what makes Report a deterministic function of the ScoreRun calls that preceded it.
func TestReport_ScoreRunIsPureAcrossHarnesses(t *testing.T) {
	opt := optOf(map[int][]string{10: nil})
	r := scoreRun("stock", eventSpec{at: 10, pauseMS: 50})

	first := eval.New(eval.Options{})
	_ = first.ScoreRun(r, opt)
	repA, err := first.Report(context.Background(), map[string][]eval.Score{"stock": {{}}})
	require.NoError(t, err)

	second := eval.New(eval.Options{})
	_ = second.ScoreRun(r, opt)
	repB, err := second.Report(context.Background(), map[string][]eval.Score{"stock": {{}}})
	require.NoError(t, err)

	require.Equal(t, repA.Policies["stock"].CompactionPauseMS, repB.Policies["stock"].CompactionPauseMS)
}
