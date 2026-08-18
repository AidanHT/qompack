package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/symbols"
)

// This file tests the store's small, unexported helpers DIRECTLY, rather than only through the
// exported surface that happens to call them.
//
// That is deliberate. Reaching storeKey through ChangedSince, or chunkingCovers through PutBytes,
// exercises the one input the caller happens to produce and leaves every boundary the helper is
// actually written to defend — an inverted span, a chunk list with a gap, a path spelled with
// backslashes — unexercised and unpinned. These are the functions where a silent behaviour change
// costs data (chunkingCovers) or costs identity (storeKey), so each gets the input table it needs
// rather than whatever a happy-path integration test drags through it.
//
// It lives in package store, not store_test, for the reason testdouble_test.go already states:
// every identifier below is unexported, and internal/testutil cannot be imported back into this
// package without an import cycle, so the fake clock these tests see is the in-package one
// newTestStore wires (testdouble_test.go's fakeClock), never a real wall clock.

// ── fsstore.go: storeKey ─────────────────────────────────────────────────────────────────────

// TestStoreKey_CanonicalizesSpellingsOfOnePath asserts every spelling difference storeKey is
// supposed to erase actually collapses to one key.
//
// This is the function that decides whether two names are the SAME FILE. ChangedSince compares a
// recorded version against a depends_on path written by another subplan or by a user (§8.3), and a
// key that disagrees with itself over "./src/a.ts" versus "src/a.ts" reports every dependency as
// stale forever — a silent, permanent cache miss rather than a visible failure.
//
// want is written as paths.Key(<canonical slash form>) rather than as a literal: paths.Key's own
// case folding is platform-dependent (§4), and hard-coding a lowercase literal here would assert
// paths.Key's behaviour instead of storeKey's. What is pinned below is exactly what storeKey adds
// on top of Key — separator conversion, path.Clean, the leading "./", and the empty result.
func TestStoreKey_CanonicalizesSpellingsOfOnePath(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		want        string
		windowsOnly bool
	}{
		{name: "empty stays empty", in: "", want: ""},
		{name: "bare dot is not a path", in: ".", want: ""},
		{name: "dot slash is not a path", in: "./", want: ""},
		{name: "already canonical", in: "src/auth.ts", want: paths.Key("src/auth.ts")},
		{name: "leading ./ is stripped", in: "./src/auth.ts", want: paths.Key("src/auth.ts")},
		{name: "doubled separator collapses", in: "src//auth.ts", want: paths.Key("src/auth.ts")},
		{name: "interior . is removed", in: "src/./auth.ts", want: paths.Key("src/auth.ts")},
		{name: "interior .. is resolved", in: "src/lib/../auth.ts", want: paths.Key("src/auth.ts")},
		{name: "trailing separator is dropped", in: "src/auth.ts/", want: paths.Key("src/auth.ts")},
		{name: "escaping .. is kept, not resolved away", in: "../outside.ts", want: paths.Key("../outside.ts")},
		{name: "posix absolute stays absolute", in: "/etc/hosts", want: paths.Key("/etc/hosts")},
		{name: "backslashes become slashes", in: `src\auth.ts`, want: paths.Key("src/auth.ts"), windowsOnly: true},
		{name: "mixed separators and ..", in: `src\lib/..\auth.ts`, want: paths.Key("src/auth.ts"), windowsOnly: true},
		{name: "windows absolute", in: `C:\proj\a.ts`, want: paths.Key("C:/proj/a.ts"), windowsOnly: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.windowsOnly && runtime.GOOS != "windows" {
				t.Skip("platform: filepath.ToSlash only rewrites backslashes on Windows, so this spelling is not a path elsewhere")
			}
			got := storeKey(c.in)
			require.Equal(t, c.want, got,
				"storeKey(%q) must canonicalize to %q: two spellings of one file that produce "+
					"different keys are two different files to every index in this package",
				c.in, c.want)
		})
	}
}

// TestStoreKey_IsIdempotent asserts a key fed back through storeKey is unchanged. Keys are stored
// in index files and re-read on the next Open, so a non-idempotent key would drift a little
// further from its own path on every reload.
func TestStoreKey_IsIdempotent(t *testing.T) {
	for _, in := range []string{"", ".", "./src/auth.ts", "src/lib/../auth.ts", "../outside.ts", "/etc/hosts"} {
		once := storeKey(in)
		require.Equal(t, once, storeKey(once),
			"storeKey(%q) = %q must be a fixed point: keys are persisted and re-keyed on reload",
			in, once)
	}
}

// TestStoreKey_FoldsCaseExactlyWhereThePlatformDoes asserts the case-folding half of the key
// follows paths.DefaultFold (§4): on Windows and macOS two casings of one path are one file, and
// on a case-sensitive filesystem they are genuinely two.
func TestStoreKey_FoldsCaseExactlyWhereThePlatformDoes(t *testing.T) {
	mixed, upper := storeKey("src/Auth.ts"), storeKey("SRC/AUTH.TS")
	if paths.DefaultFold() {
		require.Equal(t, mixed, upper,
			"on a case-insensitive filesystem these name one file, so they must share one key")
		return
	}
	require.NotEqual(t, mixed, upper,
		"on a case-sensitive filesystem these are two different files, and folding them together "+
			"would let one file's stored version answer for the other")
}

// TestStoreKey_DoesNotResolveAbsolutePathsAgainstTheProjectRoot pins a real LIMIT of storeKey,
// so it is a known property rather than a surprise: storeKey is a pure lookup-path function that
// touches no filesystem and knows no project root, so an absolute spelling of a project file does
// NOT collapse onto its relative key. paths.Norm is what does that, and it needs the root.
func TestStoreKey_DoesNotResolveAbsolutePathsAgainstTheProjectRoot(t *testing.T) {
	require.NotEqual(t, storeKey("/proj/src/auth.ts"), storeKey("src/auth.ts"),
		"storeKey canonicalizes spelling, not location: callers holding an absolute path must "+
			"run it through paths.Norm before using it as a store key")
}

// ── fsstore.go: chunkingCovers ───────────────────────────────────────────────────────────────

// TestChunkingCovers_RejectsAnythingThatDoesNotTile is the data-loss guard's own table.
//
// splitChecked falls back to a single whole-input chunk whenever this reports false, so every
// false below is a case where an injected chunker would otherwise have silently shredded content:
// Open(root) must reproduce exactly the bytes that were Put, and a chunk list with a gap, an
// overlap or a short total reassembles into something else entirely.
func TestChunkingCovers_RejectsAnythingThatDoesNotTile(t *testing.T) {
	c := func(off int64, n int) chunk.Chunk { return chunk.Chunk{Offset: off, Len: n} }

	cases := []struct {
		name   string
		chunks []chunk.Chunk
		n      int
		want   bool
	}{
		{name: "single chunk covering everything", chunks: []chunk.Chunk{c(0, 10)}, n: 10, want: true},
		{name: "three contiguous chunks", chunks: []chunk.Chunk{c(0, 4), c(4, 1), c(5, 5)}, n: 10, want: true},
		{name: "nil chunk list for non-empty input", chunks: nil, n: 10, want: false},
		{name: "empty chunk list for non-empty input", chunks: []chunk.Chunk{}, n: 10, want: false},
		{name: "gap between two chunks", chunks: []chunk.Chunk{c(0, 4), c(6, 4)}, n: 10, want: false},
		{name: "overlapping chunks", chunks: []chunk.Chunk{c(0, 6), c(4, 6)}, n: 10, want: false},
		{name: "does not start at zero", chunks: []chunk.Chunk{c(2, 8)}, n: 10, want: false},
		{name: "stops short of the input", chunks: []chunk.Chunk{c(0, 4), c(4, 4)}, n: 10, want: false},
		{name: "runs past the input", chunks: []chunk.Chunk{c(0, 6), c(6, 6)}, n: 10, want: false},
		{name: "zero-length member", chunks: []chunk.Chunk{c(0, 5), c(5, 0), c(5, 5)}, n: 10, want: false},
		{name: "negative-length member", chunks: []chunk.Chunk{c(0, 12), c(12, -2)}, n: 10, want: false},
		{name: "chunks out of order", chunks: []chunk.Chunk{c(5, 5), c(0, 5)}, n: 10, want: false},
		{name: "leading zero-length chunk", chunks: []chunk.Chunk{c(0, 0), c(0, 10)}, n: 10, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, chunkingCovers(tc.chunks, tc.n),
				"chunkingCovers(%v, %d) must report %v: this is the guard that stops a chunker "+
					"silently truncating stored content, and a wrong answer here is unrecoverable "+
					"data loss rather than a failed call",
				tc.chunks, tc.n, tc.want)
		})
	}
}

