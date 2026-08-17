package dag

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// The persistence tests are in-package (package dag) for the same reason every other test file
// here is: they assert on the loader's effect on the graph's own maps and counters, and Open's
// result has to be narrowed back to *graph to see them.

// logCaptured is one line a logCapture recorded: which level produced it, the message, and the
// key/value pairs. The tests assert on level and message, and read the kv pairs only to prove the
// Loud carries the diagnostics §13 invariant 10 demands (the count and the first bad offset).
type logCaptured struct {
	level string
	msg   string
	kv    []any
}

// The level names logCapture records under. They exist so an assertion reads
// c.at(logLevelLoud) rather than c.at("loud") and a typo becomes a compile error.
const (
	logLevelDebug = "debug"
	logLevelInfo  = "info"
	logLevelWarn  = "warn"
	logLevelError = "error"
	logLevelLoud  = "loud"
)

// logCapture is a logging.Logger that records every call instead of writing one.
//
// internal/logging offers Nop (which discards everything below Loud and has no backing directory)
// and New (which needs a directory and writes real files), and neither can answer "was exactly one
// Loud emitted for this Open?" — which is the assertion §13 invariant 10 turns into a test. It is
// mutex-guarded because the graph logs while holding its own write lock and a -race run has
// several goroutines reaching it in the concurrency tests.
type logCapture struct {
	mu    sync.Mutex
	lines []logCaptured
}

// logCapture must satisfy the seam every production package actually takes.
var _ logging.Logger = (*logCapture)(nil)

// With returns the receiver unchanged. A capture has no use for accumulated fields, and returning
// the same recorder is what keeps a derived logger's lines visible to the test that made it.
func (c *logCapture) With(...any) logging.Logger { return c }

func (c *logCapture) Debug(msg string, kv ...any) { c.record(logLevelDebug, msg, kv) }
func (c *logCapture) Info(msg string, kv ...any)  { c.record(logLevelInfo, msg, kv) }
func (c *logCapture) Warn(msg string, kv ...any)  { c.record(logLevelWarn, msg, kv) }
func (c *logCapture) Error(msg string, kv ...any) { c.record(logLevelError, msg, kv) }
func (c *logCapture) Loud(msg string, kv ...any)  { c.record(logLevelLoud, msg, kv) }

func (c *logCapture) record(level, msg string, kv []any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, logCaptured{level: level, msg: msg, kv: kv})
}

// at returns every line recorded at level, in order.
func (c *logCapture) at(level string) []logCaptured {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []logCaptured
	for _, l := range c.lines {
		if l.level == level {
			out = append(out, l)
		}
	}
	return out
}

// all returns every recorded line, in order.
func (c *logCapture) all() []logCaptured {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]logCaptured(nil), c.lines...)
}

// logOpen opens the graph rooted at root and narrows it back to the concrete type, so a test can
// read the maps and counters the Graph interface does not expose.
func logOpen(t *testing.T, root string, log logging.Logger) *graph {
	t.Helper()
	opened, err := Open(root, config.Defaults(), log)
	require.NoError(t, err)
	g, ok := opened.(*graph)
	require.True(t, ok, "Open must return the concrete graph")
	return g
}

// logPathOf is the deps.jsonl path Open writes under root.
func logPathOf(root string) string {
	return filepath.Join(paths.Of(root).DAG, depsLogName)
}

// logReadFile returns deps.jsonl's raw bytes, failing the test if it is absent.
func logReadFile(t *testing.T, root string) []byte {
	t.Helper()
	b, err := os.ReadFile(logPathOf(root))
	require.NoError(t, err)
	return b
}

// logInstallFixture copies one internal/dag/testdata fixture into root's deps.jsonl verbatim,
// byte for byte — the torn fixture's missing final newline is the whole point of it, so this must
// never re-terminate what it copies.
func logInstallFixture(t *testing.T, root, name string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(paths.Of(root).DAG, 0o700))
	require.NoError(t, os.WriteFile(logPathOf(root), b, 0o600))
}

