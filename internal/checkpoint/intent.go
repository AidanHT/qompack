package checkpoint

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// User intent: Original and Evolution, the tier-1 half of a checkpoint that carries the user's own
// words (§8 step 4, G2.3).
//
// Both are read from the SESSION'S OWN PROMPT RECORDS — every verbatim UserPromptSubmit capture the
// store indexed for the session, in turn order (store.SessionPrompts) — and recomputed whole
// whenever the draft is refreshed: at Begin, after every Advance, and at PreCompact just before the
// seal. A forked session's are read the same way from the sessions it continues (lineage.go). Two
// sources are deliberately not used any more:
//
//   - Encoding closed segments. Evolution used to be filled only there, and the observer closes a
//     session's segment at SessionEnd, so at every host compaction the prompts since the session
//     last ended sat in its open segment, which neither a live draft nor the cold PreCompact
//     catch-up reads. No correction a user made in a live session ever reached a checkpoint
//     (F-UAT05-1, F-UAT06-2).
//   - The dependence graph's userprompt nodes. They are keyed by turn alone, so two sessions'
//     prompts at one turn share a node and the later capture's reference replaces the earlier
//     one's: a session's intent read from the graph could be another session's.
//
// Recomputing rather than appending is what keeps the list ordered and bounded across restarts: a
// resumed draft re-reads the same records and gets the same answer, and the bounds below can leave
// the oldest entries out without a later refresh mistaking them for new ones.
//
// Evolution is bounded twice, both on the KEEP-NEWEST side (draft.go explains why): by count,
// maxIntentEvolution, and by size, EvolutionCeilingChars. Truncate never cuts tier 1, so an
// evolution bounded only by count let a session of long pastes push every checkpoint past its
// budget and cost it every pointer and decision; the size bound is the most text a rehydration can
// carry, so nothing it leaves out could ever have been injected. Whatever is left out is named in
// one DropEntry with the expand call for the newest and the oldest of it.

// evolutionElidedID is the DropEntry id (under dropIntentEvolution) that says how many earlier
// restatements the bounds left out of a checkpoint.
const evolutionElidedID = "elided"

// dropIntentEvolution is the DropEntry kind for restatements a checkpoint does not carry. It is the
// kind internal/rehydrate renders an evolution delta's own drop under, so the two sort together.
const dropIntentEvolution = "user_intent_evolution"

// EvolutionCeilingChars bounds the restatement text one checkpoint's user_intent.evolution carries,
// in host characters (UTF-16 code units, the unit Claude Code measures a hook field in).
//
// DERIVATION: it is internal/rehydrate's PayloadCeilingChars, the owner-approved D5 host ceiling
// (9,500) less the contract-probe reserve (100): the most text one rehydration can inject at all.
// Item 2 admits evolution newest first and stops at the first delta that does not fit
// (rehydrate's fillPrefix), so a restatement whose newest-first running total passes this bound
// can never reach a rehydration, and carrying it would only spend the checkpoint's budget, which
// Truncate then takes from pointers and decisions because tier 1 is never cut. It is not a new
// number; rehydrate's TestEvolutionCeiling_IsThePayloadCeiling ties the two, because rehydrate may
// import this package and not the other way round (00-ARCHITECTURE.md §3.2).
const EvolutionCeilingChars = 9400

// maxUTF8BytesPerHostChar is the most UTF-8 one host character can take: three bytes for a Basic
// Multilingual Plane rune, while a four-byte rune is two host characters and an invalid byte one.
// It is a property of the two encodings, not a tunable.
const maxUTF8BytesPerHostChar = 3

// evolutionReadLimit is how much of one restatement's capture a refresh reads. A capture longer
// than this is more than EvolutionCeilingChars host characters, so it could never be kept, and
// reading it whole would cost a large paste's bytes at every refresh to learn only that.
const evolutionReadLimit int64 = maxUTF8BytesPerHostChar * EvolutionCeilingChars

