package eval

// ── The request usage ledger: a versioned ON-DISK artifact ──────────────────────────────────────
//
// SP-19 commit 5. Everything in this file, and in provenance.go beside it, is a versioned artifact
// format written to and read from disk (testdata/baseline/phase0.ledger.v1.json, the frozen
// fixture under testdata/golden/eval/ledger/). Its JSON keys are therefore snake_case and its
// Version field is part of the contract. That is deliberately DIFFERENT from the in-memory
// 00-ARCHITECTURE.md §5.18 shapes in types.go — Report, Score, Divergence, Run — which are frozen
// camelCase wire shapes this file does not touch, extend or reinterpret.
//
// The ledger sits BESIDE the historical metrics, never on top of them (M0-04): old outputs keep
// their original definitions and provenance while corrected accounting is added separately
// versioned.
//
// Three rules govern every line below, from Qompack.md v1.5 §5.1–§5.4 and §11.2:
//
//   - Missing usage is unknown, NEVER zero. TokenCount makes an unknown count unrepresentable as a
//     zero one, Add propagates unknown, and Sum reports how many records left a category unknown
//     rather than quietly totalling the ones that did not.
//   - An estimate is not an invoice. Estimate is always labelled with its Completeness, is a lower
//     bound whenever anything is unknown, and is never written into RequestRecord.Invoice — which
//     only a record under invoice pricing may carry at all.
//   - Rates and multipliers are data with provenance, never literals. RateSchedule transports r
//     and the (TTL, w) pair with the document they came from; ConditionalCacheCost takes them as
//     parameters. No cache multiplier is spelled anywhere in this package's production code
//     (D11, 00-ARCHITECTURE.md §11.6).

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"slices"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// RequestLedgerVersion is the format version LoadRequestLedger accepts and Validate requires.
const RequestLedgerVersion = 1

// UsageCategory is one separately-reported provider usage category.
//
// §5.3 requires uncached input, cache reads, cache writes BY TTL, output and child/compaction work
// to be separable: a single "tokens" number cannot be reconciled against a rate schedule or an
// invoice, and collapsing the two cache-write TTLs hides the whole point of w being a pair.
type UsageCategory string

// The six usage categories, in canonical order.
const (
	// CategoryInput is uncached input.
	CategoryInput UsageCategory = "input"
	// CategoryOutput is generated output.
	CategoryOutput UsageCategory = "output"
	// CategoryCacheRead is a prompt-cache hit, priced at r.
	CategoryCacheRead UsageCategory = "cache_read"
	// CategoryCacheWrite5m is a prompt-cache write at the five-minute TTL, priced at w(5m).
	CategoryCacheWrite5m UsageCategory = "cache_write_5m"
	// CategoryCacheWrite1h is a prompt-cache write at the one-hour TTL, priced at w(1h). It is a
	// separate category and not a scaled CategoryCacheWrite5m, because w is a (TTL, w) pair.
	CategoryCacheWrite1h UsageCategory = "cache_write_1h"
	// CategoryThinkingOutput is the extended-thinking output a request produced. The native
	// summarization request inherits extended thinking, so it is output-priced work whose volume
	// the host does not publish — which is why it is a category that is usually unknown rather
	// than a number that is usually zero.
	CategoryThinkingOutput UsageCategory = "thinking_output"
)

// usageCategories is the canonical order, and the single source of truth for it.
var usageCategories = []UsageCategory{
	CategoryInput,
	CategoryOutput,
	CategoryCacheRead,
	CategoryCacheWrite5m,
	CategoryCacheWrite1h,
	CategoryThinkingOutput,
}

// UsageCategories returns the six usage categories in canonical order.
//
// The order is the order RequestRecord.Missing must be written in and the order Sum, Estimate and
// every diagnostic list use, so two ledgers of the same run are byte-comparable.
func UsageCategories() []UsageCategory {
	out := make([]UsageCategory, len(usageCategories))
	copy(out, usageCategories)
	return out
}

// knownCategory reports whether c is one of the six.
func knownCategory(c UsageCategory) bool {
	for _, k := range usageCategories {
		if k == c {
			return true
		}
	}
	return false
}

