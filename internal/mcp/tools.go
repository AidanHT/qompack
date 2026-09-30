package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hostperm"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/store"
)

// The argument and result types of the eight retrieval tools of Qompack.md §8.7. Their json tags
// are normative (00-ARCHITECTURE.md §5.16): they are the wire contract a model's tool call is
// decoded through, and each tool's InputSchema is generated to match.

// ErrToolNotFound is returned by Dispatch for a name no registered tool answers. It is a
// PROGRAM-level error, distinct from the tool-level `unknown tool "x"` result the server returns
// to the model: the model gets a readable result, the caller gets something to branch on.
var ErrToolNotFound = errors.New("qompack: mcp tool not found")

// The eight tool names, in the order Qompack.md §8.7 lists them. The order is contract: it is
// what `tools/list` emits and what testdata/golden/mcp/tools-list.v2.json freezes. The original
// tools-list.json remains historical evidence.
const (
	ToolRecall           = "recall"
	ToolExpand           = "expand"
	ToolReRead           = "re_read"
	ToolAlreadyTried     = "already_tried"
	ToolRecordEliminated = "record_eliminated"
	ToolTimeline         = "timeline"
	ToolWhy              = "why"
	ToolDropped          = "dropped"
)

// toolNamesInDesignOrder is the §8.7 table's own order.
var toolNamesInDesignOrder = []string{
	ToolRecall, ToolExpand, ToolReRead, ToolAlreadyTried,
	ToolRecordEliminated, ToolTimeline, ToolWhy, ToolDropped,
}

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
// 00-ARCHITECTURE.md §5.16 declares Hash/Path/Tool/Summary/TS/Score without tags; SP-01 fixed the
// lowercase wire spellings. ToolUseID and Span are SP-13 ADDITIONS (no §5.16 field is renamed,
// retyped or removed): store.Hit carries both, a hit without its tool_use_id cannot be expanded
// the way a tombstone can, and the chunk-aligned match window is what makes a follow-up `expand`
// return the matching hunk rather than the head of the object.
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
	// ToolUseID is the host's own tool call id, so a hit is expandable by id as well as by hash.
	ToolUseID string `json:"tool_use_id"`
	// Span is the chunk-aligned [start,end) byte window within Hash that matched.
	Span [2]int64 `json:"span"`
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

