package rehydrate

import (
	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/tokens"
)

// clampBudget resolves the effective budget B (Qompack.md §8.6 "target 8-12K",
// 00-ARCHITECTURE.md §11.5's runtime.rehydrate min/max).
//
// Request.Budget is a HARD CAP and is never raised. That is not a design preference, it is what
// the inherited conformance suite executes: runBudgetCase asks for 9000, 600 and 1 and asserts
// Result.Tokens <= budget for each, runDegradeCase repeats it at 1, and the shipped contract on
// Result.Tokens says "which never exceeds Request.Budget". So this function may LOWER a request
// and may FILL IN an unset one; raising a caller's stated budget to cfg.MinTokens would overrun
// someone who asked for less.
//
// cfg.MinTokens is therefore a fill target for an UNSET request only (see minFill), not a floor
// on every request. That is exactly what "target 8-12K" means: the daemon's own call passes no
// budget and fills to the ceiling, min-fill pulls the payload up toward minTokens inside it, and
// a caller who names 600 tokens gets at most 600.
//
// Every field of RehydrateCfg is a plain int, not a core.Tokens, so each conversion is explicit.
func clampBudget(req core.Tokens, cfg config.RehydrateCfg) core.Tokens {
	maxT := core.Tokens(cfg.MaxTokens)
	// cfg.MinTokens is deliberately not consulted. It is a FILL TARGET for the unset call, applied
	// by minFill inside the cap, and never a floor imposed on a caller — raising a budget somebody
	// named is exactly what the hard cap forbids. That also makes a minTokens > maxTokens config a
	// non-event here rather than something to correct: nothing in this function reads it, so a bad
	// pair cannot produce a budget above the cap. Validating the pair belongs to internal/config,
	// and a rehydration that panicked on it would fail a session start over a typo.
	if req <= 0 {
		return maxT // unset: fill to the configured ceiling
	}
	if req > maxT {
		return maxT // lowering is allowed: maxTokens is the hard cap
	}
	return req // NEVER raised, not even to minTokens
}

// The six discretionary shares, as integer percentages so the arithmetic is exact and
// platform-independent. They are ordered to match renderOrder, not by magnitude.
//
// There are SIX share-taking items, not five: Checkpoint.UserIntent.Evolution renders as
// truncatable units carrying DropEntry{Kind:"user_intent_evolution"}, so it must be allocated
// like any other discretionary item. It is listed first because renderOrder fills in importance
// order and item 2 precedes item 3 — an evolution delta is closer to the never-truncated original
// than an elimination is.
const (
	shareIntentEvolutionPct = 10
	shareEliminationsPct    = 25
	shareDecisionsPct       = 20
	shareCurrentWorkPct     = 10
	sharePointersPct        = 20
	shareInstructionsPct    = 15 // the six sum to 100
	dropReportReserveDiv    = 10 // item 7 reserves B/10
)

// shareOf returns pct percent of avail, floored.
func shareOf(avail core.Tokens, pct int) core.Tokens {
	if avail <= 0 {
		return 0
	}
	return avail * core.Tokens(pct) / 100
}

// shareCharsOf is shareOf in the host-character dimension: pct percent of avail, floored.
func shareCharsOf(avail, pct int) int {
	if avail <= 0 {
		return 0
	}
	return avail * pct / 100
}

// sharePctFor maps a discretionary kind to its percentage. ItemUserIntent's share funds only its
// evolution units — the original-intent unit is tier-1 and is admitted whole before any share is
// computed.
func sharePctFor(k ItemKind) (int, bool) {
	switch k {
	case ItemUserIntent:
		return shareIntentEvolutionPct, true
	case ItemEliminations:
		return shareEliminationsPct, true
	case ItemDecisions:
		return shareDecisionsPct, true
	case ItemCurrentWork:
		return shareCurrentWorkPct, true
	case ItemPointers:
		return sharePointersPct, true
	case ItemRestoredInstructions:
		return shareInstructionsPct, true
	default:
		return 0, false
	}
}

