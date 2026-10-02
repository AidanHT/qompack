package rehydrate

import (
	"strings"

	"github.com/qompack/qompack/internal/checkpoint"
)

// A degraded compaction that dropped material is never silent (coordinator decision D59 under D33;
// UAT-05 F-C7-UAT05-1 on candidate 7). ADR 0011 §21 made "a payload whose only admitted section would
// be item 7" no payload, and §22.1 ends tier 1 at a refused retrieval line, so at a budget too small
// for the retrieval line (150 tokens in UAT-05) the compaction injected nothing while fifteen records,
// the original request among them, were left out: the session heard nothing of the loss. When no
// section is admitted but material was dropped, the payload is this notice in section 7 instead,
// counted against the budget like any section. "No payload" still holds when nothing was dropped.

// lossNotice renders the notice for a build that admitted no section, over its complete drop set: how
// many items did not fit, that dropped() lists them with the calls that restore each (the user's
// /qompack:dropped shows the same), and the restore pointer for the verbatim original when it is a
// tier-1 overflow. It returns the largest form that fits limit — the token half priced on the
// assembled payload, the character half exact — falling to the smallest form that still says "N items
// dropped; call dropped()". It returns an empty Result when nothing was dropped or no form fits; Build
// then records the overrun as an overflow.
func lossNotice(r Request, d Deps, dropped []checkpoint.DropEntry, limit cost) (Result, []ItemStat) {
	if len(dropped) == 0 {
		return Result{}, nil
	}
	count := itoa(len(dropped)) + " items"
	if len(dropped) == 1 {
		count = "1 item"
	}
	long := "- " + count + " did not fit the rehydration budget; call dropped() to list each with the " +
		"call that restores it (the user's /qompack:dropped shows the same list)\n"
	smallest := "- " + count + " dropped; call dropped()\n"
	forms := [][]string{{long}, {smallest}}
	if p := originalRestorePointer(dropped); p != "" {
		original := "- the original request, verbatim: " + p + "\n"
		forms = [][]string{{long, original}, {smallest, original}, {smallest}}
	}
	seen := len(buildDropReport(dropped).units)
	for _, lines := range forms {
		items := []Item{{Kind: ItemDropReport, Text: itemText(ItemDropReport, 0, 0, lines), Truncated: true}}
		text := renderText(r, items)
		tok := estimate(d, text)
		if tok > limit.tok || hostChars(text) > limit.chars {
			continue
		}
		items[0].Tokens = tok
		stats := []ItemStat{{
			Kind: ItemDropReport.String(), Tokens: tok, Truncated: true, Units: len(lines), UnitsSeen: seen,
		}}
		return Result{Items: items, Text: text, Tokens: tok, Degraded: true}, stats
	}
	return Result{}, nil
}

// originalRestorePointer is the call that restores the verbatim original when the drop set names it
// as a tier-1 overflow (tier1Overflow's "; restore: <pointer>"), and "" otherwise.
func originalRestorePointer(dropped []checkpoint.DropEntry) string {
	for _, e := range dropped {
		if e.Kind != ItemUserIntent.String() || e.ID != "tier1" {
			continue
		}
		if i := strings.LastIndex(e.Detail, restorePrefix); i >= 0 {
			return oneLine(e.Detail[i+len(restorePrefix):])
		}
	}
	return ""
}

// noticeOverflow is the drop entry of a build that dropped material and could inject not even the
// notice's smallest form: what the wrapper-alone overflow says, for the case where the wrapper fits.
func noticeOverflow(budget int) checkpoint.DropEntry {
	return checkpoint.DropEntry{
		Kind: dropKindOverflow, ID: "payload",
		Detail: "OVERFLOW: the rehydration budget (" + itoa(budget) + " tokens) cannot hold even the " +
			"notice naming what was dropped; nothing was injected — call dropped() for the full accounting",
	}
}
