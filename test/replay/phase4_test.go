package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/test/replay/l3policy"
	"github.com/stretchr/testify/require"
)

// This file operationalizes Qompack.md §10 Phase 4's exit criterion — "measured reduction in total
// rewrite tokens per session, with no regression in divergence metrics; median compaction pause
// and residual span flat as session length grows" — against the committed 24-session synthetic
// corpus, replayed deterministically (Seed 1) under SP-02's stock baseline and SP-12's qompack-l3.
//
// ── WHAT THESE ROWS PROVE, AND WHAT THEY DO NOT ────────────────────────────────────────────
//
// They bound n − p: how late the policy places the cut. ResidualSpan in the harness is
// prefixTokens − KeepSet.P, computed inside Replay from the one number a policy returns, and
// RewriteTokens is w times the same span (TestPhase4_ResidualSpanIsRewriteSpan asserts the
// identity). The rewrite row and the two residual rows are therefore ONE measurement of cut
// placement wearing three hats, not three independent gates. They do not exercise O5: l3policy
// has no store, no SegmentLog, no checkpoint.Writer and no idle worker (it may not import
// internal/daemon), so frontier advancement is not in this replay path in any form.
//
// The two residual rows are scoped to the quantity the policy controls (ruling R47). Per
// compaction point, achievable_i = n_i − Pos(latest eligible candidate before the point) —
// eligible being §8.4's changepoint ∩ round-boundary set, and achievable_i = n_i when there is
// none — and slack_i = ResidualSpan_i − achievable_i is how much later than the latest boundary
// the policy could have cut but did not. The absolute bars (P95 ≤ 20 000, slope ≤ 0.02
// tokens/turn) are recorded in phase4-rewrite.json and logged, not enforced here: they are the
// frontier's to meet, and the §10 Phase 4 amortization clause is discharged by V4-VERIFY §4.4's
// live TestV4_FrontierAdvancementKeepsResidualSpanODelta and its advanceOnSegmentClose = false
// control.
//
// The compaction pause is MODELLED, never measured: deterministic replay makes no model call.
// Both files read the coefficients from eval.DefaultLatencyModel(); neither restates them.

const (
	p4PolicyStock = "stock"
	p4PolicyL3    = "qompack-l3"

	// p4RewriteRatioMax is §10 Phase 4's "measured reduction", operationalized by the plan as
	// Σ RewriteTokens[qompack-l3] ≤ 0.80 × Σ RewriteTokens[stock].
	p4RewriteRatioMax = 0.80
	// p4SlopeMax bounds the least-squares slope of median slack against turn count.
	p4SlopeMax = 0.02
	// p4QuartileRatioMax bounds the longest-quartile median against the shortest-quartile median.
	p4QuartileRatioMax = 1.25
	// p4QuartileCount is how many quartiles a corpus is cut into.
	p4QuartileCount = 4

	// p4ResidualP95Bar and p4ResidualSlopeBar are the plan's absolute residual bars, recorded
	// for the frontier's live test and never enforced by replay.
	p4ResidualP95Bar   = 20000
	p4ResidualSlopeBar = 0.02
	p4DischargedBy     = "V4-VERIFY §4.4 TestV4_FrontierAdvancementKeepsResidualSpanODelta"

	// p4SignOffEnv names a file holding the pull-request body. The §11.3 2% rule is waived for a
	// metric only by a `sign-off:` trailer there, exactly as the replay-gate reads --signoff.
	p4SignOffEnv = "QOMPACK_PHASE4_SIGNOFF"

	// p4ForceFiveMinuteEnv is the one host variable deterministic replay sets, so the test's own
	// regime is the policy's: the KNOWN five-minute one.
	p4ForceFiveMinuteEnv = "FORCE_PROMPT_CACHING_5M"

	// tokensPerK converts a residual span into the thousands the latency model is stated in.
	tokensPerK = 1000.0
)

// p4DivergenceMetrics are the five §11.3 divergence rows, by their eval.MetricsOf keys.
var p4DivergenceMetrics = []string{
	"first_divergence_turn",
	"file_set_jaccard",
	"decision_preservation",
	"redundant_reads",
	"re_attempts",
}

