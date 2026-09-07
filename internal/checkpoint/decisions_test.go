package checkpoint_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/dag/dagtest"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
	"github.com/stretchr/testify/require"
)

// newDecisionSource builds a SourceSet over REAL backends, one bare temp dir per seam. There are
// no fake packages to reach for — storetest/dagtest/negknowtest are conformance suites — so these
// tests exercise ExtractDecisions against the same store.Open/dag.Open/negknow.Open the daemon
// wires. Pins is the SP-01 stub (its All reports core.ErrNotImplemented), which doubles as the
// standing coverage of the "a failing pin source degrades, never fails" path; the one test that
// needs pins to answer swaps in memPins below.
func newDecisionSource(t *testing.T) checkpoint.SourceSet {
	t.Helper()
	cfg := config.Defaults()
	clk := testutil.NewFakeClock(testutil.Epoch)

	s, err := store.Open(t.TempDir(), cfg, store.Deps{Clock: clk})
	require.NoError(t, err)
	// A real store holds open append handles; on Windows an unreleased handle makes the
	// t.TempDir cleanup fail with a message that never mentions the store.
	t.Cleanup(func() { _ = s.Close() })

	g, err := dag.Open(t.TempDir(), cfg, logging.Nop())
	require.NoError(t, err)

	led, err := negknow.Open(t.TempDir(), cfg, nil, negknow.Deps{Session: "sess_extract", Clock: clk})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })

	ps, err := pins.Open(t.TempDir())
	require.NoError(t, err)

	return checkpoint.SourceSet{
		Store:    s,
		Segments: s.Segments(),
		Ledger:   led,
		Pins:     ps,
		Graph:    g,
		Grammar:  grammar.New(),
		Tokens:   tokens.New(cfg, ""),
	}
}

// putText stores text and returns its content root, which is what a dag.Node.Root carries.
func putText(t *testing.T, src checkpoint.SourceSet, text string) core.Hash {
	t.Helper()
	res, err := src.Store.PutBytes(context.Background(), []byte(text), store.PutOptions{})
	require.NoError(t, err)
	return res.Root.Hash
}

// addNode adds one node through the graph's own validation, failing the test on rejection.
func addNode(t *testing.T, g dag.Graph, n dag.Node) {
	t.Helper()
	require.NoError(t, g.AddNode(n))
}

// addEdge adds one edge through the graph's own validation, failing the test on rejection.
func addEdge(t *testing.T, g dag.Graph, e dag.Edge) {
	t.Helper()
	require.NoError(t, g.AddEdge(e))
}

// memPins is a minimal in-test pins.Store for source (c). It lives here rather than in any
// production file because SourceSet must only ever be wired with the real pins.Open in
// production; internal/pins itself is another seat's file set.
type memPins struct{ invs []pins.Invariant }

func (m memPins) Add(context.Context, pins.Invariant) error { return core.ErrNotImplemented }
func (m memPins) Remove(context.Context, string) error      { return core.ErrNotImplemented }
func (m memPins) All(context.Context) ([]pins.Invariant, error) {
	return m.invs, nil
}
func (m memPins) Materialize(context.Context) error { return core.ErrNotImplemented }

// explainFixtureText is the plan's §9 worked example: two load-bearing sentences and one that the
// two-sentence why must exclude.
const explainFixtureText = "Switched to a transaction-scoped pool. pgbouncer 1.18 ignores pool_timeout in transaction mode. Extra."

