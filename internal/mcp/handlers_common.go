package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/store"
)

// This file is the shared half of the eight handlers: the collaborator struct they hang off, the
// argument-validation and metering preamble every one of them runs through, and the two response
// shapes they all build. The handlers themselves are split across handlers_span.go (expand,
// re_read — the two that resolve a span) and handlers.go (the other six).
//
// One rule governs every handler and is worth stating once rather than eight times. A SEMANTIC
// MISS — an unknown hash, an unknown decision id, no such path version, an empty range — is NOT
// an error. It returns IsError:false with `"found": false` and a `"searched"` field naming what
// was looked at, because "I looked here and here and it is not there" is information the model
// can act on and `isError` is not. IsError:true is reserved for invalid arguments, backend
// failures, panics and timeouts.

// callTimeout bounds one tools/call. It is the deadline the daemon stamps on mcp.Request and the
// one `qompack mcp` waits for a reply within: budget B-F is 250 ms at p95, so five seconds is
// twenty times the budget — long enough that a cold page-in never trips it, short enough that a
// wedged retrieval returns something rather than hanging the model's turn.
const callTimeout = 5 * time.Second

// argsPreviewRunes is the width store.ToolUseRecord.ArgsPreview is capped at by §5.8.
const argsPreviewRunes = 120 //nomagic:allow ToolUseRecord.ArgsPreview is capped at 120 chars by §5.8

// ephemeralTools is the seven of eight whose results are born ephemeral (§8.7).
// record_eliminated is absent on purpose: it writes negative knowledge rather than returning
// retrieved content, so nothing about it belongs in the first-eviction tier.
var ephemeralTools = map[string]bool{
	ToolRecall: true, ToolExpand: true, ToolReRead: true, ToolAlreadyTried: true,
	ToolTimeline: true, ToolWhy: true, ToolDropped: true,
}

// toolSchemas maps each tool to the schema its arguments are validated against.
var toolSchemas = map[string]string{
	ToolRecall:           schemaRecall,
	ToolExpand:           schemaExpand,
	ToolReRead:           schemaReRead,
	ToolAlreadyTried:     schemaAlreadyTried,
	ToolRecordEliminated: schemaRecordEliminated,
	ToolTimeline:         schemaTimeline,
	ToolWhy:              schemaWhy,
	ToolDropped:          schemaDropped,
}

// The _meta.qompack keys the preamble owns. Handlers own the rest.
const (
	metaEphemeral = "ephemeral"
	metaElapsedMs = "elapsed_ms"
	metaToolUseID = "tool_use_id"
	metaHash      = "hash"
	metaPath      = "path"
	// metaUntrusted marks a response as carrying retrieved archive text: content this package read
	// back from stored evidence rather than text it composed itself. It is set on every tool that
	// renders such text (recall, expand, re_read) so a host or model reading _meta knows this text
	// has provenance elsewhere and must be treated as data, never as an instruction (SP-13 interface
	// contract, "Treat malicious stored text as data with provenance, never promoted instruction").
	metaUntrusted = "untrusted"
)

// toolFunc is one handler's body: the preamble has already applied schema defaults and validated,
// so args is a complete, schema-valid argument object.
type toolFunc func(ctx context.Context, r Request, args json.RawMessage) (Response, error)

// handlers is the collaborator set the eight tool bodies read. Every member may be nil except
// cfg, clk, log and m, which newHandlers always fills.
type handlers struct {
	store store.Store
	// ledgerFn resolves the elimination ledger on every read rather than holding it, because the
	// daemon opens it lazily and long after these handlers were built. It is never nil —
	// newHandlers wraps a plain ToolDeps.Ledger in a closure — so h.ledger() is always safe to
	// call. See ToolDeps.LedgerFn.
	ledgerFn func() negknow.Ledger
	ckpt     checkpoint.Reader
	drops    DropReporter
	prom     Promoter
	wide     Widener

	cfg  config.Config
	root string
	clk  core.Clock
	log  logging.Logger
	m    obs.Registry

	// bfHist is the histogram obs.Budgets() associates with B-F, resolved once at construction.
	// It is looked up rather than respelled because internal/obs exports no name-for-budget
	// accessor and the histogram name is an unexported constant there: Budgets() is the only
	// exported route to it, and SP-13 adds no export to a package it does not own.
	bfHist string

	// redactor re-applies the CURRENT secret policy to retrieved bytes (T20-M2-04). It is built
	// once, from the same effective config every other retrieval bound reads from, and is never
	// nil: redact.New already returns an identity Redactor when runtime.redact is disabled, so
	// every call site can invoke it unconditionally.
	redactor redact.Redactor

	// disableWhy and disableDropped are the explicit operator/build gate for the two
	// checkpoint-dependent tools (interface contract: "Core archive retrieval is independently
	// testable before checkpoint/rehydration enablement"). They are distinct from a nil ckpt/drops
	// collaborator: that means "not present in this build"; these mean "administratively off",
	// which can be true even when a real collaborator IS wired.
	disableWhy, disableDropped bool
}

