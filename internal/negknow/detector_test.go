package negknow

import (
	"context"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

// This file covers §8.3 source #3: the heuristic that infers an elimination nobody reported from
// the test-fail -> revert -> different-approach pattern, and the bounded one-hop DAG walk that
// gives the inferred record its depends_on.
//
// The graphs here are real dag.Graph values from dag.Open, not fakes: the termination obligation
// SP-07 hands this package (ADR 0007) is about the shape of the REAL graph, and a fake would let
// the fixture quietly stop being cyclic.

// ── helpers ───────────────────────────────────────────────────────────────────────────────────

// detPath is the file every single-path fixture below edits, fails a test on, and reverts.
const detPath = "src/db.ts"

// The two approach texts every pattern fixture uses. They classify differently — "widen-pool-
// timeout" against "disable-pool" — which is condition 4 of Pattern P.
const (
	approachA = "widen pool timeout"
	approachB = "disable connection pooling"
)

// newDetGraph opens a real dependence graph over root.
func newDetGraph(t *testing.T, root string, cfg config.Config) dag.Graph {
	t.Helper()
	g, err := dag.Open(root, cfg, logging.Nop())
	require.NoError(t, err)
	return g
}

func obsEdit(turn int, path, detail string) Observation {
	return Observation{Turn: core.TurnIndex(turn), Kind: ObsEdit, Path: path, Detail: detail}
}

func obsTestFail(turn int, path string, root core.Hash, tool core.ToolUseID) Observation {
	return Observation{Turn: core.TurnIndex(turn), Kind: ObsTestFail, Path: path, Root: root, ToolUse: tool}
}

func obsTestPass(turn int, path string) Observation {
	return Observation{Turn: core.TurnIndex(turn), Kind: ObsTestPass, Path: path}
}

func obsRevert(turn int, path string) Observation {
	return Observation{Turn: core.TurnIndex(turn), Kind: ObsRevert, Path: path}
}

// feedObservations pushes every observation through the real Observe path, so the ring the
// detector reads is the one SP-08 will actually fill.
func feedObservations(t *testing.T, l *ledger, signals ...Observation) {
	t.Helper()
	for _, o := range signals {
		require.NoError(t, l.Observe(context.Background(), o))
	}
}

// failRoot is the evidence root every fixture's failing test carries.
func failRoot() core.Hash { return core.HashBytes("negknow.test.detector", []byte("test output")) }

// patternP is the canonical four-observation fixture: edit, failing test, revert, different edit.
func patternP(root core.Hash, tool core.ToolUseID) []Observation {
	return []Observation{
		obsEdit(4, detPath, approachA),
		obsTestFail(5, detPath, root, tool),
		obsRevert(6, detPath),
		obsEdit(7, detPath, approachB),
	}
}

// scanFixture is the shape every negative case here shares: one project carrying both a ledger and
// a REAL dependence graph, signals fed through the real Observe path, and one Scan from turn 0.
//
// The graph is real and empty rather than nil, which is the case these fixtures want: the pattern
// matching is what is under test, and an empty graph exercises the same "no node for this tool
// use" fallback a real session's first failing test would.
func scanFixture(t *testing.T, signals []Observation) []Record {
	t.Helper()
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", newMetrics(), newFakeStore()))
	g := newDetGraph(t, root, cfg)
	feedObservations(t, l, signals...)

	det := NewDetector(l, "s1", cfg, frozenClock{})
	recs, err := det.Scan(context.Background(), g, 0)
	require.NoError(t, err)
	return recs
}

// ── Pattern P ─────────────────────────────────────────────────────────────────────────────────

// TestDetector_PatternP is the whole heuristic in one fixture, down to the reason sentence SP-11
// and SP-13 render verbatim.
func TestDetector_PatternP(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))
	g := newDetGraph(t, root, cfg)
	h := failRoot()
	feedObservations(t, l, patternP(h, "t1")...)

	det := NewDetector(l, "s1", cfg, frozenClock{})
	recs, err := det.Scan(context.Background(), g, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)

	got := recs[0]
	require.Equal(t, SourceHeuristic, got.Source)
	require.Equal(t, detPath, got.Target)
	require.Equal(t, approachA, got.Approach)
	require.Equal(t,
		"test failed at turn 5 and the change was reverted at turn 6; "+
			"a different approach (disable-pool) was taken at turn 7",
		got.Reason)
	require.Equal(t, h, got.Evidence)
	require.Equal(t, StatusActive, got.Status)
	require.Equal(t, Scope(l.elim.DefaultScope), got.Scope)
	require.Equal(t, core.SessionID("s1"), got.Session)
	require.Empty(t, got.ID, "Scan proposes; the ledger's Record is what mints an id")

	require.Equal(t, int64(1), counterValue(t, m, counterDetectorCandidates))
	require.Equal(t, int64(1), counterValue(t, m, counterDetectorEmitted))
}

