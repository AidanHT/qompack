package dag

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// The package's own validation failures. They are deliberately NOT core sentinels: a malformed
// node is a caller bug in this process, not one of the four cross-package conditions
// (ErrNotFound, ErrBudget, ErrDegraded, ErrNotImplemented) that every conformance suite probes for.
var (
	// ErrInvalidNode is returned by AddNode when a node fails validation. Nothing is mutated.
	ErrInvalidNode = errors.New("dag: invalid node")
	// ErrInvalidEdge is returned by AddEdge when an edge fails validation. Nothing is mutated.
	ErrInvalidEdge = errors.New("dag: invalid edge")
	// ErrClosed is returned by every mutating and slicing operation once the graph has marked
	// itself closed. Only Compact's post-rewrite adjacency consistency check sets that flag: it
	// means the in-memory adjacency no longer describes the edge slice, so no answer this graph
	// gives can be trusted and refusing is the only honest response.
	ErrClosed = errors.New("dag: graph closed")
)

// Graph is the dependence DAG (00-ARCHITECTURE.md §5.9): the append-only record of nodes and
// edges backing both compaction selection (backward slicing, p-selection via Node.Pos) and
// retrieval (forward slicing, shared-file/shared-symbol expansion).
type Graph interface {
	AddNode(n Node) error
	AddEdge(e Edge) error
	// Node looks up id, reporting false if it is not present.
	Node(id NodeID) (Node, bool)
	// Out returns every edge leaving id.
	Out(id NodeID) []Edge
	// In returns every edge entering id.
	In(id NodeID) []Edge
	// BackwardSlice walks backward from criteria, scoring and ordering the visited nodes.
	BackwardSlice(criteria []NodeID, o SliceOptions) (Slice, error)
	// ForwardSlice walks forward from criteria, scoring and ordering the visited nodes.
	ForwardSlice(criteria []NodeID, o SliceOptions) (Slice, error)
	// CrossingEdges is segment_coupling(pos): the number of edges whose endpoints straddle token
	// position pos (00-ARCHITECTURE.md §5.9).
	CrossingEdges(pos int) int
	// NodesAfter returns every node whose Pos is at or beyond pos.
	NodesAfter(pos int) []Node
	// Flush appends any buffered nodes/edges to dag/deps.jsonl.
	Flush(ctx context.Context) error
	// Compact rewrites the on-disk log, dropping GC'd nodes (idle only).
	Compact(ctx context.Context) error
	// Stats summarizes the graph's current state.
	Stats() GraphStats
}

// Maintainer is the maintenance half of the concrete graph Open returns. It is deliberately NOT
// folded into Graph so that every existing consumer — including SP-01's compiling stub — keeps
// satisfying the §5.9 interface unchanged (§0's amendment rule: additions, never changes).
// SP-05 and SP-12 reach it by type assertion:
//
//	if m, ok := g.(dag.Maintainer); ok { … }
type Maintainer interface {
	// Tombstone marks ids dead. They vanish immediately from Node, Out, In, NodesAfter,
	// CrossingEdges and every slice; their storage is reclaimed only by Compact.
	Tombstone(ids []NodeID) error
	// NeedsCompaction reports whether the log has accumulated enough waste to be worth rewriting.
	NeedsCompaction() bool
	// Generation is the compaction generation; 0 means never compacted.
	Generation() int
	// SetClock swaps the clock used for tombstone and generation timestamps (§4: every package
	// that observes time takes a core.Clock). It is what makes deps.jsonl goldens reproducible.
	SetClock(c core.Clock)
}

// recKind is the "type" discriminator of one dag/deps.jsonl line. The node and edge values are
// bound to the constants node.go and edge.go already declare, because those two line shapes are
// frozen by testdata/golden/contracts/dag/want/{node_line,edge_line}.jsonl (Rule W-2) and must
// never drift from what Node.MarshalJSON and Edge.MarshalJSON actually emit. The tombstone and
// generation kinds are additions this subplan owns; no fixture freezes them.
type recKind string

