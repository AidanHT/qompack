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
