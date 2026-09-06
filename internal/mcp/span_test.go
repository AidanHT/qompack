package mcp

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// The minimum-sufficient-span resolver's own tests (00-ARCHITECTURE.md §5.16 "Span default",
// Qompack.md §8.7).
//
// ResolveSpan is exercised against a REAL store rather than a synthesized chunk list, because the
// whole claim of §5.16 is that its windows land on boundaries the store itself drew: a hand-written
// []ChunkRef would let the resolver be wrong in precisely the way the alignment invariant exists to
// prevent, and every assertion would still pass.
//
// The arithmetic edges a real project cannot produce — a root with no bytes, a caller who named no
// response budget, a twenty-digit span offset — go through resolveSpan over memReader instead.
// span.go's header calls the resolver "a pure function of its arguments"; those are the arguments no
// project on disk ever hands it.

// spanAuthPath is where the 200 000-byte fixture source is stored, in paths.Key form.
const spanAuthPath = "src/auth.ts"

// The widening fixture's shape. Long lines up to spanWidenHead push FastCDC onto store.chunk.max, so
// the minimal window always cuts at 16 384; the short lines after it make the tail between that cut
// and the closing brace about thirty LINES rather than thirty bytes — which is the budget widenTail
// actually spends, and the only budget that separates the two widening tests.
const (
	spanWidenBytes = 20000
	spanWidenHead  = 15000
	spanWidenLong  = 1000
	spanWidenShort = 100
	spanWidenPath  = "src/widen.ts"
)

// spanRefusedWidenLines is the tightened runtime.mcp.spanWidenLines the refusal test resolves under.
const spanRefusedWidenLines = 2

// spanSmallBytes is the single-chunk object: below store.chunk.min, so FastCDC cannot split it and the
// resolver's chunk walk has exactly one chunk to take.
const spanSmallBytes = 900

// spanPropBytes caps a drawn object. Every draw runs the real FastCDC chunker plus a MinHash pass, so
// the cap is what keeps rapid's default hundred checks inside a few seconds; the shapes that matter —
// one chunk, a chunk short of the span cap, several pages — all live well below it.
const spanPropBytes = 48 << 10

// spanPropLine is how often the drawn content breaks a line, so a line anchor has lines to find rather
// than one 48 KiB run.
const spanPropLine = 64

// defaultSpanOpts is what handlers.spanOptsFor builds for a call carrying no explicit span: the three
// bounds read from configuration, and nothing else.
//
// The bounds are respelled here rather than taken from the unexported method so these tests stay
// statements about ResolveSpan rather than about its caller — handlers_span_test.go owns the separate
// claim that the caller passes the right ones.
func defaultSpanOpts(cfg config.Config) SpanOpts {
	return SpanOpts{
		MaxSpan:     cfg.Store.Chunk.Max,
		MaxResponse: cfg.Runtime.MCP.MaxResponseBytes,
		WidenLines:  cfg.Runtime.MCP.SpanWidenLines,
	}
}

// spanRootOf resolves a stored object's root. ResolveSpan takes a store.Root, not a hash, so every
// fixture here makes the same round trip the handlers make.
func spanRootOf(t *testing.T, f *fixture, h core.Hash) store.Root {
	t.Helper()
	root, err := f.Store.GetRoot(t.Context(), h)
	require.NoError(t, err, "GetRoot(%s)", h)
	return root
}

// spanAuthObject stores the seeded 200 000-byte TypeScript source as one recorded Read and returns the
// fixture, the exact text, the resolved root and the tool_use_id addressing it.
//
// It is newFixture rather than newSeededFixture on purpose. These tests need ONE object whose symbol
// offsets are known exactly; the forty-tool-use corpus costs about a second per fixture to build
// content none of them reads, and a second per test is the difference between a suite people run and a
// suite people avoid.
func spanAuthObject(t *testing.T) (*fixture, string, store.Root, core.ToolUseID) {
	t.Helper()
	f := newFixture(t)
	text := buildAuthTS(t)
	h, id := f.putAndRecord(t, "Read", spanAuthPath, text, 1)
	return f, text, spanRootOf(t, f, h), id
}

// requireSpanAligned asserts v is a chunk start or the object's end.
//
// Alignment is not cosmetic. span.go's header records why: a next-span that started mid-chunk would be
// aligned BACKWARDS by the following call and hand the model the same bytes twice.
func requireSpanAligned(t *testing.T, starts []int64, total, v int64, what string) {
	t.Helper()
	if v == total {
		return
	}
	require.Contains(t, starts, v, "%s (%d) must be a chunk start or the object's end (%d)", what, v, total)
}

