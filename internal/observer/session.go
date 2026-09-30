package observer

import (
	"cmp"
	"context"
	"errors"
	"math"
	"path/filepath"
	"slices"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
)

// The SessionStart/SessionEnd half of L0, under §5.21's split-ownership table: SP-08 owns the
// startup/resume branch and the whole of OnSessionEnd; SP-11's compact/clear branches are reached
// through the Rehydrator seam and are a delegation, never a call into a wave-3 package.
//
// One obligation on the startup/resume branch lives OUTSIDE this file: SP-09's RefreshStaleness.
// Its only wave-2 production caller is the wrapped SessionStart seam in
// internal/daemon/observer_ops.go, around this file's OnSessionStart — never in here, because the
// §3.2 exit criterion forbids this package importing negknow at all. It is recorded here so a
// reader of this branch does not conclude the obligation was dropped.

// The two SessionStart sources that belong to SP-11's rehydrator. Everything else — "startup",
// "resume", "", or a source this build has never heard of — is startup bookkeeping (§12.3: an
// unknown source is a host change, not a crash).
const (
	sourceCompact = "compact"
	sourceClear   = "clear"
)

// The stage names this file's soft failures report under, as observer.err.<stage>.
const (
	stageSegFrontier = "segment.frontier"
	stageSegOpen     = "segment.open"
	stageSegClose    = "segment.close"
	stageSegFollow   = "segment.follow"
	stageStoreFlush  = "store.flush"
	stageSketchSave  = "sketch.save"
	stageGC          = "gc"
)

// The two sketch files SessionEnd persists, under paths.Of(root).Sketches. tried.bloom is
// deliberately NOT here and never will be: §3.3 reserves that file for negknow.RebuildBloom.
const (
	touchSketchName   = "touch.cms"
	exploreSketchName = "explore.hll"
)

// The §6.6 feature keys the closing segment's summary is recorded under.
const (
	featPathJaccard     = "path_jaccard"
	featToolShift       = "tool_shift"
	featLexicalCohesion = "lexical_cohesion"
	featGapSeconds      = "gap_seconds"
	featTodoTransition  = "todo_transition"

	// featSegmentTokens is NOT a BOCD feature: it is the pseudo-feature store.SegmentLog.Close
	// reads Segment.Tokens from (internal/store/segments.go segTokensFeature, which is unexported;
	// the scheduler's own close spells it the same way).
	featSegmentTokens = "tokens"
)

// segmentTokens is what st's current segment has accumulated: the prefix position now, less the
// position the segment opened at. It is never negative.
func segmentTokens(st *sessionState) int {
	if n := st.PrefixTokens - st.SegStartPos; n > 0 {
		return n
	}
	return 0
}

// OnSessionStart branches on the SessionStart source: startup/resume load the store's bookkeeping,
// compact and clear delegate to the Rehydrator seam.
func (o *observer) OnSessionStart(ctx context.Context, e Event) (Output, error) {
	var out Output
	err := o.timed(histSessionStart, func() error {
		var err error
		out, err = o.onSessionStart(ctx, e)
		return err
	})
	return out, err
}

