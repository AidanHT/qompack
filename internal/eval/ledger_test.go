package eval_test

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// ── fixtures ────────────────────────────────────────────────────────────────────────────────────

// ledgerFixturePath resolves the hand-authored frozen-format fixture. It lives under
// testdata/golden/eval/, which is this package's own golden directory (alongside divergence/ and
// growth/), so no other subplan's -update run can delete it without comment.
func ledgerFixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(moduleRoot(t), "testdata", "golden", "eval", "ledger", name)
}

// phase0LedgerPath resolves the corrected request ledger for the historical Phase 0 replay. It
// sits BESIDE testdata/baseline/phase0.json, which is never touched: M0-04 requires the old
// metric outputs to keep their original definitions while the corrected accounting is added
// separately versioned.
func phase0LedgerPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(moduleRoot(t), "testdata", "baseline", "phase0.ledger.v1.json")
}

// readFixture reads a committed fixture and normalizes CRLF, so a Windows checkout that ignored
// .gitattributes cannot turn a byte-comparison into a line-ending argument.
func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
}

// encodeLedger renders a ledger the way every JSON writer in this repository does: two-space
// indent, HTML escaping off, one trailing newline. The frozen fixture is authored in exactly this
// form, so a round-trip that changes a single byte is a format change and fails.
func encodeLedger(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	require.NoError(t, enc.Encode(v))
	return buf.Bytes()
}

// ── TokenCount: unknown is not zero ─────────────────────────────────────────────────────────────

// TestTokenCount_UnknownIsNotZero is the single most important assertion in this file. Qompack.md
// §5.3 says "Missing usage is unknown, never zero", and a struct whose zero value is an
// indistinguishable 0 is precisely how that rule gets broken silently.
func TestTokenCount_UnknownIsNotZero(t *testing.T) {
	unknown := eval.UnknownTokens()
	knownZero := eval.KnownTokens(0)

	require.False(t, unknown.Known)
	require.True(t, knownZero.Known)
	require.NotEqual(t, unknown, knownZero, "an unknown count and a known zero must not be equal")

	require.JSONEq(t, `{"known":false}`, string(mustMarshal(t, unknown)))
	require.JSONEq(t, `{"known":true}`, string(mustMarshal(t, knownZero)))
	require.JSONEq(t, `{"known":true,"value":42}`, string(mustMarshal(t, eval.KnownTokens(42))))

	var back eval.TokenCount
	require.NoError(t, json.Unmarshal([]byte(`{"known":false}`), &back))
	require.Equal(t, unknown, back)
}

// TestTokenCount_AddOfUnknownYieldsUnknown: an unknown summand poisons the sum. A ledger that
// added unknown as zero would report a confident total it has no evidence for.
func TestTokenCount_AddOfUnknownYieldsUnknown(t *testing.T) {
	require.Equal(t, eval.KnownTokens(30), eval.KnownTokens(10).Add(eval.KnownTokens(20)))
	require.False(t, eval.KnownTokens(10).Add(eval.UnknownTokens()).Known)
	require.False(t, eval.UnknownTokens().Add(eval.KnownTokens(10)).Known)
	require.False(t, eval.UnknownTokens().Add(eval.UnknownTokens()).Known)

	poisoned := eval.KnownTokens(10).Add(eval.UnknownTokens())
	require.Zero(t, poisoned.Value, "an unknown result must not carry a partial value that reads as a total")
}

// TestUsageCategories_AreTheSixSeparateCategories pins the category set and its canonical order.
// §5.3 requires uncached input, cache reads, writes BY TTL, output, and the extended-thinking
// output the summarization request inherits, to be separable — a single "tokens" number cannot be
// reconciled against anything.
func TestUsageCategories_AreTheSixSeparateCategories(t *testing.T) {
	require.Equal(t, []eval.UsageCategory{
		eval.CategoryInput,
		eval.CategoryOutput,
		eval.CategoryCacheRead,
		eval.CategoryCacheWrite5m,
		eval.CategoryCacheWrite1h,
		eval.CategoryThinkingOutput,
	}, eval.UsageCategories())

	require.Equal(t, eval.UsageCategory("cache_write_5m"), eval.CategoryCacheWrite5m)
	require.Equal(t, eval.UsageCategory("cache_write_1h"), eval.CategoryCacheWrite1h)
	require.Equal(t, eval.UsageCategory("thinking_output"), eval.CategoryThinkingOutput)

	// The returned slice must be a copy: a caller that sorts it must not reorder the canonical set.
	got := eval.UsageCategories()
	got[0] = "clobbered"
	require.Equal(t, eval.CategoryInput, eval.UsageCategories()[0])
}