// admitted is the outcome of filling one item against an allowance.
type admitted struct {
	units []unit
	// used is the item's whole rendered contribution in both dimensions: its admitted units, plus
	// its section heading and separator once at least one unit is admitted.
	used      cost
	truncated bool
	// abandoned marks an item that could not be emitted at all because even its fixed units (and
	// heading) did not fit what the payload had left. Min-fill never re-admits into it: doing so
	// would render the item without the fixed unit that makes it honest (item 3's note).
	abandoned bool
	// dropped carries one DropEntry per unadmitted unit, in the order they were rejected.
	dropped []checkpoint.DropEntry
	// pending holds those same unadmitted units, in the same order, so the min-fill pass can put
	// them back. A DropEntry records what was lost but not what it cost, and re-admission needs
	// the cost; keeping the units is cheaper than re-running the builder at a second budget.
	pending []unit
}

// fillPrefix admits units in builder order while they fit, and stops at the FIRST unit that does
// not.
//
// This is prefix truncation, and the "stop, do not skip ahead to a smaller unit" rule is the whole
// point of importance ordering (Qompack.md §6.9: an embedded bitstream ordered by importance
// yields the best available reconstruction at ANY truncation point). Skipping ahead would produce
// a payload whose contents depend on the budget in a way no reader could predict, and would break
// the monotonicity property PropBuild_MonotoneInBudget pins: the item set at a smaller budget must
// be a prefix-wise subset of the set at a larger one.
//
// A unit fits when it fits BOTH dimensions of allowance: tokens and host characters (cost). A
// fixed unit — one carrying no DropEntry — is always admitted, and the fixed units still to come
// are held back from the allowance while discretionary ones are considered, so the fixed ones fit
// inside it rather than on top of it. fillWithHeading has already checked that they fit the
// payload at all, which is what keeps the character accounting exact rather than leaving an
// overrun for the hard-cap loop to find.
func fillPrefix(units []unit, allowance cost) admitted {
	out := admitted{units: make([]unit, 0, len(units))}
	truncating := false
	owed := fixedCost(units)
	for _, u := range units {
		// A fixed unit carries no DropEntry, which is how a builder says "this one is not
		// discretionary". Item 3's trailing note is the case that matters: it is emitted LAST, so
		// a prefix fill would drop it first under a tight share — and dropping it both hides how
		// many eliminations were omitted and deletes the standing instruction, which
		// runStandingInstructionCase asserts item 3 carries.
		if isFixedUnit(u) {
			out.units = append(out.units, u)
			out.used = out.used.plus(unitCost(u))
			owed = owed.minus(unitCost(u))
			continue
		}
		if truncating || !out.used.plus(unitCost(u)).plus(owed).within(allowance) {
			truncating = true
			out.dropped = append(out.dropped, u.drop)
			out.pending = append(out.pending, u)
			out.truncated = true
			continue
		}
		out.units = append(out.units, u)
		out.used = out.used.plus(unitCost(u))
	}
	return out
}

// fixedCost is the priced cost of the fixed (never-discretionary) units in units.
func fixedCost(units []unit) cost {
	var c cost
	for _, u := range units {
		if isFixedUnit(u) {
			c = c.plus(unitCost(u))
		}
	}
	return c
}

// fillWithHeading fills units against an allowance that must also cover the item's section heading
// and separator, and charges them only when at least one unit was actually admitted.
//
// Charging the heading up front is what keeps Result.Tokens == the sum over Items honest and the
// character total exact: an Item's cost is its WHOLE rendered contribution, heading included, not
// just its units. Not charging it for an empty item is what stops a heading being paid for a
// section that never renders. chargeHeading is false only for item 2's evolution deltas when the
// tier-1 pass already emitted the section's heading with the verbatim original.
//
// room is what the WHOLE payload has left for this item once every later reserve is held back. The
// heading and the item's fixed units are reserved against it before any discretionary unit is
// considered; when even they do not fit, the item cannot be emitted at all and is abandoned — every
// discretionary unit is reported through its own DropEntry — rather than rendered without the
// fixed unit that makes it honest, or rendered past the ceiling.
func fillWithHeading(d Deps, k ItemKind, b built, units []unit, allowance, room cost, chargeHeading bool) *admitted {
	var head cost
	if chargeHeading {
		head = sectionCost(d, k, b)
	}
	fixed := fixedCost(units)
	if !head.plus(fixed).within(room) {
		a := &admitted{truncated: true, abandoned: true}
		for _, u := range units {
			if isFixedUnit(u) {
				continue
			}
			a.dropped = append(a.dropped, u.drop)
			a.pending = append(a.pending, u)
		}
		return a
	}
	a := fillPrefix(units, allowance.atMost(room).minus(head))
	if len(a.units) > 0 {
		a.used = a.used.plus(head)
	}
	return &a
}

