package analyzer_test

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/analyzer/analyzertest"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// diagBlock stores content and returns the Block that names it, with a real content root so the
// scorer has something to read back. The node is a tool RESULT, which is the kind that actually
// carries content in the §8.1 item 4 chain.
func diagBlock(t *testing.T, s store.Store, id core.ToolUseID, path string, content []byte) analyzer.Block {
	t.Helper()
	res, err := s.PutBytes(context.Background(), content, store.PutOptions{Tool: "Read", Path: path})
	require.NoError(t, err)
	return analyzer.Block{
		ID:     dag.ToolResultNode(id),
		Pos:    1,
		Tokens: res.Root.Tokens,
		Kind:   dag.KindToolResult,
		Root:   res.Root.Hash,
	}
}

// diagScore runs the cheap scorer over blocks and fails the test if it reports anything.
func diagScore(t *testing.T, sc analyzer.DeltaScorer, blocks []analyzer.Block,
	c analyzer.Continuation,
) map[dag.NodeID]float64 {
	t.Helper()
	got, err := sc.Score(context.Background(), blocks, c)
	require.NoError(t, err)
	return got
}

// TestAnalyzerConformance_DeltaScorerAgainstARealStore runs analyzertest's RunDeltaScorerSuite
// against the landed scorer, bound to a store that can actually serve content.
//
// analyzertest/suite_test.go runs the same suite against a nil store; between the two, both halves
// of the constructor's contract are exercised. The /behaviour block is guarded by the Rule W-1
// probe and runs only because Score no longer reports core.ErrNotImplemented — no edit to the
// suite is permitted to make it pass (contract §9).
func TestAnalyzerConformance_DeltaScorerAgainstARealStore(t *testing.T) {
	analyzertest.RunDeltaScorerSuite(t, "analyzer.NewCheapScorer", func(t *testing.T) analyzer.DeltaScorer {
		t.Helper()
		return analyzer.NewCheapScorer(diagStore(t))
	})
}

// TestCheapScorer_ModeIsTheTierItsConstructorNames pins what /qompack:status reports. A scorer that
// misdescribed its own tier would make every cost comparison in the eval harness meaningless.
func TestCheapScorer_ModeIsTheTierItsConstructorNames(t *testing.T) {
	require.Equal(t, analyzer.DeltaCheap, analyzer.NewCheapScorer(nil).Mode())
	require.Equal(t, analyzer.DeltaCheap, analyzer.NewCheapScorer(diagStore(t)).Mode())
}

// TestCheapScorer_EveryBlockIsScoredInTheUnitInterval is §5.12's own wording, asserted over blocks
// whose content really is readable. A missing entry is indistinguishable at a selector's call site
// from a zero, and the two mean very different things.
func TestCheapScorer_EveryBlockIsScoredInTheUnitInterval(t *testing.T) {
	s := diagStore(t)
	blocks := []analyzer.Block{
		diagBlock(t, s, "toolu_pool", "docs/pooling.md", diagFixture(t, fixtureRelation, "changelog_release_notes.txt")),
		diagBlock(t, s, "toolu_typo", "docs/typo.md", diagFixture(t, fixtureRelation, "changelog_typo_fix.txt")),
		diagBlock(t, s, "toolu_tls", "internal/httpclient/hardened.go", diagFixture(t, fixtureNearDup, "tlsclient_hardened.txt")),
		{ID: dag.FileNode("docs/pooling.md"), Pos: 2, Kind: dag.KindFile},
	}

	scores := diagScore(t, analyzer.NewCheapScorer(s), blocks, diagPoolingContinuation())

	require.Len(t, scores, len(blocks), "Score returns a proxy for EACH block")
	for _, b := range blocks {
		v, ok := scores[b.ID]
		require.True(t, ok, "block %s was not scored", string(b.ID))
		require.GreaterOrEqual(t, v, 0.0)
		require.LessOrEqual(t, v, 1.0)
	}
}

// diagPoolingContinuation is an observed continuation drawn from the connection-pooling half of the
// shared-file fixture: the paths it touched, the symbols it named, and what it said.
func diagPoolingContinuation() analyzer.Continuation {
	return analyzer.Continuation{
		Text: []byte("the outbound client now shares one connection pool across every integration, " +
			"which is what bounds the file-descriptor count under burst load"),
		Symbols:  []string{"MaxIdleConns", "IdleConnTimeout"},
		Paths:    []string{"docs/pooling.md", "internal/httpclient/hardened.go"},
		FromTurn: core.TurnIndex(7),
	}
}

