package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
)

// The brief: the session's must-keep items, read from its live draft just before a compaction, for a
// caller that hands them to the host's summarizer with an instruction to reproduce them verbatim
// (the daemon's brief route, which serves `qompack mod brief`; docs/adr/0015). It is the checkpoint
// half of that route's sequence: the route settles the session and closes its open segment first,
// arms the writer's sources, and then calls BriefSnapshot, which
//
//   - opens the draft cold when none exists (Begin with coldSources, as PreCompact's cold path
//     does): drafts begin only at a frontier pass or at PreCompact, so a session's first compaction
//     usually finds none, and the PreCompact that follows finds and seals this same draft;
//   - catches it up with one bounded Advance over the closed segments it has not encoded, as
//     PreCompact's catch-up does. No lock of its own is needed: Advance is idempotent per sequence
//     number, refuses a sealed draft, and already runs concurrently from the idle and scheduler
//     frontiers;
//   - runs the seal's refreshes: the negative knowledge as the ledger holds it now, the intent from
//     every prompt the session has made (its open segment's included), and the pins read again, as
//     Finalize reads them (sealInvariants). Catch-up alone runs no Advance when nothing closed, so
//     without them a brief would miss the newest prompts, current work, eliminations and pins;
//   - copies the items under the draft's own lock;
//   - on ErrDraftSealed (a cadence seal, or a PreCompact after the caller gave up, between taking
//     the draft and advancing it) takes the successor the seal opened and retries once, as the
//     frontier advancer does;
//   - starts no new step once its context has ended, so an abandoned brief does not compete with a
//     seal for the draft.
//
// It is not read-only: the catch-up reserves segments for the live draft and persists
// state/draft-<session>.json, and the refreshes persist and emit decision nodes, exactly as an idle
// pass would. A compaction the host then skips therefore leaves its boundary encoded into the live
// draft and no sealed checkpoint, which the next seal encodes as usual.
//
// The items are the Stage A must-keep kinds, each with a stable id: the pinned invariants (inv_
// ids), the user's intent verbatim (fixed ids, BriefIDIntentOriginal and BriefIDIntentEvolutionPrefix
// plus a 1-based position), the active eliminations only (elim_ ids), the decisions (dec_ ids), and
// the current work (BriefIDCurrentWork). A decision that only restates an elimination or a pin the
// brief already lists is left out, so the summarizer is not told the same fact twice. No pointer and
// no tool result is ever an item: the draft holds none of a result's text (ToolPointer.Summary is
// never the result itself), and pointers are what the host's Read rules screen in a rehydration.
// The ids are the stored ones, unchecked: whether an id fits the caller's wire alphabet is the
// caller's question.

// BriefKind classifies one brief item.
type BriefKind string

// The Stage A must-keep kinds.
const (
	// BriefInvariant is a pinned invariant, verbatim, under its inv_ id.
	BriefInvariant BriefKind = "invariant"
	// BriefIntentOriginal is the session's original intent, verbatim (BriefIDIntentOriginal).
	BriefIntentOriginal BriefKind = "intent_original"
	// BriefIntentEvolution is one later restatement of the intent, verbatim, oldest first
	// (BriefIDIntentEvolutionPrefix plus its 1-based position).
	BriefIntentEvolution BriefKind = "intent_evolution"
	// BriefElimination is one active elimination record, under its elim_ id.
	BriefElimination BriefKind = "elimination"
	// BriefDecision is one recorded decision, under its dec_ id.
	BriefDecision BriefKind = "decision"
	// BriefCurrentWork is what is in flight (BriefIDCurrentWork).
	BriefCurrentWork BriefKind = "current_work"
)

// The fixed ids of the items that have no stored id of their own.
const (
	BriefIDIntentOriginal        = "intent:original"
	BriefIDIntentEvolutionPrefix = "intent:evolution:"
	BriefIDCurrentWork           = "work:current"
)

// BriefItem is one must-keep item: its kind, its stable id and its text.
type BriefItem struct {
	Kind BriefKind
	ID   string
	Text string
}