// p4Session is one corpus session replayed under both policies.
type p4Session struct {
	Session eval.Session
	Turns   int
	Runs    map[string]eval.Run
	Scores  map[string]eval.Score
}

// p4Corpus is the whole replay, computed once per test binary: the Belady ceiling per session is
// the expensive half, and seven tests reading one replay is the point of the identity row.
type p4Corpus struct {
	cfg      config.Config
	sessions []p4Session
	report   eval.Report
	err      error
}

var (
	p4Once  sync.Once
	p4Cache *p4Corpus
)

// phase4Corpus returns the shared replay, failing the calling test if it could not be built.
func phase4Corpus(t *testing.T) *p4Corpus {
	t.Helper()
	p4Once.Do(func() { p4Cache = replayPhase4() })
	require.NoError(t, p4Cache.err)
	require.Len(t, p4Cache.sessions, p4Cache.report.Sessions)
	return p4Cache
}

// replayPhase4 replays the corpus under stock and qompack-l3 exactly as the driver's replayCorpus
// does: one Belady ceiling per session, Divergence composed from Compare, Report from the pooled
// samples. It returns errors rather than failing a test so that whichever test runs first does
// not own the failure.
func replayPhase4() *p4Corpus {
	ctx := context.Background()
	cfg := config.Defaults()
	c := &p4Corpus{cfg: cfg}

	root, err := repoRoot()
	if err != nil {
		c.err = err
		return c
	}
	h := eval.New(eval.Options{Cfg: cfg, Log: logging.Nop()})
	sessions, err := h.Load(filepath.Join(root, "testdata", "sessions", "synthetic"))
	if err != nil {
		c.err = err
		return c
	}
	stock, ok := eval.PolicyByName(p4PolicyStock, cfg)
	if !ok {
		c.err = fmt.Errorf("phase4: no policy named %q", p4PolicyStock)
		return c
	}
	policies := []eval.Policy{stock, l3policy.New(cfg)}
	opts := eval.ReplayOptions{Deterministic: true, Seed: 1}

	scores := map[string][]eval.Score{}
	for _, s := range sessions {
		opt, _, _, err := optimalKeepSets(ctx, s)
		if err != nil {
			c.err = err
			return c
		}
		baseline := eval.BaselineRun(s)
		ps := p4Session{
			Session: s, Turns: len(s.Turns),
			Runs: map[string]eval.Run{}, Scores: map[string]eval.Score{},
		}
		for _, p := range policies {
			r, err := h.Replay(ctx, s, p, opts)
			if err != nil {
				c.err = err
				return c
			}
			sc := h.ScoreRun(r, opt)
			sc.Divergence = h.Compare(baseline, r)
			ps.Runs[p.Name()] = r
			ps.Scores[p.Name()] = sc
			scores[p.Name()] = append(scores[p.Name()], sc)
		}
		c.sessions = append(c.sessions, ps)
	}
	c.report, c.err = h.Report(ctx, scores)
	return c
}

// writePhase4Artifact writes one JSON artifact under the test project's .qompack/eval/ — never
// under the repository — and returns its path.
func writePhase4Artifact(t *testing.T, name string, v any) string {
	t.Helper()
	p := testutil.NewProject(t)
	dir := filepath.Join(p.Root, ".qompack", "eval")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	body, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, append(body, '\n'), 0o600))
	t.Logf("wrote %s:\n%s", path, body)
	return path
}

// ── the slack of a cut ──────────────────────────────────────────────────────────────────────

// p4Point is one compaction point of a qompack-l3 run with the R47 quantities attached.
type p4Point struct {
	At         core.TurnIndex
	N          int
	P          int
	Residual   int
	Achievable int
	Slack      int
	Candidates []scheduler.Candidate
	TTL        scheduler.TTLState
}

// p4Regime is the regime both the policy and the test reason under: the ladder run with
// FORCE_PROMPT_CACHING_5M alone.
func p4Regime(cfg config.Config) scheduler.CacheRegime {
	getenv := func(name string) string {
		if name == p4ForceFiveMinuteEnv {
			return "1"
		}
		return ""
	}
	return scheduler.ResolveCacheRegime(getenv, cfg.Scheduler, "", false,
		cfg.Runtime.Scheduler.Cache.AssumeMaxTTLSeconds)
}

