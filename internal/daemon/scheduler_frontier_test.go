package daemon

// The plan's 12 scheduler_frontier_test.go cases (plans/V4-SP-12-scheduler-l3.md, "Test plan"):
// segment close on the three tap boundaries, the SP-08-owns-the-first-open rule, O5 frontier
// advancement over closed-and-unencoded segments through checkpoint.Writer, the DPI guard, the
// residual-span accounting and its once-per-session over-budget warning, and the one case a fake
// cannot catch — the "tokens" pseudo-feature reaching a REAL store.SegmentLog.
//
// Every case but the real-store one runs over the in-memory doubles of
// scheduler_testhelpers_test.go through C1's rtFixture; all are parallel (none asserts the
// process-wide p-selection gate).

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
)

// frontierSeq is the foreign checkpoint sequence the DPI test marks a segment under, so the
// runtime's own draft (seq 1 from the fake writer) can never collide with it.
const frontierForeignSeq core.CheckpointSeq = 99

// newFrontierFixture is an rtFixture bound to rtSession with a fakeWriter over the fixture's own
// segment log and a Sources func that hands the writer the fixture's store, log and graph — the
// shape SP-10 will wire through SchedulerRuntimeOptions.Sources.
func newFrontierFixture(t testing.TB, mods ...func(*rtFixture)) *rtFixture {
	t.Helper()
	all := append([]func(*rtFixture){func(fx *rtFixture) { fx.writer = newFakeWriter(fx.store.segs) }}, mods...)
	fx := newRTFixture(t, all...)
	fx.bind(rtSession)
	fx.rt.mu.Lock()
	fx.rt.advancer = checkpoint.NewFrontierAdvancer(fx.writer, func() (checkpoint.SourceSet, error) {
		return checkpoint.SourceSet{Store: fx.store, Segments: fx.store.segs, Graph: fx.graph}, nil
	})
	fx.rt.mu.Unlock()
	return fx
}

// openSegment opens a segment of rtSession at start through the fixture's log (the SP-08 role).
func openSegment(t testing.TB, fx *rtFixture, start core.TurnIndex) core.SegmentID {
	t.Helper()
	id, err := fx.store.segs.Open(context.Background(), store.Segment{Session: rtSession, StartTurn: start, StartTS: fx.now()})
	require.NoError(t, err)
	return id
}

// markEncoded marks ids encoded under seq directly on the fixture's log.
func markEncoded(t testing.TB, fx *rtFixture, seq core.CheckpointSeq, ids ...core.SegmentID) {
	t.Helper()
	require.NoError(t, fx.store.segs.MarkEncoded(context.Background(), ids, seq))
}

// advance runs advanceFrontier and requires it to succeed.
func advance(t testing.TB, fx *rtFixture) {
	t.Helper()
	require.NoError(t, fx.rt.advanceFrontier(context.Background()))
}

// residualOf reads the residual under the lock.
func residualOf(fx *rtFixture) core.Tokens {
	fx.rt.mu.Lock()
	defer fx.rt.mu.Unlock()
	return fx.rt.residual
}

// frontierOf reads the frontier under the lock.
func frontierOf(fx *rtFixture) core.TurnIndex {
	fx.rt.mu.Lock()
	defer fx.rt.mu.Unlock()
	return fx.rt.frontier
}

// kvValue finds key in a captured log entry's KV list.
func kvValue(e logEntry, key string) (any, bool) {
	for i := 0; i+1 < len(e.KV); i += 2 {
		if k, ok := e.KV[i].(string); ok && k == key {
			return e.KV[i+1], true
		}
	}
	return nil, false
}

// ── Segment close on the tap's three boundaries ─────────────────────────────────────────────

