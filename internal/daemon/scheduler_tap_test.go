package daemon

// The plan's ten scheduler_tap_test.go cases plus one for the once-per-session panic recovery.
// Every case wraps a Services value through WrapServicesForScheduler over a real runtime and
// the in-memory doubles, then calls the decorated seams the way SP-05's routes do.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// Distinctive inner results, one per decorated seam.
var (
	errTapStart  = errors.New("inner: session start")
	errTapTool   = errors.New("inner: observe tool")
	errTapStop   = errors.New("inner: observe stop")
	errTapPrompt = errors.New("inner: observe prompt")
	errTapEnd    = errors.New("inner: session end")
)

const (
	tapToolUseID   core.ToolUseID = "toolu_tap_1"
	tapToolUseTurn core.TurnIndex = 9
)

// tapEvent builds the hook payload for one seam.
func tapEvent(name string, sess core.SessionID) hookio.Event {
	return hookio.Event{HookEventName: name, SessionID: sess}
}

// tapToolEvent is a PostToolUse payload for id with an optional tool input/response.
func tapToolEvent(id core.ToolUseID, tool, input, response string) hookio.Event {
	e := hookio.Event{HookEventName: "PostToolUse", SessionID: rtSession, ToolName: tool, ToolUseID: id}
	if input != "" {
		e.ToolInput = json.RawMessage(input)
	}
	if response != "" {
		e.ToolResponse = json.RawMessage(response)
	}
	return e
}

// tapRecord stores a tool-use record for id at turn 9 and returns it.
func tapRecord(fx *rtFixture, id core.ToolUseID, tool string, tokens core.Tokens) store.ToolUseRecord {
	rec := store.ToolUseRecord{
		ID: id, Session: rtSession, Turn: tapToolUseTurn, TS: fx.now() + 5_000, Tool: tool,
		ArgsPreview: "read src/a.go", Path: "src/a.go", Tokens: tokens,
	}
	fx.store.put(rec)
	return rec
}

// fnPtr identifies a function value for the "same pointer" assertions.
func fnPtr(v any) uintptr { return reflect.ValueOf(v).Pointer() }

// tapReadOnly reads runtime state under the lock, for assertions made from inside inner seams.
func tapReadOnly[T any](r *schedRuntime, read func(r *schedRuntime) T) T {
	r.mu.Lock()
	defer r.mu.Unlock()
	return read(r)
}

func TestWrapServices_CallsInnerSeamsFirst(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	r := fx.rt
	rec := tapRecord(fx, tapToolUseID, "Read", 1_200)
	var order []string
	s := &Services{
		SessionStart: func(_ context.Context, e hookio.Event) (hookio.Output, error) {
			order = append(order, "start")
			require.Equal(t, core.SessionID(""), tapReadOnly(r, func(r *schedRuntime) core.SessionID { return r.session }),
				"the tap must not have bound before the inner seam ran")
			return hookio.Output{SystemMessage: "inner-start"}, errTapStart
		},
		ObserveTool: func(_ context.Context, e hookio.Event) error {
			order = append(order, "tool")
			require.Zero(t, tapReadOnly(r, func(r *schedRuntime) int { return r.hist.seen }),
				"the tap must not have observed before the inner seam ran")
			return errTapTool
		},
		ObserveStop: func(_ context.Context, e hookio.Event, subagent bool) error {
			order = append(order, "stop")
			require.False(t, subagent)
			round := tapReadOnly(r, func(r *schedRuntime) bool { _, ok := r.rounds[tapToolUseTurn]; return ok })
			require.False(t, round, "the tap must not have recorded the round before the inner seam ran")
			return errTapStop
		},
		ObservePrompt: func(_ context.Context, e hookio.Event) (hookio.Output, error) {
			order = append(order, "prompt")
			require.Equal(t, rec.TS, tapReadOnly(r, func(r *schedRuntime) core.UnixMilli { return r.lastRequestStartTS }),
				"the tap must not have re-anchored the request start before the inner seam ran")
			return hookio.Output{SystemMessage: "inner-prompt"}, errTapPrompt
		},
		SessionEnd: func(_ context.Context, e hookio.Event) error {
			order = append(order, "end")
			require.Zero(t, fx.counter(counterPersist), "the tap must not have persisted before the inner seam ran")
			return errTapEnd
		},
	}
	WrapServicesForScheduler(s, r, fx.options())
	ctx := context.Background()

	out, err := s.SessionStart(ctx, tapEvent("SessionStart", rtSession))
	require.Equal(t, hookio.Output{SystemMessage: "inner-start"}, out, "(hookio.Output, error) passes through unchanged")
	require.ErrorIs(t, err, errTapStart)
	require.Equal(t, rtSession, r.session, "…and the tap still ran")

	require.ErrorIs(t, s.ObserveTool(ctx, tapToolEvent(tapToolUseID, "Read", "", "")), errTapTool, "error-only seams pass the inner error through")
	require.Equal(t, 1, r.hist.seen)

	require.ErrorIs(t, s.ObserveStop(ctx, tapEvent("Stop", rtSession), false), errTapStop)
	_, round := r.rounds[tapToolUseTurn]
	require.True(t, round)

	out, err = s.ObservePrompt(ctx, tapEvent("UserPromptSubmit", rtSession))
	require.Equal(t, hookio.Output{SystemMessage: "inner-prompt"}, out)
	require.ErrorIs(t, err, errTapPrompt)
	require.Equal(t, fx.now(), r.lastRequestStartTS)

	require.ErrorIs(t, s.SessionEnd(ctx, tapEvent("SessionEnd", rtSession)), errTapEnd)
	require.Equal(t, int64(2), fx.counter(counterPersist), "Persist, then Close's own Persist")
	require.Equal(t, []string{"start", "tool", "stop", "prompt", "end"}, order)
}

