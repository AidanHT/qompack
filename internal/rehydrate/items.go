package rehydrate

import (
	"context"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rules"
	"github.com/qompack/qompack/internal/skills"
)

// ── the builder seam ──

// unit is one budgetable piece of an item.
//
// Builders produce units and do NO budget arithmetic: they never call the estimator, never set
// tokens, and never reorder by cost. That separation is what makes the budget pass the single
// place where "what fits" is decided, and what lets PropBuild_MonotoneInBudget hold — the item
// set at a smaller budget is a prefix of the set at a larger one, which is only true if builder
// order is independent of the budget.
type unit struct {
	// text is the rendered line or lines, ALWAYS ending in exactly one "\n".
	text string
	// tokens is this unit's cost. Builders leave it zero; the budget pass fills it.
	tokens core.Tokens
	// drop is the DropEntry to emit if this unit does not fit the budget.
	//
	// A ZERO drop marks a unit that must never be truncated: tier-1 material (items 1, 2's
	// original-intent unit, 8) and item 3's trailing note. isFixedUnit is the predicate, and it is
	// how the budget pass tells "charge this first, whole" from "drop this and say so".
	drop checkpoint.DropEntry
}

// built is what every item builder returns.
type built struct {
	// units are the candidate units in builder order. Budget truncation takes a PREFIX of them.
	units []unit
	// seen is the total number of candidates before budgeting. It feeds item 3's "N further…"
	// note, item 3's heading, and ItemStat.UnitsSeen.
	seen int
	// drops are the DropEntries produced during CONSTRUCTION: an unavailable source, a deliberate
	// non-render, a host-truncation warning. Budget-truncation drops are NOT produced here — the
	// budget pass synthesizes those from each unadmitted unit's own drop field.
	drops []checkpoint.DropEntry
}

// isFixedUnit reports whether u must be admitted whole or not at all: it carries the zero
// DropEntry, so there is nothing to report if it is cut, because it is never cut.
func isFixedUnit(u unit) bool { return u.drop == (checkpoint.DropEntry{}) }

// eliminationsShown reports how many ELIMINATION lines b holds, excluding the trailing note unit.
// It is meaningful only for item 3's `built`; the note is the last unit and is the only one there
// carrying a zero drop.
func eliminationsShown(b built) int {
	n := len(b.units)
	if n == 0 {
		return 0
	}
	if isFixedUnit(b.units[n-1]) {
		n--
	}
	return n
}

// ── external seams this slice does not own ──

// bodyTokensFunc is skills.BodyTokens's shape: the on-disk token size of one skill's body.
//
// It is taken as a parameter rather than called directly because internal/skills does not export
// BodyTokens yet — it lands with the real Indexer, in a file this slice's sibling owns. Binding it
// here as a seam keeps buildSkillIndex compiling, testable against a fake, and a one-line wiring
// change away from the real thing. A nil bodyTokens omits the two §2.7 host-truncation warnings
// and nothing else.
type bodyTokensFunc func(root string, e skills.Entry) (core.Tokens, error)

// matchFunc is rules.Match's shape: a "**"-aware glob match of pattern against a paths.Key-form
// key. It is a seam for the same reason bodyTokensFunc is — rules.Match lands with the real
// Scanner — and re-implementing "**" semantics locally would guarantee the two drift. A nil match
// degrades a path-rule drop's detail from "matched <pointer>; did not fit…" to "did not fit…".
type matchFunc func(pattern, key string) bool

// skillBodyTokens and ruleGlobMatch are what buildAll binds the two seams above to.
//
// They stay vars rather than direct calls so a test can substitute a failing or absent
// implementation and exercise the degraded paths — the §2.7 host-truncation warnings disappearing,
// and a path-rule drop losing its "matched <pointer>; " prefix — without a build tag. Build
// remains a pure function of (Request, Deps) either way: neither seam reads state.
var (
	skillBodyTokens bodyTokensFunc = skills.BodyTokens
	ruleGlobMatch   matchFunc      = rules.Match
)

// ── bounds ──

const (
	// maxIntentBytes caps how much of the L0 verbatim prompt item 2 reads. A prompt longer than
	// this is a paste, and the tail of a paste is not the statement of intent.
	maxIntentBytes = 8192
	// maxReasonRunes bounds an elimination's reason line.
	maxReasonRunes = 240
	// maxDecisionWhatRunes and maxDecisionWhyRunes bound item 4's two prose lines.
	maxDecisionWhatRunes = 200
	maxDecisionWhyRunes  = 320
	// maxOneLineRunes bounds a pointer's `why`/`summary` and a rule's glob list: one line of
	// context, never an excerpt. The allow directive must sit on the literal's OWN line — nomagic
	// keys its exemption set by line number, so a directive in the doc comment above exempts
	// nothing.
	maxOneLineRunes = 120 //nomagic:allow a rune cap on one line of rendered prose, not a config default (§11.6)
	// maxUnitLines is the no-contents guard's line ceiling.
	maxUnitLines = 6
)

// truncMark is appended to any field the bounds cut short, and counts against that field's own
// limit — so a truncated field is never longer than the limit its spec states.
const truncMark = "…"

// symbolProxyDecay discounts a score reached through a file or symbol proxy rather than through
// the elimination's own node: the record is relevant BECAUSE something it touches is, which is
// weaker evidence than the record itself being in the slice.
const symbolProxyDecay float32 = 0.75

// The §2.7 host budgets for invoked skill bodies: "Re-injected, 5K/skill and 25K total, oldest
// dropped, truncated head-first". These describe the HOST's behaviour, not a Qompack tunable —
// nothing here changes them, item 6b only warns when they will bite.
const (
	hostSkillBodyBudgetTokens  core.Tokens = 5000
	hostSkillTotalBudgetTokens core.Tokens = 25000
)

// staleStatusTag is §8.3's stale note, rendered verbatim. A stale elimination is not deleted and
// not silently downgraded: it is shown with the reason it might no longer hold, because deciding
// that is the agent's job and hiding it re-opens G6.2.
const staleStatusTag = "stale: previously eliminated, but the evidence has changed since — re-verification may be warranted"

// activeStatusTag is the tag on a record still believed true.
const activeStatusTag = "active"

// staleResponseDrop is the eliminations.staleResponse value that EXCLUDES stale records. Any other
// value — including the Appendix C default "flag" and an unset config — includes them.
const staleResponseDrop = "drop"