// TestExtractFromEdgeExplains is the source (a) fixture: assistant A --explains--> file
// src/auth.ts at turn 12 yields one decision whose what names the file, whose why is A's first
// two sentences, and whose evidence is A's content root.
func TestExtractFromEdgeExplains(t *testing.T) {
	src := newDecisionSource(t)
	ctx := context.Background()

	root := putText(t, src, explainFixtureText)
	a := dag.AssistantNode(12)
	f := dag.FileNode("src/auth.ts")
	addNode(t, src.Graph, dag.Node{ID: a, Kind: dag.KindAssistant, Turn: 12, Root: root})
	addNode(t, src.Graph, dag.Node{ID: f, Kind: dag.KindFile, Turn: 12, Ref: "src/auth.ts"})
	addEdge(t, src.Graph, dag.Edge{From: a, To: f, Kind: dag.EdgeExplains, Weight: 1, Turn: 12})

	got, err := checkpoint.ExtractDecisions(ctx, src, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)

	d := got[0]
	require.Equal(t, "changed src/auth.ts", d.What)
	require.True(t, strings.HasPrefix(d.Why, "Switched to a transaction-scoped pool."))
	require.Contains(t, d.Why, "pgbouncer 1.18 ignores pool_timeout in transaction mode.")
	require.NotContains(t, d.Why, "Extra")
	require.Equal(t,
		"Switched to a transaction-scoped pool. pgbouncer 1.18 ignores pool_timeout in transaction mode.",
		d.Why)
	require.Equal(t, root, d.Evidence)
	require.Equal(t, core.TurnIndex(12), d.Turn)
	require.Nil(t, d.AlternativesRejected)
	require.Equal(t, checkpoint.MintDecisionID(d.What, d.Why, d.Evidence), d.ID,
		"the extracted id must be exactly what the minter derives from the same triple")
	t.Logf("fixture (a) minted id: %s  evidence: %s", d.ID, d.Evidence)
}

// TestExtractFromElimination is the source (b) fixture: a ledger record with an approach becomes
// a rejected-alternative decision.
func TestExtractFromElimination(t *testing.T) {
	src := newDecisionSource(t)
	ctx := context.Background()

	ev := putText(t, src, "pgbouncer log: pool_timeout ignored in transaction mode")
	_, err := src.Ledger.Record(ctx, negknow.Record{
		Target:   "src/auth.ts:refreshToken",
		Approach: "widen pool timeout",
		Reason:   "pgbouncer 1.18 ignores it in transaction mode",
		Evidence: ev,
	})
	require.NoError(t, err)

	got, err := checkpoint.ExtractDecisions(ctx, src, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)

	d := got[0]
	require.Equal(t, `rejected "widen pool timeout" for src/auth.ts:refreshToken`, d.What)
	require.Equal(t, "pgbouncer 1.18 ignores it in transaction mode", d.Why)
	require.Equal(t, []string{"widen pool timeout"}, d.AlternativesRejected)
	require.Equal(t, ev, d.Evidence)
	require.Equal(t, core.TurnIndex(0), d.Turn, "no elimination node in the graph, so turn falls back to from")
	t.Logf("fixture (b) minted id: %s  evidence: %s", d.ID, d.Evidence)
}

// TestExtractFromDecisionPin is the source (c) fixture: a pins.Invariant with Source "decision"
// splits on the first " because " into what and why. A pin with any other Source is not a
// decision and must not appear.
func TestExtractFromDecisionPin(t *testing.T) {
	src := newDecisionSource(t)
	src.Pins = memPins{invs: []pins.Invariant{
		{ID: "inv_dec1", Text: "use pgx directly because the pool wrapper hides timeouts", Source: "decision"},
		{ID: "inv_user", Text: "never widen the pool", Source: "user"},
		{ID: "inv_dec2", Text: "ship the daemon first", Source: "decision"},
	}}

	got, err := checkpoint.ExtractDecisions(context.Background(), src, 0)
	require.NoError(t, err)
	require.Len(t, got, 2)

	byWhat := map[string]checkpoint.Decision{}
	for _, d := range got {
		byWhat[d.What] = d
	}
	d, ok := byWhat["use pgx directly"]
	require.True(t, ok)
	require.Equal(t, "the pool wrapper hides timeouts", d.Why)
	require.Nil(t, d.AlternativesRejected)
	require.Equal(t, core.Hash{}, d.Evidence)
	require.Equal(t, core.TurnIndex(0), d.Turn)

	noBecause, ok := byWhat["ship the daemon first"]
	require.True(t, ok, "a pin without \" because \" keeps its whole text as what")
	require.Equal(t, "pinned by the user as a standing decision", noBecause.Why)
	t.Logf("fixture (c) minted id: %s  evidence: %s", d.ID, d.Evidence)
}

