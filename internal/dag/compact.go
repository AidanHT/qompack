package dag

import (
	"bytes"
	"context"
	"fmt"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// This file holds the ONE place in this package that does not append.
//
// §3.3 enforces append-only mechanically on three locations and only three: checkpoints/, pins/,
// and sketches/tried.bloom. paths.IsProtected names exactly those, and every write path in
// internal/paths refuses to replace anything under them. dag/deps.jsonl is deliberately NOT in
// that set: it is append-only in normal operation — every mutation reaches it through
// paths.AppendOnly in log.go — with Compact as the single sanctioned exception.
// 00-ARCHITECTURE.md §5.9 names that exception outright in the Graph contract:
// "rewrite the log dropping GC'd nodes (idle only)".
//
// Two invariants make the exception safe rather than merely permitted.
//
// The rewrite is derived ONLY from in-memory state that was itself loaded from this same log. It
// never reads a checkpoint, a summary, or any other derived artifact, so §4.6's
// never-compress-a-compression rule is untouched: nothing here re-summarizes a summary, and a
// compacted log is a strict subset of the records that produced it, not a digest of them.
//
// It is idle-only. Compaction walks every node and every edge and rewrites the whole file, which is
// exactly the work §8.4's O3 says belongs on user think-time and nowhere near a hook. Compact must
// never be called from a hot-path hook; SP-12 registers it as the "compact_dag" idle task, and the
// gate below makes calling it unconditionally from an idle loop safe.

// logFilePerm is deps.jsonl's mode. It matches the mode paths.AppendOnly creates the file with, so
// the file a compaction rewrites is indistinguishable from the file appends created.
const logFilePerm = 0o600

// Compact rewrites dag/deps.jsonl as a single new generation containing only live state.
//
// The sequence, and why it is this sequence:
//
//  1. The write lock is held for the WHOLE call. A concurrent AddEdge landing between the moment
//     the buffer is materialized and the moment the file is replaced would be written into memory
//     and then dropped from disk by the rewrite that was already in flight.
//  2. Pending records are flushed FIRST — through flushLocked, never the exported Flush, which
//     would deadlock against the lock this call already holds. The rewrite is materialized from
//     memory, so an unflushed record would still reach the new file, but it would ALSO still be
//     queued afterwards and the next flush would append a duplicate of a record the new generation
//     already contains.
//  3. Below the waste threshold this returns nil — a no-op, NOT an error — so SP-12's idle loop can
//     call it every tick without first asking whether it is worth doing.
//  4. Tombstoned nodes and the edges they orphan are simply not written. That is the entire point:
//     Tombstone hides a node immediately, and this is what finally reclaims its bytes.
//  5. The replacement goes through paths.WriteAtomic, so a crash mid-rewrite leaves either the old
//     file or the new one, never a half-written log.
//
// The result is a file that starts with a generation header and contains every live node in
// position order followed by every live edge in insertion order — a log that reloads into exactly
// the graph that wrote it.
func (g *graph) Compact(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return ErrClosed
	}

	if len(g.pending) > 0 {
		if err := g.flushLocked(ctx); err != nil {
			return err
		}
	}
	if !g.needsCompactionLocked() {
		return nil
	}
	if g.idxDirty {
		// Called directly, not through withIndex: the write lock is already held, and withIndex
		// takes the read lock first, which would deadlock this goroutine against itself.
		g.rebuildIndexLocked()
	}

	// posIdx is already the live nodes in (Pos, Turn, ID) order — a total order, so the rewritten
	// file is byte-reproducible from the same graph on any platform.
	liveNodes := make([]Node, 0, len(g.posIdx))
	for _, id := range g.posIdx {
		liveNodes = append(liveNodes, g.nodes[id])
	}
	// Insertion order for edges, which is the order a replay must see them in for the dedup fold
	// (strongest weight, earliest turn) to reproduce the same edge.
	keptEdges := make([]Edge, 0, len(g.edges))
	for _, e := range g.edges {
		if !g.liveLocked(e.From) || !g.liveLocked(e.To) {
			continue // tombstoned or dangling: it couples nothing that still exists (D-6)
		}
		keptEdges = append(keptEdges, e)
	}

	now := core.NowMilli(g.clock)
	buf, err := g.materializeLocked(g.gen+1, now, liveNodes, keptEdges)
	if err != nil {
		g.log.Loud("dag: cannot materialize a compacted dependence log; the old log is untouched",
			"path", g.logPath, "err", err)
		return err
	}

	// The last point at which nothing has been written. An idle task is cancelled the moment the
	// user types, and abandoning here costs only the CPU already spent.
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := paths.WriteAtomic(g.logPath, buf.Bytes(), logFilePerm); err != nil {
		g.log.Loud("dag: failed to rewrite the dependence log; the old log is untouched",
			"path", g.logPath, "err", err)
		return fmt.Errorf("dag: compact %s: %w", g.logPath, err)
	}

	g.adoptGenerationLocked(liveNodes, keptEdges, buf.Len(), now)
	return g.verifyAdjacencyLocked()
}

