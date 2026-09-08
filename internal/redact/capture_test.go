package redact_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/redact"
)

func TestCapturePolicy_PreservesUnchangedJSONBytes(t *testing.T) {
	p, err := redact.CapturePolicy(config.Defaults())
	require.NoError(t, err)
	raw := []byte(" {\n  \"session_id\" : \"s\", \"future\": [9007199254740993, \"a\\u0062\", \"\\ud83d\\ude00\", \"�\"]\n } \t")
	decision, err := p(raw)
	require.NoError(t, err)
	require.Equal(t, core.OutcomeOK, decision.Outcome)
	require.Equal(t, raw, decision.Bytes)
	require.Equal(t, core.FidelityExact, decision.Fidelity)
}

func TestCapturePolicy_RedactsDecodedAndStructuredSecrets(t *testing.T) {
	p, err := redact.CapturePolicy(config.Defaults())
	require.NoError(t, err)
	for _, raw := range []string{
		`{"unknown":"sk-abcdefghijklmnopqrstuv"}`,
		`{"unknown":"sk-abcdefghij\u006blmnopqrstuv"}`,
		`{"tool_response":{"password":"tiny"}}`,
		`{"tool_response":{"password":123456789}}`,
		`{"tool_response":{"password":{"nested":"private fixture"}}}`,
		`{"unknown":["sk-abcdefghijklmnopqrstuv"]}`,
		`{"sk-abcdefghijklmnopqrstuv":"ordinary"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			decision, err := p([]byte(raw))
			require.NoError(t, err)
			require.Equal(t, core.OutcomeOK, decision.Outcome)
			require.True(t, json.Valid(decision.Bytes))
			for _, private := range []string{"abcdefghijklmnopqrstuv", "tiny", "123456789", "private fixture"} {
				require.NotContains(t, string(decision.Bytes), private)
			}
			require.Contains(t, string(decision.Bytes), "redacted:")
			require.True(t, decision.Redacted)
			require.Equal(t, core.FidelityRedacted, decision.Fidelity)
			again, err := p(decision.Bytes)
			require.NoError(t, err)
			require.Equal(t, core.OutcomeOK, again.Outcome)
			require.Equal(t, decision.Bytes, again.Bytes)
		})
	}
}

func TestCapturePolicy_RejectsSyntaxSpanningPatternsAndExcessDepth(t *testing.T) {
	cfg := config.Defaults()
	cfg.Runtime.Redact.Patterns = []string{`"private":"fixture"`}
	p, err := redact.CapturePolicy(cfg)
	require.NoError(t, err)
	for _, raw := range [][]byte{
		[]byte(`{"private":"fixture"}`),
		append(append(bytes.Repeat([]byte("["), 130), '0'), bytes.Repeat([]byte("]"), 130)...),
	} {
		decision, err := p(raw)
		require.Error(t, err)
		require.Equal(t, core.OutcomeUnavailable, decision.Outcome)
		require.Empty(t, decision.Bytes)
	}
}

func TestCapturePolicy_RawEscapePatternCannotDisappearOnDecoding(t *testing.T) {
	cfg := config.Defaults()
	cfg.Runtime.Redact.Patterns = []string{`\\u0066ixture`}
	p, err := redact.CapturePolicy(cfg)
	require.NoError(t, err)
	decision, err := p([]byte(`{"unknown":"\u0066ixture"}`))
	require.NoError(t, err)
	require.Equal(t, core.OutcomeOK, decision.Outcome)
	require.Equal(t, core.FidelityRedacted, decision.Fidelity)
	require.NotContains(t, string(decision.Bytes), "fixture")
}

func TestCapturePolicy_StrictRulesAndDisabledMode(t *testing.T) {
	for _, pattern := range []string{"[private-fixture", ".*", "x"} {
		cfg := config.Defaults()
		cfg.Runtime.Redact.Patterns = []string{pattern}
		p, err := redact.CapturePolicy(cfg)
		require.Error(t, err)
		require.Nil(t, p)
		require.NotContains(t, err.Error(), pattern)
	}
	cfg := config.Defaults()
	cfg.Runtime.Redact.Enabled = false
	cfg.Runtime.Redact.Patterns = []string{"[invalid-but-disabled"}
	p, err := redact.CapturePolicy(cfg)
	require.NoError(t, err)
	raw := []byte(`{"password":"permitted by explicit disabled configuration"}`)
	decision, err := p(raw)
	require.NoError(t, err)
	require.Equal(t, core.OutcomeOK, decision.Outcome)
	require.Equal(t, raw, decision.Bytes)
}

func TestCapturePolicy_RejectsAmbiguousOrInvalidJSON(t *testing.T) {
	p, err := redact.CapturePolicy(config.Defaults())
	require.NoError(t, err)
	for _, raw := range [][]byte{
		[]byte(`{"same":"one","same":"two"}`),
		[]byte(`{"same":"one","\u0073ame":"two"}`),
		[]byte(`{"private fixture":`),
		[]byte(`{"x":"\ud800"}`),
		{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'},
	} {
		decision, err := p(raw)
		require.Error(t, err)
		require.Nil(t, decision.Bytes)
		require.Equal(t, core.OutcomeUnavailable, decision.Outcome)
		require.NotContains(t, err.Error(), "private fixture")
	}
}

func TestCapturePolicy_RefusesRedactedKeyCollisions(t *testing.T) {
	cfg := config.Defaults()
	cfg.Runtime.Redact.Patterns = []string{`sensitive-[ab]`}
	p, err := redact.CapturePolicy(cfg)
	require.NoError(t, err)
	decision, err := p([]byte(`{"sensitive-a":1,"sensitive-b":2}`))
	require.Error(t, err)
	require.Nil(t, decision.Bytes)
	require.Equal(t, core.OutcomeUnavailable, decision.Outcome)
}

func TestCapturePolicy_CustomPatternSeesDecodedUnknownFields(t *testing.T) {
	cfg := config.Defaults()
	cfg.Runtime.Redact.Patterns = []string{`private-fixture`}
	p, err := redact.CapturePolicy(cfg)
	require.NoError(t, err)
	decision, err := p([]byte(`{"unknown":"private-\u0066ixture","n":9007199254740993}`))
	require.NoError(t, err)
	require.Equal(t, core.OutcomeOK, decision.Outcome)
	require.False(t, bytes.Contains(decision.Bytes, []byte("private-")))
	require.Contains(t, string(decision.Bytes), "9007199254740993")
}

// TestCaptureFragmentPolicy_RedactsBytesWithNoJSONStructure covers the fragments CapturePolicy
// cannot decide: an oversize prefix, a payload the host stopped sending, or bytes that are not JSON
// at all. The same configured rules still apply, so a secret in a fragment is removed before the
// fragment is retained as evidence.
func TestCaptureFragmentPolicy_RedactsBytesWithNoJSONStructure(t *testing.T) {
	cfg := config.Defaults()
	cfg.Runtime.Redact.Patterns = []string{"PRIVATE-[A-Z]{12}"}
	p, err := redact.CaptureFragmentPolicy(cfg)
	require.NoError(t, err)

	decision, err := p([]byte(`{"hook_event_name":"PostToolUse","future":"PRIVATE-ABCDEFGHIJKL`))
	require.NoError(t, err)
	require.Equal(t, core.OutcomeOK, decision.Outcome)
	require.True(t, decision.Redacted)
	require.Equal(t, core.FidelityRedacted, decision.Fidelity)
	require.NotContains(t, string(decision.Bytes), "PRIVATE-ABCDEFGHIJKL")
	require.Contains(t, string(decision.Bytes), "PostToolUse", "non-secret evidence survives")
}

// TestCaptureFragmentPolicy_RetainsNonUTF8EvidenceVerbatim pins the difference from CapturePolicy:
// a fragment is evidence about a delivery, not a document, so bytes that could never be valid JSON
// are returned as they arrived instead of refusing the whole fragment.
func TestCaptureFragmentPolicy_RetainsNonUTF8EvidenceVerbatim(t *testing.T) {
	p, err := redact.CaptureFragmentPolicy(config.Defaults())
	require.NoError(t, err)
	raw := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0xff, 0xfe}

	decision, err := p(raw)
	require.NoError(t, err)
	require.Equal(t, core.OutcomeOK, decision.Outcome)
	require.False(t, decision.Redacted)
	require.Equal(t, raw, decision.Bytes)

	json, err := redact.CapturePolicy(config.Defaults())
	require.NoError(t, err)
	structured, err := json(raw)
	require.Error(t, err, "the JSON policy still refuses what it cannot parse")
	require.Equal(t, core.OutcomeUnavailable, structured.Outcome)
}

// TestCaptureFragmentPolicy_RefusesAnIncompleteRuleSet keeps the fragment path under the same
// all-or-nothing rule compilation as the JSON path: a pattern that will not compile refuses the
// policy outright rather than silently examining fragments under a partial rule set.
func TestCaptureFragmentPolicy_RefusesAnIncompleteRuleSet(t *testing.T) {
	cfg := config.Defaults()
	cfg.Runtime.Redact.Patterns = []string{"["}
	p, err := redact.CaptureFragmentPolicy(cfg)
	require.ErrorIs(t, err, core.ErrDegraded)
	require.Nil(t, p)
}
