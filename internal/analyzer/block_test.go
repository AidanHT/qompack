package analyzer_test

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// diagChangelog is the one path both halves of the shared-file counterexample touch. Two tool uses
// on one path is exactly what mints the shared_file edges the fixture is about.
const diagChangelog = "docs/CHANGELOG.md"

// diagGraph opens a real dependence graph. Real rather than a double for the same reason diagStore
// is: the edge KINDS and their directions are the thing under test, and a hand-built double would
// let this file assert against its own idea of them instead of against dag.BuildToolUse's.
func diagGraph(t *testing.T) dag.Graph {
	t.Helper()
	g, err := dag.Open(t.TempDir(), config.Defaults(), logging.Nop())
	require.NoError(t, err)
	return g
}

// diagNode is one node, spelled out, for the cases that do not need a whole builder chain.
func diagNode(id dag.NodeID, kind dag.NodeKind, pos int, tokens core.Tokens) dag.Node {
	return dag.Node{ID: id, Kind: kind, Pos: pos, Tokens: tokens, Turn: core.TurnIndex(pos)}
}

// ── NewBlocks ──────────────────────────────────────────────────────────────────────────────────

// TestNewBlocks_ProjectsEveryFieldAndSortsById pins the projection itself: each field comes off the
// node, and the result is ordered by id so that contract §3's candidate ordering does not have to
// be re-established downstream.
func TestNewBlocks_ProjectsEveryFieldAndSortsById(t *testing.T) {
	nodes := []dag.Node{
		{ID: dag.FileNode("z/last.go"), Kind: dag.KindFile, Pos: 30, Turn: 3},
		{ID: dag.FileNode("a/first.go"), Kind: dag.KindFile, Pos: 10, Tokens: 12, Root: core.Hash{0x07}},
		{ID: dag.AssistantNode(2), Kind: dag.KindAssistant, Pos: 20, Ephemeral: true},
	}

	blocks, err := analyzer.NewBlocks(context.Background(), nil, nodes)
	require.NoError(t, err, "none of these kinds can be superseded, so no store is needed")
	require.Len(t, blocks, 3)

	ids := make([]dag.NodeID, len(blocks))
	for i, b := range blocks {
		ids[i] = b.ID
	}
	require.Equal(t, []dag.NodeID{dag.AssistantNode(2), dag.FileNode("a/first.go"), dag.FileNode("z/last.go")}, ids,
		"blocks come back sorted by id ascending")

	first := blocks[1]
	require.Equal(t, dag.KindFile, first.Kind)
	require.Equal(t, 10, first.Pos)
	require.Equal(t, core.Tokens(12), first.Tokens)
	require.Equal(t, core.Hash{0x07}, first.Root)
	require.True(t, blocks[0].Ephemeral, "Ephemeral travels with the block: §8.7 makes it the first eviction candidate")
}

// TestNewBlocks_DeduplicatesNodeIds asserts a node named twice becomes one block. Two blocks with
// one id would charge one node's tokens twice against a single budget and credit its coverage
// twice against a single objective.
func TestNewBlocks_DeduplicatesNodeIds(t *testing.T) {
	n := diagNode(dag.FileNode("a.go"), dag.KindFile, 1, 5)

	blocks, err := analyzer.NewBlocks(context.Background(), nil, []dag.Node{n, n, n})
	require.NoError(t, err)
	require.Len(t, blocks, 1)
}

