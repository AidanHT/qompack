package store

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/symbols"
)

// The scoring weights backing the `recall` retrieval tool (Qompack.md §8.7).
//
// They are a fixed ranking policy, not a tunable: no Appendix C key configures them, and changing
// one silently reorders every recall result a session has learned to expect. The path terms
// dominate because a path is an exact statement of intent, while a text hit is evidence that
// accumulates — hence the logarithmic occurrence term, which saturates rather than letting one
// pathological file with a thousand matches outrank the file the caller actually named.
const (
	wPathExact    = 1.00
	wPathSuffix   = 0.70
	wPathContains = 0.45
	wTool         = 0.20
	wTextUnit     = 0.25 // per doubling of occurrences
	wSymbolExact  = 1.00
	wSymbolRefU   = 0.20
	wSymbolRefCap = 0.80
	wRecency      = 0.12
)

// The bounds that keep one Search inside budget B-F (MCP request to response).
//
// maxCandidates and maxScanBytes are a truncation, not a failure: retrieval is the path a
// post-compaction session depends on, so a partial ranked answer always beats an error.
const (
	maxCandidates = 512
	maxScanBytes  = 32 << 20
	maxK          = 100
	// defaultK is the `recall(query, k=5)` default of Qompack.md §8.7.
	defaultK = 5
)

// Search ranks stored tool results against q, backing the `recall` retrieval tool.
//
// Hit.Span is the minimum sufficient span (§8.7): the symbol's own extent for a symbol hit, the
// first text occurrence widened outward to its enclosing chunk boundaries for a text hit, and the
// whole root otherwise. That is what makes OpenSpan(root, Span[0], Span[1]-Span[0]) return "the
// matching function or hunk, not the file" without the caller having to compute anything.
func (s *FSStore) Search(ctx context.Context, q Query) ([]Hit, error) {
	if err := s.use(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	k := q.K
	switch {
	case k <= 0:
		k = defaultK
	case k > maxK:
		k = maxK
	}

	cands := s.candidates(q)
	if len(cands) == 0 {
		return nil, nil
	}

	qp := storeKey(q.Path)
	needContent := q.Text != "" || q.Symbol != ""

	// Bound the work BEFORE doing any of it. Both limits are computed from the in-memory index —
	// CanonBytes is already known per root — so truncation costs no I/O and is deterministic
	// rather than dependent on how far a timer got.
	limit, truncated := scanLimit(cands, needContent)
	if truncated {
		s.count("store.search.truncated", 1)
	}

	bodies := s.materializeAll(ctx, cands[:limit], needContent)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	hits := make([]Hit, 0, limit)
	for i := 0; i < limit; i++ {
		body := bodies[i]
		if needContent && body.err != nil {
			continue // a quarantined or missing object simply cannot be ranked
		}
		hit, ok := s.score(q, qp, cands[i], body.content, body.bounds, recencyRank(i, len(cands)))
		if !ok {
			continue
		}
		hits = append(hits, hit)
	}

	sortHits(hits)
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// scanLimit returns how many candidates may be examined, and whether anything was cut.
//
// The byte budget is charged from each root's recorded CanonBytes rather than from bytes actually
// read, so the decision needs no I/O and the same store answers the same query the same way every
// time — which is what lets a golden pin a recall result.
func scanLimit(cands []searchCand, needContent bool) (limit int, truncated bool) {
	limit = len(cands)
	if limit > maxCandidates {
		limit, truncated = maxCandidates, true
	}
	if !needContent {
		return limit, truncated
	}
	var budget int64
	for i := 0; i < limit; i++ {
		budget += cands[i].root.Root.CanonBytes
		if budget > maxScanBytes {
			return i, true
		}
	}
	return limit, truncated
}

// materializedBody is one candidate's decompressed content plus its chunk-end offsets.
type materializedBody struct {
	content []byte
	bounds  []int64
	err     error
}

// searchFetchers bounds how many candidate bodies are decompressed at once.
const searchFetchers = 8

// materializeAll decompresses every candidate body, concurrently.
//
// Concurrency is the difference between meeting budget B-F and missing it by an order of magnitude:
// ranking itself costs well under a millisecond over a thousand roots, and essentially the whole
// cost of a text query is one file read plus one zstd decode PER CHUNK. Those reads are
// independent and content-addressed — object reads take no lock at all — so they parallelize
// cleanly. Results are written into a position-indexed slice and scored sequentially afterwards, so
// the ranking stays exactly as deterministic as a serial scan.
func (s *FSStore) materializeAll(ctx context.Context, cands []searchCand, needContent bool) []materializedBody {
	out := make([]materializedBody, len(cands))
	if !needContent || len(cands) == 0 {
		return out
	}

	workers := searchFetchers
	if workers > len(cands) {
		workers = len(cands)
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= len(cands) || ctx.Err() != nil {
					return
				}
				content, bounds, err := s.materialize(cands[i].root)
				out[i] = materializedBody{content: content, bounds: bounds, err: err}
			}
		}()
	}
	wg.Wait()
	return out
}

