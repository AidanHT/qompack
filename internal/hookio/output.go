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
	// CustomInstructions carries the checkpointer's focus instruction (Qompack.md §8.5, O1) from the
	// daemon to the hook client, and no further. No host event accepts it: Claude Code has no
	// PreCompact hookSpecificOutput variant and 2.1.280 rejected the whole response over it (C1.12);
	// custom_instructions is PreCompact INPUT, not a summarizer-output setter (Qompack.md §7.3), and
	// §8.5 retires O1's output setter. ConformOutput drops it for every event. It stays on the IPC
	// hop only because the daemon still produces and records it (contract.History.PrecompactInstr).
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

// PreCompactOutput builds the daemon's IPC reply to the checkpoint route carrying instr as
// customInstructions (Qompack.md §8.5's focus instruction). An empty instr omits the field entirely,
// so PreCompactOutput("") serializes to {"hookSpecificOutput":{"hookEventName":"PreCompact"}}.
//
// It is an IPC value, never a host response: the host rejects any PreCompact hookSpecificOutput, so
// what `qompack checkpoint` writes is ConformOutput(EventPreCompact, PreCompactOutput(instr)), which
// is always the empty object (see HSO.CustomInstructions).
func PreCompactOutput(instr string) Output {
	return Output{HookSpecificOutput: &HSO{HookEventName: EventPreCompact, CustomInstructions: instr}}
}
