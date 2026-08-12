package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/qompack/qompack/internal/core"
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
	// Ephemeral marks results born ephemeral: first eviction candidate, surfaced to the client as
	// _meta.qompack.ephemeral (00-ARCHITECTURE.md §8.7).
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
