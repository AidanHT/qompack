package checkpoint

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/tokens"
)

// The three accumulation caps of §7/§8. They bound what a single draft can pin in memory and in
// state/draft-<session>.json, and each cap's KEEP side is deliberate:
//
//   - maxIntentEvolution keeps the NEWEST 64 restatements, and one DropEntry says how many earlier
//     ones it left out (intent.go setIntentLocked). It used to keep the oldest, on the grounds that
//     early restatements explain the session's shape, but then every correction after a session's
//     65th prompt was lost to the one field that carries the current authority (Qompack.md §8.6:
//     current authority takes precedence over obsolete intent). Nothing is lost silently: each
//     left-out restatement is still its own prompt record in the store, and the entry says so.
//   - maxOpenQuestions drops the NEWEST once full (§8's "newest dropped once full"): an open
//     question that arrived when the list was already saturated is the one the session has had
//     the least time to depend on.
//   - maxDraftDecisions keeps the head of the Turn-descending order (§8 step 3): Truncate cuts
//     tail-first, so the head is the newest and the cap discards the stalest decisions first. It
//     is the package's single declaration of §8's decision cap: the per-extraction cap in §9 is
//     the same bound applied one pass earlier, and a second constant spelling the same number is
//     free to drift from this one.
const (
	maxIntentEvolution = 64
	maxOpenQuestions   = 32
	maxDraftDecisions  = 64
)

// pointerWhyMaxRunes is §8's one-line reason cap, and this is the package's only declaration of
// it. It bounds two strings: FilePointer.Why, which §8's file-pointer bullet caps at one line, and
// the reason a DropEntry carries for a dropped open question, where the question text itself is
// the most useful identifier the drop report can keep but is agent-authored free text (§6.9). Exit
// criterion 1568 authorizes exactly one annotated 120 in internal/checkpoint, and this is it — a
// second declaration spelling the same number is free to drift from this one.
const pointerWhyMaxRunes = 120 //nomagic:allow one-line pointer reason cap (§8); unrelated to scheduler.idle.detectAfterSeconds

// draftPerm is the mode every state/draft-<session>.json is written with: private to the user,
// like everything else under .qompack/ (§3.3). The draft carries verbatim user intent, so it gets
// the store's own 0o600, not a world-readable default.
const draftPerm = 0o600

// Seq returns the checkpoint sequence number this draft will finalize as.
func (d *Draft) Seq() core.CheckpointSeq {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.seq
}

// setSeq re-points the draft at the sequence number its artifact was actually written at, which
// Finalize's O_EXCL retry can move. It exists so that d.seq — the number Advance's DPI guard
// compares each segment's recorded checkpoint against — never names a checkpoint that was not
// written.
func (d *Draft) setSeq(seq core.CheckpointSeq) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq = seq
	d.cp.Seq = seq
}

// Frontier returns the turn index up to which this draft has encoded original content — the
// incrementally advancing checkpoint frontier of §8.5.
func (d *Draft) Frontier() core.TurnIndex {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.frontier
}

// EncodedCount reports how many segments have been encoded into this draft since Begin — one of
// the two §8.5 idle-cadence inputs finalizeIfDue seals a draft on, computable with no store read.
func (d *Draft) EncodedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.encoded)
}

// EstimatedTokens prices the draft's current serialized form through the SourceSet's own
// estimator (d.src.Tokens — the writer holds no estimator of its own, §8). It is the other
// idle-cadence input: finalizeIfDue compares it against cfg.Checkpoint.BudgetTokens.
//
// A draft whose sources are not wired yet prices as zero rather than panicking: the answer is
// honest — nothing can be estimated — and the cadence simply never fires on it.
func (d *Draft) EstimatedTokens() core.Tokens {
	d.mu.Lock()
	snap := d.snapshotLocked()
	est := d.src.Tokens
	d.mu.Unlock()
	if est == nil {
		return 0
	}
	b, err := Marshal(snap)
	if err != nil {
		pkgLog().Warn("checkpoint: draft estimate: marshal failed", "err", err.Error())
		return 0
	}
	return est.Estimate(b, tokens.ClassJSON)
}

