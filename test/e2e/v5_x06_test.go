// V5 §4.6 — SP-12's live p-selection gate against SP-15's real selector, both real for the first
// time: "SP-12/SP-15 local capability guards replace native-p-selection prerequisites."
//
// The guard under test is scheduler.PSelectionAvailable(), the closing-note-3 ship-order gate.
// What makes it a LOCAL capability guard rather than a native-p-selection prerequisite is who
// opens it: the shipped daemon.NewSchedulerRuntime, after it has verified it can assemble
// candidates over a real store and DAG, and the shipped Close on the same runtime shuts it
// again. Nothing here asks the host for a compaction point, and no test-only probe is used —
// SP-12 removed SetPSelectionProbe, and this row would be worthless if it flipped the gate by
// hand: both sides are the shipped implementations, and the flag moves only because they ran.
//
// Wired: real hook events through the real binary into the real observer, store and DAG; a real
// scheduler.Runtime built by daemon.NewSchedulerRuntime over that store and DAG; the real
// scheduler.Evaluate choosing p from candidates whose positions are the real DAG's; the real
// analyzer.NewCheapScorer reading real content roots out of the real store; and the real
// analyzer.NewSelector / analyzer.Propose — the SP-15 surface the daemon composition root calls —
// gated by that runtime's lifetime.
package e2e

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/testutil"
)

// x6v5Session is this row's session identity.
const x6v5Session = core.SessionID("sess-e2e-v5-x06")

// x6v5Prefix names the seeded tool uses (toolu_<prefix>_NN over src/<prefix>_NN.go).
const x6v5Prefix = "v5x06"

// x6v5Turns is how many distinct tool results the observer indexes. Eight is enough that the cut
// the scheduler chooses leaves several blocks behind it for the selector to rank, and few enough
// that the real binary's per-hook cost stays in the noise.
const x6v5Turns = 8

// x6v5CutCandidates is how many of the seeded positions are offered to the scheduler as cut
// points: the EARLIER half. A cut point is a changepoint ∩ round boundary, which is always a
// subset of the prefix's positions, and confining the ladder to the first half guarantees that
// whichever candidate the scheduler prefers, at least x6v5Turns-x6v5CutCandidates real blocks sit
// at or after p — so the selector below has a candidate set worth ranking. This is a fixture
// choice about WHICH positions are offered, not a claim about how the runtime's own BOCD would
// have picked them; see the disposition file's assertion map.
const x6v5CutCandidates = x6v5Turns / 2

// x6v5PosBeforeP is how far before p the negative-control's illegal block is placed. Any positive
// distance is illegal under §13 invariant 4; one token is the tightest.
const x6v5PosBeforeP = 1

// x6v5Candidates builds the scheduler's cut-point ladder from the REAL DAG's tool-result nodes.
//
// Each candidate's Pos, Turn and Coupling come from the graph the observer built out of the
// hook events; ReclaimableTokens is the token mass of the tool results at or after that
// position, which is non-increasing in Pos exactly as §5.4 requires. Every candidate sits on a
// round boundary so eligible() never has to relax.
func x6v5Candidates(t *testing.T, g dag.Graph) []scheduler.Candidate {
	t.Helper()
	results := x6v5ToolResults(g)
	require.GreaterOrEqual(t, len(results), x6v5Turns,
		"the observer must have indexed every seeded tool result into the real DAG")

	out := make([]scheduler.Candidate, 0, x6v5CutCandidates)
	for _, n := range results[:x6v5CutCandidates] {
		var reclaimable core.Tokens
		for _, m := range results {
			if m.Pos >= n.Pos {
				reclaimable += m.Tokens
			}
		}
		out = append(out, scheduler.Candidate{
			Pos:               n.Pos,
			Turn:              n.Turn,
			SegmentID:         core.SegmentID(n.Turn),
			RoundBoundary:     true,
			ReclaimableTokens: reclaimable,
			Coupling:          g.CrossingEdges(n.Pos),
		})
	}
	return out
}

// x6v5ToolResults returns the real DAG's tool-result nodes, ascending by Pos, with the positions
// deduplicated (two results at one position would be one cut point, not two).
func x6v5ToolResults(g dag.Graph) []dag.Node {
	var out []dag.Node
	for _, n := range g.NodesAfter(0) {
		if n.Kind != dag.KindToolResult {
			continue
		}
		out = append(out, n)
	}
	slices.SortStableFunc(out, func(a, b dag.Node) int { return cmp.Compare(a.Pos, b.Pos) })
	out = slices.CompactFunc(out, func(a, b dag.Node) bool { return a.Pos == b.Pos })
	return out
}