// TestDecisionIDIsStableAndDeterministic pins MintDecisionID's shape and its whitespace
// normalization: runs of whitespace collapse, so reworded spacing cannot fork an id, while a
// different evidence hash must.
func TestDecisionIDIsStableAndDeterministic(t *testing.T) {
	ev := core.Hash{0x01}
	a := checkpoint.MintDecisionID("use pgx directly", "the pool wrapper hides timeouts", ev)
	require.Equal(t, a, checkpoint.MintDecisionID("use pgx directly", "the pool wrapper hides timeouts", ev))
	require.Equal(t, a,
		checkpoint.MintDecisionID("  use \t pgx\ndirectly ", "the  pool\n\nwrapper \t hides timeouts\n", ev),
		"whitespace variants must mint the identical id")
	require.Equal(t, a,
		checkpoint.MintDecisionID("use pgx directly", "the pool wrapper hides timeouts", ev),
		"unicode whitespace normalizes the same way ASCII whitespace does")

	require.Regexp(t, `^dec_[0-9a-f]{12}$`, string(a))
	require.Len(t, string(a), 16)

	require.NotEqual(t, a, checkpoint.MintDecisionID("use pgx directly", "the pool wrapper hides timeouts", core.Hash{0x02}),
		"a different evidence hash must mint a different id")
}

// TestExtractDeduplicatesByID re-derives the same decision at turns 12 and 30 — once through the
// graph's own AddEdge fold and once through a second explaining node with the identical content
// root — and must return it exactly once, at the lowest turn.
func TestExtractDeduplicatesByID(t *testing.T) {
	src := newDecisionSource(t)
	ctx := context.Background()

	root := putText(t, src, "Chose the transaction-scoped pool. It isolates timeouts.")
	e1 := dag.ToolResultNode("toolu_dup_1")
	e2 := dag.ToolResultNode("toolu_dup_2")
	f := dag.FileNode("src/auth.ts")
	addNode(t, src.Graph, dag.Node{ID: e1, Kind: dag.KindToolResult, Turn: 12, Root: root})
	addNode(t, src.Graph, dag.Node{ID: e2, Kind: dag.KindToolResult, Turn: 30, Root: root})
	addNode(t, src.Graph, dag.Node{ID: f, Kind: dag.KindFile, Turn: 12, Ref: "src/auth.ts"})
	// The same explains edge emitted at turn 12 and again at turn 30: AddEdge folds the pair to
	// the earlier turn, so the graph itself yields one turn-12 edge.
	addEdge(t, src.Graph, dag.Edge{From: e1, To: f, Kind: dag.EdgeExplains, Weight: 1, Turn: 12})
	addEdge(t, src.Graph, dag.Edge{From: e1, To: f, Kind: dag.EdgeExplains, Weight: 1, Turn: 30})
	// A distinct explaining node with the same root derives the same (what, why, evidence)
	// triple at turn 30, which only the extractor's own by-ID merge can collapse.
	addEdge(t, src.Graph, dag.Edge{From: e2, To: f, Kind: dag.EdgeExplains, Weight: 1, Turn: 30})

	// The same derivation again with the SCAN ORDER reversed: the turn-30 explainer sits at the
	// lower token position, so its candidate is seen first and the turn-12 one must displace it
	// inside the merge rather than win by arrival.
	root2 := putText(t, src, "Kept the retry cap. Raising it hid the real fault.")
	l30 := dag.ToolResultNode("toolu_dup_3")
	l12 := dag.ToolResultNode("toolu_dup_4")
	f2 := dag.FileNode("src/retry.ts")
	addNode(t, src.Graph, dag.Node{ID: l30, Kind: dag.KindToolResult, Turn: 30, Pos: 1, Root: root2})
	addNode(t, src.Graph, dag.Node{ID: l12, Kind: dag.KindToolResult, Turn: 12, Pos: 9, Root: root2})
	addNode(t, src.Graph, dag.Node{ID: f2, Kind: dag.KindFile, Turn: 12, Ref: "src/retry.ts"})
	addEdge(t, src.Graph, dag.Edge{From: l30, To: f2, Kind: dag.EdgeExplains, Weight: 1, Turn: 30})
	addEdge(t, src.Graph, dag.Edge{From: l12, To: f2, Kind: dag.EdgeExplains, Weight: 1, Turn: 12})

	got, err := checkpoint.ExtractDecisions(ctx, src, 0)
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, d := range got {
		require.Equal(t, core.TurnIndex(12), d.Turn, "the lowest-turn duplicate wins the merge either way round")
	}
}