// logCountLines counts newline-terminated records in b.
func logCountLines(b []byte) int { return bytes.Count(b, []byte{'\n'}) }

// logSampleGraph fills g with a small, fully-connected sample exercising all four record kinds:
// nodes of several kinds, edges of several kinds, a folded duplicate edge and a dangling edge. It
// returns the criteria a slice should be taken from.
func logSampleGraph(t *testing.T, g *graph) []NodeID {
	t.Helper()
	for i := range 12 {
		use := NodeID(fmt.Sprintf("tooluse:toolu_%02d", i))
		res := NodeID(fmt.Sprintf("toolresult:toolu_%02d", i))
		file := NodeID(fmt.Sprintf("file:src/pkg%02d.ts", i%4))
		require.NoError(t, g.AddNode(Node{
			ID: use, Kind: KindToolUse, Turn: core.TurnIndex(i), TS: core.UnixMilli(1767225480000 + i),
			Pos: i * 100, Ref: "Read", Tokens: core.Tokens(10 + i),
		}))
		require.NoError(t, g.AddNode(Node{
			ID: res, Kind: KindToolResult, Turn: core.TurnIndex(i), Pos: i*100 + 50,
			Ref: "Read", Tokens: core.Tokens(20 + i), Ephemeral: i%3 == 0,
		}))
		require.NoError(t, g.AddNode(Node{
			ID: file, Kind: KindFile, Turn: core.TurnIndex(i), Pos: i * 10,
			Ref: fmt.Sprintf("src/pkg%02d.ts", i%4), Tokens: core.Tokens(100),
		}))
		require.NoError(t, g.AddEdge(Edge{From: use, To: res, Kind: EdgeProduces, Weight: 1, Turn: core.TurnIndex(i)}))
		require.NoError(t, g.AddEdge(Edge{From: use, To: file, Kind: EdgeConsumes, Weight: 1, Turn: core.TurnIndex(i)}))
		// A second, identical triple: it must fold rather than append a second record.
		require.NoError(t, g.AddEdge(Edge{From: use, To: file, Kind: EdgeConsumes, Weight: 1, Turn: core.TurnIndex(i)}))
	}
	// One edge whose far endpoint never arrives (D-6): legal, counted as dangling, skipped by
	// traversal, and it must survive a reload as exactly the same dangling edge.
	require.NoError(t, g.AddEdge(Edge{From: "tooluse:toolu_00", To: "file:src/never-observed.ts", Kind: EdgeSharedFile, Weight: 1}))
	return []NodeID{"toolresult:toolu_05", "file:src/pkg02.ts"}
}

// logDump renders g's whole live state as sorted text: one line per live node and one per stored
// edge. Comparing two dumps is how the round-trip tests assert "the reloaded graph IS the graph
// that was written", rather than merely that its counters agree.
func logDump(t *testing.T, g *graph) []string {
	t.Helper()
	g.mu.RLock()
	defer g.mu.RUnlock()

	out := make([]string, 0, len(g.nodes)+len(g.edges))
	for id, n := range g.nodes {
		if g.dead[id] {
			continue
		}
		out = append(out, fmt.Sprintf("node %s kind=%d turn=%d ts=%d pos=%d ref=%q root=%s tokens=%d eph=%t",
			n.ID, n.Kind, n.Turn, n.TS, n.Pos, n.Ref, n.Root.String(), n.Tokens, n.Ephemeral))
	}
	for _, e := range g.edges {
		out = append(out, fmt.Sprintf("edge %s->%s kind=%d weight=%v turn=%d", e.From, e.To, e.Kind, e.Weight, e.Turn))
	}
	sort.Strings(out)
	return out
}