// p4Points computes achievable_i and slack_i for every compaction point of the session's
// qompack-l3 run, from the candidate set the policy exposes rather than from anything it chose.
func p4Points(cfg config.Config, s p4Session) []p4Point {
	r := s.Runs[p4PolicyL3]
	reg := p4Regime(cfg)
	out := make([]p4Point, 0, len(r.At))
	for i, at := range r.At {
		n := int(r.PrefixTokens[i])
		cands := l3policy.Candidates(cfg, s.Session, at)
		achievable := n
		for _, c := range cands {
			achievable = min(achievable, n-c.Pos)
		}
		pt := p4Point{
			At: at, N: n, P: r.Keeps[i].P, Residual: int(r.ResidualSpan[i]),
			Achievable: achievable, Candidates: cands,
		}
		pt.Slack = pt.Residual - achievable
		var now, prev core.UnixMilli
		if int(at) < len(s.Session.Turns) {
			now = s.Session.Turns[at].TS
		}
		if at > 0 {
			prev = s.Session.Turns[at-1].TS
		}
		pt.TTL, _ = scheduler.ClassifyTTL(now, prev, reg, false)
		out = append(out, pt)
	}
	return out
}

// p4MedianSlack is the nearest-rank median of a session's slacks.
func p4MedianSlack(pts []p4Point) float64 {
	v := make([]float64, len(pts))
	for i, p := range pts {
		v[i] = float64(p.Slack)
	}
	return medianOf(v)
}

// ── the artifact ────────────────────────────────────────────────────────────────────────────

// p4RewriteRow is one line of the per-session table the PR comment carries.
type p4RewriteRow struct {
	Session       string  `json:"session"`
	Turns         int     `json:"turns"`
	Stock         int     `json:"stockRewriteTokens"`
	L3            int     `json:"qompackL3RewriteTokens"`
	Ratio         float64 `json:"ratio"`
	MedianResid   float64 `json:"qompackL3MedianResidual"`
	MedianSlack   float64 `json:"qompackL3MedianSlack"`
	ResidualSpans []int   `json:"qompackL3ResidualSpans"`
}

type p4RewriteReport struct {
	Baseline         string         `json:"baseline"`
	Policy           string         `json:"policy"`
	StockTotal       int            `json:"stockTotal"`
	L3Total          int            `json:"qompackL3Total"`
	Ratio            float64        `json:"ratio"`
	MaxRatio         float64        `json:"maxRatio"`
	Sessions         int            `json:"sessions"`
	ResidualP95      float64        `json:"residual_p95"`
	ResidualP95Bar   float64        `json:"residual_p95_bar"`
	ResidualSlope    float64        `json:"residual_slope"`
	ResidualSlopeBar float64        `json:"residual_slope_bar"`
	SlackSlope       float64        `json:"slack_slope"`
	DischargedBy     string         `json:"discharged_by"`
	PerSession       []p4RewriteRow `json:"perSession"`
}

