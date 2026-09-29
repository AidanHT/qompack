package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// runtime.mcp.maxResponseBytes bounds what a content tool RETURNS — the result text the model
// receives, which is the JSON body with its content string escaped — and not merely the span of
// content inside it (V6 close-out, live report install D2 / UAT-12 (a)). The live lane measured
// expand and re_read with full:true returning 263,559 and 263,567 bytes of text against 262,144,
// and escape-heavy content overshooting by far more: a 2,134-byte capture became 2,948 bytes.

// boundTinyResponse is the smallest runtime.mcp.maxResponseBytes the configuration accepts, so the
// escape-heavy object below spans many pages.
const boundTinyResponse = 4096

// boundEscapeLine is one line of content whose JSON encoding is far larger than its bytes: quotes and
// backslashes double, control characters become six-byte \u00XX escapes, and U+2028 is escaped too.
const boundEscapeLine = "\"q\" \\p\\ \t\x01\x02\x1f <&> \u2028 é 日本 \"end\"\n"

// boundLiveTotal is the size of the live lane's big.log capture (UAT-12 cli/20-mcp-probe.json).
const boundLiveTotal = 324902

// boundEscapeHeavy returns n bytes of boundEscapeLine repeated, cut on a line boundary.
func boundEscapeHeavy(n int) string {
	s := strings.Repeat(boundEscapeLine, n/len(boundEscapeLine)+1)
	return s[:strings.LastIndex(s[:n], "\n")+1]
}

// boundLogLike returns n bytes shaped like the live big.log: log lines with a quoted field.
func boundLogLike(n int) string {
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(`2026-09-29T13:26:10Z INFO request path="/api/v1/items" status=200 took="12ms"` + "\n")
	}
	return b.String()[:n]
}

// storedBytes reads an object's whole canonical content back from the store, which is what paging
// must reproduce exactly once.
func storedBytes(t *testing.T, f *fixture, h core.Hash) string {
	t.Helper()
	root := spanRootOf(t, f, h)
	rc, err := f.Store.OpenSpan(t.Context(), h, 0, chunkTotal(root))
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	return string(b)
}

// decodeInto decodes a content tool's result text.
func decodeInto(t *testing.T, text string, v any) {
	t.Helper()
	require.NoError(t, json.Unmarshal([]byte(text), v), "the result text is not JSON: %.200s", text)
}

// pageToEnd calls tool with args, then follows next_span until the object is exhausted, asserting on
// every page that the result TEXT fits limit, that the reported span is the content returned, that
// pages are contiguous, and that a cut page always carries a cursor. It returns the concatenation.
func pageToEnd(t *testing.T, f *fixture, tool string, args map[string]any, limit int) (string, int) {
	t.Helper()
	return pageToEndFrom(t, f, tool, args, limit, 0)
}

// pageToEndFrom is pageToEnd for a walk whose first page must start at from.
func pageToEndFrom(t *testing.T, f *fixture, tool string, args map[string]any, limit int, from int64) (string, int) {
	t.Helper()
	var all strings.Builder
	prevEnd := from
	pages := 0
	for {
		resp := f.call(t, tool, args)
		require.False(t, resp.IsError, "%s page %d: %s", tool, pages, responseText(resp))
		text := responseText(resp)
		require.LessOrEqual(t, len(text), limit,
			"%s page %d: the result text is %d bytes against runtime.mcp.maxResponseBytes %d",
			tool, pages, len(text), limit)

		var body contentBody
		decodeInto(t, text, &body)
		require.True(t, body.Found)
		require.Equal(t, prevEnd, body.Span[0], "%s page %d: pages must be contiguous", tool, pages)
		require.EqualValues(t, len(body.Content), body.Span[1]-body.Span[0],
			"%s page %d: the content must be exactly the reported span", tool, pages)
		require.Greater(t, body.Span[1], body.Span[0], "%s page %d: every page must make progress", tool, pages)
		require.NotContains(t, body.Content, string(utf8.RuneError),
			"%s page %d: a page split a multi-byte rune", tool, pages)
		all.WriteString(body.Content)
		prevEnd = body.Span[1]
		pages++

		if body.Span[1] == body.TotalBytes {
			require.Empty(t, body.NextSpan, "the last page carries no cursor")
			return all.String(), pages
		}
		require.True(t, body.Truncated, "%s page %d: a cut page must say it was truncated", tool, pages)
		require.NotEmpty(t, body.NextSpan, "%s page %d: a cut page must carry next_span", tool, pages)
		next := map[string]any{"span": body.NextSpan}
		for k, v := range args {
			if k != "full" && k != "span" {
				next[k] = v
			}
		}
		args = next
	}
}