// SetCache records the prompt-cache accounting for this draft's compaction point.
func (d *Draft) SetCache(c CacheInfo) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cp.Cache = c
	d.dirty = true
	d.persistOrLogLocked()
}

// AddDrops appends entries to the explicit drop report (G4.5).
func (d *Draft) AddDrops(e ...DropEntry) {
	if len(e) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cp.Dropped = append(d.cp.Dropped, e...)
	d.dirty = true
	d.persistOrLogLocked()
}

// SetCurrentWork overrides whatever Advance derived and stops all further derivation (§7):
// SP-12's todo state (and any future manual checkpoint route; 0.3.0 ships none, D36) knows the
// real goal and next step, and a heuristic must never overwrite a statement.
func (d *Draft) SetCurrentWork(w CurrentWork) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cp.CurrentWork = w
	d.workExplicit = true
	d.dirty = true
	d.persistOrLogLocked()
}

// SetOpenQuestions replaces the explicitly contributed question list (§7). Advance's derived
// questions are kept and re-materialize after the explicit ones.
func (d *Draft) SetOpenQuestions(q []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.userOQ = d.userOQ[:0]
	for _, s := range q {
		d.addUserQuestionLocked(s)
	}
	d.dirty = true
	d.persistOrLogLocked()
}

// AddOpenQuestion appends one explicitly contributed question, deduplicated by exact string
// against everything already listed, capped at maxOpenQuestions (§7).
func (d *Draft) AddOpenQuestion(q string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.addUserQuestionLocked(q)
	d.dirty = true
	d.persistOrLogLocked()
}

// addUserQuestionLocked is the shared body of the two question mutators. Caller holds d.mu.
func (d *Draft) addUserQuestionLocked(q string) {
	if q == "" || d.questionListedLocked(q) {
		return
	}
	if len(d.userOQ)+len(d.derivedOQ) >= maxOpenQuestions {
		return // the list is full; the newest arrival is the one dropped (§8)
	}
	d.userOQ = append(d.userOQ, q)
}

// addDerivedQuestionLocked appends one Advance-derived question under the same dedup and cap.
// Caller holds d.mu.
func (d *Draft) addDerivedQuestionLocked(q string) {
	if q == "" || d.questionListedLocked(q) {
		return
	}
	if len(d.userOQ)+len(d.derivedOQ) >= maxOpenQuestions {
		return
	}
	d.derivedOQ = append(d.derivedOQ, q)
}

// questionListedLocked reports whether q is already present in either question list.
func (d *Draft) questionListedLocked(q string) bool {
	return slices.Contains(d.userOQ, q) || slices.Contains(d.derivedOQ, q)
}

// snapshot returns a deep copy of the accumulated checkpoint under the mutex (§7): the caller —
// EstimatedTokens, a test — may mutate the copy freely without racing Advance.
//
// It is NOT enough on its own for Finalize: a snapshot only freezes the bytes, it does not stop
// the draft from accepting more content afterwards. Finalize uses sealForFinalize.
func (d *Draft) snapshot() Checkpoint {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snapshotLocked()
}

// sources returns the SourceSet this draft was opened over, under d.mu. Every read of d.src goes
// through here or through Advance's own critical section: Begin writes the field (a live draft
// re-Begin-ned with a fresh SourceSet adopts it), so an unsynchronized read is a data race, not
// merely a stale value.
func (d *Draft) sources() SourceSet {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.src
}

// sealForFinalize atomically takes Finalize's snapshot and closes the draft to further content,
// reporting false when the draft was already sealed — by an Abort, or by a Finalize still in
// flight on another goroutine.
//
// The atomicity is the point. Finalize runs validate, truncate, marshal, CreateNew and
// AppendManifest without holding d.mu — it must, because that is file I/O and the draft's other
// callers cannot be blocked behind an fsync — so without a flag set inside the snapshot's own
// critical section, a concurrent Advance would mark segments as encoded into a checkpoint whose
// content was decided before they existed. Those segments are then encodable into no checkpoint
// ever: store.SegmentLog.Unencoded filters out anything already flagged, and Advance's DPI guard
// skips them for the successor draft because they name a different sequence. That is the §8.2
// content loss the guard exists to prevent, and it is reachable in production — the daemon runs
// its idle sweep and its checkpoint hook on different goroutines.
func (d *Draft) sealForFinalize() (Checkpoint, SourceSet, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sealed {
		return Checkpoint{}, SourceSet{}, false
	}
	d.sealed = true
	return d.snapshotLocked(), d.src, true
}

