package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
)

// Tool is one registered retrieval tool (00-ARCHITECTURE.md §5.16): its name and description as
// the model sees them, the JSON Schema its arguments are validated against, the handler that
// answers it, and whether its results are born ephemeral (§8.7).
type Tool struct {
	// Name is the tool's identifier, as `tools/call` names it.
	Name string
	// Title is the tool's human-readable title.
	Title string
	// Description is what the model reads to decide whether to call it.
	Description string
	// InputSchema is the JSON Schema `tools/list` advertises and `tools/call` validates against.
	InputSchema json.RawMessage
	// Handler answers a call to this tool.
	Handler Handler
	// Ephemeral marks Qompack representations via _meta.qompack.ephemeral. It does not control
	// host eviction or prove what remains in native context (ADR 0013).
	Ephemeral bool
}

// Request is one decoded `tools/call` (00-ARCHITECTURE.md §5.16).
type Request struct {
	// Session is the session the call belongs to.
	Session core.SessionID
	// Name is the tool being called.
	Name string
	// Args is the raw argument object, validated against the tool's InputSchema before Handler
	// sees it.
	Args json.RawMessage
	// Deadline bounds the call; it exists so a retrieval that cannot finish inside budget B-F
	// returns something rather than hanging the model.
	Deadline time.Time
	// Turn is the session's current turn index, or 0 when it is not known. It is an ADDITIVE
	// SP-13 field: the stdio MCP process has no turn of its own, so the daemon resolves one — from
	// its observer's live state, the tool_use index and the open segment (daemon resolveTurn) — and
	// the ephemeral ToolUseRecord this call writes is filed at it (re-resolved at the write when the
	// daemon attached a Live.Turn), and so is the DAG node of an elimination this call records.
	Turn core.TurnIndex
}

// Content is one MCP content block in a tool result.
//
// The json tags are the MCP protocol's own spellings, not Go's defaults: a content block goes on
// the wire verbatim inside the result's "content" array, and encoding/json would otherwise emit
// "Type"/"Text", which no MCP client accepts. 00-ARCHITECTURE.md §5.16 declares the fields
// without tags, so SP-01 fixes the spellings here; the "_meta" key is the protocol's own
// underscore-prefixed metadata convention.
type Content struct {
	// Type is the content block's kind, e.g. "text".
	Type string `json:"type"`
	// Text is the block's text payload.
	Text string `json:"text"`
	// Meta carries per-block metadata.
	Meta map[string]any `json:"_meta,omitempty"`
}

// Response is a Handler's answer (00-ARCHITECTURE.md §5.16).
//
// It is deliberately NOT the wire shape and carries no json tags: Serve assembles the JSON-RPC
// result envelope from it, promoting Ephemeral into _meta.qompack.ephemeral and merging Meta into
// _meta.qompack. Content, which does go on the wire verbatim, carries its own tags.
type Response struct {
	// Content is the result's content blocks.
	Content []Content
	// IsError marks a TOOL error — a failed retrieval the model should read and react to — as
	// distinct from a JSON-RPC protocol error, which is a different thing entirely.
	IsError bool
	// Ephemeral requests _meta.qompack.ephemeral = true on this response (§8.7).
	Ephemeral bool
	// Meta carries response metadata: hash, span, truncated, promoted, and so on.
	Meta map[string]any
}

// Handler answers one tool call. It returns an error only for a failure the SERVER should report
// as a protocol error; a failed retrieval the model should see is a Response with IsError set.
type Handler func(ctx context.Context, r Request) (Response, error)

// ServerOptions carries what NewServer's §5.16 signature cannot: the maximum accepted line length
// and the initialize callback. NewServer is UNCHANGED and defined in terms of this struct; adding
// a constructor is additive to a package SP-13 owns, whereas adding a method to the §5.16 Server
// interface would require an architecture amendment (§0) and is therefore not done.
type ServerOptions struct {
	// Name and Version are what `initialize` reports as serverInfo.
	Name, Version string
	// Log receives the server's own diagnostics. Nothing is ever written to the out stream except
	// JSON-RPC lines. nil means logging.Nop().
	Log logging.Logger
	// MaxLine bounds one accepted request line. 0 means defaultMaxLine; callers pass
	// cfg.Runtime.HotPath.MaxPayloadBytes so an operator's tightened limit reaches the server.
	MaxLine int
	// OnInitialize is invoked once the initialize result has been built, with the handshake
	// record the caller persists. It is nil-tolerant and best-effort: a slow or failing callback
	// must never make the handshake fail.
	OnInitialize func(Observable)
}

// Widener is the symbol-aware half of the minimal-span resolver (§8.7, 00-ARCHITECTURE.md §5.16
// "Span default"). It is declared HERE and implemented in the composition root over
// symbols.Extractor, because §3.2 does not permit mcp to import symbols — the same shape
// DropReporter uses for rehydrate. Every call site tolerates a nil Widener.
type Widener interface {
	// Widen returns [start,end) grown so that end lands on the end of the symbol enclosing
	// end-1, given the buffer b whose byte 0 corresponds to file offset base. ok is false when
	// no enclosing symbol was found or when widening would not move end forward.
	Widen(path string, b []byte, off, end int64) (newOff, newEnd int64, ok bool)
	// Find returns the [start,end) of the named symbol in b, or ok=false.
	Find(path string, b []byte, name string) (off, end int64, ok bool)
}

// Observable is the human-readable MCP handshake record written to .qompack/state/mcp.json: what
// protocol version was negotiated, by which client, at what time, in which process.
//
// It is NOT the mcp.server_registered contract observable. That one is
// contract.SessionHistory.MCPInitialized in state/history.json (contract.HistoryPath), written by
// the daemon and read by SP-05's assertion. This file is what /qompack:status renders and what a
// user is told to look at when the tools do not show up; both are written from the same place, so
// they cannot disagree.
type Observable struct {
	Initialized     bool   `json:"initialized"`
	TS              int64  `json:"ts"`
	ProtocolVersion string `json:"protocol_version"`
	ClientName      string `json:"client_name"`
	ClientVersion   string `json:"client_version"`
	ServerVersion   string `json:"server_version"`
	Tools           int    `json:"tools"`
	PID             int    `json:"pid"`
}

// StandingInstruction is the §8.7 design note made literal: `already_tried` is surfaced as a
// standing instruction, not merely as an available tool, because an affordance nobody is told to
// reach for goes unused (G6.2). It must equal rehydrate.StandingInstruction() byte for byte; a
// test in test/e2e asserts exactly that once SP-11 has merged.
const StandingInstruction = "Before committing to an approach, call already_tried."