// TestExpandFullResponseTextFitsMaxResponseBytes_EscapeHeavy is the D2 regression: with full:true
// on escape-heavy content, the result text — not the content span — stays within the bound, and the
// continuation reproduces the object exactly once.
func TestExpandFullResponseTextFitsMaxResponseBytes_EscapeHeavy(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.Config) { c.Runtime.MCP.MaxResponseBytes = boundTinyResponse }))
	h, id := f.putAndRecord(t, "Read", "data/escapes.txt", boundEscapeHeavy(6*boundTinyResponse), 1)
	want := storedBytes(t, f, h)

	got, pages := pageToEnd(t, f, ToolExpand, map[string]any{"tool_use_id": string(id), "full": true},
		boundTinyResponse)
	require.Equal(t, want, got, "following next_span must reproduce the stored object exactly once")
	require.Greater(t, pages, 1, "the object cannot fit one bounded response")
}

// TestReReadFullResponseTextFitsMaxResponseBytes_EscapeHeavy is the same bound through re_read.
func TestReReadFullResponseTextFitsMaxResponseBytes_EscapeHeavy(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.Config) { c.Runtime.MCP.MaxResponseBytes = boundTinyResponse }))
	h, _ := f.putAndRecord(t, "Read", "data/escapes.txt", boundEscapeHeavy(6*boundTinyResponse), 1)
	want := storedBytes(t, f, h)

	first := f.call(t, ToolReRead, map[string]any{"path": "data/escapes.txt", "full": true})
	require.False(t, first.IsError, responseText(first))
	require.LessOrEqual(t, len(responseText(first)), boundTinyResponse,
		"re_read full: the result text is %d bytes against %d", len(responseText(first)), boundTinyResponse)
	var body contentBody
	decodeInto(t, responseText(first), &body)
	require.True(t, body.Truncated)
	require.NotEmpty(t, body.NextSpan)
	require.Equal(t, want[:body.Span[1]], body.Content, "the first page is the object's prefix")

	// re_read takes no span, so its continuation is expand by the hash it resolved.
	rest, _ := pageToEndFrom(t, f, ToolExpand, map[string]any{"hash": body.Hash, "span": body.NextSpan},
		boundTinyResponse, body.Span[1])
	require.Equal(t, want, body.Content+rest, "re_read's page and expand's continuation are the object exactly once")
}

// TestExpandMinimalSpanResponseTextFitsMaxResponseBytes covers the default (minimal) span, whose
// window is bounded by store.chunk.max and can still escape past a small response bound.
func TestExpandMinimalSpanResponseTextFitsMaxResponseBytes(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.Config) { c.Runtime.MCP.MaxResponseBytes = boundTinyResponse }))
	h, id := f.putAndRecord(t, "Read", "data/escapes.txt", boundEscapeHeavy(6*boundTinyResponse), 1)
	want := storedBytes(t, f, h)

	got, _ := pageToEnd(t, f, ToolExpand, map[string]any{"tool_use_id": string(id)}, boundTinyResponse)
	require.Equal(t, want, got, "following next_span must reproduce the stored object exactly once")
}

// TestExpandFullAtTheDefaultBoundFitsTheLiveOvershoot reproduces the live measurement at the shipped
// default: a 324,902-byte log-like capture expanded with full:true.
func TestExpandFullAtTheDefaultBoundFitsTheLiveOvershoot(t *testing.T) {
	f := newFixture(t)
	limit := f.Cfg.Runtime.MCP.MaxResponseBytes
	require.Equal(t, config.Defaults().Runtime.MCP.MaxResponseBytes, limit)
	h, id := f.putAndRecord(t, "Read", "big.log", boundLogLike(boundLiveTotal), 1)
	want := storedBytes(t, f, h)

	got, pages := pageToEnd(t, f, ToolExpand, map[string]any{"tool_use_id": string(id), "full": true}, limit)
	require.Equal(t, want, got)
	require.Equal(t, 2, pages, "a 324,902-byte object pages in two bounded full responses")

	rr := f.call(t, ToolReRead, map[string]any{"path": "big.log", "full": true})
	require.False(t, rr.IsError, responseText(rr))
	require.LessOrEqual(t, len(responseText(rr)), limit, "re_read full at the default bound")
}