// ── RequestRecord.Validate ──────────────────────────────────────────────────────────────────────

// validRecord is a minimal record every negative case below mutates one field of.
func validRecord() eval.RequestRecord {
	return eval.RequestRecord{
		ID:            "req-1",
		Kind:          eval.KindTurn,
		Provider:      "example-provider",
		Model:         "example-model",
		RateTableDate: "2026-08-23",
		PricingMode:   eval.PricingEstimate,
		Reported: map[eval.UsageCategory]eval.TokenCount{
			eval.CategoryInput:  eval.KnownTokens(3300),
			eval.CategoryOutput: eval.KnownTokens(900),
		},
		Missing: []eval.UsageCategory{
			eval.CategoryCacheRead,
			eval.CategoryCacheWrite5m,
			eval.CategoryCacheWrite1h,
			eval.CategoryThinkingOutput,
		},
	}
}

// TestRequestRecord_Validate covers every invariant the contract names: the two views of what is
// missing may not disagree, and an invoice may not be claimed outside invoice pricing.
func TestRequestRecord_Validate(t *testing.T) {
	require.NoError(t, validRecord().Validate())

	usd := eval.Money{Micros: 1_000_000, Currency: "USD"}

	cases := []struct {
		name string
		want string
		fn   func(r *eval.RequestRecord)
	}{
		{"empty ID", "id", func(r *eval.RequestRecord) { r.ID = "" }},
		{"unknown kind", "kind", func(r *eval.RequestRecord) { r.Kind = "sideways" }},
		{"unknown pricing mode", "pricing", func(r *eval.RequestRecord) { r.PricingMode = "vibes" }},
		{"unknown category reported", "category", func(r *eval.RequestRecord) {
			r.Reported["speculative_decode"] = eval.KnownTokens(1)
		}},
		{"category absent from both views", "missing", func(r *eval.RequestRecord) {
			r.Missing = r.Missing[:len(r.Missing)-1]
		}},
		{"category in Missing but reported known", "missing", func(r *eval.RequestRecord) {
			r.Reported[eval.CategoryCacheRead] = eval.KnownTokens(120_000)
		}},
		{"reported-but-unknown is not listed as missing", "missing", func(r *eval.RequestRecord) {
			r.Reported[eval.CategoryCacheRead] = eval.UnknownTokens()
			r.Missing = []eval.UsageCategory{
				eval.CategoryCacheWrite5m, eval.CategoryCacheWrite1h, eval.CategoryThinkingOutput,
			}
		}},
		{"duplicate in Missing", "missing", func(r *eval.RequestRecord) {
			r.Missing = append(r.Missing, eval.CategoryCacheRead)
		}},
		{"Missing out of canonical order", "order", func(r *eval.RequestRecord) {
			r.Missing[0], r.Missing[3] = r.Missing[3], r.Missing[0]
		}},
		{"invoice without invoice pricing", "invoice", func(r *eval.RequestRecord) {
			r.Invoice = &usd
		}},
		{"negative retries", "retries", func(r *eval.RequestRecord) { r.Retries = -1 }},
		{"self parent", "parent", func(r *eval.RequestRecord) { r.ParentID = r.ID }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validRecord()
			tc.fn(&r)
			err := r.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}

	t.Run("invoice pricing may carry an invoice", func(t *testing.T) {
		r := validRecord()
		r.PricingMode = eval.PricingInvoice
		r.Invoice = &usd
		require.NoError(t, r.Validate())
	})
}

// TestRequestLedger_Validate_ParentIDMustResolve: attribution that names a request the ledger does
// not contain is not attribution. A child agent's tokens have to hang off something.
func TestRequestLedger_Validate_ParentIDMustResolve(t *testing.T) {
	parent := validRecord()
	child := validRecord()
	child.ID = "req-child-1"
	child.Kind = eval.KindChild
	child.ParentID = "req-1"

	l := eval.RequestLedger{Version: 1, Records: []eval.RequestRecord{parent, child}}
	require.NoError(t, l.Validate())

	orphan := l
	orphan.Records = []eval.RequestRecord{child}
	err := orphan.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "req-1")

	dup := l
	dup.Records = []eval.RequestRecord{parent, parent}
	require.ErrorContains(t, dup.Validate(), "duplicate")

	bad := l
	bad.Version = 0
	require.ErrorContains(t, bad.Validate(), "version")
}

