// Commit 6's test surface: SessionStart's source switch and segment bookkeeping, SessionEnd's
// exact flush/index/GC order, and the crash-tolerant state rows the plan's table pins by name
// (three lint plan-gate rows resolve against the names in this file).
//
// The session-lifecycle fakes live here rather than in fakes_test.go because they are wrappers
// AROUND the shared doubles: fakeStore carries no SegmentLog and no GC recording, and a field
// cannot be added to it from this file, so sessionStore embeds it and overrides exactly the three
// methods session.go adds to the observer's store surface.
package observer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// callRecorder collects the cross-collaborator call order TestOnSessionEnd_Order pins.
type callRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *callRecorder) note(step string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, step)
}

func (r *callRecorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// fakeSegClose is one recorded SegmentLog.Close call.
type fakeSegClose struct {
	ID      core.SegmentID
	EndTurn core.TurnIndex
	Feats   map[string]float64
}

// fakeSegLog is the store.SegmentLog double. It embeds the interface so the four methods
// session.go never calls need no body; calling one panics on the nil embedded interface, which is
// a louder failure than a silent zero value.
type fakeSegLog struct {
	store.SegmentLog

	mu  sync.Mutex
	rec *callRecorder

	nextID  core.SegmentID
	opens   []store.Segment
	openErr error

	current    store.Segment
	currentErr error

	frontier    core.TurnIndex
	frontierErr error

	closes   []fakeSegClose
	closeErr error
}

// newFakeSegLog returns a log with no current segment (the fresh-project answer) and 1-based ids,
// matching the real segLog's "SegmentID is 1-based so 0 can mean none".
func newFakeSegLog() *fakeSegLog {
	return &fakeSegLog{nextID: 1, currentErr: core.ErrNotFound}
}

func (l *fakeSegLog) Open(_ context.Context, s store.Segment) (core.SegmentID, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.openErr != nil {
		return 0, l.openErr
	}
	l.opens = append(l.opens, s)
	id := l.nextID
	l.nextID++
	return id, nil
}

func (l *fakeSegLog) Close(_ context.Context, id core.SegmentID, endTurn core.TurnIndex, feats map[string]float64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rec != nil {
		l.rec.note("segments.close")
	}
	if l.closeErr != nil {
		return l.closeErr
	}
	copied := make(map[string]float64, len(feats))
	for k, v := range feats {
		copied[k] = v
	}
	l.closes = append(l.closes, fakeSegClose{ID: id, EndTurn: endTurn, Feats: copied})
	return nil
}

func (l *fakeSegLog) Current(_ context.Context, _ core.SessionID) (store.Segment, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.current, l.currentErr
}

func (l *fakeSegLog) Frontier(_ context.Context, _ core.SessionID) (core.TurnIndex, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.frontier, l.frontierErr
}

func (l *fakeSegLog) opensList() []store.Segment {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]store.Segment(nil), l.opens...)
}

func (l *fakeSegLog) closesList() []fakeSegClose {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]fakeSegClose(nil), l.closes...)
}

func (l *fakeSegLog) setCurrent(s store.Segment) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.current, l.currentErr = s, nil
}

func (l *fakeSegLog) setFrontier(t core.TurnIndex) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.frontier = t
}

// sessionStore widens fakeStore with the three session.go call surfaces: Segments, GC and an
// order-recording Flush.
type sessionStore struct {
	*fakeStore
	seg *fakeSegLog
	rec *callRecorder

	mu         sync.Mutex
	gcPolicies []store.GCPolicy
	gcErr      error
	gcFn       func()
	flushFn    func()
}

func newSessionStore() *sessionStore {
	return &sessionStore{fakeStore: newFakeStore(), seg: newFakeSegLog()}
}

func (s *sessionStore) Segments() store.SegmentLog { return s.seg }

func (s *sessionStore) Flush(ctx context.Context) error {
	if s.rec != nil {
		s.rec.note("store.flush")
	}
	s.mu.Lock()
	fn := s.flushFn
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
	return s.fakeStore.Flush(ctx)
}

func (s *sessionStore) GC(_ context.Context, p store.GCPolicy) (store.GCReport, error) {
	if s.rec != nil {
		s.rec.note("store.gc")
	}
	s.mu.Lock()
	s.gcPolicies = append(s.gcPolicies, p)
	fn, err := s.gcFn, s.gcErr
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
	if err != nil {
		return store.GCReport{}, err
	}
	return store.GCReport{ScannedObjects: 1}, nil
}

