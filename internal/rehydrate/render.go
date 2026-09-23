package rehydrate

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
)

// renderOrder is the order Build fills items in, and the source of Item.Rank.
//
// It IS the iota order of ItemKind, and TestRenderOrder_IsIotaOrder pins that. The duplication is
// deliberate: types.go's constants declare the §8.6 importance ordering as a type, and this slice
// declares it as an iteration, so a kind inserted in one place and not the other fails loudly
// instead of silently changing what survives a small budget. Budget truncation drops from the
// tail, so this list is what decides that.
var renderOrder = []ItemKind{
	ItemInvariants, ItemUserIntent, ItemEliminations, ItemDecisions, ItemCurrentWork,
	ItemPointers, ItemRestoredInstructions, ItemSkillIndex, ItemDropReport, ItemAffordance,
}

// String returns k's stable, snake_case name. It is what ItemStat.Kind records and what log lines
// carry, so it is part of the drop-report state file's shape and must not be reworded.
func (k ItemKind) String() string {
	switch k {
	case ItemInvariants:
		return "invariants"
	case ItemUserIntent:
		return "user_intent"
	case ItemEliminations:
		return "eliminations"
	case ItemDecisions:
		return "decisions"
	case ItemCurrentWork:
		return "current_work"
	case ItemPointers:
		return "pointers"
	case ItemRestoredInstructions:
		return "restored_instructions"
	case ItemSkillIndex:
		return "skill_index"
	case ItemDropReport:
		return "drop_report"
	case ItemAffordance:
		return "affordance"
	default:
		return "unknown(" + itoa(int(k)) + ")"
	}
}

// sectionHeading returns k's "## N. Title" line, without a trailing newline.
//
// Item 3's rendered heading additionally carries " (top N of M by slice relevance)", which
// depends on the build and therefore cannot live in a static table; eliminationsHeading appends
// it and itemText calls that. This function returns the stable half, which is what headingCost
// and the heading-coverage test are written against.
func sectionHeading(k ItemKind) string {
	switch k {
	case ItemInvariants:
		return "## 1. Invariants (pinned, verbatim)"
	case ItemUserIntent:
		return "## 2. Original user intent (verbatim from L0 capture — never summarized)"
	case ItemEliminations:
		return "## 3. Approaches already eliminated"
	case ItemDecisions:
		return "## 4. Decisions"
	case ItemCurrentWork:
		return "## 5. Current work"
	case ItemPointers:
		return "## 6. Pointers (paths and hashes — contents are NOT restored; use expand/re_read)"
	case ItemRestoredInstructions:
		return "## 6a. Restored instructions (re-read from disk by Qompack; the host does not restore these)"
	case ItemSkillIndex:
		return "## 6b. Skill index (names and one-line descriptions only)"
	case ItemDropReport:
		return "## 7. No longer in context"
	case ItemAffordance:
		return "## 8. Retrieval"
	default:
		return "## " + k.String()
	}
}

// eliminationsHeading is item 3's rendered heading: the static half plus the "(top N of M by
// slice relevance)" honesty clause §8.6 asks for.
//
// shown and seen are the BUILDER's counts — how many elimination lines buildEliminations produced
// and how many candidates it ranked — not the counts that survived budgeting. That is deliberate:
// the trailing note unit is rendered from the same pair at build time, so heading and note always
// agree with each other, and anything the budget later cut is named in item 7 instead.
func eliminationsHeading(shown, seen int) string {
	return sectionHeading(ItemEliminations) +
		" (top " + itoa(shown) + " of " + itoa(seen) + " by slice relevance)"
}

// headingCountWidthSample is the count headingCost prices item 3's heading with. Pricing the
// widest realistic pair over-reserves by at most a token and can never under-reserve, which is
// the direction that matters: an under-reserved heading pushes the payload over the hard cap and
// forces Build's re-truncation loop to fire.
const headingCountWidthSample = 99999

// headingCost prices one item's heading line, including its newline. It is charged to the item
// alongside its units so that Result.Tokens is the true sum over Items.
func headingCost(d Deps, k ItemKind) core.Tokens {
	h := sectionHeading(k)
	if k == ItemEliminations {
		h = eliminationsHeading(headingCountWidthSample, headingCountWidthSample)
	}
	return estimate(d, h+"\n")
}