// ── the frozen format ───────────────────────────────────────────────────────────────────────────

// TestRequestLedger_GoldenFormatRoundTrip is the format freeze. The fixture is hand-authored, not
// generated: if the implementation renames a JSON key, reorders a struct field, drops an
// omitempty, or starts writing an unknown count as 0, this test fails on the exact byte.
func TestRequestLedger_GoldenFormatRoundTrip(t *testing.T) {
	want := readFixture(t, ledgerFixturePath(t, "full.json"))

	var l eval.RequestLedger
	require.NoError(t, json.Unmarshal(want, &l))
	require.NoError(t, l.Validate())

	require.Equal(t, string(want), string(encodeLedger(t, l)),
		"testdata/golden/eval/ledger/full.json is the frozen wire format; a diff here is a format change")

	// Spot-check the two shapes the format exists to keep apart.
	require.Equal(t, eval.KnownTokens(0), l.Records[0].Reported[eval.CategoryCacheWrite1h])
	require.Equal(t, eval.UnknownTokens(), l.Records[1].Reported[eval.CategoryOutput])
}

// TestRequestLedger_Sum totals the fixture. Each category reports its known total, how many
// records supplied it, and how many left it unknown; a category with any unknown record is not
// complete, and Sum never fills an unknown with zero.
func TestRequestLedger_Sum(t *testing.T) {
	var l eval.RequestLedger
	require.NoError(t, json.Unmarshal(readFixture(t, ledgerFixturePath(t, "full.json")), &l))

	sum := l.Sum()
	require.Len(t, sum, len(eval.UsageCategories()),
		"Sum must report every category, so an absent key can never be read as a zero")

	input := sum[eval.CategoryInput]
	require.Equal(t, core.Tokens(3300+3300+167_000+11_500), input.Known)
	require.Equal(t, 4, input.KnownRecords)
	require.Equal(t, 1, input.UnknownRecords) // the invoice record reports no tokens at all
	require.False(t, input.Complete())

	write1h := sum[eval.CategoryCacheWrite1h]
	require.Equal(t, core.Tokens(0), write1h.Known)
	require.Equal(t, 1, write1h.KnownRecords, "a KNOWN zero still counts as a reporting record")
	require.Equal(t, 4, write1h.UnknownRecords)
	require.False(t, write1h.Complete())
}

// TestPhase0Ledger_RecordsWhatIsTrue: the historical Phase 0 replay made no model call, so its
// ledger is all-unknown rather than all-zero. A run that reported zeros here would look like a
// free run instead of an unmeasured one.
func TestPhase0Ledger_RecordsWhatIsTrue(t *testing.T) {
	raw := readFixture(t, phase0LedgerPath(t))

	l, err := eval.LoadRequestLedger(phase0LedgerPath(t))
	require.NoError(t, err)
	require.NoError(t, l.Validate())

	require.Equal(t, 1, l.Version)
	require.Nil(t, l.Schedule, "no provider rate was applied, so no rate schedule may be claimed")
	require.Len(t, l.Records, 1)

	r := l.Records[0]
	require.Equal(t, "phase0-replay", r.ID)
	require.Equal(t, eval.KindTurn, r.Kind)
	require.Equal(t, eval.PricingNone, r.PricingMode)
	require.Empty(t, r.Reported)
	require.Equal(t, eval.UsageCategories(), r.Missing)
	require.Nil(t, r.Estimate)
	require.Nil(t, r.Invoice)
	require.Contains(t, r.Notes, "test/replay/1")
	require.Contains(t, r.Notes, "e2d9fa5aa94946582e2ab711d74dc55e74b9a413bd57e4b9c465351984707fb3")
	require.Contains(t, r.Notes, "24 synthetic sessions")
	require.Contains(t, r.Notes, "latency modelled")

	for cat, cs := range l.Sum() {
		require.Equal(t, core.Tokens(0), cs.Known, "category %s", cat)
		require.Equal(t, 0, cs.KnownRecords, "category %s", cat)
		require.Equal(t, 1, cs.UnknownRecords, "category %s", cat)
		require.False(t, cs.Complete(), "category %s must never read as known-complete", cat)
	}

	require.Len(t, l.Compaction, 1)
	m := l.Compaction[0]
	for name, tc := range map[string]eval.TokenCount{
		"nativeCompactionInput": m.NativeCompactionInput,
		"qompackFrontierWork":   m.QompackFrontierWork,
		"addedTokens":           m.AddedTokens,
		"totalObservedContext":  m.TotalObservedContext,
	} {
		require.False(t, tc.Known, "%s was never measured and must stay unknown", name)
	}

	require.Equal(t, string(raw), string(encodeLedger(t, l)))
}