// TestChunkingCovers_IsExactAboutTheTotal asserts the total-length check is an equality, not a
// bound: a chunk list one byte short and one byte long are both rejected.
func TestChunkingCovers_IsExactAboutTheTotal(t *testing.T) {
	const n = 64
	full := []chunk.Chunk{{Offset: 0, Len: n}}
	require.True(t, chunkingCovers(full, n))
	require.False(t, chunkingCovers([]chunk.Chunk{{Offset: 0, Len: n - 1}}, n),
		"a chunk list one byte short must be rejected: the missing byte is gone from the stored root")
	require.False(t, chunkingCovers([]chunk.Chunk{{Offset: 0, Len: n + 1}}, n),
		"a chunk list one byte long must be rejected: the extra byte does not exist to be read back")
}

// ── fsstore.go: marshalLine ──────────────────────────────────────────────────────────────────

// TestMarshalLine_WritesOneUnescapedRecordPerLine asserts the two properties every append-only
// index file depends on: exactly one newline, at the end, and no HTML escaping — so a "<" inside a
// path or a preview lands on disk verbatim rather than as \u003c.
func TestMarshalLine_WritesOneUnescapedRecordPerLine(t *testing.T) {
	type rec struct {
		Path string `json:"path"`
		Note string `json:"note"`
	}
	in := rec{Path: "src/<generated>&legacy.ts", Note: "a > b && c < d"}

	b, err := marshalLine(in)
	require.NoError(t, err)
	require.NotEmpty(t, b)
	require.Equal(t, byte('\n'), b[len(b)-1],
		"an index record must be newline-terminated, or the next append fuses onto it")
	require.Equal(t, 1, strings.Count(string(b), "\n"),
		"one record must be exactly one line: an embedded newline splits it into two records that "+
			"every replaying reader would then mis-parse")
	require.Contains(t, string(b), `"src/<generated>&legacy.ts"`,
		"HTML escaping must stay off so a path survives verbatim on disk")

	var back rec
	require.NoError(t, json.Unmarshal(b, &back))
	require.Equal(t, in, back, "the written line must decode back to the record that was written")
}

// TestMarshalLine_ReportsAnUnencodableRecord asserts a value encoding/json cannot represent is
// reported rather than written. The caller appends the returned bytes to an append-only index, and
// a partially encoded record cannot be taken back out again.
func TestMarshalLine_ReportsAnUnencodableRecord(t *testing.T) {
	for _, c := range []struct {
		name string
		in   any
	}{
		{name: "NaN has no JSON representation", in: math.NaN()},
		{name: "positive infinity has no JSON representation", in: math.Inf(1)},
		{name: "a channel is not encodable", in: make(chan int)},
		{name: "a func value is not encodable", in: func() {}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := marshalLine(c.in)
			require.Error(t, err,
				"an unencodable record must fail loudly: silently appending a partial line would "+
					"corrupt every subsequent replay of the index")
			require.Nil(t, b, "a failed encode must hand back no bytes at all to append")
		})
	}
}

// ── fsstore.go: appendFile and count ─────────────────────────────────────────────────────────

// bufferHandle is an io.WriteCloser that is NOT an *os.File, so it does not implement the syncer
// interface appendFile.sync asserts for. It stands in for the buffering wrapper paths.AppendOnly
// is free to return.
type bufferHandle struct {
	written []byte
	closed  bool
}

// Write records the bytes handed to it.
func (h *bufferHandle) Write(p []byte) (int, error) {
	h.written = append(h.written, p...)
	return len(p), nil
}

// Close marks the handle closed.
func (h *bufferHandle) Close() error {
	h.closed = true
	return nil
}

// TestAppendFile_SyncSkipsAHandleThatCannotFsync asserts a handle without a Sync method is written
// through and syncs as a no-op rather than as a failure.
//
// This is load-bearing rather than pedantic: sync() is called from Flush, which is the
// SessionEnd/idle barrier, and a buffering wrapper that merely cannot be fsynced must never be the
// reason SessionEnd reports an error.
func TestAppendFile_SyncSkipsAHandleThatCannotFsync(t *testing.T) {
	h := &bufferHandle{}
	a := &appendFile{p: "buffered.jsonl", w: h}

	require.NoError(t, a.write([]byte("{\"v\":1}\n")))
	require.Equal(t, "{\"v\":1}\n", string(h.written), "write must reach the underlying handle verbatim")

	require.NoError(t, a.sync(),
		"a handle that does not implement Sync must sync as a no-op: it is still a correct writer, "+
			"merely not fsynced, and SessionEnd must not fail over it")

	require.NoError(t, a.close())
	require.True(t, h.closed, "close must release the underlying handle")
}

// TestAppendFile_DegradedAfterClose asserts the three operations on a released handle behave the
// way §12.3 requires: a write reports core.ErrDegraded rather than panicking on a nil writer, and
// sync and close are idempotent no-ops.
func TestAppendFile_DegradedAfterClose(t *testing.T) {
	a := &appendFile{p: "released.jsonl", w: &bufferHandle{}}
	require.NoError(t, a.close())

	err := a.write([]byte("{}\n"))
	require.ErrorIs(t, err, core.ErrDegraded,
		"a write to a released index handle must degrade, not panic: Close races with a daemon "+
			"worker still draining its queue")
	require.NoError(t, a.sync(), "sync on a released handle must be a no-op, not an error")
	require.NoError(t, a.close(), "close must be idempotent")
}

// TestOpenAppendFile_RefusesANonIndexExtension asserts the append-only door cannot be used to open
// an arbitrary file merely by naming it something else — the property paths.AppendOnly enforces and
// openAppendFile propagates rather than swallowing.
func TestOpenAppendFile_RefusesANonIndexExtension(t *testing.T) {
	dir := t.TempDir()

	a, err := openAppendFile(filepath.Join(dir, "notes.txt"))
	require.Error(t, err, "openAppendFile must refuse a file that is not an index log")
	require.ErrorIs(t, err, core.ErrAppendOnly)
	require.Nil(t, a, "a failed open must hand back no handle to write through")

	ok, err := openAppendFile(filepath.Join(dir, "roots.jsonl"))
	require.NoError(t, err, "fixture sanity: a .jsonl index must open")
	require.NoError(t, ok.close())
}

// TestCount_ToleratesAStoreWithNoMetrics asserts the metrics guard on the Put hot path.
//
// Deps.Metrics is a public field any caller may leave nil on a hand-built FSStore (this package's
// own tests build them), and count is called from splitChecked, quarantine and Search. A nil
// registry must be silence, never a panic that takes the session down over a counter.
func TestCount_ToleratesAStoreWithNoMetrics(t *testing.T) {
	var bare FSStore
	require.NotPanics(t, func() { bare.count("store.helpers.probe", 1) },
		"count must be silent when no metrics registry is wired, not panic on the Put hot path")

	tp := newTestStore(t)
	tp.Store.count("store.helpers.probe", 3)
	require.Equal(t, int64(3), tp.counter("store.helpers.probe"),
		"a wired registry must actually receive the increment; a guard that swallowed both cases "+
			"would make every counter in this package silently zero")
}

// ── search.go: clampSpan and widenToChunks ───────────────────────────────────────────────────

// TestClampSpan_BoundsAndUninvertsASpan pins every branch of the span clamp Hit.Span passes
// through. Out-of-range arguments clamp rather than error because the callers are retrieval tools
// whose job is to return the best available answer (§8.7).
//
// The last case pins a real ASYMMETRY rather than an intention: clampSpan raises lo to 0 but never
// lowers it to total, so a lo above total survives and collapses hi onto it. See
// TestClampSpan_DoesNotClampLoDownToTotal.
func TestClampSpan_BoundsAndUninvertsASpan(t *testing.T) {
	cases := []struct {
		name           string
		lo, hi, total  int64
		want           [2]int64
		wantWithinSpan bool
	}{
		{name: "already inside the content", lo: 2, hi: 8, total: 10, want: [2]int64{2, 8}, wantWithinSpan: true},
		{name: "whole content", lo: 0, hi: 10, total: 10, want: [2]int64{0, 10}, wantWithinSpan: true},
		{name: "negative start raises to zero", lo: -5, hi: 4, total: 10, want: [2]int64{0, 4}, wantWithinSpan: true},
		{name: "end past the content clamps down", lo: 3, hi: 99, total: 10, want: [2]int64{3, 10}, wantWithinSpan: true},
		{name: "both ends out of range", lo: -7, hi: 99, total: 10, want: [2]int64{0, 10}, wantWithinSpan: true},
		{name: "inverted span collapses onto its start", lo: 8, hi: 3, total: 10, want: [2]int64{8, 8}, wantWithinSpan: true},
		{name: "wholly negative span collapses to empty at zero", lo: -3, hi: -1, total: 10, want: [2]int64{0, 0}, wantWithinSpan: true},
		{name: "empty content collapses everything to zero", lo: 4, hi: 9, total: 0, want: [2]int64{0, 0}, wantWithinSpan: true},
		{name: "start beyond the content lowers to the end", lo: 20, hi: 25, total: 10, want: [2]int64{10, 10}, wantWithinSpan: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := clampSpan(c.lo, c.hi, c.total)
			require.Equal(t, c.want, got,
				"clampSpan(%d, %d, %d) must be %v: Hit.Span is what OpenSpan is called with, so an "+
					"inverted or unbounded span reads the wrong region of a root",
				c.lo, c.hi, c.total, c.want)
			require.LessOrEqual(t, got[0], got[1], "a clamped span must never come back inverted")
			if c.wantWithinSpan {
				require.LessOrEqual(t, got[1], c.total, "the end must never exceed the content length")
			}
		})
	}
}