// guardRejectionDetail is the exact Detail every no-contents-guard drop carries. guardTripped
// matches on it, so it is one string in one place.
const guardRejectionDetail = "rejected by the no-contents guard"

// The DropEntry kinds this slice mints. They are the keys of kindRank and the tokens item 7
// renders, so a typo would silently sort an entry to the bottom of the report.
const (
	dropKindPathRule            = "path_rule"
	dropKindNestedClaudeMD      = "nested_claude_md"
	dropKindSkill               = "skill"
	dropKindElimination         = "elimination"
	dropKindDecision            = "decision"
	dropKindPointer             = "pointer"
	dropKindUserIntentEvolution = "user_intent_evolution"
	dropKindCurrentWork         = "current_work"
	dropKindIntentMismatch      = "intent_mismatch"
	dropKindUserIntentSource    = "user_intent_source"
	dropKindEliminationSource   = "elimination_source"
	// dropKindOverflow is the NAMED, REPORTABLE overflow outcome 00-ARCHITECTURE.md §5.15 and
	// Qompack.md §8.6 require: content that could not be represented inside the declared budget at
	// all — the fixed injection wrapper itself (a zero or near-zero budget), or a single tier-1
	// critical record too large to admit whole — as distinct from the ORDINARY, expected
	// discretionary truncation every other dropKind* here names. Overflowed(drops) is the single
	// place that recognizes every shape this can currently take; a caller who wants to know
	// "did this rehydration lose something it could not even name a partial version of" checks
	// that, never a raw Kind or ID comparison of its own.
	dropKindOverflow = "overflow"
)

// kindRank orders item 7. Overflow sorts FIRST — an essential record this budget could not
// represent at all outranks even the operating rules the agent no longer has. Path rules and
// nested CLAUDE.md files sort next because they are precisely the thing G4.5 says nothing
// surfaces today. The two "open_question" and "narrative" kinds are SP-10's, minted by the
// checkpointer and carried in Checkpoint.Dropped; they are ranked here so a checkpoint-time drop
// interleaves correctly with a rehydration-time one.
var kindRank = map[string]int{
	dropKindOverflow:       -1,
	dropKindPathRule:       0,
	dropKindNestedClaudeMD: 1,
	dropKindSkill:          2,
	dropKindElimination:    3,
	dropKindDecision:       4,
	dropKindPointer:        5,
	"open_question":        6,
	"narrative":            7,

	dropKindUserIntentEvolution: 8,
	dropKindCurrentWork:         9,
	dropKindIntentMismatch:      10,
	dropKindUserIntentSource:    11,
	dropKindEliminationSource:   12,
}

// unknownKindRank sorts a kind no version of this package minted after every known one, then by
// Kind ascending, so an entry from a future checkpoint schema is still deterministically ordered.
const unknownKindRank = 99

// ── the ten builders ──

// buildAll runs every item builder except item 7, which Build assembles last because it reports on
// all of the others.
func buildAll(ctx context.Context, r Request, d Deps, sc map[dag.NodeID]float32) map[ItemKind]built {
	out := make(map[ItemKind]built, len(renderOrder))
	out[ItemInvariants] = buildInvariants(ctx, r, d)
	out[ItemUserIntent] = buildUserIntent(ctx, r, d)
	out[ItemEliminations] = buildEliminations(ctx, r, d, sc)
	out[ItemDecisions] = buildDecisions(ctx, r, d, sc)
	out[ItemCurrentWork] = buildCurrentWork(ctx, r, d)
	out[ItemPointers] = buildPointers(ctx, r, d, sc)
	out[ItemRestoredInstructions] = buildRestoredInstructions(ctx, r, d, ruleGlobMatch)
	out[ItemSkillIndex] = buildSkillIndex(ctx, r, d, skillBodyTokens)
	out[ItemAffordance] = buildAffordance(ctx, r, d)
	return out
}

// buildInvariants is item 1: pinned facts, verbatim, always.
//
// Order is as stored — §7.4 makes pins append-only, so the checkpoint's order is stable. Every
// unit carries the zero DropEntry: tier 1 is never truncated, so there is nothing to report.
// The no-contents guard deliberately never runs here; a pinned invariant is verbatim human text.
func buildInvariants(_ context.Context, r Request, d Deps) built { //nolint:unparam // the nine item builders share one signature so buildAll can call them uniformly
	var b built
	for _, inv := range r.Checkpoint.Invariants {
		b.seen++
		line := "- [" + inv.ID + "] " + inv.Text
		if inv.Source != "" {
			line += " (source: " + inv.Source + ")"
		}
		b.units = append(b.units, unit{text: line + "\n"})
	}
	return b
}

// firstPromptID is the tool_use id SP-08 records this session's FIRST UserPromptSubmit capture
// under. observer.VerbatimPromptID(session, turn) == "prompt_<session>_<turn>", so turn 0 is
// derivable without a search and without an import edge: rehydrate may not import observer
// (00-ARCHITECTURE.md §3.2).
func firstPromptID(s core.SessionID) core.ToolUseID {
	return core.ToolUseID("prompt_" + string(s) + "_0")
}