// TokenCount is a token volume that may not be known.
//
// The zero value is UNKNOWN, not zero, and that is the whole design: a struct whose zero value was
// an indistinguishable 0 is exactly how "missing usage is unknown, never zero" gets broken by
// accident. An unknown count marshals as {"known":false}; a known zero marshals as {"known":true}.
type TokenCount struct {
	// Known reports whether Value means anything.
	Known bool `json:"known"`
	// Value is the token volume, and is meaningless unless Known.
	Value core.Tokens `json:"value,omitempty"`
}

// KnownTokens is a reported count of v tokens.
func KnownTokens(v core.Tokens) TokenCount { return TokenCount{Known: true, Value: v} }

// UnknownTokens is a count the provider did not report.
func UnknownTokens() TokenCount { return TokenCount{} }

// Add sums two counts. An unknown summand yields an unknown result carrying no value at all, so a
// partial total can never be read as a complete one.
func (t TokenCount) Add(o TokenCount) TokenCount {
	if !t.Known || !o.Known {
		return UnknownTokens()
	}
	return KnownTokens(t.Value + o.Value)
}

// PricingMode records which of §5.1's three separate sources a record's money came from.
type PricingMode string

// The four pricing modes.
const (
	// PricingSubscription is allowance-covered work. It is not automatically a per-token cash
	// charge, so anything priced from it is list-price-equivalent and still an estimate.
	PricingSubscription PricingMode = "subscription"
	// PricingEstimate is a configured-rate assumption.
	PricingEstimate PricingMode = "estimate"
	// PricingInvoice is a reconciled provider invoice: a separate observation source, never
	// derived from an estimate.
	PricingInvoice PricingMode = "invoice"
	// PricingNone is work no rate was applied to at all, such as a deterministic replay that made
	// no model call.
	PricingNone PricingMode = "none"
)

// knownPricingModes is the accepted set.
var knownPricingModes = []PricingMode{PricingSubscription, PricingEstimate, PricingInvoice, PricingNone}

// RequestKind classifies what a request was, so retries, aborts, compaction and child-agent work
// stay attributed rather than being folded into an undifferentiated total (§11.2).
type RequestKind string

// The six request kinds.
const (
	// KindTurn is an ordinary conversational turn.
	KindTurn RequestKind = "turn"
	// KindRetry is a re-issued request after a failure.
	KindRetry RequestKind = "retry"
	// KindAbort is a request that was billed but did not complete.
	KindAbort RequestKind = "abort"
	// KindCompaction is a summarization request. It carries the conversation history, so it is
	// never free and never O(delta).
	KindCompaction RequestKind = "compaction"
	// KindChild is a subagent's request, attributed to its parent through ParentID.
	KindChild RequestKind = "child"
	// KindNonToken is a charge with no token volume at all, such as a tool surcharge or an
	// invoice-reconciliation entry.
	KindNonToken RequestKind = "non_token"
)

// knownRequestKinds is the accepted set.
var knownRequestKinds = []RequestKind{
	KindTurn, KindRetry, KindAbort, KindCompaction, KindChild, KindNonToken,
}

// Money is an exact amount in integer micros of its Currency.
//
// It is integer because a cost ledger accumulated in float64 drifts, and a total that does not
// reproduce is one nobody can reconcile against an invoice.
type Money struct {
	// Micros is the amount, in millionths of one unit of Currency.
	Micros int64 `json:"micros"`
	// Currency is the ISO-style currency code the amount is denominated in.
	Currency string `json:"currency"`
}

// Add sums two amounts, refusing to add across currencies. A zero-Micros amount with no currency
// is treated as an empty accumulator and adopts the other side's currency.
func (m Money) Add(o Money) (Money, error) {
	switch {
	case m.Currency == "" && m.Micros == 0:
		return o, nil
	case o.Currency == "" && o.Micros == 0:
		return m, nil
	case m.Currency != o.Currency:
		return Money{}, fmt.Errorf("eval: cannot add %s to %s: currency mismatch", o.Currency, m.Currency)
	}
	return Money{Micros: m.Micros + o.Micros, Currency: m.Currency}, nil
}