// TestCheapScorer_IsDeterministic asserts two scorings of the same inputs agree exactly. Δ-scores
// feed a replay metric, so a nondeterministic proxy would make a selection figure incomparable
// between commits and attribute the noise to whatever landed in between.
func TestCheapScorer_IsDeterministic(t *testing.T) {
	s := diagStore(t)
	blocks := []analyzer.Block{
		diagBlock(t, s, "toolu_pool", "docs/pooling.md", diagFixture(t, fixtureRelation, "changelog_release_notes.txt")),
		diagBlock(t, s, "toolu_tls", "internal/httpclient/hardened.go", diagFixture(t, fixtureNearDup, "tlsclient_hardened.txt")),
	}
	sc := analyzer.NewCheapScorer(s)

	first := diagScore(t, sc, blocks, diagPoolingContinuation())
	for i := 0; i < 4; i++ {
		require.Equal(t, first, diagScore(t, sc, blocks, diagPoolingContinuation()),
			"Score must be deterministic across repeated calls")
	}
	require.Equal(t, first, diagScore(t, analyzer.NewCheapScorer(s), blocks, diagPoolingContinuation()),
		"and across scorer instances over the same store")
}

// TestCheapScorer_AnEmptyBlockSetScoresNothing asserts pricing a prefix with no candidates left is
// the normal end of a session rather than a fault.
func TestCheapScorer_AnEmptyBlockSetScoresNothing(t *testing.T) {
	sc := analyzer.NewCheapScorer(diagStore(t))

	require.Empty(t, diagScore(t, sc, nil, diagPoolingContinuation()))
	require.Empty(t, diagScore(t, sc, []analyzer.Block{}, diagPoolingContinuation()))
}

// TestCheapScorer_ACoveringContinuationOutscoresAnEmptyOne is the direction the whole proxy exists
// to express, asserted STRICTLY here rather than as the suite's "not lower": with real content
// behind the blocks, a continuation that names their paths and symbols must actually find them.
func TestCheapScorer_ACoveringContinuationOutscoresAnEmptyOne(t *testing.T) {
	s := diagStore(t)
	blocks := []analyzer.Block{
		diagBlock(t, s, "toolu_pool", "docs/pooling.md", diagFixture(t, fixtureRelation, "changelog_release_notes.txt")),
		diagBlock(t, s, "toolu_tls", "internal/httpclient/hardened.go", diagFixture(t, fixtureNearDup, "tlsclient_hardened.txt")),
	}
	sc := analyzer.NewCheapScorer(s)

	covering := diagScore(t, sc, blocks, diagPoolingContinuation())
	empty := diagScore(t, sc, blocks, analyzer.Continuation{FromTurn: core.TurnIndex(7)})

	var coveringSum, emptySum float64
	for _, b := range blocks {
		coveringSum += covering[b.ID]
		emptySum += empty[b.ID]
		require.Zero(t, empty[b.ID], "a continuation that observed nothing finds nothing to credit")
	}
	require.Greater(t, coveringSum, emptySum)
}

// TestCheapScorer_ANilStoreStillScoresEveryBlock pins the no-content composition root that
// test/guards constructs on purpose: no store means no content, which is a weaker measurement
// rather than a refusal — and the DAG key is still evidence, so a file anchor is not silently
// zeroed just because nothing could be read.
func TestCheapScorer_ANilStoreStillScoresEveryBlock(t *testing.T) {
	blocks := []analyzer.Block{
		{ID: dag.FileNode("docs/pooling.md"), Pos: 1, Kind: dag.KindFile},
		{ID: dag.FileNode("docs/unrelated.md"), Pos: 2, Kind: dag.KindFile},
		{ID: dag.ToolResultNode("toolu_no_content"), Pos: 3, Kind: dag.KindToolResult},
	}

	scores := diagScore(t, analyzer.NewCheapScorer(nil), blocks, diagPoolingContinuation())

	require.Len(t, scores, len(blocks))
	require.Greater(t, scores[dag.FileNode("docs/pooling.md")], 0.0,
		"the continuation touched this path, and the block's own DAG key says so without any store")
	require.Zero(t, scores[dag.FileNode("docs/unrelated.md")])
	require.Zero(t, scores[dag.ToolResultNode("toolu_no_content")])
}