// TestNewBlocks_ReadsSupersessionFromTheStore is why the projection takes a store at all. §8.1
// item 3 records supersession on store.ToolUseRecord.Status, not on the node, so a Block that did
// not consult the index could never mark a superseded read as the first eviction candidate.
func TestNewBlocks_ReadsSupersessionFromTheStore(t *testing.T) {
	s := diagStore(t)
	ctx := context.Background()

	older := diagRecord(t, s, diagToolUse{
		ID: "toolu_older", Session: diagSession, Turn: 1, TS: diagEpoch + 1000,
		Tool: "Read", Path: diagChangelog, Content: diagFixture(t, fixtureRelation, "changelog_release_notes.txt"),
	})
	newer := diagRecord(t, s, diagToolUse{
		ID: "toolu_newer", Session: diagSession, Turn: 2, TS: diagEpoch + 2000,
		Tool: "Read", Path: diagChangelog, Content: diagFixture(t, fixtureRelation, "changelog_typo_fix.txt"),
	})
	require.NoError(t, s.MarkSuperseded(ctx, older.ID, newer.ID))

	blocks, err := analyzer.NewBlocks(ctx, s, []dag.Node{
		diagNode(dag.ToolUseNode(older.ID), dag.KindToolUse, 1, 0),
		diagNode(dag.ToolUseNode(newer.ID), dag.KindToolUse, 2, 0),
	})
	require.NoError(t, err, "both records exist, so supersession coverage is complete")

	byID := map[dag.NodeID]analyzer.Block{}
	for _, b := range blocks {
		byID[b.ID] = b
	}
	require.True(t, byID[dag.ToolUseNode(older.ID)].Superseded)
	require.False(t, byID[dag.ToolUseNode(newer.ID)].Superseded)
}

// TestNewBlocks_UnestablishedSupersessionIsDegradedNotFalse is the honesty rule this file is built
// around. Block.Superseded is a two-valued field over a three-valued question, and a nil store or a
// record the index does not hold leaves the third answer. The blocks still come back — dropping
// them would be worse — but the caller is TOLD the coverage is partial instead of reading `false`
// as "this read is current".
func TestNewBlocks_UnestablishedSupersessionIsDegradedNotFalse(t *testing.T) {
	toolNodes := []dag.Node{
		diagNode(dag.ToolUseNode("toolu_unknown"), dag.KindToolUse, 1, 0),
		diagNode(dag.ToolResultNode("toolu_unknown"), dag.KindToolResult, 2, 0),
	}

	t.Run("nil_store", func(t *testing.T) {
		blocks, err := analyzer.NewBlocks(context.Background(), nil, toolNodes)
		require.Len(t, blocks, 2, "the blocks are returned in full")
		require.ErrorIs(t, err, core.ErrDegraded)
		require.Contains(t, err.Error(), "unestablished")
		for _, b := range blocks {
			require.False(t, b.Superseded, "the field has no third value; the error carries the third answer")
		}
	})

	t.Run("record_absent_from_the_index", func(t *testing.T) {
		blocks, err := analyzer.NewBlocks(context.Background(), diagStore(t), toolNodes)
		require.Len(t, blocks, 2)
		require.ErrorIs(t, err, core.ErrDegraded,
			"a tool use the store has no record of is one this scan cannot speak for")
	})

	t.Run("kinds_that_cannot_be_superseded_need_no_store", func(t *testing.T) {
		blocks, err := analyzer.NewBlocks(context.Background(), nil, []dag.Node{
			diagNode(dag.FileNode("a.go"), dag.KindFile, 1, 0),
			diagNode(dag.AssistantNode(1), dag.KindAssistant, 2, 0),
			diagNode(dag.DecisionNode("dec_1"), dag.KindDecision, 3, 0),
		})
		require.NoError(t, err, "a file anchor is not a read another read can replace")
		require.Len(t, blocks, 3)
	})
}

// TestNewBlocks_AnEmptyNodeSetIsNotAnError pins the degenerate case: no nodes is no blocks, and a
// prefix with nothing left in it is the normal end of a session.
func TestNewBlocks_AnEmptyNodeSetIsNotAnError(t *testing.T) {
	blocks, err := analyzer.NewBlocks(context.Background(), nil, nil)
	require.NoError(t, err)
	require.Empty(t, blocks)
}

// ── NewCandidates ──────────────────────────────────────────────────────────────────────────────

// diagCandidates builds candidates over g and fails the test on anything but the expected error
// state.
func diagCandidates(t *testing.T, nodes []dag.Node, o analyzer.CandidateOptions) []analyzer.Candidate {
	t.Helper()
	got, err := analyzer.NewCandidates(nodes, o)
	require.NoError(t, err)
	return got
}

// diagRep returns the candidate's representation of kind k.
func diagRep(t *testing.T, c analyzer.Candidate, k analyzer.RepresentationKind) analyzer.Representation {
	t.Helper()
	for _, r := range c.Reps {
		if r.Kind == k {
			return r
		}
	}
	t.Fatalf("candidate %s has no %s representation", string(c.Item), k)
	return analyzer.Representation{}
}

