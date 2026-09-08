package hookio_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
)

func TestCaptureHook_PreservesPermittedHostJSONExactly(t *testing.T) {
	raw := []byte("{\n" +
		"  \"hook_event_name\" : \"PostToolUse\",\n" +
		"  \"session_id\" : \"capture-session\",\n" +
		"  \"tool_input\" : { \"file_path\" : \"src/a.go\" },\n" +
		"  \"tool_response\" : { \"escaped\" : \"\\u003ctag\\u003e\" },\n" +
		"  \"future_field\" : { \"large_exact_integer\" : 9007199254740993123456789 }\n" +
		"}\n")
	want := bytes.Clone(raw)
	var policyInput, policyResult []byte

	cap, ev, err := hookio.CaptureHook(raw, len(raw), "privacy/v1", func(in []byte) (core.CaptureDecision, error) {
		policyInput = in
		policyResult = bytes.Clone(in)
		return core.CaptureDecision{Bytes: policyResult, Outcome: core.OutcomeOK, Fidelity: core.FidelityExact}, nil
	})
	require.NoError(t, err)
	require.Equal(t, core.EvidenceVersion, cap.Version)
	require.Equal(t, "application/json", cap.SourceFormat)
	require.Equal(t, "privacy/v1", cap.PolicyVersion)
	require.Equal(t, core.EvidenceHashVersion, cap.HashVersion)
	require.Equal(t, core.FidelityExact, cap.Fidelity)
	require.Equal(t, core.OutcomeOK, cap.Outcome)
	require.False(t, cap.Redacted)
	require.False(t, cap.Truncated)
	require.Equal(t, want, cap.Bytes, "permitted capture bytes retain formatting, escapes, and unknown fields")
	require.Equal(t, "PostToolUse", ev.HookEventName)
	require.Equal(t, core.SessionID("capture-session"), ev.SessionID)
	require.JSONEq(t, `{"file_path":"src/a.go"}`, string(ev.ToolInput))
	require.JSONEq(t, `{"escaped":"<tag>"}`, string(ev.ToolResponse))
	require.JSONEq(t, `{"large_exact_integer":9007199254740993123456789}`, string(ev.Extra["future_field"]),
		"the derived Event must not coerce an unknown integer through float64")

	// The raw caller buffer, policy input/result, returned capture, and derived Event are separate
	// ownership domains. A policy may redact in place, and callers may retain or discard its result
	// after this call, so none may alias the returned capture or parsed event.
	raw[0] = '['
	cap.Bytes[0] = '['
	require.Equal(t, want, policyInput, "CaptureHook passes policy a private copy")
	require.Equal(t, want, policyResult, "returned capture must not alias the policy result")
	require.JSONEq(t, `{"file_path":"src/a.go"}`, string(ev.ToolInput),
		"returned capture bytes must not alias parsed Event JSON")
}

func TestCaptureHook_InPlacePolicyRedactionUsesOriginalForFidelity(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PostToolUse","tool_response":"secret"}`)
	wantRaw := bytes.Clone(raw)

	cap, ev, err := hookio.CaptureHook(raw, len(raw), "privacy/v1", func(in []byte) (core.CaptureDecision, error) {
		require.Equal(t, wantRaw, in, "policy starts with an exact private copy")
		at := bytes.Index(in, []byte("secret"))
		require.GreaterOrEqual(t, at, 0)
		copy(in[at:], []byte("xxxxxx"))
		return core.CaptureDecision{
			Bytes: in, Outcome: core.OutcomeOK, Fidelity: core.FidelityRedacted, Redacted: true,
		}, nil
	})
	require.NoError(t, err)
	require.Equal(t, core.FidelityRedacted, cap.Fidelity)
	require.True(t, cap.Redacted)
	require.Equal(t, core.OutcomeOK, cap.Outcome)
	require.NotEqual(t, wantRaw, cap.Bytes)
	require.Equal(t, wantRaw, raw, "policy mutation must not write through to the caller buffer")
	require.JSONEq(t, `"xxxxxx"`, string(ev.ToolResponse), "Event is parsed from permitted bytes")
}

func TestCaptureHook_ParsesOnlyThePermittedEvent(t *testing.T) {
	// The original value is not a valid typed Event. A privacy policy may remove it before
	// deriving that Event; parsing the unpermitted input first would incorrectly prevent this.
	raw := []byte(`{"stop_hook_active":{"private":"fixture"}}`)
	calls := 0
	capture, event, err := hookio.CaptureHook(raw, len(raw), "remove-private-field/v1",
		func(in []byte) (core.CaptureDecision, error) {
			calls++
			require.Equal(t, raw, in)
			return core.CaptureDecision{
				Bytes: []byte(`{}`), Outcome: core.OutcomeOK, Fidelity: core.FidelityRedacted, Redacted: true,
			}, nil
		})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, []byte(`{}`), capture.Bytes)
	require.Equal(t, hookio.Event{}, event)
}

