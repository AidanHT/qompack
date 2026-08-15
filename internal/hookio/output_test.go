package hookio_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/hookio"
)

func writeOutput(t *testing.T, o hookio.Output) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, hookio.WriteOutput(&buf, o))
	return buf.String()
}

func TestWriteOutput_EmptyIsMinimal(t *testing.T) {
	require.Equal(t, "{}\n", writeOutput(t, hookio.Empty()))
}

func TestWriteOutput_NoHTMLEscaping(t *testing.T) {
	got := writeOutput(t, hookio.Output{SystemMessage: "a<b&c>d"})
	// With SetEscapeHTML(false), the angle brackets and ampersand survive as literal bytes. If
	// HTML-escaping were active, encoding/json would emit their six-byte unicode escapes instead
	// and this exact equality would fail.
	require.Equal(t, `{"systemMessage":"a<b&c>d"}`+"\n", got)
}

func TestSessionStartOutput_Shape(t *testing.T) {
	got := writeOutput(t, hookio.SessionStartOutput("ctx"))
	require.Equal(t, `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"ctx"}}`+"\n", got)
}

func TestSessionStartOutput_EmptyContextOmitsField(t *testing.T) {
	got := writeOutput(t, hookio.SessionStartOutput(""))
	require.Equal(t, `{"hookSpecificOutput":{"hookEventName":"SessionStart"}}`+"\n", got)
}

func TestPreCompactOutput_Shape(t *testing.T) {
	got := writeOutput(t, hookio.PreCompactOutput("narrow the summary to turns 40-52"))
	require.Equal(t,
		`{"hookSpecificOutput":{"hookEventName":"PreCompact","customInstructions":"narrow the summary to turns 40-52"}}`+"\n",
		got)
}

func TestPreCompactOutput_EmptyInstructionOmitsField(t *testing.T) {
	got := writeOutput(t, hookio.PreCompactOutput(""))
	require.Equal(t, `{"hookSpecificOutput":{"hookEventName":"PreCompact"}}`+"\n", got)
}

func TestWriteOutput_FullShape(t *testing.T) {
	yes := true
	got := writeOutput(t, hookio.Output{
		Continue:       &yes,
		SuppressOutput: &yes,
		SystemMessage:  "note",
		HookSpecificOutput: &hookio.HSO{
			HookEventName:      "PreCompact",
			AdditionalContext:  "ctx",
			CustomInstructions: "instr",
		},
	})
	require.JSONEq(t, `{
		"continue": true,
		"suppressOutput": true,
		"systemMessage": "note",
		"hookSpecificOutput": {
			"hookEventName": "PreCompact",
			"additionalContext": "ctx",
			"customInstructions": "instr"
		}
	}`, got)
}
