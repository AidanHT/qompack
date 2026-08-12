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
type HSO struct {
	HookEventName string `json:"hookEventName"`
	// AdditionalContext is SessionStart's injection channel.
	AdditionalContext string `json:"additionalContext,omitempty"`
	// CustomInstructions is PreCompact's focus-instruction channel (Qompack.md §8.5).
	CustomInstructions string `json:"customInstructions,omitempty"`
}

// The hook event names SessionStartOutput and PreCompactOutput stamp into HSO.HookEventName, so
// that exact string appears exactly once in the codebase for each.
const (
	hookEventSessionStart = "SessionStart"
	hookEventPreCompact   = "PreCompact"
)

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
	return Output{HookSpecificOutput: &HSO{HookEventName: hookEventSessionStart, AdditionalContext: ctx}}
}

// PreCompactOutput builds the PreCompact response carrying instr as customInstructions
// (Qompack.md §8.5's focus instruction). An empty instr omits the field entirely, so
// PreCompactOutput("") serializes to {"hookSpecificOutput":{"hookEventName":"PreCompact"}}.
func PreCompactOutput(instr string) Output {
	return Output{HookSpecificOutput: &HSO{HookEventName: hookEventPreCompact, CustomInstructions: instr}}
}
