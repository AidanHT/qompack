package dag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// The compaction fixture's shape. compactPairs tool_use/file pairs plus one edge each puts the log
// comfortably past autoFlushRecords, and compactTombstoned of those files are then marked dead —
// enough waste (dead nodes plus the edges they orphan) to clear the one-quarter threshold
// needsCompactionLocked applies, which is what makes Compact do anything at all.
const (
	compactPairs      = 1200
	compactTombstoned = 700
)

// compactToolID and compactFileID name the two node families the compaction fixture builds.
func compactToolID(i int) NodeID { return NodeID(fmt.Sprintf("tooluse:toolu_%05d", i)) }
func compactFileID(i int) NodeID { return NodeID(fmt.Sprintf("file:src/f%05d.ts", i)) }

// compactFixture builds a flushed graph that is genuinely worth compacting: compactPairs
// tool_use → file pairs with one consumes edge each, of which compactTombstoned files are
// tombstoned. It returns the graph with an empty pending queue, so a test can add its own
// unflushed records afterwards and know exactly which ones they are.
func compactFixture(t *testing.T, root string, log logging.Logger) *graph {
	t.Helper()
	g := logOpen(t, root, log)
	g.SetClock(testutil.NewFakeClock(testutil.Epoch))

	for i := range compactPairs {
		use, file := compactToolID(i), compactFileID(i)
		require.NoError(t, g.AddNode(Node{
			ID: use, Kind: KindToolUse, Turn: core.TurnIndex(i), Pos: i * 10, Ref: "Read", Tokens: 10,
		}))
		require.NoError(t, g.AddNode(Node{
			ID: file, Kind: KindFile, Turn: core.TurnIndex(i), Pos: i*10 + 5,
			Ref: fmt.Sprintf("src/f%05d.ts", i), Tokens: 100,
		}))
		require.NoError(t, g.AddEdge(Edge{From: use, To: file, Kind: EdgeConsumes, Weight: 1, Turn: core.TurnIndex(i)}))
	}

	dead := make([]NodeID, 0, compactTombstoned)
	for i := range compactTombstoned {
		dead = append(dead, compactFileID(i))
	}
	require.NoError(t, g.Tombstone(dead))
	require.NoError(t, g.Flush(context.Background()))
	require.Equal(t, 0, g.Stats().PendingRecords)
	require.True(t, g.NeedsCompaction(), "the fixture must actually be worth compacting")
	return g
}

// compactFirstLine decodes deps.jsonl's first line into a generic map, which is how a test can
// assert the generation header's shape without depending on the internal record struct.
func compactFirstLine(t *testing.T, root string) map[string]any {
	t.Helper()
	line, _, found := bytes.Cut(logReadFile(t, root), []byte{'\n'})
	require.True(t, found, "the compacted log must be newline-terminated")
	var out map[string]any
	require.NoError(t, json.Unmarshal(line, &out))
	return out
}

// TestCompactDropsTombstoned (test 48) asserts the whole point of the idle-only rewrite
// (00-ARCHITECTURE.md §5.9: "rewrite the log dropping GC'd nodes (idle only)"): tombstoned nodes
// and the edges they orphan leave the file entirely, the new file announces itself with a
// generation header, and the graph's own bookkeeping agrees that nothing is waiting to be
// reclaimed any more.
func TestCompactDropsTombstoned(t *testing.T) {
	root := t.TempDir()
	g := compactFixture(t, root, logging.Nop())
	before := g.Stats()
	require.Equal(t, compactTombstoned, before.Tombstoned)
	require.GreaterOrEqual(t, before.LogRecords, 2400)

	require.NoError(t, g.Compact(context.Background()))

	header := compactFirstLine(t, root)
	require.Equal(t, "generation", header["type"], "the rewritten log must start with its generation header")
	require.InDelta(t, float64(1), header["gen"], 0, "the first compaction produces generation 1")
	require.InDelta(t, float64(1), header["v"], 0, "the generation line is the one record carrying a schema version")

	// Spot-check the raw bytes (a substring scan per id over the whole file is quadratic, so a
	// sample is taken here and the exhaustive check is made against the reloaded graph below).
	raw := string(logReadFile(t, root))
	for i := 0; i < compactTombstoned; i += 97 {
		require.NotContains(t, raw, string(compactFileID(i)), "a tombstoned node must not survive the rewrite")
	}
	require.Contains(t, raw, string(compactFileID(compactPairs-1)), "a live node must survive the rewrite")

	after := g.Stats()
	require.Equal(t, 0, after.Tombstoned, "compaction is what reclaims tombstoned storage")
	require.Equal(t, 1, g.Generation())
	require.Equal(t, compactPairs*2-compactTombstoned, after.Nodes, "live nodes are untouched")
	require.Equal(t, compactPairs-compactTombstoned, after.Edges, "the orphaned edges go with their nodes")
	require.Equal(t, 0, after.Dangling)
	require.Equal(t, 0, after.PendingRecords)
	require.Less(t, after.LogBytes, before.LogBytes, "the rewritten log must be smaller than the one it replaced")
	require.Equal(t, int64(len(raw)), after.LogBytes)
	require.Equal(t, logCountLines([]byte(raw)), after.LogRecords)
	require.False(t, after.NeedsCompaction, "a freshly compacted log has no waste left")

	// The rewritten log must reload as exactly the graph in memory.
	reloaded := logOpen(t, root, logging.Nop())
	require.Equal(t, after.Nodes, reloaded.Stats().Nodes)
	require.Equal(t, after.Edges, reloaded.Stats().Edges)
	require.Equal(t, 1, reloaded.Generation(), "the generation survives a restart")
	require.Equal(t, logDump(t, g), logDump(t, reloaded))
	for i := range compactTombstoned {
		_, ok := reloaded.Node(compactFileID(i))
		require.False(t, ok, "no tombstoned node may come back from the rewritten log")
	}
}

