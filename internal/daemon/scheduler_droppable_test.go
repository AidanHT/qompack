package daemon

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// TestClassifyDrop_AdaptsStoreRecord proves the adapter maps all three record fields — Tool,
// Ephemeral, Status — onto scheduler.DropClassOf, and that the §8.7 / §8.1 ordering survives the
// adaptation: ephemeral beats everything, supersession beats the tool class.
func TestClassifyDrop_AdaptsStoreRecord(t *testing.T) {
	t.Parallel()

	require.Equal(t, DropOrdinary, ClassifyDrop(store.ToolUseRecord{Tool: "Bash"}))
	require.Equal(t, DropEphemeral, ClassifyDrop(store.ToolUseRecord{Ephemeral: true}))
	require.Equal(t, DropSuperseded, ClassifyDrop(store.ToolUseRecord{Status: store.StatusSuperseded}))

	// The ordering note: a superseded MCP or Task result is still droppable, and an ephemeral
	// superseded record ranks as ephemeral.
	require.Equal(t, DropNone, ClassifyDrop(store.ToolUseRecord{Tool: "mcp__qompack__recall"}))
	require.Equal(t, DropSuperseded, ClassifyDrop(store.ToolUseRecord{Tool: "mcp__qompack__recall", Status: store.StatusSuperseded}))
	require.Equal(t, DropNone, ClassifyDrop(store.ToolUseRecord{Tool: "Task"}))
	require.Equal(t, DropEphemeral, ClassifyDrop(store.ToolUseRecord{Tool: "Task", Ephemeral: true, Status: store.StatusSuperseded}))
	require.Greater(t, DropEphemeral.EvictionRank(), DropSuperseded.EvictionRank())
	require.Greater(t, DropSuperseded.EvictionRank(), DropOrdinary.EvictionRank())
	require.Greater(t, DropOrdinary.EvictionRank(), DropNone.EvictionRank())
}

// TestReclaimableIndex_SuffixSums pins the suffix arithmetic on three blocks: After(p) is the
// token sum of every droppable block at position >= p.
func TestReclaimableIndex_SuffixSums(t *testing.T) {
	t.Parallel()

	idx := newReclaimableIndex([]dropBlock{
		{Pos: 30, Tokens: 11, Class: DropEphemeral},
		{Pos: 10, Tokens: 5, Class: DropOrdinary},
		{Pos: 20, Tokens: 7, Class: DropSuperseded},
	})
	require.Equal(t, core.Tokens(23), idx.After(0))
	require.Equal(t, core.Tokens(23), idx.After(10))
	require.Equal(t, core.Tokens(18), idx.After(11))
	require.Equal(t, core.Tokens(11), idx.After(30))
	require.Equal(t, core.Tokens(0), idx.After(31))
	require.Equal(t, core.Tokens(23), idx.total)
	require.Equal(t, [4]int{0, 1, 1, 1}, idx.counts)
}

// TestReclaimableIndex_ExcludesDropNone: a DropNone block is counted for /qompack:status but
// contributes no tokens to any After.
func TestReclaimableIndex_ExcludesDropNone(t *testing.T) {
	t.Parallel()

	idx := newReclaimableIndex([]dropBlock{{Pos: 5, Tokens: 100, Class: DropNone}})
	require.Equal(t, core.Tokens(0), idx.After(0))
	require.Equal(t, core.Tokens(0), idx.After(5))
	require.Equal(t, core.Tokens(0), idx.After(6))
	require.Equal(t, core.Tokens(0), idx.total)
	require.Equal(t, 1, idx.counts[DropNone])
	require.Empty(t, idx.pos)
}

// bruteReclaimable is the O(N) definition the index must agree with.
func bruteReclaimable(blocks []dropBlock, p int) core.Tokens {
	var sum core.Tokens
	for _, b := range blocks {
		if b.Class == DropNone || b.Tokens <= 0 || b.Pos < p {
			continue
		}
		sum += b.Tokens
	}
	return sum
}

// TestReclaimableIndex_MonotoneNonIncreasing_Property is Qompack.md §5.4's monotonicity, proven
// rather than assumed: After(p1) >= After(p2) whenever p1 <= p2, on random block sets that
// include duplicate positions, zero and negative token counts, and every DropClass. Every
// answer is also checked against the brute-force definition.
func TestReclaimableIndex_MonotoneNonIncreasing_Property(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 200).Draw(rt, "n")
		blocks := make([]dropBlock, n)
		for i := range blocks {
			blocks[i] = dropBlock{
				Pos:    rapid.IntRange(0, 100_000).Draw(rt, "pos"),
				Tokens: core.Tokens(rapid.IntRange(-50, 5_000).Draw(rt, "tokens")),
				Class:  DropClass(rapid.IntRange(0, 3).Draw(rt, "class")),
			}
		}
		idx := newReclaimableIndex(blocks)

		queries := rapid.SliceOfN(rapid.IntRange(-10, 100_010), 1, 50).Draw(rt, "queries")
		sort.Ints(queries)
		for i, p := range queries {
			require.Equal(rt, bruteReclaimable(blocks, p), idx.After(p), "After(%d)", p)
			if i > 0 {
				require.GreaterOrEqual(rt, idx.After(queries[i-1]), idx.After(p),
					"After(%d) < After(%d)", queries[i-1], p)
			}
		}
	})
}

// TestReclaimableIndex_Empty: no blocks means nothing is reclaimable anywhere, and nothing
// panics on the empty position slice.
func TestReclaimableIndex_Empty(t *testing.T) {
	t.Parallel()

	for _, blocks := range [][]dropBlock{nil, {}} {
		idx := newReclaimableIndex(blocks)
		require.Equal(t, core.Tokens(0), idx.After(0))
		require.Equal(t, core.Tokens(0), idx.After(-1))
		require.Equal(t, core.Tokens(0), idx.After(1<<30))
		require.Equal(t, core.Tokens(0), idx.total)
		require.Equal(t, [4]int{}, idx.counts)
	}
}
