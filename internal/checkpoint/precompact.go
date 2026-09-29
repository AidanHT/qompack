package checkpoint

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// Compactor is the PreCompact seam the daemon's composition root binds. It is additive to
// 00-ARCHITECTURE.md §5.14 rather than a modification of it: §5.14's Writer stays exactly as
// declared, and this interface carries the one operation the hook needs.
//
// It returns plain data, never a hookio.Output, because internal/checkpoint may not import
// hookio (§3.2). The daemon converts.
type Compactor interface {
	PreCompact(ctx context.Context, in PreCompactInput) (PreCompactResult, error)
}

// *FileWriter is the Compactor the daemon binds. The assertion is here rather than in the daemon
// so that breaking it is a compile error in the package that owns the method, not in the package
// that merely consumes it.
var _ Compactor = (*FileWriter)(nil)

// PreCompactInput is everything the hook knows that this package cannot discover for itself.
type PreCompactInput struct {
	Session core.SessionID
	// Trigger is the host's compaction trigger, "manual" or "auto".
	Trigger string
	Now     time.Time
	// Deadline is when the caller must have an answer. PreCompact keeps its own, earlier deadline
	// inside it — see finalizeGuard.
	Deadline time.Time
	// HookTimeout is the plugin manifest's declared PreCompact timeout, supplied by the daemon and
	// recorded as timeout_ms. It is passed in rather than read here so that no literal timeout
	// appears in this package and the nomagic pass stays green.
	HookTimeout time.Duration
	Budget      core.Tokens
	Cfg         config.CheckpointCfg
	Cache       CacheInfo
	ExtraDrops  []DropEntry
	// CurrentWork overrides whatever the draft derived; nil means keep the derived value.
	CurrentWork *CurrentWork
	// OpenQuestions are appended to the draft's derived list; may be nil.
	OpenQuestions []string
}

// PreCompactResult is the plain-data result of one PreCompact. Since C1.18 the daemon's checkpoint
// seam discards it and answers the host with the empty object: the seal on disk is the whole of
// what PreCompact is for, and Instructions (the rendered focus text) reaches no hop — it survives
// only as state/precompact.json's instructions_bytes (see FocusInstructions).
type PreCompactResult struct {
	Ref          Ref
	Instructions string
	Drops        []DropEntry
	// Truncated is true iff at least one drop in the finalized checkpoint has a Kind from the
	// truncation table of §10. Pointer-validation drops are NOT truncation and do not set it: a
	// pointer removed because its file was deleted says nothing about the budget.
	Truncated bool
	Wall      time.Duration
	NewDraft  bool
}

// The PreCompact deadline arithmetic. The host allows 20 s, the shipped thin client waits 15 s,
// and the daemon's own deadline has to be strictly inside the client's wait or a slow compaction
// — exactly the case the deadline exists to survive — produces a client-side timeout instead of a
// checkpoint.
const (
	// finalizeGuard leaves the caller room to serialize and reply after we return.
	finalizeGuard = 400 * time.Millisecond
	// maxPreCompact keeps us well inside budget B-E (2 s p99, Qompack.md §11.3 L4).
	maxPreCompact = 1500 * time.Millisecond
	// minFinalizeWindow is the floor. A host that hands us a deadline already inside finalizeGuard
	// — or in the past — must still get a written checkpoint, because §12's PreCompact-timeout row
	// says finalize as-is, not give up. With this floor, Finalize always has at least this much
	// non-cancelled context to marshal and write the tier-1 document.
	minFinalizeWindow = 250 * time.Millisecond
	// coldEncodeWindow bounds the cold path's catch-up encoding, so a compaction that fires before
	// any idle window still returns inside B-E.
	coldEncodeWindow = 800 * time.Millisecond
)