// phase4RewriteReport assembles the one artifact all three cut-placement rows write: the
// per-session rewrite table, the corpus residual P95 and slope against their plan bars, and
// the slack slope replay does enforce.
func phase4RewriteReport(c *p4Corpus) p4RewriteReport {
	rep := p4RewriteReport{
		Baseline: p4PolicyStock, Policy: p4PolicyL3, MaxRatio: p4RewriteRatioMax,
		Sessions:       len(c.sessions),
		ResidualP95:    c.report.Policies[p4PolicyL3].ResidualSpan.P95,
		ResidualP95Bar: p4ResidualP95Bar, ResidualSlopeBar: p4ResidualSlopeBar,
		DischargedBy: p4DischargedBy,
	}
	turns := make([]float64, 0, len(c.sessions))
	residuals := make([]float64, 0, len(c.sessions))
	slacks := make([]float64, 0, len(c.sessions))
	for _, s := range c.sessions {
		st, l3 := s.Scores[p4PolicyStock].RewriteTokens, s.Scores[p4PolicyL3].RewriteTokens
		rep.StockTotal += st
		rep.L3Total += l3
		ratio := math.NaN()
		if st > 0 {
			ratio = float64(l3) / float64(st)
		}
		pts := p4Points(c.cfg, s)
		row := p4RewriteRow{
			Session: s.Session.ID, Turns: s.Turns, Stock: st, L3: l3, Ratio: ratio,
			MedianResid: s.Scores[p4PolicyL3].ResidualSpan.P50, MedianSlack: p4MedianSlack(pts),
		}
		for _, p := range pts {
			row.ResidualSpans = append(row.ResidualSpans, p.Residual)
		}
		rep.PerSession = append(rep.PerSession, row)
		turns = append(turns, float64(s.Turns))
		residuals = append(residuals, row.MedianResid)
		slacks = append(slacks, row.MedianSlack)
	}
	if rep.StockTotal > 0 {
		rep.Ratio = float64(rep.L3Total) / float64(rep.StockTotal)
	}
	rep.ResidualSlope = leastSquaresSlope(turns, residuals)
	rep.SlackSlope = leastSquaresSlope(turns, slacks)
	return rep
}

func TestPhase4_RewriteTokensReduced(t *testing.T) {
	c := phase4Corpus(t)
	rep := phase4RewriteReport(c)
	require.Greater(t, rep.StockTotal, 0, "a corpus stock never rewrites would make the ratio vacuous")

	var lines strings.Builder
	for _, r := range rep.PerSession {
		fmt.Fprintf(&lines, "\n  %-26s turns=%3d stock=%8d qompack-l3=%8d ratio=%.3f",
			r.Session, r.Turns, r.Stock, r.L3, r.Ratio)
	}
	t.Logf("per-session rewrite tokens:%s\n  total stock=%d qompack-l3=%d ratio=%.4f (bar %.2f)",
		lines.String(), rep.StockTotal, rep.L3Total, rep.Ratio, p4RewriteRatioMax)
	writePhase4Artifact(t, "phase4-rewrite.json", rep)

	require.LessOrEqual(t, float64(rep.L3Total), p4RewriteRatioMax*float64(rep.StockTotal),
		"§10 Phase 4: Σ RewriteTokens[qompack-l3] must be ≤ %.2f × Σ RewriteTokens[stock]", p4RewriteRatioMax)
}

// phase4SignOff returns the pull-request body named by QOMPACK_PHASE4_SIGNOFF, or "" when unset.
func phase4SignOff(t *testing.T) string {
	t.Helper()
	path := os.Getenv(p4SignOffEnv)
	if path == "" {
		return ""
	}
	return readSignOff(path)
}

func TestPhase4_NoDivergenceRegression(t *testing.T) {
	c := phase4Corpus(t)
	stockAgg := eval.MetricsOf(c.report.Policies[p4PolicyStock], c.cfg)
	l3Agg := eval.MetricsOf(c.report.Policies[p4PolicyL3], c.cfg)
	signOff := phase4SignOff(t)

	// The gate's own judge decides: same 2% threshold, same direction table, same absolute
	// floors, same sign-off trailer grammar (test/replay/gate.go).
	var blocking []string
	for _, metric := range p4DivergenceMetrics {
		base, obs := stockAgg[metric], l3Agg[metric]
		reg, regressed := judge(p4PolicyL3, metric, base, obs, signOff)
		verdict := "ok"
		if regressed {
			verdict = "REGRESSED " + deltaPctCell(reg)
			if reg.Allowed {
				verdict += " (signed off)"
			} else {
				blocking = append(blocking, fmt.Sprintf("%s: stock=%.6f qompack-l3=%.6f (%s)",
					metric, base, obs, deltaPctCell(reg)))
			}
		}
		t.Logf("%-22s stock=%12.6f qompack-l3=%12.6f %s", metric, base, obs, verdict)
	}
	require.Empty(t, blocking,
		"§11.3: no divergence metric may regress by more than 2%% without a sign-off: trailer")
}