// TestNewCandidates_OneCandidatePerItemSortedByItemAndKind pins the two orderings contract §3's
// tie-break depends on, so the selector does not have to re-establish either.
func TestNewCandidates_OneCandidatePerItemSortedByItemAndKind(t *testing.T) {
	g := diagGraph(t)
	nodes := []dag.Node{
		{ID: dag.FileNode("z.go"), Kind: dag.KindFile, Pos: 3, Tokens: 40, Root: core.Hash{0x02}},
		{ID: dag.FileNode("a.go"), Kind: dag.KindFile, Pos: 1, Tokens: 80, Root: core.Hash{0x01}},
		{ID: dag.FileNode("a.go"), Kind: dag.KindFile, Pos: 1, Tokens: 80, Root: core.Hash{0x01}},
	}

	got := diagCandidates(t, nodes, analyzer.CandidateOptions{Graph: g})

	require.Len(t, got, 2, "a node named twice is one candidate")
	require.Equal(t, dag.FileNode("a.go"), got[0].Item)
	require.Equal(t, dag.FileNode("z.go"), got[1].Item)
	for _, c := range got {
		for i := 1; i < len(c.Reps); i++ {
			require.Less(t, c.Reps[i-1].Kind, c.Reps[i].Kind, "representations are ordered by kind ascending")
		}
	}
}

// TestNewCandidates_ArchiveOnlyIsNotACandidateRepresentation is a structural guard on contract §3's
// overflow path. RepArchiveOnly is an OUTCOME, not a candidate: offering it here as a zero-cost
// representation would make "no representation of a mandatory item fits" unreachable and turn every
// overflow into a silent success.
func TestNewCandidates_ArchiveOnlyIsNotACandidateRepresentation(t *testing.T) {
	g := diagGraph(t)
	got := diagCandidates(t, []dag.Node{
		{ID: dag.FileNode("a.go"), Kind: dag.KindFile, Pos: 1, Tokens: 80, Root: core.Hash{0x01}},
	}, analyzer.CandidateOptions{Graph: g, Mandatory: map[dag.NodeID]bool{dag.FileNode("a.go"): true}})

	require.Len(t, got, 1)
	require.True(t, got[0].Mandatory)
	for _, r := range got[0].Reps {
		require.NotEqual(t, analyzer.RepArchiveOnly, r.Kind,
			"an archive-only representation would make overflow unreachable")
	}
	require.Equal(t,
		[]analyzer.RepresentationKind{analyzer.RepExactSpan, analyzer.RepCapsule, analyzer.RepPointer},
		[]analyzer.RepresentationKind{got[0].Reps[0].Kind, got[0].Reps[1].Kind, got[0].Reps[2].Kind})
}

// TestNewCandidates_ANodeWithNoContentHasOnlyAnExactSpan asserts the constraint that keeps a
// selector from spending budget on nothing: a capsule of no content and a handle that resolves to
// no content are not cheaper deliveries of an item, they are empty ones.
func TestNewCandidates_ANodeWithNoContentHasOnlyAnExactSpan(t *testing.T) {
	g := diagGraph(t)
	got := diagCandidates(t, []dag.Node{
		{ID: dag.AssistantNode(1), Kind: dag.KindAssistant, Pos: 1, Tokens: 15},
	}, analyzer.CandidateOptions{Graph: g})

	require.Len(t, got, 1)
	require.Len(t, got[0].Reps, 1)
	require.Equal(t, analyzer.RepExactSpan, got[0].Reps[0].Kind)
}