// buildUserIntent is item 2: the verbatim original intent, read from L0 — the G7.3/G2.3 closure.
//
// units[0] is the original-intent unit and carries the zero DropEntry: it is tier 1 and is never
// truncated. units[1:] are the evolution deltas, each carrying a user_intent_evolution drop, and
// the first of them also carries the "Evolution:" header line so that dropping them all removes
// the header too. When the original is empty there is no units[0] and the deltas start at index
// 0 — which is why the discriminator is isFixedUnit, not the position.
//
// It does NOT use Store.Search. Search clamps K to 100 and ranks newest-first, so the earliest
// prompt of a long session is the first thing the window excludes — and the failure is not
// benign: the candidate set is not empty, it holds this session's LATER prompts, so a relevance
// search would inject a mid-session prompt as "the verbatim original user intent" and then
// override the checkpoint's genuine original with it. That inverts the exact gap this item
// closes. Store.ToolUse on the derived id is exact and O(1). The transcript is not read either:
// it is the summary-contaminated surface §8.5's regeneration rule forbids as a source.
func buildUserIntent(ctx context.Context, r Request, d Deps) built {
	var b built
	log := loggerOf(d)
	fallback := strings.TrimSpace(r.Checkpoint.UserIntent.Original)

	text, ok := readL0Intent(ctx, r, d)
	switch {
	case !ok:
		// Degraded but correct: §8.5 guarantees the checkpoint's copy is itself verbatim-from-L0
		// and never regenerated, so this is a weaker provenance chain, not a wrong one. Info, not
		// Loud — it is the ordinary case for a session whose first prompt predates the store.
		b.drops = append(b.drops, checkpoint.DropEntry{
			Kind: dropKindUserIntentSource, ID: "l0",
			Detail: "L0 verbatim capture unavailable; using the checkpoint copy, which §8.5 " +
				"guarantees is itself verbatim-from-L0 and never regenerated",
		})
		log.Info("rehydrate: L0 verbatim prompt unavailable; using the checkpoint copy",
			"session", string(r.Session), "id", string(firstPromptID(r.Session)))
		text = fallback
	case fallback != "" && fallback != text:
		// L0 WINS. This is the mechanical guarantee that no summary-derived intent reaches the
		// context window, and it is loud because a divergence here means something regenerated
		// what §8.5 says is never regenerated.
		b.drops = append(b.drops, checkpoint.DropEntry{
			Kind: dropKindIntentMismatch, ID: string(r.Session),
			Detail: "checkpoint user_intent.original differs from the L0 capture; injecting the L0 text",
		})
		log.Loud("rehydrate: checkpoint user_intent.original differs from the L0 capture",
			"session", string(r.Session))
	}

	if text != "" {
		b.seen++
		b.units = append(b.units, unit{text: quoteLines(text)})
	}
	// Evolution is append-only and oldest-first (checkpoint/writer.go's appendEvolutionLocked), but
	// units are built NEWEST-FIRST here: fillPrefix admits a PREFIX of whatever order it is given
	// and stops at the first unit that does not fit (Qompack.md's current-authority rule; see
	// TestBuild_LatestEvolutionSurvivesTruncation). Building in stored (oldest-first) order would
	// make a tight budget keep the OLDEST restatements and drop the newest — resurrecting exactly
	// the intent a later authorized correction superseded. Reversing the BUILD order, rather than
	// special-casing which unit fillPrefix admits, is what keeps this a plain prefix cut: the
	// admitted set at any budget is still deterministic from budget alone and
	// PropBuild_MonotoneInBudget still holds, because a larger budget's prefix of this same
	// newest-first sequence always extends the smaller budget's prefix with OLDER material, never
	// reorders it.
	//
	// Each unit's drop ID is still the delta's TRUE index into Checkpoint.UserIntent.Evolution —
	// not its position in this reversed build order — so provenance (T11-AUTH-01) and any
	// consumer correlating by that index are unaffected by the display/truncation order.
	for i := len(r.Checkpoint.UserIntent.Evolution) - 1; i >= 0; i-- {
		ev := strings.TrimSpace(r.Checkpoint.UserIntent.Evolution[i])
		if ev == "" {
			continue
		}
		b.seen++
		body := quoteLines(ev)
		if len(b.units) == 0 || isFixedUnit(b.units[len(b.units)-1]) {
			// The header belongs to the first (most recent) evolution unit, so it disappears with
			// it if that one unit alone is dropped.
			body = "Evolution (most recent first):\n" + body
		}
		b.units = append(b.units, unit{
			text: body,
			drop: checkpoint.DropEntry{Kind: dropKindUserIntentEvolution, ID: itoa(i)},
		})
	}
	return b
}

// readL0Intent resolves this session's verbatim first prompt from L0, reporting ok=false for every
// reason the caller must fall back to the checkpoint's own copy.
//
// The record is sanity-checked BEFORE it is trusted: a record whose session or turn disagrees with
// the derived id means SP-08's id scheme drifted, and injecting a record this slice cannot vouch
// for is worse than injecting the checkpoint copy. That case is a Warn, because it is a contract
// drift rather than an ordinary absence.
func readL0Intent(ctx context.Context, r Request, d Deps) (string, bool) {
	if d.Store == nil {
		return "", false
	}
	log := loggerOf(d)

	rec, err := d.Store.ToolUse(ctx, firstPromptID(r.Session))
	if err != nil || rec.Root == (core.Hash{}) {
		return "", false
	}
	if rec.Session != r.Session || rec.Turn != 0 {
		log.Warn("rehydrate: L0 prompt record does not match its derived id; using the checkpoint copy",
			"session", string(r.Session), "got_session", string(rec.Session), "turn", int(rec.Turn))
		return "", false
	}

	rc, err := d.Store.Open(ctx, rec.Root)
	if err != nil || rc == nil {
		return "", false
	}
	raw, readErr := io.ReadAll(io.LimitReader(rc, maxIntentBytes))
	closeErr := rc.Close()
	if readErr != nil {
		log.Debug("rehydrate: L0 prompt read failed", "err", readErr.Error())
		return "", false
	}
	if closeErr != nil {
		log.Debug("rehydrate: L0 prompt close failed", "err", closeErr.Error())
	}

	// A prior rehydration's payload could, in a pathological transcript, have been captured as a
	// prompt; §8.5's tagging exists precisely so that material is identifiable and ignorable.
	text := strings.TrimSpace(checkpoint.StripInjections(string(trimToRuneBoundary(raw))))
	if text == "" {
		return "", false
	}
	return text, true
}