// assertCloseAndRoll drives CloseSegmentOn with the cause the tap derives from sig and asserts
// the segment was closed at the turn, rolled into a successor, counted, and that the close
// carried the open accumulator as the "tokens" pseudo-feature.
func assertCloseAndRoll(t *testing.T, sig observer.Signals, wantCause string) {
	t.Helper()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	ctx := context.Background()
	first := openSegment(t, fx, 1)
	fx.rt.AddOpenSegmentTokens(2_500)

	cause := boundaryCause(sig)
	require.Equal(t, wantCause, cause, "the tap maps the signal to this cause")
	require.NoError(t, fx.rt.CloseSegmentOn(ctx, 7, scheduler.Features{PathJaccard: 0.5, ToolShift: 1}, cause))

	closes := fx.store.segs.closeCalls
	require.Len(t, closes, 1)
	require.Equal(t, first, closes[0].ID)
	require.Equal(t, core.TurnIndex(7), closes[0].EndTurn)
	require.Equal(t, 2_500.0, closes[0].Feats[segTokensFeature], "the close carries Σ rec.Tokens as the tokens pseudo-feature")
	for _, k := range []string{"path_jaccard", "tool_shift", "lexical_cohesion", "gap_seconds", "todo_transition", "prob_changepoint"} {
		require.Contains(t, closes[0].Feats, k, "the five BOCD features plus the posterior")
	}
	require.Equal(t, 0.5, closes[0].Feats["path_jaccard"])

	opens := fx.store.segs.openCalls
	require.Len(t, opens, 2, "the fixture's open, then the roll-open")
	require.Equal(t, core.TurnIndex(8), opens[1].StartTurn, "the successor starts at the close turn + 1")
	require.Equal(t, rtSession, opens[1].Session)
	require.Equal(t, fx.now(), opens[1].StartTS)

	closed, err := fx.store.segs.Get(ctx, first)
	require.NoError(t, err)
	require.True(t, closed.Closed)
	require.Equal(t, core.Tokens(2_500), closed.Tokens, "Segment.Tokens is written from the pseudo-feature")
	cur, err := fx.store.segs.Current(ctx, rtSession)
	require.NoError(t, err)
	require.Equal(t, core.TurnIndex(8), cur.StartTurn, "the successor is the session's current segment")

	require.Equal(t, int64(1), fx.counter(counterSegmentClosedPrefix+cause))
	require.Equal(t, core.Tokens(0), fx.rt.openSegTokens, "the accumulator is reset after the roll-open")
	require.True(t, fx.rt.dirty)
	require.Zero(t, fx.log.count(logWarn))
	require.Zero(t, fx.log.count(logLoud))
}

func TestFrontier_CloseOnTodoCompleted(t *testing.T) {
	t.Parallel()
	assertCloseAndRoll(t, observer.Signals{TodoCompleted: true}, causeTodo)
}

func TestFrontier_CloseOnTestPassed(t *testing.T) {
	t.Parallel()
	assertCloseAndRoll(t, observer.Signals{TestPassed: true}, causeTest)
}

func TestFrontier_CloseOnGitCommit(t *testing.T) {
	t.Parallel()
	assertCloseAndRoll(t, observer.Signals{GitCommit: true}, causeCommit)
}

func TestFrontier_NoCloseWithoutCurrentSegment(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	ctx := context.Background()
	fx.rt.AddOpenSegmentTokens(300)

	require.NoError(t, fx.rt.CloseSegmentOn(ctx, 4, scheduler.Features{}, causeTodo), "Current ⇒ ErrNotFound is not an error")
	require.Empty(t, fx.store.segs.closeCalls)
	require.Empty(t, fx.store.segs.openCalls, "SP-08 owns the session's first Open")
	require.Zero(t, fx.counter(counterSegmentClosedPrefix+causeTodo))
	require.Equal(t, core.Tokens(300), fx.rt.openSegTokens, "nothing was closed, so nothing is reset")

	// A close BEFORE the open segment's own start turn is refused the same silent way.
	openSegment(t, fx, 10)
	require.NoError(t, fx.rt.CloseSegmentOn(ctx, 4, scheduler.Features{}, causeTest))
	require.Empty(t, fx.store.segs.closeCalls)
	require.Len(t, fx.store.segs.openCalls, 1, "only the fixture's own open")

	// And the whole mechanism is off when the frontier is not advanced on close.
	off := newRTFixture(t, func(fx *rtFixture) { fx.cfg.Checkpoint.Frontier.AdvanceOnSegmentClose = false })
	off.bind(rtSession)
	openSegment(t, off, 1)
	require.NoError(t, off.rt.CloseSegmentOn(ctx, 5, scheduler.Features{}, causeCommit))
	require.Empty(t, off.store.segs.closeCalls)
	require.Len(t, off.store.segs.openCalls, 1)
}

// ── O5 frontier advancement ─────────────────────────────────────────────────────────────────

func TestFrontier_AdvanceCallsWriterWithClosedUnencodedOnly(t *testing.T) {
	t.Parallel()
	fx := newFrontierFixture(t)
	ctx := context.Background()
	segs := fx.store.segs

	// Inserted out of turn order on purpose: B (turns 4–6) gets a lower ID than A (turns 1–3),
	// so an implementation that forwards Unencoded's ID order would hand the writer [B, A].
	b := segs.addSegment(t, rtSession, 4, 6, 800)
	a := segs.addSegment(t, rtSession, 1, 3, 1_000)
	open := openSegment(t, fx, 10)
	d := segs.addSegment(t, rtSession, 7, 8, 500)
	e := segs.addSegment(t, rtSession, 9, 9, 200)
	markEncoded(t, fx, frontierForeignSeq, d, e)
	fx.rt.NoteAPIRound(10)

	advance(t, fx)

	require.Equal(t, [][]core.SegmentID{{a, b}}, fx.writer.advanceCalls, "exactly the closed+unencoded ids, ascending by StartTurn")
	require.Len(t, fx.writer.beginCalls, 1, "the draft is opened lazily, once")
	require.Equal(t, writerBeginCall{Session: rtSession, Parent: 0}, fx.writer.beginCalls[0])
	require.Equal(t, core.TurnIndex(6), frontierOf(fx), "the frontier is what Advance returned")
	require.Len(t, fx.writer.drafts, 1, "the draft remains owned by the writer")
	require.True(t, fx.rt.dirty)
	require.Equal(t, uint64(1), fx.rt.frontierRuns)

	for _, id := range []core.SegmentID{a, b} {
		seg, err := segs.Get(ctx, id)
		require.NoError(t, err)
		require.True(t, seg.EncodedOnce, "segment %d encoded through the writer", id)
		require.Equal(t, core.CheckpointSeq(1), seg.CheckpointSeq)
	}
	cur, err := segs.Get(ctx, open)
	require.NoError(t, err)
	require.False(t, cur.EncodedOnce, "the open segment is never encoded")
	require.Zero(t, fx.counter(counterFrontierNoWriter))
	require.Zero(t, fx.log.count(logLoud))
	require.Zero(t, fx.log.count(logWarn))
}

