package hookio_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
)

// fixtureDir is testdata/golden/contracts/hookio/input/, the frozen fixture set hand-authored by
// the subplan. These files are read-only contract fixtures and must never be rewritten by a test.
const fixtureDir = "../../testdata/golden/contracts/hookio/input"

const defaultTestLimit = 1 << 20 // 1 MiB, matching runtime.hotPath.maxPayloadBytes's default

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir, name))
	require.NoError(t, err, "frozen fixture %s must exist", name)
	return b
}

const (
	fixtureSessionID  = "sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A"
	fixtureTranscript = "/home/u/.claude/projects/proj/sess.jsonl"
	fixtureCWD        = "/home/u/proj"
)

// TestReadEvent_AllSevenHookPayloads reads every frozen fixture for the seven hook types
// (SessionStart contributes four fixtures, one per source value) and asserts every field parses
// exactly as the fixture's own JSON says.
func TestReadEvent_AllSevenHookPayloads(t *testing.T) {
	t.Run("PostToolUse", func(t *testing.T) {
		ev, raw, err := hookio.ReadEvent(bytes.NewReader(readFixture(t, "post_tool_use.json")), defaultTestLimit)
		require.NoError(t, err)
		require.NotEmpty(t, raw)
		require.Equal(t, "PostToolUse", ev.HookEventName)
		require.Equal(t, core.SessionID(fixtureSessionID), ev.SessionID)
		require.Equal(t, fixtureTranscript, ev.TranscriptPath)
		require.Equal(t, fixtureCWD, ev.CWD)
		require.Equal(t, "FileRead", ev.ToolName)
		require.Equal(t, core.ToolUseID("toolu_01A2B3C4D5E6F7G8H9J0K1L2"), ev.ToolUseID)
		require.JSONEq(t, `{"file_path":"src/auth.ts"}`, string(ev.ToolInput))
		require.JSONEq(t, `{"type":"text","file":{"numLines":84}}`, string(ev.ToolResponse))
		require.Empty(t, ev.Source)
		require.Empty(t, ev.Trigger)
		require.Empty(t, ev.Prompt)
		require.False(t, ev.StopHookActive)
		require.Empty(t, ev.Extra)
	})

	t.Run("UserPromptSubmit", func(t *testing.T) {
		ev, _, err := hookio.ReadEvent(bytes.NewReader(readFixture(t, "user_prompt_submit.json")), defaultTestLimit)
		require.NoError(t, err)
		require.Equal(t, "UserPromptSubmit", ev.HookEventName)
		require.Equal(t, core.SessionID(fixtureSessionID), ev.SessionID)
		require.Equal(t, fixtureTranscript, ev.TranscriptPath)
		require.Equal(t, fixtureCWD, ev.CWD)
		require.Equal(t, "Fix the intermittent 500s on POST /api/session/refresh.", ev.Prompt)
		require.Empty(t, ev.ToolName)
		require.Empty(t, ev.Extra)
	})

	for _, source := range []string{"startup", "resume", "compact", "clear"} {
		t.Run("SessionStart_"+source, func(t *testing.T) {
			ev, _, err := hookio.ReadEvent(
				bytes.NewReader(readFixture(t, "session_start_"+source+".json")), defaultTestLimit)
			require.NoError(t, err)
			require.Equal(t, "SessionStart", ev.HookEventName)
			require.Equal(t, core.SessionID(fixtureSessionID), ev.SessionID)
			require.Equal(t, fixtureTranscript, ev.TranscriptPath)
			require.Equal(t, fixtureCWD, ev.CWD)
			require.Equal(t, source, ev.Source)
			require.Empty(t, ev.Extra)
		})
	}

	t.Run("PreCompact", func(t *testing.T) {
		ev, _, err := hookio.ReadEvent(bytes.NewReader(readFixture(t, "pre_compact.json")), defaultTestLimit)
		require.NoError(t, err)
		require.Equal(t, "PreCompact", ev.HookEventName)
		require.Equal(t, core.SessionID(fixtureSessionID), ev.SessionID)
		require.Equal(t, fixtureTranscript, ev.TranscriptPath)
		require.Equal(t, fixtureCWD, ev.CWD)
		require.Equal(t, "auto", ev.Trigger)
		require.Empty(t, ev.Extra)
	})

	t.Run("Stop", func(t *testing.T) {
		ev, _, err := hookio.ReadEvent(bytes.NewReader(readFixture(t, "stop.json")), defaultTestLimit)
		require.NoError(t, err)
		require.Equal(t, "Stop", ev.HookEventName)
		require.Equal(t, core.SessionID(fixtureSessionID), ev.SessionID)
		require.False(t, ev.StopHookActive)
		require.Empty(t, ev.Extra)
	})

	t.Run("SubagentStop", func(t *testing.T) {
		ev, _, err := hookio.ReadEvent(bytes.NewReader(readFixture(t, "subagent_stop.json")), defaultTestLimit)
		require.NoError(t, err)
		require.Equal(t, "SubagentStop", ev.HookEventName)
		require.Equal(t, core.SessionID(fixtureSessionID), ev.SessionID)
		require.True(t, ev.StopHookActive)
		require.Empty(t, ev.Extra)
	})

	t.Run("SessionEnd", func(t *testing.T) {
		ev, _, err := hookio.ReadEvent(bytes.NewReader(readFixture(t, "session_end.json")), defaultTestLimit)
		require.NoError(t, err)
		require.Equal(t, "SessionEnd", ev.HookEventName)
		require.Equal(t, core.SessionID(fixtureSessionID), ev.SessionID)
		require.Equal(t, fixtureTranscript, ev.TranscriptPath)
		require.Equal(t, fixtureCWD, ev.CWD)
		require.Empty(t, ev.Extra)
	})
}