// materializeLocked renders one whole generation into a buffer: the header first, so a reader meets
// the schema version and the generation number before anything else, then every live node, then
// every live edge. Tombstones are not carried forward — a compacted log has nothing left to
// reclaim, and re-emitting them would make the next NeedsCompaction see waste that no longer
// exists.
//
// The caller must already hold g.mu for writing.
func (g *graph) materializeLocked(gen int, ts core.UnixMilli, nodes []Node, edges []Edge) (*bytes.Buffer, error) {
	var buf bytes.Buffer

	header, err := encodeGeneration(gen, ts, len(nodes), len(edges))
	if err != nil {
		return nil, fmt.Errorf("dag: compact %s: encode generation header: %w", g.logPath, err)
	}
	buf.Write(header)
	buf.WriteByte(recordNewline)

	for _, n := range nodes {
		line, err := encodeRecord(record{kind: recNode, node: n})
		if err != nil {
			return nil, fmt.Errorf("dag: compact %s: %w", g.logPath, err)
		}
		buf.Write(line)
		buf.WriteByte(recordNewline)
	}
	for _, e := range edges {
		line, err := encodeRecord(record{kind: recEdge, edge: e})
		if err != nil {
			return nil, fmt.Errorf("dag: compact %s: %w", g.logPath, err)
		}
		buf.Write(line)
		buf.WriteByte(recordNewline)
	}
	return &buf, nil
}

// adoptGenerationLocked makes the in-memory graph agree with the file just written: the dead set is
// empty, the node map holds only what survived, and the adjacency is re-indexed against the
// compacted edge slice.
//
// Rebuilding rather than patching is deliberate. Every out/in entry is an INDEX into g.edges, so
// dropping any edge invalidates every index after it; repairing them in place is the kind of
// arithmetic that is correct until the first time it is not, and the failure mode — an adjacency
// list pointing at the wrong edge — is silent. Rebuilding from the kept slice cannot be wrong by
// construction, and verifyAdjacencyLocked checks it anyway.
//
// The caller must already hold g.mu for writing.
func (g *graph) adoptGenerationLocked(nodes []Node, edges []Edge, logBytes int, at core.UnixMilli) {
	g.nodes = make(map[NodeID]Node, len(nodes))
	for _, n := range nodes {
		g.nodes[n.ID] = n
	}
	g.dead = make(map[NodeID]bool)

	g.edges = edges
	g.out = make(map[NodeID][]int, len(g.out))
	g.in = make(map[NodeID][]int, len(g.in))
	g.edgeIdx = make(map[edgeKey]int, len(edges))
	for i, e := range edges {
		g.out[e.From] = append(g.out[e.From], i)
		g.in[e.To] = append(g.in[e.To], i)
		g.edgeIdx[edgeKey{from: e.From, to: e.To, kind: e.Kind}] = i
	}

	g.idxDirty = true
	g.gen++
	g.stats.LogRecords = 1 + len(nodes) + len(edges) // the generation header is a record too
	g.stats.LogBytes = int64(logBytes)
	g.stats.LastCompaction = at
}

// verifyAdjacencyLocked is the post-rewrite consistency check, and the ONLY producer of the closed
// state.
//
// Every stored edge must appear exactly once in some out list and exactly once in some in list, so
// the two sums must both equal the edge count. If they do not, the adjacency no longer describes
// the edge slice: Out and In would hand back edges that are not there, slicing would traverse
// relationships that do not exist, and CrossingEdges would report a coupling number the scheduler
// would then cut a segment on. None of that is recoverable by retrying, and none of it announces
// itself — so the graph refuses to answer anything at all from here on, loudly (§13 invariant 10).
//
// The caller must already hold g.mu for writing.
func (g *graph) verifyAdjacencyLocked() error {
	outDegree, inDegree := 0, 0
	for _, idxs := range g.out {
		outDegree += len(idxs)
	}
	for _, idxs := range g.in {
		inDegree += len(idxs)
	}
	if outDegree == len(g.edges) && inDegree == len(g.edges) {
		return nil
	}

	g.closed = true
	g.log.Loud("dag: adjacency is inconsistent after compaction; the graph is closed",
		"path", g.logPath, "out_degree", outDegree, "in_degree", inDegree, "edges", len(g.edges))
	return fmt.Errorf("dag: compact %s: adjacency describes %d out and %d in entries for %d edges: %w",
		g.logPath, outDegree, inDegree, len(g.edges), ErrClosed)
}