func TestFrontier_AdvanceNoWriterIsNoOp(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	fx.store.segs.addSegment(t, rtSession, 1, 3, 1_000)
	fx.rt.NoteAPIRound(3)

	advance(t, fx)
	require.Equal(t, int64(1), fx.counter(counterFrontierNoWriter), "ckpt == nil is the Rule W-2 posture, counted")
	require.Zero(t, frontierOf(fx))
	require.Equal(t, uint64(1), fx.rt.frontierRuns, "the run is still a run for the starvation counter")

	// A writer without a Sources func is the same posture.
	w := newFakeWriter(fx.store.segs)
	fx.rt.mu.Lock()
	fx.rt.advancer = nil // constructor leaves the port absent without a source supplier
	fx.rt.mu.Unlock()
	advance(t, fx)
	require.Equal(t, int64(2), fx.counter(counterFrontierNoWriter))
	require.Empty(t, w.beginCalls)

	// A configured source supplier failure propagates; it is not a successful idle action.
	fx.rt.mu.Lock()
	fx.rt.advancer = checkpoint.NewFrontierAdvancer(w, func() (checkpoint.SourceSet, error) {
		return checkpoint.SourceSet{}, context.DeadlineExceeded
	})
	fx.rt.mu.Unlock()
	require.ErrorIs(t, fx.rt.advanceFrontier(context.Background()), context.DeadlineExceeded)
	require.Equal(t, int64(2), fx.counter(counterFrontierNoWriter))
	require.Empty(t, w.beginCalls, "Begin is never reached without a SourceSet")
	require.Equal(t, 1, fx.log.count(logWarn))
	require.Zero(t, fx.log.count(logLoud))
}

func TestFrontier_AdvanceIdempotent(t *testing.T) {
	t.Parallel()
	fx := newFrontierFixture(t)
	a := fx.store.segs.addSegment(t, rtSession, 1, 3, 1_000)
	b := fx.store.segs.addSegment(t, rtSession, 4, 5, 400)
	openSegment(t, fx, 6)
	fx.rt.NoteAPIRound(6)

	advance(t, fx)
	require.Equal(t, core.TurnIndex(5), frontierOf(fx))

	advance(t, fx)
	require.Equal(t, core.TurnIndex(5), frontierOf(fx), "the second call moves nothing")
	require.Equal(t, [][]core.SegmentID{{a, b}}, fx.writer.advanceCalls, "nothing left to encode ⇒ Advance is not called again")
	require.Len(t, fx.store.segs.markCalls, 1)
	require.Len(t, fx.writer.beginCalls, 1)
	require.Len(t, fx.writer.drafts, 1, "the writer still owns its draft")
	require.Zero(t, fx.writer.abortCalls)
	require.Zero(t, fx.log.count(logLoud))
	require.Zero(t, fx.log.count(logWarn))
}

type frontierAdvanceFunc func(context.Context, core.SessionID, []core.SegmentID) (core.TurnIndex, error)

func (f frontierAdvanceFunc) Advance(ctx context.Context, session core.SessionID, ids []core.SegmentID) (core.TurnIndex, error) {
	return f(ctx, session, ids)
}