// x6v5WaitGraph drives Drain until the real DAG holds every seeded tool result. WaitIndexed
// returns on the INDEX line, and the graph stage of the same pipeline can still be a record
// behind it; this row's subject is the graph, so it waits on the graph.
func x6v5WaitGraph(t *testing.T, r *v4Rig) {
	t.Helper()
	ctx := context.Background()
	require.Eventually(t, func() bool {
		_, _ = r.D.Drain(ctx)
		return len(x6v5ToolResults(r.Opts.Graph)) >= x6v5Turns
	}, obsProcessBound, obsProcessTick,
		"the real DAG never received all %d seeded tool results: %s", x6v5Turns, obsWaitDiag{r.P.Root})
}

// x6v5Blocks projects the real DAG's tool-result nodes at or after p onto the selector's Block
// shape — the same fields the daemon's own projection carries: id, position, cost, kind, content
// root, ephemerality.
func x6v5Blocks(t *testing.T, g dag.Graph, p int) []analyzer.Block {
	t.Helper()
	var out []analyzer.Block
	for _, n := range g.NodesAfter(p) {
		if n.Kind != dag.KindToolResult {
			continue
		}
		out = append(out, analyzer.Block{
			ID: n.ID, Pos: n.Pos, Tokens: n.Tokens, Kind: n.Kind, Root: n.Root, Ephemeral: n.Ephemeral,
		})
	}
	return out
}

// x6v5Continuation is what the session "did next": it touched every seeded path and named every
// seeded symbol, so the real cheap scorer, reading each block's real content root back out of
// the real store, has something to overlap with.
func x6v5Continuation() analyzer.Continuation {
	c := analyzer.Continuation{FromTurn: core.TurnIndex(x6v5Turns)}
	for i := range x6v5Turns {
		c.Paths = append(c.Paths, fmt.Sprintf("src/%s_%02d.go", x6v5Prefix, i))
		c.Symbols = append(c.Symbols, fmt.Sprintf("handler%02d", i))
	}
	c.Text = []byte("package " + x6v5Prefix + " handler error nil")
	return c
}

// x6v5Runtime builds the shipped scheduler Runtime over the rig's real store and DAG, exactly as
// internal/cli's wireScheduler composes it.
func x6v5Runtime(t *testing.T, p *testutil.Project, r *v4Rig) scheduler.Runtime {
	t.Helper()
	rt, err := daemon.NewSchedulerRuntime(daemon.SchedulerRuntimeOptions{
		ProjectRoot: p.Root,
		Session:     x6v5Session,
		Cfg:         p.Cfg,
		Clock:       core.SystemClock(),
		Log:         p.Log,
		Metrics:     r.Opts.Metrics,
		Store:       r.Opts.Store,
		Graph:       r.Opts.Graph,
		LedgerFn:    r.LedgerFn(),
		Checkpoints: r.W,
		Sources:     r.Src,
		Getenv:      func(string) string { return "" },
	})
	require.NoError(t, err, "the shipped scheduler Runtime must compose over the real store and DAG")
	return rt
}

// x6v5Propose is the SP-15 surface the daemon composition root actually calls
// (internal/daemon/rehydrate_selection.go): Propose routes through NewSelector, so the gate that
// refuses a Selector refuses a Proposal through the same code.
func x6v5Propose(ctx context.Context, p int, blocks []analyzer.Block, lambda float64, budget core.Tokens) (analyzer.Proposal, error) {
	cands := make([]analyzer.Candidate, 0, len(blocks))
	for _, b := range blocks {
		cands = append(cands, analyzer.Candidate{
			Item: b.ID, Pos: b.Pos, Weight: 1,
			Reps: []analyzer.Representation{{
				Item: b.ID, Kind: analyzer.RepExactSpan, Coverage: 1, AssembledCost: b.Tokens,
				Prov: analyzer.Provenance{Origin: b.ID, Root: b.Root},
			}},
		})
	}
	return analyzer.Propose(ctx, p, cands, lambda, budget)
}

