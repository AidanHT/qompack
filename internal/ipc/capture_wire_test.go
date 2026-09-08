package ipc_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
)

// legacyRequest is the Request shape a daemon built before this unit compiles against: the six
// keys §5.4 shipped, and nothing else. Decoding a new request into it is the compatibility claim
// itself — an older reader must keep working against a newer hook client, which is invariant 10.
type legacyRequest struct {
	Op      ipc.Op          `json:"op"`
	Session core.SessionID  `json:"s"`
	TS      core.UnixMilli  `json:"t"`
	Reply   bool            `json:"r,omitempty"`
	Event   *hookio.Event   `json:"e,omitempty"`
	Raw     json.RawMessage `json:"x,omitempty"`
}

func captureFixture() hookio.Capture {
	return hookio.Capture{
		Version:       core.EvidenceVersion,
		Bytes:         []byte(`{"hook_event_name":"PostToolUse","future_host_key":{"n":1}}`),
		SourceFormat:  "application/json",
		PolicyVersion: "redact-json/v1",
		HashVersion:   core.EvidenceHashVersion,
		Fidelity:      core.FidelityExact,
		Outcome:       core.OutcomeOK,
		SourceBytes:   58,
		HostFields:    []string{"future_host_key", "hook_event_name"},
	}
}

// TestRequest_CarriesTheAdmittedCaptureAndNonce pins the field that closes the drop point: before
// it, hookio.Event.Extra was tagged `json:"-"` and every host key this build does not name died at
// the encode boundary, leaving only the two-case Raw allow-list. The capture crosses the wire with
// its bytes, its fidelity, its capture error, and the transform/hash versions they were decided
// under, so a daemon can persist what the host actually sent.
func TestRequest_CarriesTheAdmittedCaptureAndNonce(t *testing.T) {
	want := ipc.WithCapture(ipc.Request{
		Op:      ipc.OpObserveTool,
		Session: core.SessionID("sess-capture"),
		TS:      core.UnixMilli(1767225600123),
		Event:   &hookio.Event{HookEventName: "PostToolUse", ToolName: "Read"},
		Raw:     json.RawMessage(`{"subagent":true}`),
		Nonce:   "0123456789abcdef0123456789abcdef",
	}, captureFixture())

	line, err := ipc.EncodeRequest(want)
	require.NoError(t, err)
	got, err := ipc.DecodeRequest(line)
	require.NoError(t, err)

	require.NotNil(t, got.Capture)
	require.Equal(t, *want.Capture, *got.Capture, "every capture field survives the transport")
	require.Equal(t, want.Capture.Bytes, got.Capture.Bytes,
		"base64 transport preserves the permitted host bytes exactly")
	require.Equal(t, want.Nonce, got.Nonce)
	require.JSONEq(t, string(want.Raw), string(got.Raw), "the two-case Raw allow-list is unchanged")

	var extras map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(got.Capture.Bytes, &extras))
	require.Contains(t, extras, "future_host_key",
		"a host key hookio.Event does not name now reaches the daemon")
}

// TestRequest_CaptureAndNonceRoundTripThroughAnOlderReader is the compatibility gate in both
// directions: a request carrying the new fields still decodes on a struct without them, and a line
// an older client wrote still decodes here with the new fields simply absent.
func TestRequest_CaptureAndNonceRoundTripThroughAnOlderReader(t *testing.T) {
	newer := ipc.WithCapture(ipc.Request{
		Op:      ipc.OpObserveStop,
		Session: core.SessionID("sess-compat"),
		TS:      core.UnixMilli(42),
		Reply:   true,
		Event:   &hookio.Event{HookEventName: "Stop", SessionID: core.SessionID("sess-compat")},
		Raw:     json.RawMessage(`{"subagent":true,"agent":"scout"}`),
		Nonce:   "deadbeefdeadbeefdeadbeefdeadbeef",
	}, captureFixture())

	line, err := ipc.EncodeRequest(newer)
	require.NoError(t, err)

	var old legacyRequest
	require.NoError(t, json.Unmarshal(line, &old), "an older daemon must still decode this line")
	require.Equal(t, newer.Op, old.Op)
	require.Equal(t, newer.Session, old.Session)
	require.Equal(t, newer.TS, old.TS)
	require.True(t, old.Reply)
	require.NotNil(t, old.Event)
	require.Equal(t, "Stop", old.Event.HookEventName)
	require.JSONEq(t, string(newer.Raw), string(old.Raw))

	oldLine, err := json.Marshal(legacyRequest{
		Op: ipc.OpObservePrompt, Session: core.SessionID("sess-old"), TS: 7,
		Event: &hookio.Event{HookEventName: "UserPromptSubmit"},
	})
	require.NoError(t, err)
	restored, err := ipc.DecodeRequest(oldLine)
	require.NoError(t, err, "a line written before these fields existed must still decode")
	require.Nil(t, restored.Capture, "an absent capture is absent, not an empty one")
	require.Empty(t, restored.Nonce, "an absent nonce is absent, never synthesized on read")
	require.Equal(t, core.SessionID("sess-old"), restored.Session)
}

// TestRequest_NewFieldsAreOmittedWhenUnset keeps the hot path's line exactly as short as it was for
// every request that carries neither: omitempty means an older spool file and a newer one are the
// same bytes when there is no capture and no nonce to carry.
func TestRequest_NewFieldsAreOmittedWhenUnset(t *testing.T) {
	b, err := json.Marshal(ipc.Request{Op: ipc.OpFlush, Session: "s", TS: 1})
	require.NoError(t, err)

	var keyed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &keyed))
	require.NotContains(t, keyed, "c")
	require.NotContains(t, keyed, "n")
	require.Len(t, keyed, 3)
}