// TestOpenRoundTrip (test 41) asserts a flushed graph reloads as the same graph: identical live
// counters, an identical dump, and — the assertion that actually matters to §6.4 — a BackwardSlice
// that scores and orders the reloaded nodes exactly as it did before the process restarted.
func TestOpenRoundTrip(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()

	before := logOpen(t, root, logging.Nop())
	criteria := logSampleGraph(t, before)
	require.NoError(t, before.Flush(ctx))

	beforeSlice, err := before.BackwardSlice(criteria, SliceOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, beforeSlice.Order, "the sample must produce a non-empty slice to be worth comparing")

	after := logOpen(t, root, logging.Nop())
	bs, as := before.Stats(), after.Stats()
	require.Equal(t, bs.Nodes, as.Nodes)
	require.Equal(t, bs.Edges, as.Edges)
	require.Equal(t, bs.NodesByKind, as.NodesByKind)
	require.Equal(t, bs.EdgesByKind, as.EdgesByKind)
	require.Equal(t, bs.Dangling, as.Dangling)
	require.Equal(t, bs.MaxPos, as.MaxPos)
	require.Equal(t, 0, as.LoadErrors)
	require.False(t, as.TruncatedTail)
	require.Equal(t, bs.LogRecords, as.LogRecords, "the loader must count exactly the records Flush wrote")
	require.Equal(t, bs.LogBytes, as.LogBytes, "the loader must count exactly the bytes Flush wrote")

	require.Equal(t, logDump(t, before), logDump(t, after))

	afterSlice, err := after.BackwardSlice(criteria, SliceOptions{})
	require.NoError(t, err)
	require.Equal(t, beforeSlice.Scores, afterSlice.Scores)
	require.Equal(t, beforeSlice.Order, afterSlice.Order)
}

// TestOpenMissingFile (test 42) asserts a project that has never recorded a graph opens clean: no
// error, no nodes, generation 0, and nothing logged. A first-run Open is the overwhelmingly common
// case and it must be indistinguishable from a healthy one, not reported as degradation.
func TestOpenMissingFile(t *testing.T) {
	root := t.TempDir()
	capture := &logCapture{}
	g := logOpen(t, root, capture)

	s := g.Stats()
	require.Equal(t, 0, s.Nodes)
	require.Equal(t, 0, s.Edges)
	require.Equal(t, 0, s.LogRecords)
	require.Equal(t, int64(0), s.LogBytes)
	require.Equal(t, 0, s.LoadErrors)
	require.False(t, s.TruncatedTail)
	require.Equal(t, 0, g.Generation())
	require.Empty(t, capture.all(), "a missing log is the normal first-run case and must log nothing")

	// The directory must exist afterwards, so a caller that never ran paths.EnsureLayout can still
	// flush.
	fi, err := os.Stat(paths.Of(root).DAG)
	require.NoError(t, err)
	require.True(t, fi.IsDir())
}

// TestOpenTornTail (test 43) asserts the expected artifact of a crash mid-append — a final line
// with no newline — costs exactly the partial record and nothing else: every complete record
// loads, TruncatedTail says so, and LoadErrors stays 0 because a torn tail is not corruption.
func TestOpenTornTail(t *testing.T) {
	root := t.TempDir()
	logInstallFixture(t, root, "deps-torn.jsonl")

	capture := &logCapture{}
	g := logOpen(t, root, capture)

	s := g.Stats()
	require.Equal(t, 2, s.Nodes, "both complete node records must load")
	require.Equal(t, 1, s.Edges, "the complete edge record must load")
	require.True(t, s.TruncatedTail, "the unterminated final line must be reported")
	require.Equal(t, 0, s.LoadErrors, "a torn tail is a crash artifact, not corruption")
	require.Equal(t, 3, s.LogRecords, "the discarded partial line is not a record")

	_, ok := g.Node("file:src/partial.ts")
	require.False(t, ok, "the partial record must not be applied")

	require.Len(t, capture.at(logLevelWarn), 1, "exactly one warning, not one per line")
	require.Empty(t, capture.at(logLevelLoud), "a torn tail is expected and must not be loud")
}

