package eval

// ── Live usage accounting: running totals, differenced, never summed ─────────────────────────────
//
// V6 close-out C5.4. The host reports usage three ways, and each is right about something different
// (see livestream.go's header for the observed shape):
//
//   - result.usage is exact for the turn's main loop, output included, but blind to everything the
//     host ran beside it: the compaction summary request, subagents, helper requests.
//   - result.modelUsage is complete — every request the process made, per model — but it is a
//     RUNNING total, and on a resumed session it starts from the restored earlier spend.
//   - assistant-line usage is per request, with the cache-write TTL split, but its output count is
//     a placeholder.
//
// So each turn's full cost is the DIFFERENCE between consecutive running totals, and the part of it
// the main loop does not explain is attributed to what the turn did beside the main loop: a
// compaction when the turn compacted, a subagent when one ran, a helper otherwise. Nothing is ever
// added across results. That is the rule that keeps a resumed session, or a /compact turn whose
// result repeats the running total, from being counted twice.

import (
	"fmt"
	"sort"

	"github.com/qompack/qompack/internal/core"
)

// UsageTotals is one bundle of token volumes.
type UsageTotals struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	// CacheWrite5m and CacheWrite1h split CacheWrite by TTL. Nil means the host did not say.
	CacheWrite5m *int64 `json:"cache_write_5m,omitempty"`
	CacheWrite1h *int64 `json:"cache_write_1h,omitempty"`
	// Thinking is the part of Output that was extended thinking. Nil means the host did not say.
	Thinking *int64 `json:"thinking,omitempty"`
}

// IsZero reports whether every known volume is zero.
func (u UsageTotals) IsZero() bool {
	return u.Input == 0 && u.Output == 0 && u.CacheRead == 0 && u.CacheWrite == 0
}

// Tokens is every volume the totals carry, cache reads included: the "tokens processed" figure.
func (u UsageTotals) Tokens() int64 { return u.Input + u.Output + u.CacheRead + u.CacheWrite }

func usageOfResult(u HostUsage) UsageTotals {
	return UsageTotals{
		Input:        u.InputTokens,
		Output:       u.OutputTokens,
		CacheRead:    u.CacheReadInputTokens,
		CacheWrite:   u.CacheCreationInputTokens,
		CacheWrite5m: u.CacheCreation5m,
		CacheWrite1h: u.CacheCreation1h,
		Thinking:     u.ThinkingTokens,
	}
}

func usageOfModel(m HostModelUsage) UsageTotals {
	return UsageTotals{
		Input:      m.InputTokens,
		Output:     m.OutputTokens,
		CacheRead:  m.CacheReadInputTokens,
		CacheWrite: m.CacheCreationInputTokens,
		Thinking:   m.ThinkingTokens,
	}
}

// runningTotal is one model's running total in a modelUsage map. A model the map does not name had
// spent nothing yet, so its total is a KNOWN zero, thinking included; only an entry the host printed
// without a thinking count leaves thinking unknown.
func runningTotal(m map[string]HostModelUsage, model string) UsageTotals {
	if e, ok := m[model]; ok {
		return usageOfModel(e)
	}
	zero := int64(0)
	return UsageTotals{Thinking: &zero}
}

// sub returns u − o per volume. A split is kept only when both sides carry it.
func (u UsageTotals) sub(o UsageTotals) UsageTotals {
	out := UsageTotals{
		Input:      u.Input - o.Input,
		Output:     u.Output - o.Output,
		CacheRead:  u.CacheRead - o.CacheRead,
		CacheWrite: u.CacheWrite - o.CacheWrite,
	}
	out.CacheWrite5m = subOpt(u.CacheWrite5m, o.CacheWrite5m)
	out.CacheWrite1h = subOpt(u.CacheWrite1h, o.CacheWrite1h)
	out.Thinking = subOpt(u.Thinking, o.Thinking)
	return out
}