func TestFrontier_ConstructorUsesPortOrLegacyAdapter(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy adapter", true: "explicit port"}[explicit], func(t *testing.T) {
			fx := newRTFixture(t, func(fx *rtFixture) { fx.writer = newFakeWriter(fx.store.segs) })
			opts := fx.options()
			sourceCalls, portCalls := 0, 0
			opts.Sources = func() (checkpoint.SourceSet, error) {
				sourceCalls++
				return checkpoint.SourceSet{Store: fx.store, Segments: fx.store.segs, Graph: fx.graph}, nil
			}
			if explicit {
				opts.Frontier = frontierAdvanceFunc(func(context.Context, core.SessionID, []core.SegmentID) (core.TurnIndex, error) {
					portCalls++
					return 3, nil
				})
			}
			rt, err := NewSchedulerRuntime(opts)
			require.NoError(t, err)
			concrete, ok := rt.(*schedRuntime)
			require.True(t, ok, "NewSchedulerRuntime returns the daemon's concrete runtime")
			fx.rt = concrete
			fx.bind(rtSession)
			fx.store.segs.addSegment(t, rtSession, 1, 3, 100)
			fx.rt.NoteAPIRound(3)
			advance(t, fx)
			require.Equal(t, core.TurnIndex(3), frontierOf(fx))
			if explicit {
				require.Equal(t, 1, portCalls)
				require.Zero(t, sourceCalls, "an explicit owner port takes precedence")
				require.Empty(t, fx.writer.beginCalls)
			} else {
				require.Equal(t, 1, sourceCalls)
				require.Len(t, fx.writer.beginCalls, 1)
			}
		})
	}
}

// TestFrontier_DPIGuardPreservesOwnerOutcomeWithoutRetry replaces the scheduler-owned retry/abort
// assertion. FileWriter tests establish partial DPI publication; here the port's outcome must
// reach scheduler accounting without a second encoding attempt or draft lifecycle action.
func TestFrontier_DPIGuardPreservesOwnerOutcomeWithoutRetry(t *testing.T) {
	t.Parallel()
	fx := newFrontierFixture(t)
	ctx := context.Background()
	segs := fx.store.segs
	s1 := segs.addSegment(t, rtSession, 1, 2, 100)
	s2 := segs.addSegment(t, rtSession, 3, 4, 100)
	s3 := segs.addSegment(t, rtSession, 5, 6, 100)
	fx.rt.NoteAPIRound(6)
	calls := 0
	fx.rt.advancer = frontierAdvanceFunc(func(ctx context.Context, session core.SessionID, ids []core.SegmentID) (core.TurnIndex, error) {
		calls++
		require.Equal(t, rtSession, session)
		require.Equal(t, []core.SegmentID{s1, s2, s3}, ids)
		require.NoError(t, segs.MarkEncoded(ctx, []core.SegmentID{s2}, frontierForeignSeq))
		require.NoError(t, segs.MarkEncoded(ctx, []core.SegmentID{s1, s3}, 1))
		return 6, core.ErrAlreadyEncoded
	})
	advance(t, fx)
	require.Equal(t, 1, calls, "only the owner adjudicates and publishes the batch")
	require.Equal(t, 1, fx.log.count(logLoud))
	require.Equal(t, msgDPIGuard, fx.log.msgs(logLoud)[0])
	require.Equal(t, core.TurnIndex(6), frontierOf(fx))
	require.Empty(t, fx.writer.beginCalls, "scheduler calls only the supplied port")
	require.Zero(t, fx.writer.abortCalls)
	foreign, err := segs.Get(ctx, s2)
	require.NoError(t, err)
	require.Equal(t, frontierForeignSeq, foreign.CheckpointSeq)
	advance(t, fx)
	require.Equal(t, 1, calls, "no unencoded segments remain")
}

func TestFrontier_SessionChangeDuringAdvancePreservesOwnerWork(t *testing.T) {
	fx := newFrontierFixture(t)
	fx.store.segs.addSegment(t, rtSession, 1, 3, 100)
	fx.rt.NoteAPIRound(3)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	fx.rt.advancer = frontierAdvanceFunc(func(_ context.Context, session core.SessionID, _ []core.SegmentID) (core.TurnIndex, error) {
		if session != rtSession {
			return 0, context.Canceled
		}
		close(entered)
		<-release
		return 3, nil
	})
	finished := make(chan error, 1)
	go func() { finished <- fx.rt.advanceFrontier(context.Background()) }()
	<-entered
	fx.rt.BindSession("next-session", nil)
	// Release once while also allowing cleanup if an assertion aborts the test.
	release <- struct{}{}
	require.NoError(t, <-finished)
	require.Zero(t, frontierOf(fx), "old-session work must not update the new session's frontier")
	require.Zero(t, fx.writer.abortCalls, "rebinding has no authority over the old owner's draft")
}

