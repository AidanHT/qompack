package observertest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/observer"
	"github.com/stretchr/testify/require"
)

// This file authors the fixtures and behaviour assertions of the observertest suite (§15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md): "ExtractSignals detects todo/test/git",
// plus the hot-path and degradation properties every L0 entry point owes the host. All are
// authored now, gated behind the same Rule W-1 stub probe as the rest of the suite, so SP-08
// inherits them rather than writing its own grader.

// The SessionStart source values Claude Code sends (00-ARCHITECTURE.md §5.3, §5.21). Exported
// because SP-08's own tests branch on the same four.
const (
	// SourceStartup is a fresh session.
	SourceStartup = "startup"
	// SourceResume is a resumed session.
	SourceResume = "resume"
	// SourceCompact is the branch that follows a compaction — the rehydration path.
	SourceCompact = "compact"
	// SourceClear is an explicit context clear.
	SourceClear = "clear"
)

// Fixture constants shared by every event builder below. None duplicates a config default (D11,
// §11.6), which matters because this is not a _test.go file.
const (
	fixtureSession    = "sess_observertest"
	fixtureToolUseID  = "toolu_observertest_0001"
	fixturePath       = "src/auth.ts"
	fixtureCWD        = "/repo"
	fixtureTranscript = "/repo/.claude/transcript.jsonl"
)

// The hook event names the payloads below carry.
const (
	hookPostToolUse      = "PostToolUse"
	hookUserPromptSubmit = "UserPromptSubmit"
	hookStop             = "Stop"
	hookSessionStart     = "SessionStart"
	hookSessionEnd       = "SessionEnd"
)

// toolUseEvent is a well-formed PostToolUse payload: a file read that touched fixturePath.
func toolUseEvent() observer.Event {
	return observer.Event{
		HookEventName:  hookPostToolUse,
		SessionID:      core.SessionID(fixtureSession),
		TranscriptPath: fixtureTranscript,
		CWD:            fixtureCWD,
		ToolName:       "Read",
		ToolUseID:      core.ToolUseID(fixtureToolUseID),
		ToolInput:      json.RawMessage(`{"file_path":"src/auth.ts"}`),
		ToolResponse:   json.RawMessage(`{"content":"export async function refreshToken() {}"}`),
	}
}

// userPromptEvent is a well-formed UserPromptSubmit payload. Its prompt is the text G2.3 requires
// be captured verbatim and never regenerated.
func userPromptEvent() observer.Event {
	return observer.Event{
		HookEventName:  hookUserPromptSubmit,
		SessionID:      core.SessionID(fixtureSession),
		TranscriptPath: fixtureTranscript,
		CWD:            fixtureCWD,
		Prompt:         "Fix the intermittent failures on the refresh endpoint.",
	}
}

// stopEvent is a well-formed Stop payload.
func stopEvent() observer.Event {
	return observer.Event{
		HookEventName:  hookStop,
		SessionID:      core.SessionID(fixtureSession),
		TranscriptPath: fixtureTranscript,
		CWD:            fixtureCWD,
	}
}

// sessionStartEvent is a well-formed SessionStart payload for the given source.
func sessionStartEvent(source string) observer.Event {
	return observer.Event{
		HookEventName:  hookSessionStart,
		SessionID:      core.SessionID(fixtureSession),
		TranscriptPath: fixtureTranscript,
		CWD:            fixtureCWD,
		Source:         source,
	}
}

// sessionEndEvent is a well-formed SessionEnd payload.
func sessionEndEvent() observer.Event {
	return observer.Event{
		HookEventName:  hookSessionEnd,
		SessionID:      core.SessionID(fixtureSession),
		TranscriptPath: fixtureTranscript,
		CWD:            fixtureCWD,
	}
}

// requireDoesNotBlock asserts an Output never stops the host from proceeding. Qompack observes; it
// does not veto. A hook that set Continue to false would turn a recording failure into a broken
// session, which §12.3 forbids in as many words.
func requireDoesNotBlock(t *testing.T, out observer.Output, what string) {
	t.Helper()
	if out.Continue != nil {
		require.True(t, *out.Continue, "%s must never stop the host from continuing", what)
	}
}

