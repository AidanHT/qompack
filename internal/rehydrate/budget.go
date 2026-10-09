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
		if !truncating && u.bare != nil && !out.used.plus(unitCost(u)).plus(owed).within(allowance) {
			// The unit with its inlined result does not fit; its bare pointer line may.
			u = *u.bare
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
		return abandon(units)
	}
	a := fillPrefix(units, allowance.atMost(room).minus(head))
	if len(a.units) > 0 {
		a.used = a.used.plus(head)
	}
	return &a
}

// fillTier1 admits k's tier-1 units whole, in builder order, while they fit room. The section
// heading is charged with the first admitted unit.
//
// A tier-1 unit is a whole record — one pinned invariant, the verbatim original prompt, the
// retrieval line — and is never cut: it is emitted whole or it is named as an explicit overflow
// (tier1Drop) carrying the pointer that restores it. Filling record by record rather than item by
// item is what lets forty invariants that fit survive the one that does not.
//
// Tier 1 is ONE prefix over tier1Admission, not one prefix per item (ADR 0011 §22.1): the first
// record the budget cannot hold ends it, closed reports that an earlier item's record already
// did, and every record from there on is refused and named even when it is small enough to fit
// what is left. Letting a later, smaller record through is the cheapest-first fill §7 forbids. It
// kept the pinned invariant and lost the retrieval line at 150 tokens and did the reverse at 160
// (F-C4-UAT05-3), so what survived stopped following the admission order and stopped growing with
// the budget.
//
// One record is passed over without ending tier 1: one no payload could hold, because it and its
// heading alone exceed the host character ceiling less the wrapper and item 7's floor (reserved).
// No budget ever admits it, so leaving it out is not a budget cut, and ending tier 1 there would
// let one oversized pin take the original prompt and every share down with it at every budget —
// the loss §21.1 exists to prevent. It is named exactly like an L0 capture past intentReadLimit.
// The ceiling is a host constant, so this reads the character dimension only: a record the token
// budget cannot hold is always a budget cut, however small that budget is.
//
// The second result reports whether tier 1 is closed after this item.
func fillTier1(d Deps, k ItemKind, b built, units []unit, room, reserved cost, closed bool) (*admitted, bool) {
	head := sectionCost(d, k, b)
	a := &admitted{units: make([]unit, 0, len(units))}
	for _, u := range units {
		next := a.used.plus(unitCost(u))
		if len(a.units) == 0 {
			next = next.plus(head)
		}
		if closed || !next.within(room) {
			// Only a record some payload could hold ends tier 1.
			closed = closed || representable(head, u, reserved)
			a.truncated = true
			a.dropped = append(a.dropped, tier1Drop(k, u))
			a.pending = append(a.pending, u)
			continue
		}
		a.units = append(a.units, u)
		a.used = next
	}
	return a, closed
}

// representable reports whether a tier-1 unit u, with its section heading head, could be emitted
// in SOME payload: whether it fits the host character ceiling once reserved (the wrapper and item
// 7's floor) is held back. See fillTier1.
func representable(head cost, u unit, reserved cost) bool {
	return head.chars+u.chars <= PayloadCeilingChars-reserved.chars
}