// fillTier1 admits k's tier-1 units whole, in builder order, while they fit room, and stops at the
// first one that does not (the same prefix rule as every other item, ADR 0011 §7). The section
// heading is charged with the first admitted unit.
//
// A tier-1 unit is a whole record — one pinned invariant, the verbatim original prompt, the
// retrieval line — and is never cut: it is emitted whole or it is named as an explicit overflow
// (tier1Drop) carrying the pointer that restores it. Filling record by record rather than item by
// item is what lets forty invariants that fit survive the one that does not.
func fillTier1(d Deps, k ItemKind, b built, units []unit, room cost) *admitted {
	head := sectionCost(d, k, b)
	a := &admitted{units: make([]unit, 0, len(units))}
	for _, u := range units {
		next := a.used.plus(unitCost(u))
		if len(a.units) == 0 {
			next = next.plus(head)
		}
		if a.truncated || !next.within(room) {
			a.truncated = true
			a.dropped = append(a.dropped, tier1Drop(k, u))
			a.pending = append(a.pending, u)
			continue
		}
		a.units = append(a.units, u)
		a.used = next
	}
	return a
}

// tier1Order is the material admitted whole before any share is computed: §8.6's "verbatim,
// always" items plus the affordance line, which is a fixed ~80-token string that must survive to
// tell the agent retrieval exists at all.
//
// ItemUserIntent appears here for its FIRST unit only — the verbatim original. Its evolution
// deltas are discretionary and take a share in shareOrder.
var tier1Order = []ItemKind{ItemInvariants, ItemUserIntent, ItemAffordance}

// tier1Admission is the order step 3 ADMITS tier-1 material in, which is not the order it renders
// in: the retrieval line goes first. It is the smallest tier-1 item and the one every overflow
// pointer depends on — a drop report that says "call expand(…)" to a model that was never told
// expand exists is not a pointer — so, like item 7's floor, it is held before the invariants and
// the original prompt can claim the room (ADR 0011 §18, §21). Rendering still follows renderOrder.
var tier1Admission = []ItemKind{ItemAffordance, ItemInvariants, ItemUserIntent}

// tier1Units is the slice of k's units that step 3 admits whole, ahead of every share.
//
// For every kind but one it is all of them. ItemUserIntent is the exception: §8.6 pins the
// VERBATIM ORIGINAL, and only that — the evolution deltas that follow it are a summary of how the
// ask moved and are discretionary, which is why ItemUserIntent also appears in shareOrder and why
// step 7 refills it from units[1:].
//
// Admitting the whole item here instead would be wrong twice over. The deltas would bypass the
// budget entirely, so a checkpoint with a long evolution list would crowd out items 3 through 6a
// without ever being charged for it; and step 7 would then re-fill the same deltas out of a share,
// drop them for want of allowance, and write drop entries for units the payload is still
// rendering — a drop report that names material the reader can see.
func tier1Units(k ItemKind, b built) []unit {
	if k == ItemUserIntent && len(b.units) > 1 {
		return b.units[:1]
	}
	return b.units
}

// isTier1Kind reports whether k is admitted whole in step 3 rather than out of a share. It is the
// membership test for tier1Order, written as a function so the hard-cap eviction and the fill pass
// cannot disagree about what "tier 1" means.
func isTier1Kind(k ItemKind) bool {
	for _, t := range tier1Order {
		if t == k {
			return true
		}
	}
	return false
}

