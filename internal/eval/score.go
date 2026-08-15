package eval

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// baselinePolicy is the policy every Regression is measured against, and the policy the Phase 0
// number describes.
const baselinePolicy = "stock"

// retrievalTools are the L6 calls whose hit rate §11.2 asks for: "how often expand/re_read is
// called, and whether it prevented a re-read".
var retrievalTools = map[string]bool{
	"recall": true, "expand": true, "re_read": true, "already_tried": true,
}

// retrievalLookahead is how far after a retrieval a plain read of the same path still counts as
// the retrieval having failed to prevent it.
const retrievalLookahead = 5

// metricRounding keeps a baseline file byte-identical across runs and platforms.
const metricRounding = 1e6

// ScoreRun scores one completed Run against the Belady-optimal keep-set for each of its
// compaction events.
//
// It is a pure function of (r, opt) plus the harness's own config and logger: every quantity it
// needs about the session travels inside r (At, Demands, PrefixTokens, Horizon), which is exactly
// why those fields exist. Its one side effect is pooling the run's raw latency samples, so Report
// can recompute percentiles over the whole corpus rather than averaging per-session percentiles.
func (h *harness) ScoreRun(r Run, opt map[core.TurnIndex]KeepSet) Score {
	defer h.observe("eval.score.ms", h.clock.Now())

	var satisfied, optimal int
	var rewriteSpan int
	var rehydration core.Tokens

	for i, at := range r.At {
		best, ok := opt[at]
		if !ok {
			// A missing OPT entry means the caller scored against a map that does not cover this
			// run. A partial number would look like a result, so there is none.
			h.log.Loud("eval: no OPT keep-set for a compaction event; run not scored",
				"policy", r.Policy, "session", r.Session, "turn", int(at))
			return Score{}
		}
		demands := demandsAt(r, i)
		keep := keepAt(r, i)

		satisfied += valueOf(keep, demands)
		optimal += valueOf(best, demands)
		rehydration += keep.Tokens

		n := int(prefixAt(r, i))
		rewriteSpan += n - min(max(keep.P, 0), n)
	}

	if over := budgetOverrun(r); over > 0 {
		// A policy that cheats on the budget is not comparable to one that does not, so the
		// overrun is stated rather than absorbed into a slightly better fraction.
		h.log.Loud("eval: policy keep-set exceeded the budget",
			"policy", r.Policy, "session", r.Session, "overrunTokens", int(over))
	}

	fraction := 1.0
	if optimal > 0 {
		fraction = min(max(float64(satisfied)/float64(optimal), 0), 1)
	}

	// §11.2 defines rewrite tokens as a session TOTAL, so the sum is taken first and rounded once.
	// Rounding per event and summing would leave rewrite_span_tokens = rewrite_tokens / w off by
	// up to half a token per event, and a derived metric that does not invert cleanly is one
	// people stop trusting.
	w := h.cfg.Scheduler.Cache.WriteMultiplier

	h.poolSamples(r)

	return Score{
		FractionOfOPT:     fraction,
		RewriteTokens:     int(math.Round(w * float64(rewriteSpan))),
		RehydrationTokens: rehydration,
		RetrievalHitRate:  retrievalHitRate(r),
		CompactionPauseMS: percentilesOfInts(r.PauseMS),
		ResidualSpan:      percentilesOfTokens(r.ResidualSpan),
		FirstTurnAfterMS:  percentilesOfInts(r.FirstTurnAfterMS),
	}
}

// demandsAt, keepAt and prefixAt read the parallel per-event slices defensively: a hand-built Run
// in a test may be shorter than r.At, and a panic there would say nothing useful.
func demandsAt(r Run, i int) []Demand {
	if i < len(r.Demands) {
		return r.Demands[i]
	}
	return nil
}

func keepAt(r Run, i int) KeepSet {
	if i < len(r.Keeps) {
		return r.Keeps[i]
	}
	return KeepSet{}
}

func prefixAt(r Run, i int) core.Tokens {
	if i < len(r.PrefixTokens) {
		return r.PrefixTokens[i]
	}
	return 0
}

// valueOf counts how many of these demands a keep-set satisfies. Both the policy's keep-set and
// OPT's are always scored against the SAME demand set — the one recorded on the run — so the two
// sides can never be graded on different questions.
func valueOf(ks KeepSet, demands []Demand) int {
	if len(demands) == 0 || len(ks.IDs) == 0 {
		return 0
	}
	kept := make(map[string]bool, len(ks.IDs))
	for _, id := range ks.IDs {
		kept[id] = true
	}
	n := 0
	for _, d := range demands {
		if kept[d.BlockID] {
			n++
		}
	}
	return n
}

// budgetOverrun reports how far the largest keep-set exceeded the budget the run was made under.
// It is derived from the run's own residual bookkeeping rather than from a budget the Score does
// not carry, so it only ever reports an overrun it can actually see.
func budgetOverrun(r Run) core.Tokens {
	var worst core.Tokens
	for _, k := range r.Keeps {
		if over := k.Tokens - DefaultKeepBudget; over > worst {
			worst = over
		}
	}
	return worst
}

