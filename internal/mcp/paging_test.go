package mcp

import (
	"strconv"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// Paging consistency (V6 close-out D50). The candidate 4 live re-run (UAT-12, cli/20-mcp-probe.json)
// found two paging defects on a 354,352-byte capture: the final page, reached by following
// next_span, answered truncated:true with no next_span, so a caller told "there is more" had no way
// to ask for it; and an explicit span 0:354352 paged as next_span 217070:16384 while full:true over
// the same range paged as 217070:137282. D50: a truncated page always carries a next_span that
// continues it and a page that reaches the end is not truncated; an explicit span pages exactly like
// full:true over the same range — the same cut, and a next_span covering the rest of the requested
// span.

// pagingPage is one page of a walk: the decoded body and the _meta.qompack the response published.
type pagingPage struct {
	body contentBody
	meta map[string]any
}

// walkPages calls tool with args and follows next_span until a page carries none, returning every
// page. It asserts only what every walk needs to terminate; the tests assert the rest.
func walkPages(t *testing.T, f *fixture, tool string, args map[string]any) []pagingPage {
	t.Helper()
	var pages []pagingPage
	for {
		require.Less(t, len(pages), pagingMaxPages, "paging must terminate")
		resp := f.call(t, tool, args)
		require.False(t, resp.IsError, "%s page %d: %s", tool, len(pages), responseText(resp))
		var body contentBody
		decodeInto(t, responseText(resp), &body)
		require.True(t, body.Found)
		pages = append(pages, pagingPage{body: body, meta: resp.Meta})
		if body.NextSpan == "" {
			return pages
		}
		// Every walk continues the same way: expand by the page's hash from its cursor (re_read takes
		// no span, so that is also how a re_read page is continued).
		tool, args = ToolExpand, map[string]any{"hash": body.Hash, "span": body.NextSpan}
	}
}

// pagingMaxPages bounds a walk: the largest object here is 16 x boundTinyResponse bytes of
// escape-heavy text, which pages in well under a hundred bounded responses.
const pagingMaxPages = 200

// spanEnd parses a "<off>:<len>" cursor and returns off+len.
func spanEnd(t *testing.T, s string) int64 {
	t.Helper()
	m := spanByteRe.FindStringSubmatch(s)
	require.NotNil(t, m, "%q is not an <off>:<len> cursor", s)
	off, err := strconv.ParseInt(m[1], 10, 64)
	require.NoError(t, err)
	n, err := strconv.ParseInt(m[2], 10, 64)
	require.NoError(t, err)
	return off + n
}

// requireTruncatedIffNextSpan is D50's first rule on one page: truncated, a next_span, and "this page
// stops before the object's end" are the same statement, in the body and in _meta.qompack alike.
func requireTruncatedIffNextSpan(t *testing.T, label string, i int, p pagingPage) {
	t.Helper()
	b := p.body
	more := b.Span[1] < b.TotalBytes
	require.Equal(t, more, b.Truncated,
		"%s page %d [%d,%d) of %d: truncated must say whether the page stops before the end",
		label, i, b.Span[0], b.Span[1], b.TotalBytes)
	require.Equal(t, more, b.NextSpan != "",
		"%s page %d [%d,%d) of %d: a truncated page carries next_span, a page at the end none",
		label, i, b.Span[0], b.Span[1], b.TotalBytes)
	if more {
		require.True(t, strings.HasPrefix(b.NextSpan, strconv.FormatInt(b.Span[1], 10)+":"),
			"%s page %d: next_span %q must continue where the page stopped (%d)",
			label, i, b.NextSpan, b.Span[1])
	}
	require.Equal(t, b.Truncated, p.meta[metaTruncated], "%s page %d: _meta truncated", label, i)
	ns, ok := p.meta[metaNextSpan]
	require.Equal(t, b.NextSpan != "", ok, "%s page %d: _meta next_span presence", label, i)
	if ok {
		require.Equal(t, b.NextSpan, ns, "%s page %d: _meta next_span", label, i)
	}
}

// TestExpandEveryTruncatedPageCarriesNextSpan walks an escape-heavy object through every way a
// caller starts paging — full:true, the minimal span, an explicit span from offset 0, an explicit
// span that starts mid-object and runs to the end, and re_read full:true — and holds every page to
// D50's first rule. Before the fix the last page of every walk answered truncated:true with no
// next_span, because truncated meant "less than the whole object" and a page starting past 0 is that.
func TestExpandEveryTruncatedPageCarriesNextSpan(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.Config) { c.Runtime.MCP.MaxResponseBytes = boundTinyResponse }))
	h, id := f.putAndRecord(t, "Read", "data/escapes.txt", boundEscapeHeavy(6*boundTinyResponse), 1)
	want := storedBytes(t, f, h)
	total := len(want)
	// The mid-object walk starts on the last line, so its first byte is a rune start.
	lastOff := total - len(boundEscapeLine)
	lastLine := strconv.Itoa(lastOff) + ":" + strconv.Itoa(len(boundEscapeLine))

	walks := []struct {
		name string
		tool string
		args map[string]any
		from int
	}{
		{"expand full", ToolExpand, map[string]any{"tool_use_id": string(id), "full": true}, 0},
		{"expand minimal", ToolExpand, map[string]any{"tool_use_id": string(id)}, 0},
		{"expand span 0:total", ToolExpand, map[string]any{"hash": h.String(), "span": "0:" + strconv.Itoa(total)}, 0},
		{"expand span from mid", ToolExpand, map[string]any{"hash": h.String(), "span": lastLine}, lastOff},
		{"re_read full", ToolReRead, map[string]any{"path": "data/escapes.txt", "full": true}, 0},
	}
	for _, w := range walks {
		pages := walkPages(t, f, w.tool, w.args)
		var got strings.Builder
		for i, p := range pages {
			requireTruncatedIffNextSpan(t, w.name, i, p)
			got.WriteString(p.body.Content)
		}
		last := pages[len(pages)-1].body
		require.EqualValues(t, total, last.Span[1], "%s: the walk ends at the object's end", w.name)
		require.False(t, last.Truncated, "%s: the page that reaches the end is not truncated", w.name)
		require.Equal(t, want[w.from:], got.String(), "%s: the walk serves the rest of the object once", w.name)
	}
}