const (
	recNode recKind = nodeLineType
	recEdge recKind = edgeLineType
	recTomb recKind = "tombstone"
	recGen  recKind = "generation"
)

// record is the in-memory pending-write unit: one line that Flush will append. Which fields are
// meaningful depends on kind — node for recNode, edge for recEdge, id and ts for recTomb, gen and
// ts for recGen — so this is a tagged union rather than four separate queues, which keeps the
// append order across all four kinds exactly the order the mutations happened in.
type record struct {
	kind recKind
	node Node
	edge Edge
	id   NodeID
	ts   core.UnixMilli
	gen  int
}

// edgeKey is the deduplication identity of an edge: §8.1 item 4's shared-state edges are
// re-derived on every observation of the same path, so without this an observer that reads
// src/auth.ts two hundred times would append two hundred identical shared-file edges and the log
// would stop being linear in the session's real content.
type edgeKey struct {
	from, to NodeID
	kind     EdgeKind
}

// autoFlushRecords is how many pending records may accumulate before AddNode/AddEdge append them
// without being asked. It bounds the memory an observer that never calls Flush can pin, and it is
// the same threshold Compact uses as its minimum log size, because a log smaller than one
// auto-flush batch cannot have accumulated enough waste to be worth rewriting.
const autoFlushRecords = 2000

// compactWasteDivisor sets Compact's threshold: the log is worth rewriting once wasted records
// (tombstoned nodes plus dangling edges) reach one quarter of it.
const compactWasteDivisor = 4

// graph is the concrete Graph. It is safe for concurrent use by multiple goroutines: the daemon
// runs a worker pool alongside an idle worker, so reads and mutations genuinely overlap.
type graph struct {
	// mu guards every field below it. Node, Out, In, Stats, BackwardSlice, ForwardSlice,
	// CrossingEdges and NodesAfter take it for reading; AddNode, AddEdge, Tombstone, Flush and
	// Compact take it for writing. It is never upgraded from read to write — see withIndex.
	mu sync.RWMutex

	root    string
	logPath string
	log     logging.Logger
	cfg     config.Config
	clock   core.Clock

	nodes   map[NodeID]Node
	edges   []Edge
	out     map[NodeID][]int // node -> indexes into edges
	in      map[NodeID][]int // node -> indexes into edges
	edgeIdx map[edgeKey]int  // dedup identity -> index into edges
	dead    map[NodeID]bool  // tombstoned

	// The position indexes, rebuilt lazily. posIdx holds live node ids in (Pos, Turn, ID) order
	// and posVals the matching Pos values, so NodesAfter is one binary search over ints rather
	// than a map lookup per probe. lo and hi hold, for each live non-dangling edge, the lower and
	// higher of its two endpoints' positions — each sorted ascending INDEPENDENTLY, because
	// CrossingEdges needs two counts, not the pairs.
	posIdx   []NodeID
	posVals  []int
	lo, hi   []int
	idxDirty bool

	pending []record
	gen     int
	stats   GraphStats
	closed  bool
}

// Compile-time proof that the concrete graph satisfies both halves of its contract. SP-05 and
// SP-12 type-assert Open's result to Maintainer, and a silent failure there would surface as a
// disabled idle compaction rather than as a build error.
var (
	_ Graph      = (*graph)(nil)
	_ Maintainer = (*graph)(nil)
)

// newGraph returns an empty in-memory graph rooted at root, with no log loaded. Open layers
// persistence on top of it.
func newGraph(root string, cfg config.Config, log logging.Logger) *graph {
	if log == nil {
		log = logging.Nop()
	}
	return &graph{
		root:    root,
		logPath: filepath.Join(paths.Of(root).DAG, depsLogName),
		log:     log,
		cfg:     cfg,
		clock:   core.SystemClock(),
		nodes:   make(map[NodeID]Node),
		out:     make(map[NodeID][]int),
		in:      make(map[NodeID][]int),
		edgeIdx: make(map[edgeKey]int),
		dead:    make(map[NodeID]bool),
	}
}