// TestClampSpan_BothEndsStayInsideTheContent pins, as a standing assertion rather than a buried
// table row, that clampSpan bounds lo at BOTH ends — which is what its doc comment claims and, for
// one revision, was not what the code did.
//
// The caller that can supply an out-of-range lo is score's exact-symbol branch, where the offset
// comes from a symbol extractor rather than from the content itself. It was never a crash, because
// both consumers of the result (OpenSpan, summarize) re-clamp defensively — but retrieval publishes
// Hit.Span and a consumer has no way to tell a nonsense offset from a real one, so
// `Span[0] <= Root.CanonBytes` has to hold here rather than at each reader.
func TestClampSpan_BothEndsStayInsideTheContent(t *testing.T) {
	got := clampSpan(20, 25, 10)
	require.Equal(t, [2]int64{10, 10}, got,
		"a start past the end of the content must lower to the end, yielding a valid empty span "+
			"rather than a span that begins outside the root it indexes")

	// The property, not just the example.
	for _, total := range []int64{0, 1, 10, 4096} {
		for _, lo := range []int64{-9, -1, 0, 1, 9, 4097, 1 << 20} {
			for _, hi := range []int64{-3, 0, 2, 11, 1 << 20} {
				s := clampSpan(lo, hi, total)
				require.GreaterOrEqual(t, s[0], int64(0), "clampSpan(%d,%d,%d) start went negative", lo, hi, total)
				require.LessOrEqual(t, s[0], total, "clampSpan(%d,%d,%d) start escaped the content", lo, hi, total)
				require.LessOrEqual(t, s[1], total, "clampSpan(%d,%d,%d) end escaped the content", lo, hi, total)
				require.LessOrEqual(t, s[0], s[1], "clampSpan(%d,%d,%d) came back inverted", lo, hi, total)
			}
		}
	}
}

// TestWidenToChunks_ExpandsOutwardToChunkBoundaries asserts a match is widened to the boundaries of
// the chunks it intersects, never inward.
//
// Widening outward is what makes the span independently useful (§8.7): a chunk boundary falls at a
// content-defined position, so the widened region is self-contained rather than a match with its
// first and last lines sheared off.
func TestWidenToChunks_ExpandsOutwardToChunkBoundaries(t *testing.T) {
	// Chunk ENDS, which is the form materialize produces: chunks are [0,10) [10,20) [20,30).
	bounds := []int64{10, 20, 30}
	const total = int64(30)

	cases := []struct {
		name     string
		bounds   []int64
		lo, hi   int64
		total    int64
		want     [2]int64
		wantWide bool
	}{
		{name: "match inside the first chunk", bounds: bounds, lo: 3, hi: 5, total: total, want: [2]int64{0, 10}, wantWide: true},
		{name: "match inside the middle chunk", bounds: bounds, lo: 12, hi: 15, total: total, want: [2]int64{10, 20}, wantWide: true},
		{name: "match inside the last chunk", bounds: bounds, lo: 28, hi: 30, total: total, want: [2]int64{20, 30}, wantWide: true},
		{name: "match straddling two chunks", bounds: bounds, lo: 15, hi: 25, total: total, want: [2]int64{10, 30}, wantWide: true},
		{name: "match exactly on the boundaries", bounds: bounds, lo: 10, hi: 20, total: total, want: [2]int64{10, 20}},
		{name: "match running past the last boundary", bounds: bounds, lo: 5, hi: 40, total: total, want: [2]int64{0, 30}, wantWide: true},
		{name: "no chunk bounds falls back to a plain clamp", bounds: nil, lo: 5, hi: 40, total: total, want: [2]int64{5, 30}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := widenToChunks(c.bounds, c.lo, c.hi, c.total)
			require.Equal(t, c.want, got,
				"widenToChunks(%v, %d, %d, %d) must be %v", c.bounds, c.lo, c.hi, c.total, c.want)
			require.LessOrEqual(t, got[0], c.lo,
				"widening must never move the start FORWARD past the match: that shears off the "+
					"beginning of the very hunk the caller asked for")
			require.GreaterOrEqual(t, got[1], min64(c.hi, c.total),
				"widening must never move the end BACK before the match")
			if c.wantWide {
				require.Less(t, got[1]-got[0], c.total+1, "fixture sanity: the span stays inside the root")
			}
		})
	}
}

// min64 is the smaller of two int64s, used only to state the widening assertion above.
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// ── search.go: summarize, stripControl, truncateRunes ────────────────────────────────────────

// TestSummarize_ReportsTheFirstNonBlankLineOfTheSpan asserts the summary rules, including the two
// span repairs summarize performs on its own — a start outside the content restarts at 0, and an
// empty or inverted span runs to the end of the content rather than yielding nothing.
func TestSummarize_ReportsTheFirstNonBlankLineOfTheSpan(t *testing.T) {
	body := []byte("\n   \nfirst real line\nsecond line\nthird line\n")

	cases := []struct {
		name    string
		content []byte
		span    [2]int64
		want    string
	}{
		{name: "empty content has no summary", content: nil, span: [2]int64{0, 0}, want: ""},
		{name: "skips leading blank and whitespace-only lines", content: body, span: [2]int64{0, int64(len(body))}, want: "first real line"},
		{name: "summarizes the span, not the whole content", content: body, span: [2]int64{20, 32}, want: "second line"},
		{name: "negative start restarts at zero", content: body, span: [2]int64{-4, 20}, want: "first real line"},
		{name: "start past the end restarts at zero", content: body, span: [2]int64{9999, 10000}, want: "first real line"},
		{name: "end past the content clamps to the end", content: body, span: [2]int64{20, 9999}, want: "second line"},
		{name: "empty span runs to the end of the content", content: body, span: [2]int64{20, 20}, want: "second line"},
		{name: "inverted span runs to the end of the content", content: body, span: [2]int64{20, 4}, want: "second line"},
		{name: "an all-blank span has no summary", content: []byte("\n\n   \n\t\n"), span: [2]int64{0, 8}, want: ""},
		{name: "control characters are stripped from the reported line", content: []byte("\x01a\x00b\x1fc\x7f\n"), span: [2]int64{0, 8}, want: "abc"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, summarize(c.content, c.span),
				"summarize(%q, %v) must be %q: the summary is what a `recall` caller reads before "+
					"deciding whether to spend tokens re-materializing the root",
				string(c.content), c.span, c.want)
		})
	}
}

// TestSummarize_TruncatesToThePreviewBound asserts a long first line is cut to argsPreviewMax
// BYTES, on a rune boundary, with the ellipsis that makes the cut visible.
func TestSummarize_TruncatesToThePreviewBound(t *testing.T) {
	long := []byte(strings.Repeat("é", 300) + "\n")

	got := summarize(long, [2]int64{0, int64(len(long))})
	require.LessOrEqual(t, len(got), argsPreviewMax,
		"a summary must fit the ≤120-byte preview budget §5.8 states")
	require.True(t, strings.HasSuffix(got, previewEllipsis),
		"a truncated summary must end in an ellipsis so the reader knows it was cut")
	require.Equal(t, strings.ToValidUTF8(got, "\uFFFD"), got,
		"the cut must land on a rune boundary: half a multi-byte rune renders as a replacement "+
			"character in every consumer of the preview")
}

// TestStripControl_RemovesC0AndDelOnly asserts exactly which bytes are removed. Stripping too much
// mangles a legitimate preview; stripping too little lets a raw escape sequence out of a tool
// result and into a terminal.
func TestStripControl_RemovesC0AndDelOnly(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "nothing to strip", in: "plain ascii text", want: "plain ascii text"},
		{name: "NUL is removed", in: "a\x00b", want: "ab"},
		{name: "ESC is removed", in: "a\x1b[31mred", want: "a[31mred"},
		{name: "tab is removed", in: "a\tb", want: "ab"},
		{name: "newline is removed", in: "a\nb", want: "ab"},
		{name: "the last C0 byte is removed", in: "a\x1fb", want: "ab"},
		{name: "DEL is removed", in: "a\x7fb", want: "ab"},
		{name: "space is kept", in: "a b", want: "a b"},
		{name: "the first printable byte is kept", in: "a\x20b", want: "a b"},
		{name: "the byte after DEL is kept", in: "a\u0080b", want: "a\u0080b"},
		{name: "non-ASCII runes are kept whole", in: "héllo → wörld ✅", want: "héllo → wörld ✅"},
		{name: "only control bytes leaves nothing", in: "\x00\x01\x1f\x7f", want: ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, stripControl(c.in),
				"stripControl(%q) must be %q", c.in, c.want)
		})
	}
}

