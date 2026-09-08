package integration_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/redact"
)

// This composes the real policy and capture seam. It does not exercise installed ingress,
// persistence, lease identity or publication, so it cannot fill those V4 integration gates.
func TestCapturePolicyToHookEnvelopeRoundTrip(t *testing.T) {
	p, err := redact.CapturePolicy(config.Defaults())
	require.NoError(t, err)
	for _, raw := range [][]byte{
		[]byte("{\n \"hook_event_name\": \"PostToolUse\", \"future\": { \"n\": 9007199254740993 }\n}\n"),
		[]byte(`{"hook_event_name":"PostToolUse","future":{"credential":"sk-abcdefghij\u006blmnopqrstuv"}}`),
	} {
		capture, event, err := hookio.CaptureHook(raw, 4096, redact.CapturePolicyVersion, p)
		require.NoError(t, err)
		require.Equal(t, core.OutcomeOK, capture.Outcome)
		require.Equal(t, "PostToolUse", event.HookEventName)
		encoded, err := json.Marshal(capture)
		require.NoError(t, err)
		var restored hookio.Capture
		require.NoError(t, json.Unmarshal(encoded, &restored))
		require.Equal(t, capture, restored, "base64 transport preserves every permitted source byte")
		decoded, _, err := hookio.ReadEvent(bytes.NewReader(restored.Bytes), 4096)
		require.NoError(t, err)
		require.Equal(t, event, decoded)
		if capture.Redacted {
			require.Equal(t, core.FidelityRedacted, capture.Fidelity)
			require.NotContains(t, string(decoded.Extra["future"]), "abcdefghijklmnopqrstuv")
		} else {
			require.Equal(t, core.FidelityExact, capture.Fidelity)
			require.Equal(t, raw, restored.Bytes)
		}
	}
}
