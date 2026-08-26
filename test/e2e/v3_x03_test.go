// V3-VERIFY integration test X3 (plans/V3-VERIFY-observer-and-negative-knowledge.md §5):
// observer (DAG edges + signals) -> dag.Graph -> negknow.Detector, across SP-08 + SP-07 + SP-09.
//
// The test wires the observer's OnSignals callback into led.(negknow.Maintainer).Observe(...)
// HERE, in the test file: that composition root deliberately does not exist in production at
// wave 2 (the observer does not import negknow), and this test's job is to prove the two halves
// fit so SP-12's daemon wiring in wave 3 is a wiring change and not a redesign.
package e2e

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// x3Session is the one session identity the observer, the ledger and the detector all share: the
// observer keys its per-session state on it, the ledger scopes its records to it, and the
// detector stamps it onto every record it proposes.
const x3Session = core.SessionID("sess_v3_x03")

// x3Path is the file every step of the §5 X3 pattern happens to, in paths.Key form.
const x3Path = "src/db.ts"

// The four §5 X3 inputs' fixed identities.
const (
	x3IDEdit1    = core.ToolUseID("toolu_x3_edit1")
	x3IDTestFail = core.ToolUseID("toolu_x3_test")
	x3IDRevert   = core.ToolUseID("toolu_x3_revert")
	x3IDEdit2    = core.ToolUseID("toolu_x3_edit2")
)

// The two approach texts, whose ApproachClass values differ — condition 4 of Pattern P.
const (
	x3Approach1 = "widen pool timeout"
	x3Approach2 = "disable connection pooling"
)

// The two test outputs the scenario is run under. The failing one carries the spec's verbatim
// "--- FAIL: TestPool" marker; the passing one is a go-test "ok" summary line.
const (
	x3FailOutput = "--- FAIL: TestPool (0.03s)\n    pool_test.go:42: timeout widened but the pool still starves\nFAIL\nFAIL\tqompack/internal/db\t0.412s\n"
	x3PassOutput = "ok  \tqompack/internal/db\t0.412s\n"
)

// x3Env is everything one composed observer->DAG->ledger stack hands back to the test body.
type x3Env struct {
	proj *testutil.Project
	str  store.Store
	g    dag.Graph
	led  negknow.Ledger
	// failRoot is the stored root of the test-run output — the evidence hash the detector must
	// carry, read back from the observer's own store write.
	failRoot core.Hash
}

// x3ToolEvent builds one PostToolUse hookio.Event the way Claude Code spells it.
func x3ToolEvent(t *testing.T, root string, id core.ToolUseID, tool string, input, response map[string]any) observer.Event {
	t.Helper()
	in, err := json.Marshal(input)
	require.NoError(t, err)
	resp, err := json.Marshal(response)
	require.NoError(t, err)
	return observer.Event{
		HookEventName: "PostToolUse", SessionID: x3Session, CWD: root,
		ToolName: tool, ToolUseID: id,
		ToolInput: json.RawMessage(in), ToolResponse: json.RawMessage(resp),
	}
}

// x3PromptEvent builds one UserPromptSubmit event; each closes the current user turn, which is how
// the four tool calls land on turns 4, 5, 6 and 7.
func x3PromptEvent(root, prompt string) observer.Event {
	return observer.Event{
		HookEventName: "UserPromptSubmit", SessionID: x3Session, CWD: root, Prompt: prompt,
	}
}

