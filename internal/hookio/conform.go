package hookio

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

// KnownEvent reports whether ConformOutput has a contract for event.
func KnownEvent(event string) bool {
	_, ok := hostOutputs[event]
	return ok
}