func (s *sessionStore) policies() []store.GCPolicy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]store.GCPolicy(nil), s.gcPolicies...)
}

// sessionGraph wraps fakeGraph to record where dag.BuildSegment's node and Graph.Flush land in the
// SessionEnd order.
type sessionGraph struct {
	*fakeGraph
	rec *callRecorder
}

func (g *sessionGraph) AddNode(n dag.Node) error {
	if g.rec != nil && n.Kind == dag.KindSegment {
		g.rec.note("dag.BuildSegment")
	}
	return g.fakeGraph.AddNode(n)
}

func (g *sessionGraph) Flush(ctx context.Context) error {
	if g.rec != nil {
		g.rec.note("graph.flush")
	}
	return g.fakeGraph.Flush(ctx)
}

// fakeRehydrator records the SP-11 seam's delegations.
type fakeRehydrator struct {
	mu               sync.Mutex
	compacts, clears int
	out              hookio.Output
	err              error
}

func (r *fakeRehydrator) OnCompact(_ context.Context, _ Event) (Output, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.compacts++
	return r.out, r.err
}

func (r *fakeRehydrator) OnClear(_ context.Context, _ Event) (Output, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clears++
	return r.out, r.err
}

func (r *fakeRehydrator) counts() (compacts, clears int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.compacts, r.clears
}

// newSessionHarness builds a harness over a sessionStore.
func newSessionHarness(t *testing.T, mutators ...func(*Options)) (*harness, *sessionStore) {
	t.Helper()
	ss := newSessionStore()
	all := append([]func(*Options){func(o *Options) { o.Store = ss }}, mutators...)
	return newHarness(t, all...), ss
}

// startEvent and endEvent build the two session-lifecycle payloads.
func startEvent(source string) Event {
	return Event{HookEventName: "SessionStart", SessionID: testSession, Source: source}
}

func endEvent() Event {
	return Event{HookEventName: "SessionEnd", SessionID: testSession}
}

func TestOnSessionStart_StartupOpensSegment(t *testing.T) {
	h, ss := newSessionHarness(t)

	out, err := h.obs.OnSessionStart(context.Background(), startEvent("startup"))
	require.NoError(t, err)
	require.Equal(t, hookio.Empty(), out)

	opens := ss.seg.opensList()
	require.Len(t, opens, 1, "no current segment: exactly one SegmentLog.Open")
	require.Equal(t, core.TurnIndex(0), opens[0].StartTurn)
	require.Equal(t, testSession, opens[0].Session)
	require.False(t, opens[0].Closed)

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, core.SegmentID(1), st.Segment, "st.Segment set from Open's assigned id")
	require.Equal(t, core.TurnIndex(0), st.SegStartTurn)
	require.Equal(t, 0, st.SegStartPos, "SegStartPos is the prefix position at open")
}

func TestOnSessionStart_ResumeReusesOpenSegment(t *testing.T) {
	h, ss := newSessionHarness(t)
	ss.seg.setCurrent(store.Segment{ID: 4, Session: testSession, StartTurn: 2, Closed: false})

	_, err := h.obs.OnSessionStart(context.Background(), startEvent("resume"))
	require.NoError(t, err)

	require.Empty(t, ss.seg.opensList(), "an open segment is adopted, never re-opened")
	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, core.SegmentID(4), st.Segment)
	require.Equal(t, core.TurnIndex(2), st.SegStartTurn)
}

func TestOnSessionStart_ResumeAdoptsFrontierTurn(t *testing.T) {
	h, ss := newSessionHarness(t)
	ss.seg.setFrontier(37)

	_, err := h.obs.OnSessionStart(context.Background(), startEvent("resume"))
	require.NoError(t, err)

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, core.TurnIndex(37), st.Turn,
		"with the state file absent the segment frontier is the only resume evidence")
}

func TestOnSessionStart_CompactDelegates(t *testing.T) {
	r := &fakeRehydrator{out: hookio.Output{HookSpecificOutput: &hookio.HSO{
		HookEventName: "SessionStart", AdditionalContext: "rehydrated",
	}}}
	h, _ := newSessionHarness(t, func(o *Options) { o.Rehydrate = r })

	out, err := h.obs.OnSessionStart(context.Background(), startEvent("compact"))
	require.NoError(t, err)
	require.Equal(t, r.out, out, "the Rehydrator's output is returned verbatim")

	compacts, clears := r.counts()
	require.Equal(t, 1, compacts, "OnCompact called once")
	require.Zero(t, clears)
}

