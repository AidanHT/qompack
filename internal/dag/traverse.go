package dag

import (
	"container/heap"
	"sort"
	"time"

	"github.com/qompack/qompack/internal/core"
)

// This file is the scored backward and forward walk of 00-ARCHITECTURE.md §6.4 — "recency is a
// proxy, slicing is relevance" — expressed as a best-first (max-product) relaxation:
//
//	score(c) = 1.0 for every criterion c that exists and is live
//	score(v) = max over edges e=(u→v) reachable in the traversal direction of
//	               score(u) · Decay · e.Kind.Multiplier() · e.Weight
//
// Every factor lies in (0,1] — AddEdge normalizes Weight into that range and D-3's multipliers are
// all at or below 1.00 — so a score is non-increasing along any path. A max-heap keyed on the
// tentative score therefore finalizes each node exactly once with its true maximum: this is
// Dijkstra with multiplication in place of addition, at O((V+E)·log V).
//
// The heap is not an optimization, it is the definition. A plain FIFO queue would finalize whatever
// path it happened to reach first, so a node sitting one weak supersedes hop from a criterion
// (0.30) and two strong produces hops away (1.00 each) would score 0.255 instead of 0.7225 — the
// answer would depend on the order an observer happened to append its edges, which is not an
// answer at all.
//
// NO SELECTION AUTHORITY (§8.3, and the package comment in doc.go). What comes back is a score per
// node, never a keep-set: legal input to ranking inside a checkpoint or rehydration budget, and not
// authority to drop anything from the live context.

// scoreHint caps how much map the walk preallocates. MaxNodes may legitimately be set to a million
// by a caller that wants "no cap in practice" — §8.4's idle precomputation does exactly that,
// leaning on the deadline instead — and make(map, 1e6) would allocate tens of megabytes for a slice
// that returns thirty nodes. 512 is outside both of §11.6's forbidden literal sets.
const scoreHint = 512

// minScore is the traversal floor: a hop that would score below it is not taken. It is what stops a
// long chain of weak edges from walking the entire graph to report values no consumer can act on —
// a chain of sequence edges decays by 0.85·0.60 = 0.51 per hop and crosses this floor after
// thirteen of them.
//
// Reaching the floor is emphatically NOT a truncation. Slice.Truncated means an answer was
// withheld; the floor means the walk arrived at irrelevance, which is a complete answer to the
// question §6.4 actually asks.
const minScore float32 = 1e-4

// deadlineCheckStride is how many finalized nodes pass between wall-clock checks. Calling
// time.Now() once per node would cost more than the heap operation it guards; checking every 256
// bounds the overrun at a few microseconds of work, which is well inside the 5 ms
// DefaultDeadline it exists to enforce.
const deadlineCheckStride = 256

// heapItem is one tentative visit: a node, the best score any path found for it so far, the hop
// depth that path took, and the node's turn index (carried along so the ordering below never has to
// reach back into the graph's node map while the heap is being sifted).
type heapItem struct {
	id    NodeID
	score float32
	depth int
	turn  core.TurnIndex
}

// maxHeap is the priority queue the walk finalizes nodes from: highest score first.
type maxHeap struct {
	items []heapItem
}

// newMaxHeap returns an empty max-heap.
func newMaxHeap() *maxHeap { return &maxHeap{} }

// Len implements sort.Interface for heap.Interface.
func (h *maxHeap) Len() int { return len(h.items) }

// Less orders the queue: higher score first, ties by the LOWER turn, remaining ties by NodeID
// ascending.
//
// The tiebreak is not cosmetic. It is what makes the set of nodes that survives a MaxNodes cap
// identical on every platform and every Go version — two nodes with the same score are otherwise
// separated by nothing but the order a map happened to be iterated in, and the goldens that pin
// slice output would drift from run to run.
func (h *maxHeap) Less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	if a.score != b.score {
		return a.score > b.score
	}
	if a.turn != b.turn {
		return a.turn < b.turn
	}
	return a.id < b.id
}

