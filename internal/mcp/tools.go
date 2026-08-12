package mcp

import (
	"context"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/store"
)

// The argument and result types of the eight retrieval tools of Qompack.md §8.7. Their json tags
// are normative (00-ARCHITECTURE.md §5.16): they are the wire contract a model's tool call is
// decoded through, and each tool's InputSchema is generated to match.

// RecallArgs is `recall`: semantic search over stored tool results and file versions.
type RecallArgs struct {
	// Query is the search text.
	Query string `json:"query"`
	// K is the number of hits to return; the tool's schema defaults it to 5.
	K int `json:"k"`
}

// RecallHit is one `recall` result. It is a POINTER plus a one-line summary, never content: the
// model calls `expand` if it wants the bytes, which is what keeps recall cheap.
//
// 00-ARCHITECTURE.md §5.16 declares the fields without tags; SP-01 fixes the lowercase wire
// spellings here, since Go's defaults would put "Hash"/"Path" on the wire.
type RecallHit struct {
	// Hash is the stored content's root hash, in "sha256:…" form, ready to pass to `expand`.
	Hash string `json:"hash"`
	// Path is the file path the hit relates to, if any.
	Path string `json:"path"`
	// Tool is the tool that produced the hit, if any.
	Tool string `json:"tool"`
	// Summary is a one-line description of the hit.
	Summary string `json:"summary"`
	// TS is when the hit's content was recorded.
	TS string `json:"ts"`
	// Score is the hit's relevance score.
	Score float64 `json:"score"`
}

// ExpandArgs is `expand`: fetch a stored result back by hash or tool_use_id. It returns the
// minimum sufficient span by default (retrieval.defaultSpan), with Full as the escape hatch.
type ExpandArgs struct {
	// Hash addresses the content directly, e.g. from a tombstone or a RecallHit.
	Hash string `json:"hash"`
	// ToolUseID addresses it by the host's own tool call id.
	ToolUseID string `json:"tool_use_id"`
	// Full requests the whole object instead of the minimum sufficient span.
	Full bool `json:"full"`
	// Span names a specific span within the object.
	Span string `json:"span"`
}

// ReReadArgs is `re_read`: read a file back as it was at a point in time, from the store's own
// version history rather than from disk.
type ReReadArgs struct {
	// Path is the file to re-read, in paths.Key form.
	Path string `json:"path"`
	// At is the point in time to read at; empty means the latest recorded version.
	At string `json:"at"`
	// Full requests the whole file instead of the minimum sufficient span.
	Full bool `json:"full"`
}

// AlreadyTriedArgs is `already_tried`: the negative-knowledge query that keeps an agent from
// re-attempting an eliminated approach.
type AlreadyTriedArgs struct {
	// Target is what the approach would be applied to.
	Target string `json:"target"`
	// Approach is the approach being considered.
	Approach string `json:"approach"`
}

// AlreadyTriedResult is `already_tried`'s three-way answer (00-ARCHITECTURE.md §8.3). Three-way,
// not two-way, is the whole point: an elimination whose dependencies have changed is "stale", not
// "absent", so the agent learns both that it was tried and that the evidence may no longer hold.
type AlreadyTriedResult struct {
	// State is "absent", "active" or "stale".
	State string `json:"state"`
	// Reason is why the approach was eliminated, when it was.
	Reason string `json:"reason,omitempty"`
	// Note carries the staleness explanation, when State is "stale".
	Note string `json:"note,omitempty"`
	// Evidence is the content hash backing the elimination.
	Evidence string `json:"evidence,omitempty"`
}

// RecordEliminatedArgs is `record_eliminated`: the write side of negative knowledge.
type RecordEliminatedArgs struct {
	// Target is what the approach was applied to.
	Target string `json:"target"`
	// Approach is what was tried.
	Approach string `json:"approach"`
	// Reason is why it did not work.
	Reason string `json:"reason"`
	// Scope is "session" or "project"; empty takes eliminations.defaultScope.
	Scope string `json:"scope"`
	// DependsOn lists the file paths this elimination's validity depends on — the §8.3 staleness
	// guard. Without them an elimination can never expire, and negative knowledge that cannot
	// expire eventually blocks a viable approach.
	DependsOn []string `json:"depends_on"`
}

// TimelineArgs is `timeline`: what happened between two points in the session.
type TimelineArgs struct {
	// From is the start of the range.
	From string `json:"from"`
	// To is the end of the range.
	To string `json:"to"`
}

// WhyArgs is `why`: the rationale behind a recorded decision. It is answerable only because
// checkpoint.ExtractDecisions is the sole producer of core.DecisionID.
type WhyArgs struct {
	// DecisionID identifies the decision.
	DecisionID string `json:"decision_id"`
}

// DroppedArgs is `dropped`: the explicit drop report (G4.5). It takes no arguments — what was
// dropped is a property of the session, not of the question.
type DroppedArgs struct{}

// ToolDeps is the collaborator set RegisterAll binds the eight tools' handlers to
// (00-ARCHITECTURE.md §5.16).
type ToolDeps struct {
	// Store backs `recall`, `expand`, `re_read` and `timeline`.
	Store store.Store
	// Ledger backs `already_tried` and `record_eliminated`.
	Ledger negknow.Ledger
	// Checkpoints backs `why`.
	Checkpoints checkpoint.Reader
	// Rehydrator backs `dropped`. It is a DropReporter rather than a rehydrate type because mcp
	// may not import rehydrate (§3.2) — the interface is declared here and satisfied there.
	Rehydrator DropReporter
	// Promoter implements retrieval.promoteAfterExpansions (§8.7).
	Promoter Promoter
	// Cfg supplies retrieval.defaultSpan, retrieval.ephemeralResults and the response limits.
	Cfg config.Config
}

// RegisterAll registers the eight retrieval tools of Qompack.md §8.7 on s, with every handler
// bound to d. Always reports core.ErrNotImplemented until SP-13 lands.
func RegisterAll(s Server, d ToolDeps) error { return core.ErrNotImplemented }

// DropReporter answers the `dropped` tool: what this session's rehydration left out, so the model
// can ask for it back instead of assuming it never existed (G4.5). It is declared in mcp and
// implemented in rehydrate, because the dependency runs mcp → checkpoint, never mcp → rehydrate
// (§3.2).
type DropReporter interface {
	// CurrentDrops returns everything dropped from sess's current context.
	CurrentDrops(ctx context.Context, sess core.SessionID) ([]checkpoint.DropEntry, error)
}

// Promoter implements retrieval.promoteAfterExpansions (00-ARCHITECTURE.md §8.7): content the
// model keeps asking for has proven its demand, so it is promoted into the next checkpoint's
// pointer tier rather than being re-fetched forever.
type Promoter interface {
	// NoteExpansion records one expansion of h in sess and reports the running count and whether
	// that count has crossed the promotion threshold.
	NoteExpansion(ctx context.Context, sess core.SessionID, h core.Hash) (count int, promoted bool, err error)
	// Promoted returns every hash promoted in sess so far.
	Promoted(ctx context.Context, sess core.SessionID) ([]core.Hash, error)
}