// hostChars is s's length in host characters: UTF-16 code units, a rune outside the Basic
// Multilingual Plane two and everything else — the replacement an invalid byte becomes included —
// one. It is the count internal/rehydrate's hostChars makes (tied there to the hook client's),
// restated because this package may import neither.
func hostChars(s string) int {
	n := 0
	for _, r := range s {
		if l := utf16.RuneLen(r); l > 1 {
			n += l
			continue
		}
		n++
	}
	return n
}

// textState is how one prompt read ended.
type textState uint8

const (
	// textUnreadable: no stored root, or bytes that cannot be read; the prompt is skipped.
	textUnreadable textState = iota
	// textWhole: the text was read in full.
	textWhole
	// textTooLarge: the capture is longer than the read limit; no text is handed back.
	textTooLarge
)

// sessionPromptRecords returns s's prompt records in turn order through the store's optional
// store.SessionPrompts capability. ok is false when the store does not have it, or cannot answer;
// the caller then keeps what it had rather than recomputing from nothing.
func sessionPromptRecords(ctx context.Context, st store.Store, s core.SessionID) ([]store.ToolUseRecord, bool) {
	sp, ok := st.(store.SessionPrompts)
	if !ok {
		return nil, false
	}
	recs, err := sp.SessionPrompts(ctx, s)
	if err != nil {
		pkgLog().Warn("checkpoint: the session's prompt records are unreadable; its intent is left as it was",
			"session", string(s), "err", err.Error())
		return nil, false
	}
	return recs, true
}

// promptTextLocked returns one prompt record's verbatim text through fromStore, from the draft's
// cache when an earlier refresh kept it. limit bounds the read (zero: whole). It does not add to the
// cache: refreshIntentLocked replaces the cache with exactly the texts it kept. Caller holds d.mu.
func (d *Draft) promptTextLocked(ctx context.Context, rec store.ToolUseRecord, limit int64) (string, textState) {
	if text, ok := d.promptText[rec.ID]; ok {
		return text, textWhole
	}
	return readRootText(ctx, d.src, rec.Root, string(rec.ID), limit)
}

// readRootText reads one stored object and renders it through fromStore: whole when limit is zero,
// and otherwise only when it is at most limit bytes (textTooLarge past that, with no text). what
// names the object in the Debug line a failure leaves. A missing or unreadable object is skipped,
// never an error: GC may legitimately have collected an old prompt, and one lost restatement must
// not fail a checkpoint.
func readRootText(ctx context.Context, src SourceSet, root core.Hash, what string, limit int64) (string, textState) {
	if root.IsZero() {
		pkgLog().Debug("checkpoint: prompt has no stored root; skipped", "prompt", what)
		return "", textUnreadable
	}
	rc, err := src.Store.Open(ctx, root)
	if err != nil {
		pkgLog().Debug("checkpoint: prompt bytes unreadable; skipped", "prompt", what, "err", err.Error())
		return "", textUnreadable
	}
	defer func() { _ = rc.Close() }()
	var r io.Reader = rc
	if limit > 0 {
		// One byte past the limit is what tells "exactly the limit" from "longer than it".
		r = io.LimitReader(rc, limit+1)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		pkgLog().Debug("checkpoint: prompt bytes unreadable; skipped", "prompt", what, "err", err.Error())
		return "", textUnreadable
	}
	if limit > 0 && int64(len(b)) > limit {
		return "", textTooLarge
	}
	return fromStore(b), textWhole
}

// intentCandidate is one prompt a draft's evolution may carry: a prompt record (rec.ID set), read
// through the draft's cache, or a restatement copied from a checkpoint (copied), which is what a
// fork inherits when its ancestry's records cannot be enumerated.
type intentCandidate struct {
	rec    store.ToolUseRecord
	copied string
}

// recordCandidates wraps prompt records as candidates, in the same order.
func recordCandidates(recs []store.ToolUseRecord) []intentCandidate {
	out := make([]intentCandidate, 0, len(recs))
	for _, r := range recs {
		out = append(out, intentCandidate{rec: r})
	}
	return out
}