func TestWrapServices_UndecoratedSeamsUntouched(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	preCompact := func(context.Context, hookio.Event) (hookio.Output, error) { return hookio.Empty(), nil }
	rehydrate := func(context.Context, hookio.Event) (hookio.Output, error) { return hookio.Empty(), nil }
	mcp := func(context.Context) bool { return true }
	statusExtra := func(context.Context) (json.RawMessage, error) { return nil, nil }
	mode := func() contract.Mode { return contract.ModeFull }
	start := func(context.Context, hookio.Event) (hookio.Output, error) { return hookio.Empty(), nil }
	tool := func(context.Context, hookio.Event) error { return nil }
	stop := func(context.Context, hookio.Event, bool) error { return nil }
	prompt := func(context.Context, hookio.Event) (hookio.Output, error) { return hookio.Empty(), nil }
	end := func(context.Context, hookio.Event) error { return nil }
	s := &Services{
		PreCompact: preCompact, Rehydrate: rehydrate, MCPInitialized: mcp, StatusExtra: statusExtra, Mode: mode,
		SessionStart: start, ObserveTool: tool, ObserveStop: stop, ObservePrompt: prompt, SessionEnd: end,
	}
	WrapServicesForScheduler(s, fx.rt, fx.options())

	require.Equal(t, fnPtr(preCompact), fnPtr(s.PreCompact))
	require.Equal(t, fnPtr(rehydrate), fnPtr(s.Rehydrate))
	require.Equal(t, fnPtr(mcp), fnPtr(s.MCPInitialized))
	require.Equal(t, fnPtr(statusExtra), fnPtr(s.StatusExtra))
	require.Equal(t, fnPtr(mode), fnPtr(s.Mode), "Services.Mode is SP-05's and must not be shadowed")

	require.NotEqual(t, fnPtr(start), fnPtr(s.SessionStart), "the five L0 seams are decorated")
	require.NotEqual(t, fnPtr(tool), fnPtr(s.ObserveTool))
	require.NotEqual(t, fnPtr(stop), fnPtr(s.ObserveStop))
	require.NotEqual(t, fnPtr(prompt), fnPtr(s.ObservePrompt))
	require.NotEqual(t, fnPtr(end), fnPtr(s.SessionEnd))
}