// TestTruncateRunes_CutsOnARuneBoundary asserts the byte bound and the rune-boundary rule, and
// pins the one input where the two cannot both be honoured.
func TestTruncateRunes_CutsOnARuneBoundary(t *testing.T) {
	t.Run("a short string is returned unchanged", func(t *testing.T) {
		require.Equal(t, "short", truncateRunes("short", argsPreviewMax))
	})

	t.Run("a string of exactly max bytes is not cut", func(t *testing.T) {
		s := strings.Repeat("a", argsPreviewMax)
		require.Equal(t, s, truncateRunes(s, argsPreviewMax),
			"a string that already fits must not lose three bytes to an ellipsis it does not need")
	})

	t.Run("ascii is cut to exactly max bytes", func(t *testing.T) {
		got := truncateRunes(strings.Repeat("a", 500), argsPreviewMax)
		require.Len(t, got, argsPreviewMax)
		require.True(t, strings.HasSuffix(got, previewEllipsis))
	})

	t.Run("multi-byte runes are never split", func(t *testing.T) {
		got := truncateRunes(strings.Repeat("é", 500), argsPreviewMax)
		require.LessOrEqual(t, len(got), argsPreviewMax)
		require.True(t, strings.HasSuffix(got, previewEllipsis))
		require.Equal(t, strings.ToValidUTF8(got, "\uFFFD"), got,
			"cutting mid-rune would emit a replacement character into the preview")
		require.Equal(t, strings.Repeat("é", (argsPreviewMax-len(previewEllipsis))/2)+previewEllipsis, got,
			"the cut must land on the last COMPLETE rune that fits, not merely somewhere valid")
	})

	// The one input where "at most max bytes" and "mark the cut" conflict. The bound wins: a
	// truncation function that returns MORE bytes than it was asked for is the one thing it must
	// never do, so the ellipsis is dropped rather than the bound broken. The sole production caller
	// passes argsPreviewMax (120), so this is a latent contract — but it is exactly the sort that
	// gets discovered by overrunning a buffer downstream.
	t.Run("a bound smaller than the ellipsis drops the ellipsis, not the bound", func(t *testing.T) {
		for _, max := range []int{-1, 0, 1, 2, 3} {
			got := truncateRunes("hello", max)
			if max < 0 {
				require.Empty(t, got, "a negative bound admits nothing")
				continue
			}
			require.LessOrEqual(t, len(got), max,
				"truncateRunes(%q, %d) returned %d bytes: the byte bound is the whole contract",
				"hello", max, len(got))
		}
	})

	t.Run("a small bound still cuts on a rune boundary", func(t *testing.T) {
		// "é" is two bytes, so a bound of 3 may admit one rune but must never emit half of the second.
		got := truncateRunes("ééé", 3)
		require.LessOrEqual(t, len(got), 3)
		require.Equal(t, strings.ToValidUTF8(got, "�"), got,
			"a bound too small for the ellipsis must still not split a multi-byte rune")
	})
}

// ── search.go: ASCII case folding ────────────────────────────────────────────────────────────

// TestLowerASCII_FoldsOnlyTheASCIIUpperRange asserts the fold touches 'A'..'Z' and nothing else —
// including the bytes immediately on either side, which is where an off-by-one would silently turn
// '@' into '`' and corrupt every text search containing one.
func TestLowerASCII_FoldsOnlyTheASCIIUpperRange(t *testing.T) {
	cases := []struct {
		name  string
		in    byte
		want  byte
		folds bool
	}{
		{name: "A folds", in: 'A', want: 'a', folds: true},
		{name: "M folds", in: 'M', want: 'm', folds: true},
		{name: "Z folds", in: 'Z', want: 'z', folds: true},
		{name: "@ is just below A and does not fold", in: '@', want: '@'},
		{name: "[ is just above Z and does not fold", in: '[', want: '['},
		{name: "lower case is already folded", in: 'a', want: 'a'},
		{name: "a digit does not fold", in: '7', want: '7'},
		{name: "an underscore does not fold", in: '_', want: '_'},
		{name: "a UTF-8 lead byte does not fold", in: 0xC3, want: 0xC3},
		{name: "a UTF-8 continuation byte does not fold", in: 0x89, want: 0x89},
		{name: "the high byte does not fold", in: 0xFF, want: 0xFF},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, lowerASCII(c.in),
				"lowerASCII(%#x) must be %#x", c.in, c.want)
			if c.folds {
				require.NotEqual(t, c.in, lowerASCII(c.in), "fixture sanity: this case must actually fold")
			}
		})
	}
}

// TestFoldSearch_IsASCIIInsensitiveOnly asserts countFold and indexFold agree on one folding rule,
// and that the rule stops at ASCII: "É" must not match "é". Folding non-ASCII by byte would need
// case mapping this function does not do, and pretending otherwise would make `recall` report
// matches that are not there.
func TestFoldSearch_IsASCIIInsensitiveOnly(t *testing.T) {
	cases := []struct {
		name      string
		hay       string
		needle    string
		wantCount int
		wantIndex int
	}{
		{name: "empty needle matches nothing", hay: "abc", needle: "", wantCount: 0, wantIndex: -1},
		{name: "needle longer than hay", hay: "ab", needle: "abc", wantCount: 0, wantIndex: -1},
		{name: "empty hay", hay: "", needle: "a", wantCount: 0, wantIndex: -1},
		{name: "no match", hay: "the quick brown fox", needle: "needle", wantCount: 0, wantIndex: -1},
		{name: "match at the very start", hay: "needle here", needle: "needle", wantCount: 1, wantIndex: 0},
		{name: "match at the very end", hay: "here needle", needle: "needle", wantCount: 1, wantIndex: 5},
		{name: "hay exactly equals needle", hay: "needle", needle: "needle", wantCount: 1, wantIndex: 0},
		{name: "query case is folded", hay: "the Needle here", needle: "NEEDLE", wantCount: 1, wantIndex: 4},
		{name: "content case is folded", hay: "the NEEDLE here", needle: "needle", wantCount: 1, wantIndex: 4},
		{name: "several occurrences", hay: "NeEdLe needle NEEDLE", needle: "needle", wantCount: 3, wantIndex: 0},
		{name: "occurrences are counted non-overlapping", hay: "aaaa", needle: "aa", wantCount: 2, wantIndex: 0},
		{name: "an odd tail leaves the overlap uncounted", hay: "aaa", needle: "aa", wantCount: 1, wantIndex: 0},
		{name: "non-ASCII case is NOT folded", hay: "caf\u00c9", needle: "caf\u00e9", wantCount: 0, wantIndex: -1},
		{name: "non-ASCII matches itself exactly", hay: "caf\u00e9 au lait", needle: "caf\u00e9", wantCount: 1, wantIndex: 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.wantCount, countFold([]byte(c.hay), c.needle),
				"countFold(%q, %q) must be %d: the occurrence count is the whole text term of the "+
					"recall score", c.hay, c.needle, c.wantCount)
			require.Equal(t, c.wantIndex, indexFold([]byte(c.hay), c.needle),
				"indexFold(%q, %q) must be %d: this offset is what the reported Span is widened "+
					"from, so a wrong one returns the wrong region of the file",
				c.hay, c.needle, c.wantIndex)
		})
	}
}

// TestFoldSearch_CountAndIndexAgree asserts the two folding scanners never disagree about whether
// a needle is present. score uses countFold to grade and indexFold to place the span, so a
// disagreement would produce a hit scored on occurrences whose span points at nothing.
func TestFoldSearch_CountAndIndexAgree(t *testing.T) {
	hay := []byte("Alpha beta ALPHA gamma alpha delta")
	for _, needle := range []string{"", "a", "alpha", "ALPHA", "AlPhA", "delta", "epsilon", "gamma "} {
		present := countFold(hay, needle) > 0
		found := indexFold(hay, needle) >= 0
		require.Equal(t, present, found,
			"countFold and indexFold disagree about %q: score grades with one and places the span "+
				"with the other, so they must never diverge", needle)
	}
}

// ── search.go: ranking helpers ───────────────────────────────────────────────────────────────

