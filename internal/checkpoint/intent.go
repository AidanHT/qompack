package checkpoint

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// User intent: Original and Evolution, the tier-1 half of a checkpoint that carries the user's own
// words (§8 step 4, G2.3).
//
// Both are read from the SESSION'S OWN PROMPT RECORDS — every verbatim UserPromptSubmit capture the
// store indexed for the session, in turn order (store.SessionPrompts) — and recomputed whole
// whenever the draft is refreshed: at Begin, after every Advance, and at PreCompact just before the
// seal. Two sources are deliberately not used any more:
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
// resumed draft re-reads the same records and gets the same answer, and the cap below can drop the
// oldest entries without a later refresh mistaking them for new ones.

// evolutionElidedID is the DropEntry id (under dropIntentEvolution) that says how many earlier
// restatements the cap left out of a checkpoint.
const evolutionElidedID = "elided"

// dropIntentEvolution is the DropEntry kind for restatements a checkpoint does not carry. It is the
// kind internal/rehydrate renders an evolution delta's own drop under, so the two sort together.
const dropIntentEvolution = "user_intent_evolution"

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

// promptTextLocked returns one prompt record's verbatim text through fromStore, reading each record
// once per draft: a refresh at every PreCompact would otherwise re-open every prompt of the session.
// Caller holds d.mu.
func (d *Draft) promptTextLocked(ctx context.Context, rec store.ToolUseRecord) (string, bool) {
	if text, ok := d.promptText[rec.ID]; ok {
		return text, true
	}
	text, ok := readRootText(ctx, d.src, rec.Root, string(rec.ID))
	if !ok {
		return "", false
	}
	if d.promptText == nil {
		d.promptText = map[core.ToolUseID]string{}
	}
	d.promptText[rec.ID] = text
	return text, true
}

// readRootText reads one stored object whole and renders it through fromStore. what names the
// object in the Debug line a failure leaves. A missing or unreadable object is skipped, never an
// error: GC may legitimately have collected an old prompt, and one lost restatement must not fail
// a checkpoint.
func readRootText(ctx context.Context, src SourceSet, root core.Hash, what string) (string, bool) {
	if root.IsZero() {
		pkgLog().Debug("checkpoint: prompt has no stored root; skipped", "prompt", what)
		return "", false
	}
	rc, err := src.Store.Open(ctx, root)
	if err != nil {
		pkgLog().Debug("checkpoint: prompt bytes unreadable; skipped", "prompt", what, "err", err.Error())
		return "", false
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		pkgLog().Debug("checkpoint: prompt bytes unreadable; skipped", "prompt", what, "err", err.Error())
		return "", false
	}
	return fromStore(b), true
}

// refreshIntentLocked recomputes d.cp.UserIntent from the session's own prompt records: the first
// readable one is the Original, and every later one is an evolution entry, verbatim, oldest first,
// with exact duplicates and restatements of the Original listed once. When the records cannot be
// read at all, the draft keeps the intent it has. Caller holds d.mu.
//
// The Original the draft was seeded with (the session's own checkpoint chain, seedTierOne) stands
// when the first record's bytes are gone — G2.3's promise that the user's own words survive
// arbitrarily many generations does not depend on the object store keeping every prompt forever.
//
// A forked session (d.fork) is the other shape: its original and earlier history are the parent's,
// and EVERY one of its own prompts, its first included, is an evolution entry after them.
func (d *Draft) refreshIntentLocked(ctx context.Context) {
	recs, ok := sessionPromptRecords(ctx, d.src.Store, d.session)
	if !ok {
		return
	}
	original := d.cp.UserIntent.Original
	var later []string
	if d.fork != nil {
		original = d.fork.original
		later = slices.Clone(d.fork.evolution)
	}
	for i, rec := range recs {
		text, readable := d.promptTextLocked(ctx, rec)
		if !readable {
			continue
		}
		if i == 0 && d.fork == nil {
			if text != "" {
				original = text
			}
			continue
		}
		later = append(later, text)
	}
	d.setIntentLocked(original, later)
}

// setIntentLocked installs original and the evolution candidates, applying the dedup and the cap,
// and marks the draft dirty only when something changed. Caller holds d.mu.
func (d *Draft) setIntentLocked(original string, candidates []string) {
	evo := make([]string, 0, len(candidates))
	for _, text := range candidates {
		if text == "" || text == original || slices.Contains(evo, text) {
			continue
		}
		evo = append(evo, text)
	}
	elided := 0
	if len(evo) > maxIntentEvolution {
		elided = len(evo) - maxIntentEvolution
		evo = evo[elided:]
	}

	changed := original != d.cp.UserIntent.Original || !slices.Equal(evo, d.cp.UserIntent.Evolution)
	d.cp.UserIntent.Original = original
	d.cp.UserIntent.Evolution = evo
	if d.setElidedLocked(elided) {
		changed = true
	}
	if changed {
		d.dirty = true
	}
}

// setElidedLocked keeps exactly one DropEntry saying how many earlier restatements the cap left
// out, or none when it left out nothing, and reports whether the drop list changed. Each one is
// still a prompt record in the store, which is where the entry points. Caller holds d.mu.
func (d *Draft) setElidedLocked(n int) bool {
	want := DropEntry{}
	if n > 0 {
		want = DropEntry{
			Kind: dropIntentEvolution, ID: evolutionElidedID,
			Detail: fmt.Sprintf("%d earlier restatements were left out to hold the %d-entry evolution cap; "+
				"each is still a verbatim prompt capture in the store (timeline, recall, expand)", n, maxIntentEvolution),
		}
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

// RefreshIntent recomputes the draft's user intent from the session's prompt records now (see
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
// The session's own chain is the seed, copied and never regenerated (G2.3), and the session's own
// prompt records then refresh it. Another session's checkpoint is never a seed: a derived parent is
// only the project's newest checkpoint, and adopting its intent made every later session in the
// project start from someone else's original and report an intent_mismatch at its first
// rehydration. When the records name no readable first prompt, the session's own newest checkpoint
// is looked up — a scan of the manifest, which is why it is the last resort rather than the first.
//
// A store that cannot enumerate a session's prompts keeps the earlier behaviour: the graph's
// earliest userprompt node.
func (w *FileWriter) seedIntent(ctx context.Context, d *Draft, own *Checkpoint) {
	d.fork = w.forkIntentFor(ctx, d.session, own)
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

// handOff stashes a sealed draft's prompt-text cache for the successor Finalize is about to open
// for the same session (takeHandoff).
func (w *FileWriter) handOff(d *Draft) {
	d.mu.Lock()
	texts := d.promptText
	d.mu.Unlock()
	if len(texts) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.handoff == nil {
		w.handoff = map[core.SessionID]map[core.ToolUseID]string{}
	}
	w.handoff[d.session] = texts
}

// takeHandoff returns and forgets the prompt-text cache handOff stashed for s, or an empty one.
func (w *FileWriter) takeHandoff(s core.SessionID) map[core.ToolUseID]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	texts := w.handoff[s]
	delete(w.handoff, s)
	if texts == nil {
		texts = map[core.ToolUseID]string{}
	}
	return texts
}