// spanWidenSource generates exactly n bytes of source whose last two are a function's closing brace,
// with headLine-wide lines up to headBytes and tailLine-wide ones after it.
func spanWidenSource(n, headBytes, headLine, tailLine int) string {
	var b strings.Builder
	b.Grow(n)
	b.WriteString("export function widenMe(): void {\n")
	for i := 0; b.Len() < n-len("}\n"); i++ {
		width := headLine
		if b.Len() >= headBytes {
			width = tailLine
		}
		line := fmt.Sprintf("  // span %d %s\n", i, strings.Repeat(".", width))
		// The remainder is filled WITHOUT a newline: a trimmed final line would add a line to the
		// tail's count, which is the quantity the two widening tests are calibrated against.
		if rem := n - len("}\n") - b.Len(); rem < len(line) {
			line = strings.Repeat(".", rem)
		}
		b.WriteString(line)
	}
	b.WriteString("}\n")
	return b.String()
}

// spanWidenObject stores the widening fixture and asserts the two invariants the widening tests rest
// on, so a drifted generator fails here — where the failure names the generator — rather than as a
// mystery two assertions later.
func spanWidenObject(t *testing.T) (*fixture, string, store.Root) {
	t.Helper()
	f := newFixture(t)
	text := spanWidenSource(spanWidenBytes, spanWidenHead, spanWidenLong, spanWidenShort)
	require.Len(t, text, spanWidenBytes, "the widening fixture must be exactly spanWidenBytes")
	require.True(t, strings.HasSuffix(text, "}\n"), "the widening fixture must end on a closing brace")
	return f, text, spanRootOf(t, f, f.put(t, spanWidenPath, text))
}

// spanDrawText generates n deterministic pseudo-random ASCII bytes from seed.
//
// The content is random rather than repetitive so FastCDC finds content-defined boundaries instead of
// falling back to store.chunk.max on every chunk, which would test one chunk shape a hundred times.
func spanDrawText(seed uint64, n int) []byte {
	r := rand.New(rand.NewSource(int64(seed)))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + r.Intn(26))
		if i%spanPropLine == spanPropLine-1 {
			b[i] = '\n'
		}
	}
	return b
}

// TestMinimalSpanIsChunkAligned pins the first normative property: a minimal span begins and ends on a
// boundary the store itself drew, or on the object's end.
//
// Widening is left ON, with the path the widener needs to run at all, so the assertion covers the
// resolver's whole default path rather than the chunk walk alone: widenTail aligns its own result
// outward, and a widening that forgot to would break paging without breaking any other test here.
func TestMinimalSpanIsChunkAligned(t *testing.T) {
	f, _, root, _ := spanAuthObject(t)
	starts, total := boundaries(root)

	o := defaultSpanOpts(f.Cfg)
	o.Path = spanAuthPath
	res, err := ResolveSpan(t.Context(), f.Store, root, f.Widen, o)
	require.NoError(t, err, "ResolveSpan")

	requireSpanAligned(t, starts, total, res.Off, "Off")
	requireSpanAligned(t, starts, total, res.End, "End")
	require.Greater(t, res.End, res.Off, "a non-empty object must resolve to a non-empty span")
	require.Equal(t, int64(authTotalBytes), res.Total, "Total must be the object's canonical size")
}

// TestMinimalSpanNeverExceedsChunkMax pins the cap the "minimum sufficient" default exists to enforce:
// the chunk walk stops at store.chunk.max, and only a single chunk larger than that may exceed it.
//
// Path is deliberately empty. Step 6 is entitled to grow the tail past MaxSpan when a symbol needs it —
// that is what "sufficient" means — so asserting the cap with a widener wired in would be asserting the
// wrong invariant.
func TestMinimalSpanNeverExceedsChunkMax(t *testing.T) {
	f, _, root, _ := spanAuthObject(t)
	starts, _ := boundaries(root)

	o := defaultSpanOpts(f.Cfg)
	res, err := ResolveSpan(t.Context(), f.Store, root, f.Widen, o)
	require.NoError(t, err, "ResolveSpan")

	require.LessOrEqual(t, res.End-res.Off, maxInt64(int64(o.MaxSpan), starts[1]-starts[0]),
		"a minimal span may exceed store.chunk.max only far enough to take one whole chunk")
	require.Zero(t, f.Widen.WidenCalls, "a pathless, anchorless span must not consult the widener at all")
}