// TestNewCandidates_AssembledCostIncludesEveryOverhead is contract §2's rule about the field the
// objective actually spends: AssembledCost is the WHOLE cost of delivering a representation, not
// the naked content length. A per-chunk sum that omitted the wrapper is the difference between a
// selector that fits its budget and one that overruns at serialization time.
func TestNewCandidates_AssembledCostIncludesEveryOverhead(t *testing.T) {
	g := diagGraph(t)
	costs := analyzer.DefaultRepresentationCosts()
	const content = core.Tokens(400)

	got := diagCandidates(t, []dag.Node{
		{ID: dag.FileNode("a.go"), Kind: dag.KindFile, Pos: 1, Tokens: content, Root: core.Hash{0x01}},
	}, analyzer.CandidateOptions{Graph: g, Costs: costs})
	c := got[0]

	span := diagRep(t, c, analyzer.RepExactSpan)
	require.Equal(t, content+costs.Wrapper, span.AssembledCost)
	require.Equal(t, 1.0, span.Coverage, "the verbatim bytes cover their item")

	capsule := diagRep(t, c, analyzer.RepCapsule)
	require.Greater(t, capsule.AssembledCost, costs.Wrapper, "a capsule costs its wrapper plus its share")
	require.Less(t, capsule.AssembledCost, span.AssembledCost)
	require.Less(t, capsule.Coverage, span.Coverage,
		"a summary that costs a quarter as much does not carry a quarter of the item's usefulness")

	pointer := diagRep(t, c, analyzer.RepPointer)
	require.Equal(t, costs.Handle+costs.Wrapper, pointer.AssembledCost,
		"§4.4's pointers-never-contents is only cheaper than a span if the handle is priced")
	require.Less(t, pointer.Coverage, capsule.Coverage)
	require.Greater(t, pointer.Coverage, 0.0, "a resolvable handle is worth more than nothing")
}

// TestNewCandidates_ProvenanceKeepsTheOriginalEvidenceRoot is contract §2's chain-of-custody rule.
// A capsule is a derivative and says so; what it must NOT do is overwrite Root with its own bytes,
// because the chain back to the first observation is what makes §4.4's non-reconstructible content
// recoverable at all.
func TestNewCandidates_ProvenanceKeepsTheOriginalEvidenceRoot(t *testing.T) {
	g := diagGraph(t)
	root := core.Hash{0xAB, 0xCD}
	item := dag.ToolResultNode("toolu_evidence")

	got := diagCandidates(t, []dag.Node{
		{ID: item, Kind: dag.KindToolResult, Pos: 5, Turn: 9, Tokens: 200, Root: root},
	}, analyzer.CandidateOptions{Graph: g})
	c := got[0]

	for _, r := range c.Reps {
		require.Equal(t, root, r.Prov.Root, "%s overwrote the ORIGINAL evidence root", r.Kind)
		require.Equal(t, item, r.Prov.Origin)
		require.Equal(t, core.TurnIndex(9), r.Prov.Turn)
	}
	require.False(t, diagRep(t, c, analyzer.RepExactSpan).Prov.Derived)
	require.True(t, diagRep(t, c, analyzer.RepCapsule).Prov.Derived, "a capsule is a summary and says so")
	require.False(t, diagRep(t, c, analyzer.RepPointer).Prov.Derived)
}

// TestNewCandidates_QualificationIsUncertainUntilSomeoneEstablishesIt is the G6.3 guard, and the
// reason contract §1 puts the negknow mapping in the daemon.
//
// This package cannot import negknow (§3.2) and therefore cannot establish that any record is still
// applicable. Defaulting to QualCurrent would manufacture the false "already tried" that inverts
// negative knowledge from asset to liability — §12 rates it High — so an unqualified candidate is
// QualUncertain, whose Active() is false, and it travels and renders while constraining nothing.
func TestNewCandidates_QualificationIsUncertainUntilSomeoneEstablishesIt(t *testing.T) {
	g := diagGraph(t)
	unqualified := dag.EliminationNode("elim_unknown")
	qualified := dag.EliminationNode("elim_checked")

	got := diagCandidates(t, []dag.Node{
		{ID: unqualified, Kind: dag.KindElimination, Pos: 1},
		{ID: qualified, Kind: dag.KindElimination, Pos: 2},
	}, analyzer.CandidateOptions{
		Graph:         g,
		Qualification: map[dag.NodeID]analyzer.Qualification{qualified: analyzer.QualCurrent},
	})

	byItem := map[dag.NodeID]analyzer.Candidate{}
	for _, c := range got {
		byItem[c.Item] = c
	}
	require.Equal(t, analyzer.QualUncertain, byItem[unqualified].Reps[0].Prov.Qualification)
	require.False(t, byItem[unqualified].Reps[0].Prov.Qualification.Active(),
		"an unqualified record may be carried, but it may not constrain anything")
	require.Equal(t, analyzer.QualCurrent, byItem[qualified].Reps[0].Prov.Qualification,
		"the daemon's own mapping is honoured verbatim")
	require.True(t, byItem[qualified].Reps[0].Prov.Qualification.Active())
}

