package dag

import "sort"

// rebuildIndexLocked recomputes every position index from scratch and clears idxDirty.
//
// Three structures come out of it:
//
//   - posIdx: the live node ids in (Pos, Turn, ID) order. That triple is a TOTAL order, which is
//     what makes NodesAfter's output stable across platforms and Go versions — the goldens depend
//     on it, and a map-iteration-order tiebreak would quietly make them flaky.
//   - posVals: the matching Pos values, ascending, so NodesAfter binary-searches plain ints
//     instead of doing a map lookup per probe.
//   - lo/hi: for each live, non-dangling edge, the lower and the higher of its two endpoints'
//     positions. They are sorted ascending INDEPENDENTLY of each other: they are two multisets,
//     not a list of pairs, because CrossingEdges needs two counts and never needs to know which
//     lo belongs to which hi.
//
// The caller must already hold g.mu for writing. Compact calls this directly; every read path
// reaches it through withIndex.
func (g *graph) rebuildIndexLocked() {
	g.posIdx = g.posIdx[:0]
	for id := range g.nodes {
		if g.dead[id] {
			continue
		}
		g.posIdx = append(g.posIdx, id)
	}
	sort.Slice(g.posIdx, func(i, j int) bool {
		a, b := g.nodes[g.posIdx[i]], g.nodes[g.posIdx[j]]
		switch {
		case a.Pos != b.Pos:
			return a.Pos < b.Pos
		case a.Turn != b.Turn:
			return a.Turn < b.Turn
		default:
			return g.posIdx[i] < g.posIdx[j]
		}
	})

	g.posVals = g.posVals[:0]
	for _, id := range g.posIdx {
		g.posVals = append(g.posVals, g.nodes[id].Pos)
	}

	g.lo = g.lo[:0]
	g.hi = g.hi[:0]
	for _, e := range g.edges {
		from, okFrom := g.nodes[e.From]
		to, okTo := g.nodes[e.To]
		if !okFrom || !okTo || g.dead[e.From] || g.dead[e.To] {
			continue // dangling or tombstoned: it couples nothing that still exists (D-6)
		}
		lo, hi := from.Pos, to.Pos
		if lo > hi {
			lo, hi = hi, lo
		}
		g.lo = append(g.lo, lo)
		g.hi = append(g.hi, hi)
	}
	sort.Ints(g.lo)
	sort.Ints(g.hi)

	g.idxDirty = false
}

// withIndex runs fn with the position index guaranteed clean, under whichever lock is sufficient.
//
// sync.RWMutex has no upgrade path — taking Lock while already holding RLock in the same goroutine
// deadlocks — so a dirty index can never be rebuilt in place under the read lock. There are two
// paths instead:
//
//   - Clean index (the overwhelmingly common case, since a scheduler pass reads far more often
//     than the observer writes): take RLock, run fn, done. This is the path the sub-5µs
//     CrossingEdges budget is measured on.
//   - Dirty index: take the WRITE lock, rebuild, and run fn while still holding it.
//
// Running fn under the write lock in the second case is the important detail. The obvious
// alternative — rebuild under the write lock, release it, then re-acquire the read lock and
// re-check — is correct but pathological: a writer landing in the gap re-dirties the index, the
// reader rebuilds again, and with several hot writers a reader can pay many O(N log N) rebuilds for
// one query. That is not a theoretical concern; it was measured, and it turned
// TestConcurrentMutationAndRead from seconds into minutes. Doing fn's (read-only) work under the
// write lock costs a little concurrency on the rare dirty path and buys a hard guarantee of at most
// one rebuild per call.
//
// Code that already holds the write lock — Compact — must call rebuildIndexLocked directly and must
// NOT call this.
func (g *graph) withIndex(fn func()) {
	g.mu.RLock()
	if !g.idxDirty {
		fn()
		g.mu.RUnlock()
		return
	}
	g.mu.RUnlock()

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.idxDirty {
		g.rebuildIndexLocked()
	}
	fn()
}

// CrossingEdges is segment_coupling(pos) of Qompack.md §8.4: the number of edges whose endpoints
// straddle token position pos, which is the cheap, direct measure of how much the post-pos region
// depends on pre-pos detail.
//
// An edge straddles pos exactly when lo < pos <= hi. Because lo <= hi always holds, an edge with
// hi < pos necessarily also has lo < pos, so
//
//	crossing(pos) = #(lo < pos) - #(hi < pos)
//
// and each term is one binary search over an already-sorted slice. That O(log E) cost is what lets
// SP-12 score twenty candidate cut points inside a single scheduler pass for free.
//
// Positions at or below 0 report 0: nothing can lie before the start of the prefix. A pos beyond
// every endpoint also reports 0, because both terms then equal the edge count.
func (g *graph) CrossingEdges(pos int) int {
	if pos <= 0 {
		return 0
	}
	n := 0
	g.withIndex(func() {
		if g.closed {
			return
		}
		// sort.SearchInts returns the smallest index whose element is >= pos, which for an
		// ascending slice is exactly the count of elements strictly less than pos.
		n = sort.SearchInts(g.lo, pos) - sort.SearchInts(g.hi, pos)
	})
	return n
}

// NodesAfter returns every live node whose Pos is at or beyond pos, in posIdx order, as a freshly
// allocated slice.
//
// This is the suffix-constrained candidate set of §5.3 and §5.5: an arbitrary subset of a cached
// prefix is a worst-case edit, so the only nodes selection may legally consider are the ones after
// the chosen cut point. SP-12 hands this result to analyzer as that set.
func (g *graph) NodesAfter(pos int) []Node {
	var out []Node
	g.withIndex(func() {
		if g.closed {
			return
		}
		i := sort.SearchInts(g.posVals, pos)
		if i >= len(g.posIdx) {
			return
		}
		out = make([]Node, 0, len(g.posIdx)-i)
		for _, id := range g.posIdx[i:] {
			out = append(out, g.nodes[id])
		}
	})
	return out
}
