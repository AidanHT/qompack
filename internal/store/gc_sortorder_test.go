package store

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// gcSortOrderRoots is how many roots the allocation row sorts: the overshoot fixture's
// gcOvershootSeeds, every one of them dead, which is the shape that exposed the cost.
const gcSortOrderRoots = gcOvershootSeeds

// randomHashes returns n distinct-looking hashes from a fixed seed, with every fourth one sharing
// a long prefix with its predecessor so the comparison has to look past the first bytes.
func randomHashes(n int, seed uint64) []core.Hash {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	out := make([]core.Hash, n)
	for i := range out {
		for j := range out[i] {
			out[i][j] = byte(rng.UintN(256))
		}
		if i > 0 && i%4 == 0 {
			keep := 1 + rng.IntN(len(out[i])-1)
			copy(out[i][:keep], out[i-1][:keep])
		}
	}
	return out
}

// TestGCHashOrder_IsTheTextOrder pins what the GC sorts may use instead of comparing String()
// forms: the same order. The outcome list, the quota's tie-break and the tombstone order are all
// visible in reports and on disk, so the order must not change, only its cost.
func TestGCHashOrder_IsTheTextOrder(t *testing.T) {
	hs := randomHashes(4096, 1)
	for i := range hs {
		for _, j := range []int{i, (i + 1) % len(hs), (i * 7) % len(hs)} {
			a, b := hs[i], hs[j]
			require.Equal(t, a.String() < b.String(), gcHashLess(a, b),
				"gcHashLess(%s, %s) must agree with comparing their text forms", a, b)
		}
	}
	var zero, one core.Hash
	one[len(one)-1] = 1
	require.True(t, gcHashLess(zero, one))
	require.False(t, gcHashLess(one, zero))
	require.False(t, gcHashLess(one, one), "a hash is never less than itself")
}

// TestRecordOutcomes_SortingAllocatesNothingPerComparison pins the cost that mispriced the overshoot
// window on a -race Linux host: recordOutcomes runs before GC's sweep, where the deadline is never
// consulted, and its sorts built two String() forms per comparison. Over 3 072 dead roots that is
// tens of thousands of allocations, 171…292 ms under -race and 10…18 ms without it (w3-paths
// runs/linux). A sort of n items makes at least n-1 comparisons, so a comparator that allocates
// even once each cannot come in under that count; the outcome rows themselves are appended into
// one growing slice.
func TestRecordOutcomes_SortingAllocatesNothingPerComparison(t *testing.T) {
	s := &FSStore{}
	dead := randomHashes(gcSortOrderRoots, 2)
	allocs := testing.AllocsPerRun(3, func() {
		m := markResult{deadRoots: dead}
		var rep GCReport
		s.recordOutcomes(&m, &rep, newOutcomeLog(GCPolicy{}, &rep))
	})
	t.Logf("recordOutcomes over %d dead roots: %.0f allocations", len(dead), allocs)
	require.Less(t, allocs, float64(len(dead)-1),
		"recordOutcomes allocated %.0f times for %d roots: its sort comparator allocates", allocs, len(dead))
}