// TestExtractRanksBySliceScore wires three decisions at slice scores in the ratio 0.9 : 0.1 : 0.5
// (edge weights into the latest user prompt) and asserts the returned order is by descending
// score, not by discovery or turn order.
func TestExtractRanksBySliceScore(t *testing.T) {
	src := newDecisionSource(t)
	ctx := context.Background()

	// The current segment, so the ranking criteria carry both §9 criteria nodes.
	sid, err := src.Segments.Open(ctx, store.Segment{Session: "sess_extract", StartTurn: 8})
	require.NoError(t, err)

	latest := core.TurnIndex(9)
	prompt := dag.UserPromptNode(latest)
	addNode(t, src.Graph, dag.Node{ID: prompt, Kind: dag.KindUserPrompt, Turn: latest, Ref: "prompt"})
	addNode(t, src.Graph, dag.Node{ID: dag.SegmentNode(sid), Kind: dag.KindSegment, Turn: latest, Ref: "segment"})

	weights := []float32{0.9, 0.1, 0.5}
	files := []string{"a.ts", "b.ts", "c.ts"}
	texts := []string{
		"alpha evidence with no terminator",
		"beta evidence with no terminator",
		"gamma evidence with no terminator",
	}
	ids := make([]core.DecisionID, len(weights))
	for i := range weights {
		root := putText(t, src, texts[i])
		expl := dag.ToolResultNode(core.ToolUseID(fmt.Sprintf("toolu_rank_%d", i)))
		f := dag.FileNode(files[i])
		addNode(t, src.Graph, dag.Node{ID: expl, Kind: dag.KindToolResult, Turn: 5, Root: root})
		addNode(t, src.Graph, dag.Node{ID: f, Kind: dag.KindFile, Turn: 5, Ref: files[i]})
		addEdge(t, src.Graph, dag.Edge{From: expl, To: f, Kind: dag.EdgeExplains, Weight: 1, Turn: 5})

		// The id the extraction will mint, computed from the same derivation §9 specifies: a
		// whole unterminated text is its own first two sentences.
		ids[i] = checkpoint.MintDecisionID("changed "+files[i], texts[i], root)

		// Pre-existing decision nodes wired into the latest prompt at the controlled weight, so
		// BackwardSlice scores them proportionally to weights[i]. EdgeConsumes keeps these links
		// invisible to the explains scan.
		dn := dag.DecisionNode(ids[i])
		addNode(t, src.Graph, dag.Node{ID: dn, Kind: dag.KindDecision, Turn: 5, Ref: string(ids[i])})
		addEdge(t, src.Graph, dag.Edge{From: dn, To: prompt, Kind: dag.EdgeConsumes, Weight: weights[i], Turn: latest})
	}

	got, err := checkpoint.ExtractDecisions(ctx, src, 0)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, ids[0], got[0].ID, "score 0.9 first")
	require.Equal(t, ids[2], got[1].ID, "score 0.5 second")
	require.Equal(t, ids[1], got[2].ID, "score 0.1 last")
}