func TestCaptureHook_UsesExplicitPolicyFidelityForChangedBytes(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PostToolUse","future":"host-truncated"}`)
	permitted := []byte(`{"hook_event_name":"PostToolUse","future":"permitted-prefix"}`)

	cap, ev, err := hookio.CaptureHook(raw, max(len(raw), len(permitted)), "privacy/v1", func([]byte) (core.CaptureDecision, error) {
		return core.CaptureDecision{
			Bytes: permitted, Outcome: core.OutcomeOK, Fidelity: core.FidelityPartial,
		}, nil
	})
	require.NoError(t, err)
	require.Equal(t, permitted, cap.Bytes)
	require.Equal(t, core.FidelityPartial, cap.Fidelity)
	require.False(t, cap.Redacted, "changed bytes are not inherently a redaction")
	require.False(t, cap.Truncated)
	require.Equal(t, "PostToolUse", ev.HookEventName)
}

func TestCaptureHook_RejectsIncoherentPolicyFidelity(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PostToolUse","future":"host"}`)
	changed := []byte(`{"hook_event_name":"PostToolUse","future":"changed"}`)
	cases := []struct {
		name     string
		decision core.CaptureDecision
	}{
		{
			name:     "changed bytes claimed exact",
			decision: core.CaptureDecision{Bytes: changed, Outcome: core.OutcomeOK, Fidelity: core.FidelityExact},
		},
		{
			name:     "redacted flag claimed exact",
			decision: core.CaptureDecision{Bytes: raw, Outcome: core.OutcomeOK, Fidelity: core.FidelityExact, Redacted: true},
		},
		{
			name:     "redacted fidelity without flag",
			decision: core.CaptureDecision{Bytes: changed, Outcome: core.OutcomeOK, Fidelity: core.FidelityRedacted},
		},
		{
			name:     "truncated fidelity without flag",
			decision: core.CaptureDecision{Bytes: raw, Outcome: core.OutcomeOK, Fidelity: core.FidelityTruncated},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap, ev, err := hookio.CaptureHook(raw, max(len(raw), len(changed)), "privacy/v1", func([]byte) (core.CaptureDecision, error) {
				return tc.decision, nil
			})
			require.ErrorIs(t, err, core.ErrContract)
			require.Equal(t, core.OutcomeUnavailable, cap.Outcome)
			require.Empty(t, cap.Bytes)
			require.Equal(t, hookio.Event{}, ev)
		})
	}
}