// TestOpenCorruptLines (test 44) asserts the loader's two very different reactions to a line it
// cannot use. A malformed line is degradation: counted and surfaced with one Loud per Open (§13
// invariant 10). A line whose "type" this build simply does not know is a NEWER WRITER, not
// damage, and is skipped in silence — counting it would make every forward-compatible addition
// look like corruption to the version that predates it.
func TestOpenCorruptLines(t *testing.T) {
	root := t.TempDir()
	logInstallFixture(t, root, "deps-corrupt.jsonl")

	capture := &logCapture{}
	g := logOpen(t, root, capture)

	s := g.Stats()
	require.Equal(t, 2, s.Nodes, "every valid record before and after the damage must load")
	require.Equal(t, 1, s.Edges)
	require.Equal(t, 1, s.LoadErrors, "only the unterminated line counts; the unknown type is silent")
	require.False(t, s.TruncatedTail)

	loud := capture.at(logLevelLoud)
	require.Len(t, loud, 1, "exactly one Loud per Open, never one per bad line")
	require.Contains(t, fmt.Sprint(loud[0].kv...), "1", "the Loud must carry the error count")
}

// TestWireUnknownTypeSkippedSilently asserts the forward-compatibility rule from both directions:
// a record kind this build has never heard of, and a generation header written to a newer schema
// version, are both skipped without a LoadErrors bump and without a Loud. Only the version field
// on the generation line carries a schema version — that is where forward compatibility lives.
func TestWireUnknownTypeSkippedSilently(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(paths.Of(root).DAG, 0o700))
	require.NoError(t, os.WriteFile(logPathOf(root), []byte(strings.Join([]string{
		`{"type":"zz","id":"file:src/a.ts"}`,
		`{"type":"generation","v":2,"gen":9,"ts":1767225480000,"nodes":1,"edges":0}`,
		`{"type":"node","id":"file:src/a.ts","kind":4,"turn":1,"ts":1767225480000,"pos":10,"ref":"src/a.ts","root":"sha256:0000000000000000000000000000000000000000000000000000000000000000","tokens":5,"ephemeral":false}`,
		"",
	}, "\n")), 0o600))

	capture := &logCapture{}
	g := logOpen(t, root, capture)

	s := g.Stats()
	require.Equal(t, 0, s.LoadErrors, "a newer writer is not corruption")
	require.Equal(t, 1, s.Nodes, "the record this build does understand must still load")
	require.Equal(t, 0, g.Generation(), "a generation header from a newer schema must not be applied")
	require.Empty(t, capture.at(logLevelLoud))
}

// jsonUnicodeEscape renders c the way encoding/json's HTML escaping would: a backslash, a "u", and
// four hex digits. It is built from the byte rather than written out as a literal so that the
// escape sequence this test searches for cannot itself be mangled by an editor or a tool that
// interprets it.
func jsonUnicodeEscape(c byte) string { return fmt.Sprintf("%cu%04x", '\\', c) }

// TestLogSurvivesHTMLCharsUnescaped asserts a path containing "<", ">" and "&" reaches disk
// verbatim. encoding/json escapes those three by default, which would turn deps.jsonl into a file
// no `grep 'src/<a>&b.ts'` can find; every other JSON writer in this codebase disables that (see
// paths.AppendJSONL), and the record codec must match them.
func TestLogSurvivesHTMLCharsUnescaped(t *testing.T) {
	const ref = "src/<a>&b.ts"
	id := NodeID("file:" + ref)

	root := t.TempDir()
	ctx := context.Background()
	g := logOpen(t, root, logging.Nop())
	require.NoError(t, g.AddNode(Node{ID: id, Kind: KindFile, Turn: 1, Pos: 10, Ref: ref, Tokens: 5}))
	require.NoError(t, g.AddEdge(Edge{From: "tooluse:toolu_a", To: id, Kind: EdgeConsumes, Weight: 1, Turn: 1}))
	require.NoError(t, g.Flush(ctx))

	raw := string(logReadFile(t, root))
	require.Contains(t, raw, ref, "the path must be greppable on disk")
	for _, c := range []byte{'<', '>', '&'} {
		require.NotContains(t, raw, jsonUnicodeEscape(c),
			"%q must reach disk verbatim, not as an escape", string(c))
	}

	reloaded := logOpen(t, root, logging.Nop())
	n, ok := reloaded.Node(id)
	require.True(t, ok, "the node must round-trip through the log")
	require.Equal(t, ref, n.Ref)
	require.Equal(t, 1, reloaded.Stats().Edges)
}