func (o *observer) onSessionStart(ctx context.Context, e Event) (Output, error) {
	// 0. Nothing before the ctx check; the clock is read exactly once (decision 11).
	if err := ctx.Err(); err != nil {
		return hookio.Empty(), err
	}
	now := o.now()

	// 1-2. loadState is idempotent (sync.Once, inside session), then the per-session lock is held
	//      for the remainder of the method (decision 9).
	st := o.session(e.SessionID)
	st.mu.Lock()
	defer st.mu.Unlock()

	// 3. Frontier adoption: on resume the segment frontier is the durable floor for the turn
	//    index, and adopting the larger of the two never rewinds a state-file resume.
	frontier, err := o.opt.Store.Segments().Frontier(ctx, e.SessionID)
	if err != nil {
		o.soft(stageSegFrontier, err)
	} else if frontier > st.Turn {
		st.Turn = frontier
	}

	// 4. The segment is ensured BEFORE the source switch, so even a compact/clear delegation runs
	//    with the enrolment target in place. A roll the scheduler made since the last event (the
	//    compaction close in front of a SessionStart(compact) is the usual one) is followed first,
	//    so the rolled segment gets its node and the successor its chain edge; ensureSegment then
	//    finds the successor already held.
	o.followSegmentRoll(ctx, st, e.SessionID, now)
	o.ensureSegment(ctx, st, e.SessionID, now)

	// 5. The source switch (§7.3). compact/clear are SP-11's branches through the seam; the
	//    default arm is deliberately everything else, unknown sources included.
	switch e.Source {
	case sourceCompact:
		if o.opt.Rehydrate != nil {
			return o.opt.Rehydrate.OnCompact(ctx, e)
		}
		o.opt.Log.Info("observer: rehydrator not built yet; startup bookkeeping only", "source", e.Source)
		return hookio.Empty(), nil
	case sourceClear:
		if o.opt.Rehydrate != nil {
			return o.opt.Rehydrate.OnClear(ctx, e)
		}
		return hookio.Empty(), nil
	default: // "startup", "resume", "", or anything the host invents later
		o.opt.Log.Info("observer: session registered", "session", e.SessionID, "source", e.Source,
			"turn", st.Turn, "segment", st.Segment)
		return hookio.Empty(), nil
	}
}

// ensureSegment adopts the session's open segment or opens a fresh one, maintaining the three
// Seg* fields dag.SegmentSpec needs at close time and the SegStartPos that makes a boundary a
// position CrossingEdges can be asked about.
//
// When the open segment is the one st already holds, nothing moves: its start turn and position
// are where it opened, and everything enrolled since is in it. A SessionStart that no roll
// preceded (a compaction the scheduler left open because it had observed nothing, the frontier's
// advanceOnSegmentClose turned off, a resume) used to reset SegStartPos to the current position,
// which gave the segment node a StartPos past members already enrolled in it and a Tokens count
// that left them out. A segment adopted from the log is started at the current position: on the
// resume path PrefixTokens has just been rehydrated from state/observer.json, so that is the real
// prefix offset rather than zero.
//
// An Open failure leaves st.Segment = 0, which every call site tolerates (enrol emits nothing;
// OnSessionEnd's step 1 skips), and keeps st.PrevSegment, so the next segment still chains back
// to the last one this session had. Every roll after a session's first Open is SP-12's; the only
// close SP-08 performs is the session-end close below.
func (o *observer) ensureSegment(ctx context.Context, st *sessionState, s core.SessionID, now core.UnixMilli) {
	cur, err := o.opt.Store.Segments().Current(ctx, s)
	switch {
	case err == nil && !cur.Closed && cur.ID == st.Segment:
		st.SegStartTurn = cur.StartTurn
		return
	case err == nil && !cur.Closed:
		st.Segment = cur.ID
		st.SegStartTurn = cur.StartTurn
	default:
		if st.Segment != 0 {
			st.PrevSegment = st.Segment
		}
		id, openErr := o.opt.Store.Segments().Open(ctx, store.Segment{
			Session: s, StartTurn: st.Turn, StartTS: now,
			Features: map[string]float64{}, Closed: false,
		})
		if openErr != nil {
			o.soft(stageSegOpen, openErr)
			st.Segment = 0
		} else {
			st.Segment, st.SegStartTurn = id, st.Turn
		}
	}
	st.SegStartPos = st.PrefixTokens
}