// seal closes the draft to further content without taking a snapshot, and reports whether it was
// this call that did so. Abort uses it: an aborted draft must never become an artifact, and the
// conformance suite asserts exactly that.
func (d *Draft) seal() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sealed {
		return false
	}
	d.sealed = true
	return true
}

// unseal reopens a draft whose Finalize failed before the artifact was committed. A write that
// never landed must not cost the session its ability to keep encoding: the next cadence tick
// retries, and Advance has to be able to run in between.
func (d *Draft) unseal() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sealed = false
}

// snapshotLocked is snapshot's body. Caller holds d.mu.
func (d *Draft) snapshotLocked() Checkpoint {
	c := d.cp

	c.EncodedSegments = d.sortedEncodedLocked()
	c.Invariants = slices.Clone(c.Invariants)
	c.UserIntent.Evolution = slices.Clone(c.UserIntent.Evolution)

	c.Eliminated = slices.Clone(c.Eliminated)
	for i := range c.Eliminated {
		c.Eliminated[i].DependsOn = slices.Clone(c.Eliminated[i].DependsOn)
		c.Eliminated[i].StaleBecause = slices.Clone(c.Eliminated[i].StaleBecause)
	}

	c.Decisions = slices.Clone(c.Decisions)
	for i := range c.Decisions {
		c.Decisions[i].AlternativesRejected = slices.Clone(c.Decisions[i].AlternativesRejected)
	}

	// Explicit questions first, derived after: §8's ordering promise, materialized here so the
	// two lists can never interleave however they accumulated.
	oq := make([]string, 0, len(d.userOQ)+len(d.derivedOQ))
	oq = append(oq, d.userOQ...)
	oq = append(oq, d.derivedOQ...)
	c.OpenQuestions = oq

	if c.CurrentWork.BlockedOn != nil {
		b := *c.CurrentWork.BlockedOn
		c.CurrentWork.BlockedOn = &b
	}

	c.Pointers.Files = slices.Clone(c.Pointers.Files)
	c.Pointers.Tools = slices.Clone(c.Pointers.Tools)
	c.Dropped = slices.Clone(c.Dropped)
	c.SketchRefs = maps.Clone(c.SketchRefs)

	c.ensureNonNil()
	return c
}