// TestV5_SelectorGatedByRealScheduler is V5-VERIFY §4.6.
//
// The gate is process-global (the only package-level mutable state in scheduler) and
// TestV4_SchedulerFiresBeforeTheSimulatedStockThreshold constructs a Runtime it never closes, so a
// whole-package run reaches this row with the gate already open. The row therefore establishes
// its own precondition through the gate's documented test contract ("tests MUST defer
// DisablePSelection") and asserts the transitions it then CAUSES: closed → open by the shipped
// constructor, open → closed by the shipped Close, closed → open again by a second constructor.
//
// The negative control is the third subtest: the same p, the same real blocks and the same
// arguments that constructed a Selector a moment ago are refused once the real Runtime is
// closed — a real switch, on the shipped shutdown path, with no test hook involved.
func TestV5_SelectorGatedByRealScheduler(t *testing.T) {
	ctx := context.Background()
	scheduler.DisablePSelection()
	t.Cleanup(scheduler.DisablePSelection)

	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	r := v4StartRig(t, p)
	env := e2eEnv(p)

	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x6v5Session), env)
	obsRunHook(t, r.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, p.Root, x6v5Session, "select a keep-set behind the scheduler's cut"), env)
	r.SeedTurns(t, x6v5Session, x6v5Prefix, x6v5Turns)
	x6v5WaitGraph(t, r)

	lambda := p.Cfg.Selection.Submodular.Lambda
	all := x6v5Blocks(t, r.Opts.Graph, 0)
	require.Len(t, all, x6v5Turns, "every seeded tool result must be a real DAG block")

	// ── (a) no Runtime in this process: the local capability guard refuses ───────────────────────
	t.Run("refuses_before_any_runtime", func(t *testing.T) {
		require.False(t, scheduler.PSelectionAvailable(),
			"no scheduler Runtime has been constructed in this process yet")

		sel, err := analyzer.NewSelector(0, all, dag.Slice{}, nil, lambda, true)
		require.ErrorIs(t, err, core.ErrNotImplemented,
			"NewSelector must refuse while no local p-selection capability exists (closing note 3)")
		require.Nil(t, sel, "a refused construction must not hand back a Selector")

		_, err = x6v5Propose(ctx, 0, all, lambda, x6v5TotalTokens(all))
		require.ErrorIs(t, err, core.ErrNotImplemented,
			"Propose — the surface the daemon calls — must refuse through the same constructor")
	})

	// ── (b) the shipped constructor opens the gate; the real scheduler chooses p ─────────────────
	rt := x6v5Runtime(t, p, r)
	require.True(t, scheduler.PSelectionAvailable(),
		"NewSchedulerRuntime must open the gate once it has a real store, graph and segment log")

	live, err := rt.Evaluate(ctx)
	require.NoError(t, err)
	require.Positive(t, live.HardCeilingTokens,
		"a real Runtime must resolve a real window ladder, not the error_no_window branch: %v", live.Breakdown)

	effective := scheduler.EffectiveWindow(scheduler.HostDefaultContextWindow, scheduler.HostDefaultMaxOutput)
	cands := x6v5Candidates(t, r.Opts.Graph)
	decision := scheduler.Evaluate(scheduler.Inputs{
		Now:             core.UnixMilli(testutil.Epoch.UnixMilli()),
		ContextTokens:   effective, // the window is full: the hard ceiling fires unconditionally
		EffectiveWindow: effective,
		MaxOutputTokens: scheduler.HostDefaultMaxOutput,
		Candidates:      cands,
		Cfg:             p.Cfg.Scheduler,
	})
	require.True(t, decision.ShouldCompact,
		"a full window over real cut candidates must decide to compact: %v", decision.Breakdown)
	require.True(t, slices.ContainsFunc(cands, func(c scheduler.Candidate) bool { return c.Pos == decision.P.Pos }),
		"the chosen p must be one of the real DAG positions offered: p=%d", decision.P.Pos)
	pCut := decision.P.Pos

	blocks := x6v5Blocks(t, r.Opts.Graph, pCut)
	require.GreaterOrEqual(t, len(blocks), x6v5Turns-x6v5CutCandidates,
		"the cut must leave the later half of the prefix for the selector")
	// One block short of the whole candidate mass, so the budget BINDS: a selector that kept
	// everything would satisfy "Tokens ≤ budget" only by never having been constrained.
	budget := x6v5TotalTokens(blocks) - x6v5MinTokens(blocks)
	require.Positive(t, budget, "real blocks carry real token costs")
	t.Logf("p=%d chosen from %d real candidates; %d real blocks after p; budget %d of %d tokens",
		pCut, len(cands), len(blocks), int(budget), int(x6v5TotalTokens(blocks)))

	delta, err := analyzer.NewCheapScorer(r.Opts.Store).Score(ctx, blocks, x6v5Continuation())
	require.NoError(t, err)
	require.Len(t, delta, len(blocks), "the cheap tier scores every block it is handed")
	require.True(t, slices.ContainsFunc(blocks, func(b analyzer.Block) bool { return delta[b.ID] > 0 }),
		"the real store's content must overlap the continuation somewhere, or nothing is worth keeping: %v", delta)

	var first analyzer.Selection
	t.Run("real_runtime_opens_the_gate_and_the_selector_selects", func(t *testing.T) {
		sel, err := analyzer.NewSelector(pCut, blocks, dag.Slice{}, delta, lambda, true)
		require.NoError(t, err, "with a real Runtime live the same constructor must succeed")
		require.Equal(t, pCut, sel.P(), "the selector's p is the one the real scheduler chose")

		first, err = sel.Select(ctx, budget)
		require.NoError(t, err)
		require.NotEmpty(t, first.Keep, "positively scored blocks within budget must be kept")
		require.NotEmpty(t, first.Dropped, "a binding budget must leave something behind")
		require.LessOrEqual(t, first.Tokens, budget, "Tokens never exceeds the budget")
		t.Logf("kept %d, dropped %d, %d tokens, value %.3f, %d iters",
			len(first.Keep), len(first.Dropped), int(first.Tokens), first.Value, first.Iters)
		require.Equal(t, x6v5KeptTokens(blocks, first.Keep), first.Tokens,
			"Tokens is the true sum over Keep")
		for _, id := range first.Keep {
			n, ok := r.Opts.Graph.Node(id)
			require.True(t, ok, "kept id %s must be a real DAG node", id)
			require.GreaterOrEqual(t, n.Pos, pCut,
				"every kept block sits at or after p (§13 invariant 4): %s at %d, p=%d", id, n.Pos, pCut)
		}
		require.ElementsMatch(t, x6v5IDs(blocks), append(slices.Clone(first.Keep), first.Dropped...),
			"Keep and Dropped partition the candidate set exactly")

		prop, err := x6v5Propose(ctx, pCut, blocks, lambda, budget)
		require.NoError(t, err, "the daemon's Propose surface must construct through the open gate too")
		require.False(t, prop.Overflow)
		require.LessOrEqual(t, prop.Tokens, budget)
	})

	// ── (c) NEGATIVE CONTROL: the shipped Close shuts the gate and the same call is refused ──────
	t.Run("closing_the_runtime_closes_the_gate", func(t *testing.T) {
		require.NoError(t, daemon.CloseSchedulerRuntime(rt),
			"the shipped shutdown path must close the daemon's runtime")
		require.False(t, scheduler.PSelectionAvailable(),
			"Close must withdraw the local p-selection capability")

		sel, err := analyzer.NewSelector(pCut, blocks, dag.Slice{}, delta, lambda, true)
		require.ErrorIs(t, err, core.ErrNotImplemented,
			"NEGATIVE CONTROL: the arguments that constructed a Selector a moment ago must be "+
				"refused once the real Runtime is closed")
		require.Nil(t, sel)

		_, err = x6v5Propose(ctx, pCut, blocks, lambda, budget)
		require.ErrorIs(t, err, core.ErrNotImplemented,
			"NEGATIVE CONTROL: Propose is refused through the same closed gate")

		// The guard order is normative: the §13 invariant-4 refusal runs FIRST, so a block before
		// p is reported as the invariant violation even while the ship-order gate is closed.
		illegal := append(slices.Clone(blocks), analyzer.Block{
			ID: dag.NodeID("x6v5-before-p"), Pos: pCut - x6v5PosBeforeP, Tokens: 1,
		})
		_, err = analyzer.NewSelector(pCut, illegal, dag.Slice{}, delta, lambda, true)
		require.ErrorIs(t, err, core.ErrBudget,
			"a block before p is refused as the invariant-4 violation, ahead of the ship-order gate")
	})

	// ── (d) authorized recovery: a new Runtime over the same store re-opens the gate ─────────────
	t.Run("a_new_runtime_recovers_the_gate", func(t *testing.T) {
		again := x6v5Runtime(t, p, r)
		t.Cleanup(func() { require.NoError(t, daemon.CloseSchedulerRuntime(again)) })
		require.True(t, scheduler.PSelectionAvailable(),
			"the daemon outlives sessions: the next constructor re-opens the gate")

		sel, err := analyzer.NewSelector(pCut, blocks, dag.Slice{}, delta, lambda, true)
		require.NoError(t, err)
		second, err := sel.Select(ctx, budget)
		require.NoError(t, err)
		require.Equal(t, first.Keep, second.Keep,
			"selection over the same real inputs is deterministic across a close/reopen")
		require.Equal(t, first.Tokens, second.Tokens)
	})

	p.AssertAppendOnly(t)
}

// x6v5TotalTokens sums the blocks' token costs.
func x6v5TotalTokens(blocks []analyzer.Block) core.Tokens {
	var total core.Tokens
	for _, b := range blocks {
		total += b.Tokens
	}
	return total
}

// x6v5MinTokens is the cheapest block's token cost.
func x6v5MinTokens(blocks []analyzer.Block) core.Tokens {
	least := blocks[0].Tokens
	for _, b := range blocks[1:] {
		least = min(least, b.Tokens)
	}
	return least
}

// x6v5KeptTokens sums the token costs of the blocks named in keep.
func x6v5KeptTokens(blocks []analyzer.Block, keep []dag.NodeID) core.Tokens {
	var total core.Tokens
	for _, b := range blocks {
		if slices.Contains(keep, b.ID) {
			total += b.Tokens
		}
	}
	return total
}

// x6v5IDs lists the blocks' ids.
func x6v5IDs(blocks []analyzer.Block) []dag.NodeID {
	out := make([]dag.NodeID, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, b.ID)
	}
	return out
}