// Charge is one non-token charge, such as a tool surcharge.
type Charge struct {
	// Kind names what the charge was for.
	Kind string `json:"kind"`
	// Amount is the charge.
	Amount Money `json:"amount"`
}

// RequestRecord is one request's usage and attribution.
//
// Reported and Missing are two views of the same fact and Validate refuses to let them disagree:
// Missing must be exactly the categories that Reported omits or reports with Known false, in
// UsageCategories order. Without that rule a reader cannot tell "we asked and got nothing" from
// "we never looked", which is the distinction the whole ledger exists to preserve.
type RequestRecord struct {
	// ID identifies this request within its ledger.
	ID string `json:"id"`
	// ParentID attributes this request to another record in the same ledger: a retry to its
	// original, a child agent's work to the turn that spawned it. It must resolve.
	ParentID string `json:"parent_id,omitempty"`
	// Kind classifies the request.
	Kind RequestKind `json:"kind"`
	// Provider names the provider the request went to, or states that none did.
	Provider string `json:"provider"`
	// Model names the model, and may be empty when no model was called.
	Model string `json:"model"`
	// RateTableDate is the date of the rate schedule in force at measurement time, and may be
	// empty when no rate was applied.
	RateTableDate string `json:"rate_table_date"`
	// PricingMode records which source this record's money came from.
	PricingMode PricingMode `json:"pricing_mode"`
	// Reported is what the provider actually told us, per category.
	Reported map[UsageCategory]TokenCount `json:"reported"`
	// Missing is every category this record has no volume for, in UsageCategories order.
	Missing []UsageCategory `json:"missing"`
	// Estimate is a computed price. It is an assumption, and it is never copied into Invoice.
	Estimate *Money `json:"estimate,omitempty"`
	// Invoice is a reconciled charge, and may be set only under PricingInvoice.
	Invoice *Money `json:"invoice,omitempty"`
	// NonTokenCharges lists charges with no token volume.
	NonTokenCharges []Charge `json:"non_token_charges,omitempty"`
	// Retries counts how many times this request was re-issued.
	Retries int `json:"retries"`
	// Aborted records that the request did not complete. An aborted request is still billed.
	Aborted bool `json:"aborted"`
	// Notes carries the provenance a number alone cannot: what was measured, and what was not.
	Notes string `json:"notes,omitempty"`
}

// derivedMissing returns the categories this record has no known volume for, in canonical order.
func (r RequestRecord) derivedMissing() []UsageCategory {
	var out []UsageCategory
	for _, c := range usageCategories {
		if tc, ok := r.Reported[c]; !ok || !tc.Known {
			out = append(out, c)
		}
	}
	return out
}

// Validate checks every invariant a record can check without seeing its ledger. Cross-record rules
// — duplicate identifiers and ParentID resolution — belong to RequestLedger.Validate.
func (r RequestRecord) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("eval: request record has no id")
	}
	if r.ParentID == r.ID {
		return fmt.Errorf("eval: request %s names itself as its parent", r.ID)
	}
	if !containsKind(knownRequestKinds, r.Kind) {
		return fmt.Errorf("eval: request %s has unknown kind %q", r.ID, r.Kind)
	}
	if !containsMode(knownPricingModes, r.PricingMode) {
		return fmt.Errorf("eval: request %s has unknown pricing mode %q", r.ID, r.PricingMode)
	}
	if r.Provider == "" {
		return fmt.Errorf("eval: request %s names no provider; state %q rather than leaving it blank", r.ID, "none")
	}
	if r.Retries < 0 {
		return fmt.Errorf("eval: request %s has negative retries %d", r.ID, r.Retries)
	}
	if err := r.validateCategories(); err != nil {
		return err
	}
	if r.Invoice != nil && r.PricingMode != PricingInvoice {
		return fmt.Errorf(
			"eval: request %s carries an invoice under pricing mode %q; only %q may, and an estimate is never reconciled into one",
			r.ID, r.PricingMode, PricingInvoice)
	}
	return r.validateMoney()
}