// depsLogName is the file §7.4's directory layout names: "dag/deps.jsonl # dependence edges for
// slicing".
const depsLogName = "deps.jsonl"

// AddNode inserts or updates n.
//
// Validation is strict and rejects before mutating anything: the id must parse under the D-2
// scheme and its prefix must agree with n.Kind, and neither Pos nor Tokens may be negative. The
// kind/prefix cross-check is what replaces a zero-value "unset kind" test — NodeKind's zero value
// is KindToolUse rather than a sentinel, because the frozen fixture pins "kind":4 to KindFile and
// prepending a sentinel would renumber every kind (Rule W-2).
//
// An id that already exists is merged field-wise rather than replaced: a non-zero incoming field
// overwrites, a zero incoming field keeps what is stored. Ephemeral is sticky-true, because §8.7
// retrieval results stay first-eviction candidates for the rest of the session no matter who
// re-observes them.
func (g *graph) AddNode(n Node) error {
	if err := validateNode(n); err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return ErrClosed
	}

	merged := n
	if prev, ok := g.nodes[n.ID]; ok {
		merged = mergeNode(prev, n)
		if merged.Pos != prev.Pos {
			g.log.Debug("dag: node position moved", "id", string(n.ID), "from", prev.Pos, "to", merged.Pos)
		}
	}
	g.nodes[n.ID] = merged
	delete(g.dead, n.ID)
	g.idxDirty = true
	g.pending = append(g.pending, record{kind: recNode, node: merged})
	g.maybeAutoFlushLocked()
	return nil
}

// validateNode is AddNode's pure guard, factored out so it runs before the lock is taken.
func validateNode(n Node) error {
	if n.ID == "" {
		return fmt.Errorf("%w: empty id", ErrInvalidNode)
	}
	// ParseNodeID already rejects a missing colon, an unknown prefix and an empty key, so this
	// one call covers all three malformed-id cases; only the kind cross-check is separate.
	kind, _, ok := ParseNodeID(n.ID)
	if !ok {
		return fmt.Errorf("%w: id %q does not parse as \"<prefix>:<key>\"", ErrInvalidNode, string(n.ID))
	}
	if kind != n.Kind {
		return fmt.Errorf("%w: id %q implies kind %s but node declares %s",
			ErrInvalidNode, string(n.ID), kind, n.Kind)
	}
	if n.Pos < 0 {
		return fmt.Errorf("%w: negative pos %d", ErrInvalidNode, n.Pos)
	}
	if n.Tokens < 0 {
		return fmt.Errorf("%w: negative tokens %d", ErrInvalidNode, n.Tokens)
	}
	return nil
}

// mergeNode folds incoming into prev. A zero incoming field means "not supplied" and keeps prev's
// value; Ephemeral only ever ratchets true.
//
// File, symbol and segment nodes take the opposite rule for Pos and Turn: they are shared-state
// ANCHORS whose position is their FIRST appearance, so the earlier of the two wins. A file read at
// token 10,000 and re-read at token 90,000 must keep Pos=10,000 — otherwise every shared-file edge
// migrates toward the tail as the session grows and CrossingEdges(p) under-reports exactly the
// pre-p/post-p coupling §8.4 asks it to measure. (Zero still means "not supplied" here too, which
// means an anchor genuinely first seen at turn 0 must carry that turn on its first write.)
func mergeNode(prev, incoming Node) Node {
	merged := prev
	merged.Kind = incoming.Kind

	if isAnchorKind(incoming.Kind) {
		merged.Pos = earliest(prev.Pos, incoming.Pos)
		merged.Turn = core.TurnIndex(earliest(int(prev.Turn), int(incoming.Turn)))
	} else {
		if incoming.Pos != 0 {
			merged.Pos = incoming.Pos
		}
		if incoming.Turn != 0 {
			merged.Turn = incoming.Turn
		}
	}
	if incoming.TS != 0 {
		merged.TS = incoming.TS
	}
	if incoming.Ref != "" {
		merged.Ref = incoming.Ref
	}
	if incoming.Root != (core.Hash{}) {
		merged.Root = incoming.Root
	}
	if incoming.Tokens != 0 {
		merged.Tokens = incoming.Tokens
	}
	merged.Ephemeral = prev.Ephemeral || incoming.Ephemeral
	return merged
}

