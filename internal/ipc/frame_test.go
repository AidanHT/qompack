package ipc_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
)

// observeToolGolden and responseReplyGolden are the two frame fixtures this commit creates. Both
// pin REALITY — the exact bytes EncodeRequest/EncodeResponse produce over the shipped Request/
// Response/hookio.Event/hookio.Output types — not an illustrative shorthand. hookio.Event carries
// no omitempty on any field (internal/hookio/event.go, SP-01-owned, out of this package's reach),
// so a Request whose Event sets only four fields still serializes with all twelve of Event's keys.
// If hookio's owner ever adds omitempty to Event, this fixture is what documents the resulting
// wire-format change — it is not something internal/ipc gets to decide unilaterally.
const (
	observeToolGolden   = "../../testdata/golden/contracts/ipc/observe_tool.ndjson"
	responseReplyGolden = "../../testdata/golden/contracts/ipc/response_reply.ndjson"
)

// fixedObserveToolRequest is the exact Request task-1-spec.md names: Op observe.tool, Session
// "sess-01", TS 1730000000000, and an Event with only HookEventName/SessionID/ToolName/ToolUseID
// set — everything else at Event's zero value.
func fixedObserveToolRequest() ipc.Request {
	return ipc.Request{
		Op:      ipc.OpObserveTool,
		Session: core.SessionID("sess-01"),
		TS:      core.UnixMilli(1730000000000),
		Event: &hookio.Event{
			HookEventName: "PostToolUse",
			SessionID:     core.SessionID("sess-01"),
			ToolName:      "Read",
			ToolUseID:     core.ToolUseID("tu_1"),
		},
	}
}

// TestEncodeRequestByteExact pins EncodeRequest's output against the golden fixture this commit
// creates: compact JSON, the short Request keys (op/s/t with omitempty r/e/x), SetEscapeHTML(false),
// and exactly one trailing '\n'. The Event sub-object's own shape (all twelve keys, not just the
// four set here) is inherited from hookio.Event's tag set, not chosen by this test — see the
// comment on observeToolGolden above.
func TestEncodeRequestByteExact(t *testing.T) {
	want, err := os.ReadFile(observeToolGolden)
	require.NoError(t, err, "golden fixture missing: %s", observeToolGolden)

	got, err := ipc.EncodeRequest(fixedObserveToolRequest())
	require.NoError(t, err)

	require.Equal(t, string(want), string(got))
	require.Equal(t, 1, bytes.Count(got, []byte("\n")), "exactly one trailing newline, never two")
	require.True(t, bytes.HasSuffix(got, []byte("\n")))
}

// TestEncodeResponseByteExact pins EncodeResponse the same way, against a Reply response carrying
// an Output — the shape a UserPromptSubmit/status reply actually returns.
func TestEncodeResponseByteExact(t *testing.T) {
	want, err := os.ReadFile(responseReplyGolden)
	require.NoError(t, err, "golden fixture missing: %s", responseReplyGolden)

	got, err := ipc.EncodeResponse(fixedReplyResponse())
	require.NoError(t, err)

	require.Equal(t, string(want), string(got))
	require.Equal(t, 1, bytes.Count(got, []byte("\n")))
}

func fixedReplyResponse() ipc.Response {
	return ipc.Response{
		OK: true,
		Output: &hookio.Output{
			HookSpecificOutput: &hookio.HSO{
				HookEventName:     "SessionStart",
				AdditionalContext: "sentinel-token-abc",
			},
		},
	}
}

// TestEncodeRequest_DoesNotEscapeHTML asserts SetEscapeHTML(false) actually took effect: a naive
// json.Marshal would turn "<" into "<" inside Event.Prompt, which would corrupt a captured
// prompt or tool result containing a code snippet.
func TestEncodeRequest_DoesNotEscapeHTML(t *testing.T) {
	req := ipc.Request{
		Op:      ipc.OpObservePrompt,
		Session: core.SessionID("s"),
		TS:      1,
		Event:   &hookio.Event{Prompt: `<script>alert(1)&2</script>`},
	}
	got, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	require.Contains(t, string(got), `<script>alert(1)&2</script>`,
		"SetEscapeHTML(false) must leave HTML metacharacters literal, never \\u003c-style escapes")
}

// rawMessageNullIsNil is a cmp.Comparer for json.RawMessage that treats a nil RawMessage and the
// literal 4-byte "null" as equal. This is not laxness in the test — it documents a real,
// hookio-owned quirk: hookio.Event's ToolInput/ToolResponse have no omitempty (event.go), so a nil
// value encodes as the JSON literal null, and encoding/json's RawMessage.UnmarshalJSON stores that
// literal back verbatim rather than nil (hookio's own ReadEvent normalizes this via an unexported
// normalizeRawNull, for exactly this reason). DecodeRequest is a generic decoder with no
// Event-specific knowledge, so it does not — and should not — apply that normalization itself.
var rawMessageNullIsNil = cmp.Comparer(func(a, b json.RawMessage) bool {
	isNil := func(m json.RawMessage) bool { return len(m) == 0 || string(m) == "null" }
	if isNil(a) && isNil(b) {
		return true
	}
	return bytes.Equal(a, b)
})