// TestCompactNoOpBelowThreshold (test 49) asserts Compact is safe for SP-12's idle loop to call
// unconditionally: below the waste threshold it is a no-op that returns nil, not an error and not
// a rewrite. Rewriting a healthy log would burn idle time and churn the file for nothing.
func TestCompactNoOpBelowThreshold(t *testing.T) {
	root := t.TempDir()
	g := logOpen(t, root, logging.Nop())
	for i := range compactPairs {
		require.NoError(t, g.AddNode(Node{ID: compactToolID(i), Kind: KindToolUse, Pos: i * 10, Ref: "Read"}))
		require.NoError(t, g.AddNode(Node{ID: compactFileID(i), Kind: KindFile, Pos: i*10 + 5, Ref: "src/f.ts"}))
	}
	dead := make([]NodeID, 0, 10)
	for i := range 10 {
		dead = append(dead, compactFileID(i))
	}
	require.NoError(t, g.Tombstone(dead))
	require.NoError(t, g.Flush(context.Background()))

	before := logReadFile(t, root)
	require.GreaterOrEqual(t, g.Stats().LogRecords, 2400)
	require.False(t, g.NeedsCompaction(), "ten tombstones out of 2 400 records is not waste worth rewriting")

	require.NoError(t, g.Compact(context.Background()), "a no-op compaction is nil, not an error")
	require.Equal(t, before, logReadFile(t, root), "the log must not be touched below the threshold")
	require.Equal(t, 0, g.Generation())
	require.Equal(t, 10, g.Stats().Tombstoned, "nothing was reclaimed, so the tombstones remain")
}

// TestCompactFlushesPendingFirst (test 50) asserts Compact flushes before it rewrites. The
// rewrite is materialized from memory, so an unflushed record would still reach the new file — but
// it would ALSO still be queued afterwards, and the next flush would append a duplicate of a
// record the new generation already contains. Draining the queue first is what stops the log from
// silently double-counting the mutations that happened closest to the compaction.
func TestCompactFlushesPendingFirst(t *testing.T) {
	root := t.TempDir()
	g := compactFixture(t, root, logging.Nop())

	late := make([]NodeID, 0, 5)
	for i := range 5 {
		id := NodeID(fmt.Sprintf("decision:dec_late%02d", i))
		require.NoError(t, g.AddNode(Node{ID: id, Kind: KindDecision, Pos: 900000 + i, Ref: "late"}))
		late = append(late, id)
	}
	require.Equal(t, 5, g.Stats().PendingRecords)

	require.NoError(t, g.Compact(context.Background()))

	require.Equal(t, 0, g.Stats().PendingRecords,
		"Compact must drain the queue, or the next flush appends records the new generation already has")
	raw := string(logReadFile(t, root))
	for _, id := range late {
		require.Contains(t, raw, string(id), "a record added just before compaction must survive it")
	}

	// The decisive check: nothing may be appended twice. A reload must see each late node once.
	require.NoError(t, g.Flush(context.Background()))
	reloaded := logOpen(t, root, logging.Nop())
	require.Equal(t, g.Stats().Nodes, reloaded.Stats().Nodes)
	require.Equal(t, logDump(t, g), logDump(t, reloaded))
}