// TestExtractEmitsDecisionNodesAndEdges asserts the DAG emission: every returned decision gains a
// KindDecision node and an explains edge from its evidence node — and a second extraction is a
// fixed point, not an accumulation, because the emitted values are stable.
func TestExtractEmitsDecisionNodesAndEdges(t *testing.T) {
	src := newDecisionSource(t)
	ctx := context.Background()

	root := putText(t, src, "Pinned the retry budget. Exponential backoff thrashed the queue.")
	a := dag.AssistantNode(3)
	f := dag.FileNode("src/retry.ts")
	addNode(t, src.Graph, dag.Node{ID: a, Kind: dag.KindAssistant, Turn: 3, Root: root})
	addNode(t, src.Graph, dag.Node{ID: f, Kind: dag.KindFile, Turn: 3, Ref: "src/retry.ts"})
	addEdge(t, src.Graph, dag.Edge{From: a, To: f, Kind: dag.EdgeExplains, Weight: 1, Turn: 3})

	got, err := checkpoint.ExtractDecisions(ctx, src, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	d := got[0]

	n, ok := src.Graph.Node(dag.DecisionNode(d.ID))
	require.True(t, ok, "the decision node must exist after extraction")
	require.Equal(t, dag.KindDecision, n.Kind)
	require.Equal(t, core.TurnIndex(3), n.Turn)
	require.Equal(t, string(d.ID), n.Ref)
	require.Equal(t, root, n.Root)

	in := src.Graph.In(dag.DecisionNode(d.ID))
	require.Len(t, in, 1)
	require.Equal(t, a, in[0].From)
	require.Equal(t, dag.EdgeExplains, in[0].Kind)
	require.Equal(t, float32(1), in[0].Weight)
	require.Equal(t, core.TurnIndex(3), in[0].Turn)

	// AddNode is a field-wise merge and AddEdge a max-weight/min-turn fold, not no-ops: only
	// stable emitted values make repeated extraction converge instead of drift. Equality, not
	// Contains: the emission adds an evidence→decision explains edge that source (a) would
	// otherwise walk on the next pass, minting a second decision whose what and why are the
	// evidence text restated — a slot spent on nothing. Contains passes either way, which is why
	// the set silently converged one call late for as long as it did.
	second, err := checkpoint.ExtractDecisions(ctx, src, 0)
	require.NoError(t, err)
	require.Equal(t, got, second, "re-extraction over the same range returns the same set, not a longer one")
	n2, ok := src.Graph.Node(dag.DecisionNode(d.ID))
	require.True(t, ok)
	require.Equal(t, n, n2, "re-emission must leave the node value-identical")
	require.Equal(t, in, src.Graph.In(dag.DecisionNode(d.ID)), "re-emission must not duplicate or reshape the edge")

	stAfterSecond := src.Graph.Stats()
	third, err := checkpoint.ExtractDecisions(ctx, src, 0)
	require.NoError(t, err)
	require.Equal(t, second, third, "extraction over the converged graph is a fixed point")
	stAfterThird := src.Graph.Stats()
	require.Equal(t, stAfterSecond.Nodes, stAfterThird.Nodes, "a converged extraction adds no nodes")
	require.Equal(t, stAfterSecond.Edges, stAfterThird.Edges, "a converged extraction adds no edges")
}

// TestExtractCapsAtSixtyFour feeds 200 distinct explains-derived decisions in and requires
// exactly maxDraftDecisions back, kept in the deterministic all-scores-zero order: turn
// descending.
func TestExtractCapsAtSixtyFour(t *testing.T) {
	src := newDecisionSource(t)
	ctx := context.Background()

	f := dag.FileNode("cap.ts")
	addNode(t, src.Graph, dag.Node{ID: f, Kind: dag.KindFile, Turn: 1, Ref: "cap.ts"})
	const total = 200
	for i := 1; i <= total; i++ {
		root := putText(t, src, fmt.Sprintf("evidence body %d with no terminator", i))
		expl := dag.ToolResultNode(core.ToolUseID(fmt.Sprintf("toolu_cap_%03d", i)))
		addNode(t, src.Graph, dag.Node{ID: expl, Kind: dag.KindToolResult, Turn: core.TurnIndex(i), Root: root})
		addEdge(t, src.Graph, dag.Edge{From: expl, To: f, Kind: dag.EdgeExplains, Weight: 1, Turn: core.TurnIndex(i)})
	}

	got, err := checkpoint.ExtractDecisions(ctx, src, 0)
	require.NoError(t, err)
	require.Len(t, got, 64)
	require.Equal(t, core.TurnIndex(total), got[0].Turn, "ties on score break to the most recent turn first")
	require.Equal(t, core.TurnIndex(total-63), got[63].Turn)
}

// TestExtractRespectsFromTurn: with edges at turns 5 and 40 and from=20, only the turn-40
// decision may appear.
func TestExtractRespectsFromTurn(t *testing.T) {
	src := newDecisionSource(t)
	ctx := context.Background()

	oldRoot := putText(t, src, "old rationale before the cut")
	newRoot := putText(t, src, "new rationale after the cut")
	e5 := dag.ToolResultNode("toolu_from_5")
	e40 := dag.ToolResultNode("toolu_from_40")
	f := dag.FileNode("src/auth.ts")
	addNode(t, src.Graph, dag.Node{ID: e5, Kind: dag.KindToolResult, Turn: 5, Root: oldRoot})
	addNode(t, src.Graph, dag.Node{ID: e40, Kind: dag.KindToolResult, Turn: 40, Root: newRoot})
	addNode(t, src.Graph, dag.Node{ID: f, Kind: dag.KindFile, Turn: 5, Ref: "src/auth.ts"})
	addEdge(t, src.Graph, dag.Edge{From: e5, To: f, Kind: dag.EdgeExplains, Weight: 1, Turn: 5})
	addEdge(t, src.Graph, dag.Edge{From: e40, To: f, Kind: dag.EdgeExplains, Weight: 1, Turn: 40})

	got, err := checkpoint.ExtractDecisions(ctx, src, 20)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, core.TurnIndex(40), got[0].Turn)
	require.Equal(t, "new rationale after the cut", got[0].Why)
}