// followSegmentRoll catches st up with every roll of s's segment that the observer did not make.
//
// The scheduler (internal/daemon, scheduler_frontier.go closeSessionSegmentLocked) closes a
// session's segment and opens its successor on a changepoint, a todo completion, a passing test, a
// git commit and a compaction, and the host sends no SessionStart after any of them. Adopting a
// segment only in OnSessionStart left st.Segment naming the closed one: DAG members kept enrolling
// against it, it never got its segment node, and SessionEnd closed it again (a soft ErrAppendOnly)
// while the successor stayed open and was never encoded.
//
// Every entry point calls this under st.mu BEFORE it advances the prefix position or enrols
// anything, so st.PrefixTokens here is the position the roll was made at. That rests on two facts
// about the daemon: one session's events reach the observer one at a time (its dispatch lane runs
// the observer and then the scheduler's tap for an event before the next event starts), and every
// roll the tap makes follows the event it was made for. So the rolled segment's node spans exactly
// what was enrolled in it, StartPos to the boundary, and the successor starts at the boundary.
//
// Membership therefore follows capture order: a node belongs to the segment that was open when it
// was captured. The log's turn ranges can disagree with that in both directions. A roll in the
// middle of a turn (a todo, test or commit signal, or a changepoint, on one of turn t's tool uses)
// closes the segment at t and opens the successor at t+1; the turn's later tool uses are enrolled
// in the successor, which is also where the scheduler's open-segment account counts their tokens.
// A close at the highest tool-use turn (a compaction, or a changepoint declared at a Stop) can end
// the segment before a prompt already enrolled in it, and the prompt stays there. Enrolment cannot
// be moved after the fact, so capture order is the one rule every roll satisfies. A reader that
// partitions by turn, as the checkpoint's segment encoder does, sees the log's ranges instead.
//
// Two rolls may land between two events this observer sees (a daemon resuming from an observer
// state persisted before its predecessor's last rolls). Every segment rolled open and closed again
// in between gets its node too, empty and at the boundary, so the chain runs through it. When the
// scheduler closed the segment but opened no successor (its Open failed), st holds no segment
// until the next SessionStart opens one, exactly as after an Open failure of the observer's own.
//
// The common case is one Get of the held segment, which is still open. A held segment the log has
// no record of cannot have been rolled, and SessionEnd's close is what reports it.
func (o *observer) followSegmentRoll(ctx context.Context, st *sessionState, s core.SessionID, now core.UnixMilli) {
	if st.Segment == 0 {
		return
	}
	segs := o.opt.Store.Segments()
	if segs == nil {
		return // a store with no segment log has no rolls to follow
	}
	held, err := segs.Get(ctx, st.Segment)
	switch {
	case errors.Is(err, core.ErrNotFound):
		return
	case err != nil:
		o.soft(stageSegFollow, err)
		return
	case !held.Closed:
		return
	}
	// Every successor starts after the held segment ends, so a range from its end turn holds them
	// all: a closed one ends at or after its start, and an open one matches any upper bound past
	// its start.
	all, err := segs.Range(ctx, held.EndTurn, math.MaxInt)
	if err != nil {
		o.soft(stageSegFollow, err)
		return
	}
	var later []store.Segment
	for _, seg := range all {
		if seg.Session == s && seg.ID > held.ID {
			later = append(later, seg)
		}
	}
	slices.SortFunc(later, func(a, b store.Segment) int { return cmp.Compare(a.ID, b.ID) })
	var open *store.Segment
	for i := range later {
		if !later[i].Closed {
			open = &later[i] // the highest id wins, as SegmentLog.Current's does
		}
	}

	boundary := st.PrefixTokens
	o.soft(stageDAG, dag.BuildSegment(o.opt.Graph, dag.SegmentSpec{
		ID: held.ID, PrevID: st.PrevSegment,
		StartTurn: st.SegStartTurn, EndTurn: held.EndTurn, TS: segmentClosedAt(held, now),
		StartPos: st.SegStartPos,
		Tokens:   core.Tokens(segmentTokens(st)),
		Members:  nil, // enrolled incrementally (graph.go enrol)
	}))
	prev := held.ID
	for _, seg := range later {
		if !seg.Closed || (open != nil && seg.ID > open.ID) {
			continue
		}
		o.soft(stageDAG, dag.BuildSegment(o.opt.Graph, dag.SegmentSpec{
			ID: seg.ID, PrevID: prev,
			StartTurn: seg.StartTurn, EndTurn: seg.EndTurn, TS: segmentClosedAt(seg, now),
			StartPos: boundary, Tokens: 0,
		}))
		prev = seg.ID
	}

	st.PrevSegment, st.SegStartPos = prev, boundary
	if open != nil {
		st.Segment, st.SegStartTurn = open.ID, open.StartTurn
	} else {
		st.Segment = 0
	}
	o.count(counterSegmentFollowed)
}