// add returns u + o per volume. A split survives only when both sides carry it.
func (u UsageTotals) add(o UsageTotals) UsageTotals {
	out := UsageTotals{
		Input:      u.Input + o.Input,
		Output:     u.Output + o.Output,
		CacheRead:  u.CacheRead + o.CacheRead,
		CacheWrite: u.CacheWrite + o.CacheWrite,
	}
	out.CacheWrite5m = addOpt(u.CacheWrite5m, o.CacheWrite5m)
	out.CacheWrite1h = addOpt(u.CacheWrite1h, o.CacheWrite1h)
	out.Thinking = addOpt(u.Thinking, o.Thinking)
	return out
}

func subOpt(a, b *int64) *int64 {
	if a == nil || b == nil {
		return nil
	}
	v := *a - *b
	return &v
}

func addOpt(a, b *int64) *int64 {
	if a == nil || b == nil {
		return nil
	}
	v := *a + *b
	return &v
}

// negative names the first volume below zero, or "".
func (u UsageTotals) negative() string {
	switch {
	case u.Input < 0:
		return "input"
	case u.Output < 0:
		return "output"
	case u.CacheRead < 0:
		return "cache_read"
	case u.CacheWrite < 0:
		return "cache_write"
	case u.Thinking != nil && *u.Thinking < 0:
		return "thinking"
	}
	return ""
}

// TurnAccount is one turn's usage.
type TurnAccount struct {
	Turn int `json:"turn"`
	// LocalCommand is the built-in command the turn ran, e.g. "compact", or "".
	LocalCommand string `json:"local_command,omitempty"`
	// Compacted reports that the turn produced a compact_boundary.
	Compacted bool `json:"compacted"`
	// MainModel is the model the main loop ran on.
	MainModel string `json:"main_model"`
	// MainLoop is result.usage: the turn's main loop, exact.
	MainLoop UsageTotals `json:"main_loop"`
	// Delta is each model's running-total change across this turn: everything the turn cost.
	Delta map[string]UsageTotals `json:"delta"`
	// Beside is Delta minus MainLoop: what the host ran beside the main loop this turn.
	Beside map[string]UsageTotals `json:"beside,omitempty"`
	// BesideKind attributes Beside: compaction, child or turn (a helper request).
	BesideKind RequestKind `json:"beside_kind,omitempty"`
	// HostCostDeltaUSD is this turn's change in total_cost_usd: the host's own client-side
	// list-price estimate, reported beside ours and never merged into it.
	HostCostDeltaUSD float64 `json:"host_cost_delta_usd"`
	// StepsMatchResult reports that the deduplicated main-loop requests' input and cache volumes
	// sum to result.usage's. Nil when the turn had no requests to compare.
	StepsMatchResult *bool `json:"steps_match_result,omitempty"`
}

// SessionAccount is one process's usage, by turn and in total.
type SessionAccount struct {
	Turns []TurnAccount `json:"turns"`
	// Total is each model's final running total minus the baseline: what this process spent.
	Total map[string]UsageTotals `json:"total"`
	// HostCostUSD is the final total_cost_usd minus the baseline's.
	HostCostUSD float64 `json:"host_cost_usd"`
	// Consistent is false when any rule below was broken; Problems says which.
	Consistent bool     `json:"consistent"`
	Problems   []string `json:"problems,omitempty"`
}

// AccountBaseline is the running total a process started from: zero for a fresh session, and the
// previous invocation's final totals for a resumed one (the host restores them, CLI ≥ 2.1.277).
type AccountBaseline struct {
	ModelUsage   map[string]HostModelUsage
	TotalCostUSD float64
}

// FinalBaseline is the baseline a later invocation resuming this stream's session starts from.
func (s HostStream) FinalBaseline() AccountBaseline {
	for i := len(s.Turns) - 1; i >= 0; i-- {
		if r := s.Turns[i].Result; r != nil {
			return AccountBaseline{ModelUsage: r.ModelUsage, TotalCostUSD: r.TotalCostUSD}
		}
	}
	return AccountBaseline{}
}