// TestFlushIsAppendOnly (test 45) asserts §3.3's append discipline for deps.jsonl in the only way
// that matters to a reader: the file only ever grows, and everything a previous flush wrote is
// still there, byte for byte, at the same offsets.
func TestFlushIsAppendOnly(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	g := logOpen(t, root, logging.Nop())

	require.NoError(t, g.AddNode(Node{ID: "tooluse:toolu_a", Kind: KindToolUse, Pos: 10, Ref: "Read"}))
	require.NoError(t, g.AddNode(Node{ID: "toolresult:toolu_a", Kind: KindToolResult, Pos: 20}))
	require.NoError(t, g.Flush(ctx))
	first := logReadFile(t, root)
	require.NotEmpty(t, first)
	require.Equal(t, 2, logCountLines(first))
	require.Equal(t, int64(len(first)), g.Stats().LogBytes)

	require.NoError(t, g.AddEdge(Edge{From: "tooluse:toolu_a", To: "toolresult:toolu_a", Kind: EdgeProduces, Weight: 1}))
	require.NoError(t, g.Flush(ctx))
	second := logReadFile(t, root)

	require.Greater(t, len(second), len(first), "an append can only grow the file")
	require.True(t, bytes.HasPrefix(second, first), "the first flush's bytes must survive verbatim")
	require.Equal(t, 3, logCountLines(second))
	require.Equal(t, int64(len(second)), g.Stats().LogBytes)
	require.Equal(t, 3, g.Stats().LogRecords)
	require.Equal(t, 0, g.Stats().PendingRecords)
}

// TestFlushIsNoOpWhenNothingPending asserts a flush with an empty queue neither errors nor touches
// the file. SP-05's idle loop calls Flush on a timer, so the common case is "nothing to do".
func TestFlushIsNoOpWhenNothingPending(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	g := logOpen(t, root, logging.Nop())

	require.NoError(t, g.Flush(ctx))
	_, err := os.Stat(logPathOf(root))
	require.True(t, os.IsNotExist(err), "an empty flush must not even create the file")
}

// TestAutoFlushAt2000 (test 46) asserts an observer that never calls Flush still bounds the memory
// it pins: once autoFlushRecords records queue up, AddNode appends them without being asked.
func TestAutoFlushAt2000(t *testing.T) {
	const added = autoFlushRecords + 100

	root := t.TempDir()
	g := logOpen(t, root, logging.Nop())
	for i := range added {
		require.NoError(t, g.AddNode(Node{
			ID: NodeID(fmt.Sprintf("tooluse:toolu_%05d", i)), Kind: KindToolUse, Pos: i, Ref: "Read",
		}))
	}

	raw := logReadFile(t, root)
	require.GreaterOrEqual(t, logCountLines(raw), autoFlushRecords,
		"the auto-flush must have appended at least one full batch")

	s := g.Stats()
	require.Less(t, s.PendingRecords, autoFlushRecords, "the queue must not keep growing past the threshold")
	require.Equal(t, added, s.Nodes)
	require.Equal(t, s.LogRecords+s.PendingRecords, added, "every record is either on disk or still queued")
}

// TestFlushErrorRetainsPending (test 47) asserts a failed append loses nothing: the records stay
// in memory for the idle loop's next attempt, and the failure is loud (§13 invariant 10) rather
// than swallowed.
//
// The failure is induced by pre-creating deps.jsonl as a DIRECTORY. os.Chmod cannot express a
// read-only directory on Windows, so a permission-based version of this test would pass
// vacuously there; opening a directory for writing fails on every platform.
func TestFlushErrorRetainsPending(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(logPathOf(root), 0o700))

	capture := &logCapture{}
	// newGraph, not Open: Open would try to read the directory-shaped log first, and the subject
	// here is the write path.
	g := newGraph(root, config.Defaults(), capture)
	require.NoError(t, g.AddNode(Node{ID: "tooluse:toolu_a", Kind: KindToolUse, Pos: 10}))
	require.NoError(t, g.AddNode(Node{ID: "toolresult:toolu_a", Kind: KindToolResult, Pos: 20}))
	require.Equal(t, 2, g.Stats().PendingRecords)

	err := g.Flush(context.Background())
	require.Error(t, err, "a write that cannot happen must be reported, not swallowed")
	require.Equal(t, 2, g.Stats().PendingRecords, "a failed flush must keep every record in memory")
	require.Equal(t, 0, g.Stats().LogRecords)
	require.Len(t, capture.at(logLevelLoud), 1, "a failed append is degradation and must be loud")
}