// isAnchorKind reports whether k is a shared-state anchor whose Pos and Turn record its first
// appearance rather than its latest one. See mergeNode.
func isAnchorKind(k NodeKind) bool {
	return k == KindFile || k == KindSymbol || k == KindSegment
}

// earliest returns the smaller of two positions, treating 0 as "not supplied".
func earliest(prev, incoming int) int {
	switch {
	case incoming == 0:
		return prev
	case prev == 0:
		return incoming
	case incoming < prev:
		return incoming
	default:
		return prev
	}
}

// AddEdge inserts e, or folds it into an identical edge already present.
//
// An edge whose endpoints are not yet nodes is accepted deliberately (D-6): the log may interleave,
// and an observer legitimately emits an edge to a file node before the file node itself. Such an
// edge is stored, reported in GraphStats.Dangling, skipped by traversal and excluded from
// CrossingEdges — and stops being dangling the moment the missing node arrives, because
// danglingness is computed, never cached.
func (g *graph) AddEdge(e Edge) error {
	normalized, err := validateEdge(e)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return ErrClosed
	}

	key := edgeKey{from: normalized.From, to: normalized.To, kind: normalized.Kind}
	if i, ok := g.edgeIdx[key]; ok {
		// Fold: the strongest weight and the earliest turn seen for this triple win.
		//
		// A record is appended only when the fold actually CHANGES the stored edge. That
		// distinction is what keeps the log linear — SP-08 re-emits a shared-file edge on every
		// read of the same path, and every one of those after the first is identical, so it costs
		// nothing — while still keeping memory and disk in agreement.
		//
		// Appending nothing at all was the original rule, and it was wrong: a second emission
		// carrying a stronger weight or an earlier turn changed the in-memory edge and left no
		// trace, so a reload produced a DIFFERENT graph from the one that had been running. Edge
		// weight is a factor in every slice score, so that divergence would have moved relevance
		// rankings across a restart with nothing to point at. A property test found it.
		changed := false
		if normalized.Weight > g.edges[i].Weight {
			g.edges[i].Weight = normalized.Weight
			changed = true
		}
		if normalized.Turn < g.edges[i].Turn {
			g.edges[i].Turn = normalized.Turn
			changed = true
		}
		if changed {
			g.pending = append(g.pending, record{kind: recEdge, edge: g.edges[i]})
			g.maybeAutoFlushLocked()
		}
		return nil
	}

	idx := len(g.edges)
	g.edges = append(g.edges, normalized)
	g.edgeIdx[key] = idx
	g.out[normalized.From] = append(g.out[normalized.From], idx)
	g.in[normalized.To] = append(g.in[normalized.To], idx)
	g.idxDirty = true
	g.pending = append(g.pending, record{kind: recEdge, edge: normalized})
	g.maybeAutoFlushLocked()
	return nil
}