// buildEliminations is item 3: the top-N eliminations by slice relevance, plus the standing
// instruction that makes them actionable.
//
// The candidate set unions BOTH scopes deliberately. eliminations.defaultScope governs the scope
// NEW records are written with; it is not a read filter. §8.3 says project-scoped eliminations
// "persist and warm-start future sessions", so reading only the default scope would drop exactly
// the longest-lived negative knowledge in the store — the inverse of G6.2.
//
// When seen == 0 the whole item is omitted AND the standing instruction appears nowhere. That is
// the inherited contract, not an oversight: runStandingInstructionCase asserts the sentence is
// absent from a payload with no item 3. It is item 3's companion, not a free-floating banner.
func buildEliminations(ctx context.Context, r Request, d Deps, sc map[dag.NodeID]float32) built {
	var b built

	cands, srcDrops := eliminationCandidates(ctx, r, d)
	b.drops = append(b.drops, srcDrops...)

	dropStale := r.Cfg.Eliminations.StaleResponse == staleResponseDrop
	kept := cands[:0]
	for _, rec := range cands {
		if dropStale && rec.Status == negknow.StatusStale {
			continue
		}
		kept = append(kept, rec)
	}
	b.seen = len(kept)
	if b.seen == 0 {
		return built{drops: b.drops}
	}

	scores := make(map[string]float32, len(kept))
	for _, rec := range kept {
		scores[rec.ID] = recordScore(sc, rec)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		si, sj := scores[kept[i].ID], scores[kept[j].ID]
		if si != sj {
			return si > sj
		}
		if kept[i].TS != kept[j].TS {
			return kept[i].TS > kept[j].TS
		}
		return kept[i].ID < kept[j].ID
	})

	topN := r.Cfg.Runtime.Rehydrate.EliminationsTopN
	if topN < 0 {
		topN = 0
	}
	if topN > len(kept) {
		topN = len(kept)
	}
	for _, rec := range kept[:topN] {
		b.units = append(b.units, unit{
			text: eliminationLine(rec),
			drop: checkpoint.DropEntry{
				Kind: dropKindElimination, ID: rec.ID,
				Detail: "did not fit the rehydration budget; call already_tried(target, approach) or dropped()",
			},
		})
	}

	// The trailing note, always, and never truncated: it is what makes the digest honest about
	// what it omitted, and the standing instruction is what makes the whole item actionable.
	rest := b.seen - topN
	note := StandingInstruction() + "\n"
	if rest > 0 {
		note = itoa(rest) + " further eliminations are recorded and not shown. " + note
		// The empty ID is what makes item 7's collapse rule render this as one counted line.
		b.drops = append(b.drops, checkpoint.DropEntry{
			Kind: dropKindElimination, ID: "",
			Detail: itoa(rest) + " of " + itoa(b.seen) +
				" not shown; call already_tried(target, approach) or dropped()",
		})
	}
	b.units = append(b.units, unit{text: note})
	return b
}

// eliminationCandidates unions both ledger scopes with the checkpoint's own copy, deduplicated by
// Record.ID with the LEDGER copy winning — it carries the current Status, where the checkpoint's
// copy is frozen at write time.
//
// If one Active call errors the other is still used; if both error the checkpoint copy stands
// alone. A ledger error is reported, never fatal: item 3 degrading to the checkpoint's frozen set
// is strictly better than a session that will not start.
func eliminationCandidates(ctx context.Context, r Request, d Deps) ([]negknow.Record, []checkpoint.DropEntry) {
	var drops []checkpoint.DropEntry
	var fromLedger []negknow.Record

	if d.Ledger == nil {
		drops = append(drops, checkpoint.DropEntry{
			Kind: dropKindEliminationSource, ID: "ledger",
			Detail: "no elimination ledger is wired; using the checkpoint's frozen copy",
		})
	} else {
		for _, scope := range []negknow.Scope{negknow.ScopeSession, negknow.ScopeProject} {
			recs, err := d.Ledger.Active(ctx, scope)
			if err != nil {
				drops = append(drops, checkpoint.DropEntry{
					Kind: dropKindEliminationSource, ID: "ledger", Detail: err.Error(),
				})
				loggerOf(d).Warn("rehydrate: elimination ledger unavailable",
					"scope", string(scope), "err", err.Error())
				continue
			}
			fromLedger = append(fromLedger, recs...)
		}
	}

	out := make([]negknow.Record, 0, len(fromLedger)+len(r.Checkpoint.Eliminated))
	seen := make(map[string]struct{}, cap(out))
	for _, rec := range fromLedger {
		if _, dup := seen[rec.ID]; dup {
			continue
		}
		seen[rec.ID] = struct{}{}
		out = append(out, rec)
	}
	for _, rec := range r.Checkpoint.Eliminated {
		if _, dup := seen[rec.ID]; dup {
			continue // the ledger's Active() copy wins: it carries the current Status
		}
		// Active() answers "every StatusActive record" (negknow.Ledger.Active's own contract), so
		// a record that went stale — or was otherwise superseded — since this checkpoint was
		// written is invisible to the union above: it is no longer StatusActive, so Active() does
		// not return it, and the dedup loop above never marks it seen. Left uncorrected, the
		// checkpoint's frozen copy — still carrying whatever Status it had at write time — would
		// be admitted here and resurrect a record a later authorized re-verification overturned.
		// Get(id) is the one call that can still see a status change Active() no longer surfaces,
		// so it is consulted before the frozen copy is trusted. A ledger that has never heard of
		// the id (core.ErrNotFound, core.ErrNotImplemented, or any other error — this call must
		// degrade exactly like every other optional ledger read, never block on it) leaves the
		// checkpoint's own copy standing, unchanged from today's behaviour.
		if d.Ledger != nil {
			if cur, err := d.Ledger.Get(ctx, rec.ID); err == nil {
				rec = cur
			}
		}
		seen[rec.ID] = struct{}{}
		out = append(out, rec)
	}
	return out, drops
}

// eliminationLine renders one elimination.
func eliminationLine(rec negknow.Record) string {
	tag := activeStatusTag
	if rec.Status == negknow.StatusStale {
		tag = staleStatusTag
	}
	evidence := ""
	if rec.Evidence != (core.Hash{}) {
		evidence = " [evidence " + rec.Evidence.String() + "]"
	}
	return "- " + oneLine(rec.Target) +
		" — \"" + oneLine(rec.Approach) + "\" — " +
		truncRunes(oneLine(rec.Reason), maxReasonRunes, truncMark) +
		" [" + tag + "]" + evidence + "\n"
}