// validateCategories enforces the two-views rule over Reported and Missing. Reported is walked in
// sorted order, as is every map a validator here ranges over, so one malformed input draws the same
// error on every run (D53(a)).
func (r RequestRecord) validateCategories() error {
	for _, c := range slices.Sorted(maps.Keys(r.Reported)) {
		tc := r.Reported[c]
		if !knownCategory(c) {
			return fmt.Errorf("eval: request %s reports unknown usage category %q", r.ID, c)
		}
		if tc.Known && tc.Value < 0 {
			return fmt.Errorf("eval: request %s reports %d tokens for category %q", r.ID, tc.Value, c)
		}
	}
	seen := map[UsageCategory]bool{}
	for _, c := range r.Missing {
		if !knownCategory(c) {
			return fmt.Errorf("eval: request %s lists unknown usage category %q as missing", r.ID, c)
		}
		if seen[c] {
			return fmt.Errorf("eval: request %s lists %q as missing twice", r.ID, c)
		}
		seen[c] = true
	}

	want := r.derivedMissing()
	if len(want) != len(r.Missing) {
		return fmt.Errorf(
			"eval: request %s declares %v missing but its reported categories make %v missing; the two views may not disagree",
			r.ID, r.Missing, want)
	}
	for _, c := range want {
		if !seen[c] {
			return fmt.Errorf(
				"eval: request %s does not list %q as missing, but reports no known volume for it; the two views may not disagree",
				r.ID, c)
		}
	}
	for i, c := range want {
		if r.Missing[i] != c {
			return fmt.Errorf(
				"eval: request %s lists its missing categories out of order: got %v, want %v (UsageCategories order)",
				r.ID, r.Missing, want)
		}
	}
	return nil
}

// validateMoney checks that every amount names a currency.
func (r RequestRecord) validateMoney() error {
	for _, a := range [...]struct {
		name string
		m    *Money
	}{{"estimate", r.Estimate}, {"invoice", r.Invoice}} {
		name, m := a.name, a.m
		if m != nil && m.Currency == "" {
			return fmt.Errorf("eval: request %s has an %s with no currency", r.ID, name)
		}
	}
	for i, c := range r.NonTokenCharges {
		if c.Kind == "" {
			return fmt.Errorf("eval: request %s has an unnamed non-token charge at index %d", r.ID, i)
		}
		if c.Amount.Currency == "" {
			return fmt.Errorf("eval: request %s has a non-token charge %q with no currency", r.ID, c.Kind)
		}
	}
	return nil
}

// RateSchedule is a dated price list for one provider and model, carrying its own provenance.
//
// §5.1: configured rates are assumptions, and no rate is current merely because a document printed
// it. Source therefore names the document and date the numbers came from, and Validate refuses a
// schedule that does not say.
type RateSchedule struct {
	// Provider names the provider these rates are for.
	Provider string `json:"provider"`
	// Model names the model these rates are for.
	Model string `json:"model"`
	// Date is the rate-table date.
	Date string `json:"date"`
	// Source names the document, and the date it was read, that these numbers came from.
	Source string `json:"source"`
	// PerMillion is the price of one million tokens of a category. A category with no entry is
	// derived from CategoryInput and the matching cache multiplier, if it is a cache category,
	// and is otherwise unpriceable — which Estimate reports rather than pricing at zero.
	PerMillion map[UsageCategory]Money `json:"per_million"`
	// CacheReadMultiplier is r.
	CacheReadMultiplier float64 `json:"cache_read_multiplier"`
	// CacheWrite5mMultiplier is w at the five-minute TTL.
	CacheWrite5mMultiplier float64 `json:"cache_write_5m_multiplier"`
	// CacheWrite1hMultiplier is w at the one-hour TTL. It is carried separately because w is a
	// (TTL, w) pair, not a scalar.
	CacheWrite1hMultiplier float64 `json:"cache_write_1h_multiplier"`
}

