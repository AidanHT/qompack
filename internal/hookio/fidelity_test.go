package hookio_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
)

// permitEverything is the simplest admissible fragment policy: it retains what it was handed. The
// point of these tests is which bytes CaptureHook offers a fragment policy and how it classifies
// the result, not what a real redactor does with them (internal/redact owns that).
func permitEverything(in []byte) (core.CaptureDecision, error) {
	return core.CaptureDecision{
		Bytes: bytes.Clone(in), Outcome: core.OutcomeOK, Fidelity: core.FidelityExact,
	}, nil
}

// TestCaptureHook_OversizeRetainsABoundedTruncatedPrefix pins T20-M1-02's truncated case. Before
// this, an oversize delivery set Truncated and returned no bytes at all: the evidence that anything
// arrived, and what its first bytes were, was simply lost. A bounded prefix is retained instead,
// classified as truncated rather than as a complete capture, and the delivery is still refused.
func TestCaptureHook_OversizeRetainsABoundedTruncatedPrefix(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PostToolUse","future":"` + strings.Repeat("x", 64) + `"}`)
	limit := 32

	cap, ev, err := hookio.CaptureHook(raw, limit, "privacy/v1", permitEverything,
		hookio.CaptureFragment{Policy: permitEverything})
	require.ErrorIs(t, err, core.ErrBudget)
	require.Equal(t, core.FidelityTruncated, cap.Fidelity)
	require.Equal(t, core.CaptureErrorOversize, cap.CaptureError)
	require.True(t, cap.Truncated)
	require.Equal(t, len(raw), cap.SourceBytes, "the observed delivery size survives even when the bytes do not")
	require.Equal(t, raw[:limit], cap.Bytes, "the retained prefix is the head of what the host sent")
	require.Equal(t, core.OutcomeUnavailable, cap.Outcome, "a truncated capture is not an observation")
	require.Equal(t, hookio.Event{}, ev, "no Event may be derived from a prefix")
}

// TestCaptureHook_OversizePolicyResultRetainsAPermittedPrefix covers the second oversize gate: the
// payload fits, but what the privacy policy returned does not. The already-permitted bytes are
// re-examined as a fragment before any prefix of them is retained, because a prefix is a narrower
// disclosure than the document the policy actually decided on.
func TestCaptureHook_OversizePolicyResultRetainsAPermittedPrefix(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PostToolUse"}`)
	grown := []byte(`{"hook_event_name":"PostToolUse","added":"by-policy"}`)
	seen := 0

	cap, ev, err := hookio.CaptureHook(raw, len(raw), "privacy/v1", func([]byte) (core.CaptureDecision, error) {
		return core.CaptureDecision{Bytes: grown, Outcome: core.OutcomeOK, Fidelity: core.FidelityPartial}, nil
	}, hookio.CaptureFragment{Policy: func(in []byte) (core.CaptureDecision, error) {
		seen++
		require.Equal(t, grown[:len(raw)], in, "the fragment policy sees the permitted prefix, not the source")
		return permitEverything(in)
	}})
	require.ErrorIs(t, err, core.ErrBudget)
	require.Equal(t, 1, seen)
	require.Equal(t, core.FidelityTruncated, cap.Fidelity)
	require.Equal(t, core.CaptureErrorOversize, cap.CaptureError)
	require.Equal(t, grown[:len(raw)], cap.Bytes)
	require.Equal(t, hookio.Event{}, ev)
}

// TestCaptureHook_NonJSONIsClassifiedBinaryNotRejected pins T20-M1-02's binary case. A payload that
// is not an admissible JSON object used to be a bare contract rejection, which recorded nothing
// about what had arrived. It is now classified with its own fidelity and capture error, so a later
// reader can tell "the host sent something this build cannot parse" from "nothing was delivered".
func TestCaptureHook_NonJSONIsClassifiedBinaryNotRejected(t *testing.T) {
	cases := map[string][]byte{
		"array":        []byte(`["not","an","object"]`),
		"invalid utf8": {'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'},
		"truncated":    []byte(`{"hook_event_name":`),
		"binary":       {0x00, 0x01, 0x02, 0x89, 'P', 'N', 'G'},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			cap, ev, err := hookio.CaptureHook(raw, 4096, "privacy/v1", permitEverything,
				hookio.CaptureFragment{Policy: permitEverything})
			require.Error(t, err)
			require.ErrorIs(t, err, core.ErrDegraded)
			require.NotErrorIs(t, err, core.ErrContract, "classification replaces the bare rejection")
			require.Equal(t, core.FidelityBinary, cap.Fidelity)
			require.Equal(t, core.CaptureErrorNotJSON, cap.CaptureError)
			require.Equal(t, raw, cap.Bytes, "the bytes that arrived are the evidence")
			require.Equal(t, len(raw), cap.SourceBytes)
			require.Equal(t, core.OutcomeUnavailable, cap.Outcome)
			require.Equal(t, hookio.Event{}, ev)
		})
	}
}