// oneMiB is the per-line ceiling deps.jsonl records are held to. It is spelled out here rather
// than read from the production constant deliberately: this test's job is to fail if that ceiling
// ever moves, and a test that took its expectation from the thing under test could not do that.
const oneMiB = 1 << 20

// TestOversizeLineRefused asserts the 1 MiB per-line ceiling is enforced on the way out. The
// refusal wraps ErrInvalidNode — it is the node that is unwritable, not the disk that failed — and
// the record stays queued rather than being silently dropped on the floor.
func TestOversizeLineRefused(t *testing.T) {
	root := t.TempDir()
	g := logOpen(t, root, logging.Nop())

	require.NoError(t, g.AddNode(Node{
		ID: "file:src/huge.ts", Kind: KindFile, Pos: 1, Ref: strings.Repeat("x", oneMiB+1),
	}))
	err := g.Flush(context.Background())
	require.ErrorIs(t, err, ErrInvalidNode)
	require.Equal(t, 1, g.Stats().PendingRecords, "a record too large to write is not a record to discard")
}

// TestPropLogRoundTrip (test 53) is the property behind tests 41–44: for ANY sequence of valid
// mutations, flushing and reopening reproduces the same counters and the same graph. The
// hand-written round-trip test pins one shape; this one searches for the shape that breaks the
// codec — an anchor node re-observed out of order, a folded edge, a tombstone for a node that
// never arrived.
func TestPropLogRoundTrip(t *testing.T) {
	// rapid.T has no TempDir of its own, so the base directory is captured here and each
	// iteration gets its own numbered project beneath it.
	base := t.TempDir()
	iteration := 0

	kinds := []NodeKind{
		KindToolUse, KindToolResult, KindAssistant, KindUserPrompt,
		KindFile, KindSymbol, KindDecision, KindElimination, KindSegment,
	}
	edgeKinds := []EdgeKind{
		EdgeSequence, EdgeProduces, EdgeConsumes, EdgeSharedFile,
		EdgeSharedSymbol, EdgeSupersedes, EdgeExplains, EdgeControlOnly,
	}

	rapid.Check(t, func(rt *rapid.T) {
		iteration++
		root := filepath.Join(base, fmt.Sprintf("it%04d", iteration))
		if err := os.MkdirAll(root, 0o700); err != nil {
			rt.Fatalf("mkdir %s: %v", root, err)
		}

		opened, err := Open(root, config.Defaults(), logging.Nop())
		if err != nil {
			rt.Fatalf("Open: %v", err)
		}
		before, ok := opened.(*graph)
		if !ok {
			rt.Fatalf("Open returned %T, want *graph", opened)
		}

		var ids []NodeID
		for range rapid.IntRange(1, 40).Draw(rt, "nodes") {
			k := rapid.SampledFrom(kinds).Draw(rt, "kind")
			key := rapid.StringMatching(`[a-z0-9_.-]{1,16}`).Draw(rt, "key")
			id := newNodeID(k, key)
			n := Node{
				ID:        id,
				Kind:      k,
				Turn:      core.TurnIndex(rapid.IntRange(0, 200).Draw(rt, "turn")),
				TS:        core.UnixMilli(rapid.Int64Range(0, 1<<40).Draw(rt, "ts")),
				Pos:       rapid.IntRange(0, 500000).Draw(rt, "pos"),
				Ref:       key,
				Tokens:    core.Tokens(rapid.IntRange(0, 5000).Draw(rt, "tokens")),
				Ephemeral: rapid.Bool().Draw(rt, "ephemeral"),
			}
			if err := before.AddNode(n); err != nil {
				rt.Fatalf("AddNode(%+v): %v", n, err)
			}
			ids = append(ids, id)
		}

		// A re-emitted (From, To, Kind) triple is §8.1 item 4's shared-state case: the same edge
		// observed again on a later read of the same path. AddEdge folds it into the stored edge —
		// strongest weight, earliest turn — and journals that fold only when it actually changes
		// something, so memory and log agree either way while an identical re-emission still costs
		// nothing.
		//
		// Weight and turn are drawn FREELY here, deliberately including the re-emission that
		// strengthens an edge or moves it earlier. That is the combination an earlier version of
		// AddEdge got wrong: it folded in memory and appended no record, so a reload rebuilt a
		// different edge, and since weight is a factor in every slice score the divergence would
		// have moved relevance rankings across a restart. Constraining this generator to the shape
		// the production builders happen to emit — always full weight, never a decreasing turn —
		// would have hidden it, which is the one thing a property test must not do.
		for range rapid.IntRange(0, 60).Draw(rt, "edges") {
			from := rapid.SampledFrom(ids).Draw(rt, "from")
			to := rapid.SampledFrom(ids).Draw(rt, "to")
			if from == to {
				continue // a self-loop is invalid by construction, not a codec question
			}
			e := Edge{
				From:   from,
				To:     to,
				Kind:   rapid.SampledFrom(edgeKinds).Draw(rt, "edgeKind"),
				Weight: rapid.Float32Range(0.001, 1).Draw(rt, "weight"),
				Turn:   core.TurnIndex(rapid.IntRange(0, 200).Draw(rt, "edgeTurn")),
			}
			if err := before.AddEdge(e); err != nil {
				rt.Fatalf("AddEdge(%+v): %v", e, err)
			}
		}

		for range rapid.IntRange(0, 4).Draw(rt, "tombstones") {
			id := rapid.SampledFrom(ids).Draw(rt, "tombstone")
			if err := before.Tombstone([]NodeID{id}); err != nil {
				rt.Fatalf("Tombstone(%s): %v", id, err)
			}
		}

		if err := before.Flush(context.Background()); err != nil {
			rt.Fatalf("Flush: %v", err)
		}

		reopened, err := Open(root, config.Defaults(), logging.Nop())
		if err != nil {
			rt.Fatalf("reopen: %v", err)
		}
		after, ok := reopened.(*graph)
		if !ok {
			rt.Fatalf("Open returned %T, want *graph", reopened)
		}

		// GraphStats holds two maps, so it is compared field by field rather than with ==.
		bs, as := before.Stats(), after.Stats()
		if bs.Nodes != as.Nodes || bs.Edges != as.Edges || bs.Tombstoned != as.Tombstoned ||
			bs.Dangling != as.Dangling || bs.MaxPos != as.MaxPos ||
			bs.LogRecords != as.LogRecords || bs.LogBytes != as.LogBytes || as.LoadErrors != 0 {
			rt.Fatalf("counters differ:\n before=%+v\n after =%+v", bs, as)
		}
		if fmt.Sprint(bs.NodesByKind) != fmt.Sprint(as.NodesByKind) {
			rt.Fatalf("nodes by kind differ: %v vs %v", bs.NodesByKind, as.NodesByKind)
		}
		if fmt.Sprint(bs.EdgesByKind) != fmt.Sprint(as.EdgesByKind) {
			rt.Fatalf("edges by kind differ: %v vs %v", bs.EdgesByKind, as.EdgesByKind)
		}

		wantDump, gotDump := logDump(t, before), logDump(t, after)
		if len(wantDump) != len(gotDump) {
			rt.Fatalf("dump length %d != %d", len(wantDump), len(gotDump))
		}
		for i := range wantDump {
			if wantDump[i] != gotDump[i] {
				rt.Fatalf("dump line %d differs:\n want %s\n got  %s", i, wantDump[i], gotDump[i])
			}
		}
	})
}