// itemText renders one item's section: its heading line, then the admitted unit texts in order.
//
// Every unit text already ends in exactly one "\n" (the builder contract), so the section does
// too — which is the invariant renderBody's blank-line join depends on. An item with no admitted
// units renders as "" and contributes no section at all, rather than a bare heading.
//
// shown and seen parameterize item 3's heading and are ignored by every other kind; pass
// eliminationsShown(b) and b.seen from the item's own `built`.
func itemText(k ItemKind, shown, seen int, units []string) string {
	if len(units) == 0 {
		return ""
	}
	var b strings.Builder
	if k == ItemEliminations {
		b.WriteString(eliminationsHeading(shown, seen))
	} else {
		b.WriteString(sectionHeading(k))
	}
	b.WriteByte('\n')
	for _, u := range units {
		b.WriteString(u)
	}
	return b.String()
}

// documentHeader is the payload's first line: "# Qompack rehydration — checkpoint 0007, session
// 3f2a9c81".
//
// The sequence is %04d of Ref.Seq (wider sequences are not truncated, only unpadded ones are
// padded) and the session is its first eight characters. Eight characters is enough to identify a
// payload in a transcript and cheap enough not to matter; the full id is in the state file.
func documentHeader(r Request) string {
	return fmt.Sprintf("# Qompack rehydration — checkpoint %04d, session %s",
		int(r.Ref.Seq), shortSession(r.Session))
}

// sessionHeaderRunes is how much of the session id the document header carries.
const sessionHeaderRunes = 8

// shortSession truncates s to sessionHeaderRunes runes. It cuts on a rune boundary rather than a
// byte one: a session id is host-supplied, and a half-rune would reach the model as U+FFFD.
func shortSession(s core.SessionID) string {
	return truncRunes(string(s), sessionHeaderRunes, "")
}

// renderBody assembles the payload body: the document header, then each non-empty item's section,
// separated by exactly one blank line.
//
// The returned body carries NO trailing newline — Wrap supplies the one that precedes the close
// tag, so that Unwrap(Wrap(seq, body)) == body exactly.
func renderBody(r Request, items []Item) string {
	parts := make([]string, 0, len(items)+1)
	parts = append(parts, documentHeader(r)+"\n")
	for _, it := range items {
		if it.Text == "" {
			continue
		}
		parts = append(parts, it.Text)
	}
	// Each part ends in exactly one "\n", so joining with another "\n" puts exactly one blank
	// line between sections.
	return strings.TrimSuffix(strings.Join(parts, "\n"), "\n")
}

// renderText is the full additionalContext payload: the body, injection-tagged.
func renderText(r Request, items []Item) string {
	return Wrap(r.Ref.Seq, renderBody(r, items))
}

// render turns the budget pass's admitted units into Items and the payload, and reports the
// per-item accounting rows alongside them.
//
// Two things it does are load-bearing for the inherited conformance suite. Items are emitted in
// renderOrder with Rank as the 0-based position among EMITTED items, so an omitted kind consumes
// no rank (runItemOrderCase). And Result.Tokens is exactly the sum over Items (runBudgetCase) — but
// that total is the ASSEMBLED estimate, not a running sum of per-fragment prices.
//
// The distinction is the V6 §5 / inventory 1.6.18 correction. Each item's a.used is the sum of its
// units' and heading's INDEPENDENT estimates, and the wrapper overhead is three more independent
// estimates; summing them double-counts the estimator's per-fragment rounding and, worse, prices
// none of the blank-line separators renderBody inserts between sections. So those per-fragment
// numbers are kept only as non-negative WEIGHTS, and the reported total is the estimator applied to
// the complete rendered payload (wrapper and separators included). allocateAssembledTokens then
// re-charges the rows so their arithmetic sum equals that assembled total exactly, which keeps the
// sum-over-Items identity true without claiming any single row is an additive tokenization.
func render(r Request, d Deps, fills map[ItemKind]*admitted, all map[ItemKind]built, overhead core.Tokens) (Result, []ItemStat) {
	var res Result
	units := make(map[ItemKind]int, len(renderOrder))
	seen := make(map[ItemKind]int, len(renderOrder))

	for _, k := range renderOrder {
		a := fills[k]
		if a == nil || len(a.units) == 0 {
			continue
		}
		texts := make([]string, 0, len(a.units))
		for _, u := range a.units {
			texts = append(texts, u.text)
		}
		b := all[k]
		text := itemText(k, eliminationsShown(b), b.seen, texts)
		if text == "" {
			continue
		}

		// a.used + (on the first emitted item) the wrapper overhead is this row's heuristic WEIGHT:
		// the estimator's per-fragment price for what this section contributes. The assembled total
		// below re-normalizes it, so it never has to be exact — only proportional and non-negative.
		it := Item{Kind: k, Rank: len(res.Items), Tokens: a.used.tok, Text: text, Truncated: a.truncated}
		if len(res.Items) == 0 {
			it.Tokens += overhead
		}
		res.Items = append(res.Items, it)
		units[k] = len(a.units)
		seen[k] = b.seen
	}

	if len(res.Items) == 0 || onlyDropReport(res.Items) {
		// No items means no payload, not an empty tagged wrapper — and a rehydration that could
		// inject nothing must say it was degraded (§12.3, runDegradeCase). A drop report with
		// nothing beside it is the same case: item 7's floor is held so that an omission can be
		// NAMED next to whatever did fit, not so that a payload of nothing but "you lost
		// everything" is injected in place of none. Result.Dropped still carries every entry.
		return Result{Degraded: true}, nil
	}
	res.Text = renderText(r, res.Items)
	// The reported cost is the estimator's price for the COMPLETE assembled payload — wrapper and
	// inter-section separators included — never the sum of the per-fragment weights above.
	res.Tokens = estimate(d, res.Text)
	allocateAssembledTokens(res.Items, res.Tokens)
	return res, itemStats(res.Items, units, seen)
}