// TestCaptureHook_PartialReadIsDistinctFromTruncation pins T20-M1-02's partial case against its
// nearest neighbour. A host read that ended early and a payload this process bounded on purpose
// are different facts about a delivery: one says the sender stopped, the other says the receiver
// chose a limit. They must never be recorded under one label.
func TestCaptureHook_PartialReadIsDistinctFromTruncation(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PostToolUse","future":"complete-json-but-short-read"}`)

	partial, ev, err := hookio.CaptureHook(raw, len(raw), "privacy/v1", permitEverything,
		hookio.CaptureFragment{Policy: permitEverything, Incomplete: true})
	require.ErrorIs(t, err, core.ErrDegraded)
	require.Equal(t, core.FidelityPartial, partial.Fidelity)
	require.Equal(t, core.CaptureErrorIncomplete, partial.CaptureError)
	require.False(t, partial.Truncated, "a short read is not a truncation this process chose")
	require.Equal(t, raw, partial.Bytes)
	require.Equal(t, core.OutcomeUnavailable, partial.Outcome)
	require.Equal(t, hookio.Event{}, ev,
		"a prefix that happens to parse is still a prefix and yields no Event")

	truncated, _, err := hookio.CaptureHook(raw, 16, "privacy/v1", permitEverything,
		hookio.CaptureFragment{Policy: permitEverything})
	require.ErrorIs(t, err, core.ErrBudget)
	require.Equal(t, core.FidelityTruncated, truncated.Fidelity)
	require.NotEqual(t, partial.Fidelity, truncated.Fidelity)
	require.NotEqual(t, partial.CaptureError, truncated.CaptureError)
}

// TestCaptureHook_WithoutAFragmentPolicyNothingIsRetained pins that retention is an explicit
// caller decision. The classification is recorded either way, but a caller that supplies no
// fragment policy keeps the original contract exactly: a degraded delivery leaves no bytes behind.
func TestCaptureHook_WithoutAFragmentPolicyNothingIsRetained(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PostToolUse","future":"` + strings.Repeat("x", 64) + `"}`)

	for _, frag := range []hookio.CaptureFragment{{}, {Policy: func([]byte) (core.CaptureDecision, error) {
		return core.CaptureDecision{Bytes: []byte("leaked"), Outcome: core.OutcomeDenied}, nil
	}}} {
		cap, _, err := hookio.CaptureHook(raw, 32, "privacy/v1", permitEverything, frag)
		require.ErrorIs(t, err, core.ErrBudget)
		require.Equal(t, core.FidelityTruncated, cap.Fidelity, "the classification is recorded regardless")
		require.Equal(t, core.CaptureErrorOversize, cap.CaptureError)
		require.Empty(t, cap.Bytes, "a refusing or absent fragment policy retains nothing")
	}
}

// TestCaptureHook_RecordsObservedHostFieldsWithoutSynthesizingAbsentOnes pins M1-01's "record
// absent fields as absent, not synthetic defaults". Event's fields are value types with usable
// zeros, so a derived Event alone cannot distinguish a prompt the host omitted from one it sent
// empty. The observed key list is what keeps the two apart.
func TestCaptureHook_RecordsObservedHostFieldsWithoutSynthesizingAbsentOnes(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PostToolUse","tool_name":"","future_field":1}`)

	cap, ev, err := hookio.CaptureHook(raw, len(raw), "privacy/v1", permitEverything)
	require.NoError(t, err)
	require.Equal(t, []string{"future_field", "hook_event_name", "tool_name"}, cap.HostFields)
	require.NotContains(t, cap.HostFields, "prompt",
		"a field the host never sent must not appear as observed")
	require.Empty(t, ev.Prompt)
	require.Empty(t, ev.ToolName)
	require.Contains(t, cap.HostFields, "tool_name",
		"an empty value the host did send is observed, unlike one it omitted")
}

// TestCaptureHook_SourceBytesSurviveAPolicyDenial keeps the one fact a denial may always retain.
// The delivery's size is not its content, and losing it would make a denied hook indistinguishable
// from a hook that never ran.
func TestCaptureHook_SourceBytesSurviveAPolicyDenial(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PostToolUse"}`)
	cap, _, err := hookio.CaptureHook(raw, len(raw), "privacy/v1", func([]byte) (core.CaptureDecision, error) {
		return core.CaptureDecision{Outcome: core.OutcomeDenied}, nil
	}, hookio.CaptureFragment{Policy: permitEverything})
	require.NoError(t, err)
	require.Equal(t, core.OutcomeDenied, cap.Outcome)
	require.Equal(t, core.FidelityUnknown, cap.Fidelity)
	require.Equal(t, len(raw), cap.SourceBytes)
	require.Empty(t, cap.Bytes, "a denial never retains bytes, fragment policy or not")
}
