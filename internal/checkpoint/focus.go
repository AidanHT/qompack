package checkpoint

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

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

// SentinelPhrase is the Qompack-internal marker carried inside the snippet prohibition, and the
// value the state/precompact.json debug record carries as its "sentinel". It lets a human tell at
// a glance that a focus text, or that record, came from this package, and it is a natural phrase
// rather than a marker token so that a summarizer restating the prohibition would keep it intact.
//
// NOTHING asserts on its presence in a transcript, and nothing may (§14). Since C1.18 the focus
// text that carries it reaches no hop at all (see FocusInstructions), so no transcript can hold it.
const SentinelPhrase = "qompack checkpoint"

// The three focus-instruction templates, verbatim from Qompack.md §8.5 and §4.5 as
// plans/V4-SP-10-checkpointer-l4.md §14 quotes them.
//
// They are transcribed character for character on purpose: a paraphrase is focus text §8.5 did
// not authorize. Until C1.18 the daemon returned this text to the hook client as PreCompact
// customInstructions, and contract.CPreCompactCustomInstr searched transcripts for its first line.
// Both are retired — no host accepts a PreCompact instruction — and the text now reaches no hop
// (see FocusInstructions). focus_test.go still pins all three against their quoted text.
const (
	// standingTemplate is paragraph 1: the standing focus instruction, always rendered and always
	// first. It contains no newline, so it is also the whole of the rendered text's first line —
	// see FocusInstructions.
	standingTemplate = "Encode what a competent engineer with no session history would get wrong. " +
		"Do not restate file contents, directory structure, or command output — those are retrievable. " +
		"Prioritise: intent, decisions and their rationale, approaches eliminated and why, and " +
		"constraints discovered empirically."

	// incrementalTemplate is the O1 span-narrowing paragraph. Its three verbs take the checkpoint
	// path and the frontier turn twice: once to say what is already covered, once to say where
	// the summarizer's own span begins.
	incrementalTemplate = "A durable checkpoint (`%s`) fully covers the session through turn %d, " +
		"including all decisions, eliminations, and file state up to that point. Do not re-summarize " +
		"that material. Summarize only what happened after turn %d: new decisions, new eliminations, " +
		"new intent, current work."

	// snippetProhibition is the G3.4 paragraph, and the one that carries SentinelPhrase.
	snippetProhibition = "Do not include code snippets: every file named above is available by " +
		"path and hash through the qompack checkpoint, and a pointer costs roughly 20 tokens where a " +
		"snippet costs roughly 500."
)

// paragraphSep joins the rendered paragraphs. It is a blank line rather than a single newline so
// that paragraph 1 stays the whole of the first LINE, the shape TestFocusFirstLineIsAProbePhrase
// pins (see FocusInstructions for why that shape outlived the probe it was built for).
const paragraphSep = "\n\n"

// maxFocusBytes caps the rendered focus text. A text larger than this is a sign of a bug — the
// three paragraphs together are under 700 bytes — so the cap is a guard against an unbounded
// value, not a budget. It was sized for the PreCompact customInstructions payload C1.18 retired;
// the text now reaches only the state/precompact.json debug record, as instructions_bytes.
const maxFocusBytes = 4000

// FocusInstructions renders the Qompack.md §8.5 focus-instruction template for c, plus — when
// o.IncrementalSpan is set and o.Frontier is past 0 — the O1 span-narrowing paragraph naming
// o.CheckpointPath and turn o.Frontier, plus the G3.4 snippet prohibition when o.ForbidSnippets.
// The paragraphs are joined by a blank line.
//
// THE TEXT REACHES NO HOP (C1.18). FileWriter.PreCompact still renders it and returns it as
// PreCompactResult.Instructions, but the daemon's checkpoint seam discards that result and answers
// the empty object: no host accepts a PreCompact instruction (Claude Code has no PreCompact
// hookSpecificOutput variant, 2.1.280 rejected the whole response over one — C1.12 — and
// custom_instructions is PreCompact INPUT, Qompack.md §7.3). What survives is the
// state/precompact.json debug record, whose instructions_bytes and span_instruction fields
// describe this text. No contract assertion reads either: precompact.custom_instructions_accepted
// reports "retired" without probing anything.
//
// Paragraph 1 is still first and still the whole of the first line — standingTemplate contains no
// newline. Until C1.18 that was load-bearing: the contract probe searched transcripts for the first
// line of the recorded instruction and needed at least 24 runes of it. focus_test.go keeps pinning
// the shape so the rendered text stays §8.5's paragraphs in §8.5's order.
//
// The span paragraph is suppressed at frontier 0 because store.SegmentLog.Frontier returns 0 both
// for "nothing encoded yet" and for "turn 0 covered": naming turn 0 would point the summarizer at
// a span that may not exist.
//
// Advisory handling is normative and this is where it starts: nothing in this package may branch
// on whether the summarizer honoured either paragraph. The checkpoint on disk is the
// authoritative record (§8.5, §12), and TestNoForbiddenImports enforces the rule mechanically —
// internal/checkpoint cannot import hookio and therefore has no transcript to check compliance
// against.
//
// c and ref are unread. The signature is 00-ARCHITECTURE.md §5.14's and the templates are
// deliberately content-free: an instruction that quoted the checkpoint back at the summarizer
// would be paying full transcript price for material the artifact already holds, which is the
// cost O1 exists to avoid.
func FocusInstructions(c Checkpoint, ref Ref, o FocusOptions) string {
	paras := []string{standingTemplate}
	if o.IncrementalSpan && o.Frontier > 0 {
		// ToSlash, not a re-normalization: the caller passes a paths.Norm result, which is
		// already project-relative forward-slash form. This only guarantees that a
		// platform-native spelling reaching this function on Windows cannot put a backslash into
		// the emitted instruction, where the summarizer would read it as an escape.
		paras = append(paras, fmt.Sprintf(incrementalTemplate,
			filepath.ToSlash(o.CheckpointPath), int(o.Frontier), int(o.Frontier)))
	}
	if o.ForbidSnippets {
		paras = append(paras, snippetProhibition)
	}

	out := strings.Join(paras, paragraphSep)
	if len(out) > maxFocusBytes {
		// Truncate at a paragraph boundary, dropping from the end: a half-sentence instruction is
		// worse than a missing one, because a summarizer acts on it either way. Paragraph 1 is
		// never dropped — it is the standing instruction, so the text is never empty.
		kept := paras
		for len(kept) > 1 && len(strings.Join(kept, paragraphSep)) > maxFocusBytes {
			kept = kept[:len(kept)-1]
		}
		pkgLog().Warn("checkpoint: focus instruction over cap, truncated at a paragraph boundary",
			"bytes", len(out), "cap", maxFocusBytes, "paragraphs_dropped", len(paras)-len(kept))
		out = strings.Join(kept, paragraphSep)
	}
	return strings.TrimRight(out, " \t\r\n")
}