// refreshIntentLocked recomputes d.cp.UserIntent from the prompt records: the session's first one
// is the Original and every later one an evolution entry, verbatim, oldest first, with exact
// duplicates listed once, at their newest position, and restatements of the Original not at all.
// When the session's records cannot be read at all, the draft keeps the intent it has. Caller
// holds d.mu.
//
// The Original the draft was seeded with (the session's own checkpoint chain, seedTierOne) stands
// when the first record's bytes are gone — G2.3's promise that the user's own words survive
// arbitrarily many generations does not depend on the object store keeping every prompt forever.
//
// A forked session (d.fork) is the other shape: its original and earlier history are the ones its
// conversation came with (lineage.go), and EVERY one of its own prompts, its first included, is an
// evolution entry after them.
func (d *Draft) refreshIntentLocked(ctx context.Context) {
	own, ok := sessionPromptRecords(ctx, d.src.Store, d.session)
	d.promptsAnswered = ok
	if !ok {
		return
	}
	newest := own
	original := d.cp.UserIntent.Original
	var first *store.ToolUseRecord
	var cands []intentCandidate
	switch {
	case d.fork == nil:
		if len(own) > 0 {
			first = &own[0]
			own = own[1:]
		}
	case d.fork.fromRecords:
		first = d.fork.origin
		cands = recordCandidates(d.fork.inherited)
	default:
		if d.fork.original != "" {
			original = d.fork.original
		}
		for _, text := range d.fork.evolution {
			cands = append(cands, intentCandidate{copied: text})
		}
	}
	cands = append(cands, recordCandidates(own)...)

	keep := map[core.ToolUseID]string{}
	if first != nil {
		if text, st := d.promptTextLocked(ctx, *first, 0); st == textWhole && strings.TrimSpace(text) != "" {
			original = text
			keep[first.ID] = text
		}
	}
	evo, el := d.selectEvolutionLocked(ctx, original, cands, keep)
	d.promptText = keep
	d.setIntentLocked(original, evo, el)
	d.deriveCurrentWorkLocked(ctx, newest)
}

// deriveCurrentWorkLocked sets the derived CurrentWork — one sentence of the session's most recent
// prompt, an empty NextStep and a nil BlockedOn (§8) — from own, the session's OWN prompt records
// in turn order, at every refresh: the open segment's prompts included, and for a fork only the
// fork's own, never one it inherited. It used to be derived while encoding a closed segment, from
// the graph's userprompt nodes in the segment's turn range; those are keyed by turn alone, so a
// fork's segment found its parent's prompts at the turns the two shared and took the highest
// (F-C7-UAT06-1), and the open segment's prompts never counted. Once SetCurrentWork has spoken
// nothing is derived (§7). Caller holds d.mu.
//
// The goal is the newest prompt that gives one (goalOf), walking back from the newest record past
// a slash-command invocation, a blank prompt and one whose bytes cannot be read, at most
// goalWalkLimit records. When none does, the goal is kept, except when every one of the session's
// prompts was read and none can give one — a session with no prompt of its own yet, a fork before
// its user says anything — when a derived goal held cannot have come from this session's prompts
// and is cleared: a draft persisted by candidate 7 held its parent's prompt there.
//
// It never moves the goal back on a walk that could not read every record it passed (the context
// running out included): one whose bytes cannot be read, for now or for good, may be the very
// record the goal held came from (a resumed draft has read none of them yet), so a goal the walk
// found at an earlier turn than that one (goalTurn) replaces it only when the walk read them all.
// Then the records have the last word, even over a goal_turn none of them reaches (a store
// restored from an older backup than the draft). A walk is repeated at every refresh; each record
// it read whole is read once per draft (newestGoalLocked), so a repeat costs a failed Open for
// each unreadable record and no bytes.
//
// "Newest" is the captured turn, not the record's host stamp. A prompt that reached only a hook's
// client spool can be published behind one its host sent later (observer.prompt_out_of_host_order);
// captured turns are the canonical order and are never renumbered (D35(b)), the evolution lists the
// same records in turn order, and current work agrees with the evolution's last entry rather than
// contradicting it exactly where the order is in doubt.
func (d *Draft) deriveCurrentWorkLocked(ctx context.Context, own []store.ToolUseRecord) {
	if d.workExplicit {
		return
	}
	goal, turn, found, complete := d.newestGoalLocked(ctx, own)
	switch {
	case found && (complete || !d.goalTurnSet || turn >= d.goalTurn):
		d.setGoalTurnLocked(turn, true)
		d.setDerivedWorkLocked(goal)
	case complete && len(own) <= goalWalkLimit:
		// Every one of the session's prompts was read and none gives a goal (a walk that found one
		// and read every record it passed took the case above).
		d.setGoalTurnLocked(0, false)
		d.setDerivedWorkLocked("")
	}
}

