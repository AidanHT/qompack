package rehydrate

import (
	"context"
	"fmt"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
)

// sourceCompact is the only SessionStart source that rehydrates (Qompack.md §7.3, §8.6).
const sourceCompact = "compact"

// The slice bounds item 3 and items 4/6 rank against. They are local to this slice, not config:
// Appendix C has no key for them and internal/config is another subplan's package.
const (
	sliceMaxNodes = 5000
	sliceDecay    = float32(0.85)
	sliceDeadline = 50 * time.Millisecond
)

// Build renders the rehydrated context for r, reading from d (00-ARCHITECTURE.md §5.15).
//
// It emits Items in the normative §8.6 order, prices each against r.Budget, records everything it
// could not fit in Result.Dropped (G4.5), and wraps the whole payload in the §8.5 injection tags
// so a later read of the transcript can strip it out again rather than re-encoding it (§4.6).
// When it cannot do its full job — no checkpoint, a budget too small — it sets Result.Degraded
// and returns what it could, because a session that starts with less context is recoverable and a
// session that fails to start is not (§12.3).
//
// Build is a PURE function of (Request, Deps). It reads no clock: the payload carries no
// timestamp, PropBuild_Deterministic requires byte-identical output for identical input, and the
// one wall-clock value in this slice — State.Emitted — is stamped by the daemon service. It never
// returns a non-nil error for a missing dependency either; the only error it ever returns is
// ctx.Err() when the context is already cancelled on entry.
func Build(ctx context.Context, r Request, d Deps) (Result, error) {
	res, _, err := BuildWithStats(ctx, r, d)
	return res, err
}

