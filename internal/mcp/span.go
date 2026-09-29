package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// The minimum-sufficient-span resolver (Qompack.md §8.7, 00-ARCHITECTURE.md §5.16 "Span
// default"). expand and re_read return the matching function or hunk by default — not the file —
// resolved through the store's own chunk boundaries plus a symbol-aware widener, capped at
// store.chunk.max, with full=true as the explicit escape hatch. Most post-compaction questions
// are "what did that one function look like", not "give me the file".
//
// ResolveSpan reads NO configuration. Every bound arrives in SpanOpts, filled by its two call
// sites from cfg.Store.Chunk.Max, cfg.Runtime.MCP.MaxResponseBytes and
// cfg.Runtime.MCP.SpanWidenLines (D11, §11.6). That is what makes it a pure function of its
// arguments and therefore table- and property-testable without a project on disk.
//
// Four properties are normative and property-tested:
//
//   - [Off, End) begins and ends on a chunk boundary, or on the object's end (an ExactStart
//     explicit byte window begins at the offset it names instead);
//   - End > Off for any non-empty object;
//   - End-Off never exceeds MaxResponse;
//   - following NextSpan from offset 0 concatenates to the whole object exactly once, with no
//     gaps and no overlaps.
//
// The last one is why alignment is not cosmetic: a next-span that started mid-chunk would be
// aligned BACKWARDS by the next call and hand the model the same bytes twice. The one window that
// cannot end on a boundary — a single chunk larger than MaxResponse — is why ExactStart exists.

// spanByteRe and spanLineRe are the two explicit-span spellings: "<off>:<len>" in bytes, and
// "L<start>-L<end>" in 1-based inclusive lines.
var (
	spanByteRe = regexp.MustCompile(`^(\d+):(\d+)$`)
	spanLineRe = regexp.MustCompile(`^L(\d+)-L(\d+)$`)
)

// SpanOpts is everything ResolveSpan needs beyond the object itself.
type SpanOpts struct {
	// Full asks for the whole object rather than the minimum sufficient span. It is still capped
	// at MaxResponse: "full" means "do not narrow to the matching hunk", not "unbounded".
	Full bool
	// Explicit is a caller-supplied span: "" | "<off>:<len>" | "L<a>-L<b>".
	Explicit string
	// RuneSafe keeps a window that stops short of the object's end from ending inside a multi-byte
	// UTF-8 sequence (readWindow). The retrieval tools set it, together with ExactStart, which is
	// what lets the shifted end be followed: the next page starts exactly where this one stopped.
	RuneSafe bool
	// ExactStart starts an explicit "<off>:<len>" window at <off> itself instead of aligning it
	// back to the start of the chunk holding it. The retrieval tools set it, because the next_span
	// they publish is not always a chunk start: a cut inside one chunk larger than the response
	// bound, or inside a chunk whose JSON escaping does not fit beside the envelope, is a byte
	// offset, and aligning that cursor backwards re-serves the bytes before it — and, when the chunk
	// still does not fit, the same page with the same cursor for ever. The end is chosen as for any
	// other window.
	ExactStart bool
	// Path is the paths.Key-form path the content belongs to, or "" when it is not a file. The
	// widener needs it to pick a language dialect.
	Path string
	// AnchorSym is a symbol name the span should centre on, from re_read's "file.ts:name" form.
	AnchorSym string
	// AnchorLine is a 1-based line the span should contain, from re_read's "file.ts:120" form.
	// 0 means none.
	AnchorLine int
	// MaxSpan caps the minimal span; it is cfg.Store.Chunk.Max.
	MaxSpan int
	// MaxResponse caps every span, minimal or full; it is cfg.Runtime.MCP.MaxResponseBytes.
	MaxResponse int
	// WidenLines caps how far the symbol widener may extend the tail; it is
	// cfg.Runtime.MCP.SpanWidenLines.
	WidenLines int
}

