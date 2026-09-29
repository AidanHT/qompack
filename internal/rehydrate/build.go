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
	// The payload is bounded in two dimensions at once: the token budget above, and the host's
	// fixed character ceiling (hostcap.go, owner decision D5). Every admission below must fit both;
	// the character half is exact, so it is a guarantee and not a target.
	limit := cost{tok: budget, chars: PayloadCeilingChars}

	// ── 1. the fixed wrapper, charged before anything is admitted ────────────────────────────
	//
	// The open tag, the document header and the close tag are derivable before any item is built,
	// so they are reserved up front (nothing can push the payload over the cap) and attributed to
	// the FIRST emitted item at render time (so Result.Tokens == sum over Items holds exactly).
	// There is deliberately no synthetic ItemStat{Kind:"overhead"}: it would make the total exceed
	// the sum by construction, which runBudgetCase rejects at every budget.
	header := documentHeader(r)
	openTag := fmt.Sprintf(checkpoint.InjectionOpenTag, int(r.Ref.Seq), checkpoint.SchemaVersion)
	overhead := cost{
		tok: estimate(d, openTag+"\n") +
			estimate(d, header+"\n") +
			estimate(d, "\n"+checkpoint.InjectionCloseTag),
		chars: wrapperChars(openTag, header, checkpoint.InjectionCloseTag),
	}

	if overhead.tok >= limit.tok || overhead.chars >= limit.chars {
		// Not even the wrapper fits. No items means no payload — never an empty tagged wrapper,
		// and never a silent one either: this is a zero/tiny-budget OVERFLOW (T11-BUDGET-02), named
		// as such so Overflowed(res.Dropped) recognizes it regardless of which estimator priced
		// overhead — the bare (len+3)/4 fallback and a calibrated tokens.Estimator agree on the
		// COMPARISON this branch makes even when they disagree on the exact count.
		res := Result{Degraded: true, Seq: r.Ref.Seq}
		detail := "OVERFLOW: the injection wrapper alone (" + itoa(int(overhead.tok)) +
			" tokens) exceeds the rehydration budget (" + itoa(int(budget)) +
			" tokens); nothing was injected — call dropped() for the full accounting"
		if overhead.tok < limit.tok {
			// Only a pathological header (a session id or sequence thousands of characters wide) can
			// reach this: the wrapper is otherwise about a hundred characters.
			detail = "OVERFLOW: the injection wrapper alone (" + itoa(overhead.chars) +
				" host characters) exceeds the rehydration ceiling (" + itoa(limit.chars) +
				"); nothing was injected — call dropped() for the full accounting"
		}
		res.Dropped = append(res.Dropped, checkpoint.DropEntry{Kind: dropKindOverflow, ID: "payload", Detail: detail})
		d.Log.Loud("rehydrate: budget cannot hold the injection wrapper",
			"budget", int(budget), "overhead", int(overhead.tok),
			"ceiling_chars", limit.chars, "overhead_chars", overhead.chars)
		return res, nil, nil
	}

	// ── 2. build every item ──────────────────────────────────────────────────────────────────
	sc := sliceScores(r, d)
	all := buildAll(ctx, r, d, sc)
	priceAll(d, all)

	// ── 3. tier 1: each record admitted whole or not at all, never over the cap ───────────────
	//
	// "Never truncated" means never PARTIALLY emitted; it is not a licence to overrun the cap.
	// §8.6's own preamble introduces the eight items as "filled in importance order until the
	// budget is reached", 00-ARCHITECTURE §5.15 calls Request.Budget a hard cap from config, the
	// shipped contract on Result.Tokens says it never exceeds it, and runBudgetCase asserts that
	// at a budget of 1. A payload that overran its cap is one the host may refuse outright — an
	// all-or-nothing loss where a partial one was available — and one that overran the host's
	// character cap reaches the model as a file path and a 2,000-character preview (D5).
	//
	// Item 7's floor is held back from the first admission on: whatever tier 1 cannot carry is
	// then always namable, with the call that restores it, inside the ceiling.
	spent := overhead
	degraded := false
	fills := make(map[ItemKind]*admitted, len(renderOrder))
	floor := dropReportFloor(d)
	for _, k := range tier1Admission {
		units := tier1Units(k, all[k])
		if len(units) == 0 {
			continue
		}
		a := fillTier1(d, k, all[k], units, limit.minus(spent).minus(floor))
		fills[k] = a
		spent = spent.plus(a.used)
		if a.truncated {
			degraded = true
		}
	}
	if degraded {
		d.Log.Loud("rehydrate: tier-1 material exceeds the hard budget cap",
			"budget", int(budget), "spent", int(spent.tok),
			"ceiling_chars", limit.chars, "spent_chars", spent.chars)
	}

	// ── 4-6. reserves and per-item shares ────────────────────────────────────────────────────
	//
	// Item 7 reserves a tenth of each dimension and never less than its floor; item 6b reserves
	// runtime.rehydrate.skillIndexTokens (capped at a tenth of the budget) and a tenth of the
	// ceiling. Both are held back from every share and from every item's room below.
	reserveDrop := cost{tok: budget / dropReportReserveDiv, chars: limit.chars / dropReportReserveDiv}.atLeast(floor)
	reserveSkill := cost{tok: core.Tokens(cfg.SkillIndexTokens), chars: limit.chars / dropReportReserveDiv}
	if r := budget / dropReportReserveDiv; reserveSkill.tok > r {
		reserveSkill.tok = r
	}
	held := reserveDrop.plus(reserveSkill)
	avail := limit.minus(spent).minus(held).nonNegative()

	// The remainder from integer division goes to the LAST share-taking item in renderOrder, so
	// the arithmetic is lossless and deterministic rather than quietly dropping a few units.
	var allocated cost
	shares := make(map[ItemKind]cost, len(shareOrder))
	for _, k := range shareOrder {
		pct, _ := sharePctFor(k)
		sh := cost{tok: shareOf(avail.tok, pct), chars: shareCharsOf(avail.chars, pct)}
		shares[k] = sh
		allocated = allocated.plus(sh)
	}
	last := shareOrder[len(shareOrder)-1]
	shares[last] = shares[last].plus(avail.minus(allocated))

	// ── 7. fill in renderOrder, carrying forward ─────────────────────────────────────────────
	var carry cost
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
		allowance := shares[k].plus(carry)
		if len(units) == 0 {
			carry = allowance
			continue
		}
		// The heading is charged with the first unit this section emits. For item 2 that is the
		// tier-1 original when step 3 admitted it — charging it again would inflate the item above
		// its true rendered cost — and the first evolution delta when step 3 could not.
		charge := fills[k] == nil || len(fills[k].units) == 0
		a := fillWithHeading(d, k, b, units, allowance, limit.minus(spent).minus(held), charge)
		fills[k] = mergeIntent(fills[k], a)
		spent = spent.plus(a.used)
		carry = allowance.minus(a.used)
	}

	// ── 8. the skill index, against its own reserve plus what the shares left ────────────────
	//
	// Item 6b outranks item 7, so it is offered the carry first; item 7 keeps its own reserve and
	// is offered what 6b leaves. The token half of this is bounded by the indexer itself — it only
	// ever returns the entries that fit runtime.rehydrate.skillIndexTokens — so the carry matters in
	// the character half, where a tenth of the ceiling would otherwise cut a payload that has room.
	skillAllowance := reserveSkill.plus(carry)
	if b := all[ItemSkillIndex]; len(b.units) > 0 {
		a := fillWithHeading(d, ItemSkillIndex, b, b.units, skillAllowance, limit.minus(spent).minus(reserveDrop), true)
		fills[ItemSkillIndex] = a
		spent = spent.plus(a.used)
		skillAllowance = skillAllowance.minus(a.used)
	}
	carry = skillAllowance

	// ── 9. min-fill, for an unset request only ───────────────────────────────────────────────
	if r.Budget <= 0 {
		spent = minFill(d, all, fills, shareOrder, spent, core.Tokens(cfg.MinTokens), limit, reserveDrop)
	}

	// An original item 2 had to name rather than read whole (an L0 capture past intentReadLimit)
	// never became a unit, so no fill saw it: its tier-1 overflow is among the builder's own drops.
	// The rehydration is degraded and item 2's row says truncated, exactly as when step 3 forces a
	// tier-1 unit out. It is set after min-fill, which clears truncated once its own pending units
	// are all back in.
	if Overflowed(all[ItemUserIntent].drops) {
		degraded = true
		if a := fills[ItemUserIntent]; a != nil {
			a.truncated = true
		}
	}

	// ── 10. the drop report, last, because it reports on everything above ────────────────────
	//
	// It is built from the COMPLETE drop set and may be rendered as a prefix plus a counted tail;
	// Result.Dropped below keeps the whole list either way, which is what makes `dropped()` able to
	// answer for what section 7 could not show. Its allowance is its reserve plus whatever the
	// shares carried forward, never less than the floor held since step 3, and never more than the
	// payload has left.
	drops := collectDrops(r, all, fills)
	if b := buildDropReport(drops); len(b.units) > 0 {
		priceUnits(d, b.units)
		// Item 7 is the one item Build constructs itself rather than through buildAll, because it
		// reports on all the others. It still has to be PUT BACK into all, or render reads a zero
		// built for it and the state file records units_seen: 0 for a section that had a dozen
		// lines to choose from — the exact pair SP-16 is meant to tune the tier boundaries from.
		all[ItemDropReport] = b
		allowance := reserveDrop.plus(carry).atLeast(floor).atMost(limit.minus(spent))
		fills[ItemDropReport] = fillDropReport(d, b, allowance)
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

	res, stats := render(r, d, fills, all, overhead.tok)
	res.Dropped = drops
	res.Degraded = res.Degraded || degraded || sourceUnavailable(drops)
	res.Seq = r.Ref.Seq

	// ── the hard-cap assertion, unconditional ────────────────────────────────────────────────
	//
	// Degraded or not, Result.Tokens <= budget and hostChars(Result.Text) <= PayloadCeilingChars
	// must hold. This is a panic-free re-truncation rather than an assert: a rehydration that
	// discovered it had overrun must still return a payload, and the Loud is what makes the
	// arithmetic bug visible. The character half is exact above, so only a token estimator that
	// prices the assembled payload above the sum of its parts can reach it.
	//
	// It evicts in IMPORTANCE order, not in render order, and that distinction is load-bearing.
	// Evicting the literal tail removes items 7 and 8 first: the drop report that says what was
	// lost, and the affordance line that says how to ask for it back. That inverts §8.6 exactly,
	// and it is worst in precisely the case that needs them most.
	for (res.Tokens > limit.tok || hostChars(res.Text) > limit.chars) && len(res.Items) > 0 {
		d.Log.Loud("rehydrate: payload exceeded its budget after filling; re-truncating",
			"tokens", int(res.Tokens), "budget", int(budget),
			"chars", hostChars(res.Text), "ceiling_chars", limit.chars)
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
		// A whole section removed by the hard cap is a NAMED overflow, not a silent erasure: the
		// agent must be told which requirement is no longer in context, exactly as tier-1 overflow
		// and the wrapper-alone case already are. Each discretionary record the section carried is
		// named too, with its own restore pointer, so dropped() can answer for every one of them.
		res.Dropped = append(res.Dropped, evictionDrop(gone.Kind))
		if a := fills[gone.Kind]; a != nil && gone.Kind != ItemDropReport {
			for _, u := range a.units {
				if !isFixedUnit(u) {
					res.Dropped = append(res.Dropped, u.drop)
				}
			}
		}
		// Re-measure against the assembled text after EACH eviction rather than decrementing by the
		// evicted row's allocated share: the share was an allocation, and the true remaining cost is
		// what the estimator prices the shorter payload at (wrapper and separators included). Then
		// re-allocate the surviving rows to the new total and keep their stat rows in step.
		res.Text = renderText(r, res.Items)
		res.Tokens = estimate(d, res.Text)
		allocateAssembledTokens(res.Items, res.Tokens)
		syncStatTokens(stats, res.Items)
		res.Degraded = true
	}
	if len(res.Items) == 0 || onlyDropReport(res.Items) {
		// A payload with nothing left but the report on what it lost is no payload: render's own
		// rule, re-applied after eviction.
		res.Items, stats = nil, nil
		res.Text = ""
		res.Tokens = 0
		res.Degraded = true
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