// TestCheapScorer_AnUnreadableRootDoesNotDropTheBlock covers the root a GC pass tombstoned or a
// quarantine moved out from under the index. The block still exists and still has to be priced; the
// alternative — omitting it — hands a selector a candidate set that silently lost a member.
func TestCheapScorer_AnUnreadableRootDoesNotDropTheBlock(t *testing.T) {
	s := diagStore(t)
	missing := analyzer.Block{
		ID:     dag.ToolResultNode("toolu_vanished"),
		Pos:    1,
		Kind:   dag.KindToolResult,
		Tokens: core.Tokens(40),
		Root:   core.Hash{0x01, 0x02, 0x03},
	}

	scores := diagScore(t, analyzer.NewCheapScorer(s), []analyzer.Block{missing}, diagPoolingContinuation())

	require.Len(t, scores, 1)
	v, ok := scores[missing.ID]
	require.True(t, ok, "a block whose content cannot be read is scored on what remains, not dropped")
	require.GreaterOrEqual(t, v, 0.0)
	require.LessOrEqual(t, v, 1.0)
}

// TestCheapScorer_ALowScoreIsNotACorrectnessLabel is the qualification this tier's doc comment
// makes, turned into an assertion (gate M5-G15-B).
//
// Two blocks. The DECOY is a chat echo that repeats the continuation's sentence almost verbatim and
// contributed nothing to it. The DEPENDENCY is the source file whose one constant decided the
// outcome the continuation is reporting, and it shares almost none of the continuation's wording.
// Lexical overlap ranks them the wrong way round, and the test asserts that it does.
//
// That is not a bug to be fixed by tuning the weights: it is what a behaviour PROXY is. The number
// says "this block's vocabulary resembles what happened next"; it does not say "this block was
// necessary", and a consumer that reads a low score as evidence of irrelevance has read a claim the
// scorer never made. Deleting this test is the cheapest way to lose that distinction, which is
// exactly why it is committed.
func TestCheapScorer_ALowScoreIsNotACorrectnessLabel(t *testing.T) {
	s := diagStore(t)
	const echoed = "the deployment ran and the operator confirmed that the handshake completed without incident"

	decoy := diagBlock(t, s, "toolu_chat_echo", "logs/session.txt", []byte(echoed))
	dependency := diagBlock(t, s, "toolu_tls_hardened", "internal/httpclient/hardened.go",
		diagFixture(t, fixtureNearDup, "tlsclient_hardened.txt"))

	scores := diagScore(t, analyzer.NewCheapScorer(s),
		[]analyzer.Block{decoy, dependency},
		analyzer.Continuation{Text: []byte(echoed), FromTurn: core.TurnIndex(3)})

	require.Greater(t, scores[decoy.ID], scores[dependency.ID],
		"a verbatim echo outscores the file that actually decided the outcome: the proxy measures "+
			"vocabulary, not causation, and this ordering is the demonstration that it does")
	require.Less(t, scores[dependency.ID], scores[decoy.ID],
		"and the low score on the dependency is absence of THIS evidence, not evidence of irrelevance")
}

// TestCheapScorer_SurvivesZeroValuedArguments covers the calling shape test/guards' reflective walk
// produces once Main marks this seam landed: every method invoked with zero-valued arguments.
//
// That walk substitutes a real context for a context parameter, so it does not itself pass nil —
// this case goes one step further on purpose. store.OpenSpan dereferences the context it is handed,
// and a seam that panics costs the user their turn (§12.3), which is the outcome the whole tier's
// degrade-rather-than-refuse rule exists to avoid.
func TestCheapScorer_SurvivesZeroValuedArguments(t *testing.T) {
	sc := analyzer.NewCheapScorer(nil)

	require.NotPanics(t, func() {
		//nolint:staticcheck // SA1012: passing a nil context is precisely what the guard walk does.
		got, err := sc.Score(nil, nil, analyzer.Continuation{})
		require.NoError(t, err)
		require.Empty(t, got)
	})

	s := diagStore(t)
	block := diagBlock(t, s, "toolu_zero_ctx", "docs/pooling.md",
		diagFixture(t, fixtureRelation, "changelog_release_notes.txt"))
	require.NotPanics(t, func() {
		//nolint:staticcheck // SA1012: see above.
		got, err := analyzer.NewCheapScorer(s).Score(nil, []analyzer.Block{block}, diagPoolingContinuation())
		require.NoError(t, err)
		require.Len(t, got, 1)
	})
}