// goalWalkLimit is how many of the session's newest prompt records one derivation of the goal looks
// at before it keeps the goal it has.
//
// DERIVATION: it is maxIntentEvolution, the most restatements a checkpoint's evolution lists, so
// the walk reads no further back than the intent beside it can reach. It bounds what a run of
// skipped prompts (a user issuing slash command after slash command) costs each new prompt.
const goalWalkLimit = maxIntentEvolution

// newestGoalLocked walks own (turn order) from the newest record, at most goalWalkLimit of them,
// and returns the goal the first one that gives one gives, with its turn. complete reports that
// every record the walk passed — up to the one it found, or to the walk's end — was read whole.
// What each record it read whole gives (d.goalSeen) replaces what the previous walk kept, so a
// record is read once while it stays in the walk's reach, whatever the walk ends on. Caller holds
// d.mu.
func (d *Draft) newestGoalLocked(ctx context.Context, own []store.ToolUseRecord) (goal string, turn core.TurnIndex,
	found, complete bool,
) {
	complete = true
	seen := make(map[core.ToolUseID]string, min(len(own), goalWalkLimit))
	defer func() { d.goalSeen = seen }()
	for i := len(own) - 1; i >= 0 && len(own)-i <= goalWalkLimit; i-- {
		g, ok := d.goalSeen[own[i].ID]
		if !ok {
			text, st := d.promptTextLocked(ctx, own[i], 0)
			if st != textWhole {
				complete = false
				continue
			}
			g, _ = goalOf(text) // "" exactly when the prompt gives no goal
		}
		seen[own[i].ID] = g
		if g != "" {
			return g, own[i].Turn, true, complete
		}
	}
	return "", 0, false, complete
}

// setGoalTurnLocked records the turn the derived goal was read from (set false: there is none),
// marking the draft dirty when it changes, so the persisted goal_turn never lags the goal: two
// prompts with the same first sentence give the same goal from different turns. Caller holds d.mu.
func (d *Draft) setGoalTurnLocked(turn core.TurnIndex, set bool) {
	if !set {
		turn = 0
	}
	if d.goalTurnSet == set && d.goalTurn == turn {
		return
	}
	d.goalTurn, d.goalTurnSet = turn, set
	d.dirty = true
}

// setDerivedWorkLocked installs the derived CurrentWork for goal (empty clears it), marking the
// draft dirty only when it changes. Caller holds d.mu.
func (d *Draft) setDerivedWorkLocked(goal string) {
	if d.cp.CurrentWork.Goal == goal && d.cp.CurrentWork.NextStep == "" && d.cp.CurrentWork.BlockedOn == nil {
		return
	}
	d.cp.CurrentWork = CurrentWork{Goal: goal}
	d.dirty = true
}

// qompackCommandPrefix opens every invocation of Qompack's own slash commands
// (plugin/commands/*.md, under the plugin's name).
const qompackCommandPrefix = "/qompack:"

