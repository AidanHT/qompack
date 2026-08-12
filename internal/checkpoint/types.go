package checkpoint

import (
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/pins"
)

// SchemaVersion is the value every Checkpoint carries in its "version" field, and the ver= value
// of an injection tag. 00-ARCHITECTURE.md §5.14: the on-disk schema is verbatim from Qompack.md
// §8.5 and is versioned, so any change to the JSON shape bumps this and adds a migration in
// checkpoint/migrate.go (SP-10). It exists as a named constant rather than a bare 1 so the
// version and the migration set can never drift apart silently.
const SchemaVersion = 1

// Invariant is a single pinned fact. It is DEFINED IN pins and aliased here: checkpoint imports
// pins (for SourceSet.Pins), so the reverse edge would be an import cycle (00-ARCHITECTURE.md
// §5.14, §3.2). Being an alias rather than a copy, checkpoint.Invariant and pins.Invariant are
// the same type — a value of either satisfies both.
type Invariant = pins.Invariant

// Checkpoint is the immutable, importance-ordered checkpoint artifact of Qompack.md §8.5, in the
// Go shape 00-ARCHITECTURE.md §5.14 fixes.
//
// The json tags are explicit and frozen: this is the shape
// testdata/golden/contracts/checkpoint/want/0001.json pins (Rule W-2), field for field, in this
// exact order. Note what is NOT here: code snippets. Files are pointers with a one-line reason
// (§4.4, §13 invariant 5), which is where most of the 50K eager-restore budget is reclaimed.
//
// The three tiers are truncated in importance order by Truncate (§6.9): tier 3 first, then tier
// 2, and tier 1 never.
type Checkpoint struct {
	Version int                `json:"version"`
	Session core.SessionID     `json:"session"`
	Seq     core.CheckpointSeq `json:"seq"`
	// Created is an RFC 3339 UTC timestamp with millisecond precision, e.g.
	// "2026-01-01T00:12:30.000Z". It is a string, not a core.UnixMilli, because Qompack.md §8.5
	// spells it as a human-readable timestamp in the artifact itself.
	Created string `json:"created"`
	// Parent names the previous checkpoint's file ("0006.json"); "" for the first checkpoint of a
	// chain. 00-ARCHITECTURE.md §5.14 declares this field omitempty, so an empty Parent is absent
	// from the marshalled document rather than present as "".
	Parent string `json:"parent,omitempty"`
	// EncodedSegments is the DPI guard (§4.6): the segments whose ORIGINAL content this
	// checkpoint encodes, never a re-encoding of an earlier checkpoint's summary.
	EncodedSegments []core.SegmentID `json:"encoded_segments"`

	// ── Tier 1: never truncated ──

	// Invariants are pinned facts, verbatim.
	Invariants []Invariant `json:"invariants"`
	// UserIntent is the verbatim original intent and its verbatim deltas, captured at L0 and
	// never regenerated (G2.3).
	UserIntent UserIntent `json:"user_intent"`
	// Eliminated is structured negative knowledge: evidence-linked, staleness-guarded records.
	Eliminated []negknow.Record `json:"eliminated"`

	// ── Tier 2: truncate late ──

	Decisions     []Decision  `json:"decisions"`
	OpenQuestions []string    `json:"open_questions"`
	CurrentWork   CurrentWork `json:"current_work"`

	// ── Tier 3: truncate first ──

	Pointers Pointers `json:"pointers"`
	// Narrative is prose residue: the last resort, dropped first.
	Narrative string `json:"narrative"`

	// ── Metadata ──

	// SketchRefs names the sketch files this checkpoint's negative knowledge is backed by, e.g.
	// {"tried": "tried.bloom", "touch": "touch.cms"}.
	SketchRefs map[string]string `json:"sketch_refs"`
	// Dropped is the explicit drop report (G4.5): what this checkpoint did not carry, and why.
	Dropped []DropEntry `json:"dropped"`
	Cache   CacheInfo   `json:"cache"`
}

// UserIntent is the user's original request, verbatim, plus its verbatim deltas
// (00-ARCHITECTURE.md §5.14). Neither field is ever regenerated from a summary: both come
// straight from L0's capture (G2.3).
type UserIntent struct {
	// Original is the first statement of intent, verbatim.
	Original string `json:"original"`
	// Evolution lists every subsequent restatement or refinement, verbatim, in order.
	Evolution []string `json:"evolution"`
}

