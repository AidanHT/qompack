package eval

// ── Live rates: a dated, multi-model list-price table ───────────────────────────────────────────
//
// V6 close-out C5.4. A live trial can touch more than one model (the host runs helper requests on
// a smaller model than the session's), while RateSchedule (ledger.go) prices exactly one. The table
// below is the dated document the per-model schedules are cut from, with the provenance rule of
// ledger.go carried over unchanged: a table that does not say where its numbers came from and when
// is refused. Every figure priced from it is a list-price-equivalent ESTIMATE — the trials run on
// a subscription, which has no per-token cash charge — and it is always reported beside the host's
// own client-side estimate (total_cost_usd), never merged with it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// LiveRateTableVersion is the rate-table format version ParseLiveRateTable accepts.
const LiveRateTableVersion = 1

// liveRateProvider is the provider every live-table schedule is for.
const liveRateProvider = "anthropic"

// LiveRateTable is a dated list-price table keyed by canonical model ID.
type LiveRateTable struct {
	Version int `json:"version"`
	// Date is the rate-table date (YYYY-MM-DD).
	Date string `json:"date"`
	// Source names the documents the numbers came from and when they were read.
	Source string `json:"source"`
	// Currency is the ISO-style code every price is in.
	Currency string `json:"currency"`
	// Models maps a canonical model ID (see CanonicalModel) to its prices.
	Models map[string]LiveModelRate `json:"models"`
}

// LiveModelRate is one model's list prices. Prices are integer micros of the table's currency per
// million tokens; the multipliers apply to the input price, as ledger.go's RateSchedule does.
type LiveModelRate struct {
	InputPerMTokMicros     int64   `json:"input_per_mtok_micros"`
	OutputPerMTokMicros    int64   `json:"output_per_mtok_micros"`
	CacheReadMultiplier    float64 `json:"cache_read_multiplier"`
	CacheWrite5mMultiplier float64 `json:"cache_write_5m_multiplier"`
	CacheWrite1hMultiplier float64 `json:"cache_write_1h_multiplier"`
}

// ParseLiveRateTable decodes and validates a rate table. Unknown fields are refused, so a misspelt
// key cannot silently leave a price unset.
func ParseLiveRateTable(raw []byte) (LiveRateTable, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var t LiveRateTable
	if err := dec.Decode(&t); err != nil {
		return LiveRateTable{}, fmt.Errorf("eval: parsing the live rate table: %w", err)
	}
	if err := t.Validate(); err != nil {
		return LiveRateTable{}, err
	}
	return t, nil
}

// Validate checks the table's provenance and every model's schedule.
func (t LiveRateTable) Validate() error {
	if t.Version != LiveRateTableVersion {
		return fmt.Errorf("eval: live rate table version %d, want %d", t.Version, LiveRateTableVersion)
	}
	if len(t.Models) == 0 {
		return fmt.Errorf("eval: live rate table prices no model")
	}
	for _, m := range t.modelIDs() {
		if CanonicalModel(m) != m {
			return fmt.Errorf("eval: live rate table key %q is not canonical (want %q)", m, CanonicalModel(m))
		}
		s, _ := t.Schedule(m)
		if err := s.Validate(); err != nil {
			return fmt.Errorf("eval: live rate table model %s: %w", m, err)
		}
		r := t.Models[m]
		if r.InputPerMTokMicros <= 0 || r.OutputPerMTokMicros <= 0 {
			return fmt.Errorf("eval: live rate table model %s has a non-positive input or output price", m)
		}
	}
	if strings.TrimSpace(t.Currency) == "" {
		return fmt.Errorf("eval: live rate table names no currency")
	}
	return nil
}