func TestFrontier_CloseWritesRealSegmentTokens(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	clk := newFakeClock(epoch)
	log := newRecordingLogger()
	st, err := store.Open(root, config.Defaults(), store.Deps{Log: log, Clock: clk})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	reg := obs.New(clk)

	rt, err := NewSchedulerRuntime(SchedulerRuntimeOptions{
		ProjectRoot: root, Cfg: config.Defaults(), Clock: clk, Log: log, Metrics: reg,
		Store: st, Graph: newFakeGraph(),
	})
	require.NoError(t, err)
	t.Cleanup(scheduler.DisablePSelection)
	r, ok := rt.(*schedRuntime)
	require.True(t, ok, "NewSchedulerRuntime returns the daemon's own runtime")
	r.BindSession(rtSession, nil)

	// SP-08's first open, then 4 000 tokens of tool use observed THROUGH THE TAP.
	id, err := st.Segments().Open(ctx, store.Segment{
		Session: rtSession, StartTurn: 0, StartTS: core.NowMilli(clk), Features: map[string]float64{},
	})
	require.NoError(t, err)
	rec := store.ToolUseRecord{
		ID: "toolu_real", Session: rtSession, Turn: 3, TS: core.NowMilli(clk) + 5_000, Tool: "Read",
		ArgsPreview: "read src/a.go", Path: "src/a.go", Tokens: 4_000, Status: store.StatusOK,
	}
	require.NoError(t, st.RecordToolUse(ctx, rec))
	s := &Services{}
	WrapServicesForScheduler(s, r, SchedulerRuntimeOptions{Log: log, Metrics: reg})
	require.NoError(t, s.ObserveTool(ctx, tapToolEvent("toolu_real", "Read", `{"file_path":"src/a.go"}`, "")))
	require.Equal(t, core.Tokens(4_000), r.openSegTokens)

	r.mu.Lock()
	err = r.closeSegmentLocked(ctx, 3, scheduler.Features{PathJaccard: 1}, causeTest)
	r.mu.Unlock()
	require.NoError(t, err)

	seg, err := st.Segments().Get(ctx, id)
	require.NoError(t, err)
	require.True(t, seg.Closed)
	require.Equal(t, core.Tokens(4_000), seg.Tokens, "feats[\"tokens\"] is the only writer of Segment.Tokens")
	require.NotContains(t, seg.Features, "tokens", "the pseudo-feature is lifted off the BOCD summary")
	require.Equal(t, 1.0, seg.Features["path_jaccard"])
	for _, m := range log.msgs(logWarn) {
		require.NotContains(t, m, "without a tokens feature", "the store must not warn about a missing tokens feature")
	}
	require.Zero(t, log.count(logLoud))

	cur, err := st.Segments().Current(ctx, rtSession)
	require.NoError(t, err)
	require.Equal(t, core.TurnIndex(4), cur.StartTurn, "rolled open in the real log")
	require.Equal(t, core.Tokens(0), r.openSegTokens)
	require.Equal(t, int64(1), reg.Counter(counterSegmentClosedPrefix+causeTest).Value())

	// The closed segment's tokens now reach Evaluate through the real Range.
	r.NoteAPIRound(4)
	d, err := r.Evaluate(ctx)
	require.NoError(t, err)
	require.Equal(t, 4_000.0, d.Breakdown["context_tokens"])
}

// ── Residual accounting ─────────────────────────────────────────────────────────────────────

func TestFrontier_ResidualRecomputed(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	segs := fx.store.segs
	e1 := segs.addSegment(t, rtSession, 1, 2, 20_000)
	e2 := segs.addSegment(t, rtSession, 3, 4, 11_000)
	segs.addSegment(t, rtSession, 5, 6, 5_000) // closed, not yet encoded
	markEncoded(t, fx, frontierForeignSeq, e1, e2)
	fx.rt.NoteAPIRound(6)
	fx.rt.AddOpenSegmentTokens(4_000)

	fx.rt.mu.Lock()
	fx.rt.recomputeResidualLocked(context.Background())
	fx.rt.mu.Unlock()

	require.Equal(t, core.Tokens(40_000), fx.rt.contextTokens, "closed Σ plus the open accumulator")
	require.Equal(t, core.Tokens(9_000), residualOf(fx), "context 40 000 − encoded 31 000")
}

func TestFrontier_ResidualNeverNegative(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	segs := fx.store.segs
	e1 := segs.addSegment(t, rtSession, 1, 2, 20_000)
	markEncoded(t, fx, frontierForeignSeq, e1)
	// An encoded segment the context sum does not count. The real DPI guard refuses to mark an
	// open segment, so this shape cannot arise from the log itself; it is the one arithmetic
	// input under which Σ encoded exceeds contextTokens, which is exactly what the clamp guards.
	segs.mu.Lock()
	segs.insertLocked(store.Segment{Session: rtSession, StartTurn: 3, Tokens: 25_000, EncodedOnce: true, CheckpointSeq: frontierForeignSeq})
	segs.mu.Unlock()
	fx.rt.NoteAPIRound(4)
	fx.rt.AddOpenSegmentTokens(1_000)

	fx.rt.mu.Lock()
	fx.rt.recomputeResidualLocked(context.Background())
	fx.rt.mu.Unlock()

	require.Equal(t, core.Tokens(21_000), fx.rt.contextTokens)
	require.Equal(t, core.Tokens(0), residualOf(fx), "encoded 45 000 > context 21 000 clamps to zero")
}