// AccountHostStream turns a parsed stream into per-turn and total usage.
//
// It never sums running totals. It checks three things and says which failed rather than repairing
// anything: running totals never decrease (a decrease means the host reset them, and the
// differences would be meaningless); what the main loop reports never exceeds what the turn cost
// (a negative remainder means the attribution is wrong); and the deduplicated per-request input and
// cache volumes agree with result.usage (disagreement means a request was lost or double-counted).
// A turn the stream ended inside has no result and contributes nothing, which is itself recorded.
func AccountHostStream(s HostStream, base AccountBaseline) SessionAccount {
	acc := SessionAccount{Total: map[string]UsageTotals{}, Consistent: true}
	problem := func(format string, args ...any) {
		acc.Consistent = false
		acc.Problems = append(acc.Problems, fmt.Sprintf(format, args...))
	}

	prev := base.ModelUsage
	prevCost := base.TotalCostUSD
	mainModel := ""
	if s.Init != nil {
		mainModel = s.Init.Model
	}

	for _, t := range s.Turns {
		if t.Result == nil {
			problem("turn %d: the stream ended before its result; its usage is unknown", t.Index)
			continue
		}
		ta := TurnAccount{
			Turn:             t.Index,
			LocalCommand:     t.Result.LocalCommand,
			Compacted:        len(t.Compactions) > 0,
			MainModel:        mainModel,
			MainLoop:         usageOfResult(t.Result.Usage),
			Delta:            map[string]UsageTotals{},
			HostCostDeltaUSD: t.Result.TotalCostUSD - prevCost,
		}
		if ta.HostCostDeltaUSD < 0 {
			problem("turn %d: total_cost_usd decreased from %.6f to %.6f", t.Index, prevCost, t.Result.TotalCostUSD)
		}
		for _, model := range sortedModels(t.Result.ModelUsage, prev) {
			d := runningTotal(t.Result.ModelUsage, model).sub(runningTotal(prev, model))
			if n := d.negative(); n != "" {
				problem("turn %d: model %s running %s total decreased; the host reset its totals", t.Index, model, n)
			}
			if !d.IsZero() {
				ta.Delta[model] = d
			}
		}
		ta.Beside, ta.BesideKind = beside(t, ta, problem)
		ta.StepsMatchResult = stepsMatch(t)
		if ta.StepsMatchResult != nil && !*ta.StepsMatchResult {
			problem("turn %d: per-request input/cache volumes do not sum to result.usage", t.Index)
		}
		acc.Turns = append(acc.Turns, ta)
		prev = t.Result.ModelUsage
		prevCost = t.Result.TotalCostUSD
	}

	for model := range prev {
		d := runningTotal(prev, model).sub(runningTotal(base.ModelUsage, model))
		if !d.IsZero() {
			acc.Total[model] = d
		}
	}
	acc.HostCostUSD = prevCost - base.TotalCostUSD
	return acc
}

// beside computes Delta − MainLoop and attributes it.
func beside(t HostTurn, ta TurnAccount, problem func(string, ...any)) (map[string]UsageTotals, RequestKind) {
	out := map[string]UsageTotals{}
	for model, d := range ta.Delta {
		rest := d
		if model == ta.MainModel {
			// The TTL split of the main loop is known; the running total never carries one, so
			// the remainder's split is unknown unless the remainder wrote nothing to the cache.
			main := ta.MainLoop
			main.CacheWrite5m, main.CacheWrite1h = nil, nil
			rest = d.sub(main)
		}
		if n := rest.negative(); n != "" {
			problem("turn %d: main-loop %s exceeds the turn's %s running-total change", t.Index, n, model)
		}
		if rest.CacheWrite == 0 {
			zero := int64(0)
			z5, z1 := zero, zero
			rest.CacheWrite5m, rest.CacheWrite1h = &z5, &z1
		}
		if !rest.IsZero() {
			out[model] = rest
		}
	}
	if len(out) == 0 {
		return nil, ""
	}
	switch {
	case ta.LocalCommand == "compact" || ta.Compacted:
		return out, KindCompaction
	case t.Result.SubagentsSpawned > 0 || hasChildRequests(t):
		return out, KindChild
	default:
		return out, KindTurn
	}
}

func hasChildRequests(t HostTurn) bool {
	for _, r := range t.Requests {
		if r.ParentToolUseID != "" {
			return true
		}
	}
	return false
}