// newHandlers builds the handler set from d, filling every seam a zero ToolDeps leaves empty.
//
// A zero ToolDeps is a real case, not a defensive one: mcptest's shape block calls
// RegisterAll(s, ToolDeps{}) and RegisterProxy registers ToolDefs(ToolDeps{}) so the proxy's
// tools/list is byte-identical to the daemon's. Both must succeed, and every handler must then
// answer "not present in this build" rather than panic.
func newHandlers(d ToolDeps) *handlers {
	clk := d.Clock
	if clk == nil {
		clk = core.SystemClock()
	}
	log := d.Log
	if log == nil {
		log = logging.Nop()
	}
	m := d.Metrics
	if m == nil {
		m = obs.New(clk)
	}
	cfg := normalizeCfg(d.Cfg)
	ledgerFn := d.LedgerFn
	if ledgerFn == nil {
		l := d.Ledger
		ledgerFn = func() negknow.Ledger { return l }
	}
	return &handlers{
		store: d.Store, ledgerFn: ledgerFn, ckpt: d.Checkpoints,
		drops: d.Rehydrator, prom: d.Promoter, wide: d.Widener,
		cfg:  cfg,
		root: d.ProjectRoot,
		clk:  clk, log: log, m: m,
		bfHist:     bfHistName(),
		redactor:   redact.New(cfg),
		disableWhy: d.DisableWhy, disableDropped: d.DisableDropped,
	}
}

// ledger resolves the elimination ledger for this call, or nil when none is wired. Every
// ledger-backed handler already checks for nil, because a build without one must answer "not
// present in this build" rather than panic.
func (h *handlers) ledger() negknow.Ledger {
	if h.ledgerFn == nil {
		return nil
	}
	return h.ledgerFn()
}

// normalizeCfg fills the eight configuration keys this package reads when cfg arrives zeroed.
//
// It fills ONLY those keys, and only when their value is outside the range §11.3 declares legal,
// so a caller who supplied a real config keeps every value they set. The alternative — reading a
// zero Store.Chunk.Max as "the maximum span is nothing" — turns a wiring omission into an empty
// retrieval that looks like a store bug (D11/§11.6: every use site reads its number from config,
// and this is where a missing config becomes the documented default rather than zero).
func normalizeCfg(cfg config.Config) config.Config {
	def := config.Defaults()
	if cfg.Store.Chunk.Max <= 0 {
		cfg.Store.Chunk.Max = def.Store.Chunk.Max
	}
	if cfg.Runtime.MCP.MaxResponseBytes <= 0 {
		cfg.Runtime.MCP.MaxResponseBytes = def.Runtime.MCP.MaxResponseBytes
		cfg.Runtime.MCP.SpanWidenLines = def.Runtime.MCP.SpanWidenLines
	}
	if cfg.Retrieval.DefaultSpan == "" {
		cfg.Retrieval.DefaultSpan = def.Retrieval.DefaultSpan
	}
	if cfg.Retrieval.PromoteAfterExpansions < 1 {
		cfg.Retrieval.PromoteAfterExpansions = def.Retrieval.PromoteAfterExpansions
	}
	if cfg.Eliminations.DefaultScope == "" {
		cfg.Eliminations.DefaultScope = def.Eliminations.DefaultScope
	}
	if cfg.Eliminations.StaleResponse == "" {
		cfg.Eliminations.StaleResponse = def.Eliminations.StaleResponse
	}
	if cfg.Runtime.Budgets.MCPToolCallMs <= 0 {
		cfg.Runtime.Budgets.MCPToolCallMs = def.Runtime.Budgets.MCPToolCallMs
	}
	if cfg.Runtime.HotPath.MaxPayloadBytes <= 0 {
		cfg.Runtime.HotPath.MaxPayloadBytes = def.Runtime.HotPath.MaxPayloadBytes
	}
	return cfg
}

