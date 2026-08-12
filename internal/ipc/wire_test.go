package ipc_test

import (
	"encoding/json"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/stretchr/testify/require"
)

// TestACKAndNAK_AreTheDesignsControlBytes pins §2.4's one-byte handshake. These are wire format:
// a daemon built from a future revision of this package still has to be understood by a client
// built from this one, so the two bytes may never be respelled.
func TestACKAndNAK_AreTheDesignsControlBytes(t *testing.T) {
	require.Equal(t, byte(0x06), ipc.ACK)
	require.Equal(t, byte(0x15), ipc.NAK)
	require.NotEqual(t, ipc.ACK, ipc.NAK)
}

// TestMaxLineBytes_IsOneMebibyte pins §2.4's "1 MiB max line".
func TestMaxLineBytes_IsOneMebibyte(t *testing.T) {
	require.Equal(t, 1024*1024, ipc.MaxLineBytes)
}

// TestMaxLineBytes_IsTheCeilingTheConfigurableLimitSitsUnder is the relationship that keeps the
// protocol constant and the config key from becoming two competing sources of truth:
// runtime.hotPath.maxPayloadBytes is the OPERATIONAL accept limit an operator may lower, and
// MaxLineBytes is the ceiling it may never exceed. A default above the ceiling would mean the
// daemon advertised an acceptance it could not honour.
func TestMaxLineBytes_IsTheCeilingTheConfigurableLimitSitsUnder(t *testing.T) {
	require.LessOrEqual(t, config.Defaults().Runtime.HotPath.MaxPayloadBytes, ipc.MaxLineBytes)
}

// TestOp_SpellingsAreFrozen pins the eight operations of §5.4 and the reserved admin namespace.
// Every one of these appears in spooled NDJSON on disk, and the daemon drains yesterday's spool on
// start, so a rename is a data-format break, not a refactor.
func TestOp_SpellingsAreFrozen(t *testing.T) {
	require.Equal(t, ipc.Op("observe.tool"), ipc.OpObserveTool)
	require.Equal(t, ipc.Op("observe.prompt"), ipc.OpObservePrompt)
	require.Equal(t, ipc.Op("observe.stop"), ipc.OpObserveStop)
	require.Equal(t, ipc.Op("session.start"), ipc.OpSessionStart)
	require.Equal(t, ipc.Op("checkpoint"), ipc.OpCheckpoint)
	require.Equal(t, ipc.Op("flush"), ipc.OpFlush)
	require.Equal(t, ipc.Op("status"), ipc.OpStatus)
	require.Equal(t, ipc.Op("mcp"), ipc.OpMCP)
	require.Equal(t, "admin.", ipc.OpAdminPrefix)
}

// TestHotPathMode_SyncIsTheZeroValue asserts the normal state is what a Response says when it says
// nothing: a zero Response must not read as "stop connecting, spool everything" (§12.2).
func TestHotPathMode_SyncIsTheZeroValue(t *testing.T) {
	var zero ipc.HotPathMode
	require.Equal(t, ipc.HotSync, zero)
	require.NotEqual(t, ipc.HotSync, ipc.HotSpool)
}

// TestRequest_JSONKeysAreTheShortWireSpellings pins §5.4's key names. They are short because every
// one of these is written on the hot path, once per tool call; they are pinned because a spool file
// written by one build is drained by another.
func TestRequest_JSONKeysAreTheShortWireSpellings(t *testing.T) {
	b, err := json.Marshal(ipc.Request{
		Op:      ipc.OpObserveTool,
		Session: core.SessionID("sess-1"),
		TS:      core.UnixMilli(1767225600000),
		Reply:   true,
		Event:   &hookio.Event{HookEventName: "PostToolUse", ToolName: "Read"},
		Raw:     json.RawMessage(`{"k":1}`),
	})
	require.NoError(t, err)

	var keyed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &keyed))
	for _, k := range []string{"op", "s", "t", "r", "e", "x"} {
		require.Contains(t, keyed, k)
	}
	require.Len(t, keyed, 6, "an unexpected key would mean a field was added without a wire decision")
}

// TestRequest_OptionalFieldsAreOmitted asserts the fire-and-forget request — by far the most common
// line on the wire — carries only what it has to. Reply, Event and Raw are all omitempty, so a
// PostToolUse observation with no reply needed serialises to four keys, not six.
func TestRequest_OptionalFieldsAreOmitted(t *testing.T) {
	b, err := json.Marshal(ipc.Request{Op: ipc.OpFlush, Session: "s", TS: 1})
	require.NoError(t, err)

	var keyed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &keyed))
	require.Len(t, keyed, 3)
	require.NotContains(t, keyed, "r")
	require.NotContains(t, keyed, "e")
	require.NotContains(t, keyed, "x")
}