// TestSingleChunkObjectReturnsWholeChunk covers the degenerate object: smaller than store.chunk.min, so
// FastCDC cannot split it and the chunk walk has exactly one chunk to take.
//
// Truncated and NextSpan are the assertions that matter. A resolver that reported "truncated" for an
// object it returned in full would teach the model to page through content it already has.
func TestSingleChunkObjectReturnsWholeChunk(t *testing.T) {
	f := newFixture(t)
	text := buildFiller("small", spanSmallBytes)
	root := spanRootOf(t, f, f.put(t, "src/small.ts", text))
	require.Len(t, root.Chunks, 1, "an object below store.chunk.min must be exactly one chunk")

	res, err := ResolveSpan(t.Context(), f.Store, root, f.Widen, defaultSpanOpts(f.Cfg))
	require.NoError(t, err, "ResolveSpan")

	require.Equal(t, int64(0), res.Off, "Off")
	require.Equal(t, int64(spanSmallBytes), res.End, "End")
	require.False(t, res.Truncated, "an object returned whole is not truncated")
	require.Empty(t, res.NextSpan, "there is nothing left to page to")
	require.Equal(t, text, string(res.Body), "the body must be the whole object")
}

// TestFullReturnsWholeObjectUntilResponseCap covers full=true, §8.7's escape hatch, on an object that
// fits inside runtime.mcp.maxResponseBytes.
func TestFullReturnsWholeObjectUntilResponseCap(t *testing.T) {
	f, text, root, _ := spanAuthObject(t)

	o := defaultSpanOpts(f.Cfg)
	o.Full = true
	res, err := ResolveSpan(t.Context(), f.Store, root, f.Widen, o)
	require.NoError(t, err, "ResolveSpan")

	require.Equal(t, int64(0), res.Off, "a full read starts at zero")
	require.Equal(t, int64(authTotalBytes), res.End, "a full read that fits must return the whole object")
	require.False(t, res.Truncated, "nothing was withheld")
	require.Empty(t, res.NextSpan, "there is nothing left to page to")
	require.Equal(t, text, string(res.Body), "the body must be the object byte for byte")
}

// TestFullTruncatesAtMaxResponseBytes pins that "full" means "do not narrow to the matching hunk", never
// "unbounded": an object larger than runtime.mcp.maxResponseBytes still comes back capped, with a cursor
// to the exact remainder.
//
// The two numbers are exact rather than approximate, which is what makes this a statement about the cap:
// 400 000 bytes minus a 262 144-byte first page leaves 137 856, and any other value means the cursor and
// the cap disagree about where the page ended.
func TestFullTruncatesAtMaxResponseBytes(t *testing.T) {
	f := newFixture(t)
	root := spanRootOf(t, f, f.put(t, "src/huge.txt", buildFiller("huge", hugeObjectBytes)))

	o := defaultSpanOpts(f.Cfg)
	o.Full = true
	res, err := ResolveSpan(t.Context(), f.Store, root, f.Widen, o)
	require.NoError(t, err, "ResolveSpan")

	require.Equal(t, int64(f.Cfg.Runtime.MCP.MaxResponseBytes), res.End, "End must land on the response cap")
	require.True(t, res.Truncated, "an object larger than the cap is truncated")
	require.Equal(t, "262144:137856", res.NextSpan, "NextSpan must name the exact remainder")
	require.Len(t, res.Body, f.Cfg.Runtime.MCP.MaxResponseBytes, "the body must be exactly one capped page")
}

// TestExplicitByteSpanAlignsOutward covers step 2: a caller-supplied "<off>:<len>" is honoured by
// CONTAINING it, not by returning it. Aligning outward is what keeps the paging property true — a window
// returned verbatim would start mid-chunk, and the next call would align it backwards.
func TestExplicitByteSpanAlignsOutward(t *testing.T) {
	f, _, root, _ := spanAuthObject(t)
	starts, total := boundaries(root)

	o := defaultSpanOpts(f.Cfg)
	o.Explicit = "18300:100"
	res, err := ResolveSpan(t.Context(), f.Store, root, f.Widen, o)
	require.NoError(t, err, "ResolveSpan")

	require.LessOrEqual(t, res.Off, int64(18300), "Off must not start after the requested span")
	require.GreaterOrEqual(t, res.End, int64(18400), "End must not stop before the requested span")
	requireSpanAligned(t, starts, total, res.Off, "Off")
	requireSpanAligned(t, starts, total, res.End, "End")
}