// goalOf is the derived goal one prompt's text gives — its first sentence, capped at goalMaxRunes —
// or false when it gives none: a blank prompt, or a slash-command invocation (isCommandInvocation).
func goalOf(text string) (string, bool) {
	if isCommandInvocation(text) {
		return "", false
	}
	g := truncRunes(firstSentence(text), goalMaxRunes)
	if strings.TrimSpace(g) == "" {
		return "", false
	}
	return g, true
}

// isCommandInvocation reports whether a prompt is a slash-command invocation that says nothing about
// the task in flight. Claude Code hands UserPromptSubmit the prompt as typed, so a plugin command is
// captured as "/qompack:why dec_..." (the candidate-7 UAT-06 store holds exactly that) while a
// built-in such as /compact never reaches the hook. Two shapes count:
//
//   - any invocation of Qompack's own commands (status, why, dropped, recall, eval, pin), whatever
//     its arguments: they inspect or annotate the session, and a pinned rule is carried as an
//     invariant, not as the task;
//   - a bare "/name" with no arguments, the whole prompt one command token.
//
// Another command's arguments are the user's own words about the task and are kept, and a path
// ("/usr/bin/x fails") is not a command name because a name holds no "/".
func isCommandInvocation(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasPrefix(t, qompackCommandPrefix) || isCommandToken(t)
}

// isCommandToken reports whether t is exactly one slash-command token: "/" then a letter or digit,
// then letters, digits and "-", "_", ".", ":" (a plugin or directory namespace).
func isCommandToken(t string) bool {
	if len(t) < 2 || t[0] != '/' {
		return false
	}
	for i, r := range t[1:] {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case i > 0 && (r == '-' || r == '_' || r == '.' || r == ':'):
		default:
			return false
		}
	}
	return true
}

// elision is what the evolution bounds left out of one refresh: how many candidates, and the
// records of the newest and the oldest of them (empty for a copy with no record).
type elision struct {
	n              int
	newest, oldest core.ToolUseID
}

// note counts one candidate left out. Candidates are walked newest first, so the first id noted is
// the newest and the last the oldest.
func (el *elision) note(id core.ToolUseID) {
	el.n++
	if id == "" {
		return
	}
	if el.newest == "" {
		el.newest = id
	}
	el.oldest = id
}

// selectEvolutionLocked walks cands (oldest first) from the NEWEST, keeping each whole restatement
// while both bounds hold, and stops at the first that would pass either: that one and every older
// candidate are left out, unread, because item 2 could inject none of them after it. It returns the
// kept restatements oldest first. keep collects the texts the walk read and the draft may cache:
// the kept ones, and duplicates of them or of the original under their own ids (sharing the same
// string), so a later refresh reads neither again; d.oversized remembers the record it stopped at
// for being too long, which is not cached either. Caller holds d.mu.
func (d *Draft) selectEvolutionLocked(ctx context.Context, original string, cands []intentCandidate,
	keep map[core.ToolUseID]string,
) ([]string, elision) {
	kept := make([]string, 0, min(len(cands), maxIntentEvolution))
	var el elision
	used := 0
	stopped := false
	oversized := d.oversized
	d.oversized = ""
	for i := len(cands) - 1; i >= 0; i-- {
		c := cands[i]
		if stopped {
			el.note(c.rec.ID)
			continue
		}
		text, st := c.copied, textWhole
		switch c.rec.ID {
		case "":
		case oversized:
			st = textTooLarge
		default:
			text, st = d.promptTextLocked(ctx, c.rec, evolutionReadLimit)
		}
		switch st {
		case textUnreadable:
			continue
		case textTooLarge:
			stopped = true
			el.note(c.rec.ID)
			d.oversized = c.rec.ID
			continue
		}
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || text == original {
			if c.rec.ID != "" {
				// Cached as what it counts for, so the entry costs no second copy of the text.
				keep[c.rec.ID] = original
				if trimmed == "" {
					keep[c.rec.ID] = ""
				}
			}
			continue
		}
		if j := slices.Index(kept, text); j >= 0 {
			if c.rec.ID != "" {
				keep[c.rec.ID] = kept[j]
			}
			continue
		}
		n := hostChars(trimmed)
		if len(kept) == maxIntentEvolution || used+n > EvolutionCeilingChars {
			stopped = true
			el.note(c.rec.ID)
			continue
		}
		kept = append(kept, text)
		used += n
		if c.rec.ID != "" {
			keep[c.rec.ID] = text
		}
	}
	slices.Reverse(kept)
	return kept, el
}