// maxColdEncodeSegments bounds how many segments the cold path hands to its single Advance call.
//
// The bound is on the SLICE, not on a loop, because the cost of catching up is one full-graph scan
// per Advance rather than per segment: Advance is batch-shaped and documents "Per batch there is
// exactly ONE full-graph scan". A per-segment loop paid O(unencoded segments x |V|) and could only
// check its time budget BETWEEN calls, so one slow scan overshot the window it was meant to
// enforce. The context deadline preCompact installs is the hard stop; this is the shape guard that
// keeps a session with thousands of unencoded segments from marshalling a draft that large into
// state/ inside the hook.
const maxColdEncodeSegments = 64

// truncationKinds is the drop-kind set that means "the budget cut this", as opposed to "this
// pointer no longer resolves". PreCompactResult.Truncated is membership in this set.
var truncationKinds = map[string]bool{
	dropNarrative:      true,
	dropToolPointer:    true,
	dropFilePointer:    true,
	dropOpenQuestion:   true,
	dropAlternatives:   true,
	dropDecision:       true,
	dropNextStep:       true,
	dropBudgetExceeded: true,
}

// PreCompact is the finalize-only hook path. Everything expensive has already happened during idle
// windows (O5), so this is a seal-and-describe operation.
func (w *FileWriter) PreCompact(ctx context.Context, in PreCompactInput) (res PreCompactResult, err error) {
	timed := obs.Timed(w.m.Hist("checkpoint.precompact"), func() error {
		res, err = w.preCompact(ctx, in)
		return err
	})
	if err == nil {
		err = timed
	}
	return res, err
}

