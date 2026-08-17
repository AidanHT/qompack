package store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/symbols"
)

// searchSeed is one stored tool result a search test ranks against.
type searchSeed struct {
	id      core.ToolUseID
	tool    string
	path    string
	tsDelta time.Duration
	content []byte
}

// seedSearch stores each seed's content and records the tool_use entry that makes it a search
// candidate. Search ranks tool_use records, so content alone is invisible to it.
func seedSearch(t *testing.T, tp *testProject, seeds []searchSeed) map[core.ToolUseID]PutResult {
	t.Helper()
	ctx := context.Background()
	out := make(map[core.ToolUseID]PutResult, len(seeds))

	for _, s := range seeds {
		res, err := tp.Store.PutBytes(ctx, s.content, PutOptions{Tool: s.tool, Path: s.path})
		require.NoError(t, err)
		out[s.id] = res

		ts := core.UnixMilli(tp.Clock.Now().Add(s.tsDelta).UnixMilli())
		require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
			ID: s.id, Session: "sess-search", Turn: 1, TS: ts, Tool: s.tool,
			Root: res.Root.Hash, Path: s.path, Bytes: res.Root.RawBytes, Tokens: res.Root.Tokens,
		}))
	}
	return out
}

// hitIDs projects hits down to their tool-use ids, in rank order.
func hitIDs(hits []Hit) []core.ToolUseID {
	out := make([]core.ToolUseID, len(hits))
	for i, h := range hits {
		out[i] = h.ToolUseID
	}
	return out
}

// TestSearch_ByPathExactBeatsSuffix asserts an exact path key outranks a record whose key merely
// ends with the queried path.
func TestSearch_ByPathExactBeatsSuffix(t *testing.T) {
	tp := newTestStore(t)
	seedSearch(t, tp, []searchSeed{
		{id: "tu-suffix", tool: "FileRead", path: "web/src/auth.ts", content: []byte("suffix match body\n")},
		{id: "tu-exact", tool: "FileRead", path: "src/auth.ts", content: []byte("exact match body\n")},
	})

	hits, err := tp.Store.Search(context.Background(), Query{Path: "src/auth.ts"})
	require.NoError(t, err)
	require.Len(t, hits, 2)
	require.Equal(t, core.ToolUseID("tu-exact"), hits[0].ToolUseID,
		"the exact path key must outrank a suffix match")
	require.Greater(t, hits[0].Score, hits[1].Score)
}

// TestSearch_ByText asserts free-text ranking by occurrence count, that a zero-occurrence record is
// dropped, and that the returned span really contains the match — which is what makes
// OpenSpan(root, Span[0], Span[1]-Span[0]) the minimal sufficient span of §8.7.
func TestSearch_ByText(t *testing.T) {
	tp := newTestStore(t)
	filler := strings.Repeat("padding line that is not interesting at all\n", 40)
	once := []byte(filler + "the needle appears here\n" + filler)
	four := []byte(filler + strings.Repeat("needle line\n", 4) + filler)
	none := []byte(filler + "nothing of interest\n" + filler)

	seedSearch(t, tp, []searchSeed{
		{id: "tu-once", tool: "FileRead", path: "a.txt", content: once},
		{id: "tu-four", tool: "FileRead", path: "b.txt", content: four},
		{id: "tu-none", tool: "FileRead", path: "c.txt", content: none},
	})

	hits, err := tp.Store.Search(context.Background(), Query{Text: "needle"})
	require.NoError(t, err)
	require.Len(t, hits, 2, "the record with no occurrences must not be returned")
	require.Equal(t, core.ToolUseID("tu-four"), hits[0].ToolUseID,
		"four occurrences must outrank one")

	for _, h := range hits {
		require.Less(t, h.Span[0], h.Span[1], "a text hit must report a non-empty span")
		rc, err := tp.Store.OpenSpan(context.Background(), h.Root, h.Span[0], h.Span[1]-h.Span[0])
		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, rc.Close())
		require.NoError(t, err)
		require.Contains(t, string(got), "needle",
			"the reported span must actually contain the match it scored")
	}
}

// TestSearch_BySymbol asserts an exact symbol match reports the symbol's own extent as the span.
func TestSearch_BySymbol(t *testing.T) {
	const (
		symOffset = 812
		symLen    = 240
	)
	tp := newTestStore(t, withSymbols(fakeSymbols{
		syms: []symbols.Symbol{{Name: "refreshToken", Kind: "func", Offset: symOffset, Len: symLen}},
	}))
	seedSearch(t, tp, []searchSeed{
		{id: "tu-sym", tool: "FileRead", path: "src/auth.ts", content: bytes.Repeat([]byte("x"), 4096)},
	})

	hits, err := tp.Store.Search(context.Background(), Query{Symbol: "refreshToken"})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, [2]int64{symOffset, symOffset + symLen}, hits[0].Span)
	require.GreaterOrEqual(t, hits[0].Score, wSymbolExact, "an exact symbol match must score wSymbolExact")
}