// TestExplicitLineSpan covers step 3: "L<a>-L<b>" is resolved in the object's own text, then aligned
// outward like any other window.
//
// The expected offsets are computed by splitting the fixture rather than by calling lineStartOffset,
// because a test that computed its expectation with the function under test would agree with an
// off-by-one in it.
func TestExplicitLineSpan(t *testing.T) {
	f, text, root, _ := spanAuthObject(t)
	starts, total := boundaries(root)

	lines := strings.SplitAfter(text, "\n")
	want := strings.Join(lines[9:20], "")
	lineOff := int64(len(strings.Join(lines[:9], "")))
	lineEnd := lineOff + int64(len(want))

	o := defaultSpanOpts(f.Cfg)
	o.Explicit = "L10-L20"
	res, err := ResolveSpan(t.Context(), f.Store, root, f.Widen, o)
	require.NoError(t, err, "ResolveSpan")

	require.LessOrEqual(t, res.Off, lineOff, "Off must not start after line 10")
	require.GreaterOrEqual(t, res.End, lineEnd, "End must not stop before line 20's newline")
	requireSpanAligned(t, starts, total, res.Off, "Off")
	requireSpanAligned(t, starts, total, res.End, "End")
	require.Equal(t, want, string(res.Body[lineOff-res.Off:lineEnd-res.Off]), "lines 10 through 20 inclusive")
}

// TestExplicitSpanBeyondEndClamps covers a span that starts past the object: it resolves to the last
// chunk rather than to an empty window, because an empty answer to "show me the end" reads as "there is
// nothing there".
func TestExplicitSpanBeyondEndClamps(t *testing.T) {
	f, _, root, _ := spanAuthObject(t)
	starts, total := boundaries(root)

	o := defaultSpanOpts(f.Cfg)
	o.Explicit = "999999:100"
	res, err := ResolveSpan(t.Context(), f.Store, root, f.Widen, o)
	require.NoError(t, err, "ResolveSpan")

	require.Equal(t, chunkStartAtOrBefore(starts, total), res.Off, "Off must clamp to the last chunk's start")
	require.Equal(t, total, res.End, "End must clamp to the object's end")
	require.Empty(t, res.NextSpan, "a window that reached the end has nothing to page to")
}

// TestSymbolAnchorSelectsEnclosingFunction covers step 4 plus step 5's hardEnd: an anchor symbol is
// located by the widener, and the window grows past store.chunk.max when — and only when — that is what
// it takes to return the symbol whole.
//
// A function cut in half is not a minimum SUFFICIENT span, which is the entire reason hardEnd exists.
func TestSymbolAnchorSelectsEnclosingFunction(t *testing.T) {
	f, text, root, _ := spanAuthObject(t)

	o := defaultSpanOpts(f.Cfg)
	o.Path = spanAuthPath
	o.AnchorSym = "refreshToken"
	res, err := ResolveSpan(t.Context(), f.Store, root, f.Widen, o)
	require.NoError(t, err, "ResolveSpan")

	require.Positive(t, f.Widen.FindCalls, "the resolver must have asked the widener where the symbol is")
	require.LessOrEqual(t, res.Off, int64(refreshTokenOff), "the window must start at or before the symbol")
	require.GreaterOrEqual(t, res.End, int64(refreshTokenEnd), "the window must reach the symbol's end")
	require.Contains(t, string(res.Body), text[refreshTokenOff:refreshTokenEnd],
		"the body must carry refreshToken whole")
}

// TestSymbolWideningExtendsToFunctionEnd covers step 6: a chunk-aligned cut that lands mid-function is
// extended to the function's end, and says so.
//
// The widener is driven with WidenTo rather than left to scan, because the quantity under test is what
// the RESOLVER does with a widener's answer. WidenTo is relative to the probe's own start — widenTail
// hands the widener a buffer beginning at off — and off is zero here, so the object's end is also the
// symbol's end in probe coordinates.
func TestSymbolWideningExtendsToFunctionEnd(t *testing.T) {
	f, text, root := spanWidenObject(t)
	starts, total := boundaries(root)

	o := defaultSpanOpts(f.Cfg)
	o.Path = spanWidenPath
	base, err := ResolveSpan(t.Context(), f.Store, root, nil, o)
	require.NoError(t, err, "ResolveSpan without a widener")
	require.Less(t, base.End, total, "the unwidened window must stop short of the closing brace")
	require.LessOrEqual(t, strings.Count(text[base.End:], "\n"), o.WidenLines,
		"the fixture's tail must fit inside runtime.mcp.spanWidenLines")

	f.Widen.WidenTo = int64p(total)
	res, err := ResolveSpan(t.Context(), f.Store, root, f.Widen, o)
	require.NoError(t, err, "ResolveSpan with a widener")

	require.True(t, res.Widened, "a tail the widener moved must be reported as widened")
	require.Greater(t, res.End, base.End, "widening must move End forward")
	require.Equal(t, total, res.End, "widening must reach the symbol's end")
	requireSpanAligned(t, starts, total, res.End, "End")
	require.True(t, strings.HasSuffix(string(res.Body), "}\n"), "the widened body must end on the closing brace")
}