// onlyDropReport reports whether items is item 7 and nothing else.
func onlyDropReport(items []Item) bool {
	return len(items) == 1 && items[0].Kind == ItemDropReport
}

// allocateAssembledTokens re-charges each item's accounting row so that the arithmetic sum over
// Items equals the assembled total exactly, using the rows' current values as non-negative weights.
//
// It is a deterministic ALLOCATION, not a tokenization: the estimator prices the whole payload once,
// and this spreads that single number back across the rows the state file and SP-16 read. A positive
// difference (the assembled payload cost MORE than the fragment weights, e.g. because of unpriced
// separators) is charged to the FIRST emitted item — the one that already carries the wrapper
// overhead. A negative difference (the usual case: per-fragment rounding over-counted) is deducted
// from the tail backward, so the most important early rows keep their weight, and each deduction is
// bounded by the row's own value so no row is ever charged a negative cost. A negative target is
// clamped to zero purely so the arithmetic cannot fabricate a negative row; no supported estimator
// produces one (see assembled-budget-work.md).
func allocateAssembledTokens(items []Item, target core.Tokens) {
	if len(items) == 0 {
		return
	}
	if target < 0 {
		target = 0
	}
	var sum core.Tokens
	for i := range items {
		if items[i].Tokens < 0 {
			items[i].Tokens = 0
		}
		sum += items[i].Tokens
	}
	switch diff := target - sum; {
	case diff == 0:
		return
	case diff > 0:
		items[0].Tokens += diff
	default:
		deficit := -diff
		for i := len(items) - 1; i >= 0 && deficit > 0; i-- {
			take := items[i].Tokens
			if take > deficit {
				take = deficit
			}
			items[i].Tokens -= take
			deficit -= take
		}
	}
}

// syncStatTokens copies the re-allocated per-item costs onto their parallel accounting rows after a
// hard-cap eviction re-measures the payload. stats and items stay index-aligned throughout the
// eviction loop, so the copy keeps sum(stats.Tokens) == Result.Tokens the way itemStats did on the
// first render.
func syncStatTokens(stats []ItemStat, items []Item) {
	for i := range items {
		stats[i].Tokens = items[i].Tokens
	}
}

// ── the §8.5 injection tags ──

// Wrap wraps body in the §8.5 injection tags (00-ARCHITECTURE.md §5.14, Qompack.md §8.5, §4.6).
//
// The tags are what let a later read of the transcript strip Qompack's own injection back out
// instead of re-encoding it into the next checkpoint — the never-compress-a-compression invariant,
// enforced at the injection boundary. The version is checkpoint.SchemaVersion, not a local
// constant: InjectionOpenTag's own doc comment says "ver is SchemaVersion", and a duplicate
// literal here would drift silently past the first schema bump.
//
// The §12.1 contract probe is NOT part of this: the daemon appends it on its own line after the
// close tag, outside the injected span.
func Wrap(seq core.CheckpointSeq, body string) string {
	return fmt.Sprintf(checkpoint.InjectionOpenTag, int(seq), checkpoint.SchemaVersion) + "\n" +
		body + "\n" + checkpoint.InjectionCloseTag
}

