package hookio_test

import (
	"encoding/json"
	"os"
	"sort"
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
