package hookio_test

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/hookio"
)

// schemaDoc is testdata/host/hooks-output-schema.json: the documented host schema, transcribed
// independently of ConformOutput's own table.
type schemaDoc struct {
	Universal []string `json:"universal"`
	Events    map[string]struct {
		TopLevel           []string `json:"topLevel"`
		HookSpecificOutput []string `json:"hookSpecificOutput"`
		Discarded          []string `json:"discarded"`
	} `json:"events"`
}

func loadSchema(t *testing.T) schemaDoc {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/host/hooks-output-schema.json")
	require.NoError(t, err)
	var s schemaDoc
	require.NoError(t, json.Unmarshal(raw, &s))
	return s
}

// everything is an Output with every field set, naming event.
func everything(event string) hookio.Output {
	f, tr := false, true
	return hookio.Output{
		Continue:       &f,
		SuppressOutput: &tr,
		SystemMessage:  "banner",
		HookSpecificOutput: &hookio.HSO{
			HookEventName:      event,
			AdditionalContext:  "context",
			CustomInstructions: "focus",
		},
	}
}

// TestConformOutput_OnlyDocumentedFieldsSurvive checks, for every event the schema documents, that
// the most any daemon could say is reduced to fields the host accepts and does not discard.
func TestConformOutput_OnlyDocumentedFieldsSurvive(t *testing.T) {
	s := loadSchema(t)
	require.Len(t, s.Events, 7, "the schema documents the seven events Qompack registers")
	for event, ev := range s.Events {
		t.Run(event, func(t *testing.T) {
			require.True(t, hookio.KnownEvent(event), "ConformOutput has no contract for %s", event)
			raw, err := json.Marshal(hookio.ConformOutput(event, everything(event)))
			require.NoError(t, err)

			var doc map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &doc))
			allowed := map[string]bool{}
			for _, k := range append(append([]string{}, s.Universal...), ev.TopLevel...) {
				allowed[k] = true
			}
			for _, k := range ev.Discarded {
				delete(allowed, k)
			}
			for key, val := range doc {
				if key == "hookSpecificOutput" {
					require.NotNil(t, ev.HookSpecificOutput, "%s has no hookSpecificOutput variant: %s", event, raw)
					var hso map[string]any
					require.NoError(t, json.Unmarshal(val, &hso))
					require.Equal(t, event, hso["hookEventName"])
					fields := map[string]bool{"hookEventName": true}
					for _, f := range ev.HookSpecificOutput {
						fields[f] = true
					}
					for f := range hso {
						require.True(t, fields[f], "%s: hookSpecificOutput.%s is not accepted: %s", event, f, raw)
					}
					continue
				}
				require.True(t, allowed[key], "%s: top-level %q is not accepted or is discarded: %s", event, key, raw)
			}
		})
	}
}