func TestOnSessionStart_ClearDelegates(t *testing.T) {
	r := &fakeRehydrator{}
	h, _ := newSessionHarness(t, func(o *Options) { o.Rehydrate = r })

	_, err := h.obs.OnSessionStart(context.Background(), startEvent("clear"))
	require.NoError(t, err)

	compacts, clears := r.counts()
	require.Equal(t, 1, clears, "OnClear called once")
	require.Zero(t, compacts)
}

func TestOnSessionStart_CompactWithoutRehydratorIsEmpty(t *testing.T) {
	h, ss := newSessionHarness(t) // Rehydrate == nil until SP-11 lands

	out, err := h.obs.OnSessionStart(context.Background(), startEvent("compact"))
	require.NoError(t, err)
	require.Equal(t, hookio.Empty(), out)
	require.Len(t, ss.seg.opensList(), 1, "the segment is still ensured before the source switch")
}

func TestOnSessionStart_UnknownSourceTreatedAsStartup(t *testing.T) {
	r := &fakeRehydrator{}
	h, ss := newSessionHarness(t, func(o *Options) { o.Rehydrate = r })

	out, err := h.obs.OnSessionStart(context.Background(), startEvent("marble_origami"))
	require.NoError(t, err, "an unknown source is a host change, not a crash")
	require.Equal(t, hookio.Empty(), out)
	require.Len(t, ss.seg.opensList(), 1)

	compacts, clears := r.counts()
	require.Zero(t, compacts, "an unknown source never reaches the rehydrator")
	require.Zero(t, clears)
}

func TestOnSessionEnd_Order(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	rec := &callRecorder{}
	ss := newSessionStore()
	ss.rec, ss.seg.rec = rec, rec
	sg := &sessionGraph{fakeGraph: newFakeGraph(), rec: rec}
	h := newHarness(t, func(o *Options) {
		o.ProjectRoot = root
		o.Store = ss
		o.Graph = sg
	})

	touchPath := paths.Long(filepath.Join(paths.Of(root).Sketches, "touch.cms"))
	explorePath := paths.Long(filepath.Join(paths.Of(root).Sketches, "explore.hll"))
	statePath := paths.Long(stateFilePath(root))

	// Steps 4 (sketch.Save x2) and 5 (the state write) have file evidence rather than a fake, so
	// their position is pinned by what exists at step 3's Store.Flush (nothing yet) versus at step
	// 6's GC (both).
	ss.flushFn = func() {
		require.NoFileExists(t, touchPath, "sketches are written AFTER Store.Flush (step 4 after 3)")
		require.NoFileExists(t, explorePath)
		require.NoFileExists(t, statePath, "the state write is step 5, after Store.Flush")
	}
	ss.gcFn = func() {
		require.FileExists(t, touchPath, "sketches are written BEFORE GC (step 4 before 6)")
		require.FileExists(t, explorePath)
		require.FileExists(t, statePath, "the state write is step 5, before GC")
	}

	st := h.state(testSession)
	st.mu.Lock()
	st.Segment, st.PrevSegment = 7, 6
	st.SegStartTurn, st.Turn = 33, 41
	st.SegStartPos, st.PrefixTokens = 100, 150
	st.mu.Unlock()

	out, err := h.obs.OnSessionEnd(context.Background(), endEvent())
	require.NoError(t, err)
	require.Equal(t, hookio.Empty(), out)

	require.Equal(t,
		[]string{"dag.BuildSegment", "segments.close", "graph.flush", "store.flush", "store.gc"},
		rec.list(), "the SessionEnd call order is exact (§7.3)")

	closes := ss.seg.closesList()
	require.Len(t, closes, 1)
	require.Equal(t, core.SegmentID(7), closes[0].ID)
	require.Equal(t, core.TurnIndex(41), closes[0].EndTurn)

	node, ok := sg.Node(dag.SegmentNode(7))
	require.True(t, ok, "BuildSegment minted the segment node")
	require.Equal(t, 100, node.Pos, "StartPos is the opening token position")
	require.Equal(t, core.Tokens(50), node.Tokens, "Tokens is PrefixTokens - SegStartPos")
	require.Equal(t, "33-41", node.Ref)
	_, ok = findEdge(sg.edges(), dag.SegmentNode(6), dag.SegmentNode(7), dag.EdgeSequence)
	require.True(t, ok, "the previous -> current chain edge is what CrossingEdges sees at a boundary")

	require.NoFileExists(t, paths.Long(filepath.Join(paths.Of(root).Sketches, "tried.bloom")))

	h.obs.mu.Lock()
	_, live := h.obs.sess[testSession]
	h.obs.mu.Unlock()
	require.False(t, live, "step 7: the session is deleted from the map after the state write")
}