// SpanResult is the resolved window and the bytes in it.
type SpanResult struct {
	// Off and End are the resolved [Off, End) window in the object's canonical byte space.
	Off, End int64
	// Total is the object's canonical size.
	Total int64
	// Truncated reports that this is less than the whole object.
	Truncated bool
	// NextSpan is the "<off>:<len>" to pass back as `span` to continue reading; "" at the end.
	NextSpan string
	// Widened reports that the symbol widener extended the tail past the chunk boundary.
	Widened bool
	// Body is the content of [Off, End).
	Body []byte
}

// byteReader reads one span of an object. It exists so the resolver can run against a store, an
// in-memory chunk (`expand` given a chunk hash rather than a root hash), or a test double,
// without any of them needing to be a store.Store.
type byteReader func(off, n int64) ([]byte, error)

// ResolveSpan resolves the minimum sufficient span of root under o, reading through s.
func ResolveSpan(ctx context.Context, s store.Store, root store.Root,
	w Widener, o SpanOpts,
) (SpanResult, error) {
	if s == nil {
		return SpanResult{}, errors.New("qompack: mcp: no store")
	}
	return resolveSpan(storeReader(ctx, s, root.Hash), root, w, o)
}

// ResolveSpanFromBytes resolves the minimum sufficient span of root from an ALREADY-MATERIALIZED
// buffer, without going back to the store.
//
// It exists for the one address form the store cannot serve: `expand` given a CHUNK hash. The
// store's OpenSpan resolves through GetRoot, and a chunk hash is by construction absent from the
// root index, so a synthesized single-chunk Root read back through the store fails with
// "not found: root <hash>" — the chunk is in hand and the read still cannot happen. Handing the
// bytes straight to the resolver is what makes that escape hatch work.
func ResolveSpanFromBytes(b []byte, root store.Root, w Widener, o SpanOpts) (SpanResult, error) {
	return resolveSpan(memReader(b), root, w, o)
}

// storeReader adapts a Store to a byteReader, reading each span through OpenSpan and bounding the
// read with a LimitReader so a store that over-delivers cannot blow the response budget.
func storeReader(ctx context.Context, s store.Store, root core.Hash) byteReader {
	return func(off, n int64) ([]byte, error) {
		if n <= 0 {
			return nil, nil
		}
		rc, err := s.OpenSpan(ctx, root, off, n)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return io.ReadAll(io.LimitReader(rc, n))
	}
}

// memReader adapts an already-materialized buffer to a byteReader.
func memReader(b []byte) byteReader {
	return func(off, n int64) ([]byte, error) {
		if off < 0 || off >= int64(len(b)) || n <= 0 {
			return nil, nil
		}
		end := off + n
		if end > int64(len(b)) {
			end = int64(len(b))
		}
		return b[off:end], nil
	}
}

// chunkTotal is the object's canonical size according to its own chunk list. It is compared
// against Root.CanonBytes by the callers, which Loud-log a disagreement: two sources of truth for
// one length is exactly the kind of index corruption that otherwise shows up as a silently short
// retrieval.
func chunkTotal(root store.Root) int64 {
	var total int64
	for _, c := range root.Chunks {
		total += int64(c.Len)
	}
	if total == 0 {
		return root.CanonBytes
	}
	return total
}

// boundaries returns the chunk start offsets (offsets[0..n-1]) and the object's total size. The
// terminal offset is deliberately NOT a member: it is a chunk END, and treating it as a start
// would let chunkStartAtOrBefore return an empty window at the object's tail.
func boundaries(root store.Root) (starts []int64, total int64) {
	total = chunkTotal(root)
	if len(root.Chunks) == 0 {
		return []int64{0}, total
	}
	starts = make([]int64, 0, len(root.Chunks))
	var at int64
	for _, c := range root.Chunks {
		starts = append(starts, at)
		at += int64(c.Len)
	}
	return starts, total
}