// TestCompactPreservesSliceAnswers (test 51) is the safety property behind the rewrite: compaction
// changes the file, never the answers. It runs on a graph with NO tombstones at all — the waste
// that carries it past the gate is dangling edges (D-6), which the rewrite also drops — so any
// difference in the slice would be compaction inventing or losing a relationship rather than
// reclaiming dead storage.
func TestCompactPreservesSliceAnswers(t *testing.T) {
	const (
		nodes    = 3000
		realEdge = 1500
		dangling = 2000
	)

	root := t.TempDir()
	g := logOpen(t, root, logging.Nop())
	g.SetClock(testutil.NewFakeClock(testutil.Epoch))

	for i := range nodes {
		require.NoError(t, g.AddNode(Node{
			ID: compactToolID(i), Kind: KindToolUse, Turn: core.TurnIndex(i % 200),
			Pos: i * 7, Ref: "Read", Tokens: core.Tokens(10 + i%50),
		}))
	}
	for i := range realEdge {
		require.NoError(t, g.AddEdge(Edge{
			From: compactToolID(i), To: compactToolID(i + 1), Kind: EdgeSequence,
			Weight: 1, Turn: core.TurnIndex(i % 200),
		}))
	}
	for i := range dangling {
		require.NoError(t, g.AddEdge(Edge{
			From: compactToolID(i % nodes), To: NodeID(fmt.Sprintf("file:src/ghost%05d.ts", i)),
			Kind: EdgeSharedFile, Weight: 1,
		}))
	}
	require.NoError(t, g.Flush(context.Background()))
	require.Equal(t, 0, g.Stats().Tombstoned)
	require.True(t, g.NeedsCompaction(), "dangling edges alone must carry this graph past the gate")

	criteria := []NodeID{compactToolID(0), compactToolID(nodes / 2), compactToolID(nodes - 1)}
	forward, err := g.ForwardSlice(criteria, SliceOptions{})
	require.NoError(t, err)
	backward, err := g.BackwardSlice(criteria, SliceOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, forward.Order)
	require.NotEmpty(t, backward.Order)

	require.NoError(t, g.Compact(context.Background()))

	gotForward, err := g.ForwardSlice(criteria, SliceOptions{})
	require.NoError(t, err)
	gotBackward, err := g.BackwardSlice(criteria, SliceOptions{})
	require.NoError(t, err)
	require.Equal(t, forward.Scores, gotForward.Scores)
	require.Equal(t, forward.Order, gotForward.Order)
	require.Equal(t, backward.Scores, gotBackward.Scores)
	require.Equal(t, backward.Order, gotBackward.Order)

	// And the same again after a restart, since the whole point is that the rewritten file is a
	// faithful record of the graph, not merely that memory survived the call.
	reloaded := logOpen(t, root, logging.Nop())
	reForward, err := reloaded.ForwardSlice(criteria, SliceOptions{})
	require.NoError(t, err)
	require.Equal(t, forward.Scores, reForward.Scores)
	require.Equal(t, forward.Order, reForward.Order)
}

// TestCompactCancelled (test 52) asserts a cancelled compaction is a non-event: the old log is
// still there untouched, the error is the context's own, and the graph is still fully usable. The
// idle worker's context is cancelled the moment the user types, so this is the ordinary path, not
// an exotic one.
func TestCompactCancelled(t *testing.T) {
	root := t.TempDir()
	g := compactFixture(t, root, logging.Nop())

	before := logReadFile(t, root)
	beforeStats := g.Stats()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := g.Compact(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, before, logReadFile(t, root), "a cancelled compaction must leave the old log alone")
	require.Equal(t, 0, g.Generation(), "a cancelled compaction produces no generation")
	require.Equal(t, beforeStats.Tombstoned, g.Stats().Tombstoned)

	// Still usable: the graph was not closed, and a later compaction with a live context works.
	require.NoError(t, g.AddNode(Node{ID: "decision:dec_after", Kind: KindDecision, Pos: 999999}))
	_, err = g.BackwardSlice([]NodeID{compactFileID(compactPairs - 1)}, SliceOptions{})
	require.NoError(t, err)
	require.NoError(t, g.Compact(context.Background()))
	require.Equal(t, 1, g.Generation())
}

// TestCompactOnClosedGraph asserts the closed state is terminal for compaction too: a graph whose
// adjacency stopped describing its own edge slice must not rewrite the file it can no longer be
// trusted to describe.
func TestCompactOnClosedGraph(t *testing.T) {
	g := newTestGraph(t)
	g.setClosedForTest()
	require.ErrorIs(t, g.Compact(context.Background()), ErrClosed)
}