func (w *FileWriter) preCompact(ctx context.Context, in PreCompactInput) (PreCompactResult, error) {
	start := w.clk.Now()

	// The budget is a DURATION, not an instant, and that is not a stylistic choice.
	//
	// in.Now and in.Deadline come from the caller and are consistent with each other, whatever
	// clock produced them; w.clk may be a test's fake clock; and context deadlines are always real
	// wall-clock time. Mixing the three -- deriving an absolute instant from w.clk and handing it
	// to context.WithDeadline -- produces a context that is already expired whenever the injected
	// clock does not happen to agree with the wall. Finalize survives that, by design, because
	// §12's PreCompact-timeout row says finalize as-is. The post-write verification re-read below
	// does not: it would fail silently on every call, and PreCompactResult.Drops would quietly
	// report the pre-truncation draft instead of the artifact actually written.
	//
	// Subtracting two caller-supplied instants and applying the result with WithTimeout keeps every
	// comparison inside one time base and makes the budget mean what it says: this much wall-clock
	// time, starting now.
	//
	// The subtraction is guarded rather than clamped afterwards, because it SATURATES. A zero
	// time.Time is the furthest-past instant there is, so a caller that leaves Deadline unset makes
	// Deadline.Sub(Now) saturate at the minimum duration; subtracting finalizeGuard from that wraps
	// it POSITIVE, and the cap below then hands the most lenient budget in the table to the one
	// caller that supplied no deadline at all. Neither instant means anything on its own, so an
	// absent one takes the floor — the same answer a deadline already in the past gets.
	budget := minFinalizeWindow
	if !in.Deadline.IsZero() && !in.Now.IsZero() {
		budget = in.Deadline.Sub(in.Now) - finalizeGuard
	}
	if budget > maxPreCompact {
		budget = maxPreCompact
	}
	// The floor is what makes §12's "finalize as-is" reachable: a host that hands us a deadline
	// already inside finalizeGuard -- or in the past -- must still get a written checkpoint, so
	// Finalize always has at least this much non-cancelled context to marshal and write tier 1.
	if budget < minFinalizeWindow {
		budget = minFinalizeWindow
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	d, fresh, err := w.draftForPreCompact(ctx, in, start)
	if err != nil {
		return PreCompactResult{NewDraft: fresh}, err
	}
	// Negative knowledge is sealed as the ledger holds it NOW, not as it stood when this draft
	// last read it (Draft.refreshNegativeKnowledge). A failure seals the draft's own copy.
	if rerr := d.refreshNegativeKnowledge(ctx); rerr != nil {
		w.log.Warn("checkpoint: PreCompact could not re-read the elimination ledger; sealing the draft's copy",
			"session", string(in.Session), "err", rerr.Error())
	}

	// The checkpoint this compaction seals carries every prompt the session has made, the ones in
	// its still-open segment included: a live draft is otherwise only as fresh as its last idle
	// Advance, and no segment encoding ever reads the open segment (F-UAT05-1, intent.go).
	d.RefreshIntent(ctx)

	d.SetCache(in.Cache)
	d.AddDrops(in.ExtraDrops...)
	if in.CurrentWork != nil {
		d.SetCurrentWork(*in.CurrentWork)
	}
	for _, q := range in.OpenQuestions {
		d.AddOpenQuestion(q)
	}

	ref, err := w.Finalize(ctx, d, in.Budget)
	if err != nil {
		w.log.Loud("checkpoint: PreCompact finalize failed",
			"session", string(in.Session), "trigger", in.Trigger, "err", err.Error())
		return PreCompactResult{NewDraft: fresh, Wall: w.clk.Since(start)}, err
	}

	// Deliberately a re-read rather than d.snapshot(): it re-hashes the file against the manifest
	// line just appended, so a corrupt or short write surfaces here instead of at the next session
	// start, when the session it would have rescued is already gone.
	cp, _, rerr := w.reader.Get(ctx, ref.Seq)
	if rerr != nil {
		w.log.Loud("checkpoint: could not re-read the checkpoint just written",
			"seq", int(ref.Seq), "err", rerr.Error())
		cp = d.snapshot()
	}

	instr := FocusInstructions(cp, ref, FocusOptions{
		IncrementalSpan: in.Cfg.IncrementalSpanInstruction,
		Frontier:        ref.Frontier,
		CheckpointPath:  w.relPath(ref.Path),
		// Always true on this path: a summarizer asked to compress a span it has pointers for
		// should never be tempted to paste the file back in (G3.4).
		ForbidSnippets: true,
	})

	wall := w.clk.Since(start)
	w.writePreCompactDebug(ref, in, instr, wall)

	return PreCompactResult{
		Ref:          ref,
		Instructions: instr,
		Drops:        cp.Dropped,
		Truncated:    hasTruncationDrop(cp.Dropped),
		Wall:         wall,
		NewDraft:     fresh,
	}, nil
}

// draftForPreCompact returns the live draft for the session, opening one when compaction fired
// before any idle window did, and catches it up with every closed segment not yet encoded.
//
// The catch-up runs on BOTH paths. It used to run on the cold one only, on the ground that idle
// windows had already encoded everything by the time a warm draft was sealed. That ground is gone:
// the daemon now closes the compacting session's open segment at the compaction itself (the
// scheduler tap's PreCompact, F-UAT03-1), and a warm draft — every compaction after a session's
// first finds the successor its previous seal opened — would otherwise be sealed without the very
// span the host is compacting, leaving it to an idle pass to fold into the checkpoint after. An
// already-caught-up draft pays one segment-log read.
//
// The catch-up is ONE Advance call over a trimmed batch, oldest segment first. Both halves matter:
//
//   - One call, because each Advance does a full-graph scan (nodesInRange → dag.NodesAfter(0),
//     which copies and sorts every node in the graph under the index lock). Calling it once per
//     segment made the hook path O(unencoded segments x |V|), and the time check between calls
//     could not stop a scan already in flight.
//   - Oldest first, because the frontier only advances over a CONTIGUOUS encoded prefix (§8.5
//     O1/O5, store.SegmentLog.Frontier). Encoding newest-first — which is what this did — left a
//     gap behind every segment it encoded, so the frontier stayed where it was and the O1 span
//     paragraph bought nothing. store.SegmentLog.Unencoded already returns ascending StartTurn
//     order, so the fix is to stop re-sorting it.
func (w *FileWriter) draftForPreCompact(ctx context.Context, in PreCompactInput, start time.Time) (*Draft, bool, error) {
	d, fresh := w.liveDraft(in.Session), false
	if d == nil {
		w.m.Counter("checkpoint.cold_precompact").Add(1)
		var err error
		if d, err = w.Begin(ctx, in.Session, 0, w.coldSources()); err != nil {
			return nil, true, err
		}
		fresh = true
	}
	w.catchUpForPreCompact(ctx, d, in.Session, start)
	return d, fresh, nil
}

// catchUpForPreCompact encodes the session's closed, unencoded segments into d, oldest first, in one
// bounded Advance. Every failure is logged and swallowed: a catch-up that cannot run is not a reason
// to lose the checkpoint, which is sealed with what the draft already has.
func (w *FileWriter) catchUpForPreCompact(ctx context.Context, d *Draft, s core.SessionID, start time.Time) {
	segs, err := d.sources().Segments.Unencoded(ctx, s)
	if err != nil {
		w.log.Warn("checkpoint: PreCompact could not list unencoded segments", "err", err.Error())
		return
	}
	closed := segs[:0:0]
	for _, seg := range segs {
		// The open segment is in-flight work, and Advance would skip it anyway; leaving it out keeps
		// the common already-caught-up case from paying for an Advance that encodes nothing.
		if seg.Closed {
			closed = append(closed, seg)
		}
	}
	if len(closed) == 0 {
		return
	}
	if w.clk.Since(start) > coldEncodeWindow {
		// Begin alone has already spent the catch-up window. Seal what it seeded rather than
		// starting an encode we know will overrun budget B-E.
		w.log.Warn("checkpoint: PreCompact skipped catch-up encoding; the window was already spent",
			"unencoded", len(closed))
		return
	}
	if len(closed) > maxColdEncodeSegments {
		closed = closed[:maxColdEncodeSegments]
	}
	ids := make([]core.SegmentID, len(closed))
	for i, seg := range closed {
		ids[i] = seg.ID
	}
	if _, err := w.Advance(ctx, d, ids); err != nil {
		w.log.Warn("checkpoint: PreCompact could not encode its catch-up batch",
			"segments", len(ids), "err", err.Error())
	}
}

// preCompactDebug is .qompack/state/precompact.json: a Qompack-internal debug artifact. Nothing in
// internal/contract reads it and no assertion depends on it. It exists so /qompack:status and a
// human reading state/ can tell at a glance which build sealed the checkpoint, and it is the one
// place the rendered focus text still shows (instructions_bytes, span_instruction): since C1.18 the
// text itself reaches no hop.
type preCompactDebug struct {
	Seq               core.CheckpointSeq `json:"seq"`
	Sentinel          string             `json:"sentinel"`
	EmittedAt         core.UnixMilli     `json:"emitted_at"`
	WallMs            int64              `json:"wall_ms"`
	TimeoutMs         int64              `json:"timeout_ms"`
	InstructionsBytes int                `json:"instructions_bytes"`
	Frontier          core.TurnIndex     `json:"frontier"`
	SpanInstruction   bool               `json:"span_instruction"`
}

func (w *FileWriter) writePreCompactDebug(ref Ref, in PreCompactInput, instr string, wall time.Duration) {
	b, err := json.Marshal(preCompactDebug{
		Seq:               ref.Seq,
		Sentinel:          SentinelPhrase,
		EmittedAt:         core.NowMilli(w.clk),
		WallMs:            wall.Milliseconds(),
		TimeoutMs:         in.HookTimeout.Milliseconds(),
		InstructionsBytes: len(instr),
		Frontier:          ref.Frontier,
		SpanInstruction:   in.Cfg.IncrementalSpanInstruction && ref.Frontier > 0,
	})
	if err != nil {
		return
	}
	p := filepath.Join(w.l.State, "precompact.json")
	if err := paths.WriteAtomic(p, b, 0o600); err != nil {
		w.log.Warn("checkpoint: could not write the precompact debug artifact", "err", err.Error())
	}
}

// hasTruncationDrop reports whether the budget cut anything, as opposed to a pointer failing to
// resolve.
func hasTruncationDrop(drops []DropEntry) bool {
	for _, d := range drops {
		if truncationKinds[d.Kind] {
			return true
		}
	}
	return false
}