// sliceCriteria is the criterion node set every relevance ranking in this slice is computed
// against: what the checkpoint points AT is what the session is about.
//
// It uses dag's exported constructors, never a hand-built dag.NodeID literal. That is not style:
// a mis-spelled prefix does not fail, it silently returns a zero score for every record, and item
// 3's ordering degrades to TS/ID with no signal and no error. The constructors also encode key
// shapes a literal cannot guess — SymbolNode keys on pathKey + "#" + name.
func sliceCriteria(cp checkpoint.Checkpoint) []dag.NodeID {
	n := len(cp.Pointers.Files) + len(cp.Pointers.Tools) + len(cp.Decisions)
	out := make([]dag.NodeID, 0, n)
	seen := make(map[dag.NodeID]struct{}, n)
	add := func(id dag.NodeID) {
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, f := range cp.Pointers.Files {
		add(dag.FileNode(paths.Key(f.Path)))
	}
	for _, t := range cp.Pointers.Tools {
		add(dag.ToolUseNode(t.ToolUseID))
	}
	for _, dec := range cp.Decisions {
		add(dag.DecisionNode(dec.ID))
	}
	return out
}

// recordScore is one elimination's relevance: its own node's score, or the best discounted score
// reachable through the file or symbol it is about.
//
// A record with no path yields SymbolNode("", sym) == "symbol:#sym", which is the exact spelling
// dag mints for a symbol whose defining file is not known yet — so the pathless case is a real
// lookup, not a miss.
func recordScore(sc map[dag.NodeID]float32, rec negknow.Record) float32 {
	s := sc[dag.EliminationNode(rec.ID)]
	if rec.Desc.NormalizedPath != "" {
		if v := sc[dag.FileNode(paths.Key(rec.Desc.NormalizedPath))]; v*symbolProxyDecay > s {
			s = v * symbolProxyDecay
		}
	}
	if rec.Desc.Symbol != "" {
		if v := sc[dag.SymbolNode(paths.Key(rec.Desc.NormalizedPath), rec.Desc.Symbol)]; v*symbolProxyDecay > s {
			s = v * symbolProxyDecay
		}
	}
	return s
}

// buildDecisions is item 4: what was decided and why, ranked by slice relevance.
func buildDecisions(_ context.Context, r Request, d Deps, sc map[dag.NodeID]float32) built { //nolint:unparam // the nine item builders share one signature so buildAll can call them uniformly
	var b built

	decs := make([]checkpoint.Decision, len(r.Checkpoint.Decisions))
	copy(decs, r.Checkpoint.Decisions)
	sort.SliceStable(decs, func(i, j int) bool {
		si, sj := sc[dag.DecisionNode(decs[i].ID)], sc[dag.DecisionNode(decs[j].ID)]
		if si != sj {
			return si > sj
		}
		if decs[i].Turn != decs[j].Turn {
			return decs[i].Turn > decs[j].Turn
		}
		return decs[i].ID < decs[j].ID
	})

	for _, dec := range decs {
		b.seen++
		u := unit{
			text: decisionLines(dec),
			drop: checkpoint.DropEntry{
				Kind: dropKindDecision, ID: string(dec.ID),
				Detail: "did not fit the rehydration budget; call why(" + string(dec.ID) + ")",
			},
		}
		if guardNoContents(u) {
			b.drops = append(b.drops, checkpoint.DropEntry{
				Kind: dropKindDecision, ID: string(dec.ID), Detail: guardRejectionDetail,
			})
			continue
		}
		b.units = append(b.units, u)
	}
	return b
}

// decisionLines renders one decision: what, why, what was turned down, and what backs it.
func decisionLines(dec checkpoint.Decision) string {
	var b strings.Builder
	b.WriteString("- [")
	b.WriteString(string(dec.ID))
	b.WriteString("] (turn ")
	b.WriteString(itoa(int(dec.Turn)))
	b.WriteString(") ")
	b.WriteString(truncRunes(oneLine(dec.What), maxDecisionWhatRunes, truncMark))
	b.WriteByte('\n')

	if why := truncRunes(oneLine(dec.Why), maxDecisionWhyRunes, truncMark); why != "" {
		b.WriteString("  why: ")
		b.WriteString(why)
		b.WriteByte('\n')
	}
	if len(dec.AlternativesRejected) > 0 {
		alts := make([]string, 0, len(dec.AlternativesRejected))
		for _, a := range dec.AlternativesRejected {
			if a = oneLine(a); a != "" {
				alts = append(alts, a)
			}
		}
		if len(alts) > 0 {
			b.WriteString("  rejected: ")
			b.WriteString(strings.Join(alts, "; "))
			b.WriteByte('\n')
		}
	}
	if dec.Evidence != (core.Hash{}) {
		b.WriteString("  evidence: ")
		b.WriteString(dec.Evidence.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// buildCurrentWork is item 5: the goal, the next step and whatever blocks them, as a single unit.
// Lines with empty values are omitted; all three empty omits the item entirely.
func buildCurrentWork(_ context.Context, r Request, d Deps) built { //nolint:unparam // the nine item builders share one signature so buildAll can call them uniformly
	cw := r.Checkpoint.CurrentWork
	goal := oneLine(cw.Goal)
	next := oneLine(cw.NextStep)
	blocked := ""
	if cw.BlockedOn != nil {
		blocked = oneLine(*cw.BlockedOn)
	}
	if goal == "" && next == "" && blocked == "" {
		return built{}
	}

	var sb strings.Builder
	if goal != "" {
		sb.WriteString("goal: ")
		sb.WriteString(goal)
		sb.WriteByte('\n')
	}
	if next != "" {
		sb.WriteString("next step: ")
		sb.WriteString(next)
		sb.WriteByte('\n')
	}
	if blocked == "" {
		blocked = "none"
	}
	sb.WriteString("blocked on: ")
	sb.WriteString(blocked)
	sb.WriteByte('\n')

	b := built{seen: 1}
	u := unit{
		text: sb.String(),
		drop: checkpoint.DropEntry{Kind: dropKindCurrentWork, ID: dropKindCurrentWork},
	}
	if guardNoContents(u) {
		b.drops = append(b.drops, checkpoint.DropEntry{
			Kind: dropKindCurrentWork, ID: dropKindCurrentWork, Detail: guardRejectionDetail,
		})
		return b
	}
	b.units = append(b.units, u)
	return b
}

// buildPointers is item 6: paths and hashes, never contents (§4.4, §13 invariant 5). File
// pointers come first, then tool pointers, each ranked by slice relevance.
func buildPointers(_ context.Context, r Request, d Deps, sc map[dag.NodeID]float32) built { //nolint:unparam // the nine item builders share one signature so buildAll can call them uniformly
	var b built

	files := make([]checkpoint.FilePointer, len(r.Checkpoint.Pointers.Files))
	copy(files, r.Checkpoint.Pointers.Files)
	sort.SliceStable(files, func(i, j int) bool {
		si := sc[dag.FileNode(paths.Key(files[i].Path))]
		sj := sc[dag.FileNode(paths.Key(files[j].Path))]
		if si != sj {
			return si > sj
		}
		return files[i].Path < files[j].Path
	})
	for _, f := range files {
		b.seen++
		b.addGuarded(unit{
			text: pointerLine(f.Path, f.Hash, f.Why),
			drop: checkpoint.DropEntry{
				Kind: dropKindPointer, ID: f.Path,
				Detail: "did not fit the rehydration budget; call recall or re_read",
			},
		}, dropKindPointer, f.Path)
	}

	tools := make([]checkpoint.ToolPointer, len(r.Checkpoint.Pointers.Tools))
	copy(tools, r.Checkpoint.Pointers.Tools)
	sort.SliceStable(tools, func(i, j int) bool {
		si := sc[dag.ToolUseNode(tools[i].ToolUseID)]
		sj := sc[dag.ToolUseNode(tools[j].ToolUseID)]
		if si != sj {
			return si > sj
		}
		return tools[i].ToolUseID < tools[j].ToolUseID
	})
	for _, t := range tools {
		b.seen++
		b.addGuarded(unit{
			text: pointerLine("tool_use "+string(t.ToolUseID), t.Hash, t.Summary),
			drop: checkpoint.DropEntry{
				Kind: dropKindPointer, ID: string(t.ToolUseID),
				Detail: "did not fit the rehydration budget; call recall or re_read",
			},
		}, dropKindPointer, string(t.ToolUseID))
	}
	return b
}

// pointerLine renders one pointer: its label, its content hash, and a one-line reason. The hash is
// omitted when zero rather than rendered as sha256:0000…, which would look like a real address.
func pointerLine(label string, h core.Hash, why string) string {
	line := "- " + label
	if h != (core.Hash{}) {
		line += " " + h.String()
	}
	if why = truncRunes(oneLine(why), maxOneLineRunes, truncMark); why != "" {
		line += " — " + why
	}
	return line + "\n"
}

// addGuarded appends u unless the no-contents guard rejects it, in which case it records the
// rejection as a construction drop instead.
func (b *built) addGuarded(u unit, kind, id string) {
	if guardNoContents(u) {
		b.drops = append(b.drops, checkpoint.DropEntry{Kind: kind, ID: id, Detail: guardRejectionDetail})
		return
	}
	b.units = append(b.units, u)
}

// codeFence is the marker a rejected unit carries: the one content shape checkpoint-derived prose
// is forbidden to hold outright.
const codeFence = "```"

// guardNoContents reports whether u must be rejected: it holds a fenced code block, or runs to
// more than maxUnitLines lines.
//
// §13 invariant 5 forbids code snippets in a checkpoint; this is the same rule applied at the
// injection boundary, at build time. It runs over items 4, 5 and 6 ONLY.
//
// The fence test is CONTAINS, not "a line whose trimmed form begins with a fence", and that is a
// deliberate strengthening rather than a looser reading. Items 4, 5 and 6 collapse their free-text
// fields to a single line before rendering — a pointer's `why` must not be able to break the list —
// so by the time the guard sees the unit, a pasted fence has already been flattened onto one line
// and no longer BEGINS any line. A line-prefix test therefore never fires on exactly the input the
// guard exists to reject; TDD surfaced that here, with both the fenced-pointer and the
// fenced-decision fixtures passing the prefix test and reaching the payload verbatim.
//
// The exclusion of items 1, 2 and 3 is load-bearing. Items 1 and 2 are verbatim tier-1 material —
// a real user prompt routinely runs twenty lines and routinely contains a fenced stack trace or a
// pasted diff, so running the guard over item 2 would drop the original task statement for
// containing a code fence, silently re-opening the exact gap this slice exists to close. Item 3's
// units are already bounded by the 240-rune reason truncation and a fixed stale note.
func guardNoContents(u unit) bool {
	if strings.Contains(u.text, codeFence) {
		return true
	}
	return len(strings.Split(strings.TrimSuffix(u.text, "\n"), "\n")) > maxUnitLines
}

// sourceUnavailable reports whether any drop entry says a source the payload has NO SUBSTITUTE for
// was missing or failed — a nil scanner or indexer, or a scan that errored.
//
// It is what makes Result.Degraded true in that case, and the distinction it draws is deliberate.
// A nil Store or a nil Ledger costs provenance, not content: item 2 falls back to the checkpoint's
// own copy, which §8.5 guarantees is itself verbatim-from-L0, and item 3 falls back to
// Checkpoint.Eliminated. Nothing supplies items 6a and 6b but the scanner and the indexer, so
// their absence is material the session does not get back — exactly the "could not do its full
// job" §12.3 asks callers to be told about. Reporting it only as a drop entry left /qompack:status
// and the replay harness counting such a build as a full rehydration.
func sourceUnavailable(drops []checkpoint.DropEntry) bool {
	for _, e := range drops {
		if e.ID != "unavailable" && e.ID != "scan" {
			continue
		}
		switch e.Kind {
		case dropKindPathRule, dropKindNestedClaudeMD, dropKindSkill:
			return true
		}
	}
	return false
}

// guardTripped reports whether any entry in drops came from the no-contents guard. Build calls it
// once per build to decide whether a single Loud is warranted: the builders record the fact, they
// do not log it, so that N rejected units cost one log line rather than N.
func guardTripped(drops []checkpoint.DropEntry) bool {
	for _, e := range drops {
		if e.Detail == guardRejectionDetail {
			return true
		}
	}
	return false
}

// buildRestoredInstructions is item 6a: the `paths:`-scoped rules and nested CLAUDE.md files
// Qompack re-reads from disk because the host does not restore them (G4.1, G4.2).
//
// Path rules go first because they carry explicit `paths:` intent, where a nested CLAUDE.md is
// proximity-inferred. Bodies are included WHOLE: a rule either fits entirely or is dropped
// entirely and reported. This is the one place progressive truncation within a unit is
// deliberately not applied — a partial instruction set is worse than an absent one (G4.3).
//
// guardNoContents is NOT applied here: rule bodies are instructions, not reconstructible file
// contents, and may legitimately contain fenced examples.
func buildRestoredInstructions(ctx context.Context, r Request, d Deps, match matchFunc) built {
	var b built

	pointers := make([]string, 0, len(r.Checkpoint.Pointers.Files))
	for _, f := range r.Checkpoint.Pointers.Files {
		pointers = append(pointers, f.Path)
	}

	if d.Rules == nil {
		b.drops = append(b.drops,
			checkpoint.DropEntry{
				Kind: dropKindPathRule, ID: "unavailable",
				Detail: "no rule scanner is wired; path-scoped rules were not restored",
			},
			checkpoint.DropEntry{
				Kind: dropKindNestedClaudeMD, ID: "unavailable",
				Detail: "no rule scanner is wired; nested CLAUDE.md files were not restored",
			})
		return b
	}

	log := loggerOf(d)
	pathRules, err := d.Rules.PathScoped(ctx, r.ProjectRoot, pointers)
	if err != nil {
		pathRules = nil
		b.drops = append(b.drops, checkpoint.DropEntry{
			Kind: dropKindPathRule, ID: "scan", Detail: err.Error(),
		})
		log.Warn("rehydrate: path-scoped rule scan failed", "root", r.ProjectRoot, "err", err.Error())
	}
	nested, err := d.Rules.NestedClaudeMD(ctx, r.ProjectRoot, pointers)
	if err != nil {
		nested = nil
		b.drops = append(b.drops, checkpoint.DropEntry{
			Kind: dropKindNestedClaudeMD, ID: "scan", Detail: err.Error(),
		})
		log.Warn("rehydrate: nested CLAUDE.md scan failed", "root", r.ProjectRoot, "err", err.Error())
	}

	sortRulesByPath(pathRules)
	sortRulesByPath(nested)

	for _, rule := range pathRules {
		b.seen++
		detail := "did not fit the rehydration budget"
		if p := firstMatchingPointer(rule, pointers, match); p != "" {
			detail = "matched " + p + "; " + detail
		}
		b.units = append(b.units, unit{
			text: ruleUnitText(rule, ruleScopeLabel(rule)),
			drop: checkpoint.DropEntry{Kind: dropKindPathRule, ID: rule.Path, Detail: detail},
		})
	}
	for _, rule := range nested {
		b.seen++
		b.units = append(b.units, unit{
			text: ruleUnitText(rule, "nested"),
			drop: checkpoint.DropEntry{
				Kind: dropKindNestedClaudeMD, ID: rule.Path,
				Detail: "did not fit the rehydration budget",
			},
		})
	}
	return b
}

// sortRulesByPath orders rules ascending by Path, which is the only stable key a Rule carries.
func sortRulesByPath(rs []rules.Rule) {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Path < rs[j].Path })
}

// ruleScopeLabel is a path rule's scope clause: "paths: <globs>", or "" when it declares none, in
// which case the heading carries the path alone rather than a dangling "— paths:".
func ruleScopeLabel(rule rules.Rule) string {
	if len(rule.Globs) == 0 {
		return ""
	}
	return "paths: " + truncRunes(strings.Join(rule.Globs, ", "), maxOneLineRunes, truncMark)
}

// ruleUnitText renders one restored rule: a heading naming the file and its scope, then the body
// verbatim and whole.
func ruleUnitText(rule rules.Rule, scope string) string {
	head := "### " + rule.Path
	if scope != "" {
		head += " — " + scope
	}
	body := strings.TrimRight(rule.Body, "\n")
	if body == "" {
		return head + "\n"
	}
	return head + "\n" + body + "\n"
}

// firstMatchingPointer names the pointer path that pulled rule into the payload, so the drop
// report can say WHY a rule the agent no longer has was relevant. A nil match — the seam is
// unwired — yields "", and the drop detail degrades to the budget clause alone.
func firstMatchingPointer(rule rules.Rule, pointers []string, match matchFunc) string {
	if match == nil {
		return ""
	}
	for _, p := range pointers {
		key := paths.Key(p)
		for _, g := range rule.Globs {
			if match(paths.Key(g), key) {
				return p
			}
		}
	}
	return ""
}

// buildSkillIndex is item 6b: names and one-line descriptions only (G4.4). The host re-injects
// invoked skill BODIES but never the index, so the model loses awareness of what it could invoke
// at all; this restores the awareness without paying for the bodies.
func buildSkillIndex(ctx context.Context, r Request, d Deps, bodyTokens bodyTokensFunc) built {
	var b built
	if d.Skills == nil {
		b.drops = append(b.drops, checkpoint.DropEntry{
			Kind: dropKindSkill, ID: "unavailable",
			Detail: "no skill indexer is wired; the compact skill index was not restored",
		})
		return b
	}
	log := loggerOf(d)

	// Budget 0 is the documented "give me everything" call; the second call is the compact index
	// the payload actually carries. The literal 450 must appear nowhere: the budget is
	// runtime.rehydrate.skillIndexTokens, and the config field is a plain int, so the conversion
	// is explicit.
	all, _, allErr := d.Skills.Index(ctx, r.ProjectRoot, 0)
	if allErr != nil {
		all = nil
		log.Warn("rehydrate: full skill index unavailable", "root", r.ProjectRoot, "err", allErr.Error())
	}
	skillBudget := core.Tokens(r.Cfg.Runtime.Rehydrate.SkillIndexTokens)
	kept, _, keptErr := d.Skills.Index(ctx, r.ProjectRoot, skillBudget)
	if keptErr != nil {
		b.drops = append(b.drops, checkpoint.DropEntry{
			Kind: dropKindSkill, ID: "scan", Detail: keptErr.Error(),
		})
		log.Warn("rehydrate: compact skill index unavailable", "root", r.ProjectRoot, "err", keptErr.Error())
		return b
	}

	inIndex := make(map[string]struct{}, len(kept))
	for _, e := range kept {
		inIndex[e.Name] = struct{}{}
		b.seen++
		b.units = append(b.units, unit{
			text: "- " + oneLine(e.Name) + ": " + oneLine(e.Description) + "\n",
			drop: checkpoint.DropEntry{
				Kind: dropKindSkill, ID: e.Name,
				Detail: "did not fit the rehydration budget",
			},
		})
	}

	for _, e := range all {
		if _, ok := inIndex[e.Name]; !ok {
			b.drops = append(b.drops, checkpoint.DropEntry{
				Kind: dropKindSkill, ID: e.Name,
				Detail: "not in the compact skill index (budget " + itoa(int(skillBudget)) + " tokens)",
			})
		}
	}

	// §2.7's host budgets. These warn the model that a skill body it DOES get back is partial —
	// something no amount of Qompack budgeting can change, and something nothing else surfaces.
	if bodyTokens == nil {
		return b
	}
	var total core.Tokens
	for _, e := range all {
		bt, err := bodyTokens(r.ProjectRoot, e)
		if err != nil {
			log.Debug("rehydrate: skill body size unavailable", "skill", e.Name, "err", err.Error())
			continue
		}
		total += bt
		if bt > hostSkillBodyBudgetTokens {
			b.drops = append(b.drops, checkpoint.DropEntry{
				Kind: dropKindSkill, ID: e.Name,
				Detail: "body ~" + itoa(int(bt)) + " tokens; the host re-injects at most " +
					itoa(int(hostSkillBodyBudgetTokens)) +
					" per skill, head-first, so treat it as partial (§2.7)",
			})
		}
	}
	if total > hostSkillTotalBudgetTokens {
		b.drops = append(b.drops, checkpoint.DropEntry{
			Kind: dropKindSkill, ID: "*",
			Detail: "total skill bodies ~" + itoa(int(total)) + " tokens exceed the host's " +
				itoa(int(hostSkillTotalBudgetTokens)) +
				"-token cap; the oldest are dropped entirely (§2.7)",
		})
	}
	return b
}

// buildDropReport is item 7: what is no longer in context, named well enough to be asked for again
// (G4.5). It takes the assembled drop set — the checkpoint's own drops, plus every construction
// drop, plus every budget-truncation drop — because it reports on all of them.
//
// Consecutive entries sharing a Kind and carrying no ID collapse into one line. Those entries are
// already counted summaries ("15 of 23 not shown"), so repeating them would spend budget saying
// the same thing twice.
func buildDropReport(entries []checkpoint.DropEntry) built {
	if len(entries) == 0 {
		return built{}
	}
	sorted := make([]checkpoint.DropEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := rankOfKind(sorted[i].Kind), rankOfKind(sorted[j].Kind)
		if ri != rj {
			return ri < rj
		}
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		return sorted[i].ID < sorted[j].ID
	})

	var b built
	for i, e := range sorted {
		if e.ID == "" && i > 0 && sorted[i-1].Kind == e.Kind && sorted[i-1].ID == "" {
			continue
		}
		b.units = append(b.units, unit{text: dropLine(e)})
	}
	b.seen = len(b.units)
	return b
}

// rankOfKind is kindRank with a deterministic answer for a kind no version of this package minted.
func rankOfKind(kind string) int {
	if r, ok := kindRank[kind]; ok {
		return r
	}
	return unknownKindRank
}

// dropLine renders one drop-report line. The ID and its space are omitted when the ID is empty,
// and the " — detail" clause when the detail is.
func dropLine(e checkpoint.DropEntry) string {
	var b strings.Builder
	b.WriteString("- ")
	b.WriteString(e.Kind)
	if e.ID != "" {
		b.WriteByte(' ')
		b.WriteString(e.ID)
	}
	if e.Detail != "" {
		b.WriteString(" — ")
		b.WriteString(oneLine(e.Detail))
	}
	b.WriteByte('\n')
	return b.String()
}

// moreDropsUnit is the counted tail item 7 ends with when its own reserve could not hold the whole
// report: "… and N more; call dropped()". The complete, untruncated list is always persisted to
// the state file, so `dropped()` returns everything regardless of what fit in the payload.
//
// It is the one unit this file prices itself, because the budget pass appends it AFTER pricing.
func moreDropsUnit(d Deps, n int) unit {
	u := unit{text: "- … and " + itoa(n) + " more; call dropped()\n"}
	u.tokens = estimate(d, u.text)
	return u
}

// buildAffordance is item 8: one never-truncated line saying the retrieval tools exist. It is what
// turns a payload full of pointers into something the model can act on.
func buildAffordance(_ context.Context, r Request, d Deps) built { //nolint:unparam // the nine item builders share one signature so buildAll can call them uniformly
	return built{units: []unit{{text: AffordanceNotice() + "\n"}}, seen: 1}
}

// ── small shared helpers ──

// loggerOf is Deps.Log with the documented nil fallback. Build normalizes Deps once, but the
// builders are called directly by tests and must not panic on a zero Deps.
func loggerOf(d Deps) logging.Logger {
	if d.Log == nil {
		return logging.Nop()
	}
	return d.Log
}

// itoa is strconv.Itoa, named short because it appears inside a dozen rendered strings.
func itoa(n int) string { return strconv.Itoa(n) }

// oneLine collapses every newline in s to a single space and trims the ends, so a bounded but
// multi-line free-text field cannot break a list rendering. It does not truncate.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return strings.TrimSpace(s)
	}
	r := strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")
	return strings.TrimSpace(r.Replace(s))
}