// TestExtractEliminationTurnMapping pins source (b)'s turn derivation against the from-cut: the
// elimination node's Turn when the graph has one, the from-turn as fallback — and covers the
// closed-segment fallback of the ranking criteria on the way (an open-then-closed segment log has
// no current segment, so the newest closed one stands in).
func TestExtractEliminationTurnMapping(t *testing.T) {
	src := newDecisionSource(t)
	ctx := context.Background()

	sid, err := src.Segments.Open(ctx, store.Segment{Session: "sess_extract", StartTurn: 1})
	require.NoError(t, err)
	require.NoError(t, src.Segments.Close(ctx, sid, 40, nil))

	ev := putText(t, src, "shared evidence for the elimination rows")
	record := func(target, approach string) string {
		t.Helper()
		id, err := src.Ledger.Record(ctx, negknow.Record{
			Target: target, Approach: approach,
			Reason: "did not hold", Evidence: ev,
		})
		require.NoError(t, err)
		return id
	}
	late := record("t1", "late approach")
	record("t2", "fallback approach") // no graph node: its turn falls back to from
	early := record("t3", "early approach")
	addNode(t, src.Graph, dag.Node{ID: dag.EliminationNode(late), Kind: dag.KindElimination, Turn: 30, Ref: late})
	addNode(t, src.Graph, dag.Node{ID: dag.EliminationNode(early), Kind: dag.KindElimination, Turn: 5, Ref: early})

	got, err := checkpoint.ExtractDecisions(ctx, src, 20)
	require.NoError(t, err)
	require.Len(t, got, 2, "the turn-5 elimination falls before the cut")
	require.Equal(t, `rejected "late approach" for t1`, got[0].What)
	require.Equal(t, core.TurnIndex(30), got[0].Turn, "turn comes from the elimination node when present")
	require.Equal(t, `rejected "fallback approach" for t2`, got[1].What)
	require.Equal(t, core.TurnIndex(20), got[1].Turn, "no elimination node, so turn falls back to from")
}

// TestExtractSurvivesEmissionFailure forces the one emission error constructible through the
// public API — an explaining node that IS the decision node its own derivation mints, so the
// emitted explains edge is a self-loop the graph rejects — and requires the decision to be
// returned anyway, per §9's "errors from AddNode/AddEdge never fail the extraction".
func TestExtractSurvivesEmissionFailure(t *testing.T) {
	src := newDecisionSource(t)
	ctx := context.Background()

	text := "Loop guard chosen deliberately."
	root := putText(t, src, text)
	what := "changed loop.ts"
	id := checkpoint.MintDecisionID(what, text, root)

	expl := dag.DecisionNode(id)
	f := dag.FileNode("loop.ts")
	addNode(t, src.Graph, dag.Node{ID: expl, Kind: dag.KindDecision, Turn: 7, Ref: string(id), Root: root})
	addNode(t, src.Graph, dag.Node{ID: f, Kind: dag.KindFile, Turn: 7, Ref: "loop.ts"})
	addEdge(t, src.Graph, dag.Edge{From: expl, To: f, Kind: dag.EdgeExplains, Weight: 1, Turn: 7})

	got, err := checkpoint.ExtractDecisions(ctx, src, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, id, got[0].ID)
	require.Empty(t, src.Graph.In(dag.DecisionNode(id)), "the self-loop edge was rejected, not stored")
}