// BuildWithStats is Build plus the per-item unit counts.
//
// Result's shape is fixed by 00-ARCHITECTURE.md §5.15 and carries no room for Units/UnitsSeen, but
// the drop-report state file records both so SP-16 can tune the tier boundaries from replay
// evidence. Returning them beside the Result keeps §5.15's type untouched and keeps the counts out
// of package-level state.
func BuildWithStats(ctx context.Context, r Request, d Deps) (Result, []ItemStat, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, nil, err
	}
	d = normalizeDeps(d)

	// Belt-and-braces: startup, resume and clear never call Build, so a mis-wired caller cannot
	// inject a rehydration into a fresh session.
	if r.Source != sourceCompact {
		return Result{Seq: r.Ref.Seq}, nil, nil
	}

	cfg := r.Cfg.Runtime.Rehydrate
	budget := clampBudget(r.Budget, cfg)

	// ── 1. the fixed wrapper, charged before anything is admitted ────────────────────────────
	//
	// The open tag, the document header and the close tag are derivable before any item is built,
	// so they are reserved up front (nothing can push the payload over the cap) and attributed to
	// the FIRST emitted item at render time (so Result.Tokens == sum over Items holds exactly).
	// There is deliberately no synthetic ItemStat{Kind:"overhead"}: it would make the total exceed
	// the sum by construction, which runBudgetCase rejects at every budget.
	header := documentHeader(r)
	overhead := estimate(d, fmt.Sprintf(checkpoint.InjectionOpenTag, int(r.Ref.Seq), checkpoint.SchemaVersion)+"\n") +
		estimate(d, header+"\n") +
		estimate(d, "\n"+checkpoint.InjectionCloseTag)

	if overhead >= budget {
		// Not even the wrapper fits. No items means no payload — never an empty tagged wrapper.
		res := Result{Degraded: true, Seq: r.Ref.Seq}
		res.Dropped = append(res.Dropped, checkpoint.DropEntry{
			Kind: "narrative", ID: "payload",
			Detail: "the injection wrapper alone exceeds the rehydration budget; nothing was injected",
		})
		d.Log.Loud("rehydrate: budget cannot hold the injection wrapper",
			"budget", int(budget), "overhead", int(overhead))
		return res, nil, nil
	}

	// ── 2. build every item ──────────────────────────────────────────────────────────────────
	sc := sliceScores(r, d)
	all := buildAll(ctx, r, d, sc)
	priceAll(d, all)

	// ── 3. tier 1: admitted whole or not at all, never over the cap ──────────────────────────
	//
	// "Never truncated" means never PARTIALLY emitted; it is not a licence to overrun the cap.
	// §8.6's own preamble introduces the eight items as "filled in importance order until the
	// budget is reached", 00-ARCHITECTURE §5.15 calls Request.Budget a hard cap from config, the
	// shipped contract on Result.Tokens says it never exceeds it, and runBudgetCase asserts that
	// at a budget of 1. A payload that overran its cap is one the host may refuse outright — an
	// all-or-nothing loss where a partial one was available.
	spent := overhead
	degraded := false
	fills := make(map[ItemKind]*admitted, len(renderOrder))
	for _, k := range tier1Order {
		units := tier1Units(k, all[k])
		if len(units) == 0 {
			continue
		}
		cost := headingCost(d, k) + totalOf(units)
		if spent+cost > budget {
			// Does not fit whole: drop the item entirely and say so.
			degraded = true
			a := &admitted{truncated: true}
			for _, u := range units {
				a.dropped = append(a.dropped, tier1Drop(k, u))
				a.pending = append(a.pending, u)
			}
			fills[k] = a
			continue
		}
		fills[k] = &admitted{units: units, used: cost}
		spent += cost
	}
	if degraded {
		d.Log.Loud("rehydrate: tier-1 material exceeds the hard budget cap",
			"budget", int(budget), "spent", int(spent))
	}

	// ── 4-6. reserves and per-item shares ────────────────────────────────────────────────────
	reserveDrop := budget / dropReportReserveDiv
	reserveSkill := core.Tokens(cfg.SkillIndexTokens)
	if r := budget / dropReportReserveDiv; reserveSkill > r {
		reserveSkill = r
	}
	avail := budget - spent - reserveDrop - reserveSkill
	if avail < 0 {
		avail = 0
	}

	// The remainder from integer division goes to the LAST share-taking item in renderOrder, so
	// the arithmetic is lossless and deterministic rather than quietly dropping a few tokens.
	var allocated core.Tokens
	shares := make(map[ItemKind]core.Tokens, 6)
	for _, k := range shareOrder {
		pct, _ := sharePctFor(k)
		s := shareOf(avail, pct)
		shares[k] = s
		allocated += s
	}
	shares[shareOrder[len(shareOrder)-1]] += avail - allocated

	// ── 7. fill in renderOrder, carrying forward ─────────────────────────────────────────────
	var carry core.Tokens
	for _, k := range shareOrder {
		b := all[k]
		units := b.units
		if k == ItemUserIntent {
			// The original-intent unit was admitted whole in step 3; this share funds only the
			// evolution deltas that follow it.
			if len(units) > 0 {
				units = units[1:]
			}
		}
		allowance := shares[k] + carry
		if len(units) == 0 {
			carry = allowance
			continue
		}
		var a *admitted
		if _, already := fills[k]; already && k == ItemUserIntent {
			// The heading was already charged with the tier-1 original unit, so the evolution
			// deltas pay for themselves only — charging it twice would inflate the item's Tokens
			// above its true rendered cost and eat budget nothing renders.
			f := fillPrefix(units, allowance, false)
			a = &f
		} else {
			a = fillWithHeading(d, k, units, allowance)
		}
		fills[k] = mergeIntent(fills[k], a)
		spent += a.used
		carry = allowance - a.used
	}

	// ── 8. the skill index, against its own reserve ──────────────────────────────────────────
	if b := all[ItemSkillIndex]; len(b.units) > 0 {
		a := fillWithHeading(d, ItemSkillIndex, b.units, reserveSkill)
		fills[ItemSkillIndex] = a
		spent += a.used
	}

	// ── 9. min-fill, for an unset request only ───────────────────────────────────────────────
	if r.Budget <= 0 {
		// The new total is deliberately discarded. Min-fill re-admits into fills, which is what
		// render reads; the running counter is not consulted again, and step 10 sizes item 7 from
		// carry and its own reserve rather than from spent.
		minFill(fills, shareOrder, spent, core.Tokens(cfg.MinTokens), budget)
	}

	// ── 10. the drop report, last, because it reports on everything above ────────────────────
	//
	// It is built from the COMPLETE drop set and may be rendered truncated; Result.Dropped below
	// keeps the whole list either way, which is what makes `dropped()` able to answer for what
	// section 7 could not show.
	drops := collectDrops(r, all, fills)
	if b := buildDropReport(drops); len(b.units) > 0 {
		priceUnits(d, b.units)
		// Item 7 is the one item Build constructs itself rather than through buildAll, because it
		// reports on all the others. It still has to be PUT BACK into all, or render reads a zero
		// built for it and the state file records units_seen: 0 for a section that had a dozen
		// lines to choose from — the exact pair SP-16 is meant to tune the tier boundaries from.
		all[ItemDropReport] = b
		a := fillWithHeading(d, ItemDropReport, b.units, reserveDrop+carry)
		if a.truncated {
			rest := len(b.units) - len(a.units)
			more := moreDropsUnit(d, rest)
			a.units = append(a.units, more)
			a.used += more.tokens
		}
		fills[ItemDropReport] = a
		// spent is deliberately not advanced here. Item 7 is the last thing filled, nothing reads
		// the running total afterwards, and the hard-cap loop below re-derives the true cost from
		// the rendered Items rather than from this counter.
	}

	// The no-contents guard is a per-build event, not a per-unit one: one Loud says the injection
	// boundary rejected checkpoint-derived prose that looked like file content (§13 invariant 5),
	// and the drop report names each rejection.
	if guardTripped(drops) {
		d.Log.Loud("rehydrate: the no-contents guard rejected checkpoint-derived units",
			"session", string(r.Session), "seq", int(r.Ref.Seq))
	}

	res, stats := render(r, d, fills, all, overhead)
	res.Dropped = drops
	res.Degraded = res.Degraded || degraded || sourceUnavailable(drops)
	res.Seq = r.Ref.Seq

	// ── the hard-cap assertion, unconditional ────────────────────────────────────────────────
	//
	// Degraded or not, Result.Tokens <= budget must hold. This is a panic-free re-truncation
	// rather than an assert: a rehydration that discovered it had overrun must still return a
	// payload, and the Loud is what makes the arithmetic bug visible.
	//
	// It evicts in IMPORTANCE order, not in render order, and that distinction is load-bearing.
	// Steps 3 and 7 admit a handful of units unconditionally — the tier-1 material, and item 3's
	// standing instruction, which carries no drop entry and must survive whatever the allowance
	// says — so at a budget far below the §8.6 band the admitted set can genuinely exceed the cap.
	// Evicting the literal tail then removes items 7 and 8 first: the drop report that says what
	// was lost, and the affordance line that says how to ask for it back. That inverts §8.6
	// exactly, and it is worst in precisely the case that needs them most.
	for res.Tokens > budget && len(res.Items) > 0 {
		d.Log.Loud("rehydrate: payload exceeded its budget after filling; re-truncating",
			"tokens", int(res.Tokens), "budget", int(budget))
		i := evictIndex(res.Items)
		gone := res.Items[i]
		res.Items = append(res.Items[:i], res.Items[i+1:]...)
		stats = append(stats[:i], stats[i+1:]...)
		for j := range res.Items {
			// Rank is the 0-based position among EMITTED items (runItemOrderCase); evicting from
			// the middle leaves a hole in it that has to be closed.
			res.Items[j].Rank = j
			stats[j].Rank = j
		}
		res.Tokens -= gone.Tokens
		res.Degraded = true
		res.Text = renderText(r, res.Items)
	}
	if len(res.Items) == 0 {
		res.Text = ""
		res.Tokens = 0
	}
	return res, stats, nil
}