// TestConformOutput_Exact pins each event's reduction of the maximal reply.
func TestConformOutput_Exact(t *testing.T) {
	cases := map[string]string{
		hookio.EventSessionStart:     `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"context"},"systemMessage":"banner"}` + "\n",
		hookio.EventUserPromptSubmit: `{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"context"},"systemMessage":"banner"}` + "\n",
		hookio.EventPostToolUse:      `{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"context"},"systemMessage":"banner"}` + "\n",
		// Stop/SubagentStop additionalContext keeps Claude from stopping; a recorder never may.
		hookio.EventStop:         `{"systemMessage":"banner"}` + "\n",
		hookio.EventSubagentStop: `{"systemMessage":"banner"}` + "\n",
		// No PreCompact hookSpecificOutput variant exists, and systemMessage/continue are discarded.
		hookio.EventPreCompact: "{}\n",
		hookio.EventSessionEnd: "{}\n",
	}
	names := make([]string, 0, len(cases))
	for n := range cases {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, event := range names {
		require.Equal(t, cases[event], writeOutput(t, hookio.ConformOutput(event, everything(event))), event)
	}
}

// TestConformOutput_PreCompactOutputNeverReachesTheHost is C1.12 at the unit level: the reply the
// daemon's checkpoint seam builds conforms to the empty object.
func TestConformOutput_PreCompactOutputNeverReachesTheHost(t *testing.T) {
	got := hookio.ConformOutput(hookio.EventPreCompact, hookio.PreCompactOutput("Encode what a competent engineer ..."))
	require.Equal(t, "{}\n", writeOutput(t, got))
}

// TestConformOutput_ForeignOrEmptyHSOIsDropped covers a hookSpecificOutput the host would reject
// (another event's name) and one that says nothing (an empty additionalContext).
func TestConformOutput_ForeignOrEmptyHSOIsDropped(t *testing.T) {
	foreign := hookio.Output{HookSpecificOutput: &hookio.HSO{HookEventName: hookio.EventPreCompact, AdditionalContext: "x"}}
	require.Equal(t, "{}\n", writeOutput(t, hookio.ConformOutput(hookio.EventSessionStart, foreign)))

	require.Equal(t, "{}\n", writeOutput(t, hookio.ConformOutput(hookio.EventSessionStart, hookio.SessionStartOutput(""))))
}

// TestConformOutput_UnknownEventIsEmpty: an event without a contract gets nothing.
func TestConformOutput_UnknownEventIsEmpty(t *testing.T) {
	require.False(t, hookio.KnownEvent("PostCompact"))
	require.Equal(t, "{}\n", writeOutput(t, hookio.ConformOutput("PostCompact", everything("PostCompact"))))
	require.Equal(t, "{}\n", writeOutput(t, hookio.ConformOutput("", everything(""))))
}

// TestConformOutput_DoesNotAliasItsInput: the result shares no pointer with o, so a caller that
// mutates the daemon's reply afterwards cannot change what was written.
func TestConformOutput_DoesNotAliasItsInput(t *testing.T) {
	in := everything(hookio.EventSessionStart)
	out := hookio.ConformOutput(hookio.EventSessionStart, in)
	require.NotSame(t, in.HookSpecificOutput, out.HookSpecificOutput)
	in.HookSpecificOutput.AdditionalContext = "changed"
	require.Equal(t, "context", out.HookSpecificOutput.AdditionalContext)
}

// capDoc is the host's per-field size limit as testdata/host/hooks-output-schema.json transcribes it.
type capDoc struct {
	Limits struct {
		MaxChars     int      `json:"maxChars"`
		PreviewChars int      `json:"previewChars"`
		Fields       []string `json:"fields"`
	} `json:"limits"`
}

// TestHostFieldCap_MatchesTheTranscribedSchema ties hookio's cap constants to the independent
// transcription of the hooks reference, so neither can drift alone.
func TestHostFieldCap_MatchesTheTranscribedSchema(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/host/hooks-output-schema.json")
	require.NoError(t, err)
	var d capDoc
	require.NoError(t, json.Unmarshal(raw, &d))
	require.Equal(t, d.Limits.MaxChars, hookio.HostFieldMaxChars)
	require.Equal(t, d.Limits.PreviewChars, hookio.HostFieldPreviewChars)
	for _, f := range []string{"additionalContext", "systemMessage"} {
		require.Contains(t, d.Limits.Fields, f, "the cap covers %s, which ConformOutput can keep", f)
	}
}

// TestHostChars_CountsUTF16CodeUnits pins the unit the host measures its cap in — JavaScript's
// String.prototype.length on the parsed field, read from Claude Code 2.1.280's own code
// (plans/sdd/V6-closeout/rehydrate-cap/evidence/host-cap-unit.txt) — which internal/rehydrate
// restates for its ceiling and ties back to this function.
func TestHostChars_CountsUTF16CodeUnits(t *testing.T) {
	require.Equal(t, 0, hookio.HostChars(""))
	require.Equal(t, 3, hookio.HostChars("abc"))
	require.Equal(t, 1, hookio.HostChars("é"), "a two-byte UTF-8 rune is one unit")
	require.Equal(t, 1, hookio.HostChars("中"), "a three-byte UTF-8 rune is one unit")
	require.Equal(t, 2, hookio.HostChars("\U0001F600"), "an astral rune is a surrogate pair")
	require.Equal(t, 2, hookio.HostChars(string([]byte{0xff, 0xfe})),
		"each invalid byte reaches the host as one U+FFFD")
}

// TestHostCapOverruns reports exactly the kept fields the host would swap for a file path and a
// 2,000-character preview, measured the way a JavaScript host measures a string: UTF-16 code units.
func TestHostCapOverruns(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	ss := func(ctx, msg string) hookio.Output {
		o := hookio.SessionStartOutput(ctx)
		o.SystemMessage = msg
		return o
	}

	require.Empty(t, hookio.HostCapOverruns(hookio.Empty()))
	require.Empty(t, hookio.HostCapOverruns(ss(long(hookio.HostFieldMaxChars), "")),
		"a field AT the cap is delivered whole")
	require.Equal(t, []hookio.FieldOverrun{{Field: "hookSpecificOutput.additionalContext", Chars: hookio.HostFieldMaxChars + 1}},
		hookio.HostCapOverruns(ss(long(hookio.HostFieldMaxChars+1), "")))
	require.Equal(t, []hookio.FieldOverrun{
		{Field: "hookSpecificOutput.additionalContext", Chars: 20000},
		{Field: "systemMessage", Chars: 10001},
	}, hookio.HostCapOverruns(ss(long(20000), long(10001))), "each field is measured on its own")

	// U+1F600 is one rune but two UTF-16 code units: 5,001 of them are 10,002 host characters.
	astral := strings.Repeat("\U0001F600", 5001)
	require.Equal(t, []hookio.FieldOverrun{{Field: "hookSpecificOutput.additionalContext", Chars: 10002}},
		hookio.HostCapOverruns(ss(astral, "")))
	require.Empty(t, hookio.HostCapOverruns(ss(strings.Repeat("é", hookio.HostFieldMaxChars), "")),
		"a BMP rune is one host character however many UTF-8 bytes it takes")
}