// chunkStartAtOrBefore returns the largest chunk start not after x.
func chunkStartAtOrBefore(starts []int64, x int64) int64 {
	out := starts[0]
	for _, s := range starts {
		if s <= x {
			out = s
			continue
		}
		break
	}
	return out
}

// chunkEndAtOrAfter returns the smallest chunk boundary not before x, or the object's end.
func chunkEndAtOrAfter(starts []int64, total, x int64) int64 {
	for _, s := range starts {
		if s >= x {
			return s
		}
	}
	return total
}

// spanStr renders the "<off>:<len>" spelling `span` and NextSpan both use.
func spanStr(off, n int64) string {
	return strconv.FormatInt(off, 10) + ":" + strconv.FormatInt(n, 10)
}

// resolveSpan is ResolveSpan over an arbitrary byteReader. The steps below are 00-ARCHITECTURE.md
// §5.16's algorithm in order.
func resolveSpan(read byteReader, root store.Root, w Widener, o SpanOpts) (SpanResult, error) {
	starts, total := boundaries(root)
	if total <= 0 {
		return SpanResult{Total: 0}, nil
	}
	maxResp := int64(o.MaxResponse)
	if maxResp <= 0 {
		maxResp = total
	}

	// Step 1 — full. A full read is a byte window by definition, not a chunk window: the caller
	// asked for the object, and aligning outward would hand back more than the object.
	if o.Full {
		body, end, err := readWindow(read, 0, minInt64(total, maxResp), total, maxResp, o.RuneSafe)
		if err != nil {
			return SpanResult{}, err
		}
		res := SpanResult{Off: 0, End: end, Total: total, Truncated: end < total, Body: body}
		if end < total {
			res.NextSpan = spanStr(end, minInt64(maxResp, total-end))
		}
		return res, nil
	}

	off, end, err := resolveWindow(read, starts, total, maxResp, w, o)
	if err != nil {
		return SpanResult{}, err
	}
	res := SpanResult{Off: off, End: end, Total: total}

	// Step 6 — symbol widening at the tail.
	if o.Path != "" && w != nil && end < total {
		if newEnd, ok := widenTail(read, starts, total, maxResp, w, o, off, end); ok {
			res.End, res.Widened = newEnd, true
			end = newEnd
		}
	}

	// Step 7 — the response cap, the body, and the paging cursor. The cap lands on a chunk
	// boundary when one exists inside it, so following NextSpan never re-reads bytes the previous
	// page already returned; a single chunk larger than the whole response budget is the one case
	// that falls back to a hard cut, because there is no boundary to land on.
	if end-off > maxResp {
		aligned := chunkStartAtOrBefore(starts, off+maxResp)
		if aligned > off {
			end = aligned
		} else {
			end = off + maxResp
		}
		res.End = end
	}

	body, end, err := readWindow(read, off, end, total, maxResp, o.RuneSafe)
	if err != nil {
		return SpanResult{}, err
	}
	res.Body, res.End = body, end
	res.Truncated = off > 0 || end < total
	if end < total {
		res.NextSpan = spanStr(end, minInt64(int64(o.MaxSpan), total-end))
	}
	return res, nil
}