// TestSymbolWideningRefusedBeyondSpanWidenLines covers widenTail's line budget: the SAME widener answer
// that is accepted under the default runtime.mcp.spanWidenLines is refused under a tightened one, and
// the refusal leaves End exactly where the chunk walk put it.
//
// Driving both halves off one fixture is what makes this a statement about the budget. A refusal test
// with an object of its own would also pass if widening had simply stopped working.
func TestSymbolWideningRefusedBeyondSpanWidenLines(t *testing.T) {
	f, text, root := spanWidenObject(t)
	_, total := boundaries(root)

	o := defaultSpanOpts(f.Cfg)
	o.Path = spanWidenPath
	base, err := ResolveSpan(t.Context(), f.Store, root, nil, o)
	require.NoError(t, err, "ResolveSpan without a widener")

	o.WidenLines = spanRefusedWidenLines
	require.Greater(t, strings.Count(text[base.End:], "\n"), o.WidenLines,
		"the fixture's tail must exceed the tightened budget for the refusal to mean anything")

	f.Widen.WidenTo = int64p(total)
	res, err := ResolveSpan(t.Context(), f.Store, root, f.Widen, o)
	require.NoError(t, err, "ResolveSpan")

	require.False(t, res.Widened, "a widening past spanWidenLines must be refused")
	require.Equal(t, base.End, res.End, "a refused widening must leave End where the chunk walk put it")
	require.Positive(t, f.Widen.WidenCalls, "the budget is spent on the widener's answer, not instead of asking")
}

// TestNoWidenerIsTolerated covers the two ways the symbol half can be absent — a build with no widener at
// all, and a widener with no symbol table for this dialect. Neither may panic, and both must fall back to
// the plain chunk walk rather than to an empty answer.
//
// This is not a defensive case: §3.2 forbids this package importing symbols, so the production Widener is
// wired in the composition root and every build that has not wired it takes the nil path.
func TestNoWidenerIsTolerated(t *testing.T) {
	f, _, root, _ := spanAuthObject(t)
	starts, total := boundaries(root)

	o := defaultSpanOpts(f.Cfg)
	o.Path = spanAuthPath
	o.AnchorSym = "refreshToken"

	t.Run("a nil widener", func(t *testing.T) {
		res, err := ResolveSpan(t.Context(), f.Store, root, nil, o)
		require.NoError(t, err, "ResolveSpan with a nil widener")
		requireSpanAligned(t, starts, total, res.Off, "Off")
		requireSpanAligned(t, starts, total, res.End, "End")
		require.False(t, res.Widened, "a nil widener cannot widen")
		require.NotEmpty(t, res.Body, "a nil widener must still return a chunk-aligned window")
	})

	t.Run("a widener that finds nothing", func(t *testing.T) {
		f.Widen.Refuse = true
		t.Cleanup(func() { f.Widen.Refuse = false })

		res, err := ResolveSpan(t.Context(), f.Store, root, f.Widen, o)
		require.NoError(t, err, "ResolveSpan with a refusing widener")
		requireSpanAligned(t, starts, total, res.Off, "Off")
		requireSpanAligned(t, starts, total, res.End, "End")
		require.False(t, res.Widened, "a widener that found nothing has not widened")
	})
}

// TestLineAnchor covers step 4's line fallback: re_read's "file.ts:120" suffix arrives as AnchorLine, and
// the window has to CONTAIN that line rather than merely start near it.
func TestLineAnchor(t *testing.T) {
	f, text, root, _ := spanAuthObject(t)
	starts, total := boundaries(root)

	lines := strings.SplitAfter(text, "\n")
	lineOff := int64(len(strings.Join(lines[:399], "")))
	want := strings.TrimSuffix(lines[399], "\n")

	o := defaultSpanOpts(f.Cfg)
	o.Path = spanAuthPath
	o.AnchorLine = 400
	res, err := ResolveSpan(t.Context(), f.Store, root, f.Widen, o)
	require.NoError(t, err, "ResolveSpan")

	require.LessOrEqual(t, res.Off, lineOff, "the window must start at or before line 400")
	require.Greater(t, res.End, lineOff, "the window must reach line 400")
	requireSpanAligned(t, starts, total, res.Off, "Off")
	require.Contains(t, string(res.Body), want, "the body must carry line 400")
}