// TestExpandFollowsNextSpanWhenDefaultSpanIsFull: with retrieval.defaultSpan at "full", every call
// used to be a full read from offset 0 whatever `span` said, so following next_span handed back the
// first page for ever. An explicit span wins over full.
func TestExpandFollowsNextSpanWhenDefaultSpanIsFull(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.Config) {
		c.Runtime.MCP.MaxResponseBytes = boundTinyResponse
		c.Retrieval.DefaultSpan = "full"
	}))
	h, id := f.putAndRecord(t, "Read", "data/escapes.txt", boundEscapeHeavy(6*boundTinyResponse), 1)
	want := storedBytes(t, f, h)

	got, _ := pageToEnd(t, f, ToolExpand, map[string]any{"tool_use_id": string(id)}, boundTinyResponse)
	require.Equal(t, want, got, "following next_span must reproduce the stored object exactly once")
}

// TestExpandPagesNeverSplitARune pins that no page ends or starts inside a multi-byte UTF-8
// sequence: a split rune reaches the model as U+FFFD on both sides of the cut, a character the
// object does not contain.
func TestExpandPagesNeverSplitARune(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.Config) { c.Runtime.MCP.MaxResponseBytes = boundTinyResponse }))
	h, id := f.putAndRecord(t, "Read", "data/cjk.txt", boundCJK(5*boundTinyResponse), 1)
	want := storedBytes(t, f, h)

	got, pages := pageToEnd(t, f, ToolExpand, map[string]any{"tool_use_id": string(id), "full": true},
		boundTinyResponse)
	require.Equal(t, want, got)
	require.Greater(t, pages, 1)
}