// readWindow reads [off, end) and, when runeSafe is set and the window stops short of the object's
// end, moves that end off a split UTF-8 sequence.
//
// A chunk boundary is content-defined at the byte level and a hard cut is a byte count, so either
// can fall inside a multi-byte rune; the page would then end, and the next one begin, with a
// fragment that reaches the model as U+FFFD — a character the object does not contain, on both
// sides of the cut. The end is extended to finish the rune when that stays inside maxResp, and is
// otherwise moved back to the rune's start, never to the window's own start. Only a tail that
// really is an incomplete sequence moves, and only then are the at most utf8.UTFMax-1 bytes past
// end read: binary content keeps its window unless its last bytes happen to look like a lead byte.
func readWindow(read byteReader, off, end, total, maxResp int64, runeSafe bool) ([]byte, int64, error) {
	b, err := read(off, end-off)
	if err != nil {
		return nil, 0, err
	}
	n := end - off
	if !runeSafe || end >= total || n <= 0 || int64(len(b)) < n {
		return b, end, nil
	}
	lead := n - 1
	for lead > 0 && n-lead < utf8.UTFMax && !utf8.RuneStart(b[lead]) {
		lead--
	}
	if !utf8.RuneStart(b[lead]) || utf8.FullRune(b[lead:n]) {
		return b, end, nil
	}
	// The window ends inside a rune. Only now read the few bytes past it that could finish it, so
	// the common case — an ASCII or rune-aligned end — costs no read beyond the window itself.
	if more, merr := read(end, minInt64(utf8.UTFMax-1, total-end)); merr == nil {
		tail := append(append(make([]byte, 0, int64(len(more))+n-lead), b[lead:n]...), more...)
		if full := int64(firstFullRune(tail)); full > 0 && lead+full <= maxResp {
			out := append(append(make([]byte, 0, lead+full), b[:lead]...), tail[:full]...)
			return out, off + lead + full, nil
		}
	}
	if lead > 0 {
		return b[:lead], off + lead, nil
	}
	return b, end, nil
}

// firstFullRune returns the width of the complete rune b starts with, or 0 when b holds only part
// of one.
func firstFullRune(b []byte) int {
	if !utf8.FullRune(b) {
		return 0
	}
	_, size := utf8.DecodeRune(b)
	return size
}

// resolveWindow runs steps 2 through 5: the explicit spans, the anchor probe, and the
// chunk-walking minimal window. hardEnd is a symbol's own end, which step 5 honours past MaxSpan
// when it fits inside the response budget — a function cut in half is not a minimum SUFFICIENT
// span.
func resolveWindow(read byteReader, starts []int64, total, maxResp int64,
	w Widener, o SpanOpts,
) (off, end int64, err error) {
	var hardEnd int64
	// Step 2 — an explicit byte span.
	if m := spanByteRe.FindStringSubmatch(o.Explicit); m != nil {
		off0 := clamp64(parseInt64(m[1]), 0, total)
		want := off0 + parseInt64(m[2])
		off = chunkStartAtOrBefore(starts, off0)
		if o.ExactStart && off0 < total {
			off = off0
		}
		end = chunkEndAtOrAfter(starts, total, clamp64(want, off+1, total))
		return off, end, nil
	}

	// Step 3 — an explicit line span.
	if m := spanLineRe.FindStringSubmatch(o.Explicit); m != nil {
		probe, perr := read(0, minInt64(total, maxResp))
		if perr != nil {
			return 0, 0, perr
		}
		off0 := lineStartOffset(probe, int(parseInt64(m[1])))
		end0 := lineEndOffset(probe, int(parseInt64(m[2])))
		off = chunkStartAtOrBefore(starts, off0)
		end = chunkEndAtOrAfter(starts, total, maxInt64(end0, off+1))
		return off, end, nil
	}

	// Step 4 — the anchor probe. This is the one read that cannot be bounded by MaxSpan: a symbol
	// or a line number may legitimately sit anywhere in the object. It is bounded by MaxResponse
	// and abandoned as soon as the anchor is located, so the common — anchorless — case never
	// pays for it at all.
	var anchor int64
	if o.AnchorSym != "" || o.AnchorLine > 0 {
		probe, perr := read(0, minInt64(total, maxResp))
		if perr != nil {
			return 0, 0, perr
		}
		if o.AnchorSym != "" && w != nil {
			if a, b, ok := w.Find(o.Path, probe, o.AnchorSym); ok {
				anchor, hardEnd = a, b
			}
		}
		// A line anchor is the fallback as well as an alternative: re_read's suffix is either a
		// symbol or a line, never both, but a symbol lookup that finds nothing must not silently
		// discard a line the caller also gave.
		if anchor == 0 && hardEnd == 0 && o.AnchorLine > 0 {
			anchor = lineStartOffset(probe, o.AnchorLine)
		}
	}

	// Step 5 — walk chunks forward from the anchor's own chunk, stopping at MaxSpan but never
	// before one whole chunk has been taken.
	off = chunkStartAtOrBefore(starts, anchor)
	end = off
	for i := indexOfStart(starts, off); i < len(starts); i++ {
		next := total
		if i+1 < len(starts) {
			next = starts[i+1]
		}
		if end > off && next-off > int64(o.MaxSpan) {
			break
		}
		end = next
		if end >= total {
			end = total
			break
		}
	}
	if hardEnd > end && hardEnd-off <= maxResp {
		end = chunkEndAtOrAfter(starts, total, hardEnd)
	}
	return off, end, nil
}