// validateEdge is AddEdge's pure guard. It returns the normalized edge: a non-positive weight
// becomes 1 (an unset weight means "full strength", not "no influence") and a weight above 1 is
// clamped, so that a slice score can never grow along a hop.
func validateEdge(e Edge) (Edge, error) {
	if _, _, ok := ParseNodeID(e.From); !ok {
		return Edge{}, fmt.Errorf("%w: from %q does not parse", ErrInvalidEdge, string(e.From))
	}
	if _, _, ok := ParseNodeID(e.To); !ok {
		return Edge{}, fmt.Errorf("%w: to %q does not parse", ErrInvalidEdge, string(e.To))
	}
	if e.From == e.To {
		return Edge{}, fmt.Errorf("%w: self-loop on %q", ErrInvalidEdge, string(e.From))
	}
	if e.Kind > EdgeControlOnly {
		return Edge{}, fmt.Errorf("%w: kind %d is not a real edge kind", ErrInvalidEdge, uint8(e.Kind))
	}
	w := float64(e.Weight)
	if math.IsNaN(w) || math.IsInf(w, 0) {
		return Edge{}, fmt.Errorf("%w: weight is not finite", ErrInvalidEdge)
	}
	if e.Weight <= 0 || e.Weight > 1 {
		e.Weight = 1
	}
	return e, nil
}

// Node looks up id. A tombstoned node reports false, exactly as an absent one does: Tombstone is
// how the store's GC says a node is gone, and a caller that could still read it would be looking
// at content the object store may already have collected.
func (g *graph) Node(id NodeID) (Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.closed || g.dead[id] {
		return Node{}, false
	}
	n, ok := g.nodes[id]
	return n, ok
}

// Out returns a freshly allocated copy of every edge leaving id, excluding edges incident to a
// tombstoned node. Dangling edges ARE returned: a caller inspecting shared state legitimately
// wants to see an edge whose far endpoint has not been observed yet.
func (g *graph) Out(id NodeID) []Edge { return g.incident(id, false) }

// In returns a freshly allocated copy of every edge entering id, under Out's rules.
func (g *graph) In(id NodeID) []Edge { return g.incident(id, true) }

// incident is the shared body of Out and In. It copies, so a caller mutating the result cannot
// reach into the graph's own storage.
func (g *graph) incident(id NodeID, backward bool) []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.closed || g.dead[id] {
		return nil
	}
	idxs := g.adjacentLocked(id, backward)
	if len(idxs) == 0 {
		return nil
	}
	edges := make([]Edge, 0, len(idxs))
	for _, i := range idxs {
		e := g.edges[i]
		if g.dead[e.From] || g.dead[e.To] {
			continue
		}
		edges = append(edges, e)
	}
	if len(edges) == 0 {
		return nil
	}
	return edges
}

// adjacentLocked returns the INTERNAL edge-index slice for id, in insertion order: the in list
// when backward, the out list otherwise. It does NOT filter — filtering would force an allocation
// on the hot traversal path, and every caller already rejects dead and dangling endpoints itself.
//
// The caller must already hold g.mu (read or write) and must not retain the returned slice.
func (g *graph) adjacentLocked(id NodeID, backward bool) []int {
	if backward {
		return g.in[id]
	}
	return g.out[id]
}

// Tombstone marks ids dead, appending one tombstone record per id so the decision survives a
// restart. Ids that are absent are recorded anyway: the store's GC may legitimately collect a root
// this graph never observed, and replaying that tombstone must still be a no-op rather than an error.
func (g *graph) Tombstone(ids []NodeID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return ErrClosed
	}
	now := core.NowMilli(g.clock)
	for _, id := range ids {
		if id == "" {
			return fmt.Errorf("%w: empty id in tombstone set", ErrInvalidNode)
		}
		g.dead[id] = true
		g.pending = append(g.pending, record{kind: recTomb, id: id, ts: now})
	}
	if len(ids) > 0 {
		g.idxDirty = true
		g.maybeAutoFlushLocked()
	}
	return nil
}

// SetClock swaps the clock used for tombstone and generation timestamps. Open installs
// core.SystemClock(); the golden generator installs a testutil.FakeClock so that deps.jsonl
// fixtures are byte-reproducible.
func (g *graph) SetClock(c core.Clock) {
	if c == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.clock = c
}