// TestRecencyRank_MapsNewestFirstPositionOntoAUnitScale asserts the recency term, including the
// n <= 0 guard that stops a division by zero on an empty candidate set.
func TestRecencyRank_MapsNewestFirstPositionOntoAUnitScale(t *testing.T) {
	cases := []struct {
		name string
		i, n int
		want float64
	}{
		{name: "no candidates at all", i: 0, n: 0, want: 0},
		{name: "a negative population is not a rank", i: 3, n: -1, want: 0},
		{name: "the only candidate is the newest", i: 0, n: 1, want: 1},
		{name: "the newest of four", i: 0, n: 4, want: 1},
		{name: "the second of four", i: 1, n: 4, want: 0.75},
		{name: "the oldest of four", i: 3, n: 4, want: 0.25},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.InDelta(t, c.want, recencyRank(c.i, c.n), 0,
				"recencyRank(%d, %d) must be %v", c.i, c.n, c.want)
		})
	}

	// The candidate list is newest-first, so the rank must fall monotonically down it: a
	// non-monotonic recency term would let an older result outrank a newer identical one.
	const n = 8
	prev := recencyRank(0, n)
	for i := 1; i < n; i++ {
		got := recencyRank(i, n)
		require.Less(t, got, prev,
			"recency must decrease strictly down a newest-first candidate list (position %d)", i)
		require.Positive(t, got, "every real candidate keeps some recency credit")
		prev = got
	}
}

// TestSortHits_OrdersByScoreThenRecencyThenID asserts the ranking order and, more importantly,
// that it is TOTAL. sort.Slice is not stable, so without the tool-use id as a final tiebreak two
// hits that tie on score and timestamp could come back in either order — and a golden could not
// pin a recall result at all.
func TestSortHits_OrdersByScoreThenRecencyThenID(t *testing.T) {
	build := func() []Hit {
		return []Hit{
			{ToolUseID: "tu-b", Score: 1.0, TS: 10},
			{ToolUseID: "tu-a", Score: 1.0, TS: 10},
			{ToolUseID: "tu-c", Score: 1.0, TS: 20},
			{ToolUseID: "tu-d", Score: 2.0, TS: 5},
			{ToolUseID: "tu-e", Score: 0.5, TS: 99},
		}
	}
	want := []core.ToolUseID{"tu-d", "tu-c", "tu-a", "tu-b", "tu-e"}

	hits := build()
	sortHits(hits)
	require.Equal(t, want, hitIDs(hits),
		"hits must order by score descending, then timestamp descending, then id ascending")

	// Every permutation of the same input must land in the same order, which is the property the
	// id tiebreak exists to provide.
	perm := build()
	for i := 0; i < len(perm); i++ {
		rotated := append(append([]Hit{}, perm[i:]...), perm[:i]...)
		sortHits(rotated)
		require.Equal(t, want, hitIDs(rotated),
			"rotation %d reordered an identical hit set: the sort must be a TOTAL order, or no "+
				"golden can pin a recall result", i)
	}
}

// TestSortHits_EmptyAndSingle asserts the degenerate inputs Search hands it on a miss.
func TestSortHits_EmptyAndSingle(t *testing.T) {
	var none []Hit
	require.NotPanics(t, func() { sortHits(none) })
	require.Empty(t, none)

	one := []Hit{{ToolUseID: "tu-only", Score: 0.3}}
	sortHits(one)
	require.Equal(t, []core.ToolUseID{"tu-only"}, hitIDs(one))
}

// TestExactSymbol_MatchesByExactNameOnly asserts symbol lookup is exact and case-SENSITIVE, unlike
// the text term. Folding a symbol name would let a query for `Token` score `token` as an exact
// definition hit and report that symbol's extent as the span.
func TestExactSymbol_MatchesByExactNameOnly(t *testing.T) {
	syms := []symbols.Symbol{
		{Name: "refreshToken", Kind: "func", Offset: 100, Len: 40},
		{Name: "revokeToken", Kind: "func", Offset: 200, Len: 60},
		{Name: "refreshToken", Kind: "func", Offset: 300, Len: 10},
	}

	got, ok := exactSymbol(syms, "revokeToken")
	require.True(t, ok)
	require.Equal(t, 200, got.Offset)
	require.Equal(t, 60, got.Len)

	first, ok := exactSymbol(syms, "refreshToken")
	require.True(t, ok)
	require.Equal(t, 100, first.Offset,
		"a duplicated name must resolve to the FIRST declaration, so a repeated query is stable")

	missing, ok := exactSymbol(syms, "RefreshToken")
	require.False(t, ok, "symbol matching is case-sensitive: identifiers are, unlike free text")
	require.Equal(t, symbols.Symbol{}, missing, "a miss must report the zero symbol, not a stale one")

	_, ok = exactSymbol(nil, "refreshToken")
	require.False(t, ok, "a file with no extracted symbols cannot match one")

	_, ok = exactSymbol(syms, "")
	require.False(t, ok, "an empty name must not match a symbol")
}

// TestScanLimit_BoundsTheWorkBeforeDoingAny asserts both truncation axes, and that truncation is
// always reported. The byte budget is charged from each root's recorded CanonBytes rather than from
// bytes actually read, so the decision costs no I/O and the same store answers the same query the
// same way every time.
func TestScanLimit_BoundsTheWorkBeforeDoingAny(t *testing.T) {
	cands := func(n int, each int64) []searchCand {
		out := make([]searchCand, n)
		for i := range out {
			out[i] = searchCand{root: &rootEntry{Root: Root{CanonBytes: each}}}
		}
		return out
	}

	cases := []struct {
		name          string
		cands         []searchCand
		needContent   bool
		wantLimit     int
		wantTruncated bool
	}{
		{name: "a small path-only query scans everything", cands: cands(10, 1<<20), needContent: false, wantLimit: 10},
		{name: "a small text query scans everything", cands: cands(10, 1024), needContent: true, wantLimit: 10},
		{name: "no candidates at all", cands: nil, needContent: true, wantLimit: 0},
		{
			name:  "too many candidates truncates by count even with no content",
			cands: cands(maxCandidates+50, 1<<30), needContent: false,
			wantLimit: maxCandidates, wantTruncated: true,
		},
		{
			name:  "the byte budget cuts before the count does",
			cands: cands(10, maxScanBytes/4), needContent: true,
			wantLimit: 4, wantTruncated: true,
		},
		{
			name:  "the count cuts first when every root is tiny",
			cands: cands(maxCandidates+50, 1), needContent: true,
			wantLimit: maxCandidates, wantTruncated: true,
		},
		{
			name:  "a single oversized root still yields no candidates rather than an error",
			cands: cands(3, maxScanBytes+1), needContent: true,
			wantLimit: 0, wantTruncated: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			limit, truncated := scanLimit(c.cands, c.needContent)
			require.Equal(t, c.wantLimit, limit,
				"scanLimit must examine %d of %d candidates", c.wantLimit, len(c.cands))
			require.Equal(t, c.wantTruncated, truncated,
				"truncation must be REPORTED so Search can count it: a silently shortened scan is "+
					"indistinguishable from a store that simply held less")
			require.LessOrEqual(t, limit, len(c.cands), "the limit can never exceed the candidate set")
		})
	}
}

// ── compress.go: Decode's failure path ───────────────────────────────────────────────────────

// TestDecode_RejectsDamagedFrames asserts a damaged object fails the decode rather than yielding
// bytes.
//
// This is the mechanism getObject's quarantine rests on (§12.3 "store corrupt"): the read reports
// core.ErrNotFound and the caller degrades to "I cannot re-materialize this". If Decode ever
// returned a partial or garbage buffer for a damaged frame instead of an error, a corrupted tool
// result would be silently rehydrated into a session as if it were genuine.
func TestDecode_RejectsDamagedFrames(t *testing.T) {
	plain := []byte(strings.Repeat("the store must never hand back damaged bytes.\n", 64))
	frame, err := Encode(plain)
	require.NoError(t, err)
	require.NotEmpty(t, frame, "fixture sanity: a non-empty payload must encode to a non-empty frame")

	round, err := Decode(frame)
	require.NoError(t, err)
	require.Equal(t, plain, round, "fixture sanity: the intact frame must round-trip")

	flipped := append([]byte{}, frame...)
	flipped[len(flipped)/2] ^= 0xFF

	cases := []struct {
		name string
		in   []byte
	}{
		{name: "not a zstd frame at all", in: []byte("this is plain text, and never was a frame")},
		{name: "magic bytes only", in: frame[:4]},
		{name: "truncated mid-frame", in: frame[:len(frame)/2]},
		{name: "last byte lopped off", in: frame[:len(frame)-1]},
		{name: "one flipped byte inside the frame", in: flipped},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Decode(c.in)
			require.Error(t, err,
				"a damaged frame must fail the decode: this is the signal getObject quarantines on")
			require.ErrorContains(t, err, "store: Decode:",
				"the failure must be attributable to this package rather than surfacing as a bare "+
					"zstd error")
			require.Empty(t, out,
				"a failed Decode must hand back nothing at all: a partial buffer would be rehydrated "+
					"into a session as if it were the original tool result")
		})
	}
}