// bfHistName resolves B-F's histogram name off the budget table itself. ok is false only for a
// build whose table has lost B-F, in which case the per-tool histogram is still recorded and the
// budget one is skipped.
func bfHistName() string {
	for _, b := range obs.Budgets() {
		if b.ID == obs.BF {
			return b.Hist
		}
	}
	return ""
}

// run wraps one handler body with the preamble every tool shares: schema defaults, schema
// validation, B-F and per-tool metering, the ephemeral-at-birth path, and the _meta fields the
// server promotes into the result envelope.
//
// The schema is compiled ONCE, at registration, not per call: a schema that does not compile is a
// wiring bug that must surface when the tool is registered, and paying the parse on every
// retrieval would spend budget B-F on work whose answer never changes.
func (h *handlers) run(name string, fn toolFunc) Handler {
	schema, compileErr := Compile(json.RawMessage(toolSchemas[name]))
	toolHist := "mcp.tool." + name
	ephemeral := ephemeralTools[name] && h.cfg.Retrieval.EphemeralResults

	return func(ctx context.Context, r Request) (Response, error) {
		started := time.Now()
		resp := h.invoke(ctx, r, name, schema, compileErr, fn)

		elapsed := time.Since(started)
		if h.bfHist != "" {
			h.m.Hist(h.bfHist).Observe(elapsed)
		}
		h.m.Hist(toolHist).Observe(elapsed)

		if resp.Meta == nil {
			resp.Meta = map[string]any{}
		}
		resp.Ephemeral = ephemeral
		if ephemeral && !resp.IsError {
			h.tagEphemeral(ctx, r, name, &resp)
		}
		resp.Meta[metaElapsedMs] = elapsed.Milliseconds()
		return resp, nil
	}
}

// invoke validates and runs one call. It never returns an error: a handler failure the model
// should see is a Response with IsError set, and the JSON-RPC layer has nothing to do with it.
func (h *handlers) invoke(ctx context.Context, r Request, name string,
	schema *Schema, compileErr error, fn toolFunc,
) Response {
	if compileErr != nil {
		h.log.Loud("mcp: tool schema does not compile", "tool", name, "err", compileErr.Error())
		return errResponse("tool " + name + " is misconfigured in this build")
	}
	args, err := schema.ApplyDefaults(r.Args)
	if err != nil {
		return errResponse("invalid arguments for " + name + ": arguments must be a JSON object")
	}
	if vs := schema.Validate(args); len(vs) > 0 {
		return errResponse("invalid arguments for " + name + ": " + vs[0].String())
	}
	resp, err := fn(ctx, r, args)
	if err != nil {
		h.log.Warn("mcp: tool failed", "tool", name, "err", err.Error())
		return errResponse(name + " failed: " + err.Error())
	}
	return resp
}

// tagEphemeral stores the response body as an ephemeral object and records the matching
// ToolUseRecord, then publishes the two identifiers the model needs to expand its own retrieval
// output later. Every failure inside is logged and swallowed: a retrieval that succeeded must not
// be turned into a failure by bookkeeping.
func (h *handlers) tagEphemeral(ctx context.Context, r Request, name string, resp *Response) {
	body := responseBody(*resp)
	if len(body) == 0 {
		return
	}
	path, _ := resp.Meta[metaPath].(string)
	rec := h.recordEphemeral(ctx, r, name, body, path)
	if rec.ToolUseID == "" {
		return
	}
	resp.Meta[metaToolUseID] = string(rec.ToolUseID)
	resp.Meta[metaHash] = rec.Hash.String()
}