// TestExtractSurvivesStoreReadFailure: an explaining node whose root is absent from the store is
// skipped, counted on checkpoint.decision_read_error, and never an error.
func TestExtractSurvivesStoreReadFailure(t *testing.T) {
	src := newDecisionSource(t)
	ctx := context.Background()

	reg := obs.New(testutil.NewFakeClock(testutil.Epoch))
	checkpoint.SetObservers(logging.Nop(), reg)
	t.Cleanup(func() { checkpoint.SetObservers(nil, nil) })

	absent := core.Hash{0xAB, 0xCD}
	a := dag.AssistantNode(4)
	f := dag.FileNode("src/auth.ts")
	addNode(t, src.Graph, dag.Node{ID: a, Kind: dag.KindAssistant, Turn: 4, Root: absent})
	addNode(t, src.Graph, dag.Node{ID: f, Kind: dag.KindFile, Turn: 4, Ref: "src/auth.ts"})
	addEdge(t, src.Graph, dag.Edge{From: a, To: f, Kind: dag.EdgeExplains, Weight: 1, Turn: 4})

	got, err := checkpoint.ExtractDecisions(ctx, src, 0)
	require.NoError(t, err, "a read failure skips the decision, it does not fail the extraction")
	require.Empty(t, got)
	require.Equal(t, int64(1), reg.Counter("checkpoint.decision_read_error").Value())

	// A readable explainer pointing at a DECISION target whose own root is absent skips the same
	// way — the target's text is part of the derivation — and a dangling edge (target node never
	// observed) is skipped silently, with no counter, because there is nothing to read.
	readable := putText(t, src, "Reason text that reads fine.")
	b := dag.AssistantNode(5)
	tgt := dag.DecisionNode("dec_unreadable01")
	addNode(t, src.Graph, dag.Node{ID: b, Kind: dag.KindAssistant, Turn: 5, Root: readable})
	addNode(t, src.Graph, dag.Node{ID: tgt, Kind: dag.KindDecision, Turn: 5, Ref: "dec_unreadable01", Root: core.Hash{0xEE}})
	addEdge(t, src.Graph, dag.Edge{From: b, To: tgt, Kind: dag.EdgeExplains, Weight: 1, Turn: 5})
	addEdge(t, src.Graph, dag.Edge{From: b, To: dag.FileNode("never/observed.ts"), Kind: dag.EdgeExplains, Weight: 1, Turn: 5})

	got, err = checkpoint.ExtractDecisions(ctx, src, 0)
	require.NoError(t, err)
	require.Empty(t, got)
	require.Equal(t, int64(3), reg.Counter("checkpoint.decision_read_error").Value(),
		"one more failure per unreadable root per pass; the dangling edge adds none")
}