// AlreadyTriedResult retains the legacy ledger answers and adds "unavailable" for query
// failures (architecture §0.2 / ADR 0013) and "uncertain" for an answer the ledger could not back
// with coverage. A stale record retains its evidence; an unreadable ledger cannot establish absence
// or an active prohibition, and neither can one whose coverage is unverified.
//
// Scope, RecordedAt, DependsOn, StaleBecause and Degraded are SP-13 ADDITIONS to §5.16's four
// fields: the §8.3 staleness evidence is what turns "re-verification may be warranted" from an
// assertion into something the agent can check, and Degraded is §12.3's ledger-failure path made
// visible rather than indistinguishable from a genuine absence.
type AlreadyTriedResult struct {
	// State is "absent", "active", "stale", "unavailable" or "uncertain". Unknown states confer no
	// prohibition. "unavailable" and "uncertain" are the two outcomes that are neither an answer
	// nor an absence (§11.3 Required invariants item 8): the first says the ledger could not be
	// consulted, the second that it answered but could not back the answer with coverage. Neither
	// may be read as evidence that an approach is untried.
	State string `json:"state"`
	// Reason is why the approach was eliminated, when it was — or, for "unavailable" and
	// "uncertain", what could not be established.
	Reason string `json:"reason,omitempty"`
	// Note carries a staleness explanation, or the recovery direction for an "unavailable" or
	// "uncertain" state.
	Note string `json:"note,omitempty"`
	// Evidence is the content hash backing the elimination.
	Evidence string `json:"evidence,omitempty"`
	// Scope is the record's visibility, "session" or "project".
	Scope string `json:"scope,omitempty"`
	// RecordedAt is the record's timestamp, RFC 3339 in UTC.
	RecordedAt string `json:"recorded_at,omitempty"`
	// DependsOn is the §8.3 staleness evidence: the file hashes this elimination rests on.
	DependsOn []core.Dep `json:"depends_on,omitempty"`
	// StaleBecause names which DependsOn entries changed.
	StaleBecause []string `json:"stale_because,omitempty"`
	// Degraded marks an answer produced with no working ledger; it is not an absence witness.
	Degraded bool `json:"degraded,omitempty"`
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
// (00-ARCHITECTURE.md §5.16). Every member is nil-tolerant: a build without SP-10 has no
// checkpoint.Reader, and `why` must answer "not present in this build" rather than panic.
type ToolDeps struct {
	// Store backs `recall`, `expand`, `re_read` and `timeline`.
	Store store.Store
	// Ledger backs `already_tried` and `record_eliminated`. It is the VALUE seam, for a caller
	// that already holds an open ledger at wiring time; a caller whose ledger is opened later
	// supplies LedgerFn instead.
	Ledger negknow.Ledger
	// LedgerFn resolves the ledger LIVE, on every call, and takes precedence over Ledger.
	//
	// It exists because negknow.Open is deliberately lazy: the daemon opens the elimination ledger
	// on the first call that needs it — a compaction, or one of these two tools — not at startup,
	// so that a daemon which never needs it never creates sketches/tried.bloom. A composition root
	// therefore has no handle at wiring time, and a ToolDeps that stored one would freeze the nil.
	// The daemon's accessor (internal/cli liveLedger) opens through the daemon's own memoized
	// opener on first use; the handlers here only call it, and never open or own a ledger.
	LedgerFn func() negknow.Ledger
	// Checkpoints backs `why`.
	Checkpoints checkpoint.Reader
	// Rehydrator backs `dropped`. It is a DropReporter rather than a rehydrate type because mcp
	// may not import rehydrate (§3.2) — the interface is declared here and satisfied there.
	Rehydrator DropReporter
	// Promoter implements retrieval.promoteAfterExpansions (§8.7).
	Promoter Promoter
	// Cfg supplies retrieval.defaultSpan, retrieval.ephemeralResults, the eliminations keys and the
	// response limits, when CfgFn is nil.
	Cfg config.Config
	// CfgFn, when set, supplies the configuration on every call instead of Cfg: the daemon passes
	// its live configuration, so a key its config reload applies reaches the next tool call rather
	// than waiting for a restart (V6 close-out D49; UAT-09 met an eliminations.staleResponse change
	// the daemon had reloaded while already_tried kept answering the old form).
	CfgFn func() config.Config
	// Redactor re-applies TODAY'S secret policy to archive bytes on their way out (T20-M2-04). It
	// is an interface rather than a redact.Redactor because §3.2 forbids mcp importing redact; the
	// composition root adapts one to the other (cli.NewRetrievalRedactor).
	//
	// It is the ONE collaborator whose absence is not a graceful degradation: a nil Redactor makes
	// `recall`, `expand` and `re_read` report themselves unavailable rather than serve content this
	// build could not re-check. Serving unredacted bytes because nobody wired a redactor is the one
	// failure mode this seam exists to prevent.
	Redactor Redactor
	// Widener is the nil-tolerant symbol port of the §8.7 span widener. It is an interface rather
	// than a symbols.Extractor because §3.2 forbids mcp importing symbols; the composition root
	// adapts one to the other.
	Widener Widener
	// ProjectRoot is the worktree `re_read` resolves paths against and the tree the handshake
	// observable is written under.
	ProjectRoot string
	// HostPolicy evaluates the host's current Read deny and ask rules for a stored path
	// (V6-HOST-1). Nil means the real environment's policy for ProjectRoot, built by newHandlers,
	// so no composition root can forget to wire it: a caller supplies one only to point it at a
	// hermetic home and managed directory, as tests do.
	HostPolicy *hostperm.Policy
	// DisableWhy administratively gates `why` off: it reports itself unsupported regardless of
	// whether Checkpoints is wired. This lets core archive retrieval (recall, expand, re_read,
	// already_tried, record_eliminated, timeline) be verified and shipped independently of
	// checkpoint work landing in the same build, with no circular dependency on it (interface
	// contract: "Core archive retrieval is independently testable before checkpoint/rehydration
	// enablement"). `why` remains listed in tools/list either way.
	DisableWhy bool
	// DisableDropped is DisableWhy's sibling for `dropped`, the other checkpoint/rehydration
	// -dependent tool of the eight.
	DisableDropped bool
	// Clock is the time seam every timestamp in a response and every ephemeral record reads.
	Clock core.Clock
	// Log receives handler diagnostics; nil means logging.Nop().
	Log logging.Logger
	// Metrics receives the B-F histogram and the per-tool breakdowns; nil means a private
	// throwaway registry, so a handler never has to nil-check an instrument.
	Metrics obs.Registry
}

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

// spanPolicy is the sentence appended to `expand` and `re_read`'s descriptions. §8.7's policy has
// to be where the model reads it, which is the tool description — a policy documented only in the
// design is a policy the model never sees.
const spanPolicy = " Returns the minimum sufficient span by default; pass full=true only when " +
	"you need the whole available object. Ephemeral metadata describes Qompack records; host context retention is unknown."

// The eight input schemas, byte-for-byte as `tools/list` emits them. They are raw literals rather
// than a struct marshalled at runtime because the bytes ARE the contract: a golden freezes them,
// docs/mcp-tools.md is generated from them, and a field-order change made by a Go struct edit
// would be invisible at the call site that mattered.
const (
	schemaRecall = `{"type":"object","properties":{"query":{"type":"string","description":"Free text, or prefixed selectors combined with spaces: path:<glob>, symbol:<name>, tool:<ToolName>."},"k":{"type":"integer","minimum":1,"maximum":50,"default":5,"description":"Maximum number of hits."}},"required":["query"],"additionalProperties":false}`

	schemaExpand = `{"type":"object","properties":{"hash":{"type":"string","description":"Root or chunk hash as sha256:<64 hex>. Provide exactly one of hash or tool_use_id."},"tool_use_id":{"type":"string","description":"tool_use_id taken from a tombstone or a recall hit."},"full":{"type":"boolean","default":false,"description":"Return the whole object instead of the minimum sufficient span."},"span":{"type":"string","description":"Explicit span: \"<off>:<len>\" in bytes, or \"L<start>-L<end>\" in lines."}},"required":[],"additionalProperties":false}`

	schemaReRead = `{"type":"object","properties":{"path":{"type":"string","description":"Project-relative path. A :<symbol> or :<line> suffix anchors the minimal span."},"at":{"type":"string","description":"Empty for the latest captured version; otherwise an RFC3339 timestamp, sha256:<64 hex>, or turn:<N>. Never reads the working tree."},"full":{"type":"boolean","default":false,"description":"Return the whole file instead of the minimum sufficient span."}},"required":["path"],"additionalProperties":false}`

	schemaAlreadyTried = `{"type":"object","properties":{"target":{"type":"string","description":"File path, optionally :symbol — e.g. src/auth.ts:refreshToken."},"approach":{"type":"string","description":"The approach as one short verb phrase — e.g. widen pool timeout."}},"required":["target","approach"],"additionalProperties":false}`

	schemaRecordEliminated = `{"type":"object","properties":{"target":{"type":"string"},"approach":{"type":"string"},"reason":{"type":"string","description":"Why it does not work. Encode what a competent engineer with no session history would get wrong."},"scope":{"type":"string","enum":["session","project"],"default":"session"},"depends_on":{"type":"array","items":{"type":"string"},"description":"Project-relative paths whose contents this reason rests on; a change to any of them flips this record to stale."}},"required":["target","approach","reason"],"additionalProperties":false}`

	schemaTimeline = `{"type":"object","properties":{"from":{"type":"string","description":"Turn index, RFC3339 timestamp, or empty for the session start."},"to":{"type":"string","description":"Turn index, RFC3339 timestamp, or empty for the current frontier."}},"required":[],"additionalProperties":false}`

	schemaWhy = `{"type":"object","properties":{"decision_id":{"type":"string","description":"A dec_<12 hex> id from a checkpoint or a rehydrated decision list."}},"required":["decision_id"],"additionalProperties":false}`

	schemaDropped = `{"type":"object","properties":{},"required":[],"additionalProperties":false}`
)

// ToolDefs returns the eight §8.7 tools with their handlers bound to d, in design order.
//
// Ephemeral is true for seven of the eight as Qompack metadata, without a native eviction claim.
// record_eliminated is the exception: its acknowledgement concerns a write rather than retrieval.
func ToolDefs(d ToolDeps) []Tool {
	h := newHandlers(d)
	return []Tool{
		{
			Name:        ToolRecall,
			Title:       "Recall",
			Description: "Search captured archive material by content, path, or symbol; returns references and summaries. Capture and coverage may be partial or unavailable.",
			InputSchema: json.RawMessage(schemaRecall),
			Handler:     h.run(ToolRecall, h.recall),
			Ephemeral:   true,
		},
		{
			Name:        ToolExpand,
			Title:       "Expand",
			Description: "Retrieve available archived content by hash or tool_use_id; fidelity and coverage may be incomplete." + spanPolicy,
			InputSchema: json.RawMessage(schemaExpand),
			Handler:     h.run(ToolExpand, h.expand),
			Ephemeral:   true,
		},
		{
			Name:        ToolReRead,
			Title:       "Re-read",
			Description: "The latest captured, or a historical, version of a file, from the store's own version history — never a live read of disk." + spanPolicy,
			InputSchema: json.RawMessage(schemaReRead),
			Handler:     h.run(ToolReRead, h.reRead),
			Ephemeral:   true,
		},
		{
			Name:        ToolAlreadyTried,
			Title:       "Already tried",
			Description: "Query recorded elimination evidence: legacy answers are absent, active, or stale; a failed query is unavailable. Clients must treat unavailable or unrecognized states as unknown, never as absence or a prohibition. " + StandingInstruction,
			InputSchema: json.RawMessage(schemaAlreadyTried),
			Handler:     h.run(ToolAlreadyTried, h.alreadyTried),
			Ephemeral:   true,
		},
		{
			Name:        ToolRecordEliminated,
			Title:       "Record eliminated",
			Description: "Write negative knowledge: record that an approach does not work, with evidence and the files the reason rests on, so it survives compaction.",
			InputSchema: json.RawMessage(schemaRecordEliminated),
			Handler:     h.run(ToolRecordEliminated, h.recordEliminated),
			Ephemeral:   false,
		},
		{
			Name:        ToolTimeline,
			Title:       "Timeline",
			Description: "Retrieve recorded session segments over a turn or timestamp range. Missing events and native context coverage may be unknown.",
			InputSchema: json.RawMessage(schemaTimeline),
			Handler:     h.run(ToolTimeline, h.timeline),
			Ephemeral:   true,
		},
		{
			Name:        ToolWhy,
			Title:       "Why",
			Description: "Retrieve an attributed decision and its evidence from the checkpoint chain. Recorded reasoning does not prove model compliance.",
			InputSchema: json.RawMessage(schemaWhy),
			Handler:     h.run(ToolWhy, h.why),
			Ephemeral:   true,
		},
		{
			Name:        ToolDropped,
			Title:       "Dropped",
			Description: "Retrieve Qompack's recorded omissions for this session. This report does not establish what remains in native context.",
			InputSchema: json.RawMessage(schemaDropped),
			Handler:     h.run(ToolDropped, h.dropped),
			Ephemeral:   true,
		},
	}
}

// ToolNames returns the eight tool names in the §8.7 design order — the same order ToolDefs,
// `tools/list` and the golden use.
//
// 00-ARCHITECTURE.md §5.16's note calls this "sorted"; design order is what every other
// consumer of the tool set already agrees on, and returning a differently-ordered list from the
// one place a validator compares against the §8.7 table would make the check assert the wrong
// thing. The order is stable and total either way, which is all a validator needs.
func ToolNames() []string {
	out := make([]string, len(toolNamesInDesignOrder))
	copy(out, toolNamesInDesignOrder)
	return out
}

// RegisterAll registers the eight retrieval tools of Qompack.md §8.7 on s, with every handler
// bound to d.
func RegisterAll(s Server, d ToolDeps) error {
	for _, t := range ToolDefs(d) {
		if err := s.Register(t); err != nil {
			return err
		}
	}
	return nil
}

// RegisterProxy registers the same eight definitions with every handler replaced by fwd.
//
// It is what makes `qompack mcp` a transcoder rather than a second implementation: the proxy
// process advertises byte-identical metadata to the daemon's own server, because both read the
// same ToolDefs, and forwards the call to where the live handles actually are.
func RegisterProxy(s Server, fwd Handler) error {
	if fwd == nil {
		return errors.New("qompack: mcp: RegisterProxy needs a forwarding handler")
	}
	for _, t := range ToolDefs(ToolDeps{}) {
		t.Handler = fwd
		if err := s.Register(t); err != nil {
			return err
		}
	}
	return nil
}

// Dispatch looks r.Name up on s and calls its handler with the panic isolated
// (00-ARCHITECTURE.md §12.1: an MCP tool panic is recovered at the handler boundary, returned as
// IsError, and never kills the server).
//
// A panic becomes a RESULT, not an error: the model is mid-conversation and a protocol error it
// cannot read helps nobody. An unregistered name is the one case that IS an error return, so the
// caller can tell "this build has no such tool" from "the tool ran and failed".
func Dispatch(ctx context.Context, s Server, r Request) (resp Response, err error) {
	t := lookupTool(s, r.Name)
	if t == nil {
		return Response{}, ErrToolNotFound
	}
	defer func() {
		if p := recover(); p != nil {
			resp = errResponse("internal error in tool " + r.Name)
			err = nil
		}
	}()
	return t.Handler(ctx, r)
}

// lookupTool returns the registered tool named name, or nil.
func lookupTool(s Server, name string) *Tool {
	if s == nil {
		return nil
	}
	tools := s.Tools()
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

// errResponse is the one-line tool error every handler and Dispatch build: IsError set, one text
// block the model can read.
func errResponse(msg string) Response {
	return Response{IsError: true, Content: []Content{{Type: "text", Text: msg}}}
}