// TestSpanArithmeticWithoutAProject drives resolveSpan over memReader for the shapes a real store cannot
// produce, and for memReader itself.
//
// memReader is span.go's own adapter for "expand given a chunk hash rather than a root hash". Nothing in
// the package calls it — see handlers_span_test.go's TestExpandAcceptsChunkHash — so this is the only
// place its bounds are checked at all.
func TestSpanArithmeticWithoutAProject(t *testing.T) {
	twoChunks := store.Root{Chunks: []core.ChunkRef{{Len: 10}, {Len: 10}}, CanonBytes: 20}
	body := []byte(strings.Repeat("a", 10) + strings.Repeat("b", 10))

	t.Run("an empty root resolves to an empty span", func(t *testing.T) {
		res, err := resolveSpan(memReader(nil), store.Root{}, nil, SpanOpts{MaxSpan: 8, MaxResponse: 16})
		require.NoError(t, err, "resolveSpan over an empty root")
		require.Equal(t, SpanResult{}, res, "an object with no bytes has no window and no cursor")
	})

	t.Run("a root with no chunk list is one window", func(t *testing.T) {
		res, err := resolveSpan(memReader(body[:5]), store.Root{CanonBytes: 5}, nil,
			SpanOpts{MaxSpan: 8, MaxResponse: 16})
		require.NoError(t, err, "resolveSpan over a chunkless root")
		require.Equal(t, int64(5), res.End, "CanonBytes is the fallback length when the chunk list is empty")
		require.Empty(t, res.NextSpan, "a window that reached the end has nothing to page to")
	})

	t.Run("no response budget falls back to the object", func(t *testing.T) {
		res, err := resolveSpan(memReader(body), twoChunks, nil, SpanOpts{MaxSpan: 10})
		require.NoError(t, err, "resolveSpan with MaxResponse zero")
		require.Equal(t, int64(10), res.End, "the chunk walk still stops at MaxSpan")
		require.Equal(t, "10:10", res.NextSpan, "the remainder is still addressable")
	})

	t.Run("a twenty-digit offset clamps to the object", func(t *testing.T) {
		res, err := resolveSpan(memReader(body), twoChunks, nil,
			SpanOpts{Explicit: "99999999999999999999:5", MaxSpan: 10, MaxResponse: 20})
		require.NoError(t, err, "resolveSpan over an offset longer than an int64")
		require.Equal(t, int64(10), res.Off, "an unparseable offset clamps like any other overlong one")
		require.Equal(t, int64(20), res.End, "End clamps to the object")
	})

	t.Run("line zero is the object's start", func(t *testing.T) {
		res, err := resolveSpan(memReader(body), twoChunks, nil,
			SpanOpts{Explicit: "L0-L0", MaxSpan: 10, MaxResponse: 20})
		require.NoError(t, err, "resolveSpan over a zero line span")
		require.Equal(t, int64(0), res.Off, "there is no line before the first one")
		require.Greater(t, res.End, res.Off, "an empty line range must still resolve to a chunk")
	})

	t.Run("memReader bounds every read to its buffer", func(t *testing.T) {
		read := memReader(body)
		for _, tc := range []struct {
			name     string
			off, n   int64
			wantSize int
		}{
			{"a read inside the buffer", 2, 4, 4},
			{"a read past the buffer's end", 16, 99, 4},
			{"a read starting past the end", 20, 4, 0},
			{"a negative offset", -1, 4, 0},
			{"a non-positive length", 0, 0, 0},
		} {
			got, err := read(tc.off, tc.n)
			require.NoError(t, err, "memReader(%d, %d)", tc.off, tc.n)
			require.Len(t, got, tc.wantSize, tc.name)
		}
	})
}

// TestResolveSpanWithoutAStoreIsAnError pins the one failure ResolveSpan itself reports. A build with no
// store cannot resolve anything, and saying so is a different fact from an empty span.
func TestResolveSpanWithoutAStoreIsAnError(t *testing.T) {
	_, err := ResolveSpan(t.Context(), nil, store.Root{}, nil, SpanOpts{})
	require.Error(t, err, "a nil store must be reported")
	require.Contains(t, err.Error(), "no store", "the message must name what is missing")
}