func TestFrontier_ResidualOverBudgetWarnsOnce(t *testing.T) {
	t.Parallel()
	const maxResidual = 20_000

	t.Run("nothing to encode", func(t *testing.T) {
		t.Parallel()
		fx := newFrontierFixture(t)
		require.Equal(t, maxResidual, fx.cfg.Checkpoint.Frontier.MaxResidualTokens, "Appendix C")
		openSegment(t, fx, 1)
		fx.rt.NoteAPIRound(1)
		fx.rt.AddOpenSegmentTokens(25_000)

		advance(t, fx)
		advance(t, fx)

		require.Equal(t, core.Tokens(25_000), residualOf(fx))
		require.Empty(t, fx.writer.advanceCalls, "nothing closed ⇒ nothing to encode")
		warns := fx.log.entries(logWarn)
		require.Len(t, warns, 1, "exactly one Warn per session, on the empty-batch path too")
		require.Equal(t, msgResidualOverBudget, warns[0].Msg)
		n, ok := kvValue(warns[0], "unencodedClosed")
		require.True(t, ok)
		require.Equal(t, 0, n)
		require.Equal(t, int64(1), fx.reg.Gauge(gaugeResidualOverBudget).Value())
		require.True(t, fx.rt.residualWarned)
	})

	t.Run("three-segment backlog", func(t *testing.T) {
		t.Parallel()
		fx := newFrontierFixture(t)
		segs := fx.store.segs
		segs.addSegment(t, rtSession, 1, 2, 1_000)
		segs.addSegment(t, rtSession, 3, 4, 1_000)
		segs.addSegment(t, rtSession, 5, 6, 1_000)
		openSegment(t, fx, 7)
		fx.rt.NoteAPIRound(7)
		fx.rt.AddOpenSegmentTokens(25_000)

		advance(t, fx)
		advance(t, fx)

		require.Equal(t, core.TurnIndex(6), frontierOf(fx), "the backlog was encoded")
		require.Equal(t, core.Tokens(25_000), residualOf(fx), "the open segment alone is over budget")
		warns := fx.log.entries(logWarn)
		require.Len(t, warns, 1, "a backlog must not suppress the warning, and it fires once")
		require.Equal(t, msgResidualOverBudget, warns[0].Msg)
		n, ok := kvValue(warns[0], "unencodedClosed")
		require.True(t, ok)
		require.Equal(t, 3, n, "the line carries the backlog length")
		ft, ok := kvValue(warns[0], "frontierTurn")
		require.True(t, ok)
		require.Equal(t, 6, ft)
		require.Equal(t, int64(1), fx.reg.Gauge(gaugeResidualOverBudget).Value())
	})
}

// ── Dependency verification before advance (item 5 / G12) ────────────────────────────────────

// reReadLog delegates every SegmentLog call to inner but lets a test script what the
// verification pass sees when it RE-READS a segment. Unencoded still lists the segment as
// closed; Get answers with whatever the script says, which is how "the evidence changed between
// listing and advancing" is injected without touching internal/store.
type reReadLog struct {
	store.SegmentLog
	get func(ctx context.Context, id core.SegmentID) (store.Segment, error)
}

func (l reReadLog) Get(ctx context.Context, id core.SegmentID) (store.Segment, error) {
	if l.get != nil {
		return l.get(ctx, id)
	}
	return l.SegmentLog.Get(ctx, id)
}

// swapSegLog installs a SegmentLog on the runtime under its lock.
func swapSegLog(fx *rtFixture, l store.SegmentLog) {
	fx.rt.mu.Lock()
	fx.rt.segs = l
	fx.rt.mu.Unlock()
}

// TestFrontier_AdvanceStopsAtAGapInDurableEvidence is the "never advance past missing evidence"
// gate. Turns 4–6 have no segment at all, so the frontier stops at 3: the segment beyond the gap
// is NOT encoded out of order, and it stays listed so a later pass can still take it.
func TestFrontier_AdvanceStopsAtAGapInDurableEvidence(t *testing.T) {
	t.Parallel()
	fx := newFrontierFixture(t)
	ctx := context.Background()
	a := fx.store.segs.addSegment(t, rtSession, 1, 3, 1_000)
	beyond := fx.store.segs.addSegment(t, rtSession, 7, 9, 700) // turns 4-6 are missing
	openSegment(t, fx, 10)
	fx.rt.NoteAPIRound(10)

	advance(t, fx)

	require.Equal(t, [][]core.SegmentID{{a}}, fx.writer.advanceCalls, "only the verified prefix is submitted")
	require.Equal(t, core.TurnIndex(3), frontierOf(fx), "the frontier stops at the last durable turn")
	require.Equal(t, int64(1), fx.counter(counterFrontierUnverified))
	require.Equal(t, int64(1), fx.counter(counterFrontierUnverified+"."+evidenceGap))
	require.Equal(t, 1, fx.log.count(logWarn), "the gap is reported, not silent")

	seg, err := fx.store.segs.Get(ctx, beyond)
	require.NoError(t, err)
	require.False(t, seg.EncodedOnce, "the segment past the gap is preserved, not closed over")
}

