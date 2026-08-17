package eval_test

import (
	"encoding/json"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

// blocksOfKind returns every block of one kind, preserving order.
func blocksOfKind(bs []eval.Block, k eval.BlockKind) []eval.Block {
	var out []eval.Block
	for _, b := range bs {
		if b.Kind == k {
			out = append(out, b)
		}
	}
	return out
}

// TestBlocks_PositionsAreCumulative pins the §2 rule that Pos is a running sum over
// position-advancing blocks, and that a turn's own message block carries the turn's tokens.
func TestBlocks_PositionsAreCumulative(t *testing.T) {
	s := eval.Session{
		ID: "pos",
		Turns: []eval.Turn{
			{Index: 0, Role: "user", Tokens: 100},
			{Index: 1, Role: "assistant", Tokens: 200},
			{Index: 2, Role: "assistant", Tokens: 300},
		},
	}

	got := eval.Blocks(s, 3)

	require.Len(t, got, 3)
	require.Equal(t, []int{0, 100, 300}, []int{got[0].Pos, got[1].Pos, got[2].Pos})
	require.Equal(t, []core.Tokens{100, 200, 300},
		[]core.Tokens{got[0].Tokens, got[1].Tokens, got[2].Tokens})
	require.Equal(t, eval.BlockUserPrompt, got[0].Kind)
	require.Equal(t, eval.BlockAssistant, got[1].Kind)
	require.Equal(t, []string{"turn:0", "turn:1", "turn:2"},
		[]string{got[0].ID, got[1].ID, got[2].ID})
}

// TestBlocks_FileBlockPerDistinctKey asserts a file block is minted once per distinct paths.Key,
// which case-folds only on Windows and macOS (paths.DefaultFold). Asserting one block
// unconditionally would pass locally and fail on the Linux job that produces the baseline.
func TestBlocks_FileBlockPerDistinctKey(t *testing.T) {
	s := eval.Session{
		ID: "keys",
		Turns: []eval.Turn{
			{Index: 0, Role: "assistant", Tokens: 10, ToolCalls: []eval.ToolCall{
				{ID: "t0", Name: "FileRead", Paths: []string{"src/A.ts"}},
			}},
			{Index: 1, Role: "assistant", Tokens: 10, ToolCalls: []eval.ToolCall{
				{ID: "t1", Name: "Read", Paths: []string{"SRC/a.ts"}},
			}},
		},
	}

	files := blocksOfKind(eval.Blocks(s, 2), eval.BlockFile)

	if paths.DefaultFold() {
		require.Len(t, files, 1, "SRC/a.ts folds onto src/A.ts on a case-insensitive platform")
		require.Equal(t, "file:src/a.ts", files[0].ID)
		return
	}
	require.Len(t, files, 2, "the two spellings are distinct keys where Key does not fold")
	require.Equal(t, []string{"file:src/A.ts", "file:SRC/a.ts"}, []string{files[0].ID, files[1].ID})
}

// TestBlocks_DecisionMarkerExtracted pins the [decision:<id>] grammar and the fixed 128-token
// weight a decision block carries.
func TestBlocks_DecisionMarkerExtracted(t *testing.T) {
	s := eval.Session{
		ID: "dec",
		Turns: []eval.Turn{
			{Index: 0, Role: "assistant", Tokens: 40, Text: "done [decision:dec_abc123def456]"},
		},
	}

	decs := blocksOfKind(eval.Blocks(s, 1), eval.BlockDecision)

	require.Len(t, decs, 1)
	require.Equal(t, "dec:dec_abc123def456", decs[0].ID)
	require.Equal(t, core.Tokens(128), decs[0].Tokens)
	require.Equal(t, 0, decs[0].Pos, "a derived block takes the position of the block it came from")
}

// TestBlocks_ToolResultTokensFromPayload asserts the two ways a tool result is weighed: the
// explicit "tokens" field when the payload carries one, and len/4 rounded up otherwise.
func TestBlocks_ToolResultTokensFromPayload(t *testing.T) {
	s := eval.Session{
		ID: "weights",
		Turns: []eval.Turn{
			{Index: 0, Role: "assistant", Tokens: 10, ToolCalls: []eval.ToolCall{
				{ID: "a", Name: "Bash", Result: json.RawMessage(`{"tokens":900}`)},
				{ID: "b", Name: "Bash", Result: json.RawMessage(`12345`)},
			}},
		},
	}

	trs := blocksOfKind(eval.Blocks(s, 1), eval.BlockToolResult)

	require.Len(t, trs, 2)
	require.Equal(t, core.Tokens(900), trs[0].Tokens)
	require.Equal(t, core.Tokens(2), trs[1].Tokens, "len(`12345`) = 5, ceil(5/4) = 2")
	require.Equal(t, []string{"tu:a", "tu:b"}, []string{trs[0].ID, trs[1].ID})
}

// TestDemands_OnlyPreCompactionBlocks is the core of the demand model: only content that existed
// before the compaction can be demanded after it.
func TestDemands_OnlyPreCompactionBlocks(t *testing.T) {
	s := eval.Session{
		ID: "demand",
		Turns: []eval.Turn{
			{Index: 0, Role: "user", Tokens: 10},
			{Index: 1, Role: "assistant", Tokens: 10},
			{Index: 2, Role: "assistant", Tokens: 10, ToolCalls: []eval.ToolCall{
				{ID: "r0", Name: "FileRead", Paths: []string{"src/a.go"}},
			}},
			{Index: 3, Role: "assistant", Tokens: 10},
			{Index: 4, Role: "assistant", Tokens: 10},
			{Index: 5, Role: "assistant", Tokens: 10},
			{Index: 6, Role: "assistant", Tokens: 10, ToolCalls: []eval.ToolCall{
				{ID: "r1", Name: "FileRead", Paths: []string{"src/never-before-seen.go"}},
			}},
			{Index: 7, Role: "assistant", Tokens: 10},
			{Index: 8, Role: "assistant", Tokens: 10},
			{Index: 9, Role: "assistant", Tokens: 10, ToolCalls: []eval.ToolCall{
				{ID: "r2", Name: "FileRead", Paths: []string{"src/a.go"}},
			}},
		},
	}

	got := eval.Demands(s, 5, 25)

	require.Equal(t, []eval.Demand{
		{Turn: 9, BlockID: "file:" + paths.Key("src/a.go"), Kind: eval.DemandFileContent},
	}, got, "src/never-before-seen.go was first read after the compaction, so it is not a demand")
}

// TestDemands_DeduplicatesWithinTurn asserts one (Turn, BlockID) pair yields exactly one demand,
// so a turn that touches the same file twice cannot inflate a policy's score.
func TestDemands_DeduplicatesWithinTurn(t *testing.T) {
	s := eval.Session{
		ID: "dedup",
		Turns: []eval.Turn{
			{Index: 0, Role: "assistant", Tokens: 10, ToolCalls: []eval.ToolCall{
				{ID: "r0", Name: "FileRead", Paths: []string{"src/a.go"}},
			}},
			{Index: 1, Role: "assistant", Tokens: 10},
			{Index: 2, Role: "assistant", Tokens: 10, ToolCalls: []eval.ToolCall{
				{ID: "r1", Name: "Read", Paths: []string{"src/a.go"}},
				{ID: "r2", Name: "Edit", Paths: []string{"src/a.go"}},
			}},
		},
	}

	require.Len(t, eval.Demands(s, 1, 10), 1)
}

// TestDemands_EliminationMatchByApproachClass asserts a re-attempt is detected through the
// normalized approach class rather than through string equality, which is what makes the
// elimination demand survive the agent rephrasing itself.
func TestDemands_EliminationMatchByApproachClass(t *testing.T) {
	elim, err := json.Marshal(map[string]string{
		"target":   "src/auth.ts",
		"approach": "Widen  Pool Timeout",
		"reason":   "fails under the pinned dependency constraint",
	})
	require.NoError(t, err)
	retry, err := json.Marshal(map[string]string{"approach": "widen-pool-timeout"})
	require.NoError(t, err)

	turns := make([]eval.Turn, 13)
	for i := range turns {
		turns[i] = eval.Turn{Index: core.TurnIndex(i), Role: "assistant", Tokens: 10}
	}
	turns[3].ToolCalls = []eval.ToolCall{{ID: "e0", Name: "record_eliminated", Args: elim}}
	turns[12].ToolCalls = []eval.ToolCall{
		{ID: "x0", Name: "Edit", Paths: []string{"src/auth.ts"}, Args: retry},
	}

	got := eval.Demands(eval.Session{ID: "elim", Turns: turns}, 5, 25)

	require.Len(t, got, 1)
	require.Equal(t, eval.DemandElimination, got[0].Kind)
	require.Equal(t, core.TurnIndex(12), got[0].Turn)
}

// TestDemands_ToolResultAndDecisionReferences covers the two text-referenced demand kinds.
func TestDemands_ToolResultAndDecisionReferences(t *testing.T) {
	turns := make([]eval.Turn, 12)
	for i := range turns {
		turns[i] = eval.Turn{Index: core.TurnIndex(i), Role: "assistant", Tokens: 10}
	}
	turns[2].ToolCalls = []eval.ToolCall{{ID: "toolu_01AB", Name: "Task"}}
	turns[3].Text = "planning [decision:dec_ff00aa]"
	turns[9].Text = "as toolu_01AB reported, and per [decision:dec_ff00aa] we proceed"

	got := eval.Demands(eval.Session{ID: "refs", Turns: turns}, 5, 25)

	require.Equal(t, []eval.Demand{
		{Turn: 9, BlockID: "dec:dec_ff00aa", Kind: eval.DemandDecision},
		{Turn: 9, BlockID: "tu:toolu_01AB", Kind: eval.DemandToolResult},
	}, got, "demands are ordered by (Turn, BlockID)")
}

// The approachClass property and known-form tests live in blocks_internal_test.go: the function
// is unexported, and adding an exported test seam to production code to reach it would put
// test-only surface on the package.