// x3Drive composes the real seams — real store, real dag.Open, real negknow.Open, real observer
// over all three — and drives the §5 X3 input sequence through observer.OnToolUse, with the
// FakeClock advancing 30 s per event. testOutput selects the failing or the passing variant of
// step 2.
func x3Drive(t *testing.T, testOutput string) *x3Env {
	t.Helper()
	ctx := context.Background()

	p := testutil.NewProject(t)
	s := p.Store(t)
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)

	led, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Store: s, Graph: g, Session: x3Session, Log: p.Log, Clock: p.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })
	maint, ok := led.(negknow.Maintainer)
	require.True(t, ok, "negknow.Open must return a value satisfying negknow.Maintainer")

	// The wave-3 composition seam, inside the test file: each OnSignals delivery is mapped onto
	// the §5 X3 Observation for that event and fed to the ledger's Observe. The mapping is
	// per-event because Signals deliberately carries no turn and no store root — those belong to
	// the composer, which in production will be SP-12's daemon and here is this queue.
	var pending []func(sig observer.Signals)
	onSignals := func(sess core.SessionID, sig observer.Signals) {
		require.Equal(t, x3Session, sess, "OnSignals must report the driving session")
		require.NotEmpty(t, pending, "OnSignals fired for an event the test did not plan")
		next := pending[0]
		pending = pending[1:]
		next(sig)
	}

	obsv, err := observer.New(observer.Options{
		ProjectRoot: p.Root, Cfg: p.Cfg, Store: s, Graph: g,
		OnSignals: onSignals, Log: p.Log, Clock: p.Clock,
	})
	require.NoError(t, err)

	env := &x3Env{proj: p, str: s, g: g, led: led}

	// advance moves the FakeClock 30 s — the spec's per-event cadence.
	advance := func() { p.Clock.Advance(30 * time.Second) }

	// prompt closes the current user turn so the next tool call lands one turn later.
	prompt := func(text string) {
		advance()
		_, err := obsv.OnUserPrompt(ctx, x3PromptEvent(p.Root, text))
		require.NoError(t, err)
	}

	// drive runs one PostToolUse event after queueing its Observation mapper.
	drive := func(e observer.Event, mapper func(sig observer.Signals)) {
		advance()
		pending = append(pending, mapper)
		_, err := obsv.OnToolUse(ctx, e)
		require.NoError(t, err)
		require.Empty(t, pending, "OnToolUse must deliver exactly one OnSignals call per event")
	}

	observe := func(o negknow.Observation) {
		require.NoError(t, maint.Observe(ctx, o))
	}

	// Turns 0-3 are user turns, so the first tool call lands at turn 4.
	prompt("please look at the db pool")
	prompt("the pool is starving under load")
	prompt("try widening the timeout")
	prompt("go ahead")

	// 1. Edit src/db.ts (turn 4) -> ObsEdit with the approach text.
	drive(x3ToolEvent(t, p.Root, x3IDEdit1, "Edit",
		map[string]any{"file_path": x3Path, "new_string": "pool.timeout = 30000"},
		map[string]any{"content": "pool.timeout = 30000"},
	), func(observer.Signals) {
		observe(negknow.Observation{Kind: negknow.ObsEdit, Path: x3Path, Detail: x3Approach1, Turn: 4, ToolUse: x3IDEdit1})
	})

	// 2. Bash `go test ./internal/db/...` (turn 5) -> ObsTestFail or ObsTestPass, carrying the
	//    STORED root of the output — read back out of the observer's own index write, never a
	//    synthetic value.
	prompt("run the db tests")
	testEvent := x3ToolEvent(t, p.Root, x3IDTestFail, "Bash",
		map[string]any{"command": "go test ./internal/db/..."},
		map[string]any{"exit_code": 1, "stdout": testOutput},
	)
	failing := testOutput == x3FailOutput
	if failing {
		require.Equal(t, observer.TestFail, observer.ExtractTestOutcome(testEvent),
			"observer.ExtractTestOutcome must read the failing run as TestFail")
	} else {
		require.Equal(t, observer.TestPass, observer.ExtractTestOutcome(testEvent),
			"observer.ExtractTestOutcome must read the passing run as TestPass")
	}
	drive(testEvent, func(sig observer.Signals) {
		rec, err := s.ToolUse(ctx, x3IDTestFail)
		require.NoError(t, err, "the observer must have indexed the test run before OnSignals fires")
		require.False(t, rec.Root.IsZero(), "the test output must have been stored")
		env.failRoot = rec.Root
		kind := negknow.ObsTestFail
		if !failing {
			kind = negknow.ObsTestPass
			require.True(t, sig.TestPassed, "Signals.TestPassed must be set for the passing run")
		}
		observe(negknow.Observation{Kind: kind, Path: x3Path, Turn: 5, ToolUse: x3IDTestFail, Root: rec.Root})
	})

	// 3. Bash `git checkout -- src/db.ts` (turn 6) -> ObsRevert.
	prompt("that made it worse, undo it")
	drive(x3ToolEvent(t, p.Root, x3IDRevert, "Bash",
		map[string]any{"command": "git checkout -- src/db.ts"},
		map[string]any{"exit_code": 0, "stdout": "Updated 1 path from the index\n"},
	), func(observer.Signals) {
		observe(negknow.Observation{Kind: negknow.ObsRevert, Path: x3Path, Turn: 6, ToolUse: x3IDRevert})
	})

	// 4. Edit src/db.ts with a different approach (turn 7) -> ObsEdit.
	prompt("try disabling pooling instead")
	drive(x3ToolEvent(t, p.Root, x3IDEdit2, "Edit",
		map[string]any{"file_path": x3Path, "new_string": "pool.enabled = false"},
		map[string]any{"content": "pool.enabled = false"},
	), func(observer.Signals) {
		observe(negknow.Observation{Kind: negknow.ObsEdit, Path: x3Path, Detail: x3Approach2, Turn: 7, ToolUse: x3IDEdit2})
	})

	return env
}