// TestNewCandidates_WeightsDefaultUniformAndClampNonNegative pins both halves of contract §2's
// Weight >= 0. An unnamed item weighs 1 rather than 0 — at 0 a caller with no weighting policy yet
// would silently get a selector that drops everything it did not explicitly ask for — and a
// negative weight is clamped, since "delivering this lowers the objective" is not a preference the
// objective can express.
func TestNewCandidates_WeightsDefaultUniformAndClampNonNegative(t *testing.T) {
	g := diagGraph(t)
	unnamed, negative, weighted := dag.FileNode("a.go"), dag.FileNode("b.go"), dag.FileNode("c.go")

	got := diagCandidates(t, []dag.Node{
		{ID: unnamed, Kind: dag.KindFile, Pos: 1},
		{ID: negative, Kind: dag.KindFile, Pos: 2},
		{ID: weighted, Kind: dag.KindFile, Pos: 3},
	}, analyzer.CandidateOptions{
		Graph:   g,
		Weights: map[dag.NodeID]float64{negative: -3, weighted: 2.5},
	})

	byItem := map[dag.NodeID]float64{}
	for _, c := range got {
		byItem[c.Item] = c.Weight
	}
	require.Equal(t, 1.0, byItem[unnamed])
	require.Equal(t, 0.0, byItem[negative])
	require.Equal(t, 2.5, byItem[weighted])
}

// TestNewCandidates_ANilGraphIsDegradedNotAnEmptyClosure is the second honesty rule of this file.
// Requires is a CLAIM — "delivering this is not enough on its own" — and an empty Requires states
// the opposite claim. With no graph there is nothing to compute either from, so the candidates come
// back fully formed alongside core.ErrDegraded, and a caller that ignores the error gets a selector
// that may propose an item without its dependencies rather than one pretending there were none.
func TestNewCandidates_ANilGraphIsDegradedNotAnEmptyClosure(t *testing.T) {
	got, err := analyzer.NewCandidates([]dag.Node{
		{ID: dag.FileNode("a.go"), Kind: dag.KindFile, Pos: 1, Root: core.Hash{0x01}},
	}, analyzer.CandidateOptions{})

	require.Len(t, got, 1, "the candidates are returned in full")
	require.ErrorIs(t, err, core.ErrDegraded)
	require.Contains(t, err.Error(), "UNESTABLISHED")
	for _, r := range got[0].Reps {
		require.Empty(t, r.Requires)
	}
}

// TestNewCandidates_RequiresIsTheDataDependenceClosure walks a real §8.1 item 4 chain built by
// dag.BuildToolUse and asserts what the closure contains — and, just as importantly, what it does
// not.
//
// A tool result requires its tool use, because produces loses no information across the hop. A tool
// use does NOT require the file anchor it shares state with, because a shared_file edge is an
// association: it says two nodes touched one path, not that either is unintelligible without the
// other.
func TestNewCandidates_RequiresIsTheDataDependenceClosure(t *testing.T) {
	g := diagGraph(t)
	require.NoError(t, dag.BuildToolUse(g, dag.ObservedTool{
		ToolUseID: "toolu_read_changelog", Turn: 1, TS: diagEpoch + 1000,
		Pos: 10, Tool: "Read", PathKey: diagChangelog, Root: core.Hash{0x11}, Tokens: 60,
	}))

	use := dag.ToolUseNode("toolu_read_changelog")
	result := dag.ToolResultNode("toolu_read_changelog")
	got := diagCandidates(t, []dag.Node{
		{ID: use, Kind: dag.KindToolUse, Pos: 10},
		{ID: result, Kind: dag.KindToolResult, Pos: 10, Root: core.Hash{0x11}, Tokens: 60},
	}, analyzer.CandidateOptions{Graph: g})

	byItem := map[dag.NodeID]analyzer.Candidate{}
	for _, c := range got {
		byItem[c.Item] = c
	}
	require.Equal(t, []dag.NodeID{use}, byItem[result].Reps[0].Requires,
		"a tool result is its tool use's output, and produces loses nothing across the hop")
	require.NotContains(t, byItem[use].Reps[0].Requires, dag.FileNode(diagChangelog),
		"a shared_file edge is an association, not a dependency")
}