// responseBody concatenates a Response's text blocks: what an ephemeral record stores, and what
// re-expanding that record hands back.
func responseBody(r Response) []byte {
	if len(r.Content) == 1 {
		return []byte(r.Content[0].Text)
	}
	var b bytes.Buffer
	for _, c := range r.Content {
		b.WriteString(c.Text)
	}
	return b.Bytes()
}

// jsonResponse renders body as the single text block of a successful tool result, carrying meta
// into _meta.qompack. A marshalling failure is a tool error rather than a panic: the handler
// built the value, so a failure here is this package's bug and the model should be told plainly.
func (h *handlers) jsonResponse(name string, body any, meta map[string]any) Response {
	b, err := marshalCompact(body)
	if err != nil {
		h.log.Warn("mcp: could not marshal a tool result", "tool", name, "err", err.Error())
		return errResponse(name + " failed: could not render its result")
	}
	if meta == nil {
		meta = map[string]any{}
	}
	return Response{Content: []Content{{Type: "text", Text: string(b)}}, Meta: meta}
}

// marshalCompact renders v as compact JSON with HTML escaping off, so a path containing "&" or a
// reason containing "<" reaches the model as written rather than as an entity.
func marshalCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// nowMilli reads the handler clock at millisecond resolution — the resolution every on-disk
// timestamp in this repository uses.
func (h *handlers) nowMilli() core.UnixMilli { return core.NowMilli(h.clk) }

// rfc3339 renders a core.UnixMilli as an RFC 3339 UTC timestamp, the spelling every tool response
// uses for a time. A zero timestamp renders as "" rather than as 1970, which would read as a real
// answer.
func rfc3339(ts core.UnixMilli) string {
	if ts == 0 {
		return ""
	}
	return ts.Time().Format(time.RFC3339)
}

// dropEntriesOf normalizes a nil slice of drop entries to an empty one, so `"drops": []` reaches
// the model rather than `"drops": null` — which a model reads as "unknown", not as "none".
func dropEntriesOf(in []checkpoint.DropEntry) []checkpoint.DropEntry {
	if in == nil {
		return []checkpoint.DropEntry{}
	}
	return in
}

// redactForRetrieval re-applies the CURRENT secret policy to retrieved bytes before they are
// rendered (T20-M2-04).
//
// Capture-time redaction (internal/redact, applied once, on the way INTO the store) is necessarily
// judged by whichever rule set existed AT CAPTURE TIME. A record captured before a rule existed —
// a new built-in family, an operator's later runtime.redact.patterns addition, or capture with
// redaction disabled altogether — would otherwise be served in the clear forever, because nothing
// ever looks at it again. Redact's own contract guarantees this is safe to do a second time: it is
// idempotent against its own placeholder, so re-running it over already-redacted bytes changes
// nothing.
//
// Only the COUNT and the RULE NAMES are logged, and only via Loud — never any span of the input —
// so a diagnostic about a secret can never itself become one ("no secret reaches a log line").
func (h *handlers) redactForRetrieval(tool string, b []byte) []byte {
	out, matches := h.redactor.Redact(b)
	if len(matches) == 0 {
		return out
	}
	rules := make([]string, len(matches))
	for i, m := range matches {
		rules[i] = m.Rule
	}
	h.log.Loud("mcp: retrieval redacted content the capture-time policy had not caught",
		"tool", tool, "count", len(matches), "rules", strings.Join(rules, ","))
	return out
}

// unsupportedReason is the one sentence every administratively-gated tool reports, stated once so
// a caller learns a single rule rather than several differently-worded ones.
func unsupportedReason(name string) string {
	return name + " is disabled in this build's configuration"
}

// unsupported reports that a tool is administratively disabled — a deliberate operator/build
// decision, distinct from "not present in this build" (no collaborator was ever wired) and from a
// semantic miss. It keeps missBody's existing available/reason shape, because a caller that
// already treats availability as a third answer needs no adapter to recognize this fourth one for
// what it is: an honest "not right now", never a silent absence. The tool itself stays listed in
// tools/list either way (interface contract: "a disabled tool must report itself unsupported, not
// silently absent").
func unsupported(name string) missBody {
	return unavailable(unsupportedReason(name))
}
