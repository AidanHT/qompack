package integration_test

import (
	"bytes"
	"encoding/json"
	"strings"
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
		[]byte(`{"hook_event_name":"PostToolUse","future":{"credential":"sk-abcdefghijklmnopqrstuv"}}`),
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

// TestCapturePolicyProducesTheCompleteFidelitySet composes the real JSON and fragment policies over
// the six deliveries T20-M1-02 names. Before this unit only exact, redacted, failure and unknown
// were reachable from production code at all: partial, truncated and binary were declared, legal in
// validCaptureDecision, and produced by nothing but synthetic test policies. Each row below is a
// distinct fidelity and a distinct capture error, so a later reader can tell a delivery this
// process bounded from one the host cut short from one it could not parse.
func TestCapturePolicyProducesTheCompleteFidelitySet(t *testing.T) {
	cfg := config.Defaults()
	cfg.Runtime.Redact.Patterns = []string{"PRIVATE-[A-Z]{12}"}
	const secret = "PRIVATE-ABCDEFGHIJKL"

	json0, err := redact.CapturePolicy(cfg)
	require.NoError(t, err)
	fragment, err := redact.CaptureFragmentPolicy(cfg)
	require.NoError(t, err)
	frag := hookio.CaptureFragment{Policy: fragment}

	oversized := []byte(`{"hook_event_name":"PostToolUse","secret":"` + secret +
		`","future":"` + strings.Repeat("x", 4096) + `"}`)

	cases := []struct {
		name         string
		raw          []byte
		limit        int
		fragment     hookio.CaptureFragment
		wantFidelity core.Fidelity
		wantError    core.CaptureError
		wantOutcome  core.EvidenceOutcome
		wantBytes    bool
	}{
		{
			name: "exact", raw: []byte(`{"hook_event_name":"PostToolUse","future":1}`),
			limit: 4096, fragment: frag,
			wantFidelity: core.FidelityExact, wantOutcome: core.OutcomeOK, wantBytes: true,
		},
		{
			name: "redacted", raw: []byte(`{"hook_event_name":"PostToolUse","future":"` + secret + `"}`),
			limit: 4096, fragment: frag,
			wantFidelity: core.FidelityRedacted, wantOutcome: core.OutcomeOK, wantBytes: true,
		},
		{
			name: "truncated", raw: oversized, limit: 512, fragment: frag,
			wantFidelity: core.FidelityTruncated, wantError: core.CaptureErrorOversize,
			wantOutcome: core.OutcomeUnavailable, wantBytes: true,
		},
		{
			name: "partial", raw: []byte(`{"hook_event_name":"PostToolUse","secret":"` + secret),
			limit: 4096, fragment: hookio.CaptureFragment{Policy: fragment, Incomplete: true},
			wantFidelity: core.FidelityPartial, wantError: core.CaptureErrorIncomplete,
			wantOutcome: core.OutcomeUnavailable, wantBytes: true,
		},
		{
			name: "binary", raw: []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0xff},
			limit: 4096, fragment: frag,
			wantFidelity: core.FidelityBinary, wantError: core.CaptureErrorNotJSON,
			wantOutcome: core.OutcomeUnavailable, wantBytes: true,
		},
		{
			name: "failure", raw: []byte(`{"hook_event_name":"PostToolUse"}`), limit: 4096, fragment: frag,
			wantFidelity: core.FidelityFailure, wantError: core.CaptureErrorPolicy,
			wantOutcome: core.OutcomeUnavailable,
		},
	}

	seen := map[core.Fidelity]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := json0
			if tc.name == "failure" {
				policy = nil // an unavailable policy never implies permission
			}
			capture, event, err := hookio.CaptureHook(tc.raw, tc.limit, redact.CapturePolicyVersion,
				policy, tc.fragment)
			if tc.wantOutcome == core.OutcomeOK {
				require.NoError(t, err)
				require.Equal(t, "PostToolUse", event.HookEventName)
			} else {
				require.Error(t, err)
				require.Equal(t, hookio.Event{}, event, "a degraded delivery yields no observation")
			}
			require.True(t, capture.Fidelity.Valid())
			require.True(t, capture.CaptureError.Valid())
			require.Equal(t, tc.wantFidelity, capture.Fidelity)
			require.Equal(t, tc.wantError, capture.CaptureError)
			require.Equal(t, tc.wantOutcome, capture.Outcome)
			require.Equal(t, len(tc.raw), capture.SourceBytes)
			require.Equal(t, core.EvidenceHashVersion, capture.HashVersion)
			require.NotContains(t, string(capture.Bytes), secret,
				"no fidelity kind may retain a secret the operator's rules cover")
			if tc.wantBytes {
				require.NotEmpty(t, capture.Bytes)
			} else {
				require.Empty(t, capture.Bytes)
			}

			encoded, err := json.Marshal(capture)
			require.NoError(t, err)
			var restored hookio.Capture
			require.NoError(t, json.Unmarshal(encoded, &restored))
			require.Equal(t, capture, restored, "every fidelity kind survives the sidecar transport")
			seen[capture.Fidelity] = true
		})
	}
	require.Len(t, seen, len(cases), "each row is a distinct fidelity kind")
}