// x3RequireEdge asserts that g holds an edge from -> to of kind k.
func x3RequireEdge(t *testing.T, g dag.Graph, from, to dag.NodeID, k dag.EdgeKind) {
	t.Helper()
	for _, e := range g.Out(from) {
		if e.To == to && e.Kind == k {
			return
		}
	}
	require.Failf(t, "missing DAG edge", "no %s edge %s -> %s", k, from, to)
}

// x3AssertObserverDAG is the first §5 X3 expected-output bullet: the observer-built graph, with
// no hand-added nodes, carries the full tool_use/tool_result chain, the consumes links into the
// following turn, and file-anchored shared-state edges only.
func x3AssertObserverDAG(t *testing.T, g dag.Graph) {
	t.Helper()

	ids := []core.ToolUseID{x3IDEdit1, x3IDTestFail, x3IDRevert, x3IDEdit2}
	turns := []core.TurnIndex{4, 5, 6, 7}

	for i, id := range ids {
		use, result := dag.ToolUseNode(id), dag.ToolResultNode(id)

		n, ok := g.Node(use)
		require.True(t, ok, "tooluse node for %s must exist", id)
		require.Equal(t, dag.KindToolUse, n.Kind)
		require.Equal(t, turns[i], n.Turn, "tool call %s must sit at turn %d", id, int(turns[i]))

		n, ok = g.Node(result)
		require.True(t, ok, "toolresult node for %s must exist", id)
		require.Equal(t, dag.KindToolResult, n.Kind)

		// EdgeProduces between each tooluse/toolresult pair.
		x3RequireEdge(t, g, use, result, dag.EdgeProduces)

		// EdgeConsumes from each toolresult to the AssistantNode of the following turn.
		if i < len(ids)-1 {
			x3RequireEdge(t, g, result, dag.AssistantNode(turns[i+1]), dag.EdgeConsumes)
		}
	}

	// One FileNode("src/db.ts") every src/db.ts call couples through, D-1 oriented: both Edit
	// calls are producers, so their EdgeSharedFile runs tooluse:<edit> -> file:src/db.ts.
	fileID := dag.FileNode(paths.Key(x3Path))
	fn, ok := g.Node(fileID)
	require.True(t, ok, "the observer must have minted file:%s", x3Path)
	require.Equal(t, dag.KindFile, fn.Kind)
	x3RequireEdge(t, g, dag.ToolUseNode(x3IDEdit1), fileID, dag.EdgeSharedFile)
	x3RequireEdge(t, g, dag.ToolUseNode(x3IDEdit2), fileID, dag.EdgeSharedFile)

	// Every shared-state edge is anchored on the file node: a consuming call would couple as
	// file -> tooluse and a producing one as tooluse -> file, and ZERO EdgeSharedFile edges may
	// run tool-use to tool-use (H6's TestGraph_NoToolUseToToolUseSharedFileEdge).
	sharedFileEdges := 0
	for _, n := range g.NodesAfter(0) {
		for _, e := range g.Out(n.ID) {
			if e.Kind != dag.EdgeSharedFile {
				continue
			}
			sharedFileEdges++
			fromKind, _, okFrom := dag.ParseNodeID(e.From)
			toKind, _, okTo := dag.ParseNodeID(e.To)
			require.True(t, okFrom && okTo, "shared-file edge endpoints must parse: %s -> %s", e.From, e.To)
			require.False(t, fromKind == dag.KindToolUse && toKind == dag.KindToolUse,
				"shared state must anchor on the file node, never tool-use to tool-use: %s -> %s", e.From, e.To)
			require.True(t, fromKind == dag.KindFile || toKind == dag.KindFile,
				"an EdgeSharedFile must have the file node at one end: %s -> %s", e.From, e.To)
		}
	}
	require.GreaterOrEqual(t, sharedFileEdges, 2, "both producing Edit calls must couple through the file node")
}