// TestFrontier_AdvanceRefusesUnreadableEvidence covers evidence that has FAILED rather than gone
// missing: the segment log cannot re-read the very first candidate, so nothing advances at all.
func TestFrontier_AdvanceRefusesUnreadableEvidence(t *testing.T) {
	t.Parallel()
	fx := newFrontierFixture(t)
	fx.store.segs.addSegment(t, rtSession, 1, 3, 1_000)
	fx.store.segs.addSegment(t, rtSession, 4, 6, 900)
	openSegment(t, fx, 7)
	fx.rt.NoteAPIRound(7)
	swapSegLog(fx, reReadLog{
		SegmentLog: fx.store.segs,
		get: func(context.Context, core.SegmentID) (store.Segment, error) {
			return store.Segment{}, core.ErrNotFound
		},
	})

	advance(t, fx)

	require.Empty(t, fx.writer.advanceCalls, "no advance over evidence that cannot be read")
	require.Empty(t, fx.writer.beginCalls, "and no draft is begun for it")
	require.Zero(t, frontierOf(fx))
	require.Equal(t, int64(1), fx.counter(counterFrontierUnverified+"."+evidenceUnreadable))
}

// TestFrontier_AdvanceRefusesInFlightEvidence covers work still being produced: a segment that
// was closed when Unencoded listed it comes back OPEN on the verification re-read. In-flight work
// is never encoded, and the frontier stops at its predecessor.
func TestFrontier_AdvanceRefusesInFlightEvidence(t *testing.T) {
	t.Parallel()
	fx := newFrontierFixture(t)
	a := fx.store.segs.addSegment(t, rtSession, 1, 3, 1_000)
	inflight := fx.store.segs.addSegment(t, rtSession, 4, 6, 900)
	openSegment(t, fx, 7)
	fx.rt.NoteAPIRound(7)
	inner := fx.store.segs
	swapSegLog(fx, reReadLog{
		SegmentLog: inner,
		get: func(ctx context.Context, id core.SegmentID) (store.Segment, error) {
			seg, err := inner.Get(ctx, id)
			if id == inflight {
				seg.Closed = false
			}
			return seg, err
		},
	})

	advance(t, fx)

	require.Equal(t, [][]core.SegmentID{{a}}, fx.writer.advanceCalls)
	require.Equal(t, core.TurnIndex(3), frontierOf(fx))
	require.Equal(t, int64(1), fx.counter(counterFrontierUnverified+"."+evidenceNotClosed))
}

// TestFrontier_VerifyEvidenceStepsOverAnAlreadyEncodedNeighbour proves the prefix is not broken by
// durable evidence: an already-encoded segment is not resubmitted but contiguity continues across
// it, so its successor is still reached.
//
// Stepping over it is not the same as saying nothing about it. The row asserts both halves: b is
// absent from the verified prefix AND named in the dpi return, because the only reason it can be
// in a batch at all is that two writers disagree about who owns it. It was returning [a c] with no
// second signal that let a §4.6 violation reach the sweep as silence.
func TestFrontier_VerifyEvidenceStepsOverAnAlreadyEncodedNeighbour(t *testing.T) {
	t.Parallel()
	fx := newFrontierFixture(t)
	a := fx.store.segs.addSegment(t, rtSession, 1, 3, 1_000)
	b := fx.store.segs.addSegment(t, rtSession, 4, 6, 900)
	c := fx.store.segs.addSegment(t, rtSession, 7, 8, 400)
	markEncoded(t, fx, frontierForeignSeq, b)

	batch := []store.Segment{
		{ID: a, Session: rtSession, StartTurn: 1, EndTurn: 3, Closed: true},
		{ID: b, Session: rtSession, StartTurn: 4, EndTurn: 6, Closed: true},
		{ID: c, Session: rtSession, StartTurn: 7, EndTurn: 8, Closed: true},
	}
	ids, dpi, stop := verifyEvidence(context.Background(), fx.store.segs, rtSession, batch)
	require.Nil(t, stop, "durable evidence either side of it: nothing stops the prefix")
	require.Equal(t, []core.SegmentID{a, c}, ids, "b is not resubmitted and c is still reached")
	require.Equal(t, []core.SegmentID{b}, dpi, "and b is REPORTED, not silently dropped")
}

// dpiScriptedSegs is the two-writer disagreement one process over one log cannot produce on its
// own: Unencoded still OFFERS a segment the same log already records as encoded by another
// checkpoint. The real Unencoded filters on EncodedOnce, so extra is the only way to reach the
// state §4.6's guard exists for. Everything else falls through to the wrapped log.
type dpiScriptedSegs struct {
	store.SegmentLog
	extra []store.Segment
}