// Brief is what one BriefSnapshot read: the items, in order (invariants, the original intent, its
// evolution oldest first, the active eliminations, the decisions as the draft ranks them, the current
// work), and how the read went.
type Brief struct {
	Items []BriefItem
	// NewDraft is true when the snapshot opened the session's draft cold.
	NewDraft bool
	// Retried is true when the draft was sealed under the snapshot and it retried on the successor.
	Retried bool
	// PinsReread is true when the pins were read again for this brief; false when the read failed
	// or the context ended first, and the invariants are then the ones the draft holds.
	PinsReread bool
}

// The brief's instruments.
const (
	metricBrief            = "checkpoint.brief"
	metricBriefCold        = "checkpoint.brief.cold"
	metricBriefSealedRetry = "checkpoint.brief.sealed_retry"
	metricBriefPinsUnread  = "checkpoint.brief.pins_unread"
)

// BriefSnapshot reads session s's must-keep items from its live draft, opening, catching up and
// refreshing the draft first (see above). It reports ctx's error when ctx has ended before the
// snapshot could take a draft, and Begin's error when no draft can be opened.
func (w *FileWriter) BriefSnapshot(ctx context.Context, s core.SessionID) (b Brief, err error) {
	timed := obs.Timed(w.m.Hist(metricBrief), func() error {
		b, err = w.briefSnapshot(ctx, s)
		return err
	})
	if err == nil {
		err = timed
	}
	return b, err
}

func (w *FileWriter) briefSnapshot(ctx context.Context, s core.SessionID) (Brief, error) {
	var b Brief
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return Brief{}, err
		}
		d := w.liveDraft(s)
		if d == nil {
			var err error
			if d, err = w.Begin(ctx, s, 0, w.coldSources()); err != nil {
				return Brief{}, err
			}
			b.NewDraft = true
			w.m.Counter(metricBriefCold).Add(1)
		}
		items, reread, err := w.briefFrom(ctx, d, s)
		if errors.Is(err, ErrDraftSealed) && attempt == 0 {
			b.Retried = true
			w.m.Counter(metricBriefSealedRetry).Add(1)
			continue
		}
		if err != nil {
			return Brief{}, err
		}
		b.Items, b.PinsReread = items, reread
		return b, nil
	}
}

// briefFrom catches d up, runs the seal's refreshes and copies the items. Each step starts only while
// ctx lasts; the copy, which does no I/O, always runs. Only ErrDraftSealed is returned: every other
// failure is logged and the brief is read from what the draft has, as a seal is.
func (w *FileWriter) briefFrom(ctx context.Context, d *Draft, s core.SessionID) ([]BriefItem, bool, error) {
	if ctx.Err() == nil {
		if err := w.catchUpForBrief(ctx, d, s); err != nil {
			return nil, false, err
		}
	}
	if ctx.Err() == nil {
		if err := d.refreshNegativeKnowledge(ctx); err != nil {
			w.log.Warn("checkpoint: brief could not re-read the elimination ledger; briefing the draft's copy",
				"session", string(s), "err", err.Error())
		}
	}
	if ctx.Err() == nil {
		d.RefreshIntent(ctx)
	}
	var pinned []Invariant
	reread := false
	if ctx.Err() == nil {
		got, err := d.sources().Pins.All(ctx)
		if err == nil {
			pinned, reread = got, true
		} else {
			w.m.Counter(metricBriefPinsUnread).Add(1)
			w.log.Warn("checkpoint: brief could not re-read the pins; briefing the draft's invariants",
				"session", string(s), "err", err.Error())
		}
	}
	return d.briefItems(pinned, reread), reread, nil
}

// catchUpForBrief encodes s's closed, unencoded segments into d, oldest first, in one Advance bounded
// as PreCompact's is (maxColdEncodeSegments, and ctx). It returns ErrDraftSealed, for the caller's
// retry, and logs and swallows everything else.
func (w *FileWriter) catchUpForBrief(ctx context.Context, d *Draft, s core.SessionID) error {
	ids, err := closedUnencoded(ctx, d, s)
	if err != nil {
		w.log.Warn("checkpoint: brief could not list unencoded segments", "session", string(s), "err", err.Error())
		return nil
	}
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > maxColdEncodeSegments {
		ids = ids[:maxColdEncodeSegments]
	}
	if _, err := w.Advance(ctx, d, ids); err != nil {
		if errors.Is(err, ErrDraftSealed) {
			return err
		}
		w.log.Warn("checkpoint: brief could not encode its catch-up batch",
			"session", string(s), "segments", len(ids), "err", err.Error())
	}
	return nil
}