// Validate checks that the schedule states its provenance and carries usable multipliers.
func (s RateSchedule) Validate() error {
	for _, f := range [...]struct{ name, v string }{
		{"provider", s.Provider}, {"model", s.Model}, {"date", s.Date}, {"source", s.Source},
	} {
		name, v := f.name, f.v
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("eval: rate schedule has no %s; a rate with no provenance is not a rate", name)
		}
	}
	for _, f := range [...]struct {
		name string
		m    float64
	}{
		{"cache read multiplier", s.CacheReadMultiplier},
		{"cache write 5m multiplier", s.CacheWrite5mMultiplier},
		{"cache write 1h multiplier", s.CacheWrite1hMultiplier},
	} {
		name, m := f.name, f.m
		if !(m > 0) || math.IsInf(m, 0) {
			return fmt.Errorf("eval: rate schedule %s is %v; it must be a positive finite number", name, m)
		}
	}
	currency := ""
	for _, c := range usageCategories {
		m, ok := s.PerMillion[c]
		if !ok {
			continue
		}
		if m.Currency == "" {
			return fmt.Errorf("eval: rate schedule prices %q with no currency", c)
		}
		if m.Micros < 0 {
			return fmt.Errorf("eval: rate schedule prices %q at %d micros", c, m.Micros)
		}
		if currency == "" {
			currency = m.Currency
		} else if currency != m.Currency {
			return fmt.Errorf("eval: rate schedule mixes currencies %s and %s", currency, m.Currency)
		}
	}
	for _, c := range slices.Sorted(maps.Keys(s.PerMillion)) {
		if !knownCategory(c) {
			return fmt.Errorf("eval: rate schedule prices unknown usage category %q", c)
		}
	}
	return nil
}

// RateFor returns the price of one million tokens of c.
//
// The three cache categories are derived from CategoryInput and the matching multiplier when they
// carry no price of their own, which is where the (TTL, w) pair actually bites: a five-minute and
// a one-hour write of the same size do not cost the same. A category with neither an entry nor a
// derivation is unpriceable, and says so rather than returning zero.
func (s RateSchedule) RateFor(c UsageCategory) (Money, bool) {
	if m, ok := s.PerMillion[c]; ok {
		return m, true
	}
	var mult float64
	switch c {
	case CategoryCacheRead:
		mult = s.CacheReadMultiplier
	case CategoryCacheWrite5m:
		mult = s.CacheWrite5mMultiplier
	case CategoryCacheWrite1h:
		mult = s.CacheWrite1hMultiplier
	case CategoryInput, CategoryOutput, CategoryThinkingOutput:
		return Money{}, false
	default:
		return Money{}, false
	}
	base, ok := s.PerMillion[CategoryInput]
	if !ok || !(mult > 0) || math.IsInf(mult, 0) {
		return Money{}, false
	}
	return Money{Micros: int64(math.Round(float64(base.Micros) * mult)), Currency: base.Currency}, true
}

// CompactionMeasurement is one compaction event's four SEPARATE measurements.
//
// §11.2: "Qompack-added tokens and total observed native context are distinct". The type therefore
// offers no method at all, and in particular nothing that adds these four together — a total of
// them would be a number with no meaning, and one nobody could take back once published.
type CompactionMeasurement struct {
	// NativeCompactionInput is what the host's own summarization request consumed.
	NativeCompactionInput TokenCount `json:"native_compaction_input"`
	// QompackFrontierWork is what Qompack's own incremental checkpoint work cost.
	QompackFrontierWork TokenCount `json:"qompack_frontier_work"`
	// AddedTokens is what Qompack added to the context.
	AddedTokens TokenCount `json:"added_tokens"`
	// TotalObservedContext is the whole observed context, from a different vantage point again.
	TotalObservedContext TokenCount `json:"total_observed_context"`
}

// CategorySum is one category's total across a ledger, with the evidence behind it.
type CategorySum struct {
	// Known is the sum of the volumes that were actually reported.
	Known core.Tokens `json:"known"`
	// KnownRecords is how many records reported this category. A reported zero counts.
	KnownRecords int `json:"known_records"`
	// UnknownRecords is how many records did not.
	UnknownRecords int `json:"unknown_records"`
}