// TestCompactionMeasurement_HasNoSumMethod: §11.2 says Qompack-added tokens and total observed
// native context are DISTINCT quantities. The type therefore offers nothing that adds them, so a
// caller cannot accidentally publish a total that means nothing.
func TestCompactionMeasurement_HasNoSumMethod(t *testing.T) {
	v := reflect.TypeOf(eval.CompactionMeasurement{})
	require.Zero(t, v.NumMethod(), "CompactionMeasurement must expose no methods at all")
	require.Zero(t, reflect.PointerTo(v).NumMethod())
	require.Equal(t, 4, v.NumField(), "the four measurements stay four separate fields")
}

// ── estimates, invoices and subscriptions ───────────────────────────────────────────────────────

// testSchedule is a rate schedule with explicit provenance. The multipliers come from
// config.Defaults() (D11: r and w are config keys, never literals at a use site); the per-million
// cash rates are deliberately round invented numbers, because this repository has verified no
// provider price and must not appear to.
func testSchedule() eval.RateSchedule {
	cache := config.Defaults().Scheduler.Cache
	return eval.RateSchedule{
		Provider: "example-provider",
		Model:    "example-model",
		Date:     "2026-08-23",
		Source: "unit test: multipliers from config.Defaults().Scheduler.Cache; the per-million " +
			"amounts are invented placeholders and assert no provider price",
		PerMillion: map[eval.UsageCategory]eval.Money{
			eval.CategoryInput:          {Micros: 3_000_000, Currency: "USD"},
			eval.CategoryOutput:         {Micros: 15_000_000, Currency: "USD"},
			eval.CategoryThinkingOutput: {Micros: 15_000_000, Currency: "USD"},
		},
		CacheReadMultiplier:    cache.ReadMultiplier,
		CacheWrite5mMultiplier: cache.WriteMultiplier,
		CacheWrite1hMultiplier: oneHourWriteMultiplier,
	}
}

// oneHourWriteMultiplier is w at the one-hour TTL.
//
// w is a (TTL, w) pair, not a scalar: 1.25 at the five-minute TTL — which is what
// config.Defaults().Scheduler.Cache.WriteMultiplier holds — and 2.0 at the one hour Claude Code
// uses on a subscription. Source: Qompack.md v1.3 §5.1 and plans/QOMPACK-ERRATA.md's 2026-08-23
// cache-cost verification. It is spelled here rather than in ledger.go because production code
// never carries a multiplier literal: RateSchedule transports it as data with its Source.
const oneHourWriteMultiplier = 2.0

