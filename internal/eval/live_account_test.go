package eval_test

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

const haiku = "claude-haiku-4-5-20251001"

// TestAccountHostStream_DifferencesRunningTotals is the no-double-count rule on a real session.
// The /compact turn's result reports zero usage for its main loop while its modelUsage running
// total jumps by the summarization request; the account attributes exactly that jump to the
// compaction, and the session total equals the final running total — not the sum of results.
func TestAccountHostStream_DifferencesRunningTotals(t *testing.T) {
	s := parseLiveFixture(t, smoke1Stream)
	a := eval.AccountHostStream(s, eval.AccountBaseline{})
	require.True(t, a.Consistent, "problems: %v", a.Problems)
	require.Len(t, a.Turns, 3)

	t0 := a.Turns[0]
	require.Equal(t, eval.UsageTotals{Input: 18, Output: 328, CacheRead: 42295, CacheWrite: 8350}, stripped(t0.Delta[haiku]))
	require.Empty(t, t0.Beside, "the first turn ran nothing beside its main loop")
	require.NotNil(t, t0.StepsMatchResult)
	require.True(t, *t0.StepsMatchResult, "the two deduplicated requests sum to result.usage")

	t1 := a.Turns[1]
	require.Equal(t, "compact", t1.LocalCommand)
	require.True(t, t1.Compacted)
	require.Equal(t, eval.KindCompaction, t1.BesideKind)
	require.Equal(t, eval.UsageTotals{Input: 2258, Output: 1398, CacheRead: 24782, CacheWrite: 259}, stripped(t1.Beside[haiku]))
	require.Nil(t, t1.Beside[haiku].CacheWrite5m, "a running total carries no TTL split, so the compaction's is unknown")
	require.NotNil(t, t1.Beside[haiku].Thinking)
	require.Equal(t, int64(507), *t1.Beside[haiku].Thinking)

	total := a.Total[haiku]
	require.Equal(t, eval.UsageTotals{Input: 2286, Output: 1845, CacheRead: 88614, CacheWrite: 12107}, stripped(total))
	var summed int64
	for _, turn := range s.Turns {
		summed += turn.Result.ModelUsage[haiku].InputTokens
	}
	require.NotEqual(t, summed, total.Input, "summing running totals would have counted turn 0 three times")
	require.InDelta(t, 0.04439215, a.HostCostUSD, 1e-9)
}

// TestAccountHostStream_ResumedBaselineIsSubtracted: a resumed process's first running total
// carries the restored spend; with the previous invocation's final totals as the baseline only the
// new work is counted.
func TestAccountHostStream_ResumedBaselineIsSubtracted(t *testing.T) {
	s := parseLiveFixture(t, smoke1Stream)
	first := eval.HostStream{Init: s.Init, Turns: s.Turns[:1]}
	resumed := eval.HostStream{Init: s.Init, Turns: s.Turns[1:]}

	whole := eval.AccountHostStream(s, eval.AccountBaseline{})
	a1 := eval.AccountHostStream(first, eval.AccountBaseline{})
	a2 := eval.AccountHostStream(resumed, first.FinalBaseline())
	require.True(t, a2.Consistent, "problems: %v", a2.Problems)

	sum := stripped(a1.Total[haiku])
	sum.Input += a2.Total[haiku].Input
	sum.Output += a2.Total[haiku].Output
	sum.CacheRead += a2.Total[haiku].CacheRead
	sum.CacheWrite += a2.Total[haiku].CacheWrite
	require.Equal(t, stripped(whole.Total[haiku]), sum, "the two invocations add up to the one session exactly")

	naive := eval.AccountHostStream(resumed, eval.AccountBaseline{})
	require.Greater(t, naive.Total[haiku].CacheRead, a2.Total[haiku].CacheRead,
		"without the baseline the restored spend would be counted again")
}