// RetrievalActions counts the retrieval calls a run made. The driver reports it alongside the hit
// rate so a rate of 0.0 is never mistaken for a failure when the real answer is "never called".
func RetrievalActions(r Run) int {
	n := 0
	for _, a := range r.Actions {
		if retrievalTools[a.Tool] {
			n++
		}
	}
	return n
}

// retrievalHitRate is §11.2's "how often expand/re_read is called, and whether it prevented a
// re-read": a retrieval hits when its path was already known before the compaction and no plain
// read of that path follows within the lookahead.
func retrievalHitRate(r Run) float64 {
	total, hits := 0, 0
	for i, a := range r.Actions {
		if !retrievalTools[a.Tool] {
			continue
		}
		total++
		if retrievalPrevented(r.Actions, i, a.Paths) {
			hits++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}

// retrievalPrevented reports whether no plain read of any of paths follows within the lookahead.
func retrievalPrevented(actions []Action, from int, paths []string) bool {
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	for j := from + 1; j < len(actions) && j <= from+retrievalLookahead; j++ {
		if actions[j].Tool != toolFileRead && actions[j].Tool != toolRead {
			continue
		}
		for _, p := range actions[j].Paths {
			if want[p] {
				return false
			}
		}
	}
	return true
}

// poolSamples appends this run's raw latency samples to its policy's pool.
func (h *harness) poolSamples(r Run) {
	if r.Policy == "" {
		return
	}
	p := h.pool[r.Policy]
	for _, v := range r.PauseMS {
		p.pause = append(p.pause, float64(v))
	}
	for _, v := range r.ResidualSpan {
		p.residual = append(p.residual, float64(v))
	}
	for _, v := range r.FirstTurnAfterMS {
		p.firstTurn = append(p.firstTurn, float64(v))
	}
	h.pool[r.Policy] = p
}

// percentilesOfInts and percentilesOfTokens reduce a raw sample slice.
func percentilesOfInts(v []int) Percentiles {
	f := make([]float64, len(v))
	for i, x := range v {
		f[i] = float64(x)
	}
	return percentilesOf(f)
}

func percentilesOfTokens(v []core.Tokens) Percentiles {
	f := make([]float64, len(v))
	for i, x := range v {
		f[i] = float64(x)
	}
	return percentilesOf(f)
}

// percentilesOf is nearest-rank: idx = ceil(q·N) − 1, clamped. Nearest rank rather than
// interpolation so two runs of the same corpus can never disagree about a P95 because of a
// rounding choice.
func percentilesOf(v []float64) Percentiles {
	if len(v) == 0 {
		return Percentiles{}
	}
	s := make([]float64, len(v))
	copy(s, v)
	sort.Float64s(s)
	at := func(q float64) float64 {
		idx := int(math.Ceil(q*float64(len(s)))) - 1
		return s[min(max(idx, 0), len(s)-1)]
	}
	return Percentiles{P50: at(0.50), P95: at(0.95), P99: at(0.99), Max: s[len(s)-1]}
}

// Report aggregates per-policy scores across sessions.
//
// Ratio metrics are averaged and count metrics are summed. The three latency distributions are
// NOT averaged: an average of P95s is not a P95, so they are recomputed from the raw samples
// ScoreRun pooled. Regressions is always left empty — comparison needs a previous report, which
// eval is never given, so filling it is the gate's job.
func (h *harness) Report(ctx context.Context, scores map[string][]Score) (Report, error) {
	defer h.observe("eval.report.ms", h.clock.Now())
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}

	rep := Report{
		Policies:    make(map[string]Score, len(scores)),
		Baseline:    baselinePolicy,
		Sessions:    len(scores[baselinePolicy]),
		GeneratedAt: h.clock.Now().UTC(),
	}

	names := make([]string, 0, len(scores))
	for name := range scores {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		per := scores[name]
		if len(per) != rep.Sessions {
			return Report{}, fmt.Errorf(
				"eval: policy %q was scored over %d sessions but the %q baseline over %d; "+
					"an unequal corpus makes every comparison between them meaningless",
				name, len(per), baselinePolicy, rep.Sessions)
		}
		pool, ok := h.pool[name]
		if !ok {
			return Report{}, fmt.Errorf(
				"eval: policy %q appears in the report with no pooled samples; "+
					"ScoreRun must be called for it before Report: %w", name, core.ErrNotFound)
		}
		rep.Policies[name] = aggregate(per, pool)
	}
	return rep, nil
}

// aggregate reduces one policy's per-session scores, recomputing the latency percentiles from the
// pooled raw samples rather than from the already-reduced per-session ones.
func aggregate(per []Score, pool latencyPool) Score {
	out := Score{
		CompactionPauseMS: percentilesOf(pool.pause),
		ResidualSpan:      percentilesOf(pool.residual),
		FirstTurnAfterMS:  percentilesOf(pool.firstTurn),
	}
	if len(per) == 0 {
		return out
	}

	var fraction, jaccard, preservation, hitRate, sameDecision, firstDivergence float64
	for _, s := range per {
		fraction += s.FractionOfOPT
		jaccard += s.Divergence.FileSetJaccard
		preservation += s.Divergence.DecisionPreservation
		hitRate += s.RetrievalHitRate
		firstDivergence += float64(s.Divergence.FirstDivergenceTurn)
		if s.Divergence.SameDecision {
			sameDecision++
		}
		out.RewriteTokens += s.RewriteTokens
		out.RehydrationTokens += s.RehydrationTokens
		out.Divergence.ToolEditDistance += s.Divergence.ToolEditDistance
		out.Divergence.RedundantReads += s.Divergence.RedundantReads
		out.Divergence.ReAttempts += s.Divergence.ReAttempts
	}
	n := float64(len(per))

	out.FractionOfOPT = fraction / n
	out.RetrievalHitRate = hitRate / n
	out.Divergence.FileSetJaccard = jaccard / n
	out.Divergence.DecisionPreservation = preservation / n
	// FirstDivergenceTurn and SameDecision are the two aggregates §5.18's field types cannot carry
	// at full resolution: one is an int and one is a bool. The rounded mean and the majority
	// verdict are the honest reductions, and the finer-grained companion of SameDecision —
	// DecisionPreservation — is a float and carries the fraction exactly.
	out.Divergence.FirstDivergenceTurn = int(math.Round(firstDivergence / n))
	out.Divergence.SameDecision = sameDecision/n >= 0.5
	return out
}

// metricOrder is the canonical metric list, and the single source of truth the direction table and
// the baseline file's key count are both checked against.
var metricOrder = []string{
	"fraction_of_opt",
	"first_divergence_turn",
	"file_set_jaccard",
	"tool_edit_distance",
	"same_decision",
	"decision_preservation",
	"redundant_reads",
	"re_attempts",
	"rewrite_span_tokens",
	"rewrite_tokens",
	"forfeited_discount_tokens",
	"rehydration_tokens",
	"retrieval_hit_rate",
	"compaction_pause_ms_p50",
	"compaction_pause_ms_p95",
	"residual_span_p50",
	"residual_span_p95",
	"first_turn_after_ms_p50",
	"first_turn_after_ms_p95",
}

// higherIsBetter names the six metrics a policy wants to maximize. Everything else in metricOrder
// it wants to minimize.
var higherIsBetter = map[string]bool{
	"fraction_of_opt":       true,
	"first_divergence_turn": true,
	"file_set_jaccard":      true,
	"same_decision":         true,
	"decision_preservation": true,
	"retrieval_hit_rate":    true,
}

// MetricNames returns the canonical metric list in report order.
func MetricNames() []string {
	out := make([]string, len(metricOrder))
	copy(out, metricOrder)
	return out
}

// MetricDirection reports which way a metric is better.
func MetricDirection(metric string) Direction {
	if higherIsBetter[metric] {
		return DirHigherBetter
	}
	return DirLowerBetter
}

// MetricsOf flattens a Score into the canonical metric map, rounded to six decimals.
//
// It takes the config because rewrite_span_tokens and forfeited_discount_tokens are derived from
// Score.RewriteTokens using w and r, which are config keys and never literals (D11, §11.6).
// Reporting all three separately is deliberate: §5.2's Rewrite column is the unweighted span, the
// weighted cost is w times that, and the forfeited discount is a third quantity again. Collapsing
// them is how they got conflated in the first place.
func MetricsOf(s Score, cfg config.Config) map[string]float64 {
	w := cfg.Scheduler.Cache.WriteMultiplier
	r := cfg.Scheduler.Cache.ReadMultiplier

	span := 0.0
	if w != 0 {
		span = float64(s.RewriteTokens) / w
	}

	sameDecision := 0.0
	if s.Divergence.SameDecision {
		sameDecision = 1
	}

	out := map[string]float64{
		"fraction_of_opt":           s.FractionOfOPT,
		"first_divergence_turn":     float64(s.Divergence.FirstDivergenceTurn),
		"file_set_jaccard":          s.Divergence.FileSetJaccard,
		"tool_edit_distance":        float64(s.Divergence.ToolEditDistance),
		"same_decision":             sameDecision,
		"decision_preservation":     s.Divergence.DecisionPreservation,
		"redundant_reads":           float64(s.Divergence.RedundantReads),
		"re_attempts":               float64(s.Divergence.ReAttempts),
		"rewrite_span_tokens":       span,
		"rewrite_tokens":            float64(s.RewriteTokens),
		"forfeited_discount_tokens": span * (1 - r),
		"rehydration_tokens":        float64(s.RehydrationTokens),
		"retrieval_hit_rate":        s.RetrievalHitRate,
		"compaction_pause_ms_p50":   s.CompactionPauseMS.P50,
		"compaction_pause_ms_p95":   s.CompactionPauseMS.P95,
		"residual_span_p50":         s.ResidualSpan.P50,
		"residual_span_p95":         s.ResidualSpan.P95,
		"first_turn_after_ms_p50":   s.FirstTurnAfterMS.P50,
		"first_turn_after_ms_p95":   s.FirstTurnAfterMS.P95,
	}
	for k, v := range out {
		out[k] = math.Round(v*metricRounding) / metricRounding
	}
	return out
}
