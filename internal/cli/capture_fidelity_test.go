package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// admissionEnv is the minimal Env admitHookCapture needs: the project root is supplied by the
// caller, no environment overrides apply, and the home directory holds no user configuration.
func admissionEnv(t *testing.T) Env {
	t.Helper()
	return Env{Getenv: noEnv, HomeDir: t.TempDir(), Clock: testClock()}
}

// shortAdmissionReader delivers a prefix and then fails, which is what a host that dies mid-write
// looks like from this side of the pipe.
type shortAdmissionReader struct {
	payload []byte
	sent    bool
}

func (r *shortAdmissionReader) Read(b []byte) (int, error) {
	if r.sent {
		return 0, errors.New(admissionSecret)
	}
	r.sent = true
	return copy(b, r.payload), nil
}

// TestHookCapture_ShortReadIsRecordedAsPartial pins T20-M1-02's partial case through the CLI seam.
// A host that stops writing mid-payload used to leave nothing behind at all; what did arrive is now
// classified as partial, under the operator's own redaction rules, and is still refused for
// observation.
func TestHookCapture_ShortReadIsRecordedAsPartial(t *testing.T) {
	root := t.TempDir()
	writeAdmissionConfig(t, root, `{"runtime":{"redact":{"patterns":["PRIVATE-[A-Z]{12}"]}}}`)
	reader := &shortAdmissionReader{
		payload: []byte(`{"hook_event_name":"UserPromptSubmit","prompt":"` + admissionSecret),
	}

	in, err := readHookCapture(reader, 4096)
	require.ErrorIs(t, err, core.ErrDegraded)
	require.True(t, in.Incomplete, "a read that ends early is recorded as incomplete")
	require.NotEmpty(t, in.Raw, "what did arrive is still handed on for classification")
	require.NotContains(t, err.Error(), admissionSecret)

	capture, ev, _, err := admitHookCapture(admissionEnv(t), root, in)
	require.ErrorIs(t, err, core.ErrDegraded)
	require.Equal(t, core.FidelityPartial, capture.Fidelity)
	require.Equal(t, core.CaptureErrorIncomplete, capture.CaptureError)
	require.False(t, capture.Truncated, "a short read is not a truncation this process chose")
	require.NotEqual(t, core.OutcomeOK, capture.Outcome)
	require.Equal(t, hookio.Event{}, ev)
	require.NotEmpty(t, capture.Bytes)
	require.NotContains(t, string(capture.Bytes), admissionSecret,
		"a retained prefix clears the operator's own rules first")
}

// TestHookCapture_OversizeRetainsARedactedPrefix pins T20-M1-02's truncated case at the seam where
// the bound is a policy decision rather than the hard allocation cap: the payload cleared the cap
// readHookCapture enforces without loading any configuration, and is then bounded by the configured
// maxPayloadBytes, which is where a prefix can be retained under a loaded policy.
func TestHookCapture_OversizeRetainsARedactedPrefix(t *testing.T) {
	root := t.TempDir()
	writeAdmissionConfig(t, root,
		`{"runtime":{"hotPath":{"maxPayloadBytes":4096},"redact":{"patterns":["PRIVATE-[A-Z]{12}"]}}}`)
	raw := []byte(`{"hook_event_name":"UserPromptSubmit","prompt":"` + admissionSecret +
		`","future":"` + strings.Repeat("x", 32<<10) + `"}`)

	capture, ev, _, err := admitHookCapture(admissionEnv(t), root, hookInput{Raw: raw})
	require.ErrorIs(t, err, core.ErrBudget)
	require.Equal(t, core.FidelityTruncated, capture.Fidelity)
	require.Equal(t, core.CaptureErrorOversize, capture.CaptureError)
	require.True(t, capture.Truncated)
	require.Equal(t, len(raw), capture.SourceBytes)
	require.NotEmpty(t, capture.Bytes, "an oversize delivery still leaves its head behind as evidence")
	require.Less(t, len(capture.Bytes), len(raw))
	require.NotContains(t, string(capture.Bytes), admissionSecret)
	require.Equal(t, hookio.Event{}, ev)
}

