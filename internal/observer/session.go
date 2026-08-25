package observer

import (
	"context"
	"path/filepath"

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
)

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
	//    with the enrolment target in place.
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
// On the resume path PrefixTokens has just been rehydrated from state/observer.json, so
// SegStartPos is the real prefix offset rather than zero. An Open failure leaves st.Segment = 0,
// which every call site tolerates (enrol emits nothing; OnSessionEnd's step 1 skips). Closing on
// a changepoint is SP-12's; the only close SP-08 performs is the session-end close below.
func (o *observer) ensureSegment(ctx context.Context, st *sessionState, s core.SessionID, now core.UnixMilli) {
	cur, err := o.opt.Store.Segments().Current(ctx, s)
	if err == nil && !cur.Closed {
		st.Segment = cur.ID
		st.SegStartTurn = cur.StartTurn
	} else {
		st.PrevSegment = st.Segment
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

	// 1. The segment NODE and the previous -> current chain edge, then the session-index close.
	//    Members is nil because graph.go's enrol emitted one member -> segment edge per node as it
	//    arrived (legal under D-6). The chain edge is what makes the boundary visible to
	//    CrossingEdges: a boundary whose only crossing edge is the chain link is exactly the cheap
	//    cut the scheduler hunts for. st.PrevSegment is 0 for a project's first segment —
	//    SegmentID is 1-based precisely so 0 can mean "none".
	if st.Segment != 0 {
		feats := map[string]float64{}
		if fs, ok := o.features(st, now); ok {
			feats = map[string]float64{
				featPathJaccard: fs.PathJaccard, featToolShift: fs.ToolShift,
				featLexicalCohesion: fs.LexicalCohesion, featGapSeconds: fs.GapSeconds,
				featTodoTransition: fs.TodoTransition,
			}
		}
		o.soft(stageDAG, dag.BuildSegment(o.opt.Graph, dag.SegmentSpec{
			ID: st.Segment, PrevID: st.PrevSegment,
			StartTurn: st.SegStartTurn, EndTurn: st.Turn, TS: now,
			StartPos: st.SegStartPos,
			Tokens:   core.Tokens(st.PrefixTokens - st.SegStartPos),
			Members:  nil, // membership was enrolled incrementally (graph.go enrol)
		}))
		o.soft(stageSegClose, o.opt.Store.Segments().Close(ctx, st.Segment, st.Turn, feats))
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
	//    inside the SessionEnd hook's own 20s timeout.
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