// setIntentLocked installs original, the kept evolution and the elision entry, and marks the draft
// dirty only when something changed. Caller holds d.mu.
func (d *Draft) setIntentLocked(original string, evo []string, el elision) {
	changed := original != d.cp.UserIntent.Original || !slices.Equal(evo, d.cp.UserIntent.Evolution)
	d.cp.UserIntent.Original = original
	d.cp.UserIntent.Evolution = evo
	if d.setElidedLocked(el) {
		changed = true
	}
	if changed {
		d.dirty = true
	}
}

// setElidedLocked keeps exactly one DropEntry saying how many earlier restatements the bounds left
// out and naming the newest and the oldest of them, or none when they left out nothing, and reports
// whether the drop list changed. Each is still a prompt record in the store, which is where the
// entry points. Caller holds d.mu.
func (d *Draft) setElidedLocked(el elision) bool {
	want := DropEntry{}
	if el.n > 0 {
		want = DropEntry{Kind: dropIntentEvolution, ID: evolutionElidedID, Detail: el.detail()}
	}
	kept := d.cp.Dropped[:0:0]
	have := DropEntry{}
	for _, e := range d.cp.Dropped {
		if e.Kind == dropIntentEvolution && e.ID == evolutionElidedID {
			have = e
			continue
		}
		kept = append(kept, e)
	}
	if want != (DropEntry{}) {
		kept = append(kept, want)
	}
	if have == want {
		return false
	}
	d.cp.Dropped = kept
	return true
}

// detail is the elision entry's text: the count, the bounds, and the expand call for the newest and
// the oldest restatement left out.
func (el elision) detail() string {
	s := fmt.Sprintf("%d earlier restatements were left out to hold the evolution bounds (%d entries, "+
		"%d characters: the most one rehydration can carry); each is still a verbatim prompt capture "+
		"in the store (timeline, recall, expand)", el.n, maxIntentEvolution, EvolutionCeilingChars)
	switch {
	case el.newest == "":
	case el.oldest == el.newest:
		s += "; restore: expand(tool_use_id=" + string(el.newest) + ")"
	default:
		s += "; restore: expand(tool_use_id=" + string(el.newest) + ") is the newest left out, expand(tool_use_id=" +
			string(el.oldest) + ") the oldest"
	}
	return s
}

// EvolutionElided reports whether c's evolution bounds left some restatements out, which is when a
// restatement c does not list may still be one of the session's (internal/rehydrate's fork check).
func EvolutionElided(c Checkpoint) bool {
	for _, e := range c.Dropped {
		if e.Kind == dropIntentEvolution && e.ID == evolutionElidedID {
			return true
		}
	}
	return false
}

// RefreshIntent recomputes the draft's user intent from the prompt records now (see
// refreshIntentLocked). PreCompact calls it just before the seal, so the checkpoint a compaction
// seals carries every prompt the session has made, including those in its still-open segment. A
// sealed draft is left alone: its bytes are already decided.
func (d *Draft) RefreshIntent(ctx context.Context) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sealed {
		return
	}
	d.refreshIntentLocked(ctx)
}