func TestWrapServices_NilInnerSeamsTolerated(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	s := &Services{}
	WrapServicesForScheduler(s, fx.rt, fx.options())
	ctx := context.Background()

	out, err := s.SessionStart(ctx, tapEvent("SessionStart", rtSession))
	require.NoError(t, err)
	require.Equal(t, hookio.Output{}, out)
	require.Equal(t, rtSession, fx.rt.session)
	require.Equal(t, fx.now(), fx.rt.lastActivity, "NotifyActivity still observed")

	fx.clock.Advance(time.Second)
	require.NoError(t, s.ObserveTool(ctx, tapToolEvent(tapToolUseID, "Read", "", "")))
	require.Equal(t, fx.now(), fx.rt.lastActivity)
	require.NoError(t, s.ObserveStop(ctx, tapEvent("Stop", rtSession), false))
	out, err = s.ObservePrompt(ctx, tapEvent("UserPromptSubmit", rtSession))
	require.NoError(t, err)
	require.Equal(t, hookio.Output{}, out)
	require.NoError(t, s.SessionEnd(ctx, tapEvent("SessionEnd", rtSession)))
	require.Zero(t, fx.log.count(logLoud))
}

func TestWrapServices_ObserveToolDrivesObserve(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	rec := tapRecord(fx, tapToolUseID, "Read", 1_200)
	s := &Services{}
	WrapServicesForScheduler(s, fx.rt, fx.options())

	e := tapToolEvent(tapToolUseID, "Read", `{"file_path":"src/a.go"}`, "")
	require.NoError(t, s.ObserveTool(context.Background(), e))

	r := fx.rt
	require.Equal(t, tapToolUseTurn, r.maxTurn, "Observe ran at the record's turn")
	require.Equal(t, 1, r.hist.seen, "exactly one observation")
	require.Equal(t, []string{"Read"}, r.hist.recent.tools, "features derived from the record's tool")
	require.Equal(t, rec.TS, r.hist.lastTS, "…and its timestamp")
	require.Contains(t, r.hist.recent.paths[0], paths.Key("src/a.go"), "…and the event's paths")
	require.Equal(t, []string{"read src/a.go"}, r.hist.recent.text, "…and its ArgsPreview through observeText")
	require.NotEmpty(t, r.det.State().Posterior)
	require.Equal(t, rec.TS, r.lastActivity)
	require.Equal(t, rec.TS, r.lastAPICallTS)
	require.Equal(t, rec.TS, r.lastRequestStartTS, "a tool result is the tightest request-start anchor the hook surface offers")
	require.Equal(t, rec.Tokens, r.openSegTokens, "rec.Tokens folded into the open segment")
	require.Equal(t, rec.Tokens, r.contextTokens)
	require.Equal(t, []core.ToolUseID{tapToolUseID}, fx.store.toolUseCalls, "one store lookup")
	require.Zero(t, fx.counter(counterTapNoRecord))
}

func TestWrapServices_ObserveStopRecordsRoundBoundary(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	rec := tapRecord(fx, tapToolUseID, "Read", 100)
	s := &Services{}
	WrapServicesForScheduler(s, fx.rt, fx.options())
	ctx := context.Background()
	r := fx.rt

	require.NoError(t, s.ObserveTool(ctx, tapToolEvent(tapToolUseID, "Read", "", "")))
	_, round := r.rounds[tapToolUseTurn]
	require.False(t, round, "ObserveTool at turn 9 does not add a round boundary")

	fx.clock.Advance(30 * time.Second)
	require.NoError(t, s.ObserveStop(ctx, tapEvent("Stop", rtSession), false))
	_, round = r.rounds[tapToolUseTurn]
	require.True(t, round, "Stop is the one seam that fires once per assistant turn")
	require.Equal(t, 2, r.hist.seen, "Stop is observed too")
	require.Equal(t, fx.now(), r.lastActivity)
	require.Equal(t, rec.TS, r.lastRequestStartTS, "Stop never anchors the request start: it fires after generation")

	before := len(r.rounds)
	fx.clock.Advance(time.Second)
	require.NoError(t, s.ObserveStop(ctx, tapEvent("SubagentStop", rtSession), true))
	require.Len(t, r.rounds, before, "a subagent's Stop is not a main-agent round boundary")
	require.Equal(t, fx.now(), r.lastActivity, "…but it is activity")
}