// boundCJK returns about n bytes of three-byte runes, with a newline every forty.
func boundCJK(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		b.WriteString("日本語")
		if i%40 == 39 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// TestResolveSpanExactStartBeginsAtTheNamedOffset pins SpanOpts.ExactStart: an explicit byte span
// is aligned back to its chunk's start by default, and starts exactly at its offset when set.
func TestResolveSpanExactStartBeginsAtTheNamedOffset(t *testing.T) {
	b := []byte(strings.Repeat("abcdefgh", 64))
	root := store.Root{Chunks: []core.ChunkRef{{Len: 256}, {Len: 256}}, CanonBytes: int64(len(b))}
	o := SpanOpts{Explicit: "100:10", MaxSpan: 256, MaxResponse: boundTinyResponse}

	aligned, err := ResolveSpanFromBytes(b, root, nil, o)
	require.NoError(t, err)
	require.Equal(t, int64(0), aligned.Off, "the default aligns an explicit span outward")

	o.ExactStart = true
	exact, err := ResolveSpanFromBytes(b, root, nil, o)
	require.NoError(t, err)
	require.Equal(t, int64(100), exact.Off)
	require.Equal(t, int64(256), exact.End, "the end is still chosen as for any window")
	require.Equal(t, b[100:256], exact.Body)
	require.Equal(t, "256:256", exact.NextSpan)
}

// TestResolveSpanRuneSafeNeverEndsInsideARune pins SpanOpts.RuneSafe: a chunk boundary inside a
// three-byte rune is extended to finish the rune when the budget allows, and moved back to the
// rune's start when it does not.
func TestResolveSpanRuneSafeNeverEndsInsideARune(t *testing.T) {
	b := []byte(strings.Repeat("日", 100))
	root := store.Root{Chunks: []core.ChunkRef{{Len: 100}, {Len: 200}}, CanonBytes: int64(len(b))}

	res, err := ResolveSpanFromBytes(b, root, nil, SpanOpts{MaxSpan: 100, MaxResponse: boundTinyResponse})
	require.NoError(t, err)
	require.Equal(t, int64(100), res.End, "without RuneSafe the window ends on the chunk boundary")

	res, err = ResolveSpanFromBytes(b, root, nil,
		SpanOpts{MaxSpan: 100, MaxResponse: boundTinyResponse, RuneSafe: true})
	require.NoError(t, err)
	require.Equal(t, int64(102), res.End, "the split rune is finished inside the budget")
	require.True(t, utf8.Valid(res.Body))
	require.Equal(t, "102:100", res.NextSpan)

	res, err = ResolveSpanFromBytes(b, root, nil, SpanOpts{Full: true, MaxSpan: 100, MaxResponse: 100, RuneSafe: true})
	require.NoError(t, err)
	require.Equal(t, int64(99), res.End, "past the budget the cut moves back to the rune's start")
	require.True(t, utf8.Valid(res.Body))
}

// boundPEMHeader and boundPEMFooter are the private-key block delimiters the pem_private_key rule
// matches. They are split across + so no contiguous credential shape exists in the repository (the
// secret-fixture convention); the body between them is not a key.
const (
	boundPEMHeader = "-----BEGIN PRIV" + "ATE KEY-----\n"
	boundPEMFooter = "-----END PRIV" + "ATE KEY-----\n"
	// boundPEMLeak opens every line of the block's body (boundPEMBlock): it is what a page would
	// carry if any part of the body were served in the clear.
	boundPEMLeak = "Zm9vYmFy"
	// boundPEMLines is how many body lines the block carries: about 250 bytes with its delimiters,
	// so the whole block fits in a window even when most of the window's text is spent before it.
	boundPEMLines = 3
	// boundPEMAt is where the block starts in the object, after that much prose.
	boundPEMAt = 8000
	// boundPEMTail is the escape-heavy content after the block.
	boundPEMTail = 4000
	// boundPEMLeadFrom and boundPEMLeadTo bound how much prose each window carries before the
	// block. Across that range the text spent before the block reaches the bound, which is where
	// the cut is forced back into the block: pre + envelope + the redaction placeholder is over the
	// bound, but pre + envelope + a few raw bytes of the block is not. Before the fix, leads
	// 3,660-3,684 served the block's delimiter and body in the clear; the range keeps a margin on
	// both sides of that window, since the envelope's width moves it.
	boundPEMLeadFrom = 3600
	boundPEMLeadTo   = 3760
	// boundPEMChunkMin, boundPEMChunkTarget and boundPEMChunkMax make the object one chunk, so no
	// chunk start lies inside a window and the cut stays where the escape charge put it.
	boundPEMChunkMin    = 16 * 1024
	boundPEMChunkTarget = 32 * 1024
	boundPEMChunkMax    = 64 * 1024
)

// boundProse returns n bytes of plain prose lines, which the store keeps byte for byte (log lines
// are not: their timestamps are canonicalized, which moves every offset after them).
func boundProse(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "line %d of the notes: the build \"passed\" on the second attempt\n", i)
	}
	return b.String()[:n]
}

// boundPEMBlock returns a private-key-shaped block whose body lines differ.
func boundPEMBlock() string {
	const lineMix = 0x9e3779b97f4a7c15 // a 64-bit odd constant that spreads i across the hex digits
	var b strings.Builder
	b.WriteString(boundPEMHeader)
	for i := range boundPEMLines {
		fmt.Fprintf(&b, "%s%056x\n", boundPEMLeak, uint64(i+1)*lineMix)
	}
	b.WriteString(boundPEMFooter)
	return b.String()
}