// leastSquaresSlope fits y = a + b·x and returns b.
func leastSquaresSlope(xs, ys []float64) float64 {
	n := float64(len(xs))
	if n == 0 {
		return 0
	}
	var sx, sy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
	}
	mx, my := sx/n, sy/n
	var num, den float64
	for i := range xs {
		num += (xs[i] - mx) * (ys[i] - my)
		den += (xs[i] - mx) * (xs[i] - mx)
	}
	if den == 0 {
		return 0
	}
	return num / den
}

// medianOf is the nearest-rank median eval's percentiles use, so the two never disagree.
func medianOf(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	idx := int(math.Ceil(0.5*float64(len(s)))) - 1
	return s[min(max(idx, 0), len(s)-1)]
}

func TestPhase4_ResidualSpanFlatAsSessionGrows(t *testing.T) {
	c := phase4Corpus(t)
	rep := phase4RewriteReport(c)

	rows := append([]p4RewriteRow(nil), rep.PerSession...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Turns < rows[j].Turns })
	xs := make([]float64, len(rows))
	ys := make([]float64, len(rows))
	var lines strings.Builder
	for i, r := range rows {
		xs[i], ys[i] = float64(r.Turns), r.MedianSlack
		fmt.Fprintf(&lines, "\n  %-26s turns=%3d median_slack=%8.0f median_residual=%8.0f",
			r.Session, r.Turns, r.MedianSlack, r.MedianResid)
	}
	slope := leastSquaresSlope(xs, ys)

	q := len(rows) / p4QuartileCount
	require.Greater(t, q, 0, "a corpus needs at least %d sessions for quartiles", p4QuartileCount)
	shortest := medianOf(ys[:q])
	longest := medianOf(ys[len(ys)-q:])
	ratio := 1.0 // 0/0: neither quartile has any slack
	if shortest > 0 {
		ratio = longest / shortest
	} else if longest > 0 {
		ratio = math.Inf(1)
	}
	t.Logf("median slack per session (R47):%s\n  slack slope=%.4f tokens/turn (bar %.2f); "+
		"shortest-quartile median=%.0f longest-quartile median=%.0f ratio=%.3f (bar %.2f×)\n"+
		"  recorded, not enforced by replay: residual_slope=%.4f tokens/turn (bar %.2f), "+
		"residual_p95=%.0f (bar %d); discharged by %s",
		lines.String(), slope, p4SlopeMax, shortest, longest, ratio, p4QuartileRatioMax,
		rep.ResidualSlope, p4ResidualSlopeBar, rep.ResidualP95, p4ResidualP95Bar, p4DischargedBy)
	writePhase4Artifact(t, "phase4-rewrite.json", rep)

	require.LessOrEqual(t, slope, p4SlopeMax,
		"median slack must be flat in session length: slope ≤ %.2f tokens/turn", p4SlopeMax)
	require.LessOrEqual(t, ratio, p4QuartileRatioMax,
		"the longest quartile's median slack must be ≤ %.2f × the shortest quartile's", p4QuartileRatioMax)
}

// p4DeepCut recomputes, with scheduler.Evaluate itself, the cut §5.4 prescribes at a cold point
// over exactly the candidate set the policy exposed: the same n, the same window convention, the
// same regime, a gap that classifies cold, and nothing the policy chose.
func p4DeepCut(cfg config.Config, s eval.Session, pt p4Point) scheduler.Decision {
	n := core.Tokens(pt.N)
	return scheduler.Evaluate(scheduler.Inputs{
		Now:                     s.Turns[pt.At].TS,
		ContextTokens:           n,
		EffectiveWindow:         n + scheduler.HostAutoCompactBuffer,
		MaxOutputTokens:         scheduler.HostDefaultMaxOutput,
		LastAPICallTS:           s.Turns[pt.At-1].TS,
		Changepoint:             scheduler.ChangepointState{AtChangepoint: true},
		Candidates:              pt.Candidates,
		Cfg:                     cfg.Scheduler,
		CouplingLambda:          cfg.Selection.Submodular.Lambda,
		Regime:                  p4Regime(cfg),
		ExpiringTriggerFraction: cfg.Runtime.Scheduler.Cache.ExpiringTriggerFraction,
		AssumeMaxTTLSeconds:     cfg.Runtime.Scheduler.Cache.AssumeMaxTTLSeconds,
	})
}