func TestWrapServices_BoundarySignalsCloseSegment(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	s := &Services{}
	WrapServicesForScheduler(s, fx.rt, fx.options())
	ctx := context.Background()

	cases := []struct {
		id       core.ToolUseID
		tool     string
		input    string
		response string
		cause    string
	}{
		{"toolu_todo", "TodoWrite", `{"todos":[{"content":"x","status":"completed"}]}`, "", causeTodo},
		{"toolu_test", "Bash", `{"command":"go test ./internal/..."}`, `"ok  \tgithub.com/qompack/qompack/internal/x\t0.2s\n"`, causeTest},
		{"toolu_commit", "Bash", `{"command":"git commit -m \"feat: x\""}`, `"[main 1a2b3c] feat: x\n 1 file changed"`, causeCommit},
		{"toolu_plain", "Read", `{"file_path":"src/a.go"}`, "", ""},
	}
	for _, tc := range cases {
		tapRecord(fx, tc.id, tc.tool, 50)
		require.NoError(t, s.ObserveTool(ctx, tapToolEvent(tc.id, tc.tool, tc.input, tc.response)))
	}
	for _, cause := range []string{causeTodo, causeTest, causeCommit} {
		require.Equal(t, int64(1), fx.counter(counterTapBoundaryPrefix+cause), "CloseSegmentOn called once with cause %q", cause)
	}
	require.Equal(t, int64(3), fx.counter(counterTapBoundary), "one boundary per boundary event, none for the plain read")
	require.Equal(t, 4, fx.rt.hist.seen)
}

func TestWrapServices_SessionStartBindsAndSessionEndCloses(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t, func(fx *rtFixture) { fx.writer = newFakeWriter(fx.store.segs) })
	fx.rt.mu.Lock()
	fx.rt.draft = &checkpoint.Draft{}
	fx.rt.mu.Unlock()
	s := &Services{}
	WrapServicesForScheduler(s, fx.rt, fx.options())
	ctx := context.Background()

	e := tapEvent("SessionStart", "sess-7f3a")
	e.Source = "startup"
	e.Extra = map[string]json.RawMessage{
		"model":    json.RawMessage(`"claude-opus-4-1"`),
		"agent_id": json.RawMessage(`"researcher"`),
	}
	_, err := s.SessionStart(ctx, e)
	require.NoError(t, err)
	r := fx.rt
	require.Equal(t, core.SessionID("sess-7f3a"), r.session, "BindSession with the event's id")
	require.Equal(t, "claude-opus-4-1", r.model)
	require.True(t, r.subagent)
	require.Equal(t, "subagent_5m", r.regime.Source, "agent_id present ⇒ the subagent rung")
	require.Equal(t, fx.now(), r.lastActivity)
	require.Contains(t, r.rounds, core.TurnIndex(0))
	require.Zero(t, fx.counter(counterPersist))

	require.NoError(t, s.SessionEnd(ctx, tapEvent("SessionEnd", "sess-7f3a")))
	require.Equal(t, int64(2), fx.counter(counterPersist), "Persist, then CloseSchedulerRuntime (which persists again)")
	require.Equal(t, 1, fx.writer.abortCalls, "Close aborted the open draft")
	require.FileExists(t, fx.statePath(stateFileBOCD))
	require.FileExists(t, fx.statePath(stateFileScheduler))
	doc, err := decodeSchedulerState(mustRead(t, fx.statePath(stateFileScheduler)))
	require.NoError(t, err)
	require.Equal(t, core.SessionID("sess-7f3a"), doc.Session)
	require.Equal(t, "subagent_5m", doc.RegimeSource)
}

func TestWrapServices_PromptSeamDoesNoStoreIO(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	tapRecord(fx, tapToolUseID, "Read", 100)
	inner := func(context.Context, hookio.Event) (hookio.Output, error) {
		return hookio.Output{SystemMessage: "prompt"}, nil
	}
	s := &Services{ObservePrompt: inner}
	WrapServicesForScheduler(s, fx.rt, fx.options())

	rangeCallsAtBind := len(fx.store.segs.rangeCalls) // the bind's own recount, before the prompt
	fx.clock.Advance(2 * time.Second)
	out, err := s.ObservePrompt(context.Background(), hookio.Event{HookEventName: "UserPromptSubmit", SessionID: rtSession, Prompt: "hi"})
	require.NoError(t, err)
	require.Equal(t, hookio.Output{SystemMessage: "prompt"}, out)
	require.Empty(t, fx.store.toolUseCalls, "zero store calls inside the 250 ms reply deadline")
	require.Len(t, fx.store.segs.rangeCalls, rangeCallsAtBind, "the prompt seam reads no segments")
	require.Zero(t, fx.rt.hist.seen, "no BOCD update")
	require.Equal(t, fx.now(), fx.rt.lastActivity, "only NotifyActivity…")
	require.Equal(t, fx.now(), fx.rt.lastRequestStartTS, "…and the request-start anchor")
}