func (s dpiScriptedSegs) Unencoded(ctx context.Context, sess core.SessionID) ([]store.Segment, error) {
	out, err := s.SegmentLog.Unencoded(ctx, sess)
	if err != nil {
		return nil, err
	}
	return append(out, s.extra...), nil
}

// TestFrontier_AdvanceIsLoudAboutADPIViolation is the scheduler-side half of the §4.6 guard, and
// the direction that regressed. Verification steps over an already-encoded segment, so the writer
// never sees it and the writer's own ErrAlreadyEncoded can never fire for it. With that as the
// only guard the pass encodes the rest of the batch, drops the contested id and reports the
// violation NOWHERE — which is indistinguishable, from outside, from a clean pass.
func TestFrontier_AdvanceIsLoudAboutADPIViolation(t *testing.T) {
	fx := newFrontierFixture(t)
	a := fx.store.segs.addSegment(t, rtSession, 1, 3, 1_000)
	b := fx.store.segs.addSegment(t, rtSession, 4, 6, 900)
	markEncoded(t, fx, frontierForeignSeq, b)
	swapSegLog(fx, dpiScriptedSegs{SegmentLog: fx.store.segs, extra: []store.Segment{
		{ID: b, Session: rtSession, StartTurn: 4, EndTurn: 6, Closed: true, EncodedOnce: true},
	}})
	openSegment(t, fx, 7)
	fx.rt.NoteAPIRound(7)

	require.NoError(t, fx.rt.advanceFrontier(context.Background()),
		"the violation is logged and its ids dropped; it is not a pass failure")

	require.Equal(t, []string{msgDPIGuard}, fx.log.msgs(logLoud),
		"a DPI violation must never be able to pass silently")
	require.Equal(t, int64(1), fx.counter(counterFrontierDPIGuard))
	require.Equal(t, [][]core.SegmentID{{a}}, fx.writer.advanceCalls,
		"the clean segment still advances; the contested one is never resubmitted")
	require.Zero(t, fx.counter(counterFrontierUnverified),
		"a violation is not a shortfall of evidence and must not be reported as one")
}

// ── Cancellation and bounded resources (item 9) ──────────────────────────────────────────────

// TestFrontier_AdvanceHonoursCancellation asserts a cancelled idle budget stops the pass before
// it reaches the writer. The run still counts — the starvation counter measures passes, not work
// — but nothing is begun, nothing is advanced and the frontier does not move.
func TestFrontier_AdvanceHonoursCancellation(t *testing.T) {
	t.Parallel()
	fx := newFrontierFixture(t)
	fx.store.segs.addSegment(t, rtSession, 1, 3, 1_000)
	openSegment(t, fx, 4)
	fx.rt.NoteAPIRound(4)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := fx.rt.advanceFrontier(ctx)
	require.ErrorIs(t, err, context.Canceled)

	require.Empty(t, fx.writer.beginCalls, "a cancelled pass never opens a draft")
	require.Empty(t, fx.writer.advanceCalls)
	require.Zero(t, frontierOf(fx))
}

// TestFrontier_SegmentLogFailureLeavesTheFrontierUnchanged is the disk-failure row: the segment
// log cannot be listed, so the pass reports the failure and changes nothing. A frontier that
// advanced on an unreadable log would be a frontier over evidence nobody checked.
func TestFrontier_SegmentLogFailureLeavesTheFrontierUnchanged(t *testing.T) {
	t.Parallel()
	fx := newFrontierFixture(t)
	fx.store.segs.addSegment(t, rtSession, 1, 3, 1_000)
	fx.store.segs.unencodedErr = errors.New("disk I/O error")

	require.Error(t, fx.rt.advanceFrontier(context.Background()))
	require.Empty(t, fx.writer.beginCalls)
	require.Zero(t, frontierOf(fx))
	require.Equal(t, 1, fx.log.count(logWarn))
}

// TestFrontier_VerifyEvidenceStopsOnCancellation keeps the verification walk itself bounded: a
// budget that runs out mid-batch stops where it is rather than re-reading every remaining
// segment.
func TestFrontier_VerifyEvidenceStopsOnCancellation(t *testing.T) {
	t.Parallel()
	fx := newFrontierFixture(t)
	a := fx.store.segs.addSegment(t, rtSession, 1, 3, 1_000)
	b := fx.store.segs.addSegment(t, rtSession, 4, 6, 900)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ids, _, stop := verifyEvidence(ctx, fx.store.segs, rtSession, []store.Segment{
		{ID: a, Session: rtSession, StartTurn: 1, EndTurn: 3, Closed: true},
		{ID: b, Session: rtSession, StartTurn: 4, EndTurn: 6, Closed: true},
	})
	require.Empty(t, ids)
	require.NotNil(t, stop)
	require.Equal(t, evidenceCancelled, stop.reason)
}