// segmentClosedAt is when the log says seg closed, or now for a log that did not record it.
func segmentClosedAt(seg store.Segment, now core.UnixMilli) core.UnixMilli {
	if seg.EndTS > 0 {
		return seg.EndTS
	}
	return now
}

// OnSessionEnd is §7.3's "Flush, compact the store, write the session index", in the exact order
// the plan pins: segment node + close, graph flush, store flush, sketch persistence, state
// persistence, GC, then the session's removal from the in-memory map.
func (o *observer) OnSessionEnd(ctx context.Context, e Event) (Output, error) {
	var out Output
	err := o.timed(histSessionEnd, func() error {
		var err error
		out, err = o.onSessionEnd(ctx, e)
		return err
	})
	return out, err
}

func (o *observer) onSessionEnd(ctx context.Context, e Event) (Output, error) {
	if err := ctx.Err(); err != nil {
		return hookio.Empty(), err
	}
	now := o.now()
	st := o.session(e.SessionID)

	// The session lock is taken WITHOUT a defer: it is released explicitly after step 4, before
	// persistState runs. The plan sketches the release at step 7, but persistState snapshots
	// EVERY session under its own lock (state.go's snapshotSession) after taking o.mu — so
	// holding this session's lock into step 5 would self-deadlock on a non-reentrant mutex, and
	// would also invert decision 9's lock order (never o.mu while a sessionState.mu is held).
	// Steps 5-7 read nothing from st, so the earlier release changes no observable ordering.
	st.mu.Lock()

	// 0. A roll the scheduler made since the last event is followed first, so the close below is
	//    the session's open segment and not one the scheduler already closed.
	o.followSegmentRoll(ctx, st, e.SessionID, now)

	// 1. The segment NODE and the previous -> current chain edge, then the session-index close.
	//    Members is nil because graph.go's enrol emitted one member -> segment edge per node as it
	//    arrived (legal under D-6). The chain edge is what makes the boundary visible to
	//    CrossingEdges: a boundary whose only crossing edge is the chain link is exactly the cheap
	//    cut the scheduler hunts for. st.PrevSegment is 0 for a project's first segment —
	//    SegmentID is 1-based precisely so 0 can mean "none".
	//
	//    The end turn is never before the segment's start. A roll made for the session's last event
	//    opens the successor one turn past st.Turn, with nothing in it; the log refuses an end turn
	//    before the start, and that refusal left the session's last segment open for good. It
	//    closes at its start turn, empty.
	if st.Segment != 0 {
		endTurn := max(st.Turn, st.SegStartTurn)
		feats := map[string]float64{}
		if fs, ok := o.features(st, now); ok {
			feats = map[string]float64{
				featPathJaccard: fs.PathJaccard, featToolShift: fs.ToolShift,
				featLexicalCohesion: fs.LexicalCohesion, featGapSeconds: fs.GapSeconds,
				featTodoTransition: fs.TodoTransition,
			}
		}
		// The segment's token count rides in the same map under the store's pseudo-feature key,
		// because that is the only way SegmentLog.Close takes it (store splitSegFeatures). This
		// close is the one SP-08 performs, and it left the key out: every session-end close logged
		// "segment closed without a tokens feature" and recorded 0 tokens for good, so `timeline`
		// reported every such segment as empty (retrieval D8 of the V6 live lane).
		feats[featSegmentTokens] = float64(segmentTokens(st))
		o.soft(stageDAG, dag.BuildSegment(o.opt.Graph, dag.SegmentSpec{
			ID: st.Segment, PrevID: st.PrevSegment,
			StartTurn: st.SegStartTurn, EndTurn: endTurn, TS: now,
			StartPos: st.SegStartPos,
			Tokens:   core.Tokens(st.PrefixTokens - st.SegStartPos),
			Members:  nil, // membership was enrolled incrementally (graph.go enrol)
		}))
		o.soft(stageSegClose, o.opt.Store.Segments().Close(ctx, st.Segment, endTurn, feats))
	}

	// 2-3. §7.3 "Flush": dag/deps.jsonl, then the store's buffered writes.
	o.soft(stageGraphOut, o.opt.Graph.Flush(ctx))
	o.soft(stageStoreFlush, o.opt.Store.Flush(ctx))

	// 4. sketches/touch.cms and explore.hll. Ownership note: SP-05's flush route also calls
	//    SketchSet.Save after this seam returns, but that Save is dirty-guarded and only
	//    SketchSet.Write dirties — the observer mutates the sketches through the raw pointers
	//    WireObserver hands it, so the daemon's call is a no-op and THIS write is the one that
	//    happens. Both writers go through sketch.Save to the same two paths, so even a dirtied
	//    set's second write is idempotent and neither can produce a half-file.
	o.saveSketches()

	st.mu.Unlock() // released before o.mu is wanted anywhere below — decision 9's lock order

	// 5. state/observer.json — written BEFORE GC, so a GC failure never prevents the state file
	//    from having been written.
	o.persistState()

	// 6. §8.2 "Garbage collection. Reference-counted, run on SessionEnd", bounded by gcDeadline
	//    inside the SessionEnd hook's own 20s timeout. The store runs one pass at a time
	//    (store/gcgate.go): when another session's end or the idle scheduler is mid-pass, this call
	//    waits for it, answering to ctx, and is answered by the one follow-up pass that starts after
	//    it; gcDeadline bounds that pass, not the wait.
	rep, err := o.opt.Store.GC(ctx, store.GCPolicy{
		RetainDays:     o.opt.Cfg.Store.Retention.Days,
		RetainSessions: o.opt.Cfg.Store.Retention.Sessions,
		DryRun:         false,
		Deadline:       gcDeadline,
	})
	if err != nil {
		o.soft(stageGC, err)
	} else {
		o.opt.Log.Info("observer: gc", "scanned", rep.ScannedObjects, "deleted", rep.DeletedObjects,
			"freed", rep.BytesFreed, "truncated", rep.Truncated)
	}

	// 7. The session leaves the in-memory map; its durable half was written at step 5.
	o.mu.Lock()
	delete(o.sess, e.SessionID)
	o.mu.Unlock()

	return hookio.Empty(), nil
}

// saveSketches persists the two observer-owned sketch files via sketch.Save, skipping nil
// sketches and softing errors. It NEVER writes tried.bloom — §3.3 reserves that file for
// negknow.RebuildBloom, and sketch.Save itself refuses the name as a second line of defence.
//
// sketchMu, not the session lock, is what serializes this against the per-event feeds: the three
// sketches are per-PROJECT objects shared by every session (see the field's comment in
// observer.go).
func (o *observer) saveSketches() {
	dir := paths.Of(o.opt.ProjectRoot).Sketches

	o.sketchMu.Lock()
	defer o.sketchMu.Unlock()
	if o.opt.Touch != nil {
		o.soft(stageSketchSave, sketch.Save(filepath.Join(dir, touchSketchName), o.opt.Touch))
	}
	if o.opt.Explore != nil {
		o.soft(stageSketchSave, sketch.Save(filepath.Join(dir, exploreSketchName), o.opt.Explore))
	}
}