// TestEncode_IsReusableAcrossCalls asserts the pooled encoder and decoder stay correct when they
// are borrowed and returned repeatedly — the whole point of pooling them is that a reused encoder
// must not carry state from the previous payload into the next one.
func TestEncode_IsReusableAcrossCalls(t *testing.T) {
	payloads := [][]byte{
		[]byte("first"),
		[]byte(strings.Repeat("second payload, much longer than the first. ", 200)),
		nil,
		[]byte("fourth"),
	}
	for i, p := range payloads {
		frame, err := Encode(p)
		require.NoError(t, err, "payload %d", i)
		got, err := Decode(frame)
		require.NoError(t, err, "payload %d", i)
		require.Equal(t, len(p), len(got),
			"payload %d came back a different length: a pooled encoder must not carry state "+
				"between calls", i)
		if len(p) > 0 {
			require.Equal(t, p, got, "payload %d round-tripped to different bytes", i)
		}
	}
}

// ── objects.go: paths and staging ────────────────────────────────────────────────────────────

// TestObjectPaths_HonourCompressionAndTryBothNames asserts the write path follows
// store.compression while the read path always tries the compressed name first and the bare one
// second. That asymmetry is what lets a store whose store.compression changed mid-life still read
// every object it wrote under the other setting (§7.4).
func TestObjectPaths_HonourCompressionAndTryBothNames(t *testing.T) {
	h := core.HashBytes(core.DomainChunk, []byte("object naming probe"))
	hx := hexOf(h)
	require.Len(t, hx, 64, "an object filename is the 64 hex characters of its sha256, with no prefix")

	t.Run("compressed", func(t *testing.T) {
		tp := newTestStore(t)
		dir := filepath.Join(paths.Of(tp.Root).Objects, hx[:2], hx[2:4])

		require.Equal(t, filepath.Join(dir, hx+objectSuffix), tp.Store.objectPath(h),
			"a compressing store must write the %s name", objectSuffix)
		require.Equal(t,
			[2]string{filepath.Join(dir, hx+objectSuffix), filepath.Join(dir, hx)},
			tp.Store.objectCandidates(h),
			"readers must try the compressed name FIRST and the bare one second")
	})

	t.Run("uncompressed", func(t *testing.T) {
		tp := newTestStore(t, withCompressionNone())
		dir := filepath.Join(paths.Of(tp.Root).Objects, hx[:2], hx[2:4])

		require.Equal(t, filepath.Join(dir, hx), tp.Store.objectPath(h),
			"store.compression = %q must write the bare name", compressionNone)
		require.Equal(t,
			[2]string{filepath.Join(dir, hx+objectSuffix), filepath.Join(dir, hx)},
			tp.Store.objectCandidates(h),
			"the READ candidates must not change with the setting, or flipping store.compression "+
				"would orphan every object written before the flip")
	})
}

// TestTmpObjectPath_IsUniquePerCall asserts two writers of the same chunk never collide on one
// staging file. They would otherwise race on O_EXCL and one of them would fail a Put over a
// filename rather than over anything real.
func TestTmpObjectPath_IsUniquePerCall(t *testing.T) {
	tp := newTestStore(t)
	tmpDir := paths.Of(tp.Root).Tmp

	seen := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		p, err := tp.Store.tmpObjectPath()
		require.NoError(t, err)
		require.Equal(t, tmpDir, filepath.Dir(p),
			"crash debris must land under .qompack/tmp so it is recognizable and collectable")
		require.True(t, strings.HasPrefix(filepath.Base(p), objectTmpPrefix),
			"a staging file must carry the %q prefix, got %q", objectTmpPrefix, filepath.Base(p))
		_, dup := seen[p]
		require.False(t, dup,
			"two staging names collided (%s): concurrent writers of one chunk would then fight "+
				"over a single staging file", p)
		seen[p] = struct{}{}
	}
}

// TestEnsureDir_ReportsAFailedCreateAndCachesOnlySuccess asserts the fanout-directory cache is an
// optimistic hint that is only ever populated by a create that actually worked.
func TestEnsureDir_ReportsAFailedCreateAndCachesOnlySuccess(t *testing.T) {
	base := t.TempDir()

	blocker := filepath.Join(base, "not-a-directory")
	require.NoError(t, os.WriteFile(paths.Long(blocker), []byte("file"), 0o600))
	doomed := filepath.Join(blocker, "ab")

	require.Error(t, ensureDir(doomed),
		"a fanout directory that cannot be created must be REPORTED: the object write that follows "+
			"would otherwise fail with a much less informative error, or not at all")
	require.Error(t, ensureDir(doomed),
		"a failed create must not be cached as done, or every later write to that fanout would "+
			"skip the create and lose its object")

	real := filepath.Join(base, "cd", "ef")
	require.NoError(t, ensureDir(real))
	_, err := os.Stat(paths.Long(real))
	require.NoError(t, err, "a successful ensureDir must actually create the directory")

	// The cache is deliberately an optimistic HINT: once this process has created a directory it
	// stops re-issuing MkdirAll for it, which is what keeps hundreds of object writes per tool
	// result from paying hundreds of redundant syscalls.
	require.NoError(t, os.RemoveAll(paths.Long(real)))
	require.NoError(t, ensureDir(real),
		"a directory already created by this process must be answered from the cache without a "+
			"syscall — the deleted directory proves no create was re-issued")
}

// TestWriteStaged_NeverClobbersAnExistingFile asserts the O_EXCL on the staging open is real. A
// staging write that overwrote a file already at that path would destroy another writer's in-flight
// object rather than failing its own.
func TestWriteStaged_NeverClobbersAnExistingFile(t *testing.T) {
	tp := newTestStore(t)
	occupied := filepath.Join(paths.Of(tp.Root).Tmp, objectTmpPrefix+"occupied")
	prior := []byte("another writer's staged bytes")
	require.NoError(t, os.WriteFile(paths.Long(occupied), prior, 0o600))

	err := tp.Store.writeStaged(occupied, []byte("bytes that must never land"))
	require.Error(t, err, "staging onto an occupied path must fail rather than overwrite it")

	got, readErr := os.ReadFile(paths.Long(occupied))
	require.NoError(t, readErr)
	require.Equal(t, prior, got,
		"the prior staging file must be byte-identical: it belongs to a concurrent writer whose "+
			"Put would otherwise publish this call's bytes under its own content address")
}

// TestPutObject_FailsWhenTheStagingDirectoryIsUnusable asserts a failed staging write fails the
// enclosing putObject rather than reporting a phantom success.
//
// A root whose chunks are not all on disk is an unreadable lie, and reporting success for one would
// put an unrecoverable reference into index/roots.jsonl. This also exercises writeStaged's own
// recovery attempt — it recreates .qompack/tmp and retries once — by making that recreation
// impossible.
func TestPutObject_FailsWhenTheStagingDirectoryIsUnusable(t *testing.T) {
	tp := newTestStore(t)
	tmpDir := paths.Of(tp.Root).Tmp

	require.NoError(t, os.RemoveAll(paths.Long(tmpDir)))
	require.NoError(t, os.WriteFile(paths.Long(tmpDir), []byte("tmp is a file now"), 0o600))
	// Restore the layout before newTestStore's own cleanup closes the store.
	t.Cleanup(func() {
		_ = os.Remove(paths.Long(tmpDir))
		_ = os.MkdirAll(paths.Long(filepath.Join(tmpDir, quarantineDir)), 0o700)
	})

	payload := []byte("a chunk that can never be staged")
	h := core.HashBytes(core.DomainChunk, payload)

	n, novel, err := tp.Store.putObject(h, payload)
	require.Error(t, err, "an object that could not be staged must fail its Put")
	require.ErrorContains(t, err, "staging",
		"the failure must name the stage it happened at, so the operator can tell a staging "+
			"problem from a publish one")
	require.Zero(t, n, "a failed write must contribute no bytes to Stats.Bytes")
	require.False(t, novel, "a failed write must not be reported as a novel stored object")
	require.False(t, tp.Store.objectWritten(h), "nothing may be left at the content-addressed path")
}

// TestPutObject_FailsWhenTheFanoutDirectoryCannotBeCreated asserts a fanout directory that cannot
// be created fails the Put rather than being skipped.
//
// ensureDir's cache makes the create conditional, so this is the one place where a swallowed error
// would look exactly like a cache hit: the object write would proceed against a directory that does
// not exist, and the root that referenced it would be an unrecoverable reference in
// index/roots.jsonl.
func TestPutObject_FailsWhenTheFanoutDirectoryCannotBeCreated(t *testing.T) {
	tp := newTestStore(t)
	payload := []byte("a chunk whose fanout directory cannot exist")
	h := core.HashBytes(core.DomainChunk, payload)
	hx := hexOf(h)

	// A FILE where the first fanout level has to be a directory.
	level1 := filepath.Join(paths.Of(tp.Root).Objects, hx[:fanoutWidth])
	require.NoError(t, os.WriteFile(paths.Long(level1), []byte("not a directory"), 0o600))

	n, novel, err := tp.Store.putObject(h, payload)
	require.Error(t, err, "an object with nowhere to live must fail its Put")
	require.ErrorContains(t, err, "fanout directory",
		"the message must name what could not be created, so the operator is not left guessing "+
			"which of the write's four stages failed")
	require.Zero(t, n, "a failed write must contribute no bytes to Stats.Bytes")
	require.False(t, novel, "a failed write must not be reported as a novel stored object")
}