// normalizeDeps fills in the tolerable nils. A nil collaborator is never an error: the item that
// would have read it is skipped and a DropEntry records the absence.
func normalizeDeps(d Deps) Deps {
	if d.Log == nil {
		d.Log = logging.Nop()
	}
	return d
}

// sliceScores computes the backward slice once, for every item that ranks by relevance. A nil
// graph, an error or a deadline all yield an empty map, which degrades ordering to the documented
// TS/ID tiebreak rather than failing.
func sliceScores(r Request, d Deps) map[dag.NodeID]float32 {
	if d.Graph == nil {
		return nil
	}
	crit := sliceCriteria(r.Checkpoint)
	if len(crit) == 0 {
		return nil
	}
	sl, err := d.Graph.BackwardSlice(crit, dag.SliceOptions{
		Thin:     r.Cfg.Selection.Slicing == "thin",
		MaxNodes: sliceMaxNodes,
		Decay:    sliceDecay,
		Deadline: sliceDeadline,
	})
	if err != nil {
		d.Log.Debug("rehydrate: backward slice unavailable; ranking by recency", "err", err.Error())
		return nil
	}
	return sl.Scores
}

// totalOf sums a unit slice's priced cost.
func totalOf(units []unit) core.Tokens {
	var t core.Tokens
	for _, u := range units {
		t += u.tokens
	}
	return t
}
