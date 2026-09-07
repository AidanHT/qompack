package checkpoint

import (
	"fmt"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/tokens"
)

// The DropEntry kinds every producer in this package spells exactly once. The first eight are
// Truncate's (§6.9, the §10 cut table); the pointer_* kinds are ValidatePointers' ground-truth
// findings (G2.5) plus Finalize's store check. They are unexported because the STRINGS are the
// contract — they appear verbatim in written artifacts — and no other package mints them.
const (
	dropNarrative             = "narrative"
	dropToolPointer           = "tool_pointer"
	dropFilePointer           = "file_pointer"
	dropOpenQuestion          = "open_question"
	dropAlternatives          = "alternatives"
	dropDecision              = "decision"
	dropNextStep              = "next_step"
	dropBudgetExceeded        = "budget_exceeded"
	dropPointerMissing        = "pointer_missing"
	dropPointerInvalid        = "pointer_invalid"
	dropPointerDirty          = "pointer_dirty"
	dropPointerUntracked      = "pointer_untracked"
	dropPointerGitUnavailable = "pointer_git_unavailable"
	dropPointerUnresolvable   = "pointer_unresolvable"
)

// sizeOf measures c the one way §6.9 defines: canonical Marshal bytes priced as ClassJSON. A
// marshal error is impossible for a well-formed Checkpoint and yields sizeOf == 0, which fits
// any budget and therefore cuts nothing — the failure mode is "too big survives", never "tier 1
// is destroyed by a measurement bug".
func sizeOf(c Checkpoint, est tokens.Estimator) core.Tokens {
	b, _ := Marshal(c)
	return est.Estimate(b, tokens.ClassJSON)
}

// cutStage is one step of the fixed §6.9 cut order: the JSON field name whose config tier
// membership gates it, and the method that applies it.
type cutStage struct {
	field string
	run   func(*truncation)
}

// truncateCutOrder is the WITHIN-TIER cut order, fixed in code exactly as §10 spells it —
// config decides membership, not order. Under the shipped default tiers
// (First: pointers, narrative; Late: decisions, open_questions, current_work) the two passes
// Truncate makes over this list are precisely the plan's two blocks:
//
//	tier3CutOrder = narrative → pointers.tools (tail-first) → pointers.files (tail-first)
//	tier2CutOrder = open_questions (tail-first) → decisions[].alternatives_rejected (one step)
//	              → decisions (tail-first) → current_work.next_step → ""
//
// Keeping ONE ordered list and filtering it per pass is what lets a redistributed membership
// (say, narrative moved to Late) still cut in a defined position instead of silently never
// cutting at all.
var truncateCutOrder = []cutStage{
	{field: "narrative", run: (*truncation).cutNarrative},
	{field: "pointers", run: (*truncation).cutToolPointers},
	{field: "pointers", run: (*truncation).cutFilePointers},
	{field: "open_questions", run: (*truncation).cutOpenQuestions},
	{field: "decisions", run: (*truncation).emptyAlternatives},
	{field: "decisions", run: (*truncation).cutDecisions},
	{field: "current_work", run: (*truncation).cutNextStep},
}

// Truncate applies the §6.9 importance ordering to c against budget: tier 3 (pointers,
// narrative) is dropped first, tier 2 (decisions, open questions, current work) only after tier
// 3 is exhausted, and tier 1 (invariants, user intent, eliminations) never. Which field sits in
// which tier comes from t (config.Checkpoint.Tiers), whose entries are the JSON key names; a
// field listed in t.Never is never cut regardless of the fixed order. budget <= 0 means
// unlimited: the input comes back unchanged with a nil drop list.
//
// Slices are cut tail-first because that is lowest-value-first: the writer keeps pointer and
// decision slices in DESCENDING turn order (§8, "Pointer ordering" and "Decisions"), so index 0
// is the newest and the tail is the oldest. Truncate does not sort — recency is the only
// ordering signal available here that does not require re-running a slice, and
// TestTruncateDropsPointersTailFirst / TestAdvanceKeepsPointersNewestFirst pin the two halves of
// that contract together.
//
// Because Marshal is cheap relative to the budget check, Truncate re-measures after every cut
// GROUP, not every element: within a stage it cuts 1, 2, 4, 8, … elements between measurements,
// then binary-searches back to the smallest cut that fits (tailCut).
//
// Every removed element appends exactly one DropEntry with the §10 table's Kind/ID/Detail. The
// entries are RETURNED, never folded into c.Dropped — the caller (Finalize) owns the artifact's
// drop report, and folding them in here would make each cut grow the document it is trying to
// shrink. If both cuttable tiers are exhausted and the document still exceeds budget, the
// tier-1-only checkpoint is returned with one budget_exceeded entry: tier 1 is never truncated
// and Truncate never returns an error, so a checkpoint always gets written (G7.4 — the session
// survives even when everything else has given up).
//
// Truncate never mutates c: the working copy shares backing arrays with the input only for data
// it does not modify (tail cuts are re-slices; the alternatives step clones the decision slice).
func Truncate(c Checkpoint, budget core.Tokens, t config.TiersCfg, est tokens.Estimator) (Checkpoint, []DropEntry) {
	if budget <= 0 {
		return c, nil
	}
	tr := &truncation{c: c, budget: budget, est: est}
	if tr.fits(c) {
		return c, nil
	}
	for _, members := range [][]string{t.First, t.Late} {
		for _, stage := range truncateCutOrder {
			if !tierLists(members, stage.field) || tierLists(t.Never, stage.field) {
				continue
			}
			stage.run(tr)
			if tr.done {
				return tr.c, tr.drops
			}
		}
	}
	tr.drops = append(tr.drops, DropEntry{
		Kind:   dropBudgetExceeded,
		ID:     "tier1",
		Detail: fmt.Sprintf("tier 1 is %d tokens against a %d budget; written in full per §8.5", sizeOf(tr.c, est), budget),
	})
	return tr.c, tr.drops
}