// truncRunes truncates s to at most max RUNES, appending mark when it had to cut.
//
// The mark counts against the limit, so the result is never longer than max runes — the same rule
// negknow's record bounds use, and the reason a "truncated to 240 runes" field really is at most
// 240 runes wide. Cutting on a rune boundary matters: a half rune reaches the model as U+FFFD.
func truncRunes(s string, max int, mark string) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	keep := max - utf8.RuneCountInString(mark)
	if keep < 0 {
		keep = 0
	}
	n := 0
	for i := range s {
		if n == keep {
			return s[:i] + mark
		}
		n++
	}
	return s + mark
}

// trimToRuneBoundary drops a trailing partial rune from b, which a byte-capped read can leave
// behind. Without it a prompt cut at maxIntentBytes could end in U+FFFD.
func trimToRuneBoundary(b []byte) []byte {
	for len(b) > 0 && !utf8.Valid(b) {
		if r, size := utf8.DecodeLastRune(b); r == utf8.RuneError && size <= 1 {
			b = b[:len(b)-1]
			continue
		}
		break
	}
	return b
}

// quoteLines prefixes every line of s with "> " and ends the result with exactly one newline. It
// is item 2's rendering: a blockquote is what marks the text as the user's own words rather than
// Qompack's prose.
func quoteLines(s string) string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	var b strings.Builder
	for _, ln := range lines {
		b.WriteString("> ")
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	return b.String()
}