func TestPhase4_ResidualUnderMaxResidualTokens(t *testing.T) {
	c := phase4Corpus(t)
	rep := phase4RewriteReport(c)

	var warm, cold, none int
	var lines strings.Builder
	for _, s := range c.sessions {
		for _, pt := range p4Points(c.cfg, s) {
			fmt.Fprintf(&lines, "\n  %-26s at=%3d n=%7d P=%7d residual=%7d achievable=%7d slack=%6d ttl=%s cands=%d",
				s.Session.ID, pt.At, pt.N, pt.P, pt.Residual, pt.Achievable, pt.Slack, pt.TTL, len(pt.Candidates))
			switch {
			case len(pt.Candidates) == 0:
				// No eligible candidate: no cut, stock's keep-set, residual is the whole prefix.
				none++
				require.Equal(t, 0, pt.P, "%s at %d: no candidate means no cut", s.Session.ID, pt.At)
				require.Equal(t, 0, pt.Slack, "%s at %d: achievable is n with no candidate", s.Session.ID, pt.At)
			case pt.TTL == scheduler.TTLCold:
				// §5.4: a provably cold cache prefers the deep cut. Recompute it over the same
				// candidate set with the real selector and require the policy landed there.
				cold++
				d := p4DeepCut(c.cfg, s.Session, pt)
				require.True(t, d.ShouldCompact, "%s at %d: the cold recomputation must fire", s.Session.ID, pt.At)
				require.Equal(t, d.P.Pos, pt.P, "%s at %d: cold point must sit on the deep cut chooseP selects",
					s.Session.ID, pt.At)
			case pt.TTL == scheduler.TTLExpiring:
				require.Fail(t, "an expiring point", "%s at %d: R47 defines warm and cold points only", s.Session.ID, pt.At)
			default:
				// Warm (or unclassifiable): the cut is on the latest eligible candidate.
				warm++
				require.Equal(t, 0, pt.Slack, "%s at %d: warm point must sit on the latest eligible candidate",
					s.Session.ID, pt.At)
			}
		}
	}
	t.Logf("qompack-l3 cut placement per compaction point (R47):%s\n  warm=%d cold=%d no-candidate=%d\n"+
		"  recorded, not enforced by replay: residual_p95=%.0f (bar %d), residual_slope=%.4f (bar %.2f); "+
		"discharged by %s",
		lines.String(), warm, cold, none, rep.ResidualP95, p4ResidualP95Bar, rep.ResidualSlope,
		p4ResidualSlopeBar, p4DischargedBy)
	writePhase4Artifact(t, "phase4-rewrite.json", rep)
	require.Greater(t, warm+cold, 0, "a corpus with no cut at all would make the rows vacuous")
}

// clampP is score.go's clamp: P is confined to [0, n] before it enters the rewrite span.
func clampP(p, n int) int { return min(max(p, 0), n) }

func TestPhase4_ResidualSpanIsRewriteSpan(t *testing.T) {
	c := phase4Corpus(t)
	w := c.cfg.Scheduler.Cache.WriteMultiplier

	var corpusSpan, corpusRewrite int
	for _, s := range c.sessions {
		for _, name := range []string{p4PolicyStock, p4PolicyL3} {
			r, sc := s.Runs[name], s.Scores[name]
			require.Len(t, r.ResidualSpan, len(r.At), "%s/%s", s.Session.ID, name)
			require.Len(t, r.Keeps, len(r.At), "%s/%s", s.Session.ID, name)
			require.Len(t, r.PrefixTokens, len(r.At), "%s/%s", s.Session.ID, name)

			span := 0
			for i := range r.At {
				n := int(r.PrefixTokens[i])
				p := r.Keeps[i].P
				require.Equal(t, core.Tokens(max(n-p, 0)), r.ResidualSpan[i],
					"%s/%s event %d: residual is prefix − P", s.Session.ID, name, i)
				span += n - clampP(p, n)
			}
			want := int(math.Round(w * float64(span)))
			require.Equal(t, want, sc.RewriteTokens,
				"%s/%s: RewriteTokens is round(w × Σ(n − clamp(P)))", s.Session.ID, name)
			if name == p4PolicyL3 {
				corpusSpan += span
				corpusRewrite += sc.RewriteTokens
			}
		}
	}
	// Per-session rounding is the only slack: |Σ round(w·span_s) − w·Σ span_s| ≤ 0.5 per session.
	require.InDelta(t, w*float64(corpusSpan), float64(corpusRewrite), 0.5*float64(len(c.sessions)),
		"Σ ResidualSpan × w must equal Σ RewriteTokens to within per-session rounding")
	t.Logf("qompack-l3: Σ residual span=%d × w=%.2f → %.1f; Σ RewriteTokens=%d",
		corpusSpan, w, w*float64(corpusSpan), corpusRewrite)
}

