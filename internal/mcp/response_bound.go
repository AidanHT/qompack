package mcp

import (
	"strconv"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/store"
)

// runtime.mcp.maxResponseBytes bounds what a content tool RETURNS: the result text the model
// receives, which is the JSON body with its content string escaped and its envelope fields beside
// it. ResolveSpan can only bound the span of raw content it reads, and that is not the same number:
// the envelope adds a few hundred bytes, and JSON escaping grows content by up to six times — a
// quote or a backslash doubles, a control character or an invalid UTF-8 byte becomes a six-byte
// escape. The V6 live lane measured the difference (live report install D2, UAT-12 (a)): expand
// and re_read with full:true returned 263,559 and 263,567 bytes of text against a 262,144-byte
// bound, and an escape-heavy 2,134-byte capture became 2,948 bytes of text.
//
// boundedContent therefore renders the response, measures it, and while it is over the bound cuts
// the span back — to a chunk boundary where one lies inside the kept part, and to a rune boundary
// otherwise — and renders again. The continuation it publishes is the ordinary next_span, and the
// retrieval tools start an explicit span exactly where it points (SpanOpts.ExactStart), so paging
// is unchanged for the caller and no byte is served twice. What it bounds is the result text; the JSON-RPC
// line that carries it adds its own envelope and escapes that text once more, which is the
// transport's framing and is bounded by the transport, not by this key.

// boundedContent renders a content tool's successful answer within runtime.mcp.maxResponseBytes.
//
// The caller has already refused the call when no redactor is wired. Redaction happens here, on
// the raw window, and again after every cut: it does not map offsets, so a cut can never be applied
// to redacted bytes. render builds the body for a given window and meta its _meta.qompack fields.
func (h *handlers) boundedContent(tool string, root store.Root, span SpanResult,
	o SpanOpts, render func(content []byte, s SpanResult) contentBody,
	meta func(s SpanResult) map[string]any,
) Response {
	limit := h.cfg.Runtime.MCP.MaxResponseBytes
	starts, _ := boundaries(root)
	content, ok := h.redactForRetrieval(tool, span.Body)
	if !ok {
		return h.jsonResponse(tool, unavailable(redactorMissingReason), nil)
	}
	for {
		b, err := marshalCompact(render(content, span))
		if err != nil {
			h.log.Warn("mcp: could not marshal a tool result", "tool", tool, "err", err.Error())
			return errResponse(tool + " failed: could not render its result")
		}
		if limit <= 0 || len(b) <= limit {
			return Response{Content: []Content{{Type: "text", Text: string(b)}}, Meta: meta(span)}
		}
		next, ok := shrinkSpan(span, starts, len(b)-limit, o)
		if !ok {
			// Not even one character of content fits beside the envelope. The configured bound has a
			// floor of 4096 bytes and the envelope is a few hundred, so this takes a path of
			// thousands of characters; it is still an honest answer rather than an empty page whose
			// next_span points back at itself.
			return errResponse(tool + " failed: the response envelope alone exceeds " +
				"runtime.mcp.maxResponseBytes (" + strconv.Itoa(limit) + " bytes)")
		}
		span = next
		// The first round already Louded any rule that fired; a shorter window of the same bytes can
		// only fire a subset of them, so the later rounds redact quietly. h.redactor is non-nil:
		// redactForRetrieval answered ok above.
		content, _ = h.redactor.Redact(span.Body)
	}
}

// shrinkSpan cuts s back far enough that its rendered text loses at least excess bytes, and
// reports false when nothing of the window would remain.
//
// It walks the raw body backwards a rune at a time, charging each rune what JSON encoding costs
// for it (jsonEscapedLen, an upper bound), until the charge covers the excess. The cut then moves
// back to the last chunk boundary inside the kept part when there is one, so pages keep the
// content-defined edges the minimal span is built from. With no boundary inside — one chunk larger
// than what fits, or an in-memory chunk-hash expansion — the cut stays on the rune boundary, and
// the next call starts exactly there (SpanOpts.ExactStart).
func shrinkSpan(s SpanResult, starts []int64, excess int, o SpanOpts) (SpanResult, bool) {
	body := s.Body
	keep, charged := len(body), 0
	for keep > 0 && charged < excess {
		r, size := utf8.DecodeLastRune(body[:keep])
		charged += jsonEscapedLen(r, size)
		keep -= size
	}
	// Never leave the kept part ending inside a multi-byte rune: the next page would start on a
	// continuation byte, and JSON would render both halves as U+FFFD.
	for keep > 0 && keep < len(body) && !utf8.RuneStart(body[keep]) {
		keep--
	}
	if keep <= 0 {
		return SpanResult{}, false
	}
	end := s.Off + int64(keep)
	if aligned := chunkStartAtOrBefore(starts, end); aligned > s.Off && utf8.RuneStart(body[aligned-s.Off]) {
		end = aligned
	}
	return cutSpan(s, end, o), true
}

// cutSpan shortens s to end at end (s.Off < end < s.End) and points next_span at the rest.
func cutSpan(s SpanResult, end int64, o SpanOpts) SpanResult {
	out := s
	out.End = end
	out.Body = s.Body[:end-s.Off]
	out.Truncated = true
	// The widened tail, if any, is what was just cut away.
	out.Widened = false
	// The cursor's length follows the rule the resolver used for this kind of read: a full read
	// pages in response-sized steps, a minimal one in chunk.max-sized steps.
	step := int64(o.MaxSpan)
	if o.Full {
		step = int64(o.MaxResponse)
	}
	out.NextSpan = spanStr(end, minInt64(step, s.Total-end))
	return out
}

// jsonEscapedLen is an upper bound on the bytes encoding/json (HTML escaping off, as
// marshalCompact sets it) writes for one decoded rune of a string. size is the rune's width in the
// source, which is what an unescaped rune costs.
func jsonEscapedLen(r rune, size int) int {
	const (
		shortEscape   = 2 // \" \\ \n \r \t
		unicodeEscape = 6 // the six-byte u-escape: control bytes, invalid UTF-8, U+2028, U+2029
		// lineSeparator and paragraphSeparator are escaped by encoding/json for JavaScript safety.
		lineSeparator      = 0x2028
		paragraphSeparator = 0x2029
	)
	switch {
	case r == utf8.RuneError && size == 1:
		return unicodeEscape
	case r == '"' || r == '\\' || r == '\n' || r == '\r' || r == '\t':
		return shortEscape
	case r < ' ':
		return unicodeEscape
	case r == lineSeparator || r == paragraphSeparator:
		return unicodeEscape
	}
	return size
}