// recencyRank maps a candidate's position in newest-first order onto [0, 1], 1 being the newest.
func recencyRank(i, n int) float64 {
	if n <= 0 {
		return 0
	}
	return 1 - float64(i)/float64(n)
}

// searchCand is one candidate tool-use record plus the values scoring needs repeatedly.
type searchCand struct {
	rec  ToolUseRecord
	key  string
	root *rootEntry
}

// candidates returns the tool-use records q could match, newest first.
//
// The path filter applies exactly the three predicates the scoring block grades — equality, a
// path-segment suffix, and containment — so no candidate can survive the filter and then score
// zero on the path term, which would let an unrelated record ride into the results on its recency
// bonus alone.
func (s *FSStore) candidates(q Query) []searchCand {
	qp := storeKey(q.Path)
	tool := strings.ToLower(q.Tool)
	var since core.UnixMilli
	if !q.Since.IsZero() {
		since = core.UnixMilli(q.Since.UnixMilli())
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]searchCand, 0, len(s.toolUse))
	for _, rec := range s.toolUse {
		if tool != "" && strings.ToLower(rec.Tool) != tool {
			continue
		}
		if since != 0 && rec.TS < since {
			continue
		}
		key := storeKey(rec.Path)
		if qp != "" && !pathMatches(key, qp) {
			continue
		}
		entry, ok := s.rootIndex[rec.Root]
		if !ok {
			continue // the root was tombstoned by GC; there is nothing to return
		}
		out = append(out, searchCand{rec: *rec, key: key, root: entry})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].rec.TS != out[j].rec.TS {
			return out[i].rec.TS > out[j].rec.TS
		}
		return out[i].rec.ID < out[j].rec.ID
	})
	return out
}

// pathMatches reports whether key satisfies any of the three path predicates for query key qp.
func pathMatches(key, qp string) bool {
	return key == qp || strings.HasSuffix(key, "/"+qp) || strings.Contains(key, qp)
}

// score grades one candidate, reporting false when it does not match at all.
func (s *FSStore) score(q Query, qp string, c searchCand, content []byte, bounds []int64, rank float64) (Hit, bool) {
	total := c.root.Root.CanonBytes
	span := [2]int64{0, total}
	score := 0.0

	switch {
	case qp == "":
		// A query with no path term contributes nothing here.
	case c.key == qp:
		score += wPathExact
	case strings.HasSuffix(c.key, "/"+qp):
		score += wPathSuffix
	case strings.Contains(c.key, qp):
		score += wPathContains
	}
	if q.Tool != "" {
		score += wTool
	}

	if q.Text != "" {
		occ := countFold(content, q.Text)
		if occ == 0 && score == 0 {
			return Hit{}, false
		}
		if occ > 0 {
			score += math.Min(1.0, wTextUnit*math.Log2(1+float64(occ)))
			if at := indexFold(content, q.Text); at >= 0 {
				span = widenToChunks(bounds, int64(at), int64(at+len(q.Text)), total)
			}
		}
	}

	if q.Symbol != "" {
		syms := s.deps.Symbols.Extract(c.rec.Path, content)
		if sym, ok := exactSymbol(syms, q.Symbol); ok {
			score += wSymbolExact
			span = clampSpan(int64(sym.Offset), int64(sym.Offset+sym.Len), total)
		} else if n := s.deps.Symbols.References(content, []string{q.Symbol})[q.Symbol]; n > 0 {
			score += math.Min(wSymbolRefCap, wSymbolRefU*float64(n))
		} else if score == 0 {
			return Hit{}, false
		}
	}

	return Hit{
		Root:      c.rec.Root,
		ToolUseID: c.rec.ID,
		Path:      c.rec.Path,
		Tool:      c.rec.Tool,
		TS:        c.rec.TS,
		Score:     score + wRecency*rank,
		Summary:   summarize(content, span),
		Span:      span,
	}, true
}