// TestExpandExplicitSpanPagesLikeFull is D50's second rule at the live shape and at the smallest
// bound: an explicit span over the whole object pages exactly like full:true — the same pages, the
// same cuts, the same cursors — and every cut page's next_span covers the rest of what was asked
// for, the object's end. Before the fix the explicit span capped its window back to a chunk start
// and published a chunk.max-sized cursor (live: 217070:16384 against full's 217070:137282).
func TestExpandExplicitSpanPagesLikeFull(t *testing.T) {
	shapes := []struct {
		name  string
		limit int
		body  string
	}{
		{"live log at the default bound", 0, boundLogLike(boundLiveTotal)},
		{"escape-heavy at the smallest bound", boundTinyResponse, boundEscapeHeavy(6 * boundTinyResponse)},
	}
	for _, sh := range shapes {
		// Each walk gets its own project with the same capture. The envelope beside the content
		// carries the object's expansion count, which grows with every call; a walk run after the
		// other would render one byte longer at "expansions":10 and be cut one byte earlier, which
		// is the bound working, not the two reads differing.
		walk := func(args func(id core.ToolUseID, total int64) map[string]any) ([]pagingPage, int64) {
			f := newFixture(t, withConfig(func(c *config.Config) {
				if sh.limit > 0 {
					c.Runtime.MCP.MaxResponseBytes = sh.limit
				}
			}))
			h, id := f.putAndRecord(t, "Read", "big.log", sh.body, 1)
			total := int64(len(storedBytes(t, f, h)))
			return walkPages(t, f, ToolExpand, args(id, total)), total
		}
		full, total := walk(func(id core.ToolUseID, _ int64) map[string]any {
			return map[string]any{"tool_use_id": string(id), "full": true}
		})
		explicit, _ := walk(func(id core.ToolUseID, total int64) map[string]any {
			return map[string]any{"tool_use_id": string(id), "span": "0:" + strconv.FormatInt(total, 10)}
		})
		require.Greater(t, len(full), 1, "%s: the object must need more than one bounded page", sh.name)
		require.Len(t, explicit, len(full), "%s: the explicit span pages in as many responses as full", sh.name)
		for i := range full {
			fb, eb := full[i].body, explicit[i].body
			require.Equal(t, fb.Span, eb.Span, "%s page %d: the same cut", sh.name, i)
			require.Equal(t, fb.NextSpan, eb.NextSpan, "%s page %d: the same cursor", sh.name, i)
			require.Equal(t, fb.Truncated, eb.Truncated, "%s page %d: the same truncated", sh.name, i)
			require.Equal(t, fb.Content, eb.Content, "%s page %d: the same bytes", sh.name, i)
			if fb.NextSpan != "" {
				require.Equal(t, total, spanEnd(t, fb.NextSpan),
					"%s page %d: next_span %q must cover the rest of the requested span", sh.name, i, fb.NextSpan)
			}
		}
	}
}