// TestNewCandidates_RequiresIsTransitivelyClosedAndSorted asserts contract §2's two words about the
// field, over a three-hop evidence chain: a decision explains-linked to a tool result which is
// produced by a tool use. Delivering the decision without either is delivering an unsupported claim.
func TestNewCandidates_RequiresIsTransitivelyClosedAndSorted(t *testing.T) {
	g := diagGraph(t)
	require.NoError(t, dag.BuildToolUse(g, dag.ObservedTool{
		ToolUseID: "toolu_evidence", Turn: 1, TS: diagEpoch + 1000,
		Pos: 10, Tool: "Grep", PathKey: diagChangelog, Root: core.Hash{0x21}, Tokens: 30,
	}))
	require.NoError(t, dag.BuildDecision(g, dag.DecisionSpec{
		ID: "dec_pooling", Turn: 2, TS: diagEpoch + 2000, Pos: 20,
		Summary: "share one connection pool", Tokens: 25,
		Evidence: []dag.NodeID{dag.ToolResultNode("toolu_evidence")},
	}))

	got := diagCandidates(t, []dag.Node{
		{ID: dag.DecisionNode("dec_pooling"), Kind: dag.KindDecision, Pos: 20, Tokens: 25},
	}, analyzer.CandidateOptions{Graph: g})

	requires := got[0].Reps[0].Requires
	require.Equal(t, []dag.NodeID{
		dag.ToolResultNode("toolu_evidence"),
		dag.ToolUseNode("toolu_evidence"),
	}, requires, "the closure is transitive — evidence, and what produced the evidence — and sorted ascending")
	require.NotContains(t, requires, dag.DecisionNode("dec_pooling"),
		"an item never requires itself: that would be unsatisfiable by construction")
}

// ── the relation graph counterexample (fixture shared-file-edge-is-not-relevance) ───────────────

// diagSharedFileGraph records the two halves of the shared-file fixture as tool uses on one path,
// and returns the graph, the store and the two blocks that name their content.
func diagSharedFileGraph(t *testing.T) (dag.Graph, store.Store, analyzer.Block, analyzer.Block) {
	t.Helper()
	g, s := diagGraph(t), diagStore(t)

	notes := diagBlock(t, s, "toolu_release_notes", diagChangelog,
		diagFixture(t, fixtureRelation, "changelog_release_notes.txt"))
	typo := diagBlock(t, s, "toolu_typo_fix", diagChangelog,
		diagFixture(t, fixtureRelation, "changelog_typo_fix.txt"))

	require.NoError(t, dag.BuildToolUse(g, dag.ObservedTool{
		ToolUseID: "toolu_release_notes", Turn: 1, TS: diagEpoch + 1000,
		Pos: 10, Tool: "Read", PathKey: diagChangelog, Root: notes.Root, Tokens: notes.Tokens,
	}))
	require.NoError(t, dag.BuildToolUse(g, dag.ObservedTool{
		ToolUseID: "toolu_typo_fix", PrevToolUseID: "toolu_release_notes", PrevTurn: 1,
		Turn: 2, TS: diagEpoch + 2000, Pos: 20, Tool: "Edit",
		PathKey: diagChangelog, Writes: true, Root: typo.Root, Tokens: typo.Tokens,
	}))
	return g, s, notes, typo
}