// TestNewDeliveryNonce_LabelsInvocationsNotContent pins the nonce's whole reason to exist: two
// deliveries of byte-identical content are still two deliveries. A content hash cannot say that,
// so the label is minted, not derived.
func TestNewDeliveryNonce_LabelsInvocationsNotContent(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		nonce, err := ipc.NewDeliveryNonce()
		require.NoError(t, err)
		require.Len(t, nonce, 32, "128 bits, hex encoded")
		require.False(t, seen[nonce], "a minted nonce must never repeat")
		seen[nonce] = true
	}
}

// TestRequest_NonceSurvivesReEncodingOfTheSameDelivery is the retry half of the contract. The
// client re-encodes the very same Request value when it falls back to the spool and when a later
// drain replays that line, so one delivery keeps one label however many times it is written.
func TestRequest_NonceSurvivesReEncodingOfTheSameDelivery(t *testing.T) {
	nonce, err := ipc.NewDeliveryNonce()
	require.NoError(t, err)
	req := ipc.Request{Op: ipc.OpObserveTool, Session: "s", TS: 1, Nonce: nonce}

	for attempt := 0; attempt < 3; attempt++ {
		line, err := ipc.EncodeRequest(req)
		require.NoError(t, err)
		got, err := ipc.DecodeRequest(line)
		require.NoError(t, err)
		require.Equal(t, nonce, got.Nonce, "a retry of one delivery carries the same nonce")
		req = got
	}
}

// TestWithCapture_DropsBytesThatWouldNotFitTheFrame guards the spool: its line reader rejects an
// oversize line outright, so a capture large enough to push a request past the frame ceiling would
// cost the whole delivery, not just the evidence. The bytes are dropped and the record says so
// rather than travelling as an apparently complete capture.
func TestWithCapture_DropsBytesThatWouldNotFitTheFrame(t *testing.T) {
	oversize := captureFixture()
	oversize.Bytes = []byte(`{"x":"` + strings.Repeat("y", ipc.CaptureFrameBudget) + `"}`)

	req := ipc.WithCapture(ipc.Request{Op: ipc.OpObserveTool, Session: "s", TS: 1}, oversize)
	require.NotNil(t, req.Capture)
	require.Empty(t, req.Capture.Bytes)
	require.Equal(t, core.OutcomeUnavailable, req.Capture.Outcome)
	require.Equal(t, core.FidelityTruncated, req.Capture.Fidelity)
	require.Equal(t, core.CaptureErrorOversize, req.Capture.CaptureError)
	require.True(t, req.Capture.Truncated)
	require.Equal(t, oversize.SourceBytes, req.Capture.SourceBytes,
		"the observed delivery size is still recorded")

	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	require.Less(t, len(line), ipc.MaxLineBytes, "the frame stays inside the protocol ceiling")

	fits := ipc.WithCapture(ipc.Request{Op: ipc.OpObserveTool, Session: "s", TS: 1}, captureFixture())
	require.Equal(t, captureFixture().Bytes, fits.Capture.Bytes, "a capture that fits is untouched")
}

// TestWithCapture_KeepsTheObservationWhenTheCaptureWillNotFit is the bound on the downgrade above,
// and it is about the SHIPPED default configuration rather than an exotic one.
//
// runtime.hotPath.maxPayloadBytes defaults to 1 MiB, whose capture limit is the 4 MiB hard cap, so
// an ordinary 400 KB file read is admitted with OutcomeOK and a complete Event and arrives here
// with permitted bytes over CaptureFrameBudget. The downgrade is of the CAPTURE half only: strip
// the bytes, say why, and hand the Event across untouched. A caller that reads the degraded
// outcome as a reason to drop the whole request turns "we kept less evidence than we wanted" into
// "the tool use was never seen" — which is exactly what the daemon did before this fix.
func TestWithCapture_KeepsTheObservationWhenTheCaptureWillNotFit(t *testing.T) {
	const payloadBytes = 400 * 1000
	oversize := captureFixture()
	oversize.Bytes = []byte(`{"tool_response":"` + strings.Repeat("y", payloadBytes) + `"}`)
	oversize.SourceBytes = len(oversize.Bytes)
	require.Greater(t, len(oversize.Bytes), ipc.CaptureFrameBudget,
		"a 400 KB payload must actually cross the frame budget or this proves nothing")

	ev := &hookio.Event{HookEventName: "PostToolUse", SessionID: "s", ToolName: "Read"}
	req := ipc.WithCapture(ipc.Request{
		Op: ipc.OpObserveTool, Session: "s", TS: 1, Event: ev,
	}, oversize)

	require.Same(t, ev, req.Event, "the observation crosses the wire unchanged")
	require.Equal(t, "Read", req.Event.ToolName)
	require.Equal(t, core.OutcomeUnavailable, req.Capture.Outcome, "only the evidence half is degraded")
	require.Empty(t, req.Capture.Bytes)
	require.Equal(t, payloadBytes+len(`{"tool_response":""}`), req.Capture.SourceBytes)

	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	require.Less(t, len(line), ipc.MaxLineBytes, "and the frame still fits")
	decoded, err := ipc.DecodeRequest(line)
	require.NoError(t, err)
	require.NotNil(t, decoded.Event, "the Event survives the round trip, not just the struct field")
	require.Equal(t, "Read", decoded.Event.ToolName)
}