// TestSearch_ToolFilterAndSince asserts the tool and Since filters both narrow the candidate set.
func TestSearch_ToolFilterAndSince(t *testing.T) {
	tp := newTestStore(t)
	base := tp.Clock.Now()
	seedSearch(t, tp, []searchSeed{
		{id: "tu-read-old", tool: "FileRead", path: "a.txt", tsDelta: 0, content: []byte("read old\n")},
		{id: "tu-read-new", tool: "FileRead", path: "b.txt", tsDelta: time.Hour, content: []byte("read new\n")},
		{id: "tu-grep-old", tool: "Grep", path: "c.txt", tsDelta: 0, content: []byte("grep old\n")},
		{id: "tu-grep-new", tool: "Grep", path: "d.txt", tsDelta: time.Hour, content: []byte("grep new\n")},
	})

	hits, err := tp.Store.Search(context.Background(), Query{Tool: "FileRead"})
	require.NoError(t, err)
	require.ElementsMatch(t, []core.ToolUseID{"tu-read-old", "tu-read-new"}, hitIDs(hits))

	hits, err = tp.Store.Search(context.Background(), Query{Since: base.Add(time.Minute)})
	require.NoError(t, err)
	require.ElementsMatch(t, []core.ToolUseID{"tu-read-new", "tu-grep-new"}, hitIDs(hits))

	hits, err = tp.Store.Search(context.Background(), Query{Tool: "grep", Since: base.Add(time.Minute)})
	require.NoError(t, err)
	require.Equal(t, []core.ToolUseID{"tu-grep-new"}, hitIDs(hits), "the tool filter is case-insensitive")
}

// TestSearch_DefaultKIsFive pins the `recall(query, k=5)` default of Qompack.md §8.7.
func TestSearch_DefaultKIsFive(t *testing.T) {
	tp := newTestStore(t)
	seeds := make([]searchSeed, 0, 20)
	for i := 0; i < 20; i++ {
		seeds = append(seeds, searchSeed{
			id:      core.ToolUseID(fmt.Sprintf("tu-%02d", i)),
			tool:    "FileRead",
			path:    fmt.Sprintf("src/file%02d.ts", i),
			tsDelta: time.Duration(i) * time.Minute,
			content: []byte(fmt.Sprintf("body number %d\n", i)),
		})
	}
	seedSearch(t, tp, seeds)

	hits, err := tp.Store.Search(context.Background(), Query{Path: "src", K: 0})
	require.NoError(t, err)
	require.Len(t, hits, defaultK)

	hits, err = tp.Store.Search(context.Background(), Query{Path: "src", K: maxK + 1000})
	require.NoError(t, err)
	require.LessOrEqual(t, len(hits), maxK)
}

// TestSearch_Deterministic asserts repeated identical queries produce identical orderings, which is
// what lets a golden pin a recall result.
func TestSearch_Deterministic(t *testing.T) {
	tp := newTestStore(t)
	seeds := make([]searchSeed, 0, 12)
	for i := 0; i < 12; i++ {
		seeds = append(seeds, searchSeed{
			id:      core.ToolUseID(fmt.Sprintf("tu-%02d", i)),
			tool:    "FileRead",
			path:    fmt.Sprintf("src/mod%02d/auth.ts", i%3),
			content: []byte(strings.Repeat("shared body\n", 8)),
		})
	}
	seedSearch(t, tp, seeds)

	want, err := tp.Store.Search(context.Background(), Query{Path: "auth.ts", K: maxK})
	require.NoError(t, err)
	require.NotEmpty(t, want)
	for i := 0; i < 20; i++ {
		got, err := tp.Store.Search(context.Background(), Query{Path: "auth.ts", K: maxK})
		require.NoError(t, err)
		require.Equal(t, hitIDs(want), hitIDs(got), "run %d reordered an identical query", i)
	}
}