// TestQuarantine_RemovesTheObjectEvenWhenTheMoveFails asserts the §12.3 "store corrupt" row holds
// even when the quarantine directory cannot receive the file.
//
// Keeping the object for inspection is the nice-to-have; taking it OUT OF SERVICE is the
// requirement. Losing the race to move it must not leave a known-bad object sitting in objects/,
// where every later read would keep tripping over it and re-paying the failed decode.
func TestQuarantine_RemovesTheObjectEvenWhenTheMoveFails(t *testing.T) {
	tp := newTestStore(t)
	payload := []byte("a chunk that will be quarantined the hard way")
	h := core.HashBytes(core.DomainChunk, payload)

	_, _, err := tp.Store.putObject(h, payload)
	require.NoError(t, err)
	objPath := tp.Store.objectPath(h)

	// Block the quarantine destination with a non-empty directory, so the move cannot land there.
	blocked := filepath.Join(paths.Of(tp.Root).Tmp, quarantineDir, filepath.Base(objPath))
	require.NoError(t, os.MkdirAll(paths.Long(blocked), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(blocked, "occupant")), []byte("x"), 0o600))

	_, err = tp.Store.getObject(h, len(payload)+1)
	require.ErrorIs(t, err, core.ErrNotFound,
		"the read must still degrade to not-found rather than surfacing the failed move")

	_, statErr := os.Stat(paths.Long(objPath))
	require.True(t, os.IsNotExist(statErr),
		"a known-bad object must leave objects/ even when it cannot be preserved for inspection: "+
			"otherwise every later read re-discovers the same corruption")
	require.Equal(t, int64(1), tp.counter("store.quarantined"),
		"the quarantine must still be counted, since that counter is how the corruption becomes "+
			"visible at all")
}

// ── objects.go: isRenameContention and renameObject ──────────────────────────────────────────

// TestIsRenameContention_ClassifiesOnlyTheTwoTransientWindowsErrors asserts the retry classifier.
//
// Getting this wrong in either direction is expensive: classifying a real failure as contention
// spins the retry loop and then reports the same error anyway, while failing to recognise genuine
// antivirus contention turns a routine, self-healing hiccup into a failed Put under the daemon's
// worker pool.
//
// The whole classifier is gated on runtime.GOOS == "windows" — the two constants have unrelated
// POSIX meanings — so every case below expects false off Windows, which is exactly what the gate
// promises.
func TestIsRenameContention_ClassifiesOnlyTheTwoTransientWindowsErrors(t *testing.T) {
	linkErr := func(e error) error {
		return &os.LinkError{Op: "rename", Old: "tmp/obj-1", New: "objects/ab/cd/ff", Err: e}
	}

	cases := []struct {
		name        string
		err         error
		wantWindows bool
	}{
		{name: "no error is not contention", err: nil},
		{name: "bare ERROR_ACCESS_DENIED", err: winErrAccessDenied, wantWindows: true},
		{name: "bare ERROR_SHARING_VIOLATION", err: winErrSharingViolation, wantWindows: true},
		{name: "ERROR_ACCESS_DENIED inside a LinkError", err: linkErr(winErrAccessDenied), wantWindows: true},
		{name: "ERROR_SHARING_VIOLATION inside a LinkError", err: linkErr(winErrSharingViolation), wantWindows: true},
		{
			name:        "a doubly wrapped LinkError still classifies",
			err:         fmt.Errorf("store: publishing chunk abc: %w", linkErr(winErrSharingViolation)),
			wantWindows: true,
		},
		{name: "ERROR_FILE_NOT_FOUND is a real failure", err: linkErr(syscall.Errno(2))},
		{name: "ERROR_PATH_NOT_FOUND is a real failure", err: linkErr(syscall.Errno(3))},
		{name: "ERROR_DISK_FULL is a real failure", err: linkErr(syscall.Errno(112))},
		{name: "a plain error is a real failure", err: errors.New("something else went wrong")},
		{name: "os.ErrNotExist is a real failure", err: os.ErrNotExist},
		{
			name:        "a permission error with no errno still classifies",
			err:         fmt.Errorf("wrapped: %w", os.ErrPermission),
			wantWindows: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := c.wantWindows && runtime.GOOS == "windows"
			require.Equal(t, want, isRenameContention(c.err),
				"isRenameContention(%v) must be %v on %s: only the two transient Windows failures "+
					"an antivirus or indexer causes may be retried, and only on Windows",
				c.err, want, runtime.GOOS)
		})
	}
}

// TestRenameObject_ReportsANonContentionFailureImmediately asserts a rename failure that is not
// antivirus contention is returned on the first attempt rather than retried. Spinning the retry
// loop over a missing staging file would burn budget B-C and still report the same error.
func TestRenameObject_ReportsANonContentionFailureImmediately(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "published")

	err := renameObject(filepath.Join(dir, objectTmpPrefix+"never-staged"), dst)
	require.Error(t, err, "a rename with no source must fail")
	require.False(t, isRenameContention(err),
		"fixture sanity: a missing staging file is a real failure, not contention")

	_, statErr := os.Stat(paths.Long(dst))
	require.True(t, os.IsNotExist(statErr),
		"a failed publish must leave nothing at the content-addressed path, or a later read would "+
			"find an object that was never written")
}

// TestRenameObject_ExistingDestinationIsSuccess asserts a rename onto an object that is already
// there reports success. Objects are content-addressed, so a concurrent writer that got there first
// wrote byte-identical content and there is nothing to reconcile.
func TestRenameObject_ExistingDestinationIsSuccess(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "published")
	content := []byte("byte-identical content")
	require.NoError(t, os.WriteFile(paths.Long(dst), content, 0o600))

	tmp := filepath.Join(dir, objectTmpPrefix+"racing")
	require.NoError(t, os.WriteFile(paths.Long(tmp), content, 0o600))

	require.NoError(t, renameObject(tmp, dst),
		"losing the race to publish an identical object is not an error: both writers produced the "+
			"same content address from the same bytes")
	got, err := os.ReadFile(paths.Long(dst))
	require.NoError(t, err)
	require.Equal(t, content, got)
}

// TestRenameObject_ContentionIsRetriedThenReported asserts the Windows retry loop runs and then
// gives up rather than hanging or succeeding falsely.
//
// The contention is produced the way the real thing occurs: a handle is still open on the freshly
// written staging file when the rename is attempted, which is what MoveFileEx sees when an
// antivirus or search indexer is mid-scan. A lock that outlives the retries must FAIL the Put — the
// caller degrades (§12.3), the staging file is cleaned up, and the identical object is written
// again on the next attempt because the content address has not changed.
func TestRenameObject_ContentionIsRetriedThenReported(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: rename contention is a Windows-only failure mode — POSIX renames a file with open handles happily, so there is nothing to retry")
	}

	dir := t.TempDir()
	tmp := filepath.Join(dir, objectTmpPrefix+"held-open")
	dst := filepath.Join(dir, "published")

	held, err := os.Create(paths.Long(tmp))
	require.NoError(t, err)
	_, err = held.WriteString("staged object bytes")
	require.NoError(t, err)

	// The handle is deliberately STILL OPEN across the rename.
	renameErr := renameObject(tmp, dst)
	require.NoError(t, held.Close())

	require.Error(t, renameErr,
		"a rename blocked for the whole retry budget must be reported: the caller has to degrade "+
			"and re-Put rather than believe an object was published")
	require.True(t, isRenameContention(renameErr),
		"fixture sanity: an open handle must surface as one of the two transient Windows errors, "+
			"which is what the retry loop keys on (got %v)", renameErr)

	_, statErr := os.Stat(paths.Long(dst))
	require.True(t, os.IsNotExist(statErr),
		"a failed publish must leave nothing at the content-addressed path")
}

// ── objects.go / read.go: reading an object back ─────────────────────────────────────────────

// TestReadObjectFile_UnreadableCandidateIsNotFound asserts a candidate path that exists but cannot
// be read reports core.ErrNotFound rather than propagating a raw filesystem error.
//
// §12.3 requires the read path to degrade to "I cannot re-materialize this": every caller of the
// object read path keys on core.ErrNotFound, so an unwrapped I/O error would escape the degradation
// handling entirely and surface as a hard failure in a session.
func TestReadObjectFile_UnreadableCandidateIsNotFound(t *testing.T) {
	tp := newTestStore(t)
	h := core.HashBytes(core.DomainChunk, []byte("unreadable object"))

	// A directory sitting where the object file should be: present, but not readable as a file.
	blocker := tp.Store.objectCandidates(h)[0]
	require.NoError(t, os.MkdirAll(paths.Long(blocker), 0o700))

	raw, _, _, err := tp.Store.readObjectFile(h)
	require.Error(t, err)
	require.ErrorIs(t, err, core.ErrNotFound,
		"an unreadable object must degrade to core.ErrNotFound, which is the only failure the "+
			"read path's callers know how to handle")
	require.ErrorContains(t, err, "reading object",
		"the message must say the read itself failed, not that the object was absent")
	require.Nil(t, raw, "a failed read must hand back no bytes")
}

