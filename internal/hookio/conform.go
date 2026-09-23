package hookio

import "unicode/utf16"

// The host events Qompack registers (internal/pluginmanifest's hook table). hookio is foundation-free
// and cannot import pluginmanifest, so the seven names are restated here; internal/cli's
// host-contract tests drive every manifest entry point through ConformOutput, which is what ties the
// two tables together.
const (
	EventSessionStart     = "SessionStart"
	EventUserPromptSubmit = "UserPromptSubmit"
	EventPostToolUse      = "PostToolUse"
	EventStop             = "Stop"
	EventSubagentStop     = "SubagentStop"
	EventPreCompact       = "PreCompact"
	EventSessionEnd       = "SessionEnd"
)

// hostEventOutput is what one host event accepts from Qompack AND acts on.
type hostEventOutput struct {
	// systemMessage: the host shows it to the user.
	systemMessage bool
	// additionalContext: the host adds hookSpecificOutput.additionalContext to Claude's context.
	additionalContext bool
}

// hostOutputs is the per-event output contract, from the Claude Code hooks reference
// (https://code.claude.com/docs/en/hooks, fetched 2026-09-22) and Claude Code 2.1.280's own
// validator. A field is kept only when the host both ACCEPTS it for the event and ACTS on it:
//
//   - SessionStart, UserPromptSubmit, PostToolUse: systemMessage is a universal field shown to the
//     user, and each event has a hookSpecificOutput variant carrying additionalContext.
//   - Stop, SubagentStop: systemMessage is accepted. hookSpecificOutput.additionalContext is
//     accepted too, but "the conversation continues so Claude can act on it" — it keeps Claude from
//     stopping. Qompack observes these events and must never keep a turn alive, so it is dropped.
//   - PreCompact: there is NO hookSpecificOutput variant; 2.1.280 rejects hookEventName
//     "PreCompact" and the whole response with it. "Claude Code discards a PreCompact hook's
//     systemMessage and continue fields." custom_instructions is PreCompact INPUT (what the user
//     typed after /compact), never an output setter, so no field of Qompack's survives.
//   - SessionEnd: "Claude Code discards their JSON output fields", so nothing survives.
//
// `continue` and `suppressOutput` are dropped for every event: continue:false stops Claude entirely,
// which a recording plugin never may do, and suppressOutput "has no effect".
var hostOutputs = map[string]hostEventOutput{
	EventSessionStart:     {systemMessage: true, additionalContext: true},
	EventUserPromptSubmit: {systemMessage: true, additionalContext: true},
	EventPostToolUse:      {systemMessage: true, additionalContext: true},
	EventStop:             {systemMessage: true},
	EventSubagentStop:     {systemMessage: true},
	EventPreCompact:       {},
	EventSessionEnd:       {},
}

// ConformOutput returns o restricted to what the host accepts and acts on for event, the hook event
// being answered. It is the last step before any byte reaches the host's stdout, and it exists
// because the daemon's reply is an internal protocol: a daemon of any version may say more than the
// host takes, and a single rejected field makes the host drop the WHOLE response — and, for
// PreCompact, replay the rejection text into the post-compaction context (C1.12).
//
// A hookSpecificOutput survives only when its hookEventName is event and it carries a field the
// event accepts; one naming another event is a response the host would reject, so it is dropped.
// An event this build does not know gets the empty response.
func ConformOutput(event string, o Output) Output {
	rule, ok := hostOutputs[event]
	if !ok {
		return Empty()
	}
	var out Output
	if rule.systemMessage {
		out.SystemMessage = o.SystemMessage
	}
	if rule.additionalContext && o.HookSpecificOutput != nil &&
		o.HookSpecificOutput.HookEventName == event && o.HookSpecificOutput.AdditionalContext != "" {
		out.HookSpecificOutput = &HSO{HookEventName: event, AdditionalContext: o.HookSpecificOutput.AdditionalContext}
	}
	return out
}

// The host's per-field size limit, from the same hooks reference: "A hook's additionalContext,
// systemMessage, and initialUserMessage strings, and its plain stdout, are capped at 10,000
// characters". It is not a rejection — the host accepts the response — but over it "Claude Code
// saves the output to a file in the session directory and replaces it with the file path and a
// preview of up to the first 2,000 characters", and "doesn't ask Claude to read the file". Each
// field is measured on its own, and "this cap has no setting or environment variable to raise it".
const (
	// HostFieldMaxChars is the longest field the host delivers whole. It is the host's number, not
	// Qompack's: no config key may change it, because "this cap has no setting or environment
	// variable to raise it". It equals sketches.bloom.capacity's default only by coincidence.
	HostFieldMaxChars = 10000 //nomagic:allow Claude Code's documented hook-field cap, not a config default; unrelated to sketches.bloom.capacity
	// HostFieldPreviewChars is how much of an over-cap field Claude sees in its place.
	HostFieldPreviewChars = 2000
)

// FieldOverrun is one output field longer than HostFieldMaxChars.
type FieldOverrun struct {
	// Field is the field's JSON path in the host response, e.g. "hookSpecificOutput.additionalContext".
	Field string
	// Chars is its length in host characters (HostChars).
	Chars int
}

// HostCapOverruns returns every field of o the host will replace with a file path and a preview,
// in response order. o is what the host will actually receive, i.e. ConformOutput's result; the
// hook client makes each overrun loud, because a rehydration the host cuts to its first 2,000
// characters fails no check anywhere else (C1.12 review, finding 2).
func HostCapOverruns(o Output) []FieldOverrun {
	var out []FieldOverrun
	if o.HookSpecificOutput != nil {
		if n := HostChars(o.HookSpecificOutput.AdditionalContext); n > HostFieldMaxChars {
			out = append(out, FieldOverrun{Field: "hookSpecificOutput.additionalContext", Chars: n})
		}
	}
	if n := HostChars(o.SystemMessage); n > HostFieldMaxChars {
		out = append(out, FieldOverrun{Field: "systemMessage", Chars: n})
	}
	return out
}

// HostChars is s's length as Claude Code measures a hook field against HostFieldMaxChars: UTF-16
// code units, JavaScript's String.prototype.length. The hooks reference says only "characters";
// the unit is read from the host's own code (2.1.280 tests `field.length <= 1e4` on the parsed
// string, so a field of exactly the cap is delivered whole), recorded in
// plans/sdd/V6-closeout/rehydrate-cap/evidence/host-cap-unit.txt. It is never smaller than the rune
// count, so an overrun it misses is one no reading of "characters" would report. It is exported so
// that a producer bounding its own output (internal/rehydrate, which may not import this package)
// can be tested against the very function the hook client measures with.
func HostChars(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// KnownEvent reports whether ConformOutput has a contract for event.
func KnownEvent(event string) bool {
	_, ok := hostOutputs[event]
	return ok
}