// TestSearch_TruncatesWithoutError asserts an over-large candidate set is truncated and counted
// rather than reported as a failure: retrieval is the path a post-compaction session depends on, so
// a partial ranked answer beats an error.
func TestSearch_TruncatesWithoutError(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < maxCandidates+88; i++ {
		body := []byte(fmt.Sprintf("candidate body %d\n", i))
		res, err := tp.Store.PutBytes(ctx, body, PutOptions{Tool: "FileRead", Path: fmt.Sprintf("src/f%04d.ts", i)})
		require.NoError(t, err)
		require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
			ID: core.ToolUseID(fmt.Sprintf("tu-%04d", i)), Session: "sess-trunc", Turn: core.TurnIndex(i),
			TS: core.UnixMilli(int64(i)), Tool: "FileRead", Root: res.Root.Hash,
			Path: fmt.Sprintf("src/f%04d.ts", i),
		}))
	}

	hits, err := tp.Store.Search(ctx, Query{Path: "src", K: maxK})
	require.NoError(t, err, "truncation must never surface as an error")
	require.NotEmpty(t, hits, "a truncated search must still return ranked hits")
	require.Equal(t, int64(1), tp.counter("store.search.truncated"))
}

// TestSearch_SummaryIsFirstNonBlankLine asserts the summary skips leading blank lines and stays
// inside the ≤120-byte preview bound.
func TestSearch_SummaryIsFirstNonBlankLine(t *testing.T) {
	tp := newTestStore(t)
	body := "\n\nthe first real line carries the summary\nand this one does not\n"
	seedSearch(t, tp, []searchSeed{
		{id: "tu-sum", tool: "FileRead", path: "src/sum.ts", content: []byte(body)},
	})

	hits, err := tp.Store.Search(context.Background(), Query{Text: "summary"})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "the first real line carries the summary", hits[0].Summary)
	require.LessOrEqual(t, len(hits[0].Summary), argsPreviewMax)
}

// TestSearch_SummaryTruncatesOnRuneBoundary asserts a long first line is cut to the preview bound
// without splitting a multi-byte rune.
func TestSearch_SummaryTruncatesOnRuneBoundary(t *testing.T) {
	tp := newTestStore(t)
	body := strings.Repeat("é", 200) + "\nneedle\n"
	seedSearch(t, tp, []searchSeed{
		{id: "tu-long", tool: "FileRead", path: "src/long.ts", content: []byte(body)},
	})

	hits, err := tp.Store.Search(context.Background(), Query{Text: "é"})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.LessOrEqual(t, len(hits[0].Summary), argsPreviewMax)
	require.True(t, strings.HasSuffix(hits[0].Summary, previewEllipsis))
	require.True(t, utf8ValidString(hits[0].Summary), "a truncated summary must stay valid UTF-8")
}

// utf8ValidString reports whether s contains no replacement runes from a split encoding.
func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// TestSearch_ClosedStoreDegrades asserts Search reports core.ErrDegraded after Close.
func TestSearch_ClosedStoreDegrades(t *testing.T) {
	tp := newTestStore(t)
	require.NoError(t, tp.Store.Close())
	_, err := tp.Store.Search(context.Background(), Query{Text: "anything"})
	require.ErrorIs(t, err, core.ErrDegraded)
}

// BenchmarkSearch_1000Roots measures a text search over 1 000 roots against the ≤25 ms budget that
// keeps `recall` inside B-F.
//
// It injects a 4 KiB chunker rather than using the package's default test chunker. That is not
// tuning the benchmark to pass: the test chunker averages ~256 B chunks ON PURPOSE, so a 23 KB
// fixture yields enough chunks for boundary resynchronization to be observable, and a text search
// pays one file read per chunk. Measuring against it would report a chunk-size artefact sixteen
// times removed from the 4 KiB target store.chunk.target actually configures.
func BenchmarkSearch_1000Roots(b *testing.B) {
	t := &testing.T{}
	tp := newTestStore(t, func(_ *config.Config, d *Deps) { d.Chunker = fixedChunker{size: 4096} })
	ctx := context.Background()
	body := []byte(strings.Repeat("the quick brown fox jumps over the lazy dog\n", 190)) // ~8 KB each
	for i := 0; i < 1000; i++ {
		payload := append(append([]byte{}, body...), []byte(fmt.Sprintf("unique tail %d\n", i))...)
		res, err := tp.Store.PutBytes(ctx, payload, PutOptions{Tool: "FileRead", Path: fmt.Sprintf("src/f%04d.ts", i)})
		if err != nil {
			b.Fatal(err)
		}
		if err := tp.Store.RecordToolUse(ctx, ToolUseRecord{
			ID: core.ToolUseID(fmt.Sprintf("tu-%04d", i)), Session: "sess-bench",
			TS: core.UnixMilli(int64(i)), Tool: "FileRead", Root: res.Root.Hash,
			Path: fmt.Sprintf("src/f%04d.ts", i),
		}); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tp.Store.Search(ctx, Query{Text: "lazy dog", K: 10}); err != nil {
			b.Fatal(err)
		}
	}
}