// TestRequestLedger_Estimate_IsALowerBoundOverUnknowns: an estimate computed over a ledger with
// any unknown category is labelled incomplete and names what it could not price. It is a floor,
// not a bill.
func TestRequestLedger_Estimate_IsALowerBoundOverUnknowns(t *testing.T) {
	s := testSchedule()

	complete := eval.RequestRecord{
		ID: "req-1", Kind: eval.KindTurn, Provider: s.Provider, Model: s.Model,
		RateTableDate: s.Date, PricingMode: eval.PricingEstimate,
		Reported: map[eval.UsageCategory]eval.TokenCount{
			eval.CategoryInput:          eval.KnownTokens(1_000_000),
			eval.CategoryOutput:         eval.KnownTokens(0),
			eval.CategoryCacheRead:      eval.KnownTokens(0),
			eval.CategoryCacheWrite5m:   eval.KnownTokens(0),
			eval.CategoryCacheWrite1h:   eval.KnownTokens(0),
			eval.CategoryThinkingOutput: eval.KnownTokens(0),
		},
		Missing: nil,
	}
	require.NoError(t, complete.Validate())

	full := eval.RequestLedger{Version: 1, Records: []eval.RequestRecord{complete}, Schedule: &s}
	require.NoError(t, full.Validate())

	amount, c := full.Estimate(s)
	require.Equal(t, eval.Money{Micros: 3_000_000, Currency: "USD"}, amount)
	require.True(t, c.Complete)
	require.Empty(t, c.MissingCategories)
	require.Zero(t, c.UnknownRecords)

	partial := complete
	partial.Reported = map[eval.UsageCategory]eval.TokenCount{
		eval.CategoryInput: eval.KnownTokens(1_000_000),
	}
	partial.Missing = []eval.UsageCategory{
		eval.CategoryOutput, eval.CategoryCacheRead, eval.CategoryCacheWrite5m,
		eval.CategoryCacheWrite1h, eval.CategoryThinkingOutput,
	}
	lower := eval.RequestLedger{Version: 1, Records: []eval.RequestRecord{partial}, Schedule: &s}
	require.NoError(t, lower.Validate())

	amount2, c2 := lower.Estimate(s)
	require.Equal(t, amount, amount2, "the priced floor is the known part only")
	require.False(t, c2.Complete)
	require.Equal(t, partial.Missing, c2.MissingCategories)
	require.Equal(t, 1, c2.UnknownRecords)

	// The estimate is never written back into Invoice, on any path.
	require.Nil(t, lower.Records[0].Invoice)
	require.Nil(t, full.Records[0].Invoice)
}

// TestRequestLedger_Estimate_SubscriptionStaysAnEstimate: a subscription allowance is not a
// per-token cash charge (§5.1). Pricing one yields a list-price-equivalent figure that is still
// labelled an estimate and never an invoice.
func TestRequestLedger_Estimate_SubscriptionStaysAnEstimate(t *testing.T) {
	s := testSchedule()
	sub := eval.RequestRecord{
		ID: "req-sub", Kind: eval.KindTurn, Provider: s.Provider, Model: s.Model,
		RateTableDate: s.Date, PricingMode: eval.PricingSubscription,
		Reported: map[eval.UsageCategory]eval.TokenCount{
			eval.CategoryInput: eval.KnownTokens(1_000_000),
		},
		Missing: []eval.UsageCategory{
			eval.CategoryOutput, eval.CategoryCacheRead, eval.CategoryCacheWrite5m,
			eval.CategoryCacheWrite1h, eval.CategoryThinkingOutput,
		},
	}
	l := eval.RequestLedger{Version: 1, Records: []eval.RequestRecord{sub}, Schedule: &s}
	require.NoError(t, l.Validate())

	amount, c := l.Estimate(s)
	require.Equal(t, int64(3_000_000), amount.Micros)
	require.False(t, c.Complete)
	require.Nil(t, l.Records[0].Invoice, "a subscription is never reconciled into an invoice by estimation")
}

// TestRequestLedger_Estimate_DerivesCacheRatesFromMultipliers: the three cache categories have no
// per-million entry of their own, so they are priced as the input rate times the schedule's
// multiplier — which is where the (TTL, w) pair actually bites: a five-minute write and a one-hour
// write of the same size cost different amounts.
func TestRequestLedger_Estimate_DerivesCacheRatesFromMultipliers(t *testing.T) {
	s := testSchedule()

	price := func(cat eval.UsageCategory) int64 {
		r := eval.RequestRecord{
			ID: "req-1", Kind: eval.KindTurn, Provider: s.Provider, Model: s.Model,
			RateTableDate: s.Date, PricingMode: eval.PricingEstimate,
			Reported: map[eval.UsageCategory]eval.TokenCount{cat: eval.KnownTokens(1_000_000)},
		}
		for _, c := range eval.UsageCategories() {
			if c != cat {
				r.Missing = append(r.Missing, c)
			}
		}
		l := eval.RequestLedger{Version: 1, Records: []eval.RequestRecord{r}, Schedule: &s}
		require.NoError(t, l.Validate())
		amount, _ := l.Estimate(s)
		return amount.Micros
	}

	input := price(eval.CategoryInput)
	require.Equal(t, int64(3_000_000), input)
	require.Equal(t, int64(300_000), price(eval.CategoryCacheRead))
	require.Equal(t, int64(3_750_000), price(eval.CategoryCacheWrite5m))
	require.Equal(t, int64(6_000_000), price(eval.CategoryCacheWrite1h))
	require.Greater(t, price(eval.CategoryCacheWrite1h), price(eval.CategoryCacheWrite5m),
		"w is a (TTL, w) pair: the one-hour write is dearer than the five-minute one")
}