// sortHits orders hits by score descending, then timestamp descending, then tool-use id ascending.
// The last key makes the order TOTAL, which is what lets a golden pin it.
func sortHits(hits []Hit) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].TS != hits[j].TS {
			return hits[i].TS > hits[j].TS
		}
		return hits[i].ToolUseID < hits[j].ToolUseID
	})
}

// exactSymbol finds the symbol named name.
func exactSymbol(syms []symbols.Symbol, name string) (symbols.Symbol, bool) {
	for _, s := range syms {
		if s.Name == name {
			return s, true
		}
	}
	return symbols.Symbol{}, false
}

// materialize decompresses a root's whole content and returns it with each chunk's END offset, so
// a span can be widened outward to chunk boundaries.
func (s *FSStore) materialize(entry *rootEntry) ([]byte, []int64, error) {
	chunks := entry.Root.Chunks
	buf := make([]byte, 0, entry.Root.CanonBytes)
	bounds := make([]int64, 0, len(chunks))
	for _, c := range chunks {
		plain, err := s.getObject(c.Hash, c.Len)
		if err != nil {
			return nil, nil, err
		}
		buf = append(buf, plain...)
		bounds = append(bounds, int64(len(buf)))
	}
	return buf, bounds, nil
}

// widenToChunks expands [lo, hi) outward to the boundaries of the chunks it intersects.
//
// Widening OUTWARD rather than returning the bare match is what makes the span independently
// useful: a chunk boundary falls at a content-defined position, so the widened span is a
// self-contained region of the file rather than a match with its first and last lines sheared off.
func widenToChunks(bounds []int64, lo, hi, total int64) [2]int64 {
	if len(bounds) == 0 {
		return clampSpan(lo, hi, total)
	}
	start := int64(0)
	for _, end := range bounds {
		if lo < end {
			break
		}
		start = end
	}
	finish := total
	for _, end := range bounds {
		if hi <= end {
			finish = end
			break
		}
	}
	return clampSpan(start, finish, total)
}

// clampSpan bounds [lo, hi) to [0, total] and keeps it non-inverted.
func clampSpan(lo, hi, total int64) [2]int64 {
	if lo < 0 {
		lo = 0
	}
	if hi > total {
		hi = total
	}
	if hi < lo {
		hi = lo
	}
	return [2]int64{lo, hi}
}

// summarize returns the first non-blank line of the span, control characters stripped, truncated on
// a rune boundary to argsPreviewMax.
func summarize(content []byte, span [2]int64) string {
	if len(content) == 0 {
		return ""
	}
	lo, hi := span[0], span[1]
	if lo < 0 || lo > int64(len(content)) {
		lo = 0
	}
	if hi > int64(len(content)) || hi <= lo {
		hi = int64(len(content))
	}
	for _, line := range strings.Split(string(content[lo:hi]), "\n") {
		clean := strings.TrimSpace(stripControl(line))
		if clean != "" {
			return truncateRunes(clean, argsPreviewMax)
		}
	}
	return ""
}

// stripControl removes C0 control characters and DEL, keeping everything printable.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// truncateRunes shortens s to at most max BYTES, cutting on a rune boundary and appending an
// ellipsis when it actually cut.
func truncateRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	limit := max - len(previewEllipsis)
	if limit < 0 {
		limit = 0
	}
	cut := 0
	for i := range s {
		if i > limit {
			break
		}
		cut = i
	}
	return s[:cut] + previewEllipsis
}

// lowerASCII folds one ASCII byte to lower case, leaving every other byte alone.
func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// foldEqualAt reports whether hay[at:at+len(needle)] equals needle, ASCII-case-insensitively.
func foldEqualAt(hay []byte, at int, needle string) bool {
	for i := 0; i < len(needle); i++ {
		if lowerASCII(hay[at+i]) != lowerASCII(needle[i]) {
			return false
		}
	}
	return true
}

// countFold counts non-overlapping, ASCII-case-insensitive occurrences of needle in hay.
func countFold(hay []byte, needle string) int {
	if needle == "" || len(hay) < len(needle) {
		return 0
	}
	n := 0
	for i := 0; i+len(needle) <= len(hay); {
		if foldEqualAt(hay, i, needle) {
			n++
			i += len(needle)
			continue
		}
		i++
	}
	return n
}

// indexFold returns the first ASCII-case-insensitive occurrence of needle in hay, or -1.
func indexFold(hay []byte, needle string) int {
	if needle == "" || len(hay) < len(needle) {
		return -1
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		if foldEqualAt(hay, i, needle) {
			return i
		}
	}
	return -1
}