// TestRequest_RoundTripsTheHookEventVerbatim asserts the Event survives the wire unchanged,
// including the Extra map hookio uses to preserve host-side fields this build does not name. The
// daemon must observe exactly what the host sent, not a re-encoding of it (§13 invariant 2's
// verbatim capture is only as good as the transport underneath it).
func TestRequest_RoundTripsTheHookEventVerbatim(t *testing.T) {
	want := ipc.Request{
		Op:      ipc.OpSessionStart,
		Session: core.SessionID("sess-round-trip"),
		TS:      core.UnixMilli(1767225600123),
		Reply:   true,
		Event: &hookio.Event{
			HookEventName:  "SessionStart",
			SessionID:      core.SessionID("sess-round-trip"),
			Source:         "compact",
			TranscriptPath: "/tmp/transcript.jsonl",
			ToolInput:      json.RawMessage(`{"path":"src/a.go"}`),
		},
	}

	b, err := json.Marshal(want)
	require.NoError(t, err)

	var got ipc.Request
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, want.Op, got.Op)
	require.Equal(t, want.Session, got.Session)
	require.Equal(t, want.TS, got.TS)
	require.True(t, got.Reply)
	require.NotNil(t, got.Event)
	require.Equal(t, "compact", got.Event.Source)
	require.JSONEq(t, string(want.Event.ToolInput), string(got.Event.ToolInput))
}

// TestResponse_JSONKeysAreTheShortWireSpellings pins §5.4's response keys, and asserts the two a
// client must honour immediately are always present: mode (§12.1's contract state) and hot
// (§12.2's submode). Making either omitempty would let a degraded daemon return a response a
// client reads as fully healthy.
func TestResponse_JSONKeysAreTheShortWireSpellings(t *testing.T) {
	b, err := json.Marshal(ipc.Response{})
	require.NoError(t, err)

	var keyed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &keyed))
	require.Len(t, keyed, 3)
	for _, k := range []string{"ok", "mode", "hot"} {
		require.Contains(t, keyed, k)
	}
	require.NotContains(t, keyed, "out")
	require.NotContains(t, keyed, "err")
	require.NotContains(t, keyed, "data")
}

// TestResponse_CarriesTheDegradedModeAcrossTheWire asserts §12.1's mode survives serialisation, so
// a client learns from the daemon that the session must stop acting.
func TestResponse_CarriesTheDegradedModeAcrossTheWire(t *testing.T) {
	instr := "focus on the current work"
	want := ipc.Response{
		OK:     false,
		Mode:   contract.ModeDegradedPassive,
		Hot:    ipc.HotSpool,
		Output: &hookio.Output{HookSpecificOutput: &hookio.HSO{HookEventName: "PreCompact", CustomInstructions: instr}},
		Err:    "contract: session_start.fires",
	}

	b, err := json.Marshal(want)
	require.NoError(t, err)

	var got ipc.Response
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, contract.ModeDegradedPassive, got.Mode)
	require.Equal(t, "degraded-passive", got.Mode.String())
	require.Equal(t, ipc.HotSpool, got.Hot)
	require.False(t, got.OK)
	require.NotNil(t, got.Output)
	require.Equal(t, instr, got.Output.HookSpecificOutput.CustomInstructions)
	require.Equal(t, want.Err, got.Err)
}

// TestRequest_ATypicalHotPathLineFitsTheFrame is a sanity check on the budget: a realistic
// PostToolUse request is orders of magnitude below the 1 MiB frame limit, so the limit exists to
// bound a hostile or runaway payload rather than to constrain normal traffic.
func TestRequest_ATypicalHotPathLineFitsTheFrame(t *testing.T) {
	b, err := json.Marshal(ipc.Request{
		Op:      ipc.OpObserveTool,
		Session: core.SessionID("01JQ0000000000000000000000"),
		TS:      core.UnixMilli(1767225600000),
		Event: &hookio.Event{
			HookEventName: "PostToolUse",
			ToolName:      "Read",
			ToolUseID:     core.ToolUseID("toolu_01ABCDEFGHIJKLMNOPQRSTUV"),
			ToolInput:     json.RawMessage(`{"file_path":"src/auth.ts"}`),
		},
	})
	require.NoError(t, err)
	require.Less(t, len(b)+1, ipc.MaxLineBytes, "a typical framed line must be far below the 1 MiB ceiling")
}