func TestReadEvent_UnknownFieldsPreserved(t *testing.T) {
	ev, _, err := hookio.ReadEvent(bytes.NewReader(readFixture(t, "unknown_fields.json")), defaultTestLimit)
	require.NoError(t, err)
	require.Equal(t, "PostToolUse", ev.HookEventName)
	require.Equal(t, core.SessionID("s1"), ev.SessionID)
	require.Equal(t, "Bash", ev.ToolName)

	require.Len(t, ev.Extra, 2)
	require.JSONEq(t, `{"a":1}`, string(ev.Extra["future_field"]))
	require.JSONEq(t, `"x"`, string(ev.Extra["another_new_one"]))

	// Also exercise a locally-built payload with an unknown field alongside every recognized one,
	// so the "not claimed by a struct tag" rule is tested against the full field set, not just the
	// fixture's small subset.
	payload := `{"hook_event_name":"PostToolUse","session_id":"s","transcript_path":"t","cwd":"c",` +
		`"source":"startup","trigger":"auto","tool_name":"Bash","tool_use_id":"id",` +
		`"tool_input":{},"tool_response":{},"prompt":"p","stop_hook_active":true,` +
		`"brand_new_field":{"nested":[1,2,3]}}`
	ev2, _, err := hookio.ReadEvent(bytes.NewReader([]byte(payload)), defaultTestLimit)
	require.NoError(t, err)
	require.Len(t, ev2.Extra, 1)
	require.JSONEq(t, `{"nested":[1,2,3]}`, string(ev2.Extra["brand_new_field"]))
}

func TestReadEvent_MissingFieldsNeverPanic(t *testing.T) {
	require.NotPanics(t, func() {
		ev, raw, err := hookio.ReadEvent(bytes.NewReader([]byte(`{}`)), defaultTestLimit)
		require.NoError(t, err)
		require.Equal(t, []byte(`{}`), raw)
		require.Equal(t, hookio.Event{}, ev)
	})
}

func TestReadEvent_NullFields(t *testing.T) {
	ev, _, err := hookio.ReadEvent(
		bytes.NewReader([]byte(`{"tool_input":null,"prompt":null}`)), defaultTestLimit)
	require.NoError(t, err)
	require.Nil(t, ev.ToolInput, "a JSON null tool_input must normalize to nil, not the 4-byte literal")
	require.Empty(t, ev.Prompt)
	require.Equal(t, hookio.Event{}, ev, "null-valued claimed fields must leave the Event at its zero value")
}

func TestReadEvent_LimitExceeded(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 100)
	ev, raw, err := hookio.ReadEvent(bytes.NewReader(payload), 16)
	require.ErrorIs(t, err, core.ErrBudget)
	require.Equal(t, hookio.Event{}, ev)
	require.NotNil(t, raw)
	require.LessOrEqual(t, len(raw), 16)
}

func TestReadEvent_LimitExactFitSucceeds(t *testing.T) {
	payload := []byte(`{"cwd":"c"}`)
	ev, _, err := hookio.ReadEvent(bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	require.Equal(t, "c", ev.CWD)
}

func TestReadEvent_MalformedJSONReturnsRawAndError(t *testing.T) {
	payload := []byte(`{"hook_event_name": not valid json`)
	ev, raw, err := hookio.ReadEvent(bytes.NewReader(payload), defaultTestLimit)
	require.Error(t, err)
	require.Equal(t, hookio.Event{}, ev)
	require.Equal(t, payload, raw, "the raw bytes must be returned even on malformed JSON")
}

func TestReadEvent_TopLevelNullNeverPanics(t *testing.T) {
	require.NotPanics(t, func() {
		ev, _, err := hookio.ReadEvent(bytes.NewReader([]byte(`null`)), defaultTestLimit)
		require.NoError(t, err)
		require.Equal(t, hookio.Event{}, ev)
	})
}

func TestReadEvent_ExtraIsValidJSONPerKey(t *testing.T) {
	ev, _, err := hookio.ReadEvent(bytes.NewReader(readFixture(t, "unknown_fields.json")), defaultTestLimit)
	require.NoError(t, err)
	for k, v := range ev.Extra {
		require.True(t, json.Valid(v), "Extra[%q] must be valid JSON", k)
	}
}