// evictIndex picks which emitted Item the hard-cap loop removes: the LAST non-tier-1 item, or the
// last item of all when every one of them is tier 1.
//
// Dropping the literal tail would take item 8 (the affordance) and item 7 (the drop report) first,
// which are the two things a payload that has just lost everything else most needs to carry. Both
// are cheap and both are §8.6's answer to "you no longer have the material" — so they are the last
// things to go, not the first. items is assumed non-empty; the caller's loop guarantees it.
func evictIndex(items []Item) int {
	for i := len(items) - 1; i > 0; i-- {
		if !isTier1Kind(items[i].Kind) {
			return i
		}
	}
	return len(items) - 1
}

// shareOrder is the six discretionary items, in renderOrder. The remainder from integer division
// goes to the last entry, so the arithmetic is lossless.
var shareOrder = []ItemKind{
	ItemUserIntent, ItemEliminations, ItemDecisions,
	ItemCurrentWork, ItemPointers, ItemRestoredInstructions,
}

// estimate prices s, falling back to the baseline (len+3)/4 when no estimator is wired.
//
// It lives beside the pricing pass rather than beside Build because every caller is here or in the
// renderer: a nil Estimator degrades the arithmetic to the same formula the real one falls back to
// before calibration, which is what keeps a build with no collaborators wired producing a payload
// rather than a division by nothing.
func estimate(d Deps, s string) core.Tokens {
	if d.Tokens == nil {
		return core.Tokens((len(s) + 3) / 4)
	}
	return d.Tokens.EstimateString(s, tokens.ClassProse)
}

// priceAll fills in every unit's token and host-character cost. Builders deliberately leave both
// zero: pricing needs Deps.Tokens, and keeping it out of the builders is what lets them stay pure
// renderers.
func priceAll(d Deps, all map[ItemKind]built) {
	for k, b := range all {
		priceUnits(d, b.units)
		all[k] = b
	}
}

// priceUnits prices one unit slice in place. The character count is always recomputed: it is exact
// and cheap, and the ceiling is only a guarantee if no unit carries a stale one.
func priceUnits(d Deps, units []unit) {
	for i := range units {
		if units[i].tokens == 0 {
			units[i].tokens = estimate(d, units[i].text)
		}
		units[i].chars = hostChars(units[i].text)
	}
}

// tier1Drop is the DropEntry for a tier-1 unit that could not be admitted whole.
//
// Tier-1 units carry no drop of their own — they are never meant to be discretionary — so when the
// cap forces one out, the report has to name it here rather than reuse a builder's entry. This is
// T11-BUDGET-02's "single oversized critical record" overflow: ID "tier1" is what Overflowed
// recognizes, and Kind stays the ITEM's own kind (not dropKindOverflow) so the report still says
// WHICH essential record could not fit, not merely that something did not.
//
// A builder that knows which record a tier-1 unit is, and how to get it back, says so in the unit's
// overflow entry (buildInvariants, buildUserIntent); that entry is used as-is so the report names
// the record and the call that restores it. The generic entry below is the fallback.
func tier1Drop(k ItemKind, u unit) checkpoint.DropEntry {
	if !isFixedUnit(u) {
		return u.drop
	}
	if u.overflow != (checkpoint.DropEntry{}) {
		return u.overflow
	}
	return checkpoint.DropEntry{
		Kind: k.String(),
		ID:   "tier1",
		Detail: "OVERFLOW: tier-1 material did not fit the rehydration budget and is emitted " +
			"whole or not at all — call dropped() for the full accounting",
	}
}

// dropIDEvicted marks a section that was emitted and then removed WHOLE by the hard-cap eviction
// loop because the assembled payload overran its budget (V6 §5). The DropEntry keeps the section's
// own Kind so the report says which requirement is gone; the ID is what Overflowed recognizes.
const dropIDEvicted = "evicted"