// tierLists reports whether the tier list names field.
func tierLists(list []string, field string) bool {
	for _, f := range list {
		if f == field {
			return true
		}
	}
	return false
}

// truncation is the walking state of one Truncate call: the working copy, the accumulating drop
// report, and done, which flips the moment the document fits and stops every later stage.
type truncation struct {
	c      Checkpoint
	budget core.Tokens
	est    tokens.Estimator
	drops  []DropEntry
	done   bool
}

// fits reports whether c is inside the budget.
func (tr *truncation) fits(c Checkpoint) bool { return sizeOf(c, tr.est) <= tr.budget }

// tailCut finds how many tail elements of an n-element slice must go: the smallest count whose
// removal brings the document inside budget, found with a doubling probe (cut 1, 2, 4, 8, …
// between measurements) and a binary search back once a fitting cut is bracketed. It returns
// (count, true) when that count fits, and (n, false) when even removing everything leaves the
// document over budget — the stage is then exhausted and the walk moves on. withCut must return
// the candidate document with only the leading keep elements retained.
//
// Both halves rely on the same monotonicity: removing more elements never grows the canonical
// document, so "fits" is monotone in the cut count.
func (tr *truncation) tailCut(n int, withCut func(keep int) Checkpoint) (int, bool) {
	insufficient := 0 // cutting this many is known not to fit
	probe := 1
	for {
		k := probe
		if k > n {
			k = n
		}
		if tr.fits(withCut(n - k)) {
			lo, hi := insufficient+1, k
			for lo < hi {
				mid := lo + (hi-lo)/2
				if tr.fits(withCut(n - mid)) {
					hi = mid
				} else {
					lo = mid + 1
				}
			}
			return lo, true
		}
		if k == n {
			return n, false
		}
		insufficient = k
		probe <<= 1
	}
}

// cutNarrative is the first cut of the walk: prose residue is the §6.9 last-resort field, dropped
// first and whole.
func (tr *truncation) cutNarrative() {
	if tr.c.Narrative == "" {
		return
	}
	tr.c.Narrative = ""
	tr.drops = append(tr.drops, DropEntry{Kind: dropNarrative, ID: dropNarrative, Detail: "prose residue dropped at budget"})
	tr.done = tr.fits(tr.c)
}

// cutToolPointers drops tool pointers tail-first — oldest first, see Truncate — one DropEntry
// per pointer, keyed by tool_use_id so `expand(hash)` remains answerable about what went.
func (tr *truncation) cutToolPointers() {
	n := len(tr.c.Pointers.Tools)
	if n == 0 {
		return
	}
	k, fit := tr.tailCut(n, func(keep int) Checkpoint {
		c := tr.c
		c.Pointers.Tools = c.Pointers.Tools[:keep]
		return c
	})
	cut := tr.c.Pointers.Tools[n-k:]
	tr.c.Pointers.Tools = tr.c.Pointers.Tools[:n-k]
	for i := len(cut) - 1; i >= 0; i-- { // tail-first: the oldest pointer is cut, and reported, first
		tr.drops = append(tr.drops, DropEntry{
			Kind:   dropToolPointer,
			ID:     string(cut[i].ToolUseID),
			Detail: "truncated at budget; expand(hash) still resolves",
		})
	}
	tr.done = fit
}

