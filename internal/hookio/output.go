package hookio

import (
	"encoding/json"
	"io"
)

// Output is the typed form of a hook subcommand's JSON response on stdout
// (00-ARCHITECTURE.md §5.3). Every field is optional: the zero value Output{} serializes to the
// minimal, valid `{}` response Empty returns.
type Output struct {
	Continue           *bool  `json:"continue,omitempty"`
	SuppressOutput     *bool  `json:"suppressOutput,omitempty"`
	HookSpecificOutput *HSO   `json:"hookSpecificOutput,omitempty"`
	SystemMessage      string `json:"systemMessage,omitempty"`
}

// HSO is Output's hookSpecificOutput object: per-hook-type fields Claude Code interprets
// differently depending on which hook produced them.
//
// Output travels two hops, and only the second one is the host's. The daemon answers the hook
// client over IPC with an Output, and the client writes ConformOutput(event, thatOutput) to the
// host. Every field here is therefore legal on the IPC hop, and ConformOutput decides which of them
// the host ever sees.
type HSO struct {
	HookEventName string `json:"hookEventName"`
	// AdditionalContext is the injection channel of SessionStart, UserPromptSubmit and PostToolUse.
	AdditionalContext string `json:"additionalContext,omitempty"`
	// CustomInstructions is RETIRED (C1.18): no producer in this build sets it. A daemon before
	// C1.18 sent the checkpointer's focus instruction (Qompack.md §8.5, O1) here on its reply to the
	// checkpoint route, but no host event accepts it — Claude Code has no PreCompact
	// hookSpecificOutput variant and 2.1.280 rejected the whole response over it (C1.12), and
	// custom_instructions is PreCompact INPUT, not a summarizer-output setter (§7.3; §8.5 retires
	// O1's output setter). The field stays decodable because such a daemon can still be resident
	// after an upgrade and answer a new hook client over IPC; ConformOutput drops it for every
	// event, so it never reaches the host whoever sent it.
	CustomInstructions string `json:"customInstructions,omitempty"`
}

// WriteOutput marshals o to w with HTML-escaping disabled (so "<", ">" and "&" in, for instance, a
// system message survive unescaped) and a single trailing newline, matching the NDJSON-friendly
// shape every other on-disk/on-wire writer in this codebase uses.
func WriteOutput(w io.Writer, o Output) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(o)
}

// Empty returns the zero-cost response: PostToolUse (and every hook with nothing to say) writes
// this in the normal case. It serializes to exactly "{}\n".
func Empty() Output { return Output{} }

// SessionStartOutput builds the SessionStart response carrying ctx as additionalContext. An empty
// ctx omits the field entirely, so SessionStartOutput("") serializes to
// {"hookSpecificOutput":{"hookEventName":"SessionStart"}}.
func SessionStartOutput(ctx string) Output {
	return Output{HookSpecificOutput: &HSO{HookEventName: EventSessionStart, AdditionalContext: ctx}}
}
