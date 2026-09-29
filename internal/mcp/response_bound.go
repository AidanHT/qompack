package mcp

import (
	"bytes"
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
// is unchanged for the caller and no byte is served twice. What it bounds is the result text; the
// JSON-RPC line that carries it adds its own envelope and escapes that text once more, which is the
// transport's framing and is bounded by the transport, not by this key.
//
// A cut must never fall inside a region the retrieval redactor replaces. Redaction is re-run on
// the kept raw bytes, and a rule that needs both ends of its match does not fire on half of it: a
// cut inside a private-key block leaves BEGIN with no END, so the kept half would be served in the
// clear, and the next page, which starts exactly at the cut, would serve the other half the same
// way. safeCut therefore accepts a cut only when redacting the two sides separately gives exactly
// what redacting the whole window gave, and otherwise moves the cut back.

// boundedContent renders a content tool's successful answer within runtime.mcp.maxResponseBytes.
//
// The caller has already refused the call when no redactor is wired. Redaction happens here, on
// the raw window; every cut is on the raw window too, and is checked by safeCut against the whole
// window's redaction before it is used. render builds the body for a given window and meta its
// _meta.qompack fields.
func (h *handlers) boundedContent(tool string, root store.Root, span SpanResult,
	o SpanOpts, render func(content []byte, s SpanResult) contentBody,
	meta func(s SpanResult) map[string]any,
) Response {
	limit := h.cfg.Runtime.MCP.MaxResponseBytes
	starts, _ := boundaries(root)
	window := span.Body
	whole, ok := h.redactForRetrieval(tool, window)
	if !ok {
		return h.jsonResponse(tool, unavailable(redactorMissingReason), nil)
	}
	content := whole
	for round := 0; ; round++ {
		b, err := marshalCompact(render(content, span))
		if err != nil {
			h.log.Warn("mcp: could not marshal a tool result", "tool", tool, "err", err.Error())
			return errResponse(tool + " failed: could not render its result")
		}
		if limit <= 0 || len(b) <= limit {
			return Response{Content: []Content{{Type: "text", Text: string(b)}}, Meta: meta(span)}
		}
		next, ok := shrinkSpan(span, starts, len(b)-limit, round, o)
		if !ok {
			// Not even one character of content fits beside the envelope. The configured bound has a
			// floor of 4096 bytes and the envelope is a few hundred, so this takes a path of
			// thousands of characters; it is still an honest answer rather than an empty page whose
			// next_span points back at itself.
			return errResponse(tool + " failed: the response envelope alone exceeds " +
				"runtime.mcp.maxResponseBytes (" + strconv.Itoa(limit) + " bytes)")
		}
		keep, kept, ok := h.safeCut(window, whole, int(next.End-next.Off))
		if !ok {
			return errResponse(tool + " failed: no page of this content fits runtime.mcp.maxResponseBytes " +
				"(" + strconv.Itoa(limit) + " bytes) without cutting through content the privacy policy redacts")
		}
		span, content = cutSpan(span, span.Off+int64(keep), o), kept
	}
}

// safeCut returns a cut at or before keep that splits no redacted region of window, together with
// the redacted bytes it keeps, and false only when the search reached the window's first rune
// boundary and found that unsafe too.
//
// whole is the redaction of all of window. A cut is safe when redacting the kept part and the rest
// separately gives whole again, byte for byte: a match that straddled the cut would come out as
// one placeholder in whole but as raw bytes, or as different placeholders, on the two sides. The
// check is conservative in the other direction too — a rule whose match depends on what precedes
// or follows it may make a harmless cut look unsafe, which only moves the cut back further. Every
// cut it returns has passed that check; how it picks the next cut to try only decides how soon it
// finds one.
//
// From an unsafe cut the search moves back in one of two ways.
//
//   - When the kept side's redaction parts from whole's, the region the cut split starts at or
//     before where they part. Everything the kept side renders after that point came from raw bytes
//     at the end of the kept part, and a placeholder is never longer than its match (the Redactor
//     contract), so the region starts no later than cut minus that many bytes. The search jumps
//     there: it skips only cuts inside the region, so under that contract it finds the largest safe
//     cut, which is the region's start, in a probe or two.
//   - When the kept side renders exactly as whole's prefix — a rule whose first part alone still
//     matches, such as a token with an unbounded run — the kept side does not say where the region
//     starts. The cut then backs off by a distance that doubles on each try (a rune, two, four, …).
//     A back-off that would pass the window's first rune restarts from the last unsafe cut with a
//     distance of one rune instead of giving up, so the search ends only at the first rune
//     boundary. The doubling can step over a safe cut between two regions, which only moves the cut
//     back further; the restarts keep it to a logarithmic number of tries per halving.
//
// Before the restart (V6 close-out, w14-safecut) the back-off gave up as soon as keep-back reached
// the window's start, so a region that began before keep/2 made it skip every safe cut before the
// region and refuse a page that fits.
func (h *handlers) safeCut(window, whole []byte, keep int) (int, []byte, bool) {
	if len(window) == 0 || keep <= 0 {
		return 0, nil, false
	}
	_, first := utf8.DecodeRune(window)
	// floor moves a cut back to a rune boundary, never below the first one.
	floor := func(c int) int {
		for c > first && c < len(window) && !utf8.RuneStart(window[c]) {
			c--
		}
		return max(c, first)
	}
	cut := floor(min(keep, len(window)))
	base, back := cut, 0
	for {
		kept, _ := h.redactor.Redact(window[:cut])
		rest, _ := h.redactor.Redact(window[cut:])
		if len(kept)+len(rest) == len(whole) && bytes.HasPrefix(whole, kept) && bytes.HasSuffix(whole, rest) {
			return cut, kept, true
		}
		if cut <= first {
			return 0, nil, false
		}
		if p := commonPrefixLen(kept, whole); p < len(kept) {
			cut = floor(cut - (len(kept) - p))
			base, back = cut, 0
			continue
		}
		back = max(back<<1, 1)
		if base-back < first {
			base, back = cut, 1
		}
		cut = floor(base - back)
	}
}

// commonPrefixLen is the length of the longest common prefix of a and b.
func commonPrefixLen(a, b []byte) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
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
//
// The charge is exact for raw content but not for redacted content: a redacted region renders as
// one short placeholder, so charging its raw bytes can remove far less text than it counts. round
// is how many cuts this response has already had, and the charge doubles with each one, so a
// response converges in a logarithmic number of rounds however much of it is redacted. When a
// doubled charge would consume everything, the cut keeps the first rune instead, and only a window
// already one rune long reports false.
func shrinkSpan(s SpanResult, starts []int64, excess, round int, o SpanOpts) (SpanResult, bool) {
	body := s.Body
	charge := excess
	for i := 0; i < round && charge < len(body)*unicodeEscapeLen; i++ {
		charge <<= 1
	}
	keep, charged := len(body), 0
	for keep > 0 && charged < charge {
		r, size := utf8.DecodeLastRune(body[:keep])
		charged += jsonEscapedLen(r, size)
		keep -= size
	}
	// Never leave the kept part ending inside a multi-byte rune: the next page would start on a
	// continuation byte, and JSON would render both halves as U+FFFD.
	for keep > 0 && keep < len(body) && !utf8.RuneStart(body[keep]) {
		keep--
	}
	if keep <= 0 && round > 0 {
		_, keep = utf8.DecodeRune(body)
		if keep >= len(body) {
			keep = 0
		}
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

// unicodeEscapeLen is the width of encoding/json's six-byte u-escape, which it writes for control
// bytes, invalid UTF-8, U+2028 and U+2029. It is also the most any one source byte can cost, so
// len(body)*unicodeEscapeLen bounds what a whole body can be charged.
const unicodeEscapeLen = 6

// jsonEscapedLen is an upper bound on the bytes encoding/json (HTML escaping off, as
// marshalCompact sets it) writes for one decoded rune of a string. size is the rune's width in the
// source, which is what an unescaped rune costs.
func jsonEscapedLen(r rune, size int) int {
	const (
		shortEscape   = 2 // \" \\ \n \r \t
		unicodeEscape = unicodeEscapeLen
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