func TestOnSessionEnd_GCPolicyFromConfig(t *testing.T) {
	h, ss := newSessionHarness(t)

	_, err := h.obs.OnSessionEnd(context.Background(), endEvent())
	require.NoError(t, err)

	def := config.Defaults().Store.Retention
	pols := ss.policies()
	require.Len(t, pols, 1)
	require.Equal(t, store.GCPolicy{
		RetainDays: def.Days, RetainSessions: def.Sessions, DryRun: false, Deadline: gcDeadline,
	}, pols[0])
	require.Equal(t, 30, def.Days, "the plan's GCPolicy row pins the Appendix C defaults")
	require.Equal(t, 10, def.Sessions)
	require.Equal(t, 8*time.Second, gcDeadline, "the plan's row pins Deadline: 8s")
}

func TestOnSessionEnd_GCFailureIsSoft(t *testing.T) {
	root := t.TempDir()
	ss := newSessionStore()
	ss.gcErr = errors.New("gc broke")
	h := newHarness(t, func(o *Options) {
		o.ProjectRoot = root
		o.Store = ss
	})

	out, err := h.obs.OnSessionEnd(context.Background(), endEvent())
	require.NoError(t, err, "a GC failure never escapes the hook")
	require.Equal(t, hookio.Empty(), out)
	require.Equal(t, int64(1), h.counter("observer.err."+stageGC))
	require.FileExists(t, paths.Long(stateFilePath(root)),
		"a GC failure never prevents the state file from having been written (step 5 before 6)")
}

func TestOnSessionEnd_NeverWritesTriedBloom(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock()
	log := logging.Nop()
	st, err := store.Open(root, config.Defaults(), store.Deps{Log: log, Clock: clk})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	g, err := dag.Open(root, config.Defaults(), log)
	require.NoError(t, err)

	built, err := New(Options{
		ProjectRoot: root, Cfg: config.Defaults(), Store: st, Graph: g,
		Touch: sketch.NewCMS(testCMSEpsilon, testCMSDelta), Explore: sketch.NewHLL(testHLLRegs),
		Hot: sketch.NewMisraGries(testMGCounters), Log: log, Clock: clk,
	})
	require.NoError(t, err)

	ctx := context.Background()
	_, err = built.OnSessionStart(ctx, startEvent("startup"))
	require.NoError(t, err)
	_, err = built.OnSessionEnd(ctx, endEvent())
	require.NoError(t, err)

	sketches := paths.Of(root).Sketches
	require.FileExists(t, paths.Long(filepath.Join(sketches, "touch.cms")))
	require.FileExists(t, paths.Long(filepath.Join(sketches, "explore.hll")))
	require.NoFileExists(t, paths.Long(filepath.Join(sketches, "tried.bloom")),
		"§3.3 reserves tried.bloom for negknow.RebuildBloom; SessionEnd must never write it")
}

func TestOnSessionEnd_SegmentClosedWithFeatures(t *testing.T) {
	h, ss := newSessionHarness(t)
	ctx := context.Background()

	_, err := h.obs.OnSessionStart(ctx, startEvent("startup"))
	require.NoError(t, err)

	// Two full feature windows (2*featureWindow = 16 prior events) so features() reports true.
	for i := range 2 * featureWindow {
		h.Clock.Advance(time.Second)
		h.drive(readOf(fmt.Sprintf("toolu_feat_%02d", i),
			fmt.Sprintf("src/f%02d.ts", i), fmt.Sprintf("content %02d\n", i)))
	}

	st := h.state(testSession)
	st.mu.Lock()
	endTurn := st.Turn
	st.mu.Unlock()

	_, err = h.obs.OnSessionEnd(ctx, endEvent())
	require.NoError(t, err)

	closes := ss.seg.closesList()
	require.Len(t, closes, 1)
	require.Equal(t, endTurn, closes[0].EndTurn, "Close receives endTurn == st.Turn")
	require.Len(t, closes[0].Feats, 5, "the §6.6 five-key feature map")
	for _, key := range []string{"path_jaccard", "tool_shift", "lexical_cohesion", "gap_seconds", "todo_transition"} {
		require.Contains(t, closes[0].Feats, key)
	}
}