// Generation reports the compaction generation; 0 means the log has never been compacted.
func (g *graph) Generation() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.gen
}

// NeedsCompaction reports whether wasted records — tombstoned nodes plus dangling edges — have
// reached a quarter of the on-disk log, and the log is at least one auto-flush batch long. It is
// safe for the daemon's idle loop to call unconditionally, and Compact re-checks it anyway.
func (g *graph) NeedsCompaction() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.needsCompactionLocked()
}

// needsCompactionLocked is NeedsCompaction's body. The caller must already hold g.mu.
func (g *graph) needsCompactionLocked() bool {
	if g.stats.LogRecords < autoFlushRecords {
		return false
	}
	waste := len(g.dead) + g.danglingLocked()
	return waste*compactWasteDivisor >= g.stats.LogRecords
}

// danglingLocked counts edges at least one of whose endpoints is absent or tombstoned. The caller
// must already hold g.mu.
func (g *graph) danglingLocked() int {
	n := 0
	for _, e := range g.edges {
		if !g.liveLocked(e.From) || !g.liveLocked(e.To) {
			n++
		}
	}
	return n
}

// liveLocked reports whether id names a node that is present and not tombstoned. The caller must
// already hold g.mu.
func (g *graph) liveLocked(id NodeID) bool {
	if g.dead[id] {
		return false
	}
	_, ok := g.nodes[id]
	return ok
}

// Stats recomputes the live counters and carries the load-time ones through unchanged.
//
// A closed graph returns its last snapshot rather than recomputing: closed means the in-memory
// adjacency no longer describes the edge slice, so a freshly derived count would be a number this
// graph has no standing to report.
func (g *graph) Stats() GraphStats {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.closed {
		return g.stats
	}

	s := g.stats
	s.NodesByKind = make(map[string]int)
	s.EdgesByKind = make(map[string]int)
	s.Nodes = 0
	s.MaxPos = 0
	for id, n := range g.nodes {
		if g.dead[id] {
			continue
		}
		s.Nodes++
		s.NodesByKind[n.Kind.String()]++
		if n.Pos > s.MaxPos {
			s.MaxPos = n.Pos
		}
	}
	s.Edges = len(g.edges)
	for _, e := range g.edges {
		s.EdgesByKind[e.Kind.String()]++
	}
	s.Tombstoned = len(g.dead)
	s.Dangling = g.danglingLocked()
	s.PendingRecords = len(g.pending)
	s.Generation = g.gen
	s.NeedsCompaction = g.needsCompactionLocked()
	return s
}

// setClosedForTest drives the graph into the state Compact's consistency check produces, so that
// the ErrClosed contract can be tested without corrupting a real graph to get there. It is
// unexported and has no production caller.
func (g *graph) setClosedForTest() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
}

// maybeAutoFlushLocked appends the pending records once they reach autoFlushRecords, so that an
// observer which never calls Flush still bounds the memory it pins.
//
// It must call flushLocked and never the exported Flush: the caller already holds the write lock,
// and sync.RWMutex is not re-entrant, so reaching for the exported method here would deadlock
// against itself on the very first auto-flush. A failure is logged and swallowed — a mutation must
// not fail because the disk hiccuped, and the records stay in memory for the next attempt.
//
// The caller must already hold g.mu for writing.
func (g *graph) maybeAutoFlushLocked() {
	if len(g.pending) < autoFlushRecords {
		return
	}
	if err := g.flushLocked(context.Background()); err != nil {
		g.log.Warn("dag: auto-flush failed, records retained in memory",
			"err", err, "pending", len(g.pending))
	}
}

// Open, Flush and flushLocked live in log.go, and Compact in compact.go: this file is the
// in-memory graph and its contracts, and the two persistence halves are large enough — and
// independent enough of each other — to read on their own.