// TestExtractDecisionTargetAndRuneCaps covers the tgt.Kind == KindDecision branch of what — the
// first sentence of the TARGET's text — and both §9 rune caps, at rune boundaries, proven with
// multi-byte runes.
func TestExtractDecisionTargetAndRuneCaps(t *testing.T) {
	src := newDecisionSource(t)
	ctx := context.Background()

	longWhat := strings.Repeat("é", 200) + ". Ignored tail."
	longWhy := strings.Repeat("б", 250) + ". " + strings.Repeat("в", 250) + ". Third sentence."
	tgtRoot := putText(t, src, longWhat)
	explRoot := putText(t, src, longWhy)

	tgt := dag.DecisionNode("dec_target000001")
	expl := dag.AssistantNode(6)
	addNode(t, src.Graph, dag.Node{ID: tgt, Kind: dag.KindDecision, Turn: 6, Ref: "dec_target000001", Root: tgtRoot})
	addNode(t, src.Graph, dag.Node{ID: expl, Kind: dag.KindAssistant, Turn: 6, Root: explRoot})
	addEdge(t, src.Graph, dag.Edge{From: expl, To: tgt, Kind: dag.EdgeExplains, Weight: 1, Turn: 6})

	got, err := checkpoint.ExtractDecisions(ctx, src, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	d := got[0]

	require.Equal(t, 160, utf8.RuneCountInString(d.What), "what caps at 160 runes")
	require.True(t, utf8.ValidString(d.What), "the cap must cut at a rune boundary")
	require.Equal(t, strings.Repeat("é", 160), d.What)

	require.Equal(t, 400, utf8.RuneCountInString(d.Why), "why caps at 400 runes")
	require.True(t, utf8.ValidString(d.Why))
	require.True(t, strings.HasPrefix(d.Why, strings.Repeat("б", 250)+". "))
	require.NotContains(t, d.Why, "Third")
}

// TestExtractVerbForTargets pins the non-decision target verbs: changed for files (covered by the
// fixture test), ran for tool uses, modified for symbols, and "decided about" for everything else.
func TestExtractVerbForTargets(t *testing.T) {
	src := newDecisionSource(t)
	ctx := context.Background()

	root := putText(t, src, "Because the CLI hung on the second retry. Details follow.")
	expl := dag.AssistantNode(2)
	addNode(t, src.Graph, dag.Node{ID: expl, Kind: dag.KindAssistant, Turn: 2, Root: root})

	use := dag.ToolUseNode("toolu_verb")
	sym := dag.SymbolNode("src/x.ts", "refresh")
	res := dag.ToolResultNode("toolu_verb")
	addNode(t, src.Graph, dag.Node{ID: use, Kind: dag.KindToolUse, Turn: 2, Ref: "Bash"})
	addNode(t, src.Graph, dag.Node{ID: sym, Kind: dag.KindSymbol, Turn: 2, Ref: "src/x.ts#refresh"})
	addNode(t, src.Graph, dag.Node{ID: res, Kind: dag.KindToolResult, Turn: 2, Ref: "toolu_verb"})
	for _, to := range []dag.NodeID{use, sym, res} {
		addEdge(t, src.Graph, dag.Edge{From: expl, To: to, Kind: dag.EdgeExplains, Weight: 1, Turn: 2})
	}

	got, err := checkpoint.ExtractDecisions(ctx, src, 0)
	require.NoError(t, err)
	whats := make([]string, 0, len(got))
	for _, d := range got {
		whats = append(whats, d.What)
	}
	require.ElementsMatch(t, []string{
		"ran Bash",
		"modified src/x.ts#refresh",
		"decided about toolu_verb",
	}, whats)
}

// TestExtractValidatesSourcesAndContext pins the only two error paths §9 allows: a SourceSet that
// fails Validate, and a cancelled context.
func TestExtractValidatesSourcesAndContext(t *testing.T) {
	_, err := checkpoint.ExtractDecisions(context.Background(), checkpoint.SourceSet{}, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Store", "Validate names the first nil seam")

	src := newDecisionSource(t)
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Once with an empty graph (the cancellation surfaces at the ledger/pin sources) and once
	// with a node present (it surfaces inside the explains walk).
	_, err = checkpoint.ExtractDecisions(cctx, src, 0)
	require.ErrorIs(t, err, context.Canceled)
	addNode(t, src.Graph, dag.Node{ID: dag.AssistantNode(1), Kind: dag.KindAssistant, Turn: 1})
	_, err = checkpoint.ExtractDecisions(cctx, src, 0)
	require.ErrorIs(t, err, context.Canceled)
}

// BenchmarkExtractDecisions runs the full extraction over the §6.4-shaped synthetic session —
// dagtest's benchSpec shape, ~5,000 nodes — with every node's root pointing at real stored
// content, so the store reads, sentence splits, minting, slicing and emission are all inside the
// measured op. Budget (plan §9): < 20 ms/op.
func BenchmarkExtractDecisions(b *testing.B) {
	cfg := config.Defaults()
	ctx := context.Background()

	s, err := store.Open(b.TempDir(), cfg, store.Deps{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	g, err := dag.Open(b.TempDir(), cfg, logging.Nop())
	if err != nil {
		b.Fatal(err)
	}
	led, err := negknow.Open(b.TempDir(), cfg, nil, negknow.Deps{Session: "sess_bench"})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = led.Close() })
	ps, err := pins.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	src := checkpoint.SourceSet{
		Store: s, Segments: s.Segments(), Ledger: led, Pins: ps,
		Graph: g, Grammar: grammar.New(), Tokens: tokens.New(cfg, ""),
	}

	nodes, edges, _, _ := dagtest.Synth(7, dagtest.SynthSpec{
		Turns:                  400,
		ToolsPerTurn:           3,
		Files:                  40,
		SymbolsPerFile:         6,
		ControlOnlyFraction:    0.35,
		ControlCarriedFraction: 0.12,
		Supersessions:          30,
		TokensPerTool:          600,
	})
	roots := make([]core.Hash, 32)
	for i := range roots {
		res, err := s.PutBytes(ctx, []byte(fmt.Sprintf(
			"Decision evidence %d: the approach was chosen after profiling. It held at p99 under load %d. Trailing detail.",
			i, i)), store.PutOptions{})
		if err != nil {
			b.Fatal(err)
		}
		roots[i] = res.Root.Hash
	}
	// Synth nodes carry zero roots; give every node real, readable content so no read is skipped.
	for i := range nodes {
		nodes[i].Root = roots[i%len(roots)]
	}
	dagtest.Load(b, g, nodes, edges)
	st := g.Stats()
	b.Logf("graph: nodes=%d edges=%d explains=%d", st.Nodes, st.Edges, st.EdgesByKind[dag.EdgeExplains.String()])

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := checkpoint.ExtractDecisions(ctx, src, 0); err != nil {
			b.Fatal(err)
		}
	}
}