// TestDetector_SameClassNoEmit pins condition 4: re-phrasing the SAME approach is not a different
// approach, and inferring an elimination from it would eliminate the approach the agent is still
// pursuing.
func TestDetector_SameClassNoEmit(t *testing.T) {
	signals := patternP(failRoot(), "t1")
	signals[3] = obsEdit(7, detPath, "raise the pool timeout")
	require.Equal(t, ApproachClass(approachA), ApproachClass("raise the pool timeout"),
		"the fixture is only meaningful if the two texts really do share a class")

	require.Empty(t, scanFixture(t, signals))
}

// TestDetector_NoRevertNoEmit pins condition 3: without a revert the change is still in the tree,
// so nothing was abandoned and nothing was eliminated.
func TestDetector_NoRevertNoEmit(t *testing.T) {
	full := patternP(failRoot(), "t1")
	signals := []Observation{full[0], full[1], full[3]}

	require.Empty(t, scanFixture(t, signals))
}

// TestDetector_TestPassBreaksPattern pins condition 2's exclusion: a test that passed between the
// edit and the failure means the failure is not evidence against that edit.
func TestDetector_TestPassBreaksPattern(t *testing.T) {
	// The failure is moved to t6 so the pass can sit strictly between the edit and it: turns are
	// integers, and (t4, t5) is empty.
	signals := []Observation{
		obsEdit(4, detPath, approachA),
		obsTestPass(5, detPath),
		obsTestFail(6, detPath, failRoot(), "t1"),
		obsRevert(7, detPath),
		obsEdit(8, detPath, approachB),
	}

	require.Empty(t, scanFixture(t, signals))
}

// TestDetector_WindowExceeded pins detectWindowTurns: an edit thirteen turns after the one that
// failed is a new line of work, not the same one taken differently.
func TestDetector_WindowExceeded(t *testing.T) {
	signals := []Observation{
		obsEdit(4, detPath, approachA),
		obsTestFail(5, detPath, failRoot(), "t1"),
		obsRevert(6, detPath),
		obsEdit(20, detPath, approachB),
	}
	require.Greater(t, 20, 4+detectWindowTurns, "the fixture must fall outside the window")

	require.Empty(t, scanFixture(t, signals))
}

// TestDetector_OneEmissionPerEdit pins "each e0 participates in at most one emission": a second
// revert/re-edit cycle after the same failing edit is more of the same elimination, not a second
// one, and appending it twice would double-count the highest-value content in the transcript.
func TestDetector_OneEmissionPerEdit(t *testing.T) {
	signals := []Observation{
		obsEdit(4, detPath, approachA),
		obsTestFail(5, detPath, failRoot(), "t1"),
		obsRevert(6, detPath),
		obsEdit(7, detPath, approachB),
		obsRevert(8, detPath),
		obsEdit(9, detPath, "rewrite the query planner hint"),
	}

	recs := scanFixture(t, signals)
	require.Len(t, recs, 1)
	require.Equal(t, approachA, recs[0].Approach)
}

// ── what the DAG contributes: depends_on ──────────────────────────────────────────────────────