// Swap implements sort.Interface for heap.Interface.
func (h *maxHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

// Push implements heap.Interface. It is reached only through heap.Push, which this package only
// ever calls with a heapItem, so the comma-ok form is defensive rather than meaningful: a bare type
// assertion would trip the repository's errcheck configuration (check-type-assertions) on a branch
// that cannot be taken.
func (h *maxHeap) Push(x any) {
	if it, ok := x.(heapItem); ok {
		h.items = append(h.items, it)
	}
}

// Pop implements heap.Interface, handing back the item heap.Pop has already sifted to the end of
// the slice. Callers use the typed pop wrapper below instead.
func (h *maxHeap) Pop() any {
	last := len(h.items) - 1
	it := h.items[last]
	h.items = h.items[:last]
	return it
}

// push queues one tentative visit.
func (h *maxHeap) push(it heapItem) { heap.Push(h, it) }

// pop removes and returns the highest-scoring queued item. It reads the maximum from the front of
// the slice before delegating to heap.Pop, which keeps the any-typed return value of the
// container/heap contract off the walk's hot path entirely.
func (h *maxHeap) pop() heapItem {
	top := h.items[0]
	heap.Pop(h)
	return top
}

// hasUnfinalized reports whether any still-queued item names a node that is absent from done.
//
// This is what separates "the cap was reached" from "an answer was withheld". A graph with exactly
// MaxNodes reachable nodes yields a COMPLETE slice even though the cap was touched, and a consumer
// told otherwise would re-run a walk that has nothing left to find. The queue at that moment may
// still hold stale duplicates of nodes already finalized through a better path, which is exactly
// why the question has to be asked of the ids rather than of the queue's length.
//
// One linear pass over the backing slice, run at most once per slice call.
func (h *maxHeap) hasUnfinalized(done map[NodeID]float32) bool {
	for _, it := range h.items {
		if _, ok := done[it.id]; !ok {
			return true
		}
	}
	return false
}

// slice is the shared body of BackwardSlice and ForwardSlice; backward selects which adjacency list
// each hop follows.
//
// D-1: every edge points from earlier/producer to later/consumer, with no exceptions, so walking
// against the arrows (the In lists) answers "what did this depend on" and walking with them (the
// Out lists) answers "what depended on this". adjacentLocked hands back the right index list for
// each; it does not filter, so this loop rejects dead and absent endpoints itself.
//
// D-4: thin slicing drops EdgeControlOnly and nothing else. EdgeSequence survives and simply
// carries its low 0.60 multiplier, because §6.4's claim is that adjacency is weak evidence, not
// that it is no evidence.
//
// D-6: an edge may name an endpoint no node record has arrived for yet — the log interleaves, and
// an observer legitimately emits an edge before the node it points at. Such a hop is skipped rather
// than invented, and starts working the moment the missing node shows up.
//
// The only non-nil error this can return is ErrClosed. An unknown criterion, an empty criterion
// list and a wholly dangling neighbourhood are ordinary answers: an empty, valid, complete Slice.
func (g *graph) slice(criteria []NodeID, o SliceOptions, backward bool) (Slice, error) {
	o = o.withDefaults()

	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.closed {
		return Slice{}, ErrClosed
	}

	hint := o.MaxNodes
	if hint > scoreHint {
		hint = scoreHint
	}
	out := Slice{Scores: make(map[NodeID]float32, hint)}

	h := newMaxHeap()
	for _, c := range criteria {
		n, ok := g.nodes[c]
		if !ok || g.dead[c] {
			continue // an unknown or tombstoned criterion is skipped, never an error
		}
		h.push(heapItem{id: c, score: 1, depth: 0, turn: n.Turn})
	}
	if h.Len() == 0 {
		return out, nil
	}

	var deadline time.Time
	if o.Deadline > 0 {
		deadline = time.Now().Add(o.Deadline)
	}

	for h.Len() > 0 {
		it := h.pop()
		if _, done := out.Scores[it.id]; done {
			continue // already finalized through a better path
		}
		out.Scores[it.id] = it.score
		out.Visited++ // Visited counts nodes FINALIZED, not nodes queued

		if len(out.Scores) >= o.MaxNodes {
			out.Truncated = h.hasUnfinalized(out.Scores)
			break
		}
		if o.Deadline > 0 && out.Visited%deadlineCheckStride == 0 && time.Now().After(deadline) {
			out.Truncated = true
			break
		}
		// A node at the depth limit is scored but not expanded: the caller asked for a bounded
		// neighbourhood and got exactly one, so this is not a truncation either.
		if o.MaxDepth > 0 && it.depth >= o.MaxDepth {
			continue
		}

		for _, ei := range g.adjacentLocked(it.id, backward) {
			e := g.edges[ei]
			if o.Thin && e.Kind == EdgeControlOnly {
				continue
			}
			// Counted AFTER the thin filter and before any other test, so EdgesVisited is exactly
			// the set of edges this walk followed: for a thin walk the non-control-only edges of
			// its finalized nodes, for a full walk every edge of its own. A thin walk finalizes a
			// subset of the nodes a full walk does — it only ever removes edges, so every thin
			// path is also a full path and every full score is at least the thin one, which is
			// what keeps the minScore floor from reversing the containment — so its edge set is
			// a subset too. slice_compare_test.go asserts both halves of that.
			out.EdgesVisited++
			next := e.To
			if backward {
				next = e.From
			}
			n, ok := g.nodes[next]
			if !ok || g.dead[next] {
				continue
			}
			s := it.score * o.Decay * e.Kind.Multiplier() * e.Weight
			if s < minScore {
				continue
			}
			if _, done := out.Scores[next]; done {
				continue // finalized already, and no later path can beat a finalized score
			}
			h.push(heapItem{id: next, score: s, depth: it.depth + 1, turn: n.Turn})
		}
	}

	out.Order = orderByScore(out.Scores, g.nodes)
	return out, nil
}

// BackwardSlice walks backward from criteria — against the arrows, over the In lists (D-1) —
// scoring and ordering every node it reaches. It answers §6.4's question "what does this depend
// on", which is what compaction selection ranks a checkpoint's evidence with (§8.5).
//
// The result is scores, never a keep/drop decision (§8.3). An unknown criterion is skipped rather
// than refused; the only error is ErrClosed.
func (g *graph) BackwardSlice(criteria []NodeID, o SliceOptions) (Slice, error) {
	return g.slice(criteria, o, true)
}

// ForwardSlice walks forward from criteria — with the arrows, over the Out lists (D-1) — under
// BackwardSlice's rules in every other respect. It answers "what depended on this", which is what
// §8.7 retrieval expands a hit set with.
func (g *graph) ForwardSlice(criteria []NodeID, o SliceOptions) (Slice, error) {
	return g.slice(criteria, o, false)
}

// orderByScore renders a finalized score map as the descending order §5.9 promises: highest score
// first, ties broken by the LOWER Turn, remaining ties by NodeID ascending.
//
// The comparator is a total order — NodeID is unique within the map — so the result does not depend
// on the (randomized) order the keys were materialized in. sort.SliceStable rather than sort.Slice
// is belt and braces on top of that: the goldens pin this sequence byte-for-byte, and a slice
// order that varied by platform or Go version would make them unreproducible.
func orderByScore(scores map[NodeID]float32, nodes map[NodeID]Node) []NodeID {
	if len(scores) == 0 {
		return nil
	}

	// The keys are materialized BEFORE sorting, and that is a performance decision, not a style
	// one. A comparator that reads scores[a], scores[b] and nodes[a].Turn, nodes[b].Turn does four
	// map lookups per comparison on ~30-byte string keys, and a sort does O(n log n) comparisons:
	// for a 566-node slice that is roughly twenty thousand string-map lookups, which measured at
	// ~1.9ms — an order of magnitude more than the walk that produced the scores. Reading each
	// node's turn once, here, makes it n lookups instead.
	keys := make([]orderKey, 0, len(scores))
	for id, score := range scores {
		keys = append(keys, orderKey{id: id, score: score, turn: nodes[id].Turn})
	}

	// sort.Slice rather than sort.SliceStable: the comparator below is a TOTAL order, because
	// NodeID is unique within the map and breaks every remaining tie. Stability would therefore
	// change nothing about the output while costing extra moves, and the byte-reproducibility the
	// goldens depend on comes from the total order, not from the sort's stability.
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		switch {
		case a.score != b.score:
			return a.score > b.score
		case a.turn != b.turn:
			return a.turn < b.turn
		default:
			return a.id < b.id
		}
	})

	order := make([]NodeID, len(keys))
	for i, k := range keys {
		order[i] = k.id
	}
	return order
}

// orderKey is one node's sort key, materialized once per slice so orderByScore's comparator never
// touches a map.
type orderKey struct {
	id    NodeID
	score float32
	turn  core.TurnIndex
}