// runToolUseIsSilentCase asserts PostToolUse stays on the hot path: it reports no error for a
// well-formed event, never blocks the tool call, and never emits through the SessionStart or
// PreCompact injection channels, which belong to other hooks entirely.
func runToolUseIsSilentCase(t *testing.T, factory func(t *testing.T) observer.Observer) {
	t.Helper()
	out, err := factory(t).OnToolUse(context.Background(), toolUseEvent())
	require.NoError(t, err)
	requireDoesNotBlock(t, out, "PostToolUse")

	if out.HookSpecificOutput != nil {
		require.Equal(t, hookPostToolUse, out.HookSpecificOutput.HookEventName,
			"a hook must stamp its OWN event name")
		require.Empty(t, out.HookSpecificOutput.AdditionalContext,
			"additionalContext is SessionStart's channel, not PostToolUse's")
		require.Empty(t, out.HookSpecificOutput.CustomInstructions,
			"customInstructions is PreCompact's channel, not PostToolUse's")
	}
}

// runUserPromptCase asserts UserPromptSubmit records the prompt without blocking it. The verbatim
// capture itself (G2.3) is not observable through this interface — the prompt goes to the store,
// not to the Output — so SP-08 must assert it white-box in internal/observer.
func runUserPromptCase(t *testing.T, factory func(t *testing.T) observer.Observer) {
	t.Helper()
	out, err := factory(t).OnUserPrompt(context.Background(), userPromptEvent())
	require.NoError(t, err)
	requireDoesNotBlock(t, out, "UserPromptSubmit")
}

// runSessionStartSourceCase asserts every one of the four SessionStart sources is handled: the
// source switch lives in OnSessionStart and delegates (§5.21), so an unrecognized source must
// still produce a valid, non-blocking response rather than an error.
func runSessionStartSourceCase(t *testing.T, factory func(t *testing.T) observer.Observer) {
	t.Helper()
	ctx := context.Background()

	for _, source := range []string{SourceStartup, SourceResume, SourceCompact, SourceClear} {
		out, err := factory(t).OnSessionStart(ctx, sessionStartEvent(source))
		require.NoError(t, err, "source=%s must be handled", source)
		requireDoesNotBlock(t, out, "SessionStart")

		if out.HookSpecificOutput != nil {
			require.Equal(t, hookSessionStart, out.HookSpecificOutput.HookEventName,
				"source=%s: additionalContext only reaches the transcript when the event name matches", source)
		}
	}

	// An unknown source is a host change, not a crash: degrade, do not fail (§12.1, §12.3).
	out, err := factory(t).OnSessionStart(ctx, sessionStartEvent("a-source-that-does-not-exist-yet"))
	require.NoError(t, err, "an unrecognized source must degrade, never error")
	requireDoesNotBlock(t, out, "SessionStart")
}

// runStopCase asserts both Stop and SubagentStop are accepted. The subagent flag exists so that
// subagent detail is captured before the host double-compresses it (G10.1); which of the two a
// call was is not observable through the Output, so SP-08 must assert the capture white-box.
func runStopCase(t *testing.T, factory func(t *testing.T) observer.Observer) {
	t.Helper()
	ctx := context.Background()

	for _, subagent := range []bool{false, true} {
		out, err := factory(t).OnStop(ctx, stopEvent(), subagent)
		require.NoError(t, err, "OnStop(subagent=%v) must be handled", subagent)
		requireDoesNotBlock(t, out, "Stop")
	}
}

// runSessionEndCase asserts SessionEnd completes without reporting an error. It flushes the
// store, writes the session index and runs GC (§5.21), none of which may surface a failure into
// the host's shutdown path.
func runSessionEndCase(t *testing.T, factory func(t *testing.T) observer.Observer) {
	t.Helper()
	out, err := factory(t).OnSessionEnd(context.Background(), sessionEndEvent())
	require.NoError(t, err)
	requireDoesNotBlock(t, out, "SessionEnd")
}

// runDegradeCase is §12.3 — everything fails toward "do nothing". A zero Event carries no session
// id, no transcript path and no tool payload, which is exactly what a host-side contract change
// looks like from in here. Every entry point must return a usable Output and must not panic.
func runDegradeCase(t *testing.T, factory func(t *testing.T) observer.Observer) {
	t.Helper()
	ctx := context.Background()
	var empty observer.Event

	cases := map[string]func(o observer.Observer) (observer.Output, error){
		"OnToolUse":      func(o observer.Observer) (observer.Output, error) { return o.OnToolUse(ctx, empty) },
		"OnUserPrompt":   func(o observer.Observer) (observer.Output, error) { return o.OnUserPrompt(ctx, empty) },
		"OnStop":         func(o observer.Observer) (observer.Output, error) { return o.OnStop(ctx, empty, false) },
		"OnSessionStart": func(o observer.Observer) (observer.Output, error) { return o.OnSessionStart(ctx, empty) },
		"OnSessionEnd":   func(o observer.Observer) (observer.Output, error) { return o.OnSessionEnd(ctx, empty) },
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := call(factory(t))
			requireKnownError(t, err)
			requireDoesNotBlock(t, out, name)
		})
	}
}