// The three fixed pieces of checkpoint.InjectionOpenTag, derived from the constant itself rather
// than written out a second time, so Unwrap can never drift from Wrap. For
// "<!-- qompack:injected seq=%d ver=%d -->" they are "<!-- qompack:injected seq=", " ver=" and
// " -->".
var openTagPrefix, openTagInfix, openTagSuffix = splitOpenTag()

// splitOpenTag decomposes checkpoint.InjectionOpenTag around its two %d verbs.
func splitOpenTag() (prefix, infix, suffix string) {
	const verb = "%d"
	t := checkpoint.InjectionOpenTag
	i := strings.Index(t, verb)
	if i < 0 {
		return t, "", ""
	}
	rest := t[i+len(verb):]
	j := strings.Index(rest, verb)
	if j < 0 {
		return t[:i], rest, ""
	}
	return t[:i], rest[:j], rest[j+len(verb):]
}

// Unwrap is Wrap's inverse: it returns the injected body and the checkpoint sequence it came from,
// reporting ok=false when the tags are absent or malformed.
//
// Three deliberate behaviours:
//
//   - It is NON-GREEDY, matching checkpoint.StripInjections: the span ends at the FIRST close tag,
//     so two adjacent injected spans are two spans rather than one swallowing the text between.
//   - It TOLERATES trailing content after the close tag, because the daemon appends the §12.1
//     contract probe there. That content is not part of the body and is not returned.
//   - It does NOT gate on ver. A payload from a newer schema must still be recognizable as an
//     injected span, or the never-re-encode guarantee would fail exactly at a version skew. The
//     value must parse as a number; it need not equal checkpoint.SchemaVersion.
//
// A rejected payload yields ("", 0, false) — never a partial body, which a caller would strip the
// wrong span with.
func Unwrap(s string) (body string, seq core.CheckpointSeq, ok bool) {
	if !strings.HasPrefix(s, openTagPrefix) {
		return "", 0, false
	}
	rest := s[len(openTagPrefix):]

	i := strings.Index(rest, openTagInfix)
	if i < 0 {
		return "", 0, false
	}
	seqText := rest[:i]
	rest = rest[i+len(openTagInfix):]

	j := strings.Index(rest, openTagSuffix)
	if j < 0 {
		return "", 0, false
	}
	verText := rest[:j]
	rest = rest[j+len(openTagSuffix):]

	n, err := strconv.Atoi(seqText)
	if err != nil {
		return "", 0, false
	}
	if _, err := strconv.Atoi(verText); err != nil {
		return "", 0, false
	}

	if !strings.HasPrefix(rest, "\n") {
		return "", 0, false
	}
	rest = rest[1:]

	end := strings.Index(rest, checkpoint.InjectionCloseTag)
	if end < 0 {
		return "", 0, false
	}
	body = rest[:end]
	if !strings.HasSuffix(body, "\n") {
		return "", 0, false
	}
	return body[:len(body)-1], core.CheckpointSeq(n), true
}

// AffordanceNotice is §8.6 item 8: "one line telling the agent that recall, re_read and
// already_tried exist".
//
// It deliberately does NOT append StandingInstruction(). The inherited conformance case
// runStandingInstructionCase asserts that with no item 3 the payload does not contain that
// sentence at all — "the standing instruction belongs to item 3; with no item 3 it must not be
// emitted" — and item 8 is emitted on every rehydration, so ending it with the instruction would
// fail that case on every checkpoint with an empty eliminations set.
//
// One line, ~310 characters: roughly 80 tokens at the baseline estimate, an order of magnitude
// under the ~200-token lazy affordance §4.4 budgets for, and two orders under the 75K of eager
// restoration it replaces.
func AffordanceNotice() string {
	return "Qompack retrieval is available: recall(query,k) · expand(hash|tool_use_id) · " +
		"re_read(path,at) · already_tried(target,approach) · record_eliminated(target,approach,reason) · " +
		"timeline(from,to) · why(decision_id) · dropped()."
}
