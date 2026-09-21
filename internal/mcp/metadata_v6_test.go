package mcp

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestV6_MetadataUsesCurrentRedactionPolicy(t *testing.T) {
	f := newFixture(t)
	h := newHandlers(f.Deps)
	for _, tool := range []string{ToolWhy, ToolDropped, ToolTimeline} {
		res := h.jsonResponse(tool, map[string]any{
			"nested": []any{map[string]string{"reason": "aws_access_key_id = " + secretAWSExampleKey}},
		}, nil)
		require.NotContains(t, res.Content[0].Text, secretAWSExampleKey)
		require.Contains(t, res.Content[0].Text, "«redacted:")
		require.True(t, json.Valid([]byte(res.Content[0].Text)), "redaction must preserve the JSON structure")
	}
}

func TestV6_MetadataWithoutPrivacyPolicyIsUnavailable(t *testing.T) {
	f := newFixture(t, withoutRedactor())
	h := newHandlers(f.Deps)
	for _, tool := range []string{ToolWhy, ToolDropped, ToolTimeline} {
		res := h.jsonResponse(tool, map[string]string{"reason": "PRIVATE SUMMARY"}, nil)
		require.NotContains(t, res.Content[0].Text, "PRIVATE SUMMARY")
		var body missBody
		require.NoError(t, json.Unmarshal([]byte(res.Content[0].Text), &body))
		require.NotNil(t, body.Available)
		require.False(t, *body.Available)
	}
}