// TestDecodeRequestRoundTrip asserts DecodeRequest(EncodeRequest(r)) reproduces r exactly, over a
// wide range of generated requests including the optional Event and the optional Raw.
//
// Raw is drawn as `observe stop --subagent`'s own dispatch payload, agent name and all. That shape
// matters more than an arbitrary blob: hookio.Event.Extra is tagged `json:"-"` and does NOT
// survive this round trip, so Raw is the only field carrying what the hook client parsed out of
// the payload — the subagent's name included — across to the daemon. A Raw that lost a key here
// would name every subagent capture "subagent".
func TestDecodeRequestRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		req := ipc.Request{
			Op:      rapid.SampledFrom(ipc.KnownOps()).Draw(rt, "op"),
			Session: core.SessionID(rapid.StringMatching(`[a-zA-Z0-9_-]{1,24}`).Draw(rt, "session")),
			TS:      core.UnixMilli(rapid.Int64Range(0, 4102444800000).Draw(rt, "ts")),
			Reply:   rapid.Bool().Draw(rt, "reply"),
		}
		if rapid.Bool().Draw(rt, "hasEvent") {
			req.Event = &hookio.Event{
				HookEventName:  rapid.StringMatching(`[A-Za-z]{1,20}`).Draw(rt, "hookEventName"),
				SessionID:      req.Session,
				ToolName:       rapid.StringMatching(`[A-Za-z]{0,20}`).Draw(rt, "toolName"),
				StopHookActive: rapid.Bool().Draw(rt, "stopHookActive"),
			}
		}
		if rapid.Bool().Draw(rt, "hasRaw") {
			agent := rapid.StringMatching(`[a-zA-Z0-9_-]{1,24}`).Draw(rt, "agent")
			req.Raw = json.RawMessage(`{"subagent":true,"agent":"` + agent + `"}`)
		}

		encoded, err := ipc.EncodeRequest(req)
		require.NoError(rt, err)

		decoded, err := ipc.DecodeRequest(encoded)
		require.NoError(rt, err)

		if diff := cmp.Diff(req, decoded, rawMessageNullIsNil); diff != "" {
			rt.Fatalf("round trip mismatch (-want +got):\n%s", diff)
		}
	})
}

// TestDecodeResponseRoundTrip is DecodeRequest's counterpart for Response.
func TestDecodeResponseRoundTrip(t *testing.T) {
	want := ipc.Response{
		OK:  false,
		Err: "contract: session_start.fires",
		Output: &hookio.Output{
			SystemMessage: "degraded",
		},
	}
	encoded, err := ipc.EncodeResponse(want)
	require.NoError(t, err)

	got, err := ipc.DecodeResponse(encoded)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestDecodeRequest_RejectsMalformedJSON asserts a decode failure is an ordinary error, never a
// panic, for input a fuzzer or a corrupted spool line might produce.
func TestDecodeRequest_RejectsMalformedJSON(t *testing.T) {
	_, err := ipc.DecodeRequest([]byte(`{not json`))
	require.Error(t, err)
}

// TestLineReaderRejectsOversize is task-1-spec's table row verbatim: a 100-byte line against a
// 64-byte maxLine fails with ErrLineTooLong, and the reader resynchronizes so the following
// 10-byte line still reads correctly.
func TestLineReaderRejectsOversize(t *testing.T) {
	long := strings.Repeat("a", 100) + "\n"
	short := strings.Repeat("b", 10) + "\n"
	lr := ipc.NewLineReader(strings.NewReader(long+short), 64)

	_, err := lr.ReadLine()
	require.ErrorIs(t, err, ipc.ErrLineTooLong)

	line, err := lr.ReadLine()
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("b", 10), string(line))
}

// TestLineReader_AcceptsLineAtExactLimit asserts the boundary itself is not rejected: maxLine is
// an inclusive ceiling, not an exclusive one.
func TestLineReader_AcceptsLineAtExactLimit(t *testing.T) {
	payload := strings.Repeat("c", 20)
	lr := ipc.NewLineReader(strings.NewReader(payload+"\n"), len(payload)+1)

	line, err := lr.ReadLine()
	require.NoError(t, err)
	require.Equal(t, payload, string(line))
}

// TestLineReader_HandlesLineLargerThanTheInternalBuffer proves the ReadSlice-based growth loop
// actually accumulates across multiple internal bufio fills rather than only handling a line that
// fits in one read.
func TestLineReader_HandlesLineLargerThanTheInternalBuffer(t *testing.T) {
	payload := strings.Repeat("d", 10000)
	lr := ipc.NewLineReader(strings.NewReader(payload+"\n"), 20000)

	line, err := lr.ReadLine()
	require.NoError(t, err)
	require.Equal(t, payload, string(line))
}

// TestLineReader_ZeroMaxLineDefaultsToMaxLineBytes asserts a caller that forgets to set maxLine
// gets the protocol ceiling rather than a reader that rejects every line.
func TestLineReader_ZeroMaxLineDefaultsToMaxLineBytes(t *testing.T) {
	lr := ipc.NewLineReader(strings.NewReader("hi\n"), 0)
	line, err := lr.ReadLine()
	require.NoError(t, err)
	require.Equal(t, "hi", string(line))
}

// TestLineReader_EOFAtStreamEnd asserts a clean stream end is reported as an ordinary error (io.EOF
// or a wrap of it), never a panic and never ErrLineTooLong.
func TestLineReader_EOFAtStreamEnd(t *testing.T) {
	lr := ipc.NewLineReader(strings.NewReader(""), 64)
	_, err := lr.ReadLine()
	require.Error(t, err)
	require.NotErrorIs(t, err, ipc.ErrLineTooLong)
}