// TestRequestLedger_Estimate_MissingRateIsNotAZeroPrice: a category with neither a per-million
// entry nor a derivable multiplier is unpriceable. It is reported as missing, not priced at zero.
func TestRequestLedger_Estimate_MissingRateIsNotAZeroPrice(t *testing.T) {
	s := testSchedule()
	delete(s.PerMillion, eval.CategoryOutput)

	r := eval.RequestRecord{
		ID: "req-1", Kind: eval.KindTurn, Provider: s.Provider, Model: s.Model,
		RateTableDate: s.Date, PricingMode: eval.PricingEstimate,
		Reported: map[eval.UsageCategory]eval.TokenCount{
			eval.CategoryOutput: eval.KnownTokens(1_000_000),
		},
		Missing: []eval.UsageCategory{
			eval.CategoryInput, eval.CategoryCacheRead, eval.CategoryCacheWrite5m,
			eval.CategoryCacheWrite1h, eval.CategoryThinkingOutput,
		},
	}
	l := eval.RequestLedger{Version: 1, Records: []eval.RequestRecord{r}, Schedule: &s}

	amount, c := l.Estimate(s)
	require.Zero(t, amount.Micros)
	require.False(t, c.Complete)
	require.Contains(t, c.MissingCategories, eval.CategoryOutput)
}

// TestMoney_IsIntegerMicros: no float money. A repeated 0.1 in binary floating point does not add
// up, and a cost ledger that drifts is worse than none.
func TestMoney_IsIntegerMicros(t *testing.T) {
	f := reflect.TypeOf(eval.Money{}).Field(0)
	require.Equal(t, "Micros", f.Name)
	require.Equal(t, reflect.Int64, f.Type.Kind())

	total := eval.Money{Currency: "USD"}
	for range 10 {
		var err error
		total, err = total.Add(eval.Money{Micros: 100_000, Currency: "USD"})
		require.NoError(t, err)
	}
	require.Equal(t, int64(1_000_000), total.Micros)

	_, err := total.Add(eval.Money{Micros: 1, Currency: "EUR"})
	require.ErrorContains(t, err, "currency")
}

// ── the §5.6 conditional cache illustration ─────────────────────────────────────────────────────

// TestConditionalCacheCost_IsNVersusWritePlusHits pins §5.6's arithmetic: N identical uses cost N
// uncached, and w + (N−1)·r with one write and N−1 hits.
func TestConditionalCacheCost_IsNVersusWritePlusHits(t *testing.T) {
	cache := config.Defaults().Scheduler.Cache
	w, r := cache.WriteMultiplier, cache.ReadMultiplier

	uncached, cached := eval.ConditionalCacheCost(2, w, r)
	require.InDelta(t, 2.0, uncached, 1e-9)
	require.InDelta(t, 1.35, cached, 1e-9, "§5.6: with w=1.25 and r=0.1, two uses cost 1.35 rather than 2")

	uncached, cached = eval.ConditionalCacheCost(1, w, r)
	require.InDelta(t, 1.0, uncached, 1e-9)
	require.InDelta(t, w, cached, 1e-9, "one use is one write and no hit")

	uncached, cached = eval.ConditionalCacheCost(0, w, r)
	require.Zero(t, uncached)
	require.Zero(t, cached, "no uses is no request, not a free write")
}