// TestExpandBoundCutNeverSplitsARedactedRegion pins that the response bound's cut never lands
// inside a region the retrieval redactor replaced (V6 close-out, w13-mcpresp review). Re-redacting
// the kept part alone does not see the whole secret: a cut inside a private-key block leaves BEGIN
// with no END, the rule no longer matches, and the start of the block is served; the next page,
// which starts exactly at the cut, has END with no BEGIN, and serves the rest of the block.
//
// The store is written with capture-time redaction off (a record that predates the rule), so the
// key is in the archive in the clear and only retrieval redaction stands between it and the model.
// Each window starts boundPEMLeadFrom..boundPEMLeadTo bytes before the block (an explicit span,
// which starts exactly at its offset) and is followed with next_span until it is past the block.
func TestExpandBoundCutNeverSplitsARedactedRegion(t *testing.T) {
	f := newFixture(t, withCaptureRedactionDisabled(), withConfig(func(c *config.Config) {
		c.Runtime.MCP.MaxResponseBytes = boundTinyResponse
		c.Store.Chunk.Min, c.Store.Chunk.Target, c.Store.Chunk.Max = boundPEMChunkMin, boundPEMChunkTarget, boundPEMChunkMax
	}))
	block := boundPEMBlock()
	body := boundProse(boundPEMAt) + block + boundEscapeHeavy(boundPEMTail)
	h, _ := f.putAndRecord(t, "Read", "keys/notes.txt", body, 1)
	require.Equal(t, body, storedBytes(t, f, h), "the store must keep the object byte for byte")
	require.Len(t, spanRootOf(t, f, h).Chunks, 1, "the object must be one chunk")
	blockEnd := int64(boundPEMAt + len(block))

	for lead := boundPEMLeadFrom; lead < boundPEMLeadTo; lead++ {
		start := int64(boundPEMAt - lead)
		args := map[string]any{"hash": h.String(), "span": fmt.Sprintf("%d:%d", start, boundTinyResponse)}
		for page := 0; ; page++ {
			resp := f.call(t, ToolExpand, args)
			require.False(t, resp.IsError, "lead %d page %d: %s", lead, page, responseText(resp))
			text := responseText(resp)
			require.LessOrEqual(t, len(text), boundTinyResponse, "lead %d page %d", lead, page)
			var got contentBody
			decodeInto(t, text, &got)
			require.Equal(t, start, got.Span[0], "lead %d page %d: pages must be contiguous", lead, page)
			require.Greater(t, got.Span[1], got.Span[0], "lead %d page %d: every page must make progress", lead, page)
			require.NotContains(t, got.Content, "PRIV"+"ATE KEY-----",
				"lead %d page %d %v: a private-key delimiter was served in the clear", lead, page, got.Span)
			require.NotContains(t, got.Content, "-----BEGIN",
				"lead %d page %d %v: part of a private-key delimiter was served in the clear", lead, page, got.Span)
			require.NotContains(t, got.Content, boundPEMLeak,
				"lead %d page %d %v: part of the private-key body was served in the clear", lead, page, got.Span)
			if got.Span[1] > blockEnd || got.Span[1] == got.TotalBytes {
				break
			}
			require.NotEmpty(t, got.NextSpan, "lead %d page %d", lead, page)
			start = got.Span[1]
			args = map[string]any{"hash": h.String(), "span": got.NextSpan}
		}
	}
}

// TestShrinkSpanChargeDoublesEachRound pins the convergence rule of the bound's cut loop: the
// escape charge is exact for raw content but not for redacted content, so each further round
// doubles it, and a charge that would consume the window keeps its first rune instead. A response
// therefore needs a logarithmic number of rounds however much of it the redactor compresses.
func TestShrinkSpanChargeDoublesEachRound(t *testing.T) {
	const size, excess = 1000, 10
	s := SpanResult{Off: 0, End: size, Total: size, Body: []byte(strings.Repeat("a", size))}
	o := SpanOpts{Full: true, MaxSpan: size, MaxResponse: size}

	for round, want := range map[int]int64{0: size - excess, 1: size - 2*excess, 3: size - 8*excess} {
		got, ok := shrinkSpan(s, []int64{0}, excess, round, o)
		require.True(t, ok, "round %d", round)
		require.Equal(t, want, got.End, "round %d", round)
	}

	got, ok := shrinkSpan(s, []int64{0}, excess, 20, o)
	require.True(t, ok)
	require.Equal(t, int64(1), got.End, "a charge past the whole window keeps the first rune")

	one := SpanResult{Off: 0, End: 3, Total: 3, Body: []byte("日")}
	_, ok = shrinkSpan(one, []int64{0}, excess, 5, o)
	require.False(t, ok, "a window already one rune long has nothing left to cut")
	_, ok = shrinkSpan(s, []int64{0}, size+1, 0, o)
	require.False(t, ok, "a first-round excess past the whole window leaves nothing")
}