// widenTail extends end to the end of the symbol enclosing it, when the widener finds one and the
// extension stays inside both the line budget and the response budget.
//
// The probe is bounded at 2 x MaxSpan past off, not at the object's end: a widener needs enough
// text after the cut to see the enclosing symbol close, and twice the maximum chunk is enough for
// any function a chunk boundary could have split, while an unbounded probe would read the whole
// object to widen by four lines.
func widenTail(read byteReader, starts []int64, total, maxResp int64,
	w Widener, o SpanOpts, off, end int64,
) (int64, bool) {
	probeEnd := minInt64(total, off+2*int64(o.MaxSpan))
	if probeEnd <= off {
		return 0, false
	}
	probe, err := read(off, probeEnd-off)
	if err != nil || int64(len(probe)) < end-off {
		return 0, false
	}
	_, e2, ok := w.Widen(o.Path, probe, 0, end-off)
	if !ok || e2 <= end-off || e2 > int64(len(probe)) {
		return 0, false
	}
	extra := probe[end-off : e2]
	if bytes.Count(extra, []byte{'\n'}) > o.WidenLines || e2 > maxResp {
		return 0, false
	}
	widened := chunkEndAtOrAfter(starts, total, off+e2)
	if widened <= end {
		return 0, false
	}
	return widened, true
}

// indexOfStart returns the position of the chunk whose start is exactly off.
func indexOfStart(starts []int64, off int64) int {
	for i, s := range starts {
		if s == off {
			return i
		}
	}
	return 0
}

// lineStartOffset returns the byte offset of 1-based line n in b, or len(b) when b has fewer
// lines than that.
func lineStartOffset(b []byte, n int) int64 {
	if n <= 1 {
		return 0
	}
	seen := 1
	for i, c := range b {
		if c != '\n' {
			continue
		}
		seen++
		if seen == n {
			return int64(i + 1)
		}
	}
	return int64(len(b))
}

// lineEndOffset returns the byte offset just past the newline ending 1-based line n, or len(b)
// when b has fewer lines than that.
func lineEndOffset(b []byte, n int) int64 {
	if n < 1 {
		return 0
	}
	seen := 1
	for i, c := range b {
		if c != '\n' {
			continue
		}
		if seen == n {
			return int64(i + 1)
		}
		seen++
	}
	return int64(len(b))
}

// parseInt64 reads a decimal the regexp has already proven to be digits. An overflow — a span
// offset longer than 19 digits — clamps to the maximum rather than failing: every use of the
// result is itself clamped to the object's size a line later.
func parseInt64(s string) int64 {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return int64(^uint64(0) >> 1)
	}
	return v
}

// clamp64 constrains v to [lo, hi]. hi wins when the two cross, which happens when a caller asks
// for a span starting past the object's end.
func clamp64(v, lo, hi int64) int64 {
	if v > hi {
		return hi
	}
	if v < lo {
		return lo
	}
	return v
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