// TestCacheBreakEvenReads_IsNotWOverR is the correction §5.6 exists to make. The ordinary
// break-even is the smallest N with w + (N−1)r < N — 2 at the five-minute TTL and 3 at the one
// hour — and NOT w/r, which would say 12.5 and 20.
func TestCacheBreakEvenReads_IsNotWOverR(t *testing.T) {
	cache := config.Defaults().Scheduler.Cache
	r := cache.ReadMultiplier

	require.Equal(t, 2, eval.CacheBreakEvenReads(cache.WriteMultiplier, r))
	require.Equal(t, 3, eval.CacheBreakEvenReads(oneHourWriteMultiplier, r))

	require.NotEqual(t, int(cache.WriteMultiplier/r), eval.CacheBreakEvenReads(cache.WriteMultiplier, r),
		"w/r is 12.5 here and is not the break-even; §5.6 retires that rule")
	require.NotEqual(t, int(oneHourWriteMultiplier/r), eval.CacheBreakEvenReads(oneHourWriteMultiplier, r),
		"w/r is 20 at the one-hour TTL and is not the break-even either")

	// w = 3.7 is the case that forbids reintroducing the bare closed form: the exact root
	// (w−r)/(1−r) is 4, which floating point lands just below, so floor(x)+1 says 4 where the
	// arithmetic the caller actually sees says 5.
	require.Equal(t, 5, eval.CacheBreakEvenReads(3.7, r))
	uncached, cached := eval.ConditionalCacheCost(4, 3.7, r)
	require.GreaterOrEqual(t, cached, uncached, "at N=4 caching is not yet cheaper, to the last bit")

	// Cross-check the closed form against a brute-force search over both regimes.
	for _, w := range []float64{cache.WriteMultiplier, oneHourWriteMultiplier, 1.0, 3.7, 0.5} {
		want := 0
		for n := 1; n <= 1000; n++ {
			uncached, cached := eval.ConditionalCacheCost(n, w, r)
			if cached < uncached {
				want = n
				break
			}
		}
		require.Equal(t, want, eval.CacheBreakEvenReads(w, r), "w=%v", w)
	}

	// r ≥ 1 means a hit is never cheaper than a fresh read, so no N works and 0 says so.
	require.Zero(t, eval.CacheBreakEvenReads(1.25, 1.0))
	require.Zero(t, eval.CacheBreakEvenReads(1.25, math.NaN()))
}

// TestRateSchedule_Validate: a rate with no provenance is not a rate. §5.1 says configured rates
// are assumptions and that no rate is current merely because a document printed it, so a schedule
// that will not say where its numbers came from is refused outright.
func TestRateSchedule_Validate(t *testing.T) {
	require.NoError(t, testSchedule().Validate())

	cases := []struct {
		name string
		want string
		fn   func(s *eval.RateSchedule)
	}{
		{"no provider", "provider", func(s *eval.RateSchedule) { s.Provider = "" }},
		{"no model", "model", func(s *eval.RateSchedule) { s.Model = "" }},
		{"no date", "date", func(s *eval.RateSchedule) { s.Date = "" }},
		{"no source", "source", func(s *eval.RateSchedule) { s.Source = " " }},
		{"zero read multiplier", "cache read multiplier", func(s *eval.RateSchedule) { s.CacheReadMultiplier = 0 }},
		{"negative 5m multiplier", "cache write 5m", func(s *eval.RateSchedule) { s.CacheWrite5mMultiplier = -1 }},
		{"infinite 1h multiplier", "cache write 1h", func(s *eval.RateSchedule) {
			s.CacheWrite1hMultiplier = math.Inf(1)
		}},
		{"no currency", "currency", func(s *eval.RateSchedule) {
			s.PerMillion[eval.CategoryInput] = eval.Money{Micros: 1}
		}},
		{"negative price", "micros", func(s *eval.RateSchedule) {
			s.PerMillion[eval.CategoryInput] = eval.Money{Micros: -1, Currency: "USD"}
		}},
		{"mixed currencies", "currenc", func(s *eval.RateSchedule) {
			s.PerMillion[eval.CategoryOutput] = eval.Money{Micros: 1, Currency: "EUR"}
		}},
		{"unknown category priced", "category", func(s *eval.RateSchedule) {
			s.PerMillion["speculative_decode"] = eval.Money{Micros: 1, Currency: "USD"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testSchedule()
			tc.fn(&s)
			require.ErrorContains(t, s.Validate(), tc.want)
		})
	}

	t.Run("a ledger validates its schedule", func(t *testing.T) {
		s := testSchedule()
		s.Source = ""
		l := eval.RequestLedger{Version: 1, Records: []eval.RequestRecord{validRecord()}, Schedule: &s}
		require.ErrorContains(t, l.Validate(), "source")
	})

	t.Run("a multiplier-only schedule is legitimate", func(t *testing.T) {
		s := testSchedule()
		s.PerMillion = map[eval.UsageCategory]eval.Money{}
		require.NoError(t, s.Validate())
		_, ok := s.RateFor(eval.CategoryCacheRead)
		require.False(t, ok, "a cache rate cannot be derived without an input rate to derive it from")
	})
}

// TestLoadRequestLedger_Errors: a missing or malformed artifact is an error, never an empty ledger
// that would read as a run with nothing to report.
func TestLoadRequestLedger_Errors(t *testing.T) {
	_, err := eval.LoadRequestLedger(filepath.Join(t.TempDir(), "absent.json"))
	require.ErrorContains(t, err, "reading the request ledger")

	bad := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte("{"), 0o600))
	_, err = eval.LoadRequestLedger(bad)
	require.ErrorContains(t, err, "parsing the request ledger")
}