// TestGetObject_LengthDisagreementQuarantines asserts the index cross-check.
//
// zstd's per-frame checksum catches a damaged frame, but it cannot catch an object whose CONTENT is
// intact and simply is not what index/roots.jsonl says it is. Handing those bytes back would
// re-materialize a root out of the wrong content — silently. So the object is removed from service
// and the read reports core.ErrNotFound (§12.3 "store corrupt").
func TestGetObject_LengthDisagreementQuarantines(t *testing.T) {
	tp := newTestStore(t)
	payload := []byte("a chunk whose recorded length will not match")
	h := core.HashBytes(core.DomainChunk, payload)

	_, novel, err := tp.Store.putObject(h, payload)
	require.NoError(t, err)
	require.True(t, novel)

	got, err := tp.Store.getObject(h, len(payload))
	require.NoError(t, err, "fixture sanity: the honest length must read back fine")
	require.Equal(t, payload, got)

	_, err = tp.Store.getObject(h, len(payload)+1)
	require.ErrorIs(t, err, core.ErrNotFound,
		"an object that disagrees with the index about its own length must read as not-found, "+
			"never as bytes")
	require.Equal(t, int64(1), tp.counter("store.quarantined"),
		"the quarantine must be counted: §12.3 requires the corruption to be visible, not silent")

	require.False(t, tp.Store.objectExists(h),
		"the mismatched object must be moved out of objects/ so a later read cannot keep tripping "+
			"over it")
	quarantined := filepath.Join(paths.Of(tp.Root).Tmp, quarantineDir, hexOf(h)+objectSuffix)
	_, statErr := os.Stat(paths.Long(quarantined))
	require.NoError(t, statErr, "the object must be kept in tmp/quarantine so a human can inspect it")
}

// TestGetChunk_ReadsAnObjectWithNoIndexEntry asserts the crash-recovery case GetChunk documents: an
// object left on disk by a crash between its write and its index append is still readable. There is
// simply no recorded length to check it against, so the frame checksum stands alone for that one
// case.
func TestGetChunk_ReadsAnObjectWithNoIndexEntry(t *testing.T) {
	tp := newTestStore(t)
	payload := []byte("written to objects/ but never announced in index/roots.jsonl")
	h := core.HashBytes(core.DomainChunk, payload)

	_, _, err := tp.Store.putObject(h, payload)
	require.NoError(t, err)

	tp.Store.mu.RLock()
	_, known := tp.Store.chunkSet[h]
	tp.Store.mu.RUnlock()
	require.False(t, known, "fixture sanity: this chunk must have no index entry")

	got, err := tp.Store.GetChunk(context.Background(), h)
	require.NoError(t, err,
		"an orphaned object must still be readable: refusing it would discard content that is "+
			"provably intact and recoverable after a crash")
	require.Equal(t, payload, got)
}

// TestStatObject_FindsAnObjectUnderEitherName asserts statObject — objectExists's error-reporting
// sibling, used by GC's sweep — locates an object written under either candidate name and reports
// its on-disk size.
func TestStatObject_FindsAnObjectUnderEitherName(t *testing.T) {
	payload := []byte("stat probe payload, long enough to be worth compressing. " +
		"stat probe payload, long enough to be worth compressing.")
	h := core.HashBytes(core.DomainChunk, payload)

	t.Run("compressed name", func(t *testing.T) {
		tp := newTestStore(t)
		written, _, err := tp.Store.putObject(h, payload)
		require.NoError(t, err)

		fi, ok := tp.Store.statObject(h)
		require.True(t, ok, "a stored object must be found by statObject")
		require.NotNil(t, fi)
		require.Equal(t, written, fi.Size(),
			"statObject must report the COMPRESSED on-disk size, which is what GC's sweep accounts "+
				"bytes reclaimed from")
	})

	t.Run("bare name, found on the second candidate", func(t *testing.T) {
		tp := newTestStore(t, withCompressionNone())
		written, _, err := tp.Store.putObject(h, payload)
		require.NoError(t, err)
		require.Equal(t, int64(len(payload)), written, "fixture sanity: nothing is compressed here")

		fi, ok := tp.Store.statObject(h)
		require.True(t, ok,
			"an object written under the bare name must still be found: statObject tries the "+
				"compressed candidate first and must fall through to the second")
		require.Equal(t, int64(len(payload)), fi.Size())
	})

	t.Run("an object that was never stored", func(t *testing.T) {
		tp := newTestStore(t)
		fi, ok := tp.Store.statObject(core.HashBytes(core.DomainChunk, []byte("never stored")))
		require.False(t, ok, "an absent object must report absent, not an error and not a zero FileInfo")
		require.Nil(t, fi)
	})
}

// ── stats.go ─────────────────────────────────────────────────────────────────────────────────

// TestStats_RefusesAClosedStoreAndACancelledContext asserts both guards Stats runs before it takes
// any lock. Stats backs /qompack:status, which the daemon may call at any point in a shutdown.
func TestStats_RefusesAClosedStoreAndACancelledContext(t *testing.T) {
	tp := newTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tp.Store.Stats(ctx)
	require.ErrorIs(t, err, context.Canceled,
		"a cancelled context must be honoured before the index is walked")

	require.NoError(t, tp.Store.Close())
	_, err = tp.Store.Stats(context.Background())
	require.ErrorIs(t, err, core.ErrDegraded,
		"a closed store must report core.ErrDegraded rather than reading a released index")
}

// TestStats_SketchesCountsRootsCarryingASignature asserts Stats.Sketches reports the one number
// this package can actually answer for.
//
// The bloom, count-min and HLL sketches Stats.Sketches is documented around are the DAEMON's files;
// nothing in internal/store writes or owns them. What the store does hold is the per-root MinHash
// signature near-duplicate detection reads, so that is what is reported — a count, not a fabricated
// byte total for files this package does not own.
func TestStats_SketchesCountsRootsCarryingASignature(t *testing.T) {
	sig := sketch.Signature{Perms: 4, Mins: []uint64{11, 22, 33, 44}}
	tp := newTestStore(t, withCanon(canonWithSignature(sig)))
	ctx := context.Background()

	st, err := tp.Store.Stats(ctx)
	require.NoError(t, err)
	require.Zero(t, st.Sketches[sketchSignatureKey], "an empty store carries no signatures")

	for _, p := range []string{"src/a.ts", "src/b.ts", "src/c.ts"} {
		_, err := tp.Store.PutBytes(ctx, []byte("body of "+p+"\n"), PutOptions{Tool: "FileRead", Path: p})
		require.NoError(t, err)
	}

	st, err = tp.Store.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, st.Sketches[sketchSignatureKey],
		"every root whose canonicalization produced a signature must be counted, since that count "+
			"is what /qompack:status reports for this store's sketch state")
}

// TestStats_SketchesIsZeroWithoutSignatures asserts a root with no MinHash signature is not
// counted, so the number above is a real count rather than a root count wearing a different name.
//
// MinHash is switched OFF through configuration rather than by injecting a canonicalizer that
// attaches nothing: store.canonicalize.minhash.enabled is a real, supported deployment, and it is
// the only state in which the production pipeline legitimately produces a signature-free root.
func TestStats_SketchesIsZeroWithoutSignatures(t *testing.T) {
	tp := newTestStore(t, withMinHash(false, 0.9))
	ctx := context.Background()

	_, err := tp.Store.PutBytes(ctx, []byte("a root with no signature\n"), PutOptions{Path: "src/a.ts"})
	require.NoError(t, err)

	st, err := tp.Store.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, st.Objects, "fixture sanity: the root really was stored")
	require.Zero(t, st.Sketches[sketchSignatureKey],
		"a root carrying no signature must not be counted as one")
}

// TestSegmentCount_NoSegmentLogIsZero asserts the nil guard on the segment log.
//
// It is reachable rather than theoretical: openFS assigns s.seg last, so every failure before that
// point leaves an FSStore whose seg is nil, and releaseWriters nil-checks it for the same reason. A
// Stats call on such a value must report zero segments rather than dereferencing nothing.
func TestSegmentCount_NoSegmentLogIsZero(t *testing.T) {
	var bare FSStore
	require.NotPanics(t, func() { _ = bare.segmentCount() })
	require.Zero(t, bare.segmentCount(),
		"a store with no segment log holds no segments; reading one must not panic")
}