// closedUnencoded lists s's closed segments not yet encoded, in the segment log's own ascending
// order: the open segment is in-flight work, which Advance would skip anyway.
func closedUnencoded(ctx context.Context, d *Draft, s core.SessionID) ([]core.SegmentID, error) {
	segs, err := d.sources().Segments.Unencoded(ctx, s)
	if err != nil {
		return nil, err
	}
	var ids []core.SegmentID
	for _, seg := range segs {
		if seg.Closed {
			ids = append(ids, seg.ID)
		}
	}
	return ids, nil
}

// briefItems copies the must-keep items out of d under its lock. pinned replaces the draft's own
// invariants when reread is true.
func (d *Draft) briefItems(pinned []Invariant, reread bool) []BriefItem {
	d.mu.Lock()
	defer d.mu.Unlock()
	invs := d.cp.Invariants
	if reread {
		invs = pinned
	}
	var out []BriefItem
	restated := map[core.DecisionID]bool{}
	for _, inv := range invs {
		if strings.TrimSpace(inv.Text) == "" {
			continue
		}
		out = append(out, BriefItem{Kind: BriefInvariant, ID: inv.ID, Text: inv.Text})
		if dec, ok := pinDecision(inv, 0); ok {
			restated[dec.ID] = true
		}
	}
	if o := d.cp.UserIntent.Original; strings.TrimSpace(o) != "" {
		out = append(out, BriefItem{Kind: BriefIntentOriginal, ID: BriefIDIntentOriginal, Text: o})
	}
	for i, e := range d.cp.UserIntent.Evolution {
		if strings.TrimSpace(e) == "" {
			continue
		}
		out = append(out, BriefItem{
			Kind: BriefIntentEvolution, ID: BriefIDIntentEvolutionPrefix + strconv.Itoa(i+1), Text: e,
		})
	}
	for _, r := range d.cp.Eliminated {
		// The decision an elimination mints restates it, whether or not the record is still active:
		// an active one is listed below, and a stale one is not to be kept at all.
		if dec, ok := eliminationDecision(r, 0); ok {
			restated[dec.ID] = true
		}
		if r.Status != negknow.StatusActive {
			continue
		}
		out = append(out, BriefItem{Kind: BriefElimination, ID: r.ID, Text: eliminationText(r)})
	}
	for _, dec := range d.cp.Decisions {
		if restated[dec.ID] {
			continue
		}
		out = append(out, BriefItem{Kind: BriefDecision, ID: string(dec.ID), Text: decisionText(dec)})
	}
	if t := currentWorkText(d.cp.CurrentWork); t != "" {
		out = append(out, BriefItem{Kind: BriefCurrentWork, ID: BriefIDCurrentWork, Text: t})
	}
	return out
}

// eliminationText renders one elimination record's three free-text fields, verbatim.
func eliminationText(r negknow.Record) string {
	var b strings.Builder
	b.WriteString("ruled out")
	if r.Approach != "" {
		fmt.Fprintf(&b, ": %s", r.Approach)
	}
	if r.Target != "" {
		fmt.Fprintf(&b, " (target: %s)", r.Target)
	}
	if r.Reason != "" {
		fmt.Fprintf(&b, "; reason: %s", r.Reason)
	}
	return b.String()
}

// decisionText renders one decision's what and why, verbatim.
func decisionText(dec Decision) string {
	if dec.Why == "" {
		return dec.What
	}
	return dec.What + "; why: " + dec.Why
}

// currentWorkText renders the current work's set fields, or "" when none is set.
func currentWorkText(w CurrentWork) string {
	var parts []string
	if strings.TrimSpace(w.Goal) != "" {
		parts = append(parts, "goal: "+w.Goal)
	}
	if strings.TrimSpace(w.NextStep) != "" {
		parts = append(parts, "next step: "+w.NextStep)
	}
	if w.BlockedOn != nil && strings.TrimSpace(*w.BlockedOn) != "" {
		parts = append(parts, "blocked on: "+*w.BlockedOn)
	}
	return strings.Join(parts, "; ")
}
