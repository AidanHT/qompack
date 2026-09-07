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

// SentinelPhrase is the Qompack-internal marker carried inside the snippet prohibition. It exists
// so a human — or /qompack:status, or an e2e test — can tell at a glance that a given
// customInstructions payload came from this package, and it is a natural phrase rather than a
// marker token so that a summarizer restating the prohibition keeps it intact.
//
// NOTHING asserts on it. contract.CPreCompactCustomInstr probes the FIRST LINE of the emitted
// instruction (paragraph 1), never the sentinel, so no contract assertion may be built on this
// constant's presence in a transcript (§14).
const SentinelPhrase = "qompack checkpoint"

// The three focus-instruction templates, verbatim from Qompack.md §8.5 and §4.5 as
// plans/V4-SP-10-checkpointer-l4.md §14 quotes them.
//
// They are transcribed character for character on purpose. A paraphrase makes the plugin emit an
// instruction §8.5 did not authorize, and — because contract.CPreCompactCustomInstr probes
// whatever the emitted first line happens to be — silently re-keys that probe against every
// transcript already written, so the assertion goes on passing while observing a phrase no
// summarizer ever saw. focus_test.go pins all three against their quoted text.
const (
	// standingTemplate is paragraph 1: the standing focus instruction, always emitted and always
	// first. It contains no newline, so it is also the whole of the emitted instruction's first
	// line — see FocusInstructions.
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

// paragraphSep joins the emitted paragraphs. It is a blank line rather than a single newline
// because paragraph 1 must be the whole of the first LINE: a single newline would split it and
// leave contract.probePhrase with a truncated phrase.
const paragraphSep = "\n\n"

// maxFocusBytes caps the emitted custom_instructions payload. A payload larger than this is a
// sign of a bug — the three paragraphs together are under 700 bytes — so the cap is a guard
// against an unbounded value reaching the most expensive call in the session, not a budget.
const maxFocusBytes = 4000

// FocusInstructions renders the Qompack.md §8.5 focus-instruction template for c, plus — when
// o.IncrementalSpan is set and o.Frontier is past 0 — the O1 span-narrowing paragraph naming
// o.CheckpointPath and turn o.Frontier, plus the G3.4 snippet prohibition when o.ForbidSnippets.
// The paragraphs are joined by a blank line and the result is emitted through PreCompact's
// custom_instructions channel.
//
// PARAGRAPH 1'S POSITION IS LOAD-BEARING. contract.CPreCompactCustomInstr's probe is
// probePhrase(History.PrecompactInstr): the first line of the emitted instruction, required to be
// at least 24 runes (internal/contract/assertions.go). standingTemplate contains no newline, so
// the first line is the whole paragraph — comfortably over the floor and specific enough not to
// false-positive. Emitting anything shorter first (a heading, a bullet marker, a leading blank
// line) would make that assertion report "no probe phrase long enough" forever, which is a
// permanent non-observation dressed up as a pass.
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
		// worse than a missing one, because the summarizer acts on it either way. Paragraph 1 is
		// never dropped — it is the probe phrase.
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