// TestExpandExplicitSubSpanNextSpanCoversTheRestOfTheRequest pins the cursor of a cut explicit span
// that stops short of the object: it covers the rest of the requested span — up to the chunk
// boundary the request resolved to — so following it finishes the request in bounded pages instead
// of drifting into chunk.max-sized steps; the page that completes the request, still short of the
// object's end, is truncated and carries a cursor on into the object.
func TestExpandExplicitSubSpanNextSpanCoversTheRestOfTheRequest(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.Config) { c.Runtime.MCP.MaxResponseBytes = boundTinyResponse }))
	h, _ := f.putAndRecord(t, "Read", "data/escapes.txt", boundEscapeHeavy(16*boundTinyResponse), 1)
	want := storedBytes(t, f, h)
	root := spanRootOf(t, f, h)
	starts, total := boundaries(root)
	require.Greater(t, len(starts), 2, "the object must span several chunks")

	const reqOff, reqLen = 1000, 3 * boundTinyResponse
	reqEnd := chunkEndAtOrAfter(starts, total, reqOff+reqLen)
	require.Less(t, reqEnd, total, "the request must stop short of the object")

	args := map[string]any{"hash": h.String(), "span": strconv.Itoa(reqOff) + ":" + strconv.Itoa(reqLen)}
	var got strings.Builder
	for i := 0; ; i++ {
		require.Less(t, i, pagingMaxPages, "the request must finish")
		body := spanContentOf(t, f, ToolExpand, args)
		require.True(t, body.Truncated, "page %d stops before the object's end", i)
		require.NotEmpty(t, body.NextSpan, "page %d: a truncated page carries next_span", i)
		got.WriteString(body.Content)
		if body.Span[1] >= reqEnd {
			require.Equal(t, reqEnd, body.Span[1], "page %d: the request ends where it resolved", i)
			break
		}
		require.Equal(t, reqEnd, spanEnd(t, body.NextSpan),
			"page %d [%d,%d): next_span %q must cover the rest of the requested span [%d,%d)",
			i, body.Span[0], body.Span[1], body.NextSpan, body.Span[1], reqEnd)
		args = map[string]any{"hash": h.String(), "span": body.NextSpan}
	}
	require.Equal(t, want[reqOff:reqEnd], got.String(), "the request is served exactly once")
}