// TestUnreadableRootIsReportedNotAnsweredEmpty covers every read the resolver makes — the full read, the
// line probe, the anchor probe and the final window — against a root the store cannot open.
//
// The distinction is the point. A store failure is not a small span: returning an empty window for an
// object the store could not read would report "this function is empty" for a corrupt index, which is
// the one answer a model has no way to question.
func TestUnreadableRootIsReportedNotAnsweredEmpty(t *testing.T) {
	f := newFixture(t)
	// A root whose hash was never indexed: its chunk list is well formed, so the resolver's arithmetic
	// runs in full and only the reads fail.
	ghost := store.Root{Chunks: []core.ChunkRef{{Len: 100}}, CanonBytes: 100}

	for name, o := range map[string]SpanOpts{
		"a full read":     {Full: true, MaxSpan: 16, MaxResponse: 64},
		"a minimal span":  {MaxSpan: 16, MaxResponse: 64},
		"a line span":     {Explicit: "L1-L2", MaxSpan: 16, MaxResponse: 64},
		"a symbol anchor": {Path: "src/ghost.ts", AnchorSym: "gone", MaxSpan: 16, MaxResponse: 64},
		"a line anchor":   {Path: "src/ghost.ts", AnchorLine: 2, MaxSpan: 16, MaxResponse: 64},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveSpan(t.Context(), f.Store, ghost, f.Widen, o)
			require.Error(t, err, "an unreadable root must be reported rather than answered")
		})
	}
}

// TestWideningRefusedWhenItCannotBeTrusted covers widenTail's three structural refusals, as distinct
// from its line budget: a probe with no room to look ahead, an answer that does not move End, and an
// answer that points past the buffer the widener was actually given.
//
// The last one is the dangerous case. A Widener is implemented outside this package over
// symbols.Extractor (§3.2), so an offset in the wrong coordinate space is a live possibility, and
// honouring it would slice a buffer out of bounds rather than merely widen too far.
func TestWideningRefusedWhenItCannotBeTrusted(t *testing.T) {
	f, _, root := spanWidenObject(t)
	_, total := boundaries(root)

	base := defaultSpanOpts(f.Cfg)
	base.Path = spanWidenPath
	unwidened, err := ResolveSpan(t.Context(), f.Store, root, nil, base)
	require.NoError(t, err, "ResolveSpan without a widener")

	for name, tc := range map[string]struct {
		widenTo int64
		maxSpan int
	}{
		"an end past the probe":          {total + spanWidenBytes, f.Cfg.Store.Chunk.Max},
		"an end that does not move":      {unwidened.End, f.Cfg.Store.Chunk.Max},
		"a probe with no room to look":   {total, 0},
		"an end before the window's own": {1, f.Cfg.Store.Chunk.Max},
	} {
		t.Run(name, func(t *testing.T) {
			o := base
			o.MaxSpan = tc.maxSpan
			f.Widen.WidenTo = int64p(tc.widenTo)
			t.Cleanup(func() { f.Widen.WidenTo = nil })

			res, rerr := ResolveSpan(t.Context(), f.Store, root, f.Widen, o)
			require.NoError(t, rerr, "ResolveSpan")
			require.False(t, res.Widened, "an untrustworthy widening must be refused")
		})
	}
}

// TestPropertyNextSpanPagingCoversObjectExactly is span.go's fourth normative property: following
// NextSpan from offset zero concatenates to the whole object exactly once, with no gaps and no overlaps.
//
// It is the property that makes alignment load-bearing rather than tidy, and the one a table test cannot
// reach: the failure mode is a boundary ARRANGEMENT, not a value, so the object has to be drawn and
// chunked for real. Sizes are capped at spanPropBytes because each draw pays for a full FastCDC pass.
func TestPropertyNextSpanPagingCoversObjectExactly(t *testing.T) {
	f := newFixture(t)
	whole := defaultSpanOpts(f.Cfg)
	whole.Full = true
	var seq int

	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, spanPropBytes).Draw(rt, "object bytes")
		seed := rapid.Uint64().Draw(rt, "content seed")
		seq++

		put, err := f.Store.PutBytes(t.Context(), spanDrawText(seed, n),
			store.PutOptions{Path: fmt.Sprintf("prop/page%d.bin", seq)})
		require.NoError(rt, err, "PutBytes(%d bytes)", n)
		root := put.Root

		want, err := ResolveSpan(t.Context(), f.Store, root, nil, whole)
		require.NoError(rt, err, "reading the object whole")

		var pages []byte
		o := defaultSpanOpts(f.Cfg)
		for i := 0; ; i++ {
			require.Less(rt, i, len(root.Chunks)+2, "paging must terminate inside the chunk count")
			page, perr := ResolveSpan(t.Context(), f.Store, root, nil, o)
			require.NoError(rt, perr, "ResolveSpan(span=%q)", o.Explicit)
			require.Equal(rt, int64(len(pages)), page.Off, "page %d must start where its predecessor ended", i)
			require.Greater(rt, page.End, page.Off, "every page of a non-empty object must be non-empty")
			pages = append(pages, page.Body...)
			if page.NextSpan == "" {
				break
			}
			o.Explicit = page.NextSpan
		}
		require.Equal(rt, want.Body, pages, "paging must reproduce the object byte for byte")
	})
}