// evictionDrop is the DropEntry for a whole section the hard-cap loop evicted after assembly.
//
// The section's units were admitted and rendered, so they carry no drop of their own — removing them
// silently would erase a current requirement without a word in the report. This names the kind
// (never dropKindOverflow, so the report still says WHICH section is gone) and marks it ID "evicted"
// so Overflowed treats it as the explicit overflow it is.
func evictionDrop(k ItemKind) checkpoint.DropEntry {
	return checkpoint.DropEntry{
		Kind: k.String(),
		ID:   dropIDEvicted,
		Detail: "OVERFLOW: this section was emitted then evicted whole to keep the assembled payload " +
			"within the hard budget cap — call dropped() for the full accounting",
	}
}

// Overflowed reports whether dropped names an EXPLICIT overflow: essential content Build could
// not represent inside the declared budget AT ALL, as opposed to the ordinary discretionary
// truncation every other DropEntry in the report describes.
//
// It recognizes the three shapes Build produces — the fixed injection wrapper alone exceeding a
// zero/tiny budget (Kind dropKindOverflow), a single tier-1/critical record too large to admit whole
// (ID "tier1", Kind the item's own), and a whole section evicted after assembly to hold the hard cap
// (ID dropIDEvicted, Kind the item's own) — so a caller never has to know which internal path
// produced the entry. This is the one predicate 00-ARCHITECTURE.md §5.15's "emit explicit overflow"
// contract is checked against: Result.Degraded alone is not specific enough, because a missing
// checkpoint or an unavailable rule scanner also degrade without ever losing a record Build could
// not even partially represent.
func Overflowed(dropped []checkpoint.DropEntry) bool {
	for _, e := range dropped {
		if e.Kind == dropKindOverflow || e.ID == "tier1" || e.ID == dropIDEvicted {
			return true
		}
	}
	return false
}

// mergeIntent folds a share-funded fill into whatever tier 1 already admitted for the same kind.
//
// Only ItemUserIntent needs it: its original unit was admitted in the tier-1 pass and its
// evolution deltas are filled from a share afterwards, so the two halves must end up in one
// admitted with the units in order and the costs summed. Every other kind passes through.
func mergeIntent(base, add *admitted) *admitted {
	switch {
	case base == nil:
		return add
	case add == nil:
		return base
	}
	base.units = append(base.units, add.units...)
	base.used = base.used.plus(add.used)
	base.dropped = append(base.dropped, add.dropped...)
	base.pending = append(base.pending, add.pending...)
	base.truncated = base.truncated || add.truncated
	return base
}

// collectDrops assembles the complete drop report, in the order §8.6 item 7 renders it:
// what the checkpoint already knew was dropped, then what construction dropped, then what
// budget truncation dropped.
//
// The result is Result.Dropped in full — the rendered section 7 may be truncated to a counted
// line, but the state file and therefore the `dropped` tool always get everything.
func collectDrops(r Request, all map[ItemKind]built, fills map[ItemKind]*admitted) []checkpoint.DropEntry {
	out := make([]checkpoint.DropEntry, 0, len(r.Checkpoint.Dropped)+8)
	out = append(out, r.Checkpoint.Dropped...)
	for _, k := range renderOrder {
		out = append(out, all[k].drops...)
	}
	for _, k := range renderOrder {
		if a := fills[k]; a != nil {
			out = append(out, a.dropped...)
		}
	}
	// A zero DropEntry is not a drop; it is a fixed unit's empty slot.
	kept := out[:0]
	for _, e := range out {
		if e != (checkpoint.DropEntry{}) {
			kept = append(kept, e)
		}
	}
	return kept
}