func TestState_RoundTrip(t *testing.T) {
	root := t.TempDir()
	first := stateHarness(t, root)
	first.drive(readOf("toolu_rt_1", "src/a.ts", "alpha\n"))

	st := first.state(testSession)
	st.mu.Lock()
	st.Turn, st.PrefixTokens = 41, 128340
	st.Segment, st.PrevSegment = 7, 6
	st.SegStartTurn, st.SegStartPos = 33, 104880
	st.LastToolUseTurn = 40
	st.mu.Unlock()

	require.NoError(t, first.obs.Persist(context.Background()))

	got := stateHarness(t, root).state(testSession)
	got.mu.Lock()
	defer got.mu.Unlock()
	require.Equal(t, core.TurnIndex(41), got.Turn)
	require.Equal(t, 128340, got.PrefixTokens)
	require.Equal(t, core.SegmentID(7), got.Segment)
	require.Equal(t, core.SegmentID(6), got.PrevSegment)
	require.Equal(t, core.TurnIndex(33), got.SegStartTurn)
	require.Equal(t, 104880, got.SegStartPos)
	require.Equal(t, core.ToolUseID("toolu_rt_1"), got.LastToolUseID)
	require.Equal(t, core.TurnIndex(40), got.LastToolUseTurn)
	require.Len(t, got.ToolUses, 1, "the tool-use ring is restored")
	require.Equal(t, core.ToolUseID("toolu_rt_1"), got.ToolUses[0].ID)
}

func TestState_ResumedPrevTurnStillGuardsTheCycle(t *testing.T) {
	root := t.TempDir()
	first := stateHarness(t, root)
	first.setTurn(testSession, 7)
	first.drive(readOf("toolu_prev", "src/a.ts", "alpha\n")) // LastToolUseID/Turn = toolu_prev/7
	require.NoError(t, first.obs.Persist(context.Background()))

	second := stateHarness(t, root)
	st := second.state(testSession)
	st.mu.Lock()
	require.Equal(t, core.TurnIndex(7), st.LastToolUseTurn,
		"PrevTurn is persisted rather than recomputed as 0")
	st.mu.Unlock()

	// A tool use at turn 7 after the reload is a parallel sibling of toolu_prev. With PrevTurn
	// restored to 7, dag.BuildToolUse's PrevTurn < Turn guard suppresses the consumes edge; a
	// PrevTurn recomputed as 0 would emit it and close SP-07 D-7's cycle.
	second.drive(readOf("toolu_next", "src/b.ts", "beta\n"))
	for _, e := range second.Graph.edges() {
		if e.Kind == dag.EdgeConsumes && e.To == dag.AssistantNode(7) {
			t.Fatalf("a resumed same-turn sibling produced a toolresult -> assistant consumes edge: %+v", e)
		}
	}
}

func TestState_CorruptFileRecovers(t *testing.T) {
	root := t.TempDir()
	path := stateFilePath(root)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(path)), stateDirPerm))
	require.NoError(t, os.WriteFile(paths.Long(path), []byte("{{{"), stateFilePerm))

	h := stateHarness(t, root)
	st := h.state(testSession)

	require.Equal(t, core.TurnIndex(0), st.Turn, "fresh state: a corrupt file never blocks a session")
	require.Equal(t, int64(1), h.counter("observer.err."+stageState), "the failure is Warned and counted")
	require.FileExists(t, paths.Long(path+corruptStateSuffix), "observer.json.bad exists")
}

func TestState_AtomicWrite(t *testing.T) {
	root := t.TempDir()
	h := stateHarness(t, root)
	path := paths.Long(stateFilePath(root))
	require.NoError(t, h.obs.Persist(context.Background())) // the file exists before the reader starts

	stop := make(chan struct{})
	var torn atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			b, err := os.ReadFile(path)
			if err != nil {
				continue // a rename-in-progress read failure is not a partial CONTENT observation
			}
			var ps persistedState
			if json.Unmarshal(b, &ps) != nil {
				torn.Add(1)
			}
		}
	}()

	for i := range 100 {
		st := h.state(testSession)
		st.mu.Lock()
		st.TodoDone[fmt.Sprintf("todo-%03d", i)] = true
		st.mu.Unlock()
		require.NoError(t, h.obs.Persist(context.Background()))
	}
	close(stop)
	wg.Wait()

	require.Zero(t, torn.Load(),
		"paths.WriteAtomic must never let a reader observe a partial file at the target path")
}