func TestWrapServices_MissingRecordCounted(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	s := &Services{ObserveTool: func(context.Context, hookio.Event) error { return errTapTool }}
	WrapServicesForScheduler(s, fx.rt, fx.options())

	fx.clock.Advance(3 * time.Second)
	err := s.ObserveTool(context.Background(), tapToolEvent("toolu_missing", "Read", "", ""))
	require.ErrorIs(t, err, errTapTool, "the inner result is returned unchanged")
	require.Equal(t, int64(1), fx.counter(counterTapNoRecord))
	require.Zero(t, fx.rt.hist.seen, "no Observe for that event")
	require.Equal(t, fx.now(), fx.rt.lastActivity, "the timestamp work still happens")
	require.Equal(t, fx.now(), fx.rt.lastRequestStartTS)
	require.Zero(t, fx.log.count(logLoud))

	fx.store.toolUseErr = errors.New("index unreadable")
	require.ErrorIs(t, s.ObserveTool(context.Background(), tapToolEvent("toolu_other", "Read", "", "")), errTapTool)
	require.Equal(t, int64(1), fx.counter(counterTapNoRecord), "only ErrNotFound is a missing record")
	require.Zero(t, fx.rt.hist.seen)
}

func TestWrapServices_NilRuntimeIsNoOp(t *testing.T) {
	t.Parallel()
	start := func(context.Context, hookio.Event) (hookio.Output, error) { return hookio.Empty(), nil }
	tool := func(context.Context, hookio.Event) error { return nil }
	stop := func(context.Context, hookio.Event, bool) error { return nil }
	prompt := func(context.Context, hookio.Event) (hookio.Output, error) { return hookio.Empty(), nil }
	end := func(context.Context, hookio.Event) error { return nil }
	s := &Services{SessionStart: start, ObserveTool: tool, ObserveStop: stop, ObservePrompt: prompt, SessionEnd: end}
	WrapServicesForScheduler(s, nil, SchedulerRuntimeOptions{})
	require.Equal(t, fnPtr(start), fnPtr(s.SessionStart))
	require.Equal(t, fnPtr(tool), fnPtr(s.ObserveTool))
	require.Equal(t, fnPtr(stop), fnPtr(s.ObserveStop))
	require.Equal(t, fnPtr(prompt), fnPtr(s.ObservePrompt))
	require.Equal(t, fnPtr(end), fnPtr(s.SessionEnd))

	empty := &Services{}
	WrapServicesForScheduler(empty, nil, SchedulerRuntimeOptions{})
	require.Nil(t, empty.SessionStart)
	require.Nil(t, empty.ObserveTool)
}

func TestWrapServices_PanicRecoveredOncePerSession(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	s := &Services{ObserveTool: func(context.Context, hookio.Event) error { return errTapTool }}
	WrapServicesForScheduler(s, fx.rt, fx.options())
	fx.rt.mu.Lock()
	fx.rt.st = nil // the record lookup now dereferences a nil store: a panic inside the tap
	fx.rt.mu.Unlock()

	for range 3 {
		require.ErrorIs(t, s.ObserveTool(context.Background(), tapToolEvent(tapToolUseID, "Read", "", "")), errTapTool,
			"the inner seam's result is returned unchanged")
	}
	require.Equal(t, 1, fx.log.count(logLoud), "one Loud per session, not per event")
	require.Equal(t, msgTapPanic, fx.log.msgs(logLoud)[0])
	require.Equal(t, int64(3), fx.counter(counterTapPanic))

	_, err := s.SessionStart(context.Background(), tapEvent("SessionStart", "sess-next"))
	require.NoError(t, err)
	require.ErrorIs(t, s.ObserveTool(context.Background(), tapToolEvent(tapToolUseID, "Read", "", "")), errTapTool)
	require.Equal(t, 2, fx.log.count(logLoud), "a new session may log once more")
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	return b
}