// minFill re-admits previously dropped units toward cfg.MinTokens.
//
// It runs ONLY for an unset request — the daemon's own call, where B == maxTokens. When a caller
// named a budget, raising it is precisely what the hard cap forbids, so min-fill does not run at
// all. This is what makes minTokens a fill target rather than dead configuration: §8.6 says
// "target 8-12K", and a payload that stopped at 3K while 9K of ranked material was available and
// the cap was 12K would be leaving quality on the table for no reason.
//
// It walks the truncated items in renderOrder and re-admits their dropped units in the order they
// were dropped, removing the corresponding drop entries, while the slack covers them. The slack is
// two-dimensional: the token target, and the host-character ceiling less what is held for item 7 —
// min-fill may never spend the room the overflow report needs. A unit re-admitted into an item
// that had emitted nothing also pays that item's heading and separator. An abandoned item is never
// refilled: its fixed units did not fit, and refilling it would render it without them.
func minFill(d Deps, all map[ItemKind]built, items map[ItemKind]*admitted, order []ItemKind,
	spent cost, floor core.Tokens, limit, held cost,
) cost {
	target := floor
	if top := limit.tok - held.tok; target > top {
		target = top
	}
	slack := cost{tok: target - spent.tok, chars: limit.chars - held.chars - spent.chars}
	if slack.tok <= 0 || slack.chars <= 0 {
		return spent
	}
	for _, k := range order {
		a := items[k]
		if a == nil || a.abandoned || !a.truncated || len(a.dropped) == 0 {
			continue
		}
		// The dropped units are gone; their costs are not recoverable from the DropEntry alone,
		// so re-admission is driven by the carried-over unit list the caller stashes in pending.
		for len(a.pending) > 0 {
			u := a.pending[0]
			need := unitCost(u)
			if len(a.units) == 0 {
				need = need.plus(sectionCost(d, k, all[k]))
			}
			if !need.within(slack) {
				break
			}
			a.units = append(a.units, u)
			a.used = a.used.plus(need)
			a.pending = a.pending[1:]
			a.dropped = a.dropped[1:]
			slack = slack.minus(need)
			spent = spent.plus(need)
		}
		if len(a.pending) == 0 {
			a.truncated = false
		}
		if slack.tok <= 0 || slack.chars <= 0 {
			break
		}
	}
	return spent
}

// moreSample is the count dropReportFloor prices item 7's counted tail with. Seven digits is wider
// than any drop report a checkpoint can produce; pricing the wide case means the floor can never
// under-reserve the tail it exists to guarantee.
const moreSample = 9999999

// dropReportFloor is the smallest item 7 that still reports an omission: its heading and separator
// plus the counted tail ("- … and N more; call dropped()"). Build holds it back from tier 1 onward,
// so whatever the payload has to omit, it can always say so and name the call that lists it —
// the overflow report fits inside the ceiling by construction rather than by luck (D5).
func dropReportFloor(d Deps) cost {
	return sectionCost(d, ItemDropReport, built{}).plus(unitCost(moreDropsUnit(d, moreSample)))
}

// fillDropReport fills item 7 against allowance: every line when they all fit, otherwise the
// longest PREFIX of lines that fits beside the counted tail naming how many were left out.
//
// Its lines are not fixed units even though they carry no DropEntry of their own: they are the
// report on everything else, the complete list is persisted to the state file regardless, and the
// tail tells the model to call dropped() for the rest. Admitting them wholesale, as a fixed unit
// would be, is what used to push a small payload over its cap and get item 7 evicted — leaving a
// payload that had lost most of its material with no word that it had.
func fillDropReport(d Deps, b built, allowance cost) *admitted {
	head := sectionCost(d, ItemDropReport, b)
	whole := head.plus(sumCost(b.units))
	if whole.within(allowance) {
		return &admitted{units: append([]unit(nil), b.units...), used: whole}
	}
	best := -1
	var bestTail unit
	used := head
	for keep := 0; keep < len(b.units); keep++ {
		tail := moreDropsUnit(d, len(b.units)-keep)
		if used.plus(unitCost(tail)).within(allowance) {
			best, bestTail = keep, tail
		}
		used = used.plus(unitCost(b.units[keep]))
		if !used.within(allowance) {
			break
		}
	}
	if best < 0 {
		// Not even the heading and the tail fit. The floor Build holds makes this unreachable short
		// of a report with ten million lines; the state file still carries every entry.
		return &admitted{truncated: true}
	}
	a := &admitted{units: append(append([]unit(nil), b.units[:best]...), bestTail), truncated: true}
	a.used = head.plus(sumCost(a.units))
	return a
}