// seedIntent fills a fresh draft's user intent (§8 step 4). own is the checkpoint of THIS session
// the draft descends from, or nil when its parent is another session's or there is none.
//
// The session's own chain is the seed, copied and never regenerated (G2.3), and the prompt records
// then refresh it. Another session's checkpoint is never a seed: a derived parent is only the
// project's newest checkpoint, and adopting its intent made every later session in the project
// start from someone else's original and report an intent_mismatch at its first rehydration. When
// the records name no readable first prompt, the session's own newest checkpoint is looked up — a
// scan of the manifest, which is why it is the last resort rather than the first.
//
// A store that cannot enumerate a session's prompts keeps the earlier behaviour: the graph's
// earliest userprompt node.
func (w *FileWriter) seedIntent(ctx context.Context, d *Draft, own *Checkpoint) {
	d.fork = w.forkIntentFor(ctx, d.src.Store, d.session, own)
	switch {
	case d.fork != nil:
		d.cp.UserIntent.Original = d.fork.original
		d.cp.UserIntent.Evolution = slices.Clone(d.fork.evolution)
	case own != nil:
		d.cp.UserIntent.Original = own.UserIntent.Original
	}
	if _, capable := d.src.Store.(store.SessionPrompts); !capable {
		if d.cp.UserIntent.Original == "" {
			if first, ok := earliestPrompt(d.src.Graph); ok {
				if text, ok := readPromptText(ctx, d.src, first); ok {
					d.cp.UserIntent.Original = text
				}
			}
		}
		return
	}
	d.refreshIntentLocked(ctx)
	if d.cp.UserIntent.Original != "" || own != nil || d.fork != nil {
		return
	}
	if latest, ok := w.ownLatest(ctx, d.session); ok {
		d.cp.UserIntent.Original = latest.UserIntent.Original
		d.refreshIntentLocked(ctx)
	}
}

// ownLatest is session s's own newest verifying checkpoint, if it has one. Reader.Latest answers
// with the project's newest when s has none; that answer is not s's and is refused here.
func (w *FileWriter) ownLatest(ctx context.Context, s core.SessionID) (Checkpoint, bool) {
	cp, _, err := w.reader.Latest(ctx, s)
	if err != nil || cp.Session != s {
		return Checkpoint{}, false
	}
	return cp, true
}

// promptHandoff is what a sealed draft learned of its session's prompt records, carried to the
// successor Finalize opens for the same session (handOff, takeHandoff): the texts its intent kept
// (promptText), the goal each record its last goal walk read whole gives (goalSeen), and the record
// its evolution walk found past evolutionReadLimit (oversized). A record's bytes never change, so
// all three stay valid for whichever draft of the session uses them.
//
// The goals and the oversized record are what keep a long paste off the hook path. The successor is
// opened inside PreCompact, and promptText never holds a prompt past the read limit; carried alone,
// it left the successor's first refresh to read the newest prompt whole again for its goal (up to
// the hook's 4 MiB capture) and its first evolutionReadLimit+1 bytes again, at every compaction
// (audit 2's #25).
type promptHandoff struct {
	texts     map[core.ToolUseID]string
	goals     map[core.ToolUseID]string
	oversized core.ToolUseID
}

// handOff stashes what a sealed draft learned of its prompt records for the successor Finalize is
// about to open for the same session (takeHandoff). It is bounded as the draft's own state is: the
// cache holds only what the draft's intent kept (refreshIntentLocked), the goals at most
// goalWalkLimit entries, one short goal each.
func (w *FileWriter) handOff(d *Draft) {
	d.mu.Lock()
	h := promptHandoff{texts: d.promptText, goals: d.goalSeen, oversized: d.oversized}
	d.mu.Unlock()
	if len(h.texts) == 0 && len(h.goals) == 0 && h.oversized == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.handoff == nil {
		w.handoff = map[core.SessionID]promptHandoff{}
	}
	w.handoff[d.session] = h
}

// takeHandoff returns and forgets what handOff stashed for s, with an empty text cache when nothing
// was. Begin takes it on every path, so a stash is never left behind by a Begin that found a live
// draft, resumed a persisted one or failed.
func (w *FileWriter) takeHandoff(s core.SessionID) promptHandoff {
	w.mu.Lock()
	defer w.mu.Unlock()
	h := w.handoff[s]
	delete(w.handoff, s)
	if h.texts == nil {
		h.texts = map[core.ToolUseID]string{}
	}
	return h
}
