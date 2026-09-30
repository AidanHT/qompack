package daemon

// A daemon restarted in the middle of a session gets no SessionStart for it (V6 close-out wave 14,
// D46 follow-up). Its scheduler runtime used to stay bound to nothing for the rest of the session
// unless a PreCompact came: every Evaluate answered error_no_window and Persist wrote nothing. The
// first hook of the live session to reach the restarted daemon now binds it, restoring the
// session's persisted account exactly as a SessionStart bind does.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/store"
)

// registryDaemon is a fakeDaemon whose registry is a real one, so the tap's liveness question has
// an answer.
type registryDaemon struct {
	fakeDaemon
	reg *SessionRegistry
}

func (d *registryDaemon) Registry() *SessionRegistry { return d.reg }

// restartedAfter is the project a restarted daemon finds: the previous daemon bound rtSession,
// observed one 700-token read at turn 9 in its open segment, and persisted its state. It returns
// the restarted daemon's runtime, bound to nothing, over the same project.
func restartedAfter(t *testing.T) *rtFixture {
	t.Helper()
	ctx := context.Background()
	prev := newRTFixture(t)
	prev.bind(rtSession)
	_, err := prev.store.segs.Open(ctx, store.Segment{Session: rtSession, StartTurn: 0})
	require.NoError(t, err)
	tapRecord(prev, tapToolUseID, "Read", 700)
	ps := &Services{ObserveTool: func(context.Context, hookio.Event) error { return nil }}
	WrapServicesForScheduler(ps, prev.rt, prev.options())
	require.NoError(t, ps.ObserveTool(ctx, tapToolEvent(tapToolUseID, "Read", "", "")))
	require.NoError(t, prev.rt.Persist(ctx))

	fx := newRTFixture(t, withRoot(prev))
	require.Empty(t, fx.rt.session, "fixture sanity: the restarted runtime starts unbound")
	return fx
}

// putRead stores a read of rtSession at turn with tokens for the tap to look up.
func putRead(fx *rtFixture, id core.ToolUseID, turn core.TurnIndex, tokens core.Tokens) {
	fx.store.put(store.ToolUseRecord{
		ID: id, Session: rtSession, Turn: turn, TS: fx.now() + 9_000, Tool: "Read",
		ArgsPreview: "read src/b.go", Path: "src/b.go", Tokens: tokens,
	})
}

// allSeams is a Services value whose five L0 seams all succeed.
func allSeams() *Services {
	return &Services{
		SessionStart:  func(context.Context, hookio.Event) (hookio.Output, error) { return hookio.Empty(), nil },
		ObserveTool:   func(context.Context, hookio.Event) error { return nil },
		ObserveStop:   func(context.Context, hookio.Event, bool) error { return nil },
		ObservePrompt: func(context.Context, hookio.Event) (hookio.Output, error) { return hookio.Empty(), nil },
		SessionEnd:    func(context.Context, hookio.Event) error { return nil },
	}
}

// TestWrapServices_FirstToolAfterARestartBindsTheLiveSession: the first tool use of the live
// session binds the runtime, restoring the previous daemon's account for it (turn 9, 700 tokens in
// the open segment) and adding the read itself. The scheduler then answers with a window, and its
// state is persisted under the session again.
func TestWrapServices_FirstToolAfterARestartBindsTheLiveSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := restartedAfter(t)
	const later core.ToolUseID = "toolu_first_after_restart"
	putRead(fx, later, tapToolUseTurn+3, 300)
	s := allSeams()
	WrapServicesForScheduler(s, fx.rt, fx.options())
	require.Equal(t, 1.0, fx.evaluate(t).Breakdown["error_no_window"], "fixture sanity: unbound answers no window")

	require.NoError(t, s.ObserveTool(ctx, tapToolEvent(later, "Read", "", "")))

	require.Equal(t, rtSession, fx.rt.session, "the first hook of the live session binds it")
	require.Equal(t, core.Tokens(1_000), fx.rt.openSegTokens, "the persisted 700 plus the read's 300")
	require.Equal(t, tapToolUseTurn+3, fx.rt.maxTurn)
	require.NotContains(t, fx.evaluate(t).Breakdown, "error_no_window", "the bound runtime answers with a window")
	require.NoError(t, fx.rt.Persist(ctx))
	doc, err := decodeSchedulerState(mustRead(t, fx.statePath(stateFileScheduler)))
	require.NoError(t, err)
	require.Equal(t, rtSession, doc.Session)
	require.Equal(t, core.Tokens(1_000), doc.OpenSegmentTokens)
	require.Equal(t, tapToolUseTurn+3, doc.MaxTurn)
	require.Equal(t, int64(1), fx.counter(counterTapBindFirstHook))
}