// TestDetector_DepsFromDAG pins the one-hop walk: the files the failing test actually read become
// the elimination's staleness baseline, deduplicated across the two edge directions and sorted.
func TestDetector_DepsFromDAG(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))
	g := newDetGraph(t, root, cfg)

	const (
		schema = "db/schema.sql"
		conf   = "config/db.yml"
	)
	use := dag.ToolUseNode("t1")
	rootSchema := versionRoot("schema")
	rootConf := versionRoot("conf")

	require.NoError(t, g.AddNode(dag.Node{ID: use, Kind: dag.KindToolUse, Turn: 5, Ref: "Bash"}))
	require.NoError(t, g.AddNode(dag.Node{
		ID: dag.FileNode(paths.Key(schema)), Kind: dag.KindFile, Turn: 5, Ref: paths.Key(schema), Root: rootSchema,
	}))
	require.NoError(t, g.AddNode(dag.Node{
		ID: dag.FileNode(paths.Key(conf)), Kind: dag.KindFile, Turn: 5, Ref: paths.Key(conf), Root: rootConf,
	}))
	// A result node on the same tool use: a non-file neighbour the walk must ignore.
	require.NoError(t, g.AddNode(dag.Node{ID: dag.ToolResultNode("t1"), Kind: dag.KindToolResult, Turn: 5, Ref: "t1"}))

	require.NoError(t, g.AddEdge(dag.Edge{From: use, To: dag.FileNode(paths.Key(schema)), Kind: dag.EdgeConsumes, Turn: 5}))
	require.NoError(t, g.AddEdge(dag.Edge{From: use, To: dag.FileNode(paths.Key(conf)), Kind: dag.EdgeConsumes, Turn: 5}))
	// The same file reachable a second time, from the other direction and under the other kept
	// kind: the dedup is what stops it appearing twice.
	require.NoError(t, g.AddEdge(dag.Edge{From: dag.FileNode(paths.Key(conf)), To: use, Kind: dag.EdgeSharedFile, Turn: 5}))
	require.NoError(t, g.AddEdge(dag.Edge{From: use, To: dag.ToolResultNode("t1"), Kind: dag.EdgeProduces, Turn: 5}))

	feedObservations(t, l, patternP(failRoot(), "t1")...)
	det := NewDetector(l, "s1", cfg, frozenClock{})
	recs, err := det.Scan(context.Background(), g, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)

	require.Equal(t, []Dep{
		{Path: paths.Key(conf), Hash: rootConf},
		{Path: paths.Key(schema), Hash: rootSchema},
	}, recs[0].DependsOn, "sorted ascending by path, each file once, no non-file neighbour")
}

// TestDetector_DepsFallback pins the fallback: with no graph at all the candidate still gets a
// staleness baseline, out of the store, through the same resolveDeps the explicit sources use.
func TestDetector_DepsFallback(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	fs := newFakeStore().
		withHistory(paths.Key(detPath), storedVersion(10, "db")).
		withHistory(paths.Key("package-lock.json"), storedVersion(11, "lock"))
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, fs))
	feedObservations(t, l, patternP(failRoot(), "t1")...)

	det := NewDetector(l, "s1", cfg, frozenClock{})
	recs, err := det.Scan(context.Background(), nil, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, []Dep{
		{Path: paths.Key(detPath), Hash: versionRoot("db")},
		{Path: paths.Key("package-lock.json"), Hash: versionRoot("lock")},
	}, recs[0].DependsOn, "resolveDeps' own candidate order: the target first, then autoDepCandidates")
}

// ── evidence, windowing and the append boundary ───────────────────────────────────────────────

// TestDetector_NoEvidenceDropped pins the requireEvidence refusal on the inferred source: a
// heuristic guess with nothing behind it is exactly the record that must not reach the log.
func TestDetector_NoEvidenceDropped(t *testing.T) {
	root, cfg := newProject(t)
	cfg.Eliminations.RequireEvidence = true
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))
	g := newDetGraph(t, root, cfg)
	feedObservations(t, l, patternP(core.Hash{}, "t1")...)

	det := NewDetector(l, "s1", cfg, frozenClock{})
	recs, err := det.Scan(context.Background(), g, 0)
	require.NoError(t, err)
	require.Empty(t, recs)
	require.Equal(t, int64(1), counterValue(t, m, counterDetectorCandidates))
	require.Equal(t, int64(1), counterValue(t, m, counterDetectorDroppedNoEvidence))
	require.Equal(t, int64(0), counterValue(t, m, counterDetectorEmitted))
}

// TestDetector_Since pins the incremental contract SP-08 and SP-12 both scan with: an earlier
// window's candidates are not re-proposed.
func TestDetector_Since(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))
	g := newDetGraph(t, root, cfg)
	h := failRoot()
	feedObservations(t, l,
		obsEdit(1, "src/a.ts", approachA),
		obsTestFail(2, "src/a.ts", h, "t1"),
		obsRevert(3, "src/a.ts"),
		obsEdit(4, "src/a.ts", approachB),
		obsEdit(12, "src/b.ts", approachA),
		obsTestFail(13, "src/b.ts", h, "t2"),
		obsRevert(14, "src/b.ts"),
		obsEdit(15, "src/b.ts", approachB),
	)
	det := NewDetector(l, "s1", cfg, frozenClock{})

	all, err := det.Scan(context.Background(), g, 0)
	require.NoError(t, err)
	require.Len(t, all, 2, "from turn 0 both windows are in scope")

	late, err := det.Scan(context.Background(), g, 10)
	require.NoError(t, err)
	require.Len(t, late, 1)
	require.Equal(t, "src/b.ts", late[0].Target)
}