// TestAccountHostStream_DecreasingTotalIsReportedNotRepaired: a running total that goes down means
// the host reset it; the account says so instead of producing negative work.
func TestAccountHostStream_DecreasingTotalIsReportedNotRepaired(t *testing.T) {
	s := parseLiveFixture(t, smoke1Stream)
	a := eval.AccountHostStream(eval.HostStream{Init: s.Init, Turns: s.Turns[2:]}, eval.AccountBaseline{
		ModelUsage:   map[string]eval.HostModelUsage{haiku: {InputTokens: 1 << 40}},
		TotalCostUSD: 1,
	})
	require.False(t, a.Consistent)
	require.NotEmpty(t, a.Problems)
	joined := strings.Join(a.Problems, "\n")
	require.Contains(t, joined, "decreased")
}

// TestLedgerRecords_SplitThinkingAndKeepUnknownsUnknown: the ledger view prices visible output and
// thinking separately so they sum to the host's output, and a compaction's cache write, whose TTL
// the host never reports, stays unknown rather than being guessed into one TTL.
func TestLedgerRecords_SplitThinkingAndKeepUnknownsUnknown(t *testing.T) {
	a := eval.AccountHostStream(parseLiveFixture(t, smoke1Stream), eval.AccountBaseline{})
	recs := a.LedgerRecords("smoke1", "anthropic", eval.PricingSubscription, "2026-09-22")
	l := eval.RequestLedger{Version: eval.RequestLedgerVersion, Records: recs}
	require.NoError(t, l.Validate())
	require.Len(t, recs, 4, "three main-loop records and one compaction record")

	main0 := recs[0]
	require.Equal(t, eval.KnownTokens(147), main0.Reported[eval.CategoryOutput])
	require.Equal(t, eval.KnownTokens(181), main0.Reported[eval.CategoryThinkingOutput])
	require.Equal(t, eval.KnownTokens(8350), main0.Reported[eval.CategoryCacheWrite1h])
	require.Empty(t, main0.Missing)

	var compaction eval.RequestRecord
	for _, r := range recs {
		if r.Kind == eval.KindCompaction && r.ParentID != "" {
			compaction = r
		}
	}
	require.Equal(t, "smoke1-t01-main", compaction.ParentID)
	require.Equal(t, []eval.UsageCategory{eval.CategoryCacheWrite5m, eval.CategoryCacheWrite1h}, compaction.Missing)

	sums := l.Sum()
	require.Equal(t, 3, sums[eval.CategoryCacheWrite1h].KnownRecords)
	require.Equal(t, 1, sums[eval.CategoryCacheWrite1h].UnknownRecords)
}

// TestEstimateLive_MatchesTheHostsOwnListPrice: the rate table prices a fully known request to
// within the half-micro rounding of each category of the host's own costUSD, which is what makes
// the table's claude-haiku-4-5 row a checked number rather than a copied one.
func TestEstimateLive_MatchesTheHostsOwnListPrice(t *testing.T) {
	rates := loadRates(t)
	s := parseLiveFixture(t, smoke1Stream)
	a := eval.AccountHostStream(s, eval.AccountBaseline{})
	recs := a.LedgerRecords("smoke1", "anthropic", eval.PricingSubscription, rates.Date)

	first := eval.EstimateLive(eval.RequestLedger{Version: eval.RequestLedgerVersion, Records: recs[:1]}, rates)
	require.True(t, first.Completeness.Complete)
	hostMicros := s.Turns[0].Result.TotalCostUSD * 1e6
	require.LessOrEqual(t, math.Abs(float64(first.Total.Micros)-hostMicros), 3.0,
		"ours %d micros, the host's %.1f", first.Total.Micros, hostMicros)

	whole := eval.EstimateLive(eval.RequestLedger{Version: eval.RequestLedgerVersion, Records: recs}, rates)
	require.False(t, whole.Completeness.Complete, "the compaction's cache-write TTL is unknown")
	require.Equal(t, []eval.UsageCategory{eval.CategoryCacheWrite5m, eval.CategoryCacheWrite1h}, whole.Completeness.MissingCategories)
	require.LessOrEqual(t, float64(whole.Total.Micros), a.HostCostUSD*1e6+3,
		"an incomplete estimate is a lower bound")
	require.Equal(t, "USD", whole.Total.Currency)
}