func (t LiveRateTable) modelIDs() []string {
	out := make([]string, 0, len(t.Models))
	for m := range t.Models {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// datedModelSuffix and contextTag are what CanonicalModel strips: a snapshot date
// ("-20251001") and a context-window tag ("[1m]").
var (
	datedModelSuffix = regexp.MustCompile(`-\d{8}$`)
	contextTag       = regexp.MustCompile(`\[[^\]]*\]$`)
)

// CanonicalModel reduces a host model ID to the key a rate table uses: lowercased, with any
// context-window tag and snapshot-date suffix removed. "claude-haiku-4-5-20251001" and
// "claude-opus-5-5[1m]" become "claude-haiku-4-5" and "claude-opus-5-5".
func CanonicalModel(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	id = contextTag.ReplaceAllString(id, "")
	return datedModelSuffix.ReplaceAllString(id, "")
}

// Schedule returns the RateSchedule for model, or false when the table does not price it.
// Thinking output is priced at the output rate: it is output the provider bills as output.
func (t LiveRateTable) Schedule(model string) (RateSchedule, bool) {
	key := CanonicalModel(model)
	r, ok := t.Models[key]
	if !ok {
		return RateSchedule{}, false
	}
	out := Money{Micros: r.OutputPerMTokMicros, Currency: t.Currency}
	return RateSchedule{
		Provider: liveRateProvider,
		Model:    key,
		Date:     t.Date,
		Source:   t.Source,
		PerMillion: map[UsageCategory]Money{
			CategoryInput:          {Micros: r.InputPerMTokMicros, Currency: t.Currency},
			CategoryOutput:         out,
			CategoryThinkingOutput: out,
		},
		CacheReadMultiplier:    r.CacheReadMultiplier,
		CacheWrite5mMultiplier: r.CacheWrite5mMultiplier,
		CacheWrite1hMultiplier: r.CacheWrite1hMultiplier,
	}, true
}

// LiveEstimate is a multi-model ledger priced against a live rate table.
type LiveEstimate struct {
	// Total is the estimate: a LOWER BOUND whenever Completeness says it is incomplete.
	Total Money `json:"total"`
	// Completeness merges every model's completeness: the union of missing categories, the sum
	// of records with an unknown category, and incomplete whenever any model was unpriceable.
	Completeness Completeness `json:"completeness"`
	// ByModel is each model's own estimate.
	ByModel map[string]Money `json:"by_model"`
	// Unpriced names every model the table does not price; its records contribute nothing.
	Unpriced []string `json:"unpriced,omitempty"`
	// RateTableDate is the table's date.
	RateTableDate string `json:"rate_table_date"`
}

// EstimateLive prices l model by model against t.
//
// Each model's records are priced by RequestLedger.Estimate under that model's schedule, so the
// arithmetic, the rounding and the unknown-is-not-zero rule are exactly ledger.go's. A record
// whose model the table does not price is named in Unpriced and makes the estimate incomplete.
func EstimateLive(l RequestLedger, t LiveRateTable) LiveEstimate {
	out := LiveEstimate{
		Total:         Money{Currency: t.Currency},
		ByModel:       map[string]Money{},
		RateTableDate: t.Date,
		Completeness:  Completeness{Complete: true},
	}
	byModel := map[string][]RequestRecord{}
	for _, r := range l.Records {
		byModel[r.Model] = append(byModel[r.Model], r)
	}
	models := make([]string, 0, len(byModel))
	for m := range byModel {
		models = append(models, m)
	}
	sort.Strings(models)

	missing := map[UsageCategory]bool{}
	for _, m := range models {
		recs := byModel[m]
		sched, ok := t.Schedule(m)
		if !ok {
			out.Unpriced = append(out.Unpriced, m)
			out.Completeness.Complete = false
			for _, c := range usageCategories {
				missing[c] = true
			}
			for _, r := range recs {
				if len(r.derivedMissing()) > 0 {
					out.Completeness.UnknownRecords++
				}
			}
			continue
		}
		money, comp := RequestLedger{Version: RequestLedgerVersion, Records: recs}.Estimate(sched)
		out.ByModel[m] = money
		out.Total.Micros += money.Micros
		out.Completeness.UnknownRecords += comp.UnknownRecords
		if !comp.Complete {
			out.Completeness.Complete = false
		}
		for _, c := range comp.MissingCategories {
			missing[c] = true
		}
	}
	for _, c := range usageCategories {
		if missing[c] {
			out.Completeness.MissingCategories = append(out.Completeness.MissingCategories, c)
		}
	}
	return out
}

// MicrosToUnits converts integer micros to a float amount for display only; every sum is taken in
// micros.
func MicrosToUnits(micros int64) float64 {
	const perUnit = 1e6
	return math.Round(float64(micros)) / perUnit
}