// stepsMatch compares the deduplicated main-loop requests with result.usage over the volumes the
// per-request lines report exactly.
func stepsMatch(t HostTurn) *bool {
	var in, cr, cw int64
	n := 0
	for _, r := range t.Requests {
		if r.ParentToolUseID != "" {
			continue
		}
		n++
		in += r.Usage.InputTokens
		cr += r.Usage.CacheReadInputTokens
		cw += r.Usage.CacheCreationInputTokens
	}
	if n == 0 {
		return nil
	}
	u := t.Result.Usage
	ok := in == u.InputTokens && cr == u.CacheReadInputTokens && cw == u.CacheCreationInputTokens
	return &ok
}

func sortedModels(a, b map[string]HostModelUsage) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]HostModelUsage{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// ── the request ledger view ─────────────────────────────────────────────────────────────────────

// LedgerRecords renders the account as SP-19 request records: one main-loop record per turn and
// one record per model for what ran beside it. idPrefix makes the identifiers unique within a
// multi-trial ledger.
//
// The host's inclusive output_tokens is split into output (visible) and thinking_output when the
// host reported the thinking share, so the two categories sum to what the host billed as output and
// neither is counted twice; when it did not, output is the inclusive figure and thinking_output is
// unknown. A beside-the-main-loop cache write carries no TTL split — the running totals never do —
// so its two TTL categories are unknown whenever it wrote anything, and the volume is kept in the
// record's notes rather than guessed into one TTL.
func (a SessionAccount) LedgerRecords(idPrefix, provider string, mode PricingMode, rateDate string) []RequestRecord {
	var out []RequestRecord
	for _, t := range a.Turns {
		mainID := fmt.Sprintf("%s-t%02d-main", idPrefix, t.Turn)
		kind := KindTurn
		if t.LocalCommand == "compact" {
			kind = KindCompaction
		}
		main := ledgerRecord(mainID, "", kind, provider, t.MainModel, mode, rateDate, t.MainLoop)
		main.Notes = "main agent loop, from result.usage"
		out = append(out, main)
		models := make([]string, 0, len(t.Beside))
		for m := range t.Beside {
			models = append(models, m)
		}
		sort.Strings(models)
		for _, m := range models {
			u := t.Beside[m]
			rec := ledgerRecord(fmt.Sprintf("%s-t%02d-beside-%s", idPrefix, t.Turn, m), mainID,
				t.BesideKind, provider, m, mode, rateDate, u)
			rec.Notes = fmt.Sprintf("running-total change beside the main loop (%s); "+
				"cache_write %d tokens, TTL split %s", t.BesideKind, u.CacheWrite, splitState(u))
			out = append(out, rec)
		}
	}
	return out
}

// tokensOf converts a host volume to core.Tokens. The host's volumes are int64 on the wire;
// core.Tokens is an int, which is 64 bits on every target this repository builds.
func tokensOf(v int64) core.Tokens { return core.Tokens(v) }

func splitState(u UsageTotals) string {
	if u.CacheWrite5m != nil && u.CacheWrite1h != nil {
		return "known"
	}
	return "unknown"
}

func ledgerRecord(id, parent string, kind RequestKind, provider, model string, mode PricingMode,
	rateDate string, u UsageTotals,
) RequestRecord {
	rep := map[UsageCategory]TokenCount{
		CategoryInput:     KnownTokens(tokensOf(u.Input)),
		CategoryCacheRead: KnownTokens(tokensOf(u.CacheRead)),
	}
	if u.Thinking != nil {
		rep[CategoryOutput] = KnownTokens(tokensOf(u.Output - *u.Thinking))
		rep[CategoryThinkingOutput] = KnownTokens(tokensOf(*u.Thinking))
	} else {
		rep[CategoryOutput] = KnownTokens(tokensOf(u.Output))
	}
	if u.CacheWrite5m != nil && u.CacheWrite1h != nil {
		rep[CategoryCacheWrite5m] = KnownTokens(tokensOf(*u.CacheWrite5m))
		rep[CategoryCacheWrite1h] = KnownTokens(tokensOf(*u.CacheWrite1h))
	}
	rec := RequestRecord{
		ID:            id,
		ParentID:      parent,
		Kind:          kind,
		Provider:      provider,
		Model:         model,
		RateTableDate: rateDate,
		PricingMode:   mode,
		Reported:      rep,
	}
	rec.Missing = rec.derivedMissing()
	return rec
}
