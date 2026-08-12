package checkpoint

import "github.com/qompack/qompack/internal/core"

// FocusOptions configures one FocusInstructions call (00-ARCHITECTURE.md §5.14).
type FocusOptions struct {
	// IncrementalSpan requests the O1 span-narrowing paragraph: the single cheapest line in the
	// whole design relative to what it buys, because it shrinks the most expensive call in the
	// session and operationalizes "never compress a compression" inside a pipeline the plugin
	// otherwise cannot touch (Qompack.md closing note 4).
	IncrementalSpan bool
	// Frontier is the turn index everything before which is already encoded in the checkpoint,
	// and which the span-narrowing paragraph names.
	Frontier core.TurnIndex
	// CheckpointPath is the artifact's path, named in the span-narrowing paragraph so the
	// summarizer can be pointed at it rather than at the transcript.
	CheckpointPath string
	// ForbidSnippets adds the G3.4 instruction that the summary must contain pointers and
	// reasons, never code.
	ForbidSnippets bool
}

// FocusInstructions renders the Qompack.md §8.5 focus-instruction template for c, plus — when
// o.IncrementalSpan is set — the span-narrowing paragraph naming o.CheckpointPath and turn
// o.Frontier. It is emitted through PreCompact's custom_instructions channel.
//
// It returns "" in this build. FocusInstructions has no error return, so core.ErrNotImplemented
// is unavailable to it, and the empty string is the honest zero value: an empty
// custom_instructions field is omitted by hookio.PreCompactOutput entirely, which leaves the
// host's own summarizer behaviour exactly as it would be without the plugin. Any non-empty
// placeholder would instead be injected verbatim into the most expensive call in the session.
// SP-10 owns the real template.
func FocusInstructions(c Checkpoint, ref Ref, o FocusOptions) string { return "" }