// Decision is one tier-2 recorded decision (00-ARCHITECTURE.md §5.14). ExtractDecisions is the
// only producer of ID, and therefore the only thing that makes the `why(decision_id)` MCP tool
// answerable (§5.16).
type Decision struct {
	ID   core.DecisionID `json:"id"`
	What string          `json:"what"`
	Why  string          `json:"why"`
	// AlternativesRejected lists the approaches considered and turned down, with their reasons.
	AlternativesRejected []string `json:"alternatives_rejected"`
	// Evidence is the content hash of whatever backs this decision.
	Evidence core.Hash `json:"evidence"`
	// Turn is the turn index this decision was reached at.
	Turn core.TurnIndex `json:"turn"`
}

// CurrentWork is the tier-2 statement of what is in flight (00-ARCHITECTURE.md §5.14).
//
// 00-ARCHITECTURE.md §5.14 declares the Go fields without json tags; the tags below are the
// spellings Qompack.md §8.5's artifact uses, which is normative for the on-disk shape.
type CurrentWork struct {
	// Goal is the current objective.
	Goal string `json:"goal"`
	// NextStep is the immediate next action.
	NextStep string `json:"next_step"`
	// BlockedOn names what the work is waiting on; nil (JSON null) when nothing blocks it.
	BlockedOn *string `json:"blocked_on"`
}

// Pointers is the tier-3 pointer set: paths and tool-use identifiers with one-line reasons, never
// content (00-ARCHITECTURE.md §5.14, §4.4). §13 invariant 5 forbids code snippets anywhere in a
// checkpoint, and this is the field that would otherwise be tempted to carry them.
type Pointers struct {
	// Files points at files by path and content hash, with a one-line reason each.
	Files []FilePointer `json:"files"`
	// Tools points at prior tool results by tool_use_id and content hash, with a one-line summary.
	Tools []ToolPointer `json:"tools"`
}

// FilePointer is one entry of Pointers.Files: {path, hash, why} and nothing else
// (00-ARCHITECTURE.md §5.14 names the type without spelling it out; SP-01 declares it here from
// Qompack.md §8.5's artifact, which is normative for the field names).
type FilePointer struct {
	// Path is the file's project-relative path (paths.Key form).
	Path string `json:"path"`
	// Hash is the file's content root hash at the time this checkpoint was written.
	Hash core.Hash `json:"hash"`
	// Why is the one-line reason this file is worth re-reading. It is a reason, never an excerpt.
	Why string `json:"why"`
}

// ToolPointer is one entry of Pointers.Tools: {tool_use_id, hash, summary} and nothing else
// (00-ARCHITECTURE.md §5.14 names the type without spelling it out; SP-01 declares it here from
// Qompack.md §8.5's artifact, which is normative for the field names).
type ToolPointer struct {
	// ToolUseID is the host's identifier for the tool call whose result this points at.
	ToolUseID core.ToolUseID `json:"tool_use_id"`
	// Hash is the stored result's content root hash, so `expand` can retrieve it.
	Hash core.Hash `json:"hash"`
	// Summary is the one-line description of what the result contained. It is a summary of the
	// result's shape, never the result itself.
	Summary string `json:"summary"`
}

// DropEntry is one line of the explicit drop report (G4.5, 00-ARCHITECTURE.md §5.14): what was
// left out of a checkpoint or a rehydration, identified well enough to be asked for again.
type DropEntry struct {
	// Kind classifies what was dropped, e.g. "path_rule", "tool_output", "narrative".
	Kind string `json:"kind"`
	// ID identifies the dropped item within its Kind.
	ID string `json:"id"`
	// Detail is an optional one-line explanation of why it was dropped.
	Detail string `json:"detail,omitempty"`
}

// CacheInfo records the prompt-cache accounting behind this checkpoint's compaction point
// (00-ARCHITECTURE.md §5.14): what p was chosen, what rewriting it cost, and what the cache's
// TTL state was at the time.
type CacheInfo struct {
	// PChosen is the token position the compaction point was placed at.
	PChosen int `json:"p_chosen"`
	// RewriteTokens is the number of tokens the chosen p forced to be re-written to the cache.
	RewriteTokens int `json:"rewrite_tokens"`
	// TTLState is the cache's state at write time: "warm", "expiring", "cold" or "unknown"
	// (scheduler.TTLState's values, spelled as a plain string because checkpoint may not import
	// scheduler — §3.2).
	TTLState string `json:"ttl_state"`
}