// TestDetector_DoesNotAppend pins the boundary the plan draws: Scan proposes, and the caller
// (SP-08's observer, SP-12's idle task) decides when the proposal becomes a record.
func TestDetector_DoesNotAppend(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))
	g := newDetGraph(t, root, cfg)
	feedObservations(t, l, patternP(failRoot(), "t1")...)

	det := NewDetector(l, "s1", cfg, frozenClock{})
	recs, err := det.Scan(context.Background(), g, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, 0, l.Health().Records, "Scan appends nothing")

	id, err := l.Record(context.Background(), recs[0])
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.Equal(t, 1, l.Health().Records)
}

// TestDetector_TerminatesOnCyclicGraph is the wave-1 INHERIT obligation (ADR 0007), discharged
// against a REAL cyclic fixture rather than an argument.
//
// §8.1 item 4 directs a shared-file edge into a tool use that consumed a file and out of one that
// produced it, so an ordinary Read-then-Edit of one file closes a legal loop —
// tooluse:t1 -> toolresult:t1 -> assistant:2 -> tooluse:t2 -> file:a -> tooluse:t1 — and
// internal/dag's TestReadThenWriteClosesALegitimateCycle pins that it really is a cycle. Scan's
// one-hop walk must see the cycle's edges as ordinary neighbours and never follow them back.
func TestDetector_TerminatesOnCyclicGraph(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))
	g := newDetGraph(t, root, cfg)

	const cyclePath = "src/a.ts"
	key := paths.Key(cyclePath)
	require.NoError(t, dag.BuildToolUse(g, dag.ObservedTool{
		ToolUseID: "t1", Turn: 1, Pos: 100, ResultPos: 200,
		Tool: "Read", PathKey: key, Writes: false,
	}))
	require.NoError(t, dag.BuildToolUse(g, dag.ObservedTool{
		ToolUseID: "t2", PrevToolUseID: "t1", PrevTurn: 1, Turn: 2, Pos: 300, ResultPos: 400,
		Tool: "Edit", PathKey: key, Writes: true,
	}))
	// BuildToolUse mints the file node without a Root — it has no content hash to give it — and
	// the one-hop walk requires one. AddNode merges field-wise, so this supplies it in place.
	fileRoot := versionRoot("cycle")
	require.NoError(t, g.AddNode(dag.Node{
		ID: dag.FileNode(key), Kind: dag.KindFile, Turn: 1, Ref: key, Root: fileRoot,
	}))

	// The fixture is only meaningful while both halves of the loop are present.
	require.True(t, hasEdge(g.In(dag.ToolUseNode("t1")), dag.FileNode(key), dag.ToolUseNode("t1")),
		"file:a -> tooluse:t1 (the read's shared-file edge) closes the loop")
	require.True(t, hasEdge(g.Out(dag.ToolUseNode("t2")), dag.ToolUseNode("t2"), dag.FileNode(key)),
		"tooluse:t2 -> file:a (the write's shared-file edge) opens it")

	obs := []Observation{
		{Turn: 4, Kind: ObsEdit, Path: key, Detail: approachA},
		{Turn: 5, Kind: ObsTestFail, Path: key, Root: failRoot(), ToolUse: "t1"},
		{Turn: 6, Kind: ObsRevert, Path: key},
		{Turn: 7, Kind: ObsEdit, Path: key, Detail: approachB},
	}
	feedObservations(t, l, obs...)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	det := NewDetector(l, "s1", cfg, frozenClock{})

	done := make(chan struct{})
	var (
		recs []Record
		err  error
	)
	go func() {
		defer close(done)
		recs, err = det.Scan(ctx, g, 0)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		require.FailNow(t, "Scan did not return on a cyclic graph")
	}

	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, []Dep{{Path: key, Hash: fileRoot}}, recs[0].DependsOn,
		"the cycle's file node is named exactly once, however many edges reach it")
}

// hasEdge reports whether edges contains one running from -> to.
func hasEdge(edges []dag.Edge, from, to dag.NodeID) bool {
	for _, e := range edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}