// TestWrapServices_FirstStopOrCapturedPromptAfterARestartBinds: a Stop, or the worker's capture of a
// prompt, is as much the live session's first hook as a tool use is.
func TestWrapServices_FirstStopOrCapturedPromptAfterARestartBinds(t *testing.T) {
	t.Parallel()
	prompt := hookio.Event{HookEventName: "UserPromptSubmit", SessionID: rtSession, Prompt: "carry on"}
	for name, first := range map[string]func(*Services) error{
		"stop": func(s *Services) error {
			return s.ObserveStop(context.Background(), tapEvent("Stop", rtSession), false)
		},
		"captured prompt": func(s *Services) error {
			_, err := s.ObservePrompt(observer.WithPromptCaptureOnly(context.Background()), prompt)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fx := restartedAfter(t)
			s := allSeams()
			WrapServicesForScheduler(s, fx.rt, fx.options())

			require.NoError(t, first(s))
			require.Equal(t, rtSession, fx.rt.session)
			require.Equal(t, core.Tokens(700), fx.rt.openSegTokens, "the persisted account is restored")
			require.Equal(t, tapToolUseTurn, fx.rt.maxTurn)
			require.NotContains(t, fx.evaluate(t).Breakdown, "error_no_window")
		})
	}
}

// TestWrapServices_ReplyOnlyPromptDoesNotBind: the reply path runs inside SP-05's 250 ms prompt
// reply deadline, where the tap does no store I/O and no state-file read. It leaves the bind to the
// worker's capture of the same prompt, which follows it.
func TestWrapServices_ReplyOnlyPromptDoesNotBind(t *testing.T) {
	t.Parallel()
	fx := restartedAfter(t)
	s := allSeams()
	WrapServicesForScheduler(s, fx.rt, fx.options())
	ranges := len(fx.store.segs.rangeCalls)

	_, err := s.ObservePrompt(observer.WithPromptReplyOnly(context.Background()),
		hookio.Event{HookEventName: "UserPromptSubmit", SessionID: rtSession, Prompt: "carry on"})
	require.NoError(t, err)
	require.Empty(t, fx.rt.session, "the reply path does not bind")
	require.Len(t, fx.store.segs.rangeCalls, ranges, "and reads no segments")
	require.Zero(t, fx.counter(counterTapBindFirstHook))
}

// TestWrapServices_AReplayOfASessionNoHookTouchedDoesNotBind: a restarted daemon drains what its
// predecessor left, and a replayed delivery of a session no hook has touched since the restart is not
// the live session. Binding to it would leave the live one unbound for good, since only a
// SessionStart rebinds a bound runtime. The first hook the registry has seen binds, and what the
// runtime observed while unbound is folded into the restored account.
func TestWrapServices_AReplayOfASessionNoHookTouchedDoesNotBind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := restartedAfter(t)
	reg := NewSessionRegistry()
	fx.rt.mu.Lock()
	fx.rt.d = &registryDaemon{reg: reg}
	fx.rt.mu.Unlock()
	const replayed, live core.ToolUseID = "toolu_replayed", "toolu_live"
	putRead(fx, replayed, tapToolUseTurn+1, 300)
	putRead(fx, live, tapToolUseTurn+2, 200)
	s := allSeams()
	WrapServicesForScheduler(s, fx.rt, fx.options())

	require.NoError(t, s.ObserveTool(ctx, tapToolEvent(replayed, "Read", "", "")))
	require.Empty(t, fx.rt.session, "a session the registry has not seen live does not bind")
	require.Equal(t, int64(1), fx.counter(counterTapBindNotLive))

	reg.Touch(rtSession, fx.now()) // the live hook's route touches the registry before dispatch
	require.NoError(t, s.ObserveTool(ctx, tapToolEvent(live, "Read", "", "")))
	require.Equal(t, rtSession, fx.rt.session, "the live session's first hook binds it")
	require.Equal(t, core.Tokens(1_200), fx.rt.openSegTokens,
		"the persisted 700, the 300 observed while unbound, and the live read's 200")
	require.Equal(t, tapToolUseTurn+2, fx.rt.maxTurn)
}

// TestWrapServices_AHookOfAnotherSessionLeavesTheBoundOneAlone: binding on the first hook applies to
// a runtime bound to nothing. Rebinding a bound runtime stays a SessionStart's decision.
func TestWrapServices_AHookOfAnotherSessionLeavesTheBoundOneAlone(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	const bound core.SessionID = "sess-bound-first"
	fx.bind(bound)
	s := allSeams()
	WrapServicesForScheduler(s, fx.rt, fx.options())

	require.NoError(t, s.ObserveStop(context.Background(), tapEvent("Stop", rtSession), false))
	require.Equal(t, bound, fx.rt.session)
	require.Zero(t, fx.counter(counterTapBindFirstHook))
}