// abandon refuses every unit of an item that is not filled at all: each discretionary unit is
// named through its own DropEntry and kept pending, and fixed units are not rendered without the
// records they accompany. It is fillWithHeading's abandoned outcome, for an item Build does not
// offer any room to.
func abandon(units []unit) *admitted {
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

// tier1Admission is the material admitted whole before any share is computed — §8.6's "verbatim,
// always" items plus the affordance line, a fixed ~80-token string that must survive to tell the
// agent retrieval exists at all — in the order step 3 ADMITS it, which is not the order it renders
// in: the retrieval line goes first. It is the smallest tier-1 item and the one every overflow
// pointer depends on — a drop report that says "call expand(…)" to a model that was never told
// expand exists is not a pointer — so, like item 7's floor, it is held before the invariants and
// the original prompt can claim the room (ADR 0011 §18, §21). Rendering still follows renderOrder.
//
// ItemUserIntent is here for its tier-1 units only (tier1Units). Its older evolution deltas are
// discretionary and take a share in shareOrder.
var tier1Admission = []ItemKind{ItemAffordance, ItemInvariants, ItemUserIntent}

// tier1Units is the slice of k's units that step 3 admits whole, ahead of every share.
//
// For every kind but one it is all of them. ItemUserIntent is the exception: §8.6 pins the
// VERBATIM ORIGINAL, and the evolution deltas that follow it are a record of how the ask moved,
// which is why ItemUserIntent also appears in shareOrder and why step 7 refills it from what
// follows these units. Step 3 admits the original and the NEWEST restatement — the correction in
// force (owner decision D49, F-C4-UAT06-3). Evolution holds every later prompt, so a correction
// admitted only out of item 2's tenth is pushed out by a handful of ordinary prompts; the newest
// restatement is the current authority, and admitting it with the original is what keeps the
// original from standing alone as the requirement. It is one whole record and, like every tier-1
// record, is emitted whole or named with the pointer that restores it. The builder orders the
// deltas newest first, so it is the first unit after the original (or the first unit, when there
// is no original unit to show).
//
// Admitting every delta here instead would be wrong twice over. The deltas would crowd out items 3
// through 6a before any share is computed; and step 7 would then re-fill the same deltas out of a
// share, drop them for want of allowance, and write drop entries for units the payload is still
// rendering — a drop report that names material the reader can see. The older deltas keep the
// share, and whatever room the payload leaves unused goes to them after every share (Build step 9a).
func tier1Units(k ItemKind, b built) []unit {
	if k != ItemUserIntent {
		return b.units
	}
	n := 1 // the newest restatement
	if len(b.units) > 0 && isFixedUnit(b.units[0]) {
		n++ // the verbatim original before it
	}
	if n > len(b.units) {
		n = len(b.units)
	}
	return b.units[:n]
}

// admissionRank is where Build admits k's section, first admitted lowest: item 7, whose floor is
// held back before anything is admitted; then tier 1 in tier1Admission order (the retrieval line,
// the invariants, item 2); then the share-taking items and the skill index, in render order.
func admissionRank(k ItemKind) int {
	if k == ItemDropReport {
		return 0
	}
	for i, t := range tier1Admission {
		if t == k {
			return 1 + i
		}
	}
	return 1 + len(tier1Admission) + int(k)
}

// evictIndex picks which emitted Item the hard-cap loop removes: the one Build admitted LAST
// (admissionRank), so eviction is the fill run backwards and a payload the loop trims holds the
// same kind of prefix a smaller budget's fill would have produced.
//
// That puts every share-taking item and the skill index before tier 1, and — the part the tail
// fallback got wrong (F-C4-UAT05-3) — tier 1 in the reverse of ADR 0011 §21.2's admission order:
// item 2, then the invariants, then the retrieval line. Dropping the literal tail would take item
// 8 and item 7 first, which are the two things a payload that has just lost everything else most
// needs to carry (§18); evicting the retrieval line before the invariants left a payload of pins
// with pointers the model was never told how to follow. Item 7 goes last because its floor is held
// before anything else is admitted; a payload of item 7 alone is no payload, so once it and the
// retrieval line are all that is left and they do not fit, nothing is injected — the same outcome
// the fill pass reaches at a budget that cannot hold both. items is assumed non-empty; the caller's
// loop guarantees it.
func evictIndex(items []Item) int {
	last := 0
	for i := range items {
		if admissionRank(items[i].Kind) > admissionRank(items[last].Kind) {
			last = i
		}
	}
	return last
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
		if units[i].bare != nil {
			bare := []unit{*units[i].bare}
			priceUnits(d, bare)
			units[i].bare = &bare[0]
		}
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
// line, but the state file and therefore the `dropped` tool always get everything. No reason in it
// shows an absolute path outside the project or a path the build withholds (gateDropReasons, D61).
func collectDrops(r Request, d Deps, all map[ItemKind]built, fills map[ItemKind]*admitted) []checkpoint.DropEntry {
	out := make([]checkpoint.DropEntry, 0, len(r.Checkpoint.Dropped)+8)
	if fellBack(r) {
		out = append(out, fallbackDrop(r))
	}
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
	return gateDropReasons(kept, pathJudgeFor(r, d))
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

// refusedNewest reports whether step 3 refused item 2's newest restatement: whether its tier-1
// fill left a discretionary unit pending. Its older deltas then take no share and no unused room
// (see the share loop in Build).
func refusedNewest(a *admitted) bool {
	if a == nil {
		return false
	}
	for _, u := range a.pending {
		if !isFixedUnit(u) {
			return true
		}
	}
	return false
}

// withoutKind returns order with k left out, as a new slice: min-fill's order when item 2 may not
// be re-admitted (Build step 9).
func withoutKind(order []ItemKind, k ItemKind) []ItemKind {
	out := make([]ItemKind, 0, len(order))
	for _, o := range order {
		if o != k {
			out = append(out, o)
		}
	}
	return out
}