func TestCaptureHook_DenialAndUnavailableDoNotExposePolicyBytes(t *testing.T) {
	const secret = "policy-private-secret"
	raw := []byte(`{"hook_event_name":"PostToolUse"}`)

	cases := []struct {
		name    string
		version string
		policy  func([]byte) (core.CaptureDecision, error)
		outcome core.EvidenceOutcome
		wantErr bool
	}{
		{
			name:    "denied",
			version: "privacy/v1",
			policy: func([]byte) (core.CaptureDecision, error) {
				return core.CaptureDecision{Bytes: []byte(secret), Outcome: core.OutcomeDenied}, nil
			},
			outcome: core.OutcomeDenied,
		},
		{
			name:    "policy error",
			version: "privacy/v1",
			policy: func([]byte) (core.CaptureDecision, error) {
				return core.CaptureDecision{Bytes: []byte(secret), Outcome: core.OutcomeOK, Fidelity: core.FidelityExact}, errors.New(secret)
			},
			outcome: core.OutcomeUnavailable,
			wantErr: true,
		},
		{
			name: "policy panic", version: "privacy/v1", outcome: core.OutcomeUnavailable, wantErr: true,
			policy: func([]byte) (core.CaptureDecision, error) { panic(secret) },
		},
		{
			name:    "unsupported outcome",
			version: "privacy/v1",
			policy: func([]byte) (core.CaptureDecision, error) {
				return core.CaptureDecision{Bytes: []byte(secret), Outcome: core.OutcomeUncertain, Fidelity: core.FidelityUnknown}, nil
			},
			outcome: core.OutcomeUnavailable,
			wantErr: true,
		},
		{name: "nil policy", version: "privacy/v1", outcome: core.OutcomeUnavailable, wantErr: true},
		{
			name:    "missing policy version",
			version: "",
			policy: func(in []byte) (core.CaptureDecision, error) {
				return core.CaptureDecision{Bytes: in, Outcome: core.OutcomeOK, Fidelity: core.FidelityExact}, nil
			},
			outcome: core.OutcomeUnavailable,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap, ev, err := hookio.CaptureHook(raw, len(raw), tc.version, tc.policy)
			require.Equal(t, tc.outcome, cap.Outcome)
			require.Empty(t, cap.Bytes)
			require.Equal(t, hookio.Event{}, ev)
			if tc.wantErr {
				require.Error(t, err)
				require.NotContains(t, err.Error(), secret, "backend policy text must not reach the caller")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCaptureHook_RejectsInvalidSourceAndPolicyOutput(t *testing.T) {
	valid := []byte(`{"hook_event_name":"PostToolUse","stop_hook_active":false}`)
	invalidUTF8 := []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}

	cases := []struct {
		name   string
		raw    []byte
		policy func([]byte) (core.CaptureDecision, error)
	}{
		{name: "malformed source", raw: []byte(`{"hook_event_name":`)},
		{name: "non-object source", raw: []byte(`[]`)},
		{name: "invalid utf8 source", raw: invalidUTF8},
		{name: "invalid typed source", raw: []byte(`{"stop_hook_active":"not-a-bool"}`)},
		{
			name: "malformed policy output", raw: valid,
			policy: func([]byte) (core.CaptureDecision, error) {
				return core.CaptureDecision{Bytes: []byte(`{"hook_event_name":`), Outcome: core.OutcomeOK, Fidelity: core.FidelityPartial}, nil
			},
		},
		{
			name: "non-object policy output", raw: valid,
			policy: func([]byte) (core.CaptureDecision, error) {
				return core.CaptureDecision{Bytes: []byte(`null`), Outcome: core.OutcomeOK, Fidelity: core.FidelityPartial}, nil
			},
		},
		{
			name: "invalid typed policy output", raw: valid,
			policy: func([]byte) (core.CaptureDecision, error) {
				return core.CaptureDecision{Bytes: []byte(`{"stop_hook_active":"not-a-bool"}`), Outcome: core.OutcomeOK, Fidelity: core.FidelityPartial}, nil
			},
		},
		{
			name: "invalid utf8 policy output", raw: valid,
			policy: func([]byte) (core.CaptureDecision, error) {
				return core.CaptureDecision{Bytes: invalidUTF8, Outcome: core.OutcomeOK, Fidelity: core.FidelityPartial}, nil
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := tc.policy
			if policy == nil {
				policy = func(in []byte) (core.CaptureDecision, error) {
					return core.CaptureDecision{Bytes: in, Outcome: core.OutcomeOK, Fidelity: core.FidelityExact}, nil
				}
			}
			cap, ev, err := hookio.CaptureHook(tc.raw, 4096, "privacy/v1", policy)
			require.ErrorIs(t, err, core.ErrContract)
			require.Empty(t, cap.Bytes)
			require.Equal(t, hookio.Event{}, ev)
		})
	}
}

func TestCaptureHook_RejectsSourceAndResultBeyondBudget(t *testing.T) {
	tooLarge := []byte(`{"hook_event_name":"PostToolUse","future":"` + strings.Repeat("x", 32) + `"}`)
	valid := []byte(`{"hook_event_name":"PostToolUse"}`)

	cases := []struct {
		name      string
		raw       []byte
		limit     int
		policy    func([]byte) (core.CaptureDecision, error)
		wantCalls int
	}{
		{
			name:  "source",
			raw:   tooLarge,
			limit: len(tooLarge) - 1,
			policy: func(in []byte) (core.CaptureDecision, error) {
				return core.CaptureDecision{Bytes: in, Outcome: core.OutcomeOK, Fidelity: core.FidelityExact}, nil
			},
			wantCalls: 0,
		},
		{
			name:  "policy result",
			raw:   valid,
			limit: len(valid),
			policy: func(in []byte) (core.CaptureDecision, error) {
				return core.CaptureDecision{Bytes: append(bytes.Clone(in), ' '), Outcome: core.OutcomeOK, Fidelity: core.FidelityPartial}, nil
			},
			wantCalls: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			policy := func(in []byte) (core.CaptureDecision, error) {
				calls++
				return tc.policy(in)
			}
			cap, ev, err := hookio.CaptureHook(tc.raw, tc.limit, "privacy/v1", policy)
			require.ErrorIs(t, err, core.ErrBudget)
			require.True(t, cap.Truncated)
			require.Empty(t, cap.Bytes)
			require.Equal(t, hookio.Event{}, ev)
			require.Equal(t, tc.wantCalls, calls)
		})
	}
}