// TestHookCapture_BinaryPayloadIsClassifiedNotSilentlyRejected pins T20-M1-02's binary case. Bytes
// that are not an admissible JSON object are recorded as binary evidence with their own capture
// error rather than vanishing behind a bare contract rejection.
func TestHookCapture_BinaryPayloadIsClassifiedNotSilentlyRejected(t *testing.T) {
	root := t.TempDir()
	writeAdmissionConfig(t, root, `{"runtime":{"redact":{"patterns":["PRIVATE-[A-Z]{12}"]}}}`)
	raw := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x00, 0xff}

	capture, ev, _, err := admitHookCapture(admissionEnv(t), root, hookInput{Raw: raw})
	require.ErrorIs(t, err, core.ErrDegraded)
	require.NotErrorIs(t, err, core.ErrContract)
	require.Equal(t, core.FidelityBinary, capture.Fidelity)
	require.Equal(t, core.CaptureErrorNotJSON, capture.CaptureError)
	require.Equal(t, raw, capture.Bytes, "non-UTF-8 evidence is retained as it arrived")
	require.NotEqual(t, core.OutcomeOK, capture.Outcome)
	require.Equal(t, hookio.Event{}, ev)
}

// TestHookCapture_AdmittedPayloadRecordsObservedHostFields pins the admitted path's record of which
// host fields were actually present. Event's fields are value types with usable zeros, so without
// this list an omitted field and an empty one read identically downstream.
func TestHookCapture_AdmittedPayloadRecordsObservedHostFields(t *testing.T) {
	root := t.TempDir()
	writeAdmissionConfig(t, root, `{}`)
	raw := []byte(`{"hook_event_name":"UserPromptSubmit","cwd":"","future_host_key":{"n":1}}`)

	capture, ev, _, err := admitHookCapture(admissionEnv(t), root, hookInput{Raw: raw})
	require.NoError(t, err)
	require.Equal(t, core.OutcomeOK, capture.Outcome)
	require.Equal(t, core.FidelityExact, capture.Fidelity)
	require.Equal(t, core.CaptureErrorNone, capture.CaptureError)
	require.Equal(t, []string{"cwd", "future_host_key", "hook_event_name"}, capture.HostFields)
	require.NotContains(t, capture.HostFields, "prompt", "an omitted field stays omitted")
	require.Empty(t, ev.Prompt)
	require.Equal(t, raw, capture.Bytes)
}

// TestHookCapture_DeliveryNonceAndCaptureReachTheSpooledRequest is the transport half of this unit.
// Two host invocations of byte-identical input are two deliveries and carry two nonces; the
// admitted capture travels beside the Event, so a host key hookio.Event does not name survives the
// encode boundary that used to drop it.
func TestHookCapture_DeliveryNonceAndCaptureReachTheSpooledRequest(t *testing.T) {
	root := t.TempDir()
	writeAdmissionConfig(t, root, `{}`)
	require.NoError(t, os.MkdirAll(paths.Of(root).Run, 0o700))
	require.NoError(t, ipc.WriteState(root, ipc.ReadState(root, config.Defaults())))
	raw, err := json.Marshal(map[string]any{
		"cwd": root, "session_id": "nonce-session", "hook_event_name": "UserPromptSubmit",
		"prompt": "hello", "future_host_key": map[string]any{"n": 1},
	})
	require.NoError(t, err)

	deliver := func() ipc.Request {
		var out, errw bytes.Buffer
		code := Dispatch(context.Background(), All(), argvFor("observe prompt"), Env{
			Getenv: noEnv, HomeDir: t.TempDir(), Stdin: bytes.NewReader(raw), Clock: testClock(),
		}, &out, &errw)
		require.Equal(t, ExitOK, code)
		req := onlySpooledRequest(t, root)
		require.NoError(t, os.RemoveAll(paths.Of(root).Spool))
		return req
	}

	first, second := deliver(), deliver()
	require.Len(t, first.Nonce, 32, "128 bits of minted delivery label, hex encoded")
	require.NotEqual(t, first.Nonce, second.Nonce,
		"two host invocations of identical bytes are two distinct deliveries")

	require.NotNil(t, first.Capture)
	require.Equal(t, core.OutcomeOK, first.Capture.Outcome)
	require.Equal(t, core.FidelityExact, first.Capture.Fidelity)
	require.Equal(t, core.EvidenceHashVersion, first.Capture.HashVersion)
	require.NotEmpty(t, first.Capture.PolicyVersion)
	require.JSONEq(t, string(raw), string(first.Capture.Bytes))
	require.Equal(t, first.Capture.Bytes, second.Capture.Bytes,
		"identical content, so only the nonce distinguishes the two deliveries")

	var extras map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(first.Capture.Bytes, &extras))
	require.Contains(t, extras, "future_host_key",
		"a host key hookio.Event does not name now survives ipc.EncodeRequest")
	require.Contains(t, first.Capture.HostFields, "future_host_key")
	require.Empty(t, first.Event.Extra,
		"Event.Extra still does not cross the wire; the capture is what carries it")
}