// TestResolveSpanExplicitWholeRangeMatchesFull is the resolver-level half of D50's second rule: with
// the retrieval tools' options, an explicit span over the whole object resolves to exactly the window,
// the body and the cursor a full read resolves to, even where no chunk boundary sits at the response
// cap. Before the fix the explicit window was capped back to the chunk start at 300.
func TestResolveSpanExplicitWholeRangeMatchesFull(t *testing.T) {
	b := []byte(strings.Repeat("0123456789", 100))
	root := store.Root{Chunks: []core.ChunkRef{{Len: 300}, {Len: 300}, {Len: 400}}, CanonBytes: int64(len(b))}
	base := SpanOpts{MaxSpan: 300, MaxResponse: 512, ExactStart: true, RuneSafe: true}

	fo := base
	fo.Full = true
	full, err := ResolveSpanFromBytes(b, root, nil, fo)
	require.NoError(t, err)
	require.Equal(t, int64(512), full.End)
	require.Equal(t, "512:488", full.NextSpan)

	eo := base
	eo.Explicit = "0:1000"
	explicit, err := ResolveSpanFromBytes(b, root, nil, eo)
	require.NoError(t, err)
	require.Equal(t, full, explicit, "an explicit span over the whole object resolves like full")

	// Past the cut, the cursor full published pages on to the end, and that page is not truncated.
	eo.Explicit = full.NextSpan
	rest, err := ResolveSpanFromBytes(b, root, nil, eo)
	require.NoError(t, err)
	require.Equal(t, [2]int64{512, 1000}, [2]int64{rest.Off, rest.End})
	require.False(t, rest.Truncated, "a page that reaches the end is not truncated")
	require.Empty(t, rest.NextSpan)
}

// TestResolveSpanFullCursorCoversTheRestOfTheObject pins full's cursor on an object more than twice
// the cap: it names the rest of the object, as an explicit span's cursor names the rest of its
// request, so the two page alike and following it keeps pages response-sized rather than capping
// the cursor at one response.
func TestResolveSpanFullCursorCoversTheRestOfTheObject(t *testing.T) {
	b := []byte(strings.Repeat("0123456789", 200))
	root := store.Root{Chunks: []core.ChunkRef{{Len: 700}, {Len: 700}, {Len: 600}}, CanonBytes: int64(len(b))}
	o := SpanOpts{Full: true, MaxSpan: 700, MaxResponse: 512, ExactStart: true, RuneSafe: true}

	res, err := ResolveSpanFromBytes(b, root, nil, o)
	require.NoError(t, err)
	require.Equal(t, "512:1488", res.NextSpan)

	o.Full, o.Explicit = false, res.NextSpan
	next, err := ResolveSpanFromBytes(b, root, nil, o)
	require.NoError(t, err)
	require.Equal(t, [2]int64{512, 1024}, [2]int64{next.Off, next.End}, "the next page is response-sized")
	require.True(t, next.Truncated)
	require.Equal(t, "1024:976", next.NextSpan)
}

// TestResolveSpanExplicitHugeLengthReadsToTheEnd pins that an explicit length near the int64 maximum
// asks for the rest of the object. The end used to be computed as offset plus length, which wrapped
// negative for any offset past 0 and resolved to the first chunk's end: a caller asking for
// everything from 1 on was served 299 bytes with a chunk.max-sized cursor.
func TestResolveSpanExplicitHugeLengthReadsToTheEnd(t *testing.T) {
	b := []byte(strings.Repeat("0123456789", 100))
	root := store.Root{Chunks: []core.ChunkRef{{Len: 300}, {Len: 300}, {Len: 400}}, CanonBytes: int64(len(b))}
	o := SpanOpts{Explicit: "1:9223372036854775807", MaxSpan: 300, MaxResponse: 512, ExactStart: true, RuneSafe: true}

	res, err := ResolveSpanFromBytes(b, root, nil, o)
	require.NoError(t, err)
	require.Equal(t, [2]int64{1, 513}, [2]int64{res.Off, res.End}, "a response-sized page from the offset")
	require.Equal(t, "513:487", res.NextSpan, "the cursor covers the rest of the object")
}