// TestRelations_ASharedFileEdgeIsPreservedAndProvesNothing is gate M5-G15-B's third counterexample.
//
// Two tool uses touch docs/CHANGELOG.md: one reads the connection-pooling release notes, the other
// fixes a spelling mistake in a 2019 entry. The DAG links both to the file node with shared_file
// edges. The test asserts all three parts of the qualification at once:
//
//  1. the edges EXIST and are preserved verbatim, kind and direction included — filtering them out
//     because the closure has no use for them would destroy the record;
//  2. they are not dependencies, so neither tool use requires the other or the file anchor;
//  3. and they carry no relevance, checked independently: the cheap Δ scorer, which knows nothing
//     about the graph, scores the typo fix near zero against a continuation drawn from the pooling
//     notes, and well below the notes themselves.
func TestRelations_ASharedFileEdgeIsPreservedAndProvesNothing(t *testing.T) {
	g, s, notes, typo := diagSharedFileGraph(t)
	useNotes, useTypo := dag.ToolUseNode("toolu_release_notes"), dag.ToolUseNode("toolu_typo_fix")
	file := dag.FileNode(diagChangelog)

	rel := analyzer.NewRelations(g, []dag.NodeID{useNotes, useTypo, file})

	// 1. preserved, verbatim.
	var shared []analyzer.Relation
	for _, e := range rel.Edges {
		if e.Kind == dag.EdgeSharedFile {
			shared = append(shared, e)
		}
	}
	require.NotEmpty(t, shared, "the shared_file edges must survive into the preserved record")
	for _, e := range shared {
		require.False(t, e.Dependence, "a shared_file edge is recorded as an association, not a dependency")
	}
	require.Subset(t, []dag.NodeID{useNotes, useTypo, file}, rel.Items)

	// 2. not dependencies.
	got := diagCandidates(t, []dag.Node{
		{ID: useNotes, Kind: dag.KindToolUse, Pos: 10},
		{ID: useTypo, Kind: dag.KindToolUse, Pos: 20},
	}, analyzer.CandidateOptions{Graph: g})
	for _, c := range got {
		require.NotContains(t, c.Reps[0].Requires, file, "%s must not require the file it shares", string(c.Item))
		require.NotContains(t, c.Reps[0].Requires, useNotes+useTypo)
	}
	require.NotContains(t, got[0].Reps[0].Requires, useTypo)
	require.NotContains(t, got[1].Reps[0].Requires, useNotes)

	// 3. no relevance, checked by an independent signal.
	scores := diagScore(t, analyzer.NewCheapScorer(s), []analyzer.Block{notes, typo}, diagPoolingContinuation())
	require.Greater(t, scores[notes.ID], scores[typo.ID],
		"the two contents are joined by an edge and share no subject; the edge is not evidence about either")
}

// TestRelations_AbsenceOfAnEdgeEstablishesNothing is the more dangerous direction of the same
// qualification, and the one plan §2 states outright: content outside a slice is not thereby shown
// to be irrelevant.
//
// Relations.Items is what makes the distinction legible. An item that was asked about and has no
// edges appears in Items with nothing in Edges; an item that was never asked about does not appear
// at all. Without Items the two would render identically, and "we found no connection" would be
// indistinguishable from "we never looked".
func TestRelations_AbsenceOfAnEdgeEstablishesNothing(t *testing.T) {
	g, _, _, _ := diagSharedFileGraph(t)
	isolated := dag.FileNode("docs/never-observed.md")
	unasked := dag.FileNode("docs/not-in-the-query.md")

	rel := analyzer.NewRelations(g, []dag.NodeID{isolated})

	require.Contains(t, rel.Items, isolated, "it was asked about")
	require.NotContains(t, rel.Items, unasked, "it was not asked about, which is a different fact")
	for _, e := range rel.Edges {
		require.NotEqual(t, isolated, e.Item)
	}

	// And the whole point: having no edge is not having been ruled out. The node is still a
	// perfectly good candidate, priced and carried like any other.
	got, err := analyzer.NewCandidates([]dag.Node{{ID: isolated, Kind: dag.KindFile, Pos: 1, Tokens: 10}},
		analyzer.CandidateOptions{Graph: g})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, isolated, got[0].Item)
	require.Empty(t, got[0].Reps[0].Requires)
}

// TestRelations_ANilGraphStillReportsWhatWasAsked pins the same distinction at the degenerate end:
// with no graph there are no edges, but the item set is still reported, so "nothing could be read"
// does not render as "nothing is connected".
func TestRelations_ANilGraphStillReportsWhatWasAsked(t *testing.T) {
	items := []dag.NodeID{dag.FileNode("b.go"), dag.FileNode("a.go"), dag.FileNode("a.go"), ""}

	rel := analyzer.NewRelations(nil, items)

	require.Equal(t, []dag.NodeID{dag.FileNode("a.go"), dag.FileNode("b.go")}, rel.Items,
		"deduplicated and sorted; the empty id is not an item")
	require.Empty(t, rel.Edges)
}