// TestMoney_AddAcrossEmptyAccumulators: a zero-value Money is an empty accumulator and adopts the
// other side's currency, so summing a list does not need a special first iteration.
func TestMoney_AddAcrossEmptyAccumulators(t *testing.T) {
	usd := eval.Money{Micros: 5, Currency: "USD"}

	got, err := eval.Money{}.Add(usd)
	require.NoError(t, err)
	require.Equal(t, usd, got)

	got, err = usd.Add(eval.Money{})
	require.NoError(t, err)
	require.Equal(t, usd, got)

	// A zero amount that names a currency is still a currency claim, and mismatches.
	_, err = usd.Add(eval.Money{Currency: "EUR"})
	require.ErrorContains(t, err, "currency")
}

// TestRequestRecord_ValidateMoney: every amount has to name a currency, including the charges.
func TestRequestRecord_ValidateMoney(t *testing.T) {
	cases := []struct {
		name string
		want string
		fn   func(r *eval.RequestRecord)
	}{
		{"estimate without currency", "currency", func(r *eval.RequestRecord) {
			r.Estimate = &eval.Money{Micros: 1}
		}},
		{"invoice without currency", "currency", func(r *eval.RequestRecord) {
			r.PricingMode = eval.PricingInvoice
			r.Invoice = &eval.Money{Micros: 1}
		}},
		{"unnamed charge", "unnamed non-token charge", func(r *eval.RequestRecord) {
			r.NonTokenCharges = []eval.Charge{{Amount: eval.Money{Micros: 1, Currency: "USD"}}}
		}},
		{"charge without currency", "currency", func(r *eval.RequestRecord) {
			r.NonTokenCharges = []eval.Charge{{Kind: "web_search", Amount: eval.Money{Micros: 1}}}
		}},
		{"unknown category listed as missing", "category", func(r *eval.RequestRecord) {
			r.Missing = append(r.Missing, "speculative_decode")
		}},
		{"negative token volume", "tokens", func(r *eval.RequestRecord) {
			r.Reported[eval.CategoryInput] = eval.KnownTokens(-1)
		}},
		{"no provider", "provider", func(r *eval.RequestRecord) { r.Provider = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validRecord()
			tc.fn(&r)
			require.ErrorContains(t, r.Validate(), tc.want)
		})
	}
}

// TestRateSchedule_RateForRejectsAnUnknownCategory: an unrecognised category is unpriceable, not
// free.
func TestRateSchedule_RateForRejectsAnUnknownCategory(t *testing.T) {
	_, ok := testSchedule().RateFor("speculative_decode")
	require.False(t, ok)
}

// TestCacheBreakEvenReads_DegenerateInputs: non-finite inputs and a break-even beyond any practical
// reuse count both answer 0 rather than a number nobody should act on.
func TestCacheBreakEvenReads_DegenerateInputs(t *testing.T) {
	r := config.Defaults().Scheduler.Cache.ReadMultiplier
	require.Zero(t, eval.CacheBreakEvenReads(math.Inf(1), r))
	require.Zero(t, eval.CacheBreakEvenReads(1.25, math.Inf(-1)))
	require.Zero(t, eval.CacheBreakEvenReads(math.NaN(), r))
	require.Zero(t, eval.CacheBreakEvenReads(1e12, r), "no practical break-even")
}

// mustMarshal renders v with the package's plain encoder.
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return raw
}