// runReplayCase asserts one event delivered twice is not an error. Hooks are re-delivered in
// practice — a retried tool call, a resumed session — and dedup happens at the content level in
// the store, so a second delivery must be absorbed rather than rejected.
func runReplayCase(t *testing.T, factory func(t *testing.T) observer.Observer) {
	t.Helper()
	o := factory(t)
	ctx := context.Background()

	_, err := o.OnToolUse(ctx, toolUseEvent())
	require.NoError(t, err)
	_, err = o.OnToolUse(ctx, toolUseEvent())
	require.NoError(t, err, "the same tool_use_id delivered twice must be absorbed, not rejected")
}

// runExtractSignalsCase is §15's "ExtractSignals detects todo/test/git" row (G1.5). The payload
// shapes below are the canonical ones Claude Code sends today; SP-08 may recognize MORE shapes,
// but must recognize at least these, because they are what the scheduler's task-boundary trigger
// was specified against (Qompack.md §8.4). If a host-side shape change makes one of them wrong,
// the fix is to update this fixture in the same commit that updates the parser — not to loosen
// the assertion.
func runExtractSignalsCase(t *testing.T) {
	t.Helper()

	t.Run("todo_completion", func(t *testing.T) {
		e := toolUseEvent()
		e.ToolName = "TodoWrite"
		e.ToolInput = json.RawMessage(`{"todos":[{"content":"trace the timeout","status":"completed"},{"content":"write the fix","status":"in_progress"}]}`)
		got := observer.ExtractSignals(e)
		require.True(t, got.TodoCompleted, "a todo moving to completed is a task boundary")
	})

	t.Run("passing_test", func(t *testing.T) {
		e := toolUseEvent()
		e.ToolName = "Bash"
		e.ToolInput = json.RawMessage(`{"command":"go test ./..."}`)
		e.ToolResponse = json.RawMessage(`{"exit_code":0,"stdout":"ok  \tgithub.com/example/pkg\t0.42s\n"}`)
		got := observer.ExtractSignals(e)
		require.True(t, got.TestPassed, "a passing test run is a task boundary")
	})

	t.Run("git_commit", func(t *testing.T) {
		e := toolUseEvent()
		e.ToolName = "Bash"
		e.ToolInput = json.RawMessage(`{"command":"git commit -m \"extract the identity call\""}`)
		e.ToolResponse = json.RawMessage(`{"exit_code":0,"stdout":"[main 1a2b3c4] extract the identity call\n"}`)
		got := observer.ExtractSignals(e)
		require.True(t, got.GitCommit, "a commit is a task boundary")
	})

	t.Run("touched_paths", func(t *testing.T) {
		got := observer.ExtractSignals(toolUseEvent())
		require.Contains(t, got.Paths, fixturePath, "a file read reports the path it touched")
	})

	t.Run("an_ordinary_read_is_not_a_task_boundary", func(t *testing.T) {
		got := observer.ExtractSignals(toolUseEvent())
		require.False(t, got.TodoCompleted)
		require.False(t, got.TestPassed)
		require.False(t, got.GitCommit)
	})

	t.Run("a_failing_test_is_not_a_passing_one", func(t *testing.T) {
		e := toolUseEvent()
		e.ToolName = "Bash"
		e.ToolInput = json.RawMessage(`{"command":"go test ./..."}`)
		e.ToolResponse = json.RawMessage(`{"exit_code":1,"stdout":"FAIL\tgithub.com/example/pkg\t0.42s\n"}`)
		got := observer.ExtractSignals(e)
		require.False(t, got.TestPassed, "a failing test run closes nothing")
	})

	t.Run("a_zero_event_yields_no_signals", func(t *testing.T) {
		got := observer.ExtractSignals(observer.Event{})
		require.Equal(t, observer.Signals{}, got, "an empty payload must not manufacture a boundary")
	})
}