// Complete reports whether every record in the ledger supplied this category, and at least one
// did. An empty ledger reports nothing complete: no data is not complete data.
func (c CategorySum) Complete() bool { return c.UnknownRecords == 0 && c.KnownRecords > 0 }

// Completeness labels an Estimate with what it could not account for.
type Completeness struct {
	// Complete reports whether every category was priced and every record reported it.
	Complete bool `json:"complete"`
	// MissingCategories names every category that was unpriceable or unreported, in canonical
	// order.
	MissingCategories []UsageCategory `json:"missing_categories"`
	// UnknownRecords is how many records left at least one category unknown.
	UnknownRecords int `json:"unknown_records"`
}

// RequestLedger is a versioned set of request records, their rate schedule and the compaction
// measurements taken alongside them.
type RequestLedger struct {
	// Version is the artifact format version.
	Version int `json:"version"`
	// Records is every request, in the order they were made.
	Records []RequestRecord `json:"records"`
	// Schedule is the rate schedule in force, or nil when no rate was applied at all.
	Schedule *RateSchedule `json:"schedule"`
	// Compaction is the per-event compaction measurements.
	Compaction []CompactionMeasurement `json:"compaction"`
}

// LoadRequestLedger reads a ledger artifact from disk. It does not validate: a caller that wants
// the invariants enforced calls Validate, and a caller inspecting a rejected artifact does not.
func LoadRequestLedger(path string) (RequestLedger, error) {
	var l RequestLedger
	raw, err := os.ReadFile(path) //nolint:gosec // an explicitly named ledger artifact
	if err != nil {
		return l, fmt.Errorf("eval: reading the request ledger %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return l, fmt.Errorf("eval: parsing the request ledger %s: %w", path, err)
	}
	return l, nil
}

// Validate checks the ledger's version, every record, and the cross-record rules a record cannot
// check for itself.
func (l RequestLedger) Validate() error {
	if l.Version != RequestLedgerVersion {
		return fmt.Errorf("eval: request ledger version %d, want %d", l.Version, RequestLedgerVersion)
	}
	byID := make(map[string]bool, len(l.Records))
	for _, r := range l.Records {
		if err := r.Validate(); err != nil {
			return err
		}
		if byID[r.ID] {
			return fmt.Errorf("eval: request ledger has a duplicate record id %q", r.ID)
		}
		byID[r.ID] = true
	}
	for _, r := range l.Records {
		if r.ParentID != "" && !byID[r.ParentID] {
			return fmt.Errorf(
				"eval: request %s is attributed to parent %q, which this ledger does not contain",
				r.ID, r.ParentID)
		}
	}
	if l.Schedule != nil {
		return l.Schedule.Validate()
	}
	return nil
}

// Sum totals every category across the ledger.
//
// It returns an entry for every category, always, so an absent key can never be misread as a zero;
// and it counts the records that left a category unknown rather than treating them as zeros, so a
// total always travels with how much of the ledger it actually covers.
func (l RequestLedger) Sum() map[UsageCategory]CategorySum {
	out := make(map[UsageCategory]CategorySum, len(usageCategories))
	for _, c := range usageCategories {
		var cs CategorySum
		for _, r := range l.Records {
			if tc, ok := r.Reported[c]; ok && tc.Known {
				cs.Known += tc.Value
				cs.KnownRecords++
				continue
			}
			cs.UnknownRecords++
		}
		out[c] = cs
	}
	return out
}

// Estimate prices the ledger against s.
//
// The returned amount is a LOWER BOUND whenever the Completeness says so: unknown volumes are not
// priced at zero, they are named. The result is an estimate on every path — including for records
// under PricingSubscription, where it is a list-price-equivalent figure and not a cash charge —
// and it is never written into any record's Invoice.
func (l RequestLedger) Estimate(s RateSchedule) (Money, Completeness) {
	sums := l.Sum()
	total := Money{Currency: scheduleCurrency(s)}
	var missing []UsageCategory

	for _, c := range usageCategories {
		cs := sums[c]
		rate, priced := s.RateFor(c)
		if !priced || cs.UnknownRecords > 0 {
			missing = append(missing, c)
		}
		if !priced || rate.Currency != total.Currency {
			continue
		}
		total.Micros += scaleByMillion(cs.Known, rate.Micros)
	}

	for _, r := range l.Records {
		for _, ch := range r.NonTokenCharges {
			if ch.Amount.Currency == total.Currency {
				total.Micros += ch.Amount.Micros
			}
		}
	}

	unknown := 0
	for _, r := range l.Records {
		if len(r.derivedMissing()) > 0 {
			unknown++
		}
	}

	return total, Completeness{
		Complete:          len(missing) == 0 && unknown == 0,
		MissingCategories: missing,
		UnknownRecords:    unknown,
	}
}

// scheduleCurrency is the currency a schedule prices in, or "" when it prices nothing.
func scheduleCurrency(s RateSchedule) string {
	for _, c := range usageCategories {
		if m, ok := s.PerMillion[c]; ok && m.Currency != "" {
			return m.Currency
		}
	}
	return ""
}

// scaleByMillion is tokens × perMillion / 1_000_000 in integer arithmetic, rounded half up.
func scaleByMillion(tokens core.Tokens, perMillion int64) int64 {
	const million = 1_000_000
	n := int64(tokens) * perMillion
	if n < 0 {
		return -((-n + million/2) / million)
	}
	return (n + million/2) / million
}

// ConditionalCacheCost is Qompack.md §5.6's illustration: N identical uses of a prefix, one write,
// every later use hitting, with the base input cost normalized to 1.
//
// Uncached is N. Cached is w + (N−1)·r — one write and N−1 hits. With w = 1.25 and r = 0.1 two
// uses cost 1.35 rather than 2.
//
// The ordinary break-even is NOT w/r. That rule is retired from default policy (§5.6): it answers
// a different question, and at these multipliers it would say 12.5 uses where the real answer is
// two. CacheBreakEvenReads computes the real one. This is arithmetic under explicit assumptions,
// not an operational threshold and not a claim about hit rates.
//
// Zero or fewer uses is no request at all, and costs nothing on either side — not a free write.
func ConditionalCacheCost(n int, w, r float64) (uncached, cached float64) {
	if n <= 0 {
		return 0, 0
	}
	return float64(n), w + float64(n-1)*r
}

// CacheBreakEvenReads is the smallest N for which w + (N−1)·r < N: the number of uses at which
// writing the cache first is cheaper than not caching at all.
//
// It returns 0 when no such N exists — r ≥ 1 means a hit never beats a fresh read — or when the
// inputs are not finite.
//
// It is computed by evaluating ConditionalCacheCost around the closed-form root (N > (w−r)/(1−r))
// rather than from the closed form alone. The two disagree by one at, for instance, w = 3.7 and
// r = 0.1, where the exact root is an integer that floating point lands just below; deciding with
// the same arithmetic the caller sees keeps the two functions consistent by construction.
func CacheBreakEvenReads(w, r float64) int {
	if math.IsNaN(w) || math.IsNaN(r) || math.IsInf(w, 0) || math.IsInf(r, 0) || r >= 1 {
		return 0
	}
	n := 1
	if x := (w - r) / (1 - r); x > 1 {
		if x >= 1e9 {
			return 0 // no practical break-even
		}
		n = int(math.Floor(x)) + 1
	}
	for n > 1 {
		u, c := ConditionalCacheCost(n-1, w, r)
		if c >= u {
			break
		}
		n--
	}
	for range 8 {
		u, c := ConditionalCacheCost(n, w, r)
		if c < u {
			return n
		}
		n++
	}
	return 0
}

// containsKind reports whether v is present in set.
func containsKind(set []RequestKind, v RequestKind) bool {
	for _, k := range set {
		if k == v {
			return true
		}
	}
	return false
}

// containsMode reports whether v is present in set.
func containsMode(set []PricingMode, v PricingMode) bool {
	for _, m := range set {
		if m == v {
			return true
		}
	}
	return false
}