func TestEstimateLive_UnpricedModelIsNamed(t *testing.T) {
	rates := loadRates(t)
	rec := eval.RequestRecord{
		ID: "x", Kind: eval.KindTurn, Provider: "anthropic", Model: "claude-unknown-9", PricingMode: eval.PricingSubscription,
		Reported: map[eval.UsageCategory]eval.TokenCount{eval.CategoryInput: eval.KnownTokens(10)},
	}
	est := eval.EstimateLive(eval.RequestLedger{Version: eval.RequestLedgerVersion, Records: []eval.RequestRecord{rec}}, rates)
	require.Equal(t, []string{"claude-unknown-9"}, est.Unpriced)
	require.False(t, est.Completeness.Complete)
	require.Zero(t, est.Total.Micros)
}

func TestLiveRateTable_ValidatesProvenanceAndKeys(t *testing.T) {
	rates := loadRates(t)
	require.Equal(t, "2026-09-22", rates.Date)
	for _, m := range []string{"claude-haiku-4-5-20251001", "claude-opus-5-5[1m]", "Claude-Sonnet-5"} {
		_, ok := rates.Schedule(m)
		require.True(t, ok, "%s resolves to a priced model", m)
	}
	require.Equal(t, "claude-opus-5-5", eval.CanonicalModel("claude-opus-5-5[1m]"))

	for name, doc := range map[string]string{
		"no source":     `{"version":1,"date":"2026-09-22","currency":"USD","models":{"m":{"input_per_mtok_micros":1,"output_per_mtok_micros":1,"cache_read_multiplier":0.5,"cache_write_5m_multiplier":1,"cache_write_1h_multiplier":1}}}`,
		"dated key":     `{"version":1,"date":"d","source":"s","currency":"USD","models":{"m-20250101":{"input_per_mtok_micros":1,"output_per_mtok_micros":1,"cache_read_multiplier":0.5,"cache_write_5m_multiplier":1,"cache_write_1h_multiplier":1}}}`,
		"zero multiply": `{"version":1,"date":"d","source":"s","currency":"USD","models":{"m":{"input_per_mtok_micros":1,"output_per_mtok_micros":1,"cache_read_multiplier":0,"cache_write_5m_multiplier":1,"cache_write_1h_multiplier":1}}}`,
		"unknown field": `{"version":1,"date":"d","source":"s","currency":"USD","models":{},"extra":1}`,
		"no models":     `{"version":1,"date":"d","source":"s","currency":"USD","models":{}}`,
	} {
		_, err := eval.ParseLiveRateTable([]byte(doc))
		require.Error(t, err, name)
	}
}

func loadRates(t *testing.T) eval.LiveRateTable {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "eval", "live", "rates.json"))
	require.NoError(t, err)
	rates, err := eval.ParseLiveRateTable(raw)
	require.NoError(t, err)
	return rates
}

func stripped(u eval.UsageTotals) eval.UsageTotals {
	return eval.UsageTotals{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
}

// TestAccountHostStream_FreshSessionKnowsItsThinking: a model absent from a fresh (zero) baseline
// had spent nothing yet, thinking included, so the first turn's thinking delta and the session's
// thinking total are known numbers rather than unknown ones.
func TestAccountHostStream_FreshSessionKnowsItsThinking(t *testing.T) {
	a := eval.AccountHostStream(parseLiveFixture(t, smoke1Stream), eval.AccountBaseline{})
	require.True(t, a.Consistent, "problems: %v", a.Problems)
	d0 := a.Turns[0].Delta[haiku]
	require.NotNil(t, d0.Thinking, "the first turn's thinking is its running total minus zero")
	require.Equal(t, int64(181), *d0.Thinking)
	total := a.Total[haiku]
	require.NotNil(t, total.Thinking, "the session's thinking total is the final running total minus zero")
	require.Equal(t, int64(777), *total.Thinking)
}