// cutFilePointers drops file pointers tail-first, one DropEntry per pointer, keyed by path so
// `re_read(path)` remains answerable about what went.
func (tr *truncation) cutFilePointers() {
	n := len(tr.c.Pointers.Files)
	if n == 0 {
		return
	}
	k, fit := tr.tailCut(n, func(keep int) Checkpoint {
		c := tr.c
		c.Pointers.Files = c.Pointers.Files[:keep]
		return c
	})
	cut := tr.c.Pointers.Files[n-k:]
	tr.c.Pointers.Files = tr.c.Pointers.Files[:n-k]
	for i := len(cut) - 1; i >= 0; i-- {
		tr.drops = append(tr.drops, DropEntry{
			Kind:   dropFilePointer,
			ID:     cut[i].Path,
			Detail: "truncated at budget; re_read(path) still resolves",
		})
	}
	tr.done = fit
}

// cutOpenQuestions is the first tier-2 cut: open questions go tail-first, and each DropEntry
// carries the question text itself (capped at pointerWhyMaxRunes) — an open question has no
// retrieval handle, so its text is the only identity worth keeping.
func (tr *truncation) cutOpenQuestions() {
	n := len(tr.c.OpenQuestions)
	if n == 0 {
		return
	}
	k, fit := tr.tailCut(n, func(keep int) Checkpoint {
		c := tr.c
		c.OpenQuestions = c.OpenQuestions[:keep]
		return c
	})
	cut := tr.c.OpenQuestions[n-k:]
	tr.c.OpenQuestions = tr.c.OpenQuestions[:n-k]
	for i := len(cut) - 1; i >= 0; i-- {
		tr.drops = append(tr.drops, DropEntry{
			Kind:   dropOpenQuestion,
			ID:     fmt.Sprintf("oq_%d", n-k+i),
			Detail: capRunes(cut[i], pointerWhyMaxRunes),
		})
	}
	tr.done = fit
}

// emptyAlternatives empties alternatives_rejected on EVERY decision in one step (§10): the
// decisions themselves — id, what, why, evidence — are worth far more than their rejected
// alternatives, so the alternatives all go before a single decision does. The decision slice is
// cloned so the caller's copy is never written through.
func (tr *truncation) emptyAlternatives() {
	emptied := false
	for _, d := range tr.c.Decisions {
		if len(d.AlternativesRejected) > 0 {
			emptied = true
			break
		}
	}
	if !emptied {
		return
	}
	ds := append([]Decision(nil), tr.c.Decisions...)
	for i := range ds {
		if len(ds[i].AlternativesRejected) == 0 {
			continue
		}
		// An explicit empty slice, not nil: alternatives_rejected has no omitempty, and a nil
		// here would marshal as null where the schema shows an array.
		ds[i].AlternativesRejected = []string{}
		tr.drops = append(tr.drops, DropEntry{
			Kind:   dropAlternatives,
			ID:     string(ds[i].ID),
			Detail: "alternatives_rejected emptied at budget",
		})
	}
	tr.c.Decisions = ds
	tr.done = tr.fits(tr.c)
}

// cutDecisions drops whole decisions tail-first, one DropEntry per decision, keyed by decision
// id so `why(decision_id)` remains answerable about what went.
func (tr *truncation) cutDecisions() {
	n := len(tr.c.Decisions)
	if n == 0 {
		return
	}
	k, fit := tr.tailCut(n, func(keep int) Checkpoint {
		c := tr.c
		c.Decisions = c.Decisions[:keep]
		return c
	})
	cut := tr.c.Decisions[n-k:]
	tr.c.Decisions = tr.c.Decisions[:n-k]
	for i := len(cut) - 1; i >= 0; i-- {
		tr.drops = append(tr.drops, DropEntry{
			Kind:   dropDecision,
			ID:     string(cut[i].ID),
			Detail: "truncated at budget; why(decision_id) still resolves",
		})
	}
	tr.done = fit
}

// cutNextStep is the LAST tier-2 cut (§10): next_step is blanked, while goal and blocked_on are
// kept — what the work is for and what it waits on survive even when how to proceed does not.
func (tr *truncation) cutNextStep() {
	if tr.c.CurrentWork.NextStep == "" {
		return
	}
	tr.c.CurrentWork.NextStep = ""
	tr.drops = append(tr.drops, DropEntry{Kind: dropNextStep, ID: "current_work.next_step", Detail: "truncated at budget"})
	tr.done = tr.fits(tr.c)
}