type p4PauseRow struct {
	Session  string  `json:"session"`
	Residual []int   `json:"residualSpan"`
	PauseMS  []int   `json:"pauseMs"`
	P50MS    float64 `json:"pauseMsP50"`
}

type p4PauseReport struct {
	PauseModelled  bool         `json:"pause_modelled"`
	InterceptMS    float64      `json:"intercept_ms"`
	MSPerKResidual float64      `json:"ms_per_k_residual"`
	PerSession     []p4PauseRow `json:"per_session"`
}

func TestPhase4_CompactionPauseModelled(t *testing.T) {
	c := phase4Corpus(t)
	lat := eval.DefaultLatencyModel()
	require.True(t, lat.Modelled, "the harness's latency model is a model, and says so")

	var rows []p4PauseRow
	for _, s := range c.sessions {
		for _, name := range []string{p4PolicyStock, p4PolicyL3} {
			r := s.Runs[name]
			require.Len(t, r.PauseMS, len(r.ResidualSpan), "%s/%s", s.Session.ID, name)
			for i, residual := range r.ResidualSpan {
				want := int(math.Round(lat.PauseBaseMS + lat.PausePerKResidualMS*float64(residual)/tokensPerK))
				require.Equal(t, want, r.PauseMS[i],
					"%s/%s event %d: pause is linear in residual under the harness's own coefficients",
					s.Session.ID, name, i)
			}
			if name == p4PolicyL3 {
				row := p4PauseRow{Session: s.Session.ID, P50MS: s.Scores[name].CompactionPauseMS.P50}
				for i := range r.ResidualSpan {
					row.Residual = append(row.Residual, int(r.ResidualSpan[i]))
					row.PauseMS = append(row.PauseMS, r.PauseMS[i])
				}
				rows = append(rows, row)
			}
		}
	}
	writePhase4Artifact(t, "phase4-pause.json", p4PauseReport{
		PauseModelled:  true,
		InterceptMS:    lat.PauseBaseMS,
		MSPerKResidual: lat.PausePerKResidualMS,
		PerSession:     rows,
	})
}

func TestPhase4_PSelectionGateHonoured(t *testing.T) {
	ctx := context.Background()
	c := phase4Corpus(t)
	scheduler.DisablePSelection()
	defer scheduler.DisablePSelection()
	require.False(t, scheduler.PSelectionAvailable())

	// One side: p-selection is not submodular selection, so the policy still cuts. Pick the
	// first point at which it has a candidate, so the assertion is about a real cut.
	var s eval.Session
	var at core.TurnIndex
	found := false
	for _, ps := range c.sessions {
		for _, pt := range p4Points(c.cfg, ps) {
			if len(pt.Candidates) > 0 {
				s, at, found = ps.Session, pt.At, true
				break
			}
		}
		if found {
			break
		}
	}
	require.True(t, found, "the corpus must have at least one point with an eligible candidate")
	ks, err := l3policy.New(c.cfg).KeepSet(ctx, s, at, eval.DefaultKeepBudget)
	require.NoError(t, err)
	require.NotEmpty(t, ks.IDs)
	require.Greater(t, ks.P, 0)

	// The other side: the selector refuses to construct while the gate is down (closing note 3).
	_, err = analyzer.NewSelector(0, nil, dag.Slice{}, nil, c.cfg.Selection.Submodular.Lambda,
		c.cfg.Selection.Submodular.LazyGreedy)
	require.ErrorIs(t, err, core.ErrNotImplemented)
}