// TestPropertySpanNeverExceedsMaxResponse is the third normative property, over the whole argument space
// rather than one path through it: whatever the caller asks for — full, an explicit span, a symbol, a
// line, a nonsense string — the resolved window fits inside runtime.mcp.maxResponseBytes.
//
// The budget is what stands between a retrieval tool and a blown context window, so it has to hold for
// argument combinations no handler would ever build, not merely for the ones spanOptsFor produces.
func TestPropertySpanNeverExceedsMaxResponse(t *testing.T) {
	f, _, root, _ := spanAuthObject(t)
	spans := []string{"", "0:1", "4096:100", "L1-L4", "L10-L20", "999999999:8", "99999999999999999999:5", "nope"}
	symbols := []string{"", "refreshToken", "login", "logout", "nosuchsymbol"}

	rapid.Check(t, func(rt *rapid.T) {
		o := SpanOpts{
			Full:        rapid.Bool().Draw(rt, "full"),
			Explicit:    rapid.SampledFrom(spans).Draw(rt, "explicit"),
			Path:        rapid.SampledFrom([]string{"", spanAuthPath}).Draw(rt, "path"),
			AnchorSym:   rapid.SampledFrom(symbols).Draw(rt, "symbol"),
			AnchorLine:  rapid.IntRange(0, 4000).Draw(rt, "line"),
			MaxSpan:     rapid.IntRange(0, 1<<16).Draw(rt, "max span"),
			MaxResponse: rapid.IntRange(1, 1<<18).Draw(rt, "max response"),
			WidenLines:  rapid.IntRange(0, 200).Draw(rt, "widen lines"),
		}
		var w Widener
		if rapid.Bool().Draw(rt, "widener") {
			w = &fakeWidener{}
		}

		res, err := ResolveSpan(t.Context(), f.Store, root, w, o)
		require.NoError(rt, err, "ResolveSpan(%+v)", o)

		require.LessOrEqual(rt, res.End-res.Off, int64(o.MaxResponse), "the window must fit the response budget")
		require.GreaterOrEqual(rt, res.Off, int64(0), "a window cannot start before the object")
		require.LessOrEqual(rt, res.End, res.Total, "a window cannot end past the object")
		require.Greater(rt, res.End, res.Off, "a non-empty object must resolve to a non-empty window")
		require.Len(rt, res.Body, int(res.End-res.Off), "the body must be exactly the resolved window")
	})
}

// BenchmarkResolveSpanMinimal measures the default path — no anchor, no explicit span, one widening probe
// — over a 200 000-byte source, which is the shape budget B-F is actually spent on.
//
// The store is opened here rather than through newFixture because the shared fixtures take a *testing.T
// and a benchmark has none. Seat F owns the 40 MB bench fixture; this one deliberately stays at the size
// a real source file is.
func BenchmarkResolveSpanMinimal(b *testing.B) {
	root := b.TempDir()
	require.NoError(b, paths.EnsureLayout(paths.Of(root)), "EnsureLayout")

	cfg := config.Defaults()
	st, err := store.Open(root, cfg, store.Deps{Log: logging.Nop(), Clock: newFakeClock(epoch)})
	require.NoError(b, err, "store.Open")
	b.Cleanup(func() { _ = st.Close() })

	put, err := st.PutBytes(b.Context(), []byte(buildFiller("bench", authTotalBytes)),
		store.PutOptions{Path: spanAuthPath})
	require.NoError(b, err, "PutBytes")

	o := defaultSpanOpts(cfg)
	o.Path = spanAuthPath
	w := &fakeWidener{}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := ResolveSpan(b.Context(), st, put.Root, w, o); err != nil {
			b.Fatalf("ResolveSpan: %v", err)
		}
	}
}