// sortedEncodedLocked renders the encoded-segment set in ascending id order — the
// encoded_segments field's required order (§8 step 5's "sorted(union)"). Caller holds d.mu.
func (d *Draft) sortedEncodedLocked() []core.SegmentID {
	out := make([]core.SegmentID, 0, len(d.encoded))
	for id := range d.encoded {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// draftFile is the state/draft-<session>.json wrapper of §7. The five leading keys — session,
// seq, parent, frontier, encoded — are flat and spelled exactly as listed here: the commit-8 e2e
// suite reads them by name to assert frontier advancement across idle ticks, so they are a
// cross-file contract, not an implementation detail. Extra keys are additive and ignored there.
//
// work_explicit and user_questions extend the plan's illustrated shape: both are state a resumed
// draft cannot reconstruct from the checkpoint document alone (workExplicit is a flag about how
// CurrentWork came to hold its value; the user/derived split decides open-question ordering), and
// dropping either across a daemon restart would change behaviour the restart is supposed to make
// free.
type draftFile struct {
	Session       core.SessionID     `json:"session"`
	Seq           core.CheckpointSeq `json:"seq"`
	Parent        core.CheckpointSeq `json:"parent"`
	Frontier      core.TurnIndex     `json:"frontier"`
	Encoded       []core.SegmentID   `json:"encoded"`
	Started       core.UnixMilli     `json:"started"`
	WorkExplicit  bool               `json:"work_explicit,omitempty"`
	UserQuestions []string           `json:"user_questions,omitempty"`
	Checkpoint    json.RawMessage    `json:"checkpoint"`
}

// persist writes the draft to its state file UNCONDITIONALLY. It is the entry point for the two
// callers that need the file to exist afterwards whatever the draft's change-tracking says: Begin,
// creating or normalizing state/draft-<session>.json, and the tests. The mutators, already holding
// the lock, use persistLocked, which writes only what has changed.
//
// A sealed draft is still refused: nothing may recreate the file of a draft whose successor now
// owns that path.
func (d *Draft) persist() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dirty = true
	return d.persistLocked()
}

// persistLocked writes state/draft-<session>.json through paths.WriteAtomic (§7): state/ is not
// checkpoints/, so atomic replacement is legal there and IsProtected never fires. Caller holds
// d.mu.
//
// It writes only when something has actually changed, and never once the draft is sealed. Both
// conditions matter operationally:
//
//   - The idle tick calls Begin and Advance for every live session, plus the finished sessions the
//     daemon folds in from the store's recent-session index. Most of those calls change nothing,
//     and an unconditional write would re-marshal and re-fsync each of their drafts on every tick
//     for the rest of the daemon's life.
//   - draftPathFor is keyed on the SESSION, not on the draft, so a sealed draft that persisted
//     after Finalize opened its successor would overwrite the successor's freshly written file
//     with the retired draft's state.
func (d *Draft) persistLocked() error {
	if d.sealed || !d.dirty {
		return nil
	}
	raw, err := Marshal(d.snapshotLocked())
	if err != nil {
		return fmt.Errorf("checkpoint: draft %s: %w", d.session, err)
	}
	b, err := json.Marshal(draftFile{
		Session:       d.session,
		Seq:           d.seq,
		Parent:        d.parent,
		Frontier:      d.frontier,
		Encoded:       d.sortedEncodedLocked(),
		Started:       core.UnixMilli(d.started.UnixMilli()),
		WorkExplicit:  d.workExplicit,
		UserQuestions: d.userOQ,
		Checkpoint:    raw,
	})
	if err != nil {
		return fmt.Errorf("checkpoint: draft %s: %w", d.session, err)
	}
	if err := paths.WriteAtomic(d.path, b, draftPerm); err != nil {
		return fmt.Errorf("checkpoint: draft %s: %w", d.session, err)
	}
	d.dirty = false
	return nil
}

// persistOrLogLocked persists and, because the void mutators have no error channel, reports a
// failure through the package logger instead of losing it. Caller holds d.mu.
func (d *Draft) persistOrLogLocked() {
	if err := d.persistLocked(); err != nil {
		pkgLog().Error("checkpoint: draft persist failed", "session", string(d.session), "err", err.Error())
	}
}

// decodeDraftFile parses one persisted draft wrapper plus its embedded checkpoint document —
// the read half of §7's persistence, factored out of Begin so the round trip is testable on its
// own. The checkpoint passes through Unmarshal, so Migrate runs on it exactly as it would on a
// finalized artifact.
func decodeDraftFile(b []byte) (draftFile, Checkpoint, error) {
	var df draftFile
	if err := json.Unmarshal(b, &df); err != nil {
		return draftFile{}, Checkpoint{}, fmt.Errorf("checkpoint: draft file: %w", err)
	}
	if len(df.Checkpoint) == 0 {
		return draftFile{}, Checkpoint{}, fmt.Errorf("checkpoint: draft file: no checkpoint document")
	}
	cp, err := Unmarshal(df.Checkpoint)
	if err != nil {
		return draftFile{}, Checkpoint{}, err
	}
	return df, cp, nil
}

// splitQuestions separates a restored OpenQuestions list back into the user/derived halves using
// the persisted user list: materialization is user-first concatenation, so everything that is not
// an explicit question is a derived one. Order within each half is preserved.
func splitQuestions(all, user []string) (userOut, derived []string) {
	userSet := make(map[string]bool, len(user))
	for _, q := range user {
		userSet[q] = true
	}
	for _, q := range all {
		if userSet[q] {
			continue
		}
		derived = append(derived, q)
	}
	return slices.Clone(user), derived
}