// TestV3_ObserverDagDrivesHeuristicEliminationDetector is §5 X3.
func TestV3_ObserverDagDrivesHeuristicEliminationDetector(t *testing.T) {
	ctx := context.Background()

	env := x3Drive(t, x3FailOutput)

	// ── the DAG the observer built, without any hand-added nodes ──
	x3AssertObserverDAG(t, env.g)

	// ── 5. the detector, constructed over the ledger's own observation ring ──
	src, ok := env.led.(negknow.ObservationSource)
	require.True(t, ok, "the value negknow.Open returns must satisfy negknow.ObservationSource")
	det := negknow.NewDetector(src, x3Session, env.proj.Cfg, env.proj.Clock)

	records, err := det.Scan(ctx, env.g, 0)
	require.NoError(t, err)
	require.Len(t, records, 1, "Scan must return exactly one Record for the fail->revert->re-approach pattern")
	rec := records[0]

	require.Equal(t, x3Path, rec.Target)
	require.Equal(t, x3Approach1, rec.Approach)
	require.Equal(t, negknow.SourceHeuristic, rec.Source)

	// Evidence is the STORED root of the failing test output — the hash came from the observer's
	// own store write, not from a synthetic value.
	require.False(t, env.failRoot.IsZero())
	require.Equal(t, env.failRoot, rec.Evidence,
		"Evidence must equal the stored root of the failing test output")

	// DependsOn resolves through the composed stack to the file the failing test was about —
	// guarded by the store version the observer's own Edit writes appended — sorted by path.
	require.NotEmpty(t, rec.DependsOn, "the record must carry a staleness guard")
	require.True(t, sort.SliceIsSorted(rec.DependsOn, func(i, j int) bool {
		return rec.DependsOn[i].Path < rec.DependsOn[j].Path
	}), "DependsOn must be sorted by path")
	depPaths := make([]string, 0, len(rec.DependsOn))
	for _, d := range rec.DependsOn {
		depPaths = append(depPaths, d.Path)
	}
	require.Contains(t, depPaths, paths.Key(x3Path), "the eliminated file itself must guard the record")
	history, err := env.str.FileHistory(ctx, paths.Key(x3Path))
	require.NoError(t, err)
	require.NotEmpty(t, history, "the observer's Edit writes must have appended file versions")
	latest := history[0]
	for _, v := range history[1:] {
		if v.TS > latest.TS {
			latest = v
		}
	}
	for _, d := range rec.DependsOn {
		if d.Path == paths.Key(x3Path) {
			require.Equal(t, latest.Root, d.Hash,
				"the staleness baseline must be the store's newest version of %s", x3Path)
		}
	}

	// ── Scan does not append ──
	require.Zero(t, env.led.Health().Records, "Scan proposes; it must never append")
	id, err := env.led.Record(ctx, rec)
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.Equal(t, 1, env.led.Health().Records, "a subsequent led.Record must append exactly one record")

	// ── the whole sequence again with a PASSING test output: TestPass breaks the pattern ──
	t.Run("passing_test_breaks_the_pattern", func(t *testing.T) {
		env2 := x3Drive(t, x3PassOutput)
		src2, ok := env2.led.(negknow.ObservationSource)
		require.True(t, ok)
		det2 := negknow.NewDetector(src2, x3Session, env2.proj.Cfg, env2.proj.Clock)
		records2, err := det2.Scan(context.Background(), env2.g, 0)
		require.NoError(t, err)
		require.Empty(t, records2, "a passing test run must yield zero heuristic eliminations")
		require.Zero(t, env2.led.Health().Records)
	})
}
